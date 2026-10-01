import os,json,subprocess,time,signal,shutil,hashlib
from pathlib import Path
R=Path('/Users/richz/lab/man/utxo-review-20261002');os.chdir(R)
while 'ALL OWNER-FUEL MATRIX PASS' not in (R/'review-owner-progress.log').read_text():
 if 'Traceback' in (R/'review-owner-progress.log').read_text():raise RuntimeError('prior experiment failed')
 time.sleep(10)
E=dict(os.environ,PATH='/usr/local/go/bin:/opt/homebrew/bin:'+os.environ['PATH'],UTXO_EXPERIMENT_COMMIT='250ms',UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms',UTXO_E8_LARGE_GENESIS='1')
B=R/'bin-review3';B.mkdir(exist_ok=True);O=R/'review-storage-tps-results';O.mkdir(exist_ok=True)
for role in ['committee','member','gateway','payctl']:
 subprocess.run(['go','build','-tags=comet_v3','-o',str(B/('member-real' if role=='member' else role)),'./cmd/'+role],env=E,check=True)
(B/'member').write_text('#!/bin/sh\nexec env GOMAXPROCS=16 GOGC=200 "'+str(B/'member-real')+'" "$@"\n');(B/'member').chmod(0o755)
def run(name,sync=False,memory=False,rate=100,count=1000):
 D=O/name;D.mkdir();L=R/'.run'/name
 env=dict(E,UTXO_EXPERIMENT_MEMBER_SYNC='1' if sync else '0')
 for k in ['COMMITTEE_MEMORY','MEMBER_MEMORY','GATEWAY_MEMORY','MEM_BLOCKSTORE']:env['UTXO_EXPERIMENT_'+k]='1' if memory else '0'
 def ctl(args,file,timeout=240):
  with (D/file).open('w') as f:subprocess.run([str(B/'payctl')]+list(map(str,args)),stdout=f,stderr=f,env=env,check=True,timeout=timeout)
 ctl(['init-lab','-dir',L,'-port',29000,'-outputs',count+100,'-v4'],'init.log')
 lab=json.loads((L/'lab.json').read_text());lab['Nodes']=[n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')];(L/'lab.json').write_text(json.dumps(lab))
 metadata={'env':{k:v for k,v in env.items() if k.startswith('UTXO_')},'count':count,'rate':rate,'nodes':9,'member_GOMAXPROCS':16,'member_GOGC':200,'wallet_no_sync':True,'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in B.iterdir()}}
 (D/'configuration.json').write_text(json.dumps(metadata,indent=2))
 with (D/'nodes.log').open('w') as f:
  p=subprocess.Popen([str(B/'payctl'),'lab-run','-dir',str(L),'-bin',str(B)],env=env,stdout=f,stderr=f)
  try:
   end=time.monotonic()+90
   while 'Laboratory running:' not in (D/'nodes.log').read_text():
    if p.poll() is not None:raise RuntimeError('lab exited')
    if time.monotonic()>end:raise TimeoutError('ready')
    time.sleep(.2)
   ctl(['bench-v4','-dir',L,'-start',0,'-count',count,'-rate',rate,'-concurrency',256,'-max-pending',4096,'-same-org','-wallet-no-sync'],'bench.log')
   time.sleep(2)
  finally:p.send_signal(signal.SIGINT);p.wait(timeout=60)
 ctl(['audit','-dir',L],'audit.log')
 for source in (L/'reports').glob('*.json'):shutil.copy2(source,D/source.name)
 audit=json.loads((D/'audit.json').read_text());c=[r for r in audit if r['Name'].startswith('committee')]
 assert len({r['StateHash'] for r in c})==1 and all(r['Gap']=='0' and r['Closed']==count for r in c)
 s=json.loads((D/'bench-v4-0.json').read_text())['Summary'];assert s['failed']==s['final_unfinished']==0 and s['fast_completed']==count
 print('PASS',name,{k:s[k] for k in ['elapsed_s','completed_per_second','fast_p50_ms','fast_p95_ms','dispatch_lag_p95_ms','sampled_peak_unfinished']},flush=True)
if os.environ.get('REVIEW_TPS_ONLY') != '1':
 for i in range(3):
  for sync in ([False,True] if i%2==0 else [True,False]):run(f'storage-{i}-'+('sync' if sync else 'nosync'),sync=sync)
for i in range(3):run(f'latest-tps-v2-{i}',memory=True,rate=2000,count=30000)
print('ALL STORAGE AND TPS CONTROLS PASS',flush=True)
