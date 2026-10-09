"""Temporary diagnostic service reachable only inside the WireGuard test tunnel.

Serves a fixed synthetic payload; no filesystem access or arbitrary uploads.
The VPN's public transport remains UDP. HTTP is only application traffic inside it.
"""
import argparse
import hashlib
import http.server
import json
import time

PAYLOAD = bytes(range(256)) * 2048
DIGEST = hashlib.sha256(PAYLOAD).hexdigest()
PACE_BYTES = 1024
PACE_SECONDS = 1 / 16  # 16 KiB/s; 512 KiB takes about 32 seconds.


class Handler(http.server.BaseHTTPRequestHandler):
    server_version = 'CWGTransferDiagnostic/1'
    sys_version = ''

    def setup(self):
        super().setup()
        self.connection.settimeout(20)

    def log_message(self, format, *args):
        pass  # No request headers, URLs, or user data in logs.

    def headers_for(self, size, content_type='application/octet-stream'):
        self.send_response(200)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(size))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Connection', 'close')
        self.end_headers()

    def do_GET(self):
        if self.path not in ('/download', '/download-paced'):
            self.send_error(404)
            return
        self.headers_for(len(PAYLOAD))
        try:
            if self.path == '/download':
                self.wfile.write(PAYLOAD)
            else:
                for offset in range(0, len(PAYLOAD), PACE_BYTES):
                    self.wfile.write(PAYLOAD[offset:offset + PACE_BYTES])
                    self.wfile.flush()
                    time.sleep(PACE_SECONDS)
        except (ConnectionError, TimeoutError):
            pass

    def do_POST(self):
        if self.path != '/upload':
            self.send_error(404)
            return
        if self.headers.get('Transfer-Encoding') is not None or self.headers.get('Content-Length') != str(len(PAYLOAD)):
            self.send_error(400)
            return
        try:
            received = self.rfile.read(len(PAYLOAD))
            if len(received) != len(PAYLOAD):
                self.send_error(400)
                return
            response = json.dumps({'received_bytes': len(received), 'sha256': hashlib.sha256(received).hexdigest()}).encode()
            self.headers_for(len(response), 'application/json')
            self.wfile.write(response)
        except (ConnectionError, TimeoutError):
            pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bind', choices=['10.77.0.1', '127.0.0.1'], default='10.77.0.1')
    parser.add_argument('--port', type=int, default=18080)
    args = parser.parse_args()
    with http.server.HTTPServer((args.bind, args.port), Handler) as server:
        print('Fixed 512 KiB diagnostic ready on', args.bind, args.port, flush=True)
        server.serve_forever()


if __name__ == '__main__':
    main()
