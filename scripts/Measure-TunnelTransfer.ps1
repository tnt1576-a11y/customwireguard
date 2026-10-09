#Requires -RunAsAdministrator
# User-run only: tests real WireGuard data in split mode, then stops in finally.
# No public TCP transport or fallback: application HTTP stays inside CWG's UDP VPN.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
$cfg=Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
if (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) { throw 'Stop CWG before this transfer test.' }
$parts=$cfg.endpoint.Split(':')
if ($parts.Count -ne 2) { throw 'This helper requires an IPv4 endpoint.' }
$route=Find-NetRoute -RemoteIPAddress $parts[0] | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
$adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $route -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs; the server must use a physical adapter.' }
$stamp=[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$prefix='tunnel-transfer-' + $stamp
$reportPath=Join-Path $bundlePath ($prefix + '.txt')
$curl=(Get-Command curl.exe -ErrorAction Stop).Source
$ping=(Get-Command ping.exe -ErrorAction Stop).Source
$payloadPath=Join-Path $bundlePath ($prefix + '-upload.bin')
$payload=New-Object byte[] (512*1024)
for ($offset=0; $offset -lt $payload.Length; $offset++) { $payload[$offset]=[byte]($offset % 256) }
[IO.File]::WriteAllBytes($payloadPath,$payload)
$expectedHash=(Get-FileHash -LiteralPath $payloadPath -Algorithm SHA256).Hash.ToLowerInvariant()
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Run-Command([string]$Name,[string]$Executable,[string]$Arguments) {
    Write-Report ("`n" + $Name + ' start UTC: ' + [DateTime]::UtcNow.ToString('o'))
    $stdout=Join-Path $bundlePath ($prefix + '-' + $Name + '.stdout.log')
    $stderr=Join-Path $bundlePath ($prefix + '-' + $Name + '.stderr.log')
    $process=Start-Process -FilePath $Executable -ArgumentList $Arguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        $code=$process.ExitCode
        Write-Report ('Exit code: ' + $code + '; end UTC: ' + [DateTime]::UtcNow.ToString('o'))
        foreach ($path in @($stdout,$stderr)) {
            $text=Get-Content -LiteralPath $path -Raw
            if ($text) { Write-Report $text.TrimEnd() }
        }
        return $code
    } finally { $process.Dispose() }
}
Write-Report ('CWG split-tunnel transfer UTC: ' + [DateTime]::UtcNow.ToString('o'))
Write-Report ('Endpoint: ' + $cfg.endpoint + '; physical adapter: ' + $adapter.Name)
Write-Report 'This starts Test mode only. Normal internet/background traffic stays outside CWG.'
Write-Report 'Synthetic 512 KiB download (16 KiB/s), download (unpaced), and upload; all inside the UDP VPN.'
Write-Report 'Allow about two minutes. CWG stops automatically. The private server diagnostic must already be running.'
$started=$false
try {
    & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $bundlePath -Mode Test -TracePackets
    $started=$true
    $trace=Get-ChildItem -LiteralPath $bundlePath -Filter 'packets-test-*.jsonl' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if ($trace) { Write-Report ('Packet trace: ' + $trace.Name) }
    $null=Run-Command 'before-ping' $ping '-4 -n 4 -w 1200 10.77.0.1'
    foreach ($name in @('download-paced','download','upload')) {
        $outputPath=Join-Path $bundlePath ($prefix + '-' + $name + '.body')
        $timeout=if ($name -eq 'download-paced') {50} else {25}
        $arguments='-q --ipv4 --noproxy "*" --connect-timeout 8 --max-time ' + $timeout + ' --fail --silent --show-error --output "' + $outputPath + '" --write-out "http_code=%{http_code} download=%{size_download} upload=%{size_upload} elapsed=%{time_total}\n"'
        if ($name -eq 'upload') { $arguments+=' --header "Expect:" --header "Content-Type: application/octet-stream" --data-binary "@' + $payloadPath + '"' }
        $arguments+=' "http://10.77.0.1:18080/' + $name + '"'
        $code=Run-Command $name $curl $arguments
        $verified=$false
        if ($code -eq 0 -and (Test-Path -LiteralPath $outputPath)) {
            if ($name -eq 'upload') {
                try {
                    $result=Get-Content -LiteralPath $outputPath -Raw | ConvertFrom-Json
                    $verified=$result.received_bytes -eq $payload.Length -and $result.sha256 -eq $expectedHash
                } catch { Write-Report 'Invalid upload verification response.' }
            } else {
                $verified=(Get-Item -LiteralPath $outputPath).Length -eq $payload.Length -and (Get-FileHash -LiteralPath $outputPath -Algorithm SHA256).Hash.ToLowerInvariant() -eq $expectedHash
            }
        }
        Write-Report ('Verified complete payload: ' + $verified)
    }
    $null=Run-Command 'after-ping' $ping '-4 -n 4 -w 1200 10.77.0.1'
} finally {
    if ($started) {
        & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $bundlePath
        Write-Report ('Stopped Test mode UTC: ' + [DateTime]::UtcNow.ToString('o'))
        $log=Join-Path $bundlePath 'transport.stderr.log'
        if (Test-Path -LiteralPath $log) { Copy-Item -LiteralPath $log -Destination (Join-Path $bundlePath ($prefix + '-transport.log')) }
    }
}
Write-Report ('Completed UTC: ' + [DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved report: ' + $reportPath)
Write-Host 'CWG is stopped. Reconnect your usual VPN and report done.'
