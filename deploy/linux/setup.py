#!/usr/bin/env python3
"""Fresh Debian server setup. Owns named CWG files/tables, never the ruleset."""
import argparse
import errno
import hashlib
import ipaddress
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import zipfile

# Management modules live in the owned program directory. Avoid creating
# untracked bytecode there that would block a clean uninstall/reinstall.
sys.dont_write_bytecode = True

ROOT = Path('/etc/cwg')
STATE = ROOT / 'install-state.json'
BINARY = Path('/usr/local/lib/cwg/cwg')
ADMIN = Path('/usr/local/lib/cwg/server-admin.py')
USERS = Path('/usr/local/lib/cwg/users.py')
WG = Path('/etc/wireguard/cwg0.conf')
SYSCTL = Path('/etc/sysctl.d/90-cwg.conf')
UNIT = Path('/etc/systemd/system')
UNITS = ('cwg.service', 'wg-quick@cwg0.service', 'cwg-guard.service')
SCRIPTS = ('Start-Client.ps1', 'Stop-Client.ps1', 'WireGuard-Service.ps1', 'Diagnose-Client.ps1',
           'App-Tunnel.ps1', 'App-Tunnel.Common.ps1', 'App-EngineProcess.ps1',
           'Open-AppClient.ps1', 'Install-AppEngine.ps1', 'Desktop-Bridge.ps1', 'Import-CwgProfile.ps1')


def run(*args, capture=False, check=True):
    return subprocess.run(args, check=check, text=True, stdout=subprocess.PIPE if capture else None,
                          stderr=subprocess.PIPE if capture else None)


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1 << 20), b''):
            h.update(block)
    return h.hexdigest()


def endpoint(value):
    if ':' not in value:
        value += ':443'
    host, port = value.rsplit(':', 1)
    ip = ipaddress.IPv4Address(host)
    if ip.is_unspecified or ip.is_loopback or ip.is_multicast or not port.isdecimal():
        raise ValueError('Use the server IPv4 address and a UDP port.')
    number = int(port)
    if not 1 <= number <= 65535 or number == 51820:
        raise ValueError('Outer port must be 1..65535 and different from native port 51820.')
    return f'{ip}:{number}'


def interface(value):
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,15}', value) or value in ('lo', 'cwg0'):
        raise ValueError('Specify the physical outgoing interface, for example eth0 or ens3.')
    return value


def release_files(release):
    release = Path(release).resolve(strict=True)
    hashes = {}
    for line in (release / 'SHA256SUMS').read_text(encoding='utf-8-sig').splitlines():
        expected, name = line.split('  ', 1)
        parts = name.split('/')
        if not re.fullmatch('[a-f0-9]{64}', expected) or any(x in ('', '.', '..') for x in parts) or '\\' in name or ':' in name:
            raise ValueError('Invalid release checksum entry.')
        if name in hashes:
            raise ValueError('Duplicate release checksum entry.')
        hashes[name] = expected
    arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine())
    if not arch:
        raise ValueError('This installer requires Linux amd64 or arm64.')
    required = [f'bin/cwg-linux-{arch}', 'bin/cwg-windows-amd64.exe', 'CWG.exe', 'deploy/linux/cwg.service',
                'deploy/linux/cwg-multi.service', 'deploy/linux/users.py']
    required += ['deploy/linux/' + name for name in ('telegram_admin.py', 'telegram_bot.py', 'telegram_helper.py',
                 'cwg-telegram.service', 'cwg-telegram-admin.socket', 'cwg-telegram-admin@.service')]
    required += ['scripts/' + x for x in SCRIPTS]
    required += [x for x in hashes if x.startswith('THIRD-PARTY-LICENSES/')]
    if not any(x.startswith('THIRD-PARTY-LICENSES/') for x in required):
        raise ValueError('Release licenses are missing.')
    for name in required:
        path = release / name
        if path.is_symlink() or not path.is_file() or any(p.is_symlink() for p in path.parents if p != release and release in p.parents):
            raise ValueError('Invalid release file.')
        if release not in path.resolve().parents or digest(path) != hashes.get(name):
            raise ValueError(f'Release integrity check failed: {name}')
    return release, f'bin/cwg-linux-{arch}', required


