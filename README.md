# CWG: experimental UDP transport for WireGuard

The current research path is a **client-driven encrypted UDP request/reply
carrier**. It carries complete native WireGuard datagrams and remains UDP-only;
there is no TCP fallback. Each authenticated request permits one same-sized
response; the server queues return traffic until the client requests it.
See [request/reply mode and its bandwidth cost](docs/EXCHANGE.md).
The original raw carrier and its pacing option remain available for comparison.
The earlier [HTTP/3 experiment](docs/HTTP3.md) remains
available for reference, but failed preflight on the target network on both
tested ports. Work on that path has stopped at the user's request.

The original raw carrier wraps the **complete WireGuard UDP datagram** in authenticated encryption and
variable padding. Standard WireGuard remains responsible for the VPN's keys,
handshakes, replay protection and IP tunnel. CWG removes its exposed packet types,
counters and exact small-packet lengths from the outer connection.

This is a research prototype for testing a network where UDP is selectively
filtered. **Successful local tests do not establish DPI evasion on that network.**
The raw carrier looks like unfamiliar encrypted UDP. IP
addresses, lengths, timing and volume remain observable. No design here promises
that a server address will avoid blocking. CWG has not had an independent security
audit and is not a drop-in replacement for a mature VPN product.

## Included

- Windows x64 client and Linux x64/ARM64 server executables in the release ZIP.
- Independent outer secret; XChaCha20-Poly1305 and HKDF-SHA256 from Go's standard
  and official cryptography libraries. WireGuard cryptography is unchanged.
- Fresh challenge per association; distinct directional keys; bounded replay
  window; authenticated endpoint migration; unauthenticated packets receive no
  application reply and never reach WireGuard.
- `balanced` padding (default) and `minimal` padding profiles. Optional data
  pacing limits bursts in each sending direction, with a bounded delay queue
  and explicit drop counters. CWG does not retransmit data packets.
- A non-disruptive authenticated probe at 256, 512, 1024 and 1440 UDP payload bytes.
- Windows key-bundle/start/stop helpers and a Linux systemd installer that keeps
  the original WireGuard port inaccessible from external interfaces.
- Full-mode Windows helper selects the existing physical route, adds an endpoint
  bypass route when needed, and binds the outer IPv4 socket to that interface
  before connecting. Unsupported or failed binding stops startup. Restart the
  client after changing network adapters; this does not provide a kill switch.
- Tests with real WireGuard implementations and loopback UDP sockets, including
  IPv4/IPv6 packets in both directions at a 1280-byte tunnel MTU.
- Version-separated request/reply mode, bounded queues and outstanding requests,
  idle/active polling, per-request response correlation and timeout counters.

One server process/outer secret serves **one active client**. Additional clients
need separate instances, ports, WireGuard configurations and secrets. Starting a
second raw client with the same secret replaces the first association. HTTP/3
rejects simultaneous flows instead. The `probe`
command does not replace it.

## Optional raw UDP pacing (0.3.0)

Set `"pace_kbps": 1000` in each private raw client/server JSON configuration
to cap each sender at approximately 1 Mbit/s. The receiver's setting does not
limit its peer; configure both sides. `"pace_queue_ms": 150` bounds queued
delay; it defaults to 150 when pacing is enabled. Zero/omitted `pace_kbps`
preserves the original immediate-send behavior. Valid limits are 128..100000
kbit/s and 10..1000 ms. Existing older binaries reject these new fields.

Pacing spaces encrypted data datagrams, counting padding and conservative IP/UDP
header overhead. It retains at most 256 queued packets and discards packets
whose wait budget is exceeded. Authentication/control traffic remains bounded
separately and is not delayed behind bulk data. Session replacement clears the
queue; authenticated endpoint migration updates queued destinations. Logs expose
`pacing_queued`, `pacing_dropped`, current/peak queue bytes and cumulative wait
microseconds, separately from receive-queue drops and socket errors.

This is a fixed rate ceiling, not adaptive congestion control or loss recovery.
Start conservatively and raise the rate only after a sustained user-run test.
Pacing cannot make an unfamiliar protocol indistinguishable from allowed traffic,
and deliberately low initial limits will reduce throughput.

## First deployment (original raw transport)

The first deployment should use a server you control, with an otherwise reachable
IPv4 address. For a new deployment, inspect the server's
existing firewall, interfaces and services before installing. Keep SSH available.

1. Extract the release and install the official WireGuard Windows client.
2. Create a private bundle from PowerShell. Choose an outer UDP port other than
   51820. The example uses 443; that choice does not make the traffic QUIC.

   ```powershell
   .\scripts\New-Bundle.ps1 -Endpoint YOUR_SERVER_IPV4:443 -Name server1
   ```

   This locks the `private` parent directory to your account, SYSTEM and local
   Administrators **before** generating keys. Existing bundles are not overwritten.
   Do not paste keys or complete configurations into chat, logs or Git.

