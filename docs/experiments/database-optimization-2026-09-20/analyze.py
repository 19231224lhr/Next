"""Recheck balances, stage times and cumulative patch comparisons."""
import contextlib, io, json, runpy, statistics as st, sys
from pathlib import Path

root = Path(__file__).resolve().parent
analyzer = root.parent/'parallel-relay-2026-09-19/analyze.py'
result = {'runs': {}, 'groups': {}, 'traces': {}}
stages = ['base', 'index', 'prepare', 'credit', 'map']
def stats(values):
    xs = sorted(values)
    return {'n':len(xs), 'p50':st.median(xs), 'p95':xs[int((len(xs)-1)*.95)], 'mean':st.mean(xs), 'min':xs[0], 'max':xs[-1]} if xs else {}

for directory in sorted(root.glob('dbo-*')):
    reports = directory/'reports'
    b = json.loads((reports/'bench-v4-0.json').read_text())
    audit = json.loads((reports/'audit.json').read_text())
    assert b['Summary']['failed'] == 0 and len(b['Samples']) == 100
    assert len(audit) == 14 and all(n['Pending'] == 0 for n in audit)
    committees = [n for n in audit if n['Name'].startswith('committee')]
    assert len({n['StateHash'] for n in committees}) == 1
    assert all(n['Payments'] == n['Closed'] == 100 and n['Gap'] == '0' and n['CAL'] == '2000000025600' and n['FUEL'] == '2000000000000' and n['Rewards'] == '8400' and n['Burned'] == '1000' for n in committees)
    first = min(s['SentUnixNS'] for s in b['Samples'])
    last = max(s['SentUnixNS'] for s in b['Samples'])
    result['runs'][directory.name] = {'summary':b['Summary'], 'send_window_s':(last-first)/1e9,
        'last_send_to_finished_s':(b['Summary']['finished_unix_ns']-last)/1e9,
        'audit':'100 successes, 14 empty outboxes, 4 equal states, balances/fees exact'}
    if not (reports/'trace100').exists(): continue
    sys.argv = [str(analyzer), str(directory)]
    with contextlib.redirect_stdout(io.StringIO()):
        d = runpy.run_path(str(analyzer),run_name='__main__')
    ns, timeline = d['ns'], d['timeline']
    heights = sorted({r['height'] for r in d['rows']})
    follow = {}
    for node in ['gateway0','gateway1','wallet']+['org0-member'+str(i) for i in range(4)]+['org1-member'+str(i) for i in range(4)]:
        rows = []
        for h in heights:
            delta = lambda end,start: (ns(node,end,h)-ns(node,start,h))/1e6
            row = {'height':h, 'payments':sum(r['height']==h for r in d['rows']),
                'queue_ms':delta('follow_update_started','follow_update_requested'),
                'apply_in_writer_ms':delta('follow_apply_done','follow_apply_start'),
                'after_apply_to_return_ms':delta('follow_committed','follow_apply_done')}
            if any(e['Stage']=='follow_prepare_start' for e in timeline[node]):
                row['prepare_outside_ms'] = delta('follow_prepare_done','follow_prepare_start')
            rows.append(row)
        follow[node] = {'blocks':rows, 'apply_total_ms':sum(r['apply_in_writer_ms'] for r in rows),
                        'prepare_total_ms':sum(r.get('prepare_outside_ms',0) for r in rows)}
    stores = {}
    for node in ['gateway0','gateway1']+['committee'+str(i) for i in range(4)]:
        stores[node] = [e['Fields'] for e in timeline[node] if e['Stage']=='store_update']
    result['traces'][directory.name] = {'stages_ms':d['summary'], 'business_blocks':d['blocks'], 'followers':follow, 'stores':stores,
        'approval_queue_ms':stats([r['storage_queue_ms'] for r in d['votes']]),
        'approval_callback_ms':stats([r['storage_callback_ms'] for r in d['votes']]),
        'approval_return_ms':stats([r['storage_return_ms'] for r in d['votes']]),
        'first_send_to_last_commit_s':(max(r['commit_ns'] for r in d['rows'])-first)/1e9,
        'last_send_to_last_commit_s':(max(r['commit_ns'] for r in d['rows'])-last)/1e9}

for stage in stages:
    reports = [json.loads(p.read_text()) for p in sorted(root.glob('dbo-'+stage+'-[123]/reports/bench-v4-0.json'))]
    assert len(reports) == 3
    fast = [(s['FastUnixNS']-s['SentUnixNS'])/1e6 for b in reports for s in b['Samples']]
    elapsed = [b['Summary']['elapsed_s'] for b in reports]
    result['groups'][stage] = {'rounds':3,'payments':len(fast), 'elapsed_s':stats(elapsed), 'pooled_fast_ms':stats(fast),
        'round_fast_p95_ms':[b['Summary']['fast_p95_ms'] for b in reports],
        'send_window_s':[result['runs'][f'dbo-{stage}-{i}']['send_window_s'] for i in range(1,4)]}
result['warm'] = {}
for label in ['warm-prepare','warm-credit','warm-map','warm2-credit','warm2-map']:
    reports = root/label/'reports'
    if not reports.exists(): continue
    warm = json.loads((reports/'bench-v4-0.json').read_text())
    measured = json.loads((reports/'bench-v4-1200.json').read_text())
    audit = json.loads((reports/'audit.json').read_text())
    assert warm['Summary']['failed'] == measured['Summary']['failed'] == 0
    assert all(n['Pending']==0 for n in audit)
    committees = [n for n in audit if n['Name'].startswith('committee')]
    assert len({n['StateHash'] for n in committees})==1
    assert all(n['Payments']==n['Closed']==1500 and n['CAL']=='2000000409600' and n['FUEL']=='2000000000000' and n['Rewards']=='126000' and n['Burned']=='15000' for n in committees)
    before = json.loads((reports/'warm-sizes-before.json').read_text())
    after = json.loads((reports/'warm-sizes-after.json').read_text())
    tail = warm['Samples'][-300:]
    result['warm'][label] = {'warmup':warm['Summary'],'next_300_new_wallet':measured['Summary'],
        'same_wallet_last_300_fast_ms':stats([(s['FastUnixNS']-s['SentUnixNS'])/1e6 for s in tail]),
        'same_wallet_last_300_block_ms':stats([(s['BlockObservedUnixNS']-s['SentUnixNS'])/1e6 for s in tail]),
        'node_file_growth':{k:{'before':v,'after':after[k]} for k,v in before.items() if not k.startswith('bench-')},
        'new_wallet_replays_prior_blocks':True}
(root/'comparison.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8')
print(json.dumps(result['groups'],indent=2))
