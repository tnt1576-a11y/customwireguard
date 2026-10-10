# CWG v1 protocol and threat model

This document describes an experimental wrapper, not a new WireGuard cipher suite.
It has no formal security proof or external audit. Use test-generated keys only
in tests and unique random secrets for real pairs.

## Wire format

Each UDP payload is a random 24-byte nonce followed by XChaCha20-Poly1305
ciphertext and its 16-byte tag. The encrypted plaintext contains:

| Field | Bytes |
|---|---:|
| Version (1) | 1 |
| Message type | 1 |
| Unix seconds, big-endian | 8 |
| Client association ID | 16 |
| Directional sequence number, big-endian | 8 |
| Inner payload length, big-endian | 2 |
| Inner payload | variable |
| Random padding | variable |

Fixed total overhead is 76 bytes. There are no clear magic bytes, fixed version,
message type, peer identifier or counter. The visible nonce is random. Encryption
authenticates the type, ID, timestamp, sequence, length, payload and padding.
IP/UDP headers remain visible. Maximum UDP payload is 1440 bytes.

## Keys and association setup

A 32-byte random pair secret is separate from both WireGuard private keys and the
WireGuard preshared key. HKDF-SHA256 derives two direction keys with labels
`customwireguard/v1/bootstrap/client-to-server` and `.../server-to-client`.

1. Client sends encrypted HELLO (type 1), new random 16-byte ID, sequence zero,
   empty payload, total length uniformly selected from 256–512 bytes.
2. Server authenticates it and returns encrypted WELCOME (type 2), same ID,
   sequence zero, fresh random 32-byte token, exactly the request's length.
3. Both derive direction keys with the corresponding
   `customwireguard/v1/session/` label and salt `clientID || serverToken` (48 bytes).
4. Client proves possession of session keys by sending an authenticated PING or
   DATA. Only then does the server replace the active association and forward data.

The server holds at most one active and one pending association per instance.
Pending challenges expire after 15 seconds. Clients generate a fresh ID after
15 seconds of failed setup. Retries use randomized intervals of 1.7–3 seconds
(plus timer scheduling). There is no periodic outer rekey interruption: fresh
outer keys are derived on reconnection. WireGuard's normal inner rekeying remains.

Session keys do **not** provide independent forward secrecy against compromise of
the outer secret: recorded WELCOME tokens can then be recovered. WireGuard still
provides its own established security properties. The wrapper never substitutes
for WireGuard authentication, encryption or key rotation.

## Data, replay and migration

DATA (3) carries the untouched WireGuard UDP datagram. PING (4) and PONG (5) carry
an eight-byte opaque value. Session sequences start at 1. Separate per-direction
32768-packet replay windows reject duplicates and old packets while tolerating
reordering. Timestamps accept at most 120 seconds in the past and 30 seconds in
the future. Changing a timestamp requires a valid authentication tag.

Server reboot loses associations; old DATA cannot authenticate under a fresh
server challenge. An old WELCOME is irrelevant to a newly randomized client ID.
A captured old bootstrap may elicit a fresh response shortly after restart while
its timestamp is valid; this is **not** claimed to be replay-proof active-probe
resistance across restart. During a process lifetime, a bounded 155-second cache
binds accepted bootstrap IDs to their initial source and message type. It suppresses
replays from other endpoints. Legitimate source changes during setup require a
new ID. Authenticated established data can migrate the endpoint: only a newer
sequence changes the return address, never an older reordered packet.

The wrapper accepts an outer-key holder, not a WireGuard identity. Someone with
that outer key can disrupt the one-client association or submit packets to the
fixed loopback WireGuard backend; they still cannot impersonate its WireGuard
peer without the inner key. Do not share a bundle among people/devices.

## Padding

- `minimal`: uniformly choose 0–15 added bytes, capped at the packet budget.
- `balanced`: payloads up to 192 bytes get an outer length uniformly selected
  between max(256, payload+76) and 512 bytes. Larger payloads add 0–64 bytes,
  capped at 1440 total bytes. Near the cap the available range shrinks.

Padding changes lengths, not application timings. No constant-rate cover traffic,
traffic morphing, congestion control, reliability, retransmission or stream
head-of-line blocking is introduced by CWG. UDP loss remains UDP loss. Bounded
queues drop under overload instead of allocating unbounded memory.

## Diagnostics and resource controls

PROBE (6) and PROBE_REPLY (7) use bootstrap keys and sequence zero, with a unique
ID and empty payload. Replies match request size and do not create or replace an
association. Their acceptable length is 256–1440. The CLI cycles through four
sizes. These are temporary diagnostics, not traffic camouflage.

Bootstrap decryption attempts use a global 100/s token bucket (burst 200) after
active/pending data decoding. Bootstrap responses are capped at 2/s (burst 4);
session ping replies at 20/s (burst 30). Authenticated same-ID HELLO retries get at
most two welcomes per second. The bootstrap cache has a hard 512-entry bound.
These controls limit allocation/reflection, not volumetric denial of service.
Established packets still require authentication work; traffic flooding can
consume CPU/bandwidth. Read queues hold at most 256 datagrams per socket and the
client setup queue holds at most 64 WireGuard datagrams.

Unauthenticated/malformed datagrams receive no CWG response. Oversize datagrams
are dropped. An authenticated bootstrap response never exceeds its request's
length. All WireGuard backend and client-side listeners/targets must be loopback;
the Windows client accepts input only from the configured local WireGuard port.
Kernel WireGuard's UDP port must additionally be blocked externally because its
native listener is not intrinsically loopback-only.

## Limits of the threat model

This removes direct WireGuard packet bytes and fixed small-packet sizes. It does
not implement a permitted application protocol and does not prevent:

- classifiers using entropy, length distributions, direction, timing or volume;
- blocking unknown UDP, data-center addresses, entire networks or the server IP;
- correlation across connections or endpoints;
- bootstrap replay observations after server restart, key compromise or endpoint
  compromise;
- application/DNS leaks caused by incorrect OS routing or stopping the interface.

The outer bootstrap and padding choices may themselves become recognizable.
Do not infer safety from a packet analyzer labeling the traffic merely as UDP.
Keep protocol changes reviewable, pair them with cross-version tests, and evaluate
against the actual network rather than adding speculative fake protocol prefixes.
