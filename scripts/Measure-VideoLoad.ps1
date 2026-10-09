#Requires -RunAsAdministrator
# User-run only: one continuous Full session, with user-controlled video load.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Bundle,
    [ValidateRange(3,120)][int]$SamplesPerPhase=30
)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
foreach ($name in @('client-state.json','wiresock-filter-state.json')) {
    if (Test-Path -LiteralPath (Join-Path $bundlePath $name)) { throw 'Stop CWG and finish any filter recovery before this test.' }
}
$prefix='video-load-'+[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$directory=Join-Path $bundlePath $prefix
New-Item -ItemType Directory -Path $directory -ErrorAction Stop | Out-Null
$reportPath=Join-Path $bundlePath ($prefix+'.txt')
$samplesPath=Join-Path $directory 'samples.jsonl'
$oldTraces=@(Get-ChildItem -LiteralPath $bundlePath -Filter 'packets-full-*.jsonl' -File | ForEach-Object Name)
foreach ($name in @('transport.stderr.log','transport.stdout.log')) {
    $source=Join-Path $bundlePath $name
    if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination (Join-Path $directory ('previous-'+$name)) }
}
function Write-Report([string]$Text) {
    Add-Content -LiteralPath $reportPath -Value $Text -Encoding UTF8
    Write-Host $Text
}
function Check-TraceLimit {
    # A capped trace no longer performs its synchronous file writes. Do not
    # mistake that timing transition for an effect caused only by video traffic.
    foreach ($line in Get-Content -LiteralPath $tracePath -Tail 3) {
        try {$record=$line | ConvertFrom-Json} catch {continue} # concurrently appended last line
        if ($record.event -eq 'limit_reached') {
            Write-Report ('TRACE LIMIT REACHED UTC: '+$record.utc+'; test interrupted because logging is no longer equivalent.')
            throw 'Packet trace reached its record limit. This run cannot isolate video load after that point.'
        }
    }
}
function Read-TunnelBytes {
    try {
        $stats=Get-NetAdapterStatistics -Name cwg-full -ErrorAction Stop
        return [pscustomobject]@{Received=[long]$stats.ReceivedBytes;Sent=[long]$stats.SentBytes}
    } catch {return $null}
}
function Measure-Phase([string]$Name) {
    Check-TraceLimit
    Write-Report ("`nPHASE "+$Name+' start UTC: '+[DateTime]::UtcNow.ToString('o'))
    $before=Read-TunnelBytes
    $received=0
    $times=@()
    for ($i=0; $i -lt $SamplesPerPhase; $i++) {
        Check-TraceLimit
        $timer=[Diagnostics.Stopwatch]::StartNew()
        $utc=[DateTime]::UtcNow.ToString('o')
        $status='Error';$rtt=$null
        try {
            $reply=$pinger.Send('10.77.0.1',1000)
            $status=$reply.Status.ToString()
            if ($status -eq 'Success') {$received++;$rtt=$reply.RoundtripTime;$times+=$rtt}
        } catch {$status='PingError'}
        [ordered]@{utc=$utc;phase=$Name;sample=$i+1;status=$status;rtt_ms=$rtt} | ConvertTo-Json -Compress |
            Add-Content -LiteralPath $samplesPath -Encoding UTF8
        Write-Host ($Name+' '+($i+1)+'/'+$SamplesPerPhase+': '+$status+' '+$rtt+' ms')
        Check-TraceLimit
        $remaining=1000-[int]$timer.ElapsedMilliseconds
        if ($remaining -gt 0) {Start-Sleep -Milliseconds $remaining}
    }
    $after=Read-TunnelBytes
    $result=[ordered]@{phase=$Name;sent=$SamplesPerPhase;received=$received;loss_percent=[math]::Round(100*(1-$received/[double]$SamplesPerPhase),1);mean_rtt_ms=$null;tunnel_received_bytes=$null;tunnel_sent_bytes=$null}
    if ($times.Count) {$result.mean_rtt_ms=[math]::Round(($times | Measure-Object -Average).Average,1)}
    if ($before -and $after) {$result.tunnel_received_bytes=$after.Received-$before.Received;$result.tunnel_sent_bytes=$after.Sent-$before.Sent}
    Write-Report ($result | ConvertTo-Json -Compress)
    Write-Report ('Phase end UTC: '+[DateTime]::UtcNow.ToString('o'))
}
Write-Report ('Same-session video load test UTC: '+[DateTime]::UtcNow.ToString('o'))
Write-Report 'Begin with YouTube/downloads closed. Tracing remains enabled; the outer UDP session is not restarted between phases.'
Write-Report 'Follow the two prompts to start a video and then close its tab. Keep this PowerShell window open.'
Write-Report ('Interrupted cleanup: .\scripts\Stop-Client.ps1 -Bundle "'+$bundlePath+'"')
$started=$false;$tracePath=$null;$pinger=$null
try {
    & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $bundlePath -Mode Full -TracePackets
    $started=$true
    $traces=@(Get-ChildItem -LiteralPath $bundlePath -Filter 'packets-full-*.jsonl' -File | Where-Object {$_.Name -notin $oldTraces})
    if ($traces.Count -ne 1) {throw 'Cannot identify this session packet trace.'}
    $tracePath=$traces[0].FullName
    Write-Report ('Packet trace: '+$tracePath)
    $pinger=[Net.NetworkInformation.Ping]::new()
    Measure-Phase '01-idle'
    Write-Report ('Waiting for user to start video UTC: '+[DateTime]::UtcNow.ToString('o'))
    $null=Read-Host 'Start a YouTube video now. Press Enter once it is playing OR trying to load'
    Measure-Phase '02-video'
    Write-Report ('Waiting for user to close video UTC: '+[DateTime]::UtcNow.ToString('o'))
    $null=Read-Host 'Close the YouTube tab now, then press Enter'
    Measure-Phase '03-idle-after'
    Write-Report ('Completed UTC: '+[DateTime]::UtcNow.ToString('o'))
} finally {
    if ($pinger) {$pinger.Dispose()}
    try {
        if ($started -or (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json'))) {
            & (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $bundlePath
            Write-Report ('CWG stopped UTC: '+[DateTime]::UtcNow.ToString('o'))
        }
    } finally {
        foreach ($name in @('transport.stderr.log','transport.stdout.log')) {
            $source=Join-Path $bundlePath $name
            if (Test-Path -LiteralPath $source) {Copy-Item -LiteralPath $source -Destination (Join-Path $directory $name)}
        }
        if ($tracePath) {Copy-Item -LiteralPath $tracePath -Destination (Join-Path $directory (Split-Path $tracePath -Leaf))}
        Write-Host ('Saved report: '+$reportPath)
    }
}
