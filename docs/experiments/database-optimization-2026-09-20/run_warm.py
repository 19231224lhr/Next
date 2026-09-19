"""Each fresh lab warms with 1,200 payments, then measures another 300."""
import json, os, shutil, signal, subprocess, time, sys
from pathlib import Path
root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
out = root/'docs/experiments/database-optimization-2026-09-20'
followup = '--followup' in sys.argv
log = (root/'.run'/('db-warm-followup.log' if followup else 'db-warm.log')).open('w',buffering=1)
os.dup2(log.fileno(),1); os.dup2(log.fileno(),2)
old = Path('/Users/richz/lab/man/utxo-fastpay')
oldcmd = f'{old}/bin/payctl lab-run -dir {old}/experiments/fresh-active-001 -bin {old}/bin'
rows = subprocess.check_output(['ps','-axo','pid=,command='],text=True).splitlines()
pids = [int(r.strip().split(None,1)[0]) for r in rows if r.strip().split(None,1)[-1]==oldcmd]
assert len(pids)<=1
env = dict(os.environ, UTXO_BENCH_WARMUP='1200', UTXO_BENCH_COUNT='300')
for k in ['UTXO_SETTLEMENT_TRACE','UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE','UTXO_COMET_PROFILE']: env.pop(k,None)
try:
    if pids:
        os.kill(pids[0],signal.SIGINT)
        for _ in range(35):
            try: os.kill(pids[0],0)
            except ProcessLookupError: break
            time.sleep(1)
        else: raise RuntimeError('old lab did not stop')
    for stage in (['map','credit'] if followup else ['prepare','credit','map']):
        label=('warm2-' if followup else 'warm-')+stage
        subprocess.run(['python3',str(out/'run_one.py'),label,'db-'+stage+'-bin','plain'],cwd=root,env=env,check=True)
        lab = root/'.run'/('group-'+label)
        reports = out/label/'reports'
        shutil.copytree(lab/'reports',reports,dirs_exist_ok=True)
        audit = json.loads((reports/'audit.json').read_text())
        assert all(n['Pending']==0 for n in audit)
        committees = [n for n in audit if n['Name'].startswith('committee')]
        assert len({n['StateHash'] for n in committees})==1
        assert all(n['Payments']==n['Closed']==1500 and n['Gap']=='0' and n['Rewards']=='126000' and n['Burned']=='15000' for n in committees)
        for start,count in [(0,1200),(1200,300)]:
            b=json.loads((reports/f'bench-v4-{start}.json').read_text())
            assert b['Summary']['failed']==0 and len(b['Samples'])==count
finally:
    if pids:
        subprocess.run(['screen','-L','-dmS','utxo-fresh-active','env','-u','UTXO_SETTLEMENT_TRACE','-u','UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE','UTXO_EXPERIMENT_FLUSH=10ms','UTXO_EXPERIMENT_GOSSIP=10ms',str(old/'bin/payctl'),'lab-run','-dir',str(old/'experiments/fresh-active-001'),'-bin',str(old/'bin')],cwd=old,check=True)
print('warm database comparisons complete',flush=True)
