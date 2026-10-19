#!/usr/bin/env python3
"""Archive native evidence and prepared fixtures without live service databases."""
import argparse
import hashlib
import json
import pathlib
import tarfile


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', type=pathlib.Path, required=True)
    p.add_argument('--out', type=pathlib.Path, required=True)
    p.add_argument('--smokes', action='store_true', help='select only smoke*/perf-smoke* directories under root')
    a = p.parse_args()
    root = a.root.resolve()
    a.out.parent.mkdir(parents=True, exist_ok=True)
    if a.out.exists():
        raise FileExistsError(a.out)
    selected = []
    for path in sorted(root.rglob('*')):
        if not path.is_file():
            continue
        rel = path.relative_to(root)
        if a.smokes and not rel.parts[0].startswith(('smoke', 'perf-smoke')):
            continue
        fixture = any(part.startswith('fixture-') for part in rel.parts)
        # Prepared fixtures contain only experiment-generated identities/state.
        # Native JSON retains signed requests, blocks, results and snapshots.
        if fixture or path.suffix in {'.json', '.jsonl', '.csv', '.log', '.toml', '.key'}:
            selected.append((path, rel))
    with tarfile.open(a.out, 'x:gz') as archive:
        for path, rel in selected:
            archive.add(path, arcname=str(rel), recursive=False)
    digest = hashlib.file_digest(a.out.open('rb'), 'sha256').hexdigest()
    index = {'root': str(root), 'archive': a.out.name, 'sha256': digest,
             'files': len(selected), 'bytes': a.out.stat().st_size,
             'scope': 'native reports/configs/logs plus complete prepared fixtures; excludes live run databases',
             'identities': 'all included private keys are disposable local experiment identities'}
    a.out.with_suffix(a.out.suffix + '.json').write_text(json.dumps(index, indent=2) + '\n')
    print(json.dumps(index))


if __name__ == '__main__':
    main()
