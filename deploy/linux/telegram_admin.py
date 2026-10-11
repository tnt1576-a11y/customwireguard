#!/usr/bin/env python3
"""Install optional Telegram support after a managed CWG server is ready."""
import argparse
import getpass
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import time
import tempfile

sys.dont_write_bytecode = True
ROOT = Path('/etc/cwg-telegram')
PROGRAM = Path('/usr/local/lib/cwg-telegram')
DATA = Path('/var/lib/cwg-telegram')
STATE = ROOT / 'install-state.json'
UNITS = ('cwg-telegram.service', 'cwg-telegram-admin.socket', 'cwg-telegram-admin@.service')
ACCOUNT = 'cwg-telegram'


def run(*args, check=True):
    return subprocess.run(args, text=True, capture_output=True, check=check)


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def module(name):
    source = Path(__file__).with_name(name + '.py')
    spec = importlib.util.spec_from_file_location(name, source)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def save(state):
    data = (json.dumps(state, indent=2) + '\n').encode()
    temporary = ROOT / '.install-state.stage'
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data); stream.flush(); os.fsync(stream.fileno())
        os.replace(temporary, STATE)
        directory = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(directory)
        finally: os.close(directory)
    finally:
        temporary.unlink(missing_ok=True)


def protected(path, mode, uid=0, gid=0):
    info = Path(path).lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != uid or info.st_gid != gid or stat.S_IMODE(info.st_mode) != mode:
        raise ValueError('Managed file ownership or permissions changed')


def managed_server():
    source = Path('/usr/local/lib/cwg/server-admin.py')
    protected(source, 0o755)
    spec = importlib.util.spec_from_file_location('cwg_server_setup', source)
    setup = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(setup)
    state = setup.read_state()
    if state.get('mode') != 'multi' or state.get('status') != 'ready':
        raise ValueError('Install a managed multi-user CWG server first')
    setup.verify(state)


def allowed_files():
    files = {str(ROOT / 'bot.json'): 0o600}
    files.update({str(PROGRAM / name): 0o644 for name in ('telegram_bot.py', 'telegram_helper.py', 'telegram_admin.py')})
    files.update({str(Path('/etc/systemd/system') / name): 0o644 for name in UNITS})
    return files


def read_state():
    protected(STATE, 0o600)
    raw = STATE.read_bytes()
    if len(raw) > 16384:
        raise ValueError('Invalid installation state')
    state = json.loads(raw)
    if state.get('schema') != 1 or set(state.get('files', {})) != set(allowed_files()):
        raise ValueError('Invalid installation manifest')
    return state


def verify(state, missing=False):
    import grp
    import pwd
    for directory, mode in ((ROOT, 0o700), (PROGRAM, 0o755)):
        if directory.is_symlink() or not directory.is_dir() or directory.stat().st_uid != 0 or stat.S_IMODE(directory.stat().st_mode) != mode:
            raise ValueError('Managed directory changed')
    if state.get('uid') is not None:
        user, group = pwd.getpwnam(ACCOUNT), grp.getgrnam(ACCOUNT)
        if (user.pw_uid, user.pw_gid, user.pw_dir, user.pw_shell) != (state['uid'], state['gid'], '/nonexistent', '/usr/sbin/nologin') or group.gr_gid != state['gid'] or group.gr_mem:
            raise ValueError('Dedicated Telegram account changed')
    for name, mode in allowed_files().items():
        path = Path(name)
        if missing and not path.exists() and not path.is_symlink():
            continue
        protected(path, mode)
        if digest(path) != state['files'][name]:
            raise ValueError('A managed file changed; inspect before continuing')


