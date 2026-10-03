"""Package explicit public project source for reviewer follow-up, never keys/configs."""
from pathlib import Path
import hashlib
import json

root = Path(__file__).resolve().parents[4]
out = Path(__file__).resolve().parent / 'independent-review'
groups = {
    'implementation-payment.txt': [
        'internal/member/direct.go', 'internal/member/blocks.go',
        'internal/member/partial_reclaim.go', 'internal/member/source_recovery.go',
        'internal/rules/direct.go', 'internal/rules/source_recovery.go',
        'internal/committee/engine.go', 'internal/committee/app.go',
        'internal/store/bolt.go', 'internal/store/group.go',
    ],
    'implementation-representation.txt': [
        'internal/redaction/repair.go', 'internal/redaction/batch.go',
        'internal/redaction/decision.go',
        'internal/redaction/history_export_test.go',
    ],
    'implementation-comet-overlay.txt': [
        'third_party/cometbft/README.md', 'third_party/cometbft/overlay.py',
        'third_party/cometbft/consensus/block_identity.go',
        'third_party/cometbft/consensus/block_identity_test.go',
        'third_party/cometbft/types/redaction.go',
        'third_party/cometbft/store/redaction.go',
        'third_party/cometbft/store/catchup_test.go',
    ],
    'implementation-boundary-tests.txt': [
        'internal/member/approval_boundary_test.go',
        'internal/member/source_recovery_test.go',
        'internal/committee/submission_boundary_test.go',
        'internal/redaction/revision_boundaries_test.go',
        'docs/research/proof-code-map-wire4.md',
    ],
}
manifest = {}
for name, paths in groups.items():
    parts = ['Project source snapshot; production baseline f856c7b. '
             'Files are complete unless explicitly stated. This package does not '
             'include generated keys, local runtime configuration, or credentials.\n']
    for rel in paths:
        data = (root / rel).read_bytes()
        manifest[rel] = hashlib.sha256(data).hexdigest()
        parts.append('\n\n===== FILE: '+rel+' =====\n'+data.decode('utf-8'))
    (out / name).write_text(''.join(parts), encoding='utf-8')
    print(name, (out / name).stat().st_size)
(out / 'code-packet-sha256.json').write_text(json.dumps(manifest, indent=2)+'\n')
