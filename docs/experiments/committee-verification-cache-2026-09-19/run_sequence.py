"""Isolated cache A/B/A; same executable and getter, only cache bypass differs."""
import hashlib, json, os, signal, subprocess, time, shutil, sys
from pathlib import Path

root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
followup = '--followup' in sys.argv
store_profile = '--store-profile' in sys.argv
log = (root/'.run'/('cache-store-profile.log' if store_profile else 'cache-followup.log' if followup else 'cache-sequence.log')).open('w', buffering=1)
os.dup2(log.fileno(), 1)
os.dup2(log.fileno(), 2)
out = root/'docs/experiments/committee-verification-cache-2026-09-19'
old = Path('/Users/richz/lab/man/utxo-fastpay')
out.mkdir(parents=True, exist_ok=True)
bin_name = 'cache-store-bin' if store_profile else 'cache-new-bin'
bin_dir = root/'.run'/bin_name
bin_dir.mkdir(exist_ok=True)
for name in ['member', 'gateway', 'payctl']:
    shutil.copy2(root/'.run/submission-new-bin'/name, bin_dir/name)
env = dict(os.environ, PATH='/usr/local/go/bin:'+os.environ['PATH'])
subprocess.run(['go', 'build', '-tags=comet_v3', '-o', str(bin_dir/'committee'), './cmd/committee'], cwd=root, env=env, check=True)
manifest = {}
for folder in ['submission-new-bin', bin_name]:
    for name in ['committee', 'member', 'gateway', 'payctl']:
        manifest[folder+'/'+name] = hashlib.sha256((root/'.run'/folder/name).read_bytes()).hexdigest()
if followup:
    assert manifest == json.loads((out/'binaries.json').read_text()), 'follow-up binaries changed'
elif not store_profile:
    (out/'binaries.json').write_text(json.dumps(manifest, indent=2)+'\n')
else:
    (out/'binaries-store.json').write_text(json.dumps(manifest, indent=2)+'\n')
for name in ['member', 'gateway', 'payctl']:
    assert manifest['submission-new-bin/'+name] == manifest[bin_name+'/'+name]

oldcmd = f"{old}/bin/payctl lab-run -dir {old}/experiments/fresh-active-001 -bin {old}/bin"
lines = subprocess.check_output(['ps', '-axo', 'pid=,command='], text=True).splitlines()
pids = [int(line.strip().split(None,1)[0]) for line in lines if line.strip().split(None,1)[-1] == oldcmd]
assert len(pids) <= 1
runs = [
    ('cache-plain-a1','cache-new-bin','plain',True),
    ('cache-plain-b1','cache-new-bin','plain',False),
    ('cache-plain-a2','cache-new-bin','plain',True),
    ('cache-plain-b2','cache-new-bin','plain',False),
    ('cache-original','submission-new-bin','plain',False),
    ('cache-trace-a','cache-new-bin','trace',True),
    ('cache-trace-b','cache-new-bin','trace',False),
    ('cache-profile-b','cache-new-bin','trace',False),
]
if followup:
    # Resolve the foreground-tail regression/variance seen in the first pairs.
    runs = [('cache-plain-a3','cache-new-bin','plain',True),
            ('cache-plain-b3','cache-new-bin','plain',False),
            ('cache-plain-b4','cache-new-bin','plain',False),
            ('cache-plain-a4','cache-new-bin','plain',True)]
if store_profile:
    runs = [('cache-store-profile','cache-store-bin','trace',False)]
runner = root/'docs/experiments/stages100-2026-09-19/run_experiment.py'
try:
    if pids:
        os.kill(pids[0], signal.SIGINT)
        for _ in range(35):
            try: os.kill(pids[0], 0)
            except ProcessLookupError: break
            time.sleep(1)
        else: raise RuntimeError('previous lab did not stop')
    # Fixed block computation, with no other lab competing for CPU/storage.
    if not followup and not store_profile:
        with (out/'fixed-block.txt').open('w') as log:
            subprocess.run(['go','test','./internal/committee','-run','TestDirectCacheFixedBlockEquivalence','-count=3','-v'],cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
    for label, binaries, trace, disabled in runs:
        runenv = dict(env)
        if disabled: runenv['UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE'] = '1'
        else: runenv.pop('UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE',None)
        if label in ['cache-profile-b','cache-store-profile']: runenv['UTXO_COMET_PROFILE'] = '1'
        else: runenv.pop('UTXO_COMET_PROFILE',None)
        print('START',label,flush=True)
        subprocess.run(['python3',str(runner),label,binaries,trace],cwd=root,env=runenv,check=True)
        lab = root/'.run'/('group-'+label)
        dest = out/label
        dest.mkdir(exist_ok=True)
        shutil.copytree(lab/'reports',dest/'reports',dirs_exist_ok=True)
        record = json.loads((dest/'reports/experiment.json').read_text())
        record['disable_direct_cache'] = disabled
        record['comet_profile'] = label in ['cache-profile-b','cache-store-profile']
        (dest/'reports/experiment.json').write_text(json.dumps(record,indent=2)+'\n')
finally:
    if pids:
        subprocess.run(['screen','-L','-dmS','utxo-fresh-active','env','-u','UTXO_SETTLEMENT_TRACE','-u','UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE','UTXO_EXPERIMENT_FLUSH=10ms','UTXO_EXPERIMENT_GOSSIP=10ms',str(old/'bin/payctl'),'lab-run','-dir',str(old/'experiments/fresh-active-001'),'-bin',str(old/'bin')],cwd=old,check=True)
print('verification cache experiments complete',flush=True)
