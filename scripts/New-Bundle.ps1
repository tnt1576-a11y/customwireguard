[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Endpoint,
    [ValidatePattern('^[a-zA-Z0-9_-]{1,32}$')][string]$Name = 'server1',
    [ValidateSet('balanced','minimal')][string]$Profile = 'balanced',
    [string]$Executable = (Join-Path (Split-Path $PSScriptRoot -Parent) 'bin\cwg-windows-amd64.exe')
)
$ErrorActionPreference = 'Stop'
$base = Join-Path (Split-Path $PSScriptRoot -Parent) 'private'
if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) { throw 'Build/extract the release first, or specify -Executable.' }
New-Item -ItemType Directory -Force -Path $base | Out-Null
# Protect the parent BEFORE the executable writes keys into a new child folder.
$acl = New-Object System.Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true, $false)
$rights = [System.Security.AccessControl.FileSystemRights]::FullControl
$inherit = [System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
$propagate = [System.Security.AccessControl.PropagationFlags]::None
$allow = [System.Security.AccessControl.AccessControlType]::Allow
$identities = @([System.Security.Principal.WindowsIdentity]::GetCurrent().User,
    [System.Security.Principal.SecurityIdentifier]::new('S-1-5-18'),
    [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
foreach ($identity in $identities) {
    $acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($identity,$rights,$inherit,$propagate,$allow))
}
Set-Acl -LiteralPath $base -AclObject $acl
$destination = Join-Path $base $Name
& $Executable init -endpoint $Endpoint -out $destination -profile $Profile
if ($LASTEXITCODE -ne 0) { throw 'Bundle generation failed; existing bundles are never overwritten.' }
Write-Host "Created private bundle: $destination"
Write-Host 'Transfer only server.json and cwg0.conf to the Linux server using SSH.'
