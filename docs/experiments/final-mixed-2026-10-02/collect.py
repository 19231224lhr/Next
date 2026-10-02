"""Create a compact evidence copy; exclude node databases and private keys."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import shutil
import tarfile

ap = argparse.ArgumentParser()
ap.add_argument('root', type=Path)
args = ap.parse_args()
root = args.root.resolve()
evidence = root / 'evidence'
evidence.mkdir()
manifest = {}
for path in sorted((root / 'results').rglob('*')):
    if not path.is_file():
        continue
    relative = path.relative_to(root / 'results')
    # Public configurations, observations, reports and run logs only.
    assert path.suffix in ('.json', '.jsonl', '.log')
    data = path.read_bytes()
    destination = evidence / 'results' / relative
    destination.parent.mkdir(parents=True, exist_ok=True)
    compressed = len(data) > 200_000
    if compressed:
        destination = destination.with_suffix(destination.suffix + '.gz')
        destination.write_bytes(gzip.compress(data, mtime=0))
    else:
        destination.write_bytes(data)
    manifest[str(relative)] = {'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data),
                               'stored': str(destination.relative_to(evidence)), 'gzip': compressed}
(evidence / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
sources = {name: hashlib.sha256((root / name).read_bytes()).hexdigest()
           for name in ['cmd/payctl/direct.go', 'cmd/payctl/audit.go', 'run.py']}
(evidence / 'client-source-sha256.json').write_text(json.dumps(sources, indent=2) + '\n')
shutil.copy2(root / 'formal.log', evidence / 'formal.log')
with tarfile.open(root / 'mixed-evidence.tar.gz', 'w:gz') as tar:
    tar.add(evidence, arcname='.')
print('Evidence files:', len(manifest), 'archive bytes:', (root / 'mixed-evidence.tar.gz').stat().st_size)
