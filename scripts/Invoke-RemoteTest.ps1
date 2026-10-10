#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding()]
param([Parameter(Mandatory)][string]$StatePath)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$state=Get-Content -LiteralPath $StatePath -Raw | ConvertFrom-Json
if($state.Version -ne 1 -or $state.Computer -ne $env:COMPUTERNAME -or $state.Mode -notin 'Smoke','Performance'){throw 'Invalid remote test state.'}
$runDirectory=Split-Path (Resolve-Path -LiteralPath $StatePath).Path -Parent
$guard=[IO.File]::Open((Join-Path $runDirectory 'run.lock'),[IO.FileMode]::CreateNew,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
Start-Transcript -LiteralPath (Join-Path $runDirectory 'runner.log') | Out-Null
function Write-Phase([string]$Name){@{UTC=[DateTime]::UtcNow.ToString('o');Phase=$Name} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $runDirectory 'phase.json') -Encoding UTF8}
function Assert-ManagementRoute {
    $local=@(Get-NetIPAddress -AddressFamily IPv4)
    if(@($local | Where-Object IPAddress -eq $state.ControllerAddress).Count){throw 'Refusing VPN tests on the controller PC.'}
    if(-not @($local | Where-Object IPAddress -eq $state.TestAddress).Count){throw 'Test address no longer belongs to this PC.'}
    $route=Find-NetRoute -RemoteIPAddress $state.ControllerAddress | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
    $script:adapter=Get-NetAdapter | Where-Object {$_.InterfaceGuid.ToString() -eq $state.AdapterGuid} | Select-Object -First 1
    if(-not $route -or -not $adapter.HardwareInterface -or $route.InterfaceIndex -ne $adapter.ifIndex -or $route.NextHop -ne '0.0.0.0'){throw 'Controller is no longer on the original physical LAN route.'}
}
$result=[ordered]@{StartedUTC=[DateTime]::UtcNow.ToString('o');Mode=$state.Mode;Success=$false;Error=''}
try {
    Assert-ManagementRoute
    $deadline=[DateTime]::Parse($state.DeadlineUTC).ToUniversalTime()
    if(($deadline-[DateTime]::UtcNow).TotalMinutes -lt 5){throw 'Cleanup deadline is too close or has expired.'}
    $task=Get-ScheduledTask -TaskName $state.CleanupTask -ErrorAction Stop
    if($task.Principal.UserId -notin 'SYSTEM','S-1-5-18' -or $task.State -eq 'Disabled' -or -not @($task.Triggers | Where-Object {$_.StartBoundary}).Count){throw 'Timed SYSTEM cleanup task is not armed.'}
    $proof=Get-Content -LiteralPath (Join-Path $runDirectory 'cleanup-result.json') -Raw | ConvertFrom-Json
    if(-not $proof.Success -or $proof.Identity -ne 'S-1-5-18'){throw 'SYSTEM cleanup has not been demonstrated successfully.'}
    if(Test-Path -LiteralPath (Join-Path $state.Bundle 'client-state.json')){throw 'CWG bundle already has saved running state.'}
    if(@(Get-Service -Name 'WireGuardTunnel$*' -ErrorAction SilentlyContinue | Where-Object Status -eq Running).Count){throw 'Another WireGuard tunnel is running on the test PC.'}
    $allowed=@(Get-Content -LiteralPath (Join-Path $state.Bundle 'cwg-full.conf') | Where-Object {$_ -match '^\s*AllowedIPs\s*='})
    if($allowed.Count -ne 1 -or $allowed[0] -notmatch '^\s*AllowedIPs\s*=\s*0\.0\.0\.0/1\s*,\s*128\.0\.0\.0/1\s*$'){throw 'Remote test requires the LAN-preserving two-/1 Full profile.'}
    Write-Phase 'preparing'
    if($state.IPv6OriginallyEnabled){Disable-NetAdapterBinding -Name $adapter.Name -ComponentID ms_tcpip6 | Out-Null}
    Assert-ManagementRoute
    if($state.Mode -eq 'Performance'){
        Write-Phase 'performance'
        $previousReports=@(Get-ChildItem -LiteralPath $state.Bundle -Directory -Filter 'adaptive-performance-*' | Select-Object -ExpandProperty FullName)
        & (Join-Path $PSScriptRoot 'Measure-AdaptivePerformance.ps1') -Bundle $state.Bundle
        $newReports=@(Get-ChildItem -LiteralPath $state.Bundle -Directory -Filter 'adaptive-performance-*' | Where-Object FullName -notin $previousReports)
        if($newReports.Count -ne 1){throw 'Could not identify the performance report for this run.'}
        $reportPath=Join-Path $newReports[0].FullName 'report.txt'
        $transfers=@(Get-Content -LiteralPath $reportPath | Where-Object {$_ -match '^\{'} | ForEach-Object {$_ | ConvertFrom-Json} | Where-Object direction)
        $result['ReportPath']=$reportPath
        if($transfers.Count -ne 8 -or @($transfers | Where-Object complete_and_verified -ne $true).Count){throw 'One or more performance transfers failed verification.'}
    }else{
        & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $state.Bundle -Mode Full -TracePackets
        Assert-ManagementRoute
        Write-Phase 'tunnel-active'
        $ping=[Net.NetworkInformation.Ping]::new()
        try{
            $samples=@(1..20 | ForEach-Object {$p=$ping.Send('10.77.0.1',1500);[pscustomobject]@{Status=$p.Status.ToString();RTT=$p.RoundtripTime};Start-Sleep -Milliseconds 250})
        }finally{$ping.Dispose()}
        $result['Pings']=$samples
        $exitAddress=& curl.exe --ipv4 --silent --show-error --max-time 20 https://api.ipify.org
        if($LASTEXITCODE -ne 0){throw 'HTTPS exit-IP test failed.'}
        $cfg=Get-Content -LiteralPath (Join-Path $state.Bundle 'client.json') -Raw | ConvertFrom-Json
        if($exitAddress.Trim() -ne $cfg.endpoint.Split(':')[0]){throw 'Internet exit address did not match the CWG server.'}
        $result['ExitAddress']=$exitAddress.Trim()
        if(@($samples | Where-Object Status -eq 'Success').Count -ne 20){throw 'Tunnel ping loss in readiness test.'}
        # Keep the tunnel active long enough for independent SSH/RDP reachability
        # checks from the controller. The SYSTEM deadline remains armed.
        Start-Sleep -Seconds 40
        Assert-ManagementRoute
    }
    $result.Success=$true
}catch{$result.Error=$_.Exception.Message}
finally{
    Write-Phase 'cleanup'
    try{& (Join-Path $PSScriptRoot 'Stop-RemoteTest.ps1') -StatePath $StatePath -FromRunner}catch{$result.Success=$false;$result.Error+=' Cleanup: '+$_.Exception.Message}
    $result['FinishedUTC']=[DateTime]::UtcNow.ToString('o')
    $result | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $runDirectory 'result.json') -Encoding UTF8
    Write-Phase 'finished'
    Stop-Transcript | Out-Null
    $guard.Dispose()
}
if(-not $result.Success){throw $result.Error}
