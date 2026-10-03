"""Mixed ordinary payments and source recovery, using frozen node binaries.

Node binaries and all source files match the final-chain build. The client
adds no changes between these two experiments. Run serially on the Mac Studio.
"""
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


def write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def events(path):
    if not path.exists():
        return []
    result = []
    for line in path.read_text().splitlines():
        if line.startswith('EVENT '):
            try:
                result.append(json.loads(line[6:]))
            except json.JSONDecodeError:
                pass  # A concurrent log write may be incomplete; read next tick.
    return result


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--root', type=Path, required=True)
    ap.add_argument('--frozen', type=Path, required=True)
    ap.add_argument('--name', default='results')
    ap.add_argument('--rounds', type=int, default=3)
    ap.add_argument('--modes', default='NRCP')
    ap.add_argument('--count', type=int, default=7000)
    ap.add_argument('--pairs', type=int, default=8)
    args = ap.parse_args()
    root, frozen = args.root.resolve(), args.frozen.resolve()
    os.chdir(root)
    out = root / args.name
    out.mkdir()  # Never overwrite prior evidence.
    (root / '.run').mkdir(exist_ok=True)
    binary = frozen / 'bin-final'
    payctl = root / 'payctl-mixed'
    env = {k: v for k, v in os.environ.items()
           if not k.startswith('UTXO_') and k not in ('GODEBUG', 'GOGC', 'GOMAXPROCS')}
    env['PATH'] = '/usr/local/go/bin:/opt/homebrew/bin:' + env['PATH']
    env.update(UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
               UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_BUDGET='1',
               UTXO_EXPERIMENT_MEMBER_SYNC='1', UTXO_SETTLEMENT_TRACE='1')
    for key in ['COMMITTEE_MEMORY', 'MEMBER_MEMORY', 'GATEWAY_MEMORY', 'MEM_BLOCKSTORE']:
        env['UTXO_EXPERIMENT_' + key] = '0'

    def cmd(argv, log, treatment=None, timeout=300):
        with log.open('w') as f:
            subprocess.run(list(map(str, argv)), cwd=root, env=treatment or env,
                           stdout=f, stderr=subprocess.STDOUT, check=True, timeout=timeout)

    cmd(['go', 'test', '-tags=comet_v3', './cmd/payctl'], out / 'client-tests.log')
    cmd(['go', 'build', '-tags=comet_v3', '-o', payctl, './cmd/payctl'], out / 'client-build.log')
    prior = json.loads((frozen / 'results/build.json').read_text())
    hashes = {str(p.relative_to(frozen)): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in binary.iterdir()}
    assert hashes == prior['binaries'], 'frozen node binaries changed'
    modified = []
    for name, digest in prior['source'].items():
        if hashlib.sha256((root / name).read_bytes()).hexdigest() != digest:
            modified.append(name)
    assert modified == [], modified  # Same final build, including observation client.
    write(out / 'build.json', {'base': prior, 'changed_client_files': modified,
          'client_sha256': hashlib.sha256(payctl.read_bytes()).hexdigest(),
          'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
          'actual_binary_hashes': hashes,
          'env': {k: v for k, v in env.items() if k.startswith('UTXO_')}})
    template = root / '.run' / (args.name + '-template')
    cmd([payctl, 'init-lab', '-dir', template, '-port', 32000,
         '-outputs', args.count + 100, '-v4'], out / 'init.log')
    cfg = json.loads((template / 'lab.json').read_text())
    write(out / 'genesis-template.json', json.loads(Path(cfg['Network']).read_text()))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def get(url):
        start = time.time_ns()
        with opener.open(url, timeout=3) as response:
            value = json.load(response)
        return {'start_ns': start, 'end_ns': time.time_ns(), 'value': value}

    for rep in range(1, args.rounds + 1):
        order = list(args.modes)
        if rep % 2 == 0:
            order.reverse()
        for mode in order:
            name = f'r{rep}-{mode}'
            target = out / name
            target.mkdir()
            lab = root / '.run' / (args.name + '-' + name)
            shutil.copytree(template, lab)
            for p in [lab / 'lab.json', *(lab / 'config').glob('*.json')]:
                p.write_text(p.read_text().replace(str(template), str(lab)))
            cfg = json.loads((lab / 'lab.json').read_text())
            network = json.loads(Path(cfg['Network']).read_text())
            network['GenesisTime'] = (datetime.now(timezone.utc) - timedelta(seconds=1)).isoformat()
            write(Path(cfg['Network']), network)
            treatment = env.copy()
            if mode in 'CP':
                treatment['UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY'] = '1'
            pause = target / 'adaptation.paused'
            if mode == 'P':
                pause.write_text('pause all adaptation endpoints\n')
                treatment['UTXO_EXPERIMENT_ADAPTATION_PAUSE_FILE'] = str(pause)
            write(target / 'configuration.json', {'mode': mode, 'repeat': rep,
                  'normal_count': args.count, 'normal_rate': 100, 'pairs': args.pairs,
                  'nodes': 14, 'normal_wallet_no_sync': True, 'fault_wallet_sync': True,
                  'fault_observe_for_s': 55, 'fee_mode': 'explicit organization sponsorship',
                  'auto_topup': False, 'env': {k: v for k, v in treatment.items() if k.startswith('UTXO_')}})
            print('START', name, flush=True)
            logs, jobs = [], []
            with (target / 'lab.log').open('w') as logfile:
                proc = subprocess.Popen([str(binary / 'payctl'), 'lab-run', '-dir', str(lab), '-bin', str(binary)],
                                        env=treatment, stdout=logfile, stderr=logfile)
                try:
                    deadline = time.monotonic() + 90
                    while 'Laboratory running:' not in (target / 'lab.log').read_text():
                        if proc.poll() is not None:
                            raise RuntimeError('laboratory exited')
                        if time.monotonic() > deadline:
                            raise TimeoutError('laboratory readiness')
                        time.sleep(.2)
                    time.sleep(3)
                    f = (target / 'bench.log').open('w'); logs.append(f)
                    bench = subprocess.Popen([str(payctl), 'bench-v4', '-dir', str(lab), '-start', '100',
                            '-count', str(args.count), '-rate', '100', '-concurrency', '64',
                            '-max-pending', '256', '-wallet-no-sync'], env=treatment, stdout=f, stderr=f)
                    jobs.append(bench)
                    started = time.monotonic()
                    next_sample = started
                    fault_jobs, resumed = [], mode != 'P'
                    with (target / 'observations.jsonl').open('w') as samplelog:
                        while True:
                            now = time.monotonic()
                            if now - started > 160:
                                raise TimeoutError('mixed run')
                            if len(fault_jobs) < args.pairs and now - started >= 10 + len(fault_jobs):
                                i = len(fault_jobs)
                                f = (target / f'pair-{i}.log').open('w'); logs.append(f)
                                flags = [] if mode == 'N' else ['-withhold-parent']
                                flags += ['-recover-source'] if mode == 'R' else (['-wait-repair'] if mode in 'CP' else [])
                                job = subprocess.Popen([str(payctl), 'demo-v4', '-dir', str(lab),
                                    '-input', str(i), '-events', '-observe-for', '55s', *flags],
                                    env=treatment, stdout=f, stderr=f)
                                jobs.append(job); fault_jobs.append(job)
                            if now >= next_sample:
                                sample = {'unix_ns': time.time_ns(), 'nodes': {}, 'targets': {}, 'errors': {}}
                                for node in cfg['Nodes']:
                                    if node['Binary'] == 'member' or node['Name'] == 'committee0':
                                        try:
                                            sample['nodes'][node['Name']] = get(node['URL'] + '/debug/budget')
                                        except Exception as e:
                                            sample['errors'][node['Name']] = str(e)
                                for i in range(len(fault_jobs)):
                                    ready = [e for e in events(target / f'pair-{i}.log') if e['stage'] == 'parent_ready']
                                    if not ready:
                                        continue
                                    output = ready[0]['output']
                                    row = {'output': output, 'replica': 0}
                                    for key, suffix in [('obligation', '/v3/obligations/' + output),
                                                        ('repair', '/v3/repairs/' + output + '/status')]:
                                        try:
                                            row[key] = get(network['CommitteeURLs'][0] + suffix)
                                        except Exception as e:
                                            row[key + '_unavailable'] = str(e)
                                    sample['targets'][str(i)] = row
                                samplelog.write(json.dumps(sample) + '\n'); samplelog.flush()
                                next_sample = time.monotonic() + 1
                            if not resumed and len(fault_jobs) == args.pairs:
                                reports = [lab / f'reports/direct-{i}.json' for i in range(args.pairs)]
                                if all(p.exists() for p in reports):
                                    snapshots = []
                                    for p in reports:
                                        report = json.loads(p.read_text())
                                        output = report['parent_output']
                                        nodes = [{'obligation': get(u + '/v3/obligations/' + output),
                                                  'repair': get(u + '/v3/repairs/' + output + '/status')}
                                                 for u in network['CommitteeURLs']]
                                        snapshots.append({'output': output, 'nodes': nodes})
                                    if all(x['obligation']['value']['Status'] == 3 for s in snapshots for x in s['nodes']):
                                        assert all(not x['repair']['value']['Committed'] and not x['repair']['value']['Materialized']
                                                   for s in snapshots for x in s['nodes'])
                                        write(target / 'paused-observations.json', snapshots)
                                        pause.unlink()  # Own experiment marker only.
                                        write(target / 'resume.json', {'unix_ns': time.time_ns()})
                                        resumed = True
                                        print('RESUMED', name, flush=True)
                            for job in jobs:
                                if job.poll() not in (None, 0):
                                    raise RuntimeError('client failed; inspect bench/pair logs')
                            if len(fault_jobs) == args.pairs and all(j.poll() == 0 for j in jobs):
                                break
                            time.sleep(.1)
                    assert resumed
                    # Independently query all replicas and all local resource states.
                    final = {}
                    for i in range(args.pairs):
                        report = json.loads((lab / f'reports/direct-{i}.json').read_text())
                        output = report['parent_output']
                        final[str(i)] = []
                        for u in network['CommitteeURLs']:
                            row = {'health': get(u + '/healthz')}
                            if mode != 'N':
                                row['obligation'] = get(u + '/v3/obligations/' + output)
                            if mode in 'CP':
                                row['repair'] = get(u + '/v3/repairs/' + output + '/status')
                            final[str(i)].append(row)
                    write(target / 'final-status.json', final)
                    write(target / 'final-resources.json', {n['Name']: get(n['URL'] + '/debug/budget?details=1')
                          for n in cfg['Nodes'] if n['Binary'] in ['member', 'committee']})
                    time.sleep(2)
                finally:
                    for job in jobs:
                        if job.poll() is None:
                            job.terminate(); job.wait(timeout=10)
                    if proc.poll() is None:
                        proc.send_signal(signal.SIGINT); proc.wait(timeout=60)
                    for log in logs:
                        log.close()
            cmd([payctl, 'audit', '-dir', lab], target / 'audit.log', treatment)
            for p in (lab / 'reports').glob('*.json'):
                shutil.copy2(p, target / p.name)
            rows = json.loads((target / 'audit.json').read_text())
            committees = [n for n in rows if n['Name'].startswith('committee')]
            assert len(committees) == 4 and len({n['StateHash'] for n in committees}) == 1
            assert all(n['Gap'] == '0' and n['Payments'] == n['Closed'] == args.count + 2 * args.pairs for n in committees)
            assert all(n.get('Pending', 0) == 0 for n in rows)
            bench = json.loads((target / 'bench-v4-100.json').read_text())
            assert bench['Summary']['failed'] == 0
            write(target / 'events.json', {str(i): events(target / f'pair-{i}.log') for i in range(args.pairs)})
            print('PASS', name, bench['Summary'], flush=True)
    print('ALL MIXED RUNS PASS', flush=True)


if __name__ == '__main__':
    main()
