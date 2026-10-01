import os,subprocess,time,signal,json,threading,urllib.request,shutil
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-review-20261002'); os.chdir(root)
env=dict(os.environ,PATH='/usr/local/go/bin:/opt/homebrew/bin:'+os.environ['PATH'],UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms',UTXO_EXPERIMENT_BUDGET='1')
bin=root/'bin'; out=root/'review-paired-results'; out.mkdir(exist_ok=True)
for role in ['committee','member','gateway','payctl']:
 subprocess.run(['go','build','-tags=comet_v3','-o',str(bin/role),'./cmd/'+role],env=env,check=True)
def ctl(args, logfile, timeout=300):
 with logfile.open('w') as f: subprocess.run([str(bin/'payctl')]+args,env=env,stdout=f,stderr=f,check=True,timeout=timeout)
def run(name, repair=None, wait_final=None):
 target=out/name; target.mkdir()
 lab=root/'.run'/name
 ctl(['init-lab','-dir',str(lab),'-port','28000','-outputs','4300' if repair is not None else '110','-v4'],target/'init.log')
 if wait_final is not None: ctl(['chain-v4','-dir',str(lab),'-length','100','-prepare','-e5-owner-fuel'],target/'prepare.log')
 lf=(target/'lab.log').open('w'); proc=subprocess.Popen([str(bin/'payctl'),'lab-run','-dir',str(lab),'-bin',str(bin)],env=env,stdout=lf,stderr=lf)
 jobs=[]; handles=[]; stop=threading.Event(); thread=None
 try:
  deadline=time.monotonic()+90
  while 'Laboratory running:' not in (target/'lab.log').read_text():
   if proc.poll() is not None: raise RuntimeError('lab exited')
   if time.monotonic()>deadline: raise TimeoutError('lab ready')
   time.sleep(.2)
  if repair is not None:
   labcfg=json.loads((lab/'lab.json').read_text()); network=json.loads(Path(labcfg['Network']).read_text())
   url=network['CommitteeURLs'][0]+'/debug/budget'
   def sample():
    with (target/'reserve-timeline.jsonl').open('w') as f:
     while not stop.is_set():
      try:
       with urllib.request.urlopen(url,timeout=2) as r: value=json.load(r)
       f.write(json.dumps(value)+'\n'); f.flush()
      except Exception as e: f.write(json.dumps({'error':str(e)})+'\n'); f.flush()
      stop.wait(.1)
   thread=threading.Thread(target=sample);thread.start()
   for i in range(4):
    h=(target/f'demo-{i}.log').open('w');handles.append(h)
    args=[str(bin/'payctl'),'demo-v4','-dir',str(lab),'-input',str(i)]
    if repair: args+=['-withhold-parent']
    jobs.append(subprocess.Popen(args,env=env,stdout=h,stderr=h))
   ctl(['bench-v4','-dir',str(lab),'-start','100','-count','4000','-concurrency','64','-max-pending','256','-rate','100','-wallet-no-sync'],target/'bench.log',100)
   for job in jobs:
    if job.wait(timeout=100)!=0:raise RuntimeError('demo failed')
  else:
   args=['chain-v4','-dir',str(lab),'-length','100','-e5-owner-fuel']
   if wait_final:args+=['-wait-final']
   ctl(args,target/'chain.log',240)
  time.sleep(3)
 finally:
  stop.set()
  if thread:thread.join(timeout=5)
  for job in jobs:
   if job.poll() is None:job.terminate()
  proc.send_signal(signal.SIGINT);proc.wait(timeout=40);lf.close()
  for h in handles:h.close()
 ctl(['audit','-dir',str(lab)],target/'audit.log')
 if wait_final is not None:ctl(['chain-v4','-dir',str(lab),'-length','100','-audit'],target/'chain-audit.log')
 for f in (lab/'reports').glob('*.json'):shutil.copy2(f,target/f.name)
 rows=json.loads((target/'audit.json').read_text()); committees=[r for r in rows if r['Name'].startswith('committee')]
 assert len(committees)==4 and len({r['StateHash'] for r in committees})==1
 assert all(r.get('Pending',0)==0 for r in rows)
 assert all(r['Gap']=='0' and r['Payments']==r['Closed'] for r in committees)
 if repair is not None:
  for row in committees:
   r=row['Recovery'];assert r['Open']==0 and r['Repaired']==0
   assert r['Recovered']==(4 if repair else 0),r
   assert r['PaidCAL']==r['RecoveredCAL'],r
   assert all(x['Spent']==0 and x['Reserved']==0 for x in r['Reserves']),r
  s=json.loads((target/'bench-v4-100.json').read_text())['Summary']
  assert s['failed']==0 and s['fast_completed']==4000 and s['final_unfinished']==0,s
 print('PASS '+name,flush=True)
for i in range(3):
 for repair in ([False,True] if i%2==0 else [True,False]):run(f'pair-{i}-'+('repair' if repair else 'normal'),repair=repair)
for i in range(3):
 for wait in [False,True]:run(f'chain-{i}-'+('wait' if wait else 'fast'),wait_final=wait)
print('ALL PAIRED AND CHAIN TESTS PASS',flush=True)
