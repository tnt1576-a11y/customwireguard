#Requires -RunAsAdministrator
# User-run only. Isolate tracing using the SAME original bundle/path each time.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
if (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')) { throw 'Stop CWG before this comparison.' }
if (Test-Path -LiteralPath (Join-Path $bundlePath 'wiresock-filter-state.json')) { throw 'Finish WireSock filter restoration before this comparison.' }
$sourceHashes=@{}
foreach ($file in @('client.json','cwg-full.conf')) { $sourceHashes[$file]=(Get-FileHash -LiteralPath (Join-Path $bundlePath $file)).Hash }
$prefix='trace-comparison-'+[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$parent=Join-Path $bundlePath $prefix
New-Item -ItemType Directory -Path $parent -ErrorAction Stop | Out-Null
$reportPath=Join-Path $bundlePath ($prefix+'.txt')
$ping=(Get-Command ping.exe -ErrorAction Stop).Source
$fixedLogs=@('transport.stdout.log','transport.stderr.log','wireguard-install.stdout.log','wireguard-install.stderr.log','wireguard-uninstall.stdout.log','wireguard-uninstall.stderr.log')
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Diagnostic-Files {
    Get-ChildItem -LiteralPath $bundlePath -File | Where-Object { $_.Name -like 'diagnostic-*' -or $_.Name -like 'packets-full-*.jsonl' }
}
function Copy-FixedLogs([string]$Destination) {
    foreach ($name in $fixedLogs) {
        $source=Join-Path $bundlePath $name
        if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination (Join-Path $Destination $name) }
    }
}
$prior=Join-Path $parent 'previous-logs'
New-Item -ItemType Directory -Path $prior -ErrorAction Stop | Out-Null
Copy-FixedLogs $prior
Write-Report ('Client tracing comparison UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Report ('Original bundle used in every phase: '+$bundlePath)
Write-Report 'Four Full-mode runs: tracing off, on, off, on. No configuration copies, binding changes, or wire-format changes.'
Write-Report 'Keep other VPNs disconnected and leave applications/network settings unchanged. Allow about 3-6 minutes.'
$cases=@(
    @{Name='01-off';Trace=$false}, @{Name='02-on';Trace=$true},
    @{Name='03-off';Trace=$false}, @{Name='04-on';Trace=$true}
)
foreach ($case in $cases) {
    foreach ($file in $sourceHashes.Keys) {
        if ((Get-FileHash -LiteralPath (Join-Path $bundlePath $file)).Hash -ne $sourceHashes[$file]) { throw 'Configuration changed during comparison.' }
    }
    $phase=Join-Path $parent $case.Name
    New-Item -ItemType Directory -Path $phase -ErrorAction Stop | Out-Null
    $previousFiles=@(Diagnostic-Files | ForEach-Object Name)
    $started=$false
    Write-Report ("`nPHASE "+$case.Name+' UTC: '+[DateTime]::UtcNow.ToString('o')+'; packet tracing: '+$case.Trace)
    try {
        $startArgs=@{Bundle=$bundlePath;Mode='Full';TracePackets=[bool]$case.Trace}
        & (Join-Path $PSScriptRoot 'Start-Client.ps1') @startArgs
        $started=$true
        $stdout=Join-Path $phase 'initial-ping.stdout.log'
        $stderr=Join-Path $phase 'initial-ping.stderr.log'
        $process=Start-Process -FilePath $ping -ArgumentList '-4 -n 12 -w 1000 10.77.0.1' -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        try {
            Write-Report ('Initial private ping exit: '+$process.ExitCode)
            Write-Report ((Get-Content -LiteralPath $stdout -Raw).TrimEnd())
        } finally {$process.Dispose()}
        & (Join-Path $PSScriptRoot 'Diagnose-Client.ps1') -Bundle $bundlePath
        $diagnostic=Get-ChildItem -LiteralPath $bundlePath -Filter 'diagnostic-*.txt' -File |
            Where-Object {$_.Name -notin $previousFiles} | Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1
        if (-not $diagnostic) {throw 'No new diagnostic report was created.'}
        Write-Report (Get-Content -LiteralPath $diagnostic.FullName -Raw)
    } finally {
        try {
            if ($started -or (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json'))) {
                & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $bundlePath
                Write-Report ('Stopped '+$case.Name+' UTC: '+[DateTime]::UtcNow.ToString('o'))
            }
        } finally {
            Copy-FixedLogs $phase
            foreach ($file in Diagnostic-Files) {
                if ($file.Name -notin $previousFiles) {Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $phase $file.Name)}
            }
        }
    }
    if ($case.Name -ne '04-on') {Start-Sleep -Milliseconds 3000}
}
Write-Report ('Completed UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Host ('CWG is stopped. Report: '+$reportPath)
