#!/usr/bin/env python3
"""Run one fresh real-service C3 or legacy workload on Linux ext4."""
import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import platform
import signal
import shutil
import subprocess
import sys
import time
import urllib.request

from metrics import distribution, mixed_metrics

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('next_runner', HERE.parent/'baseline-design-2026-10-13'/'run_next.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)


def save(path, data):
    path.write_text(json.dumps(data, indent=2)+'\n', encoding='utf-8')


def read(path):
    return json.loads(path.read_text(encoding='utf-8'))


def clone_fixture(source, destination):
    """Copy only a pre-service snapshot, updating filesystem paths, not identities."""
    source, destination = source.resolve(), destination.resolve()
    if destination.exists() or not any((source/n).is_file() for n in ('c3-fees-fixture.json','c3-runner-fixture.json')):
        raise ValueError('fixture must be prepared and destination fresh')
    if (source/'reports').is_dir() and any((source/'reports').iterdir()):
        raise ValueError('cannot clone an already executed laboratory')
    if (source/'manifest.json').exists() or any(source.glob('c3-wallet-*.db')):
        raise ValueError('cannot clone a started laboratory')
    shutil.copytree(source, destination, ignore=shutil.ignore_patterns('adapter-logs', 'manifest.json'))
    for path in destination.rglob('*'):
        if path.is_file() and path.suffix in ('.json', '.toml', '.yaml', '.yml'):
            original = path.read_text(encoding='utf-8')
            rewritten = original.replace(str(source), str(destination))
            if original != rewritten:
                path.write_text(rewritten, encoding='utf-8')


def collect_traces(directory, network):
    report = read(directory/'reports'/'chain-v4.json')
    records = []
    for hop in report.get('Hops', []):
        fact = hop['Fact']
        if isinstance(fact, list):
            if not base.valid_output_id(fact):
                raise ValueError('invalid native fact identifier')
            fact = bytes(fact).hex()
        url = network['CommitteeURLs'][0].rstrip('/')+'/debug/settlement/'+fact
        try:
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(url, timeout=5) as response:
                value = json.load(response)
            records.append({'hop': hop['Hop'], 'fact': fact, 'records': value})
        except Exception as exc:
            records.append({'hop': hop['Hop'], 'fact': fact, 'error': str(exc)})
    save(directory/'reports'/'settlement-traces.json', records)


def run(args):
    if sys.platform != 'linux':
        raise RuntimeError('use Linux/WSL, with all processes under the same taskset')
    directory, binary = args.run_dir.resolve(), args.bin.resolve()
    if directory.exists():
        raise FileExistsError(directory)
    directory.parent.mkdir(parents=True, exist_ok=True)
    if base.filesystem_type(directory) != 'ext4':
        raise ValueError('run directory must be Linux ext4')
    for name in ('payctl', 'member', 'gateway', 'committee'):
        if not (binary/name).is_file():
            raise FileNotFoundError(binary/name)
    env = os.environ.copy()
    env['NO_PROXY'] = env['no_proxy'] = '127.0.0.1,localhost'
    for key in (*base.STORAGE_KEYS, 'UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY',
                'UTXO_EXPERIMENT_BUDGET', 'UTXO_SETTLEMENT_TRACE'):
        env.pop(key, None)
    if args.storage == 'memory':
        env.update(base.MEMORY_ENV)
    else:
        env['UTXO_EXPERIMENT_MEMBER_SYNC'] = '1'
    economic = args.workload == 'fees'
    if economic:
        if args.variant != 'c3':
            raise ValueError('fee scenarios require C3')
        env['UTXO_EXPERIMENT_BUDGET'] = '1'
        env['UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY'] = '1'
    if args.trace:
        if args.workload != 'chain':
            raise ValueError('trace supported only on diagnostic chains')
        env['UTXO_SETTLEMENT_TRACE'] = '1'
    payctl = str(binary/'payctl')
    initlog = directory.parent/(directory.name+'-init.log')
    init = [payctl, 'init-lab', '--dir', str(directory), '--c3' if args.variant == 'c3' else '--v4',
            '--outputs', '1', '--port', str(base.find_ports())]
    if args.fixture:
        clone_fixture(args.fixture, directory)
        marker=directory/'c3-runner-fixture.json'
        if marker.exists() and read(marker) != fixture_parameters(args):
            raise ValueError('fixture parameters differ from requested workload')
    else:
        base.run_command(init, initlog, env)
    logs = directory/'adapter-logs'
    logs.mkdir()
    if initlog.exists():
        initlog.replace(logs/'init.log')
    if args.workload == 'chain':
        prepare = [payctl, 'chain-v4', '--dir', str(directory), '--prepare']
    elif args.workload == 'mixed':
        prepare = [payctl, 'e8', '--dir', str(directory), '--prepare', '--wallets', '32',
                   '--capacity', str(max(2048, int(args.rate*args.duration)+1000)), '--seed', '23']
    else:
        prepare = [payctl, 'c3-fees', '--dir', str(directory), '--prepare', '--count', str(args.count), '--timeout', '2s']
    if not args.fixture:
        base.run_command(prepare, logs/'prepare.log', env)
    if args.save_fixture:
        save(directory/'c3-runner-fixture.json',fixture_parameters(args))
        clone_fixture(directory, args.save_fixture)
    network_path = directory/'config'/'network.json'
    network = read(network_path)
    if args.workload == 'chain':
        owner1 = list((directory/'keys'/'owner1.key').read_bytes()[-32:])
        original = network['Genesis']['Outputs']
        network['Genesis']['Outputs'] = [o for o in original if o['Output']['Recipient']['Owner'] != owner1]
        if len(original)-len(network['Genesis']['Outputs']) != 1:
            raise ValueError('receiver genesis CAL removal failed')
        save(network_path, network)
    lab_path = directory/'lab.json'
    lab = read(lab_path)
    save(logs/'original-node-inventory.json', lab['Nodes'])
    if not economic:
        lab['Nodes'] = [n for n in lab['Nodes'] if not n['Name'].startswith('org1-member') and n['Name'] != 'gateway1']
    save(lab_path, lab)
    counts = {name: sum(n['Binary'] == name for n in lab['Nodes']) for name in ('member', 'gateway', 'committee')}
    expected = {'member': 8 if economic else 4, 'gateway': 2 if economic else 1, 'committee': 4}
    if counts != expected:
        raise ValueError(f'unexpected service counts: {counts}')
    manifest = {'variant': args.variant, 'workload': args.workload, 'trace': args.trace,
                'harness_sha256':{n:hashlib.sha256((HERE/n).read_bytes()).hexdigest() for n in ('run_c3.py','run_matrix.py','metrics.py')},
                'storage': args.storage, 'arguments': {k:str(v) if isinstance(v, pathlib.Path) else v for k,v in vars(args).items()},
                'platform': platform.platform(), 'cpu_affinity': sorted(os.sched_getaffinity(0)),
                'environment': {k:v for k,v in env.items() if k.startswith('UTXO_') or k == 'GOMAXPROCS'},
                'services': counts, 'binary_hashes': {n:hashlib.sha256((binary/n).read_bytes()).hexdigest() for n in ('payctl','member','gateway','committee')},
                'started_unix_ns': time.time_ns()}
    save(directory/'manifest.json', manifest)
    lablog = logs/'lab-run.log'
    with lablog.open('w', encoding='utf-8') as stream:
        process = subprocess.Popen([payctl,'lab-run','--dir',str(directory),'--bin',str(binary)],
                                   stdout=stream, stderr=subprocess.STDOUT, env=env, start_new_session=True)
        try:
            base.wait_lab(process, lablog)
            if args.workload == 'chain':
                command = [payctl,'chain-v4','--dir',str(directory),'--length',str(args.length)]
                if args.trace:
                    command.append('--trace')
            elif args.workload == 'mixed':
                command = [payctl,'e8','--dir',str(directory),'--lanes','16','--pending','512',
                           '--warm','100','--rate',str(args.rate),'--duration',f'{args.duration}s','--drain','60s','--chain']
            else:
                command = [payctl,'c3-fees','--dir',str(directory),'--case',args.case,'--count',str(args.count),
                           '--source-delay',f'{args.delay_ms}ms','--drain','90s']
            save(logs/'command.json', command)
            code = base.run_command(command, logs/'workload.log', env, timeout=max(420,args.duration+180), check=False)
            result = {'native_exit_code': code, 'variant':args.variant, 'workload':args.workload}
            if args.workload == 'chain':
                native = read(directory/'reports'/'chain-v4.json')
                result.update(base.chain_result(native, args.length))
                result.update(distribution(result.pop('samples_ms'), 'receipt_ms'))
                result.update(distribution([h['CertificateBytes'] for h in native['Hops']], 'certificate_bytes'))
                result['build_total_ms'] = sum(h['BuildMS'] for h in native['Hops'])
                if args.trace:
                    collect_traces(directory, network)
            elif args.workload == 'mixed':
                result.update(mixed_metrics(read(directory/'reports'/'e8-report.json'), read(directory/'reports'/'e8-start.json')))
            else:
                native = read(directory/'reports'/'c3-fees.json')
                result.update({'case':args.case, 'delay_ms':args.delay_ms, 'count':args.count, 'error':native.get('Error','')})
            save(directory/'result.json', result)
            return result
        finally:
            base.stop_lab(process)


def fixture_parameters(args):
    return {'variant':args.variant,'workload':args.workload,
            'count':args.count if args.workload=='fees' else None,
            'rate':args.rate if args.workload=='mixed' else None,
            'duration':args.duration if args.workload=='mixed' else None}


def main():
    def terminated(_signal, _frame):
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        raise InterruptedError('runner terminated')
    signal.signal(signal.SIGTERM, terminated)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bin', type=pathlib.Path, required=True)
    p.add_argument('--run-dir', type=pathlib.Path, required=True)
    p.add_argument('--variant', choices=('baseline','c3'), required=True)
    p.add_argument('--workload', choices=('chain','mixed','fees'), required=True)
    p.add_argument('--storage', choices=('memory','sync'), default='memory')
    p.add_argument('--trace', action='store_true')
    p.add_argument('--length', type=int, default=100)
    p.add_argument('--count', type=int, default=1)
    p.add_argument('--case', choices=('source-first','child-first','compensated','delay'), default='source-first')
    p.add_argument('--delay-ms', type=int, default=0)
    p.add_argument('--rate', type=int, default=100)
    p.add_argument('--duration', type=int, default=30)
    p.add_argument('--fixture', type=pathlib.Path, help='reuse an unstarted prepared fixture')
    p.add_argument('--save-fixture', type=pathlib.Path, help='snapshot prepared fixture before services start')
    args = p.parse_args()
    if not 1 <= args.length <= 100 or args.count < 1 or args.rate <= 0 or args.duration <= 0 or args.delay_ms < 0:
        p.error('invalid workload dimensions')
    try:
        result = run(args)
        print(json.dumps(result))
        if result['native_exit_code']:
            raise SystemExit(result['native_exit_code'])
    except Exception as exc:
        if args.run_dir.is_dir():
            save(args.run_dir/'runner-error.json', {'error':repr(exc)})
        raise


if __name__ == '__main__':
    main()
