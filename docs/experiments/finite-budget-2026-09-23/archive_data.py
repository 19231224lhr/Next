#!/usr/bin/env python3
"""Lossless compression and checksums for the stopped E2 evidence directory."""
import argparse
import gzip
import hashlib
import json
import shutil
from pathlib import Path

OUT=Path(__file__).resolve().parent


def digest(path, compressed=False):
    h=hashlib.sha256()
    opener=gzip.open if compressed else open
    with opener(path,'rb') as f:
        for chunk in iter(lambda:f.read(1024*1024),b''):h.update(chunk)
    return h.hexdigest()


def main(compress):
    if compress:
        for p in sorted(OUT.rglob('*')):
            if not p.is_file() or p.suffix not in ['.json','.jsonl','.csv','.log'] or p.stat().st_size<256*1024:continue
            target=p.with_name(p.name+'.gz')
            with p.open('rb') as src, target.open('wb') as dst:
                with gzip.GzipFile(filename='',mode='wb',fileobj=dst,mtime=0) as out:shutil.copyfileobj(src,out)
            if digest(p)!=digest(target,True):raise RuntimeError('Compression mismatch: '+str(p))
            p.unlink()
    records=[]
    for p in sorted(OUT.rglob('*')):
        if not p.is_file() or '__pycache__' in p.parts or p.name=='data-manifest.json':continue
        record={'path':p.relative_to(OUT).as_posix(),'bytes':p.stat().st_size,'sha256':digest(p)}
        if p.name.endswith(('.json.gz','.jsonl.gz','.csv.gz','.log.gz')):record['uncompressed_sha256']=digest(p,True)
        records.append(record)
    (OUT/'data-manifest.json').write_text(json.dumps(records,indent=2)+'\n')
    print(len(records),'files;',round(sum(r['bytes'] for r in records)/1024**2,2),'MiB')


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--compress',action='store_true');args=p.parse_args()
    main(args.compress)
