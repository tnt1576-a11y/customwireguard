[CmdletBinding()]
param([Parameter(Mandatory)][string]$BundleRoot)
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$destination=(Resolve-Path -LiteralPath $BundleRoot).Path
$compiler=Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$framework=Split-Path $compiler -Parent
$wpf=Join-Path $framework 'WPF'
if(-not (Test-Path $compiler)){throw 'Building CWG Desktop requires the Windows .NET Framework compiler.'}
$scratch=Join-Path $root ('.cache\desktop-build-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $scratch | Out-Null
$files=@('scripts/Desktop-Bridge.ps1','scripts/Import-CwgProfile.ps1','scripts/App-Tunnel.ps1','scripts/App-Tunnel.Common.ps1','scripts/App-EngineProcess.ps1','scripts/Install-AppEngine.ps1','bin/cwg-windows-amd64.exe')
$hashes=foreach($name in $files){(Get-FileHash -LiteralPath (Join-Path $destination $name)).Hash.ToLowerInvariant()+'  '+$name}
$hashFile=Join-Path $scratch 'tools.sha256';$hashes | Set-Content -LiteralPath $hashFile -Encoding ASCII
$references=@('System.dll','System.Core.dll','System.Xaml.dll','System.Web.Extensions.dll','System.Windows.Forms.dll','System.Drawing.dll') | ForEach-Object {'/reference:'+(Join-Path $framework $_)}
$references+=@('PresentationCore.dll','PresentationFramework.dll','WindowsBase.dll') | ForEach-Object {'/reference:'+(Join-Path $wpf $_)}
& $compiler /nologo /target:winexe /platform:x64 /optimize+ /utf8output ('/out:'+(Join-Path $destination 'CWG.exe')) ('/win32manifest:'+(Join-Path $root 'desktop\app.manifest')) ('/resource:'+(Join-Path $root 'desktop\MainWindow.xaml')+',Cwg.MainWindow.xaml') ('/resource:'+$hashFile+',Cwg.ToolHashes') @references (Join-Path $root 'desktop\Cwg.App.cs')
if($LASTEXITCODE -ne 0){throw 'CWG Desktop compilation failed.'}
Write-Output ('Built CWG.exe; SHA256 '+(Get-FileHash -LiteralPath (Join-Path $destination 'CWG.exe')).Hash)
