#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess)]
param(
    [Parameter(Mandatory)][string]$ControllerAddress,
    [Parameter(Mandatory)][string]$TestAddress,
    [Parameter(Mandatory)][string]$PublicKeyPath,
    [ValidateRange(1024,65535)][int]$Port=2222
)
$ErrorActionPreference='Stop'
function Get-PrivateIPv4([string]$Value) {
    $ip=[Net.IPAddress]::Parse($Value)
    $b=$ip.GetAddressBytes()
    if($b.Length -ne 4 -or -not ($b[0] -eq 10 -or ($b[0] -eq 172 -and $b[1] -ge 16 -and $b[1] -le 31) -or ($b[0] -eq 192 -and $b[1] -eq 168))) { throw 'Use a private LAN IPv4 address.' }
    return $ip.IPAddressToString
}
$ControllerAddress=Get-PrivateIPv4 $ControllerAddress
$TestAddress=Get-PrivateIPv4 $TestAddress
if($ControllerAddress -eq $TestAddress){throw 'The controller and test PC must be different computers.'}
if(-not [Environment]::Is64BitProcess -or [Environment]::OSVersion.Version.Build -lt 17763){throw 'Use 64-bit Windows PowerShell on Windows 10 1809 or later.'}
$key=(Get-Content -LiteralPath (Resolve-Path -LiteralPath $PublicKeyPath).Path -Raw).Trim()
if($key -notmatch '^ssh-ed25519 [A-Za-z0-9+/]+={0,2}(?: [A-Za-z0-9_.@-]+)?$'){throw 'Expected one Ed25519 public key line.'}
# Reject the controller PC before any install, account, firewall or service change.
$addresses=@(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop)
if(@($addresses | Where-Object IPAddress -eq $ControllerAddress).Count){throw 'This is the controller PC. Run setup on the separate test PC through RDP.'}
$local=@($addresses | Where-Object IPAddress -eq $TestAddress)
if($local.Count -ne 1){throw 'The specified test address is not assigned to exactly one local interface.'}
$adapter=Get-NetAdapter -ErrorAction Stop | Where-Object ifIndex -eq $local[0].InterfaceIndex | Select-Object -First 1
if(-not $adapter.HardwareInterface){throw 'The test address must be on a physical LAN adapter.'}
$route=Find-NetRoute -RemoteIPAddress $ControllerAddress | Where-Object {$_.CimClass.CimClassName -eq 'MSFT_NetRoute'} | Select-Object -First 1
if(-not $route -or $route.InterfaceIndex -ne $adapter.ifIndex -or $route.NextHop -ne '0.0.0.0'){throw 'The controller must be directly reachable on this LAN interface.'}
$sshRoot=Join-Path $env:ProgramData 'ssh'
$stateRoot=Join-Path $env:ProgramData 'CWGRemoteTest'
$statePath=Join-Path $stateRoot 'setup-state.json'
$configPath=Join-Path $sshRoot 'sshd_config'
$keyPath=Join-Path $sshRoot 'administrators_authorized_keys'
$account='cwgtest'
$rule='CWG-RemoteTest-SSH'
$existingService=Get-Service sshd -ErrorAction SilentlyContinue
if($existingService){
    throw ('Existing OpenSSH service found (status: '+$existingService.Status+'). Nothing changed; inspect that installation first.')
}
foreach($directory in @($sshRoot,$stateRoot)){
    if(Test-Path -LiteralPath $directory -ErrorAction Stop){
        $item=Get-Item -LiteralPath $directory -Force -ErrorAction Stop
        if(-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)){
            throw ('Expected an ordinary directory at '+$directory+'. Nothing changed.')
        }
        # An empty leftover directory is not an installed server. Include hidden
        # entries and fail if listing is denied; never infer emptiness from an error.
        $entries=@(Get-ChildItem -LiteralPath $directory -Force -ErrorAction Stop)
        if($entries.Count){
            throw ('Existing files found in '+$directory+' ('+$entries.Count+' entries). Nothing changed; inspect that directory first.')
        }
    }
}
if((Get-LocalUser -Name $account -ErrorAction SilentlyContinue) -or (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue)) {throw 'The reserved test account or firewall rule already exists. Nothing changed.'}
if(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue){throw 'The requested SSH port is already in use.'}
if(-not $PSCmdlet.ShouldProcess($env:COMPUTERNAME,"Install LAN-only SSH on ${TestAddress}:$Port for $ControllerAddress using a dedicated administrator account")){return}