def install(owner, token_file):
    import grp
    import pwd
    managed_server()
    protected(token_file, 0o600)
    with Path(token_file).open('r') as stream:
        token = stream.read(8193).strip()
    cfg = {'owner_id': owner, 'token': token, 'enabled_at': int(time.time())}
    # Validate without writing the token to command arguments or terminal output.
    if not re.fullmatch(r'[0-9]{5,16}:[A-Za-z0-9_-]{30,100}', token) or not 0 < owner < 1 << 52:
        raise ValueError('Invalid token or private owner ID')
    if STATE.exists():
        state = read_state(); verify(state)
        previous = json.loads((ROOT / 'bot.json').read_text())
        if state.get('status') != 'ready' or previous['owner_id'] != owner or previous['token'] != token:
            raise ValueError('Existing bot differs or is incomplete; inspect before replacing it')
        print('Existing optional bot installation verified; activation unchanged.')
        return
    targets = [ROOT, PROGRAM, DATA, Path('/run/cwg-telegram-admin.sock')] + [Path('/etc/systemd/system') / n for n in UNITS]
    if any(p.exists() or p.is_symlink() for p in targets):
        raise ValueError('Reserved Telegram paths already exist')
    for database in ('passwd', 'group'):
        if run('getent', database, ACCOUNT, check=False).returncode == 0:
            raise ValueError('Reserved Telegram account already exists')
    source = Path(__file__).parent
    content = {str(ROOT / 'bot.json'): (json.dumps(cfg) + '\n').encode()}
    content.update({str(PROGRAM / n): (source / n).read_bytes() for n in ('telegram_bot.py', 'telegram_helper.py', 'telegram_admin.py')})
    content.update({str(Path('/etc/systemd/system') / n): (source / n).read_bytes() for n in UNITS})
    state = {'schema': 1, 'status': 'installing', 'uid': None, 'gid': None,
             'files': {name: hashlib.sha256(data).hexdigest() for name, data in content.items()}}
    ROOT.mkdir(mode=0o700); PROGRAM.mkdir(mode=0o755); PROGRAM.chmod(0o755); save(state)
    try:
        run('useradd', '--system', '--user-group', '--home-dir', '/nonexistent', '--shell', '/usr/sbin/nologin', ACCOUNT)
        user = pwd.getpwnam(ACCOUNT); state.update(uid=user.pw_uid, gid=user.pw_gid); save(state)
        for name, data in content.items():
            path = Path(name)
            with path.open('xb') as stream:
                stream.write(data); stream.flush(); os.fsync(stream.fileno())
            path.chmod(allowed_files()[name])
        run('systemctl', 'daemon-reload')
        run('systemd-analyze', 'verify', *(str(Path('/etc/systemd/system') / n) for n in UNITS))
        state['status'] = 'ready'; save(state); verify(state)
    except BaseException:
        state['status'] = 'failed'; save(state)
        raise
    print('Optional bot installed but OFF. Run this script with enable to verify Telegram and start it.')


def identify(token_file):
    protected(token_file, 0o600)
    with Path(token_file).open('r') as stream:
        token = stream.read(8193).strip()
    if not re.fullmatch(r'[0-9]{5,16}:[A-Za-z0-9_-]{30,100}', token):
        raise ValueError('Invalid token')
    updates = module('telegram_bot').Telegram(token).call('getUpdates',
        {'limit': 20, 'timeout': 0, 'allowed_updates': ['message']})
    if not isinstance(updates, list) or len(updates) > 20:
        raise ValueError('Invalid identification response')
    identities = set()
    now = int(time.time())
    for update in updates:
        message = update.get('message', {}) if isinstance(update, dict) else {}
        if not isinstance(message, dict):
            continue
        chat, sender = message.get('chat', {}), message.get('from', {})
        if not isinstance(chat, dict) or not isinstance(sender, dict):
            continue
        identifier, date = sender.get('id'), message.get('date')
        if chat.get('type') == 'private' and chat.get('id') == identifier and type(identifier) is int and identifier > 0 and sender.get('is_bot') is False and type(date) is int and now - 60 <= date <= now + 5 and message.get('text') == '/start' and 'forward_origin' not in message:
            identities.add(identifier)
    print('Recent private /start sender IDs (no account was authorized): ' + ', '.join(map(str, sorted(identities))))
    print('Use your own ID with install --owner-id. If empty or ambiguous, send /start to your dedicated bot and repeat; do not guess.')


