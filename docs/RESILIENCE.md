# Detection limits and continuity

CWG currently hides native WireGuard message types and lengths behind authenticated
encrypted UDP. Unknown or unauthenticated probes receive no useful response;
replay checks and bounded queues remain in place. These properties reduce easy
signature matching and probing. They do not make an endpoint invisible.

Observers still see the destination address, UDP port, packet lengths, timing,
duration and volume. The current carrier's 1400..1440-byte packets, matching
request/reply sizes and high-rate polling are potential behavioral fingerprints.
Using UDP 443 does not make this traffic HTTPS or QUIC. An operator can block the
destination IP irrespective of encryption or whether it has proved VPN use.

The 2024 USENIX study [Fingerprinting Obfuscated Proxy Traffic with Encapsulated
TLS Handshakes](https://www.usenix.org/conference/usenixsecurity24/presentation/xue-fingerprinting)
demonstrates fingerprinting of some obfuscated proxies despite padding and
encapsulation. It supports caution about relying on a new header alone; it does
not establish detection of CWG or identify the cause of loss on this network.
The 2025 research [Censorship Evasion with Unidentified Protocol Generation](https://www.usenix.org/conference/usenixsecurity25/presentation/wails)
explores generating different protocols and evaluating their detectability. It
is a possible direction for future controlled profile experiments, not a promise
that an untested custom protocol will remain undetected.

The current priority is to preserve measured stability and collect bounded,
sampled diagnostics. Do not introduce unmeasured padding, jitter or cover traffic
into the working profile: extra overhead or delay could undo the latency gains.
Future profile experiments should vary one characteristic at a time, use separate
configuration/versioning, and compare loaded ping, loss, overhead, authentication
and probing behavior before adoption. Keep cryptography standard. A unique profile
can itself be distinguishable; novelty is not evidence of censorship resistance.

For continuity after an address block, a separately provisioned fallback endpoint
and a tested recovery procedure can reduce downtime. They cannot prevent blocking,
and no additional server or automatic IP rotation is configured by this update.
Keep client credentials distinct when adding users and do not publish live
credentials or client packages. No future-DPI or permanent-IP-availability
guarantee is possible from these tests.
