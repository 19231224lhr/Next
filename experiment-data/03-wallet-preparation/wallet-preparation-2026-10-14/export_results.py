"""Export native evidence without fixture keys or live wallet databases."""
import argparse
import csv
import gzip
import hashlib
import json
import pathlib
import shutil
import statistics

p = argparse.ArgumentParser()
p.add_argument('lab', type=pathlib.Path)
p.add_argument('--output', type=pathlib.Path, default=pathlib.Path(__file__).resolve().parent)
a = p.parse_args()
out = a.output
out.mkdir(parents=True, exist_ok=True)
summary = {}
for name in ('cache-pilot3pairs', 'cache-confirm7triples', 'final-confirm7triples'):
    source, dest = a.lab/name, out/'results'/name
    dest.mkdir(parents=True, exist_ok=True)
    for file in ('plan.json', 'status.jsonl', 'summary.json', 'runs.csv', 'timing-breakdown.json', 'timing-breakdown.csv'):
        shutil.copy2(source/file, dest/file)
    timing = json.loads((source/'timing-breakdown.json').read_text())
    plan = json.loads((source/'plan.json').read_text())
    hops = []
    for job in plan['jobs']:
        run_name = f"r{job['repeat']}-{job['arm']}"
        src, dst = source/run_name, dest/run_name
        dst.mkdir(exist_ok=True)
        for relative in ('result.json', 'native-client.json', 'manifest.json', 'reports/chain-v4.json'):
            if (src/relative).is_file():
                target = dst/relative
                target.parent.mkdir(parents=True, exist_ok=True)
                if relative == 'reports/chain-v4.json':
                    # Preserve exact native bytes without duplicating large
                    # pretty-printed certificate arrays in the checkout.
                    target.with_suffix('.json.gz').write_bytes(gzip.compress((src/relative).read_bytes(), mtime=0))
                else:
                    shutil.copy2(src/relative, target)
        if job['arm'] == 'fastpay':
            r = json.loads((src/'native-client.json').read_text())
            values = zip(r['build_ms'], r['samples_ms'], r['sent_offset_ms'], r['receipt_offset_ms'])
        else:
            r = json.loads((src/'reports/chain-v4.json').read_text())
            values = ((h['BuildMS'], h['FastMS'], h['SentOffsetMS'], h['ReadyOffsetMS']) for h in r['Hops'])
        for i, (build, receipt, sent, ready) in enumerate(values):
            hops.append(dict(**job, payment=i+1, build_ms=build, receipt_ms=receipt, sent_ms=sent, ready_ms=ready))
    with (dest/'payments.csv').open('w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=list(hops[0]))
        writer.writeheader()
        writer.writerows(hops)
    arms = {}
    for arm in timing['arms']:
        rows = [r for r in timing['rows'] if r['arm'] == arm]
        arms[arm] = {field: {'median': statistics.median(r[field] for r in rows),
                            'min': min(r[field] for r in rows), 'max': max(r[field] for r in rows)}
                     for field in ('next_request_build_ms', 'chain_ms', 'receipts_sum_ms')}
    pairs = []
    for repeat in range(int(plan['arguments']['repeats'])):
        pair = {r['arm']: r for r in timing['rows'] if r['repeat'] == repeat}
        pairs.append({'repeat': repeat, **{field: 100*(1-pair['after'][field]/pair['before'][field])
                                           for field in ('next_request_build_ms', 'chain_ms')}})
    summary[name] = {'arms': arms, 'paired_reduction_percent': pairs,
                     'all_complete': json.loads((source/'summary.json').read_text())['all_complete']}
(out/'comparison.json').write_text(json.dumps(summary, indent=2)+'\n')
hashes = {str(f.relative_to(out)): hashlib.sha256(f.read_bytes()).hexdigest()
          for f in sorted((out/'results').rglob('*')) if f.is_file()}
(out/'evidence-sha256.json').write_text(json.dumps(hashes, indent=2)+'\n')
print(json.dumps(summary, indent=2))
