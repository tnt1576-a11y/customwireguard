"""Portable installer guards; real service/NAT validation also needs Linux."""
import argparse
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('cwg_server_setup', Path(__file__).resolve().parents[1] / 'deploy/linux/setup.py')
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)


class InstallerGuards(unittest.TestCase):
    def test_endpoint_and_interface_reject_command_or_rule_injection(self):
        self.assertEqual(setup.endpoint('192.0.2.10'), '192.0.2.10:443')
        self.assertEqual(setup.endpoint('192.0.2.10:8443'), '192.0.2.10:8443')
        for value in ('127.0.0.1', '0.0.0.0', '224.0.0.1', 'x;reboot', '192.0.2.1:0', '192.0.2.1:65536', '192.0.2.1:51820', '[::1]:443'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                setup.endpoint(value)
        self.assertEqual(setup.interface('ens3'), 'ens3')
        for value in ('eth0";drop', 'lo', 'cwg0', '../eth0', 'eth0.foo', 'x' * 16):
            with self.subTest(value=value), self.assertRaises(ValueError):
                setup.interface(value)

    def test_cleanup_paths_cannot_escape_owned_files(self):
        for name in ('/etc/cwg/server.json', '/etc/cwg/generated/client.json', '/usr/local/lib/cwg/cwg', '/etc/wireguard/cwg0.conf'):
            self.assertTrue(setup.owned_path(name), name)
        for name in ('/etc/shadow', '/etc/cwg/../shadow', '/etc/wireguard/other.conf', '/etc/cwg-other/x', 'etc/cwg/x', '/etc/systemd/system/ssh.service', '/etc/cwg'):
            self.assertFalse(setup.owned_path(name), name)

    def test_network_rules_own_tables_only_and_reject_ipv6_bypass(self):
        rules, settings, unit = setup.network_files('eth0', 443)
        self.assertNotIn('flush ruleset', rules)
        self.assertIn('iifname != "lo" udp dport 51820 drop', rules)
        self.assertIn('iifname "cwg0" meta nfproto ipv6 reject', rules)
        self.assertIn('ip saddr 10.77.0.0/24 oifname "eth0" masquerade', rules)
        self.assertLess(settings.index('accept_ra=2'), settings.index('all.forwarding=1'))
        self.assertIn('server-admin.py remove-tables', unit)

    def test_release_corruption_and_path_traversal_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            names = ['bin/cwg-linux-amd64', 'bin/cwg-windows-amd64.exe', 'deploy/linux/cwg.service', 'THIRD-PARTY-LICENSES/Go.txt']
            names += ['scripts/' + name for name in setup.SCRIPTS]
            entries = []
            for name in names:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(name.encode())
                entries.append(f'{setup.digest(path)}  {name}')
            sums = root / 'SHA256SUMS'
            sums.write_text('\n'.join(entries))
            with patch.object(setup.platform, 'machine', return_value='x86_64'):
                self.assertEqual(setup.release_files(root)[1], 'bin/cwg-linux-amd64')
                (root / names[0]).write_bytes(b'changed')
                with self.assertRaisesRegex(ValueError, 'integrity'):
                    setup.release_files(root)
                sums.write_text('\n'.join(entries) + '\n' + '0' * 64 + '  ../outside')
                with self.assertRaisesRegex(ValueError, 'checksum entry'):
                    setup.release_files(root)

    def test_failed_tunnel_stop_keeps_backend_firewall(self):
        for failure in ('interface', 'process'):
            calls = []

            def command(*args, **kwargs):
                calls.append(args)
                active = (failure == 'interface' and args[:3] == ('ip', 'link', 'show')) or (failure == 'process' and args[:2] == ('systemctl', 'is-active'))
                return subprocess.CompletedProcess(args, 0 if active else 1, '', '')

            with patch.object(setup, 'run', command), self.assertRaisesRegex(ValueError, 'retained'):
                setup.stop_owned()
            self.assertFalse(any('nft' in c or 'cwg-guard.service' in c for c in calls))

    def test_unchanged_reinstall_preserves_existing_keys(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'binary'
            binary.write_bytes(b'verified release fixture')
            state_path = root / 'state.json'
            state_path.write_text('{}')
            keys = root / 'keys'
            keys.write_bytes(b'existing-key-fixture')
            state = dict(status='ready', endpoint='192.0.2.10:443', wan='eth0', binary_sha256=setup.digest(binary))
            args = argparse.Namespace(endpoint='192.0.2.10', wan='eth0', release=str(root))
            with patch.object(setup, 'STATE', state_path), patch.object(setup, 'release_files', return_value=(root, 'binary', [])), patch.object(setup, 'read_state', return_value=state), patch.object(setup, 'verify') as verify, patch.object(setup, 'run') as run:
                setup.install(args)
                verify.assert_called_once_with(state)
                run.assert_not_called()
                self.assertEqual(keys.read_bytes(), b'existing-key-fixture')
                args.endpoint = '192.0.2.11'
                with self.assertRaisesRegex(ValueError, 'differs'):
                    setup.install(args)
                run.assert_not_called()

    def test_uninstall_preserves_unrelated_files_and_handles_missing_directories(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            root = base / 'etc-cwg'
            root.mkdir()
            state_path = root / 'install-state.json'
            state_path.write_text('{}')
            owned = root / 'server.json'
            owned.write_bytes(b'owned fixture')
            additional = root / 'keep.txt'
            additional.write_bytes(b'unrelated fixture')
            binary = base / 'lib-cwg' / 'cwg'
            unit = base / 'units'
            state = {'files': {str(owned): setup.digest(owned)}}
            with patch.object(setup, 'ROOT', root), patch.object(setup, 'STATE', state_path), patch.object(setup, 'BINARY', binary), patch.object(setup, 'UNIT', unit), patch.object(setup, 'read_state', return_value=state), patch.object(setup, 'stop_owned'), patch.object(setup, 'run'):
                setup.uninstall()
            self.assertFalse(owned.exists())
            self.assertFalse(state_path.exists())
            self.assertEqual(additional.read_bytes(), b'unrelated fixture')


if __name__ == '__main__':
    unittest.main()
