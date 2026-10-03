"""Narrow fresh-start regression; retain reports, never replace a prior run."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time

ROOT = Path(__file__).resolve().parent
OUT = ROOT / 'results'
OUT.mkdir()
BIN = ROOT / 'bin'
BIN.mkdir(exist_ok=True)
(ROOT / '.run').mkdir(exist_ok=True)
ENV = {k: v for k, v in os.environ.items() if not k.startswith('UTXO_')}
ENV['PATH'] = '/usr/local/go/bin:/opt/homebrew/bin:' + ENV['PATH']
ENV.update(UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
           UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_BUDGET='1',
           UTXO_EXPERIMENT_MEMBER_SYNC='1', GOMAXPROCS='16', GOGC='200')
for name in ['COMMITTEE_MEMORY', 'MEMBER_MEMORY', 'GATEWAY_MEMORY', 'MEM_BLOCKSTORE']:
    ENV['UTXO_EXPERIMENT_' + name] = '0'

def command(argv, log, timeout=300):
    with (OUT / log).open('w') as f:
        subprocess.run(list(map(str, argv)), cwd=ROOT, env=ENV,
                       stdout=f, stderr=subprocess.STDOUT, check=True, timeout=timeout)

def ctl(args, log):
    command([BIN / 'payctl'] + args, log)

command(['python3', 'third_party/cometbft/overlay.py'], 'overlay.log')
command(['go', 'test', '-tags=comet_v3', './...'], 'test.log', 900)
command(['go', 'vet', '-tags=comet_v3', './...'], 'vet.log', 600)
for role in ['payctl', 'member', 'gateway', 'committee']:
    command(['go', 'build', '-tags=comet_v3', '-o', BIN / role, './cmd/' + role], role + '-build.log')
(OUT / 'build.json').write_text(json.dumps({
    'baseline': 'c3b20aca249a6d9f010eb5c94f41ba33d4e4d3d2',
    'source': json.loads((ROOT / 'source-manifest.json').read_text()),
    'binaries': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()},
    'env': {k:v for k,v in ENV.items() if k.startswith('UTXO_') or k in ['GOMAXPROCS','GOGC']}
}, indent=2))
print('BUILD AND TESTS PASS', flush=True)

for mode in ['load', 'chain']:
    lab = ROOT / '.run' / mode
    dest = OUT / mode
    dest.mkdir()
    ctl(['init-lab', '-dir', lab, '-port', '31000', '-outputs', '110', '-v4'], mode + '/init.log')
    if mode == 'chain':
        ctl(['chain-v4', '-dir', lab, '-length', '100', '-prepare', '-e5-owner-fuel'], mode + '/prepare.log')
        args = ['chain-v4', '-dir', lab, '-length', '100', '-e5-owner-fuel']
    else:
        ctl(['e5-load', '-dir', lab, '-prepare', '-count', '1000', '-offset', '0'], mode + '/prepare.log')
        args = ['e5-load', '-dir', lab, '-count', '1000', '-offset', '0', '-rate', '100', '-seed', '23']
    config = json.loads((lab / 'lab.json').read_text())
    config['Nodes'] = [n for n in config['Nodes'] if n['Binary'] == 'committee'
                       or n['Name'] == 'gateway0' or n['Name'].startswith('org0-member')]
    assert len(config['Nodes']) == 9
    (lab / 'lab.json').write_text(json.dumps(config))
    print('START', mode, flush=True)
    with (dest / 'lab.log').open('w') as f:
        proc = subprocess.Popen([str(BIN/'payctl'), 'lab-run', '-dir', str(lab), '-bin', str(BIN)],
                                cwd=ROOT, env=ENV, stdout=f, stderr=f)
        try:
            deadline = time.monotonic() + 90
            while 'Laboratory running:' not in (dest/'lab.log').read_text():
                if proc.poll() is not None: raise RuntimeError('lab exited')
                if time.monotonic() > deadline: raise TimeoutError('lab startup')
                time.sleep(.2)
            time.sleep(2)
            ctl(args, mode + '/run.log')
            time.sleep(2)
        finally:
            if proc.poll() is None:
                proc.send_signal(signal.SIGINT)
                proc.wait(timeout=60)
    ctl(['audit','-dir',lab],mode+'/audit.log')
    ctl(['chain-v4' if mode=='chain' else 'e5-load','-dir',lab,'-audit'],mode+'/workload-audit.log')
    shutil.copytree(lab/'reports',dest/'reports')
    audit=json.loads((dest/'reports/audit.json').read_text())
    committees=[r for r in audit if r['Name'].startswith('committee')]
    count=100 if mode=='chain' else 1000
    assert len(committees)==4 and len({r['StateHash'] for r in committees})==1
    assert all(r['Payments']==r['Closed']==count and r['Gap']=='0' for r in committees)
    assert all(r.get('Pending',0)==0 for r in audit)
    (dest/'passed.json').write_text(json.dumps({'count':count,'state_agreement':True,'outbox_empty':True}))
    print('PASS',mode,count,flush=True)
print('ALL REGRESSIONS PASS',flush=True)
