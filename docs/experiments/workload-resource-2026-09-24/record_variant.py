#!/usr/bin/env python3
"""Record actual per-binary provenance when only the workload driver changes."""
import hashlib,json,subprocess
from pathlib import Path
OUT=Path(__file__).resolve().parent;ROOT=OUT.parents[2];BIN=ROOT/'.run/e8-bin'
build=json.loads((OUT/'build.json').read_text())
old=OUT/'build-initial.json'
if not old.exists():old.write_text(json.dumps(build,indent=2)+'\n')
build['source_commit']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
build['binaries']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()}
build['compiler_vcs']={}
for p in BIN.iterdir():
    meta=subprocess.check_output(['/usr/local/go/bin/go','version','-m',p],text=True)
    build['compiler_vcs'][p.name]=[s.strip() for s in meta.splitlines() if 'vcs.' in s]
build['note']='Only payctl was rebuilt for balanced C root ownership; A/B payment rules and all node binaries are unchanged. Commit field is the original build base; source_commit identifies the corrected implementation.'
(OUT/'build.json').write_text(json.dumps(build,indent=2)+'\n')
(OUT/'build-balanced.json').write_text(json.dumps(build,indent=2)+'\n')
print(json.dumps(build['binaries']))
