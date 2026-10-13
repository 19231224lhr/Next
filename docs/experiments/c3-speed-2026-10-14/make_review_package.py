"""Package explicitly selected source and measured reports, excluding lab keys."""
import pathlib
import subprocess
import zipfile

repo = pathlib.Path(__file__).resolve().parents[3]
out = pathlib.Path(__file__).resolve().parent
sources = [
    'protocol/v3.go', 'protocol/v3_initial_test.go', 'protocol/direct_payment.go',
    'internal/rules/direct.go', 'internal/rules/direct_parent_verification_test.go',
    'internal/member/direct.go', 'internal/member/direct_approval_encoding_test.go',
    'internal/gateway/direct.go', 'internal/gateway/direct_summary_reuse_test.go',
    'internal/gateway/direct_relay.go', 'internal/gateway/direct_install_bytes_test.go',
    'internal/gateway/direct_outbox_cache.go', 'internal/gateway/direct_outbox_cache_test.go',
    'internal/transport/direct.go', 'internal/transport/direct_install_bytes_test.go',
    'cmd/gateway/direct.go', 'cmd/payctl/direct_chain.go', 'cmd/payctl/c3.go',
    'internal/rules/c3_policy.go', 'internal/wallet/direct.go',
]
data = pathlib.Path(r'\\wsl.localhost\Ubuntu\home\qyinf\next-c3-speed-20261014')
with zipfile.ZipFile(out / 'gpt-stage5-review.zip', 'w', zipfile.ZIP_DEFLATED) as archive:
    for name in sources:
        archive.write(repo / name, 'source/' + name)
    archive.write(out / 'DECISIONS.md', 'DECISIONS.md')
    diff = subprocess.run(['git', 'diff', '--', *sources], cwd=repo, capture_output=True, check=True).stdout
    archive.writestr('changes.patch', diff)
    for run in ['stage3-compact-paired-chain', 'stage4-paired-mixed', 'stage4-sameorg-paired-chain']:
        for name in ['summary.json', 'runs.csv', 'plan.json']:
            archive.write(data / run / name, 'results/' + run + '/' + name)
    for name in ['gateway0-cpu-cum.txt', 'gateway0-sched.txt', 'org0-member0-cpu-cum.txt']:
        archive.write(data / 'services-stage4-trace' / name, 'diagnostics/' + name)
print(out / 'gpt-stage5-review.zip')
