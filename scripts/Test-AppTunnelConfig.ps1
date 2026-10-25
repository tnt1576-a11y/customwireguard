$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'App-Tunnel.Common.ps1')
function Assert($Condition,[string]$Message){if(-not $Condition){throw $Message}}
$key=[Convert]::ToBase64String([byte[]](1..32))
$native="[Interface]`nPrivateKey = $key`nAddress = 10.77.0.2/32`nMTU = 1088`nListenPort = 51822`n[Peer]`nPublicKey = $key`nPresharedKey = $key`nEndpoint = 127.0.0.1:51821`n"
$carrier=[pscustomobject]@{listen='127.0.0.1:51821';wireguard='127.0.0.1:51822'}
$settings=[pscustomobject]@{mode='Include';applications=@([pscustomobject]@{kind='exe';path='C:\Test Apps\Browser.exe'},[pscustomobject]@{kind='folder';path='C:\Other App'})}
$cfg=New-CwgAppConfig $native $carrier $settings 'Ethernet' @('192.168.50.0/24')
Assert ($cfg.route.final -eq 'direct') 'Include must default to direct.'
Assert ($cfg.route.rules[0].action -eq 'hijack-dns') 'DNS must have an explicit tunnel policy.'
Assert ($cfg.route.rules[1].action -eq 'reject' -and $cfg.route.rules[1].ip_version -eq 6) 'Selected IPv6 must be rejected before WireGuard.'
Assert ($cfg.route.rules[2].outbound -eq 'cwg') 'Selected app must use WireGuard.'
$patterns=$cfg.route.rules[2].process_path_regex
Assert ('c:\test apps\browser.exe' -match $patterns[0]) 'Exact exe matching must ignore Windows case.'
Assert ('C:\Test Apps\Browser.exe.evil.exe' -notmatch $patterns[0]) 'Exact exe must not match a suffix.'
Assert ('C:\Other App\Helpers\child.exe' -match $patterns[1]) 'Folder must include child executables.'
Assert ('C:\Other App2\child.exe' -notmatch $patterns[1]) 'Folder must not match a sibling.'
$settings.mode='Exclude';$cfg=New-CwgAppConfig $native $carrier $settings 'Ethernet' @()
Assert ($cfg.route.final -eq 'cwg' -and $cfg.route.rules[1].outbound -eq 'direct') 'Exclude must default to WireGuard after bypass matches.'
Assert ($cfg.route.rules[2].action -eq 'reject') 'Unselected IPv6 must be rejected in exclude mode.'
$settings.mode='All';$cfg=New-CwgAppConfig $native $carrier $settings 'Ethernet' @()
Assert ($cfg.route.rules.Count -eq 2 -and $cfg.route.final -eq 'cwg') 'All mode must ignore the application list.'
$settings.mode='Include';$settings.applications=@();$rejected=$false
try{$null=New-CwgAppConfig $native $carrier $settings 'Ethernet' @()}catch{$rejected=$true}
Assert $rejected 'An empty Include mode must be refused.'
$settings.applications=@([pscustomobject]@{kind='exe';path='C:\App.exe'})
$rejected=$false;try{$null=New-CwgAppConfig ($native.Replace('51822','51823')) $carrier $settings 'Ethernet' @()}catch{$rejected=$true}
Assert $rejected 'Mismatched WireGuard ports must be refused.'
Assert ((Get-CwgIPv4Prefix '192.168.50.123' 24) -eq '192.168.50.0/24') 'LAN prefix calculation failed.'
Assert ((Get-CwgIPv4Prefix '10.19.45.2' 20) -eq '10.19.32.0/20') 'Non-octet prefix calculation failed.'
$temporary=Join-Path ([IO.Path]::GetTempPath()) ('cwg-app-config-'+[guid]::NewGuid().ToString('N')+'.json')
try{
    Write-CwgJson $temporary @{status='Starting'}
    Write-CwgJson $temporary @{status='Running'}
    Assert ((Read-CwgJson $temporary).status -eq 'Running') 'Atomic state replacement failed.'
}finally{if(Test-Path $temporary){Remove-Item -LiteralPath $temporary}}
Write-Output 'Application routing configuration checks passed; no networking was changed.'
