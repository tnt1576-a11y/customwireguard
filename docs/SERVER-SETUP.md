# Install on a fresh Debian server

`deploy/linux/setup.py` installs the tested request/reply transport with spare-slot
repair, WireGuard, forwarding, IPv4 NAT and systemd services. Use Debian 13 with
systemd and an amd64 or arm64 CPU. Linux amd64 was exercised in a fresh Debian 13
container with real kernel WireGuard; arm64 is cross-compiled, not runtime tested.

This is a **fresh installation**, not an upgrade command for an existing CWG
server. It refuses existing CWG files, reserved ports, firewall tables and
overlapping tunnel networks before touching them. The 0.6 development revision
creates an initial `owner` account and supports separate named accounts; see
[multi-user status and provisioning](MULTI-USER.md). Loaded WAN validation and
coordinated rate control are still pending. Use matching 0.6 binaries and source;
the published 0.5.8 installer remains the verified single-client release.
Do not share one account's ZIP between simultaneous users.

## Install

Transfer and extract the release ZIP to a private directory on the new server.
Use the package containing this document and `deploy/linux/setup.py`. Install
`python3` and `unzip` if the server does not already have them. Check the downloaded
archive checksum against the checksum supplied with your trusted release. The
installer also verifies the files it consumes against the extracted `SHA256SUMS`.

As root, enter the extracted release directory and inspect the outgoing route:

```sh
ip -4 route get 1.1.1.1
```

Use its `dev` interface name and the server's public IPv4 address below. For
example, replace `eth0` and `YOUR_SERVER_IPV4` with your own values:

```sh
python3 deploy/linux/setup.py install --release . --endpoint YOUR_SERVER_IPV4:443 --wan eth0
python3 /usr/local/lib/cwg/server-admin.py verify
```

The installer obtains missing `wireguard-tools`, `nftables`, `iproute2` and
`procps` packages through Debian APT. The server needs repository access. It does
not reboot the host, replace the global firewall, change SSH or edit the default
route. Allow **UDP 443** in your provider firewall and existing host firewall.
An accept in CWG's own nftables chain does not override a drop in another chain.
Existing forwarding policies must also permit `cwg0` to the selected WAN and
established return traffic. Keep your existing management connection available.

CWG binds to the selected WAN's local IPv4 address, using the public endpoint
when that address is local. This also supports a public address translated by a
provider to the WAN's private address. A later WAN address change requires
configuration work; the installer does not automatically migrate an installation.

The native WireGuard backend is UDP 51820 and is blocked on non-loopback
interfaces. Tunnel networks are `10.77.0.0/24` and `fd77:77:77::/64`. The generated
profiles use MTU 1280; this differs from the older, manually tuned 1088-byte
IPv4-only deployment. Full mode captures public IPv6, but this installer provides
IPv4 Internet egress only: IPv6 forwarding from the tunnel is rejected promptly
rather than falling back to the client's physical Internet connection.

## Give the client its private package

The result is `/etc/cwg/client.zip`, readable only by root. Copy it securely to
the intended Windows computer; do not publish it or commit it to GitHub. It
contains that client's keys, the verified Windows executable and start/stop
helpers. It excludes the server's WireGuard private key and installation state.

Install the official WireGuard Windows client, extract the ZIP, then open
Administrator PowerShell in the extracted `cwg-client` directory:

```powershell
.\scripts\Start-Client.ps1 -Bundle .\config -Mode Test
ping 10.77.0.1
.\scripts\Stop-Client.ps1 -Bundle .\config
.\scripts\Start-Client.ps1 -Bundle .\config -Mode Full
curl.exe --ipv4 --max-time 20 https://api.ipify.org
.\scripts\Stop-Client.ps1 -Bundle .\config
```

The probe checks the outer transport; the ping verifies WireGuard and the final
request verifies Internet forwarding. Full mode requires a physical route to
the server. Stop other VPNs before trying it. Stopping CWG removes the tunnel;
the helper does not provide a persistent kill switch. The export also includes
the [application-selection GUI](APPLICATION-CLIENT.md): stop native CWG, run
`.\scripts\Open-AppClient.ps1 -Bundle .\config`, install its pinned app engine,
and select programs or folders before connecting.

## Verify, repeat and recover

Running the identical install command again verifies the managed installation
and preserves its keys and client ZIP. Different endpoint, WAN or binary inputs
are refused. Changes made outside the installer are detected before uninstall
or identical-install verification; investigate them rather than deleting the
state file. The `verify` command checks managed hashes, active services, owned
firewall tables, forwarding settings and the native interface. It does not prove
reachability through an ISP or a provider firewall.

```sh
python3 /usr/local/lib/cwg/server-admin.py verify
systemctl status cwg cwg-guard wg-quick@cwg0
journalctl -u cwg -n 20 --no-pager
```

An ordinary failed installation stops its services, removes its firewall tables
and restores the previous live forwarding settings. It also removes its own
persistent forwarding file so reboot does not re-enable a failed setup. Private
files and a `failed` state remain for inspection. If a process/interface cannot
stop, the backend firewall is deliberately retained. Interrupted or externally
modified installations may require manual inspection.

To remove a managed installation, including a verified failed setup:

```sh
python3 /usr/local/lib/cwg/server-admin.py uninstall
```

Uninstall checks every managed file before stopping anything. It removes only
owned files and tables, preserving unrelated files and nftables tables. Packages
remain installed. After a successful installation, uninstall leaves live
forwarding settings enabled in case another service uses them, but removes
CWG's persistent sysctl file. Inspect the host before manually restoring those
settings; recorded previous values are in the private installation state before
uninstall. Reinstall after removal creates new keys and requires a new client ZIP.

## Validation of this revision

On 2026-10-10 the installer was exercised in a booted Debian 13 systemd container
with a private network namespace, separate client and WAN namespaces, and actual
kernel WireGuard. Tests covered service hardening, repeat-install key retention,
verified 1 MiB forwarding/NAT, full IPv4/IPv6 routes, local LAN preservation,
prompt unsupported-IPv6 failure, client ZIP contents, changed-file refusal,
unrelated file/table preservation, injected service-start failure, forwarding
rollback, failed-state uninstall and clean recovery installation. Dependencies
were supplied from official Debian packages to the offline guest; its online
APT branch was not exercised end to end. This is not an Internet-path or DPI test.
