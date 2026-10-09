# Local validation

Updated on 2026-10-09. Local and user-run server/path results are recorded below;
reliable Full-mode internet access remains unresolved.

## Passed locally

- Real WireGuard Noise handshake over CWG and real loopback UDP sockets, followed
  by IPv4 and IPv6 packets in both directions, including 1280-byte inner packets.
- Both padding profiles; exact UDP payload round trips at 32, 148, 1280 and 1364
  bytes; independent authenticated diagnostics without replacing a live session.
- Every-byte packet tampering, truncation, wrong keys, reflected directions,
  timestamp bounds/overflow, fresh session key separation, replay/reordering,
  authenticated endpoint migration and source-bound bootstrap replay rejection.
- No response or backend forwarding for unauthenticated packets, including an
  oversized UDP datagram. Bounded queues, fixed replay state and strict loopback
  configuration validation are enforced by the implementation.
- `go vet ./...` and module integrity verification during the release build.
- A 15-second parser fuzz run: 1,275,211 executions, no failures.
- PowerShell helper parsing and Linux installer Bash syntax validation.
- Recovery when the first encrypted server welcome is intentionally dropped.
- The exact Windows release executable: both-way UDP payload delivery at 32,
  148, 1280 and 1364 bytes; all four authenticated probe sizes; unauthenticated
  silence; the diagnostic preserved an already active client association.
- The packaged Windows key-generation helper was run with disposable keys. Its
  protected parent ACL and generated key-file access rules were checked, then the
  temporary test directory was removed.

The initial network tests inside the execution sandbox timed out because localhost
UDP was not delivered. The same tests passed with localhost networking permitted.

## Dependency check

The official `govulncheck` v1.8.0 scan reported zero affected symbols and zero
vulnerabilities in imported packages. It noted module-level advisory
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) for the unmaintained
`golang.org/x/crypto/openpgp` package. CWG does not import or link OpenPGP; it uses
`chacha20poly1305` from that module. The linked external modules are
`golang.org/x/crypto v0.57.0` and `golang.org/x/sys v0.48.0`.

Compiler: official Go 1.27.2 windows/amd64, archive SHA-256 verified against the
Go download API before extraction. The project requires Go 1.26 or newer.

## Limited performance observation

On this Windows PC's AMD Ryzen 7 7800X3D, one codec-only benchmark that sealed and
opened a frame carrying a 1280-byte inner IP packet measured approximately
1.82 microseconds per iteration (about 704 MB/s of inner payload). This excludes
WireGuard encryption, UDP sockets, scheduling, routing and real network latency.
It is **not** measured VPN throughput or evidence of near-native WAN performance.

## Server deployment on 2026-10-09

The release Linux amd64 binary was deployed to the user's Debian 13 server
(kernel 6.12, systemd 257). Its SHA-256 matched the packaged binary. CWG,
the native WireGuard interface, the port guard, and the server-specific IPv4
forwarding/NAT unit were active and enabled for boot. Systemd unit verification
passed; CWG had zero restarts and no warning-level journal entries at inspection.
The native WireGuard UDP port was blocked on non-loopback interfaces. IPv4
forwarding was enabled with rules restricted to the configured client on cwg0;
IPv6 internet forwarding was left disabled.

At the user's request, no client probe, tunnel activation, or traffic test was
performed against that server. Running services and installed rules do not prove
end-to-end connectivity. Private deployment files remain outside release files.

## Windows helper correction on 2026-10-09

The user's first WAN probe received 8/8 replies across 256, 512, 1024, and 1440
byte payloads (median about 85.85 ms). This is evidence of outer UDP reachability
on that connection only. The following native tunnel startup hit a Windows
helper race: the helper queried the service before WireGuard finished installing,
cleaned up CWG, and left a subsequently created tunnel service without transport.
Existing Windows service records and WireGuard logs confirmed the leftover.

The start/stop helpers now explicitly wait for the GUI executable, use its own
exit code, save its stdout/stderr, and wait for service readiness/deletion.
Cleanup can recover an orphan without a state file only after matching the
service executable and exact bundle configuration path. A delayed GUI executable
fixture and mocked service records passed 14 offline regression checks under
Windows PowerShell 5.1 and PowerShell 7; these checks create no real tunnel,
network traffic, or Windows services. The updated VPN connection remains for the
user to test.

## User-run full-mode diagnostic on 2026-10-09

After the helper correction, the user successfully started Test mode and received
4/4 private IPv4 ping replies. Full mode also established a native WireGuard
handshake. Its diagnostic showed the expected IPv4 /1 routes, an Ethernet /32
server bypass, MTU 1280, and the configured tunnel DNS servers.

