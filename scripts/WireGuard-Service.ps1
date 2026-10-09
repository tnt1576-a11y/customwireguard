# Shared service helpers. Dot-sourcing this file performs no service operations.
function Invoke-CwgWireGuardCommand {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Executable,
        [Parameter(Mandatory)][ValidateSet('Install','Uninstall')][string]$Action,
        [Parameter(Mandatory)][string]$Target,
        [Parameter(Mandatory)][string]$LogDirectory
    )
    if ($Target.Contains('"') -or $Target.Contains("`r") -or $Target.Contains("`n")) { throw 'Invalid WireGuard command argument.' }
    $verb = if ($Action -eq 'Install') { '/installtunnelservice' } else { '/uninstalltunnelservice' }
    $logName = 'wireguard-' + $Action.ToLowerInvariant()
    $stdout = Join-Path $LogDirectory ($logName + '.stdout.log')
    $stderr = Join-Path $LogDirectory ($logName + '.stderr.log')
    # wireguard.exe is a GUI executable: explicitly wait and use THIS process's
    # exit code, never a potentially stale $LASTEXITCODE from the CWG probe.
    $command = Start-Process -FilePath $Executable -ArgumentList ($verb + ' "' + $Target + '"') -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr -ErrorAction Stop
    try {
        if ($command.ExitCode -ne 0) {
            throw "WireGuard $Action failed (exit code $($command.ExitCode)). See $stderr and $stdout."
        }
    } finally { $command.Dispose() }
}

function Wait-CwgWireGuardService {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][ValidateSet('cwg-test','cwg-full')][string]$Tunnel,
        [Parameter(Mandatory)][ValidateSet('Running','Removed')][string]$State,
        [ValidateRange(1,60)][int]$TimeoutSeconds = 15
    )
    $serviceName = 'WireGuardTunnel$' + $Tunnel
    $timer = [Diagnostics.Stopwatch]::StartNew()
    do {
        $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
        try {
            if ($State -eq 'Removed' -and -not $service) { return }
            if ($State -eq 'Running' -and $service -and $service.Status -eq 'Running') { return }
        } finally {
            # A lingering ServiceController handle can delay service deletion.
            if ($service) { $service.Dispose() }
        }
        Start-Sleep -Milliseconds 100
    } while ($timer.Elapsed.TotalSeconds -lt $TimeoutSeconds)
    throw "WireGuard service $serviceName did not reach state $State within $TimeoutSeconds seconds. Check the WireGuard logs."
}

function Remove-CwgWireGuardService {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Executable,
        [Parameter(Mandatory)][string]$BundlePath,
        [Parameter(Mandatory)][ValidateSet('cwg-test','cwg-full')][string]$Tunnel
    )
    $serviceName = 'WireGuardTunnel$' + $Tunnel
    $service = Get-CimInstance -ClassName Win32_Service -Filter ("Name='{0}'" -f $serviceName) -ErrorAction Stop
    if (-not $service) { return $false }
    $configPath = Join-Path $BundlePath ($Tunnel + '.conf')
    # Recover only a service installed from THIS bundle, even if a previous
    # helper lost its state file. Never remove another bundle's tunnel by name.
    $exePattern = [regex]::Escape($Executable)
    $configPattern = [regex]::Escape($configPath)
    $expected = '^\s*(?:"' + $exePattern + '"|' + $exePattern + ')\s+/tunnelservice\s+(?:"' + $configPattern + '"|' + $configPattern + ')\s*$'
    if ($service.PathName -notmatch $expected) { throw "Service $serviceName does not belong to this bundle; refusing to remove it." }
    Invoke-CwgWireGuardCommand -Executable $Executable -Action Uninstall -Target $Tunnel -LogDirectory $BundlePath
    Wait-CwgWireGuardService -Tunnel $Tunnel -State Removed
    return $true
}
