import os,subprocess,time,signal,json
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-authorized-repair-20260929')
os.chdir(root)
env=dict(os.environ,PATH='/usr/local/go/bin:/opt/homebrew/bin:'+os.environ['PATH'],UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms',UTXO_SETTLEMENT_TRACE='1')
subprocess.run(['go','build','-tags=comet_v3','-o','bin/payctl','./cmd/payctl'],env=env,check=True)
lab=root/'.run/smoke-final';lab.parent.mkdir(exist_ok=True)
bin=root/'bin';out=root/'smoke-final-results';out.mkdir(exist_ok=True)
subprocess.run([str(bin/'payctl'),'init-lab','-dir',str(lab),'-port','24000','-outputs','1200','-v4'],env=env,check=True)
with (out/'lab.log').open('w') as log:
 proc=subprocess.Popen([str(bin/'payctl'),'lab-run','-dir',str(lab),'-bin',str(bin)],env=env,stdout=log,stderr=log)
 jobs=[];logs=[]
 try:
  end=time.monotonic()+60
  while 'Laboratory running:' not in (out/'lab.log').read_text():
   if proc.poll() is not None: raise RuntimeError('lab exited')
   if time.monotonic()>end: raise TimeoutError('readiness')
   time.sleep(.2)
  for i in range(4):
   f=(out/f'repair-{i}.log').open('w');logs.append(f)
   jobs.append(subprocess.Popen([str(bin/'payctl'),'demo-v4','-dir',str(lab),'-input',str(i),'-withhold-parent'],env=env,stdout=f,stderr=f))
  for p in jobs:
   if p.wait(timeout=100)!=0: raise RuntimeError('repair demo failed')
  with (out/'normal.log').open('w') as log2:
   subprocess.run([str(bin/'payctl'),'bench-v4','-dir',str(lab),'-start','100','-count','1000','-concurrency','64','-max-pending','256','-rate','100','-wallet-no-sync'],env=env,stdout=log2,stderr=log2,check=True,timeout=90)
  time.sleep(3)
 finally:
  for p in jobs:
   if p.poll() is None:p.terminate()
  proc.send_signal(signal.SIGINT);proc.wait(timeout=40)
  for f in logs:f.close()
with (out/'audit.log').open('w') as log:
 subprocess.run([str(bin/'payctl'),'audit','-dir',str(lab)],env=env,stdout=log,stderr=log,check=True)
for f in (lab/'reports').glob('*.json'):(out/f.name).write_bytes(f.read_bytes())
rows=json.loads((lab/'reports/audit.json').read_text());committees=[r for r in rows if r['Name'].startswith('committee')]
assert len(committees)==4 and len({r['StateHash'] for r in committees})==1
assert all(r.get('Pending',0)==0 for r in rows)
assert all(r['Gap']=='0' and r['Payments']==r['Closed'] and r['Revisions']>=1 for r in committees)
print('NETWORK SMOKE PASS',flush=True)
