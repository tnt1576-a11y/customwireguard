# Offline fixtures only: no actual bindings, tunnels, processes or traffic.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\filter-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Compare-WireSockFilter.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
Set-Content -LiteralPath (Join-Path $fixture 'Compare-WireSockFilter.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'Start-Client.ps1') -Value @'
param($Bundle,$Mode,[switch]$TracePackets)
if ($Mode -ne 'Full' -or -not $TracePackets) {throw 'Wrong startup settings'}
$global:CwgPhase=Split-Path $Bundle -Leaf
$global:CwgStarted+=$global:CwgPhase
$global:CwgPhaseBindings+=$global:CwgFilterEnabled
Set-Content -LiteralPath (Join-Path $Bundle 'client-state.json') -Value '{}'
'@
Set-Content -LiteralPath (Join-Path $fixture 'Stop-Client.ps1') -Value @'
param($Bundle)
$global:CwgStopped+=(Split-Path $Bundle -Leaf)
Remove-Item -LiteralPath (Join-Path $Bundle 'client-state.json')
$global:CwgPhase=''
'@
Set-Content -LiteralPath (Join-Path $fixture 'Diagnose-Client.ps1') -Value @'
param($Bundle)
if (($global:CwgScenario -eq 'baseline-error' -and $global:CwgPhase -eq '01-attached') -or
    ($global:CwgScenario -eq 'detached-error' -and $global:CwgPhase -eq '02-detached')) {throw 'Fixture diagnostic error'}
Set-Content -LiteralPath (Join-Path $Bundle 'diagnostic-fixture.txt') -Value 'Fixture: no network test performed.'
'@
function Get-NetAdapter {
    param([switch]$Physical,$ErrorAction)
    [pscustomobject]@{ifIndex=12;InterfaceGuid='{11111111-1111-1111-1111-111111111111}';Name=$(if ($global:CwgRenamed) {'Renamed Ethernet'} else {'Fixture Ethernet'});Status=$(if ($global:CwgScenario -eq 'path-down' -and -not $global:CwgFilterEnabled) {'Down'} else {'Up'})}
}
function Get-NetAdapterBinding {
    param($Name,$ComponentID,$ErrorAction)
    if ($ComponentID -ne 'nt_ndiswgc' -or $Name -ne (Get-NetAdapter).Name) {throw 'Wrong binding lookup'}
    [pscustomobject]@{Name=$Name;ComponentID=$ComponentID;Enabled=$global:CwgFilterEnabled}
}
function Disable-NetAdapterBinding {
    param($InputObject,$Confirm,$ErrorAction)
    if ($InputObject.ComponentID -ne 'nt_ndiswgc' -or $InputObject.Name -ne (Get-NetAdapter).Name) {throw 'Wrong filter target'}
    $global:CwgDisableCalls++
    $global:CwgFilterEnabled=$false
    if ($global:CwgScenario -eq 'renamed') {$global:CwgRenamed=$true}
    if ($global:CwgScenario -eq 'disable-throws') {throw 'Fixture: disable applied, then failed'}
}
function Enable-NetAdapterBinding {
    param($InputObject,$Confirm,$ErrorAction)
    if ($InputObject.ComponentID -ne 'nt_ndiswgc' -or $InputObject.Name -ne (Get-NetAdapter).Name) {throw 'Wrong restoration target'}
    $global:CwgEnableCalls++
    if ($global:CwgScenario -eq 'restore-error') {throw 'Fixture restoration failure'}
    $global:CwgFilterEnabled=$true
}
function Find-NetRoute {
    param($RemoteIPAddress,$ErrorAction)
    $tunnel=$global:CwgPhase -and $RemoteIPAddress -eq '1.1.1.1'
    if ($global:CwgScenario -eq 'wrong-route' -and $global:CwgPhase -eq '02-detached') {$tunnel=$false}
    [pscustomobject]@{CimClass=[pscustomobject]@{CimClassName='MSFT_NetRoute'};InterfaceIndex=$(if ($tunnel) {39} else {12});InterfaceAlias=$(if ($tunnel) {'cwg-full'} else {(Get-NetAdapter).Name})}
}
function Start-Sleep {param($Milliseconds)}
function Get-Command {param($Name,$ErrorAction);[pscustomobject]@{Source=$Name}}
function Start-Process {
    param($FilePath,$ArgumentList,$WindowStyle,[switch]$Wait,[switch]$PassThru,$RedirectStandardOutput,$RedirectStandardError)
    if ($FilePath -ne 'ping.exe' -or $ArgumentList -ne '-4 -n 12 -w 1000 10.77.0.1' -or $WindowStyle -ne 'Hidden') {throw 'Unexpected fixture command'}
    Set-Content -LiteralPath $RedirectStandardOutput -Value 'Fixture ping'
    Set-Content -LiteralPath $RedirectStandardError -Value ''
    $process=[pscustomobject]@{ExitCode=0}
    $process | Add-Member -MemberType ScriptMethod -Name Dispose -Value {}
    return $process
}
$oldSession=$env:SESSIONNAME
try {
    $env:SESSIONNAME='Console'
    foreach ($scenario in @('success','renamed','baseline-error','detached-error','disable-throws','restore-error','wrong-route','path-down','already-disabled','no-opt-in')) {
        $global:CwgScenario=$scenario;$global:CwgFilterEnabled=$scenario -ne 'already-disabled';$global:CwgRenamed=$false
        $global:CwgPhase='';$global:CwgStarted=@();$global:CwgStopped=@();$global:CwgPhaseBindings=@();$global:CwgDisableCalls=0;$global:CwgEnableCalls=0
        $bundle=Join-Path $fixture $scenario
        New-Item -ItemType Directory -Path $bundle | Out-Null
        Set-Content -LiteralPath (Join-Path $bundle 'client.json') -Value '{"endpoint":"203.0.113.1:443"}'
        Set-Content -LiteralPath (Join-Path $bundle 'cwg-full.conf') -Value "[Interface]`nAddress = 10.77.0.2/32`n[Peer]`nAllowedIPs = 0.0.0.0/1, 128.0.0.0/1"
        $hash=(Get-FileHash -LiteralPath (Join-Path $bundle 'cwg-full.conf')).Hash
        $failed=$false
        try {
            & (Join-Path $fixture 'Compare-WireSockFilter.ps1') -Bundle $bundle -TemporarilyUnbindWireSock:($scenario -ne 'no-opt-in') | Out-Null
        } catch { $failed=$true }
        if ($failed -ne ($scenario -notin @('success','renamed'))) {throw ('Unexpected result: '+$scenario)}
        if (($global:CwgStarted -join ',') -ne ($global:CwgStopped -join ',')) {throw ('Tunnel cleanup mismatch: '+$scenario)}
        if ((Get-FileHash -LiteralPath (Join-Path $bundle 'cwg-full.conf')).Hash -ne $hash) {throw 'Original config changed'}
        $state=Join-Path $bundle 'wiresock-filter-state.json'
        if ($scenario -eq 'restore-error') {
            if ($global:CwgFilterEnabled -or -not (Test-Path -LiteralPath $state)) {throw 'Failed restoration lost recovery state'}
            $global:CwgScenario='recover'
            $started=$global:CwgStarted.Count
            & (Join-Path $fixture 'Compare-WireSockFilter.ps1') -Bundle $bundle -RestoreOnly | Out-Null
            if (-not $global:CwgFilterEnabled -or (Test-Path -LiteralPath $state) -or $global:CwgStarted.Count -ne $started) {throw 'RestoreOnly failed or started a tunnel'}
        } elseif ((Test-Path -LiteralPath $state) -or ($global:CwgFilterEnabled -ne ($scenario -ne 'already-disabled'))) {throw ('Binding or recovery mismatch: '+$scenario)}
        if ($scenario -in @('success','renamed')) {
            if (($global:CwgStarted -join ',') -ne '01-attached,02-detached,03-restored' -or ($global:CwgPhaseBindings -join ',') -ne 'True,False,True' -or $global:CwgDisableCalls -ne 1 -or $global:CwgEnableCalls -ne 1) {throw 'Comparison order or binding changes incorrect'}
        }
        if ($scenario -in @('baseline-error','no-opt-in','already-disabled') -and ($global:CwgDisableCalls -or $global:CwgEnableCalls)) {throw 'Filter changed despite failed prerequisite'}
        Write-Host ('PASS: '+$scenario)
    }
} finally {$env:SESSIONNAME=$oldSession}
Write-Host 'Ten offline filter-comparison cases passed, including interrupted-disable restoration and RestoreOnly recovery.'
