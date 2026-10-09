# Network experiment plan

Use the same server, client, ISP and approximate time window when comparing modes.
Record the mode, server port, version and padding profile. Never collect private
keys or unrelated users' traffic in an experiment report.

1. **Outer reachability:** run eight authenticated probes. Preserve reply rate,
   per-size success and RTTs. Compare `balanced` and `minimal` transport runs on
   the same endpoint; the diagnostic's packet-size pattern is independent of the
   selected data-padding profile.
2. **Inner connectivity:** activate the split configuration and ping both tunnel
   addresses. Check `wg show cwg0 latest-handshakes` on the server and the client's
   WireGuard status. A successful probe with no inner handshake calls for checking
   keys, local ports, service logs and the private WireGuard target.
3. **Data sizes:** exercise packets through the real tunnel up to its 1280 MTU.
   Success on small probes alone is insufficient. If the 1440-byte outer sample
   fails, inspect path MTU and filtering rather than assuming a cipher problem.
4. **Performance:** transfer between client and controlled server (iperf3 or an
   equivalent controlled test). Record throughput, CPU, packet loss, median and
   p95 latency both idle and during a transfer. Where native WireGuard is blocked,
   collect its speed baseline on an unrestricted connection to the same server;
   do not compare unrelated routes as if they isolate wrapper overhead.
5. **Stability:** test several hours, idle-to-active, Wi-Fi changes, mobile NAT,
   loss/reordering and server restart. The current timeout can take about 70
   seconds plus handshake retries to recover after the server loses state.
6. **Blocking observations:** distinguish immediate setup failure, failure after
   sustained data, and endpoint-wide reachability loss. Check independent HTTPS
   reachability to the same server only if you deliberately provisioned such a
   service. One failed UDP port does not establish that the IP was blocked.

For diagnosis, capture only the dedicated outer port on the server for a bounded
interval and compare received packet counts with local sends. Outer captures are
still sensitive metadata. Keep them private and outside release/source packages.
Do not send packet contents or keys to a public capture-analysis service.

Success means "this version worked on this ISP at this time with these measured
properties." It does not establish that traffic was undetected or predict how
long a server address will remain reachable.

## Local verification boundaries

The Go integration test uses real WireGuard code, real sockets and in-memory TUN
devices. It verifies cryptographic interoperability and IPv4/IPv6 byte delivery.
It does not install Windows tunnel services, validate Linux nftables/systemd on a
live server, test internet NAT/DNS, or exercise an ISP's DPI. Those checks belong
to the first server deployment. The source includes install helpers for that step;
do not describe cross-compilation as a successful Linux runtime test.
