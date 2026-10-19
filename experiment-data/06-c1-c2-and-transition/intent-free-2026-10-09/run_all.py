"""Run the major v5 experiments serially on fresh, separately named roots."""
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import time

BASE = Path('/Users/richz/projects')
ARCHIVE = BASE / 'intent-free-20261009-source-v5.tar.gz'
DRIVERS = 'docs/experiments/intent-free-2026-10-09'
jobs = [('single', 'chain'), ('cross', 'chain-cross'), ('mixed', 'mixed'), ('capital', 'capital')]
record = BASE / 'intent-free-20261009-progress.json'
progress = {'archive_sha256': hashlib.sha256(ARCHIVE.read_bytes()).hexdigest(), 'jobs': []}
def save():
    record.write_text(json.dumps(progress, indent=2) + '\n')
for name, kind in jobs:
    root = BASE / ('intent-free-20261009-' + name)
    root.mkdir(exist_ok=False)
    with tarfile.open(ARCHIVE) as tar:
        for member in tar.getmembers():
            (root / member.name).resolve().relative_to(root.resolve())
            if not member.isfile() and not member.isdir():
                raise ValueError('Unexpected archive link')
        tar.extractall(root)
    row = {'name': name, 'root': str(root), 'start_unix': time.time(), 'status': 'running'}
    progress['jobs'].append(row)
    save()
    head = json.loads((root / 'source-manifest.json').read_text())['git_head_reference']
    if kind.startswith('chain'):
        command = ['python3', str(root / DRIVERS / 'run_chain.py'), '--root', str(root),
                   '--commit', head, '--source-manifest', str(root / 'source-manifest.json')]
        if kind == 'chain-cross':
            command.append('--cross-org')
    elif kind == 'mixed':
        command = ['python3', str(root / DRIVERS / 'run_mixed.py'), '--root', str(root)]
    else:
        command = ['python3', str(root / 'docs/experiments/current-capital-2026-10-05/run.py'),
                   '--output', str(root / 'results'), '--case', 'matrix', '--repeats', '3']
    print('START', name, flush=True)
    with (root / 'formal.log').open('w') as log:
        result = subprocess.run(command, cwd=root, stdout=log, stderr=subprocess.STDOUT)
    row.update(status='passed' if result.returncode == 0 else 'failed',
               returncode=result.returncode, end_unix=time.time())
    save()
    print(row['status'].upper(), name, flush=True)
    if result.returncode:
        raise SystemExit(result.returncode)
    if kind.startswith('chain'):
        subprocess.run(['python3', str(root / DRIVERS / 'analyze_chain.py'), str(root / 'results')], check=True)
    elif kind == 'mixed':
        subprocess.run(['python3', str(root / DRIVERS / 'mixed_collect.py'), str(root)], check=True)
        subprocess.run(['python3', str(root / DRIVERS / 'mixed_analyze.py'), str(root / 'evidence/results')], check=True)
print('ALL MAJOR EXPERIMENTS COMPLETED', flush=True)
