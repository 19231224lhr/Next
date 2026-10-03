"""Check actual frozen artifacts, with explicit manifest scope."""
from pathlib import Path
import json
import hashlib
import subprocess

out = Path(__file__).resolve().parent
root = out.parents[3]
e = root / 'docs/experiments/final-evidence-2026-10-04'
load = lambda p: json.loads(p.read_text(encoding='utf-8'))
manifest = load(e/'source-manifest.json')['files']
chain = load(e/'results/build.json')
mixed = load(e/'results-mixed/build.json')
recovery = load(e/'results-recovery/build.json')
assert chain['source'] == mixed['base']['source'] == recovery['base']['source']
assert chain['binaries'] == mixed['actual_binary_hashes'] == recovery['actual_binary_hashes']
assert all(manifest[k] == v for k,v in chain['source'].items())
assert recovery['client_sha256'] == mixed['client_sha256'] == chain['binaries']['bin-final/payctl']
assert chain['comet_go'] == mixed['base']['comet_go'] == recovery['base']['comet_go']
blob = subprocess.check_output(['git','show','f856c7b:third_party/cometbft/overlay.py'],cwd=root)
result = {'base':'f856c7b','manifest_file_count':len(manifest), 'build_source_file_count':len(chain['source']),
    'manifest_additional_files':sorted(set(manifest)-set(chain['source'])),
    'identical_chain_P_R_source_binary_and_expanded_Comet':True,
    'binaries':chain['binaries'],'client':recovery['client_sha256'],
    'member_entry':'bin-final/member is a shell wrapper: exec env GOMAXPROCS=16 GOGC=200 bin-final/member-real; see run_chain.py.',
    'overlay_git_blob_sha256':hashlib.sha256(blob).hexdigest(),
    'scope':'309 manifest entries = 305 build source entries + two runner scripts + go.mod/go.sum. Counts include tests; not 309 production files.'}
(out/'build-identity-check.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8')
paths=['internal/rules/direct_deadline.go','internal/committee/repair.go','cmd/payctl/direct_dispatch.go','cmd/payctl/direct_pace.go',
       'docs/experiments/final-evidence-2026-10-04/results-mixed/dispatch-analysis.json',
       'docs/paper/review-2026-10-02/fourth-revision-2026-10-04/version-bridge.json',
       'docs/paper/review-2026-10-02/fourth-revision-2026-10-04/build-identity-check.json']
packet=[]
for p in paths: packet.append('\n=== '+p+' ===\n'+(root/p).read_text(encoding='utf-8'))
(out/'independent-review/final-small-checks.txt').write_text('\n'.join(packet),encoding='utf-8')
print(json.dumps(result,indent=2))
