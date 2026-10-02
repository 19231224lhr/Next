"""Collect public configuration and results after run.py; excludes all private keys."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile

parser = argparse.ArgumentParser()
parser.add_argument('root', type=Path)
args = parser.parse_args()
root = args.root.resolve()
out = root / 'results'
assert 'ALL SIX FINAL CONTINUATION RUNS PASS' in (root / 'progress.log').read_text()
networks = []
for folder in sorted(out.glob('r[123]-*')):
    lab = root / '.run' / folder.name
    cfg = json.loads((lab / 'lab.json').read_text())
    network = json.loads(Path(cfg['Network']).read_text())
    (folder / 'network.json').write_text(json.dumps(network, indent=2) + '\n')
    network.pop('GenesisTime')
    networks.append(network)
assert len(networks) == 6 and all(n == networks[0] for n in networks)
shutil.copy2(root / 'progress.log', out / 'progress.log')
shutil.copy2(root / 'run.py', out / 'executed-run.py')
metadata = {'same_network_except_genesis_time': True,
            'macos': subprocess.check_output(['sw_vers'], text=True),
            'cpu': subprocess.check_output(['sysctl', '-n', 'machdep.cpu.brand_string'], text=True).strip(),
            'memory_bytes': subprocess.check_output(['sysctl', '-n', 'hw.memsize'], text=True).strip()}
(out / 'host.json').write_text(json.dumps(metadata, indent=2) + '\n')
with tarfile.open(root / 'results.tgz', 'w:gz') as archive:
    archive.add(out, arcname='results')
print(hashlib.sha256((root / 'results.tgz').read_bytes()).hexdigest())