Full-mode delivery was nevertheless poor: private small pings received 0/4,
1200-byte pings 1/4, and internet pings 0/3. Explicit DNS and numeric-IP HTTPS
timed out. The client recorded at least 1346 data sends while the server's
received counter rose by 598 during that session. CWG queue, oversize, rejection
and socket error counters remained zero; server UDP receive-buffer errors were
also zero. The observations show substantial packet delivery loss but do not
identify its location or prove censorship/DPI caused it.

An alternate UDP 8443 server listener and private client profile were prepared
for a port-only comparison, preserving protocol, padding, keys and the native
WireGuard backend. The assistant verified service/configuration state only;
client reachability and the comparison remain for the user to test. Only one
client profile may be active at a time.

The subsequent user-run UDP 8443 test still lost 3/4 small and 3/4 large tunnel
pings. DNS completed in that run and 2/3 internet pings replied, but all three
HTTPS requests timed out. Changing the port did not establish reliable service.
The four fixed-size startup probes differ from live variable-size traffic and
its higher rate; neither size sensitivity nor load-related loss has yet been
isolated. Version 0.1.1 adds a low-rate adjacent-size probe comparison for the
user to run, without changing the wire protocol or server configuration.

The user's following low-rate comparison received all 40 replies (8 control,
32 adjacent-size), covering even/odd payloads from 256 to 1440 bytes with median
RTT about 87-91 ms. That does not reproduce the full-tunnel loss and weakens a
simple length/MTU-cutoff hypothesis. Version 0.1.2 adds a bounded authenticated
session-rate measurement without WireGuard, to distinguish outer-session loss
under load from the native/full-tunnel path. It uses the existing v1 Ping/Pong
messages and does not alter the remote server or transport data format.

The user-run session-rate test then passed 288/288 round trips: 8/8 at 1/s,
40/40 at 5/s, 80/80 at 10/s and 160/160 at 20/s. Median RTT was about 88 ms;
p95 ranged from 90 to 109 ms. No rejected or late replies were recorded.
This validates the outer session at the tested pacing and sizes without native
WireGuard or full-tunnel routes. It does not cover higher rates, bursts, or
prove where the full-tunnel loss occurs. Version 0.1.3 adds optional bounded
socket metadata traces and a user-run Test/Full comparison to locate that loss.
Traces contain timestamps, socket addresses, lengths and SHA-256 digests only;
the wire protocol and padding are unchanged. Trace files are private diagnostics.

The user-run traced comparison delivered all 12 Test-mode private pings and all
15 outer datagrams in each direction. Full mode delivered only 1/8 small, 1/4
large and 0/3 internet pings; numeric-IP HTTPS timed out. SHA-256 correlation
found 580/1306 client outer writes at the server socket. Of 930 server outer
writes during the client trace interval, 366 reached the client socket. The
corresponding local WireGuard datagrams matched across successful forwarding;
there were no socket write errors or trace-limit markers. These observations
locate substantial loss between socket boundaries but cannot separate Windows,
router, path filtering or the server network stack. Test mode and the paced
session measurement still do not reproduce this loss. A user-run, endpoint-only
Packet Monitor capture is prepared to narrow it to network-stack boundaries;
no rate limit or DPI mechanism is confirmed.

The subsequent endpoint-filtered Windows/server interface captures repeated the
failure. During Full mode, 295 client datagrams recorded at the Ethernet driver
were absent from both server NIC capture and server socket logs. Their captured
IPv4 and UDP checksums were valid. All 575 server writes within the client trace
interval appeared in the server NIC capture; only 235 reached the client socket.
The Windows capture also omitted some successfully delivered packets, so absence
there cannot by itself establish loss. The server capture reported zero kernel
drops. Two explicit Windows drops were recorded, insufficient to explain the
total. Router/path filtering, NIC behavior and traffic-dependent loss remain
unresolved; the evidence does not prove DPI.

The user explicitly chose to keep the project UDP-only. Version 0.1.4 adds a
bounded timing/length replay using fresh authenticated probes, without starting
WireGuard or changing routes. It compares recorded bursts with evenly spaced
packets of the same lengths at the same average rate. The ordinary server reply
limit stays 20/s; this diagnostic requires an explicit server-only allowance of
200/s. Mirrored replies do not reproduce the independent downstream traffic of
a real full tunnel. No actual captured ciphertext or browsing data is replayed.

