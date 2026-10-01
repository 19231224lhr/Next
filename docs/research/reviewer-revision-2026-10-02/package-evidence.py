from pathlib import Path
import tarfile
R=Path('/Users/richz/lab/man/utxo-review-20261002')
with tarfile.open(R/'review-evidence-small.tgz','w:gz') as t:
 for pattern in ['review-summary.json','review-chain-memory-results/*/chain-v4.json','review-chain-memory-results/*/audit.json','review-owner-recovery-results/*/e3-summary.json','review-owner-recovery-results/*/recovery-config.json','review-owner-recovery-results/*/reports/audit.json','review-storage-tps-results/*/configuration.json','review-storage-tps-results/*/bench-v4-0.json','review-paired-results/*/cal-distribution.json']:
  for p in R.glob(pattern):t.add(p,arcname=str(p.relative_to(R)))
