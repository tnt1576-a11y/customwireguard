# Windows application client

The application client keeps CWG's existing UDP carrier and uses a separate,
checksum-pinned sing-box 1.14.3 engine for local WireGuard and Windows routing.
WireGuard Windows and WireSock are not required for this mode. Stop the native
CWG client before starting application mode; they use the same loopback ports.
The server-side transport and credentials are unchanged.

## Open the controls

Extract the complete Windows app folder and double-click **CWG.exe**. Accept
Windows' administrator prompt. No terminal commands are needed. The private
owner package already has a profile; other users click **Import configuration**
and select the `.cwg` file supplied by their administrator through the bot.

1. Choose **Full PC** to tunnel all apps, or **Split tunnel** with **Only these
   apps use CWG** or **These apps bypass CWG**. Add executables or installation
   folders. Your choices are saved with the profile.
2. Click **Connect**. On first use the app installs the official routing engine
   automatically, checking archive and executable SHA-256 against pinned values.
   Bundles containing `engine/sing-box-1.14.3-windows-amd64.zip` work without a
   download. No permanent filtering service is installed in this step.
3. Restart applications whose existing connections need to move. A folder rule
   includes executables in its subfolders; an individual program rule covers
   that executable only. Add separately installed launchers/helpers separately.
4. Click **Disconnect** to restore normal networking. Closing the window while
   connected leaves CWG running in the notification area. Its tray menu can open
   the controls, disconnect, or disconnect and quit.

If GitHub is inaccessible, obtain the official archive on another PC and put it
in the app's `engine` folder before clicking Connect. Advanced manual setup:

```powershell
.\scripts\Install-AppEngine.ps1 -ArchivePath C:\Downloads\sing-box-1.14.3-windows-amd64.zip
```

The installer performs the same pinned checks for offline input. An already
installed matching engine is verified and reused. A different existing engine
is refused. Failed downloads remain in a staging folder under `bin` for inspection.

Keep `CWG.exe`, `scripts` and `bin` together. The executable checks embedded
helper hashes before importing or connecting; do not mix releases. Imported
profiles contain only bounded data and have private Windows permissions.
The app requires 64-bit Windows 10/11 and .NET Framework 4.8. The legacy
`Open-AppClient.ps1` controls remain available for existing automation.

The compact shareable Windows ZIP includes the verified engine ready to run in
`bin/app-engine`; it needs no first-use download or extraction. It keeps only the
desktop executable, carrier, engine, required helpers, short instructions and
checksums. Dependency notices are combined in `THIRD-PARTY-NOTICES.txt`.
Build this package with `scripts/Package-Desktop.ps1 -Release RELEASE_FOLDER
-EngineArchive OFFICIAL_ENGINE_ZIP -Destination NEW_OUTPUT_FOLDER`.

## Routing behavior

- Selected application TCP and UDP connections enter the native WireGuard
  endpoint, then the unchanged encrypted CWG UDP carrier. Bypassed applications
  use the physical adapter. There is no TCP carrier fallback.
- Ordinary Windows DNS uses the tunnel while app mode is active, including DNS
  requested on behalf of bypassed apps. Windows shares DNS resolution, so it
  cannot reliably be attributed to an individual application. App-owned DoH or
  other encrypted DNS follows that application's routing rule.
- The current server provides IPv4 Internet access. Public IPv6 for tunneled
  applications is rejected. Bypassed apps retain their normal IPv6 route.
  The physical adapter's IPv6 binding is not disabled.
- The directly attached IPv4 LAN and CWG server address bypass application
  routing. RDP/SSH management on that LAN remains reachable.
- Rules match executable paths, case-insensitively. Folder rules include child
  programs inside the selected folder. They do not follow arbitrary descendant
  processes launched from other folders. Unknown processes use the mode's
  default: direct in Include mode, tunneled in Exclude/All mode.
- There is one app session per PC. A physical route to the server is required;
  startup refuses to put CWG through an existing virtual VPN adapter.

