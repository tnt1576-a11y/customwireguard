#Requires -RunAsAdministrator
# User-run only. Tests server internet forwarding through a private diagnostic
# bundle with one public /32 route. Leaves the original configurations intact.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
if (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) { throw 'Stop CWG before this test.' }
$configPath=Join-Path $bundlePath 'client.json'
$cfg=Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$endpointParts=$cfg.endpoint.Split(':')
if ($endpointParts.Count -ne 2) { throw 'This helper requires an IPv4 endpoint.' }
$serverIP=$endpointParts[0]
function Selected-Route([string]$Destination) {
    Find-NetRoute -RemoteIPAddress $Destination | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
}
foreach ($destination in @($serverIP,'1.1.1.1','9.9.9.9')) {
    $route=Selected-Route $destination
    $adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
    if (-not $route -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs; test destinations must initially use a physical adapter.' }
}
$original=Get-Content -LiteralPath (Join-Path $bundlePath 'cwg-test.conf') -Raw
if ([regex]::Matches($original,'(?im)^[\t ]*AllowedIPs[\t ]*=').Count -ne 1) { throw 'Expected exactly one WireGuard peer AllowedIPs entry.' }
if ($original -match '(?im)^[\t ]*DNS[\t ]*=') { throw 'The base Test configuration must not change system DNS.' }
$derived=[regex]::Replace($original,'(?im)^[\t ]*AllowedIPs[\t ]*=.*$','AllowedIPs = 10.77.0.0/24, fd77:77:77::/64, 1.1.1.1/32')
$prefix='selective-internet-' + [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$diagnosticBundle=Join-Path $bundlePath $prefix
# This directory inherits the private bundle's restricted permissions. It retains
# diagnostic logs and keys for recovery; never include it in a public package.
New-Item -ItemType Directory -Path $diagnosticBundle -ErrorAction Stop | Out-Null
Copy-Item -LiteralPath $configPath -Destination (Join-Path $diagnosticBundle 'client.json')
[IO.File]::WriteAllText((Join-Path $diagnosticBundle 'cwg-test.conf'),$derived,[Text.UTF8Encoding]::new($false))
$reportPath=Join-Path $bundlePath ($prefix + '.txt')
$curl=(Get-Command curl.exe -ErrorAction Stop).Source
$ping=(Get-Command ping.exe -ErrorAction Stop).Source
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Run-Command([string]$Name,[string]$Executable,[string]$Arguments) {
    Write-Report ("`n" + $Name + ' start UTC: ' + [DateTime]::UtcNow.ToString('o'))
    $stdout=Join-Path $diagnosticBundle ($Name + '.stdout.log')
    $stderr=Join-Path $diagnosticBundle ($Name + '.stderr.log')
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
Write-Report ('CWG selective internet test UTC: ' + [DateTime]::UtcNow.ToString('o'))
Write-Report ('Outer endpoint: ' + $cfg.endpoint)
Write-Report ('Private diagnostic bundle: ' + $diagnosticBundle)
Write-Report 'This starts split Test mode with only 1.1.1.1 added to the private tunnel routes.'
Write-Report 'It tests server internet forwarding; other public destinations retain their normal routes. System DNS is unchanged.'
Write-Report 'The same UDP transport and encryption are used. Allow about one minute. CWG stops automatically.'
$started=$false
try {
    & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $diagnosticBundle -Mode Test -TracePackets
    $started=$true
    foreach ($destination in @($serverIP,'1.1.1.1','9.9.9.9')) {
        $route=Selected-Route $destination
        Write-Report ("Route to ${destination}: " + ($route | Select-Object DestinationPrefix,NextHop,InterfaceAlias,InterfaceIndex | ConvertTo-Json -Compress))
        if (-not $route) { throw 'Cannot verify test route.' }
        if ($destination -eq '1.1.1.1') {
            if ($route.InterfaceAlias -ne 'cwg-test' -or $route.DestinationPrefix -ne '1.1.1.1/32') { throw 'Test destination did not select the isolated tunnel route.' }
        } else {
            $adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
            if (-not $adapter.HardwareInterface) { throw 'An unrelated public destination unexpectedly routes through a virtual adapter.' }
        }
    }
    $null=Run-Command 'before-private-ping' $ping '-4 -n 4 -w 1200 10.77.0.1'
    $null=Run-Command 'internet-ping' $ping '-4 -n 3 -w 1200 1.1.1.1'
    $bodyPath=Join-Path $diagnosticBundle 'internet-https.body'
    $arguments='-q --ipv4 --noproxy "*" --connect-timeout 8 --max-time 15 --fail --silent --show-error --verbose --output "' + $bodyPath + '" --write-out "http_code=%{http_code} elapsed=%{time_total}\n" https://1.1.1.1/cdn-cgi/trace'
    $code=Run-Command 'internet-https' $curl $arguments
    if ($code -eq 0 -and (Test-Path -LiteralPath $bodyPath)) {
        $body=Get-Content -LiteralPath $bodyPath -Raw
        $match=[regex]::Match($body,'(?m)^ip=([^\r\n]+)')
        if ($match.Success) {
            Write-Report ('HTTPS observed public IP: ' + $match.Groups[1].Value)
            Write-Report ('Matches configured server IP: ' + ($match.Groups[1].Value -eq $serverIP))
        } else { Write-Report 'HTTPS succeeded but the response did not contain an observed IP.' }
    }
    $null=Run-Command 'after-private-ping' $ping '-4 -n 4 -w 1200 10.77.0.1'
} finally {
    if ($started) {
        & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $diagnosticBundle
        Write-Report ('Stopped isolated Test bundle UTC: ' + [DateTime]::UtcNow.ToString('o'))
    }
}
Write-Report ('Completed UTC: ' + [DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved report: ' + $reportPath)
Write-Host 'CWG is stopped. Reconnect your usual VPN and report done.'
