# Offline fixtures only: no real process, packet capture, probes, or network state.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\quic-path-'+[guid]::NewGuid().ToString('N'))
foreach ($dir in @('scripts','bin','h3','raw')) {New-Item -ItemType Directory -Path (Join-Path $fixture $dir) -Force | Out-Null}
$source=(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Measure-QUICPath.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
Set-Content -LiteralPath (Join-Path $fixture 'scripts\Measure-QUICPath.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'bin\cwg-windows-amd64.exe') -Value 'not executable'
Set-Content -LiteralPath (Join-Path $fixture 'h3\client.json') -Value '{"transport":"h3","endpoint":"203.0.113.1:443"}'
Set-Content -LiteralPath (Join-Path $fixture 'raw\client.json') -Value '{"endpoint":"203.0.113.1:8443"}'
function Get-Command {param($Name,$ErrorAction); if ($Name -ne 'pktmon.exe') {throw 'Unexpected lookup'}; [pscustomobject]@{Source='Invoke-FakePktmon'}}
function Get-NetUDPEndpoint {param($LocalPort,$ErrorAction); if ($global:QCase -eq 'active-client') {[pscustomobject]@{LocalPort=51821}}}
function Find-NetRoute {param($RemoteIPAddress); [pscustomobject]@{CimClass=[pscustomobject]@{CimClassName='MSFT_NetRoute'};InterfaceIndex=12}}
function Get-NetAdapter {[pscustomobject]@{ifIndex=12;HardwareInterface=($global:QCase -ne 'virtual-route');Name='fixture'}}
function Invoke-FakePktmon {
    $items=@($args); $global:LASTEXITCODE=0; $global:QCalls+=($items -join ' ')
    switch ($items[0]) {
        'status' {if ($global:QCase -eq 'existing-capture') {'Packet Monitor is running.'} else {'Packet Monitor is not running.'}}
        'filter' {switch ($items[1]) {
            'list' {if ($global:QFilter) {$display=$global:QFilter; if ($display.Length -gt 26) {$display=$display.Substring(0,23)+'...'}; "Filters`n1 $display UDP"} else {"Filters`n None"}}
            'add' {$global:QFilter=$items[2];'added'}
            'remove' {$global:QFilter='';'removed'}
            default {throw 'Unknown filter command'}
        }}
        'start' {$i=[Array]::IndexOf($items,'--file-name'); Set-Content -LiteralPath $items[$i+1] -Value 'fixture capture';'started'}
        'stop' {if ($global:QCase -eq 'stop-error') {$global:LASTEXITCODE=1};'stop result'}
        'etl2pcap' {Set-Content -LiteralPath $items[3] -Value 'fixture';'converted'}
        'etl2txt' {Set-Content -LiteralPath $items[3] -Value 'fixture';'converted'}
        default {throw 'Unexpected pktmon action'}
    }
}
function Start-Process {
    param($FilePath,$ArgumentList,$WindowStyle,[switch]$PassThru,$RedirectStandardOutput,$RedirectStandardError)
    $global:QLaunches+=($ArgumentList -join ' ')
    if ($global:QCase -eq 'launch-error') {throw 'fixture launch failure'}
    if ($ArgumentList[0] -ne 'probe' -or $WindowStyle -ne 'Hidden') {throw 'Unexpected process action'}
    if ($global:QCase -eq 'foreign-filter') {$global:QFilter='ChangedBySomeoneElse'}
    Set-Content -LiteralPath $RedirectStandardOutput -Value '{}'
    Set-Content -LiteralPath $RedirectStandardError -Value 'fixture preflight error'
    $process=[pscustomobject]@{ExitCode=1;HasExited=$false}
    $process | Add-Member ScriptMethod WaitForExit {
        param($ms)
        if ($global:QCase -eq 'wait-error' -and $ms) {throw 'fixture wait failure'}
        if ($global:QCase -eq 'timeout' -and $ms) {return $false}
        $this.HasExited=$true;return $true
    }
    $process | Add-Member ScriptMethod Kill {$this.HasExited=$true; $global:QKills++}
    $process | Add-Member ScriptMethod Refresh {}
    $process | Add-Member ScriptMethod Dispose {}
    return $process
}
$cases=@('failed-probes','timeout','launch-error','wait-error','existing-capture','existing-filter','active-client','virtual-route','foreign-filter','stop-error')
foreach ($case in $cases) {
    $global:QCase=$case; $global:QCalls=@(); $global:QLaunches=@(); $global:QFilter=''; $global:QKills=0
    if ($case -eq 'existing-filter') {$global:QFilter='UserFilter'}
    $failed=$false
    try {& (Join-Path $fixture 'scripts\Measure-QUICPath.ps1') -Bundle (Join-Path $fixture 'h3') -RawBundle (Join-Path $fixture 'raw') | Out-Null} catch {$failed=$true}
    $guard=$case -in @('existing-capture','existing-filter','active-client','virtual-route')
    $expectedFail=$guard -or $case -in @('launch-error','wait-error','stop-error')
    if ($failed -ne $expectedFail) {throw ('Unexpected result: '+$case)}
    if ($guard) {
        if ($global:QLaunches.Count -or ($global:QCalls | Where-Object {$_ -match '^(start|stop|filter (add|remove))'})) {throw 'Changed preexisting state'}
    } else {
        if ('stop' -notin $global:QCalls) {throw ('Capture cleanup missing: '+$case)}
        if ($case -eq 'foreign-filter') {
            if ('filter remove' -in $global:QCalls) {throw 'Removed foreign filter'}
        } elseif ($global:QFilter -or 'filter remove' -notin $global:QCalls) {throw ('Filter cleanup missing: '+$case)}
        if ($case -notin @('launch-error','wait-error','stop-error')) {
            if ($global:QLaunches.Count -ne 3 -or $global:QLaunches[0] -notmatch 'h3.*-count 2$' -or $global:QLaunches[1] -notmatch 'raw.*-count 4$' -or $global:QLaunches[2] -notmatch 'h3.*-count 2$') {throw 'Wrong comparison sequence'}
        }
        if ($case -eq 'timeout' -and $global:QKills -ne 3) {throw 'Timeout did not stop owned children'}
        if ($case -eq 'wait-error' -and $global:QKills -ne 1) {throw 'Exception did not stop owned child'}
    }
    Write-Host ('PASS: '+$case)
}
Write-Host 'Ten offline preflight/capture ownership fixtures passed. No real probes or network changes.'
