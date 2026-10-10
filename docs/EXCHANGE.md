# Experimental request/reply UDP (0.4.0 through 0.5.1)

## Burst handling and investigation in 0.5.1

On both peers, optionally add exchange_burst_ms: 200 alongside
exchange_queue_ms: 60. The burst deadline must be at least the normal deadline
and at most 250 ms. Omitting it preserves the previous FIFO behavior.

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