The user-run replay passed 923/923 recorded and 923/923 paced round trips, each
over roughly 36 seconds, with no rejected or late responses. Packet lengths and
outbound timing alone did not reproduce the loss. This does not rule out traffic
classification that depends on the real bidirectional pattern or another
difference between probes and full-tunnel traffic.

Offline time-window analysis of the two preceding full-tunnel captures found
the first unmatched outbound packet at 0.809 and 0.807 seconds after outer socket
traffic began, about 0.1 seconds after the first WireGuard input. Loss was already
substantial in the first five seconds, and delivery continued throughout both
38-40 second runs. Those runs do not show a clean cutoff after 5-10 seconds.
They cannot rule out faster selective filtering, throttling or local/path faults.

Version 0.1.5 adds Windows IPv4 socket interface selection before connect, using
IP_UNICAST_IF with verified readback. Full-mode startup supplies the physical
route's interface even when an endpoint bypass route already exists. This is
routing hardening and a diagnostic change, not a confirmed fix for the observed
loss. The wire format, encryption, padding and UDP-only transport are unchanged.
The user selected UDP 443 for the next manual comparison. Port selection does
not make this protocol HTTP/3 or establish protection against IP blocking.

The user-run UDP 443 comparison with v0.1.5 confirmed the interface binding but
still failed: split mode passed 12/12 pings; Full mode passed 2/12 private pings,
1/3 internet pings, and timed out on HTTPS. Hash correlation found 379/944 outer
client writes at the server (59.9% loss), and 784/1882 server writes within the
client interval at the client (58.3% loss; boundary timing is approximate). Loss
began at 0.811 seconds. No socket/queue errors or Linux UDP buffer errors were
recorded. Neither changing the port nor explicitly selecting the adapter solved
the problem. One server rejection does not account for the missing datagrams.

A user-run controlled transfer is prepared using Test mode, so normal background
internet traffic stays off CWG. A temporary HTTP service binds only to the inner
WireGuard address and serves fixed synthetic 512 KiB payloads. Paced download,
unpaced download and upload exercise real WireGuard traffic while preserving
the UDP-only outer transport. This isolates sustained traffic from full routing
and aggregate background traffic; it cannot by itself attribute loss to DPI.
The helper checks length and SHA256, records metadata, and stops in finally.

The controlled split-tunnel transfer passed. The 512 KiB paced download completed
in 32.286 seconds, the unpaced download in 0.652 seconds, and the upload in 0.855
seconds; all lengths and SHA256 values matched. All eight surrounding pings
replied. Hash correlation matched all 1048 outbound and 1230 return datagrams in
the test interval, including the burst transfer phases. No new rejects or socket,
queue or Linux UDP buffer errors were observed. This weakens a blanket packet-
format block, simple bandwidth threshold, or fixed 5-10 second cutoff hypothesis.
It does not rule out classification of the different Full-mode traffic pattern.

The temporary private transfer service was stopped after this result. The next
user-run helper, Measure-SelectiveInternet.ps1, creates a private diagnostic copy
of Test mode with only 1.1.1.1/32 added to its AllowedIPs. It checks the actual
routes before sending tests, verifies HTTPS and the observed public IP, and stops
in finally. Original configurations and system DNS settings remain unchanged.
This separates internet forwarding/NAT from Full mode and aggregate background
traffic. Offline PS 5.1/7 fixtures verify isolation, aborting on an incorrect
route, and cleanup after a failed command. It has not yet been run on the user's
real tunnel. No transport redesign has been made on this evidence.

The selective internet test passed: all 11 private/public pings replied, HTTPS
returned 200 in 0.358 seconds and reported the configured server's public IPv4
address. All 23 outbound and 22 return datagrams matched across traces. Thus the
tested internet forwarding/NAT path works without Full mode. The remaining
failure is associated with broader routing, DNS/IPv6 settings, background
traffic or their interaction with the local/path filters; no single cause is
established. Ethernet IPv6 is disabled and no IPv6 default route is present.
WireSock's application and drivers are running and its Ethernet filter is bound;
this is not proof that a second VPN tunnel is connected or caused the failure.

Compare-FullSettings.ps1 prepares private configurations for four cases using
the same Full startup path: selective routes, the original Full config, Full
without DNS changes, and IPv4-only Full. The last case is skipped unless IPv6 is
disabled on the physical adapter and no IPv6 default route exists. It leaves
network filters and original configs untouched, verifies actual routes before
testing, and stops each tunnel in finally. Offline PS 5.1/7 fixtures cover all
variants, the IPv6 skip guard, a wrong route, and command-launch cleanup. The
user-run comparison has now completed, with the results below.

