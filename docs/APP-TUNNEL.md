# Application tunnel development status

Application selection is now implemented using a separate local routing engine.
See [Windows application client](APPLICATION-CLIENT.md) for controls, measured
results and limits. The normal 0.5.8 carrier and native client are unchanged.
The failed WireSock comparisons below explain why another local engine was used.

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

## Alternative development path

A private prototype using the existing WireGuard Go implementation and its
userspace network stack connected through the unchanged CWG carrier. On the
separate PC, a request to the private tunnel HTTP endpoint returned a server
response, and an HTTPS request to an IP-check service returned the VPN server's
public IPv4 address. The network stack performed name resolution inside the
tunnel. It created no Windows interface or system route. This proves the basic
alternative path, not completed app selection or stable loaded performance.

The initial plan considered a custom local SOCKS endpoint and ProxiFyre. The
delivered implementation instead uses sing-box 1.14.3 for userspace WireGuard,
TUN routing, DNS and application rules in one engine. Its WireGuard endpoint
successfully connects to the loopback CWG carrier. No ProxiFyre installation or
custom SOCKS service is required; the Internet-facing carrier remains UDP.

## Validation scope

The final implementation has no exposed SOCKS listener. Include/exclude modes,
folder children, TCP/UDP, an uncached DNS query observed on the server, LAN access,
carrier restart and supervisor failure were tested. Timestamped UDP measurements
separate the actual transfer window from idle time. Final SYSTEM cleanup evidence
postdates each completed run. Failed comparisons remain in the private evidence.

The lab has no working public IPv6 path. Route ordering and a rejected connection
were checked, but live dual-stack bypass/fragment handling remains a documented
limit. Application routing is not a security boundary against deliberate route
bypass, and there is no persistent kill switch after stop. See the operator guide
for the common Windows DNS policy and lifecycle behavior.

Sources: [WireSock CLI](https://www.wiresock.net/documentation/wiresock-secure-connect/wiresock-connect-cli.html),
[WireSock proxy and app options](https://www.wiresock.net/documentation/wiresock-secure-connect/advanced-parameters.html),
[ProxiFyre configuration](https://github.com/wiresock/proxifyre/blob/main/docs/configuration.md).
