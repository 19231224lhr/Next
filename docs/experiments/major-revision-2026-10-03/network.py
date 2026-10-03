"""D1: fixed H/L/L-M/L-C matrix; retained-state SIGSTOP/CONT faults.

Each TCP direction is a bounded FIFO of chunks with release time arrival+delay.
The reader does not sleep per chunk, so this is propagation delay, not an
intentional bandwidth cap. HTTP and encrypted committee P2P use the same proxy.
No keys/signatures/consensus checks are changed. Not a WAN emulator.
"""
import argparse, asyncio, hashlib, json, os, resource, shutil, signal, subprocess, time
from pathlib import Path
from urllib.request import urlopen

def write(path, value):
    path.write_text(json.dumps(value, indent=2))

def replace(value, mapping):
    if isinstance(value, str): return mapping.get(value, value)
    if isinstance(value, list): return [replace(v, mapping) for v in value]
    if isinstance(value, dict): return {k: replace(v, mapping) for k,v in value.items()}
    return value

class Proxy:
    def __init__(self, delay):
        self.delay, self.servers, self.tasks = delay, [], set()
        self.stats = {}

    async def add(self, name, port, target):
        self.stats[name] = dict(connections=0, chunks=0, bytes=0, hold_ns=0, max_hold_ns=0)
        async def connected(reader, writer):
            task = asyncio.current_task(); self.tasks.add(task)
            upstream = None
            try:
                r,w = await asyncio.open_connection('127.0.0.1', target)
                upstream = w; self.stats[name]['connections'] += 1
                async def direction(src, dst):
                    q = asyncio.Queue(64)
                    async def read():
                        while data := await src.read(16384):
                            await q.put((time.monotonic_ns(), data))
                        await q.put((0,None))
                    async def send():
                        while True:
                            stamp,data = await q.get()
                            if data is None: return
                            await asyncio.sleep(max(0, self.delay-(time.monotonic_ns()-stamp)/1e9))
                            elapsed = time.monotonic_ns()-stamp
                            s=self.stats[name];s['chunks']+=1;s['bytes']+=len(data);s['hold_ns']+=elapsed;s['max_hold_ns']=max(s['max_hold_ns'],elapsed)
                            dst.write(data);await dst.drain()
                    pair=[asyncio.create_task(read()),asyncio.create_task(send())]
                    try: await asyncio.gather(*pair)
                    finally:
                        for t in pair:t.cancel()
                        await asyncio.gather(*pair,return_exceptions=True)
                pair=[asyncio.create_task(direction(reader,w)),asyncio.create_task(direction(r,writer))]
                try:
                    await asyncio.wait(pair,return_when=asyncio.FIRST_COMPLETED)
                finally:
                    for t in pair:t.cancel()
                    await asyncio.gather(*pair,return_exceptions=True)
            except (ConnectionError,OSError): pass
            finally:
                writer.close()
                if upstream: upstream.close()
                self.tasks.discard(task)
        self.servers.append(await asyncio.start_server(connected,'127.0.0.1',port))

    async def close(self):
        for server in self.servers: server.close();await server.wait_closed()
        for task in list(self.tasks):task.cancel()
        await asyncio.gather(*list(self.tasks),return_exceptions=True)

