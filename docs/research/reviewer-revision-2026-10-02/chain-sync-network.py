"""Matched 100-hop chains: vary member sync, then add synchronous wait controls."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time

ROOT = Path('/Users/richz/lab/man/utxo-review-20261002')
os.chdir(ROOT)
BIN = ROOT / 'bin-review3'
OUT = ROOT / 'review-chain-sync-results'
OUT.mkdir(exist_ok=True)
BASE = dict(os.environ, PATH='/usr/local/go/bin:/opt/homebrew/bin:' + os.environ['PATH'],
            UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
            UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_BUDGET='1')
for key in ['COMMITTEE_MEMORY', 'MEMBER_MEMORY', 'GATEWAY_MEMORY', 'MEM_BLOCKSTORE']:
    BASE['UTXO_EXPERIMENT_' + key] = '0'

def run(index, sync, wait):
    name = f'chain-{index}-' + ('sync' if sync else 'nosync') + ('-wait' if wait else '-fast')
    target = OUT / name
    target.mkdir()
    lab = ROOT / '.run' / name
    env = dict(BASE, UTXO_EXPERIMENT_MEMBER_SYNC='1' if sync else '0')
    def ctl(args, output, timeout=300):
        with (target / output).open('w') as f:
            subprocess.run([str(BIN / 'payctl')] + list(map(str, args)), env=env,
                           stdout=f, stderr=f, check=True, timeout=timeout)
    ctl(['init-lab', '-dir', lab, '-port', 31000, '-outputs', 110, '-v4'], 'init.log')
    cfg = json.loads((lab / 'lab.json').read_text())
    cfg['Nodes'] = [n for n in cfg['Nodes'] if n['Binary'] == 'committee'
                    or n['Name'] == 'gateway0' or n['Name'].startswith('org0-member')]
    (lab / 'lab.json').write_text(json.dumps(cfg))
    ctl(['chain-v4', '-dir', lab, '-length', 100, '-prepare', '-e5-owner-fuel'], 'prepare.log')
    metadata = {'nodes': 9, 'length': 100, 'wait_final': wait, 'member_sync': sync,
                'member_GOMAXPROCS': 16, 'member_GOGC': 200, 'wallet_no_sync': True,
                'env': {k:v for k,v in env.items() if k.startswith('UTXO_')},
                'binaries': {p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()}}
    (target / 'configuration.json').write_text(json.dumps(metadata, indent=2))
    with (target / 'lab.log').open('w') as f:
        proc = subprocess.Popen([str(BIN / 'payctl'), 'lab-run', '-dir', str(lab), '-bin', str(BIN)],
                                env=env, stdout=f, stderr=f)
        try:
            deadline = time.monotonic() + 90
            while 'Laboratory running:' not in (target / 'lab.log').read_text():
                if proc.poll() is not None:
                    raise RuntimeError('lab exited')
                if time.monotonic() > deadline:
                    raise TimeoutError('lab ready')
                time.sleep(.2)
            args = ['chain-v4', '-dir', lab, '-length', 100, '-e5-owner-fuel']
            if wait:
                args += ['-wait-final']
            ctl(args, 'chain.log')
            time.sleep(2)
        finally:
            proc.send_signal(signal.SIGINT)
            proc.wait(timeout=60)
    ctl(['audit', '-dir', lab], 'audit.log')
    ctl(['chain-v4', '-dir', lab, '-length', 100, '-audit'], 'chain-audit.log')
    for source in (lab / 'reports').glob('*.json'):
        shutil.copy2(source, target / source.name)
    audit = json.loads((target / 'audit.json').read_text())
    committees = [x for x in audit if x['Name'].startswith('committee')]
    assert len(committees) == 4 and len({x['StateHash'] for x in committees}) == 1
    assert all(x['Gap'] == '0' and x['Payments'] == x['Closed'] == 100 for x in committees)
    assert all(x.get('Pending', 0) == 0 for x in audit)
    print('PASS', name, flush=True)

for i in range(3):
    for sync in ([False, True] if i % 2 == 0 else [True, False]):
        run(i, sync, False)
    run(i, True, True)
print('ALL SYNC CHAIN CONTROLS PASS', flush=True)
