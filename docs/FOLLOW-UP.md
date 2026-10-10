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

Current implementation baseline: 0.5.6; its optional spare-slot repair is tested,
but residual loss remains. The items below remain incomplete until their relevant
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

Compatibility is not yet established. Evaluate a WireSock native WireGuard peer
pointed at CWG's local UDP carrier, with CWG itself using the physical interface.
Verify application include/exclude behavior, process children and executable
paths, DNS and IPv6 behavior, local LAN/RDP access, and reconnect/cleanup. Test
both selected-application and excluded-application modes without accidentally
routing the carrier back into the tunnel. Preserve the existing native client
until the alternative has passed user-run tests. No WireSock driver, adapter,
application policy or route changes are part of the current performance update.
