"""Exercise the exact shipped executable over localhost UDP. No OS VPN changes."""
import json
import hashlib
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import tempfile


def udp():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(('127.0.0.1', 0))
    s.settimeout(5)
    return s


def address(s):
    host, port = s.getsockname()
    return f'{host}:{port}'


def run(exe, *args):
    result = subprocess.run([str(exe), *args], capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise RuntimeError(f'Executable command failed ({args[0]}): {result.stderr}')
    return result.stdout


def main():
    exe = Path(sys.argv[1]).resolve(strict=True)
    interface_args = ['-interface-index', str(int(sys.argv[2]))] if len(sys.argv) > 2 else []
    processes = []
    sockets = []
    failed = True
    with tempfile.TemporaryDirectory(prefix='cwg-release-smoke-') as temporary:
        root = Path(temporary)
        try:
            backend, wg, outer_reservation, local_reservation = [udp() for _ in range(4)]
            sockets.extend((backend, wg, outer_reservation, local_reservation))
            server_address, client_address = address(outer_reservation), address(local_reservation)
            bundle = root / 'generated'
            run(exe, 'init', '-endpoint', server_address, '-out', str(bundle))
            client = json.loads((bundle / 'client.json').read_text())
            server = json.loads((bundle / 'server.json').read_text())
            client['listen'], client['wireguard'] = client_address, address(wg)
            server['listen'], server['wireguard'] = server_address, address(backend)
            for name, cfg in [('client', client), ('server', server)]:
                (bundle / f'{name}.json').write_text(json.dumps(cfg))
                run(exe, 'check', '-config', str(bundle / f'{name}.json'))
            outer_reservation.close()
            local_reservation.close()
            for mode in ('server', 'client'):
                processes.append(subprocess.Popen(
                    [str(exe), mode, '-config', str(bundle / f'{mode}.json'),
                     '-trace', str(root / f'{mode}-packets.jsonl')] + (interface_args if mode == 'client' else []),
                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                    creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0)))
            # Process creation returns before sockets bind. Use an authenticated
            # readiness check before sending our non-retransmitted test payload.
            ready = json.loads(run(exe, 'probe', '-config', str(bundle / 'client.json'), '-count', '2'))
            assert ready['received'] >= 1, 'Server did not become ready'
            target_host, target_port = client_address.rsplit(':', 1)
            for size in (32, 148, 1280, 1364):
                original = secrets.token_bytes(size)
                wg.sendto(original, (target_host, int(target_port)))
                received, peer = backend.recvfrom(65535)
                assert received == original, f'Outgoing data mismatch at {size}'
                backend.sendto(received, peer)
                echoed, _ = wg.recvfrom(65535)
                assert echoed == original, f'Return data mismatch at {size}'
            result = json.loads(run(exe, 'probe', '-config', str(bundle / 'client.json'), '-count', '4'))
            assert result['received'] == 4, 'One or more probe sizes failed'
            # Probe must not disrupt the running client association.
            original = b'live connection after diagnostic'
            wg.sendto(original, (target_host, int(target_port)))
            assert backend.recvfrom(65535)[0] == original, 'Probe disrupted live association'
            attacker = udp()
            sockets.append(attacker)
            server_host, server_port = server_address.rsplit(':', 1)
            attacker.sendto(secrets.token_bytes(256), (server_host, int(server_port)))
            attacker.settimeout(0.25)
            try:
                attacker.recvfrom(65535)
                raise AssertionError('Unauthenticated packet got a response')
            except socket.timeout:
                pass
            for process in processes:
                assert process.poll() is None, 'Transport stopped unexpectedly'
            traces = {mode: [json.loads(line) for line in (root / f'{mode}-packets.jsonl').read_text().splitlines()]
                      for mode in ('client', 'server')}
            def hashes(mode, event, field, value):
                return {r['sha256'] for r in traces[mode] if r['event'] == event and r.get(field) == value}
            assert hashes('client', 'write', 'remote', server_address) <= hashes('server', 'read', 'local', server_address), 'Outer upload trace mismatch'
            assert hashes('client', 'read', 'remote', server_address) <= hashes('server', 'write', 'local', server_address), 'Outer download trace mismatch'
            assert hashes('client', 'read', 'local', client_address) <= hashes('server', 'write', 'remote', address(backend)), 'Native upload trace mismatch'
            assert hashes('client', 'write', 'local', client_address) <= hashes('server', 'read', 'remote', address(backend)), 'Native download trace mismatch'
            assert hashlib.sha256(original).hexdigest() in hashes('client', 'read', 'local', client_address), 'Missing known test packet in trace'
            print(json.dumps({'exact_executable': str(exe), 'roundtrip_sizes': [32, 148, 1280, 1364],
                              'probe': result, 'unauthenticated_silence': True,
                              'live_session_preserved': True, 'socket_traces_correlated': True,
                              'os_routes_changed': False}, indent=2))
            failed = False
        finally:
            for process in processes:
                if process.poll() is None:
                    process.terminate()
                try:
                    _, stderr = process.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    _, stderr = process.communicate()
                if failed or process.returncode not in (0, 1, -15, 3221225786):
                    # Diagnostics contain only aggregate counts, not configurations.
                    sys.stderr.write(stderr.decode(errors='replace'))
            for s in sockets:
                s.close()


if __name__ == '__main__':
    main()
