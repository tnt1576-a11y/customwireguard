# Offline preflight fixtures. No accounts, services, firewall or network changes.
[CmdletBinding()]
param([switch]$CheckAcl)
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
$fixture=Join-Path $root ('test-output\remote-setup-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
$source=(Get-Content (Join-Path $PSScriptRoot 'Setup-RemoteTestHost.ps1') -Raw).Replace('#Requires -RunAsAdministrator','# Offline fixture')
$copy=Join-Path $fixture 'Setup-RemoteTestHost.ps1'
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
function Get-Service {param($Name);if($global:CwgRemoteFixtureCase -eq 'existing'){[pscustomobject]@{Name='sshd';Status='Stopped'}}}
function Get-ChildItem {
    [CmdletBinding()]
    param([string]$LiteralPath,[switch]$Force)
    if($global:CwgRemoteFixtureCase -eq 'unreadable' -and (Split-Path $LiteralPath -Leaf) -eq 'ssh'){throw 'Fixture directory access denied'}
    Microsoft.PowerShell.Management\Get-ChildItem -LiteralPath $LiteralPath -Force:$Force -ErrorAction Stop
}
function Get-LocalUser {param($Name)}
function Get-NetFirewallRule {param($Name)}
function Get-NetTCPConnection {param($LocalPort,$State)}
function Add-WindowsCapability {throw 'Unexpected Windows installation in preflight fixture'}
function New-LocalUser {throw 'Unexpected account mutation in preflight fixture'}
function Set-Service {throw 'Unexpected service mutation in preflight fixture'}
function New-NetFirewallRule {throw 'Unexpected firewall mutation in preflight fixture'}
foreach($caseName in @('plan','controller','virtual','routed','existing','public-address','empty-ssh','empty-state','empty-both','ssh-files','state-files','hidden-file','unreadable','directory-link','file-at-directory')) {
    $global:CwgRemoteFixtureCase=$caseName
    $caseRoot=Join-Path $fixture $caseName
    New-Item -ItemType Directory -Path $caseRoot | Out-Null
    $source.Replace('$env:ProgramData',("'"+$caseRoot.Replace("'","''")+"'")) | Set-Content -LiteralPath $copy -Encoding UTF8
    $sshPath=Join-Path $caseRoot 'ssh'
    $statePath=Join-Path $caseRoot 'CWGRemoteTest'
    if($caseName -in @('empty-ssh','empty-both','ssh-files','hidden-file','unreadable')){New-Item -ItemType Directory -Path $sshPath | Out-Null}
    if($caseName -in @('empty-state','empty-both','state-files')){New-Item -ItemType Directory -Path $statePath | Out-Null}
    if($caseName -eq 'ssh-files'){'existing configuration' | Set-Content -LiteralPath (Join-Path $sshPath 'sshd_config')}
    if($caseName -eq 'state-files'){'existing state' | Set-Content -LiteralPath (Join-Path $statePath 'setup-state.json')}
    if($caseName -eq 'hidden-file'){
        $hiddenPath=Join-Path $sshPath 'hidden.txt'
        'hidden existing file' | Set-Content -LiteralPath $hiddenPath
        [IO.File]::SetAttributes($hiddenPath,[IO.FileAttributes]::Hidden)
    }
    if($caseName -eq 'directory-link'){
        $linkTarget=Join-Path $caseRoot 'link-target'
        New-Item -ItemType Directory -Path $linkTarget | Out-Null
        New-Item -ItemType Junction -Path $sshPath -Target $linkTarget | Out-Null
    }
    if($caseName -eq 'file-at-directory'){'ordinary file' | Set-Content -LiteralPath $sshPath}
    $controller=if($caseName -eq 'public-address'){'203.0.113.10'}else{'10.20.30.10'}
    $expected=switch($caseName){
        'controller' {'This is the controller PC'}
        'virtual' {'physical LAN adapter'}
        'routed' {'directly reachable'}
        'existing' {'Existing OpenSSH'}
        'public-address' {'private LAN IPv4'}
        'ssh-files' {'Existing files found in '+$sshPath}
        'state-files' {'Existing files found in '+$statePath}
        'hidden-file' {'Existing files found in '+$sshPath}
        'unreadable' {'Fixture directory access denied'}
        'directory-link' {'Expected an ordinary directory'}
        'file-at-directory' {'Expected an ordinary directory'}
    }
    $failure=$null
    try { & $copy -ControllerAddress $controller -TestAddress '10.20.30.100' -PublicKeyPath $key -WhatIf } catch { $failure=$_.Exception.Message }
    if($caseName -in @('plan','empty-ssh','empty-state','empty-both')) {if($failure){throw $failure}}
    elseif(-not $failure -or -not $failure.Contains($expected)){throw "Wrong preflight failure for ${caseName}: $failure"}
    if($caseName -ne 'state-files' -and (Test-Path (Join-Path $statePath 'setup-state.json'))){throw 'Preflight created setup state'}
    Write-Host "PASS: $caseName"
}
foreach($name in @('Setup-RemoteTestHost.ps1','Revoke-RemoteTestHost.ps1')) {
    $tokens=$null;$errors=$null
    $null=[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $name),[ref]$tokens,[ref]$errors)
    if($errors.Count){throw ($errors | Out-String)}
}
Write-Host 'PASS: both scripts parse; all preflight fixtures were offline and made no installation changes.'
if($CheckAcl){
    $identity=[Security.Principal.WindowsIdentity]::GetCurrent()
    $principal=[Security.Principal.WindowsPrincipal]::new($identity)
    if(-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run -CheckAcl in an elevated process; it modifies only disposable fixture ACLs.'}
    $ast=[Management.Automation.Language.Parser]::ParseInput($source,[ref]$null,[ref]$null)
    $function=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Protect-AdminFile'},$true)
    . ([scriptblock]::Create($function.Extent.Text))
    $aclRoot=Join-Path $fixture 'acl-check'
    New-Item -ItemType Directory -Path $aclRoot | Out-Null
    $aclFile=Join-Path $aclRoot 'fixture.txt'
    'disposable ACL fixture' | Set-Content -LiteralPath $aclFile
    foreach($path in @($aclRoot,$aclFile)){
        $acl=Get-Acl -LiteralPath $path
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-1-0'),[Security.AccessControl.FileSystemRights]::Read,[Security.AccessControl.AccessControlType]::Allow))
        Set-Acl -LiteralPath $path -AclObject $acl
        Protect-AdminFile $path
        $actual=Get-Acl -LiteralPath $path
        $rules=@($actual.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]))
        if(-not $actual.AreAccessRulesProtected -or $actual.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne 'S-1-5-32-544' -or $rules.Count -ne 2){throw "Incorrect ACL protection/owner/rule count: $path"}
        foreach($rule in $rules){
            if($rule.IdentityReference.Value -notin @('S-1-5-18','S-1-5-32-544') -or $rule.IsInherited -or $rule.AccessControlType -ne 'Allow' -or $rule.FileSystemRights -ne 'FullControl'){throw "Incorrect ACL rule: $path"}
        }
        $wantedInheritance=if($path -eq $aclRoot){[Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{[Security.AccessControl.InheritanceFlags]::None}
        if(@($rules | Where-Object InheritanceFlags -ne $wantedInheritance).Count){throw "Incorrect ACL inheritance: $path"}
    }
    Write-Host 'PASS: directory/file ACLs replace broad explicit grants with SYSTEM and Administrators only.'
}
