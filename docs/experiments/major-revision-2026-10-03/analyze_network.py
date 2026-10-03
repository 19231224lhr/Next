"""Recompute D1 run-level statistics without pooling independent runs."""
import csv,json,statistics,tarfile
from pathlib import Path
E=Path(__file__).resolve().parent
ROOT=E/'matrix-fd16384'
if not ROOT.exists():
    with tarfile.open(E/'matrix-fd16384.tgz') as archive:
        archive.extractall(E,filter='data')
def quantile(xs,p):
    xs=sorted(xs); pos=(len(xs)-1)*p; i=int(pos)
    return xs[i]+(xs[min(i+1,len(xs)-1)]-xs[i])*(pos-i)
def backlog(samples,key):
    events=[]
    for s in samples:
        events.extend([(s['SentNS'],1),(key(s),-1)])
    n=peak=0
    for _,delta in sorted(events):
        n+=delta;peak=max(peak,n)
    assert n==0
    return peak
rows=[];details=[]
for d in sorted(ROOT.iterdir()):
    if not d.is_dir():continue
    assert (d/'passed.json').exists(),d
    r=json.loads((d/'reports/fault-v4.json').read_text());ss=r['Samples']
    assert len(ss)==1200 and not r['WalletError']
    assert all(s['ReadyNS']>s['SentNS'] and s['PublicNS']>0 and min(s['MemberNS'])>0 for s in ss)
    case,rep=d.name.rsplit('-',1)
    row=dict(run=d.name,case=case,rep=int(rep),payments=1200,attempts=sum(s['Attempts'] for s in ss))
    metrics={'receipt':[s['FastMS'] for s in ss],
      'scheduled_receipt':[(s['ReadyNS']-s['ScheduledNS'])/1e6 for s in ss],
      'dispatch':[(s['SentNS']-s['ScheduledNS'])/1e6 for s in ss],
      'public':[(s['PublicNS']-s['SentNS'])/1e6 for s in ss],
      'members':[(max(s['MemberNS'])-s['SentNS'])/1e6 for s in ss]}
    for name,xs in metrics.items():
        for tag,q in [('p50',.5),('p95',.95),('p99',.99)]:row[name+'_'+tag+'_ms']=quantile(xs,q)
    row['span_s']=(max(s['SentNS'] for s in ss)-min(s['SentNS'] for s in ss))/1e9
    row['public_drain_s']=(max(s['PublicNS'] for s in ss)-max(s['SentNS'] for s in ss))/1e9
    row['member_drain_s']=(max(max(s['MemberNS']) for s in ss)-max(s['SentNS'] for s in ss))/1e9
    row['public_peak']=backlog(ss,lambda s:s['PublicNS'])
    row['member_peak']=backlog(ss,lambda s:max(s['MemberNS']))
    ev=json.loads((d/'events.json').read_text());pause=next((x['ns'] for x in ev if x['action']=='pause'),None);resume=next((x['ns'] for x in ev if x['action']=='resume'),None)
    if pause:
        row['ready_in_fault']=sum(pause<s['ReadyNS']<resume for s in ss)
        row['public_in_fault']=sum(pause<s['PublicNS']<resume for s in ss)
        # Sent at least one second after SIGSTOP, received before SIGCONT:
        # every observed organization QC must use the other three members.
        votes=[s for s in ss if s['SentNS']>pause+1_000_000_000 and s['ReadyNS']<resume]
        if case=='L-M':assert all({v['Member'] for v in s['Certificate']['QC']['Votes']}=={0,1,2} for s in votes)
        row['fault_clean_qcs']=len(votes)
    else:row.update(ready_in_fault=0,public_in_fault=0,fault_clean_qcs=0)
    chains=[]
    for i in range(2):
        c=json.loads((d/f'chain{i}/chain-v4.json').read_text());assert c['Requested']==10 and len(c['Hops'])==10
        assert all(h['CertificateInput'] for h in c['Hops'][1:])
        if pause:assert pause<c['Hops'][0]['SentUnixNS']<c['Hops'][-1]['ReadyUnixNS']<resume
        chains.append({k:c[k] for k in ['FastChainMS','PublicChainMS','ClosedChainMS']})
    row['chain_receipt_median_ms']=statistics.median(c['FastChainMS'] for c in chains)
    row['chain_receipt_max_ms']=max(c['FastChainMS'] for c in chains)
    rtt=json.loads((d/'rtt.json').read_text());row['rpc_health_rtt_median_ms']=statistics.median(x/1e6 for r in rtt for x in r['ns'])
    proxy=json.loads((d/'proxy.json').read_text())
    for kind in ['rpc','p2p']:
        ps=[v for k,v in proxy.items() if k.endswith(kind)]
        # A peer pair keeps one bidirectional connection after duplicate-peer
        # resolution. A validator may use outgoing connections only, leaving
        # its own listening proxy idle; every configured peer address is proxied.
        assert (all(s['chunks']>0 for s in ps) if kind=='rpc' else sum(s['chunks'] for s in ps)>0)
        row[kind+'_hold_mean_ms']=sum(s['hold_ns'] for s in ps)/sum(s['chunks'] for s in ps)/1e6
    audits=json.loads((d/'reports/audit.json').read_text());cs=[a for a in audits if a['Name'].startswith('committee')]
    assert len({c['StateHash'] for c in cs})==1 and all(c['Gap']=='0' and c['Payments']==c['Closed']==1320 for c in cs)
    assert all(c.get('Pending',0)==0 for c in audits)
    row['missing_sources_fulfilled']=cs[0]['Recovery']['Fulfilled']
    row['compensated']=cs[0]['Recovery']['PaidCAL']
    details.append(dict(run=d.name,chains=chains,progress_errors=r['ProgressErrors'],state_hash=cs[0]['StateHash']))
    rows.append(row)
with (E/'network-runs.csv').open('w',newline='',encoding='utf-8') as f:
    w=csv.DictWriter(f,fieldnames=list(rows[0]));w.writeheader();w.writerows(rows)
out=dict(build='9879f64',runs=len(rows),ordinary_payments=sum(r['payments'] for r in rows),chain_payments=20*len(rows),warm_payments=100*len(rows),conditions={})
for case in ['H','L','L-M','L-C']:
    rr=[r for r in rows if r['case']==case];assert len(rr)==3
    out['conditions'][case]={k:dict(median=statistics.median(r[k] for r in rr),min=min(r[k] for r in rr),max=max(r[k] for r in rr)) for k in rows[0] if k not in ['case','run','rep']}
out['details']=details
(E/'network-summary.json').write_text(json.dumps(out,indent=2)+'\n')
print('run receiptP50 receiptP95 publicP95 drain membersPeak ready/public during fault chainMS')
for r in rows:print(r['run'],*(round(r[k],3) for k in ['receipt_p50_ms','receipt_p95_ms','public_p95_ms','member_drain_s','member_peak','ready_in_fault','public_in_fault','chain_receipt_median_ms']))
