# Offline cleanup regression fixtures; no real services, tasks or bindings change.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\remote-cleanup-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content (Join-Path $PSScriptRoot 'Stop-RemoteTest.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
$source | Set-Content -LiteralPath (Join-Path $fixture 'Stop-RemoteTest.ps1') -Encoding UTF8
@'
param($Bundle)
$global:RemoteCleanupCalls.Add('stop-client')
if($global:RemoteCleanupCase -eq 'stop-failure'){throw 'Fixture stop failed'}
'@ | Set-Content -LiteralPath (Join-Path $fixture 'Stop-Client.ps1') -Encoding UTF8
function Get-NetAdapter {
    [pscustomobject]@{Name='Fixture Ethernet';HardwareInterface=$true;InterfaceGuid=$(if($global:RemoteCleanupCase -eq 'wrong-adapter'){'changed-guid'}else{'fixture-guid'})}
}
function Get-NetAdapterBinding {param($Name,$ComponentID);[pscustomobject]@{Enabled=$global:RemoteCleanupIPv6}}
function Enable-NetAdapterBinding {param($Name,$ComponentID);$global:RemoteCleanupCalls.Add('restore-ipv6');$global:RemoteCleanupIPv6=$true}
function Get-ScheduledTask {param($TaskName);[pscustomobject]@{TaskName=$TaskName}}
function Stop-ScheduledTask {param([Parameter(ValueFromPipeline)]$InputObject);process{$global:RemoteCleanupCalls.Add('stop-runner');if($global:RemoteCleanupCase -eq 'runner-failure'){throw 'Fixture runner stop failed'}}}
function Unregister-ScheduledTask {param($TaskName,[switch]$Confirm);$global:RemoteCleanupCalls.Add('remove-watchdog')}
foreach($case in 'watchdog','normal','proof','disabled-ipv6','stop-failure','runner-failure','wrong-adapter','wrong-computer'){
    $global:RemoteCleanupCase=$case
    $global:RemoteCleanupCalls=[Collections.Generic.List[string]]::new()
    $global:RemoteCleanupIPv6=$false
    $run=Join-Path $fixture $case
    New-Item -ItemType Directory -Path $run | Out-Null
    $state=[ordered]@{Version=1;Computer=$env:COMPUTERNAME;CleanupTask=('CWG-Lab-Cleanup-'+('a'*32));RunnerTask=('CWG-Lab-Run-'+('a'*32));Bundle=$run;AdapterGuid='fixture-guid';IPv6OriginallyEnabled=($case -ne 'disabled-ipv6')}
    if($case -eq 'wrong-computer'){$state.Computer='different-computer'}
    $statePath=Join-Path $run 'state.json'
    $state | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8
    $failure=$null
    try{& (Join-Path $fixture 'Stop-RemoteTest.ps1') -StatePath $statePath -FromRunner:($case -eq 'normal') -KeepWatchdog:($case -eq 'proof')}catch{$failure=$_.Exception.Message}
    if(($case -in 'stop-failure','runner-failure','wrong-adapter','wrong-computer') -ne [bool]$failure){throw "Unexpected failure state for ${case}: $failure"}
    if($case -eq 'wrong-computer'){
        if($global:RemoteCleanupCalls.Count){throw 'Wrong host changed state'}
    }else{
        $report=Get-Content (Join-Path $run 'cleanup-result.json') -Raw | ConvertFrom-Json
        if($report.Success -eq [bool]$failure){throw 'Incorrect cleanup result'}
        if($case -notin 'wrong-adapter','disabled-ipv6' -and -not $global:RemoteCleanupIPv6){throw 'IPv6 restoration missing, including after failed client cleanup'}
        if($case -in 'wrong-adapter','disabled-ipv6' -and $global:RemoteCleanupCalls.Contains('restore-ipv6')){throw 'Wrong binding restoration'}
        if(($case -notin 'normal','proof') -and $global:RemoteCleanupCalls[0] -ne 'stop-runner'){throw 'Watchdog must stop runner before client cleanup'}
        if($case -in 'normal','proof' -and $global:RemoteCleanupCalls.Contains('stop-runner')){throw 'Normal cleanup stopped its own task'}
        if(($case -in 'proof','stop-failure','runner-failure','wrong-adapter') -eq $global:RemoteCleanupCalls.Contains('remove-watchdog')){throw 'Watchdog removal policy failed'}
    }
    Write-Host "PASS: $case"
}
