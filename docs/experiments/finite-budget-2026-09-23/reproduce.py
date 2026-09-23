#!/usr/bin/env python3
"""E2: independent two-hop units, finite grants, unchanged payment rules."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone, timedelta
import importlib.util
import json
import hashlib
import math
import os
from pathlib import Path
import shutil
import signal
import subprocess
import threading
import time

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
BIN = ROOT / '.run/budget-bin'
spec = importlib.util.spec_from_file_location('chain_lab', OUT.parent/'continuous-respending-2026-09-22/reproduce.py')
labutil = importlib.util.module_from_spec(spec)
spec.loader.exec_module(labutil)
labutil.OUT, labutil.BIN = OUT, BIN
ENV = labutil.ENV
ENV['UTXO_EXPERIMENT_BUDGET'] = '1'
write, get, command = labutil.write, labutil.get, labutil.command


def configure(label, count, kind=None, grant=None):
    dest = OUT / label
    dest.mkdir()
    runtime = ROOT/'.run'/('budget-'+label)
    template = ROOT/'.run/budget-pristine-template'
    with (dest/'setup.log').open('w') as log:
        if not template.exists():
            command([BIN/'payctl','init-lab','-dir',template,'-outputs',6000,'-port',27000,'-v4'],log)
            command([BIN/'payctl','chain-v4','-dir',template,'-prepare'],log)
            (template/'prepared').write_text('pristine E2: no payments\n')
        assert (template/'prepared').exists()
        shutil.copytree(template,runtime)
    for p in [runtime/'lab.json',*list((runtime/'config').glob('*.json'))]:
        p.write_text(p.read_text().replace(str(template),str(runtime)))
    lab=json.loads((runtime/'lab.json').read_text())
    lab['Nodes']=[n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')]
    write(runtime/'lab.json',lab)
    network=json.loads(Path(lab['Network']).read_text())
    network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
    org=network['Organizations'][0]['Org']
    config=network['Genesis']['Grants'][0]['Organization']
    for g in network['Genesis']['Grants']:
        if g['Organization']==config and g['Key']['Kind']==kind: g['Amount']=grant
    if kind==1:
        for a in network['Accounts']:
            if a['Owner']==org and a['Asset']==1:a['Balance']=grant
    for p in (runtime/'config').glob('*.json'):
        obj=json.loads(p.read_text())
        if 'Workers' in obj:obj['Workers']=1;write(p,obj)
    write(Path(lab['Network']),network)
    write(dest/'network.json',network)
    return dest,runtime,lab,network


def sample(lab, dest, stop, probe=False):
    nodes=[n for n in lab['Nodes'] if n['Name']=='committee0' or n['Name'].startswith('org0-member')]
    interval=.05 if probe else .5
    detail_interval=.1 if probe else 10
    def read(n,detail):
        started=time.time_ns()
        try:return {'node':n['Name'],'requested_ns':started,'snapshot':get(n['URL']+'/debug/budget'+('?detail=1' if detail else ''))}
        except Exception as e:return {'node':n['Name'],'requested_ns':started,'error':str(e)}
    with (dest/'samples.jsonl').open('w') as log, ThreadPoolExecutor(max_workers=5) as pool:
        next_detail=0
        while not stop.is_set():
            now=time.monotonic();detail=now>=next_detail
            if detail:next_detail=now+detail_interval
            for row in pool.map(lambda n:read(n,detail),nodes):log.write(json.dumps(row)+'\n')
            log.flush();stop.wait(max(0,interval-(time.monotonic()-now)))


def run_case(label, duration=300, delay=1, kind=None, grant=None, count=0, late=False, replay=False, gateway_parent=False, serial=False, drain=60, disk=False):
    dest,runtime,lab,network=configure(label,count,kind,grant)
    node_env=dict(ENV)
    node_env['UTXO_EXPERIMENT_SERIAL_DIRECT']='1' if serial else '0'
    disk = disk or replay
    if disk:
        # Two-payment correctness controls retain actual revised blocks for the
        # existing stopped-history audit. The turnover matrix stays in memory.
        node_env['UTXO_EXPERIMENT_MEM_BLOCKSTORE']='0'
        node_env['UTXO_EXPERIMENT_COMMITTEE_MEMORY']='0'
    write(dest/'configuration.json',{'duration_s':duration,'delay_s':delay,'kind':kind,'grant':grant,'rate_units_s':20,'count':count,'pending_units':128,'workers_per_member':1,'drain_s':drain,'blockstore_memory':not disk,'gateway_parent':gateway_parent,'serial_signing':serial,'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()}})
    stop=threading.Event();sampler=None
    with (dest/'nodes.log').open('w') as log:
        process=subprocess.Popen([str(BIN/'payctl'),'lab-run','-dir',str(runtime),'-bin',str(BIN)],cwd=ROOT,env=node_env,stdout=log,stderr=subprocess.STDOUT)
        try:
            end=time.monotonic()+60
            while True:
                if process.poll() is not None:raise RuntimeError('laboratory exited')
                try:
                    health={n['Name']:get(n['URL']+'/healthz') for n in lab['Nodes']}
                    if all(int(h.get('height',0))>=1 for k,h in health.items() if k.startswith('committee')):break
                except (OSError,ValueError):pass
                if time.monotonic()>end:raise TimeoutError('health')
                time.sleep(.2)
            time.sleep(3)
            sampler=threading.Thread(target=sample,args=(lab,dest,stop,count==1));sampler.start()
            time.sleep(.2)
            with (dest/'driver.log').open('w') as driver:
                args=[BIN/'payctl','budget-v4','-dir',runtime,'-duration',str(duration)+'s','-parent-delay',str(delay)+'s','-count',count,'-pending',128,'-drain',str(drain)+'s']
                if gateway_parent:args+=['-gateway-parent']
                if late:args+=['-late-parent']
                if replay:args+=['-replay']
                command(args,driver)
            time.sleep(1)
            write(dest/'last-snapshots.json',{n['Name']:get(n['URL']+'/debug/budget?detail=1') for n in lab['Nodes'] if n['Binary'] in ['committee','member']})
            write(dest/'health-after.json',{n['Name']:get(n['URL']+'/healthz') for n in lab['Nodes']})
        finally:
            stop.set()
            if sampler:sampler.join()
            if process.poll() is None:process.send_signal(signal.SIGINT)
            try:process.wait(timeout=60)
            except subprocess.TimeoutExpired:process.kill();process.wait();raise
            if (runtime/'reports').exists():shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
            shutil.copytree(runtime/'logs',dest/'node-logs',dirs_exist_ok=True)
    with (dest/'audit.log').open('w') as log:
        command([BIN/'payctl','audit','-dir',runtime],log)
        command([BIN/'payctl','budget-audit','-dir',runtime],log)
    shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
    audit=json.loads((dest/'reports/audit.json').read_text())
    committee=[n for n in audit if n['Name'].startswith('committee')]
    assert len({n['StateHash'] for n in committee})==1,'committee divergence'
    report=json.loads((dest/'reports/budget-v4.json').read_text())
    summary={'label':label,'offered':report['Offered'],'admitted':report['Admitted'],'not_started':report['NotStarted'],
             'parent_ready':sum(u['Parent']['ReadyUnixNS']>0 for u in report['Units']),
             'child_ready':sum(u['Child']['ReadyUnixNS']>0 for u in report['Units']),
             'closed_units':sum(u['Parent']['MemberClosedUnixNS']>0 and u['Child']['MemberClosedUnixNS']>0 for u in report['Units']),
             'payments':committee[0]['Payments'],'closed_payments':committee[0]['Closed'],'gap':committee[0]['Gap']}
    # Timers use Go's monotonic clock. Wall-clock corrections must not create
    # a false early-publication assertion.
    assert all(u['FirstSubmitUnixNS']==0 or u['FirstSubmitDelayNS']>=int(delay*1e9) for u in report['Units'])
    if count==1:
        late = late or report['Units'][0].get('RepairExpected', False)
        samples=[json.loads(line) for line in (dest/'samples.jsonl').read_text().splitlines()]
        public=[r['snapshot'] for r in samples if r['node']=='committee0' and 'snapshot' in r]
        assert max(r['Usage']['Reserved'] for s in public for r in s['Resources'] if r['Key']['Kind']==1)==100,'no real CAL registration'
        assert any(s.get('Detail',{}).get('Open',0)==1 for s in public),'no actual missing-input responsibility'
        final=json.loads((dest/'last-snapshots.json').read_text())['committee0']
        assert final['Detail']['Fulfilled']==int(not late) and final['Detail']['Repaired']==int(late)
        assert all(r['Usage']=={'Reserved':0,'Spent':100 if late else 0} for r in final['Resources'] if r['Key']['Kind']==1)
        assert summary['closed_units']==1
        members=[r['snapshot'] for r in samples if r['node'].startswith('org0-member') and 'snapshot' in r]
        assert max(s['Reserved'] for x in members for r in x['Resources'] if r['Key']['Kind']==1 for s in r['Slices'])>=100
        assert final['Detail']['Fee']['Maximum']==2000
        assert final['Detail']['Fee']['Rewards']+final['Detail']['Fee']['Burned']==188+(5 if late else 0)
    write(dest/'summary.json',summary);print(json.dumps(summary),flush=True)
    return dest


def calibrate(dest):
    rows=[json.loads(line) for line in (dest/'samples.jsonl').read_text().splitlines()]
    def percentile(v,p):return sorted(v)[min(len(v)-1,int((len(v)-1)*p))]
    references={}
    for kind in [1,3]:
        values={}
        for row in rows:
            if not row['node'].startswith('org0-member') or 'snapshot' not in row:continue
            for r in row['snapshot']['Resources']:
                if r['Key']['Kind']==kind:values.setdefault(row['node'],[]).append(sum(s['Reserved'] for s in r['Slices']))
        references[kind]=max(percentile(v,.95) for v in values.values())
    fuel_transient=[]
    for row in rows:
        if not row['node'].startswith('org0-member') or 'snapshot' not in row:continue
        for d in row['snapshot'].get('Detail',{}).get('Debits',[]):
            if d['Key']['Kind']==2:fuel_transient.append(d['Charged']-d['Released']-d['NetSpent'])
    fuel=20*300*2*94+percentile(fuel_transient,.95)
    levels={}
    for kind,ref in references.items():
        levels[str(kind)]=[math.ceil(max(200 if kind==1 else 44,ref*f)*3/2) for f in [.5,1,2]]
    levels['2']=[math.ceil(fuel*f*3/2) for f in [.6,1,1.5]]
    result={'reference_member_p95':references,'fuel_member_reference':fuel,'fuel_transient_p95':percentile(fuel_transient,.95),'grants':levels}
    write(OUT/'calibration.json',result);return result


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--case',choices=['probe','calibration','window-check','matrix','fee'],default='probe');p.add_argument('--skip-build',action='store_true');p.add_argument('--prefix',default='')
    args=p.parse_args()
    if not args.skip_build:labutil.build()
    if args.case=='probe':run_case(args.prefix+'probe',duration=5,delay=3,count=1)
    elif args.case=='calibration':calibrate(run_case(args.prefix+'calibration',duration=30))
    elif args.case=='window-check':
        dest=run_case(args.prefix+'window-check',duration=30,delay=3)
        assert json.loads((dest/'summary.json').read_text())['not_started']==0,'driver window limits high-budget delay control'
    elif args.case=='fee':
        run_case(args.prefix+'fee-normal',duration=5,delay=3,count=1,replay=True)
        run_case(args.prefix+'fee-repair',duration=45,delay=35,count=1,late=True,replay=True)
    else:
        c=json.loads((OUT/'calibration.json').read_text())
        for kind in [1,2,3]:
            for name,grant in zip(['low','medium','high'],c['grants'][str(kind)]):run_case(f'{args.prefix}k{kind}-{name}',kind=kind,grant=grant)
        run_case(args.prefix+'k1-medium-delay3',kind=1,grant=c['grants']['1'][1],delay=3)
