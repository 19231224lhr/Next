#!/usr/bin/env python3
"""E4: finite load, process pauses, and gates before actual message forwarding."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone, timedelta
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
BIN = ROOT / '.run/e4-bin'
ENV = dict(os.environ, PATH='/usr/local/go/bin:/opt/homebrew/bin:' + os.environ['PATH'])
ENV.update(UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
           UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_MEM_BLOCKSTORE='1',
           UTXO_EXPERIMENT_COMMITTEE_MEMORY='1', UTXO_EXPERIMENT_MEMBER_MEMORY='1',
           UTXO_EXPERIMENT_GATEWAY_MEMORY='1', UTXO_SETTLEMENT_TRACE='1',
           UTXO_EXPERIMENT_BUDGET='1', GOMAXPROCS='16', GOGC='200')
for k in ['UTXO_TRACE_ALL', 'UTXO_RUNTIME_TRACE', 'UTXO_EXPERIMENT_SERIAL_DIRECT']:
    ENV.pop(k, None)
PROXY = 'http://127.0.0.1:29000'


def write(path, value):
    path.write_text(json.dumps(value, separators=(',', ':')) + '\n')


def get(url, body=None, timeout=2):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers={'Content-Type': 'application/json'})
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=timeout) as r:
        raw = r.read()
    return {'alive': True} if raw.strip() == b'alive' else json.loads(raw)


def command(args, log):
    subprocess.run([str(x) for x in args], cwd=ROOT, env=ENV, stdout=log,
                   stderr=subprocess.STDOUT, check=True)


def build():
    BIN.mkdir(parents=True, exist_ok=True)
    with (OUT/'build.log').open('w') as log:
        command(['python3', ROOT/'third_party/cometbft/overlay.py'], log)
        for role in ['payctl', 'committee', 'gateway', 'member']:
            command(['go', 'build', '-tags=comet_v3', '-o', BIN/role, './cmd/'+role], log)
    write(OUT/'build.json', {'commit': subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),
          'binaries': {p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()},
          'sources': {str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in (ROOT/'cmd/payctl').glob('direct_fault*.go')},
          'environment': {k:v for k,v in ENV.items() if k.startswith('UTXO_') or k in ['GOGC','GOMAXPROCS']}})


def wait_until(predicate, seconds=40):
    deadline = time.monotonic()+seconds
    while time.monotonic()<deadline:
        try:
            value=predicate()
            if value:return value
        except (OSError, ValueError):pass
        time.sleep(.05)
    raise TimeoutError('condition not reached')


def run_case(label, kind, seed=23, rate=200, phases=(30,30,60), count=None):
    dest=OUT/label;dest.mkdir()
    if (OUT/'build.json').exists():shutil.copyfile(OUT/'build.json',dest/'build.json')
    labdir=ROOT/'.run'/('e4-'+label)
    cohort=count if count is not None else int(rate*sum(phases))
    with (dest/'setup.log').open('w') as log:
        command([BIN/'payctl','init-lab','-dir',labdir,'-outputs','1','-port','26000','-v4'],log)
        command([BIN/'payctl','fault-v4','-dir',labdir,'-prepare','-count',cohort*12 if kind=='C' else cohort],log)
    lab=json.loads((labdir/'lab.json').read_text())
    if kind!='C':lab['Nodes']=[x for x in lab['Nodes'] if x['Binary']=='committee' or x['Name']=='gateway0' or x['Name'].startswith('org0-member')]
    write(labdir/'lab.json',lab)
    network=json.loads(Path(lab['Network']).read_text());network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
    write(Path(lab['Network']),network)
    gateway_network=json.loads(json.dumps(network));gateway_network['CommitteeURLs']=[PROXY+'/'+str(i) for i in range(4,8)]
    org_key=network['Organizations'][0]['Org']
    gateway_network['Members'][org_key]=[PROXY+'/'+str(i) for i in range(4)]
    write(labdir/'config/network-gateway.json',gateway_network)
    cpath=labdir/'config/gateway0.json';c=json.loads(cpath.read_text());c['Network']=str(labdir/'config/network-gateway.json');c['Members']=[PROXY+'/'+str(i) for i in range(4)];write(cpath,c)
    write(dest/'configuration.json', {'kind':kind,'seed':seed,'rate':rate,'phases':phases,'count':cohort,'lab':lab})
    shutil.copyfile(lab['Network'],dest/'network.json')
    procs={};logs=[];events=[];paused=set();load=None;target=seed%4
    def event(name,**kw):
        events.append(dict(Event=name,NS=time.time_ns(),**kw));write(dest/'events.json',events)
    def spawn(name,args):
        log=(labdir/'logs'/(name+'.log')).open('w');logs.append(log)
        p=subprocess.Popen([str(x) for x in args],cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT);procs[name]=p;return p
    def pause(name):
        procs[name].send_signal(signal.SIGSTOP);paused.add(name)
        wait_until(lambda:'T' in subprocess.check_output(['ps','-o','stat=','-p',str(procs[name].pid)],text=True),3)
        event('paused',node=name,pid=procs[name].pid)
    def resume(name):
        procs[name].send_signal(signal.SIGCONT);paused.discard(name);event('resumed',node=name)
    def ready_count():
        path=labdir/'reports/fault-events.jsonl'
        return sum('"Kind":"ready"' in line for line in path.read_text().splitlines()) if path.exists() else 0
    def public_count():
        path=labdir/'reports/fault-events.jsonl'
        return sum('"Kind":"public"' in line for line in path.read_text().splitlines()) if path.exists() else 0
    def rule_for(k):
        block=[str(i)+'/v1/commands' for i in range(4,8)]
        if k=='B1':block += [str(i)+'/v3/certificates' for i in range(4)]
        if k=='B2':block += [str(i)+'/v3/certificates' for i in range(1,4)]
        if k=='B3a':block += [str(i)+'/v3/transactions' for i in range(4)]
        if k=='B3b':block += [str(i)+'/v3/transactions' for i in range(2,4)]
        return {'Block':block}
    try:
        spawn('proxy',[BIN/'payctl','fault-proxy','-dir',labdir])
        for node in lab['Nodes']:spawn(node['Name'],[BIN/node['Binary'],'-config',node['Config']])
        for node in lab['Nodes']:
            wait_until(lambda n=node: int(get(n['URL']+'/healthz').get('height',1))>=1,60)
        get(PROXY+'/control');time.sleep(2)
        event('healthy',pids={k:p.pid for k,p in procs.items()})
        if kind.startswith('B'):get(PROXY+'/control',rule_for(kind))
        if kind=='D':pause('org0-member0');pause('org0-member1')
        args=[BIN/'payctl','fault-v4','-dir',labdir,'-count',cohort,'-rate',rate,'-drain','60s']
        if kind=='C':args+=['-mode','conflicts']
        load=spawn('load',args)
        if kind=='C':
            load.wait(timeout=240);time.sleep(5)
        else:
            start=wait_until(lambda:json.loads((labdir/'reports/fault-start.json').read_text()),60)['StartedNS']
            if kind.startswith('A'):
                injected=released=False
                with (dest/'samples.jsonl').open('w') as samples,ThreadPoolExecutor(max_workers=9) as pool:
                    while load.poll() is None:
                        elapsed=(time.time_ns()-start)/1e9
                        if elapsed>=phases[0] and not injected:
                            if kind=='A1':get(PROXY+'/control',{'DelayMS':{str(target)+'/v3/transactions':250}})
                            if kind=='A2':pause('org0-member'+str(target))
                            event('fault_window_started',member=target);injected=True
                        if elapsed>=sum(phases[:2]) and not released:
                            if kind=='A1':get(PROXY+'/control',{})
                            if kind=='A2':resume('org0-member'+str(target))
                            event('fault_window_ended');released=True
                        def sample(node):
                            try:return {'Node':node['Name'],'Status':get(node['URL']+('/debug/budget' if node['Binary']=='member' else '/healthz'),timeout=.4)}
                            except Exception as e:return {'Node':node['Name'],'Error':str(e)}
                        values=list(pool.map(sample,[x for x in lab['Nodes'] if x['Binary']!='gateway']))
                        samples.write(json.dumps({'NS':time.time_ns(),'Nodes':values})+'\n');samples.flush()
                        if elapsed>sum(phases)+130:raise TimeoutError('load did not finish')
                        time.sleep(.6)
            elif kind=='D':
                time.sleep(3);assert ready_count()==0;event('no_quorum_verified');resume('org0-member0');resume('org0-member1');load.wait(timeout=60)
            else:
                if kind in ['B1','B2']:wait_until(lambda:ready_count()==cohort,15)
                if kind=='B2':wait_until(lambda:get(PROXY+'/control')['Counts'].get('0/v3/certificates',{}).get('OK',0)>=cohort,5)
                if kind=='B3a':wait_until(lambda:get(PROXY+'/control')['Counts'].get('0/v3/transactions',{}).get('Blocked',0)>0,5)
                if kind=='B3b':wait_until(lambda:all(get(PROXY+'/control')['Counts'].get(str(i)+'/v3/transactions',{}).get('OK',0)>0 for i in [0,1]),5)
                before=get(PROXY+'/control')['Counts']
                assert all(before.get(str(i)+'/v1/commands',{}).get('Forwarded',0)==0 for i in range(4,8))
                if kind=='B1':assert all(before.get(str(i)+'/v3/certificates',{}).get('Forwarded',0)==0 for i in range(4))
                if kind=='B2':assert all(before.get(str(i)+'/v3/certificates',{}).get('Forwarded',0)==0 for i in range(1,4))
                if kind in ['B3a','B3b']:assert all(before.get(str(i)+'/v3/transactions',{}).get('Forwarded',0)==0 for i in range(0 if kind=='B3a' else 2,4))
                pause('gateway0');event('boundary',ready=ready_count(),public=public_count(),proxy=get(PROXY+'/control'))
                # Member URLs are unproxied: backup publication remains reachable.
                time.sleep(10)
                event('stopped_window_end',ready=ready_count(),public=public_count(),proxy=get(PROXY+'/control'),committee=get(network['CommitteeURLs'][0]+'/healthz'))
                if kind=='B2':assert public_count()==cohort, 'installed member did not take over'
                if kind in ['B1','B3a','B3b']:assert public_count()==0, 'unexpected uncontrolled publication'
                if kind in ['B3a','B3b']:assert ready_count()==0, 'QC without sufficient forwarded approvals'
                get(PROXY+'/control',{});resume('gateway0');load.wait(timeout=65)
        event('load_end',returncode=load.returncode,proxy=get(PROXY+'/control'))
        if load.returncode:raise RuntimeError('E4 load failed; evidence retained')
        time.sleep(1)
    finally:
        for name in list(paused):resume(name)
        for name,p in procs.items():
            if p.poll() is None:p.send_signal(signal.SIGINT)
        for name,p in procs.items():
            try:p.wait(timeout=35)
            except subprocess.TimeoutExpired:p.kill();p.wait();event('forced_stop',node=name)
        for log in logs:log.close()
        shutil.copytree(labdir/'reports',dest/'reports',dirs_exist_ok=True)
        shutil.copytree(labdir/'logs',dest/'node-logs',dirs_exist_ok=True)
    with (dest/'audit.log').open('w') as log:
        command([BIN/'payctl','audit','-dir',labdir],log)
        args=[BIN/'payctl','fault-v4','-dir',labdir,'-audit']
        if kind=='C':args+=['-mode','conflicts']
        command(args,log)
    shutil.copytree(labdir/'reports',dest/'reports',dirs_exist_ok=True)
    audits=json.loads((dest/'reports/audit.json').read_text());committees=[x for x in audits if x['Name'].startswith('committee')]
    assert len({x['StateHash'] for x in committees})==1
    assert all(x['Pending']==0 for x in audits)
    write(dest/'passed.json',{'label':label,'kind':kind,'committee_payments':committees[0]['Payments'],'all_audits_passed':True})
    print(json.dumps({'label':label,'passed':True}),flush=True)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--suite',choices=['smoke','controls','conflicts','pilot','formal'],default='smoke');p.add_argument('--skip-build',action='store_true');p.add_argument('--prefix',default='v1-');a=p.parse_args()
    if not a.skip_build:build()
    if a.suite=='smoke':
        run_case(a.prefix+'smoke','A0',rate=20,phases=(1,1,2),count=80)
    elif a.suite=='controls':
        for repeat,seed in enumerate([23,37,59],1):
            for kind in ['B1','B2','B3a','B3b','D']:run_case(a.prefix+kind+'-r'+str(repeat),kind,seed=seed,count=1)
        run_case(a.prefix+'conflicts','C',count=20)
    elif a.suite=='conflicts':
        run_case(a.prefix+'conflicts','C',count=20)
    elif a.suite=='pilot':
        for kind in ['A0','A1','A2']:run_case(a.prefix+kind+'-pilot',kind,rate=200,phases=(3,3,6))
    else:
        for repeat,seed in enumerate([23,37,59],1):
            order=['A0','A1','A2'];order=order[repeat-1:]+order[:repeat-1]
            for kind in order:run_case(a.prefix+kind+'-r'+str(repeat),kind,seed=seed)
