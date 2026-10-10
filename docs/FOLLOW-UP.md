# Follow-up requirements

## Active implementation objective

The user has authorized continued automatic testing on the separate LAN Windows
PC, with stability and loaded latency taking priority over speed. The controller
PC must not connect/disconnect CWG or have its networking changed for these tests.

Completion requires all of the following, not just the transport experiment:

- Improve tolerance of remaining packet loss and timeouts, validate with actual
  WireGuard and controlled isolated/burst loss, then compare on the separate PC.
  Preserve failed results and report residual loss; do not infer DPI intent.
- Deliver a repeatable installer for a fresh Linux server, including prerequisites,
  forwarding/NAT, service hardening, credentials, verification and recovery.
- Implement Windows application include/exclude split tunneling with usable
  controls, using WireSock if it passes compatibility testing or a dedicated GUI
  client if necessary. Verify DNS, IPv6, child-process behavior and LAN access.
- Add optional lightweight Telegram monitoring/management with authorized owner
  checks, bounded polling/resources, protected secrets and explicit management
  commands. Verify behavior and measure idle/active resource cost.
- Support two or three simultaneous users with independent credentials, fair
  resource use and revocation. Provide tested provisioning/export instructions
  for giving a separate bundle to another user; sharing one bundle is insufficient.

Current implementation baseline: 0.5.8 with spare-slot repair. Bounded queue,
handshake deadline and loss-accounting corrections passed controlled regressions
and a final separate-PC run with 8/8 transfers and 1715/1715 active rapid pings.
Residual loss remains (0.14725% outer timeouts and one client data expiry in that
run); this is not a long-term reliability or general latency guarantee. The
fresh Debian installer now passes isolated real-WireGuard/NAT and lifecycle
tests; see SERVER-SETUP.md for its one-client scope and limitations.
Application splitting now has a working GUI/controller and separate-PC tests;
see APPLICATION-CLIENT.md for its defined DNS, IPv6 and shutdown behavior.
Telegram and independent-user features below remain incomplete until their
source, runtime tests and operator-facing deliverables demonstrate completion.

## Two or three independent users

Target workload: at most two or three people gaming, browsing, streaming video
or movies, and using social media. Stability and loaded latency take precedence
over higher single-client speed. The current server has one active client
association per instance; another authenticated client replaces that association.
Sharing the current client bundle is not multi-user support.

Implement separate outer credentials, WireGuard keys/peers and tunnel addresses,
bounded per-client session and replay state, and fair per-client queues under an
aggregate rate limit on the existing UDP listener. Retain authentication before
replying or allocating expensive state. Test two clients first, then three, with
one streaming or transferring while the others send game/voice-sized traffic.
Measure aggregate CPU including kernel work, packet loss, loaded ping, memory,
outer bandwidth and reconnects. The observed server has one vCPU and about 1 GB
RAM; no multi-client capacity claim is established by the single-client result.

This is a recorded requirement, not an implemented feature of 0.5.5.

## Per-application split tunneling on Windows

After throughput and burst handling are validated, support selecting applications
that use CWG and applications that use the normal connection. The user requested
this because WireSock offers application selection; WireSock integration or an
alternative Windows implementation can satisfy the requirement.

WireSock Secure Connect 3.6.1.1 was tested on the separate Windows PC. Direct
loopback connection failed with a route-selection error. Its local SOCKS5 UDP
comparison completed the control handshake but delivered no UDP to the local
relay. This is not a usable integration. See APP-TUNNEL.md for the evidence and
the userspace WireGuard alternative that passed an initial tunneled HTTPS test.
Application selection now uses sing-box's userspace WireGuard endpoint and
Windows application rules behind dedicated GUI controls. Include/exclude modes,
folder children, DNS, LAN access, TCP/UDP and failure recovery passed on the
separate PC. The final 32 MiB transfer window returned 218/218 UDP probes with
p95 97.494 ms. Live public IPv6 bypass remains unverified because this lab has no
working IPv6 WAN. The original native client is preserved, and the temporary
WireSock installation was removed. No controller networking was changed.
