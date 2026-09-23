#!/usr/bin/env python3
"""E3 Mac laboratory: fixed budget, user FUEL, real disk history revisions."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import threading
import time

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
spec = importlib.util.spec_from_file_location('owner', OUT.parent/'owner-fuel-2026-09-23/reproduce.py')
owner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owner)
e2 = owner.e2
e2.OUT = OUT
e2.BIN = ROOT/'.run/e3-bin'
e2.labutil.OUT, e2.labutil.BIN = OUT, e2.BIN
e2.ENV.update(UTXO_EXPERIMENT_MEM_BLOCKSTORE='0', UTXO_EXPERIMENT_COMMITTEE_MEMORY='0',
              UTXO_EXPERIMENT_SERIAL_DIRECT='0')
base_configure, base_command = e2.configure, e2.command
base_sample = e2.sample
PERCENT, SEED = 0, 23
# Calibration candidates; identical for all anomaly ratios. No live top-ups.
CAL_GRANT = 60000


def configure(label, count, kind=None, grant=None):
    dest, runtime, lab, network = base_configure(label, count, 1, CAL_GRANT)
    for g in network['Genesis']['Grants']:
        if g['Key']['Kind'] in [3, 4]:
            g['Amount'] = max(g['Amount'], 1000000000)
    encoded = json.dumps(network, separators=(',', ':'))+'\n'
    Path(lab['Network']).write_text(encoded)
    (dest/'network.json').write_text(encoded)
    e2.write(dest/'e3-config.json', {'percent': PERCENT, 'seed': SEED,
        'cal_grant': CAL_GRANT, 'automatic_refill': False,
        'parent_release': 'after observed committed repair AND all four physical stores',
        'normal_delay_s': 1, 'poll_ms': 250, 'deadlines': 'consensus anchored, 30 seconds',
        'process_sample_s': 2,
        'user_fuel': True, 'code_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'source_sha256': {p: hashlib.sha256((ROOT/p).read_bytes()).hexdigest() for p in [
            'cmd/payctl/direct_budget.go', 'cmd/payctl/direct_repair_probe.go',
            'cmd/committee/repair_runtime.go', 'internal/redaction/observe.go']}})
    return dest, runtime, lab, network


def command(args, log):
    if len(args) > 1 and args[1] == 'budget-v4':
        args = list(args)+['-repair-percent', PERCENT, '-repair-seed', SEED]
    return base_command(args, log)


e2.configure, e2.command = configure, command


def cpu_seconds(value):
    total = 0.0
    for part in value.split(':'):
        total = total*60+float(part)
    return total


def sample(lab, dest, stop, probe=False):
    def processes():
        with (dest/'processes.jsonl').open('w') as log:
            while not stop.is_set():
                stamp = time.time_ns()
                try:
                    text = subprocess.check_output(['ps', '-axo', 'pid=,time=,rss=,command='], text=True)
                    for line in text.splitlines():
                        fields = line.strip().split(None, 3)
                        if len(fields) != 4:
                            continue
                        for node in lab['Nodes']:
                            if '-config' in fields[3] and node['Config'] in fields[3]:
                                log.write(json.dumps({'unix_ns':stamp, 'node':node['Name'], 'pid':int(fields[0]),
                                    'cpu_s':cpu_seconds(fields[1]), 'rss_kib':int(fields[2])})+'\n')
                except (OSError, ValueError, subprocess.CalledProcessError) as error:
                    log.write(json.dumps({'unix_ns':stamp, 'error':str(error)})+'\n')
                log.flush()
                stop.wait(2)
    worker = threading.Thread(target=processes)
    worker.start()
    try:
        base_sample(lab, dest, stop, probe)
    finally:
        worker.join()


e2.sample = sample


def run(label, percent, seed, duration=300, count=0):
    global PERCENT, SEED
    PERCENT, SEED = percent, seed
    dest = owner.run(label, duration=duration, count=count, delay=1, drain=90, disk=True)
    subprocess.run(['python3', str(OUT/'analyze.py'), str(dest)], check=True, cwd=ROOT)
    return dest


if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('--case', choices=['smoke', 'calibration', 'matrix'], default='smoke')
    p.add_argument('--skip-build', action='store_true')
    p.add_argument('--prefix', default='r1-')
    a = p.parse_args()
    if not a.skip_build:
        e2.labutil.build()
    if a.case == 'smoke':
        for i in range(3):
            run(f'{a.prefix}normal-{i}', 0, 23+i, duration=5, count=1)
            run(f'{a.prefix}repair-{i}', 100, 23+i, duration=5, count=1)
    elif a.case == 'calibration':
        for percent in [0, 5]:
            run(f'{a.prefix}calibration-p{percent}', percent, 23, duration=60)
    else:
        # Refuse a formal matrix until these exact binaries/configuration have
        # passed both capacity controls. This is a validity check, not recovery.
        for percent in [0, 5]:
            d = OUT/f'{a.prefix}calibration-p{percent}'
            result = json.loads((d/'e3-summary.json').read_text())
            assert result['passed'], 'calibration not complete'
            config = json.loads((d/'configuration.json').read_text())
            assert config['binaries'] == {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in e2.BIN.iterdir()}
            assert json.loads((d/'e3-config.json').read_text())['cal_grant'] == CAL_GRANT
        for seed, order in [(23, [0, 1, 5]), (37, [1, 5, 0]), (59, [5, 0, 1])]:
            for percent in order:
                run(f'{a.prefix}s{seed}-p{percent}', percent, seed)
