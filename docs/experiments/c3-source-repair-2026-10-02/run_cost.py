import argparse,json,os,shutil,signal,subprocess,time
from pathlib import Path

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--root',type=Path,required=True);a=ap.parse_args();root=a.root.resolve()
 while 'C3 NETWORK COMPLETE' not in (root/'c3-progress.log').read_text():
  if 'Traceback' in (root/'c3-progress.log').read_text():raise RuntimeError('functional suite failed')
  time.sleep(2)
 env=dict(os.environ,PATH='/usr/local/go/bin:/opt/homebrew/bin:'+os.environ['PATH'],UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms')
 for k in ['UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY','UTXO_EXPERIMENT_ADAPTATION_PAUSE_FILE']:env.pop(k,None)
 for source in [root/'baseline',root]:
  binaries=source/'bin-c3';binaries.mkdir(exist_ok=True)
  for role in ['committee','member','gateway','payctl']:subprocess.run(['go','build','-tags=comet_v3','-o',str(binaries/role),'./cmd/'+role],cwd=source,env=env,check=True)
 results=root/'cost-results';results.mkdir(exist_ok=True)
 for round in range(3):
  for variant in (['baseline','candidate'] if round%2==0 else ['candidate','baseline']):
   source=root/'baseline' if variant=='baseline' else root;binary=source/'bin-c3';out=results/f'{variant}-{round+1}';out.mkdir();lab=root/'.run'/f'cost-{variant}-{round+1}'
   with (out/'init.log').open('w') as f:subprocess.run([str(binary/'payctl'),'init-lab','-dir',str(lab),'-port','28000','-outputs','450','-v4'],env=env,stdout=f,stderr=f,check=True)
   f=(out/'lab.log').open('w');proc=subprocess.Popen([str(binary/'payctl'),'lab-run','-dir',str(lab),'-bin',str(binary)],env=env,stdout=f,stderr=f)
   try:
    end=time.monotonic()+60
    while 'Laboratory running:' not in (out/'lab.log').read_text():
     if proc.poll() is not None or time.monotonic()>end:raise RuntimeError('startup')
     time.sleep(.2)
    with (out/'bench.log').open('w') as log:subprocess.run([str(binary/'payctl'),'bench-v4','-dir',str(lab),'-count','300','-concurrency','64','-max-pending','256','-rate','100','-wallet-no-sync'],env=env,stdout=log,stderr=log,check=True,timeout=90)
    time.sleep(2)
   finally:proc.send_signal(signal.SIGINT);proc.wait(timeout=40);f.close()
   with (out/'audit.log').open('w') as log:subprocess.run([str(binary/'payctl'),'audit','-dir',str(lab)],env=env,stdout=log,stderr=log,check=True)
   for p in (lab/'reports').glob('*.json'):shutil.copyfile(p,out/p.name)
   rows=json.loads((out/'audit.json').read_text());committees=[x for x in rows if x['Name'].startswith('committee')]
   assert len(committees)==4 and len({x['StateHash'] for x in committees})==1
   assert all(x.get('Pending',0)==0 for x in rows)
   assert all(x['Gap']=='0' and x['Payments']==x['Closed'] for x in committees)
   print('PASS',variant,round+1,flush=True)
 print('C3 COST COMPLETE',flush=True)
if __name__=='__main__':main()
