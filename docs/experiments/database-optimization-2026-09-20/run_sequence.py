"""Independent cumulative binaries; fresh labs, unchanged 100/64 workload."""
import hashlib, json, os, shutil, signal, subprocess, time, urllib.request
from pathlib import Path

root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
out = root/'docs/experiments/database-optimization-2026-09-20'
out.mkdir(parents=True, exist_ok=True)
log = (root/'.run/db-sequence.log').open('w', buffering=1)
os.dup2(log.fileno(), 1)
os.dup2(log.fileno(), 2)
env = dict(os.environ, PATH='/usr/local/go/bin:'+os.environ['PATH'])
for key in ['UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE', 'UTXO_COMET_PROFILE', 'UTXO_SETTLEMENT_TRACE']:
    env.pop(key, None)

# Mapping-only candidate: restore the source even if compilation fails.
source = root/'internal/store/bolt.go'
original = source.read_bytes()
needle = b'&bolt.Options{Timeout: time.Second}'
mapped = b'&bolt.Options{Timeout: time.Second, InitialMmapSize: 16 << 20}'
assert original.count(needle) == 1 or original.count(mapped) == 1
try:
    source.write_bytes(original.replace(needle, mapped))
    dest = root/'.run/db-map-bin'
    dest.mkdir(exist_ok=True)
    for name in ['committee', 'member', 'gateway', 'payctl']:
        subprocess.run(['go','build','-tags=comet_v3','-o',str(dest/name),'./cmd/'+name],cwd=root,env=env,check=True)
finally:
    source.write_bytes(original)

stages = ['base', 'index', 'prepare', 'credit', 'map']
manifest = {stage: {name: hashlib.sha256((root/'.run'/('db-'+stage+'-bin')/name).read_bytes()).hexdigest()
                   for name in ['committee','member','gateway','payctl']} for stage in stages}
(out/'binaries.json').write_text(json.dumps(manifest, indent=2)+'\n')
old = Path('/Users/richz/lab/man/utxo-fastpay')
oldcmd = f'{old}/bin/payctl lab-run -dir {old}/experiments/fresh-active-001 -bin {old}/bin'
lines = subprocess.check_output(['ps','-axo','pid=,command='],text=True).splitlines()
pids = [int(line.strip().split(None,1)[0]) for line in lines if line.strip().split(None,1)[-1] == oldcmd]
assert len(pids) <= 1
runner = out/'run_one.py'
try:
    if pids:
        os.kill(pids[0], signal.SIGINT)
        for _ in range(35):
            try: os.kill(pids[0], 0)
            except ProcessLookupError: break
            time.sleep(1)
        else: raise RuntimeError('previous lab did not stop')
    runs = [(f'dbo-{stage}-{r}',stage,'plain') for r in range(1,4)
            for stage in (stages if r != 2 else list(reversed(stages)))]
    runs += [(f'dbo-{stage}-trace',stage,'trace') for stage in stages]
    for label, stage, trace in runs:
        print('START', label, flush=True)
        subprocess.run(['python3',str(runner),label,'db-'+stage+'-bin',trace],cwd=root,env=env,check=True)
        lab = root/'.run'/('group-'+label)
        reports = out/label/'reports'
        shutil.copytree(lab/'reports',reports,dirs_exist_ok=True)
        b = json.loads((reports/'bench-v4-0.json').read_text())
        assert b['Summary']['failed'] == 0 and len(b['Samples']) == 100
        audit = json.loads((reports/'audit.json').read_text())
        assert len(audit) == 14 and all(n['Pending'] == 0 for n in audit)
        committees = [n for n in audit if n['Name'].startswith('committee')]
        assert len({n['StateHash'] for n in committees}) == 1
        assert all(n['Payments'] == n['Closed'] == 100 and n['Gap'] == '0' and n['Rewards'] == '8400' and n['Burned'] == '1000' for n in committees)
        sizes = {str(p.relative_to(lab)): sum(f.stat().st_size for f in p.rglob('*') if f.is_file())
                 for p in lab.glob('committee*/comet/data/*') if p.is_dir()}
        (reports/'database-sizes.json').write_text(json.dumps(sizes,indent=2)+'\n')

    # Same-database restart: block, results and commit still work with no search index.
    lab = root/'.run/group-dbo-credit-trace'
    bindir = root/'.run/db-credit-bin'
    config = json.loads((lab/'config/network.json').read_text())
    with (out/'restart.log').open('w') as restartlog:
        proc = subprocess.Popen([str(bindir/'payctl'),'lab-run','-dir',str(lab),'-bin',str(bindir)],cwd=root,env=env,stdout=restartlog,stderr=restartlog)
        try:
            deadline = time.monotonic()+45
            while 'Laboratory running:' not in (out/'restart.log').read_text():
                if proc.poll() is not None: raise RuntimeError('restart exited')
                if time.monotonic() > deadline: raise TimeoutError('restart readiness')
                time.sleep(.1)
            checks = []
            for url in config['CommitteeURLs']:
                for route in ['block', 'block_results', 'commit']:
                    with urllib.request.urlopen(url+'/'+route+'?height=3',timeout=10) as response:
                        body = json.load(response)
                        assert body
                        checks.append({'url':url,'route':route,'height':3,'status':response.status})
            (out/'restart-checks.json').write_text(json.dumps(checks,indent=2)+'\n')
        finally:
            proc.send_signal(signal.SIGINT)
            proc.wait(timeout=35)
    subprocess.run([str(bindir/'payctl'),'audit','-dir',str(lab)],cwd=root,env=env,check=True,stdout=(out/'restart-audit-output.txt').open('w'))
    restarted = json.loads((lab/'reports/audit.json').read_text())
    before = json.loads((out/'dbo-credit-trace/reports/audit.json').read_text())
    assert restarted == before, 'restart changed business/accounting state'
finally:
    if pids:
        subprocess.run(['screen','-L','-dmS','utxo-fresh-active','env','-u','UTXO_SETTLEMENT_TRACE','-u','UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE','UTXO_EXPERIMENT_FLUSH=10ms','UTXO_EXPERIMENT_GOSSIP=10ms',str(old/'bin/payctl'),'lab-run','-dir',str(old/'experiments/fresh-active-001'),'-bin',str(old/'bin')],cwd=old,check=True)
print('database experiments complete',flush=True)
