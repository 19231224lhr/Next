"""Verify published files and restore archives split to fit GitHub's file limit."""
from pathlib import Path
import argparse
import hashlib
import json

ROOT = Path(__file__).resolve().parent


def resolve(name):
    p = (ROOT / name).resolve()
    if not p.is_relative_to(ROOT):
        raise ValueError(f'Path outside experiment-data: {name}')
    return p


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def verify_files():
    manifest = json.loads((ROOT / 'FILE-MANIFEST.json').read_text(encoding='utf-8'))
    for entry in manifest['files']:
        p = resolve(entry['path'])
        if p.stat().st_size != entry['bytes'] or digest(p) != entry['sha256']:
            raise ValueError(f'Checksum mismatch: {entry["path"]}')
    print(f'Verified {len(manifest["files"])} published files.')


def restore(entry, verify_only):
    combined = hashlib.sha256()
    size = 0
    for part in entry['parts']:
        p = resolve(part['path'])
        if p.stat().st_size != part['bytes'] or digest(p) != part['sha256']:
            raise ValueError(f'Part checksum mismatch: {part["path"]}')
        with p.open('rb') as f:
            for block in iter(lambda: f.read(1024 * 1024), b''):
                combined.update(block)
                size += len(block)
    if size != entry['bytes'] or combined.hexdigest() != entry['sha256']:
        raise ValueError(f'Archive checksum mismatch: {entry["target"]}')
    target = resolve(entry['target'])
    if verify_only:
        print('Verified archive:', entry['target'])
        return
    if target.exists():
        if digest(target) != entry['sha256']:
            raise FileExistsError(f'Refusing to overwrite: {target}')
        print('Already restored:', entry['target'])
        return
    with target.open('xb') as out:
        for part in entry['parts']:
            with resolve(part['path']).open('rb') as f:
                for block in iter(lambda: f.read(1024 * 1024), b''):
                    out.write(block)
    if digest(target) != entry['sha256']:
        raise ValueError(f'Restored archive checksum mismatch: {target}')
    print('Restored:', entry['target'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify-only', action='store_true', help='Verify checksums without creating combined archives.')
    args = parser.parse_args()
    verify_files()
    for item in json.loads((ROOT / 'ARCHIVES.json').read_text(encoding='utf-8')):
        restore(item, args.verify_only)
