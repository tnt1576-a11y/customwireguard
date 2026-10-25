# Local validation

Updated on 2026-10-10. Local and user-run server/path results are recorded below;
sustained Full-mode internet reliability remains unverified.

## 0.5.8 bounded queue corrections (2026-10-10)

Inspection of the final 0.5.7 trace exposed two separate local failures: 951
native originals expired during duplex, then the client's 256-entry outer
receive handoff dropped 131 replies eight seconds later. The earlier summary
covered zero server reader drops and missed the client drops; it is corrected
below. These are measured local drops, not evidence of DPI intent.

The new FEC scheduler defers parity when either original queue has spent half
its existing deadline. A deterministic constrained-duplex regression delivers
1000 bulk originals and 20 small arrivals at 100 Hz with no original expiry;
the previous scheduler delivered 945 bulk originals and expired 55 under the
same schedule. Small waiting time is at most 125 microseconds in that fixture.
Deferred backup expiry is expected and separately counted.

A paused-consumer UDP test reproduces a 387-reply burst. The larger fixed client
outer handoff accepts all 387. An overload case confirms its 1024-packet bound,
drop attribution, ordered delivery and unchanged socket-read timestamps.

The first candidate (before small-slot borrowing) passed vet, the complete Go
suite, and exact Windows/Linux FEC smoke. The second PC then completed 8/8
verified transfers, 458/458 regular pings and 2002/2003 active rapid pings.
Rapid ping median/p95/maximum were 107/119/142 ms. The initial idle phase was
already around 107 ms, so absolute latency cannot be compared directly with
earlier 96 ms-path runs. Every phase's regular-ping median was 105..108 ms.
Client and server reader/socket drops, host UDP errors, and native queue
expirations were zero. There were still 1323 outer timeouts (0.18369%).

This candidate rejected 62 small native packets at the old 128-slot class cap
despite unused total capacity. One sampled 64-byte native upload was missing at
the time of the rapid-ping failure during initial idle. The subsequent correction
allows bounded borrowing up to 256 small packets without increasing total
capacity or the 60 ms deadline. Its regression delivers the observed 182-packet
burst plus a fresh arrival alongside 1024 bulk packets, with no loss and under
40 ms small-packet waiting time. Separate overload checks preserve the hard
small deadline and fixed borrowing bound. Live follow-up results follow below.

The second candidate passed all local checks, but completed only 7/8 live
transfers. The 50 Mbps-target download received 24,914,104 of 62,500,000 bytes
before its original 30-second deadline. Regular pings were 503/503, active rapid
pings 2190/2192 (median/p95/max 99/119/194 ms). Client and server reader/socket
drops and queue-full counts were zero. The client's outer-reader peak reached
451, exceeding the former 256 limit without dropping. There were 172 client
native expirations, including 169 during a burst after rate reductions, and
1849 outer timeouts (0.24065%) with 51 loss backoffs. Results and cleanup were
retained; the failed download was not reclassified or given a longer deadline.

That investigation found inconsistent loss accounting: after a loss-triggered
reduction, old successful replies were excluded while old timeouts remained in
the denominator. The correction retains those successes for loss accounting
only. A regression first reduces 8000 to 5600 requests/s for real 3% loss, then
feeds 900 older successful replies and 18 older timeouts (1.96%). The previous
denominator fabricated 100% loss and cut again to 3920; the corrected controller
holds 5600. Subsequent persistent loss still cuts to 3920. Delay-backoff cohort
exclusion, conservative loss-only timeout handling, no-feedback behavior and
bounded pacing retain their separate regression coverage.

The first candidate with the loss denominator correction still completed 7/8
transfers; its 50 Mbps-target download delivered 7.91 Mbps before the unchanged
deadline. It had 1729 outer expirations (0.24007%), 33 loss backoffs, and no client
or server reader/socket drops. Regular pings were 487/492 and rapid active pings
2120/2216 (median/p95/max 102/118/156 ms). Of the rapid failures, 95 were during
the initial handshake wait and one occurred later. All 119 client native queue
expirations occurred when startup traffic finally drained. No general live
latency or throughput improvement is established by these sequential runs.

Server counters show the first native handshake arrived, but its reply expired
in the ordinary 60 ms queue: one native receive, one native expiration, and zero
native sends. No native reply was sent until about five seconds later. The next
correction gives exact native handshake/control packets eight bounded priority
slots and a one-second deadline, within the same total queue capacity. Ordinary
data deadlines remain unchanged. Tests cover a 100 ms poll gap, priority over
data/parity, exact shapes, overload, expiry, rebase and association reset.

With that correction, the exact Windows/Linux 0.5.8 binaries passed their
smoke checks and the separate PC completed 8/8 transfers. Regular pings were
443/443; active rapid pings were 1938/1940 (median/p95/max 96/110/142 ms).
The server sent the handshake reply without control expiry. There were no queue
capacity, reader or socket drops, but four client and 74 server data expirations
remained, along with 1229 outer timeouts (0.17355%) and 18 loss backoffs. FEC
reconstructed 267 downstream and one upstream datagram in the measured window;
some reconstructions may have preceded reordered originals. Transfers delivered
16.41 and 29.93 Mbps singly, 43.40 Mbps combined in parallel, 24.78 Mbps burst
upload, 18.33 Mbps smooth upload, and 17.98/15.00 Mbps in duplex. Sampled native
hashes still missed two downstream datagrams. This is a successful short run,
not proof of complete path-loss recovery or long-term stability.

The matched spare-slot repair run used the same final binaries, 8000 requests/s,
2 ms reorder target, 60/350 ms data deadlines and 4096 total queue slots. It
completed 8/8 transfers, 391/391 regular pings and 1715/1715 active rapid pings.
Rapid ping median/p95/max was 107/116/151 ms; the idle path had moved to around
107 ms, so the absolute median is not a controlled comparison against the
preceding 96 ms-path run. No queue-capacity, reader, socket or host UDP errors
occurred. One client data packet expired; server data and both handshake queues
had zero expiry. There were 956 outer timeouts (0.14725%), 10 loss backoffs and
110 TCP retransmissions. One sampled downstream native hash was still missing.

The repair run delivered 24.47/48.92 Mbps in the single-download phases,
40.58 Mbps combined in parallel, 24.70 Mbps burst upload, 17.56 Mbps smooth
upload and 17.32/18.72 Mbps in duplex. All original transfer sizes, SHA256 checks
and 30-second timeouts were retained. Raw rapid-ping failures during startup or
cleanup are preserved separately (3 of 1794 raw samples); none fell in the
standard measurement window. Five-second startup failure in the earlier trial
was inside that window and remains counted as failure above.

Decision: use 0.5.8 with spare-slot repair for normal operation. The bounded
queue and accounting fixes apply in that mode; FEC remains opt-in. Both final
modes passed exact Windows/Linux smoke checks and real Full-mode tests, but
sequential short runs do not establish universal latency improvement, zero loss,
DPI invisibility or long-term IP-block resistance. Earlier failed trials remain
part of this record. Linux arm64 is cross-compiled, not runtime-tested.

## 0.5.7 interleaved recovery (2026-10-10)

The optional FEC implementation passed the full Go suite and vet, exact Windows
and Linux amd64 executable smoke tests, and real WireGuard integration with
isolated native-data losses and four consecutive original losses in each
direction. Tests also cover legacy clients, incompatible preflight rejection,
malformed metadata, multiple-loss limits, bounded state, immediate originals,
partial-group expiry, parity scheduling and preserving the existing MTU limit.
The cold synthetic burst fixture uses a 500 ms queue deadline to isolate recovery
from startup ramp-up; it does not establish a 60 ms cold-burst delivery guarantee.

The separate Windows test PC ran the same eight verified transfers (281.25 MB)
and concurrent 20 Hz pings. Both profiles used a ceiling of 8000 requests/s,
2 ms reorder target, 60/350 ms small/bulk deadlines and 1088-byte IP MTU.
The comparison replaced spare-slot repair with FEC; it did not change encryption
or the outer UDP request/reply sizing. Each test's SYSTEM cleanup succeeded.