def pair_owner(api, username, duration=120):
    """Prove possession of a one-time setup code, never trust the first sender."""
    nonce = 'cwg_' + secrets.token_urlsafe(24)
    started, deadline, offset = int(time.time()), time.monotonic() + duration, 0
    print('Open this one-time link from your own Telegram account and press Start:')
    print(f'https://t.me/{username}?start={nonce}', flush=True)
    while time.monotonic() < deadline:
        updates = api.call('getUpdates', {'offset': offset, 'limit': 20, 'timeout': 10, 'allowed_updates': ['message']})
        if not isinstance(updates, list) or len(updates) > 20:
            raise ValueError('Invalid pairing response')
        for row in updates:
            if not isinstance(row, dict) or type(row.get('update_id')) is not int:
                continue
            offset = max(offset, row['update_id'] + 1)
            msg = row.get('message', {})
            if not isinstance(msg, dict) or 'forward_origin' in msg:
                continue
            chat, sender = msg.get('chat', {}), msg.get('from', {})
            if not isinstance(chat, dict) or not isinstance(sender, dict):
                continue
            owner, date = sender.get('id'), msg.get('date')
            if (type(owner) is int and 0 < owner < 1 << 52 and sender.get('is_bot') is False
                    and chat.get('type') == 'private' and chat.get('id') == owner
                    and type(date) is int and date >= max(started, int(time.time()) - 60)
                    and date <= int(time.time()) + 5 and msg.get('text') == '/start ' + nonce):
                return owner
    raise ValueError('Pairing timed out; no owner was authorized')


def configure(mode='ask', token_file=None, owner=None):
    """Interactive optional setup; failure never tears down a working VPN."""
    if mode == 'ask':
        if not sys.stdin.isatty():
            print('Non-interactive install: Telegram unchanged. Use --telegram enabled with --telegram-token-file and --telegram-owner.')
            return
        choice = input('Enable Telegram management? [y/N]: ').strip().lower()
        if choice not in ('', 'y', 'yes', 'n', 'no'):
            raise ValueError('Choose yes or no')
        mode = 'enabled' if choice in ('y', 'yes') else 'disabled'
    if mode == 'disabled':
        if STATE.exists():
            verify(read_state()); disable()
        print('Telegram management is disabled. CWG remains available.')
        return
    if STATE.exists():
        state = read_state(); verify(state)
        previous = json.loads((ROOT / 'bot.json').read_text())
        if owner is not None and previous['owner_id'] != owner:
            raise ValueError('Existing bot owner differs; inspect before replacing it')
        if token_file is not None:
            protected(token_file, 0o600)
            with Path(token_file).open('r') as stream:
                token = stream.read(8193).strip()
            if not secrets.compare_digest(previous['token'], token):
                raise ValueError('Existing bot token differs; inspect before replacing it')
        enable(state)
        return
    temporary = None
    try:
        if token_file is None:
            if not sys.stdin.isatty():
                raise ValueError('Provide a protected token file for non-interactive setup')
            token = getpass.getpass('Bot token (hidden): ').strip()
            fd, name = tempfile.mkstemp(prefix='.cwg-telegram-token-', dir='/root')
            temporary = Path(name)
            with os.fdopen(fd, 'w') as stream:
                stream.write(token + '\n'); stream.flush(); os.fsync(stream.fileno())
            token_file = temporary
        protected(token_file, 0o600)
        with Path(token_file).open('r') as stream:
            token = stream.read(8193).strip()
        if not re.fullmatch(r'[0-9]{5,16}:[A-Za-z0-9_-]{30,100}', token):
            raise ValueError('Invalid token')
        api = module('telegram_bot').Telegram(token)
        identity = api.call('getMe', {})
        username = identity.get('username', '')
        if identity.get('is_bot') is not True or not re.fullmatch(r'[A-Za-z0-9_]{5,32}', username) or api.call('getWebhookInfo', {}).get('url'):
            raise ValueError('Use a dedicated bot without an existing webhook')
        if owner is None:
            if not sys.stdin.isatty():
                raise ValueError('Provide the numeric owner ID for non-interactive setup')
            value = input('Your numeric Telegram user ID (Enter to pair by a one-time link): ').strip()
            owner = int(value) if value else pair_owner(api, username)
        install(owner, token_file); enable(read_state())
        print(f'Telegram ready: https://t.me/{username} (authorized owner {owner}). Send /start for commands.')
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def enable(state):
    verify(state); managed_server()
    if state['status'] != 'ready':
        raise ValueError('Incomplete installation')
    bot = module('telegram_bot'); cfg = bot.configuration(ROOT / 'bot.json'); api = bot.Telegram(cfg['token'])
    identity = api.call('getMe', {})
    if identity.get('is_bot') is not True or api.call('getWebhookInfo', {}).get('url'):
        raise ValueError('Use a dedicated bot without an existing webhook')
    run('systemctl', 'enable', '--now', 'cwg-telegram-admin.socket', 'cwg-telegram.service')
    run('systemctl', 'is-active', '--quiet', 'cwg-telegram-admin.socket', 'cwg-telegram.service')
    print('Optional bot enabled. Open its private chat and send /status from the configured owner account.')


