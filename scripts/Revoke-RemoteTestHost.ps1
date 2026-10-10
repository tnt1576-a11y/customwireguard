#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess)]
param()
$ErrorActionPreference='Stop'
$stateRoot=Join-Path $env:ProgramData 'CWGRemoteTest'
$statePath=Join-Path $stateRoot 'setup-state.json'
$state=Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
if($state.Version -ne 1 -or $state.Computer -ne $env:COMPUTERNAME -or $state.Account -ne 'cwgtest' -or $state.Rule -ne 'CWG-RemoteTest-SSH'){throw 'Unrecognized setup state; nothing changed.'}
if(-not $PSCmdlet.ShouldProcess($env:COMPUTERNAME,'Revoke CWG remote test access; preserve account files and diagnostic data')){return}
if($state.AccountSID){
    $user=Get-LocalUser -SID $state.AccountSID -ErrorAction SilentlyContinue
    if($user){if($user.Name -ne $state.Account){throw 'Account identity changed; inspect before continuing.'};Disable-LocalUser -InputObject $user}
}
Get-NetFirewallRule -Name $state.Rule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
$sshRoot=Join-Path $env:ProgramData 'ssh'
$configPath=Join-Path $sshRoot 'sshd_config'
if((Test-Path -LiteralPath $configPath) -and (Get-FileHash -LiteralPath $configPath).Hash -eq $state.ConfigHash){
    Stop-Service sshd
    Set-Service sshd -StartupType Disabled
} else {Write-Warning 'SSH configuration changed since setup; the test account is disabled, but the service was left unchanged.'}
$keyPath=Join-Path $sshRoot 'administrators_authorized_keys'
if((Test-Path -LiteralPath $keyPath) -and (Get-FileHash -LiteralPath $keyPath).Hash -eq $state.KeyHash){Remove-Item -LiteralPath $keyPath}
Write-Host 'CWG test access revoked. Account/profile files, setup state and installed OpenSSH components were preserved.'
