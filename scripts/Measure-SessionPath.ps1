#Requires -RunAsAdministrator
# User-run rate comparison without a WireGuard tunnel or routing/DNS changes.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$executable = Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe'
$configPath = Join-Path $bundlePath 'client.json'
if (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue) { throw 'Stop CWG first: this diagnostic temporarily owns its server session.' }
$cfg = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$endpointParts = $cfg.endpoint.Split(':')
if ($endpointParts.Count -ne 2) { throw 'This helper requires an IPv4 endpoint.' }
$route = Find-NetRoute -RemoteIPAddress $endpointParts[0] | Where-Object { $_.CimClass.CimClassName -eq 'MSFT_NetRoute' } | Select-Object -First 1
$adapter = Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $route -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs first; the endpoint must route through the physical network adapter.' }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')
$reportPath = Join-Path $bundlePath ('session-path-' + $stamp + '.txt')
$stdout = Join-Path $bundlePath ('session-path-' + $stamp + '.json')
$stderr = Join-Path $bundlePath ('session-path-' + $stamp + '.stderr.log')
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
Write-Report ('Session-rate measurement (UTC): ' + [DateTime]::UtcNow.ToString('o'))
Write-Report ('Endpoint: ' + $cfg.endpoint + '; physical adapter: ' + $adapter.Name)
Write-Report 'Measuring 1, 5, 10 and 20 packets/s, eight seconds each. Allow about 45 seconds.'
Write-Report 'No WireGuard tunnel is created. The rates stay within the existing server response limit.'
$command = Start-Process -FilePath $executable -ArgumentList ('session-probe -config "' + $configPath + '"') -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
try {
    Write-Report ('Process exit code: ' + $command.ExitCode)
    $raw = Get-Content -LiteralPath $stdout -Raw
    if ($raw) {
        $result = $raw | ConvertFrom-Json
        Write-Report $result.meaning
        if ($result.stages) {
            Write-Report (($result.stages | Select-Object packets_per_second,sent,received,loss_percent,median_ms,p95_ms,rejected,late | Format-Table -AutoSize | Out-String).TrimEnd())
        }
        Write-Report ($result.socket_stats | ConvertTo-Json -Compress)
    }
    $errorText = Get-Content -LiteralPath $stderr -Raw
    if ($errorText) { Write-Report $errorText.TrimEnd() }
    if (-not $raw) { throw 'No JSON result; inspect the saved stderr log.' }
} finally { $command.Dispose() }
Write-Report ('Completed (UTC): ' + [DateTime]::UtcNow.ToString('o'))
Write-Host "Saved report: $reportPath"
