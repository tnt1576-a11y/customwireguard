#Requires -RunAsAdministrator
# User-run only. Compare routing/settings with identical outer UDP transport.
# Uses private child bundles; no edits to original configs or network filters.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
if (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) { throw 'Stop CWG before this comparison.' }
$cfg=Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
$parts=$cfg.endpoint.Split(':')
if ($parts.Count -ne 2) { throw 'This helper requires an IPv4 endpoint.' }
$serverIP=$parts[0]
function Selected-Route([string]$Destination) {
    Find-NetRoute -RemoteIPAddress $Destination | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
}
$physicalRoute=Selected-Route $serverIP
$adapter=Get-NetAdapter | Where-Object ifIndex -eq $physicalRoute.InterfaceIndex | Select-Object -First 1
if (-not $physicalRoute -or -not $adapter.HardwareInterface) { throw 'Disconnect other VPNs; the server must use a physical adapter.' }
$full=Get-Content -LiteralPath (Join-Path $bundlePath 'cwg-full.conf') -Raw
$split=Get-Content -LiteralPath (Join-Path $bundlePath 'cwg-test.conf') -Raw
foreach ($text in @($full,$split)) {
    foreach ($key in @('Address','AllowedIPs')) {
        if ([regex]::Matches($text,'(?im)^[\t ]*'+$key+'[\t ]*=').Count -ne 1) { throw ('Expected exactly one '+$key+' entry.') }
    }
}
if ([regex]::Matches($full,'(?im)^[\t ]*DNS[\t ]*=').Count -ne 1 -or $split -match '(?im)^[\t ]*DNS[\t ]*=') { throw 'Expected DNS only in the original Full config.' }
$selective=[regex]::Replace($split,'(?im)^[\t ]*AllowedIPs[\t ]*=.*$','AllowedIPs = 10.77.0.0/24, fd77:77:77::/64, 1.1.1.1/32')
$noDns=[regex]::Replace($full,'(?im)^[\t ]*DNS[\t ]*=.*\r?\n?','')
$ipv4=[regex]::Replace($full,'(?im)^[\t ]*AllowedIPs[\t ]*=.*$','AllowedIPs = 0.0.0.0/1, 128.0.0.0/1')
$addressMatch=[regex]::Match($full,'(?im)^[\t ]*Address[\t ]*=[\t ]*([^\r\n]+)')
$v4Addresses=@($addressMatch.Groups[1].Value.Split(',') | ForEach-Object {$_.Trim()} | Where-Object {$_ -notmatch ':'})
if (-not $v4Addresses.Count) { throw 'No IPv4 interface address found.' }
$ipv4=[regex]::Replace($ipv4,'(?im)^[\t ]*Address[\t ]*=.*$',('Address = '+($v4Addresses -join ', ')))
$cases=@(
    @{Name='SelectiveWithFullSetup';Text=$selective},
    @{Name='FullBaseline';Text=$full},
    @{Name='FullNoDNS';Text=$noDns},
    @{Name='FullIPv4Only';Text=$ipv4}
)
$prefix='full-settings-' + [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$parent=Join-Path $bundlePath $prefix
New-Item -ItemType Directory -Path $parent -ErrorAction Stop | Out-Null
$reportPath=Join-Path $bundlePath ($prefix+'.txt')
$curl=(Get-Command curl.exe -ErrorAction Stop).Source
$ping=(Get-Command ping.exe -ErrorAction Stop).Source
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Run-Command([string]$Directory,[string]$Name,[string]$Executable,[string]$Arguments) {
    Write-Report ("`n" + $Name + ' start UTC: ' + [DateTime]::UtcNow.ToString('o'))
    $stdout=Join-Path $Directory ($Name+'.stdout.log')
    $stderr=Join-Path $Directory ($Name+'.stderr.log')
    $process=Start-Process -FilePath $Executable -ArgumentList $Arguments -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        $code=$process.ExitCode
        Write-Report ('Exit code: '+$code+'; end UTC: '+[DateTime]::UtcNow.ToString('o'))
        foreach ($path in @($stdout,$stderr)) {
            $text=Get-Content -LiteralPath $path -Raw
            if ($text) { Write-Report $text.TrimEnd() }
        }
        return $code
    } finally { $process.Dispose() }
}
Write-Report ('CWG Full settings comparison UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Report ('Endpoint: '+$cfg.endpoint+'; physical adapter: '+$adapter.Name)
Write-Report 'Four cases: selective routes with Full startup, ordinary Full, Full without DNS changes, Full IPv4 only.'
Write-Report 'Private diagnostic copies only. FullNoDNS uses the existing system resolver during this diagnostic.'
Write-Report 'IPv4-only runs only when the physical adapter has IPv6 disabled and there is no IPv6 default route.'
Write-Report 'Network filters and original configurations are unchanged. Each tunnel stops automatically. Allow about three minutes.'
foreach ($case in $cases) {
    if ($case.Name -eq 'FullIPv4Only') {
        # A diagnostic must not silently create an IPv6 bypass on a working IPv6 network.
        $binding=Get-NetAdapterBinding -Name $adapter.Name -ComponentID ms_tcpip6 -ErrorAction Stop
        $v6Defaults=@(Get-NetRoute -AddressFamily IPv6 -DestinationPrefix '::/0' -ErrorAction SilentlyContinue)
        if ($null -eq $binding -or $binding.Enabled -or $v6Defaults.Count) {
            Write-Report 'SKIPPED FullIPv4Only: IPv6 may be available outside the tunnel.'
            continue
        }
    }
    $diagnosticBundle=Join-Path $parent $case.Name
    New-Item -ItemType Directory -Path $diagnosticBundle -ErrorAction Stop | Out-Null
    Copy-Item -LiteralPath (Join-Path $bundlePath 'client.json') -Destination (Join-Path $diagnosticBundle 'client.json')
    [IO.File]::WriteAllText((Join-Path $diagnosticBundle 'cwg-full.conf'),$case.Text,[Text.UTF8Encoding]::new($false))
    $started=$false
    try {
        Write-Report ("`nCASE "+$case.Name+' start UTC: '+[DateTime]::UtcNow.ToString('o'))
        Write-Report ('Private diagnostic bundle: '+$diagnosticBundle)
        & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $diagnosticBundle -Mode Full -TracePackets
        $started=$true
        foreach ($destination in @($serverIP,'1.1.1.1','9.9.9.9')) {
            $route=Selected-Route $destination
            Write-Report ("Route to ${destination}: "+($route | Select-Object DestinationPrefix,NextHop,InterfaceAlias,InterfaceIndex | ConvertTo-Json -Compress))
            if (-not $route) { throw 'Cannot verify diagnostic route.' }
            $wantTunnel=$destination -eq '1.1.1.1' -or ($destination -eq '9.9.9.9' -and $case.Name -ne 'SelectiveWithFullSetup')
            if ($wantTunnel -and $route.InterfaceAlias -ne 'cwg-full') { throw 'A test destination did not use cwg-full.' }
            if (-not $wantTunnel -and $route.InterfaceIndex -ne $physicalRoute.InterfaceIndex) { throw 'An intended physical route changed.' }
        }
        Write-Report ('Tunnel DNS: '+(Get-DnsClientServerAddress -InterfaceAlias cwg-full -AddressFamily IPv4 | Select-Object InterfaceAlias,ServerAddresses | ConvertTo-Json -Compress))
        $null=Run-Command $diagnosticBundle 'private-ping' $ping '-4 -n 4 -w 1200 10.77.0.1'
        $bodyPath=Join-Path $diagnosticBundle 'internet-https.body'
        $arguments='-q --ipv4 --noproxy "*" --connect-timeout 8 --max-time 12 --fail --silent --show-error --output "'+$bodyPath+'" --write-out "http_code=%{http_code} elapsed=%{time_total}\n" https://1.1.1.1/cdn-cgi/trace'
        $code=Run-Command $diagnosticBundle 'internet-https' $curl $arguments
        if ($code -eq 0 -and (Test-Path -LiteralPath $bodyPath)) {
            $match=[regex]::Match((Get-Content -LiteralPath $bodyPath -Raw),'(?m)^ip=([^\r\n]+)')
            if ($match.Success) { Write-Report ('HTTPS observed public IP: '+$match.Groups[1].Value+'; matches server: '+($match.Groups[1].Value -eq $serverIP)) }
        }
        $null=Run-Command $diagnosticBundle 'after-private-ping' $ping '-4 -n 2 -w 1200 10.77.0.1'
    } finally {
        if ($started) {
            & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $diagnosticBundle
            Write-Report ('Stopped '+$case.Name+' UTC: '+[DateTime]::UtcNow.ToString('o'))
            $log=Join-Path $diagnosticBundle 'transport.stderr.log'
            if (Test-Path -LiteralPath $log) { Write-Report ((Get-Content -LiteralPath $log -Tail 6) -join "`n") }
        }
    }
}
Write-Report ('Completed UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved comparison: '+$reportPath)
Write-Host 'CWG is stopped. Reconnect your usual VPN and report done.'
