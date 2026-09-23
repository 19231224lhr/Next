#!/usr/bin/env python3
"""Archive only E3 public experimental evidence, never keys or runtime DBs."""
import hashlib
import json
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parent


def main():
    archive = ROOT/'evidence'
    archive.mkdir(exist_ok=True)
    manifest=[]
    for case in sorted(ROOT.iterdir()):
        if not case.is_dir() or not (case.name.startswith('r1-') or case.name.startswith('seq-r')):
            continue
        # Failed launches are evidence too, but an actively running case must
        # be packaged only after the experiment runner has exited.
        files=sorted(p for p in case.rglob('*') if p.is_file())
        target=archive/(case.name+'.tar.gz')
        with tarfile.open(target,'w:gz',compresslevel=6) as tar:
            for p in files:
                tar.add(p,arcname=str(p.relative_to(ROOT)))
        manifest.append({'case':case.name,'file':target.name,'bytes':target.stat().st_size,
            'sha256':hashlib.sha256(target.read_bytes()).hexdigest(),'source_files':len(files),
            'completed_summary':(case/'e3-summary.json').exists() or (case/'reports/liability-audit.json').exists()})
    (archive/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps({'archives':len(manifest),'bytes':sum(r['bytes'] for r in manifest)},indent=2))


if __name__=='__main__':
    main()
