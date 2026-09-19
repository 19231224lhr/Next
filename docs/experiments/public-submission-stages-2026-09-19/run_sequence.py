import json,os,signal,subprocess,time
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-fastpay-v12')
old=Path('/Users/richz/lab/man/utxo-fastpay')
oldcmd=f"{old}/bin/payctl lab-run -dir {old}/experiments/fresh-active-001 -bin {old}/bin"
lines=subprocess.check_output(['ps','-axo','pid=,command='],text=True).splitlines()
pids=[int(line.strip().split(None,1)[0]) for line in lines if line.strip().split(None,1)[-1]==oldcmd]
assert len(pids)<=1
try:
    if pids:
        os.kill(pids[0],signal.SIGINT)
        for _ in range(35):
            try: os.kill(pids[0],0)
            except ProcessLookupError: break
            time.sleep(1)
        else: raise RuntimeError('old laboratory failed to stop')
    runner=root/'docs/experiments/stages100-2026-09-19/run_experiment.py'
    import hashlib
    manifest=json.loads((root/'docs/experiments/committee-input-only-2026-09-19/binaries.json').read_text())
    for name in ['committee','member','gateway','payctl']:
        key='submission-new-bin/'+name
        assert hashlib.sha256((root/'.run'/key).read_bytes()).hexdigest()==manifest[key], name
    for label,mode in [('submission-stages1','trace'),('submission-stages-control','plain')]:
        subprocess.run(['python3',str(runner),label,'submission-new-bin',mode],cwd=root,check=True)

finally:
    if pids:
        subprocess.run(['screen','-L','-dmS','utxo-fresh-active','env','-u','UTXO_SETTLEMENT_TRACE','UTXO_EXPERIMENT_FLUSH=10ms','UTXO_EXPERIMENT_GOSSIP=10ms',str(old/'bin/payctl'),'lab-run','-dir',str(old/'experiments/fresh-active-001'),'-bin',str(old/'bin')],cwd=old,check=True)
print('stage timing sequence complete',flush=True)
