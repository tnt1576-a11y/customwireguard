#Requires -RunAsAdministrator
# User-run only. Records H3 socket metadata without Packet Monitor or a tunnel.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle,[Parameter(Mandatory)][string]$RawBundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
$rawPath=(Resolve-Path -LiteralPath $RawBundle).Path
$executable=(Resolve-Path -LiteralPath (Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe')).Path
$cfg=Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
$raw=Get-Content -LiteralPath (Join-Path $rawPath 'client.json') -Raw | ConvertFrom-Json
if ($cfg.transport -ne 'h3' -or $raw.transport -eq 'h3') {throw 'Use H3 and raw bundles respectively.'}
$parts=$cfg.endpoint.Split(':');$rawParts=$raw.endpoint.Split(':')
if ($parts.Count -ne 2 -or $rawParts.Count -ne 2 -or $parts[0] -ne $rawParts[0]) {throw 'Both bundles must point to the same literal IPv4 server.'}
$server=[Net.IPAddress]::Parse($parts[0])
if ($server.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {throw 'An IPv4 server is required.'}
foreach ($folder in @($bundlePath,$rawPath)) {if (Test-Path -LiteralPath (Join-Path $folder 'client-state.json')) {throw 'Stop CWG first.'}}
if (Get-NetUDPEndpoint -LocalPort 51821,51822 -ErrorAction SilentlyContinue) {throw 'Stop CWG first.'}
$route=Find-NetRoute -RemoteIPAddress $server.IPAddressToString | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
if (-not $route) {throw 'No route to server.'}
$adapter=Get-NetAdapter | Where-Object ifIndex -eq $route.InterfaceIndex | Select-Object -First 1
if (-not $adapter.HardwareInterface) {throw 'Disconnect other VPNs first; use a physical route to the server.'}
$prefix=Join-Path $bundlePath ('h3-socket-'+[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff'))
$report=$prefix+'.report.txt'
function Write-Report([string]$Text) {Add-Content -LiteralPath $report -Value $Text -Encoding UTF8; Write-Host $Text}
Write-Report ('H3 socket comparison UTC '+[DateTime]::UtcNow.ToString('o'))
Write-Report ('Physical route: '+$adapter.Name+'; interface '+$route.InterfaceIndex+'; H3 '+$cfg.endpoint+'; raw '+$raw.endpoint)
Write-Report 'No VPN, routes, DNS, adapter binding or Packet Monitor changes. Socket writes are not proof of network delivery.'
$cases=@(
    @{Name='01-default';Folder=$bundlePath;Index=0;Trace=$true},
    @{Name='02-bound';Folder=$bundlePath;Index=[int]$route.InterfaceIndex;Trace=$true},
    @{Name='03-default';Folder=$bundlePath;Index=0;Trace=$true},
    @{Name='04-raw';Folder=$rawPath;Index=0;Trace=$false}
)
foreach ($case in $cases) {
    $stdout=$prefix+'-'+$case.Name+'.stdout.log'; $stderr=$prefix+'-'+$case.Name+'.stderr.log'
    $trace=$prefix+'-'+$case.Name+'.jsonl'
    $arguments=@('probe','-config',('"'+(Join-Path $case.Folder 'client.json')+'"'),'-count','2')
    if ($case.Trace) {$arguments+=@('-trace',('"'+$trace+'"'),'-interface-index',[string]$case.Index)}
    Write-Report ("`n"+([DateTime]::UtcNow.ToString('o'))+' BEGIN '+$case.Name)
    $process=Start-Process -FilePath $executable -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        # Cache the process handle before it exits, so PS5.1 can retain its exit
        # status. Never treat a missing exit code as a successful preflight.
        $null=$process.Handle
        if (-not $process.WaitForExit(30000)) {$process.Kill(); $process.WaitForExit(); Write-Report 'Owned probe exceeded 30 seconds and was stopped.'}
        $process.WaitForExit()
        $exitCode=$process.ExitCode
        if ($null -eq $exitCode) {Write-Report 'Exit code unavailable; inspect output below (not a success indication).'} else {Write-Report ('Exit code: '+$exitCode)}
        foreach ($path in @($stdout,$stderr)) {$text=Get-Content -LiteralPath $path -Raw -ErrorAction SilentlyContinue; if ($text) {Write-Report $text.TrimEnd()}}
        if ($case.Trace -and (Test-Path -LiteralPath $trace)) {
            $records=@(Get-Content -LiteralPath $trace | Where-Object {$_} | ForEach-Object {$_ | ConvertFrom-Json})
            Write-Report ('Socket metadata: writes='+@($records | Where-Object event -eq 'h3-write').Count+' reads='+@($records | Where-Object event -eq 'h3-read').Count+' write_errors='+@($records | Where-Object event -eq 'h3-write-error').Count)
        }
    } finally {
        if (-not $process.HasExited) {$process.Kill(); $process.WaitForExit()}
        $process.Dispose()
    }
    Write-Report (([DateTime]::UtcNow.ToString('o'))+' END '+$case.Name)
}
Write-Report ('Completed UTC '+[DateTime]::UtcNow.ToString('o'))
Write-Host ('Saved '+$report+'. Reconnect your usual VPN and report done. No tunnel was started.')
