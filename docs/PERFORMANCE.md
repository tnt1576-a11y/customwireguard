# Bandwidth, CPU and latency

This describes the shared request/reply profile. Calculations and measurements
are labeled separately; neither predicts future filtering. Version 0.6.3 reduces
buffer allocation without changing the packet format or traffic policy.

## Extra traffic

Each authenticated request permits one same-sized reply, normally 1400-1440
bytes of UDP payload. Empty requests/replies are padded too. A download needs
an ongoing uplink stream even if the application itself has little to upload.
Uploads have the reverse cost. Full-duplex traffic can use both directions
productively; one-way traffic usually cannot.

At the 1420-byte midpoint, combined traffic is
`requests_per_second * 1420 * 2` bytes/s before outer IP/UDP and link overhead.

| Sustained request rate | Each direction | Sent + received per hour |
|---|---:|---:|
| 4/s, normal idle | 0.045 Mbps | 40.9 MB |
| 200/s, configured active base | 2.272 Mbps | 2.04 GB |
| 2400/s, per-user grant with three busy users | 27.264 Mbps | 24.54 GB |
| 6800/s, available grant with one busy user | 77.248 Mbps | 69.52 GB |

Decimal units; hourly totals assume a constant rate for the whole hour. Adaptive
scheduling normally changes it. Grants are not useful application speeds. Idle
resumes after the quiet interval; users do not continually consume the active
reservation. Shared scheduling keeps arrival headroom and reserves capacity for
other slots; see MULTI-USER.md. A provider may bill only one direction: combined
traffic figures do not predict its bill.

Outer IPv4 + UDP adds 28 bytes per packet, about 2% here, before link overhead.
A full 1280-byte inner IP packet becomes a 1312-byte WireGuard packet, then
roughly 1420 bytes outside. That is about 11% in its data direction; the paired
reverse packet is additional. TCP/IP headers, polling, retries and repair copies
further reduce the useful fraction. Small gaming packets have proportionally
much more padding. Describing this as only an 11% overhead VPN would be wrong.

## CPU findings

`cwg-app-carrier.exe` is the Go carrier copied into the protected runtime. Its
work is separate from sing-box, which provides local routing and WireGuard.
The carrier does outer encryption/decryption, fresh nonce and padding generation,
buffer allocation/copying, per-datagram UDP I/O, and request/window bookkeeping.
The pacer already groups bounded work; batching is not completely absent.

A bounded CPU/allocation profile on the Windows 10 test PC (i7-6700HQ, 8 logical
CPUs) found Windows socket calls were the largest cost: `runtime.cgocall` was
48.7% of CPU samples. During the profiled mixed workload the carrier allocated
486 MB, with Seal, receive-buffer copies and Open accounting for most of it.
Profiling was confined to a private test build; releases have no profiling
listener or automatic profile recording.

The 0.6.3 adaptive client reuses its send/header scratch buffers and decrypts
exclusively owned receive packets in place. Queued/reordered payloads retain their
own backing arrays. Shared bootstrap fanout remains non-mutating. Cryptography,
fresh random nonces/padding, packet sizes, rates, deadlines and repair stay the
same. Existing server installations need no upgrade for this client change.

On the same test PC, three offline codec repetitions measured:

| Codec path, 1312-byte payload / 1400-byte packet | Time per seal + open | Allocation |
|---|---:|---:|
| 0.6.2 | 3649-3658 ns | 4224 bytes, 3 allocations |
| 0.6.3 scratch + in-place path | 2484-2517 ns | 0 bytes, 0 allocations |

That is about 32% less codec time. It excludes sockets, scheduling and WireGuard,
so it is not a claim of 32% lower overall CPU. Reproduce the new codec benchmark:

```powershell
go test ./internal/wire -run '^$' -bench '^BenchmarkSealOpenScratch1280$' -benchmem -benchtime=2s -count=3
```

Four valid unprofiled WAN trials alternated original/optimized/original/optimized.
Each used a 4-second idle sample, 20/s game-like UDP, a verified 16 MiB download
paced at 25 Mbps, and a verified 4 MiB upload paced at 10 Mbps. CPU windows also
include startup/tail work within each phase. Only the test programs were routed
through CWG; the controller PC's VPN was not changed.

