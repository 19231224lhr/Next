#!/usr/bin/env python3
"""Export compact auditable timestamps without keys, certificates or full stores."""
import csv,gzip,json,math,sys
from pathlib import Path
D=Path(__file__).resolve().parent
FIELDS=['Index','Outcome','ScheduledUnixNS','SentUnixNS','CertificateReceivedUnixNS','FastUnixNS','BlockObservedUnixNS','MemberObservedUnixNS','TaskDoneUnixNS','FastMS','BlockObservedMS','MemberAppliedMS','DispatchLagMS','Error']
def quant(xs,q):
 xs=sorted(xs)
 return xs[int((len(xs)-1)*q)] if xs else 0
for label in sys.argv[1:]:
 E=D/label
 report=json.loads((E/'reports/bench-v4-0.json').read_text())
 summary=report['Summary'];rows=report['Samples'];blocks=json.loads((E/'blocks.json').read_text())
 with gzip.open(E/'payments.csv.gz','wt',newline='') as f:
  out=csv.DictWriter(f,fieldnames=FIELDS,extrasaction='ignore');out.writeheader();out.writerows(rows)
 base=summary['started_unix_ns'];end=summary['finished_unix_ns'];windows=[]
 for start_s in range(0,math.ceil((end-base)/1e9),10):
  lo=base+start_s*10**9;hi=min(end,lo+10*10**9);duration=(hi-lo)/1e9
  batch=[r for r in rows if lo<r['SentUnixNS']<=hi]
  active=[r for r in rows if 0<r['SentUnixNS']<=hi and (not r['TaskDoneUnixNS'] or r['TaskDoneUnixNS']>hi)]
  windows.append(dict(start_s=start_s,duration_s=duration,sent=len(batch),sent_tps=len(batch)/duration,observed_commits=sum(b['successful'] for b in blocks if lo<b['observed_ns']<=hi),pending=len(active),oldest_ms=max([(hi-r['SentUnixNS'])/1e6 for r in active],default=0),fast_p50_ms=quant([r['FastMS'] for r in batch],.5),fast_p95_ms=quant([r['FastMS'] for r in batch],.95),member_p95_ms=quant([r['MemberAppliedMS'] for r in batch],.95)))
 sent=[r['SentUnixNS'] for r in rows if r['SentUnixNS']>0]
 committed=[b for b in blocks if b['successful']]
 out=dict(summary=summary,windows=windows,actual_send_tps=(len(sent)-1)/((max(sent)-min(sent))/1e9),tail_member_ms=(max(r['MemberObservedUnixNS'] for r in rows)-max(sent))/1e6,tail_commit_observed_ms=(max(b['observed_ns'] for b in committed)-max(sent))/1e6,successful_executions=sum(b['successful'] for b in blocks),failed_executions=sum(b['transactions']-b['successful'] for b in blocks),transaction_blocks=len(committed),mean_transactions_per_block=sum(b['successful'] for b in committed)/len(committed))
 def cpu_time(value):
  n=0
  for v in value.split(':'):n=n*60+float(v)
  return n
 resources=json.loads((E/'resources.json').read_text());costs=[]
 for x,y in zip(resources,resources[1:]):
  if not base<=x['unix_ns']<y['unix_ns']<=end:continue
  old={line.split(None,3)[0]:line.split(None,3) for line in x['rows']}
  total=0;rss=0
  for line in y['rows']:
   row=line.split(None,3)
   if row[0] in old:
    total+=cpu_time(row[2])-cpu_time(old[row[0]][2]);rss+=int(row[1])
  costs.append(dict(duration_s=(y['unix_ns']-x['unix_ns'])/1e9,cpu_seconds=total,process_rss_sum_mib=rss/1024))
 out['steady_resource_intervals']=costs
 (E/'compact.json').write_text(json.dumps(out,indent=2))
 print(label,round(out['actual_send_tps'],2),round(summary['completed_per_second'],2),'tail_ms',round(out['tail_member_ms'],2))
