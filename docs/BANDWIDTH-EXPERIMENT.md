# Opt-in demand experiment, after 0.6.3

Status: research on `codex/bandwidth-demand`; **not enabled or included in the
published 0.6.3 app**. The second PC was restored to the released client after
testing. The live server was not replaced. No account/profile defaults changed.

## Problem and first implementation

A single server queue bit could restore previously proven high capacity, even
for a brief burst. Unused capacity then decreased slowly. The CPU comparison
recorded many padded empty replies and much more traffic than ideal per-packet
overhead calculations predicted.

The experimental `exchange_economy` boolean is default-off and accepted only on
an adaptive exchange client without FEC. It changes demand policy, not framing:

- Count successful original uploads and authenticated data-bearing replies per
  controller interval. Replies still include repair in this estimate, preserving
  headroom for useful backup traffic.
- When there is no local queue and fewer than a quarter of replies report a
  server queue, reduce an oversized rate after two intervals. Target 1.5 times
  measured demand, at least the existing active base, and halve at most once per
  reduction. Existing authenticated congestion/loss backoff takes precedence.
- Do not restore a remembered high rate solely from a remote queue bit. A real
  upload queue can resume only capacity needed to drain that observed burst in
  about 20 ms, capped by recently proven capacity and existing loss/delay guards.
- Retain authenticated growth for sustained demand, the normal rate ceiling,
  shared-server grants, queue bounds/deadlines, 4/s idle policy and repair code.

Packets remain 1400-1440 bytes with one same-sized response. Encryption, random
nonces/padding, authentication, anti-replay and anti-amplification are unchanged.
Traffic timing changes, so unchanged framing does not imply unchanged filtering
behavior. The experiment does not claim future DPI invisibility.

The adaptive client also records `exchange_request_bytes` and
`exchange_reply_bytes`. These count successfully written request UDP payloads
and authenticated matched reply UDP payloads. They exclude bootstrap, unmatched
or invalid traffic, outer IP/UDP, link overhead and other processes. They are
not a complete ISP billing meter. Other transport modes do not populate them.

## Initial Windows 10 WAN results, 2026-10-11

Same i7-6700HQ second PC, server and existing account. Only test programs were
tunneled. Each trial used 4 seconds of idle, game-like UDP, a verified 16 MiB
download paced at 25 Mbps, and a verified 4 MiB upload paced at 10 Mbps. UDP
probes ran at 20/s during game/download/upload phases. Automatic SYSTEM cleanup
was validated before starting. Workload phases were identical to the CPU study.

| Measurement | Released policy + counters | First prototype | Revised prototype |
|---|---:|---:|---:|
| Recorded request + matched reply UDP bytes | 243,733,721 | 188,262,560 | 114,454,611 |
| Relative reduction | baseline | 22.8% | 53.0% |
| Requests | 85,861 | 66,312 | 40,311 |
| Empty replies | 53,812 | 39,993 | 14,194 |
| Application probes delivered | 400/400 | 400/400 | 398/398 |
| Game median / p95, ms | 97.53 / 106.54 | 100.92 / 111.78 | 100.27 / 109.15 |
| Download median / p95, ms | 92.39 / 108.28 | 95.03 / 109.60 | 93.21 / 104.00 |
| Upload median / p95, ms | 92.24 / 106.43 | 94.60 / 109.70 | 93.29 / 102.73 |
| Verified download completion, s | 5.591 | 5.698 | 5.586 |
| Verified upload completion, s | 4.108 | 5.082 | 5.016 |
| Carrier CPU seconds over measurement phases | 4.719 | 3.609 | 3.047 |
| Expired outer exchanges | 25 | 0 | 3 |
| Client queue drops / expirations | 0 / 0 | 0 / 0 | 0 / 0 |
| Client repair copies sent / expired | 11,120 / 6 | 10,683 / 67 | 10,282 / 549 |

The revised prototype substantially reduced traffic and CPU in this short
workload. Download completion was similar and all application probes arrived.
However, **upload completion was about 22% slower**, game p95 was 2.6 ms higher,
and fewer spare exchanges delivered backup copies before their existing expiry.
Those tradeoffs prevent treating the experiment as a verified replacement for
the release. The first prototype disabled all rapid resume; the revision added
bounded upload resume and changed the sustained remote-backlog threshold from
one eighth to one quarter. These sequential single trials are not a statistical
guarantee and do not measure prolonged filtering or heavy burst loss.

The revised trial still used roughly 5.46 times its 20 MiB bulk payload in the
recorded exchanges, including small probes, padding and tail polling. A 53% cut
from a wasteful baseline is not low absolute overhead. Small-packet padding and
one-request/one-reply cost remain significant.

## Validation and next gate

Go tests and vet pass with the option off and with dedicated economy tests for
demand decay, authenticated growth, loss backoff, bounded upload resume and stale
capacity rejection. Actual WireGuard tests cover IPv4/IPv6/MTU, an unchanged
shared server, and recovery of deliberately lost native packets through repair.
All existing regression tests pass. This does not substitute for sustained WAN
loss/burst validation.

Before any default or app UI change: isolate the short-upload slowdown; repeat
counterbalanced comparisons; test burst loss and pauses under mixed traffic and
two/three users; quantify exact wire bytes over matching measurement windows.
Retain the release behavior unless latency and loss remain acceptable. Do not
put this experimental field into profiles imported by the released desktop app;
its strict data-only importer intentionally has no new option yet. The test used
an isolated private kit and its command-line helper.

Reducing the active floor risks extra polling latency. Shorter idle packets,
asymmetric responses or packing several small native packets would require a
different negotiated format and security/compatibility analysis; they are
outside the current requirement to preserve packet format.
