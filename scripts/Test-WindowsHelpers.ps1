# Offline regression tests only. No real WireGuard executable, services, routes,
# sockets, or credentials are used. Run under Windows PowerShell 5.1 and PS 7.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$StubExecutable,
    [string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'
if (-not $OutputDirectory) { $OutputDirectory = Join-Path (Split-Path $PSScriptRoot -Parent) 'test-output\windows-helpers' }
. (Join-Path $PSScriptRoot 'WireGuard-Service.ps1')
$caseDirectory = Join-Path $OutputDirectory ([guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $caseDirectory -Force | Out-Null
function Assert([bool]$Condition,[string]$Message) { if (-not $Condition) { throw $Message } }
$passed = 0

# A delayed GUI executable must finish before the helper returns, preserving
# quoted paths and using its real exit code rather than $LASTEXITCODE.
$target = Join-Path $caseDirectory 'path with spaces.conf'
$global:LASTEXITCODE = 23
Invoke-CwgWireGuardCommand -Executable $StubExecutable -Action Install -Target $target -LogDirectory $caseDirectory
Assert (Test-Path -LiteralPath ($target + '.observed.json')) 'Returned before GUI process completed.'
$observed = Get-Content -LiteralPath ($target + '.observed.json') -Raw | ConvertFrom-Json
Assert ($observed[0] -eq '/installtunnelservice' -and $observed[1] -eq $target) 'Arguments were changed or split.'
$passed++

$target = Join-Path $caseDirectory 'failure.conf'
$global:LASTEXITCODE = 0
$failed = $false
try { Invoke-CwgWireGuardCommand -Executable $StubExecutable -Action Install -Target $target -LogDirectory $caseDirectory }
catch { $failed = $_.Exception.Message -match 'exit code 7' }
Assert $failed 'Installer failure was hidden by a stale success exit code.'
Assert ((Get-Content -LiteralPath (Join-Path $caseDirectory 'wireguard-install.stderr.log') -Raw) -match 'Simulated installer error') 'Installer stderr was not saved.'
$passed++

$target = Join-Path $caseDirectory 'uninstall target'
Invoke-CwgWireGuardCommand -Executable $StubExecutable -Action Uninstall -Target $target -LogDirectory $caseDirectory
$observed = Get-Content -LiteralPath ($target + '.observed.json') -Raw | ConvertFrom-Json
Assert ($observed[0] -eq '/uninstalltunnelservice' -and $observed[1] -eq $target) 'Uninstall did not wait or preserve its argument.'
$passed++

$failed = $false
try { Invoke-CwgWireGuardCommand -Executable $StubExecutable -Action Uninstall -Target (Join-Path $caseDirectory 'uninstall-failure') -LogDirectory $caseDirectory }
catch { $failed = $_.Exception.Message -match 'exit code 7' }
Assert $failed 'Uninstaller failure was ignored.'
$passed++

# Shadow service discovery so readiness/removal polling cannot access real SCM.
$script:statuses = @('', 'StartPending', 'Running')
$script:pollCount = 0
$script:disposed = 0
function Get-Service {
    param($Name,$ErrorAction)
    Assert ($Name -ceq 'WireGuardTunnel$cwg-test') 'Incorrect tunnel service name.'
    $index = [Math]::Min($script:pollCount,$script:statuses.Count-1)
    $script:pollCount++
    $status = $script:statuses[$index]
    if (-not $status) { return }
    $controller = [pscustomobject]@{ Status=$status }
    $controller | Add-Member -MemberType ScriptMethod -Name Dispose -Value { $script:disposed++ }
    return $controller
}
Wait-CwgWireGuardService -Tunnel cwg-test -State Running
Assert ($script:pollCount -eq 3 -and $script:disposed -eq 2) 'Did not wait for delayed service registration/start.'
$passed++
$script:statuses = @('StopPending', 'Stopped', '')
$script:pollCount = 0
$script:disposed = 0
Wait-CwgWireGuardService -Tunnel cwg-test -State Removed
Assert ($script:pollCount -eq 3 -and $script:disposed -eq 2) 'Did not wait for removal or retained a service handle.'
$passed++
$script:statuses = @('')
$failed = $false
try { Wait-CwgWireGuardService -Tunnel cwg-test -State Running -TimeoutSeconds 1 }
catch { $failed = $_.Exception.Message -match 'did not reach state Running' }
Assert $failed 'Missing service was reported as running.'
$passed++
$script:statuses = @('Stopped')
$failed = $false
try { Wait-CwgWireGuardService -Tunnel cwg-test -State Removed -TimeoutSeconds 1 }
catch { $failed = $_.Exception.Message -match 'did not reach state Removed' }
Assert $failed 'Stopped service was reported as removed.'
$passed++

# Replace uninstall and service lookups; verify recovery is scoped to the exact
# bundle, including paths with spaces and quoted/unquoted SCM argument forms.
$script:fakeService = $null
$script:uninstallCalls = 0
function Get-CimInstance { param($ClassName,$Filter,$ErrorAction); return $script:fakeService }
function Invoke-CwgWireGuardCommand { param($Executable,$Action,$Target,$LogDirectory); $script:uninstallCalls++ }
function Wait-CwgWireGuardService { param($Tunnel,$State) }
$exe = 'C:\Program Files\WireGuard\wireguard.exe'
$bundle = Join-Path $caseDirectory 'private bundle'
$config = Join-Path $bundle 'cwg-test.conf'
foreach ($argument in @($config,('"' + $config + '"'))) {
    $script:fakeService = [pscustomobject]@{PathName=('"' + $exe + '" /tunnelservice ' + $argument)}
    Assert (Remove-CwgWireGuardService -Executable $exe -BundlePath $bundle -Tunnel cwg-test) 'Owned orphan was not recovered.'
    $passed++
}
foreach ($command in @(
    ('"' + $exe + '" /tunnelservice "C:\different\cwg-test.conf"'),
    ('"C:\different\wireguard.exe" /tunnelservice "' + $config + '"'),
    ('"' + $exe + '" /tunnelservice "' + $config + '" extra')
)) {
    $script:fakeService = [pscustomobject]@{PathName=$command}
    $failed = $false
    try { $null = Remove-CwgWireGuardService -Executable $exe -BundlePath $bundle -Tunnel cwg-test }
    catch { $failed = $_.Exception.Message -match 'does not belong to this bundle' }
    Assert $failed 'Cleanup accepted an unrelated service command line.'
    $passed++
}
Assert ($script:uninstallCalls -eq 2) 'Cleanup invoked uninstall for an unrelated service.'
$script:fakeService = $null
Assert (-not (Remove-CwgWireGuardService -Executable $exe -BundlePath $bundle -Tunnel cwg-test)) 'Missing service should need no cleanup.'
$passed++
Write-Output ("Passed $passed offline Windows helper checks under PowerShell " + $PSVersionTable.PSVersion + '. No VPN or network tests performed.')
