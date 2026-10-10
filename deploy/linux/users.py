#!/usr/bin/env python3
"""Private, root-only account management for a managed CWG multi-user server."""
import argparse
import base64
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import time
import zipfile

sys.dont_write_bytecode = True

ROOT = Path('/etc/cwg')
JOURNAL = ROOT / 'user-transaction.json'
STATUS = Path('/run/cwg/status.json')
GROUP = 'cwg-secrets'


def json_bytes(value):
    return (json.dumps(value, indent=2) + '\n').encode()


def username(value):
    if not re.fullmatch(r'[a-z][a-z0-9_-]{0,31}', value):
        raise ValueError('Use a lowercase user name, 1..32 letters, digits, _ or -; begin with a letter.')
    return value


def native_fields(text):
    return dict(line.split(' = ', 1) for line in text.splitlines() if ' = ' in line)


def atomic(path, data, mode=0o600, gid=None):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('Refusing a symlink at a managed path.')
    fd, name = tempfile.mkstemp(prefix='.cwg-stage-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fchmod(stream.fileno(), mode)
            if gid is not None:
                os.fchown(stream.fileno(), 0, gid)
            os.fsync(stream.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        Path(name).unlink(missing_ok=True)


def group_id():
    import grp
    group = grp.getgrnam(GROUP)
    if group.gr_mem:
        raise ValueError('The CWG credential group must not contain login users.')
    return group.gr_gid


def initialize(setup, state):
    """Called during a fresh install, before services are started."""
    import grp
    try:
        grp.getgrnam(GROUP)
    except KeyError:
        setup.run('groupadd', '--system', GROUP)
        state['credential_group_created'] = True
        setup.save(state)
    else:
        raise ValueError('Reserved CWG credential group already exists.')
    gid = group_id()
    server = json.loads((ROOT / 'generated/server.json').read_text())
    native = native_fields(setup.WG.read_text())
    state.update(mode='multi', revision=1, users={'owner': {
        'enabled': True, 'slot': 2, 'public_key': native['PublicKey'],
        'preshared_key': native['PresharedKey'], 'config': server,
        'export': str(ROOT / 'client.zip')}})
    multi = server_config(state)
    atomic(ROOT / 'server.json', json_bytes(multi), 0o640, gid)
    os.chown(ROOT, 0, gid)
    ROOT.chmod(0o750)
    state['files'][str(ROOT / 'server.json')] = setup.digest(ROOT / 'server.json')
    setup.save(state)


def server_config(state):
    return {'mode': 'server-multi', 'listen': f"{state['listen_ip']}:{state['endpoint'].rsplit(':', 1)[1]}",
            'max_pps': 8000, 'revision': state['revision'],
            'users': [{'name': name, 'config': user['config']} for name, user in sorted(state['users'].items()) if user['enabled']]}


def native_config(state, original):
    text = original.split('[Peer]', 1)[0].rstrip() + '\n'
    for name, user in sorted(state['users'].items()):
        if user['enabled']:
            slot = user['slot']
            text += (f"\n[Peer]\nPublicKey = {user['public_key']}\nPresharedKey = {user['preshared_key']}\n"
                     f'AllowedIPs = 10.77.0.{slot}/32, fd77:77:77::{slot}/128\n')
    return text.encode()


def command(*args, data=None):
    # Do not echo private stdin, stdout or stderr in a failed management call.
    result = subprocess.run(args, input=data, capture_output=True, timeout=15)
    if result.returncode:
        raise ValueError(f'Management command failed: {Path(args[0]).name} (exit {result.returncode}).')
    return result.stdout


def bundle(state, slot, secret, private_key, psk):
    """Copy only public files; replace all three credential files independently."""
    base = ROOT / 'generated'
    config = json.loads((base / 'client.json').read_text())
    config['key'] = secret
    replacements = {'client.json': json_bytes(config)}
    for name in ('cwg-test.conf', 'cwg-full.conf'):
        text = (base / name).read_text()
        fields = native_fields(text)
        text = text.replace('PrivateKey = ' + fields['PrivateKey'], 'PrivateKey = ' + private_key)
        text = text.replace('PresharedKey = ' + fields['PresharedKey'], 'PresharedKey = ' + psk)
        text = text.replace('10.77.0.2/32', f'10.77.0.{slot}/32').replace('fd77:77:77::2/128', f'fd77:77:77::{slot}/128')
        replacements[name] = text.encode()
    output = io.BytesIO()
    with zipfile.ZipFile(ROOT / 'client.zip') as template, zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as z:
        for item in template.infolist():
            if item.filename.startswith('cwg-client/config/'):
                continue
            # The verified installer created this root-only template. Still use
            # an explicit public-file allowlist so exports cannot inherit keys.
            if item.filename == 'cwg-client/CONNECT.txt' or item.filename == 'cwg-client/bin/cwg-windows-amd64.exe' or item.filename.startswith(('cwg-client/scripts/', 'cwg-client/THIRD-PARTY-LICENSES/')):
                z.writestr(item.filename, template.read(item))
        for name, data in replacements.items():
            z.writestr('cwg-client/config/' + name, data)
    return output.getvalue()


def load_ready(setup):
    if JOURNAL.exists():
        raise ValueError('A pending user operation needs recovery: run users.py recover.')
    state = setup.read_state()
    if state.get('mode') != 'multi' or state['status'] != 'ready':
        raise ValueError('This command requires a ready managed multi-user installation.')
    setup.verify(state)
    return state


def plan_change(setup, state, action, name):
    username(name)
    next_state = copy.deepcopy(state)
    next_state['revision'] += 1
    files = {}
    existing = next_state['users'].get(name)
    if action == 'add':
        if existing:
            raise ValueError('User name already exists, including revoked users; choose a new name.')
        active = {user['slot'] for user in next_state['users'].values() if user['enabled']}
        if len(active) >= 3:
            raise ValueError('At most three active users are supported.')
        if len(next_state['users']) >= 64:
            raise ValueError('The retained account history limit is 64; inspect before rotating further.')
        slot = next(value for value in (2, 3, 4) if value not in active)
        private = command('wg', 'genkey').decode().strip()
        public = command('wg', 'pubkey', data=(private + '\n').encode()).decode().strip()
        psk = base64.b64encode(secrets.token_bytes(32)).decode()
        secret = base64.b64encode(secrets.token_bytes(32)).decode()
        server = json.loads((ROOT / 'generated/server.json').read_text())
        server['key'] = secret
        export = ROOT / f'user-{name}.zip'
        if export.exists() or export.is_symlink():
            raise ValueError('The export path already exists; no files changed.')
        next_state['users'][name] = {'enabled': True, 'slot': slot, 'public_key': public,
                                     'preshared_key': psk, 'config': server, 'export': str(export)}
        files[str(export)] = bundle(state, slot, secret, private, psk)
    elif action == 'revoke':
        if not existing or not existing['enabled']:
            raise ValueError('User is missing or already revoked.')
        existing['enabled'] = False
    else:
        raise ValueError('Unknown user operation.')
    files[str(ROOT / 'server.json')] = json_bytes(server_config(next_state))
    files[str(setup.WG)] = native_config(next_state, setup.WG.read_text())
    for path, data in files.items():
        next_state['files'][path] = hashlib.sha256(data).hexdigest()
    return {'schema': 1, 'action': action, 'name': name, 'previous_revision': state['revision'],
            'state': next_state, 'files': {path: base64.b64encode(data).decode() for path, data in files.items()}}


def check_plan(setup, plan):
    # Validate private JSON and WG syntax before creating the durable journal.
    with tempfile.TemporaryDirectory(prefix='user-check-', dir=ROOT) as directory:
        root = Path(directory)
        config = root / 'server.json'
        config.write_bytes(base64.b64decode(plan['files'][str(ROOT / 'server.json')], validate=True))
        command(str(setup.BINARY), 'check-multi', '-config', str(config))
        native = root / 'cwgstage.conf'
        native.write_bytes(base64.b64decode(plan['files'][str(setup.WG)], validate=True))
        # strip checks wg-quick structure. Actual wg syncconf also validates keys.
        command('wg-quick', 'strip', str(native))


def apply_peer_change(plan):
    # Touch only this account. Reapplying a complete syncconf also resubmits
    # existing peer key material on some wg/kernel versions; the live test
    # observed new native handshakes for unaffected peers after that operation.
    user = plan['state']['users'][plan['name']]
    if plan['action'] == 'revoke':
        command('wg', 'set', 'cwg0', 'peer', user['public_key'], 'remove')
    elif plan['action'] == 'add':
        slot = user['slot']
        peer = (f"[Interface]\n\n[Peer]\nPublicKey = {user['public_key']}\n"
                f"PresharedKey = {user['preshared_key']}\n"
                f'AllowedIPs = 10.77.0.{slot}/32, fd77:77:77::{slot}/128\n')
        command('wg', 'addconf', 'cwg0', '/dev/stdin', data=peer.encode())
    else:
        raise ValueError('Invalid transaction action.')


def recover(setup):
    if not JOURNAL.exists():
        raise ValueError('No pending user operation.')
    if JOURNAL.is_symlink() or JOURNAL.stat().st_size > 64 << 20:
        raise ValueError('Invalid private transaction journal.')
    plan = json.loads(JOURNAL.read_text())
    state = setup.read_state()
    if (plan.get('schema') != 1 or plan['previous_revision'] not in (state['revision'], state['revision'] - 1)
            or plan['state']['revision'] != plan['previous_revision'] + 1):
        raise ValueError('Transaction revision does not match the installation.')
    name = username(plan['name'])
    allowed = {str(ROOT / 'server.json'), str(setup.WG), str(ROOT / f'user-{name}.zip')}
    if not set(plan['files']).issubset(allowed):
        raise ValueError('Invalid transaction target.')
    check_plan(setup, plan)
    gid = group_id()
    for path, encoded in plan['files'].items():
        data = base64.b64decode(encoded, validate=True)
        if hashlib.sha256(data).hexdigest() != plan['state']['files'][path]:
            raise ValueError('Transaction content failed its integrity check.')
        current = Path(path)
        if current.exists() and setup.digest(current) not in (state['files'].get(path), plan['state']['files'][path]):
            raise ValueError('A transaction target was modified externally; inspect before recovery.')
        if path == str(ROOT / 'server.json'):
            atomic(current, data, 0o640, gid)
        else:
            atomic(current, data)
    apply_peer_change(plan)
    deadline = time.monotonic() + 12
    while True:
        try:
            observed = json.loads(STATUS.read_text())
            if observed.get('revision') == plan['state']['revision'] and time.time() * 1000 - observed['utc_unix_ms'] < 10000:
                break
        except (OSError, ValueError, KeyError):
            pass
        if time.monotonic() >= deadline:
            raise ValueError('Server reload not acknowledged. The private journal is retained; run recover after checking the service.')
        time.sleep(.2)
    setup.save(plan['state'])
    setup.verify(plan['state'])
    JOURNAL.unlink()
    print(f"User {name}: {plan['action']} applied; revision {plan['state']['revision']} acknowledged.")
    if plan['action'] == 'add':
        print('Private client export: ' + plan['state']['users'][name]['export'])


def change(setup, action, name):
    state = load_ready(setup)
    plan = plan_change(setup, state, action, name)
    check_plan(setup, plan)
    atomic(JOURNAL, json_bytes(plan))
    recover(setup)


def list_users(setup):
    state = setup.read_state()
    return {'revision': state.get('revision'), 'pending_operation': JOURNAL.exists(),
            'users': [{'name': name, 'enabled': user['enabled'], 'ipv4': f"10.77.0.{user['slot']}", 'export': user['export']}
                      for name, user in sorted(state.get('users', {}).items())]}


def load_setup():
    path = Path(__file__).with_name('server-admin.py')
    if not path.exists():
        path = Path(__file__).with_name('setup.py')
    spec = importlib.util.spec_from_file_location('cwg_setup', path)
    setup = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(setup)
    return setup


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    for action in ('add', 'revoke'):
        sub.add_parser(action).add_argument('name', type=username)
    sub.add_parser('list')
    sub.add_parser('recover')
    args = parser.parse_args()
    if os.name != 'posix' or os.geteuid() != 0:
        raise ValueError('Run as root on the server.')
    os.umask(0o077)
    import fcntl
    with open('/run/lock/cwg-install.lock', 'a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('Another CWG management operation is running.') from None
        setup = load_setup()
        if args.action == 'list':
            print(json.dumps(list_users(setup), indent=2))
        elif args.action == 'recover':
            recover(setup)
        else:
            change(setup, args.action, args.name)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.SubprocessError):
        # The journal may contain keys; never include exception input in output.
        print('CWG user operation failed. Inspect the private server state; use recover if a transaction is pending.', file=sys.stderr)
        sys.exit(1)
