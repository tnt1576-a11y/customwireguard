#!/usr/bin/env python3
"""Socket-activated root helper; accepts only fixed CWG operations."""
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import re
import socket
import stat
import struct
import subprocess
import sys
import time
import zipfile

PROGRAM = Path('/usr/local/lib/cwg')
ROOT = Path('/etc/cwg')


def request(value):
    if not isinstance(value, dict):
        raise ValueError('Invalid command')
    action = value.get('action')
    if action in ('status', 'users', 'restart') and set(value) == {'action'}:
        return value
    if action in ('add', 'revoke', 'config') and set(value) == {'action', 'name'} and isinstance(value['name'], str) and re.fullmatch(r'[a-z][a-z0-9_-]{0,31}', value['name']):
        return value
    raise ValueError('Unsupported command')


def private_json(path, limit=262144):
    with open(path, 'rb') as stream:
        data = stream.read(limit + 1)
    if len(data) > limit:
        raise ValueError('State too large')
    return json.loads(data)


def run(*args, timeout=25):
    result = subprocess.run(args, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise ValueError('CWG operation failed; inspect the server or recover a pending user transaction')
    return result.stdout


def client_profile(name):
    """Export only this active account's data; never programs or server keys."""
    state = private_json(ROOT / 'install-state.json')
    user = state.get('users', {}).get(name)
    if not user or user.get('enabled') is not True or (ROOT / 'user-transaction.json').exists():
        raise ValueError('Account is disabled, unknown, or an operation is pending')
    path = ROOT / ('client.zip' if name == 'owner' else f'user-{name}.zip')
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        raise ValueError('Private export permissions changed')
    if info.st_size > 64 << 20 or hashlib.sha256(path.read_bytes()).hexdigest() != state['files'].get(str(path)):
        raise ValueError('Private export changed')
    selected = ('client.json', 'cwg-full.conf', 'cwg-test.conf')
    output = io.BytesIO()
    with zipfile.ZipFile(path) as source, zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as target:
        for item in selected:
            entry = source.getinfo('cwg-client/config/' + item)
            if entry.file_size > 16384:
                raise ValueError('Profile too large')
            target.writestr(item, source.read(entry))
        target.writestr('profile.json', json.dumps({'schema': 1, 'name': name, 'format': 'cwg-profile'}))
    raw = output.getvalue()
    if len(raw) > 8192:
        raise ValueError('Profile too large')
    return {'text': f'Private configuration for {name}. Import this .cwg file in CWG. Give it only to this user; one account per simultaneous device.',
            'document': {'name': name + '.cwg', 'base64': base64.b64encode(raw).decode()}}


def execute(value):
    value = request(value)
    state = private_json(ROOT / 'install-state.json')
    if state.get('mode') != 'multi' or state.get('status') != 'ready':
        raise ValueError('A completed managed multi-user installation is required')
    action = value['action']
    if action == 'status':
        props = run('systemctl', 'show', 'cwg', '-p', 'ActiveState', '-p', 'SubState', '-p', 'MemoryCurrent', '-p', 'CPUUsageNSec')
        text = 'CWG server\n' + props.strip()
        try:
            current = private_json('/run/cwg/status.json')
            age = max(0, int(time.time() - current['utc_unix_ms'] / 1000))
            text += f"\nCounters age: {age}s; revision: {current['revision']}\nShared queue expirations: {current['queue_expired']}; drops: {current['queue_dropped']}"
            for name, counters in sorted(current['users'].items()):
                if not re.fullmatch(r'[a-z][a-z0-9_-]{0,31}', name):
                    continue
                text += f"\n{name}: sessions={counters['sessions']}, requests={counters['exchange_requests']}, data expirations={counters['exchange_queue_expired']}"
        except (OSError, ValueError, KeyError, TypeError):
            text += '\nRuntime counters unavailable.'
        return text
    if action == 'users':
        lines = ['CWG accounts (use a separate bundle per device):']
        for name, user in sorted(state['users'].items()):
            if re.fullmatch(r'[a-z][a-z0-9_-]{0,31}', name):
                lines.append(name + ': ' + ('enabled' if user['enabled'] else 'revoked'))
        if (ROOT / 'user-transaction.json').exists():
            lines.append('A user operation is pending; inspect and recover through SSH.')
        return '\n'.join(lines)
    if action == 'restart':
        run('systemctl', 'restart', 'cwg')
        run('systemctl', 'is-active', '--quiet', 'cwg')
        return 'CWG transport restarted. Clients reconnect automatically; native peer configuration was preserved.'
    name = value['name']
    if action == 'config':
        return client_profile(name)
    run('/usr/bin/python3', str(PROGRAM / 'users.py'), action, name)
    if action == 'add':
        return client_profile(name)
    return f'Account {name} revoked.'


def main():
    import fcntl
    import pwd
    if os.geteuid() != 0 or len(sys.argv) != 1:
        raise ValueError('Socket activation required')
    with socket.socket(fileno=os.dup(0)) as conn:
        conn.settimeout(4)
        _, uid, _ = struct.unpack('3i', conn.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if uid not in (0, pwd.getpwnam('cwg-telegram').pw_uid):
            return
        raw = bytearray()
        while b'\n' not in raw and len(raw) <= 1024:
            chunk = conn.recv(1025 - len(raw))
            if not chunk:
                return
            raw.extend(chunk)
        try:
            if len(raw) > 1024 or not raw.endswith(b'\n'):
                raise ValueError('Invalid command')
            value = request(json.loads(raw))
            fd = os.open('/run/lock/cwg-telegram-admin.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, 'r+') as lock:
                info = os.fstat(lock.fileno())
                if info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600 or not stat.S_ISREG(info.st_mode):
                    raise ValueError('Invalid management lock')
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                text = lock.read(4097)
                if len(text) > 4096:
                    raise ValueError('Invalid rate state')
                recent = json.loads(text) if text else {}
                action, now = value['action'], int(time.time())
                if action in ('restart', 'add', 'revoke'):
                    interval = 30 if action == 'restart' else 3
                    group = 'restart' if action == 'restart' else 'change'
                    if now - recent.get(group, 0) < interval:
                        raise ValueError('Management action rate-limited; wait before retrying')
                    recent[group] = now
                    lock.seek(0); lock.truncate(); json.dump(recent, lock); lock.flush(); os.fsync(lock.fileno())
                response = execute(value)
        except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
            response = 'CWG management refused or did not complete. Inspect server state through SSH before retrying; recover any pending user operation.'
        response = response if isinstance(response, dict) else {'text': response[:3500]}
        encoded = json.dumps(response).encode() + b'\n'
        if len(encoded) > 16384:
            raise ValueError('Management response too large')
        conn.sendall(encoded)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError):
        print('CWG Telegram helper failed; inspect local service state.', file=sys.stderr)
        sys.exit(1)
