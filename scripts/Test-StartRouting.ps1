# Offline fixtures: no real VPN, routes, services, sockets or probe traffic.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$fixture = Join-Path $root ('test-output\start-routing-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $fixture 'WireGuard') -Force | Out-Null
$source = (Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Start-Client.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
Set-Content -LiteralPath (Join-Path $fixture 'Start-Client.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'WireGuard\wireguard.exe') -Value 'fixture only'
Set-Content -LiteralPath (Join-Path $fixture 'fake-cwg.ps1') -Value '$global:LASTEXITCODE=0'
Set-Content -LiteralPath (Join-Path $fixture 'WireGuard-Service.ps1') -Value 'function Invoke-CwgWireGuardCommand {} function Wait-CwgWireGuardService {}'
Set-Content -LiteralPath (Join-Path $fixture 'Stop-Client.ps1') -Value 'param($Bundle); $global:CwgCleaned=$true'
Set-Content -LiteralPath (Join-Path $fixture 'client.json') -Value '{"endpoint":"203.0.113.1:443"}'
Set-Content -LiteralPath (Join-Path $fixture 'cwg-test.conf') -Value "[Peer]`nAllowedIPs = 10.77.0.0/24, fd77:77:77::/64"
function Get-Service { param($Name,$ErrorAction) }
function Get-NetUDPEndpoint { param($LocalPort,$ErrorAction) }
function Find-NetRoute {
    param($RemoteIPAddress)
    [pscustomobject]@{CimClass=[pscustomobject]@{CimClassName='MSFT_NetRoute'};InterfaceIndex=12;NextHop='192.0.2.1'}
}
function Get-NetAdapter { param($ErrorAction); [pscustomobject]@{ifIndex=12;HardwareInterface=$global:CwgPhysical;Name='Fixture Ethernet'} }
function Get-NetAdapterBinding {
    param($Name,$ComponentID,$ErrorAction)
    if ($global:CwgCase -eq 'Full-IPv6-unknown') { return }
    if ($global:CwgCase -eq 'Full-IPv6-query-error') { throw 'Fixture binding query failed' }
    [pscustomobject]@{Enabled=($global:CwgCase -in @('Full-IPv6-enabled','Full-dual-stack','Test'))}
}
function Get-NetRoute {
    param($DestinationPrefix,$PolicyStore,$ErrorAction,$AddressFamily)
    if ($AddressFamily -eq 'IPv6') {
        if ($global:CwgCase -eq 'Full-IPv6-route-error') { throw 'Fixture route query failed' }
        if ($global:CwgCase -eq 'Full-IPv6-default') { [pscustomobject]@{DestinationPrefix='::/0'} }
        else { [pscustomobject]@{DestinationPrefix='fe80::/64'} }
    } elseif ($global:CwgExisting) { [pscustomobject]@{DestinationPrefix=$DestinationPrefix} }
}
function New-NetRoute { param($DestinationPrefix,$InterfaceIndex,$NextHop,$RouteMetric,$PolicyStore); $global:CwgRouteCreated=$true }
function Start-Sleep { param($Milliseconds) }
function Start-Process {
    param($FilePath,$ArgumentList,$WorkingDirectory,$WindowStyle,$RedirectStandardOutput,$RedirectStandardError,[switch]$PassThru)
    $global:CwgArguments=$ArgumentList
    $result=[pscustomobject]@{Id=123;StartTime=[DateTime]::UtcNow;HasExited=$false}
    $result | Add-Member -MemberType ScriptMethod -Name Refresh -Value {}
    return $result
}
$originalProgramFiles=$env:ProgramFiles
try {
    $env:ProgramFiles=$fixture
    $cases=@('Full-new-route','Full-existing-route','Test','Full-virtual-route','Full-IPv6-enabled','Full-IPv6-default','Full-IPv6-unknown','Full-IPv6-query-error','Full-IPv6-route-error','Full-dual-stack')
    foreach ($case in $cases) {
        $global:CwgCase=$case
        $shouldFail=$case -eq 'Full-virtual-route' -or $case.StartsWith('Full-IPv6-')
        $prefixes=if ($case -eq 'Full-dual-stack') {'0.0.0.0/1, 128.0.0.0/1, ::/1, 8000::/1'} else {'0.0.0.0/1, 128.0.0.0/1'}
        Set-Content -LiteralPath (Join-Path $fixture 'cwg-full.conf') -Value "[Peer]`nAllowedIPs = $prefixes # fixture"
        $global:CwgArguments=@(); $global:CwgRouteCreated=$false; $global:CwgCleaned=$false
        $global:CwgExisting=$case -eq 'Full-existing-route'
        $global:CwgPhysical=$case -ne 'Full-virtual-route'
        $mode=if ($case -eq 'Test') {'Test'} else {'Full'}
        $statePath=Join-Path $fixture 'client-state.json'
        if (Test-Path -LiteralPath $statePath) { Remove-Item -LiteralPath $statePath }
        $failed=$false
        try { & (Join-Path $fixture 'Start-Client.ps1') -Bundle $fixture -Mode $mode -Executable (Join-Path $fixture 'fake-cwg.ps1') | Out-Null } catch {
            if (-not $shouldFail) { throw }
            $failed=$true
        }
        $state=Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
        if ($shouldFail) {
            if (-not $failed -or $global:CwgArguments.Count -or $global:CwgRouteCreated -or -not $global:CwgCleaned) { throw ('Startup guard failed: ' + $case) }
        } elseif ($mode -eq 'Full') {
            $flag=[Array]::IndexOf($global:CwgArguments,'-interface-index')
            if ($flag -lt 0 -or $global:CwgArguments[$flag+1] -ne '12' -or $state.TransportInterfaceIndex -ne 12) { throw 'Physical interface not passed or persisted' }
            $expectedNew=-not $global:CwgExisting
            if ($global:CwgRouteCreated -ne $expectedNew -or $state.AddedRoute -ne $expectedNew) { throw 'Route ownership changed' }
        } elseif ('-interface-index' -in $global:CwgArguments -or $global:CwgRouteCreated -or $state.TransportInterfaceIndex -ne 0) { throw 'Test mode unexpectedly pinned or changed routes' }
        Write-Host ('PASS: ' + $case)
    }
    Set-Content -LiteralPath (Join-Path $fixture 'client.json') -Value '{"endpoint":"203.0.113.1:443","transport":"h3"}'
    foreach ($case in @('H3-valid','H3-MTU','H3-IPv6')) {
        $global:CwgCase=$case; $global:CwgArguments=@(); $global:CwgPhysical=$true; $global:CwgExisting=$false; $global:CwgRouteCreated=$false
        $mtu=if ($case -eq 'H3-MTU') {1280} else {1088}
        $address=if ($case -eq 'H3-IPv6') {'10.77.0.2/32, fd77:77:77::2/128'} else {'10.77.0.2/32'}
        Set-Content -LiteralPath (Join-Path $fixture 'cwg-full.conf') -Value "[Interface]`nMTU = $mtu`nAddress = $address`n[Peer]`nAllowedIPs = 0.0.0.0/1, 128.0.0.0/1"
        if (Test-Path -LiteralPath $statePath) { Remove-Item -LiteralPath $statePath }
        $failed=$false
        try { & (Join-Path $fixture 'Start-Client.ps1') -Bundle $fixture -Mode Full -Executable (Join-Path $fixture 'fake-cwg.ps1') | Out-Null } catch { if ($case -eq 'H3-valid') { throw }; $failed=$true }
        if ($case -eq 'H3-valid') {
            if (-not $global:CwgArguments.Count -or -not $global:CwgRouteCreated) { throw 'H3 start did not run' }
        } elseif (-not $failed -or $global:CwgArguments.Count -or $global:CwgRouteCreated -or (Test-Path -LiteralPath $statePath)) { throw ('H3 failed to reject unsafe native profile: ' + $case) }
        Write-Host ('PASS: ' + $case)
    }
    Set-Content -LiteralPath (Join-Path $fixture 'client.json') -Value '{"endpoint":"203.0.113.1:443","transport":"exchange","exchange_pps":100}'
    foreach ($case in @('Exchange-valid','Exchange-oversize','Exchange-missing','Exchange-duplicate')) {
        $global:CwgCase=$case; $global:CwgArguments=@(); $global:CwgPhysical=$true; $global:CwgExisting=$false; $global:CwgRouteCreated=$false
        $mtu= switch($case) {'Exchange-valid' {"MTU = 1088"};'Exchange-oversize' {"MTU = 1281"};'Exchange-duplicate' {"MTU = 1088`nMTU = 1280"};default {''}}
        Set-Content -LiteralPath (Join-Path $fixture 'cwg-full.conf') -Value "[Interface]`n$mtu`nAddress = 10.77.0.2/32`n[Peer]`nAllowedIPs = 0.0.0.0/1, 128.0.0.0/1"
        if(Test-Path -LiteralPath $statePath){Remove-Item -LiteralPath $statePath}
        $failed=$false
        try {& (Join-Path $fixture 'Start-Client.ps1') -Bundle $fixture -Mode Full -Executable (Join-Path $fixture 'fake-cwg.ps1') | Out-Null} catch {if($case -eq 'Exchange-valid'){throw};$failed=$true}
        if($case -eq 'Exchange-valid') {if(-not $global:CwgArguments.Count -or -not $global:CwgRouteCreated){throw 'Exchange start did not run'}}
        elseif(-not $failed -or $global:CwgArguments.Count -or $global:CwgRouteCreated -or (Test-Path -LiteralPath $statePath)){throw ('Exchange MTU guard failed: '+$case)}
        Write-Host ('PASS: '+$case)
    }
} finally { $env:ProgramFiles=$originalProgramFiles }
Write-Host 'Seventeen offline routing helper cases passed. No network or system state changed.'
