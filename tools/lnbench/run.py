#!/usr/bin/env python3
"""Run an already deployed case; preserve raw measurements and process samples."""
import argparse
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]

def drain(case, out):
    from lab import LNCLI, command
    run=ROOT/'.run'/('ln-'+case)
    nodes=json.loads((run/'manifest.json').read_text())['nodes']
    start=time.monotonic()
    while True:
        snapshots=[command([LNCLI,'--network=regtest','--lnddir='+str(run/f'node{i}'),
                    f'--rpcserver=127.0.0.1:{19009+i}','listchannels']) for i in range(nodes)]
        pending=sum(len(c.get('pending_htlcs',[])) for n in snapshots for c in n['channels'])
        if pending==0 or time.monotonic()-start>60:
            out.write_text(json.dumps({'wait_seconds':time.monotonic()-start,'pending':pending,'channels':snapshots},indent=2))
            if pending:raise RuntimeError('HTLCs did not drain; no next case started')
            return
        time.sleep(.2)

def trial(case, label, extra):
    run = ROOT / '.run' / ('ln-' + case)
    out = ROOT / 'docs/experiments/lightning-2026-09-24' / case / label
    if out.exists():
        raise RuntimeError('result already exists: ' + str(out))
    out.mkdir(parents=True)
    drain(case,out/'quiescent-before.json')
    pids = json.loads((out.parent / 'deployment.json').read_text())['pids']
    args = [str(ROOT / '.run/lnbench'), '-config', str(run / 'endpoints.json'), '-out', str(out)] + extra
    (out / 'command.json').write_text(json.dumps(args))
    with (out / 'driver.log').open('w') as log, (out / 'resources.jsonl').open('w') as resources:
        proc = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT)
        start = time.monotonic()
        while proc.poll() is None:
            ps = subprocess.run(['ps', '-o', 'pid=,%cpu=,rss=,time=', '-p', ','.join(map(str,pids+[proc.pid]))],
                                capture_output=True, text=True)
            resources.write(json.dumps({'seconds':time.monotonic()-start,'ps':ps.stdout})+'\n')
            resources.flush()
            time.sleep(1)
        if proc.returncode:
            raise RuntimeError(f'{label}: driver failed; see {out}/driver.log')
    result = json.loads((out / 'summary.json').read_text())
    drain(case,out/'quiescent-after.json')
    print(label, json.dumps(result), flush=True)
    return result

def main():
    p = argparse.ArgumentParser()
    p.add_argument('case')
    p.add_argument('--mode', choices=['pilot','formal','chain'], default='pilot')
    p.add_argument('--rate', type=float, default=50)
    a = p.parse_args()
    if a.mode == 'chain':
        trial(a.case,'chain100',['-chain','-amount','950000','-count','100'])
        return
    trial(a.case,'warmup',['-count','20'])
    trial(a.case,'serial100',['-count','100'])
    if a.mode == 'formal':
        trial(a.case,'sustained',['-rate',str(a.rate),'-duration','180s'])
        return
    for rate in [5,20,50,100,200,500,1000]:
        r = trial(a.case,f'rate{rate}',['-rate',str(rate),'-duration','10s'])
        if r['Succeeded'] != r['Planned'] or r['DispatchMS']['P95'] > 20 or r['ElapsedSeconds'] > 12:
            print('presweep boundary; inspect raw failures/queues before increasing load',flush=True)
            break

if __name__ == '__main__':
    main()
