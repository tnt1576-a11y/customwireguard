#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding()]
param([Parameter(Mandatory)][string]$StatePath,[switch]$KeepWatchdog,[switch]$FromRunner)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$state=Get-Content -LiteralPath $StatePath -Raw | ConvertFrom-Json
if($state.Version -ne 1 -or $state.Computer -ne $env:COMPUTERNAME -or $state.CleanupTask -notmatch '^CWG-Lab-Cleanup-[a-f0-9]{32}$'){throw 'Unrecognized remote test state; no cleanup performed.'}
$runDirectory=Split-Path (Resolve-Path -LiteralPath $StatePath).Path -Parent
$lock=[IO.File]::Open((Join-Path $runDirectory 'cleanup.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
try {
    $problems=@()
    # A timed cleanup first stops the owning runner so it cannot create another
    # tunnel after cleanup. Normal finally cleanup must not stop its own task.
    if(-not $KeepWatchdog -and -not $FromRunner){
        try{
            if($state.RunnerTask -notmatch '^CWG-Lab-Run-[a-f0-9]{32}$'){throw 'Invalid owning runner task.'}
            Get-ScheduledTask -TaskName $state.RunnerTask -ErrorAction SilentlyContinue | Stop-ScheduledTask -ErrorAction Stop
        }catch{$problems+=$_.Exception.Message}
    }
    try{& (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $state.Bundle}catch{$problems+=$_.Exception.Message}
    try{
        $adapter=@(Get-NetAdapter | Where-Object {$_.InterfaceGuid.ToString() -eq $state.AdapterGuid})
        if($adapter.Count -ne 1 -or -not $adapter[0].HardwareInterface){throw 'Original physical adapter not found; refusing to change another adapter.'}
        $binding=Get-NetAdapterBinding -Name $adapter[0].Name -ComponentID ms_tcpip6
        if($state.IPv6OriginallyEnabled -and -not $binding.Enabled){Enable-NetAdapterBinding -Name $adapter[0].Name -ComponentID ms_tcpip6 | Out-Null}
        if((Get-NetAdapterBinding -Name $adapter[0].Name -ComponentID ms_tcpip6).Enabled -ne $state.IPv6OriginallyEnabled){throw 'IPv6 binding restoration did not match baseline.'}
    }catch{$problems+=$_.Exception.Message}
    if(Test-Path -LiteralPath (Join-Path $state.Bundle 'client-state.json')){$problems+='CWG state remains after cleanup.'}
    $result=[ordered]@{UTC=[DateTime]::UtcNow.ToString('o');Computer=$env:COMPUTERNAME;Identity=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value;Success=($problems.Count -eq 0);Problems=$problems}
    $result | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $runDirectory 'cleanup-result.json') -Encoding UTF8
    if($problems.Count){throw ($problems -join '; ')}
    if(-not $KeepWatchdog){Unregister-ScheduledTask -TaskName $state.CleanupTask -Confirm:$false -ErrorAction SilentlyContinue}
    Write-Output 'CWG remote-test cleanup verified.'
}finally{$lock.Dispose()}
