#Requires -RunAsAdministrator
# User-run only: bounded outer preflights and passive packet capture. No tunnel,
# routing, DNS, adapter binding, or WireGuard service changes.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle, [Parameter(Mandatory)][string]$RawBundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
$rawPath=(Resolve-Path -LiteralPath $RawBundle).Path
$executable=(Resolve-Path -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe')).Path
$pktmon=(Get-Command pktmon.exe -ErrorAction Stop).Source
$cfg=Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
$raw=Get-Content -LiteralPath (Join-Path $rawPath 'client.json') -Raw | ConvertFrom-Json
if ($cfg.transport -ne 'h3' -or $raw.transport -eq 'h3') {throw 'Use an H3 primary bundle and a raw comparison bundle.'}
$parts=$cfg.endpoint.Split(':'); $rawParts=$raw.endpoint.Split(':')
if ($parts.Count -ne 2 -or $rawParts.Count -ne 2 -or $parts[0] -ne $rawParts[0]) {throw 'Both endpoints must use the same literal IPv4 server.'}
$serverIP=[Net.IPAddress]::Parse($parts[0])
if ($serverIP.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {throw 'IPv4 endpoints are required.'}
foreach ($port in @($parts[1],$rawParts[1])) {if ([int]$port -lt 1 -or [int]$port -gt 65535) {throw 'Invalid endpoint port.'}}
if ($parts[1] -eq $rawParts[1]) {throw 'The comparison requires separate H3 and raw server ports.'}
if ((Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) -or (Test-Path -LiteralPath (Join-Path $rawPath 'client-state.json')) -or (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue)) {throw 'Stop CWG before running this preflight-only comparison.'}
$route=Find-NetRoute -RemoteIPAddress $serverIP.IPAddressToString | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
if (-not $route) {throw 'Cannot find the route to the server.'}
$adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $adapter.HardwareInterface) {throw 'Disconnect other VPNs first; the server must route through your physical adapter.'}
$status=(& $pktmon status 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0 -or $status -notmatch 'Packet Monitor is not running') {throw 'Packet Monitor is running or its state is unknown; existing captures will not be changed.'}
$filters=(& $pktmon filter list 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0 -or $filters -notmatch '(?m)^\s*None\s*$') {throw 'Packet Monitor has existing filters; they will not be changed.'}
$stamp=[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$prefix=Join-Path $bundlePath ('quic-path-'+$stamp)
$report=$prefix+'.report.txt'; $etl=$prefix+'.etl'; $filterName='Q-'+$stamp # short enough for pktmon's name column
$filterAdded=$false; $captureStarted=$false; $captureStopped=$false
function Write-Report([string]$Text) {Add-Content -LiteralPath $report -Value $Text -Encoding UTF8; Write-Host $Text}
function Invoke-PacketMonitor([string[]]$Arguments) {
    $output=& $pktmon @Arguments 2>&1; $code=$LASTEXITCODE
    Write-Report (([DateTime]::UtcNow.ToString('o'))+' pktmon '+($Arguments -join ' ')+"`r`n"+($output | Out-String).TrimEnd())
    if ($code -ne 0) {throw ('Packet Monitor failed with exit code '+$code)}
}
function Invoke-Preflight([string]$Name,[string]$Folder,[int]$Count) {
    Write-Report ("`n"+([DateTime]::UtcNow.ToString('o'))+' BEGIN '+$Name)
    $stdout=$prefix+'-'+$Name+'.stdout.log'; $stderr=$prefix+'-'+$Name+'.stderr.log'
    $arguments=@('probe','-config',('"'+(Join-Path $Folder 'client.json')+'"'),'-count',[string]$Count)
    $process=Start-Process -FilePath $executable -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        $null=$process.Handle
        if (-not $process.WaitForExit(30000)) {
            # Only this freshly created child process is owned by the helper.
            $process.Kill(); $process.WaitForExit()
            Write-Report 'Preflight exceeded 30 seconds; owned child stopped.'
        }
        $process.WaitForExit()
        if ($null -eq $process.ExitCode) {Write-Report 'Exit code unavailable; inspect output (not a success indication).'} else {Write-Report ('Exit code: '+$process.ExitCode)}
        foreach ($path in @($stdout,$stderr)) {
            $text=Get-Content -LiteralPath $path -Raw -ErrorAction SilentlyContinue
            if ($text) {Write-Report $text.TrimEnd()}
        }
    } finally {
        if (-not $process.HasExited) {$process.Kill(); $process.WaitForExit()}
        $process.Dispose()
    }
    Write-Report (([DateTime]::UtcNow.ToString('o'))+' END '+$Name)
}
Write-Report ('Outer preflight comparison; UTC '+[DateTime]::UtcNow.ToString('o'))
Write-Report ('Physical adapter '+$adapter.Name+'; H3 '+$cfg.endpoint+'; raw '+$raw.endpoint)
Write-Report 'No VPN tunnel is started. Capture is limited to UDP to/from this server, capped at 16 MB. Treat the resulting files as private.'
try {
    # One endpoint-scoped filter includes both comparison ports. No other hosts
    # are captured, and no existing global filter is replaced.
    Invoke-PacketMonitor -Arguments @('filter','add',$filterName,'-i',$serverIP.IPAddressToString,'-t','UDP')
    $filterAdded=$true
    Invoke-PacketMonitor -Arguments @('start','--capture','--comp','all','--type','all','--pkt-size','0','--file-name',$etl,'--file-size','16','--log-mode','circular')
    $captureStarted=$true
    Invoke-Preflight '01-h3' $bundlePath 2
    Invoke-Preflight '02-raw' $rawPath 4
    Invoke-Preflight '03-h3' $bundlePath 2
} finally {
    try {
        if ($captureStarted) {Invoke-PacketMonitor -Arguments @('stop'); $captureStopped=$true}
    } finally {
        if ($filterAdded) {
            $current=(& $pktmon filter list 2>&1 | Out-String)
            $listed=@($current -split "`r?`n" | Where-Object {$_ -match '^\s*\d+\s+'})
            if ($LASTEXITCODE -eq 0 -and $listed.Count -eq 1 -and $listed[0].Contains($filterName)) {
                Invoke-PacketMonitor -Arguments @('filter','remove')
            } else {Write-Warning ('Filters changed; left them untouched. Our filter: '+$filterName)}
        }
    }
    if ($captureStopped -and (Test-Path -LiteralPath $etl)) {
        Invoke-PacketMonitor -Arguments @('etl2pcap',$etl,'--out',($prefix+'.pcapng'))
        Invoke-PacketMonitor -Arguments @('etl2txt',$etl,'--out',($prefix+'.txt'),'--brief')
        if ((Get-Item -LiteralPath ($prefix+'.pcapng')).Length -le 156) {Write-Report 'WARNING: packet capture is empty; it cannot establish that no packets left this PC.'}
    }
}
Write-Report ('Completed UTC '+[DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved '+$report+'. Reconnect your usual VPN and report done. No VPN tunnel was started.')
