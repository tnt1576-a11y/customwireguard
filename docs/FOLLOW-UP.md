# Implementation status

The 0.6 delivery addresses the requested stability work, fresh Debian installer,
Windows application selection, optional Telegram management, and up to three
independent users. Automated WAN testing runs on the separate LAN Windows PC;
the controller's VPN, routes and adapters are never changed for testing.

## Delivered

- Transport: spare-slot repair, bounded queues, authenticated shared-rate grants,
  safe capacity borrowing, and a finite arrival-burst allowance. See
  [measured results and retained failures](VALIDATION.md). Remaining outer loss
  and occasional latency spikes are reported rather than treated as fixed.
- [Fresh Debian installation](SERVER-SETUP.md): checked release files, private
  credentials, WireGuard, forwarding/NAT, service hardening, verification and
  install-failure recovery. Existing manual installations are refused; the live
  legacy server required a separate backed-up migration.
- [Windows application controls](APPLICATION-CLIENT.md): include/exclude/all
  modes, executable and folder rules, TCP/UDP, DNS handling, LAN access,
  transport restart and independent cleanup. The pinned sing-box engine supplies
  local application routing. WireSock was tested but did not provide a working
  integration; see [compatibility evidence](APP-TUNNEL.md).
- [Independent accounts](MULTI-USER.md): separate outer credentials and native
  keys, three active accounts, hot provisioning/revocation, private exports,
  bounded fair scheduling, and durable interrupted-operation recovery.
- [Optional Telegram management](TELEGRAM.md): private owner authorization,
  status/users/add/config/revoke/restart commands, profile delivery, bounded polling,
  unprivileged bot plus restricted helper, and clean removal. It stays off until
  the operator supplies a private token and enables it. Systemd lifecycle tests
  used a local TLS fixture; the owner later confirmed live bot replies.

## Practical limits

Three-user WAN tests use three independent stacks on one Windows PC and one
access network. They establish the documented workloads, not three concurrent
maximum-speed users or long-term reliability. Occasional outer timeouts remain.
Polling/padding consumes bandwidth in both directions, even for downloads.

The server currently provides IPv4 Internet egress. Selected public IPv6 traffic
is rejected; the lab has no working public IPv6 WAN, so a live dual-stack bypass
test is still unavailable. Stopping the client restores ordinary networking;
there is no persistent kill switch. Rules cover configured executable paths and
folder descendants, not arbitrary child processes installed elsewhere.

The fresh installer targets Debian 13 or newer with systemd. Linux amd64 has
runtime validation; arm64 is cross-compiled only. Future DPI invisibility and
server-IP survival cannot be guaranteed; see [resilience limits](RESILIENCE.md).
