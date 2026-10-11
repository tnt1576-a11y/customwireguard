[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Release,
    [Parameter(Mandatory)][string]$EngineArchive,
    [Parameter(Mandatory)][string]$Destination
)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
Add-Type -AssemblyName System.IO.Compression.FileSystem
$releasePath=(Resolve-Path -LiteralPath $Release).Path
$archivePath=(Resolve-Path -LiteralPath $EngineArchive).Path
$target=[IO.Path]::GetFullPath($Destination)
if((Test-Path -LiteralPath $target) -or (Test-Path -LiteralPath ($target+'.zip'))){throw 'Choose a new package destination.'}
if((Get-FileHash -LiteralPath $archivePath).Hash -ne $script:CwgEngineArchiveSHA){throw 'The engine archive does not match the pinned official release.'}
$sums=@{}
foreach($line in [IO.File]::ReadAllLines((Join-Path $releasePath 'SHA256SUMS'))){
    if($line -notmatch '^([a-f0-9]{64})  (.+)$'){throw 'Invalid release checksum list.'}
    $sums[$Matches[2]]=$Matches[1]
}
$required=@('CWG.exe','bin/cwg-windows-amd64.exe')
$required+=@('Desktop-Bridge.ps1','Import-CwgProfile.ps1','App-Tunnel.ps1','App-Tunnel.Common.ps1','App-EngineProcess.ps1','Install-AppEngine.ps1') | ForEach-Object {'scripts/'+$_}
$licenses=@(Get-ChildItem -LiteralPath (Join-Path $releasePath 'THIRD-PARTY-LICENSES') -File | Sort-Object Name)
if(-not $licenses.Count){throw 'The release must include dependency licenses.'}
foreach($name in $required+@($licenses | ForEach-Object {'THIRD-PARTY-LICENSES/'+$_.Name})){
    if((Get-FileHash -LiteralPath (Join-Path $releasePath $name)).Hash -ne $sums[$name]){throw ('Release integrity check failed: '+$name)}
}
New-Item -ItemType Directory -Path $target | Out-Null
foreach($name in $required){
    $file=Join-Path $target $name
    New-Item -ItemType Directory -Path (Split-Path $file -Parent) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $releasePath $name) -Destination $file
}
# Store only the engine already used by the tested installer. Its optional
# libcronet.dll and the nested installer archive are unnecessary for CWG.
$engineDir=Join-Path $target 'bin\app-engine'
New-Item -ItemType Directory -Path $engineDir | Out-Null
$zip=[IO.Compression.ZipFile]::OpenRead($archivePath)
try{
    $prefix='sing-box-'+$script:CwgEngineVersion+'-windows-amd64/'
    $entry=$zip.GetEntry($prefix+'sing-box.exe')
    if(-not $entry){throw 'The archive is missing its engine.'}
    $inputStream=$entry.Open();$outputStream=[IO.File]::Create((Join-Path $engineDir 'sing-box.exe'))
    try{$inputStream.CopyTo($outputStream)}finally{$outputStream.Dispose();$inputStream.Dispose()}
    $entry=$zip.GetEntry($prefix+'LICENSE')
    if(-not $entry){throw 'The archive is missing its license.'}
    $reader=New-Object IO.StreamReader($entry.Open())
    try{$engineLicense=$reader.ReadToEnd()}finally{$reader.Dispose()}
}finally{$zip.Dispose()}
if((Get-FileHash -LiteralPath (Join-Path $engineDir 'sing-box.exe')).Hash -ne $script:CwgEngineSHA){throw 'The extracted engine does not match its pinned checksum.'}
$notices=New-Object Text.StringBuilder
$null=$notices.AppendLine('CWG third-party notices - retain this file when sharing the app.').AppendLine()
foreach($license in $licenses){
    $null=$notices.AppendLine('--- '+$license.Name+' ---').AppendLine([IO.File]::ReadAllText($license.FullName)).AppendLine()
}
$null=$notices.AppendLine('--- sing-box '+$script:CwgEngineVersion+' (unmodified upstream executable) ---').AppendLine($engineLicense)
$null=$notices.AppendLine('Release: https://github.com/SagerNet/sing-box/releases/tag/v'+$script:CwgEngineVersion)
$null=$notices.AppendLine('Corresponding source: https://github.com/SagerNet/sing-box/archive/refs/tags/v'+$script:CwgEngineVersion+'.tar.gz')
[IO.File]::WriteAllText((Join-Path $target 'THIRD-PARTY-NOTICES.txt'),$notices.ToString(),(New-Object Text.UTF8Encoding($false)))
$version=(Get-Content -LiteralPath (Join-Path $releasePath 'BUILD-INFO.json') -Raw | ConvertFrom-Json).version
$instructions=@"
CWG Desktop $version

1. Extract the entire folder. Keep its files together.
2. Double-click CWG.exe and accept the administrator prompt.
3. Import your private .cwg file from the server administrator.
4. Choose Full PC or Split tunnel, select your apps, and click Connect.

The verified routing engine is ready to use offline. No download or terminal commands are needed.
Disconnect restores normal networking. Closing while connected leaves CWG in the tray.
LAN access stays direct. Windows DNS uses CWG while connected; tunneled public IPv6 is blocked.
There is no kill switch after disconnecting.

This shareable app has no account credentials. Each simultaneous device needs its own profile.
Your administrator can create one using /add name in their Telegram bot.
Keep your .cwg file private. Do not share a folder after importing your profile into it.
This build is unsigned; obtain it from your trusted administrator.
License notices and routing-engine source links are in THIRD-PARTY-NOTICES.txt.
"@
[IO.File]::WriteAllText((Join-Path $target 'START-HERE.txt'),$instructions,(New-Object Text.UTF8Encoding($false)))
$checksums=Get-ChildItem -LiteralPath $target -Recurse -File | Sort-Object FullName | ForEach-Object {
    (Get-FileHash -LiteralPath $_.FullName).Hash.ToLowerInvariant()+'  '+$_.FullName.Substring($target.Length+1).Replace('\','/')
}
$checksums | Set-Content -LiteralPath (Join-Path $target 'SHA256SUMS') -Encoding ASCII
Compress-Archive -LiteralPath $target -DestinationPath ($target+'.zip') -CompressionLevel Optimal
((Get-FileHash -LiteralPath ($target+'.zip')).Hash.ToLowerInvariant()+'  '+[IO.Path]::GetFileName($target+'.zip')) | Set-Content -LiteralPath ($target+'.zip.sha256') -Encoding ASCII
Write-Output ('Shareable package: '+$target+'.zip')
