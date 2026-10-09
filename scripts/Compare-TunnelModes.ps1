#Requires -RunAsAdministrator
# Explicitly user-run: starts Test then Full mode, collects bounded diagnostics,
# and stops each mode in finally. Never run this on behalf of the user.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$configPath = Join-Path $bundlePath 'client.json'
$cfg = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
if (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) { throw 'Stop CWG before running the comparison.' }
foreach ($name in @('cwg-test','cwg-full')) {
    $service = Get-Service -Name ('WireGuardTunnel$' + $name) -ErrorAction SilentlyContinue
    if ($service) { $service.Dispose(); throw 'Stop the existing CWG service before running the comparison.' }
}
if (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue) { throw 'Stop CWG first; its local ports are in use.' }
$endpointParts = $cfg.endpoint.Split(':')
if ($endpointParts.Count -ne 2) { throw 'This comparison requires an IPv4 endpoint.' }
$serverIP = $endpointParts[0]
$route = Find-NetRoute -RemoteIPAddress $serverIP | Where-Object { $_.CimClass.CimClassName -eq 'MSFT_NetRoute' } | Select-Object -First 1
$adapter = Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $route -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs first; the server must route through a physical adapter.' }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$prefix = 'mode-comparison-' + $stamp
$reportPath = Join-Path $bundlePath ($prefix + '.txt')
$pingPath = (Get-Command ping.exe -ErrorAction Stop).Source
$curlPath = (Get-Command curl.exe -ErrorAction Stop).Source
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Invoke-Measurement([string]$Name,[string]$Executable,[string]$Arguments) {
    Write-Report ("`n" + $Name + ' UTC: ' + [DateTime]::UtcNow.ToString('o'))
    $stdout = Join-Path $bundlePath ($prefix + '-' + $Name + '.stdout.log')
    $stderr = Join-Path $bundlePath ($prefix + '-' + $Name + '.stderr.log')
    $process = Start-Process -FilePath $Executable -ArgumentList $Arguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        Write-Report ('Exit code: ' + $process.ExitCode)
        foreach ($path in @($stdout,$stderr)) {
            $text = Get-Content -LiteralPath $path -Raw
            if ($text) { Write-Report $text.TrimEnd() }
        }
    } finally { $process.Dispose() }
}
Write-Report ('CWG mode comparison UTC: ' + [DateTime]::UtcNow.ToString('o'))
Write-Report ('Endpoint: ' + $cfg.endpoint + '; physical adapter: ' + $adapter.Name)
Write-Report 'This command starts Test then Full mode and stops both automatically. Allow about two minutes.'
Write-Report 'Packet traces contain encrypted-packet hashes and socket metadata, never packet bodies or keys.'
foreach ($mode in @('Test','Full')) {
    $started = $false
    try {
        Write-Report ("`nStarting " + $mode + ' UTC: ' + [DateTime]::UtcNow.ToString('o'))
        & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $bundlePath -Mode $mode -TracePackets
        $started = $true
        foreach ($destination in @($serverIP,'127.0.0.1','10.77.0.1','1.1.1.1')) {
            $chosen = Find-NetRoute -RemoteIPAddress $destination | Where-Object { $_.CimClass.CimClassName -eq 'MSFT_NetRoute' } | Select-Object -First 1
            Write-Report ("Route to ${destination}: " + (($chosen | Select-Object DestinationPrefix,NextHop,InterfaceAlias,InterfaceIndex,RouteMetric | ConvertTo-Json -Compress)))
        }
        Invoke-Measurement ($mode + '-small-ping') $pingPath '-4 -n 8 -w 1200 -l 32 10.77.0.1'
        Invoke-Measurement ($mode + '-large-ping') $pingPath '-4 -n 4 -w 1200 -f -l 1200 10.77.0.1'
        if ($mode -eq 'Full') {
            Invoke-Measurement 'Full-internet-ping' $pingPath '-4 -n 3 -w 1200 1.1.1.1'
            Invoke-Measurement 'Full-https' $curlPath '-q --ipv4 --noproxy "*" --connect-timeout 8 --max-time 15 --silent --show-error --verbose https://1.1.1.1/cdn-cgi/trace'
        }
    } finally {
        if ($started) {
            & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $bundlePath
            Write-Report ('Stopped ' + $mode + ' UTC: ' + [DateTime]::UtcNow.ToString('o'))
            $log = Join-Path $bundlePath 'transport.stderr.log'
            if (Test-Path -LiteralPath $log) {
                Copy-Item -LiteralPath $log -Destination (Join-Path $bundlePath ($prefix + '-' + $mode + '-transport.log'))
                Write-Report ((Get-Content -LiteralPath $log -Tail 8) -join "`n")
            }
        }
    }
}
Write-Report ('Completed UTC: ' + [DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved comparison: ' + $reportPath)
Write-Host 'CWG is stopped. You can reconnect your usual VPN and report that the comparison is done.'
