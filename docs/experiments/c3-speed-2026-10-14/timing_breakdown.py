"""Account for each measured chain without adding unrelated phase medians."""
import argparse
import csv
import json
import pathlib
import statistics

p = argparse.ArgumentParser()
p.add_argument('root', type=pathlib.Path)
a = p.parse_args()
plan = json.loads((a.root / 'plan.json').read_text())
rows = []
for job in plan['jobs']:
    run = a.root / f"r{job['repeat']}-{job['arm']}"
    if job['arm'] == 'fastpay':
        report = json.loads((run / 'native-client.json').read_text())
        sent = report['sent_offset_ms']
        ready = report['receipt_offset_ms']
        fast = report['samples_ms']
        builds = report['build_ms']
        chain = report['chain_total_ms']
    else:
        report = json.loads((run / 'reports/chain-v4.json').read_text())
        sent = [h['SentOffsetMS'] for h in report['Hops']]
        ready = [h['ReadyOffsetMS'] for h in report['Hops']]
        fast = [h['FastMS'] for h in report['Hops']]
        builds = [h['BuildMS'] for h in report['Hops']]
        chain = report['FastChainMS']
    assert len(sent) == len(ready) == len(fast) == len(builds)
    gaps = sum(sent[i] - ready[i-1] for i in range(1, len(sent)))
    residual = chain - sum(fast) - gaps
    assert abs(residual) < .01, (run, residual)
    # Construction of payment 1 precedes both chain timers and is excluded here.
    prep = sum(builds[1:])
    rows.append(dict(**job, payments=len(sent), chain_ms=chain,
                     receipts_sum_ms=sum(fast), inter_payment_gaps_ms=gaps,
                     next_request_build_ms=prep, other_gap_ms=gaps-prep,
                     accounting_residual_ms=residual,
                     top10_receipts_sum_ms=sum(sorted(fast, reverse=True)[:10])))
with (a.root / 'timing-breakdown.csv').open('w', newline='') as f:
    writer = csv.DictWriter(f, fieldnames=list(rows[0]))
    writer.writeheader()
    writer.writerows(rows)
stats = {}
for arm in dict.fromkeys(row['arm'] for row in rows):
    subset = [row for row in rows if row['arm'] == arm]
    stats[arm] = {key: statistics.median(row[key] for row in subset)
                  for key in rows[0] if key not in ('arm', 'repeat')}
output = dict(scope='Exact per-run timer accounting; summary columns are independent medians and need not sum.',
              arms=stats, rows=rows)
(a.root / 'timing-breakdown.json').write_text(json.dumps(output, indent=2) + '\n')
print(json.dumps(stats, indent=2))
