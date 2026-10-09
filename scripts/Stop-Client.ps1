#Requires -RunAsAdministrator
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'WireGuard-Service.ps1')
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$statePath = Join-Path $bundlePath 'client-state.json'
$wireguardExe = Join-Path $env:ProgramFiles 'WireGuard\wireguard.exe'
if (-not (Test-Path -LiteralPath $statePath)) {
    $recovered = $false
    foreach ($tunnel in @('cwg-test','cwg-full')) {
        if (Remove-CwgWireGuardService -Executable $wireguardExe -BundlePath $bundlePath -Tunnel $tunnel) { $recovered = $true }
    }
    if ($recovered) { Write-Host 'Removed a leftover WireGuard service belonging to this bundle. No saved transport/route state was available.' }
    else { Write-Host 'No saved CWG client state or leftover tunnel service in this bundle.' }
    return
}
$state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
if ($state.Version -ne 1 -or $state.Tunnel -notin @('cwg-test','cwg-full')) { throw 'Unrecognized state; refusing cleanup.' }
if ($state.TunnelInstalled) {
    $null = Remove-CwgWireGuardService -Executable $wireguardExe -BundlePath $bundlePath -Tunnel $state.Tunnel
}
if ($state.ProcessId -gt 0) {
    $process = Get-Process -Id $state.ProcessId -ErrorAction SilentlyContinue
    if ($process) {
        if ($process.Path -ne $state.Executable -or $process.StartTime.ToUniversalTime().ToString('o') -ne $state.ProcessStartUTC) { throw 'PID belongs to a different process; refusing to stop it.' }
        Stop-Process -Id $process.Id
    }
}
if ($state.AddedRoute) {
    if ($state.RoutePrefix -notmatch '^(\d{1,3}\.){3}\d{1,3}/32$' -or $state.InterfaceIndex -lt 1) { throw 'Invalid saved route; refusing route changes.' }
    Get-NetRoute -DestinationPrefix $state.RoutePrefix -InterfaceIndex $state.InterfaceIndex -NextHop $state.NextHop -PolicyStore ActiveStore -ErrorAction SilentlyContinue |
        Where-Object RouteMetric -eq 3 | Remove-NetRoute -Confirm:$false
}
Remove-Item -LiteralPath $statePath
Write-Host 'CWG tunnel and transport stopped; any endpoint route created by the helper was removed.'
