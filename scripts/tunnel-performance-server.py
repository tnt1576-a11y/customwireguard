"""Bounded synthetic transfer endpoint inside WireGuard, never a public listener.
Application HTTP runs inside the encrypted UDP tunnel. No files are served or
uploaded data stored. Run under a time-limited service for user-invoked tests.
"""
import argparse, hashlib, http.server, json, socket, threading, time
from urllib.parse import urlsplit, parse_qs

BLOCK=bytes(range(256))*64
MAX_BYTES=64*1024*1024

class Server(http.server.ThreadingHTTPServer):
    daemon_threads=True
    request_queue_size=4
    def __init__(self,*args,**kwargs):
        self.slots=threading.BoundedSemaphore(4)
        super().__init__(*args,**kwargs)
    def process_request(self,request,address):
        if not self.slots.acquire(blocking=False):self.shutdown_request(request);return
        try:super().process_request(request,address)
        except BaseException:self.slots.release();raise
    def process_request_thread(self,request,address):
        try:super().process_request_thread(request,address)
        finally:self.slots.release()

class Handler(http.server.BaseHTTPRequestHandler):
    server_version='CWGPerformance/1';sys_version=''
    def setup(self):
        super().setup();self.connection.settimeout(15)
        self.connection.setsockopt(socket.IPPROTO_TCP,socket.TCP_NODELAY,1)
    def log_message(self,*args):pass
    def headers_for(self,size,kind='application/octet-stream'):
        self.send_response(200);self.send_header('Content-Length',str(size));self.send_header('Content-Type',kind)
        self.send_header('Cache-Control','no-store');self.send_header('Connection','close');self.end_headers()
    def do_GET(self):
        try:
            parsed=urlsplit(self.path);q=parse_qs(parsed.query,strict_parsing=True)
            if parsed.path!='/download' or set(q)!={'bytes','bps'} or any(len(v)!=1 for v in q.values()):raise ValueError()
            count=int(q['bytes'][0]);rate=int(q['bps'][0])
            if not 1<=count<=MAX_BYTES or rate not in (1250000,3125000,6250000):raise ValueError()
        except ValueError:self.send_error(400);return
        try:
            self.headers_for(count);sent=0;started=time.monotonic()
            while sent<count:
                block=BLOCK[:min(len(BLOCK),count-sent)];self.wfile.write(block);sent+=len(block)
                delay=started+sent/rate-time.monotonic()
                if delay>0:time.sleep(delay)
        except (ConnectionError,TimeoutError):pass
    def do_POST(self):
        try:
            if self.path!='/upload' or self.headers.get('Transfer-Encoding') is not None:raise ValueError()
            if len(self.headers.get_all('Content-Length',[]))!=1:raise ValueError()
            count=int(self.headers['Content-Length'])
            if not 1<=count<=MAX_BYTES:raise ValueError()
        except (ValueError,TypeError):self.send_error(400);return
        try:
            digest=hashlib.sha256();received=0
            while received<count:
                data=self.rfile.read(min(65536,count-received))
                if not data:return
                received+=len(data);digest.update(data)
            data=json.dumps({'received_bytes':received,'sha256':digest.hexdigest()}).encode()
            self.headers_for(len(data),'application/json');self.wfile.write(data)
        except (ConnectionError,TimeoutError):pass

def main():
    p=argparse.ArgumentParser();p.add_argument('--bind',choices=['10.77.0.1','127.0.0.1'],default='10.77.0.1');p.add_argument('--port',type=int,default=18081)
    args=p.parse_args()
    with Server((args.bind,args.port),Handler) as server:
        print('Bounded synthetic performance endpoint ready',flush=True);server.serve_forever()
if __name__=='__main__':main()
