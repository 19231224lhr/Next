"""Join wallet-local follow markers to committee-0 commits by height and fact.

This measures successful retrieval, not the earliest availability of evidence.
Percentiles are computed per run; the summary reports medians of run quantiles.
"""
import csv
import json
from pathlib import Path
from statistics import median
import sys


def quantiles(values):
    values = sorted(values)
    return {'n': len(values), 'p50': median(values),
            'p95': values[(95 * len(values) + 99) // 100 - 1],
            'min': values[0], 'max': values[-1]}


def main(root):
    rows, runs = [], []
    for folder in sorted(root.glob('r[123]-*')):
        report = json.loads((folder / 'chain-v4.json').read_text())
        followers = [{r['Height']: r for r in wallet}
                     for wallet in json.loads((folder / 'chain-follow-timing.json').read_text())]
        settlements = json.loads((folder / 'settlements.json').read_text())['0']
        current = []
        for i, hop in enumerate(report['Hops']):
            events = [e for e in settlements[bytes(hop['Fact']).hex()] if e.get('CommittedUnixNS', 0)]
            assert len({e['Height'] for e in events}) == 1
            event = min(events, key=lambda e: e['CommittedUnixNS'])
            f = followers[hop['Receiver']][event['Height']]
            assert f['FetchStartNS'] <= f['FetchDoneNS'] <= f['VerifiedNS'] <= f['CommittedNS']
            row = {'case': folder.name, 'mode': report['Mode'], 'hop': hop['Hop'],
                   'height': event['Height'], 'receiver': hop['Receiver'],
                   'fetch_attempts': f['Attempts']}
            boundaries = {
                'send_to_commit_ms': (hop['SentUnixNS'], event['CommittedUnixNS']),
                'commit_to_complete_fetch_ms': (event['CommittedUnixNS'], f['FetchDoneNS']),
                'successful_fetch_ms': (f['FetchStartNS'], f['FetchDoneNS']),
                'verify_ms': (f['FetchDoneNS'], f['VerifiedNS']),
                'prepare_and_record_ms': (f['VerifiedNS'], f['CommittedNS']),
                'record_to_driver_observe_ms': (f['CommittedNS'], hop['FinalUnixNS']),
                'commit_to_driver_observe_ms': (event['CommittedUnixNS'], hop['FinalUnixNS']),
            }
            if i + 1 < len(report['Hops']):
                nxt = report['Hops'][i + 1]
                boundaries['driver_observe_to_next_build_ms'] = (hop['FinalUnixNS'], nxt['BuildStartUnixNS'])
                boundaries['next_build_to_send_ms'] = (nxt['BuildStartUnixNS'], nxt['SentUnixNS'])
                boundaries['ready_to_next_send_ms'] = (hop['ReadyUnixNS'], nxt['SentUnixNS'])
            row.update({k: (end - start) / 1e6 for k, (start, end) in boundaries.items()})
            # The post-commit callback and driver's read can race. Preserve signed
            # differences rather than hiding callback scheduling behind a clamp.
            current.append(row)
        rows.extend(current)
        metrics = sorted(k for r in current for k in r if k.endswith('_ms'))
        runs.append({'case': folder.name, 'mode': report['Mode'],
                     'metrics': {k: quantiles([r[k] for r in current if k in r]) for k in sorted(set(metrics))}})
    fields = list(dict.fromkeys(k for row in rows for k in row))
    with (root / 'follow-timing.csv').open('w', newline='') as f:
        writer = csv.DictWriter(f, fields); writer.writeheader(); writer.writerows(rows)
    summary = {'endpoint_note': 'Complete successful BlockData retrieval includes next-height signed header, block and results. Commit-to-fetch includes evidence availability, polling, transport and replica lag.',
               'runs': runs, 'medians_of_run_quantiles': {}}
    for mode in ['fast', 'wait_final']:
        chosen = [r for r in runs if r['mode'] == mode]
        summary['medians_of_run_quantiles'][mode] = {
            k: {q: median(r['metrics'][k][q] for r in chosen) for q in ['p50', 'p95']}
            for k in chosen[0]['metrics']}
    (root / 'follow-timing-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    print(json.dumps(summary['medians_of_run_quantiles'], indent=2))


if __name__ == '__main__':
    main(Path(sys.argv[1]))
