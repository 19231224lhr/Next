"""Audit all exported runs and compare uninstrumented versions without selection."""
import json,statistics
from collections import defaultdict
from pathlib import Path
root=Path(__file__).resolve().parent
groups=defaultdict(list); runs=[]
for path in sorted(root.glob('group-*/reports/experiment.json')):
    metadata=json.loads(path.read_text()); n=metadata['count']; reports=path.parent
    b=json.loads((reports/'bench-v4-0.json').read_text())
    a=json.loads((reports/'audit.json').read_text())
    assert len(b['Samples'])==n and b['Summary']['failed']==0
    assert len({s['Fact'] for s in b['Samples']})==n
    assert all(r['Pending']==0 for r in a)
    c=[r for r in a if r['Name'].startswith('committee')]
    assert len(c)==4 and len({r['StateHash'] for r in c})==1
    outputs=n if metadata.get('target_rate') else 128
    assert all(r['Payments']==r['Closed']==n and r['Gap']=='0' and int(r['CAL'])==2000000000000+200*outputs and int(r['FUEL'])==2000000000000 and int(r['Rewards'])==84*n and int(r['Burned'])==10*n for r in c)
    row={'label':metadata['label'],'count':n,**b['Summary'],'audit':'passed'}
    runs.append(row)
    if not metadata['trace'] and n==100: groups[metadata['binaries']].append(row)
summary={name:{'n_runs':len(rows), 'elapsed_mean_s':statistics.mean(r['elapsed_s'] for r in rows),
               'elapsed_range_s':[min(r['elapsed_s'] for r in rows),max(r['elapsed_s'] for r in rows)],
               'mean_run_fast_p50_ms':statistics.mean(r['fast_p50_ms'] for r in rows),
               'mean_run_fast_p95_ms':statistics.mean(r['fast_p95_ms'] for r in rows)} for name,rows in groups.items()}
result={'runs':runs,'uninstrumented_groups':summary,'total_payments':sum(r['count'] for r in runs),'audit':'all passed'}
(root/'comparison.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'groups':summary,'payments':result['total_payments']},indent=2))
