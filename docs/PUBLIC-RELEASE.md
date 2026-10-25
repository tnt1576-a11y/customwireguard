# Public-release preparation

The 0.6.4 release was prepared for public distribution on 2026-10-25. This is a
privacy and packaging review, not an independent security audit of the protocol.

## Reviewed scope

- Tracked source, documentation, scripts and examples, including the separate
  bandwidth-research branch.
- All 39 existing commits reachable from the repository's branches and release
  tags, including deleted/previous file contents and author metadata.
- Both existing releases, their notes and all eight downloadable assets. Local
  copies were compared with GitHub's SHA-256 asset digests before inspection.
- The rebuilt Windows app, server installer and source archives for 0.6.4,
  including ZIP member names, embedded scripts/resources, binaries and manifests.

Checks used known local credential values and identifiers without publishing
those values, plus patterns for tokens, private keys, user-directory paths and
unintended private artifact names. Git history uses the repository owner's
public GitHub noreply identity. No live connection keys, bot tokens, personal
server endpoints, owner IDs, local account names or machine identifiers were
found in the reviewed public material. Machine-specific LAN cleanup notes were
generalized and the corresponding fixture now uses invented LAN addresses.

The pinned, unmodified third-party engine contains upstream build paths and
author/copyright notices. These are upstream provenance, not data from a CWG
user's machine; license attributions are preserved. See [THIRD_PARTY.md](../THIRD_PARTY.md).

Private working directories, SSH material, imported/generated profiles, raw logs,
captures, backups, test results and the raw audit report remain local and ignored.
The public app ZIP is unconfigured. Server-generated client ZIPs and `.cwg`
profiles remain private per-user exports.

## Release behavior

The public release retains the 0.6.3 carrier protocol and desktop recovery.
There is no transport redesign in 0.6.4. The changes concern documentation,
version metadata, license distribution and publication hygiene. The installer
now verifies and preserves CWG's MIT license in owner/client exports; named-user
exports retain it too. Third-party licenses remain separate.

Go module verification, vet and regression tests, portable installer/management
tests, Windows helper checks, archive manifests and installer consumed-file
checks are part of preparation. Existing network-performance measurements are
historical: no new public-server speed or DPI-resistance claim is made for this
documentation and packaging release.

## Before future publication

Review staged files and every release asset, including nested archives and
binary metadata. Scan all reachable history if new branches/tags are added.
Keep profiles, tokens, account IDs, endpoints and raw diagnostics out of public
issues. Recheck GitHub release notes as well as source. Ignore rules alone do not
protect a value that has already been committed. See [SECURITY.md](../SECURITY.md).
