#Requires -RunAsAdministrator
# User-run only. No WireGuard service, routing or DNS changes are made.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle,[string]$Pattern)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
$executable=Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe'
$configPath=Join-Path $bundlePath 'client.json'
if (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue) { throw 'Stop CWG first; this measurement owns its outer session.' }
foreach ($name in @('cwg-test','cwg-full')) {
    $service=Get-Service -Name ('WireGuardTunnel$'+$name) -ErrorAction SilentlyContinue
    if ($service) { $service.Dispose(); throw 'Stop the existing CWG tunnel before measuring.' }
}
$cfg=Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$parts=$cfg.endpoint.Split(':')
if ($parts.Count -ne 2) { throw 'This helper requires an IPv4 endpoint.' }
$route=Find-NetRoute -RemoteIPAddress $parts[0] | Where-Object { $_.CimClass.CimClassName -eq 'MSFT_NetRoute' } | Select-Object -First 1
$adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $route -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs first; the server must route over your physical adapter.' }
if ($Pattern) { $patternPath=(Resolve-Path -LiteralPath $Pattern).Path } else {
    $latest=Get-ChildItem -LiteralPath $bundlePath -Filter 'packets-full-*.jsonl' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $latest) { throw 'No Full-mode packet trace found. Supply -Pattern with the previous trace path.' }
    $patternPath=$latest.FullName
}
$stamp=[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$report=Join-Path $bundlePath ('pattern-replay-'+$stamp+'.txt')
function Write-Report([string]$Text) { Add-Content -LiteralPath $report -Value $Text -Encoding UTF8; Write-Host $Text }
Write-Report ('UDP pattern measurement UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Report ('Source timing/lengths: '+$patternPath)
Write-Report ('Endpoint: '+$cfg.endpoint+'; physical adapter: '+$adapter.Name)
Write-Report 'Measuring recorded bursts, then the same packet lengths evenly spaced at the same average rate.'
Write-Report 'No VPN or routes are started. Captured packet contents are never replayed. Allow about two minutes.'
Write-Report 'The server must be prepared with -probe-rate-limit 200; the ordinary 20/s allowance would invalidate this comparison.'
foreach ($style in @('recorded','paced')) {
    $stdout=Join-Path $bundlePath ('pattern-replay-'+$stamp+'-'+$style+'.json')
    $stderr=Join-Path $bundlePath ('pattern-replay-'+$stamp+'-'+$style+'.stderr.log')
    Write-Report ($style+' start UTC: '+[DateTime]::UtcNow.ToString('o'))
    $arguments=@('replay-probe','-config',('"'+$configPath+'"'),'-pattern',('"'+$patternPath+'"'),'-style',$style)
    $process=Start-Process -FilePath $executable -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        $raw=Get-Content -LiteralPath $stdout -Raw
        if ($raw) { $result=$raw | ConvertFrom-Json; Write-Report (($result | Select-Object style,sent,received,loss_percent,median_ms,p95_ms,rejected,late,send_duration_ms,max_schedule_lag_ms | Format-List | Out-String).TrimEnd()); Write-Report ($result.socket_stats | ConvertTo-Json -Compress) }
        $errorText=Get-Content -LiteralPath $stderr -Raw
        if ($errorText) { Write-Report $errorText.TrimEnd() }
        if ($process.ExitCode -ne 0 -or -not $raw) { throw 'Measurement failed; inspect the saved report and stderr.' }
    } finally { $process.Dispose() }
}
Write-Report ('Completed UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved report: '+$report)
