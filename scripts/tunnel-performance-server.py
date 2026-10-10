"""Bounded synthetic transfer endpoint inside WireGuard, never a public listener.
Application HTTP runs inside the encrypted UDP tunnel. No files are served or
uploaded data stored. Run under a time-limited service for user-invoked tests.
"""
import argparse, contextlib, datetime, hashlib, http.server, json, re, socket, struct, sys, threading, time, uuid
from urllib.parse import urlsplit, parse_qs

BLOCK=bytes(range(256))*64
MAX_BYTES=64*1024*1024

def tcp_info(conn):
    # Linux UAPI tcp_info, v6.12 include/uapi/linux/tcp.h. Read only fields
    # present in the returned length; other platforms explicitly report None.
    if not sys.platform.startswith('linux'):return None
    try:
        data=conn.getsockopt(socket.IPPROTO_TCP,socket.TCP_INFO,256)
        fields={}
        for name,offset,kind in [
            ('rto_us',8,'I'),('snd_mss',16,'I'),('unacked',24,'I'),
            ('lost',32,'I'),('retrans',36,'I'),('rtt_us',68,'I'),
            ('snd_ssthresh',76,'I'),('snd_cwnd',80,'I'),('total_retrans',100,'I'),
            ('pacing_bytes_per_s',104,'Q'),('bytes_acked',120,'Q'),
            ('bytes_received',128,'Q'),('notsent_bytes',144,'I'),
            ('delivery_bytes_per_s',160,'Q'),('busy_us',168,'Q'),
            ('rwnd_limited_us',176,'Q'),('sndbuf_limited_us',184,'Q'),
            ('bytes_sent',200,'Q'),('bytes_retrans',208,'Q'),
            ('reordering_events',220,'I'),('rcv_ooo_packets',224,'I'),('snd_wnd',228,'I')]:
            if len(data)>=offset+struct.calcsize('='+kind):
                fields[name]=struct.unpack_from('='+kind,data,offset)[0]
        fields['state']=data[0];fields['congestion_state']=data[1]
        fields['sndbuf_bytes']=conn.getsockopt(socket.SOL_SOCKET,socket.SO_SNDBUF)
        fields['rcvbuf_bytes']=conn.getsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF)
        return fields
    except OSError:return None

def udp_errors():
    if not sys.platform.startswith('linux'):return None
    try:
        with open('/proc/net/snmp') as f:lines=f.read().splitlines()
        for i,line in enumerate(lines[:-1]):
            if line.startswith('Udp:'):
                values=dict(zip(line.split()[1:],map(int,lines[i+1].split()[1:])))
                return {k:values.get(k) for k in ('InErrors','RcvbufErrors','SndbufErrors')}
    except (OSError,ValueError):pass
    return None

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
    @contextlib.contextmanager
    def track(self,direction,count,rate):
        identifier=self.headers.get('X-CWG-Test-ID','')
        if not re.fullmatch('[A-Za-z0-9-]{1,64}',identifier):identifier=uuid.uuid4().hex
        started=time.monotonic();stop=threading.Event()
        progress={'bytes':0,'io_block_max_ms':0.0,'completed':False}
        def sample(event):
            print(json.dumps({'event':event,'utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'id':identifier,'direction':direction,'target_bytes':count,'target_bytes_per_s':rate,
                'elapsed_s':round(time.monotonic()-started,4),'progress':dict(progress),
                'tcp':tcp_info(self.connection),'host_udp_errors':udp_errors()}),flush=True)
        def watch():
            while not stop.wait(1):
                sample('transfer_sample')
                if time.monotonic()-started>45:
                    try:self.connection.shutdown(socket.SHUT_RDWR)
                    except OSError:pass
                    break
        sample('transfer_start')
        worker=threading.Thread(target=watch,daemon=True);worker.start()
        try:yield progress
        finally:
            stop.set();worker.join(timeout=2);sample('transfer_end')
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
        with self.track('download',count,rate) as progress:
            try:
                self.headers_for(count);sent=0;started=time.monotonic()
                while sent<count:
                    block=BLOCK[:min(len(BLOCK),count-sent)];before=time.monotonic()
                    self.wfile.write(block);sent+=len(block)
                    progress['bytes']=sent
                    progress['io_block_max_ms']=max(progress['io_block_max_ms'],round((time.monotonic()-before)*1000,3))
                    delay=started+sent/rate-time.monotonic()
                    if delay>0:time.sleep(delay)
                progress['completed']=True
            except (ConnectionError,TimeoutError):pass
    def do_POST(self):
        try:
            if self.path!='/upload' or self.headers.get('Transfer-Encoding') is not None:raise ValueError()
            if len(self.headers.get_all('Content-Length',[]))!=1:raise ValueError()
            count=int(self.headers['Content-Length'])
            if not 1<=count<=MAX_BYTES:raise ValueError()
        except (ValueError,TypeError):self.send_error(400);return
        with self.track('upload',count,None) as progress:
            try:
                digest=hashlib.sha256();received=0
                while received<count:
                    before=time.monotonic();data=self.rfile.read(min(65536,count-received))
                    if not data:return
                    received+=len(data);digest.update(data);progress['bytes']=received
                    progress['io_block_max_ms']=max(progress['io_block_max_ms'],round((time.monotonic()-before)*1000,3))
                data=json.dumps({'received_bytes':received,'sha256':digest.hexdigest()}).encode()
                self.headers_for(len(data),'application/json');self.wfile.write(data)
                progress['completed']=True
            except (ConnectionError,TimeoutError):pass

def main():
    p=argparse.ArgumentParser();p.add_argument('--bind',choices=['10.77.0.1','127.0.0.1'],default='10.77.0.1');p.add_argument('--port',type=int,default=18081)
    args=p.parse_args()
    with Server((args.bind,args.port),Handler) as server:
        print('Bounded synthetic performance endpoint ready',flush=True);server.serve_forever()
if __name__=='__main__':main()
