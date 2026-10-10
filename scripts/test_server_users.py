"""Account isolation and bounded management plans; runtime tests run on Debian."""
import base64
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile

spec = importlib.util.spec_from_file_location('cwg_users', Path(__file__).resolve().parents[1] / 'deploy/linux/users.py')
users = importlib.util.module_from_spec(spec)
spec.loader.exec_module(users)


class Users(unittest.TestCase):
    def fixture(self, root):
        generated = root / 'generated'
        generated.mkdir()
        client = dict(mode='client', key='OWNER-OUTER', listen='127.0.0.1:51821', endpoint='192.0.2.10:443')
        server = dict(mode='server', key='OWNER-OUTER', listen='192.0.2.10:443', transport='exchange')
        (generated / 'client.json').write_text(json.dumps(client))
        (generated / 'server.json').write_text(json.dumps(server))
        native = '[Interface]\nPrivateKey = OWNER-PRIVATE\nAddress = 10.77.0.2/32, fd77:77:77::2/128\n\n[Peer]\nPublicKey = SERVER-PUBLIC\nPresharedKey = OWNER-PSK\n'
        for name in ('cwg-test.conf', 'cwg-full.conf'):
            (generated / name).write_text(native)
        wg = root / 'cwg0.conf'
        wg.write_text('[Interface]\nPrivateKey = SERVER-PRIVATE\nAddress = 10.77.0.1/24\nMTU = 1280\n\n[Peer]\nPublicKey = OWNER-PUBLIC\nPresharedKey = OWNER-PSK\nAllowedIPs = 10.77.0.2/32\n')
        with zipfile.ZipFile(root / 'client.zip', 'w') as z:
            z.writestr('cwg-client/config/client.json', json.dumps(client))
            z.writestr('cwg-client/config/accidental-extra.conf', 'NEVER-EXPORT')
            z.writestr('cwg-client/unexpected-secret.txt', 'NEVER-EXPORT')
            z.writestr('cwg-client/scripts/Open-AppClient.ps1', 'public script')
            z.writestr('cwg-client/bin/cwg-windows-amd64.exe', 'public binary')
        owner = dict(enabled=True, slot=2, public_key='OWNER-PUBLIC', preshared_key='OWNER-PSK', config=server, export=str(root / 'client.zip'))
        state = dict(mode='multi', status='ready', listen_ip='192.0.2.10', endpoint='192.0.2.10:443', revision=1, users={'owner': owner}, files={})
        return state, SimpleNamespace(WG=wg)

    def test_distinct_bundle_and_private_key_boundary(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state, setup = self.fixture(root)
            with patch.object(users, 'ROOT', root):
                data = users.bundle(state, 3, 'BOB-OUTER', 'BOB-PRIVATE', 'BOB-PSK')
            with zipfile.ZipFile(io.BytesIO(data)) as z:
                combined = b''.join(z.read(name) for name in z.namelist())
                for secret in (b'OWNER-OUTER', b'OWNER-PRIVATE', b'OWNER-PSK', b'SERVER-PRIVATE', b'NEVER-EXPORT'):
                    self.assertNotIn(secret, combined)
                self.assertIn(b'BOB-OUTER', combined)
                self.assertIn(b'BOB-PRIVATE', combined)
                self.assertIn(b'10.77.0.3/32', combined)
                self.assertIn(b'fd77:77:77::3/128', combined)
                self.assertEqual(len([n for n in z.namelist() if '/config/' in n]), 3)

    def test_add_revoke_preserves_other_keys_and_bounds_active_users(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state, setup = self.fixture(root)
            original = json.dumps(state, sort_keys=True)
            def command(*args, **kwargs):
                return b'BOB-PRIVATE\n' if args[1] == 'genkey' else b'BOB-PUBLIC\n'
            with patch.object(users, 'ROOT', root), patch.object(users, 'command', command):
                plan = users.plan_change(setup, state, 'add', 'bob')
                self.assertEqual(json.dumps(state, sort_keys=True), original)
                next_state = plan['state']
                self.assertEqual(next_state['users']['owner'], state['users']['owner'])
                self.assertEqual(next_state['users']['bob']['slot'], 3)
                self.assertNotEqual(next_state['users']['bob']['config']['key'], 'OWNER-OUTER')
                native = base64.b64decode(plan['files'][str(setup.WG)])
                self.assertIn(b'OWNER-PUBLIC', native)
                self.assertIn(b'BOB-PUBLIC', native)
                revoked = users.plan_change(setup, next_state, 'revoke', 'bob')['state']
                self.assertEqual(revoked['users']['owner'], state['users']['owner'])
                self.assertEqual([u['name'] for u in users.server_config(revoked)['users']], ['owner'])
                self.assertNotIn(b'BOB-PUBLIC', users.native_config(revoked, setup.WG.read_text()))
                third = users.plan_change(setup, next_state, 'add', 'charlie')['state']
                self.assertEqual(third['users']['charlie']['slot'], 4)
                with self.assertRaisesRegex(ValueError, 'three'):
                    users.plan_change(setup, third, 'add', 'fourth')
                with self.assertRaisesRegex(ValueError, 'already exists'):
                    users.plan_change(setup, revoked, 'add', 'bob')

    def test_names_and_empty_revocation(self):
        for name in ('../x', 'x;reboot', 'UPPER', '1user', 'a' * 33, 'a/b', ''):
            with self.assertRaises(ValueError):
                users.username(name)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state, setup = self.fixture(root)
            with patch.object(users, 'ROOT', root):
                plan = users.plan_change(setup, state, 'revoke', 'owner')
            self.assertEqual(users.server_config(plan['state'])['users'], [])
            self.assertNotIn(b'[Peer]', base64.b64decode(plan['files'][str(setup.WG)]))

    def test_runtime_operation_touches_only_target_peer(self):
        plan = {'action': 'add', 'name': 'bob', 'state': {'users': {
            'bob': {'slot': 3, 'public_key': 'BOB-PUBLIC', 'preshared_key': 'BOB-PSK'},
            'owner': {'slot': 2, 'public_key': 'OWNER-PUBLIC', 'preshared_key': 'OWNER-PSK'}}}}
        with patch.object(users, 'command') as command:
            users.apply_peer_change(plan)
            args, kwargs = command.call_args
            self.assertEqual(args, ('wg', 'addconf', 'cwg0', '/dev/stdin'))
            self.assertIn(b'BOB-PSK', kwargs['data'])
            self.assertNotIn(b'OWNER-', kwargs['data'])
            plan['action'] = 'revoke'
            users.apply_peer_change(plan)
            command.assert_called_with('wg', 'set', 'cwg0', 'peer', 'BOB-PUBLIC', 'remove')


if __name__ == '__main__':
    unittest.main()
