# Contributing

Start with the [README](README.md), [protocol](docs/PROTOCOL.md),
[request/reply design](docs/EXCHANGE.md) and [security policy](SECURITY.md).
Discuss substantial transport or routing changes before preparing a large patch.

## Local checks

Use Go 1.26 or later, Python 3 and Windows PowerShell for the desktop helpers.
The full build also requires the Windows .NET Framework compiler.

```sh
go mod verify
go vet ./...
go test ./...
python -m unittest discover -s scripts -p "test_*.py"
```

Go integration tests use loopback sockets and in-memory WireGuard TUNs; they do
not connect to a public VPN server. Installer unit tests use disposable fixtures.
Actual installation and tunnel-performance checks are separate, privileged work.
Use a dedicated test host and a verified cleanup watchdog for those checks.

```powershell
.\scripts\Test-AppTunnelConfig.ps1
.\scripts\Test-AppTunnelRecovery.ps1
.\scripts\Build.ps1 -Version 0.6.4
```

`Build.ps1` writes ignored `dist/` artifacts. For the compact Windows package,
use `Package-Desktop.ps1` with the pinned, unmodified upstream routing-engine
archive. Never weaken the checksum checks to accommodate a different download.

## Before a pull request

- Keep packet compatibility, authentication, replay checks, queue bounds and
  buffer ownership explicit. Document changes to privacy or network behavior.
- Add focused tests for behavior that can regress. For performance changes,
  compare CPU, latency, loss, traffic volume and useful throughput under the same
  workload; distinguish measurements from expectations.
- Use documentation addresses such as `192.0.2.10`, generic machine names and
  disposable test keys. Do not include real profiles, logs, captures or tokens.
- Review the diff and untracked files. Keep `private/`, `.cache/`, `.tools/`,
  `dist/`, test outputs and imported configuration out of source commits.
- Preserve third-party notices. Contributions to CWG's own code are under the
  repository's [MIT license](LICENSE).

The `codex/bandwidth-demand` branch contains an opt-in research prototype. Its
reported traffic savings came with an upload slowdown; it is not the release
default. Do not promote experimental settings without validation.
