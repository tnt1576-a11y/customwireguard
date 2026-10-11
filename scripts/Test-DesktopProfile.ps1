param([Parameter(Mandatory)][string]$Carrier)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
. (Join-Path $PSScriptRoot 'Import-CwgProfile.ps1')
Add-Type -AssemblyName System.IO.Compression.FileSystem
$work=Join-Path (Split-Path $PSScriptRoot -Parent) ('.cache\profile-test-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory (Join-Path $work 'bin') | Out-Null
Copy-Item -LiteralPath $Carrier -Destination (Join-Path $work 'bin\cwg-windows-amd64.exe')
& $Carrier init -endpoint 203.0.113.10:443 -out (Join-Path $work 'fixture') *> $null
if($LASTEXITCODE -ne 0){throw 'Fixture generation failed'}
$cfg=Read-CwgJson (Join-Path $work 'fixture\client.json')
$cfg | Add-Member transport 'exchange' -Force
$cfg | Add-Member exchange_shared $true -Force
$cfg | Add-Member exchange_max_pps 8000 -Force
$native=[IO.File]::ReadAllText((Join-Path $work 'fixture\cwg-full.conf')) -replace '(?m)^MTU\s*=.*','MTU = 1280'
$files=[ordered]@{'profile.json'='{"schema":1,"format":"cwg-profile","name":"alice"}';'client.json'=($cfg | ConvertTo-Json -Depth 12);'cwg-full.conf'=$native;'cwg-test.conf'=$native}
function Pack([string]$Name,$Entries){
 $path=Join-Path $work ($Name+'.cwg');$zip=[IO.Compression.ZipFile]::Open($path,'Create')
 try{foreach($entry in $Entries){$item=$zip.CreateEntry($entry.Name);$writer=New-Object IO.StreamWriter($item.Open(),(New-Object Text.UTF8Encoding($false)));try{$writer.Write([string]$entry.Value)}finally{$writer.Dispose()}}}finally{$zip.Dispose()}
 return $path
}
$good=Pack 'valid' @($files.GetEnumerator())
$result=Import-CwgProfile $good $work
if($result.Name -ne 'alice' -or (Read-CwgJson (Join-Path $result.Bundle 'apps.json')).mode -ne 'All'){throw 'Valid import failed'}
if(-not (Get-Acl -LiteralPath $result.Bundle).AreAccessRulesProtected){throw 'Profile permissions inherit'}
$cases=@('traversal','duplicate','directive','oversize','metadata','ports','carrier')
foreach($case in $cases){
 $entries=@($files.GetEnumerator() | ForEach-Object {[pscustomobject]@{Name=$_.Name;Value=$_.Value}})
 switch($case){
  traversal {$entries[0].Name='../profile.json'}
  duplicate {$entries[0].Name='client.json'}
  directive {$entries[2].Value+="`nPostUp = unwanted-command"}
  oversize {$entries[2].Value='x'*17000}
  metadata {$entries[0].Value='{"schema":1,"format":"cwg-profile","name":"../bad"}'}
  ports {$entries[2].Value=$entries[2].Value.Replace('Endpoint = 127.0.0.1:51821','Endpoint = 127.0.0.1:51999')}
  carrier {$bad=$entries[1].Value | ConvertFrom-Json;$bad.endpoint='bad';$entries[1].Value=$bad | ConvertTo-Json -Depth 12}
 }
 $rejected=$false;try{$null=Import-CwgProfile (Pack $case $entries) $work}catch{$rejected=$true}
 if(-not $rejected){throw ('Accepted invalid profile: '+$case)}
}
if(@(Get-ChildItem (Join-Path $work 'profiles') -Directory).Count -ne 1){throw 'Failed import left a partial profile'}
Write-Output 'Desktop profile checks passed: valid import, private ACL, seven invalid archives, failed-import cleanup. No networking changed.'
