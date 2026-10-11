"""Portable Telegram authorization, replay and command-boundary tests."""
import copy
import importlib.util
import json
import io
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def load(name):
    path = Path(__file__).resolve().parents[1] / 'deploy/linux' / (name + '.py')
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


bot = load('telegram_bot')
helper = load('telegram_helper')
admin = load('telegram_admin')
CFG = {'owner_id': 123456, 'enabled_at': 990}


def update(identifier=1, text='/status', date=1000):
    return {'update_id': identifier, 'message': {'date': date, 'text': text,
            'chat': {'id': 123456, 'type': 'private'}, 'from': {'id': 123456, 'is_bot': False}}}


class API:
    def __init__(self, updates):
        self.updates, self.sent = updates, []

    def call(self, method, body):
        if method == 'getUpdates':
            return self.updates
        self.sent.append(body)
        return {}


class TelegramBoundaries(unittest.TestCase):
    def test_only_recent_unforwarded_private_owner_messages(self):
        self.assertEqual(bot.command(update(), CFG, 1000), {'action': 'status'})
        cases = []
        for field, value in [('chat', {'id': 123456, 'type': 'group'}),
                             ('chat', {'id': 999, 'type': 'private'}),
                             ('from', {'id': 999, 'is_bot': False}),
                             ('from', {'id': 123456, 'is_bot': True}),
                             ('from', None), ('chat', None), ('date', 989), ('date', 1006),
                             ('forward_origin', {}), ('text', 'x' * 129)]:
            value_update = update()
            value_update['message'][field] = value
            cases.append(value_update)
        cases += [{'update_id': 2, 'edited_message': update()['message']}, update(date=939)]
        for value in cases:
            self.assertIsNone(bot.command(value, CFG, 1000), value)

    def test_no_shell_or_arbitrary_paths(self):
        for text in ['/restart; reboot', '/revoke ../owner', '/add x\n/restart', '/add --help', '/revoke owner extra']:
            self.assertEqual(bot.command(update(text=text), CFG, 1000), {'action': 'help'})
        for value in [{'action': 'shell', 'command': 'id'}, {'action': 'restart', 'unit': 'ssh'},
                      {'action': 'revoke', 'name': '../owner'}, {'action': 'add', 'name': 'a;id'},
                      {'action': 'status', 'path': '/etc/shadow'}]:
            with self.assertRaises(ValueError):
                helper.request(value)
        self.assertEqual(helper.request({'action': 'revoke', 'name': 'alice-2'}), {'action': 'revoke', 'name': 'alice-2'})

    def test_offset_persisted_before_action_and_not_replayed_after_crash(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'offset.json'
            api = API([update(text='/restart')])
            executed = []

            def manager(request):
                self.assertEqual(json.loads(path.read_text())['offset'], 2)
                executed.append(request)
                raise RuntimeError('simulate process interruption after management')

            worker = bot.Bot(CFG, path, api, manager, lambda: 1000)
            with self.assertRaises(RuntimeError):
                worker.poll()
            recovered = bot.Bot(CFG, path, api, manager, lambda: 1005)
            recovered.poll()
            self.assertEqual(executed, [{'action': 'restart'}])
            self.assertEqual(api.sent, [])

    def test_foreign_sender_silent_owner_rate_limited_and_reply_fixed(self):
        with tempfile.TemporaryDirectory() as temp:
            foreign = update(1)
            foreign['message']['from']['id'] = 999
            api = API([foreign, update(2, '/users'), update(3, '/restart')])
            executed = []
            worker = bot.Bot(CFG, Path(temp) / 'offset.json', api,
                             lambda req: executed.append(req) or 'ok', lambda: 1000)
            worker.poll()
            self.assertEqual(executed, [{'action': 'users'}])
            self.assertEqual(api.sent, [{'chat_id': CFG['owner_id'], 'text': 'ok', 'protect_content': True}])
            self.assertEqual(worker.state['offset'], 4)

    def test_invalid_state_does_not_reset_replay_protection(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'offset.json'
            path.write_text('{"offset":-1,"last_action":0}')
            with self.assertRaises(ValueError):
                bot.Bot(CFG, path, API([]))

    def test_api_bounds_and_secret_safe_errors(self):
        class Connection:
            status = 200
            def __init__(self, raw): self.raw, self.closed, self.limit = raw, False, None
            def request(self, *args): pass
            def getresponse(self): return self
            def read(self, limit): self.limit = limit; return self.raw[:limit]
            def close(self): self.closed = True
        for raw in [b'x' * (bot.MAX_RESPONSE + 2), b'{invalid', b'{"ok":false,"description":"secret fake-token"}']:
            connection = Connection(raw)
            api = bot.Telegram('fake-token', lambda: connection)
            with self.assertRaises(bot.APIError) as error:
                api.call('getUpdates', {})
            self.assertNotIn('fake-token', str(error.exception))
            self.assertEqual(connection.limit, bot.MAX_RESPONSE + 1)
            self.assertTrue(connection.closed)
        connection = Connection(b'{"ok":false,"parameters":{"retry_after":999999}}')
        with self.assertRaises(bot.APIError) as error:
            bot.Telegram('fake-token', lambda: connection).call('getUpdates', {})
        self.assertEqual(error.exception.retry, 300)

    def test_helper_only_operates_on_completed_managed_server(self):
        with patch.object(helper, 'private_json', return_value={'mode': 'multi', 'status': 'failed'}), patch.object(helper, 'run') as run:
            with self.assertRaises(ValueError):
                helper.execute({'action': 'restart'})
            run.assert_not_called()
        ready = {'mode': 'multi', 'status': 'ready'}
        with patch.object(helper, 'private_json', return_value=ready), patch.object(helper, 'run', return_value='') as run:
            result = helper.execute({'action': 'add', 'name': 'alice'})
            run.assert_called_once_with('/usr/bin/python3', str(helper.PROGRAM / 'users.py'), 'add', 'alice')
            self.assertIn('/etc/cwg/user-alice.zip', result)
            self.assertNotIn('PrivateKey', result)

    def test_identity_lookup_never_authorizes_or_sends_a_message(self):
        with tempfile.TemporaryDirectory() as temp:
            token = Path(temp) / 'token'
            token.write_text('123456:' + 'A' * 35)
            recent = update(text='/start')
            forwarded = copy.deepcopy(recent)
            forwarded['message']['forward_origin'] = {}
            api = API([recent, forwarded, update(2, '/start', date=800)])
            with patch.object(admin, 'protected'), patch.object(admin.time, 'time', return_value=1000), \
                    patch.object(admin, 'module', return_value=type('Module', (), {'Telegram': lambda token: api})), \
                    patch('sys.stdout', new_callable=io.StringIO) as output:
                admin.identify(token)
            self.assertIn('123456', output.getvalue())
            self.assertIn('no account was authorized', output.getvalue())
            self.assertEqual(api.sent, [])

    def test_enable_refuses_existing_webhook_before_service_changes(self):
        class DedicatedAPI:
            def call(self, method, body):
                return {'is_bot': True} if method == 'getMe' else {'url': 'https://example.invalid/existing'}
        fake = SimpleNamespace(configuration=lambda p: {'token': 'fixture'}, Telegram=lambda token: DedicatedAPI())
        with patch.object(admin, 'verify'), patch.object(admin, 'managed_server'), \
                patch.object(admin, 'module', return_value=fake), patch.object(admin, 'run') as run:
            with self.assertRaises(ValueError):
                admin.enable({'status': 'ready'})
            run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
