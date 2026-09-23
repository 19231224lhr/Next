#!/usr/bin/env python3
"""Export evidence only: no node databases, binaries, or private keys."""
import gzip
import hashlib
import json
from pathlib import Path
import zipfile

ROOT=Path(__file__).resolve().parent
manifest={}
with zipfile.ZipFile(ROOT/'evidence.zip','w',zipfile.ZIP_STORED) as archive:
    for case in sorted(ROOT.glob('adaptive-r*')):
        if not (case/'summary.json').exists():continue
        for relative in ['summary.json','analysis.json','configuration.json','adaptive-policy.json','health-after.json','last-snapshots.json','network.json','samples.jsonl','reports/budget-v4.json','reports/budget-audit.json','reports/audit.json','reports/reserve-control.jsonl','driver.log','audit.log']:
            path=case/relative
            if not path.exists():continue
            raw=path.read_bytes();name=str(path.relative_to(ROOT))
            manifest[name]={'sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw)}
            archive.writestr(name+'.gz' if len(raw)>100000 else name,gzip.compress(raw,compresslevel=6,mtime=0) if len(raw)>100000 else raw)
    for name in ['analysis.json','summary.csv','curves.csv','build.json','build.log','project-tests.log','race.log','vet.log','matrix.log','source-provenance.json','candidate-source.zip']:
        path=ROOT/name
        if not path.exists():continue
        raw=path.read_bytes();manifest[name]={'sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw)}
        archive.writestr(name+'.gz' if name=='curves.csv' else name,gzip.compress(raw,compresslevel=6,mtime=0) if name=='curves.csv' else raw)
    archive.writestr('manifest.json',json.dumps(manifest,indent=2)+'\n')
print('Packaged',len(manifest),'files; bytes',(ROOT/'evidence.zip').stat().st_size)
