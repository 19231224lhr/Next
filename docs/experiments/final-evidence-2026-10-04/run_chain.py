"""Frozen-build, paired 100-hop continuation test; run on the Mac Studio."""
import argparse
from datetime import datetime, timezone, timedelta
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
import urllib.request


def write(path, data):
    path.write_text(json.dumps(data, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--commit', required=True)
    args = parser.parse_args()
    root = args.root.resolve()
    (root / '.run').mkdir(exist_ok=True)
    out = root / 'results'
    out.mkdir()  # Never replace an existing experiment.
    binary = root / 'bin-final'
    binary.mkdir()
    env = {k: v for k, v in os.environ.items()
           if not k.startswith('UTXO_') and k not in ('GODEBUG', 'GOGC', 'GOMAXPROCS')}
    env['PATH'] = '/usr/local/go/bin:/opt/homebrew/bin:' + env['PATH']
    env.update(UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
               UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_BUDGET='1',
               UTXO_EXPERIMENT_MEMBER_SYNC='1', UTXO_SETTLEMENT_TRACE='1')
    for key in ['COMMITTEE_MEMORY', 'MEMBER_MEMORY', 'GATEWAY_MEMORY', 'MEM_BLOCKSTORE']:
        env['UTXO_EXPERIMENT_' + key] = '0'

    def cmd(argv, log, timeout=300):
        with log.open('w') as f:
            subprocess.run(list(map(str, argv)), cwd=root, env=env, stdout=f,
                           stderr=subprocess.STDOUT, check=True, timeout=timeout)

    cmd(['python3', 'third_party/cometbft/overlay.py'], out / 'overlay.log')
    for role in ['payctl', 'committee', 'gateway', 'member']:
        target = binary / ('member-real' if role == 'member' else role)
        cmd(['go', 'build', '-tags=comet_v3', '-o', target, './cmd/' + role], out / (role + '-build.log'))
    wrapper = binary / 'member'
    wrapper.write_text('#!/bin/sh\nexec env GOMAXPROCS=16 GOGC=200 "' + str(binary / 'member-real') + '" "$@"\n')
    wrapper.chmod(0o755)
    hashes = lambda paths: {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}
    source = [p for name in ['cmd', 'internal', 'crypto', 'protocol', 'finality', 'third_party']
              for p in (root / name).rglob('*') if p.is_file()]
    write(out / 'build.json', {'commit': args.commit, 'source': hashes(source),
          'binaries': hashes(binary.iterdir()), 'comet_go': hashes((root / '.scratch/comet-src').rglob('*.go')),
          'go': subprocess.check_output(['go', 'version'], env=env, text=True).strip(),
          'env': {k: v for k, v in env.items() if k.startswith('UTXO_')},
          'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()})
    cmd(['go', 'test', '-tags=comet_v3', './cmd/payctl', './internal/member', './internal/wallet', './internal/committee'], out / 'tests.log')
    print('BUILD AND TARGETED TESTS PASS', flush=True)

    ctl = lambda argv, log: cmd([binary / 'payctl'] + list(argv), log)
    template = root / '.run/template'
    ctl(['init-lab', '-dir', template, '-port', 31000, '-outputs', 110, '-v4'], out / 'init.log')
    ctl(['chain-v4', '-dir', template, '-length', 100, '-prepare', '-e5-owner-fuel'], out / 'prepare.log')
    cfg = json.loads((template / 'lab.json').read_text())
    cfg['Nodes'] = [n for n in cfg['Nodes'] if n['Binary'] == 'committee'
                    or n['Name'] == 'gateway0' or n['Name'].startswith('org0-member')]
    assert len(cfg['Nodes']) == 9
    write(template / 'lab.json', cfg)
    # Public configuration only; never archive the generated private keys.
    pristine = json.loads(Path(cfg['Network']).read_text())
    write(out / 'genesis-template.json', pristine)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def get(url):
        with opener.open(url, timeout=5) as r:
            return json.load(r)

    for repeat in range(1, 4):
        for wait in ([False, True] if repeat % 2 else [True, False]):
            name = f'r{repeat}-' + ('wait' if wait else 'fast')
            target = out / name
            target.mkdir()
            lab = root / '.run' / name
            shutil.copytree(template, lab)
            for p in [lab / 'lab.json', *(lab / 'config').glob('*.json')]:
                p.write_text(p.read_text().replace(str(template), str(lab)))
            cfg = json.loads((lab / 'lab.json').read_text())
            network = json.loads(Path(cfg['Network']).read_text())
            network['GenesisTime'] = (datetime.now(timezone.utc) - timedelta(seconds=1)).isoformat()
            write(Path(cfg['Network']), network)
            write(target / 'configuration.json', {'wait_final': wait, 'hops': 100,
                  'nodes': 9, 'owner_fuel': True, 'member_sync': True, 'wallet_no_sync': True,
                  'env': {k: v for k, v in env.items() if k.startswith('UTXO_')},
                  'member_GOMAXPROCS': 16, 'member_GOGC': 200})
            print('START', name, flush=True)
            with (target / 'lab.log').open('w') as log:
                proc = subprocess.Popen([str(binary / 'payctl'), 'lab-run', '-dir', str(lab), '-bin', str(binary)],
                                        cwd=root, env=env, stdout=log, stderr=log)
                try:
                    deadline = time.monotonic() + 90
                    while 'Laboratory running:' not in (target / 'lab.log').read_text():
                        if proc.poll() is not None:
                            raise RuntimeError('lab exited')
                        if time.monotonic() > deadline:
                            raise TimeoutError('lab ready')
                        time.sleep(.2)
                    time.sleep(3)
                    command = ['chain-v4', '-dir', lab, '-length', 100, '-e5-owner-fuel']
                    if wait:
                        command += ['-wait-final']
                    ctl(command, target / 'chain.log')
                    report = json.loads((lab / 'reports/chain-v4.json').read_text())
                    events = {str(i): {bytes(h['Fact']).hex(): get(url + '/debug/settlement/' + bytes(h['Fact']).hex())
                                     for h in report['Hops']} for i, url in enumerate(network['CommitteeURLs'])}
                    write(target / 'settlements.json', events)
                    time.sleep(2)
                finally:
                    if proc.poll() is None:
                        proc.send_signal(signal.SIGINT)
                        proc.wait(timeout=60)
            ctl(['audit', '-dir', lab], target / 'audit.log')
            ctl(['chain-v4', '-dir', lab, '-audit'], target / 'chain-audit.log')
            for p in (lab / 'reports').glob('*.json'):
                if p.name != 'fault-v4.json':  # Full signed samples are unnecessary for this timing audit.
                    shutil.copy2(p, target / p.name)
            rows = json.loads((target / 'audit.json').read_text())
            committees = [n for n in rows if n['Name'].startswith('committee')]
            assert len(committees) == 4 and len({n['StateHash'] for n in committees}) == 1
            assert all(n['Gap'] == '0' and n['Payments'] == n['Closed'] == 100 for n in committees)
            assert all(n.get('Pending', 0) == 0 for n in rows)
            assert not report.get('Error') and len(report['Hops']) == 100
            print('PASS', name, {k: report[k] for k in ['FastChainMS', 'PublicChainMS', 'ClosedChainMS']}, flush=True)
    print('ALL SIX FINAL CONTINUATION RUNS PASS', flush=True)


if __name__ == '__main__':
    main()
