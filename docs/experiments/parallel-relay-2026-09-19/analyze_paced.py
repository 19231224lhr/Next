"""Finite fixed-target-rate probes: reconstruct unique commits and backlog.

Uses complete per-fact settlement records, not the overwritten 4096-event ring.
Dispatch lag exposes generator backpressure; queue slope is descriptive only.
"""
import csv,json,statistics,sys
from pathlib import Path
root=Path(sys.argv[1]); reports=root/'reports'
b=json.loads((reports/'bench-v4-0.json').read_text())
settlement=json.loads((reports/'trace100/settlement.json').read_text())
audit=json.loads((reports/'audit.json').read_text())
n=b['Summary']['count']; samples=b['Samples']
assert b['Summary']['failed']==0 and len(samples)==n
assert all(s['SentUnixNS'] and s['FastUnixNS'] and s['MemberObservedUnixNS'] for s in samples)
assert all(r['Pending']==0 for r in audit)
committees=[r for r in audit if r['Name'].startswith('committee')]
assert len(committees)==4 and len({r['StateHash'] for r in committees})==1
assert all(r['Payments']==r['Closed']==n and r['Gap']=='0' for r in committees)
rows=[]
for s in samples:
    f=s['Fact']
    for node in settlement.values(): assert sum(r['CommittedUnixNS']>0 for r in node[f])==1
    c=[r for r in settlement['committee0'][f] if r['CommittedUnixNS']][0]
    rows.append({'fact':f,'scheduled':s['ScheduledUnixNS'],'sent':s['SentUnixNS'],'received':c['ReceivedUnixNS'],
                 'accepted':c['AcceptedUnixNS'],'fast':s['FastUnixNS'],'commit':c['CommittedUnixNS'],
                 'wallet':s['BlockObservedUnixNS'],'member':s['MemberObservedUnixNS']})
def stats(xs):
    xs=sorted(xs);return {'p50':statistics.median(xs),'p95':xs[int((len(xs)-1)*.95)],'max':max(xs)}
start=b['Summary']['started_unix_ns']; last_sent=max(r['sent'] for r in rows)
end=max(r['member'] for r in rows)
progress=[]
for offset in range(0,int((end-start)/1e6)+501,500):
    stamp=start+offset*1000000
    pending=[r for r in rows if r['received']<=stamp<r['commit']]
    progress.append({'seconds':offset/1000,**{s:sum(r[s]<=stamp for r in rows) for s in ['sent','received','accepted','fast','commit','wallet','member']},
                     'before_committee':sum(r['sent']<=stamp<r['received'] for r in rows),
                     'committee_pending':len(pending),'oldest_committee_ms':max([(stamp-r['received'])/1e6 for r in pending],default=0)})
steady=[r for r in progress if 5<=r['seconds']<=(last_sent-start)/1e9-1]
xs=[r['seconds'] for r in steady]; ys=[r['committee_pending'] for r in steady]
mx,my=statistics.mean(xs),statistics.mean(ys)
slope=sum((x-mx)*(y-my) for x,y in zip(xs,ys))/sum((x-mx)**2 for x in xs)
resources=[json.loads(line) for line in (reports/'resources.jsonl').read_text().splitlines()]
result={'benchmark':b['Summary'],'audit':'passed','unique_commits':n,
        'actual_send_rate':(n-1)/((last_sent-min(r['sent'] for r in rows))/1e9),
        'send_to_commit_ms':stats([(r['commit']-r['sent'])/1e6 for r in rows]),
        'dispatch_lag_ms':stats([s.get('DispatchLagMS',(s['SentUnixNS']-s['ScheduledUnixNS'])/1e6) for s in samples]),
        'dispatch_clock':'monotonic' if all('DispatchLagMS' in s for s in samples) else 'wall difference; may contain sub-millisecond clock adjustment',
        'committee_pending_peak':max(r['committee_pending'] for r in progress),
        'committee_pending_slope_per_s':slope,'oldest_committee_peak_ms':max(r['oldest_committee_ms'] for r in progress),
        'commit_drain_after_last_send_ms':(max(r['commit'] for r in rows)-last_sent)/1e6,
        'member_drain_after_last_send_ms':(end-last_sent)/1e6,
        'peak_process_rss_mib_sum':max(r['rss_kib_sum'] for r in resources)/1024,
        'runtime_file_growth_bytes':resources[-1]['runtime_file_bytes']-resources[0]['runtime_file_bytes'],
        'note':'Finite 20-second target-rate diagnostic run; not a maximum or long-term TPS claim. RSS sums may count shared pages; file growth includes WAL and logs.'}
(root/'paced-summary.json').write_text(json.dumps(result,indent=2)+'\n')
for name,data in [('paced-progress.csv',progress),('paced-transactions.csv',rows)]:
    with (root/name).open('w',newline='',encoding='utf-8') as out:
        w=csv.DictWriter(out,fieldnames=list(data[0]));w.writeheader();w.writerows(data)
print(json.dumps(result,indent=2))