| Profile | Transfers | Regular pings | Dense active pings | Dense median / p95 / max ms | Client queue expiry | Outer timeouts |
| --- | ---: | ---: | ---: | --- | ---: | --- |
| Fresh 0.5.6 baseline | 8/8 | 488/489 | 2143/2143 | 96 / 109 / 133 | 381 | 1676 (0.22625%) |
| FEC candidate A | 8/8 | 504/504 | 2196/2197 | 94 / 105 / 144 | 1 | 1496 (0.18854%) |
| Same FEC candidate B | 8/8 | 448/449 | 1978/1979 | 97 / 106 / 123 | 84 | 1136 (0.16067%) |

Dense active windows span the first through last regular measurement ping. Raw
counts including startup/shutdown are 2205/2222, 2260/2274 and 2054/2057. The
candidate B regular and dense losses occurred during the initial idle phase;
they remain included. Server active queue expirations were 1/0/0 respectively.
No reader drops, full-queue drops, socket errors or host UDP error increments
were recorded. Each run retained one association. Endpoint TCP retransmits were
165/107/189; no consistent retransmit improvement is claimed.

FEC A reconstructed 296 downstream and 14 upstream datagrams; B reconstructed
269 and 6. These counters include reconstruction before a reordered original,
not just confirmed network loss. Both reported zero invalid, oversized-skipped
or late FEC frames. Sampled native hashes still had one missing downstream hash
in A and one in each direction in B. FEC is not a solution for all loss patterns.

Candidate single download rates ranged 15.93-23.02 Mbps; burst uploads were
24.78-24.79 Mbps, smooth uploads 18.21-18.91 Mbps, and duplex uploads/downloads
18.39-19.06 / 24.39-24.41 Mbps. The baseline duplex result was 10.5 / 8.8 Mbps.
These early sequential observations motivated further FEC testing, but changing
path conditions prevent attributing all differences to it.
They do not demonstrate ping-loss elimination or permanent DPI resistance.

A 20-second server sample during candidate A upload activity averaged 39.54%
total CPU busy (46% maximum), with CWG averaging 32.41% (36.26% maximum).
A later service memory snapshot was 16,072,704 bytes. These single-client
measurements are not a two- or three-user capacity test.

The first exact 0.5.7 release run subsequently completed only 6/8 transfers.
The 50 Mbps-target download received 47,460,776 of 62,500,000 bytes, and one
parallel download received 28,053,912 of 31,250,000 bytes before their unchanged
30-second deadlines. Both are failures, not verified transfers. Regular pings
were 585/587; dense active pings were 2562/2563 with 96/109/143 ms median/p95/max
(raw 2637/2641). Outer timeouts rose to 3123 (0.36794%); endpoint TCP snapshots
recorded 1075 retransmits. There was one client queue expiry and zero server
queue expiries, reader/socket drops or host UDP error increments. FEC reconstructed
502 downstream datagrams but still missed one sampled native downstream hash.
The final-binary failure prompted a matched FEC-off control; the two favorable
candidate runs alone are insufficient to establish consistent live improvement.

The release smoke fixture also needed a timing correction: it previously waited
1.1 seconds for counters emitted every five seconds. It now waits for actual
positive data/parity counters in both process logs before reporting success.
The exact Windows and Linux amd64 binaries then passed; runtime code was unchanged.

The matched control used the same 0.5.7 binaries, ceiling and queues, with FEC
disabled and spare-slot repair enabled on both sides. It also completed 6/8
transfers; the 50 Mbps-target download and one parallel download hit their
deadlines. Dense active pings were 2564/2564, median/p95/max 102/118/141 ms
(raw 2637/2641), and regular pings were 590/590. Outer timeouts remained similar
at 3052 (0.34353%), with three client queue expiries, no server queue expiries,
and 233 endpoint TCP retransmits. No sampled native hash was missing, although
backup copies can mask original losses. Similar deadline failures with FEC off
show that FEC overhead alone does not explain the changing transfer outcomes;
the control's loss-free ping result also prevents claiming FEC always improves
interactive loss. A final FEC-restored comparison follows this control.

The restored FEC run completed 7/8 transfers; its 50 Mbps-target download again
missed the 30-second deadline. Regular pings were 544/545 and dense active pings
2381/2383, median/p95/max 96/109/177 ms (raw 2457/2461). Outer timeouts were 2498
(0.31299%), with 193 endpoint TCP retransmits, 521 downstream and eight upstream
FEC reconstructions. Client queue expirations rose to 951 during duplex traffic;
seven sampled native upload hashes were missing. Server queue expiries, server
reader drops, socket errors and host UDP error increments remained zero. A later
per-client audit found 131 client outer-reader drops at 19:06:36 UTC, with that
256-packet queue full. These occurred after the 951 native queue expirations
reported at 19:06:28 UTC and are a separate local loss source. The prior compact
summary omitted the client reader counters; they are not zero for this run. All three
on/off/on trials retained one client association and restored the test PC's
networking successfully; failed transfers are preserved as failed results.

Decision: retain the 0.5.6 repair profile for normal use. The 0.5.7 FEC feature
is available as an opt-in experiment, not a demonstrated general replacement.
The Linux deployment and stopped lab kit were restored to 0.5.6. The controller
bundle was not changed or activated during these experiments. Next work must
address recovery traffic competing with fresh packets during reduced capacity,
and distinguish losses before encoding from losses FEC can reconstruct. The
installer, per-application splitting, Telegram and multi-user work remain active.

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

The user completed that comparison: both H3 attempts timed out after about
eight seconds; raw UDP on the alternate port received all four replies at
256, 512, 1024 and 1440 bytes, median about 89 ms. The server capture contained
exactly those eight raw request/reply packets and no UDP 443 packets, with zero
kernel capture drops. The Windows capture contained no packets at all, including
the known-working raw flow, so it is unusable for deciding whether QUIC left the
PC. The endpoint was reachable on the alternate port during this test. Protocol,
port-specific and local delivery causes remain unresolved; this is not proof
of DPI or complete endpoint blocking.

The diagnostic also exposed two helper defects: a long Packet Monitor filter
name was truncated, preventing the ownership check from cleaning it up, and
PS5.1 did not retain process exit codes. The one known stopped filter was
removed after checking its displayed name, endpoint and sole-filter state.
New filter names fit the display column; process handles are cached before
waiting; empty captures are explicitly marked unusable. Ten offline capture
fixtures passed both shells, including truncated-name behavior.

Version 0.2.1 adds opt-in Windows H3 preflight socket metadata and physical
interface binding. The wrapper preserves UDP socket interfaces and records
only times, endpoints, sizes and hashes, never payloads or secrets. It logs
successful socket operations; writes do not prove delivery. Local tests verify
normal and interface-bound handshakes, capability preservation, and retained
writes when the peer is silent. Measure-H3Socket.ps1 prepares default/bound/
default H3 preflights plus a raw control, without Packet Monitor or a tunnel.
Seven offline helper fixtures passed PS7 and PS5.1.

The user completed the socket comparison. Each of the three H3 attempts logged
12 accepted writes of 1200 bytes, zero reads and zero write errors before an
eight-second timeout. Explicit physical-interface binding did not change the
result. None of these 36 payload hashes appeared at the server NIC. The raw
control passed two probes (256 and 512 bytes), and the server captured all four
request/reply packets with zero kernel capture drops. This places the observed
loss after socket acceptance and before server capture, without distinguishing
local filtering, router/ISP filtering, transit or upstream server filtering.
It does not identify DPI. Protocol and destination port remain confounded.

The next comparison exchanges the existing protocols' public ports: H3 on
8443 and raw on 443, using separate probe-only client configurations, unchanged
keys, and the same pinned certificate. Temporary server overrides have a
timed rollback to the previous listeners. This comparison changes no client
routes and starts no tunnel. Comparing across runs can suggest a protocol or
port dependency, but a time-dependent network policy remains a possible cause.

That comparison is complete. H3 on UDP 8443 again logged 36 accepted socket
writes and no reads, with no matching packets at the server NIC. Raw UDP on 443
passed both probes; all four request/reply packets were captured, with no kernel
capture drops. Combined with the previous run, the failure followed the H3
traffic across ports. This supports a traffic-format dependency, but does not
prove where filtering occurs; the raw and H3 socket behaviors also differ.
The original listeners were restored and the temporary rollback timer stopped.
At the user's request, further QUIC experiments were abandoned. No compact-TLS
probe was run against the real server, and no such setting was deployed.

