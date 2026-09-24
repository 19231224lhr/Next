#!/usr/bin/env python3
"""Record committed experiment source alongside unchanged trial binaries."""
import hashlib,json,subprocess
from pathlib import Path
OUT=Path(__file__).resolve().parent
ROOT=OUT.parents[2]
def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT)
commit=git('rev-parse','HEAD').decode().strip()
paths=git('diff-tree','--no-commit-id','--name-only','-r',commit).decode().splitlines()
hashes={}
for path in paths:
    disk=(ROOT/path).read_bytes().replace(b'\r\n',b'\n')
    recorded=git('show',f'{commit}:{path}')
    assert disk==recorded,path
    hashes[path]=hashlib.sha256(disk).hexdigest()
build=json.loads((OUT/'build.json').read_text())
out={'source_commit':commit,'build':build,'source_sha256_lf':hashes,
     'note':'Initial binaries were built from the uncommitted E8 patch on the recorded base commit; the same source was subsequently gofmt-normalized and committed. Formal A1/B1 use this initial variant; later cases identify the balanced-driver variant in their own build manifests.'}
(OUT/'source-manifest.json').write_text(json.dumps(out,indent=2)+'\n')
print(commit)
