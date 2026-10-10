"""User/QA localhost benchmark. Synthetic UDP, temporary keys, no VPN/routes.

Usage: python bench-burst-recovery.py EXE baseline|candidate [one_way_delay_ms=45] [outer_link_mbps=0]
Rates are application payload Mbps offered IN EACH DIRECTION simultaneously.
This is a carrier benchmark, not a claim about Internet or native WireGuard speed.
"""
import collections, heapq, json, pathlib, secrets, select, socket, struct
import subprocess, sys, tempfile, threading, time

SIZE=1120

def udp():
    s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
    s.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,4<<20)
    s.setsockopt(socket.SOL_SOCKET,socket.SO_SNDBUF,4<<20)
    s.bind(('127.0.0.1',0));s.setblocking(False)
    return s

def main():
    exe=str(pathlib.Path(sys.argv[1]).resolve(strict=True))
    profile=sys.argv[2]
    assert profile in ('baseline','candidate')
    delay=float(sys.argv[3])/1000 if len(sys.argv)>3 else .045
    duration=5
    link_mbps=float(sys.argv[4]) if len(sys.argv)>4 else 0
    assert 0<=link_mbps<=1000
    assert 0<=delay<=.5 and 2<=duration<=30
    stop=threading.Event();errors=[];processes=[];threads=[];logs=[]
    backend, wg, public, local, relay=[udp() for _ in range(5)]
    sockets=[backend,wg,public,local,relay]
    addresses={name:s.getsockname() for name,s in [('backend',backend),('wg',wg),('public',public),('local',local),('relay',relay)]}
    def addr(name):return '%s:%d'%addresses[name]
    results=collections.defaultdict(lambda:collections.defaultdict(list))
    sent=collections.Counter();peer=[None];drops=[0]
    def forward():
        queue=[];serial=0;client=None;link_ready={}
        try:
            while not stop.is_set():
                now=time.perf_counter()
                while queue and queue[0][0]<=now:
                    _,_,data,destination=heapq.heappop(queue);relay.sendto(data,destination)
                timeout=max(0,min(.002,queue[0][0]-now)) if queue else .002
                if select.select([relay],[],[],timeout)[0]:
                    for _ in range(128):
                        try:data,source=relay.recvfrom(2048)
                        except (BlockingIOError, ConnectionResetError):break
                        if source==addresses['public']: destination=client
                        else:client=source;destination=addresses['public']
                        if destination:
                            if len(queue)>=32768:drops[0]+=1;continue
                            at=time.perf_counter()
                            if link_mbps:
                                ready=max(at,link_ready.get(destination,at))
                                if ready-at>.020:drops[0]+=1;continue
                                link_ready[destination]=ready+(len(data)+28)*8/(link_mbps*1e6)
                                at=link_ready[destination]
                            serial+=1;heapq.heappush(queue,(at+delay,serial,data,destination))
        except Exception as e:errors.append(repr(e));stop.set()
    def receive(s,direction):
        try:
            while not stop.is_set():
                if not select.select([s],[],[],.05)[0]:continue
                for _ in range(128):
                    try:data,source=s.recvfrom(2048)
                    except (BlockingIOError, ConnectionResetError):break
                    if s is backend:peer[0]=source
                    if len(data) in (SIZE,80) and data[:4]==b'CWGB':
                        stage,seq,started=struct.unpack_from('!IQd',data,4)
                        if data[24:]!=b'\xa5'*(len(data)-24):raise AssertionError('payload corruption')
                        results[stage][direction].append((seq,(time.perf_counter()-started)*1000))
        except Exception as e:errors.append(repr(e));stop.set()
    temporary=tempfile.TemporaryDirectory(prefix='cwg-adaptive-bench-')
    try:
        tmp=temporary.name
        root=pathlib.Path(tmp)
        subprocess.run([exe,'init','-endpoint',addr('relay'),'-out',str(root/'bundle')],check=True,stdout=subprocess.DEVNULL)
        for name in ('client','server'):
            path=root/'bundle'/(name+'.json');cfg=json.loads(path.read_text())
            cfg.update(transport='exchange',exchange_queue_ms=60)
            cfg['exchange_burst_ms']=350 if name=='client' or profile=='candidate' else 200
            if profile=='candidate':cfg['exchange_queue_packets']=4096
            if name=='client':cfg.update(listen=addr('local'),wireguard=addr('wg'),exchange_pps=200,exchange_max_pps=9000 if profile=='candidate' else 6000)
            else:cfg.update(listen=addr('public'),wireguard=addr('backend'))
            path.write_text(json.dumps(cfg))
        public.close();local.close()
        for name in ('server','client'):
            log=(root/(name+'.stderr')).open('w+b');logs.append(log)
            processes.append(subprocess.Popen([exe,name,'-config',str(root/'bundle'/(name+'.json'))],stdout=subprocess.DEVNULL,stderr=log,creationflags=getattr(subprocess,'CREATE_NO_WINDOW',0)))
        threads=[threading.Thread(target=forward),threading.Thread(target=receive,args=(backend,'upload')),threading.Thread(target=receive,args=(wg,'download'))]
        for thread in threads:thread.start()
        until=time.perf_counter()+4
        while peer[0] is None and time.perf_counter()<until:
            wg.sendto(b'bind',addresses['local']);time.sleep(.05)
        assert peer[0] is not None,('No association',errors)
        def send_pair(stage,seq,size):
            data=b'CWGB'+struct.pack('!IQd',stage,seq,time.perf_counter())+b'\xa5'*(size-24)
            wg.sendto(data,addresses['local']);backend.sendto(data,peer[0]);sent[stage]+=1
        start=time.perf_counter();seq=0
        while time.perf_counter()-start<2.5:
            for _ in range(4):seq+=1;send_pair(1,seq,SIZE)
            time.sleep(.001)
        time.sleep(4)
        start=time.perf_counter();next_burst=start;next_small=start;bursts=0;left=0
        bulk_seq=small_seq=0
        while time.perf_counter()-start<6.0:
            now=time.perf_counter()
            if bursts<6 and now>=next_burst:
                bursts+=1;left=3000;next_burst+=.85
            if left:
                for _ in range(min(64,left)):
                    bulk_seq+=1;send_pair(2,bulk_seq,SIZE);left-=1
            if now>=next_small:
                small_seq+=1;send_pair(3,small_seq,80);next_small=now+.01
            time.sleep(.001)
            if errors:raise RuntimeError(errors)
        time.sleep(.5)
        for stage,label in [(2,'3000-packet-bursts-after-idle'),(3,'small-packets-during-bursts')]:
            row={'phase':label,'profile':profile,'outer_link_mbps':link_mbps,'sent_each_way':sent[stage],'relay_drops':drops[0]}
            for direction in ('upload','download'):
                values=results[stage][direction];unique={x[0] for x in values};lat=sorted(x[1] for x in values)
                assert len(unique)==len(values),'duplicate payload'
                row[direction]={'received':len(unique),'loss_percent':round(100*(1-len(unique)/sent[stage]),2),'one_way_p50_ms':round(lat[len(lat)//2],2) if lat else None,'one_way_p95_ms':round(lat[int(len(lat)*.95)],2) if lat else None,'max_ms':round(max(lat),2) if lat else None}
            print(json.dumps(row),flush=True)
            if profile=='candidate' and not link_mbps:
                assert all(row[d]['received']==sent[stage] for d in ('upload','download')), row
                if stage==3:assert all(row[d]['one_way_p95_ms'] < delay*1000+20 for d in ('upload','download')), row
        assert not errors,errors
    finally:
        stop.set()
        for p in processes:
            p.terminate()
            try:p.wait(timeout=5)
            except subprocess.TimeoutExpired:p.kill();p.wait()
        for thread in threads:thread.join(timeout=2)
        for log in logs:
            log.seek(0);sys.stderr.write(log.read().decode(errors='replace'));log.close()
        for s in sockets:s.close()
        temporary.cleanup()
    print('Local synthetic carrier benchmark only; no OS VPN or route changes.',flush=True)

if __name__=='__main__':main()