async def main():
    ap=argparse.ArgumentParser();ap.add_argument('--root',type=Path,required=True);ap.add_argument('--cases',default='H,L,L-M,L-C');ap.add_argument('--rounds',type=int,default=3);ap.add_argument('--campaign',default='network-results');ap.add_argument('--skip-build',action='store_true')
    args=ap.parse_args();root=args.root.resolve();os.chdir(root)
    soft,hard=resource.getrlimit(resource.RLIMIT_NOFILE)
    resource.setrlimit(resource.RLIMIT_NOFILE,(min(16384,hard) if hard!=resource.RLIM_INFINITY else 16384,hard))
    out=root/args.campaign;out.mkdir(exist_ok=True);binary=root/'bin';binary.mkdir(exist_ok=True);(root/'.run').mkdir(exist_ok=True)
    env={k:v for k,v in os.environ.items() if not k.startswith('UTXO_')}
    env.update(PATH='/usr/local/go/bin:/opt/homebrew/bin:'+env['PATH'],GOMAXPROCS='16',GOGC='200',UTXO_EXPERIMENT_COMMIT='250ms',UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms',UTXO_EXPERIMENT_BUDGET='1',UTXO_EXPERIMENT_MEMBER_SYNC='1')
    for name in ['COMMITTEE_MEMORY','MEMBER_MEMORY','GATEWAY_MEMORY','MEM_BLOCKSTORE']:env['UTXO_EXPERIMENT_'+name]='0'
    def command(args,path,timeout=900):
        with path.open('w') as f:subprocess.run(list(map(str,args)),env=env,cwd=root,stdout=f,stderr=f,check=True,timeout=timeout)
    if not args.skip_build:
        command(['python3','third_party/cometbft/overlay.py'],out/'overlay.log')
        command(['go','test','-tags=comet_v3','./...'],out/'tests.log')
        command(['go','vet','-tags=comet_v3','./...'],out/'vet.log')
        for role in ['payctl','committee','member','gateway']:command(['go','build','-tags=comet_v3','-o',binary/role,'./cmd/'+role],out/(role+'-build.log'))
    def fingerprint(directory):return {str(p.relative_to(directory)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(directory.rglob('*')) if p.is_file()}
    write(out/'build.json',dict(baseline='9879f64',source=json.loads((root/'source-manifest.json').read_text()),binaries=fingerprint(binary),overlay=fingerprint(root/'.scratch/comet-src'),runner_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),env={k:v for k,v in env.items() if k.startswith('UTXO_') or k in ['GOMAXPROCS','GOGC']}))
    print('BUILD PASS',flush=True)
    for rep in range(args.rounds):
        cases=args.cases.split(',');cases=cases[rep%len(cases):]+cases[:rep%len(cases)]
        for case in cases:
            label=f'{case}-{rep+1}';dest=out/label
            if (dest/'passed.json').exists():
                print('REUSE VERIFIED',label,flush=True);continue
            dest.mkdir();lab=root/'.run'/(args.campaign+'-'+label)
            ctl=lambda argv,log:command([binary/'payctl',*argv],dest/log)
            ctl(['init-lab','-dir',lab,'-port','31000','-outputs','1','-v4'],'init.log')
            ctl(['e5-load','-dir',lab,'-prepare','-count','1300'],'prepare-load.log')
            ctl(['chain-v4','-dir',lab,'-length','100','-prepare','-e5-owner-fuel'],'prepare-chain.log')
            # Chain owners need disjoint FUEL beyond the independent cohort.
            config=json.loads((lab/'lab.json').read_text());network=Path(config['Network']);n=json.loads(network.read_text())
            # Owner0 has 1300 cohort + 200 chain FUEL. The other two owners each
            # have 100; their offsets must be relative to their own coin lists.
            nodes=[x for x in config['Nodes'] if x['Binary']=='committee' or x['Name']=='gateway0' or x['Name'].startswith('org0-member')]
            config['Nodes']=nodes
            proxy=Proxy(0 if case=='H' else .025);mapping={}
            for node in nodes:
                if node['Binary']=='member':
                    port=int(node['URL'].rsplit(':',1)[1]);mapping[node['URL']]='http://127.0.0.1:'+str(port+3000)
                    await proxy.add(node['Name']+'-rpc',port+3000,port)
            write(network,replace(n,mapping))
            for node in nodes:
                p=Path(node['Config']);c=replace(json.loads(p.read_text()),mapping)
                if node['Binary']=='committee':
                    index=c['Index'];await proxy.add('committee'+str(index)+'-p2p',34200+index,31200+index)
                    for j in range(4):c['Peers']=c['Peers'].replace(':'+str(31200+j),':'+str(34200+j))
                write(p,c)
            write(lab/'lab.json',config)
            write(dest/'configuration.json',dict(case=case,round=rep+1,delay_per_direction_ms=proxy.delay*1000,fd_limit=resource.getrlimit(resource.RLIMIT_NOFILE),load=1200,rate=20,warm=100,fault=[15,45],nodes=nodes,untouched=['wallet-gateway HTTP','committee application HTTP','wallet receipt delivery','disk latency']))
            processes={};files=[];jobs=[];paused=None
            def spawn(name,argv):
                f=(dest/(name+'.log')).open('w');files.append(f)
                p=subprocess.Popen(list(map(str,argv)),env=env,cwd=root,stdout=f,stderr=f);processes[name]=p;return p
            def get(url):
                with urlopen(url,timeout=3) as r:return r.read()
            async def waitproc(p,limit):
                until=time.monotonic()+limit
                while p.poll() is None:
                    if time.monotonic()>until:raise TimeoutError('process timeout')
                    await asyncio.sleep(.1)
                if p.returncode:raise RuntimeError('process exit '+str(p.returncode))
            try:
                for node in nodes:spawn(node['Name'],[binary/node['Binary'],'-config',node['Config']])
                for node in nodes:
                    end=time.monotonic()+90
                    while True:
                        try:await asyncio.to_thread(get,node['URL']+'/healthz');break
                        except Exception:
                            if processes[node['Name']].poll() is not None or time.monotonic()>end:raise
                            await asyncio.sleep(.2)
                calibration=[]
                for url in mapping.values():
                    values=[]
                    for _ in range(5):
                        start=time.monotonic_ns();await asyncio.to_thread(get,url+'/healthz');values.append(time.monotonic_ns()-start)
                    calibration.append(dict(url=url,ns=values))
                write(dest/'rtt.json',calibration)
                warm=spawn('warm',[binary/'payctl','e5-load','-dir',lab,'-count','100','-rate','20','-seed','23'])
                await waitproc(warm,90)
                shutil.copytree(lab/'reports',dest/'warm-reports')
                # Two driver-only copies share the authentic genesis and keys,
                # but have independent wallets/reports and disjoint origin indices.
                chain_dirs=[]
                for i in range(2):
                    d=lab/('chain'+str(i));d.mkdir();(d/'reports').mkdir();(d/'keys').mkdir()
                    shutil.copyfile(lab/'keys/chain-c.key',d/'keys/chain-c.key');write(d/'lab.json',config);chain_dirs.append(d)
                    # The existing offline chain auditor resolves node DBs relative
                    # to the driver directory; point it to the same retained stores.
                    for node in nodes:
                        if node['Binary']=='committee':(d/node['Name']).symlink_to(lab/node['Name'],target_is_directory=True)
                start=time.monotonic();events=[]
                load=spawn('load',[binary/'payctl','e5-load','-dir',lab,'-count','1200','-offset','100','-rate','20','-seed',23+rep]);jobs.append(load)
                for at,action in [(15,'pause'),(20,'chain0'),(30,'chain1'),(45,'resume')]:
                    await asyncio.sleep(max(0,start+at-time.monotonic()))
                    if action=='pause' and case in ['L-M','L-C']:
                        target='org0-member3' if case=='L-M' else 'committee3';paused=processes[target];paused.send_signal(signal.SIGSTOP)
                        events.append(dict(action=action,target=target,pid=paused.pid,ns=time.time_ns(),offset=time.monotonic()-start))
                    elif action=='resume' and paused:
                        paused.send_signal(signal.SIGCONT);paused=None;events.append(dict(action=action,ns=time.time_ns(),offset=time.monotonic()-start))
                    elif action.startswith('chain'):
                        i=int(action[-1]);p=spawn(action,[binary/'payctl','chain-v4','-dir',chain_dirs[i],'-length','10','-e5-owner-fuel','-origin-index',1300+i,'-fuel-offset',20*i,'-payer-fuel-offset','1300']);jobs.append(p)
                        events.append(dict(action=action,ns=time.time_ns(),offset=time.monotonic()-start))
                    write(dest/'events.json',events)
                for p in jobs:await waitproc(p,max(1,start+120-time.monotonic()))
                await asyncio.sleep(2)
            finally:
                if paused:paused.send_signal(signal.SIGCONT)
                for p in processes.values():
                    if p.poll() is None:p.send_signal(signal.SIGINT)
                for p in processes.values():
                    end=time.monotonic()+45
                    while p.poll() is None and time.monotonic()<end:await asyncio.sleep(.1)
                    if p.poll() is None:p.kill();p.wait()
                for f in files:f.close()
                write(dest/'proxy.json',proxy.stats);await proxy.close()
                shutil.copytree(lab/'reports',dest/'reports',dirs_exist_ok=True)
                for i in range(2):
                    d=lab/('chain'+str(i))/'reports'
                    if d.exists():shutil.copytree(d,dest/('chain'+str(i)),dirs_exist_ok=True)
            ctl(['audit','-dir',lab],'audit.log')
            ctl(['e5-load','-dir',lab,'-audit'],'load-audit.log')
            for d in chain_dirs:ctl(['chain-v4','-dir',d,'-audit'],d.name+'-audit.log')
            shutil.copytree(lab/'reports',dest/'reports',dirs_exist_ok=True)
            audit=json.loads((dest/'reports/audit.json').read_text());cs=[r for r in audit if r['Name'].startswith('committee')]
            assert len(cs)==4 and len({r['StateHash'] for r in cs})==1
            assert all(r['Gap']=='0' and r['Payments']==r['Closed']==1320 for r in cs)
            assert all(r.get('Pending',0)==0 for r in audit)
            write(dest/'passed.json',dict(payments=1320,state_agreement=True,outbox_empty=True));print('PASS',label,flush=True)

if __name__=='__main__':asyncio.run(main())
