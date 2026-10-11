# Experimental request/reply UDP

## Queue fixes in 0.5.8

Loss-only backoffs now keep successful replies from the older sending flight
in the loss denominator, matching the older timeouts that are already counted.
Those replies still cannot justify growth or a delay decision at the reduced
rate. Delay-triggered reductions continue excluding both successes and timeouts
from their older flight. The loss threshold, reduction factor, growth cooldown,
request ceiling and timeout are unchanged; continued loss still reduces rate.

The burst-mode client hands outer replies to the event loop through a fixed
1024-packet channel (previously 256). It consumes packets immediately; there is
no wait for a batch. Original socket-read timestamps and native queue deadlines
are retained. The server's unauthenticated public input remains bounded at 256.

Small native packets still have 128 reserved queue slots. When there is unused
total capacity they may borrow up to another 128 slots, with the same short
deadline and four-small-to-one-bulk service rule. The total configured capacity
does not grow. `exchange_small_queue_full` and `exchange_bulk_queue_full` now
identify which data class rejects a packet. Together with
`exchange_control_queue_full`, their sum is `exchange_queue_full`.

Native handshake initiation, reply and cookie packets have a separate eight-slot
priority queue within the same total capacity. Their one-second deadline covers
brief idle polling gaps that would otherwise force a native handshake retry.
Exact native type words and lengths select this queue; ordinary encrypted data
keeps its existing deadline. Handshakes are not copied or retransmitted by CWG.
`exchange_control_sent` and `exchange_control_expired` distinguish their outcomes.

FEC defers parity when either original queue has spent half its deadline.
Originals continue under the normal priority rule. Deferred parity keeps its
50 ms lifetime and fixed bound; `exchange_fec_deferred` counts scheduling
decisions, not unique packets. This prevents recoverable redundancy from
consuming slots needed by originals that have not yet been encoded.

## Optional interleaved recovery in 0.5.7

`exchange_fec: true` selects authenticated, interleaved XOR recovery. It requires
exchange transport, an adaptive client, and `exchange_repair` disabled. Both
endpoints must support and enable it. A capability in the authenticated preflight
and bootstrap makes an incompatible endpoint fail before the Windows helper
changes routes. A capable server still accepts legacy clients without FEC.

Four interleaved groups hold up to four native WireGuard data packets each.
Originals are forwarded immediately; one parity packet per group can reconstruct
one missing original. This can tolerate four consecutive original-packet losses
when the losses occupy separate groups and their parity arrives. Two missing
originals in one group remain unrecoverable. Native WireGuard still authenticates
every reconstructed datagram; FEC does not replace either encryption layer.

Partial groups flush after an 8 ms target when the scheduler next runs. Parity
expires 50 ms after the group's first original and shares the existing scheduled
request slots, including during bulk load. Small originals retain priority, with
bounded turns for parity; an urgent small original can defer parity further.
There is no acknowledgement-driven retransmission or reliable outer stream.
Parity consumes capacity, so useful throughput can decrease. The fixed group and
parity bounds prevent unlimited buffering, and all state resets on association
changes. Receive groups have a 250 ms recovery lifetime and 128 fixed slots.

The framing is inside the encrypted exchange payload. Handshakes and native
packets above 1280 bytes pass in their original format; this preserves the existing
native MTU limit but leaves those larger datagrams without FEC. The current
1088-byte IP MTU keeps ordinary native data within the protected size.

Counters `exchange_fec_data`, `exchange_fec_parity`, `exchange_fec_recovered`,
`exchange_fec_expired`, `exchange_fec_skipped`, `exchange_fec_invalid`, and
`exchange_fec_late` distinguish these events. Recovery can include reconstructing
a reordered original before it arrives, so it is not an exact path-loss count.
Parity expiry is not a dropped primary packet. Outer `exchange_expired` remains
unchanged even when FEC recovers the enclosed native data.

