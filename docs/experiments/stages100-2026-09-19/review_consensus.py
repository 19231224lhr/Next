"""Read-only reanalysis of the existing run; does not run another benchmark.

Intervals include scheduling, I/O and communication: they are not fsync-only
measurements. The overlap is descriptive, not a causal attribution.
"""
import csv
import json
import statistics
from pathlib import Path

p = Path(__file__).resolve().parent
summary = json.loads((p / 'stage-summary.json').read_text())
settlement = json.loads((p / 'reports/trace100/settlement.json').read_text())
timelines = {n: json.loads((p / 'reports/trace100' / f'{n}-timeline.json').read_text()) for n in settlement}


def at(node, height, stage, vote=''):
    found = [e['UnixNS'] for e in timelines[node] if e['Stage'] == stage
             and e['Fields'].get('height') == str(height) and vote in e['Fields'].get('vote', '')]
    assert len(found) == 1, (node, height, stage, len(found))
    return found[0]


blocks = []
for block in summary['blocks']:
    h, proposer = block['height'], block['proposer']
    stamps = [at(proposer, h, 'prepare_done'), at(proposer, h, 'signed proposal'),
              at('committee0', h, 'received complete proposal block'), at('committee0', h, 'process_enter'),
              at('committee0', h, 'process_done'), at('committee0', h, 'signed and pushed vote', 'TYPE_PREVOTE'),
              at('committee0', h, 'entering precommit step'),
              at('committee0', h, 'signed and pushed vote', 'TYPE_PRECOMMIT'),
              at('committee0', h, 'entering commit step'), at('committee0', h, 'finalize_enter')]
    names = ['prepare_to_sign_proposal', 'signed_to_received_proposal', 'received_to_process', 'process',
             'process_to_signed_prevote', 'signed_prevote_to_precommit', 'precommit_to_signed_precommit',
             'signed_precommit_to_commit', 'commit_step_to_finalize']
    row = {'height': h, 'payments': block['payments'], 'proposer': proposer}
    row.update({name+'_ms': (end-start)/1e6 for name, start, end in zip(names, stamps, stamps[1:])})
    assert all(row[name+'_ms'] >= 0 for name in names)
    assert abs(sum(row[name+'_ms'] for name in names)-block['prepare_to_finalize_ms']) < .000001
    row['new_height_timer'] = [e['Fields']['timeout'] for e in timelines['committee0']
                               if e['Stage'] == 'received tock' and e['Fields'].get('height') == str(h)]
    blocks.append(row)
(p / 'consensus-review-breakdown.json').write_text(json.dumps(blocks, indent=2)+'\n')

rows = []
with (p / 'transactions.csv').open(encoding='utf-8-sig') as source:
    for tx in csv.DictReader(source):
        rec = next(r for r in settlement['committee0'][tx['fact']] if r['CommittedUnixNS'])
        h, accepted = rec['Height'], rec['AcceptedUnixNS']
        previous = at('committee0', h-1, 'commit_done')
        prepare = at(tx['proposer'], h, 'prepare_enter')
        rows.append({'fact': tx['fact'], 'height': h, 'accepted_before_previous_commit': accepted < previous,
                     'acceptance_to_prepare_ms': (prepare-accepted)/1e6,
                     'overlap_with_previous_uncommitted_ms': max(0, min(previous, prepare)-accepted)/1e6})
assert len(rows) == 100
(p / 'proposal-wait-overlap.json').write_text(json.dumps(rows, indent=2)+'\n')
result = {'blocks': len(blocks), 'block_medians': {k: statistics.median(r[k] for r in blocks)
                                                for k in blocks[0] if k.endswith('_ms')},
          'accepted_before_previous_commit': sum(r['accepted_before_previous_commit'] for r in rows),
          'acceptance_wait_total_ms': sum(r['acceptance_to_prepare_ms'] for r in rows),
          'overlap_with_previous_uncommitted_total_ms': sum(r['overlap_with_previous_uncommitted_ms'] for r in rows),
          'rounds': {n: sorted({e['Fields']['round'] for e in es if e['Stage'] == 'entering new round'})
                     for n, es in timelines.items()}}
print(json.dumps(result, indent=2))
