#!/usr/bin/env python3
"""E7: two independent receiver-driven wallets and site-aware HTTP delay."""
import argparse, copy, gzip, hashlib, importlib.util, json, os, shutil, signal, subprocess, time, urllib.request
from pathlib import Path
from datetime import datetime, timezone, timedelta

OUT=Path(__file__).resolve().parent; ROOT=OUT.parents[2]
spec=importlib.util.spec_from_file_location('e5',OUT.parent/'delivery-gate-2026-09-24/reproduce.py')
labutil=importlib.util.module_from_spec(spec);spec.loader.exec_module(labutil)
labutil.OUT=OUT;labutil.BIN=ROOT/'.run/e7-bin'
labutil.ENV.pop('UTXO_E5_OBSERVE',None)
labutil.ENV.update(UTXO_E8_METRICS='1',UTXO_E8_LARGE_GENESIS='1')
BIN,ENV=labutil.BIN,labutil.ENV
write,cmd,get=labutil.write,labutil.command,labutil.get

def post(url,data):
    req=urllib.request.Request(url,data=json.dumps(data).encode(),headers={'Content-Type':'application/json'})
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=10) as r:return r.read()

def replace(x,mapping):
    if isinstance(x,str):return mapping.get(x,x)
    if isinstance(x,list):return [replace(v,mapping) for v in x]
    if isinstance(x,dict):return {k:replace(v,mapping) for k,v in x.items()}
    return x

