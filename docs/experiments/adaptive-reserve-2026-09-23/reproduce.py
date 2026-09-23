#!/usr/bin/env python3
"""E2 same offered traffic: finite fixed backing versus public adaptive increments."""
import argparse
import importlib.util
import json
from pathlib import Path

OUT=Path(__file__).resolve().parent
ROOT=OUT.parents[2]
spec=importlib.util.spec_from_file_location('owner',OUT.parent/'owner-fuel-2026-09-23/reproduce.py')
owner=importlib.util.module_from_spec(spec);spec.loader.exec_module(owner)
e2=owner.e2
e2.OUT=OUT;e2.BIN=ROOT/'.run/adaptive-reserve-bin'
e2.labutil.OUT=OUT;e2.labutil.BIN=e2.BIN
adaptive=False
lead='800ms'
cap=14400
step=False

def configure(label,count,kind=None,grant=None):
    dest,runtime,lab,network=owner.configure(label,count,kind,grant)
    if adaptive:
        with (dest/'reserve-setup.log').open('w') as log:
            owner.base_command([e2.BIN/'payctl','budget-v4','-dir',runtime,'-prepare-reserve','-reserve-max',cap],log)
        network=json.loads(Path(lab['Network']).read_text())
        encoded=json.dumps(network,separators=(',',':'))+'\n'
        Path(lab['Network']).write_text(encoded);(dest/'network.json').write_text(encoded)
    (dest/'adaptive-policy.json').write_text(json.dumps({'enabled':adaptive,'cap':cap,'lead':lead,'gamma':1.25,'sample_ms':200,'burst':200,'unit':100,'stop_uncertified_age_s':30,'rate_first':10 if step else 20,'rate_second':30 if step else 20,'demand':'declared offered two-hop trace, 40 signed payments/s * 100 CAL; retries excluded'},indent=2)+'\n')
    return dest,runtime,lab,network

def command(args,log):
    if len(args)>1 and args[1]=='budget-v4':
        args=list(args)+['-owner-fuel']
        if step:args+=['-rate',10,'-rate-next',30]
        if adaptive:args+=['-reserve-max',cap,'-reserve-lead',lead]
    return owner.base_command(args,log)

e2.configure=configure;e2.command=command

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--case',choices=['smoke','low','high','adaptive','delay','delay-high','step','step-high'],default='smoke');p.add_argument('--skip-build',action='store_true');p.add_argument('--prefix',default='adaptive-r1-');p.add_argument('--lead',default='800ms')
    a=p.parse_args();lead=a.lead
    if not a.skip_build:e2.labutil.build()
    adaptive=a.case in ['smoke','adaptive','delay','step']
    step=a.case in ['step','step-high']
    cap=28800 if a.case in ['delay','delay-high'] else 21600 if step else 14400
    e2.run_case(a.prefix+a.case,duration=15 if a.case=='smoke' else 300,delay=3 if a.case in ['delay','delay-high'] else 1,kind=1,grant=cap if a.case in ['high','delay-high','step-high'] else 3600,gateway_parent=True)
