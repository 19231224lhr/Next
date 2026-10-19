"""Freeze v5 source and experiment drivers, excluding runtimes and keys."""
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
files = [ROOT / 'go.mod', ROOT / 'go.sum']
for directory in ('cmd', 'internal', 'crypto', 'protocol', 'finality', 'third_party'):
    files += [p for p in (ROOT / directory).rglob('*') if p.is_file()
              and '__pycache__' not in p.parts]
for directory in ('intent-free-2026-10-09', 'current-capital-2026-10-05'):
    files += [p for p in (ROOT / 'docs/experiments' / directory).iterdir()
              if p.is_file() and p.suffix in ('.py', '.md')]
for name in ('owner-fuel-2026-09-23/reproduce.py',
             'finite-budget-2026-09-23/reproduce.py',
             'finite-budget-2026-09-23/analyze.py',
             'continuous-respending-2026-09-22/reproduce.py'):
    files.append(ROOT / 'docs/experiments' / name)
files = sorted(set(files))
manifest = {
    'protocol': 5,
    'purpose': 'Intent removed; node-enforced immutable genesis wallet binding',
    'git_head_reference': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
    'working_tree_changes_included': True,
    'files': {p.relative_to(ROOT).as_posix(): {
        'bytes': p.stat().st_size, 'sha256': hashlib.sha256(p.read_bytes()).hexdigest()}
        for p in files},
}
path = HERE / 'source-manifest.json'
path.write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
archive = HERE / 'source-v5.tar.gz'
with tarfile.open(archive, 'w:gz') as tar:
    for p in files:
        tar.add(p, arcname=p.relative_to(ROOT).as_posix(), recursive=False)
    tar.add(path, arcname='source-manifest.json')
record = {'archive': archive.name, 'bytes': archive.stat().st_size,
          'archive_sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
          'manifest_sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
(HERE / 'freeze-record.json').write_text(json.dumps(record, indent=2) + '\n')
print(json.dumps(record))
