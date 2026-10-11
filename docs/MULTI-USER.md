# Independent users in 0.6

The 0.6 server has one UDP listener and up to three independent user
contexts. Each account has its own outer credential, WireGuard keys and tunnel
address. Negotiated rates pass local, delayed-path and three-user WAN load tests.
Legacy 0.5.8 single-user bundles cannot negotiate shared mode; export matching
0.6 bundles for the shared server. The fresh installer refuses existing manual
installations rather than overwriting them. Results below describe bounded tests,
not a long-term zero-loss or undetectability guarantee.

## Provisioning commands

A fresh install from a 0.6 package creates the `owner` account and its private
`/etc/cwg/client.zip`. Management commands require root on that managed server:

```sh
python3 /usr/local/lib/cwg/users.py list
python3 /usr/local/lib/cwg/users.py add alice
python3 /usr/local/lib/cwg/users.py add bob
```

The resulting files are `/etc/cwg/user-alice.zip` and
`/etc/cwg/user-bob.zip`. Copy each ZIP securely to its intended person. These
files contain client private keys; never publish them or put them in GitHub.
They contain `CWG.exe` and the native Windows helpers. The desktop app installs
its pinned routing engine automatically on first Connect. See [application client
instructions](APPLICATION-CLIENT.md). With the optional bot enabled, `/add alice`
creates the same independent account and sends a small `alice.cwg` document to
the owner. Share that file and the public Windows app ZIP with Alice; they import
the file in the app. `/config alice` resends it; `/revoke alice` disables it.

Use one account for each simultaneous device. The fourth active account is
refused. Names are lowercase identifiers, up to 32 characters, beginning with a
letter; digits, underscore and hyphen are accepted afterward. Revoked names are
retained and cannot be reused; choose a new name to issue fresh credentials.
At most 64 historical accounts are retained by this initial management tool.

```sh
python3 /usr/local/lib/cwg/users.py revoke alice
python3 /usr/local/lib/cwg/server-admin.py verify
```

Revocation removes both the outer context and native peer. Its old ZIP remains
private on the server for inspection but can no longer establish a tunnel.
Unchanged accounts keep their outer sessions and native peer state. Removing
the last active account is valid. Future provisioning can reuse a free tunnel
address with new keys.

## Reload and recovery

The server polls the private configuration once per second. Only modified users
are replaced. Invalid configuration changes are rejected while the current
contexts remain running. Status under `/run/cwg/status.json` records the applied
revision and per-user counters, with no credentials.

User operations hold the same management lock as installation. A root-only
journal is flushed before replacing files. Success requires both native peer
application and acknowledgment of the new outer-server revision. If an operation
is interrupted, inspect the service and finish the recorded operation with:

```sh
python3 /usr/local/lib/cwg/users.py recover
```

Recovery uses the original planned keys, preserving an already-written client
ZIP. It finishes the operation rather than undoing a revocation. Do not remove
the journal or edit the installation state manually. Further account changes
and uninstall are refused while a journal is pending. A boot during a pending
operation may apply new configuration files before management state is updated;
use recovery to reconcile them.

The outer process uses systemd DynamicUser with a dedicated read-only credential
group. Only its outer server configuration is group-readable. The native server
private key, exports, journal and installation state stay root-only. Management
verifies the active native peer/address mapping and private file permissions.

## Transport behavior and remaining work

There is no clear user identifier added to packets. Unknown addresses and short
bootstrap packets go through bounded authentication against at most three
contexts. Accepted, non-replayed authentication updates a bounded source-address
cache. Source-port changes and reuse are covered by regression tests.

Established input uses round-robin per-user queues, a shared 8,000-request/s
ceiling and a 5 ms queue deadline. Spare dispatch capacity is borrowed. Tests
cover equal saturated shares, aggregate pacing, queue expiry and borrowing.
The first development clients independently increased their request rates and
could oversubscribe that shared ceiling. The first real
WireGuard sandbox run recorded 988/1000 and 989/1000 probes for the continuing
clients, with thousands of outer requests expiring at the shared queue.

