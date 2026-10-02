"""Run the executed-prefix reader microbenchmark serially on Mac Studio."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

p = argparse.ArgumentParser()
p.add_argument('--root', type=Path, required=True)
args = p.parse_args()
root = args.root.resolve()
out = root / 'results'
out.mkdir()  # New evidence only; never overwrite a previous run.
env = os.environ.copy()
env['PATH'] = '/usr/local/go/bin:/opt/homebrew/bin:' + env['PATH']
env['GOMAXPROCS'] = '16'
env['GOGC'] = '100'
for k in list(env):
    if k.startswith('UTXO_') or k == 'GODEBUG':
        del env[k]

def command(argv, name, extra=None):
    start = time.time()
    with (out / (name + '.log')).open('w') as log:
        result = subprocess.run(argv, cwd=root, env=env | (extra or {}),
                                stdout=log, stderr=subprocess.STDOUT, timeout=900)
    (out / (name + '.status.json')).write_text(json.dumps({
        'command': argv, 'start_unix': start, 'end_unix': time.time(),
        'returncode': result.returncode}, indent=2))
    result.check_returncode()

metadata = {'production_commit': '721800c7de0a6526f58501fb78823d99426b69cb',
            'source_archive_commit': '0d90417', 'env': {k: env[k] for k in ('GOMAXPROCS','GOGC')},
            'test_sha256': hashlib.sha256((root/'internal/redaction/reader_cost_test.go').read_bytes()).hexdigest()}
for key, argv in [('go', ['go','version']), ('hardware', ['sysctl','-n','machdep.cpu.brand_string']),
                  ('memory', ['sysctl','-n','hw.memsize'])]:
    metadata[key] = subprocess.check_output(argv, cwd=root, env=env, text=True).strip()
(out/'build.json').write_text(json.dumps(metadata, indent=2))
command(['go','test','-count=1','-tags=comet_v3','./internal/redaction',
         '-run','^TestHistoricalReaderEquivalence$'], 'equivalence')
command(['go','vet','-tags=comet_v3','./internal/redaction'], 'vet')
for run in range(1,4):
    command(['go','test','-count=1','-tags=comet_v3','./internal/redaction',
             '-run','^TestHistoricalReaderExperiment$','-v'], f'run{run}',
            {'UTXO_READER_RESULTS': str(out/f'run{run}')})
    print(f'run {run} complete', flush=True)
print('ALL READER RUNS PASS', flush=True)
