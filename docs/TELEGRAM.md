# Optional Telegram management

Telegram support is separate from the transport and is **off by default**. It
requires a completed managed 0.6 multi-user installation. It does not support the
older manually installed server layout. Use a dedicated bot and keep its token
out of chat, GitHub, client ZIPs and shell command arguments.

## Set up a dedicated owner

Create a bot with Telegram's BotFather. On the Linux server, as root, save its
token privately. This example prompts without echo and refuses to overwrite an
existing file:

```sh
python3 -c 'import getpass,os; token=getpass.getpass("Bot token: ").strip(); fd=os.open("/root/cwg-telegram-token",os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600); os.write(fd,(token+"\n").encode()); os.close(fd)'
```

Open the bot's **private** Telegram chat from your own account and send `/start`.
Within one minute, run this from the extracted CWG release directory:

```sh
python3 deploy/linux/telegram_admin.py identify --token-file /root/cwg-telegram-token
```

The command lists recent private `/start` sender IDs. It sends no message and
authorizes nobody. If the result is empty or ambiguous, repeat your own `/start`
and the lookup. Use your numeric ID in place of `YOUR_NUMERIC_ID` below:

```sh
python3 deploy/linux/telegram_admin.py install --owner-id YOUR_NUMERIC_ID --token-file /root/cwg-telegram-token
python3 /usr/local/lib/cwg-telegram/telegram_admin.py enable
```

Installation verifies the managed CWG server, creates a dedicated non-login
account, and installs the optional units **without starting them**. `enable`
checks the token with Telegram and refuses a bot that already uses a webhook.
Repeated installation verifies identical files and credentials without rotating
keys or changing activation. Retain the original token file privately or remove
that specific file after successful setup; the service's root-only configuration
contains its own protected copy.

The implementation uses outbound HTTPS long polling through the
[official Telegram Bot API](https://core.telegram.org/bots/api#getupdates).
It needs no public TCP listener, webhook, domain or change to the UDP tunnel.
The production endpoint is fixed to `api.telegram.org`, with normal TLS
certificate validation. A network outage affects management availability, not
the running tunnel. Do not run another poller against the same bot.

## Commands

| Private owner message | Result |
|---|---|
| `/status` | Service state, memory/CPU counters, and per-user transport counters with their age |
| `/users` | Enabled/revoked accounts and whether a transaction needs recovery |
| `/add alice` | Adds a separate account and creates its private ZIP on the server |
| `/revoke alice` | Revokes that account's outer credential and native peer |
| `/restart` | Restarts the outer transport; clients reconnect and native peers remain configured |
| `/help` | Command list |

Bundles and private keys are **never sent through Telegram**. Retrieve an added
user's `/etc/cwg/user-alice.zip` through SSH and give it only to that person, as
described in [multi-user setup](MULTI-USER.md). Account limits and recovery rules
remain the same as the root CLI. No arbitrary shell command, file path, service
name, key export or automatic upgrade is accepted.

Both the message sender and private-chat ID must equal the configured owner.
Group, forwarded, edited, stale and foreign-sender messages are ignored. Commands
must be newer than installation and at most 60 seconds old. Use the exact command
syntax above. The owner is limited to one accepted command per three seconds;
extra commands during that interval are ignored. Restarts are additionally
limited to one per 30 seconds by the root helper.

The received update offset is flushed to disk **before** executing management.
After a crash or missing response, an action may already have completed; check
`/status` or `/users`, or inspect the server through SSH, before retrying. It is
deliberately not replayed automatically. A failed account operation retains its
private transaction journal; recover it using the instructions in MULTI-USER.md.
Monitoring is on demand; this version does not send unsolicited periodic alerts.

## Disable, verify or remove

```sh
python3 /usr/local/lib/cwg-telegram/telegram_admin.py verify
python3 /usr/local/lib/cwg-telegram/telegram_admin.py disable
python3 /usr/local/lib/cwg-telegram/telegram_admin.py uninstall
```

Disable stops polling and new management requests and waits for a bounded,
already accepted account operation to finish. Removal deletes only verified bot
files/account and its known offset state; it preserves the CWG server, peer keys
and exported bundles. Changed files/permissions or unexpected bot state files
are refused for inspection. Unrelated program-directory files are preserved.
Remove this optional component before uninstalling the main CWG server. If a bot
installation fails, its private manifest is retained for inspection/removal.

For token or owner rotation, disable/remove the component and reinstall using a
new protected token file and the intended owner ID. Revoke a compromised token
through BotFather. Keep SSH access available independently of Telegram.

## Resource use and validation

The polling process runs as `cwg-telegram`, cannot read server/client private keys,
and has a 64 MiB memory limit and 10% of one CPU quota. It uses 40-second long
polls, at most 20 updates per response, a 1 MiB response bound and 5–300-second
failure backoff. Its root helper is socket-activated, accepts only the dedicated
UID/root, and exits after each fixed command. Each helper has a 35-second deadline,
128 MiB memory limit and 50% CPU quota; at most two socket instances may exist,
and a management lock prevents concurrent account operations. The helper can
write only the required CWG configuration/lock locations under its systemd policy.

An isolated Debian runtime test used the real service identities, socket
activation, certificate-verified local HTTPS API fixture and actual CWG account
management. During 42.016 seconds of idle polling, the bot consumed 0.001995 CPU
seconds and 11.63 MiB memory. Six owner commands over 34.158 seconds consumed
0.024086 bot CPU seconds; bot peak was 13.63 MiB and the provisioning helper peaked
at 52.88 MiB including its child process. Helper CPU is separately quota-limited
and is not included in the bot CPU figures. These are fixture measurements,
not measurements against Telegram's external service.

Tests covered foreign-owner silence, source/permission tampering, refusal to
control another service, denial to unrelated local users, private-key boundaries,
account add/revoke, restart, repeated installation, clean removal, injected
install-failure cleanup and preservation of unrelated files. Portable tests cover
offset-before-action crash behavior, malformed/API response bounds and lookup
without authorization. No real bot token was supplied, so live Telegram delivery
remains a setup check for the operator.
