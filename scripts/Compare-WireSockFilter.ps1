#Requires -RunAsAdministrator
# User-run comparison only. Never run this helper on the user's behalf.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Bundle,
    [switch]$TemporarilyUnbindWireSock,
    [switch]$RestoreOnly
)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
$recoveryPath=Join-Path $bundlePath 'wiresock-filter-state.json'
$component='nt_ndiswgc'
$reportPath=$null
function Write-Report([string]$Text) {
    if ($reportPath) { Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8 }
    Write-Host $Text
}
function Get-TargetAdapter {
    $found=@(Get-NetAdapter -Physical -ErrorAction Stop | Where-Object { [string]$_.InterfaceGuid -eq $adapterGuid })
    if ($found.Count -ne 1) { throw 'Cannot uniquely identify the original physical adapter. Recovery state was retained.' }
    return $found[0]
}
function Get-TargetBinding {
    $target=Get-TargetAdapter
    $found=@(Get-NetAdapterBinding -Name $target.Name -ComponentID $component -ErrorAction Stop)
    if ($found.Count -ne 1 -or $found[0].ComponentID -ne $component -or $null -eq $found[0].Enabled) {
        throw 'Cannot verify the WireSock filter binding.'
    }
    return $found[0]
}
function Restore-Filter {
    $saved=Get-Content -LiteralPath $recoveryPath -Raw | ConvertFrom-Json
    if ($saved.Version -ne 1 -or $saved.ComponentID -ne $component -or
        $saved.OriginallyEnabled -ne $true -or $saved.AdapterGuid -ne $adapterGuid) { throw 'Unrecognized filter recovery state; no changes made.' }
    $binding=Get-TargetBinding
    if (-not $binding.Enabled) { Enable-NetAdapterBinding -InputObject $binding -Confirm:$false -ErrorAction Stop }
    if (-not (Get-TargetBinding).Enabled) { throw 'WireSock filter restoration did not take effect. Recovery state was retained.' }
    Remove-Item -LiteralPath $recoveryPath -ErrorAction Stop
    Write-Report 'Verified: WireSock filter is enabled again on the original adapter.'
}
if ($RestoreOnly) {
    if ($TemporarilyUnbindWireSock) { throw 'Use RestoreOnly by itself.' }
    if (-not (Test-Path -LiteralPath $recoveryPath)) { Write-Host 'No pending WireSock filter restoration.'; return }
    $saved=Get-Content -LiteralPath $recoveryPath -Raw | ConvertFrom-Json
    $adapterGuid=[string]$saved.AdapterGuid
    $parsed=[guid]::Empty
    if (-not [guid]::TryParse($adapterGuid,[ref]$parsed)) { throw 'Invalid adapter identity in recovery state.' }
    Restore-Filter
    return
}
if (-not $TemporarilyUnbindWireSock) { throw 'This comparison requires -TemporarilyUnbindWireSock. It briefly detaches only WireSock from the physical adapter, then restores it.' }
if (Test-Path -LiteralPath $recoveryPath) { throw 'Finish pending recovery first: run this helper with -RestoreOnly.' }
if (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) { throw 'Stop CWG before this comparison.' }
if ($env:SESSIONNAME -like 'RDP-*') { throw 'Run this comparison at the local PC; changing the binding may interrupt remote access.' }
$cfg=Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
$parts=$cfg.endpoint.Split(':')
if ($parts.Count -ne 2) { throw 'An IPv4 server endpoint is required.' }
$serverIP=[Net.IPAddress]::Parse($parts[0])
if ($serverIP.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or [Net.IPAddress]::IsLoopback($serverIP)) { throw 'A remote IPv4 server endpoint is required.' }
function Selected-Route([string]$Address) {
    Find-NetRoute -RemoteIPAddress $Address -ErrorAction Stop | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
}
$route=Selected-Route $serverIP.IPAddressToString
$adapter=Get-NetAdapter -Physical -ErrorAction Stop | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $route -or -not $adapter -or $adapter.Status -ne 'Up') { throw 'Disconnect other VPNs; the server must route through an active physical adapter.' }
$adapterGuid=[string]$adapter.InterfaceGuid
if (-not (Get-TargetBinding).Enabled) { throw 'WireSock is already detached from this adapter; this comparison would not isolate it.' }
$prefix='filter-comparison-'+[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$parent=Join-Path $bundlePath $prefix
New-Item -ItemType Directory -Path $parent -ErrorAction Stop | Out-Null
$reportPath=Join-Path $bundlePath ($prefix+'.txt')
$sourceHashes=@{}
foreach ($file in @('client.json','cwg-full.conf')) { $sourceHashes[$file]=(Get-FileHash -LiteralPath (Join-Path $bundlePath $file)).Hash }
$ping=(Get-Command ping.exe -ErrorAction Stop).Source
function Wait-PhysicalPath {
    # Adapter reconfiguration can briefly interrupt connectivity. Inspect routes;
    # never send background probes or change other interfaces to recover it.
    for ($attempt=0; $attempt -lt 40; $attempt++) {
        try {
            $target=Get-TargetAdapter
            $selected=Selected-Route $serverIP.IPAddressToString
            if ($target.Status -eq 'Up' -and $selected.InterfaceIndex -eq $target.ifIndex) { return }
        } catch { }
        Start-Sleep -Milliseconds 500
    }
    throw 'The original physical path did not return within 20 seconds.'
}
function Run-Phase([string]$Name,[bool]$FilterEnabled) {
    Wait-PhysicalPath
    if ((Get-TargetBinding).Enabled -ne $FilterEnabled) { throw 'The WireSock binding changed unexpectedly.' }
    $phase=Join-Path $parent $Name
    New-Item -ItemType Directory -Path $phase -ErrorAction Stop | Out-Null
    foreach ($file in @('client.json','cwg-full.conf')) {
        $source=Join-Path $bundlePath $file
        if ((Get-FileHash -LiteralPath $source).Hash -ne $sourceHashes[$file]) { throw 'Original configuration changed during comparison.' }
        Copy-Item -LiteralPath $source -Destination (Join-Path $phase $file)
    }
    Write-Report ("`nPHASE "+$Name+' UTC: '+[DateTime]::UtcNow.ToString('o')+'; WireSock enabled: '+$FilterEnabled)
    Write-Report ('Private test bundle: '+$phase)
    $started=$false
    try {
        & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $phase -Mode Full -TracePackets
        $started=$true
        $target=Get-TargetAdapter
        if ((Selected-Route $serverIP.IPAddressToString).InterfaceIndex -ne $target.ifIndex -or
            (Selected-Route '1.1.1.1').InterfaceAlias -ne 'cwg-full') { throw 'Unexpected test routing.' }
        # Keep the association active before the normal diagnostics, so the
        # comparison samples beyond just the first few seconds after startup.
        $stdout=Join-Path $phase 'initial-ping.stdout.log'
        $stderr=Join-Path $phase 'initial-ping.stderr.log'
        $process=Start-Process -FilePath $ping -ArgumentList '-4 -n 12 -w 1000 10.77.0.1' -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        try {
            Write-Report ('Initial private ping exit: '+$process.ExitCode)
            Write-Report ((Get-Content -LiteralPath $stdout -Raw).TrimEnd())
        } finally { $process.Dispose() }
        & (Join-Path $PSScriptRoot 'Diagnose-Client.ps1') -Bundle $phase
        $diagnostic=Get-ChildItem -LiteralPath $phase -Filter 'diagnostic-*.txt' -File | Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1
        if (-not $diagnostic) { throw 'Diagnostic report was not created.' }
        Write-Report (Get-Content -LiteralPath $diagnostic.FullName -Raw)
        if ((Get-TargetBinding).Enabled -ne $FilterEnabled) { throw 'WireSock binding changed during the test; this phase is inconclusive.' }
    } finally {
        if ($started -or (Test-Path -LiteralPath (Join-Path $phase 'client-state.json'))) {
            & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $phase
            Write-Report ('Stopped '+$Name+' UTC: '+[DateTime]::UtcNow.ToString('o'))
        }
    }
}
Write-Report ('WireSock filter comparison UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Report ('Adapter: '+$adapter.Name+'; component: '+$component+'; endpoint: '+$cfg.endpoint)
Write-Report 'Three otherwise identical Full-mode tests: filter attached, detached, restored. UDP transport/configuration stays unchanged.'
Write-Report 'The binding changes may briefly interrupt Ethernet. Keep this PowerShell window open. Allow about 3-5 minutes.'
Write-Report ('If interrupted, restore with: .\scripts\Compare-WireSockFilter.ps1 -Bundle "'+$bundlePath+'" -RestoreOnly')
$restoreNeeded=$false
try {
    Run-Phase '01-attached' $true
    # Reserve and durably write recovery state BEFORE changing the binding.
    $saved=[ordered]@{Version=1;ComponentID=$component;AdapterGuid=$adapterGuid;AdapterName=$adapter.Name;OriginallyEnabled=$true}
    $bytes=[Text.Encoding]::UTF8.GetBytes(($saved | ConvertTo-Json))
    $stream=[IO.File]::Open($recoveryPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try { $stream.Write($bytes,0,$bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
    $restoreNeeded=$true
    Disable-NetAdapterBinding -InputObject (Get-TargetBinding) -Confirm:$false -ErrorAction Stop
    Start-Sleep -Milliseconds 3000
    Run-Phase '02-detached' $false
    Restore-Filter
    $restoreNeeded=$false
    Start-Sleep -Milliseconds 3000
    Run-Phase '03-restored' $true
} finally {
    if ($restoreNeeded) {
        try { Restore-Filter } catch {
            Write-Warning ('Filter recovery incomplete. Keep the saved state and run -RestoreOnly. '+$_.Exception.Message)
            throw
        }
    }
}
Write-Report ('Completed UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Host ('CWG is stopped; WireSock filter is restored. Report: '+$reportPath)