3. Transfer **only** `private/server1/server.json` and `cwg0.conf` to a private
   directory on the server using SSH. Transfer the matching Linux binary and the
   entire `deploy/linux` directory. The server does not need the client's
   WireGuard private key.

4. On a compatible Linux server, install the prerequisites and run the installer:

   ```sh
   # Debian/Ubuntu prerequisites, if not already installed:
   sudo apt-get install wireguard-tools nftables iproute2 python3
   chmod +x ./bin/cwg-linux-amd64
   sudo bash ./deploy/linux/install-server.sh ./bin/cwg-linux-amd64 ./private-server-files
   ```

   Use `cwg-linux-arm64` on ARM64. The installer requires systemd 247+, refuses to
   overwrite existing CWG files, starts the port guard before WireGuard, and never
   flushes other firewall tables. A partial install is left for inspection if a
   command fails; it is not automatically deleted. Allow the selected **outer UDP
   port** in the cloud and host firewalls. UDP 51820 must remain private. Existing
   firewall policies can still reject traffic; an accept in one nftables base
   chain cannot override a drop in another.

5. On Windows, test only the outer transport:

   ```powershell
   .\bin\cwg-windows-amd64.exe probe -config .\private\server1\client.json -count 8
   ```

   A received response authenticates the peer holding the outer secret. Look at
   each sample: small replies with missing 1440-byte replies suggest investigating
   path MTU or size filtering. This command does not prove VPN routing or sustained
   data transfer. Server/client clocks must be synchronized.

6. From **elevated PowerShell**, start the split test tunnel:

   ```powershell
   .\scripts\Start-Client.ps1 -Bundle .\private\server1 -Mode Test
   ping 10.77.0.1
   ping fd77:77:77::1
   ```

   Test mode routes only the private test subnets, leaving ordinary internet
   traffic on its existing connection. The helper starts CWG hidden, installs only
   the selected WireGuard tunnel service, and saves recovery state in the private
   bundle. Its logs contain aggregate counters, not keys or packet contents.

7. Stop with:

   ```powershell
   .\scripts\Stop-Client.ps1 -Bundle .\private\server1
   ```

   Use the same bundle. The helper checks process identity before stopping it and
   removes only an endpoint route it recorded creating. Re-run it if interrupted.

## Full internet traffic

First confirm split mode works. The Linux installer intentionally does **not**
enable internet forwarding or NAT. Set those up after examining the actual server
and its firewall. Required behavior is IPv4 forwarding from `cwg0` to the chosen
egress interface, restricted to source `10.77.0.2`, source NAT on that egress, and
the corresponding established return path. Do not expose forwarding to other
clients or replace the host's firewall ruleset. For IPv6, provision working IPv6
egress or deliberately leave it unavailable through the tunnel.

Once the server is ready, stop Test mode, then start `-Mode Full`. This helper pins
an IPv4 host route to the outer server through the current physical interface
before installing the full tunnel. Without that bypass, the userspace transport
could route into its own WireGuard tunnel. Reconnect after changing Wi-Fi/network
interfaces because the bypass route is tied to the selected interface.

The generated full profile uses IPv4 and IPv6 `/1` routes so Windows WireGuard's
special `/0` firewall mode does not block the separate transport process. **This
does not provide a system-wide kill switch.** While installed, IPv6 follows the
tunnel; without server IPv6 egress it fails. Removing/stopping the tunnel restores
ordinary connectivity. Applications must not assume anonymity or leak protection
from this prototype. More-specific local routes remain possible.

An IPv4-only Full profile can instead be used on a host whose physical connection
already has IPv6 disabled. The Windows helper refuses a profile without full IPv6
routes if IPv6 is enabled on the selected physical adapter, an IPv6 default route
exists, or it cannot inspect that state. It does not change adapter bindings.
This is a startup check, not continuous leak protection; stop before changing
network settings. The deployment-specific client kit uses this profile with DNS
retained after it passed the user's comparison test. Generated bundles retain
the original dual-stack default. A subsequent normal-use test failed with the
identical profile, so this is **not a verified fix**. See the measured results in
[validation](docs/VALIDATION.md).

Where WireSock's `nt_ndiswgc` filter is attached to the physical adapter,
`scripts/Compare-WireSockFilter.ps1 -Bundle PATH -TemporarilyUnbindWireSock`
provides an explicit, user-run comparison: Full mode with the filter attached,
temporarily detached, then restored. All three phases copy the same configuration,
save packet metadata and DNS/HTTPS/ping results, and stop their own tunnel. The
binding change may briefly interrupt the physical connection; run locally with
other VPNs disconnected and keep PowerShell open. Recovery state is written
before the change; `-RestoreOnly` retries filter restoration after interruption.
The helper identifies the adapter by GUID and changes only the WireSock binding.
This tests a possible local cause; the filter's presence is not evidence of fault.

