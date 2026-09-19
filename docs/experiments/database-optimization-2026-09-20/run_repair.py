"""Exercise real compensation, historical revision and restart with null indexing."""
import json, os, signal, subprocess, time
from pathlib import Path

root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
out = root/'docs/experiments/database-optimization-2026-09-20/repair'
out.mkdir(exist_ok=True)
lab = root/'.run/group-dbo-credit-3'
bins = root/'.run/db-credit-bin'
env = dict(os.environ, UTXO_EXPERIMENT_FLUSH='10ms', UTXO_EXPERIMENT_GOSSIP='10ms')
env.pop('UTXO_SETTLEMENT_TRACE',None)
def run_lab(name, command):
    with (out/(name+'-lab.log')).open('w') as log:
        proc = subprocess.Popen([str(bins/'payctl'),'lab-run','-dir',str(lab),'-bin',str(bins)],cwd=root,env=env,stdout=log,stderr=log)
        try:
            deadline = time.monotonic()+45
            while 'Laboratory running:' not in (out/(name+'-lab.log')).read_text():
                if proc.poll() is not None: raise RuntimeError('lab exited')
                if time.monotonic() > deadline: raise TimeoutError('lab readiness')
                time.sleep(.1)
            with (out/(name+'-output.txt')).open('w') as result:
                subprocess.run([str(bins/'payctl'),*command],cwd=root,env=env,stdout=result,stderr=subprocess.STDOUT,timeout=120,check=True)
            time.sleep(2)
        finally:
            proc.send_signal(signal.SIGINT)
            proc.wait(timeout=35)
    with (out/(name+'-audit-output.txt')).open('w') as log:
        subprocess.run([str(bins/'payctl'),'audit','-dir',str(lab)],cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
    audit = json.loads((lab/'reports/audit.json').read_text())
    assert all(n['Pending']==0 for n in audit)
    committees = [n for n in audit if n['Name'].startswith('committee')]
    assert len({n['StateHash'] for n in committees}) == 1
    assert all(n['Revisions']==1 and n['Gap']=='0' and n['Payments']==n['Closed'] for n in committees)
    (out/(name+'-audit.json')).write_text(json.dumps(audit,indent=2)+'\n')

run_lab('repair', ['demo-v4','-dir',str(lab),'-input','110','-withhold-parent'])
run_lab('restart', ['bench-v4','-dir',str(lab),'-start','112','-count','8','-concurrency','4'])
for name in ['demo-v4-110.json','bench-v4-112.json']:
    source=lab/'reports'/name
    if source.exists(): (out/name).write_bytes(source.read_bytes())
print('real repair, late parent and repaired-history restart passed',flush=True)
