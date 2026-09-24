#!/usr/bin/env python3
"""Fetch fixed official macOS ARM64 releases and verify HTTPS SHA256 manifests."""
import hashlib
import json
from pathlib import Path
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
DEST = ROOT / '.run/ln-tools'
RELEASES = [
    ('https://github.com/lightningnetwork/lnd/releases/download/v0.21.3-beta/',
     'manifest-v0.21.3-beta.txt', 'lnd-darwin-arm64-v0.21.3-beta.tar.gz'),
    ('https://bitcoincore.org/bin/bitcoin-core-31.1/',
     'SHA256SUMS', 'bitcoin-31.1-arm64-apple-darwin.tar.gz'),
]

def main():
    DEST.mkdir(parents=True, exist_ok=True)
    records = []
    for base, sums, name in RELEASES:
        for filename in [sums, name]:
            target = DEST / filename
            if not target.exists():
                partial = target.with_suffix(target.suffix + '.part')
                urllib.request.urlretrieve(base + filename, partial)
                partial.replace(target)
        expected = next(line.split()[0] for line in (DEST / sums).read_text().splitlines()
                        if line.split()[-1].lstrip('*') == name)
        digest = hashlib.sha256((DEST / name).read_bytes()).hexdigest()
        if digest != expected:
            raise RuntimeError('checksum mismatch: ' + name)
        with tarfile.open(DEST / name) as archive:
            for member in archive.getmembers():
                if DEST.resolve() not in (DEST / member.name).resolve().parents:
                    raise RuntimeError('archive path outside tools directory')
            archive.extractall(DEST)
        records.append({'url': base + name, 'sha256': digest, 'checksum_source': base + sums})
        print('verified', name, flush=True)
    out = ROOT / 'docs/experiments/lightning-2026-09-24'
    out.mkdir(parents=True, exist_ok=True)
    (out / 'downloads.json').write_text(json.dumps(records, indent=2))

if __name__ == '__main__':
    main()
