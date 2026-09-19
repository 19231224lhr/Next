import csv
import json
import statistics
from pathlib import Path

ROOT = Path(__file__).resolve().parent
events = json.loads((ROOT / 'gateway0-timeline.json').read_text())
bench = json.loads((ROOT / 'benchmark.json').read_text())
origin = bench['Summary']['started_unix_ns']

def stats(values):
    values = sorted(values)
    return {'n': len(values), 'median': statistics.median(values),
            'p95': values[int((len(values)-1)*.95)], 'max': max(values),
            'mean': statistics.mean(values), 'sum': sum(values)}

def write_csv(name, rows):
    with (ROOT / name).open('w', encoding='utf-8-sig', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0]), lineterminator='\n')
        writer.writeheader()
        writer.writerows(rows)

def first(stage, fact):
    return min(e['UnixNS'] for e in events if e['Stage'] == stage and e['Fields'].get('spend') == fact)

store = []
labels = {'persist_callback': 'new_payment', 'relay_callback': 'retry_status',
          'follow_update_started': 'block_follow'}
for event in events:
    if event['Stage'] != 'store_update':
        continue
    f = event['Fields']
    marks = [e for e in events if e['Stage'] in labels
             and int(f['callback_ns']) <= e['UnixNS'] <= int(f['evaluated_ns'])]
    assert len(marks) == 1, (f, marks)
    m = marks[0]
    row = {'kind': labels[m['Stage']], 'fact': m['Fields'].get('spend', ''),
           'height': m['Fields'].get('height', ''),
           'requested_ns': int(f['requested_ns']), 'locked_ns': int(f['locked_ns']),
           'returned_ns': int(f['returned_ns']), 'no_changes': f['no_changes'],
           'page_bytes': int(f['page_bytes']), 'write_count': int(f['write_count'])}
    for name, start, end in [('lock_wait_ms', 'requested_ns', 'locked_ns'),
                             ('begin_ms', 'locked_ns', 'callback_ns'),
                             ('business_ms', 'callback_ns', 'evaluated_ns'),
                             ('mutations_ms', 'evaluated_ns', 'writes_ns'),
                             ('commit_ms', 'writes_ns', 'returned_ns')]:
        row[name] = (int(f[end])-int(f[start]))/1e6
    row['bbolt_write_sync_ms'] = int(f['write_ns'])/1e6
    row['bbolt_spill_ms'] = int(f['spill_ns'])/1e6
    row['bbolt_rebalance_ms'] = int(f['rebalance_ns'])/1e6
    row['total_ms'] = (row['returned_ns']-row['requested_ns'])/1e6
    assert f['failed'] == 'false'
    store.append(row)

groups = []
for start in (e for e in events if e['Stage'] == 'relay_group_start'):
    end = next(e for e in events if e['Stage'] == 'relay_group_done'
               and e['Fields']['scan_ns'] == start['Fields']['scan_ns']
               and e['Fields']['offset'] == start['Fields']['offset'])
    begin_ns, end_ns = start['UnixNS'], end['UnixNS']
    entered = [e for e in events if e['Stage'] == 'relay_enter' and begin_ns <= e['UnixNS'] <= end_ns]
    ready = max(e['UnixNS'] for e in events if e['Stage'] == 'relay_ready' and begin_ns <= e['UnixNS'] <= end_ns)
    submits = [e['UnixNS'] for e in events if e['Stage'] == 'submit_done' and begin_ns <= e['UnixNS'] <= end_ns]
    groups.append({'start_ns': begin_ns, 'end_ns': end_ns,
                   'start_offset_ms': (begin_ns-origin)/1e6, 'count': len(entered),
                   'duration_ms': (end_ns-begin_ns)/1e6,
                   'all_install_done_ms': (ready-begin_ns)/1e6,
                   'tail_after_submit_ms': (end_ns-max(submits))/1e6 if submits else '',
                   'facts': ';'.join(e['Fields']['spend'] for e in entered)})

def overlap(start, end, intervals):
    return sum(max(0, min(end, b)-max(start, a)) for a, b in intervals)/1e6

active = [(g['start_ns'], g['end_ns']) for g in groups]
scans = [(int(e['Fields']['start_ns']), e['UnixNS']) for e in events if e['Stage'] == 'relay_scan']
payments = []
for sample in bench['Samples']:
    fact = sample['Fact']
    save_start, save_end = first('outbox_persist_start', fact), first('outbox_persist_done', fact)
    picked = first('relay_enter', fact)
    row = next(dict(s) for s in store if s['kind'] == 'new_payment' and s['fact'] == fact)
    row.update(index=sample['Index'], save_start_ns=save_start, save_end_ns=save_end,
               picked_ns=picked, save_ms=(save_end-save_start)/1e6,
               pre_store_ms=(row['requested_ns']-save_start)/1e6,
               queue_ms=(picked-save_end)/1e6,
               queue_during_groups_ms=overlap(save_end,picked,active),
               queue_during_scans_ms=overlap(save_end,picked,scans))
    row['queue_other_ms'] = row['queue_ms'] - row['queue_during_groups_ms'] - row['queue_during_scans_ms']
    payments.append(row)

summary = {'benchmark': bench['Summary'], 'store_by_kind': {}, 'payments': {}, 'groups': {}}
for kind in labels.values():
    selected = [s for s in store if s['kind'] == kind]
    summary['store_by_kind'][kind] = {'count': len(selected), 'changed': sum(s['no_changes']=='false' for s in selected)}
    for field in ['lock_wait_ms', 'business_ms', 'mutations_ms', 'commit_ms', 'bbolt_write_sync_ms', 'bbolt_spill_ms', 'total_ms']:
        summary['store_by_kind'][kind][field] = stats([s[field] for s in selected])
for field in ['save_ms','pre_store_ms','queue_ms','queue_during_groups_ms','queue_during_scans_ms','queue_other_ms']:
    summary['payments'][field] = stats([s[field] for s in payments])
for field in ['duration_ms','all_install_done_ms','tail_after_submit_ms']:
    summary['groups'][field] = stats([g[field] for g in groups if g[field] != ''])
summary['scan_ms'] = stats([(b-a)/1e6 for a,b in scans if a>=origin])
summary['coverage'] = {'events':len(events),'store':len(store),'payments':len(payments),'groups':len(groups)}
write_csv('store-updates.csv',store)
write_csv('payment-waits.csv',payments)
write_csv('relay-groups.csv',groups)
(ROOT/'summary.json').write_text(json.dumps(summary,indent=2),encoding='utf-8',newline='\n')
print(json.dumps(summary,indent=2))
print('SLOWEST GROUPS',sorted(groups,key=lambda g:g['duration_ms'],reverse=True)[:3])
