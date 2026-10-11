#!/usr/bin/env python3
"""Optional unprivileged, owner-only Telegram control client."""
import http.client
import json
import os
from pathlib import Path
import re
import socket
import ssl
import sys
import tempfile
import time

MAX_RESPONSE = 1 << 20
HELP = 'CWG commands: /status, /users, /add NAME, /revoke NAME, /restart, /help. Private bundles stay on the server.'


class APIError(Exception):
    def __init__(self, retry=5):
        super().__init__('Telegram request failed')
        self.retry = max(5, min(300, retry))


def configuration(path):
    with Path(path).open('rb') as stream:
        data = stream.read(8193)
    if len(data) > 8192:
        raise ValueError('Configuration too large')
    cfg = json.loads(data)
    if not re.fullmatch(r'[0-9]{5,16}:[A-Za-z0-9_-]{30,100}', cfg.get('token', '')):
        raise ValueError('Invalid token format')
    owner = cfg.get('owner_id')
    if type(owner) is not int or not 0 < owner < 1 << 52:
        raise ValueError('A numeric private owner ID is required')
    if type(cfg.get('enabled_at')) is not int or cfg['enabled_at'] <= 0:
        raise ValueError('Installation timestamp is required')
    return cfg


class Telegram:
    def __init__(self, token, connection=None):
        self.token = token
        self.connection = connection or (lambda: http.client.HTTPSConnection(
            'api.telegram.org', timeout=50, context=ssl.create_default_context()))

    def call(self, method, body):
        if method not in ('getUpdates', 'getMe', 'getWebhookInfo', 'sendMessage'):
            raise ValueError('Unsupported API method')
        conn = self.connection()
        try:
            conn.request('POST', '/bot' + self.token + '/' + method,
                         json.dumps(body).encode(), {'Content-Type': 'application/json'})
            response = conn.getresponse()
            raw = response.read(MAX_RESPONSE + 1)
            if len(raw) > MAX_RESPONSE:
                raise APIError()
            value = json.loads(raw)
            if not isinstance(value, dict):
                raise APIError()
            if response.status != 200 or value.get('ok') is not True:
                retry = value.get('parameters', {}).get('retry_after', 5)
                raise APIError(retry if type(retry) is int else 5)
            return value['result']
        except (OSError, ValueError, KeyError, TypeError, http.client.HTTPException):
            # URLs contain the bot token. Never print a network exception or body.
            raise APIError() from None
        finally:
            conn.close()


def atomic_state(path, value):
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix='.offset-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
        if os.name == 'posix':
            directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
    finally:
        Path(name).unlink(missing_ok=True)


def command(update, cfg, now):
    message = update.get('message')
    if not isinstance(message, dict) or 'forward_origin' in message:
        return None
    chat, sender = message.get('chat', {}), message.get('from', {})
    if not isinstance(chat, dict) or not isinstance(sender, dict):
        return None
    if chat.get('type') != 'private' or type(chat.get('id')) is not int or type(sender.get('id')) is not int or chat.get('id') != cfg['owner_id'] or sender.get('id') != cfg['owner_id'] or sender.get('is_bot') is not False:
        return None
    date = message.get('date')
    if type(date) is not int or date < max(cfg['enabled_at'], now - 60) or date > now + 5:
        return None
    text = message.get('text')
    if not isinstance(text, str) or len(text) > 128:
        return None
    if text in ('/start', '/help'):
        return {'action': 'help'}
    if text in ('/status', '/users', '/restart'):
        return {'action': text[1:]}
    match = re.fullmatch(r'/(add|revoke) ([a-z][a-z0-9_-]{0,31})', text)
    if match:
        return {'action': match[1], 'name': match[2]}
    return {'action': 'help'}


def management(request):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
        conn.settimeout(35)
        conn.connect('/run/cwg-telegram-admin.sock')
        conn.sendall(json.dumps(request).encode() + b'\n')
        data = bytearray()
        while b'\n' not in data and len(data) <= 16384:
            chunk = conn.recv(4096)
            if not chunk:
                break
            data.extend(chunk)
        if len(data) > 16384 or not data.endswith(b'\n'):
            raise ValueError('Invalid management response')
        result = json.loads(data)
        if not isinstance(result.get('text'), str) or len(result['text']) > 3500:
            raise ValueError('Invalid management response')
        return result['text']


class Bot:
    def __init__(self, cfg, state_path, api, manager=management, clock=time.time):
        self.cfg, self.path, self.api, self.manager, self.clock = cfg, Path(state_path), api, manager, clock
        self.state = {'offset': 0, 'last_action': 0}
        if self.path.exists():
            with self.path.open('rb') as stream:
                raw = stream.read(4097)
            if len(raw) > 4096:
                raise ValueError('Invalid offset state')
            self.state = json.loads(raw)
            if set(self.state) != {'offset', 'last_action'} or any(type(v) is not int or v < 0 for v in self.state.values()):
                raise ValueError('Invalid offset state')

    def poll(self):
        updates = self.api.call('getUpdates', {'offset': self.state['offset'], 'limit': 20,
                                              'timeout': 40, 'allowed_updates': ['message']})
        if not isinstance(updates, list) or len(updates) > 20:
            raise APIError()
        for update in updates:
            if not isinstance(update, dict) or type(update.get('update_id')) is not int or update['update_id'] < 0:
                continue
            if update['update_id'] < self.state['offset']:
                continue
            now = int(self.clock())
            action = command(update, self.cfg, now)
            limited = now - self.state['last_action'] < 3
            self.state['offset'] = update['update_id'] + 1
            if action and not limited:
                self.state['last_action'] = now
            # Persist BEFORE management. A crash may lose a reply/action, but
            # never deliberately replay a restart or account operation.
            atomic_state(self.path, self.state)
            if not action or limited:
                continue
            if action['action'] == 'help':
                text = HELP
            else:
                try:
                    text = self.manager(action)
                except (OSError, ValueError):
                    text = 'Management reply unavailable. Check server state before retrying; the action may have completed.'
            self.api.call('sendMessage', {'chat_id': self.cfg['owner_id'], 'text': text,
                                          'protect_content': True})


def main():
    if len(sys.argv) != 1:
        raise ValueError('This service takes no arguments')
    credentials = Path(os.environ['CREDENTIALS_DIRECTORY'])
    cfg = configuration(credentials / 'bot.json')
    api = Telegram(cfg['token'])
    bot = Bot(cfg, '/var/lib/cwg-telegram/offset.json', api)
    delay = 5
    while True:
        try:
            bot.poll()
            delay = 5
        except APIError as error:
            print('Telegram unavailable; retrying with bounded backoff.', flush=True)
            time.sleep(max(delay, error.retry))
            delay = min(300, delay * 2)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError):
        print('CWG Telegram stopped: inspect private configuration and service state.', file=sys.stderr)
        sys.exit(1)
