"""Run matched old/new pool executables serially, with fresh synchronous stores."""
import argparse
import json
import pathlib
import platform
import statistics
import subprocess
import time

p = argparse.ArgumentParser()
p.add_argument('--rounds', type=int, default=5)
p.add_argument('--transfers', type=int, default=200)
p.add_argument('--ablation', action='store_true', help='also run fastpath-only and final serial completion')
args = p.parse_args()
out = pathlib.Path(__file__).resolve().parent
repo = out.parents[2]
stamp = time.strftime('%Y%m%d-%H%M%S')
run = out / ('comparison-' + stamp)
run.mkdir()
records = []
modes = ('before', 'fastpath', 'serial', 'after') if args.ablation else ('before', 'after')
for i in range(args.rounds):
    for mode in (modes if i % 2 == 0 else tuple(reversed(modes))):
        store = repo / '.scratch' / ('pool-speed-' + stamp) / f'{i}-{mode}'
        binary = 'after' if mode == 'serial' else mode
        cmd = [str(out / f'pool-lab-{binary}.exe'), '-transfers', str(args.transfers), '-store-dir', str(store)]
        if mode == 'serial':
            cmd.append('-serial-completion')
        result = subprocess.run(cmd, cwd=repo, text=True, capture_output=True, check=True)
        data = json.loads(result.stdout)
        (run / f'{i}-{mode}.json').write_text(result.stdout, encoding='utf-8')
        assert data['subsequent_ordinary_payment_approved']
        records.append(dict(round=i, mode=mode, p50_ms=data['p50_ms'], p95_ms=data['p95_ms']))
        print(records[-1], flush=True)
summary = {'platform': platform.platform(), 'transfers_per_run': args.transfers, 'rounds': args.rounds, 'records': records}
for mode in modes:
    summary[mode] = {key: statistics.median(r[key] for r in records if r['mode'] == mode) for key in ('p50_ms', 'p95_ms')}
summary['p50_reduction_percent'] = 100 * (1 - summary['after']['p50_ms'] / summary['before']['p50_ms'])
summary['p95_reduction_percent'] = 100 * (1 - summary['after']['p95_ms'] / summary['before']['p95_ms'])
(run / 'summary.json').write_text(json.dumps(summary, indent=2), encoding='utf-8')
print(json.dumps(summary, indent=2))
