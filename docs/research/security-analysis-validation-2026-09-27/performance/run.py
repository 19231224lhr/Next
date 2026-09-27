import importlib.util, pathlib, subprocess, json, hashlib
lab = pathlib.Path('/Users/richz/lab/man')
spec = importlib.util.spec_from_file_location('driver', lab/'security-smoke-driver.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
roots = {'baseline':lab/'utxo-security-baseline-20260927', 'candidate':lab/'utxo-security-review-20260927'}
def select(label):
    m.ROOT=roots[label]
    m.BIN=m.ROOT/'.run/e5-bin'
    m.OUT=m.ROOT/'.run/security-performance'
    m.OUT.mkdir(parents=True,exist_ok=True)
for label in roots:
    select(label)
    m.BIN.mkdir(parents=True,exist_ok=True)
    with (m.OUT/'build.log').open('w') as log:
        m.command(['python3',m.ROOT/'third_party/cometbft/overlay.py'],log)
        for role in ['payctl','committee','member','gateway']:
            m.command(['go','build','-tags=comet_v3','-o',m.BIN/role,'./cmd/'+role],log)
    m.write(m.OUT/'build.json', {'source':label,'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in m.BIN.iterdir()},'environment':{k:v for k,v in m.ENV.items() if k.startswith('UTXO_') or k in ['GOGC','GOMAXPROCS']}})
for i in range(1,4):
    for label in ('baseline','candidate'):
        select(label)
        m.run_case(label+'-r'+str(i),'direct',500,5000,seed=23,warm=100,window=10)
