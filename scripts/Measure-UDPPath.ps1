#Requires -RunAsAdministrator
# User-run, low-rate size comparison. No VPN, route, or DNS setting is changed.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$executable = Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe'
$configPath = Join-Path $bundlePath 'client.json'
if (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue) { throw 'Stop the CWG tunnel/transport before this isolated path measurement.' }
$cfg = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$endpointParts = $cfg.endpoint.Split(':')
if ($endpointParts.Count -ne 2) { throw 'This measurement helper requires an IPv4 endpoint.' }
$route = Find-NetRoute -RemoteIPAddress $endpointParts[0] | Where-Object { $_.CimClass.CimClassName -eq 'MSFT_NetRoute' } | Select-Object -First 1
$adapter = Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $route -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs first; the CWG server must route through your physical network adapter.' }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')
$reportPath = Join-Path $bundlePath ('udp-path-' + $stamp + '.txt')
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
Write-Report ('Low-rate UDP path measurement (UTC): ' + [DateTime]::UtcNow.ToString('o'))
Write-Report ('Endpoint: ' + $cfg.endpoint + '; physical adapter: ' + $adapter.Name)
Write-Report 'No VPN connection is started. Authenticated probes are spaced 650-1100 ms apart.'
$cases = @(
    @{Name='control';Count=8;Sizes='256,512,1024,1440'},
    @{Name='adjacent-sizes';Count=32;Sizes='256,257,258,259,512,513,514,515,1024,1025,1026,1027,1436,1437,1438,1439'}
)
foreach ($case in $cases) {
    Write-Report ("`nRunning " + $case.Name + ' (' + $case.Count + ' probes); allow up to two minutes...')
    $stdout = Join-Path $bundlePath ('udp-path-' + $stamp + '-' + $case.Name + '.json')
    $stderr = Join-Path $bundlePath ('udp-path-' + $stamp + '-' + $case.Name + '.stderr.log')
    $arguments = 'probe -config "' + $configPath + '" -count ' + $case.Count + ' -sizes ' + $case.Sizes
    $command = Start-Process -FilePath $executable -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        Write-Report ('Process exit code: ' + $command.ExitCode)
        $raw = Get-Content -LiteralPath $stdout -Raw
        if ($raw) {
            $result = $raw | ConvertFrom-Json
            Write-Report ('Sent=' + $result.sent + ' Received=' + $result.received + ' Loss=' + $result.loss_percent + '% Median=' + $result.median_ms + ' ms')
            $rows = $result.samples | Group-Object udp_payload_bytes | ForEach-Object {
                [pscustomobject]@{Bytes=[int]$_.Name;Sent=$_.Count;Received=@($_.Group | Where-Object received).Count}
            } | Sort-Object Bytes
            Write-Report (($rows | Format-Table -AutoSize | Out-String).TrimEnd())
        }
        $errorText = Get-Content -LiteralPath $stderr -Raw
        if ($errorText) { Write-Report $errorText.TrimEnd() }
        if (-not $raw) { throw 'Probe produced no JSON result; inspect the saved stderr log.' }
    } finally { $command.Dispose() }
}
Write-Report ('Completed (UTC): ' + [DateTime]::UtcNow.ToString('o'))
Write-Host "Saved report: $reportPath"
