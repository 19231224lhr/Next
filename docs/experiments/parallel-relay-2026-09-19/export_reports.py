"""Export completed reports only; never include lab identities, keys, or DBs."""
import hashlib,json,tarfile
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-fastpay-v12')
out=root/'.run/parallel-relay'
with tarfile.open(out/'all-reports.tgz','w:gz') as archive:
    for experiment in sorted((root/'.run').glob('group-parallel-*/reports/experiment.json')):
        reports=experiment.parent
        archive.add(reports,arcname=reports.relative_to(root/'.run'))
hashes={}
for name in ['relay-step2-bin','relay-memory-a-bin','relay-parallel-b-bin','relay-profile-bin','relay-final-bin']:
    hashes[name]={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (root/'.run'/name).iterdir() if p.is_file()}
(out/'binaries.json').write_text(json.dumps(hashes,indent=2)+'\n')
