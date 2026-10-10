# Application tunnel development status

Application selection is not yet delivered. The normal 0.5.8 client and its
stability profile remain the supported local test baseline.

## WireSock compatibility results, 2026-10-10 UTC

WireSock Secure Connect 3.6.1.1 was installed on the separate Windows 10 test PC
from the official checksum-verified, Authenticode-valid installer. The controller
PC was not changed. Tests used an independent SYSTEM cleanup task and explicit
LAN exclusions, with network lock off.

- Direct native profile: WireSock repeatedly reported that it could not find a
  route to the loopback VPN endpoint. A native WireGuard handshake did not start.
- Local SOCKS5 UDP adapter: the SOCKS control connection and UDP ASSOCIATE
  succeeded, but no UDP datagrams reached the loopback relay. Selected HTTP
  requests failed; the attempted tunnel also disrupted name resolution for the
  bypass client. There was no successful app-routing comparison.
- The first harness attempt misread a process exit code and stopped after a
  successful import. That harness error is separate from the actual limitations
  observed in the two subsequent comparisons.

The maintainer previously documented that a localhost SOCKS endpoint is
unsupported: [WireSock issue 64](https://github.com/wiresock/WireSockUI/issues/64).
That older explanation is consistent with the new runtime observations; it is
not evidence that every future WireSock release will behave the same way.

All trial profiles, processes and tasks were removed. The temporary WireSock
installation was uninstalled successfully without reboot. The required Microsoft
.NET Desktop Runtime 10.0.12 remains installed on the test PC. Final inspection
confirmed the original Ethernet default route and DNS, IPv6 binding enabled,
and no trial UDP listeners or WireSock services.

## Alternative verified so far

A private prototype using the existing WireGuard Go implementation and its
userspace network stack connected through the unchanged CWG carrier. On the
separate PC, a request to the private tunnel HTTP endpoint returned a server
response, and an HTTPS request to an IP-check service returned the VPN server's
public IPv4 address. The network stack performed name resolution inside the
tunnel. It created no Windows interface or system route. This proves the basic
alternative path, not completed app selection or stable loaded performance.

The next implementation is a local TCP/UDP SOCKS endpoint backed by that network
stack, with an application selector forwarding chosen processes into it. The
SOCKS control connection is local; the Internet-facing carrier remains UDP.
Evaluate [ProxiFyre](https://github.com/wiresock/proxifyre) for selection and GUI
controls before writing a Windows filtering driver.

## Required checks before delivery

- Authenticated loopback listener, bounded connections, UDP associations, buffers
  and idle timeouts; no direct Internet fallback in the proxy.
- Included and excluded applications, process paths and children, TCP and UDP,
  LAN/RDP access, carrier exclusion, service lifecycle and reconnect recovery.
- DNS behavior for Windows shared resolution and app-owned DNS. A successful
  hostname request through an explicit SOCKS client alone is insufficient.
- IPv6 routing and failure behavior. ProxiFyre currently documents that fragmented
  IPv6 datagrams pass directly even when ordinary unsupported-family traffic is
  blocked. Resolve or clearly constrain that behavior before claiming complete
  app isolation; do not label IPv6 verified based only on an IPv4 test network.
- Repeat the throughput and loaded-latency tests against the original client.
  Keep failed comparisons and distinguish harness completion from functional
  success. Cleanup verification must be newer than the finished run, not the
  initial watchdog proof.

Sources: [WireSock CLI](https://www.wiresock.net/documentation/wiresock-secure-connect/wiresock-connect-cli.html),
[WireSock proxy and app options](https://www.wiresock.net/documentation/wiresock-secure-connect/advanced-parameters.html),
[ProxiFyre configuration](https://github.com/wiresock/proxifyre/blob/main/docs/configuration.md).
