"""Summarize per-run percentiles; do not pool different rounds as independent runs."""
import json
import pathlib
import re
import statistics
import sys

root = pathlib.Path(__file__).resolve().parent
source = root / (sys.argv[1] if len(sys.argv) > 1 else 'recipient-final-raw.txt')
raw = source.read_text(encoding='utf-8-sig')
pattern = r'round=(\d+) extra_gateway_receiver_delay_ms=(\d+) recipient_collector=(true|false) n=(\d+) p50_ms=([\d.]+) p95_ms=([\d.]+)'
records = [dict(round=int(r), delay_ms=int(d), receiver=(m == 'true'), n=int(n), p50_ms=float(p), p95_ms=float(q))
           for r, d, m, n, p, q in re.findall(pattern, raw)]
assert len(records) == 12 and 'PASS' in raw
summary = {'source': source.name, 'records': records, 'comparisons': []}
for delay in (0, 20):
    arms = {}
    for receiver in (False, True):
        runs = [x for x in records if x['delay_ms'] == delay and x['receiver'] == receiver]
        arms['receiver' if receiver else 'gateway'] = {
            k: statistics.median(x[k] for x in runs) for k in ('p50_ms', 'p95_ms')}
    summary['comparisons'].append(dict(delay_ms=delay, **arms,
        p50_reduction_percent=100*(1-arms['receiver']['p50_ms']/arms['gateway']['p50_ms'])))
target = source.with_suffix('.summary.json')
target.write_text(json.dumps(summary, indent=2), encoding='utf-8')
print(json.dumps(summary['comparisons'], indent=2))
