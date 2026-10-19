#!/usr/bin/env python3
"""Additional 12 s delay condition, preserving all original delay observations."""
import argparse
import json
import pathlib
import subprocess
import sys
import time


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', type=pathlib.Path, required=True)
    p.add_argument('--fixtures', type=pathlib.Path, required=True)
    p.add_argument('--current-bin', type=pathlib.Path, required=True)
    a = p.parse_args()
    a.root.mkdir(parents=True, exist_ok=False)
    (a.root/'extension-plan.json').write_text(json.dumps({
        'delay_ms': 12000, 'repeats': 3, 'count': 20,
        'reason': 'Original 0–4000 ms conditions completed without compensation; add a longer delay to observe the transition. Original results retained.',
        'created_unix_ns': time.time_ns()}, indent=2) + '\n')
    for repeat in range(1, 4):
        name = f'delays-r{repeat}-12000ms'
        command = [sys.executable, str(pathlib.Path(__file__).with_name('run_c3.py')),
                   '--bin', str(a.current_bin), '--run-dir', str(a.root/name),
                   '--variant', 'c3', '--workload', 'fees', '--case', 'delay',
                   '--count', '20', '--delay-ms', '12000',
                   '--fixture', str(a.fixtures/f'fixture-delays-r{repeat}')]
        print('START ' + name, flush=True)
        with (a.root/(name+'-runner.log')).open('x') as log:
            result = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT)
        with (a.root/'extension-status.jsonl').open('a') as status:
            status.write(json.dumps({'name': name, 'returncode': result.returncode}) + '\n')
        print('END ' + name + ' ' + str(result.returncode), flush=True)
        if result.returncode:
            raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
