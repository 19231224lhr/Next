"""Read retained reports only: completion, drain and concurrent observer counts."""
import argparse, collections, json, pathlib, statistics

p=argparse.ArgumentParser();p.add_argument('root',type=pathlib.Path);p.add_argument('output',type=pathlib.Path);a=p.parse_args()
rows=[]
for file in sorted(a.root.glob('r*-*/reports/chain-v4.json')):
    directory=file.parent.parent
    r=json.loads(file.read_text()); result=json.loads((directory/'result.json').read_text());hs=r['Hops']
    events=[]
    for h in hs:
        if h.get('ReadyUnixNS') and h.get('MemberClosedUnixNS'):
            events.extend([(h['ReadyUnixNS'],1),(h['MemberClosedUnixNS'],-1)])
    peak=live=0
    for _,delta in sorted(events):live+=delta;peak=max(peak,live)
    fast=r['FastChainMS']; public=r['PublicChainMS']; closed=r['ClosedChainMS']
    max_public=max(h['FinalOffsetMS'] for h in hs)
    rows.append(dict(run=directory.name,repeat=int(directory.name.split('-')[0][1:]),arm=directory.name.split('-')[1],
        completed=len(hs),requested=r['Requested'],fast_ms=fast,public_ms=public,closed_ms=closed,max_public_ms=max_public,
        public_drain_ms=public-fast,all_public_drain_ms=max_public-fast,closed_drain_ms=closed-fast,
        observer_peak=peak,total_http_attempts=sum(h['Attempts'] for h in hs),retried_hops=sum(h['Attempts']>1 for h in hs),
        progress_errors=sum(h.get('ProgressErrors',0) for h in hs),final_checks=sum(h.get('FinalChecks',0) for h in hs),
        final_confirmed=sum(h.get('FinalUnixNS',0)>0 for h in hs),member_closed=sum(h.get('MemberClosedUnixNS',0)>0 for h in hs),
        native_exit=result['native_exit_code'],error=r.get('Error',''),closure_status=result.get('closure_status'),closure_errors=result.get('closure_errors',[])))
stats={}
for arm in ['before','after']:
    s=[x for x in rows if x['arm']==arm]
    stats[arm]={k:dict(median=statistics.median(x[k] for x in s),min=min(x[k] for x in s),max=max(x[k] for x in s)) for k in ['fast_ms','public_ms','closed_ms','max_public_ms','public_drain_ms','closed_drain_ms','observer_peak','final_checks']}
pairs=[]
for repeat in sorted(set(x['repeat'] for x in rows)):
    s={x['arm']:x for x in rows if x['repeat']==repeat}
    pairs.append(dict(repeat=repeat,reduction_percent={k:100*(1-s['after'][k]/s['before'][k]) for k in ['fast_ms','public_ms','closed_ms','max_public_ms']},
                      drain_delta_ms={k:s['after'][k]-s['before'][k] for k in ['public_drain_ms','closed_drain_ms']},
                      observer_peak_delta=s['after']['observer_peak']-s['before']['observer_peak']))
fp=[]
for f in sorted(a.root.glob('r*-fastpay/native-client.json')):
    x=json.loads(f.read_text());fp.append(dict(run=f.parent.name,completed=x.get('completed'),probe_completed=x.get('probe_completed'),principal_conserved=x.get('principal_conserved')))
out=dict(source=str(a.root),scope='Retained client reports. Closure means wallet public observation plus recorded all-member closure; no new store-key inspection or economic audit was performed.',
         all_complete=all(x['completed']==x['requested']==x['final_confirmed']==x['member_closed']==1000 and not x['error'] and not x['closure_errors'] and x['native_exit']==0 and x['closure_status']=='complete' for x in rows),
         rows=rows,summary=stats,paired=pairs,fastpay=fp)
a.output.write_text(json.dumps(out,indent=2)+'\n')
print(json.dumps(dict(all_complete=out['all_complete'],summary=stats,paired=pairs),indent=2))
