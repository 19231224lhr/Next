"""Record actual version differences, without relabeling old measurements."""
from pathlib import Path
import hashlib
import json
import subprocess

root = Path(__file__).resolve().parents[4]
out = Path(__file__).resolve().parent
def git(*args):
    return subprocess.check_output(['git', *args], cwd=root)

versions = ['721800c', '9879f64', 'f856c7b']
areas = ['internal/member', 'internal/committee', 'internal/rules', 'internal/redaction',
         'internal/transport', 'internal/wallet', 'third_party/cometbft', 'protocol', 'finality',
         'cmd/committee', 'cmd/gateway', 'cmd/member', 'cmd/internal', 'go.mod', 'go.sum']
records = {}
for left, right in zip(versions, versions[1:]):
    changed = git('diff','--name-only', left, right, '--', *areas).decode().splitlines()
    records[left+'..'+right] = {
        'checked_paths': areas,
        'changed_production': [f for f in changed if f.endswith('.go') and not f.endswith('_test.go')],
        'changed_tests_and_support': [f for f in changed if not (f.endswith('.go') and not f.endswith('_test.go'))],
        'client_changes': git('diff','--name-only',left,right,'--','cmd/payctl').decode().splitlines(),
        'entrypoints_and_dependencies': git('diff','--name-status',left,right,'--',
            'cmd/committee','cmd/gateway','cmd/member','cmd/internal','go.mod','go.sum').decode().splitlines(),
    }
paths = ['internal/redaction/repair.go', 'internal/redaction/batch.go',
         'internal/redaction/decision.go']
paths += git('ls-tree','-r','--name-only','f856c7b','--','third_party/cometbft').decode().splitlines()
hashes = {p:{v:hashlib.sha256(git('show',v+':'+p)).hexdigest() for v in versions} for p in paths}
assert all(len(set(v.values()))==1 for v in hashes.values())
assert not records['9879f64..f856c7b']['changed_production']
(out/'version-bridge.json').write_text(json.dumps({'differences': records,
    'identical_representation_and_overlay_files': hashes}, indent=2)+'\n', encoding='utf-8')
print(json.dumps(records, indent=2))