The four-case comparison on UDP 443 found a working Full profile. The original
dual-stack Full profile with DNS failed all six private pings and HTTPS. Full
without DNS changes and IPv4-only Full with DNS retained each passed 6/6 pings
and HTTPS, returning the server's public IP. Selective routing with Full startup
also passed. Hash correlation matched 284/691 outbound packets for the original
Full profile, 291/291 for Full without DNS, and 354/354 for IPv4-only Full. Return
matches were 157/409, 213/214, and 391/392 respectively; the last two each have
one unmatched return datagram near the end of the trace. These short tests do
not establish zero packet loss or long-term reliability.

The private client kit now uses the exact tested IPv4-only Full configuration
with DNS retained. Its physical Ethernet IPv6 binding is already disabled and
there is no IPv6 default route. The startup helper checks these prerequisites
when a Full profile lacks full IPv6 routes, aborting before creating an endpoint
route or starting a transport/tunnel when unsafe or unknown. It never changes
NIC bindings. Ten offline cases passed on PowerShell 7 and Windows PowerShell
5.1, covering IPv6 enabled/default/unknown/query failures, existing-route
ownership, unchanged dual-stack behavior and Test mode. These fixtures use no
real network, routes or services.

The wire format and cryptography were unchanged between the failing and working
cases. The failure is associated with the Full-mode settings, but the exact
DNS/IPv6, local-filter or network-path interaction has not been identified.
The subsequent user-run normal test failed. The configuration was byte-identical
to the successful IPv4-only comparison. Its endpoint bypass and IPv4 tunnel
routes were present, yet all 11 diagnostic private/public pings failed. Explicit
DNS and numeric-IP HTTPS also timed out, so this is not only a name-resolution
failure. The user's separate ping showed a few replies followed by timeouts.
Client counters recorded 609 authenticated data packets received over about 90
seconds, with zero rejections, socket errors or queue drops. The server stayed
active without restarts or new CWG/Linux UDP errors. This does not show a complete
outer-connection cutoff, and invalidates treating the earlier short pass as a
configuration fix. No client packet trace was enabled for this normal run, so
an exact end-to-end loss percentage cannot be computed from these counters.

WireSock's physical-adapter filter remains attached. A new user-run helper,
Compare-WireSockFilter.ps1, prepares an attached/detached/restored comparison
using the identical Full configuration, tracing all phases. It changes only
nt_ndiswgc, requires an explicit opt-in flag, writes recovery state before
detaching, and restores by adapter GUID in normal and error paths. RestoreOnly
can retry after interruption. Ten offline fixtures passed PowerShell 7 and
Windows PowerShell 5.1, including renamed-adapter restoration, an exception
after detachment took effect, diagnosis/routing failures, and failed restoration
with saved-state recovery. The actual binding has not been changed by the
assistant. The user has now completed the real comparison. All three phases
passed 23/23 private/public pings, both DNS checks and three HTTPS requests each;
every IP check showed the configured server. This includes the initial phase
before any binding change. Hash correlation matched 524/524, 548/548 and 484/484
outbound packets. Return matches were 490/490, 507/508 and 455/458; all four
unmatched return packets were within 0.17 seconds before or 0.1 seconds after
the final client trace record. Each traced association lasted about 29-31
seconds. No exact zero-loss or long-term reliability claim follows. All copies
of client.json and the native Full configuration matched the original bytes.
The filter was verified enabled again, no recovery state remained and all CWG
tunnels were stopped. Server restarts/errors remained zero and rejects did not
increase. The severe failure did not reproduce, so this comparison does not
identify WireSock as the cause or conclusively exclude intermittent filtering.

With the current IPv4-only profile, all successful comparisons used packet
tracing, unlike the failed normal run. Tracing writes synchronously under a
mutex and can change timing. Configuration-directory differences also remained
between those runs. Compare-ClientTracing.ps1 now prepares four user-run phases
(off/on/off/on), keeping the original bundle path and native configuration fixed.
It changes no network bindings, uses the same diagnostics, preserves prior and
per-phase evidence, and stops each tunnel. Six offline cases passed PowerShell 7
and Windows PowerShell 5.1, covering flag order, same-path startup, failed/partial
startup cleanup, diagnosis/launch failure, pending filter recovery and evidence
preservation after a cleanup error. No actual client run has been performed by
the assistant. The real comparison is now complete. With tracing off, the two
runs passed 3/23 and 6/23 total pings, and 0/3 and 1/3 HTTPS requests. With tracing
on, both runs passed 23/23 pings and 3/3 HTTPS requests, reporting the server's
public IP. The traced associations lasted 28.485 and 29.311 seconds. Hash matching
found all 322/322 and 259/259 outbound packets and all 274/274 and 208/208 return
packets. The failed untraced runs had no CWG queue/socket/rejection errors, but
no client hash trace exists for an exact loss calculation. Same native profile,
bundle path and interface binding were used. This repeats the tracing-related
effect; it does not prove a specific driver, congestion or DPI mechanism. A fresh
UDP flow per startup and background/network variability can still affect results.

