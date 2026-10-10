[CmdletBinding()]
param([string]$Destination,[string]$ArchivePath)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
if(-not $Destination){$Destination=Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\app-engine'}
if(-not [Environment]::Is64BitOperatingSystem -or $env:PROCESSOR_ARCHITECTURE -eq 'ARM64'){throw 'This pinned application engine supports Windows x64.'}
$destinationPath=[IO.Path]::GetFullPath($Destination)
if(Test-Path $destinationPath){
    $exe=Join-Path $destinationPath 'sing-box.exe'
    if((Test-Path $exe) -and (Get-FileHash $exe).Hash -eq $script:CwgEngineSHA){Write-Output 'Verified application engine is already installed.';return}
    throw 'The destination already exists with different contents. Choose an empty destination.'
}
$parent=Split-Path $destinationPath -Parent;New-Item -ItemType Directory -Force $parent | Out-Null
$stage=Join-Path $parent ('app-engine-download-'+[guid]::NewGuid().ToString('N'));New-Item -ItemType Directory $stage | Out-Null
$archive=Join-Path $stage 'release.zip'
[Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
$url="https://github.com/SagerNet/sing-box/releases/download/v$script:CwgEngineVersion/sing-box-$script:CwgEngineVersion-windows-amd64.zip"
if($ArchivePath){
    Copy-Item -LiteralPath (Resolve-Path -LiteralPath $ArchivePath).Path -Destination $archive
}else{Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $archive -TimeoutSec 180}
if((Get-FileHash $archive).Hash -ne $script:CwgEngineArchiveSHA){throw 'Downloaded engine checksum does not match the pinned official release.'}
Expand-Archive -LiteralPath $archive -DestinationPath (Join-Path $stage 'unpacked')
$source=Join-Path $stage "unpacked\sing-box-$script:CwgEngineVersion-windows-amd64"
if((Get-FileHash (Join-Path $source 'sing-box.exe')).Hash -ne $script:CwgEngineSHA){throw 'Extracted engine checksum mismatch.'}
$ready=Join-Path $stage 'ready';New-Item -ItemType Directory $ready | Out-Null
Copy-Item -LiteralPath (Join-Path $source 'sing-box.exe'),(Join-Path $source 'LICENSE') -Destination $ready
[IO.Directory]::Move($ready,$destinationPath)
# Keep the upstream archive and license; no driver or system service is installed here.
Write-Output "Installed sing-box $script:CwgEngineVersion for CWG application routing in $destinationPath."