## Paced raw UDP (0.3.0)

The next version returns to the existing authenticated raw wire format and adds
optional outbound data pacing on each peer. A bounded FIFO limits queued delay
and memory; late packets and over-budget arrivals are counted separately from
socket failures and receive-queue drops. There is no extra retransmission,
forward-error correction, cover traffic, or claim of protocol invisibility.
The initial deployment uses 1000 kbit/s per sender and a 150 ms queue limit;
this intentionally prioritizes a controllable test rate over maximum speed.

Focused tests cover rate spacing, burst suppression, delay expiry, session
reset, endpoint rebinding, hard queue bounds, counters, disabled pacing and
configuration bounds. Real WireGuard channel-TUN tests also pass with pacing
enabled, carrying IPv4/IPv6 in both directions at 1280-byte MTU over localhost.
The first sandboxed run could not exchange loopback traffic; the same tests
passed with normal host loopback access. No actual client VPN was started.

Module verification, vet and the full Go suite passed. The exact Windows and
Linux amd64 executables passed temporary localhost bidirectional burst tests:
ten 1200-byte payloads arrived intact in each direction over about 100 ms at
1000 kbit/s. Authentication, probe non-interference and socket-trace correlation
also passed. Thirteen offline startup/routing fixtures passed both PowerShell 7
and 5.1. The client now displays its configured sending limit; the Full-mode
diagnostic retains process exit codes on PS5.1. Actual target-network stability
with pacing remains unverified until the user runs the new client.

The user completed that test: of 871 outer client writes, 382 appeared at both
the server NIC and socket. In the aligned test window, all 564 server writes
appeared at its NIC and 225 at the client socket. That is 56.1% upload and 60.1%
return loss. Both peers reported zero pacing/receive-queue drops, socket errors,
oversize packets or rejected packets. The first unmatched client write occurred
1.311 seconds after its first write. Loss continued throughout the session;
one direct-IP HTTPS request succeeded while the others failed. There was no
complete persistent disconnect or observed explanation based solely on the QUIC
fixed bit in random raw payloads. Server capture reported zero kernel drops.
These measurements still do not isolate local filtering from network filtering.

## Request/reply experiment (0.4.0)

Based on successful probe traffic, the next mode removes unsolicited server
data transmissions and correlates one same-sized authenticated response with
each client request. See [the protocol and costs](EXCHANGE.md). It retains
standard cryptography while separating its keys and encrypted version from raw
v1. Focused tests cover tamper/truncation/reflection and cross-version rejection,
response-only behavior, replay rejection, bounded/expired queues, malformed
frames, request correlation and response destinations after reordering/migration.
Real WireGuard IPv4/IPv6 at 1280-byte MTU passed over localhost in both directions.
Seventeen offline startup fixtures passed PowerShell 7 and 5.1, including the
native MTU guard.

Subsequent user-run tests established short-session connectivity. At100requests/s,
the small/large tunnel pings, Internet pings, DNS and three HTTPS checks passed.
In a225.8second video comparison, all21797outgoing datagrams matched server reads;
return delivery was complete except for the final8replies at client shutdown.
Video-phase tunnel ping loss was5%, and replaying server arrival/request timing
exactly reproduced248native-data queue expirations. This distinguishes the
transport's own queue loss from outer path loss.

At200requests/s, a128.4second comparison passed all90pings. Mean idle latency
was98ms and video171ms; user reported smooth/mostly smooth240p playback. All24090
outgoing datagrams matched server reads. Return delivery was complete except
for17final replies at shutdown. The server still discarded141native packets
(126expired,15queue-full), mainly in bursts. The server NIC capture had expired
before this run; this comparison used both endpoint socket traces. Different
duration and browser activity prevent treating absolute drop counts as a
controlled rate comparison. None of this establishes the filtering mechanism.

## Adaptive scheduler (0.5.0)

The new opt-in client controller preserves the version2 request/reply format.
Tests cover growth only after authenticated delivery, loss/delay backoff and
cooldown, idle decay, bounded burst credits, exact request/size matching,
out-of-order replies, slot reuse, expiry, queue-age/overflow counters, and
deterministic hash sampling. Real WireGuard channel-TUN IPv4/IPv6/MTU and recovery
after server state loss exercise the adaptive client over localhost.

The carrier benchmark scripts/bench-exchange.py creates temporary keys and UDP
endpoints, adds45ms in each direction, and offers simultaneous1/10/25/50Mbit/s
payload traffic in five-second stages. The initial Windows development build
delivered approximately50Mbit/s each way at the highest stage, with no payload
loss; one-way p95 was below49ms including the45ms simulated path. This is a
synthetic localhost carrier result, not end-to-end Internet or kernel-WireGuard
throughput. The Windows system's ability to run this harness is also part of
the measurement. Exact packaged binary validation is recorded with deployment.

Three offline performance-helper fixtures cover successful integrity checks,
failed transfers and cleanup after partial startup. Source and packaged client
must also pass the existing startup/routing checks before deployment.

The first user-run adaptive performance test transferred 200 MB of application
payload through native Windows WireGuard to a diagnostic endpoint on the server's
tunnel address. All six transfers completed and passed SHA-256 verification.
The 10/25/50 Mbit/s download targets achieved 9.82/24.55/22.74 Mbit/s, respectively.
Upload at a 25 Mbit/s target averaged 22.42 Mbit/s. Simultaneous upload/download
at 25 Mbit/s each averaged 22.53/24.55 Mbit/s. All 356 tunnel pings succeeded;
median RTT was 92 ms overall, with 115 ms overall p95 and 157 ms maximum.
Idle median was 92 ms; simultaneous-load median/p95 were 94/137 ms.

There was one association with no client socket errors or server restarts.
Both peers' 1/128 sampled outer packet hashes matched throughout the transfer
phases; this does not establish zero loss. The client counted 18 expired outer
requests before shutdown. It also counted 3,219 native-data queue expirations
during upload/duplex traffic; the server counted none during the active test.
Upload burst handling therefore remains an implementation concern. The cause
of the slower 50 Mbit/s download is unresolved and does not establish a fixed
ISP rate cap. These short server-directed transfers do not measure general
Internet download performance, long-term stability, or future DPI resistance.

## Burst queue investigation (0.5.1)

The new queue was compared with the original 60 ms FIFO using identical
localhost carrier traffic and 45 ms of simulated delay in each direction.
Six 1000-packet bursts were offered in each direction, with small packets
interspersed. The old queue dropped approximately 40% of bulk packets and
4.18% of small packets. With the 200 ms bulk deadline and bounded small-packet
priority, all 6000 bulk packets in each direction and all small packets arrived.
Small-packet one-way p95 was below 48 ms including the 45 ms simulated path;
bulk-packet p95 rose to about 169 ms. That is an explicit delay/loss tradeoff
for bulk traffic, not a claim that every packet retains a 60 ms queue limit.

Focused tests cover bounds, starvation prevention, per-class expiry, startup
rebasing, native WireGuard handshakes, smooth upload integrity and cancellation.
The first user-run 50 Mbit/s slowdown was unexplained at this release.
Per-connection TCP telemetry and single-versus-parallel downloads distinguish
congestion/retransmission, receive-window, application pacing and carrier limits.

The subsequent user-run 0.5.1 comparison completed and verified all eight
transfers (281.25 MB). The single 50 Mbit/s download reached 45.25 Mbit/s,
compared with 22.74 Mbit/s previously. The first 25 Mbit/s download was slower
at 16.02 Mbit/s. Parallel downloads averaged 22.13 and 20.71 Mbit/s individually;
their different completion times mean adding those averages is not a measured
aggregate over a common interval. Bursty and smooth 25 Mbit/s uploads reached
23.43 and 23.66 Mbit/s. Simultaneous upload/download reached 23.69/22.72 Mbit/s.
All 386 tunnel pings succeeded: median 94 ms, p95 112 ms, maximum 128 ms.

