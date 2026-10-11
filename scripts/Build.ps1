[CmdletBinding()]
param([string]$Version = '0.6.3')
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^\d+\.\d+\.\d+(-[A-Za-z0-9.]+)?$') { throw 'Invalid version.' }
$root = Split-Path $PSScriptRoot -Parent
$go = Join-Path $root '.tools\go\bin\go.exe'
if (-not (Test-Path -LiteralPath $go)) { $go = (Get-Command go -ErrorAction Stop).Source }
$previousLocation = Get-Location
$saved = @{}
foreach ($name in @('GOCACHE','GOMODCACHE','GOTELEMETRY','CGO_ENABLED','GOOS','GOARCH')) { $saved[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
function Invoke-Go {
    & $go @args
    if ($LASTEXITCODE -ne 0) { throw ('go command failed: ' + ($args -join ' ')) }
}
function Copy-TreeFiles([string]$Directory,[string]$DestinationRoot) {
    $origin=Join-Path $root $Directory
    Get-ChildItem -LiteralPath $origin -Recurse -File | Where-Object { $_.Extension -in @('.go','.ps1','.py','.sh','.md','.service','.socket','.nft','.example','.cs','.xaml','.manifest') } | ForEach-Object {
        $relative=$_.FullName.Substring($root.Length+1)
        $destination=Join-Path $DestinationRoot $relative
        New-Item -ItemType Directory -Path (Split-Path $destination -Parent) -Force | Out-Null
        Copy-Item -LiteralPath $_.FullName -Destination $destination
    }
}
try {
    Set-Location -LiteralPath $root
    $env:GOCACHE=Join-Path $root '.cache\go-build'
    $env:GOMODCACHE=Join-Path $root '.cache\go-mod'
    $env:GOTELEMETRY='off'
    $env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
    $gofmt=Join-Path (Split-Path $go -Parent) 'gofmt.exe'
    $sources=Get-ChildItem -LiteralPath (Join-Path $root 'cmd'),(Join-Path $root 'internal') -Filter '*.go' -Recurse -File
    $unformatted=& $gofmt -l @($sources.FullName)
    if ($unformatted) { throw 'Go source is not formatted; run gofmt first.' }
    Invoke-Go mod verify
    Invoke-Go vet ./...
    Invoke-Go test ./... -count=1
    $stamp=[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')
    $release=Join-Path $root ("dist\cwg-$Version-$stamp")
    if (Test-Path -LiteralPath $release) { throw 'Release directory already exists.' }
    New-Item -ItemType Directory -Path (Join-Path $release 'bin') -Force | Out-Null
    foreach ($target in @(@('windows','amd64','.exe'),@('linux','amd64',''),@('linux','arm64',''))) {
        $env:GOOS=$target[0]; $env:GOARCH=$target[1]
        $destination=Join-Path $release ("bin\cwg-$($target[0])-$($target[1])$($target[2])")
        Invoke-Go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$Version" -o $destination ./cmd/cwg
    }
    foreach ($directory in @('scripts','deploy','docs','examples')) { Copy-TreeFiles $directory $release }
    & (Join-Path $PSScriptRoot 'Build-Desktop.ps1') -BundleRoot $release
    Copy-Item -LiteralPath (Join-Path $root 'README.md') -Destination $release
    $licenses=Join-Path $release 'THIRD-PARTY-LICENSES'
    New-Item -ItemType Directory -Path $licenses | Out-Null
    Copy-Item -LiteralPath (Join-Path (Split-Path (Split-Path $go -Parent) -Parent) 'LICENSE') -Destination (Join-Path $licenses 'Go.txt')
    $env:GOOS='windows'; $env:GOARCH='amd64'
    foreach ($module in @('golang.org/x/crypto','golang.org/x/sys','golang.org/x/net','golang.org/x/text','github.com/quic-go/quic-go','github.com/quic-go/masque-go','github.com/quic-go/qpack','github.com/yosida95/uritemplate/v3','github.com/dunglas/httpsfv')) {
        $moduleDir=& $go list -m -f '{{.Dir}}' $module
        if ($LASTEXITCODE -ne 0) { throw 'Could not locate dependency license.' }
        $licenseFile = @('LICENSE','LICENSE.md','LICENSE.txt') | ForEach-Object { Join-Path $moduleDir $_ } | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
        if (-not $licenseFile) { throw ('No dependency license found for ' + $module) }
        Copy-Item -LiteralPath $licenseFile -Destination (Join-Path $licenses (($module -replace '/','-') + '.txt'))
    }
    $goVersion=& $go version
    [ordered]@{version=$Version;built_utc=[DateTime]::UtcNow.ToString('o');compiler=$goVersion;tests='go mod verify; go vet ./...; go test ./... -count=1';linux_runtime_tested=$false} |
        ConvertTo-Json | Set-Content -LiteralPath (Join-Path $release 'BUILD-INFO.json') -Encoding UTF8
    $checksums=Get-ChildItem -LiteralPath $release -Recurse -File | Sort-Object FullName | ForEach-Object {
        $relative=$_.FullName.Substring($release.Length+1).Replace('\','/')
        '{0}  {1}' -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(),$relative
    }
    $checksums | Set-Content -LiteralPath (Join-Path $release 'SHA256SUMS') -Encoding ASCII
    $archive=$release + '.zip'
    Compress-Archive -LiteralPath $release -DestinationPath $archive
    $sourceDir=$release + '-source'
    New-Item -ItemType Directory -Path $sourceDir | Out-Null
    foreach ($directory in @('cmd','internal','scripts','deploy','docs','examples','desktop')) { Copy-TreeFiles $directory $sourceDir }
    foreach ($file in @('README.md','go.mod','go.sum','.gitignore')) { Copy-Item -LiteralPath (Join-Path $root $file) -Destination $sourceDir }
    $sourceArchive=$sourceDir + '.zip'
    Compress-Archive -LiteralPath $sourceDir -DestinationPath $sourceArchive
    foreach ($zip in @($archive,$sourceArchive)) {
        ('{0}  {1}' -f (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant(),[IO.Path]::GetFileName($zip)) |
            Set-Content -LiteralPath ($zip + '.sha256') -Encoding ASCII
    }
    $release | Set-Content -LiteralPath (Join-Path $root 'dist\latest.txt') -Encoding UTF8
    Write-Host "Release: $archive"
    Write-Host "Source:  $sourceArchive"
} finally {
    Set-Location -LiteralPath $previousLocation
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name,$saved[$name],'Process') }
}