The user clarified that WireSock's app was closed in both earlier runs and there
were no intentional network changes, then recalled that YouTube was active during
the failed normal run and nothing was open during the passing comparison. That
adds a traffic-load confounder. A one-session idle/video/idle test is prepared in
Measure-VideoLoad.ps1. It keeps tracing enabled, records one-second private ping
samples and per-phase tunnel byte deltas, and asks the user when to start and
close the video. No connection restart occurs between phases. It detects the
trace record limit and stops rather than interpreting the logging cutoff as a
video-only effect. Five offline cases passed PS7 and Windows PS5.1, including
phase loss/recovery, trace-limit abort, interrupted prompting and partial-start
cleanup. The real load test remains user-run and pending; no transport redesign,
pacing change, server change or actual client test was made by the assistant.

## Follow-up: traced video test and HTTP/3 implementation

The user completed the one-session video test. Initial idle received 4/30 ping
replies, video received 1/30, and the final idle phase received 7/30. YouTube
stayed loading or barely played. Tracing remained active below its record cap.
Hash correlation matched 823/2014 outbound and 727/1794 return datagrams, with
some shutdown-boundary uncertainty. The first unmatched outbound packet was
about 0.912 seconds after transport start and 0.152 seconds after the first
WireGuard input, before the video phase. Loss persisted across packet-size
buckets. This disproves tracing as a reliable workaround; video did not initiate
this failure. A mechanism or DPI attribution remains unproven.

An alternative standard HTTP/3 CONNECT-UDP carrier is now implemented; see
HTTP3.md for identity, MTU, protocol and operational details. Local tests cover
real WireGuard handshakes and bidirectional IPv4 packets at the 1088-byte tunnel
MTU, pinned TLS, authorization failure, rejection of other proxy destinations,
concurrent-flow rejection, oversized packets in both directions and recovery.
These localhost checks do not establish behavior on the target network.
The exact Windows and Linux executables also passed mock-UDP forwarding and
reconnection after a localhost server restart. Windows interface binding was
checked on an unconnected UDP socket (the form QUIC uses). Thirteen offline
startup fixtures passed on PowerShell 7 and Windows PowerShell 5.1, including
H3 MTU/IPv6 rejection before routing changes. Module verification, vet and the
full Go suite passed. Go's vulnerability scan found no affected symbols or
imported packages; six advisories were confined to unused parts of required
modules. This is an automated dependency check, not a security audit.

The first real user-run H3 attempt timed out during preflight with "no recent
network activity". No tunnel service or route was created. The subsequent
Diagnose-Client refusal is expected because startup did not complete. A live
read-only check found UDP 443 listening, the service active with zero restarts,
the expected firewall rules and no socket-buffer drops on that listener.
Application counters do not record QUIC handshake arrivals, so these checks
cannot identify which packets were lost or whether the cause is DPI.

Measure-QUICPath.ps1 prepares a user-run H3/raw/H3 preflight comparison on the
same server, with endpoint-scoped Packet Monitor capture and timestamped logs.
It starts no tunnel and changes no routes, DNS or adapter bindings. Each child
is limited to 30 seconds. Existing captures/filters are preserved; owned capture
and child processes are cleaned up on handled errors. Ten offline fixtures
passed PS7 and Windows PS5.1, including timeouts, failed probes, launch/wait
errors, existing captures/filters, changed filters, active-client/virtual-route
guards and capture-stop failure. The real comparison remains user-run/pending.

## Not verified here

- Sustained full-tunnel internet reliability, DNS behavior across applications,
  throughput, and native Windows recovery across reboot/network changes. Short
  user-run forwarding/NAT, transfer and Full-profile HTTPS tests passed as above.
- Race-detector execution: no supported C compiler is installed on this host.
- DPI evasion, absence of traffic classification, long-run endpoint reachability,
  or continued success on the target ISP.

Release contents and binary hashes are recorded separately in `SHA256SUMS` and
`BUILD-INFO.json`. The exact Windows executable has a separate localhost smoke
test (`scripts/smoke.py`). No real server credentials or deployment configurations
are in the release.
