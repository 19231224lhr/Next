import os,subprocess,time,signal,json,threading,urllib.request,shutil
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-review-20261002'); os.chdir(root)
env=dict(os.environ,UTXO_EXPERIMENT_COMMIT='250ms',UTXO_EXPERIMENT_COMMITTEE_MEMORY='0',UTXO_EXPERIMENT_MEMBER_MEMORY='1',UTXO_EXPERIMENT_GATEWAY_MEMORY='1',UTXO_EXPERIMENT_MEM_BLOCKSTORE='0',PATH='/usr/local/go/bin:/opt/homebrew/bin:'+os.environ['PATH'],UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms',UTXO_EXPERIMENT_BUDGET='1')
bin=root/'bin-review3'; out=root/'review-liability-controls'; out.mkdir(exist_ok=True)
while 'ALL STORAGE AND TPS CONTROLS PASS' not in (root/'review-tps-v2-progress.log').read_text():
 if 'Traceback' in (root/'review-tps-v2-progress.log').read_text():raise RuntimeError('prior experiment failed')
 time.sleep(5)
def ctl(args, logfile, timeout=300):
 with logfile.open('w') as f: subprocess.run([str(bin/'payctl')]+args,env=env,stdout=f,stderr=f,check=True,timeout=timeout)
def run(mode):
 target=out/mode; target.mkdir();lab=root/'.run'/('control-'+mode)
 ctl(['init-lab','-dir',str(lab),'-port','31000','-outputs','10','-v4'],target/'init.log')
 ctl(['liability-v4','-dir',str(lab),'-case',mode,'-prepare'],target/'prepare.log')
 with (target/'nodes.log').open('w') as log:
  p=subprocess.Popen([str(bin/'payctl'),'lab-run','-dir',str(lab),'-bin',str(bin)],env=env,stdout=log,stderr=log)
  try:
   end=time.monotonic()+60
   while 'Laboratory running:' not in (target/'nodes.log').read_text():
    if p.poll() is not None:raise RuntimeError('lab exited')
    if time.monotonic()>end:raise TimeoutError('ready')
    time.sleep(.2)
   ctl(['liability-v4','-dir',str(lab),'-case',mode],target/'sequence.log',150)
   time.sleep(2)
  finally:p.send_signal(signal.SIGINT);p.wait(timeout=40)
 ctl(['liability-v4','-dir',str(lab),'-audit'],target/'liability-audit.log')
 ctl(['audit','-dir',str(lab)],target/'audit.log')
 for f in (lab/'reports').glob('*.json'):shutil.copy2(f,target/f.name)
 rows=json.loads((target/'audit.json').read_text());c=[r for r in rows if r['Name'].startswith('committee')]
 assert len(c)==4 and len({r['StateHash'] for r in c})==1
 print('PASS',mode,flush=True)
for mode in ['missing','cross']:run(mode)
print('ALL LIABILITY CONTROLS PASS',flush=True)