def network_files(wan, port):
    interface(wan)
    if not 1 <= port <= 65535 or port == 51820:
        raise ValueError('Invalid public UDP port.')
    # Public IPv6 is captured by the client and rejected promptly here. An
    # IPv4-only server must not cause the client to bypass its ISP over IPv6.
    rules = f'''table inet cwg_guard {{
 chain input {{
  type filter hook input priority -10; policy accept;
  iifname != "lo" udp dport 51820 drop
  udp dport {port} accept
 }}
 chain forward {{
  type filter hook forward priority -10; policy accept;
  iifname "cwg0" meta nfproto ipv6 reject
  iifname "cwg0" ip saddr != 10.77.0.0/24 drop
  iifname "cwg0" oifname "{wan}" accept
  iifname "cwg0" drop
  oifname "cwg0" ct state established,related accept
  oifname "cwg0" drop
 }}
}}
table ip cwg_nat {{
 chain postrouting {{
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr 10.77.0.0/24 oifname "{wan}" masquerade
 }}
}}
'''
    sysctl = (f'net.ipv6.conf.{wan}.accept_ra=2\n'
              'net.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=1\n')
    guard = '''[Unit]
Description=CWG private backend guard and Internet forwarding
After=network-online.target nftables.service
Wants=network-online.target
Before=wg-quick@cwg0.service cwg.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/cwg/guard.nft
ExecStop=/usr/bin/python3 /usr/local/lib/cwg/server-admin.py remove-tables
[Install]
WantedBy=multi-user.target
'''
    return rules, sysctl, guard


def socket_buffer_settings():
    # Linux can silently cap SO_RCVBUF/SO_SNDBUF while the Go setters succeed.
    # Match the transport's 2 MiB request and preserve larger host-wide limits.
    settings = ''
    for name in ('net.core.rmem_max', 'net.core.wmem_max'):
        current = int(run('sysctl', '-n', name, capture=True).stdout.strip())
        if current <= 0:
            raise ValueError('Invalid UDP socket buffer limit.')
        settings += f'{name}={max(current, 2 << 20)}\n'
    return settings


def owned_path(path):
    p = PurePosixPath(str(path))
    fixed = ('/usr/local/lib/cwg/cwg', '/usr/local/lib/cwg/server-admin.py', '/usr/local/lib/cwg/users.py',
             '/etc/wireguard/cwg0.conf', '/etc/sysctl.d/90-cwg.conf',
             '/etc/systemd/system/cwg.service', '/etc/systemd/system/cwg-guard.service',
             '/etc/systemd/system/wg-quick@cwg0.service.d/cwg.conf')
    return p.is_absolute() and '..' not in p.parts and (str(p) in fixed or PurePosixPath('/etc/cwg') in p.parents)


