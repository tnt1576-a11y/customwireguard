# Independent users: 0.6 development status

The development server has one UDP listener and up to three independent user
contexts. Each account has its own outer credential, WireGuard keys and tunnel
address. The deployed 0.5.8 server and published 0.5.8 client packages remain the
single-user baseline. **Multi-user WAN validation and coordinated client rate
limits are unfinished; do not treat this development work as the final release.**

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
They contain the app-selection GUI and the native Windows helpers. The GUI's
pinned routing engine is installed separately using its Install app engine
button. See [application client instructions](APPLICATION-CLIENT.md).

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
However, existing clients independently increase their request rates. Three
clients can temporarily oversubscribe that shared ceiling. The first real
WireGuard sandbox run recorded 988/1000 and 989/1000 probes for the continuing
clients, with thousands of outer requests expiring at the shared queue.

A diagnostic comparison limited each client to 2,500 requests/s. Both continuing
clients returned 1000/1000 probes over 25 seconds and verified separate 4,423,680
byte downloads; their p95 RTTs were 7.768 and 6.754 ms within the local sandbox.
The third user was intentionally revoked after six seconds: its first 200 probes
all returned, later probes stopped, and its interrupted download correctly
failed. The other native handshake timestamps and outer session counts stayed
unchanged. These are local validation results, not Internet latency or capacity
claims. A coordinated-rate design and loaded second-PC WAN comparison remain
required before deployment.

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
