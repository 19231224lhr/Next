"""Export independent final run medians; do not pool trial groups."""
import csv
import json
from pathlib import Path

root = Path(__file__).resolve().parent
series = [
    ('final-build-chain100-9triples', '0-3', 100),
    ('final-build-chain1000-7triples', '0-3', 1000),
    ('final-eightcpu-chain1000-7triples', '0-7', 1000),
]
with (root / 'final-results.csv').open('w', newline='', encoding='utf-8') as stream:
    writer = csv.writer(stream)
    writer.writerow(['dataset', 'cpu_affinity', 'payments', 'repeats_per_arm',
        'frozen_c3_ms', 'next_c3_ms', 'fastpay_ms', 'time_reduction_pct',
        'next_min_ms', 'next_max_ms', 'fastpay_min_ms', 'fastpay_max_ms', 'all_complete'])
    for name, cpus, count in series:
        data = json.loads((root / 'raw-results' / name / 'summary.json').read_text())
        arms = data['arms']
        nxt = arms['after']['chain_total_ms']
        fp = arms['fastpay']['chain_total_ms']
        writer.writerow([name, cpus, count, nxt['n'], arms['before']['chain_total_ms']['median'],
            nxt['median'], fp['median'], 100 * (1 - nxt['median'] / fp['median']),
            nxt['min'], nxt['max'], fp['min'], fp['max'], data['all_complete']])
print(root / 'final-results.csv')
