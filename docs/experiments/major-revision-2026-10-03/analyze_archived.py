"""Additional per-observation metrics; never add quantiles of two intervals."""
import csv,gzip,json,math,statistics
from pathlib import Path
E=Path(__file__).resolve().parent
root=E.parent/'final-mixed-2026-10-02/results'
rows=[]
def q(xs,p):
    return sorted(xs)[max(0,math.ceil(len(xs)*p)-1)]
for d in sorted(root.glob('r[123]-*')):
    p=d/'bench-v4-100.json'
    raw=p.read_bytes() if p.exists() else gzip.decompress(p.with_suffix('.json.gz').read_bytes())
    ss=json.loads(raw)['Samples']
    assert all(s['ScheduledUnixNS']>0 and s['FastUnixNS']>=s['SentUnixNS']>0 for s in ss)
    r=dict(run=d.name,n=len(ss),early_send_count=sum(s['SentUnixNS']<s['ScheduledUnixNS'] for s in ss),minimum_dispatch_ms=min((s['SentUnixNS']-s['ScheduledUnixNS'])/1e6 for s in ss))
    for tag,a,b in [('scheduled_receipt','FastUnixNS','ScheduledUnixNS'),('dispatch','SentUnixNS','ScheduledUnixNS'),('actual_receipt','FastUnixNS','SentUnixNS')]:
        vals=[(s[a]-s[b])/1e6 for s in ss]
        for name,pct in [('p50',.5),('p95',.95),('p99',.99)]:r[tag+'_'+name+'_ms']=q(vals,pct)
    rows.append(r)
with (E/'mixed-scheduled-receipt.csv').open('w',newline='') as f:
    w=csv.DictWriter(f,list(rows[0]));w.writeheader();w.writerows(rows)
summary={case:{k:statistics.median(r[k] for r in rows if r['run'].endswith(case)) for k in rows[0] if k not in ['run','n']} for case in ['N','R','C','P']}
(E/'mixed-scheduled-receipt.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps(summary,indent=2))