The client recorded 580 native queue expirations, all during the deliberately
bursty upload; smooth upload and duplex recorded none. The previous run recorded
3219 expirations with a different transfer mix, so this is not a controlled
percentage improvement. Server userspace native queues recorded zero drops
during active transfers. Small-packet queue delay maxima were 10.38 ms on the
client and 14.119 ms on the server; bulk maxima were 199.949/146.897 ms.
There was one client association, one rate backoff and 58 expired outer requests,
with no socket errors. All sampled outer hashes matched during transfer phases;
1/128 sampling cannot establish zero path loss.

Linux UDP receive-buffer errors increased by 1023 during the test. A subsequent
per-socket inspection located all 1023 drops on CWG's loopback WireGuard backend
socket, whose receive buffer was 212992 bytes. The public UDP socket recorded
zero drops. The increases of 443, 332 and 248 occurred during the single
50 Mbit/s download, parallel downloads and duplex respectively. Server TCP
retransmission counts in those phases were 445, 335 and 248, closely matching
the local socket losses. This identifies a local source of retransmissions
that userspace counters and post-read traces cannot observe.

The first 25 Mbit/s download had one TCP retransmission and a prolonged small
congestion window; there were no server UDP receive-buffer drops in that phase.
Its precise trigger remains unresolved. Recorded receive-window limitations
were at most 3.42% of TCP busy time across downloads, and send-buffer-limited
time was zero. These results do not establish the cause of the earlier run's
50 Mbit/s slowdown or long-term network filtering behavior.

## Backend receive buffer correction (0.5.2)

