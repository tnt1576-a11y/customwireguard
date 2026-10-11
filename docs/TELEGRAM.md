# Optional Telegram management

Telegram management requires a managed 0.6 server. The fresh installer asks
**Enable Telegram management? [y/N]** after verifying CWG. Answer yes to enter a
hidden bot token. Enter your numeric Telegram user ID, or press Enter and open
the generated one-time pairing link from your own account. Pairing requires the
random setup code; an arbitrary first sender is never authorized. Use a dedicated
bot created with BotFather. Answer no to leave management disabled.

Enable or disable an existing installation without rotating VPN keys:

```sh
python3 /usr/local/lib/cwg-telegram/telegram_admin.py configure
```

If it has never been installed, run `deploy/linux/telegram_admin.py configure`
from the release directory. Noninteractive installs do not prompt or enable
Telegram implicitly. Explicit automation uses:

```sh
python3 deploy/linux/setup.py install --release . --endpoint YOUR_SERVER_IPV4:443 --wan eth0 --telegram enabled --telegram-token-file /root/bot-token --telegram-owner YOUR_NUMERIC_ID
```

The token file must be root-owned, mode 600. Keep tokens out of command arguments,
source and client packages. Repeated setup accepts matching credentials and
refuses a different owner/token. For identity changes, disable/remove the bot
component and configure it again. Optional setup failure leaves CWG available;
retry bot setup independently.

## Create and share a user

Open the bot's private chat and send `/start`. Only the configured owner can
manage accounts.

| Command | Result |
|---|---|
| `/status` | Service health and per-user transport counters |
| `/users` | Active/revoked accounts and pending-operation status |
| `/add alice` | Creates a separate account and sends `alice.cwg` to the owner |
| `/config alice` | Resends an active account's configuration |
| `/config owner` | Sends the initial owner's configuration |
| `/revoke alice` | Revokes that account's outer credential and native peer |
| `/restart` | Restarts the transport; clients reconnect |
| `/help` | Command list |

Give `alice.cwg` and the public Windows app ZIP to Alice. They extract the app,
double-click **CWG.exe**, click **Import configuration**, select the `.cwg` file,
choose Full PC or Split tunnel, and connect. Each simultaneous device needs its
own account. Three active accounts are supported, including the owner. Revoked
names remain in history; choose a new name when replacing an account.

The `.cwg` document contains that user's private connection keys. It is sent as
an ordinary bot document, not a Secret Chat, and can be forwarded to the intended
user. Treat the file and owner account as credentials. No server private key,
bot token, programs or another user's keys are included. The app accepts only
four bounded data files and never executes profile content. An SSH-only export
remains available at `/etc/cwg/user-alice.zip`.

Commands must be exact, unforwarded private messages from the owner, newer than
installation and at most 60 seconds old. Group, edited and foreign messages are
ignored. Wait at least three seconds between commands. Restart is additionally
limited to once per 30 seconds. The update offset is saved before execution:
after a missing response, check `/users`, then use `/config alice` to retrieve an
already-created account. Actions are not replayed automatically. Recover pending
transactions through SSH as described in [MULTI-USER.md](MULTI-USER.md).

## Operation and removal

The bot uses outbound HTTPS polling and document upload through the
[official Bot API](https://core.telegram.org/bots/api#senddocument), with normal
TLS validation. It needs no domain, webhook or new public listener. An outage
affects management availability, not the existing tunnel. Do not run another
poller against the same bot. No periodic notifications are sent.

```sh
python3 /usr/local/lib/cwg-telegram/telegram_admin.py verify
python3 /usr/local/lib/cwg-telegram/telegram_admin.py disable
python3 /usr/local/lib/cwg-telegram/telegram_admin.py enable
python3 /usr/local/lib/cwg-telegram/telegram_admin.py uninstall
```

Removal verifies owned files and waits for bounded accepted operations. It
preserves CWG accounts, keys and exports. Remove this component before removing
CWG itself. Keep SSH access available independently of Telegram.

The polling service runs as a dedicated non-login user with a 64 MiB memory limit
and 10% of one CPU quota. It cannot directly read VPN keys; its restricted root
helper deliberately permits exporting a named active client profile, plus fixed
status/account/restart operations. It accepts no arbitrary command, path or
service name. Each helper has a 35-second deadline, 128 MiB memory limit and 50%
CPU quota, with serialized management changes.

Tests cover authorization, stale/forwarded messages, replay protection, pairing,
multipart bounds, export isolation and repeated setup identity. Earlier systemd
lifecycle tests used a local TLS fixture. Live `/start` and `/status` replies
were subsequently confirmed by the owner on 2026-10-11.
