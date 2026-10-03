"""Recompute the narrow current-build autonomous source-recovery check."""
from pathlib import Path
import csv
import json

root = Path(__file__).resolve().parent
run = root / 'results-recovery' / 'r1-R'
def read(name):
    return json.loads((run / name).read_text(encoding='utf-8'))

events = read('events.json')
statuses = read('final-status.json')
resources = read('final-resources.json')
audit = read('audit.json')
summary = read('bench-v4-100.json')['Summary']
rows = []
for pair, history in sorted(events.items()):
    stages = {event['stage']: event for event in history}
    assert set(stages) == {'parent_ready', 'child_ready', 'child_public_observed',
                           'source_fulfilled_observed'}
    parent, child = stages['parent_ready'], stages['child_ready']
    origin = child['sent_unix_ns']
    row = {'pair': int(pair),
           'parent_receipt_ms': (parent['unix_ns']-parent['sent_unix_ns'])/1e6,
           'child_receipt_ms': (child['unix_ns']-origin)/1e6,
           'child_public_observed_ms': (stages['child_public_observed']['unix_ns']-origin)/1e6,
           'source_fulfilled_observed_ms': (stages['source_fulfilled_observed']['unix_ns']-origin)/1e6}
    direct = read(f'direct-{pair}.json')
    assert direct['parent_withheld'] and direct['compensation_avoided']
    assert abs(direct['autonomous_source_observed_ms']-row['source_fulfilled_observed_ms']) < .02
    assert len(statuses[pair]) == 4
    assert all(replica['obligation']['value']['Status'] == 1 for replica in statuses[pair])
    rows.append(row)

committees = [node for node in audit if node['Name'].startswith('committee')]
assert len(committees) == 4 and len({node['StateHash'] for node in committees}) == 1
for node in committees:
    assert node['Payments'] == node['Closed'] == 2006 and node['Gap'] == '0'
    recovery = node['Recovery']
    assert recovery['Fulfilled'] == 3
    assert all(recovery[key] == 0 for key in ['Open','Repaired','Recovered','PaidCAL','RecoveredCAL'])
    assert int(node['Rewards'])+int(node['Burned']) == 188564
assert all(node['Pending'] == 0 for node in audit)
for name, snapshot in resources.items():
    if 'member' not in name:
        continue
    for resource in snapshot['value']['Resources']:
        if resource['Key']['Kind'] in (1,3,4):
            assert sum(s['Reserved'] for s in resource['Slices']) == 0
assert summary['failed'] == summary['not_sent'] == summary['unknown'] == 0
assert summary['member_completed'] == summary['wallet_block_observed'] == 2000
assert summary['final_unfinished'] == 0

out = root / 'results-recovery'
with (out/'targets.csv').open('w', newline='', encoding='utf-8') as stream:
    writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
    writer.writeheader()
    writer.writerows(rows)
result = {'scope': 'One functional R run; not a recovery latency distribution or throughput comparison.',
          'total_payments': 2006, 'ordinary_payments': 2000, 'source_child_pairs': 3,
          'committee_agreement': True, 'pending_empty': True,
          'member_cal_execution_bytes_reserved_zero': True,
          'fuel_charged': 188564, 'fuel_rewards': 168504, 'fuel_burned': 20060,
          'targets': rows, 'ordinary_summary': summary,
          'timing': 'Client observed timestamps, not internal first availability or member recovery start.',
          'fault': 'Source collector withholds delivery; it stays alive to observe. No process-crash claim.'}
(out/'analysis.json').write_text(json.dumps(result, indent=2)+'\n', encoding='utf-8')
print(json.dumps({k:v for k,v in result.items() if k!='ordinary_summary'}, indent=2))
