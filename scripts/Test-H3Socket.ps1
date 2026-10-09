# Offline process fixtures; no probe traffic or OS networking changes.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\h3-socket-'+[guid]::NewGuid().ToString('N'))
foreach ($dir in @('scripts','bin','h3','raw')) {New-Item -ItemType Directory -Path (Join-Path $fixture $dir) -Force | Out-Null}
$source=(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Measure-H3Socket.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
Set-Content -LiteralPath (Join-Path $fixture 'scripts\Measure-H3Socket.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'bin\cwg-windows-amd64.exe') -Value 'fixture'
Set-Content -LiteralPath (Join-Path $fixture 'h3\client.json') -Value '{"transport":"h3","endpoint":"203.0.113.1:443"}'
Set-Content -LiteralPath (Join-Path $fixture 'raw\client.json') -Value '{"endpoint":"203.0.113.1:8443"}'
function Get-NetUDPEndpoint {param($LocalPort,$ErrorAction); if ($global:SCase -eq 'active') {[pscustomobject]@{LocalPort=51821}}}
function Find-NetRoute {param($RemoteIPAddress); [pscustomobject]@{CimClass=[pscustomobject]@{CimClassName='MSFT_NetRoute'};InterfaceIndex=12}}
function Get-NetAdapter {[pscustomobject]@{ifIndex=12;Name='fixture';HardwareInterface=($global:SCase -ne 'virtual')}}
function Start-Process {
    param($FilePath,$ArgumentList,$WindowStyle,[switch]$PassThru,$RedirectStandardOutput,$RedirectStandardError)
    $global:SCalls+=, @($ArgumentList)
    if ($global:SCase -eq 'launch-error') {throw 'fixture launch failure'}
    if ($ArgumentList[0] -ne 'probe' -or $WindowStyle -ne 'Hidden') {throw 'Unexpected process'}
    Set-Content -LiteralPath $RedirectStandardOutput -Value '{}'
    Set-Content -LiteralPath $RedirectStandardError -Value 'fixture timeout'
    $traceIndex=[Array]::IndexOf($ArgumentList,'-trace')
    if ($traceIndex -ge 0 -and $global:SCase -ne 'no-trace') {
        Set-Content -LiteralPath $ArgumentList[$traceIndex+1].Trim('"') -Value '{"event":"h3-write","bytes":1200}'
    }
    $process=[pscustomobject]@{Handle=1;ExitCode=1;HasExited=$false}
    $process | Add-Member ScriptMethod WaitForExit {param($ms); if ($global:SCase -eq 'wait-error' -and $ms) {throw 'fixture wait failure'}; if ($global:SCase -eq 'timeout' -and $ms) {return $false};$this.HasExited=$true;return $true}
    $process | Add-Member ScriptMethod Kill {$this.HasExited=$true;$global:SKills++}
    $process | Add-Member ScriptMethod Dispose {}
    return $process
}
foreach ($case in @('failed-probes','no-trace','timeout','launch-error','wait-error','virtual','active')) {
    $global:SCase=$case;$global:SCalls=@();$global:SKills=0;$failed=$false
    try {& (Join-Path $fixture 'scripts\Measure-H3Socket.ps1') -Bundle (Join-Path $fixture 'h3') -RawBundle (Join-Path $fixture 'raw') | Out-Null} catch {$failed=$true}
    if ($failed -ne ($case -in @('launch-error','wait-error','virtual','active'))) {throw ('Wrong result: '+$case)}
    if ($case -in @('virtual','active')) {if ($global:SCalls.Count) {throw 'Guard launched a process'}}
    elseif ($case -notin @('launch-error','wait-error')) {
        if ($global:SCalls.Count -ne 4) {throw 'Wrong phase count'}
        for ($i=0;$i -lt 4;$i++) {
            $a=$global:SCalls[$i];$index=[Array]::IndexOf($a,'-interface-index')
            if ($i -eq 3) {if ('-trace' -in $a -or $index -ge 0 -or ($a -join ' ') -notmatch 'raw') {throw 'Wrong raw control'}}
            else {if ($index -lt 0 -or [int]$a[$index+1] -ne $(if ($i -eq 1) {12} else {0}) -or '-trace' -notin $a) {throw 'Wrong H3 trace or binding'}}
        }
    }
    if ($case -eq 'timeout' -and $global:SKills -ne 4) {throw 'Timed-out children not cleaned up'}
    if ($case -eq 'wait-error' -and $global:SKills -ne 1) {throw 'Interrupted child not cleaned up'}
    Write-Host ('PASS: '+$case)
}
Write-Host 'Seven offline socket-diagnostic fixtures passed; no network state changed.'
