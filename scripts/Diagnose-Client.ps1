#Requires -RunAsAdministrator
# User-invoked diagnostics for an ALREADY running Full tunnel. This script does
# not start/stop a VPN, change routes, change DNS settings, or read private keys.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$service = Get-Service -Name 'WireGuardTunnel$cwg-full' -ErrorAction SilentlyContinue
try {
    if (-not $service -or $service.Status -ne 'Running') { throw 'Start CWG Full mode yourself before running this diagnostic.' }
} finally { if ($service) { $service.Dispose() } }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')
$reportPath = Join-Path $bundlePath ('diagnostic-' + $stamp + '.txt')
$curlPath = (Get-Command curl.exe -ErrorAction Stop).Source
$pingPath = (Get-Command ping.exe -ErrorAction Stop).Source
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Invoke-DiagnosticCommand([string]$Name,[string]$Executable,[string]$Arguments) {
    Write-Report ("`n--- " + $Name + ' ---')
    $stdout = Join-Path $bundlePath ('diagnostic-' + $stamp + '-' + $Name + '.stdout.log')
    $stderr = Join-Path $bundlePath ('diagnostic-' + $stamp + '-' + $Name + '.stderr.log')
    $command = Start-Process -FilePath $Executable -ArgumentList $Arguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        Write-Report ('Exit code: ' + $command.ExitCode)
        foreach ($path in @($stdout,$stderr)) {
            $text = Get-Content -LiteralPath $path -Raw -ErrorAction SilentlyContinue
            if ($text) { Write-Report $text.TrimEnd() }
        }
    } finally { $command.Dispose() }
}
Write-Report ('CWG diagnostic started (UTC): ' + [DateTime]::UtcNow.ToString('o'))
Write-Report 'User-invoked tests only. Existing tunnel/routes/DNS settings are not changed.'
Write-Report "`n--- Routes ---"
$routes = Get-NetRoute -AddressFamily IPv4 | Where-Object {
    $_.InterfaceAlias -eq 'cwg-full' -or $_.DestinationPrefix -in @('0.0.0.0/0','0.0.0.0/1','128.0.0.0/1') -or
    ($_.DestinationPrefix -match '/32$' -and $_.RouteMetric -eq 3)
}
Write-Report (($routes | Select-Object DestinationPrefix,NextHop,InterfaceAlias,RouteMetric | Format-Table -AutoSize | Out-String).TrimEnd())
Write-Report ((Get-NetIPInterface -InterfaceAlias 'cwg-full' | Select-Object AddressFamily,NlMtu,InterfaceMetric | Format-Table -AutoSize | Out-String).TrimEnd())
Write-Report ((Get-DnsClientServerAddress -InterfaceAlias 'cwg-full' | Select-Object AddressFamily,ServerAddresses | Format-Table -AutoSize | Out-String).TrimEnd())

Invoke-DiagnosticCommand 'tunnel-small-ping' $pingPath '-4 -n 4 -w 1500 -l 32 10.77.0.1'
Invoke-DiagnosticCommand 'tunnel-large-ping' $pingPath '-4 -n 4 -w 1500 -f -l 1200 10.77.0.1'
Invoke-DiagnosticCommand 'internet-ping' $pingPath '-4 -n 3 -w 1500 1.1.1.1'
foreach ($dnsServer in @('System','1.1.1.1')) {
    Write-Report ("`n--- DNS via " + $dnsServer + ' ---')
    try {
        $query = @{Name='api.ipify.org';Type='A';DnsOnly=$true;QuickTimeout=$true;ErrorAction='Stop'}
        if ($dnsServer -ne 'System') { $query.Server = $dnsServer }
        $answer = Resolve-DnsName @query
        Write-Report (($answer | Select-Object Name,Type,IPAddress,NameHost | Format-Table -AutoSize | Out-String).TrimEnd())
    } catch { Write-Report ('DNS error: ' + $_.Exception.Message) }
}
# -q disables curlrc; --noproxy bypasses any proxy environment setting. The
# numeric-IP HTTPS request separates name resolution from TCP/TLS delivery.
$common = '-q --ipv4 --noproxy "*" --connect-timeout 6 --max-time 15 --silent --show-error --verbose'
Invoke-DiagnosticCommand 'https-direct-ip' $curlPath ($common + ' --output NUL https://1.1.1.1/cdn-cgi/trace')
Invoke-DiagnosticCommand 'https-ipify' $curlPath ($common + ' https://api.ipify.org')
Invoke-DiagnosticCommand 'https-example' $curlPath ($common + ' --output NUL https://example.com/')
Write-Report "`n--- Recent CWG counters ---"
$transportLog = Join-Path $bundlePath 'transport.stderr.log'
if (Test-Path -LiteralPath $transportLog) { Write-Report ((Get-Content -LiteralPath $transportLog -Tail 8) -join "`n") }
Write-Report ('Completed (UTC): ' + [DateTime]::UtcNow.ToString('o'))
Write-Host "`nSaved diagnostic report: $reportPath"
Write-Host 'The tunnel is still running. Stop it yourself using Stop-Client.ps1 when finished.'