| Measurement | 0.6.2, two trials | 0.6.3, two trials |
|---|---:|---:|
| Total carrier CPU seconds across phases | 9.563 | 8.703 |
| Phase-weighted CPU, percent of one logical CPU | 17.58% | 16.01% |
| Per-trial total CPU seconds | 4.047 / 5.516 | 3.984 / 4.719 |
| Download phase CPU, one CPU | 23.31 / 34.35% | 23.82 / 29.51% |
| Upload phase CPU, one CPU | 19.51 / 26.69% | 21.06 / 19.92% |
| Game UDP delivered | 239/239 | 240/240 |
| Download UDP delivered | 320/320 | 319/319 |
| Upload UDP delivered | 239/239 | 239/239 |
| Game median / p95 ranges, ms | 101.00-108.12 / 113.91-113.93 | 100.11-101.04 / 110.37-112.09 |
| Download median / p95 ranges, ms | 95.24-103.73 / 110.12-112.84 | 93.95-98.07 / 107.85-109.04 |
| Upload median / p95 ranges, ms | 94.87-103.90 / 110.76-112.06 | 93.76-98.11 / 103.96-110.58 |
| Download completion, seconds | 5.590 / 5.602 | 5.776 / 5.582 |
| Upload completion, seconds | 4.187 / 4.206 | 4.113 / 4.159 |
| Final request counters | 67,758 / 83,101 | 70,018 / 91,530 |
| Expired outer exchanges | 54 / 1 | 15 / 1 |

Measured aggregate CPU time fell about **9%**, with **0/798 lost application
probes in each version**. This is a small sample, not a guaranteed reduction:
adaptive rates varied, and individual phases were sometimes slightly worse.
Task Manager's whole-machine CPU scale is approximately these percentages
divided by eight on this PC. Outer exchange expiry is different from application
loss; repaired traffic and requests still pending at stop must not be called loss.

One run was excluded because the temporary UDP echo endpoint expired; another
aborted before measurements on a concurrent state-file read. Both were cleaned
up and repeated. No application probe losses, session resets, reader drops or socket errors
occurred in the valid measured trials. Nonzero outer expirations are listed above.
Full tests, authenticated tamper/ownership checks and 8.1 million fuzz cases passed.
These short tests do not establish long-term network stability.

## Measured traffic and next work

The same four trials transferred **20 MiB of useful bulk data each**, plus the
small UDP probes. Ethernet counters totaled **198-265 MB sent + received** per
trial: roughly 9.5-12.6 times the bulk bytes. These counters include link traffic,
test-tail polling and possible LAN/background traffic, so they are not exact
per-process billing measurements. They nevertheless show why an idealized
130-140% extra-data estimate is insufficient for this workload.

Most replies were empty (the transport still pads them to 1400-1440 bytes),
and repair duplicates also affect the demand controller. The CPU release does
not reduce this bandwidth. The next experiment should reduce unnecessary demand
and polling, measure useful bytes against actual outer traffic, and retain repair
until tests show it is safe to change. Small requests/asymmetric replies would
require explicit compatibility and anti-amplification design.

## Recommended optimization order

1. **Reduce unnecessary traffic.** This also reduces expensive socket calls,
   encryption and allocations. Compare identical paced workloads, latency and
   packet loss before changing the default policy.
2. **Assess the remaining socket cost.** Windows-specific batching might help,
   but larger queues/batches can worsen ping. Retain current latency bounds.
3. **Treat packet-size changes as a separate protocol experiment.** Smaller idle
   packets, small-packet packing or fewer empty replies could cut traffic, but
   change observable sizes/timing or add polling delay. They need compatibility
   negotiation and controlled WAN comparisons. Cutting polling is not a free
   latency improvement.

The [Go diagnostics guide](https://go.dev/doc/diagnostics) explains profiling and
its overhead. Longer mixed-traffic tests matter more than raising peak speed.

## Other tradeoffs

On the research branch, [the opt-in bandwidth experiment](BANDWIDTH-EXPERIMENT.md)
records the initial implementation and WAN tradeoffs. It is not part of 0.6.3.

Padding conceals obvious exposed WireGuard fields and lengths, but does not
make CWG indistinguishable from HTTPS. Port, timing, lengths, volume and the
server address remain observable. Avoiding one classifier does not guarantee
future invisibility or prevent IP blocking.

Split tunneling preserves the local LAN, but ordinary Windows DNS uses the
tunnel globally. Selected public IPv6 is rejected, and disconnect restores
ordinary networking without a persistent kill switch.
