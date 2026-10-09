"""Exact-binary, localhost-only HTTP/3 smoke test. No routes, TUNs, or real keys.

Usage: python scripts/smoke-h3.py PATH_TO_CWG
Creates a temporary TLS identity and mock UDP WireGuard endpoints. Exercises
the actual binary, maximum datagram size, authentication, and server restart.
"""
import base64
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time

binary = str(Path(sys.argv[1]).resolve())
flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0

def sock():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(("127.0.0.1", 0))
    s.settimeout(4)
    return s

def addr(s):
    return f"127.0.0.1:{s.getsockname()[1]}"

with tempfile.TemporaryDirectory(prefix="cwg-h3-local-") as directory:
    root = Path(directory)
    with sock() as backend, sock() as native, sock() as public, sock() as listener:
        endpoint, local = addr(public), addr(listener)
        public.close()
        listener.close()
        subprocess.run([binary, "tls-init", "-endpoint", endpoint, "-out", str(root / "tls")],
                       check=True, stdout=subprocess.DEVNULL, creationflags=flags)
        common = {"transport": "h3", "profile": "balanced", "key": base64.b64encode(os.urandom(32)).decode(),
                  "tls_cert": str(root / "tls/server-cert.pem")}
        server = dict(common, mode="server", listen=endpoint, wireguard=addr(backend), tls_key=str(root / "tls/server-key.pem"))
        client = dict(common, mode="client", listen=local, wireguard=addr(native), endpoint=endpoint, h3_target=addr(backend))
        for name, cfg in (("server", server), ("client", client)):
            (root / f"{name}.json").write_text(json.dumps(cfg), encoding="utf-8")
        processes, logs = [], []

        def launch(mode):
            log = open(root / f"{mode}-{len(logs)}.log", "wb")
            logs.append(log)
            p = subprocess.Popen([binary, mode, "-config", str(root / f"{mode}.json")],
                                 stdout=subprocess.DEVNULL, stderr=log, creationflags=flags)
            processes.append(p)
            return p

        def stop(p):
            if p.poll() is None:
                p.terminate()
                try:
                    p.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    p.kill()
                    p.wait(timeout=5)

        def wait_ready(count):
            # QUIC's idle timer can restart on the first ack-eliciting packet
            # sent after the last receive: 45s idle + up to 15s keepalive.
            deadline = time.monotonic() + 80
            while time.monotonic() < deadline:
                if cp.poll() is not None:
                    raise RuntimeError("client stopped unexpectedly")
                content = Path(logs[1].name).read_text(errors="replace")
                if content.count("CONNECT-UDP established;") >= count:
                    return
                time.sleep(.1)
            # These logs contain only temporary localhost identities.
            for log in logs:
                print(Path(log.name).read_text(errors="replace"), file=sys.stderr)
            raise RuntimeError("HTTP/3 setup/reconnection timed out")

        def exchange(size):
            payload = os.urandom(size)
            native.sendto(payload, ("127.0.0.1", int(local.split(":")[1])))
            data, peer = backend.recvfrom(65535)
            assert data == payload, "outbound payload changed"
            backend.sendto(data, peer)
            data, _ = native.recvfrom(65535)
            assert data == payload, "return payload changed"

        try:
            sp = launch("server")
            result = subprocess.run([binary, "probe", "-config", str(root / "client.json"), "-count", "2"],
                                    capture_output=True, timeout=25, creationflags=flags)
            assert result.returncode == 0, "pinned HTTP/3 preflight failed"
            assert json.loads(result.stdout)["authenticated_responses"] == 2
            cp = launch("client")
            wait_ready(1)
            for size in (32, 92, 148, 512, 1120):
                exchange(size)
            native.sendto(os.urandom(1121), ("127.0.0.1", int(local.split(":")[1])))
            backend.settimeout(.2)
            try:
                backend.recvfrom(65535)
                raise AssertionError("oversize datagram forwarded")
            except socket.timeout:
                pass
            backend.settimeout(4)
            exchange(148)
            stop(sp)
            sp = launch("server")
            wait_ready(2)
            exchange(1120)
            print("PASS: exact binary; pinned HTTP/3; 32..1120 byte bidirectional UDP; oversize drop/recovery; automatic reconnect after server restart. Localhost only.")
        finally:
            for p in reversed(processes):
                stop(p)
            for log in logs:
                log.close()