A diagnostic comparison limited each client to 2,500 requests/s. Both continuing
clients returned 1000/1000 probes over 25 seconds and verified separate 4,423,680
byte downloads; their p95 RTTs were 7.768 and 6.754 ms within the local sandbox.
The third user was intentionally revoked after six seconds: its first 200 probes
all returned, later probes stopped, and its interrupted download correctly
failed. The other native handshake timestamps and outer session counts stayed
unchanged. These are local validation results, not Internet latency or capacity
claims.

The shared protocol negotiates `exchange_shared` inside the
authenticated handshake. Each request includes encrypted demand and acknowledgment
of the current grant. Replies advertise a versioned rate grant. Capacity withdrawn
from a busy user is held until that user acknowledges the reduction; it is then
available to another user. Silent clients expire grants after one second, anchored
to the request's send time, before the server reclaims capacity after two seconds.
This prevents a delayed reply from renewing a reclaimed grant. Three minimum
200-request/s reservations allow accounts to join without over-allocation.
Idle clients still actually poll at four requests/s. Grants leave 10% of the hard
ceiling available for arrival jitter (except the minimum 600-request/s setup).
A single busy client can borrow up to 6,800 with an 8,000-request/s hard ceiling;
three equally busy clients receive 2,400 each. Ordinary congestion control can reduce
actual rates below those grants. The server's bounded ingress limiter remains.

All three clients may retain an 8,000-request/s configured maximum; the server
coordinates their usable shares. An incompatible legacy handshake is refused.
This shared mode currently supports spare-slot repair but not experimental FEC.
No clear user identifier or larger outer datagram is introduced. See the
[encrypted exchange framing](EXCHANGE.md#shared-rate-negotiation-06-development).

With negotiated rates, the local three-user test returned 1000/1000 probes for
both continuing users, verified both independent downloads, and recorded zero
shared-queue expirations. Repeating with 45 ms delay in each direction returned
1000/1000 for both, with p95 RTTs of 95.328 and 95.202 ms and zero shared-queue
expirations. The third user was revoked during both tests without resetting the
others. The final candidate additionally preserves young requests when a worker
is busy and permits a bounded 16 ms arrival batch to pass immediately. The shared
queue still expires after 5 ms; sustained dispatch remains capped at 8,000/s.

The final three-user WAN test returned 6600/6600 application UDP probes and
verified three downloads (32/16/16 MiB) plus an 8 MiB burst upload. All three
downloads overlapped for 17.045 seconds; their target aggregate rate was 20 Mbps.
Overall per-user p95 RTTs were 109.934, 103.490 and 102.553 ms. The shared queue
recorded zero drops/expirations. The clients still recorded 18 outer timeouts out
of 330,737 requests (0.00544%); backup repair covered the observed application
probes, not a promise to recover every future loss. No original native-data or
control-packet expiry appeared in the last active server sample, and all users
kept one outer session. Spare repair copies did expire under load, as designed.
See [full comparison and resource measurements](VALIDATION.md#06-shared-rate-and-arrival-stability-2026-10-11).

Lifecycle tests also cover independent bundle credentials, fourth-user refusal,
revocation, malformed reload rejection, and interrupted provisioning recovery.
The final management source passed fresh/repeated installation, export boundary
checks, changed-file refusal, preservation of unrelated files/tables, injected
start-failure rollback, failed-state uninstall and recovery. Negative tests
detect changed private-file permissions and unexpected native peers. Python
bytecode generation is disabled so clean uninstall does not leave generated
files blocking the next installation.
The earlier full `wg syncconf` application refreshed unaffected native handshakes
in this environment; management now applies only the named peer using `wg
addconf` or `wg set ... remove`. Failed experiments are retained privately.
