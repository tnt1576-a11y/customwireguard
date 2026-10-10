# Windows test PC over the local network

Use a second Windows PC so full-tunnel transitions cannot disconnect the Codex
controller. Windows 10 1809 or later supports Microsoft's optional OpenSSH server.
The controller uses plain SSH commands and SFTP; no RDP password, WinRM trust
changes, router port forwarding or public SSH listener is needed.

## One-time access setup

Generate a dedicated Ed25519 key on the controller. Keep its private half there;
copy only the public key, Setup-RemoteTestHost.ps1 and Revoke-RemoteTestHost.ps1
to the test PC through the user's existing RDP session. Run setup from 64-bit
Administrator Windows PowerShell, providing the controller/test LAN IPv4
addresses and public-key path. TCP port 2222 is the default.

The setup refuses the controller PC, a nonphysical or routed management path,
existing OpenSSH server files/service, an existing reserved account/rule or an
occupied port. Empty ordinary SSH/state folders are allowed; hidden entries,
unreadable folders, directory links and files at those paths cause setup to stop
with a specific error before installation. It installs the Windows capabilities
if missing, creates the local cwgtest administrator account with an undisclosed random password, disables
SSH password authentication, and accepts only the supplied public key from the
controller address. Administrative access is required for tunnel services and
test route changes; this is full administrative access to the test PC.

The listener binds only the test PC's LAN IPv4 address. Its firewall rule limits
the source, local address, interface, program and port, with edge traversal
disabled. A newly installed broad OpenSSH port22 rule is disabled. Key/config
ACLs permit SYSTEM and Administrators. Reused empty folders receive a fresh
protected ACL, replacing any broad explicit grants as well as inherited ones.
The script validates the SSH configuration before starting it and prints the
public host-key fingerprint for verification
over RDP. Pin that fingerprint in a dedicated known_hosts file before automation;
do not disable host-key checks or send the controller private key to the test PC.

Installation failure disables the fresh service/rule and created test account.
Existing SSH installations are deliberately left untouched for inspection.
Revoke-RemoteTestHost.ps1 disables the exact saved account SID, removes the test
firewall rule, and stops/disables SSH only if its configuration still matches.
It removes the dedicated authorized-keys file only if unchanged. It preserves
account profiles, test results, host keys and optional Windows components.
DHCP address changes require updating the deliberately narrow access scope.
Keep the test PC awake during tests; setup does not change its power settings.

### When Windows optional-feature installation fails

Supply `-OpenSshArchive <path-to-OpenSSH-Win64.zip>` to use the standalone
Microsoft release instead of Windows Features on Demand. This revision pins
`10.0.0.0p2-Preview` and SHA-256
`23f50f3458c4c5d0b12217c6a5ddfde0137210a30fa870e98b29827f7b43aba5`.
Download it from the official release linked below. The deployment bundle may
include that archive so the test PC does not need to download it.

Setup checks the archive hash, copies it into the protected state folder,
rechecks it, then verifies the Microsoft signatures before installing files in
`%ProgramFiles%\CWGOpenSSH`. Only SYSTEM and Administrators can write the binary
folder; Authenticated Users can read/execute. The helper registers only `sshd`,
using the same required service privileges as Microsoft's installation script.
It does not run the upstream script, replace `ssh-agent`, change PATH or install
an MSI. The server starts only after the restricted configuration is validated
and the scoped firewall rule is in place. Logs go to
`%ProgramData%\CWGRemoteTest\sshd.log`. This standalone package needs manual
updates; it is not serviced as a Windows optional feature.

`-ResumeFailedInstall` permits a retry only when the state folder contains
exactly the initial incomplete setup JSON, its computer/address/account/rule
identity matches, and its account SID and key/config hashes are empty. Existing
SSH services, SSH files, accounts, firewall rules and binary folders still cause
setup to stop. The previous JSON is copied to a timestamped file before retrying.
It does not silently repair partially configured servers. Revocation disables
the same dedicated server/account/rule and preserves its installed binary folder.

The offline fixture script accepts `-OpenSshArchive` to validate the real signed
runtime and rendered configuration using `sshd -t/-T`, without opening a listener
or installing services. `-CheckAcl` verifies permissions on disposable test files
and requires an elevated process.

## Before allowing automated VPN tests

Access setup does not start a VPN or change routes, adapters, IPv6 bindings or
RDP. After key/host verification, inventory the test PC, verify administrator
rights, and stage/hash-check a private CWG kit and official WireGuard install.
Never run the shared single-client profile on both PCs simultaneously.

Verify a physical on-link route to the controller before and while the test
tunnel is active. The existing CWG IPv4 Full profile uses two /1 routes, which
leave more-specific LAN routes in effect. A native /0 WireGuard profile can
enable restrictive firewall behavior; do not assume a /32 route alone defeats
it. Verify RDP/SSH continuity, DNS and IPv6 behavior on this actual machine.

Install and verify a bounded cleanup task running locally on the test PC before
the first tunnel start. It must stop the specific CWG bundle if the test or SSH
control session fails, preserve other VPNs, and restore any temporary adapter
settings. The test itself must also clean up in finally. Capture pre/post route,
service, resource and transport counters; retrieve bounded results over the LAN.
Only after these checks should repeated tests start automatically. Interactive
video playback is separate from SSH's noninteractive session.

This revision prepares access and documents the test procedure. Remote host
installation, host identity, VPN continuity and the cleanup task are not verified
until the user runs setup and the controller connects to that machine.

Sources: [Microsoft installation](https://learn.microsoft.com/en-us/windows-server/administration/openssh/openssh_install_firstuse),
[server configuration](https://learn.microsoft.com/en-us/windows-server/administration/openssh/openssh_server_configuration),
and [WireGuard Windows routing/firewall behavior](https://github.com/WireGuard/wireguard-windows/blob/master/docs/netquirk.md).
Standalone package: [Microsoft Win32-OpenSSH release](https://github.com/PowerShell/Win32-OpenSSH/releases/tag/10.0.0.0p2-Preview)
and [ZIP installation documentation](https://github.com/PowerShell/Win32-OpenSSH/wiki/Install-Win32-OpenSSH).