The server now requests a 2 MiB receive buffer for its loopback WireGuard
backend before starting its reader, matching the request on the public socket.
Failure to set the buffer produces a startup error. The OS can cap this request;
Linux reports double the requested size to include accounting overhead (see
[socket(7)](https://man7.org/linux/man-pages/man7/socket.7.html)).
No rate, queue deadline, wire format, client scheduler or TCP tuning changes
are included. A repeated user-run comparison is needed to measure the effect
on kernel drops and download recovery. Bursty uploads can still exceed the
200 ms client queue deadline; this server correction does not fix those drops.

## Input queue and larger upload bursts (0.5.3)

The user-run 0.5.2 repeat verified all eight transfers (281.25 MB). Downloads
at 25/50 Mbit/s reached 24.52/46.42 Mbit/s; two parallel download averages were
23.45 and 24.24 Mbit/s individually. Bursty/smooth uploads reached 23.40/23.73
Mbit/s; simultaneous upload/download reached 23.70/24.52 Mbit/s. All 349 pings
succeeded, median 95 ms, p95 100 ms, maximum 111 ms. Neither Linux host UDP
receive-buffer errors nor either server socket's drop count increased.

The larger kernel buffer exposed another bottleneck: 1791 packets were dropped
at the server's bounded reader-to-event-loop handoff, despite zero exchange
queue drops. Native sampled hashes missing downstream and TCP retransmissions
remain consistent with local loss. Client queue expiry was 1104 packets, all
in the deliberately bursty upload; smooth upload and duplex had none. The peak
client queue was 1874 packets. There was no reset/socket error; all sampled
outer hashes matched during transfer phases. Do not interpret that sampling
as proof of zero outer loss or claim all downloads are now lossless.

The 0.5.3 correction enlarges only the burst-enabled server's native handoff
and permits a 350 ms client bulk deadline. Focused regression tests exercise
input bounds, per-reader drop attribution, packet order/read timestamps,
the measured 1874-packet upload burst, small-packet latency and hard expiry.
The subsequent user-run 0.5.3 comparison verified all eight transfers (281.25
MB). Downloads at 25/50 Mbit/s reached 24.46/46.51 Mbit/s. Individual parallel
download averages were 24.51 and 22.34 Mbit/s. Bursty/smooth uploads reached
23.42/23.67 Mbit/s; simultaneous upload/download reached 23.75/24.54 Mbit/s.
All 353 pings succeeded: median 93 ms, p95 107 ms, maximum 120 ms. The preceding
run's p95 was 100 ms, so this does not claim an improvement in every latency
measure. Idle-before median/p95 were 93/94 ms; duplex median/p95 were 93/108 ms.

Both peers' reader handoff drops were zero, including the server native reader
that previously dropped 1791 packets. Its observed peak was 965 queued packets,
above the former 256-packet limit and below the new 2048-packet bound. Host UDP
receive-buffer errors and server socket drops also remained zero. Client queue
expirations fell from 1104 to zero. These measurements verify the targeted input
and age-limit corrections for this run, rather than establish lossless transport.

The remaining loss reached the bounded exchange queues: 710 client capacity
drops during the deliberately bursty upload and 312 server expirations during
the 50 Mbit/s download. Client maximum bulk wait was 330.754 ms; server maximum
was 199.999 ms. Small-packet maxima were 19.759/18.189 ms respectively. Smooth
upload and duplex had no exchange queue drops. Observed server TCP download
retransmissions were 0 (25 Mbit/s), 539 (50 Mbit/s), 0/2 (parallel), and 29
(duplex). These counts are sampled through application write completion and
do not directly measure missing outer datagrams. One association, no socket
errors, 15 expired outer requests and one rate backoff were recorded. All outer
hash samples matched within transfer phases; sampling remains 1/128.

The current build remains the measured baseline. A user-run six-minute
idle/video/idle comparison using the existing Measure-VideoLoad helper and
120 samples per phase checks application behavior beyond server-directed
synthetic transfers. Remaining burst-capacity/expiry loss is unresolved;
larger buffers alone do not add sending capacity or prove future stability.

The user completed that video comparison on 0.5.3 and reported smooth or mostly
smooth fixed-1080p playback. The association lasted about 412.8 seconds including
prompts, with three measured phases of about 122 seconds each. All 360 pings
succeeded. Idle-before/video/idle-after mean RTTs were 101.0/96.6/101.8 ms;
medians were 101/95/101 ms, p95 values 106/105/108 ms, and maxima 116/112/121 ms.
The video phase received 53.79 MB and sent 3.72 MB on the tunnel interface;
these are interface traffic measurements, not video encoding bitrate or capacity.

There was one uninterrupted client association, no socket errors, no reader
handoff drops on either peer, and no server socket-buffer drops. Server counters
were compared with a pre-phase baseline after association establishment to
exclude earlier runs and offline keepalive expiry. During video, 1352 server
bulk packets expired at the existing deadline. Five client packets expired
during the prompt between video and final idle; no client queue drops occurred
inside the three measured phases. This remains a working short-session baseline
with unresolved burst loss. Smooth playback is not evidence of zero packet loss.

The sampled outer trace also contained two unmatched client writes and one
unmatched server write during video. It was uncapped and sampled at 1/128;
these observations do not give an exact full-session loss rate or locate the
loss within the path. The client counted 83 expired outer requests and two
rate backoffs without reconnecting. This run did not reproduce a complete
5-10 second connection cutoff, but does not establish long-term DPI resistance.

## 0.5.4 burst recovery and higher-rate trial

The release passed go mod verify, go vet and the full Go suite (transport tests
55.635 seconds). New deterministic checks cover recent delivery-based resume,
expiry of remembered capacity, stale replies, congestion invalidation and
conservative recovery, plus a bounded 3000-packet bulk burst alongside small
traffic. PowerShell 5.1 and 7 offline fixtures cover normal/high-rate transfers,
transfer failure and partial startup cleanup. No actual Windows tunnel was
started by the assistant.

Exact Windows and Linux executables were tested with temporary keys, localhost
mock peers and 45 ms added one-way delay. Six 3000-packet bursts followed an idle
period. All 18000 bulk packets in each direction arrived on both platforms, as
did all small packets sent alongside them. Windows small-packet one-way p95
was 47.53/48.26 ms upload/download; Linux was 47.32/47.32 ms. The Linux 0.5.3
comparison lost 31.01 percent upload and 55.64 percent download of that same
offered bulk pattern; its small-packet p95 was 47.09/47.05 ms. These are synthetic
carrier measurements, not Internet ping or native WireGuard speed measurements.

Five-second simultaneous 25/50/75 Mbps offered-load stages, with small traffic
alongside, delivered every payload and small packet on both platforms. The
final 75 Mbps stage reported 74.98 Mbps each way on Windows and 74.97 on Linux;
the benchmark divides delivered bytes by the send interval and allows a short
drain afterward, so these numbers do not establish sustained capacity. Small
one-way p95 at 75 Mbps was 47.42/48.28 ms on Windows and 52.40/52.97 ms on Linux.
Large packets still queued: Linux bulk p95 was 76.83/74.24 ms at 75 Mbps, and
the initial cold 25 Mbps stage had bulk p95 173.63/218.47 ms. Priority preserves
small-packet responsiveness, not uniformly low latency for every packet size.

An earlier Linux candidate exposed 148 client native-input drops, including
one small packet, at 75 Mbps. Matching the client native handoff to the server's
2048 bound corrected that loss in the final exact-artifact comparison. Packet
age still starts at socket read and includes time in the handoff. The encrypted
format, public input bound, native MTU and small-packet deadline are unchanged.

A separate candidate comparison imposed a 40 Mbps outer link with a bounded
20 ms queue. Both versions lost substantial bulk data under overload. Candidate
small-packet p95 was 65.85/51.63 ms versus 69.10/66.42 ms for 0.5.3. Congestion
cannot be removed by enlarging queues; the trial retains backoff and pacing.
Exact-artifact smoke tests passed authentication, integrity, replay rejection,
unauthenticated silence and trace correlation. The bounded diagnostic endpoint
also passed localhost integrity and invalid/oversize request checks, including
the new 75 Mbps rate. Linux ARM64 was cross-compiled, not runtime tested.

The next USER-RUN test is Measure-AdaptivePerformance.ps1 with -HighRate. It
repeats the earlier transfers and adds a bounded 75 Mbps download, totaling
337.5 MB plus carrier overhead. Compare loaded ping with idle and with 0.5.3,
as well as per-phase queue expirations/full drops, reader/kernel drops and TCP
retransmissions. Real-network burst loss, speed and latency remain unverified
for this version until that test. Per-application split tunneling is recorded
separately in FOLLOW-UP.md; no WireSock policy or adapter changes were made.

## Completed 0.5.4 target-network comparison (2026-10-10)

The user completed the higher-rate trial with nine verified transfers totaling
337.5 MB. All 372 pings succeeded: median 91 ms, p95 100 ms, maximum 111 ms.
Idle-before median/p95 were 91/94 ms, the 75 Mbps download phase was 91/98 ms,
bursty upload 91/101 ms and duplex 91/100 ms. The 25/50/75 Mbps downloads
delivered 24.54/49.09/66.56 Mbps. Parallel downloads totaled 49.05 Mbps;
bursty/smooth uploads delivered 23.91/23.75 Mbps. Duplex was 23.78 Mbps up
and 24.55 Mbps down. These are short transfer measurements on one client.

Active-window server deltas showed no exchange expiry/full drops, reader drops,
or kernel UDP errors. Server expiry after the client stopped is excluded.
The client recorded no reader/full drops, but 48 bulk expirations immediately
after two rate backoffs during bursty upload. It recorded 46 outer-request
expirations, one association and no socket errors. All sampled hashes matched
within measured phases; the uncapped trace samples 1/128 and cannot establish
zero path loss. The two backoffs may reflect feedback from the same disturbance;
the old counters cannot establish their exact trigger.

Server TCP download retransmissions were zero at 25/50 Mbps and for both parallel
downloads, 219 at the 75 Mbps target and 161 during duplex. The high-rate sender
was receive-window-limited for 91.67 percent of its recorded busy time. Burst
upload had 2712 out-of-order receives. Loss and reordering can both produce TCP
retransmits; these values do not establish DPI detection. Server upload telemetry
describes its own sending side and cannot certify zero client retransmissions.

The server inventory was one vCPU and about 967 MiB RAM, with approximately
660 MiB available when read. CWG's resident service accounting was about 12.9 MiB
(peak 15.4 MiB). Its cumulative 23.9 CPU seconds since deployment lack an exact
test baseline and exclude WireGuard kernel work. They are not a multi-user
capacity measurement. The current instance supports one active client; the
two-to-three-user requirement and fair-sharing validation are in FOLLOW-UP.md.

## 0.5.5 stability change

Delay decisions now use feedback from requests sent at the current reduced
rate. Older replies still deliver payloads and update RTT, and all old timeouts
still retire flight slots and count as expirations. After a delay-triggered
reduction, old timeouts cannot trigger another cut for the same queued flight.
Loss-only backoff retains the previous conservative timeout response. Fresh
delay or loss still reduces the rate.
Reason-specific backoff and prior-flight counters make that distinction visible.
Sending limits, queues, priority and encrypted packet format are unchanged.

The final release passed module verification, vet and the complete Go suite
(transport tests 55.856 seconds). Deterministic regressions cover old delay
feedback, old timeout accounting after a delay cut, new loss/delay backoff,
conservative loss-only response, mixed-flight expiry and bounded storage.
Exact Windows and Linux amd64 binaries delivered all 18000 burst packets in
each direction with 45 ms simulated one-way delay; all accompanying small
packets arrived. Their small-packet one-way p95 was 47.54/48.03 ms on Windows
and 47.11/47.10 ms on Linux. Bulk one-way p95 was 200.75/218.38 ms on Windows
and 287.94/301.30 ms on Linux. This preserves priority, not uniformly low bulk
latency. No real Windows VPN, routes or adapter settings were changed by QA.

With a simulated 40 Mbps outer link and 20 ms bounded link queue, both versions
still lost most of the deliberately overloaded bulk burst: 77.34/77.98 percent
on 0.5.4 versus 77.56/78.19 percent on the final candidate. All small packets
arrived. Small-packet one-way p95 was 65.21/49.09 ms versus 65.34/48.17 ms;
candidate upload maximum was 105.59 ms versus 88.61 ms. Relay drops were
3480 versus 3720. These single-run comparisons do not prove equivalence or
improvement under overload. They show the remaining limitation explicitly.

An earlier unshipped candidate suppressed old timeouts after every kind of cut.
It prolonged overload, with 8484 relay drops versus 2980 in that comparison,
so timeout suppression was narrowed to delay-triggered reductions. Exact-artifact
smoke checks passed payload integrity, authentication, unauthenticated silence,
association preservation and trace correlation. One Linux smoke attempt exposed
a test race while comparing two still-growing traces; the test now takes a fixed
client snapshot and allows up to two seconds for server records to arrive,
without dropping unmatched hashes. The corrected test passed on both platforms.
Linux arm64 was cross-compiled only; race-detector limitations remain below.

The next USER-RUN test is Measure-AdaptivePerformance.ps1 without -HighRate:
25/50 Mbps downloads, parallel download, bursty/smooth upload and duplex, with
ping throughout. Compare queue expirations and loaded latency with the completed
0.5.4 run. Target-network improvement remains unverified until that comparison.

## Completed 0.5.5 target-network comparison (2026-10-10)

The user completed eight verified transfers totaling 281.25 MB. All 339 pings
succeeded; median/p95/max were 96/110/124 ms. The idle-before median was 96 ms,
as were all loaded-phase medians. Download25/50 delivered 24.52/49.05 Mbps,
parallel downloads totaled 49.06 Mbps, bursty/smooth uploads were 23.80/23.68
Mbps and duplex was 23.70 up/24.53 down. This run's baseline path RTT was higher
than the previous run, so absolute RTT alone does not establish a code regression.

Client queue expirations were zero, but 27 capacity drops remained during the
50 Mbps download. There was one loss-triggered backoff during parallel download,
69 expired outer requests and no delay-triggered backoffs. Therefore this run
does not directly exercise or prove the delay-to-timeout suppression fix. There
was one uninterrupted association, no socket errors and no reader handoff drops
on either peer. Active server queue drops and observed kernel UDP error deltas
were zero; post-stop expiry is excluded.

Server-observed TCP retransmissions were zero at download25 and duplex, 865 at
download50 and 26/12 on the parallel downloads. All sampled outer hashes matched,
but one native-upload sample during download50 and one native-download sample
during parallel download were unmatched. The trace is sampled 1/128 and cannot
prove zero outer loss or uniquely locate either missing native sample. Stability
remains incomplete despite successful pings and transfers. Remote testing on a
separate LAN PC is being prepared to make subsequent comparisons repeatable.

## Remote access setup validation

The setup/revocation scripts parse on Windows PowerShell 5.1 and PowerShell 7.
Offline preflight fixtures cover WhatIf, wrong controller PC, virtual adapter,
routed management path, existing SSH service and nonprivate address rejection.
No accounts, services, firewall rules or network settings were changed by those
fixtures. Actual Windows 10 installation, SSH authentication and LAN continuity
during a VPN test remain unverified until the user runs the setup package.

## Still not verified here

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

## 2026-10-10: automatic test on the separate Windows 10 host

The existing v0.5.5 client/server were exercised from Windows 10 Pro 19045 on an
Intel i7-6700HQ, 16 GB RAM and 1 Gbit Ethernet after updating that host's signed
WireGuard installation from 0.5.3 to 1.1.1. The older client failed to set metric
and MTU; the updated client ran the unchanged 1088-byte profile. This is a new
host/OS baseline, not a controlled performance comparison with the controller PC.

The 16:45:29 UTC adaptive test completed 8/8 verified transfers (281.25 MB).
All 367 tunnel pings succeeded: median 96 ms, p95 107 ms, maximum 119 ms. Phase
medians remained 96-97 ms, including load. Delivered rates were 24.50/42.26 Mbps
for the single 25/50 Mbps download targets, 24.51+24.52 Mbps in parallel,
24.63 Mbps burst-paced upload, 18.48 Mbps smooth upload, and 18.83 Mbps upload
with 24.51 Mbps download in duplex. The slower smooth upload and single-flow
50 Mbps phase remain observations requiring investigation, not evidence of DPI.

Client counters: one association, zero queue expiry/full/reader/socket drops,
275 request expirations and three loss backoffs; zero delay backoffs. Server
counter deltas within the ping window recorded two queue expirations, zero
queue-full/reader/socket drops, and zero host UDP error increments. Post-stop
server expirations were excluded. The endpoint's final TCP snapshots reported
0/2 retransmits for the single 25/50 Mbps downloads, 177/13 for the parallel
flows and 16 for the duplex download. Zero observed ping loss therefore does
not imply zero underlying loss or retransmission.

SSH and the RDP TCP port were reachable from the controller while Full mode was
active. Successful, failed-start and deliberately interrupted runs all restored
the original IPv6 binding and physical default route, with no CWG service left.
An actual scheduled watchdog interruption produced Task Scheduler termination
status 267014 and SYSTEM cleanup success without relying on runner finally.
The eight offline cleanup fixtures also passed in Windows PowerShell 5.1 and 7.
No CWG connection, adapter or route change was performed on the controller PC.
These short tests establish remote test readiness, not future DPI resistance or
multi-user capacity. Core protocol/binaries and server deployment were unchanged.

## 0.5.6 stability experiments (2026-10-10)

The assistant ran the same eight-transfer, 281.25 MB suite on the separate
Windows 10 host, using pinned SSH and a demonstrated SYSTEM cleanup watchdog.
The controller PC's VPN, routes and adapters were not changed. All pings below
are to the server's tunnel address during idle, download, burst/smooth upload
and duplex phases; they are not Internet-game latency measurements.

| Candidate / ceiling | Verified transfers | Pings received/sent | Median / p95 / max ms | Outer reply expirations |
| --- | ---: | ---: | --- | --- |
| Original 0.5.5 / 9000 | 8/8 | 388/388 | 95 / 106 / 119 | 162 (0.02241%) |
| Original 0.5.5 / 7500 | 8/8 | 360/360 | 95 / 110 / 131 | 118 (0.02141%) |
| Smaller pacing batches / 9000 | 8/8 | 441/441 | 97 / 110 / 122 | 272 (0.04157%) |
| Reorder only / 9000 | 7/8 | 467/467 | 100 / 115 / 130 | 1153 (0.13830%) |
| Original 0.5.5 restored / 9000 | 8/8 | 401/401 | 94 / 104 / 118 | 764 (0.10503%) |
| Reorder + repair / 9000 | 8/8 | 422/422 | 101 / 113 / 388 | 1053 (0.13992%) |
| Reorder + repair / 8000 | 8/8 | 426/426 | 93 / 103 / 114 | 863 (0.12476%) |
| Reorder + repair / 6000 | 8/8 | 421/421 | 94 / 102 / 116 | 434 (0.08565%) |

The smaller-batch experiment was reverted: the final pacer retains the original
2 ms credit cap and maximum 32-packet batch. Only an exact-boundary floating-point
credit correction remains. The reorder-only candidate had a failed transfer;
the 9000 repair candidate had a genuine 388 ms spike during smooth upload.
Neither was adopted as the final operating profile.

Restoring the original binary did not restore the earlier low timeout frequency.
Path conditions varied during this sequential comparison, so the lower-ceiling
results do not prove a causal percentage improvement. The 6000 run delivered
24.53/22.37 Mbps for single 25/50 Mbps download targets, 24.39+22.89 Mbps parallel
downloads, 24.76 Mbps burst upload, 18.57 Mbps smooth upload, and 18.20 Mbps upload
with 24.37 Mbps download in duplex. It recorded one server native-queue expiry;
client queue expiry, queue-full, reader/socket drops and host UDP error increments
were zero. TCP endpoint snapshots still recorded 125 retransmits. Speed was not
the optimization target, and the single-flow slowdown remains variable.

Sampled authenticated packet hashes locate most observed outer loss between
server send and client receipt. Thousands of replies also arrived out of order.
This does not locate a particular router or prove intentional DPI interference.
No server host UDP error increments or client reader drops explained those
samples. Hash matches with repair enabled can come from either copy and cannot
prove delivery of the original packet. Zero observed tunnel ping loss does not
mean zero underlying loss, nor does a short run establish long-term stability.

Local regression coverage includes bounded reorder storage, skipped gaps,
timer release with no further arrivals, late delivery, association reset,
backup spacing/expiry, handshake exclusion and disabled-option compatibility.
Real WireGuard channel-TUN tests cover both directions, IPv4/IPv6 and MTU-sized
payloads. A relay deliberately drops one nonempty encrypted native data packet
in each direction; repair delivers each original IP packet without an application
retry, and duplicate packets do not escape WireGuard replay protection.
These tests create localhost sockets, not OS VPN interfaces or routes.

### Exact 0.5.6 binaries and denser ping comparisons

`go mod verify`, `go vet ./...` and the full fresh Go suite passed. The exact
Windows/Linux amd64 release executables passed localhost packet-size, probe,
unauthenticated-silence, burst and trace-correlation checks. An additional
synthetic native-data fixture observed exactly one backup in each direction
from each executable. Linux arm64 was cross-compiled only.

The release was then tested with 20 Hz asynchronous small pings in addition to
the standard suite's pings. Dense counts below use the first through last
standard measurement ping as their window; raw startup/shutdown samples are
retained separately. A single association remained active in each run.

| 6000 requests/s profile | Verified transfers | Dense pings received/sent | Dense median / p95 / max ms | Outer expirations |
| --- | ---: | ---: | --- | --- |
| Protection on, first final run | 8/8 | 2180/2181 | 99 / 112 / 178 | 813 (0.13141%) |
| Protection on, unchanged repeat | 7/8 | 2512/2513 | 95 / 106 / 125 | 1550 (0.22806%) |
| Protection off, matched ceiling | 3/8 | 3095/3096 | 98 / 109 / 210 | 1843 (0.22007%) |
| Protection restored, same ceiling | 7/8 | 2825/2825 | 98 / 113 / 164 | 1867 (0.24394%) |

The protected repeat's failed 50 Mbps-target download received 60,220,176 of
62,500,000 bytes before the diagnostic's 30-second deadline. It did not complete
and is counted as a failure. The control timed out all five download transfers;
all three uploads passed. Its single/parallel download rates were approximately
4-10 Mbps versus 9-21 Mbps in the protected repeat. Similar outer timeout rates
with different transfer outcomes suggest that ordering/repair helps tolerate
loss; changing path conditions prevent claiming a controlled causal percentage.
One dense active ping was lost in each of these runs, so ping-loss elimination
and consistent latency improvement are not established. The protected first
run had five client queue expirations, the protected repeat zero, and the control
114; server active queue expirations were zero/one/zero respectively. After
protection was restored, the only incomplete transfer was again the 50 Mbps
target download. Its single/parallel download rates recovered to 8-19 Mbps and
all 2825 dense active-window pings returned. There were zero client queue
expirations, one server expiry, and 234 endpoint TCP retransmits. This on/off/on
sequence supports improved transfer tolerance, not removal of the path loss.

Raw dense results were 2244/2261, 2587/2591 and 3170/3174. Their additional
failures occurred outside the defined measurement window while establishing or
removing the tunnel; they are not silently included as successful data samples.
The restored-protection run had 2902/2904 raw responses; both failures were
outside the measurement window.
The maximum 275 ms raw ping in the first run was during startup. Its 178 ms
in-window maximum remains included. A 20-second server CPU sample during that
run averaged 29.05% total busy time (42.16% peak), with CWG at 25.49% average;
this does not establish an all-day CPU or multi-user capacity bound.

### Selected 8000-request profile

The lower 6000 ceiling was not retained: repeated 50 Mbps-target transfers hit
their deadline, and less spare capacity left more backup copies unsent. The
exact 0.5.6 release was retested at 8000 requests/s with reorder/repair enabled.
All eight transfers completed and verified (281.25 MB). Standard pings were
456/456, median/p95/max 99/108/117 ms. Dense active-window pings were 1994/1996
(0.1002% observed loss), with median/p95/p99/max of 99/110/114/120 ms among
replies. There were two genuine in-window timeouts; the other three failures
in the 2068/2073 raw samples occurred outside the measurement window.

Delivered rates were 24.44/27.63 Mbps for the single 25/50 Mbps targets,
11.02+12.65 Mbps parallel downloads, 24.73 Mbps burst upload, 18.75 Mbps smooth
upload, and 17.99 Mbps upload with 24.31 Mbps download in duplex. These rates
are lower than earlier favorable runs and are reported without promising a
fixed speed. The session had 1910 outer expirations (0.27440%), 46 loss backoffs,
one client queue expiry, zero active server queue expiry, zero reader/socket
drops or host UDP error increments, and 787 endpoint TCP retransmits. There was
one association; no reconnect was recorded. No missing sampled native hash was
observed in either direction, but either copy can satisfy a hash match.

This selected profile improves transfer tolerance in these comparisons;
it does not fix the underlying outer loss or demonstrate consistently lower
latency than the original. The remaining losses occur between measured send
and receipt points, which include OS/network components. DPI intent is not
established. More frequent pings exposed loss missed by the standard suite.
No claim of a loss-free path, future DPI immunity or multi-user readiness is made.

Final live Windows/Linux amd64 hashes are recorded in release `SHA256SUMS`.
The Linux service uses the verified release on UDP443, with the unchanged native
1088-byte MTU. Client settings are 8000 requests/s, 2 ms reorder target and repair
enabled. The old binary/configuration and a server rollback are preserved.

## 0.6 shared rate and arrival stability (2026-10-11)

Three independent userspace WireGuard devices ran on the separate LAN Windows
PC, each with its own outer secret, native keys/PSK, address and loopback ports.
They used the actual WAN path and the same server UDP 443 listener. This did not
install adapters or change Windows routes/DNS. Each first verified the public
Internet exit through HTTPS. UDP probes ran at 40/s per user for 55 seconds.
Three integrity-checked downloads requested 32 MiB at 10 Mbps and 16 MiB each at
5 Mbps, starting five seconds apart. The first user also uploaded an unpaced
8 MiB payload. Both machines had independent timed cleanup, and cleanup was
verified after each run. The existing single-user server was restored afterward.

| Candidate | Application UDP replies | Shared queue expired / dropped | Per-user overall p95 RTT, ms | Burst upload |
|---|---:|---:|---|---:|
| Negotiated grants, 4 ms burst allowance | 6586 / 6591 | 1607 / 75 | 100.191 / 104.777 / 111.040 | 30.915 s |
| 10% grant headroom, preserve blocked worker packets | 6597 / 6599 | 1024 / 10 | 99.372 / 111.773 / 102.324 | 32.765 s |
| Final: bounded 16 ms arrival burst, 64-packet worker handoff | 6600 / 6600 | 0 / 0 | 109.934 / 103.490 / 102.553 | 7.926 s |

All four transfers in every loaded row passed size and SHA-256 verification.
An earlier fixture run had a Python module-name collision, so its transfers
never started. Its 6599/6599 unloaded probes are excluded from loaded claims.
The failed fixture and each preceding comparison remain in private evidence.

In the final run, all three downloads overlapped for 17.045 seconds. The overlap
returned 682/682, 682/682 and 681/681 UDP probes, with p95 RTTs of 108.330, 99.240
and 105.205 ms. Overall maximum RTTs were 124.740, 116.567 and 123.158 ms. The
preceding first loaded comparison had a simultaneous brief 213–243 ms spike;
short sequential trials cannot establish that a code change caused every change
in path latency.

Final client counters recorded 18 outer timeouts in 330,737 requests (0.00544%),
no original upload queue expirations, one outer session per client and no idle
resumes. The last active server sample had no native data/control expirations;
spare repair copies expired while original traffic occupied slots. Later native
handshake/control expirations after client shutdown are not counted as active
load failures. There were no shared ingress drops, expirations or unknown drops.
The nominal native-data queue deadlines and 5 ms shared-ingress deadline were
unchanged. The final change immediately absorbs a bounded arrival burst rather
than extending stale-packet retention.

During the final 36.401-second sampled transfer interval, CWG consumed 24.21% of
one CPU. Total host busy time was 30.94%, including the synthetic HTTP endpoint
and kernel work; steal was 0.058%. Peak CWG cgroup memory was 11.36 MiB. These
measurements support this tested three-user, 20 Mbps aggregate download workload
on the observed one-vCPU server. They do not establish three simultaneous
maximum-speed users or performance on three different access networks. Outer
padding and polling still have substantial bandwidth cost. No test here proves
future DPI invisibility, IP-block prevention or long-term zero loss.

The final source passed the full Go vet/test suite and real IPv4/IPv6 WireGuard
loopback tests. Focused regressions cover acknowledgement-safe rate borrowing,
delayed grant expiry, maximum native payloads, rejected legacy negotiation,
blocked-worker fairness, compressed-arrival dispatch and the unchanged sustained
rate/deadline limits. The portable installer/account tests also passed.

## 0.6 release deployment and exact-bundle tests (2026-10-11)

The final Linux/Windows 0.6.0 binaries and freshly exported MTU-1280 profiles
were tested after migrating the live legacy installation to the managed layout.
All old CWG files, private keys, units and sysctl files were copied and verified
by hashes, ownership and modes before activation. Original files were retained
separately. A 15-minute independent rollback guarded activation and validation.
The public listener is UDP443; the obsolete UDP8443 comparison was retired.

The identical three-user WAN workload above ran twice with the release profiles:

| Exact release trial | UDP replies | Shared queue expired / dropped | Overall p95 RTT per user, ms | 8 MiB upload |
|---|---:|---:|---|---:|
| First | 6595 / 6596 | 5 / 0 | 105.040 / 108.365 / 111.837 | 36.435 s |
| Unchanged repeat | 6598 / 6598 | 0 / 0 | 106.508 / 101.273 / 101.909 | 37.589 s |

All four payload transfers in each row passed SHA-256 verification. The first
run had a brief common latency spike (maximum 235.852 ms), one application loss,
42 outer timeouts in 384,950 requests (0.01091%), one client and one server
original-data queue expiry, and one idle resume. The repeat recorded zero outer
timeouts in 391,616 requests, zero client original-data expiry and no resumes;
maximum RTT was 123.472 ms. These short trials do not prove permanent zero loss.
An attempted repeat had a test-harness JSON encoding error and never started
its probe workload; it is preserved and excluded from successful counts.

Upload time did not reproduce the earlier candidate's best result. The fresh
profiles use MTU1280 instead of the earlier manual MTU1088, but this was not a
controlled MTU comparison; no cause is established. The release is retained for
its measured stability and independent-user behavior, without a fixed upload
speed claim. In the first release trial's 52.573-second sampled transfer window,
CWG used 24.60% of one CPU, the host was 30.03% busy and peak CWG memory was
12.95 MiB. The workload is three independent stacks on one lab PC/access network.

The exact owner bundle also passed application Include mode: selected programs
and configured folder children used the server exit, bypassed programs used the
physical exit, fresh DNS answered, LAN remained reachable, and selected IPv6
connection attempts failed. There is no working public IPv6 WAN in this lab,
so this is not a complete live dual-stack bypass test. A 32 MiB download verified
its digest at about 24.58 Mbps. The entire UDP test returned 500/500 probes;
its actual 10.965-second download interval returned 218/218 with p95 97.582 ms
and maximum 110.764 ms. Forced carrier termination recovered, and the independent
watchdog cleaned up after supervisor termination. Native Full mode separately
verified the public exit, LAN access and 8/8 tunnel pings (96-99 ms).

Independent SYSTEM cleanup restored the original lab route, DNS and IPv6 binding.
Temporary accounts were revoked and test services stopped. The controller's VPN
and networking were not changed. The new private owner bundle replaces old
single-user credentials; additional people need separately provisioned accounts.

The exact public package also passed a fresh Debian13 amd64 install/repeat,
three independent authenticated preflights, account limits/revocation, private
Windows export boundaries, preserved larger UDP socket limits and optional-bot
uninstall boundaries. Twenty-three portable installer/account/Telegram checks
passed. Full Go vet/tests and real IPv4/IPv6 WireGuard tests passed before build.
Optional Telegram's original systemd/management/lifecycle/resource tests used a
local TLS API fixture. Subsequent live delivery is documented below.

## 0.6.1 desktop and live Telegram (2026-10-11)

The Windows desktop app adds Full PC, Include and Exclude choices, saved executable
and folder rules, profile import, automatic pinned engine setup, and tray controls.
It uses the 0.6 transport without changing its pacing or packet format. The Linux
transport remained 0.6.0 during these compatibility checks.

On the separate Windows 10 PC, a private driver loaded the compiled app and
invoked its real import bridge, radio controls and Connect/Disconnect events.
The first driver incorrectly expected synchronous startup and stopped at
Connecting; independent SYSTEM cleanup restored networking. After correcting the
driver to wait for Running, profile import, automatic offline engine installation,
saved rules, all three routing choices and LAN access passed:

| Mode | Selected program | Other program | UDP received | Median / p95 ms |
|---|---|---|---|---|
| Full PC | Server egress | Server egress | 160/160 | 93.4 / 102.4 |
| Only selected apps | Server egress | Direct egress | 160/160 | 103.8 / 111.4 |
| Selected apps bypass | Direct egress | Server egress | 159/160 | 100.8 / 110.2 |

These are short functional trials, not evidence that residual transport loss or
future DPI blocking is solved. The exact carrier SHA-256 was
`7f666ad15348e88ea860534ee802051c722e1f0195d16b75a69789b6fdbdf653`.
The app's later preview-only DPI correction and version metadata were checked
offline. Cleanup verified the original default gateway, DNS and IPv6 binding,
removed test tasks and stopped the tunnel. The temporary account was revoked.

The owner confirmed live bot `/start` and `/status` replies. The real restricted
helper created a separate test account and returned a data-only profile; the app
imported it for the routing tests. Actual Telegram `sendDocument` then delivered
the owner's profile to the configured private owner chat. No server key or bot
token is exported. The source and public packages contain no live credentials.

A fresh isolated Debian 13 installation passed release verification, the optional
Telegram-disabled path, GUI exports for both owner and new users, repeat setup,
revocation and clean uninstall. The container had independent networking and a
time limit and was stopped afterward. Portable Python tests passed 29 cases;
profile import tests passed under PowerShell 5.1 and 7, covering data-only
whitelisting, traversal/duplicate/oversize rejection, private ACLs, malformed
directives and failed-import cleanup. Go module verification, vet and tests passed.
# 0.6.2 desktop disconnect recovery (2026-10-11)

The reported stuck disconnect was reproduced as an extra per-user FullControl
ACE on the protected runtime directory. The old Stop path rejected that ACL
before cleanup. The affected second PC was recovered without executing its
runtime scripts, and its normal Ethernet route was verified.

The rebuilt 0.6.2 app was tested on that Windows 10 PC, using the actual app's
button handlers and embedded helper-hash checks, with independent SYSTEM cleanup.
Normal Full PC connect/disconnect passed. A second connection deliberately added
the extra user ACE, replaced the runtime signal helper with a failing marker,
and substituted an unrelated PowerShell process into the saved supervisor record.
Disconnect passed, the marker did not execute, the unrelated process survived,
and the replacement runtime granted access only to SYSTEM and Administrators.
A third connect and ordinary disconnect then passed. Each connection returned a
tunnel ping (3/3 total); this was a lifecycle regression, not a performance run.

Final cleanup verified no CWG carrier/engine remained, default route via Ethernet
to its original LAN gateway and DNS, with the existing IPv6 binding enabled. Offline
recovery guards additionally cover unexpected task actions, linked runtime roots
and child links. App configuration and profile-import checks passed under Windows
PowerShell 5.1. The build passed Go module verification, vet and all Go tests with
local loopback networking allowed; the initial sandboxed run could not open test
sockets. Transport code was not changed for this release.

# 0.6.3 allocation optimization (2026-10-11)

A private, bounded CPU/allocation profile on the Windows 10 i7-6700HQ test PC
identified socket I/O as the largest CPU cost and significant codec allocation
churn. The adaptive client now uses explicit send/header scratch buffers and
in-place decryption of exclusively owned receive packets. The wire layout,
cryptography, padding, pacing, congestion control and repair remain unchanged.
Bootstrap and shared-server fanout retain the non-mutating decoder.

Four valid, unprofiled trials alternated 0.6.2 and the candidate twice using the
same verified 16 MiB download, 4 MiB upload, idle and game-like UDP phases.
Each version delivered 798/798 UDP probes; all downloads and uploads passed
integrity checks. Aggregate carrier CPU time fell from 9.563 to 8.703 seconds
(about 9%), while adaptive packet counts varied. Individual phase CPU results
were mixed; this is not a universal reduction guarantee. Latency was comparable
or slightly lower in these short trials. Full numbers, protocol expirations,
excluded harness runs and unexpectedly high traffic totals are in PERFORMANCE.md.
No server transport replacement was needed for these tests.

The codec benchmark on that PC improved from 3649-3658 ns and 4224 B / three
allocations per operation to 2484-2517 ns and zero allocations on the new path.
Go module verification, vet and all tests passed, including actual WireGuard,
loss/reordering and shared-user tests. New ownership/tamper/compatibility checks
passed; 8,099,146 fuzz cases compared both decoders without a failure. No new
concurrent buffer ownership was introduced; race instrumentation was not run.

The exact compact 0.6.3 app ZIP passed the actual UI button-handler regression on
the second PC: ordinary connect/disconnect, changed runtime ACL recovery without
executing a replaced helper or killing the unrelated process from forged state,
then successful reconnect/disconnect. All three tunnel pings returned. The
embedded tool hashes were enforced. Independent SYSTEM cleanup restored the
Ethernet default route, DNS and existing IPv6 binding; the controller's networking
was not changed. Offline recovery, routing and profile-import checks also passed.
Profiling harnesses, keys, profiles and raw results are excluded from the app ZIP.

# 0.6.4 public packaging (2026-10-25)

The public preparation retains the 0.6.3 transport. No new WAN-performance
measurement is claimed. Go module verification, vet and all regression tests
passed. Portable installer, management and Telegram tests passed 29 cases,
including preservation of the MIT license in owner and named-user exports.
Offline Windows routing/recovery and data-only profile import checks passed.

The exact packaged desktop executable rendered its offline preview with an
invented documentation address; it did not start a tunnel or change networking.
Release checks verify the embedded helper hashes, all archive manifests, the
13-file compact app whitelist, dependency notices and installer consumed-file
integrity for amd64 and arm64. arm64 remains cross-compiled only. The public
privacy review is described in PUBLIC-RELEASE.md.
