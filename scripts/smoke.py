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
import time


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
    pace = int(sys.argv[3]) if len(sys.argv) > 3 else 0
    transport = sys.argv[4] if len(sys.argv) > 4 else 'raw'
    reorder_ms = int(sys.argv[5]) if len(sys.argv) > 5 else 0
    repair = bool(int(sys.argv[6])) if len(sys.argv) > 6 else False
    fec = bool(int(sys.argv[7])) if len(sys.argv) > 7 else False
    assert transport in ('raw', 'exchange') and not (pace and transport == 'exchange')
    assert 0 <= reorder_ms <= 5 and (not reorder_ms or transport == 'exchange')
    assert not repair or transport == 'exchange'
    assert not fec or (transport == 'exchange' and not repair)
    sizes = (32, 148, 1280, 1312 if transport == 'exchange' else 1364)
    processes = []
    stderr_files = []
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
                cfg['transport'] = transport
                if transport == 'exchange':
                    cfg['exchange_pps'] = 100
                    if reorder_ms or repair or fec:
                        cfg['exchange_reorder_ms'] = reorder_ms
                        cfg['exchange_repair'] = repair
                        cfg['exchange_fec'] = fec
                        # Preserve the fixed-rate smoke's 500 ms burst budget
                        # while exercising the adaptive-only client receiver.
                        cfg['exchange_queue_ms'] = 500
                        if name == 'client': cfg['exchange_max_pps'] = 9000
                if pace:
                    cfg['pace_kbps'], cfg['pace_queue_ms'] = pace, 150
                (bundle / f'{name}.json').write_text(json.dumps(cfg))
                run(exe, 'check', '-config', str(bundle / f'{name}.json'))
            outer_reservation.close()
            local_reservation.close()
            for mode in ('server', 'client'):
                stderr_file = (root / f'{mode}.stderr').open('w+b')
                stderr_files.append(stderr_file)
                processes.append(subprocess.Popen(
                    [str(exe), mode, '-config', str(bundle / f'{mode}.json'),
                     '-trace', str(root / f'{mode}-packets.jsonl')] + (interface_args if mode == 'client' else []),
                    stdout=subprocess.DEVNULL, stderr=stderr_file,
                    creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0)))
            # Process creation returns before sockets bind. Use an authenticated
            # readiness check before sending our non-retransmitted test payload.
            ready = json.loads(run(exe, 'probe', '-config', str(bundle / 'client.json'), '-count', '2'))
            assert ready['received'] >= 1, 'Server did not become ready'
            target_host, target_port = client_address.rsplit(':', 1)
            for size in sizes:
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
            burst_elapsed = {}
            if pace or transport == 'exchange':
                # Burst traffic must be delivered intact and actually spaced in
                # both directions. These are temporary localhost UDP sockets.
                messages = [i.to_bytes(2, 'big') + secrets.token_bytes(1198) for i in range(10)]
                for direction, source, target, receiver in [
                    ('upload', wg, (target_host, int(target_port)), backend),
                    ('download', backend, peer, wg),
                ]:
                    for message in messages:
                        source.sendto(message, target)
                    first = None
                    for message in messages:
                        got, _ = receiver.recvfrom(65535)
                        now = time.monotonic()
                        if first is None:
                            first = now
                        assert got == message, f'{direction} burst changed, reordered or lost data'
                    elapsed = time.monotonic() - first
                    ceiling = 9000 if reorder_ms or repair or fec else 100
                    minimum = (len(messages)-1) * (1200+48) * 8 / (pace*1000) if pace else (len(messages)-1)/ceiling
                    assert elapsed >= minimum * 0.75, f'{direction} pacing not applied: {elapsed}'
                    burst_elapsed[direction] = round(elapsed, 4)
            repair_checked = False
            if repair:
                # Synthetic native data header exercises the exact executable's
                # repair path. This UDP fixture has no WireGuard replay filter,
                # so it must observe exactly two identical native datagrams.
                # Real WireGuard duplicate rejection is covered by Go tests.
                for direction, source, target, receiver in [
                    ('upload', wg, (target_host, int(target_port)), backend),
                    ('download', backend, peer, wg),
                ]:
                    packet = b'\x04\x00\x00\x00' + secrets.token_bytes(1084)
                    source.sendto(packet, target)
                    assert receiver.recvfrom(65535)[0] == packet, f'{direction} repair original missing'
                    assert receiver.recvfrom(65535)[0] == packet, f'{direction} backup missing or changed'
                    receiver.settimeout(.1)
                    try:
                        receiver.recvfrom(65535)
                        raise AssertionError(f'{direction} emitted more than one backup')
                    except socket.timeout:
                        pass
                    finally:
                        receiver.settimeout(5)
                repair_checked = True
            if fec:
                for source, target, receiver in [(wg,(target_host,int(target_port)),backend),(backend,peer,wg)]:
                    packet = b'\x04\x00\x00\x00' + secrets.token_bytes(1084)
                    source.sendto(packet,target)
                    assert receiver.recvfrom(65535)[0] == packet, 'FEC changed native data'
                    receiver.settimeout(.1)
                    try:
                        receiver.recvfrom(65535)
                        raise AssertionError('Parity or reconstructed duplicate escaped FEC decoder')
                    except socket.timeout:
                        pass
                    finally:
                        receiver.settimeout(5)
                # Adaptive configurations report every five seconds. Wait for
                # actual evidence, not a sleep shorter than that interval.
                deadline = time.monotonic() + 8
                while True:
                    observed = []
                    for mode in ('server', 'client'):
                        complete = (root / f'{mode}.stderr').read_bytes().split(b'\n')[:-1]
                        samples = [json.loads(line) for line in complete if line.startswith(b'{')]
                        observed.append(any(row.get('exchange_fec_data', 0) > 0 and
                                            row.get('exchange_fec_parity', 0) > 0 for row in samples))
                    if all(observed):
                        break
                    assert all(p.poll() is None for p in processes), 'Transport stopped before FEC counters'
                    assert time.monotonic() < deadline, 'Exact executable did not exercise FEC parity in both directions'
                    time.sleep(.05)
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
            def read_trace(mode):
                # The peers are still writing. Ignore an unfinished final line;
                # compare a fixed client snapshot with a draining server trace.
                complete = (root / f'{mode}-packets.jsonl').read_bytes().split(b'\n')[:-1]
                return [json.loads(line) for line in complete if line]
            traces = {'client': read_trace('client')}
            def hashes(mode, event, field, value):
                return {r['sha256'] for r in traces[mode] if r['event'] == event and r.get(field) == value}
            deadline = time.monotonic() + 2
            while True:
                traces['server'] = read_trace('server')
                unmatched = {
                    'Outer upload': hashes('client', 'write', 'remote', server_address) - hashes('server', 'read', 'local', server_address),
                    'Outer download': hashes('client', 'read', 'remote', server_address) - hashes('server', 'write', 'local', server_address),
                    'Native upload': hashes('client', 'read', 'local', client_address) - hashes('server', 'write', 'remote', address(backend)),
                    'Native download': hashes('client', 'write', 'local', client_address) - hashes('server', 'read', 'remote', address(backend)),
                }
                if not any(unmatched.values()):
                    break
                assert time.monotonic() < deadline, 'Trace mismatch: ' + str({k: len(v) for k, v in unmatched.items() if v})
                time.sleep(.01)
            assert hashlib.sha256(original).hexdigest() in hashes('client', 'read', 'local', client_address), 'Missing known test packet in trace'
            print(json.dumps({'exact_executable': str(exe), 'transport': transport, 'roundtrip_sizes': sizes,
                              'probe': result, 'unauthenticated_silence': True,
                              'live_session_preserved': True, 'socket_traces_correlated': True,
                              'pace_kbps': pace, 'burst_seconds': burst_elapsed,
                              'repair_copies_verified': repair_checked,
                              'fec_enabled': fec,
                              'os_routes_changed': False}, indent=2))
            failed = False
        finally:
            for process, stderr_file in zip(processes, stderr_files):
                if process.poll() is None:
                    process.terminate()
                try:
                    process.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.communicate()
                stderr_file.seek(0)
                stderr = stderr_file.read()
                if failed or process.returncode not in (0, 1, -15, 3221225786):
                    # Diagnostics contain only aggregate counts, not configurations.
                    sys.stderr.write(stderr.decode(errors='replace'))
            for stderr_file in stderr_files:
                stderr_file.close()
            for s in sockets:
                s.close()


if __name__ == '__main__':
    main()