function Protect-AdminFile([string]$Path) {
    $item=Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Refusing to set permissions through a directory link.'}
    $acl=if($item.PSIsContainer){New-Object Security.AccessControl.DirectorySecurity}else{New-Object Security.AccessControl.FileSecurity}
    $inheritance=if($item.PSIsContainer){[Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{[Security.AccessControl.InheritanceFlags]::None}
    $acl.SetAccessRuleProtection($true,$false)
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    foreach($sid in @('S-1-5-18','S-1-5-32-544')){
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),[Security.AccessControl.FileSystemRights]::FullControl,$inheritance,[Security.AccessControl.PropagationFlags]::None,[Security.AccessControl.AccessControlType]::Allow))
    }
    # Replace the complete DACL, including any explicit broad grants left on an
    # empty reused folder. Removing inheritance alone would retain those grants.
    Set-Acl -LiteralPath $Path -AclObject $acl -ErrorAction Stop
}
New-Item -ItemType Directory -Path $stateRoot -Force | Out-Null
Protect-AdminFile $stateRoot
$state=[ordered]@{Version=1;Computer=$env:COMPUTERNAME;TestAddress=$TestAddress;ControllerAddress=$ControllerAddress;Port=$Port;Account=$account;AccountSID='';Rule=$rule;ConfigHash='';KeyHash='';Complete=$false;CreatedUTC=[DateTime]::UtcNow.ToString('o')}
function Save-State { $state | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding UTF8 }
Save-State
try {
    foreach($capability in @('OpenSSH.Client~~~~0.0.1.0','OpenSSH.Server~~~~0.0.1.0')) {
        if((Get-WindowsCapability -Online -Name $capability).State -ne 'Installed'){
            $installed=Add-WindowsCapability -Online -Name $capability
            if($installed.RestartNeeded){throw 'Windows requires a restart to complete OpenSSH installation. Access has not been enabled.'}
        }
    }
    # Installation may create a broad port22 rule. This is a fresh server only.
    Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue | Disable-NetFirewallRule | Out-Null
    Stop-Service sshd -ErrorAction Stop
    Set-Service sshd -StartupType Manual
    $sshBin=Join-Path $env:WINDIR 'System32\OpenSSH'
    $sshd=Join-Path $sshBin 'sshd.exe'
    $keygen=Join-Path $sshBin 'ssh-keygen.exe'
    & $keygen -lf (Resolve-Path -LiteralPath $PublicKeyPath).Path | Out-Null
    if($LASTEXITCODE -ne 0){throw 'OpenSSH rejected the public key.'}
    New-Item -ItemType Directory -Path $sshRoot -Force | Out-Null
    Protect-AdminFile $sshRoot
    & $keygen -A
    if($LASTEXITCODE -ne 0){throw 'SSH host key generation failed.'}
    Protect-AdminFile (Join-Path $sshRoot 'ssh_host_ed25519_key')
    $random=New-Object byte[] 48
    $rng=[Security.Cryptography.RandomNumberGenerator]::Create()
    try{$rng.GetBytes($random)}finally{$rng.Dispose()}
    $password=ConvertTo-SecureString ('Cwg!7a'+[Convert]::ToBase64String($random)) -AsPlainText -Force
    $user=New-LocalUser -Name $account -Password $password -PasswordNeverExpires -UserMayNotChangePassword -Description 'Dedicated CWG remote test account'
    $state.AccountSID=$user.SID.Value;Save-State
    $admins=Get-LocalGroup -SID 'S-1-5-32-544'
    Add-LocalGroupMember -Group $admins -Member $user
    $key | Set-Content -LiteralPath $keyPath -Encoding ASCII
    Protect-AdminFile $keyPath
    $state.KeyHash=(Get-FileHash -LiteralPath $keyPath).Hash;Save-State
    if(Test-Path -LiteralPath $configPath){Copy-Item -LiteralPath $configPath -Destination (Join-Path $stateRoot 'installation-default-sshd_config')}
    @"
# Dedicated CWG test host. Managed by Setup-RemoteTestHost.ps1.
Port $Port
AddressFamily inet
ListenAddress $TestAddress
HostKey __PROGRAMDATA__/ssh/ssh_host_ed25519_key
PubkeyAuthentication yes
PasswordAuthentication no
AuthenticationMethods publickey
PermitEmptyPasswords no
AllowUsers $account@$ControllerAddress
AuthorizedKeysFile __PROGRAMDATA__/ssh/administrators_authorized_keys
AllowTcpForwarding no
AllowAgentForwarding no
GatewayPorts no
X11Forwarding no
MaxAuthTries 3
LoginGraceTime 20
Subsystem sftp sftp-server.exe
"@ | Set-Content -LiteralPath $configPath -Encoding ASCII
    Protect-AdminFile $configPath
    $state.ConfigHash=(Get-FileHash -LiteralPath $configPath).Hash;Save-State
    & $sshd -t -f $configPath
    if($LASTEXITCODE -ne 0){throw 'OpenSSH configuration validation failed.'}
    New-NetFirewallRule -Name $rule -DisplayName 'CWG tests: SSH from designated LAN controller only' -Direction Inbound -Action Allow -Protocol TCP -LocalPort $Port -LocalAddress $TestAddress -RemoteAddress $ControllerAddress -InterfaceAlias $adapter.Name -Program $sshd -Profile Any -EdgeTraversalPolicy Block | Out-Null
    Set-Service sshd -StartupType Automatic
    Start-Service sshd
    (Get-Service sshd).WaitForStatus('Running',[TimeSpan]::FromSeconds(15))
    $listener=@(Get-NetTCPConnection -State Listen -LocalPort $Port | Where-Object LocalAddress -eq $TestAddress)
    if(-not $listener.Count){throw 'SSH did not listen on the designated LAN address.'}
    $state.Complete=$true;Save-State
    Write-Host "READY: $account@$TestAddress port $Port; permitted controller $ControllerAddress"
    Write-Host 'Copy the following host fingerprint back to the chat (it is public):'
    & $keygen -lf (Join-Path $sshRoot 'ssh_host_ed25519_key.pub')
    Write-Host 'No VPN, route, IPv6 binding, RDP or router settings were changed. Keep this PC awake.'
    Write-Host 'To revoke this access, run Revoke-RemoteTestHost.ps1 as Administrator on this PC.'
} catch {
    $problem=$_
    Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue | Disable-NetFirewallRule -ErrorAction SilentlyContinue | Out-Null
    Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue | Disable-NetFirewallRule -ErrorAction SilentlyContinue | Out-Null
    if(Get-Service sshd -ErrorAction SilentlyContinue){Stop-Service sshd -ErrorAction SilentlyContinue;Set-Service sshd -StartupType Disabled -ErrorAction SilentlyContinue}
    if($state.AccountSID){Get-LocalUser -SID $state.AccountSID -ErrorAction SilentlyContinue | Disable-LocalUser -ErrorAction SilentlyContinue}
    Save-State
    throw $problem
}
