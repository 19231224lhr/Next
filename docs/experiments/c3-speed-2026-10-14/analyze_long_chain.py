"""Analyze client-local chain/follower timestamps; emit no transaction identifiers."""
import argparse, bisect, json, pathlib, statistics

p=argparse.ArgumentParser()
p.add_argument('root',type=pathlib.Path)
p.add_argument('output',type=pathlib.Path)
a=p.parse_args()

def quantile(v,p):
    v=sorted(v)
    x=(len(v)-1)*p; i=int(x)
    return v[i]+(v[min(i+1,len(v)-1)]-v[i])*(x-i)

def distribution(v):
    return dict(n=len(v),sum_ms=sum(v),p50_ms=statistics.median(v),p95_ms=quantile(v,.95),max_ms=max(v)) if v else {}

def analyze(directory):
    r=json.loads((directory/'reports/chain-v4.json').read_text())
    hops=r['Hops']
    f=directory/'reports/chain-follow-timing.json'
    followers=json.loads(f.read_text()) if f.exists() else []
    phases=[]
    for wallet,records in enumerate(followers):
        for rec in records:
            for label,lo,hi in [('fetch','FetchStartNS','FetchDoneNS'),('verify','FetchDoneNS','VerifiedNS'),('commit','VerifiedNS','CommittedNS')]:
                start,end=rec.get(lo,0),rec.get(hi,0)
                if start and end>=start:
                    phases.append(dict(wallet=wallet,height=rec['Height'],phase=label,start=start,end=end,duration_ms=(end-start)/1e6))
    rows=[]
    finalTimes=[]; closedTimes=[]; readyTimes=[]; events=[]
    for h in hops:
        sent,ready=h['SentUnixNS'],h['ReadyUnixNS']
        outstanding_receipt_public=bisect.bisect_right(readyTimes,sent)-bisect.bisect_right(finalTimes,sent)
        outstanding_public_member=bisect.bisect_right(finalTimes,sent)-bisect.bisect_right(closedTimes,sent)
        overlaps=[{k:x[k] for k in ['wallet','height','phase','duration_ms']} for x in phases if x['start']<ready and sent<x['end']]
        row=dict(hop=h['Hop'],receipt_ms=h['FastMS'],build_ms=h['BuildMS'],attempts=h['Attempts'],final_checks=h.get('FinalChecks',0),
                 pending_receipt_public_at_send=outstanding_receipt_public,pending_public_member_at_send=outstanding_public_member,
                 follower_overlap=overlaps,public_offset_ms=h.get('FinalOffsetMS'),closed_offset_ms=h.get('MemberOffsetMS'))
        rows.append(row)
        if ready: bisect.insort(readyTimes,ready)
        final=h.get('FinalUnixNS',0);closed=h.get('MemberClosedUnixNS',0)
        if final: bisect.insort(finalTimes,final)
        if closed: bisect.insort(closedTimes,closed)
        if ready and closed: events.extend([(ready,1),(closed,-1)])
    live=peak=0;peakAt=0
    for instant,delta in sorted(events):
        live+=delta
        if live>peak:peak,peakAt=live,instant
    start=r['StartedUnixNS']
    windows=[]
    for i in range(0,len(rows),100):
        band=rows[i:i+100]
        windows.append(dict(hops=[band[0]['hop'],band[-1]['hop']],receipt=distribution([x['receipt_ms'] for x in band]),
                            build=distribution([x['build_ms'] for x in band]),retries=sum(max(0,x['attempts']-1) for x in band),
                            final_checks=sum(x['final_checks'] for x in band),peak_pending_public=max(x['pending_receipt_public_at_send'] for x in band),
                            peak_pending_member=max(x['pending_public_member_at_send'] for x in band),top5=sorted(band,key=lambda x:x['receipt_ms'],reverse=True)[:5]))
    firstPublic=min(finalTimes) if finalTimes else None
    return dict(run=directory.name,chain_ms=r['FastChainMS'],observer_peak=peak,observer_peak_offset_ms=(peakAt-start)/1e6,
                first_public_offset_ms=(firstPublic-start)/1e6 if firstPublic else None,
                windows=windows,top10=sorted(rows,key=lambda x:x['receipt_ms'],reverse=True)[:10],
                overlap_groups={label:distribution([x['receipt_ms'] for x in rows if bool(x['follower_overlap'])==overlap]) for label,overlap in [('overlap',True),('no_overlap',False)]},
                phase_groups={phase:distribution([x['receipt_ms'] for x in rows if any(y['phase']==phase for y in x['follower_overlap'])]) for phase in ['fetch','verify','commit']},
                top10_first_block_overlap=sum(any(y['height']==1 for y in x['follower_overlap']) for x in sorted(rows,key=lambda x:x['receipt_ms'],reverse=True)[:10]),
                first_public_groups={label:distribution([x['receipt_ms'] for x,h in zip(rows,hops) if bool(firstPublic and h['SentUnixNS']<firstPublic)==before]) for label,before in [('before_first_public',True),('after_first_public',False)]},
                follower_phases=[dict(wallet=x['wallet'],height=x['height'],phase=x['phase'],duration_ms=x['duration_ms'],start_offset_ms=(x['start']-start)/1e6,end_offset_ms=(x['end']-start)/1e6) for x in phases])

dirs=[a.root] if (a.root/'reports/chain-v4.json').exists() else sorted(x.parent.parent for x in a.root.glob('*/reports/chain-v4.json'))
result=dict(clock_scope='All comparisons use client process timestamps: chain samples and the three wallet followers in payctl. No service clocks are compared. UnixNS assumes no client wall-clock discontinuity; overlap is correlation, not causation.',runs=[analyze(d) for d in dirs])
a.output.write_text(json.dumps(result,indent=2)+'\n')
for r in result['runs']:
    print(r['run'],'chain',round(r['chain_ms'],1),'firstpublic',round(r['first_public_offset_ms'],1),'peak',r['observer_peak'], 'top10',[(x['hop'],round(x['receipt_ms'],2),len(x['follower_overlap'])) for x in r['top10']])
