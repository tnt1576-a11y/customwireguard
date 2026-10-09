# HTTP/3 comparison (v0.2.0)

This alternative carries the complete WireGuard datagram in standard HTTP/3
CONNECT-UDP (RFC 9298), HTTP Datagrams (RFC 9297), and QUIC DATAGRAM frames
(RFC 9221). quic-go v0.63.0 supplies QUIC/TLS/HTTP/3; masque-go v0.6.0 supplies
CONNECT request construction, validation, and the client datagram connection.
The server relay permits only its configured literal loopback WireGuard target.
It has no general-purpose proxy access, DNS resolution or TCP fallback.

The original custom encryption/padding is not nested inside HTTP/3. WireGuard
encryption remains intact inside QUIC's TLS 1.3 encryption. A domain-separated
HMAC of the existing 32-byte outer secret authenticates encrypted HTTP requests.
Keep that secret private. QUIC 0-RTT is disabled; packets are congestion-controlled
and paced by QUIC, but lost DATAGRAM frames are not retransmitted. Application
TCP connections inside WireGuard still perform their own retransmissions.

## Certificate and configuration

For an IP-only deployment, generate a separate identity in a new private
directory on the server:

```sh
./cwg-linux-amd64 tls-init -endpoint SERVER_IPV4:443 -out NEW_PRIVATE_TLS_DIR
```

The self-signed ECDSA P-256 certificate has the exact IP in its SAN and expires
after one year. Transfer only `server-cert.pem` to the client over trusted SSH.
The client uses an isolated trust store, normal IP/expiry verification and an
additional exact-certificate comparison. It never uses InsecureSkipVerify.
Changing the server identity requires distributing a new pin. The key stays
on the server. This is not a publicly trusted web certificate; browsers will
not trust it, and the transport is not indistinguishable from browser traffic.

Starting with an existing private raw configuration, add these JSON fields to
the client (retain its existing mode/listen/endpoint/wireguard/key/profile):

```json
{
  "transport": "h3",
  "tls_cert": "server-cert.pem",
  "h3_target": "127.0.0.1:51820"
}
```

Add `transport: h3`, `tls_cert: server-cert.pem` and `tls_key: server-key.pem`
to the server configuration. Do not add `h3_target` on the server: its existing
`wireguard` field is the single allowed target. Relative certificate paths are
resolved against the JSON file's directory, regardless of process working
directory. The profile field is retained for configuration compatibility but
does not add raw-carrier padding to HTTP/3.

For systemd credential isolation, load the JSON, certificate and key as three
credentials. A reviewed override for an existing CWG installation can use:

```ini
[Service]
LoadCredential=
LoadCredential=server.json:/etc/cwg/server-h3.json
LoadCredential=server-cert.pem:/etc/cwg/server-cert.pem
LoadCredential=server-key.pem:/etc/cwg/server-key.pem
ExecStart=
ExecStart=/usr/local/lib/cwg/cwg-0.2.0 server -config %d/server.json
```

Preserve the current files, trace, service overrides and WireGuard MTU before
switching. The original first-install script is for raw transport; do not pass
an H3 configuration to it. Keep native WireGuard's external UDP port blocked.

## MTU and operation

This first comparison intentionally uses **IPv4 only, MTU 1088 on both native
WireGuard interfaces**, and a 1120-byte maximum WireGuard UDP payload. QUIC
starts at 1200 bytes and path MTU discovery is disabled for this comparison.
This avoids requiring a larger path for full-sized tunnel packets. Oversized
datagrams are counted and dropped; they never silently switch to a reliable
stream. The lower MTU trades some efficiency for a conservative path size.
Do not use this profile for IPv6: its minimum link MTU is 1280. The Windows
Full helper retains its guard against physical IPv6 being enabled outside an
IPv4-only tunnel. It does not change adapter bindings.

The existing Windows Start/Stop helpers work with this transport. Native
WireGuard still points to localhost, so the helper and transport executable
must be running; importing the configuration alone is insufficient.

```powershell
.\scripts\Start-Client.ps1 -Bundle .\config-h3 -Mode Test
ping 10.77.0.1
.\scripts\Stop-Client.ps1 -Bundle .\config-h3
.\scripts\Start-Client.ps1 -Bundle .\config-h3 -Mode Full
curl.exe --ipv4 --max-time 20 https://api.ipify.org
.\scripts\Stop-Client.ps1 -Bundle .\config-h3
```

Preflight now checks pinned TLS and authenticated HTTP/3 responses. Because
HTTP requests use reliable streams, success is **not** a UDP loss measurement
and does not prove the CONNECT-UDP flow or native WireGuard works. Only the
user's actual tunnel test can verify that. Raw size/rate/replay probes are
rejected with an H3 configuration. `-TracePackets` records local encrypted
WireGuard datagrams only, not QUIC packet boundaries; leave it off for the first
comparison. Matching these local hashes between peers can diagnose delivered
WireGuard packets but cannot localize a lost outer QUIC packet.

On interruption the client closes the old connection and retries with a delay
that grows to 10 seconds. QUIC uses a 45-second idle timeout and 15-second
keepalive. The server accepts one active CONNECT-UDP flow; a stale flow may
take roughly a minute to release (QUIC may restart its idle timer on the first
ack-eliciting send after a receive). Changing physical adapters still needs
a client restart so the Windows interface pin and bypass route can be rebuilt.
Neither this helper nor this release supplies a firewall kill switch.

## What this comparison can establish

If preflight itself times out, the tunnel has not started. Do not interpret a
later Diagnose-Client refusal as an additional VPN failure. With coordinated
server-side capture prepared, the user can run this bounded comparison instead:

```powershell
.\scripts\Measure-QUICPath.ps1 -Bundle .\config-h3 -RawBundle .\config-8443
```

It performs H3/raw/H3 probes without a tunnel or routing changes. It requires
Administrator PowerShell and a physical route to the same server in both
bundles. Packet Monitor captures only UDP to/from that server; files stay in
the private H3 bundle. Existing capture/filter state is not overwritten. This
comparison changes both protocol and port, so a pass/fail difference alone
cannot distinguish protocol classification from a port-specific policy.
Capturing packets can itself affect timing. Correlate both endpoint captures
before concluding where loss occurs; neither one proves an ISP's intent.

The preceding raw-carrier video test lost 26/30 idle pings, 29/30 during video,
and 23/30 afterward, even with tracing active. Matched packet hashes showed
about 59% loss each way, beginning before the video. That invalidates treating
tracing as a reliable workaround and does not establish YouTube as the trigger.
It remains compatible with filtering, driver/path behavior or another cause;
it does not prove DPI.

HTTP/3 changes the observable protocol and introduces standard congestion
control. It is a meaningful next experiment, not an established fix. The IP,
QUIC handshake, packet sizes, timing and volume remain observable. A network
can block QUIC, classify this implementation or block the IP. No future
undetectability or native-WireGuard throughput guarantee is made.

Primary references: [masque-go](https://github.com/quic-go/masque-go),
[QUIC datagrams](https://quic-go.net/docs/quic/datagrams/),
[RFC 9298](https://www.rfc-editor.org/rfc/rfc9298.html).