def disable():
    run('systemctl', 'disable', '--now', 'cwg-telegram.service', 'cwg-telegram-admin.socket', check=False)
    for name in ('cwg-telegram.service', 'cwg-telegram-admin.socket'):
        if run('systemctl', 'is-active', '--quiet', name, check=False).returncode == 0:
            raise ValueError('Optional bot could not be stopped')
    # Let an already accepted account operation finish rather than interrupting
    # its transaction. The helper has its own 35-second runtime deadline.
    for _ in range(40):
        current = run('systemctl', 'list-units', '--no-legend', '--plain',
                      '--state=active,activating,deactivating', 'cwg-telegram-admin@*.service').stdout
        if not current.strip():
            return
        time.sleep(1)
    raise ValueError('A management helper is still active')


def uninstall(state):
    verify(state, missing=state.get('status') != 'ready')
    disable()
    if DATA.exists():
        if DATA.is_symlink() or DATA.stat().st_uid != state['uid']:
            raise ValueError('Bot state directory ownership changed')
        entries = list(DATA.iterdir())
        if any(p.name != 'offset.json' or p.is_symlink() or not p.is_file() or p.stat().st_uid != state['uid'] for p in entries):
            raise ValueError('Unexpected bot state files; preserved for inspection')
        for p in entries:
            p.unlink()
        DATA.rmdir()
    for path in map(Path, allowed_files()):
        path.unlink(missing_ok=True)
    if state.get('uid') is not None:
        run('userdel', ACCOUNT)
        run('groupdel', ACCOUNT, check=False)
    STATE.unlink()
    for path in (ROOT, PROGRAM):
        try: path.rmdir()
        except OSError: print('Preserved directory containing additional files: ' + str(path))
    run('systemctl', 'daemon-reload')
    print('Optional bot removed; CWG server, peers and bundles were preserved.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    create = sub.add_parser('install'); create.add_argument('--owner-id', type=int, required=True); create.add_argument('--token-file', type=Path, required=True)
    lookup = sub.add_parser('identify'); lookup.add_argument('--token-file', type=Path, required=True)
    wizard = sub.add_parser('configure'); wizard.add_argument('--mode', choices=('ask', 'enabled', 'disabled'), default='ask')
    wizard.add_argument('--token-file', type=Path); wizard.add_argument('--owner-id', type=int)
    for name in ('verify', 'enable', 'disable', 'uninstall'):
        sub.add_parser(name)
    args = parser.parse_args()
    if os.name != 'posix' or os.geteuid() != 0:
        raise ValueError('Run as root on the Linux server')
    import fcntl
    os.umask(0o077)
    with open('/run/lock/cwg-telegram-install.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.action == 'configure':
            configure(args.mode, args.token_file, args.owner_id)
        elif args.action == 'identify':
            identify(args.token_file)
        elif args.action == 'install':
            install(args.owner_id, args.token_file)
        else:
            state = read_state()
            if args.action == 'uninstall': uninstall(state)
            elif args.action == 'disable': disable(); print('Optional bot disabled.')
            elif args.action == 'enable': enable(state)
            else: verify(state); print('Optional bot files, permissions and dedicated identity verified.')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        # Exception values may contain a credential path or API token.
        print('CWG Telegram operation failed. Inspect the private installation state and systemd journal; no secrets are printed.', file=sys.stderr)
        sys.exit(1)
