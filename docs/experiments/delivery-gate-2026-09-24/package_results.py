#!/usr/bin/env python3
"""Archive E5 public experiment evidence only; never runtime databases or keys."""
import gzip
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import tarfile

OUT=Path(__file__).resolve().parent
DEST=OUT/'evidence';DEST.mkdir(exist_ok=True)
manifest={}
parser=argparse.ArgumentParser();parser.add_argument('--prefix',default='v3-');args=parser.parse_args()
for case in sorted(OUT.glob(args.prefix+'*')):
    if not (case/'passed.json').exists():continue
    target=DEST/(case.name+'.tar.gz')
    with tarfile.open(target,'w:gz',compresslevel=6) as archive:
        for name in ['configuration.json','build.json','passed.json','gate.json','resources.jsonl','audit.log','reports','node-logs']:
            path=case/name
            if path.exists():archive.add(path,arcname=case.name+'/'+name)
        for path in case.glob('*-timing.json'):archive.add(path,arcname=case.name+'/'+path.name)
        for name in ['warm.json','warm-audit.json']:
            path=case/name
            if path.exists():archive.add(path,arcname=case.name+'/'+name)
    if target.stat().st_size>=95*1024**2:raise RuntimeError('evidence archive too large for repository')
    with tarfile.open(target,'r:gz') as archive:
        for member in archive:
            if member.isfile():
                with archive.extractfile(member) as f:
                    while f.read(1024**2):pass
    shutil.copyfile(case/'payments.jsonl.gz',DEST/(case.name+'-payments.jsonl.gz'))
for path in sorted(DEST.iterdir()):
    if path.is_file() and path.name!='manifest.json':
        manifest[path.name]={'bytes':path.stat().st_size,'sha256':hashlib.file_digest(path.open('rb'),'sha256').hexdigest()} if hasattr(hashlib,'file_digest') else {'bytes':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
(DEST/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Verified evidence archives:',len(manifest))
