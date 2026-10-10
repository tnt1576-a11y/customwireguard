"""User/QA localhost benchmark. Synthetic UDP, temporary keys, no VPN/routes.

Usage: python bench-exchange.py EXE [one_way_delay_ms=45] [seconds_per_stage=5]
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
    delay=float(sys.argv[2])/1000 if len(sys.argv)>2 else .045
    duration=float(sys.argv[3]) if len(sys.argv)>3 else 5
    assert 0<=delay<=.5 and 2<=duration<=30
    stop=threading.Event();errors=[];processes=[];threads=[];logs=[]
    backend, wg, public, local, relay=[udp() for _ in range(5)]
    sockets=[backend,wg,public,local,relay]
    addresses={name:s.getsockname() for name,s in [('backend',backend),('wg',wg),('public',public),('local',local),('relay',relay)]}
    def addr(name):return '%s:%d'%addresses[name]
    results=collections.defaultdict(lambda:collections.defaultdict(list))
    sent=collections.Counter();peer=[None];drops=[0]
    def forward():
        queue=[];serial=0;client=None
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
                            serial+=1;heapq.heappush(queue,(time.perf_counter()+delay,serial,data,destination))
        except Exception as e:errors.append(repr(e));stop.set()
    def receive(s,direction):
        try:
            while not stop.is_set():
                if not select.select([s],[],[],.05)[0]:continue
                for _ in range(128):
                    try:data,source=s.recvfrom(2048)
                    except (BlockingIOError, ConnectionResetError):break
                    if s is backend:peer[0]=source
                    if len(data)==SIZE and data[:4]==b'CWGB':
                        stage,seq,started=struct.unpack_from('!IQd',data,4)
                        if data[24:]!=b'\xa5'*(SIZE-24):raise AssertionError('payload corruption')
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
            if name=='client':cfg.update(listen=addr('local'),wireguard=addr('wg'),exchange_pps=200,exchange_max_pps=6000)
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
        for stage,mbps in enumerate((1,10,25,50),1):
            rate=mbps*1e6/(8*SIZE);start=time.perf_counter();next_send=start;seq=0
            while time.perf_counter()-start<duration:
                now=time.perf_counter();budget=min(32,max(0,int((now-next_send)*rate)+1))
                for _ in range(budget):
                    seq+=1;data=b'CWGB'+struct.pack('!IQd',stage,seq,time.perf_counter())+b'\xa5'*(SIZE-24)
                    wg.sendto(data,addresses['local']);backend.sendto(data,peer[0]);sent[stage]+=1;next_send+=1/rate
                time.sleep(.001)
                if errors:raise RuntimeError(errors)
            actual=time.perf_counter()-start
            time.sleep(max(.3,delay*3))
            row={'offered_mbps_each_way':mbps,'elapsed_s':round(actual,3),'packets_sent_each_way':sent[stage],'relay_drops':drops[0]}
            for direction in ('upload','download'):
                values=results[stage][direction];unique={x[0] for x in values};lat=sorted(x[1] for x in values)
                assert len(unique)==len(values),'duplicate payload'
                row[direction]={'delivered_mbps':round(len(unique)*SIZE*8/actual/1e6,2),'loss_percent':round(100*(1-len(unique)/sent[stage]),2),'one_way_p50_ms':round(lat[len(lat)//2],2) if lat else None,'one_way_p95_ms':round(lat[int(len(lat)*.95)],2) if lat else None}
            print(json.dumps(row),flush=True)
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