Controlled tests cover real WireGuard with isolated and four-packet burst loss
in both directions, malformed metadata, bounded state, legacy compatibility and
the exact Windows/Linux executables. Target-network results were mixed, so the
regular deployment uses the corrected 0.5.8 build with spare-slot repair and
FEC defaults off; see [validation](VALIDATION.md#058-bounded-queue-corrections-2026-10-10).
This changes loss tolerance, not the observable outer packet format,
and makes no promise of DPI invisibility or protection from IP blocking.

## Optional stability protection in 0.5.6

The live stability profile uses `exchange_max_pps: 8000` on the client,
`exchange_reorder_ms: 2` and `exchange_repair: true` on both endpoints. Existing
bulk and small-packet queues remain unchanged. Both new options default off;
they require exchange transport, with adaptive scheduling on the client. They
do not change the authenticated wire format and can be enabled independently.

Reordering protection holds up to 128 authenticated outer sequence positions.
In-order data passes immediately. A gap is skipped after the configured 1..5 ms
target, including when no further packets arrive; late authenticated data is
still delivered. Empty exchanges advance ordering too. Timing feedback is
processed before buffering. This is a timer target, not a hard real-time bound:
OS scheduling can make the observed hold longer. No reliable ordered stream or
wait for a retransmission is introduced.

Repair retains at most 256 recently sent native WireGuard data packets. Each
may get one fresh outer encryption and sequence number in an otherwise empty
scheduled slot, at least 5 ms after the original and before its 30 ms expiry.
Primary queued data always wins. Handshakes are excluded. Native WireGuard
authenticates and discards duplicates; repair caches reset with the association.
Saturated traffic may leave no slots for copies, and both copies can be lost.

Repair does not schedule extra requests or raise the ceiling. Copied payloads
can affect the adaptive controller's useful-reply demand, so total traffic and
CPU cost are not guaranteed identical to a run with repair disabled. The
existing padded polling bandwidth cost remains substantial. `data_sent` and
`data_received` include copies; they are not unique application-byte counters.

New counters report reordered requests/replies, held/late/forced reorder
deliveries and maximum hold time, plus repair copies sent and expired. Repair
expiry counts discarded backups, not lost primary traffic. `exchange_expired`
still counts missing outer replies, even if their native data was recovered by
a copy. Short test results do not establish future DPI resistance or eliminate
the remaining path loss. See [validation](VALIDATION.md#056-stability-experiments-2026-10-10).

## Congestion feedback in 0.5.5

A rate reduction now marks the send-time boundary of the previous flight.
Replies for requests sent before that boundary still count as deliveries,
release their in-flight slots and deliver any accepted native payload. Their
RTT remains observable, but does not justify another delay cut or growth.

When a reduction was triggered by delay, requests sent before that reduction
also cannot trigger another cut through later timeouts. Those timeouts still
retire slots and count as expirations. Loss-only reductions retain the prior
conservative timeout response; suppressing their older losses prolonged overload
in a constrained-link experiment. Fresh delay and loss still reduce the rate,
even during the growth cooldown. This is feedback accounting, not a blind period
that ignores new congestion.

`exchange_prior_flight_replies` and `exchange_prior_flight_expired` identify
such old feedback (the expiry counter applies only after a delay cut).
`exchange_loss_backoffs` and `exchange_delay_backoffs`
identify the trigger; both may increment for one backoff when both conditions
hold. `exchange_backoffs` still counts decisions and `exchange_expired` still
counts every request timeout. These counters do not identify an ISP's intent.

The 9000 requests/s ceiling, pacing, queue capacity, 350 ms bulk deadline,
60 ms small-packet deadline, wire format and encryption are unchanged. The next
user-run comparison omits `-HighRate`, focusing on 25/50 Mbps load, upload
bursts, duplex and loaded ping. Local controller regression tests cannot prove
the cause of a past ISP disturbance or a future improvement on that path.

## Burst recovery and headroom in 0.5.4

The next comparison uses a 9000 requests/s ceiling, a 350 ms bulk deadline on
both peers, and `exchange_queue_packets: 4096`. This optional field accepts
256..4096 and requires burst mode; omission retains 2048. Of the configured
capacity, 128 places remain reserved for packets no larger than 256 bytes.
Those small packets retain their 60 ms deadline and four-to-one priority.
The client native input handoff now matches the server's 2048 packets; public
input remains at 256. This prevents a local native batch from dropping small
packets before the priority queue sees them. Socket-read timestamps still bound
their total userspace age.

Cold growth is now at most fourfold per control interval, gated on authenticated
replies and demand. After any congestion backoff, growth is limited to 10 percent
(at least 100 requests/s) per interval for that association. The existing
delay/loss backoff, cooldown, in-flight limits and bounded pacer still apply.

The controller remembers a rate for 30 seconds only after an interval actually
delivers at least 80 percent of that rate, without expired requests. Local
backlog of at least 16 packets or an authenticated server backlog hint can
resume that rate. Resume requires a recent reply, acceptable smoothed RTT,
no newly expired requests and no cooldown. A congestion backoff erases the
remembered capacity; a fresh association starts without it. Empty polling and
isolated small packets do not trigger resume. `exchange_resumes` counts it.

This provides more sending capacity and faster burst recovery while preserving
small-packet priority. Bulk packets may wait up to 350 ms on the server, compared
with 200 ms previously. Length is only a heuristic: full-sized latency-sensitive
datagrams share the bulk queue. Sustained overload can still lose packets.
At 9000 requests/s, outer UDP payload traffic can approach 102 Mbps EACH way,
plus IP/link overhead. This is a test ceiling, not a promised Internet speed.
The encrypted version-2 packet format and one same-sized reply per request are
unchanged. Older binaries reject the new optional configuration field.

The user can run `Measure-AdaptivePerformance.ps1 -Bundle .\config -HighRate`
to add a six-second 75 Mbps download target to the existing comparison. This
keeps each transfer below the endpoint's 64 MiB cap and totals 337.5 MB of
application payload plus carrier overhead. Without `-HighRate`, the original
eight transfers and 281.25 MB remain unchanged. The assistant runs only local
mock-peer validation; the user runs the real Windows tunnel test.

Per-application split tunneling is a separate recorded
[follow-up requirement](FOLLOW-UP.md).

## Input bursts in 0.5.3

Burst-enabled servers now reserve 2048 packets for the native WireGuard socket's
handoff to the event loop. The public input remains limited to 256 packets.
This addresses a second loss point exposed after increasing the kernel receive
buffer in 0.5.2. Exchange queue deadlines include time since the socket read;
waiting in the handoff does not give packets a fresh deadline. Kernel residence
before the read is not measured by this timestamp.

The bulk deadline now accepts up to 400 ms. The next deployment uses 350 ms
on the client and retains 200 ms on the server. An observed 1874-packet upload
burst needs at least about 312 ms at 6000 requests/s, exceeding 200 ms.
Small packets keep their 60 ms deadline and priority; the 1920 bulk/128 small
queue capacities and sending ceiling are unchanged. This trades additional
possible bulk waiting time for fewer burst drops; it does not add bandwidth.

Reader drop and peak counters identify each socket: server reader 0 is public
and reader 1 native; client reader 0 is native and reader 1 public. The existing
queue_dropped aggregate is retained. Exchange queue expiration/full counters
are separate from these input drops and from kernel UDP drops.

## Burst handling and investigation in 0.5.1

On both peers, optionally add exchange_burst_ms: 200 alongside
exchange_queue_ms: 60. The burst deadline must be at least the normal deadline
and at most 400 ms as of 0.5.3 (250 ms previously). Omitting it preserves the previous FIFO behavior.

When enabled, packets over 256 bytes use the bulk deadline. Smaller encrypted
WireGuard packets retain the shorter deadline and can pass bulk packets, with
at most four small packets before a waiting bulk packet. This is a length-based
hint, not identification of TCP flows or decrypted packet contents. It can
reorder the two size classes; order within each class is preserved. The total
limit remains 2048 packets, with 128 places reserved for small packets.
At 6000 requests/s, a 1000-packet burst needs about 167 ms to drain, exceeding
the previous 60 ms deadline. Bulk packets may now wait longer instead of
being dropped, while small packets avoid that bulk backlog. Sustained overload
still expires data at bounded deadlines; this does not create more bandwidth.

Burst-enabled deployments report timestamped counters every second, including
small-packet queue depth and maximum waiting time. The carrier format, 6000/s
ceiling, native MTU and cryptography are unchanged.

The updated performance helper compares 25 and 50 Mbit/s single downloads,
two simultaneous 25 Mbit/s downloads, the original curl upload, a smoothly
paced upload, and simultaneous upload/download. It transfers 281.25 MB of
application payload plus polling/padding overhead and stops CWG in cleanup.
The new cwg smooth-upload command generates its own payload, verifies the
server's hash, and permits only the tunnel address or localhost. It uses
small paced reads with bounded catch-up, retaining OS TCP buffer autotuning.
The original upload remains as a direct comparison because curl rate limiting
averages over several seconds ([curl manual](https://curl.se/docs/manpage.html#--limit-rate)).

The tunnel-only HTTP endpoint records per-transfer progress, blocked I/O time,
and Linux TCP information: retransmissions, congestion window, peer receive
window, acknowledged bytes and time limited by receive/send buffers. Host UDP
buffer errors are cumulative counters and must be compared as deltas. Fields
follow the [Linux TCP UAPI](https://github.com/torvalds/linux/blob/v6.12/include/uapi/linux/tcp.h);
unsupported platforms report null rather than invented zeros. The endpoint
has at most four handlers, no payload storage, and a 45-second transfer deadline.
Telemetry starts only for user-initiated transfers; it creates no test traffic.

## Adaptive scheduling in 0.5.0

For the client, set transport to exchange, exchange_pps to 200,
exchange_max_pps to 6000, and exchange_queue_ms to 60. On the server, set
exchange_queue_ms to 60; exchange_max_pps is client-only. These are private
JSON configuration fields. Leave native MTU unchanged while comparing rates.

Omitting exchange_max_pps retains the legacy fixed scheduler. The adaptive
ceiling accepts 200..12000 requests/second; the active base still accepts
10..200 (100 when omitted). Idle polling stays around four/second. The initial
6000/s ceiling permits roughly 52 Mbit/s of inner IP packets at MTU1088 before
application overhead. At that ceiling, outer UDP payload traffic is about
68 Mbit/s EACH way, excluding IP/link overhead. Replies require equal-sized
requests even for a download, so upload usage is substantial.

The controller initially sends at the base rate. Once authenticated replies
show backlog, local queued data, or high response utilization, it doubles the
rate per control interval up to its ceiling. The interval is at least100ms and
at least the smoothed RTT. More than2% expired requests in a populated interval,
or a minimum interval RTT over25ms above the session minimum, reduces the rate
by30% with a recovery cooldown. Unused capacity decreases toward the base.
This is an experimental controller, not a proven general-purpose congestion
control algorithm; high-rate acceptance on the target network is unverified.

Credits accrue by elapsed time. Timer wakeups are at least1ms apart, coalescing
high-rate sends into at most2ms of traffic and never more than32datagrams.
A delayed process cannot accumulate a large catch-up burst. UDP datagrams stay
separate; this does not claim kernel batch I/O on either platform. Outstanding
requests use bounded indexed storage and linear total expiry work, with a
RTT/rate-sized working window and a hard16384 pending limit. Reply deadlines
adapt between300ms and2s. Late authenticated replies are counted separately.
The replay window is32768packets and remains fixed-memory.

Explicit queue deadlines accept20..500ms and use at most2048native datagrams.
The initial60ms deadline replaces the500ms baseline; expired and full-queue
drops are separate counters. A cold client may hold at most one second of
pre-association data, then starts its normal queue deadline when the outer
association is ready, avoiding loss of the first native WireGuard handshake.
No new cipher, outer retransmission, FEC, aggregation or fragmentation is added.

Adaptive deployments report counters every5s. For tracing, both sides can use
`--trace-sample 128` with `--trace`: the same datagram hash is selected at either
endpoint, approximately1/128 of packets. A trace_config record declares the
sampling. The Windows helper selects this automatically for adaptive clients.
The100000record cap remains; samples must never be reported as a full capture.
Unsampled aggregate counters still cover the whole session.

The user-run Measure-AdaptivePerformance.ps1 requires the temporary
tunnel-performance-server.py endpoint bound only to the WireGuard address.
The original 0.5.0 test transferred200MB of synthetic application payload with additional polling/
padding overhead, measures ping throughout each load phase, validates complete
transfers by SHA256, saves results, and stops CWG in finally. It starts no test
automatically; the user must invoke it. The diagnostic HTTP is application data
inside the UDP VPN, not a TCP-based outer transport.

## Legacy fixed scheduler and wire format

This changes the observed traffic pattern, not WireGuard's cryptography. The
previous paced raw test still lost about 56% of outgoing packets and 60% of
return packets, without local pacing/receive-queue drops or socket errors.
Independent request/reply probes repeatedly passed. That motivates this mode;
it does not prove which network device is filtering or that this mode will work.

Set `"transport": "exchange"` on both peers. Remove `pace_kbps` and
`pace_queue_ms`. Set `"exchange_pps": 100` on the client (default 100, allowed
10..200). The client controls the polling rate; the server does not independently
send background requests. Native WireGuard MTU must be at most 1280; the Windows
helper requires it explicitly. The current private test keeps IPv4 MTU 1088.

After the authenticated bootstrap, each request contains zero or one complete
WireGuard datagram. It is padded to a random 1400..1440 UDP payload bytes. The
server may forward the embedded datagram only to the configured loopback backend.
It returns exactly one response of the request's size, containing zero or one
queued return datagram. No server data is sent without a fresh authenticated
request. A response echoes the request sequence inside its encryption; the
client checks that it corresponds to an outstanding request and matches its size.
Reordered requests are answered to their own source, even after endpoint movement.

The outer codec uses XChaCha20-Poly1305, random 24-byte nonces and HKDF-SHA256,
as before. Version 2 has independent directional bootstrap/session derivation
domains and an encrypted version field, preventing confusion with version 1.
The preflight also uses version 2, so a legacy listener cannot pass it. Session
replay protection, timestamp checks, fresh challenges and silent rejection of
unauthenticated packets remain. No new cipher is invented.

The client targets 100 requests/second during activity, with interval jitter,
and about four/second after two seconds without native data. A queued-data flag
can extend activity. Multiple independent requests can be in flight; there is
no stop-and-wait delay or delivery-order requirement. Up to 512 requests are
tracked; they expire after three seconds. After 15 seconds without authenticated
responses, the client discards that association and starts a new bootstrap.
Each peer buffers at most 128 native datagrams for at most 500 ms before dropping
them. There is no outer retransmission, FEC or promise to repair packet loss.

At the default rate, outer traffic is roughly **1.1 Mbit/s in each direction**
while active, even if little useful data is carried, and **45 kbit/s each way**
while idle. Useful throughput is lower and depends on MTU and network delivery.
These figures exclude IP/link overhead; jitter and scheduling change the actual
rate. Higher native traffic can exhaust the bounded queues. This is not a claim
of native WireGuard performance. Stop the client when the test is finished.

`exchange_requests` and `exchange_replies` count successful socket operations
at the sender and authenticated events at the receiver. `exchange_empty` counts
empty replies. `exchange_expired` counts requests without an accepted reply in
time, `exchange_pending` is the current request count, `exchange_window_full`
counts skipped sends, and `exchange_queue_dropped` counts expired/overfull or
reset native-data queues. They complement, rather than replace, packet-hash
comparison at both endpoints. A timeout is not proof of ISP classification.

This is a custom implementation of a request/reply strategy, not a claim of a
scientifically novel or permanently undetectable protocol. Endpoint addresses,
sizes, timing, rate and random-looking bytes remain observable. Research on
[blocking fully encrypted traffic](https://www.usenix.org/conference/usenixsecurity23/presentation/wu-mingshi)
shows why an unknown packet format alone is insufficient; that study concerns
a different censorship system and does not identify the cause on this network.

The real user must run the target-network test. Automated tests use temporary
localhost endpoints and keys, including real WireGuard with channel TUNs. Legacy
session/replay probe commands are unavailable in exchange mode; use `probe` and
the ordinary Windows Start/Diagnose/Stop helpers.

## Shared-rate negotiation (0.6 development)

`exchange_shared: true` requires adaptive exchange, independent credentials and
`server-multi` on the server. The authenticated HELLO payload is the ASCII marker
`cwg-share1`. WELCOME contains the existing 32-byte random token followed by a
one-byte capability marker with value 1. Session derivation uses only the token,
as before. A shared server rejects legacy HELLO; preflight PROBE remains unchanged.

Shared EXCHANGE payloads begin with four encrypted bytes: acknowledged grant
version (uint16, big endian), desired rate (uint8), and configured maximum rate
(uint8). Rate values use units of 100 requests/s, from 2 through 120; desired must
not exceed maximum. The unchanged native datagram follows at offset 4.

Shared EXCHANGE_REPLY keeps the eight-byte echoed request sequence and one-byte
`more` flag, then adds a uint16 big-endian grant version and uint8 rate. The native
datagram follows at offset 12. Empty requests/replies still carry this metadata.
The maximum 1312-byte native payload still fits within a 1400-byte outer datagram;
the normal outer size range remains 1400–1440 bytes. There is no clear account ID.

Only newest authenticated requests change server demand or acknowledge a grant.
Only a newest reply matching an outstanding request changes the client grant.
The grant expires one second after that request was sent, not one second after
its reply arrived. The server holds a silent reservation for two seconds. Rate
reductions retain reserved capacity until acknowledged; increases use capacity
already free. At most 20 allocation updates per second bound generation changes.
Version zero is skipped. All three minimum slots are reserved even before all
accounts exist. A configured aggregate reduction may temporarily leave old
reservations above the new limit while reductions are acknowledged; the bounded
server ingress limiter continues to enforce its new dispatch ceiling.

These grants coordinate cooperative clients and do not replace authentication,
per-user queues, congestion control, or the ingress limiter. An authenticated
abusive user can still consume network/cryptographic resources. Experimental FEC
and shared negotiation cannot be enabled together in this version.

Grant capacity is quantized down to 100-request/s units and leaves 10% below the
hard dispatch ceiling, subject to the three minimum reservations. The dispatch
token bucket permits up to 16 ms of its configured rate in an immediate burst
(minimum eight tokens). This handles network-compressed arrivals without adding
queue time. Worker handoffs hold at most 64 packets, and a full handoff causes the
fair dispatcher to try another user while retaining the original receive time.
