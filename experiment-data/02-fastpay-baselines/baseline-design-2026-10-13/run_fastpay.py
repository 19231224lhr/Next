#!/usr/bin/env python3
"""Run official four-authority FastPay plus a native receipt-timing adapter."""
import argparse
import json
import math
import os
import pathlib
import signal
import shutil
import socket
import subprocess
import sys
import time

def interrupted(signum, _frame):
    raise InterruptedError(f'benchmark interrupted by signal {signum}')

def tcp_ready(config):
    try:
        with socket.create_connection((config['host'],config['base_port']),timeout=.2):
            return True
    except OSError:
        return False


def run(args):
    if sys.platform != 'linux':
        raise RuntimeError('run under Linux')
    count = args.length if args.workload == 'chain' else args.count
    if count < 1 or count > 30000 or not math.isfinite(args.rate) or args.rate < 0:
        raise ValueError('invalid count/rate')
    root = args.run_dir.resolve()
    root.mkdir(parents=True, exist_ok=False)
    binary = args.bin.resolve()
    client = binary / 'examples/baseline_client'
    processes = []
    logs = []
    try:
        descriptions = []
        ports = set()
        for i in range(4) if args.seed_config is None else ():
            # Reserve separate one-shard authority endpoints. Startup is outside timing.
            while True:
                with socket.socket() as s:
                    s.bind(('127.0.0.1', 0))
                    port = s.getsockname()[1]
                if port not in ports:
                    ports.add(port)
                    break
            cfg = root / f'server{i}.json'
            descriptions.append(subprocess.check_output([str(binary/'server'), '--server', str(cfg),
                'generate', '--protocol', args.protocol, '--host', '127.0.0.1', '--port', str(port), '--shards', '1'], text=True))
        if args.seed_config is None:
            (root/'committee.json').write_text(''.join(descriptions))
        else:
            for name in ('committee.json','accounts.json','roles.json','initial_accounts.txt',*(f'server{i}.json' for i in range(4))):
                shutil.copy2(args.seed_config/name,root/name)
        common = ['--dir', str(root), '--workload', args.workload, '--count', str(count), '--amount', '100', '--rate', str(args.rate)]
        if args.client_mode:
            common.extend(('--client-mode',args.client_mode))
        if args.seed_config is None:
            subprocess.run([str(client), *common, '--prepare'], check=True, capture_output=True, text=True)
        for i in range(4):
            log = (root/f'server{i}.log').open('w')
            logs.append(log)
            processes.append(subprocess.Popen([str(binary/'server'), '--server', str(root/f'server{i}.json'), 'run',
                '--committee', str(root/'committee.json'), '--initial-accounts', str(root/'initial_accounts.txt')], stdout=log, stderr=subprocess.STDOUT))
        # The upstream log precedes listener binding; TCP must actually accept.
        configs=[json.loads((root/f'server{i}.json').read_text())['authority'] for i in range(4)]
        end = time.monotonic()+15
        while time.monotonic() < end:
            if any(p.poll() is not None for p in processes):
                raise RuntimeError('authority exited during startup')
            ready = all(tcp_ready(c) for c in configs) if args.protocol=='Tcp' else all('Listening' in (root/f'server{i}.log').read_text(errors='replace') for i in range(4))
            if ready:
                break
            time.sleep(0.05)
        else:
            raise TimeoutError('FastPay authorities did not start')
        result = subprocess.run([str(client), *common], capture_output=True, text=True, timeout=420)
        (root/'native-client.json').write_text(result.stdout)
        (root/'native-client.stderr').write_text(result.stderr)
        if result.returncode:
            raise RuntimeError(f'native FastPay client failed ({result.returncode}): {result.stderr[-2000:]}')
        report = json.loads(result.stdout)
        if report['completed'] != count or len(report['samples_ms']) != count or not report['probe_completed'] or not report['principal_conserved']:
            raise ValueError('incomplete verified receiver samples')
        report.update(run_dir=str(root), processes={'authorities':4,'client':1}, storage='memory')
        return report
    finally:
        for p in processes:
            if p.poll() is None:
                p.send_signal(signal.SIGTERM)
        for p in processes:
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                p.kill(); p.wait()
        for log in logs:
            log.close()


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, interrupted)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bin', required=True, type=pathlib.Path)
    p.add_argument('--run-dir', required=True, type=pathlib.Path)
    p.add_argument('--workload', required=True, choices=('chain','independent'))
    p.add_argument('--length', type=int, default=100)
    p.add_argument('--count', type=int, default=100)
    p.add_argument('--rate', type=float, default=0)
    p.add_argument('--protocol', choices=('Tcp','Udp'), default='Tcp')
    p.add_argument('--client-mode', choices=('normal','receiver-confirmed'))
    p.add_argument('--seed-config', type=pathlib.Path, help='reuse retained test configuration for a failure replay')
    print(json.dumps(run(p.parse_args()), sort_keys=True))
