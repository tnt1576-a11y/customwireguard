# Third-party components

The [MIT license](LICENSE) covers CWG's own source and documentation. It does
not relicense bundled third-party programs or dependencies.

- **Go and Go modules:** release bundles include their notices under
  `THIRD-PARTY-LICENSES/`; the compact Windows app consolidates notices in
  `THIRD-PARTY-NOTICES.txt`. Exact module versions are in `go.mod` and `go.sum`.
- **sing-box:** the compact Windows app contains the unmodified, checksum-pinned
  upstream routing engine. sing-box is licensed under **GPL-3.0-or-later**.
  Its license and source links accompany the executable. CWG communicates with
  it as a separate process; CWG's MIT license does not cover that executable.
- **WireGuard:** the Debian installer obtains native WireGuard through the
  distribution package manager. The optional legacy Windows mode uses the
  official WireGuard application, installed separately.
- **Microsoft OpenSSH:** optional remote-test helpers can install a pinned
  Microsoft package. It is not included in the public CWG app ZIP.

For the pinned desktop engine, see the
[upstream release](https://github.com/SagerNet/sing-box/releases/tag/v1.14.3),
[corresponding source](https://github.com/SagerNet/sing-box/archive/refs/tags/v1.14.3.tar.gz)
and [license](https://github.com/SagerNet/sing-box/blob/v1.14.3/LICENSE).
Retain these notices when redistributing packages. Upstream binaries may contain
their developers' build paths and copyright attributions; these are not data
from a CWG user's PC and are not removed or altered.
