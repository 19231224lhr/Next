#!/usr/bin/env python3
"""Freeze current production files and every transitive E2 Python helper."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
EXTRA = [
    'docs/experiments/current-capital-2026-10-05/run.py',
    'docs/experiments/current-capital-2026-10-05/freeze.py',
    'docs/experiments/current-capital-2026-10-05/README.md',
    'docs/experiments/owner-fuel-2026-09-23/reproduce.py',
    'docs/experiments/finite-budget-2026-09-23/reproduce.py',
    'docs/experiments/finite-budget-2026-09-23/analyze.py',
    'docs/experiments/continuous-respending-2026-09-22/reproduce.py',
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path, help='new source package directory')
    parser.add_argument('--extra', action='append', default=[], help='additional repository-relative file/directory')
    args = parser.parse_args()
    files = [ROOT / 'go.mod', ROOT / 'go.sum', *[ROOT / p for p in EXTRA]]
    for directory in ['cmd', 'internal', 'protocol', 'crypto', 'finality']:
        files.extend(p for p in (ROOT / directory).rglob('*') if p.is_file()
                     and (p.suffix in ['.go', '.mod', '.sum', '.proto', '.tmpl', '.h', '.c', '.s']
                          or 'testdata' in p.parts))
    excluded = {'.git', 'node_modules', '__pycache__', 'bin'}
    for directory in ['third_party/cometbft', '.scratch/comet-src',
                      'docs/experiments/wait-baseline-2026-10-05', *args.extra]:
        relative = Path(directory)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError('extra inputs must be repository-relative without parent traversal')
        # Keep the logical repository path: .scratch can be a managed junction
        # to the actual Comet tree in another checkout. Archive its file bytes.
        target = ROOT / relative
        if not target.exists():
            if directory in args.extra:
                raise FileNotFoundError(target)
            continue
        files.extend(p for p in ([target] if target.is_file() else target.rglob('*'))
                     if p.is_file() and not any(part in excluded for part in p.parts))
    files = sorted(set(files))
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    manifest = {'files': {
        p.relative_to(ROOT).as_posix(): {'bytes': p.stat().st_size,
               'sha256': hashlib.sha256(p.read_bytes()).hexdigest()} for p in files}}
    try:
        manifest['head'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        manifest['git_status'] = subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True)
        (output / 'working-tree.patch').write_bytes(subprocess.check_output(
            ['git', 'diff', '--binary', '--', 'cmd', 'internal', 'crypto', 'finality',
             'protocol', 'third_party', 'go.mod', 'go.sum',
             'docs/experiments/current-capital-2026-10-05',
             'docs/experiments/wait-baseline-2026-10-05'], cwd=ROOT))
    except (OSError, subprocess.CalledProcessError):
        manifest['head'] = 'no Git metadata; see byte manifest'
    (output / 'source-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
    archive_path = output / 'source.tgz'
    with tarfile.open(archive_path, 'w:gz') as archive:
        for path in files:
            archive.add(path, arcname=path.relative_to(ROOT).as_posix(), recursive=False)
        archive.add(output / 'source-manifest.json', arcname='source-bundle-manifest.json')
        if (output / 'working-tree.patch').exists():
            archive.add(output / 'working-tree.patch', arcname='source-working-tree.patch')
    print(json.dumps({'archive': str(archive_path), 'files': len(files),
                      'sha256': hashlib.sha256(archive_path.read_bytes()).hexdigest()}))


if __name__ == '__main__':
    main()
