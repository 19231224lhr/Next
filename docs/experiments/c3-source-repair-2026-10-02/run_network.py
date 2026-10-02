"""Focused C3 network evidence. Fresh laboratory for each treatment/repetition.
No original publisher or gateway INSTALL sends the withheld source in recovery mode.
The off control retains the same member approval material and observed blocks.
"""
import argparse, json, os, shutil, signal, subprocess, time, urllib.request
from pathlib import Path

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--root',type=Path,required=True);ap.add_argument('--rounds',type=int,default=3)
 args=ap.parse_args();root=args.root.resolve();os.chdir(root)
 env=dict(os.environ, PATH='/usr/local/go/bin:/opt/homebrew/bin:'+os.environ['PATH'],UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms')
 env.pop('UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY',None);env.pop('UTXO_EXPERIMENT_ADAPTATION_PAUSE_FILE',None)
 binaries=root/'bin-c3';binaries.mkdir(exist_ok=True)
 results=root/'c3-results';results.mkdir(exist_ok=True)
 for role in ['committee','member','gateway','payctl']:
  subprocess.run(['go','build','-tags=comet_v3','-o',str(binaries/role),'./cmd/'+role],env=env,check=True)
 summary=[]
 for round in range(args.rounds):
  for mode in (['recovery','control','paused'] if round%2==0 else ['control','recovery','paused']):
   name=f'{mode}-{round+1}';out=results/name;out.mkdir();lab=root/'.run'/name;lab.parent.mkdir(exist_ok=True)
   treatment=env.copy()
   if mode!='recovery':treatment['UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY']='1'
   pause=out/'adaptation.paused'
   if mode=='paused':
    pause.write_text('all four adaptation services paused for C3 test\n');treatment['UTXO_EXPERIMENT_ADAPTATION_PAUSE_FILE']=str(pause)
   with (out/'init.log').open('w') as f:
    subprocess.run([str(binaries/'payctl'),'init-lab','-dir',str(lab),'-port','28000','-outputs','450','-v4'],env=treatment,stdout=f,stderr=f,check=True)
   network=json.loads(Path(json.loads((lab/'lab.json').read_text())['Network']).read_text())
   def get(url):
    with urllib.request.urlopen(url,timeout=3) as r:return json.load(r)
   logfile=(out/'lab.log').open('w');proc=subprocess.Popen([str(binaries/'payctl'),'lab-run','-dir',str(lab),'-bin',str(binaries)],env=treatment,stdout=logfile,stderr=logfile)
   job=None
   try:
    end=time.monotonic()+60
    while 'Laboratory running:' not in (out/'lab.log').read_text():
     if proc.poll() is not None:raise RuntimeError('lab exited')
     if time.monotonic()>end:raise TimeoutError('readiness')
     time.sleep(.2)
    flags=['-recover-source'] if mode=='recovery' else ['-wait-repair']
    with (out/'demo.log').open('w') as log:
     job=subprocess.Popen([str(binaries/'payctl'),'demo-v4','-dir',str(lab),'-input','0','-withhold-parent',*flags],env=treatment,stdout=log,stderr=log)
     if mode=='paused':
      report=lab/'reports/direct-0.json';end=time.monotonic()+75
      while not report.exists():
       if job.poll() is not None:raise RuntimeError('demo ended before economic report')
       if time.monotonic()>end:raise TimeoutError('decision while paused')
       time.sleep(.1)
      partial=json.loads(report.read_text());output=partial['parent_output'];observed=[]
      end=time.monotonic()+15
      while True:
       observed=[{'obligation':get(u+'/v3/obligations/'+output),'repair':get(u+'/v3/repairs/'+output+'/status')} for u in network['CommitteeURLs']]
       if all(x['obligation']['Status']==3 for x in observed):break
       if time.monotonic()>end:raise TimeoutError('late recovery while paused')
       time.sleep(.1)
      assert all(not x['repair']['Committed'] and not x['repair']['Materialized'] for x in observed)
      (out/'paused-observations.json').write_text(json.dumps({'report':partial,'nodes':observed},indent=2))
      pause.unlink() # only this script's fault marker; resumes adaptation
     if job.wait(timeout=100)!=0:raise RuntimeError('demo failed')
    report=json.loads((lab/'reports/direct-0.json').read_text())
    assert (report.get('compensation_avoided') is True)==(mode=='recovery')
    if mode!='recovery':
     assert report['late_parent_reserve_recovered']
     assert all(x['Snapshot']['Materialized'] and x['Snapshot']['IdentityStable'] for x in report['physical_repair']['Nodes'])
    if mode!='paused':
     with (out/'bench.log').open('w') as f:
      subprocess.run([str(binaries/'payctl'),'bench-v4','-dir',str(lab),'-start','10','-count','300','-concurrency','64','-max-pending','256','-rate','100','-wallet-no-sync'],env=treatment,stdout=f,stderr=f,check=True,timeout=90)
    time.sleep(2)
   finally:
    if job and job.poll() is None:job.terminate();job.wait(timeout=10)
    proc.send_signal(signal.SIGINT);proc.wait(timeout=40);logfile.close()
   with (out/'audit.log').open('w') as f:
    subprocess.run([str(binaries/'payctl'),'audit','-dir',str(lab)],env=treatment,stdout=f,stderr=f,check=True,timeout=30)
   for p in (lab/'reports').glob('*.json'):shutil.copyfile(p,out/p.name)
   rows=json.loads((out/'audit.json').read_text());committees=[x for x in rows if x['Name'].startswith('committee')]
   assert len(committees)==4 and len({x['StateHash'] for x in committees})==1
   assert all(x.get('Pending',0)==0 for x in rows)
   assert all(x['Gap']=='0' and x['Payments']==x['Closed'] for x in committees)
   record={'mode':mode,'round':round+1,'report':report,'committee_audit':committees[0]};summary.append(record)
   (results/'summary.json').write_text(json.dumps(summary,indent=2))
   print('PASS',name,report,flush=True)
 print('C3 NETWORK COMPLETE',flush=True)

if __name__=='__main__':main()
