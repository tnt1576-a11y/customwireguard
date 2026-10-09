# Experimental request/reply UDP (0.4.0)

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
