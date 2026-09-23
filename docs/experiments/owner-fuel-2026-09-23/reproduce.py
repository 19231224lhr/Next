#!/usr/bin/env python3
"""E2 rerun: final user FUEL, no sponsor grants/accounts; reuse identical E2 driver."""
import argparse
import importlib.util
import json
from pathlib import Path

OUT=Path(__file__).resolve().parent
ROOT=OUT.parents[2]
spec=importlib.util.spec_from_file_location('e2',OUT.parent/'finite-budget-2026-09-23/reproduce.py')
e2=importlib.util.module_from_spec(spec);spec.loader.exec_module(e2)
e2.OUT=OUT;e2.BIN=ROOT/'.run/owner-fuel-bin'
e2.labutil.OUT=OUT;e2.labutil.BIN=e2.BIN
base_configure=e2.configure
base_command=e2.command

def configure(label,count,kind=None,grant=None):
    dest,runtime,lab,network=base_configure(label,count,kind,grant)
    with (dest/'owner-fuel-setup.log').open('w') as log:
        base_command([e2.BIN/'payctl','budget-v4','-dir',runtime,'-prepare-owner-fuel'],log)
    network=json.loads(Path(lab['Network']).read_text())
    # Final fee origins enlarge genesis; compact JSON stays within the existing reader limit.
    encoded=json.dumps(network,separators=(',',':'))+'\n'
    Path(lab['Network']).write_text(encoded)
    (dest/'network.json').write_text(encoded)
    assert not any(g['Key']['Kind'] in [2,5] for g in network['Genesis']['Grants'])
    assert all(a['Balance']==0 for a in network['Accounts'] if a['Asset']==2)
    return dest,runtime,lab,network

def command(args,log):
    if len(args)>1 and args[1]=='budget-v4':args=list(args)+['-owner-fuel']
    return base_command(args,log)

e2.configure=configure;e2.command=command

def run(label,**kwargs):
    dest=e2.run_case(label,gateway_parent=True,**kwargs)
    report=json.loads((dest/'reports/budget-v4.json').read_text());assert report['OwnerFuel']
    audit=json.loads((dest/'reports/budget-audit.json').read_text())
    assert all(p['FeeSource']==2 and (p['Refund']==0 or p['WalletRefundFinal']) for p in audit['Payments'] if p['Public'])
    return dest

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--case',choices=['probe','fee','matrix','gate'],default='probe');p.add_argument('--skip-build',action='store_true');p.add_argument('--prefix',default='owner-r1-')
    a=p.parse_args()
    if not a.skip_build:e2.labutil.build()
    if a.case=='probe':run(a.prefix+'probe',duration=5,delay=3,count=1)
    elif a.case=='fee':
        run(a.prefix+'fee-normal',duration=5,delay=3,count=1,replay=True)
        run(a.prefix+'fee-repair',duration=45,delay=35,count=1,late=True,replay=True)
    elif a.case=='gate':
        for kind,grant in [(1,3600),(3,1122)]:run(a.prefix+'gate-k'+str(kind),kind=kind,grant=grant,serial=True)
    else:
        for kind,grants in [(1,[3600,7200,14400]),(3,[1122,2244,4488])]:
            for name,grant in zip(['low','medium','high'],grants):run(a.prefix+'k'+str(kind)+'-'+name,kind=kind,grant=grant)
        run(a.prefix+'k1-medium-delay3',kind=1,grant=7200,delay=3)
