"""Package selected project evidence for the user's sidebar review; no lab keys."""
import argparse
import hashlib
import json
import pathlib
import subprocess
import zipfile

p = argparse.ArgumentParser()
p.add_argument('--name', default='gpt-stage10-review.zip')
a = p.parse_args()
here = pathlib.Path(__file__).resolve().parent
repo = here.parents[2]
lab = pathlib.Path(r'\\wsl.localhost\Ubuntu\home\qyinf\next-c3-speed-20261014')
sources = [
    'protocol/v3.go', 'protocol/v3_initial_test.go', 'protocol/direct_payment.go',
    'internal/rules/direct.go', 'internal/rules/direct_parent_verification_test.go',
    'internal/member/direct.go', 'internal/member/direct_approval_encoding_test.go',
    'internal/member/blocks.go', 'internal/member/c3.go', 'internal/member/blocks_approval_reuse_test.go',
    'internal/gateway/direct.go', 'internal/gateway/direct_summary_reuse_test.go',
    'internal/gateway/direct_relay.go', 'internal/gateway/direct_relay_test.go',
    'internal/gateway/direct_install_bytes_test.go', 'internal/gateway/direct_scan_completion_test.go',
    'internal/gateway/direct_outbox_cache.go', 'internal/gateway/direct_outbox_cache_test.go',
    'internal/transport/direct.go', 'internal/transport/direct_install_bytes_test.go',
    'cmd/gateway/direct.go', 'cmd/payctl/direct_chain.go', 'cmd/payctl/direct_chain_length_test.go',
    'cmd/payctl/c3.go', 'internal/rules/c3_policy.go', 'internal/wallet/direct.go',
]
files = [(repo / name, 'source/' + name) for name in sources]
for name in ['DECISIONS.md', 'REPRODUCE.md', 'stage9b-approval-micro.md',
             'stage9b-background-check.json', 'stage10-gateway-focused-race.log',
             'stage10-gateway-transport.log']:
    files.append((here / name, name))
for series in ['stage6-tuned-fastpay-1009triples', 'stage8-long-confirm7triples',
               'stage9b-long-confirm7triples', 'stage10-long-pilot3triples']:
    for name in ['summary.json', 'runs.csv', 'plan.json']:
        path = lab / series / name
        if path.exists():
            files.append((path, 'results/' + series + '/' + name))
manifest = {'base_head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip(),
            'scope': 'Selected current source and recorded evidence; source is uncommitted. No private lab keys, configs, state or databases.',
            'files': []}
destination = here / a.name
with zipfile.ZipFile(destination, 'x', zipfile.ZIP_DEFLATED) as z:
    for path, arcname in files:
        data = path.read_bytes()
        z.writestr(arcname, data)
        manifest['files'].append({'path': arcname, 'sha256': hashlib.sha256(data).hexdigest(), 'bytes':len(data)})
    diff = subprocess.run(['git', 'diff', '--', *sources], cwd=repo, capture_output=True, check=True).stdout
    z.writestr('tracked-changes.patch', diff)
    z.writestr('manifest.json', json.dumps(manifest, indent=2))
print(json.dumps({'path': str(destination), 'files':len(files), 'bytes':destination.stat().st_size}))
