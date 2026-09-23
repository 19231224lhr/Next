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
        with tarfile.open(target,'w:gz',compresslevel=6) as tar:
            for p in files:tar.add(p,arcname=str(p.relative_to(ROOT)))
        manifest.append({'case':case.name,'file':target.name,'bytes':target.stat().st_size,
                         'sha256':hashlib.sha256(target.read_bytes()).hexdigest(),'source_files':len(files),
                         'passed_marker':(case/'passed.json').exists(),
                         'role':'formal' if case.name.startswith('v5-A') else 'final-conflicts' if case.name=='v6-conflicts' else 'control' if case.name.startswith('v3-B') or case.name.startswith('v3-D') else 'pilot-or-debug'})
    (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps({'archives':len(manifest),'bytes':sum(r['bytes'] for r in manifest)},indent=2))

if __name__=='__main__':main()