Stopping or losing the routing engine restores normal networking. **This is not
a persistent kill switch.** While the engine remains active, a failed CWG carrier
is restarted and selected flows have no direct fallback. A disconnected remote
server can still cause connection timeouts; the UI's connected status means the
local routing rules are active, not that every remote destination is reachable.

## Lifecycle and recovery

The controller copies its runtime into `C:\ProgramData\CWG-AppTunnel`, protected
for SYSTEM and Administrators. A SYSTEM supervisor and independent periodic/startup
watchdog run from that protected location. Neither runs scripts from a writable
user folder. Startup waits for the Windows tunnel adapter to be ready. Each
session has a unique adapter name, and shutdown sends a console interrupt to let
the engine release its adapter before resorting to a bounded forced stop.

The watchdog handles a dead supervisor and optional test deadlines. If Windows
itself is stalled it cannot promise an immediate cleanup. Carrier logs rotate at
8 MiB, keeping one previous file for each stream; logs and runtime configurations
are private. The runtime directory remains after disconnect for diagnosis.

```powershell
.\scripts\App-Tunnel.ps1 -Action Status
.\scripts\App-Tunnel.ps1 -Action Stop
```

For automation, save `config\apps.json` using the structure in
`examples/apps.json.example`, then use:

```powershell
.\scripts\App-Tunnel.ps1 -Action Start -Bundle .\config
```

`-TestMinutes 5` sets a bounded test session. All networking tests in development
were performed on the separate Windows 10 PC, with another SYSTEM cleanup task.

## Verification and limits

Tests on 2026-10-10 UTC verified selected and bypassed HTTPS egress, exact/folder
matching, a child executable, TCP payload integrity, UDP echo traffic, fresh DNS,
LAN access, carrier restart, supervisor failure, watchdog cleanup, and restarting
after cleanup. An uncached test DNS query and its response were observed on the
server's WireGuard interface. Windows configuration tests also check IPv6 reject
rule ordering and atomic state updates under PowerShell 5.1.

The final Include trial transferred a SHA-256-verified 32 MiB download at about
24.6 Mbit/s. During the actual 10.975-second transfer window, **218/218** UDP
probes returned: median **93.586 ms**, p95 **97.494 ms**, maximum **115.079 ms**.
The entire 25-second probe stream returned 499/499. The Exclude trial returned
500/500 over a stream with a transfer overlap; its p95 was 108.936 ms. These are
short sequential trials, not proof of a universal latency improvement over the
native client or of zero underlying carrier loss.

The test network has no working public IPv6 path. The IPv6 connection attempt
failed and the rules capture/reject selected IPv6, but successful bypass IPv6 and
fragmented IPv6 behavior on a live dual-stack WAN remain unverified. This client
does not claim a general leak-proof security boundary against applications that
deliberately bind other interfaces or bypass ordinary routing.

Earlier failed trials are preserved: PowerShell 5.1 needed an explicit null
string for atomic file replacement; forced engine termination left a stale
adapter name and caused a later startup failure. The final lifecycle fixes
addressed these failures. WireSock compatibility results remain in APP-TUNNEL.md.

The routing engine is a separate upstream GPL-3.0-or-later program with its own
license and naming restriction. Its executable is not part of the CWG source
build; the installer obtains the unmodified official release. Keep its license
when transferring the dependency. Upstream release, corresponding tagged source,
and configuration documentation:

- [sing-box 1.14.3 release](https://github.com/SagerNet/sing-box/releases/tag/v1.14.3)
- [Tagged source and license](https://github.com/SagerNet/sing-box/tree/v1.14.3)
- [WireGuard endpoint](https://sing-box.sagernet.org/configuration/endpoint/wireguard/)
- [Windows TUN and DNS behavior](https://sing-box.sagernet.org/configuration/inbound/tun/)
- [Application routing rules](https://sing-box.sagernet.org/configuration/route/rule/)
