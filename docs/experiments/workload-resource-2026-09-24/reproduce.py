#!/usr/bin/env python3
"""E8 fresh-state single-host workload sensitivity; no protocol parameter sweep."""
import argparse, gzip, hashlib, importlib.util, json, os, shutil, signal, subprocess, time
from pathlib import Path
from datetime import datetime, timezone, timedelta

OUT=Path(__file__).resolve().parent
ROOT=OUT.parents[2]
spec=importlib.util.spec_from_file_location('e5',OUT.parent/'delivery-gate-2026-09-24/reproduce.py')
labutil=importlib.util.module_from_spec(spec);spec.loader.exec_module(labutil)
labutil.OUT=OUT;labutil.BIN=ROOT/'.run/e8-bin'
labutil.ENV.pop('UTXO_E5_OBSERVE',None)
labutil.ENV.update(UTXO_E8_METRICS='1',UTXO_E8_LARGE_GENESIS='1')
BIN,ENV=labutil.BIN,labutil.ENV

def write(path,x): labutil.write(path,x)
def cmd(args,log): labutil.command(args,log)
def stats(nodes,state=False):
    return {n['Name']:labutil.get(n['URL']+'/debug/e8'+('?state=1' if state else '')) for n in nodes}

def fresh_fixture(runtime,log):
    template=ROOT/'.run/e8-template'
    if not template.exists():cmd([BIN/'payctl','init-lab','-dir',template,'-outputs',1,'-port',26000,'-v4'],log)
    # The template has never booted: include initial Comet node/PV files too.
    shutil.copytree(template,runtime)
    for path in runtime.rglob('*.json'):
        path.write_text(path.read_text().replace(str(template),str(runtime)))

def run(label,mode,rate,duration,seed=23,warm=2048,surge=False,metrics=True):
    dest=OUT/label;dest.mkdir()
    runtime=ROOT/'.run'/('e8-'+label)
    ENV['UTXO_E8_METRICS']='1' if metrics else '0'
    capacity=rate*(210 if surge else duration)+warm+10000
    with (dest/'setup.log').open('w') as log:
        fresh_fixture(runtime,log)
        cmd([BIN/'payctl','e8','-dir',runtime,'-prepare','-wallets',2 if mode=='A' else 2048,'-capacity',capacity,'-seed',seed],log)
    lab=json.loads((runtime/'lab.json').read_text())
    lab['Nodes']=[n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')]
    write(runtime/'lab.json',lab)
    network_path=Path(lab['Network'])
    network=json.loads(network_path.read_text())
    network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
    write(network_path,network)
    write(dest/'configuration.json',dict(mode=mode,rate=rate,duration=duration,seed=seed,warm=warm,surge=surge,capacity=capacity,metrics=metrics,origins=len(network['Genesis']['Outputs']),network_sha256=hashlib.sha256(network_path.read_bytes()).hexdigest()))
    del network
    shutil.copyfile(OUT/'build.json',dest/'build.json')
    procs={};logs=[];passed=False
    def spawn(name,args):
        log=(runtime/'logs'/(name+'.log')).open('w');logs.append(log)
        p=subprocess.Popen(list(map(str,args)),cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT);procs[name]=p;return p
    try:
        for n in lab['Nodes']:spawn(n['Name'],[BIN/n['Binary'],'-config',n['Config']])
        for n in lab['Nodes']:
            deadline=time.monotonic()+240
            while True:
                try:labutil.get(n['URL']+'/healthz');break
                except Exception:
                    if procs[n['Name']].poll() is not None or time.monotonic()>deadline:raise
                    time.sleep(.3)
        if metrics:write(dest/'metrics-initial.json',stats(lab['Nodes'],True))
        args=[BIN/'payctl','e8','-dir',runtime,'-rate',rate,'-duration',f'{duration}s','-warm',warm,'-seed',seed]
        if mode=='C':args+=['-chain']
        if surge:args+=['-surge']
        p=spawn('load',args)
        deadline=time.monotonic()+(195 if surge else duration)+warm/200+120
        base=False;started=None;last=0
        with (dest/'resources.jsonl').open('w') as out:
            while p.poll() is None:
                if started is None and (runtime/'reports/e8-start.json').exists():
                    started=json.loads((runtime/'reports/e8-start.json').read_text());write(dest/'start.json',started)
                now=time.time_ns()
                if started and not base and now>=started['FormalNS']:
                    if metrics:write(dest/'metrics-formal-start.json',{'NS':now,'Nodes':stats(lab['Nodes'])})
                    base=True
                if time.monotonic()-last>=1:
                    ids=','.join(str(x.pid) for x in procs.values() if x.poll() is None)
                    ps=subprocess.check_output(['ps','-o','pid=,time=,rss=','-p',ids],text=True)
                    out.write(json.dumps({'NS':now,'ps':ps,'pids':{k:v.pid for k,v in procs.items()}})+'\n');out.flush();last=time.monotonic()
                if any(procs[n['Name']].poll() is not None for n in lab['Nodes']):raise RuntimeError('node exited')
                if time.monotonic()>deadline:raise TimeoutError('experiment exceeded deadline')
                time.sleep(.2)
        write(dest/'load-exit.json',{'returncode':p.returncode,'NS':time.time_ns()})
        if metrics:write(dest/'metrics-final.json',stats(lab['Nodes'],True))
        passed=p.returncode==0
    finally:
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
            cmd([BIN/'payctl','e8','-dir',runtime,'-audit'],log)
        shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
        audit=json.loads((dest/'reports/audit.json').read_text())
        assert len({x['StateHash'] for x in audit if x['Name'].startswith('committee')})==1
        assert all(x['Pending']==0 for x in audit)
        write(dest/'passed.json',{'correctness_audit':True})
        # Only this script's fresh runtime is disposable, after evidence and audits exist.
        resolved=runtime.resolve();assert resolved.parent==(ROOT/'.run').resolve() and resolved.name=='e8-'+label
        shutil.rmtree(resolved)
    for path in dest.rglob('*.json'):
        if path.stat().st_size>2_000_000:
            with path.open('rb') as src,gzip.open(str(path)+'.gz','wb') as dst:shutil.copyfileobj(src,dst)
            path.unlink()
    print(json.dumps({'case':label,'passed':passed}),flush=True)
    if not passed:raise RuntimeError('experiment incomplete; evidence retained')

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--label',required=True);p.add_argument('--mode',choices=['A','B','C'],default='B');p.add_argument('--rate',type=int,default=500);p.add_argument('--duration',type=int,default=300);p.add_argument('--seed',type=int,default=23);p.add_argument('--warm',type=int,default=2048);p.add_argument('--surge',action='store_true');p.add_argument('--skip-build',action='store_true');p.add_argument('--no-metrics',action='store_true');a=p.parse_args()
    if not a.skip_build:labutil.build()
    run(a.label,a.mode,a.rate,a.duration,a.seed,a.warm,a.surge,not a.no_metrics)
