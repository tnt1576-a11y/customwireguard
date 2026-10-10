# Offline preflight fixtures. No accounts, services, firewall or network changes.
[CmdletBinding()]
param([switch]$CheckAcl,[string]$OpenSshArchive='')
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
$cases=@('plan','controller','virtual','routed','existing','public-address','empty-ssh','empty-state','empty-both','ssh-files','state-files','hidden-file','unreadable','directory-link','file-at-directory','resume','resume-no-switch','resume-complete','resume-account','resume-other-host','resume-extra-file','resume-missing-fields','bad-archive')
if($OpenSshArchive){$cases+='bundled-plan'}
foreach($caseName in $cases) {
    $global:CwgRemoteFixtureCase=$caseName
    $caseRoot=Join-Path $fixture $caseName
    New-Item -ItemType Directory -Path $caseRoot | Out-Null
    $caseSource=$source.Replace('$env:ProgramData',("'"+$caseRoot.Replace("'","''")+"'"))
    $caseSource.Replace('$env:ProgramFiles',("'"+$caseRoot.Replace("'","''")+"'")) | Set-Content -LiteralPath $copy -Encoding UTF8
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
    if($caseName -like 'resume*'){
        New-Item -ItemType Directory -Path $statePath | Out-Null
        $saved=[ordered]@{Version=1;Computer=$env:COMPUTERNAME;TestAddress='10.20.30.100';ControllerAddress='10.20.30.10';Port=2222;Account='cwgtest';Rule='CWG-RemoteTest-SSH';Complete=$false;AccountSID='';ConfigHash='';KeyHash=''}
        if($caseName -eq 'resume-complete'){$saved.Complete=$true}
        if($caseName -eq 'resume-account'){$saved.AccountSID='S-1-5-21-1'}
        if($caseName -eq 'resume-other-host'){$saved.Computer='different-test-host'}
        if($caseName -eq 'resume-missing-fields'){$saved.Remove('KeyHash')}
        if($caseName -eq 'resume-extra-file'){'unknown contents' | Set-Content -LiteralPath (Join-Path $statePath 'unknown.txt')}
        $saved | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $statePath 'setup-state.json')
    }
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
        {$_ -like 'resume-*'} {'Existing files found in '+$statePath}
        'bad-archive' {'archive hash does not match'}
    }
    $failure=$null
    $extra=@{}
    if($caseName -like 'resume*' -and $caseName -ne 'resume-no-switch'){$extra.ResumeFailedInstall=$true}
    if($caseName -eq 'bad-archive'){$extra.OpenSshArchive=$key}
    if($caseName -eq 'bundled-plan'){$extra.OpenSshArchive=$OpenSshArchive}
    try { & $copy -ControllerAddress $controller -TestAddress '10.20.30.100' -PublicKeyPath $key -WhatIf @extra } catch { $failure=$_.Exception.Message }
    if($caseName -in @('plan','empty-ssh','empty-state','empty-both','resume','bundled-plan')) {if($failure){throw $failure}}
    elseif(-not $failure -or -not $failure.Contains($expected)){throw "Wrong preflight failure for ${caseName}: $failure"}
    if($caseName -ne 'state-files' -and $caseName -notlike 'resume*' -and (Test-Path (Join-Path $statePath 'setup-state.json'))){throw 'Preflight created setup state'}
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
    Protect-AdminFile $aclRoot -AllowReadExecute
    $readRules=@((Get-Acl -LiteralPath $aclRoot).GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]))
    $readRule=@($readRules | Where-Object {$_.IdentityReference.Value -eq 'S-1-5-11'})
    if($readRules.Count -ne 3 -or $readRule.Count -ne 1 -or $readRule[0].FileSystemRights -ne 'ReadAndExecute, Synchronize' -or $readRule[0].InheritanceFlags -ne 'ContainerInherit, ObjectInherit'){throw 'Binary directory did not grant only read/execute to authenticated users'}
    Write-Host 'PASS: standalone binary directory grants authenticated users read/execute only.'
}
if($OpenSshArchive){
    # Validate the real executable and rendered setup configuration without
    # installing services, opening a listener or generating system host keys.
    $runtime=Join-Path $fixture 'runtime with spaces'
    Expand-Archive -LiteralPath $OpenSshArchive -DestinationPath $runtime
    $sshBin=Join-Path $runtime 'OpenSSH-Win64'
    foreach($file in @(Microsoft.PowerShell.Management\Get-ChildItem -LiteralPath $sshBin -File | Where-Object Extension -in '.exe','.dll','.ps1','.psm1')){
        $signature=Get-AuthenticodeSignature -LiteralPath $file.FullName
        if($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch '(^|,\s*)O=Microsoft Corporation(,|$)'){throw ('Invalid Microsoft signature: '+$file.Name)}
    }
    $hostKey=Join-Path $runtime 'test_host_key'
    $keygenProcess=Start-Process -FilePath (Join-Path $sshBin 'ssh-keygen.exe') -ArgumentList ('-q -t ed25519 -N "" -f "'+$hostKey+'"') -WindowStyle Hidden -Wait -PassThru
    if($keygenProcess.ExitCode -ne 0){throw 'Fixture host-key generation failed'}
    $ControllerAddress='10.20.30.10';$TestAddress='10.20.30.100';$Port=2222;$account='cwgtest'
    $template=[regex]::Match($source,'(?s)@"\r?\n(# Dedicated CWG test host.*?)\r?\n"@').Groups[1].Value
    if(-not $template){throw 'Setup SSH configuration template not found'}
    $config=$ExecutionContext.InvokeCommand.ExpandString($template)
    $config=$config.Replace('HostKey __PROGRAMDATA__/ssh/ssh_host_ed25519_key',('HostKey "'+$hostKey.Replace('\','/')+'"'))
    $configPath=Join-Path $runtime 'sshd_config'
    $config | Set-Content -LiteralPath $configPath -Encoding ASCII
    & (Join-Path $sshBin 'sshd.exe') -t -f $configPath
    if($LASTEXITCODE -ne 0){throw 'Bundled sshd rejected setup configuration'}
    $effective=& (Join-Path $sshBin 'sshd.exe') -T -f $configPath
    if($LASTEXITCODE -ne 0){throw 'Bundled sshd effective configuration failed'}
    foreach($setting in @('port 2222','passwordauthentication no','authenticationmethods publickey','allowusers cwgtest@10.20.30.10','allowtcpforwarding no','allowagentforwarding no')){
        if($effective -notcontains $setting){throw "Effective configuration missing: $setting"}
    }
    Write-Host 'PASS: signed bundled sshd accepts the exact setup config and expected authentication settings; no listener was started.'
}
