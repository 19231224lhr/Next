"""Read retained traces; do not rerun or change the payment system."""
import json
from pathlib import Path
from statistics import median

HERE = Path(__file__).resolve().parent
SOURCE = HERE.parent / 'committee-verification-cache-2026-09-19'

def distribution(values):
    values = sorted(values)
    if not values:
        return {'n': 0}
    return {'n': len(values), 'p50_ms': median(values),
            'p95_ms': values[min(len(values)-1, int(len(values)*.95))],
            'total_ms': sum(values), 'max_ms': max(values)}

def analyze(label):
    folder = SOURCE / label / 'reports/trace100'
    bench = json.loads((folder/'benchmark.json').read_text())
    output = {'followers': {}, 'approval': {}, 'stores': {}}
    for file in sorted(folder.glob('*-timeline.json')):
        events = json.loads(file.read_text())
        assert len(events) < 4096, file
        by_height = {}
        for event in events:
            if event['Stage'].startswith('follow_') and 'height' in event['Fields']:
                by_height.setdefault(event['Fields']['height'], {})[event['Stage']] = event['UnixNS']
        rows = {}
        for height, stages in by_height.items():
            pairs = {'queue_ms': ('follow_update_requested', 'follow_update_started'),
                     'apply_ms': ('follow_apply_start', 'follow_apply_done'),
                     'after_apply_ms': ('follow_apply_done', 'follow_committed')}
            if all(x in stages for pair in pairs.values() for x in pair):
                rows[height] = {name: (stages[end]-stages[start])/1e6
                               for name, (start, end) in pairs.items()}
        if rows:
            output['followers'][file.stem] = rows
        # Exclude genesis/bootstrap. No-change attempts are not physical commits.
        updates = [e for e in events if e['Stage'] == 'store_update' and
                   int(e['Fields']['requested_ns']) >= bench['Summary']['started_unix_ns']]
        if updates:
            committed = [e['Fields'] for e in updates if e['Fields']['no_changes'] == 'false' and e['Fields']['failed'] == 'false']
            output['stores'][file.stem] = {
                'attempts': len(updates), 'physical_commits': len(committed),
                'no_change': sum(e['Fields']['no_changes'] == 'true' for e in updates),
                'commit': distribution([(int(e['returned_ns'])-int(e['writes_ns']))/1e6 for e in committed]),
                'write_count_total': sum(int(e['write_count']) for e in committed),
                'page_bytes_total': sum(int(e['page_bytes']) for e in committed)}
    observations = {}
    for sample in bench['Samples']:
        by_node = {}
        for event in sample.get('Foreground', []):
            by_node.setdefault(event['node'], {})[event['stage']] = event['unix_ns']
        for node, stages in by_node.items():
            for name, (start, end) in {
                'queue': ('commit_requested', 'update_started'),
                'evaluate': ('update_started', 'update_evaluated'),
                'after_evaluate': ('update_evaluated', 'commit_returned')}.items():
                if start in stages and end in stages:
                    observations.setdefault(name, []).append((stages[end]-stages[start])/1e6)
    output['approval'] = {name: distribution(values) for name, values in observations.items()}
    return output

result = {label: analyze(label) for label in ['cache-trace-a', 'cache-trace-b', 'cache-store-profile']}
(HERE/'historical-analysis.json').write_text(json.dumps(result, indent=2)+'\n', encoding='utf-8')
for label, data in result.items():
    print(label, json.dumps({'approval': data['approval'], 'height3': {
        name: rows.get('3') for name, rows in data['followers'].items()}, 'stores': data['stores']}, indent=2))