def save(state):
    fd, name = tempfile.mkstemp(prefix='.install-state-', dir=STATE.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(json.dumps(state, indent=2) + '\n')
            stream.flush()
            os.fchmod(stream.fileno(), 0o600)
            os.fsync(stream.fileno())
        os.replace(name, STATE)
        directory = os.open(STATE.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        Path(name).unlink(missing_ok=True)


def read_state():
    s = json.loads(STATE.read_text())
    if s.get('schema') != 1 or any(not owned_path(p) for p in s['files']):
        raise ValueError('Invalid managed installation state.')
    return s


def write_owned(state, path, data, mode=0o600):
    path = Path(path)
    if not owned_path(path) or path.exists() or path.is_symlink():
        raise ValueError(f'Refusing to replace existing file: {path}')
    parent_new = not path.parent.exists()
    path.parent.mkdir(parents=True, exist_ok=True)
    if parent_new and path.parent in (BINARY.parent, UNIT / 'wg-quick@cwg0.service.d'):
        path.parent.chmod(0o755)
    with path.open('xb') as f:
        f.write(data.encode() if isinstance(data, str) else data)
    path.chmod(mode)
    state['files'][str(path)] = digest(path)
    save(state)


def remove_tables():
    read_state()  # A similarly named table in an unmanaged install is not ours.
    for family, name in (('ip', 'cwg_nat'), ('inet', 'cwg_guard')):
        if run('nft', 'list', 'table', family, name, capture=True, check=False).returncode == 0:
            run('nft', 'delete', 'table', family, name)


def stop_owned():
    for service in UNITS[:2]:
        run('systemctl', 'disable', '--now', service, capture=True, check=False)
    # Keep the private-backend guard if either process/interface cannot stop.
    if (run('systemctl', 'is-active', '--quiet', 'cwg.service', capture=True, check=False).returncode == 0 or
            run('ip', 'link', 'show', 'cwg0', capture=True, check=False).returncode == 0):
        raise ValueError('CWG did not stop; private-backend firewall and files were retained.')
    run('systemctl', 'disable', '--now', 'cwg-guard.service', capture=True, check=False)
    remove_tables()


def verify(state):
    for name, expected in state['files'].items():
        p = Path(name)
        if p.is_symlink() or not p.is_file() or digest(p) != expected:
            raise ValueError(f'Managed file changed or missing: {p}')
    if state.get('mode') == 'multi':
        import grp
        credential_group = grp.getgrnam('cwg-secrets')
        if credential_group.gr_mem:
            raise ValueError('The CWG credential group has unexpected login members.')
        if ROOT.stat().st_uid != 0 or ROOT.stat().st_gid != credential_group.gr_gid or ROOT.stat().st_mode & 0o777 != 0o750:
            raise ValueError('Private configuration directory permissions changed.')
        for p in [STATE, WG] + [Path(name) for name in state['files'] if ROOT in Path(name).parents]:
            expected_mode = 0o640 if p == ROOT / 'server.json' else 0o600
            if p.is_symlink() or p.stat().st_uid != 0 or p.stat().st_mode & 0o777 != expected_mode:
                raise ValueError('A private file no longer has its required permissions.')
        if (ROOT / 'server.json').stat().st_gid != credential_group.gr_gid:
            raise ValueError('Server credential group changed.')
    run(str(BINARY), 'check-multi' if state.get('mode') == 'multi' else 'check', '-config', str(ROOT / 'server.json'))
    run('systemctl', 'is-active', '--quiet', *UNITS)
    for family, name in (('ip', 'cwg_nat'), ('inet', 'cwg_guard')):
        run('nft', 'list', 'table', family, name, capture=True)
    for key, expected in state['sysctl_applied'].items():
        if run('sysctl', '-n', key, capture=True).stdout.strip() != expected:
            raise ValueError(f'Kernel setting changed: {key}')
    link = json.loads(run('ip', '-j', 'link', 'show', 'cwg0', capture=True).stdout)
    if len(link) != 1 or link[0]['mtu'] != 1280:
        raise ValueError('Native tunnel interface is not ready.')
    if state.get('mode') == 'multi':
        expected = {u['public_key']: {f"10.77.0.{u['slot']}/32", f"fd77:77:77::{u['slot']}/128"}
                    for u in state['users'].values() if u['enabled']}
        observed = {}
        for line in run('wg', 'show', 'cwg0', 'allowed-ips', capture=True).stdout.splitlines():
            key, *addresses = line.replace(',', ' ').split()
            observed[key] = set(addresses)
        if expected != observed:
            raise ValueError('Active WireGuard peer identities or address isolation differs from managed state.')


def export_client(state, release, verified):
    path = ROOT / 'client.zip'
    if path.exists():
        raise ValueError('Client export already exists.')
    with zipfile.ZipFile(path, 'x', compression=zipfile.ZIP_DEFLATED) as z:
        for name in verified:
            if name in ('bin/cwg-windows-amd64.exe', 'CWG.exe') or name.startswith(('scripts/', 'THIRD-PARTY-LICENSES/')):
                z.write(release / name, 'cwg-client/' + name)
        for name in ('client.json', 'cwg-test.conf', 'cwg-full.conf'):
            z.write(ROOT / 'generated' / name, 'cwg-client/config/' + name)
        z.writestr('cwg-client/CONNECT.txt', '''Keep this ZIP private; it contains this user's keys.
Extract the complete folder and double-click CWG.exe. Accept the Windows
administrator prompt. Choose Full PC or Split tunnel, then Connect.
The routing engine is downloaded and checksum-verified on first use if absent.
Import a .cwg file from your administrator to use another account.

Optional legacy native mode (official WireGuard Windows required):
Open Administrator PowerShell in this folder:
  .\\scripts\\Start-Client.ps1 -Bundle .\\config -Mode Test
  ping 10.77.0.1
  .\\scripts\\Stop-Client.ps1 -Bundle .\\config
For IPv4 Internet access, start with -Mode Full instead.
Stop with the same Stop-Client command. Public IPv6 is captured and rejected by
the server; it does not fall back to the ISP while the Full tunnel is active.
Full PC and application selection are both available in CWG.exe.
All Windows DNS uses the tunnel while app mode runs. Selected public IPv6 is blocked;
LAN access bypasses the tunnel. Stop native CWG before using app mode.
There is no kill switch after removal. This export is one user's private identity;
create a separate named user on the server for each simultaneous device.
''')
    path.chmod(0o600)
    state['files'][str(path)] = digest(path)
    save(state)


def install(args):
    ep, wan = endpoint(args.endpoint), interface(args.wan)
    release, executable, verified = release_files(args.release)
    if STATE.exists():
        state = read_state()
        if state['status'] != 'ready' or state['endpoint'] != ep or state['wan'] != wan or state['binary_sha256'] != digest(release / executable):
            raise ValueError('Managed installation differs or is incomplete. Inspect it; no files were changed.')
        verify(state)
        print('Existing installation verified; keys and client export preserved.')
        return
    if platform.system() != 'Linux' or not Path('/run/systemd/system').is_dir():
        raise ValueError('A booted Linux systemd host is required.')
    os_release = dict(line.split('=', 1) for line in Path('/etc/os-release').read_text().splitlines() if '=' in line)
    if os_release.get('ID', '').strip('"') != 'debian' or int(os_release.get('VERSION_ID', '0').strip('"')) < 13:
        raise ValueError('This installer targets Debian 13 or newer.')
    reserved = (ROOT, BINARY.parent, WG, SYSCTL, UNIT / 'cwg.service', UNIT / 'cwg-guard.service', UNIT / 'wg-quick@cwg0.service.d')
    if any(p.exists() or p.is_symlink() for p in reserved):
        raise ValueError('Existing CWG files found. Fresh installation refused before dependency or network changes.')
    for parent in (ROOT.parent, BINARY.parent.parent, WG.parent, SYSCTL.parent, UNIT):
        if parent.exists() and (parent.is_symlink() or parent.stat().st_uid != 0 or parent.stat().st_mode & 0o022):
            raise ValueError(f'Installation parent must be a root-owned protected directory: {parent}')
    missing = [x for x in ('wg', 'wg-quick', 'nft', 'ip', 'ss', 'sysctl') if not shutil.which(x)]
    if missing:
        run('apt-get', 'update')
        run('apt-get', 'install', '-y', '--no-install-recommends', 'wireguard-tools', 'nftables', 'iproute2', 'procps')
    run('ip', 'link', 'show', wan, capture=True)
    outgoing = json.loads(run('ip', '-j', 'route', 'get', '1.1.1.1', capture=True).stdout)
    if len(outgoing) != 1 or outgoing[0].get('dev') != wan:
        raise ValueError('The selected interface is not the current IPv4 Internet route.')
    addresses = json.loads(run('ip', '-4', '-j', 'address', 'show', 'dev', wan, capture=True).stdout)
    local_ips = {a['local'] for link in addresses for a in link.get('addr_info', []) if a.get('scope') == 'global'}
    public_ip, public_port = ep.rsplit(':', 1)
    listen_ip = public_ip if public_ip in local_ips else outgoing[0].get('prefsrc')
    if listen_ip not in local_ips:
        raise ValueError('Cannot determine the selected WAN IPv4 source address.')
    if run('ip', 'link', 'show', 'cwg0', capture=True, check=False).returncode == 0:
        raise ValueError('cwg0 is already in use.')
    for family, network in (('-4', '10.77.0.0/24'), ('-6', 'fd77:77:77::/64')):
        for route in json.loads(run('ip', family, '-j', 'route', 'show', 'table', 'all', capture=True).stdout):
            dest = route.get('dst', 'default')
            if dest != 'default' and ipaddress.ip_network(dest, strict=False).overlaps(ipaddress.ip_network(network)):
                raise ValueError(f'Existing route overlaps CWG tunnel network: {network}')
    for port in (51820, int(ep.rsplit(':', 1)[1])):
        if run('ss', '-H', '-lun', f'sport = :{port}', capture=True).stdout.strip():
            raise ValueError(f'UDP port {port} is already in use.')
    for family, name in (('ip', 'cwg_nat'), ('inet', 'cwg_guard')):
        if run('nft', 'list', 'table', family, name, capture=True, check=False).returncode == 0:
            raise ValueError('Reserved CWG firewall table already exists.')
    rules, settings, guard = network_files(wan, int(ep.rsplit(':', 1)[1]))
    settings += socket_buffer_settings()
    applied = dict(line.split('=', 1) for line in settings.splitlines())
    before = {k: run('sysctl', '-n', k, capture=True).stdout.strip() for k in applied}
    state = dict(schema=1, status='installing', endpoint=ep, wan=wan, listen_ip=listen_ip, binary_sha256=digest(release / executable),
                 files={}, sysctl_before=before, sysctl_applied=applied)
    ROOT.mkdir(mode=0o700)
    save(state)
    try:
        write_owned(state, BINARY, (release / executable).read_bytes(), 0o755)
        write_owned(state, ADMIN, Path(__file__).read_bytes(), 0o755)
        write_owned(state, USERS, (release / 'deploy/linux/users.py').read_bytes(), 0o755)
        version = run(str(BINARY), 'version', capture=True).stdout.strip()
        match = re.fullmatch(r'cwg (\d+)\.(\d+)\.(\d+)(?:-[\w.]+)?', version)
        if not match or tuple(map(int, match.groups())) < (0, 6, 0):
            raise ValueError('This installer requires CWG 0.6.0 or newer.')
        generated = ROOT / 'generated'
        run(str(BINARY), 'init', '-out', str(generated), '-endpoint', ep)
        for path in generated.iterdir():
            path.chmod(0o600)
            state['files'][str(path)] = digest(path)
        save(state)
        for name in ('server.json', 'client.json'):
            path = generated / name
            cfg = json.loads(path.read_text())
            cfg.update(transport='exchange', exchange_queue_ms=60, exchange_burst_ms=350,
                       exchange_queue_packets=4096, exchange_reorder_ms=2, exchange_repair=True, exchange_shared=True)
            if name == 'client.json':
                cfg.update(exchange_pps=200, exchange_max_pps=8000)
            else:
                # A wildcard UDP socket can reply from a different interface's
                # address. Pin the WAN source, including behind provider NAT.
                cfg['listen'] = f'{listen_ip}:{public_port}'
            path.write_text(json.dumps(cfg, indent=2) + '\n')
            state['files'][str(path)] = digest(path)
            save(state)
            run(str(BINARY), 'check', '-config', str(path))
        write_owned(state, ROOT / 'server.json', (generated / 'server.json').read_bytes())
        write_owned(state, WG, (generated / 'cwg0.conf').read_bytes())
        import importlib.util
        module_spec = importlib.util.spec_from_file_location('cwg_users', USERS)
        users = importlib.util.module_from_spec(module_spec)
        module_spec.loader.exec_module(users)
        users.initialize(SimpleNamespace(WG=WG, run=run, save=save, digest=digest), state)
        write_owned(state, ROOT / 'guard.nft', rules)
        run('nft', '--check', '--file', str(ROOT / 'guard.nft'))
        write_owned(state, SYSCTL, settings, 0o644)
        write_owned(state, UNIT / 'cwg.service', (release / 'deploy/linux/cwg-multi.service').read_bytes(), 0o644)
        write_owned(state, UNIT / 'cwg-guard.service', guard, 0o644)
        write_owned(state, UNIT / 'wg-quick@cwg0.service.d/cwg.conf', '[Unit]\nRequires=cwg-guard.service\nAfter=cwg-guard.service\n', 0o644)
        run('systemctl', 'daemon-reload')
        for key, value in applied.items():
            run('sysctl', '-q', '-w', f'{key}={value}')
        run('systemctl', 'enable', '--now', 'cwg-guard.service')
        run('systemctl', 'enable', '--now', 'wg-quick@cwg0.service', 'cwg.service')
        export_client(state, release, verified)
        verify(state)
        state['status'] = 'ready'
        save(state)
    except BaseException:
        state['status'] = 'failed'
        save(state)
        stop_owned()
        for key, value in reversed(list(before.items())):
            run('sysctl', '-q', '-w', f'{key}={value}', check=False)
        # Do not re-enable forwarding on reboot after a failed installation.
        if str(SYSCTL) in state['files'] and digest(SYSCTL) == state['files'][str(SYSCTL)]:
            SYSCTL.unlink()
            del state['files'][str(SYSCTL)]
            save(state)
        raise
    print('Server services ready. Allow the outer UDP port in any provider firewall and test the exported client.')
    print('Private Windows client export: /etc/cwg/client.zip (copy securely; do not publish).')
    print('Initial account: owner. Add independent users with: python3 /usr/local/lib/cwg/users.py add NAME')


def uninstall():
    if Path('/etc/cwg-telegram/install-state.json').exists():
        raise ValueError('Remove optional Telegram support with telegram_admin.py uninstall before removing CWG.')
    state = read_state()
    if (ROOT / 'user-transaction.json').exists():
        raise ValueError('Recover the pending user operation before uninstalling.')
    # Check every file before stopping services or removing any. Never erase
    # changes made outside this installer or follow a substituted symlink.
    for name, expected in state['files'].items():
        p = Path(name)
        if p.is_symlink() or not p.is_file() or digest(p) != expected:
            raise ValueError(f'Changed managed file; inspect before uninstall: {p}')
    stop_owned()
    for name in state['files']:
        Path(name).unlink()
    STATE.unlink()
    for path in (ROOT / 'generated', ROOT, BINARY.parent, UNIT / 'wg-quick@cwg0.service.d'):
        try:
            path.rmdir()  # Only empty owned directories; never recursive deletion.
        except FileNotFoundError:
            pass  # An interrupted install may not have created this directory.
        except OSError as error:
            if error.errno != errno.ENOTEMPTY:
                raise
            print(f'Preserved directory containing additional files: {path}')
    run('systemctl', 'daemon-reload')
    if state.get('credential_group_created'):
        import grp
        try:
            group = grp.getgrnam('cwg-secrets')
            if not group.gr_mem:
                run('groupdel', 'cwg-secrets')
            else:
                print('Preserved CWG credential group because external members were added.')
        except KeyError:
            pass
    print('Owned CWG files, services and tables removed. Dependency packages remain.')
    print('Live networking settings remain in case another service now needs them; their CWG persistence file was removed.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    setup = sub.add_parser('install', help='install or verify an identical managed installation')
    setup.add_argument('--release', required=True)
    setup.add_argument('--endpoint', required=True)
    setup.add_argument('--wan', required=True)
    setup.add_argument('--telegram', choices=('ask', 'enabled', 'disabled'), default='ask')
    setup.add_argument('--telegram-token-file', type=Path)
    setup.add_argument('--telegram-owner', type=int)
    for command in ('verify', 'uninstall', 'remove-tables'):
        sub.add_parser(command)
    args = parser.parse_args()
    if platform.system() != 'Linux' or os.geteuid() != 0:
        raise ValueError('Run this command as root.')
    os.umask(0o077)
    if args.command != 'remove-tables':
        import fcntl
        lock = open('/run/lock/cwg-install.lock', 'a')
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('Another CWG management operation is running.') from None
    if args.command == 'install':
        install(args)
        # The VPN transaction is complete before optional management begins.
        # Release its lock before starting a bot which can provision accounts.
        lock.close()
        source = Path(args.release).resolve() / 'deploy/linux/telegram_admin.py'
        if source.is_file():
            command = ['python3', str(source), 'configure', '--mode', args.telegram]
            if args.telegram_token_file is not None:
                command += ['--token-file', str(args.telegram_token_file)]
            if args.telegram_owner is not None:
                command += ['--owner-id', str(args.telegram_owner)]
            result = run(*command, check=False)
            if result.returncode:
                raise ValueError('CWG is installed; optional Telegram setup did not complete. Run telegram_admin.py configure to retry.')
        elif args.telegram == 'enabled':
            raise ValueError('CWG is installed; Telegram setup files are missing from the release.')
    elif args.command == 'verify':
        state = read_state()
        if state['status'] != 'ready':
            raise ValueError('Installation is incomplete.')
        verify(state)
        print('Managed files, services, firewall tables, forwarding and native interface verified.')
    elif args.command == 'uninstall':
        uninstall()
    else:
        remove_tables()


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError) as e:
        print(f'CWG setup failed: {e}', file=sys.stderr)
        sys.exit(1)
