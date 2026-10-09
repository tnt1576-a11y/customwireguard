#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Bundle,
    [ValidateSet('Test','Full')][string]$Mode = 'Test',
    [string]$Executable,
    [switch]$TracePackets
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'WireGuard-Service.ps1')
if (-not $Executable) { $Executable = Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe' }
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$executablePath = (Resolve-Path -LiteralPath $Executable).Path
$configPath = Join-Path $bundlePath 'client.json'
$statePath = Join-Path $bundlePath 'client-state.json'
$wireguardExe = Join-Path $env:ProgramFiles 'WireGuard\wireguard.exe'
if (-not (Test-Path -LiteralPath $wireguardExe)) { throw 'Install the official WireGuard Windows client first.' }
if (Test-Path -LiteralPath $statePath) { throw 'This bundle has saved client state. Run Stop-Client.ps1 on it before starting again.' }
& $executablePath check -config $configPath
if ($LASTEXITCODE -ne 0) { throw 'Invalid client configuration.' }
$cfg = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$tunnelName = if ($Mode -eq 'Full') { 'cwg-full' } else { 'cwg-test' }
$tunnelPath = Join-Path $bundlePath ($tunnelName + '.conf')
if (-not (Test-Path -LiteralPath $tunnelPath)) { throw 'Missing WireGuard configuration.' }
if ($cfg.transport -eq 'h3') {
    $nativeText = Get-Content -LiteralPath $tunnelPath -Raw
    if ($nativeText -notmatch '(?m)^\s*MTU\s*=\s*1088\s*$' -or $nativeText -match '(?m)^\s*(Address|AllowedIPs)\s*=.*:') {
        throw 'HTTP/3 comparison requires MTU = 1088 and IPv4-only Address/AllowedIPs in the native WireGuard profile.'
    }
    Write-Host 'Transport: HTTP/3 CONNECT-UDP with pinned TLS. The preflight verifies HTTP/3 access only.'
}
foreach ($name in @('cwg-test','cwg-full')) {
    if (Get-Service -Name ('WireGuardTunnel$' + $name) -ErrorAction SilentlyContinue) { throw 'A CWG tunnel service already exists. Stop its original bundle first.' }
}
if (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue) { throw 'Local port 51821 or 51822 is in use. Stop the existing tunnel/transport first.' }
& $executablePath probe -config $configPath -count 4
if ($LASTEXITCODE -ne 0) { throw 'No authenticated server reply. No routes or tunnel services were changed.' }

$state = [ordered]@{ Version=1; Executable=$executablePath; ProcessId=0; ProcessStartUTC=''; Tunnel=$tunnelName; TunnelInstalled=$false; AddedRoute=$false; RoutePrefix=''; InterfaceIndex=0; NextHop=''; TransportInterfaceIndex=0 }
# Reserve the state file exclusively to prevent simultaneous starts of this bundle.
$reservation = [System.IO.File]::Open($statePath,[System.IO.FileMode]::CreateNew,[System.IO.FileAccess]::Write,[System.IO.FileShare]::None)
$reservation.Dispose()
function Save-State { $state | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8 }
Save-State
try {
    if ($Mode -eq 'Full') {
        $endpointParts = $cfg.endpoint.Split(':')
        if ($endpointParts.Count -ne 2) { throw 'Full-mode helper currently requires a server IPv4 address.' }
        $serverIP = [System.Net.IPAddress]::Parse($endpointParts[0])
        if ($serverIP.AddressFamily -ne [System.Net.Sockets.AddressFamily]::InterNetwork) { throw 'Full-mode helper currently requires a server IPv4 address.' }
        if ([System.Net.IPAddress]::IsLoopback($serverIP)) { throw 'Full mode needs a remote server, not a loopback endpoint.' }
        $route = Find-NetRoute -RemoteIPAddress $serverIP.IPAddressToString |
            Where-Object { $_.CimClass.CimClassName -eq 'MSFT_NetRoute' } | Select-Object -First 1
        if (-not $route) { throw 'Cannot determine the existing physical route to the server.' }
        $adapter = Get-NetAdapter -ErrorAction Stop | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
        if (-not $adapter.HardwareInterface) { throw 'The server currently routes through a virtual adapter. Disconnect other VPNs before Full mode.' }
        # An IPv4-only profile is useful on IPv4-only hosts, but must not silently
        # leave a working IPv6 default path outside the tunnel. Inspect only;
        # never disable a network binding on the user's behalf.
        $allowedPrefixes = @(foreach ($line in Get-Content -LiteralPath $tunnelPath) {
            if ($line -match '^\s*AllowedIPs\s*=\s*([^#]+)') {
                $Matches[1].Split(',') | ForEach-Object { $_.Trim() }
            }
        })
        $coversIPv6 = ('::/0' -in $allowedPrefixes) -or
            (('::/1' -in $allowedPrefixes) -and ('8000::/1' -in $allowedPrefixes))
        if (-not $coversIPv6) {
            $binding = @(Get-NetAdapterBinding -Name $adapter.Name -ComponentID ms_tcpip6 -ErrorAction Stop)
            if ($binding.Count -ne 1 -or $binding[0].Enabled -ne $false) {
                throw 'This Full profile does not route public IPv6. It requires IPv6 already disabled on the physical adapter; no adapter settings were changed.'
            }
            $ipv6Defaults = @(Get-NetRoute -AddressFamily IPv6 -PolicyStore ActiveStore -ErrorAction Stop |
                Where-Object DestinationPrefix -eq '::/0')
            if ($ipv6Defaults.Count) { throw 'This Full profile does not route public IPv6, but Windows has an IPv6 default route. Startup stopped to avoid an IPv6 bypass.' }
        }
        $state.TransportInterfaceIndex = [int]$route.InterfaceIndex
        Save-State
        $prefix = $serverIP.IPAddressToString + '/32'
        $existing = Get-NetRoute -DestinationPrefix $prefix -PolicyStore ActiveStore -ErrorAction SilentlyContinue
        if (-not $existing) {
            $state.RoutePrefix=$prefix; $state.InterfaceIndex=$route.InterfaceIndex; $state.NextHop=$route.NextHop
            # Record intent first so interrupted setup can be undone.
            $state.AddedRoute=$true; Save-State
            New-NetRoute -DestinationPrefix $prefix -InterfaceIndex $route.InterfaceIndex -NextHop $route.NextHop -RouteMetric 3 -PolicyStore ActiveStore | Out-Null
        }
        Write-Host 'Full mode requires Linux forwarding/NAT. This helper does not provide a firewall kill switch after the tunnel is removed.'
    }
    $stdout = Join-Path $bundlePath 'transport.stdout.log'
    $stderr = Join-Path $bundlePath 'transport.stderr.log'
    $clientArguments = @('client','-config',('"' + $configPath + '"'))
    if ($Mode -eq 'Full') {
        $clientArguments += @('-interface-index',[string]$state.TransportInterfaceIndex)
        Write-Host ('Outer UDP transport will use physical adapter ' + $adapter.Name + ' (index ' + $state.TransportInterfaceIndex + ').')
    }
    if ($TracePackets) {
        $tracePath = Join-Path $bundlePath ('packets-' + $Mode.ToLowerInvariant() + '-' + [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff') + '.jsonl')
        $clientArguments += @('-trace',('"' + $tracePath + '"'))
        Write-Host ('Encrypted packet metadata will be saved to ' + $tracePath)
    }
    $process = Start-Process -FilePath $executablePath -ArgumentList $clientArguments -WorkingDirectory $bundlePath -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
    $state.ProcessId=$process.Id; $state.ProcessStartUTC=$process.StartTime.ToUniversalTime().ToString('o'); Save-State
    Start-Sleep -Milliseconds 750
    if ($process.HasExited) { throw 'The transport stopped; inspect transport.stderr.log in the private bundle.' }
    $state.TunnelInstalled=$true; Save-State
    Invoke-CwgWireGuardCommand -Executable $wireguardExe -Action Install -Target $tunnelPath -LogDirectory $bundlePath
    Wait-CwgWireGuardService -Tunnel $tunnelName -State Running
    $process.Refresh()
    if ($process.HasExited) { throw 'The transport stopped during WireGuard startup; inspect transport.stderr.log.' }
    Write-Host "CWG $Mode mode started. Test with: ping 10.77.0.1"
    Write-Host "Stop with: .\scripts\Stop-Client.ps1 -Bundle `"$bundlePath`""
} catch {
    $problem = $_
    try { & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $bundlePath } catch { Write-Warning 'Automatic cleanup was incomplete. Keep client-state.json and run Stop-Client.ps1 again.' }
    throw $problem
}
