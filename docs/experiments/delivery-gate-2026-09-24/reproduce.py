#!/usr/bin/env python3
"""E5: same backend, A immediate delivery vs B three stored copies/public commit."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
import urllib.request
from datetime import datetime, timezone, timedelta

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
BIN = ROOT / '.run/e5-bin'
ENV = dict(os.environ, PATH='/usr/local/go/bin:/opt/homebrew/bin:' + os.environ['PATH'])
ENV.update(UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
           UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_MEM_BLOCKSTORE='1',
           UTXO_EXPERIMENT_COMMITTEE_MEMORY='1', UTXO_EXPERIMENT_MEMBER_MEMORY='1',
           UTXO_EXPERIMENT_GATEWAY_MEMORY='1', UTXO_EXPERIMENT_BUDGET='1',
           GOMAXPROCS='16', GOGC='200')
for key in ['UTXO_TRACE_ALL','UTXO_RUNTIME_TRACE','UTXO_EXPERIMENT_SERIAL_DIRECT','UTXO_SETTLEMENT_TRACE']:
    ENV.pop(key,None)

def write(path,obj):
    path.write_text(json.dumps(obj,separators=(',',':'))+'\n')

def get(url):
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(url,timeout=3) as r:
        raw=r.read()
    return True if raw.strip()==b'alive' else json.loads(raw)

def command(args,log):
    subprocess.run(list(map(str,args)),cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT,check=True)

def build():
    BIN.mkdir(parents=True,exist_ok=True)
    with (OUT/'build.log').open('w') as log:
        command(['python3',ROOT/'third_party/cometbft/overlay.py'],log)
        for role in ['payctl','committee','member','gateway']:
            command(['go','build','-tags=comet_v3','-o',BIN/role,'./cmd/'+role],log)
    write(OUT/'build.json',{'commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),
         'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()},
         'environment':{k:v for k,v in ENV.items() if k.startswith('UTXO_') or k in ['GOGC','GOMAXPROCS']}})

def wait_health(nodes):
    for node in nodes:
        deadline=time.monotonic()+60
        while True:
            try:
                get(node['URL']+'/healthz');break
            except Exception:
                if time.monotonic()>deadline:raise
                time.sleep(.1)

def run_case(label,mode,rate,count,seed=23,warm=100,window=0):
    dest=OUT/label;dest.mkdir()
    labdir=ROOT/'.run'/('e5-'+label)
    with (dest/'setup.log').open('w') as log:
        command([BIN/'payctl','init-lab','-dir',labdir,'-outputs','1','-port','26000','-v4'],log)
        command([BIN/'payctl','e5-load','-dir',labdir,'-prepare','-count',count,'-offset',warm],log)
        lab=json.loads((labdir/'lab.json').read_text())
        lab['Nodes']=[n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')]
        write(labdir/'lab.json',lab)
        network=json.loads(Path(lab['Network']).read_text())
        network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
        write(Path(lab['Network']),network)
        if mode!='direct':command([BIN/'payctl','e5-proxy','-dir',labdir,'-setup'],log)
    lab=json.loads((labdir/'lab.json').read_text())
    write(dest/'configuration.json',dict(mode=mode,rate=rate,count=count,seed=seed,warm=warm,window=window))
    shutil.copyfile(OUT/'build.json',dest/'build.json')
    procs={};logs=[]
    def spawn(name,args):
        log=(labdir/'logs'/(name+'.log')).open('w');logs.append(log)
        p=subprocess.Popen(list(map(str,args)),cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT);procs[name]=p;return p
    try:
        if mode!='direct':spawn('proxy',[BIN/'payctl','e5-proxy','-dir',labdir,'-mode',mode])
        for node in lab['Nodes']:spawn(node['Name'],[BIN/node['Binary'],'-config',node['Config']])
        wait_health(lab['Nodes']);time.sleep(1)
        if warm:
            p=spawn('warm',[BIN/'payctl','e5-load','-dir',labdir,'-count',warm,'-rate','200','-seed',seed]);p.wait(timeout=100)
            if p.returncode:raise RuntimeError('warmup failed')
            shutil.copyfile(labdir/'reports/fault-v4.json',dest/'warm.json')
        args=[BIN/'payctl','e5-load','-dir',labdir,'-count',count,'-offset',warm,'-rate',rate,'-seed',seed]
        if window:args+=['-window',str(window)+'s']
        p=spawn('load',args);deadline=time.monotonic()+max(count/rate,window)+110
        with (dest/'resources.jsonl').open('w') as samples:
            while p.poll() is None:
                ids=','.join(str(p.pid) for p in procs.values() if p.poll() is None)
                ps=subprocess.check_output(['ps','-o','pid=,pcpu=,rss=','-p',ids],text=True)
                samples.write(json.dumps({'NS':time.time_ns(),'ps':ps})+'\n');samples.flush()
                if time.monotonic()>deadline:raise TimeoutError('load exceeded deadline')
                time.sleep(1)
        if mode!='direct':write(dest/'gate.json',get('http://127.0.0.1:29000/stats'))
        if p.returncode:raise RuntimeError('load incomplete; inspect retained evidence')
        time.sleep(1)
    finally:
        for p in procs.values():
            if p.poll() is None:p.send_signal(signal.SIGINT)
        for p in procs.values():
            try:p.wait(timeout=35)
            except subprocess.TimeoutExpired:p.kill();p.wait()
        for log in logs:log.close()
        shutil.copytree(labdir/'reports',dest/'reports',dirs_exist_ok=True)
        shutil.copytree(labdir/'logs',dest/'node-logs',dirs_exist_ok=True)
    with (dest/'audit.log').open('w') as log:
        command([BIN/'payctl','audit','-dir',labdir],log)
        command([BIN/'payctl','fault-v4','-dir',labdir,'-audit'],log)
        if warm:
            shutil.copyfile(labdir/'reports/fault-v4.json',dest/'main.json')
            shutil.copyfile(dest/'warm.json',labdir/'reports/fault-v4.json')
            command([BIN/'payctl','fault-v4','-dir',labdir,'-audit'],log)
            shutil.copyfile(labdir/'reports/fault-audit.json',dest/'warm-audit.json')
            shutil.copyfile(dest/'main.json',labdir/'reports/fault-v4.json')
            command([BIN/'payctl','fault-v4','-dir',labdir,'-audit'],log)
    shutil.copytree(labdir/'reports',dest/'reports',dirs_exist_ok=True)
    audit=json.loads((dest/'reports/audit.json').read_text())
    assert len({a['StateHash'] for a in audit if a['Name'].startswith('committee')})==1
    assert all(a['Pending']==0 for a in audit)
    write(dest/'passed.json',{'passed':True,'count':count,'warm':warm})
    print(json.dumps({'case':label,'passed':True}),flush=True)

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--suite',choices=['smoke','calibration','formal'],default='smoke');p.add_argument('--skip-build',action='store_true');p.add_argument('--prefix',default='v1-');a=p.parse_args()
    if not a.skip_build:build()
    if a.suite=='smoke':
        for mode in ['A','B']:run_case(a.prefix+'smoke-'+mode,mode,20,20,warm=5)
    elif a.suite=='calibration':
        for i,seed in enumerate([23,37,59],1):
            for mode in (['direct','A'] if i%2 else ['A','direct']):run_case(a.prefix+f'cal-{mode}-{i}',mode,200,100,seed)
    else:
        for rate in [200,1000]:
            for i,seed in enumerate([23,37,59],1):
                for mode in (['A','B'] if i%2 else ['B','A']):run_case(a.prefix+f'{rate}-{mode}-{i}',mode,rate,rate*30,seed,window=30)
