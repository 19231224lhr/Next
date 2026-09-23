#!/usr/bin/env python3
"""Archive public E4 evidence only, after all node processes have stopped."""
import hashlib
import json
from pathlib import Path
import tarfile

ROOT=Path(__file__).resolve().parent

def main():
    out=ROOT/'evidence';out.mkdir(exist_ok=True);manifest=[]
    for case in sorted(ROOT.glob('v*-*')):
        if not case.is_dir():continue
        files=sorted(p for p in case.rglob('*') if p.is_file())
        assert not any(p.suffix in ['.key','.db'] for p in files)
        target=out/(case.name+'.tar.gz')
        config=json.loads((case/'configuration.json').read_text()) if (case/'configuration.json').exists() else {}
        role='pilot-or-debug'
        if config.get('kind') in ['A0','A1','A2'] and config.get('count')==24000:role='formal'
        elif config.get('kind') in ['B1','B2','B3a','B3b','D']:role='control'
        elif config.get('kind')=='C' and case.name not in ['v3-conflicts','v4-conflicts','v5-conflicts']:role='final-conflicts'
        with tarfile.open(target,'w:gz',compresslevel=6) as tar:
            for p in files:tar.add(p,arcname=str(p.relative_to(ROOT)))
        manifest.append({'case':case.name,'file':target.name,'bytes':target.stat().st_size,
                         'sha256':hashlib.sha256(target.read_bytes()).hexdigest(),'source_files':len(files),
                         'passed_marker':(case/'passed.json').exists(),
                         'role':role})
    (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps({'archives':len(manifest),'bytes':sum(r['bytes'] for r in manifest)},indent=2))

if __name__=='__main__':main()
