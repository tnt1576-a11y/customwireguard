#Requires -RunAsAdministrator
# User-run only. Capture the encrypted outer UDP flow while Compare-TunnelModes
# starts and stops its tunnels. Do not run the comparison on the user's behalf.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$pktmon = (Get-Command pktmon.exe -ErrorAction Stop).Source
$cfg = Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
$parts = $cfg.endpoint.Split(':')
if ($parts.Count -ne 2) { throw 'This diagnostic requires a literal IPv4 endpoint.' }
$serverIP = [System.Net.IPAddress]::Parse($parts[0])
$serverPort = [int]$parts[1]
if ($serverIP.AddressFamily -ne [System.Net.Sockets.AddressFamily]::InterNetwork -or $serverPort -lt 1 -or $serverPort -gt 65535) { throw 'Invalid IPv4 endpoint.' }
$status = (& $pktmon status 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0 -or $status -notmatch 'Packet Monitor is not running') { throw 'Packet Monitor is already running or its state is unknown. This helper will not replace another capture.' }
$filters = (& $pktmon filter list 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0 -or $filters -notmatch '(?m)^\s*None\s*$') { throw 'Packet Monitor has existing filters. This helper will not change them or capture their traffic.' }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$prefix = Join-Path $bundlePath ('network-path-' + $stamp)
$etl = $prefix + '.etl'
$log = $prefix + '.commands.txt'
$filterName = 'CWG-' + $stamp
$filterAdded = $false
$captureStarted = $false
$captureStopped = $false
function Invoke-PacketMonitor([string[]]$Arguments) {
    $output = & $pktmon @Arguments 2>&1
    $code = $LASTEXITCODE
    $text = ($output | Out-String).TrimEnd()
    Add-Content -LiteralPath $log -Value (([DateTime]::UtcNow.ToString('o')) + ' pktmon ' + ($Arguments -join ' ') + "`r`n" + $text) -Encoding UTF8
    Write-Host $text
    if ($code -ne 0) { throw ('Packet Monitor failed with exit code ' + $code + '. See ' + $log) }
}
Write-Host 'Capturing only the server IP/UDP port used by CWG, including encrypted datagrams.'
Write-Host 'The capture stays in this private bundle and is capped at 64 MB. No keys are recorded.'
try {
    Invoke-PacketMonitor -Arguments @('filter','add',$filterName,'-i',$serverIP.IPAddressToString,'-t','UDP','-p',([string]$serverPort))
    $filterAdded = $true
    Invoke-PacketMonitor -Arguments @('start','--capture','--comp','all','--type','all','--pkt-size','0','--file-name',$etl,'--file-size','64','--log-mode','circular')
    $captureStarted = $true
    & (Join-Path $PSScriptRoot 'Compare-TunnelModes.ps1') -Bundle $bundlePath
} finally {
    try {
        if ($captureStarted) {
            Invoke-PacketMonitor -Arguments @('stop')
            $captureStopped = $true
        }
    } finally {
        if ($filterAdded) {
            $currentFilters = (& $pktmon filter list 2>&1 | Out-String)
            $listed = @($currentFilters -split "`r?`n" | Where-Object { $_ -match '^\s*\d+\s+' })
            # This installed pktmon can only remove ALL filters. Remove them
            # only when the one filter created by this run is still the only one.
            if ($LASTEXITCODE -eq 0 -and $listed.Count -eq 1 -and $listed[0].Contains($filterName)) {
                Invoke-PacketMonitor -Arguments @('filter','remove')
            } else {
                Write-Warning ('Filter state changed or could not be verified. Left filters untouched; our filter is ' + $filterName)
            }
        }
    }
    if ($captureStopped -and (Test-Path -LiteralPath $etl)) {
        Invoke-PacketMonitor -Arguments @('etl2pcap',$etl,'--out',($prefix + '.pcapng'))
        Invoke-PacketMonitor -Arguments @('etl2txt',$etl,'--out',($prefix + '.txt'),'--brief')
        Write-Host ('Saved network capture: ' + $prefix + '.pcapng')
    }
}
Write-Host 'CWG and the capture have stopped. Reconnect your usual VPN and report done.'