def run(label,mode,rate,duration,rtt=0,seed=23,warm=10,capacity=65000,lanes=64):
    dest=OUT/label;dest.mkdir();runtime=ROOT/'.run'/('e7-'+label)
    with (dest/'setup.log').open('w') as log:
        template=ROOT/'.run/e7-template'
        if not template.exists():cmd([BIN/'payctl','init-lab','-dir',template,'-outputs',1,'-port',26000,'-v4'],log)
        shutil.copytree(template,runtime)
        for p in runtime.rglob('*.json'):p.write_text(p.read_text().replace(str(template),str(runtime)))
        cmd([BIN/'payctl','e7','-dir',runtime,'-capacity',capacity],log)
    lab=json.loads((runtime/'lab.json').read_text());network=json.loads(Path(lab['Network']).read_text())
    network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat();write(Path(lab['Network']),network)
    def site(n):
        if n['Binary']=='committee':return 2
        return 0 if n['Name'].startswith('org0-') or n['Name']=='gateway0' else 1
    targets=[dict(Name=n['Name'],URL=n['URL'],Site=site(n)) for n in lab['Nodes']]
    targets += [dict(Name='wallet'+str(i),URL='http://127.0.0.1:'+str(29200+i),Site=i) for i in range(2)]
    proxy='http://127.0.0.1:29100';write(runtime/'proxy.json',dict(Targets=targets,RTT=rtt*1000000))
    maps=[{t['URL']:f'{proxy}/{s}/{i}' for i,t in enumerate(targets)} for s in range(3)]
    for s in range(3):write(runtime/f'network-site{s}.json',replace(network,maps[s]))
    for n in lab['Nodes']:
        c=replace(json.loads(Path(n['Config']).read_text()),maps[site(n)])
        c['Network']=str(runtime/f'network-site{site(n)}.json');write(Path(n['Config']),c)
    for s in range(2):write(runtime/f'wallet{s}.json',dict(Dir=str(runtime),Network=str(runtime/f'network-site{s}.json'),Listen=f'127.0.0.1:{29200+s}',Site=s,Gateway=maps[s][lab['Gateways'][s]],Receivers=[maps[s][t['URL']] for t in targets[-2:]]))
    write(dest/'configuration.json',dict(mode=mode,rate=rate,duration=duration,rtt=rtt,seed=seed,warm=warm,capacity=capacity,targets=targets,routes=maps,origins=len(network['Genesis']['Outputs']),genesis_sha256=hashlib.sha256(Path(lab['Network']).read_bytes()).hexdigest()))
    del network
    shutil.copyfile(OUT/'build.json',dest/'build.json')
    procs={};logs=[];passed=False
    def spawn(name,args):
        log=(runtime/'logs'/(name+'.log')).open('w');logs.append(log)
        p=subprocess.Popen(list(map(str,args)),cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT);procs[name]=p
    def health(name,url):
        deadline=time.monotonic()+240
        while True:
            try:get(url+'/healthz');return
            except Exception:
                if procs[name].poll() is not None or time.monotonic()>deadline:raise
                time.sleep(.2)
    def calibration(name):
        rows=[]
        for a,b in [(0,0),(0,1),(0,2),(1,0),(1,2)]:
            idx=next(i for i,t in enumerate(targets) if t['Site']==b)
            ns=[]
            for _ in range(10):
                start=time.monotonic_ns();get(f'{proxy}/{a}/{idx}/healthz');ns.append(time.monotonic_ns()-start)
            rows.append(dict(source=a,target=b,elapsed_ns=ns))
        write(dest/f'calibration-{name}.json',rows)
    try:
        spawn('proxy',[BIN/'payctl','e7-proxy','-config',runtime/'proxy.json']);health('proxy',proxy)
        for n in lab['Nodes']:spawn(n['Name'],[BIN/n['Binary'],'-config',n['Config']])
        for n in lab['Nodes']:health(n['Name'],n['URL'])
        for s in range(2):spawn('wallet'+str(s),[BIN/'payctl','e7-wallet','-config',runtime/f'wallet{s}.json'])
        for t in targets[-2:]:health(t['Name'],t['URL'])
        calibration('before')
        with (dest/'resources.jsonl').open('w') as out:
            for phase,seconds,hz in [('warm',warm,50),('formal',duration,rate)]:
                if seconds==0:continue
                options=dict(Phase=phase,Rate=hz,Duration=seconds*1000000000,Drain=60000000000,Seed=seed,Lanes=lanes,Fast=128,Pending=1024,Chain=mode in 'CD',Cross=mode in 'BD')
                write(dest/(phase+'-options.json'),options)
                write(dest/(phase+'-proxy-start.json'),get(proxy+'/stats'))
                write(dest/(phase+'-metrics-start.json'),{n['Name']:get(n['URL']+'/debug/e8') for n in lab['Nodes']})
                for t in targets[-2:]:post(t['URL']+'/run',options)
                deadline=time.monotonic()+seconds+90
                while True:
                    statuses=[get(t['URL']+'/status') for t in targets[-2:]]
                    ids=','.join(str(p.pid) for p in procs.values() if p.poll() is None)
                    ps=subprocess.check_output(['ps','-o','pid=,time=,rss=','-p',ids],text=True)
                    out.write(json.dumps(dict(NS=time.time_ns(),phase=phase,status=statuses,ps=ps,pids={k:v.pid for k,v in procs.items()}))+'\n');out.flush()
                    if all(not s['Active'] for s in statuses):
                        if any(s.get('Error') or s['Pending'] for s in statuses):raise RuntimeError('phase incomplete: '+str(statuses))
                        break
                    if any(p.poll() is not None for p in procs.values()):raise RuntimeError('process exited')
                    if time.monotonic()>deadline:raise TimeoutError('phase deadline')
                    time.sleep(1)
        calibration('after');write(dest/'proxy-stats.json',get(proxy+'/stats'))
        write(dest/'metrics-final.json',{n['Name']:get(n['URL']+'/debug/e8?state=1') for n in lab['Nodes']})
        passed=True
    finally:
        # Stop wallets first, then nodes. Successful snapshots are audited offline.
        for name,p in procs.items():
            if name.startswith('wallet') and p.poll() is None:p.send_signal(signal.SIGINT)
        for name,p in procs.items():
            if name.startswith('wallet'):
                try:p.wait(timeout=30)
                except subprocess.TimeoutExpired:p.kill();p.wait();passed=False
        for p in procs.values():
            if p.poll() is None:p.send_signal(signal.SIGINT)
        for p in procs.values():
            try:p.wait(timeout=120)
            except subprocess.TimeoutExpired:p.kill();p.wait();passed=False
        for log in logs:log.close()
        shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
        shutil.copytree(runtime/'logs',dest/'node-logs',dirs_exist_ok=True)
    if passed:
        with (dest/'audit.log').open('w') as log:
            cmd([BIN/'payctl','audit','-dir',runtime],log)
            cmd([BIN/'payctl','e7','-dir',runtime,'-audit'],log)
        shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
        audit=json.loads((dest/'reports/audit.json').read_text())
        assert len({x['StateHash'] for x in audit if x['Name'].startswith('committee')})==1
        assert all(x['Pending']==0 for x in audit)
        write(dest/'passed.json',dict(global_audit=True,payment_audit=True))
        resolved=runtime.resolve();assert resolved.parent==(ROOT/'.run').resolve() and resolved.name=='e7-'+label
        shutil.rmtree(resolved)
    for path in dest.rglob('*.json'):
        if path.stat().st_size>2_000_000:
            with path.open('rb') as src,gzip.open(str(path)+'.gz','wb') as dst:shutil.copyfileobj(src,dst)
            path.unlink()
    print(json.dumps(dict(case=label,passed=passed)),flush=True)
    if not passed:raise RuntimeError('E7 incomplete; evidence retained')

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--label',required=True);p.add_argument('--mode',choices=list('ABCD'),default='D');p.add_argument('--rate',type=int,default=200);p.add_argument('--duration',type=int,default=120);p.add_argument('--rtt',type=int,default=0);p.add_argument('--seed',type=int,default=23);p.add_argument('--warm',type=int,default=10);p.add_argument('--capacity',type=int,default=65000);p.add_argument('--skip-build',action='store_true');a=p.parse_args()
    if not a.skip_build:labutil.build()
    run(a.label,a.mode,a.rate,a.duration,a.rtt,a.seed,a.warm,a.capacity)
