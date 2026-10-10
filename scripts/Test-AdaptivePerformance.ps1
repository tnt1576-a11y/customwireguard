# Offline fixtures: no VPN, routing, HTTP or ping is performed.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\adaptive-helper-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Measure-AdaptivePerformance.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture').Replace('[Net.NetworkInformation.Ping]::new()','(New-FixturePing)').Replace('$count=$bps*10','$count=256').Replace('$count=$bps*6','$count=256')
Set-Content -LiteralPath (Join-Path $fixture 'Measure-AdaptivePerformance.ps1') -Value $source -Encoding UTF8
Set-Content -LiteralPath (Join-Path $fixture 'Start-Client.ps1') -Value @'
param($Bundle,$Mode,[switch]$TracePackets)
if($Mode -ne 'Full' -or -not $TracePackets){throw 'Bad start mode'}
$global:AdaptiveStarts++
Set-Content -LiteralPath (Join-Path $Bundle 'client-state.json') -Value '{}'
if($global:AdaptiveCase -eq 'partial-start'){throw 'Fixture partial start'}
'@
Set-Content -LiteralPath (Join-Path $fixture 'Stop-Client.ps1') -Value @'
param($Bundle)
$global:AdaptiveStops++
Remove-Item -LiteralPath (Join-Path $Bundle 'client-state.json')
'@
function New-FixturePing {
    $p=[pscustomobject]@{}
    $p|Add-Member ScriptMethod Send {param($Address,$Timeout);if($Address -ne '10.77.0.1'){throw 'Wrong target'};[pscustomobject]@{Status='Success';RoundtripTime=90}}
    $p|Add-Member ScriptMethod Dispose {$global:AdaptivePingDisposed=$true}
    return $p
}
function Start-Sleep {param($Milliseconds)}
function Start-Process {
    param($FilePath,$ArgumentList,$WindowStyle,[switch]$PassThru,$RedirectStandardOutput,$RedirectStandardError)
    if($WindowStyle -ne 'Hidden'){throw 'Diagnostic window not hidden'}
    $smooth=$ArgumentList.StartsWith('smooth-upload ')
    if($smooth){
        if($ArgumentList -notmatch '-bytes 256 -bps 3125000 -id [A-Za-z0-9-]+'){throw 'Invalid smooth upload arguments'}
        '{"speed_upload":3125000,"complete_and_verified":true,"received_bytes":256}'|Set-Content -LiteralPath $RedirectStandardOutput
    }else{
    if($ArgumentList -notmatch '--output "([^"]+)"'){throw 'Missing output'}
    $bodyPath=$Matches[1]
    $payload=[byte[]](0..255);$sha=[Security.Cryptography.SHA256]::Create()
    try{$digest=[BitConverter]::ToString($sha.ComputeHash($payload)).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose()}
    if($ArgumentList -match '/upload'){
        if($ArgumentList -notmatch '--data-binary "@([^"]+)"'){throw 'Missing upload'}
        if((Get-FileHash -LiteralPath $Matches[1]).Hash.ToLowerInvariant() -ne $digest){throw 'Wrong upload pattern'}
        @{received_bytes=256;sha256=$digest}|ConvertTo-Json|Set-Content -LiteralPath $bodyPath
    }else{[IO.File]::WriteAllBytes($bodyPath,$payload)}
    '{"speed_download":1250000,"speed_upload":1250000}'|Set-Content -LiteralPath $RedirectStandardOutput
    }
    ''|Set-Content -LiteralPath $RedirectStandardError
    $p=[pscustomobject]@{Handle=1;HasExited=$true;ExitCode=$(if($global:AdaptiveCase -eq 'curl-failure'){28}else{0})}
    $p|Add-Member ScriptMethod WaitForExit {}
    $p|Add-Member ScriptMethod Kill {$this.HasExited=$true}
    $p|Add-Member ScriptMethod Dispose {}
    return $p
}
foreach($case in @('success','high-rate','curl-failure','partial-start')){
    $global:AdaptiveCase=$case;$global:AdaptiveStarts=0;$global:AdaptiveStops=0;$global:AdaptivePingDisposed=$false
    $bundle=Join-Path $fixture $case;New-Item -ItemType Directory -Path $bundle|Out-Null
    '{"transport":"exchange","exchange_max_pps":6000,"exchange_burst_ms":200}'|Set-Content -LiteralPath (Join-Path $bundle 'client.json')
    $failed=$false
    try{& (Join-Path $fixture 'Measure-AdaptivePerformance.ps1') -Bundle $bundle -HighRate:($case -eq 'high-rate') 6>$null}catch{if($case -ne 'partial-start'){throw};$failed=$true}
    if($failed -ne ($case -eq 'partial-start') -or $global:AdaptiveStarts -ne 1 -or $global:AdaptiveStops -ne 1){throw ('Cleanup failed: '+$case)}
    if($case -ne 'partial-start'){
        if(-not $global:AdaptivePingDisposed){throw 'Ping object leaked'}
        $directory=Get-ChildItem -LiteralPath $bundle -Directory|Select-Object -First 1
        $report=Get-Content -LiteralPath (Join-Path $directory.FullName 'report.txt') -Raw
        $expected=if($case -in @('success','high-rate')){':true'}else{':false'}
        $count=if($case -eq 'high-rate'){9}else{8}
        if([regex]::Matches($report,'"complete_and_verified"'+$expected).Count -ne $count){throw ('Transfer result wrong: '+$case)}
        if(($case -eq 'high-rate') -ne $report.Contains('"target_mbps":75')){throw 'High-rate phase selection failed'}
        if(@(Get-ChildItem -LiteralPath $directory.FullName -File|Where-Object Extension -in '.body','.bin').Count){throw 'Synthetic files left behind'}
    }
    Write-Host ('PASS: '+$case)
}
Write-Host 'Four offline adaptive performance helper cases passed.'