The completed filter comparison passed with the filter attached, detached and
restored; it did not reproduce the failure. With the current IPv4-only profile,
successful diagnostic runs enabled packet tracing while the failed normal run
did not. This is a correlation, not a confirmed workaround. The user-run
`scripts/Compare-ClientTracing.ps1 -Bundle PATH` isolates it with four Full runs
(tracing off/on/off/on) using the same original configuration and bundle path.
It changes no bindings, stops each tunnel, and archives previous/per-run logs
in the private bundle. Packet tracing performs synchronous file writes and can
alter scheduling; the comparison does not identify the specific timing or
network mechanism. The completed comparison reproduced severe loss with tracing off
and success with tracing on in both repetitions. Each startup also creates a
fresh UDP flow, so network variability remains a possible confounder.

To investigate the user's report that the failed normal run had YouTube active,
`scripts/Measure-VideoLoad.ps1 -Bundle PATH` measures one continuously connected
Full session with tracing on: idle, user-started YouTube playback, then idle after
the user closes the tab. It records timed private pings and tunnel byte counters,
prompts for the two manual actions, and stops the tunnel on completion/error.
It aborts if the 100,000-record trace limit is reached: logging then stops doing
synchronous writes, which would confound the load test. Tracing is a bounded
diagnostic, not a production pacing mechanism or a verified reliability fix.

## Transport settings and limits

For a size-dependent path investigation, `probe` also accepts a bounded list of
payload lengths, for example `-count 16 -sizes 256,257,258,259`. The existing
256/512/1024/1440 default stays unchanged. The probes use the existing authenticated
diagnostic message, with the same conservative pacing and no association change.
`scripts/Measure-UDPPath.ps1 -Bundle PATH` compares the normal sizes with adjacent
sizes while the VPN is stopped, saving results in the private bundle. Run it on
the network under investigation with other VPNs disconnected. It sends probes
only when the user explicitly runs it; it does not change routing or DNS.

`scripts/Measure-SessionPath.ps1 -Bundle PATH` isolates an authenticated outer
session from WireGuard and full-tunnel routing. It runs `session-probe` at 1, 5,
10 and 20 packets/s, eight seconds per stage, using existing v1 Ping/Pong frames.
Stop the normal client and other VPNs first: this diagnostic takes ownership of
the server's one active session, though it never sends WireGuard data or changes
routes. Its rates stay within the existing server's 20/s Ping response limit.

Private JSON settings are `mode`, `listen`, `wireguard`, `key`, `profile`, and
client-only `endpoint`. Addresses are literal IP:port values; DNS re-resolution is
not performed. The binary supports IPv4/IPv6 outer sockets, but generated bundles
and the Windows full-mode helper currently target an IPv4 server. Change padding
by editing `profile` and restarting; the two ends can use different profiles.

The WireGuard interface MTU is 1280. Maximum outer UDP payload is 1440 bytes, or
1488 bytes including IPv6+UDP headers. A smaller real path MTU needs further
adjustment or a fragmentation-aware carrier; CWG does not implement fragmentation,
PMTU discovery or automatic MTU negotiation. Padding shrinks near the datagram
limit; oversize packets are dropped and counted. Native WireGuard handshakes and
retransmissions are still carried unchanged inside the wrapper.

An outer association adds one round trip on startup. Keepalive probes are spaced
randomly 20–30 seconds apart. A client reconnects after 70 seconds without an
authenticated response. Data is not intentionally delayed. The userspace hop,
extra authentication and padding have a real cost; measure your own throughput,
latency and CPU consumption. A codec microbenchmark is not a VPN speed test.

Read [the protocol and threat model](docs/PROTOCOL.md) and
[the experiment plan](docs/TEST-PLAN.md) before interpreting a successful connection.

## Build and verify

Go 1.26 or newer. Dependency versions are pinned in `go.mod` and `go.sum`. Only
the official Go cryptography library and its platform dependency are linked into
the transport; the WireGuard library is used for tests.

```powershell
.\scripts\Build.ps1
```

The script runs tests and vet, builds Windows x64 and Linux x64/ARM64 without CGO,
and creates a fresh release directory, release/source ZIPs and checksums under
`dist`. Tests need working localhost UDP; a sandbox that silently drops loopback
traffic cannot run the networking tests. No OS tunnel, routing change, public
server, or administrator privilege is required for the Go tests.

```sh
go test ./...
go vet ./...
go test ./internal/wire -run '^$' -fuzz FuzzOpen -fuzztime 15s
go test ./internal/wire -run '^$' -bench BenchmarkSealOpen1280
```

When a C compiler is available, also use `go test -race ./...`. Test the exact
Windows release executable with `python scripts/smoke.py PATH_TO_EXE`.

## Primary references

- [WireGuard protocol](https://www.wireguard.com/protocol/) and
  [obfuscation limitations](https://www.wireguard.com/known-limitations/).
- [Go XChaCha20-Poly1305 implementation](https://github.com/golang/crypto/tree/master/chacha20poly1305).
- [Research on blocking fully encrypted traffic](https://gfw.report/publications/usenixsecurity23/en/).
  That study measured TCP; it illustrates a limitation of random-looking traffic,
  not proof about this UDP protocol on your ISP.
