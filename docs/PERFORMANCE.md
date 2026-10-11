# Bandwidth, CPU and latency

These calculations describe the shared request/reply profile. They are not
traffic-meter measurements or a guarantee about future filtering. Desktop
0.6.2 changes disconnect recovery, not transport performance.

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

A local Windows codec microbenchmark on a Ryzen 7 7800X3D measured **1879 ns per
seal + open, 4224 bytes allocated and 3 allocations per operation** for a
1312-byte WireGuard payload in a 1400-byte outer packet. It reported 681 MB/s
using 1280-byte useful-payload accounting. Command:

```powershell
go test ./internal/wire -run '^$' -bench '^BenchmarkSealOpen1280$' -benchmem -benchtime=3s -count=1
```

This excludes sockets, scheduling, routing, WireGuard and the actual network.
It is not a Windows carrier CPU measurement or end-to-end speed claim. The
previously sampled server CWG load of 24.60% of one CPU in a 52.573-second
three-user test is a Linux measurement, not Windows process CPU usage.
See VALIDATION.md for its context.

## Recommended optimization order

1. **Profile the real Windows carrier.** Capture CPU and allocation profiles
   separately on the test PC at idle, with gaming-like UDP, and at a fixed
   download/video rate. Record CPU time, packet rate, application loss and
   latency percentiles. The [Go diagnostics guide](https://go.dev/doc/diagnostics)
   explains profiling tools and overhead. Profiling should be local, bounded
   and disabled during normal use.
2. **Reduce allocations and unnecessary copies.** Reuse bounded packet buffers
   with explicit ownership; use caller-owned AEAD destinations where safe.
   Preserve replay handling, fresh nonces, queue bounds and buffer lifetimes.
   This can reduce garbage collection without changing the wire format. Verify
   with benchmarks, race checks and loss/load tests; no improvement percentage
   has been established yet.
3. **Optimize the remaining measured bottleneck.** If padding randomness
   dominates, investigate whether already-encrypted filler needs independent
   randomness; never weaken nonce generation. If socket I/O dominates, assess
   Windows-specific batching. Larger queues/batches can worsen ping, so retain
   the existing latency bounds unless measurements justify a change.
4. **Treat bandwidth changes as a separate protocol experiment.** Smaller idle
   packets, small-packet packing or fewer empty replies could cut traffic, but
   change observable sizes/timing or add polling delay. They need compatibility
   negotiation and controlled WAN comparisons. Cutting polling is not a free
   latency improvement.

The first implementation target should be memory churn at the existing useful
speed. Success means lower CPU without material regression in application loss,
median/p95/p99 latency or recovery. Longer mixed-traffic tests matter more than
raising peak speed for this use.

## Other tradeoffs

Padding conceals obvious exposed WireGuard fields and lengths, but does not
make CWG indistinguishable from HTTPS. Port, timing, lengths, volume and the
server address remain observable. Avoiding one classifier does not guarantee
future invisibility or prevent IP blocking.

Split tunneling preserves the local LAN, but ordinary Windows DNS uses the
tunnel globally. Selected public IPv6 is rejected, and disconnect restores
ordinary networking without a persistent kill switch.
