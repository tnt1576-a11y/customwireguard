# Offline preflight fixtures. No accounts, services, firewall or network changes.
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\remote-setup-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content (Join-Path $PSScriptRoot 'Setup-RemoteTestHost.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
$source=$source.Replace('$env:ProgramData',("'"+$fixture.Replace("'","''")+"'"))
$copy=Join-Path $fixture 'Setup-RemoteTestHost.ps1'
$source | Set-Content -LiteralPath $copy -Encoding UTF8
$key=Join-Path $fixture 'controller.pub'
'ssh-ed25519 AAAA fixture' | Set-Content -LiteralPath $key -Encoding ASCII
function Get-NetIPAddress {param($AddressFamily)
    $ip=if($global:CwgRemoteFixtureCase -eq 'controller'){'10.20.30.10'}else{'10.20.30.100'}
    [pscustomobject]@{IPAddress=$ip;InterfaceIndex=15}
}
function Get-NetAdapter {[pscustomobject]@{ifIndex=15;HardwareInterface=($global:CwgRemoteFixtureCase -ne 'virtual');Name='Fixture Ethernet'}}
function Find-NetRoute {param($RemoteIPAddress)
    [pscustomobject]@{CimClass=[pscustomobject]@{CimClassName='MSFT_NetRoute'};InterfaceIndex=15;NextHop=$(if($global:CwgRemoteFixtureCase -eq 'routed'){'10.20.30.1'}else{'0.0.0.0'})}
}
function Get-Service {param($Name);if($global:CwgRemoteFixtureCase -eq 'existing'){[pscustomobject]@{Name='sshd'}}}
function Get-LocalUser {param($Name)}
function Get-NetFirewallRule {param($Name)}
function Get-NetTCPConnection {param($LocalPort,$State)}
function Add-WindowsCapability {throw 'Unexpected Windows installation in preflight fixture'}
function New-LocalUser {throw 'Unexpected account mutation in preflight fixture'}
function Set-Service {throw 'Unexpected service mutation in preflight fixture'}
function New-NetFirewallRule {throw 'Unexpected firewall mutation in preflight fixture'}
foreach($caseName in @('plan','controller','virtual','routed','existing','public-address')) {
    $global:CwgRemoteFixtureCase=$caseName
    $controller=if($caseName -eq 'public-address'){'203.0.113.10'}else{'10.20.30.10'}
    $expected=switch($caseName){
        'controller' {'This is the controller PC'}
        'virtual' {'physical LAN adapter'}
        'routed' {'directly reachable'}
        'existing' {'Existing OpenSSH'}
        'public-address' {'private LAN IPv4'}
    }
    $failure=$null
    try { & $copy -ControllerAddress $controller -TestAddress '10.20.30.100' -PublicKeyPath $key -WhatIf } catch { $failure=$_.Exception.Message }
    if($caseName -eq 'plan') {if($failure){throw $failure}}
    elseif(-not $failure -or -not $failure.Contains($expected)){throw "Wrong preflight failure for ${caseName}: $failure"}
    if(Test-Path (Join-Path $fixture 'CWGRemoteTest')){throw 'Preflight created setup state'}
    Write-Host "PASS: $caseName"
}
foreach($name in @('Setup-RemoteTestHost.ps1','Revoke-RemoteTestHost.ps1')) {
    $tokens=$null;$errors=$null
    $null=[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $name),[ref]$tokens,[ref]$errors)
    if($errors.Count){throw ($errors | Out-String)}
}
Write-Host 'PASS: both scripts parse; all fixtures were offline and mutation-free.'
