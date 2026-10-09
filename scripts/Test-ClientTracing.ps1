# Offline fixtures: fake tunnel, ping and diagnostics. No real network changes.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\tracing-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Compare-ClientTracing.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
Set-Content -LiteralPath (Join-Path $fixture 'Compare-ClientTracing.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'Start-Client.ps1') -Value @'
param($Bundle,$Mode,[switch]$TracePackets)
if ($Bundle -ne $global:CwgOriginalBundle -or $Mode -ne 'Full') {throw 'Bundle path or mode changed'}
$global:CwgCalls++
$global:CwgTraceFlags+=[bool]$TracePackets
Set-Content -LiteralPath (Join-Path $Bundle 'client-state.json') -Value '{}'
Set-Content -LiteralPath (Join-Path $Bundle 'transport.stderr.log') -Value ('phase-'+$global:CwgCalls)
if ($TracePackets) {Set-Content -LiteralPath (Join-Path $Bundle ('packets-full-fixture-'+$global:CwgCalls+'.jsonl')) -Value '{}'}
if ($global:CwgScenario -eq 'partial-start') {throw 'Fixture failure after saving state'}
'@
Set-Content -LiteralPath (Join-Path $fixture 'Stop-Client.ps1') -Value @'
param($Bundle)
if ($Bundle -ne $global:CwgOriginalBundle) {throw 'Stopped wrong bundle'}
$global:CwgStops++
if ($global:CwgScenario -eq 'cleanup-error') {throw 'Fixture cleanup failure'}
Remove-Item -LiteralPath (Join-Path $Bundle 'client-state.json')
'@
Set-Content -LiteralPath (Join-Path $fixture 'Diagnose-Client.ps1') -Value @'
param($Bundle)
if ($Bundle -ne $global:CwgOriginalBundle) {throw 'Diagnosed wrong bundle'}
if ($global:CwgScenario -eq 'diagnostic-error') {throw 'Fixture diagnostic failure'}
Set-Content -LiteralPath (Join-Path $Bundle ('diagnostic-fixture-'+$global:CwgCalls+'.txt')) -Value 'Fixture: no traffic sent'
'@
function Get-Command {param($Name,$ErrorAction);[pscustomobject]@{Source=$Name}}
function Start-Sleep {param($Milliseconds)}
function Start-Process {
    param($FilePath,$ArgumentList,$WindowStyle,[switch]$Wait,[switch]$PassThru,$RedirectStandardOutput,$RedirectStandardError)
    if ($FilePath -ne 'ping.exe' -or $ArgumentList -ne '-4 -n 12 -w 1000 10.77.0.1' -or $WindowStyle -ne 'Hidden') {throw 'Unexpected command'}
    if ($global:CwgScenario -eq 'ping-launch-error') {throw 'Fixture ping launch failure'}
    Set-Content -LiteralPath $RedirectStandardOutput -Value 'Fixture ping timed out'
    Set-Content -LiteralPath $RedirectStandardError -Value ''
    # A network failure must be recorded, not prevent the remaining comparisons.
    $result=[pscustomobject]@{ExitCode=1}
    $result | Add-Member -MemberType ScriptMethod -Name Dispose -Value {}
    return $result
}
foreach ($scenario in @('success','partial-start','diagnostic-error','ping-launch-error','cleanup-error','recovery-pending')) {
    $global:CwgScenario=$scenario;$global:CwgCalls=0;$global:CwgStops=0;$global:CwgTraceFlags=@()
    $bundle=Join-Path $fixture $scenario
    New-Item -ItemType Directory -Path $bundle | Out-Null
    $global:CwgOriginalBundle=$bundle
    Set-Content -LiteralPath (Join-Path $bundle 'client.json') -Value '{"endpoint":"203.0.113.1:443"}'
    Set-Content -LiteralPath (Join-Path $bundle 'cwg-full.conf') -Value 'fixture-only configuration'
    Set-Content -LiteralPath (Join-Path $bundle 'transport.stderr.log') -Value 'previous-run-evidence'
    $hash=(Get-FileHash -LiteralPath (Join-Path $bundle 'cwg-full.conf')).Hash
    if ($scenario -eq 'recovery-pending') {Set-Content -LiteralPath (Join-Path $bundle 'wiresock-filter-state.json') -Value '{}'}
    $failed=$false
    try {& (Join-Path $fixture 'Compare-ClientTracing.ps1') -Bundle $bundle 6>$null} catch {$failed=$true}
    if ($failed -ne ($scenario -ne 'success')) {throw ('Unexpected outcome: '+$scenario)}
    $expected=if ($scenario -eq 'success') {4} elseif ($scenario -eq 'recovery-pending') {0} else {1}
    if ($global:CwgCalls -ne $expected -or $global:CwgStops -ne $expected) {throw 'Start/cleanup counts differ'}
    if ($scenario -eq 'success' -and ($global:CwgTraceFlags -join ',') -ne 'False,True,False,True') {throw 'Wrong trace sequence'}
    if ((Get-FileHash -LiteralPath (Join-Path $bundle 'cwg-full.conf')).Hash -ne $hash) {throw 'Original config changed'}
    if ((Test-Path -LiteralPath (Join-Path $bundle 'client-state.json')) -ne ($scenario -eq 'cleanup-error')) {throw 'Incorrect state cleanup'}
    if ($expected) {
        $comparison=Get-ChildItem -LiteralPath $bundle -Directory -Filter 'trace-comparison-*' | Select-Object -First 1
        if ((Get-Content -LiteralPath (Join-Path $comparison.FullName 'previous-logs\transport.stderr.log') -Raw).Trim() -ne 'previous-run-evidence') {throw 'Previous evidence lost'}
        if ((Get-Content -LiteralPath (Join-Path $comparison.FullName '01-off\transport.stderr.log') -Raw).Trim() -ne 'phase-1') {throw 'Failed-phase evidence not archived'}
        if ($scenario -eq 'success') {
            foreach ($phase in @('01-off','02-on','03-off','04-on')) {
                $directory=Join-Path $comparison.FullName $phase
                $traces=@(Get-ChildItem -LiteralPath $directory -Filter 'packets-full-*.jsonl')
                if ($traces.Count -ne [int]$phase.EndsWith('-on')) {throw 'Trace artifacts mixed across phases'}
                if (@(Get-ChildItem -LiteralPath $directory -Filter 'diagnostic-*.txt').Count -ne 1) {throw 'Diagnostic artifacts mixed across phases'}
            }
        }
    }
    Write-Host ('PASS: '+$scenario)
}
Write-Host 'Six offline tracing comparison cases passed; configuration paths, trace order, cleanup and evidence preservation verified.'
