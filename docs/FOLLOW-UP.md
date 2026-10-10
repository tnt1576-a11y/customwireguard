# Follow-up requirements

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
