# Security and private data

CWG is experimental. Use the latest published release and read its limitations.
It has not received an independent security audit. Neither custom UDP framing
nor port 443 guarantees resistance to traffic classification or server blocking.

## Report a vulnerability privately

Use [GitHub's private vulnerability reporting form](https://github.com/tnt1576-a11y/customwireguard/security/advisories/new).
Describe the affected version, impact and a minimal reproduction with invented
addresses and disposable test credentials. Do not open a public issue containing
an exploitable vulnerability, live credentials or a working private profile.

There is no promised response time or commercial support agreement. Ordinary
bugs with sanitized reproductions can go in GitHub Issues.

## What to keep private

- `.cwg` profiles, generated client/server JSON and WireGuard configuration files.
- Server-generated `client.zip` and per-user ZIPs: these contain connection keys.
- Telegram bot tokens, pairing codes, owner/chat IDs and SSH private keys.
- Diagnostic output, packet captures and screenshots containing endpoints,
  usernames, file paths, public keys, machine names or other identifying details.

Share only the original, unconfigured Windows release ZIP. Importing a profile
adds private data to the extracted folder; do not redistribute that folder.
The server's optional Telegram bot sends profiles through ordinary Telegram bot
messages, not Secret Chats. Restrict it to the intended owner account.

The repository ignores common local secrets and generated artifacts, but ignore
rules do not remove already tracked files or protect Git history. Review staged
changes and release ZIP contents before every push. Rotate exposed credentials;
deleting a public file alone is not enough.

## Network behavior

The server is self-hosted; there is no bundled public server or universal
configuration. Each user/device needs its own profile. DNS uses the tunnel while
the desktop client is connected, even for bypassed applications. Tunneled public
IPv6 is blocked by the current IPv4-egress design. Disconnecting restores normal
networking; there is no persistent kill switch after disconnect or engine exit.
