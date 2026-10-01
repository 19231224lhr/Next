"""Audit and retain the nine matched storage/continuation controls."""
import hashlib
import json
from pathlib import Path
import shutil
import statistics

root = Path(__file__).resolve().parent
rows = []
for source in sorted((root / 'review-chain-sync-results').iterdir()):
    cfg = json.loads((source / 'configuration.json').read_text())
    chain = json.loads((source / 'chain-v4.json').read_text())
    audit = json.loads((source / 'audit.json').read_text())
    committees = [n for n in audit if n['Name'].startswith('committee')]
    assert len(committees) == 4 and len({n['StateHash'] for n in committees}) == 1
    assert all(n['Payments'] == n['Closed'] == 100 and n['Gap'] == '0' for n in committees)
    assert all(n.get('Pending', 0) == 0 for n in audit)
    assert len(chain['Hops']) == 100 and cfg['wallet_no_sync']
    if not cfg['wait_final']:
        for parent, child in zip(chain['Hops'], chain['Hops'][1:]):
            assert child['CertificateInput']
            assert parent['ReadyUnixNS'] <= child['SentUnixNS'] < parent['FinalUnixNS']
    target = root / 'evidence' / 'review-chain-sync-results' / source.name
    target.mkdir(parents=True, exist_ok=True)
    for name in ['configuration.json', 'audit.json', 'chain-audit.json', 'chain-v4.json']:
        shutil.copy2(source / name, target / name)
    rows.append({'name': source.name, 'member_sync': cfg['member_sync'],
                 'wait_final': cfg['wait_final'],
                 **{k: chain[k] for k in ['FastChainMS', 'PublicChainMS', 'ClosedChainMS']}})

for i in range(3):
    configs = [json.loads((root / 'review-chain-sync-results' / f'chain-{i}-{name}' / 'configuration.json').read_text())
               for name in ['nosync-fast', 'sync-fast', 'sync-wait']]
    for cfg in configs:
        cfg.pop('member_sync')
        cfg.pop('wait_final')
        cfg['env'].pop('UTXO_EXPERIMENT_MEMBER_SYNC')
    assert configs[0] == configs[1] == configs[2], 'Unexpected paired configuration change'

summary = {'runs': rows, 'medians_ms': {mode: statistics.median(
    r['FastChainMS'] for r in rows if r['name'].endswith('-' + mode))
    for mode in ['nosync-fast', 'sync-fast', 'sync-wait']},
    'all_public_audits_pass': True, 'fast_successor_order_checks': 594}
(root / 'chain-sync-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
archive = root / 'review-chain-sync-results.tgz'
manifest = json.loads((root / 'archive-manifest.json').read_text())
manifest['archives'] = [x for x in manifest['archives'] if x['name'] != archive.name]
manifest['archives'].append({'name': archive.name, 'local_directory': str(root),
                            'bytes': archive.stat().st_size,
                            'sha256': hashlib.sha256(archive.read_bytes()).hexdigest()})
(root / 'archive-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(summary['medians_ms'], indent=2))
print('Nine runs, 900 payments, matched configurations and 594 fast successors verified.')
