#Requires -RunAsAdministrator
# User-run only. Transfers and pings go through the real CWG tunnel.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Bundle)
$ErrorActionPreference='Stop'
$bundlePath=(Resolve-Path -LiteralPath $Bundle).Path
if(Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json')){throw 'Stop CWG before starting this measurement.'}
$cfg=Get-Content -LiteralPath (Join-Path $bundlePath 'client.json') -Raw | ConvertFrom-Json
if($cfg.transport -ne 'exchange' -or -not $cfg.exchange_max_pps){throw 'This test requires the adaptive exchange configuration.'}
if(-not $cfg.exchange_burst_ms){throw 'This comparison requires the burst-queue update.'}
$stamp=[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-fff')
$directory=Join-Path $bundlePath ('adaptive-performance-'+$stamp)
New-Item -ItemType Directory -Path $directory | Out-Null
$report=Join-Path $directory 'report.txt';$samples=Join-Path $directory 'pings.jsonl'
$curl=(Get-Command curl.exe -ErrorAction Stop).Source
$executable=Join-Path (Split-Path $PSScriptRoot -Parent) 'bin/cwg-windows-amd64.exe'
$ownedProcesses=[Collections.Generic.List[object]]::new()
$ownedFiles=[Collections.Generic.List[string]]::new()
$hashes=@{};$block=New-Object byte[] 65536
for($i=0;$i -lt $block.Length;$i++){$block[$i]=[byte]($i%256)}
function Write-Report([string]$Text){Add-Content -LiteralPath $report -Value $Text -Encoding UTF8;Write-Host $Text}
function Payload-Hash([long]$Count){
    if($hashes.ContainsKey($Count)){return $hashes[$Count]}
    $sha=[Security.Cryptography.SHA256]::Create();$left=$Count
    try{
        while($left -gt 0){$n=[int][math]::Min($left,$block.Length);$null=$sha.TransformBlock($block,0,$n,$block,0);$left-=$n}
        $null=$sha.TransformFinalBlock([byte[]]@(),0,0)
        $value=[BitConverter]::ToString($sha.Hash).Replace('-','').ToLowerInvariant();$hashes[$Count]=$value;return $value
    }finally{$sha.Dispose()}
}
function Start-Transfer([string]$Phase,[string]$Direction,[int]$Mbps,[switch]$Smooth){
    $bps=[long]$Mbps*125000;$count=$bps*10
    $body=Join-Path $directory ($Phase+'-'+$Direction+'.body');$ownedFiles.Add($body)
    $stdout=Join-Path $directory ($Phase+'-'+$Direction+'.json');$stderr=Join-Path $directory ($Phase+'-'+$Direction+'.stderr.log')
    $expected=Payload-Hash $count
    $arguments='-q --ipv4 --noproxy "*" --connect-timeout 6 --max-time 30 --fail --silent --show-error --output "'+$body+'" --write-out "%{json}"'
    $program=$curl
    $identifier=$Phase+'-'+$stamp.Replace('-','')
    $arguments+=' --header "X-CWG-Test-ID: '+$identifier+'"'
    if($Smooth){
        if($Direction -ne 'upload'){throw 'Smooth pacing is upload-only.'}
        $program=$executable
        $arguments='smooth-upload -endpoint 10.77.0.1:18081 -bytes '+$count+' -bps '+$bps+' -id '+$identifier
    }elseif($Direction -eq 'upload'){
        $inputPath=Join-Path $directory ($Phase+'-input.bin');$ownedFiles.Add($inputPath)
        $stream=[IO.File]::Create($inputPath)
        try{$left=$count;while($left -gt 0){$n=[int][math]::Min($left,$block.Length);$stream.Write($block,0,$n);$left-=$n}}finally{$stream.Dispose()}
        $arguments+=' --limit-rate '+$bps+' --header "Expect:" --header "Content-Type: application/octet-stream" --data-binary "@'+$inputPath+'" "http://10.77.0.1:18081/upload"'
    }else{$arguments+=' "http://10.77.0.1:18081/download?bytes='+$count+'&bps='+$bps+'"'}
    $process=Start-Process -FilePath $program -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    $null=$process.Handle;$ownedProcesses.Add($process)
    return [pscustomobject]@{Process=$process;Direction=$Direction;Body=$body;Stats=$stdout;Expected=$expected;Bytes=$count;Mbps=$Mbps;Smooth=[bool]$Smooth;Id=$identifier}
}
function Measure-Phase([string]$Name,[object[]]$Transfers,[int]$IdleSamples=0){
    Write-Report ('PHASE '+$Name+' start UTC: '+[DateTime]::UtcNow.ToString('o'))
    $times=[Collections.Generic.List[long]]::new();$sent=0
    do{
        $watch=[Diagnostics.Stopwatch]::StartNew();$utc=[DateTime]::UtcNow.ToString('o');$status='Error';$rtt=$null
        try{$reply=$pinger.Send('10.77.0.1',1000);$status=$reply.Status.ToString();if($status -eq 'Success'){$rtt=$reply.RoundtripTime;$times.Add($rtt)}}catch{$status='PingError'}
        $sent++
        $progress=@{}
        foreach($transfer in $Transfers){
            if($transfer.Direction -eq 'download' -and (Test-Path -LiteralPath $transfer.Body)){
                $progress[$transfer.Id]=(Get-Item -LiteralPath $transfer.Body).Length
            }
        }
        [ordered]@{utc=$utc;phase=$Name;status=$status;rtt_ms=$rtt;download_bytes=$progress}|ConvertTo-Json -Compress|Add-Content -LiteralPath $samples -Encoding UTF8
        $remaining=200-[int]$watch.ElapsedMilliseconds;if($remaining -gt 0){Start-Sleep -Milliseconds $remaining}
        $running=@($Transfers|Where-Object {-not $_.Process.HasExited}).Count
    }while($running -gt 0 -or $sent -lt $IdleSamples)
    $ordered=@($times|Sort-Object)
    $summary=[ordered]@{phase=$Name;sent=$sent;received=$times.Count;loss_percent=[math]::Round(100*(1-$times.Count/[double]$sent),2);p50_ms=$null;p95_ms=$null;max_ms=$null}
    if($ordered.Count){$summary.p50_ms=$ordered[[int][math]::Floor(($ordered.Count-1)*.5)];$summary.p95_ms=$ordered[[int][math]::Floor(($ordered.Count-1)*.95)];$summary.max_ms=$ordered[-1]}
    Write-Report ($summary|ConvertTo-Json -Compress)
    foreach($transfer in $Transfers){
        $transfer.Process.WaitForExit();$exitCode=$transfer.Process.ExitCode
        $stats=$null;try{$stats=Get-Content -LiteralPath $transfer.Stats -Raw|ConvertFrom-Json}catch{}
        $verified=$false
        if($exitCode -eq 0 -and $transfer.Smooth){
            $verified=$stats -and $stats.complete_and_verified -and $stats.received_bytes -eq $transfer.Bytes
        }elseif($exitCode -eq 0 -and (Test-Path -LiteralPath $transfer.Body)){
            if($transfer.Direction -eq 'download'){$verified=(Get-Item -LiteralPath $transfer.Body).Length -eq $transfer.Bytes -and (Get-FileHash -LiteralPath $transfer.Body).Hash.ToLowerInvariant() -eq $transfer.Expected}
            else{try{$body=Get-Content -LiteralPath $transfer.Body -Raw|ConvertFrom-Json;$verified=$body.received_bytes -eq $transfer.Bytes -and $body.sha256 -eq $transfer.Expected}catch{}}
        }
        $speed=$null;if($stats){$bytesPerSecond=if($transfer.Direction -eq 'download'){$stats.speed_download}else{$stats.speed_upload};$speed=[math]::Round([double]$bytesPerSecond*8/1000000,2)}
        Write-Report ([ordered]@{phase=$Name;id=$transfer.Id;direction=$transfer.Direction;smooth=$transfer.Smooth;target_mbps=$transfer.Mbps;measured_mbps=$speed;process_exit=$exitCode;complete_and_verified=[bool]$verified}|ConvertTo-Json -Compress)
    }
    Write-Report ('Phase end UTC: '+[DateTime]::UtcNow.ToString('o'))
}
$started=$false;$pinger=$null
try{
    Write-Report ('Adaptive CWG user-run performance test UTC: '+[DateTime]::UtcNow.ToString('o'))
    Write-Report 'Close videos/downloads and disconnect other VPNs. Allow 3-5 minutes. The test transfers 281.25 MB of synthetic payload; UDP padding and polling add further traffic.'
    & (Join-Path $PSScriptRoot 'Start-Client.ps1') -Bundle $bundlePath -Mode Full -TracePackets
    $started=$true;$pinger=[Net.NetworkInformation.Ping]::new()
    Measure-Phase 'idle-before' @() 20
    foreach($rate in @(25,50)){$name='download-'+$rate;$transfer=Start-Transfer $name 'download' $rate;Measure-Phase $name @($transfer)}
    $a=Start-Transfer 'parallel-25-a' 'download' 25;$b=Start-Transfer 'parallel-25-b' 'download' 25;Measure-Phase 'parallel-download-50' @($a,$b)
    $transfer=Start-Transfer 'upload-bursty-25' 'upload' 25;Measure-Phase 'upload-bursty-25' @($transfer)
    $transfer=Start-Transfer 'upload-smooth-25' 'upload' 25 -Smooth;Measure-Phase 'upload-smooth-25' @($transfer)
    $up=Start-Transfer 'duplex-25-up' 'upload' 25 -Smooth;$down=Start-Transfer 'duplex-25-down' 'download' 25;Measure-Phase 'duplex-25' @($up,$down)
    Measure-Phase 'idle-after' @() 20
}finally{
    foreach($process in $ownedProcesses){try{if(-not $process.HasExited){$process.Kill();$process.WaitForExit()}}catch{Write-Warning 'Could not stop a diagnostic transfer process.'}finally{$process.Dispose()}}
    if($pinger){$pinger.Dispose()}
    try{
        if($started -or (Test-Path -LiteralPath (Join-Path $bundlePath 'client-state.json'))){& (Join-Path $PSScriptRoot 'Stop-Client.ps1') -Bundle $bundlePath}
    }finally{
        $log=Join-Path $bundlePath 'transport.stderr.log';if(Test-Path -LiteralPath $log){Copy-Item -LiteralPath $log -Destination (Join-Path $directory 'transport.log')}
        foreach($path in $ownedFiles){if(Test-Path -LiteralPath $path){Remove-Item -LiteralPath $path}}
        Write-Host ('Saved report: '+$report)
    }
}
Write-Host 'CWG stopped. Reconnect your usual VPN and report done.'
