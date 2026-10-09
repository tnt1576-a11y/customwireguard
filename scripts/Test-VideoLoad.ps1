# Offline fixtures; the real Ping object and all tunnel operations are replaced.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\video-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Measure-VideoLoad.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture').Replace('[Net.NetworkInformation.Ping]::new()','(New-FixturePing)')
Set-Content -LiteralPath (Join-Path $fixture 'Measure-VideoLoad.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'Start-Client.ps1') -Value @'
param($Bundle,$Mode,[switch]$TracePackets)
if ($Mode -ne 'Full' -or -not $TracePackets -or $Bundle -ne $global:CwgBundle) {throw 'Incorrect startup'}
$global:CwgStarts++
Set-Content -LiteralPath (Join-Path $Bundle 'client-state.json') -Value '{}'
$global:CwgTrace=Join-Path $Bundle 'packets-full-fixture.jsonl'
Set-Content -LiteralPath $global:CwgTrace -Value '{}'
if ($global:CwgCase -eq 'partial-start') {throw 'Fixture partial startup failure'}
'@
Set-Content -LiteralPath (Join-Path $fixture 'Stop-Client.ps1') -Value @'
param($Bundle)
if ($Bundle -ne $global:CwgBundle) {throw 'Wrong cleanup target'}
$global:CwgStops++
Remove-Item -LiteralPath (Join-Path $Bundle 'client-state.json')
'@
function New-FixturePing {
    $pinger=[pscustomobject]@{}
    $pinger | Add-Member ScriptMethod Send {
        param($Address,$Timeout)
        if ($Address -ne '10.77.0.1' -or $Timeout -ne 1000) {throw 'Wrong ping target'}
        $global:CwgPings++
        [pscustomobject]@{Status=$(if ($global:CwgPrompts -eq 1) {'TimedOut'} else {'Success'});RoundtripTime=90}
    }
    $pinger | Add-Member ScriptMethod Dispose {$global:CwgDisposed=$true}
    return $pinger
}
function Read-Host {
    param($Prompt)
    $global:CwgPrompts++
    if ($global:CwgCase -eq 'prompt-error') {throw 'Fixture prompt interrupted'}
    if ($global:CwgCase -eq 'trace-limit') {Add-Content -LiteralPath $global:CwgTrace -Value '{"event":"limit_reached","utc":"fixture-time"}'}
    return ''
}
function Start-Sleep {param($Milliseconds)}
function Get-NetAdapterStatistics {
    param($Name,$ErrorAction)
    if ($Name -ne 'cwg-full') {throw 'Wrong counter interface'}
    [pscustomobject]@{ReceivedBytes=$global:CwgPings*4096;SentBytes=$global:CwgPings*128}
}
foreach ($case in @('success','trace-limit','prompt-error','partial-start','pending-state')) {
    $global:CwgCase=$case;$global:CwgStarts=0;$global:CwgStops=0;$global:CwgPrompts=0;$global:CwgPings=0;$global:CwgDisposed=$false
    $global:CwgBundle=Join-Path $fixture $case
    New-Item -ItemType Directory -Path $global:CwgBundle | Out-Null
    if ($case -eq 'pending-state') {Set-Content -LiteralPath (Join-Path $global:CwgBundle 'client-state.json') -Value '{}'}
    $failed=$false
    try {& (Join-Path $fixture 'Measure-VideoLoad.ps1') -Bundle $global:CwgBundle -SamplesPerPhase 3 6>$null} catch {$failed=$true}
    if ($failed -ne ($case -ne 'success')) {throw ('Unexpected result: '+$case)}
    $expected=if ($case -eq 'pending-state') {0} else {1}
    if ($global:CwgStarts -ne $expected -or $global:CwgStops -ne $expected) {throw 'Session restarted or cleanup missing'}
    if ($case -notin @('partial-start','pending-state') -and -not $global:CwgDisposed) {throw 'Ping object not disposed'}
    if ($case -eq 'success') {
        $dir=Get-ChildItem -LiteralPath $global:CwgBundle -Directory -Filter 'video-load-*' | Select-Object -First 1
        $samples=@(Get-Content -LiteralPath (Join-Path $dir.FullName 'samples.jsonl') | ForEach-Object {$_ | ConvertFrom-Json})
        if ($samples.Count -ne 9 -or @($samples | Where-Object status -eq 'TimedOut').Count -ne 3 -or $global:CwgPrompts -ne 2) {throw 'Incorrect sample/phase measurement'}
        $report=Get-Content -LiteralPath ($dir.FullName+'.txt') -Raw
        if ($report -notmatch '"loss_percent":100' -or $report -notmatch '03-idle-after') {throw 'Loss/recovery not reported'}
    }
    if ($case -eq 'trace-limit' -and ($global:CwgPings -ne 3 -or $global:CwgPrompts -ne 1)) {throw 'Continued after logging stopped'}
    Write-Host ('PASS: '+$case)
}
Write-Host 'Five offline video-load cases passed: one session, phase loss/recovery, trace limit, interruption and partial-start cleanup.'
