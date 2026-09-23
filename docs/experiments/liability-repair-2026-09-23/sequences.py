#!/usr/bin/env python3
"""Bounded real-network missing-parent and cross-organization sequences."""
import argparse
from datetime import datetime, timezone, timedelta
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import signal
import subprocess
import time

OUT=Path(__file__).resolve().parent
ROOT=OUT.parents[2]
spec=importlib.util.spec_from_file_location('base',OUT.parent/'finite-budget-2026-09-23/reproduce.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)
BIN=ROOT/'.run/e3-sequence-bin'
ENV=dict(base.ENV,UTXO_EXPERIMENT_MEM_BLOCKSTORE='0',UTXO_EXPERIMENT_COMMITTEE_MEMORY='0',UTXO_EXPERIMENT_SERIAL_DIRECT='0')


def command(args,log):
    subprocess.run([str(x) for x in args],cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT,check=True)


def build():
    BIN.mkdir(exist_ok=True)
    for role in ['committee','gateway','member','member-real']:
        shutil.copy2(ROOT/'.run/e3-bin'/role,BIN/role)
    with (OUT/'sequence-build.log').open('w') as log:
        command(['go','build','-tags=comet_v3','-o',BIN/'payctl','./cmd/payctl'],log)


def run(mode,index,prefix):
    label=f'{prefix}{mode}-{index}'
    dest=OUT/label;dest.mkdir()
    runtime=ROOT/'.run'/('e3-'+label)
    with (dest/'setup.log').open('w') as log:
        command([BIN/'payctl','init-lab','-dir',runtime,'-outputs',1,'-port',28000,'-v4'],log)
        command([BIN/'payctl','liability-v4','-dir',runtime,'-case',mode,'-prepare'],log)
    lab=json.loads((runtime/'lab.json').read_text())
    if mode=='missing':
        lab['Nodes']=[n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')]
    base.write(runtime/'lab.json',lab)
    network=json.loads(Path(lab['Network']).read_text())
    network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
    base.write(Path(lab['Network']),network);base.write(dest/'network.json',network)
    base.write(dest/'configuration.json',{'case':mode,'timeout_s':90,'user_fuel':True,'cal_grant_each_org':60000,
        'code_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),
        'driver_sha256':hashlib.sha256((ROOT/'cmd/payctl/direct_liability.go').read_bytes()).hexdigest(),
        'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()}})
    with (dest/'nodes.log').open('w') as log:
        proc=subprocess.Popen([str(BIN/'payctl'),'lab-run','-dir',str(runtime),'-bin',str(BIN)],cwd=ROOT,env=ENV,stdout=log,stderr=subprocess.STDOUT)
        try:
            end=time.monotonic()+60
            while True:
                if proc.poll() is not None:raise RuntimeError('nodes exited')
                try:
                    health={n['Name']:base.get(n['URL']+'/healthz') for n in lab['Nodes']}
                    if all(int(h.get('height',0))>=1 for k,h in health.items() if k.startswith('committee')):break
                except (OSError,ValueError):pass
                if time.monotonic()>end:raise TimeoutError('node health')
                time.sleep(.2)
            with (dest/'driver.log').open('w') as driver:
                command([BIN/'payctl','liability-v4','-dir',runtime,'-case',mode],driver)
            time.sleep(1)
            base.write(dest/'last-snapshots.json',{n['Name']:base.get(n['URL']+'/debug/budget?detail=1') for n in lab['Nodes'] if n['Binary'] in ['committee','member']})
        finally:
            if proc.poll() is None:proc.send_signal(signal.SIGINT)
            try:proc.wait(timeout=60)
            except subprocess.TimeoutExpired:proc.kill();proc.wait();raise
            if (runtime/'reports').exists():shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
            shutil.copytree(runtime/'logs',dest/'node-logs',dirs_exist_ok=True)
    with (dest/'audit.log').open('w') as log:
        command([BIN/'payctl','audit','-dir',runtime],log)
        command([BIN/'payctl','liability-v4','-dir',runtime,'-case',mode,'-audit'],log)
    shutil.copytree(runtime/'reports',dest/'reports',dirs_exist_ok=True)
    print(json.dumps({'case':label,'passed':True,'audit':'four stopped committee stores and real BlockStore revisions'}),flush=True)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--skip-build',action='store_true');p.add_argument('--prefix',default='seq-r1-')
    a=p.parse_args()
    if not a.skip_build:build()
    for i in range(3):
        for mode in ['missing','cross']:run(mode,i,a.prefix)
