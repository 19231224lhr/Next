#!/usr/bin/env python3
import argparse,bisect,csv,gzip,json,statistics
from pathlib import Path
OUT=Path(__file__).resolve().parent
def read(p):
    if p.exists():return json.loads(p.read_text())
    with gzip.open(str(p)+'.gz','rt') as f:return json.load(f)
def q(xs,p):
    xs=sorted(xs)
    return xs[int((len(xs)-1)*p)] if xs else None
def closed(s):
    return max(s['PublicNS'],*s['MemberNS']) if s['PublicNS'] and all(s['MemberNS']) else 0
def analyze(dest):
    r=read(dest/'reports/e8-report.json');start=read(dest/'start.json');cfg=read(dest/'configuration.json')
    lo,hi=start['FormalNS'],r['EndSendNS'];ss=[s for s in r['Samples'] if s['ScheduledNS']>=lo]
    sent=[s for s in ss if s['SentNS']>0];fast=[s for s in sent if s['ReadyNS']>0]
    commits={}
    if (dest/'metrics-final.json').exists():
        final=read(dest/'metrics-final.json')
        for name,d in final.items():
            if name.startswith('committee'):
                for b in d['Blocks'] or []:commits[b['Height']]=min(commits.get(b['Height'],b['NS']),b['NS'])
    edges=[s for s in sent if s['Parent']>=0];unfinal=unknown=0
    for s in edges:
        p=r['Samples'][s['Parent']];ns=commits.get(p['Height'])
        if ns is None:unknown+=1
        elif s['SentNS']<ns:unfinal+=1
    out=dict(case=dest.name,mode=cfg['mode'],rate=cfg['rate'],duration=(hi-lo)/1e9,sent=len(sent),ready=len(fast),errors=sum(bool(s.get('Error')) for s in sent),unsent_constructed=sum(not s['SentNS'] for s in ss),warm_sent=sum(s['SentNS']>0 and s['ScheduledNS']<lo for s in r['Samples']),actual_tps=len(sent)/((hi-lo)/1e9),certificate_inputs=sum(s['CertificateInput'] for s in sent),chain_edges=len(edges),before_parent_commit=unfinal,parent_commit_unknown=unknown,missing_inputs=sum(s['Missing'] for s in sent),progress_errors=r['ProgressErrors'],skipped_pacing_including_warm=r['SkippedPacing'],no_ready_including_warm=r['NoReady'],pending_full_including_warm=r['PendingFull'],extra_submit_attempts=sum(max(0,s['Attempts']-1) for s in sent),drain_s=(r['FinishedNS']-hi)/1e9)
    phases={'build':[(s['SentNS']-s['BuildNS'])/1e6 for s in sent], 'fast':[(s['ReadyNS']-s['SentNS'])/1e6 for s in fast], 'response':[(s['ReceivedNS']-s['SentNS'])/1e6 for s in fast], 'wallet':[(s['ReadyNS']-s['ReceivedNS'])/1e6 for s in fast], 'public':[(s['PublicNS']-s['SentNS'])/1e6 for s in sent if s['PublicNS']], 'member':[(max(s['MemberNS'])-s['SentNS'])/1e6 for s in sent if all(s['MemberNS'])], 'closed':[(closed(s)-s['SentNS'])/1e6 for s in sent if closed(s)], 'dispatch':[(s['SentNS']-s['ScheduledNS'])/1e6 for s in sent]}
    for phase,xs in phases.items():
        for key,p in [('p50',.5),('p95',.95),('p99',.99)]:out[phase+'_'+key+'_ms']=q(xs,p)
    out['active_senders']=len({s['Sender'] for s in sent});out['active_receivers']=len({s['Receiver'] for s in sent})
    out['root_cal_inputs_used']=sum(s['Parent']<0 for s in sent)
    out['driver_descriptor_including_warm']=r['Descriptor']
    if cfg['mode']=='C':
        roots={};tips={}
        for s in r['Samples']:
            if not s['SentNS']:continue
            root=s['Index'] if s['Parent']<0 else roots[s['Parent']]
            roots[s['Index']]=root;tips[root]=s['Hop']
        out['chains_including_warm']={'started':len(tips),'ten_hops_completed':sum(h==10 for h in tips.values()),'cutoff_prefixes':sum(h<10 for h in tips.values()),'unstarted_remaining_hops':sum(10-h for h in tips.values())}
    points=[]
    for kind,values in [('Sent',[s['SentNS'] for s in sent]),('Ready',[s['ReadyNS'] for s in fast]),('Public',[s['PublicNS'] for s in sent if s['PublicNS']]),('Closed',[closed(s) for s in sent if closed(s)])]:points.append((kind,sorted(values)))
    timeline=[]
    for sec in range(int((r['FinishedNS']-lo)/1e9)+2):
        ns=lo+sec*10**9;row={'Second':sec}
        for kind,values in points:row[kind]=bisect.bisect_right(values,ns)
        row['Pending']=row['Sent']-row['Closed'];timeline.append(row)
    out['peak_pending']=max(x['Pending'] for x in timeline)
    out['window_60s']=[]
    for second in range(0,int((hi-lo)/1e9),60):
        stop=min(second+60,(hi-lo)/1e9);xs=[s for s in sent if lo+second*1e9<=s['SentNS']<lo+stop*1e9]
        out['window_60s'].append(dict(start_s=second,end_s=stop,sent=len(xs),tps=len(xs)/(stop-second),fast_p95_ms=q([(s['ReadyNS']-s['SentNS'])/1e6 for s in xs if s['ReadyNS']],.95),closed_p95_ms=q([(closed(s)-s['SentNS'])/1e6 for s in xs if closed(s)],.95)))
    online=dest/'reports/e8-timeline.jsonl'
    if online.exists():
        rows=[json.loads(line) for line in online.read_text().splitlines()];rows=[x for x in rows if x['NS']>=lo]
        out['max_oldest_observed_ms']=max(((x['NS']-x['OldestNS'])/1e6 for x in rows if x['OldestNS']),default=0)
    with (dest/'timeline.csv').open('w',newline='') as f:w=csv.DictWriter(f,fieldnames=timeline[0].keys());w.writeheader();w.writerows(timeline)
    if (dest/'metrics-formal-start.json').exists():
        base=read(dest/'metrics-formal-start.json')['Nodes'];final=read(dest/'metrics-final.json');details={}
        for name,node in final.items():
            b=base[name];d={k:node['Descriptor'][k]-b['Descriptor'][k] for k in node['Descriptor']}
            d['http_bytes']=sum(x['RequestBytes']+x['ResponseBytes'] for x in node['HTTP'].values())-sum(x['RequestBytes']+x['ResponseBytes'] for x in b['HTTP'].values());d['hit_rate']=d['Hits']/d['Queries'] if d['Queries'] else None;details[name]=d
            d['http_by_category']={k:{field:x[field]-b['HTTP'].get(k,{}).get(field,0) for field in x} for k,x in node['HTTP'].items()}
        out['nodes']=details;out['http_payload_bytes_per_ready']=sum(d['http_bytes'] for d in details.values())/max(len(fast),1)
        initial=read(dest/'metrics-initial.json');growth={}
        for name,node in final.items():
            before=initial[name].get('State') or {};after=node.get('State') or {}
            if not before or not after:continue
            def totals(x):return [sum(v[i] for db in x.values() for v in db.values()) for i in [0,1]]
            b,a=totals(before),totals(after);growth[name]={'initial_entries':b[0],'final_entries':a[0],'added_entries':a[0]-b[0],'added_logical_bytes':a[1]-b[1]}
        out['state_growth_including_warm']=growth
    resources=[json.loads(line) for line in (dest/'resources.jsonl').read_text().splitlines()]
    resources=[x for x in resources if x['NS']>=lo]
    def proc(x):
        rows={}
        reverse={str(v):k for k,v in x['pids'].items()}
        for line in x['ps'].splitlines():
            pid,cpu,rss=line.split();parts=cpu.split(':');seconds=sum(float(v)*60**i for i,v in enumerate(reversed(parts)))
            rows[reverse[pid]]=(seconds,int(rss)*1024)
        return rows
    if len(resources)>1:
        first,last=proc(resources[0]),proc(resources[-1]);cpu={};peak={}
        out['first_formal_rss_bytes']={name:x[1] for name,x in first.items()}
        for name,x in first.items():
            if name in last:cpu[name]=max(0,last[name][0]-x[0])*1000
        for row in resources:
            for name,x in proc(row).items():peak[name]=max(peak.get(name,0),x[1])
        out['peak_service_rss_bytes']=max(sum(x[1] for name,x in proc(row).items() if name!='load') for row in resources)
        observed_completions=sum(closed(s)>0 and resources[0]['NS']<=closed(s)<=resources[-1]['NS'] for s in r['Samples'])
        out['cpu_window_seconds']=(resources[-1]['NS']-resources[0]['NS'])/1e9
        out['cpu_window_completions']=observed_completions
        out['cpu_core_ms']=cpu;out['peak_rss_bytes']=peak
        out['service_core_ms_per_completion']=sum(v for k,v in cpu.items() if k!='load')/max(observed_completions,1)
        out['driver_core_ms_per_completion']=cpu.get('load',0)/max(observed_completions,1)
    out['origin_outputs']=cfg['origins']
    (dest/'summary.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({k:v for k,v in out.items() if k!='nodes'},ensure_ascii=False))
    return out
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('labels',nargs='+');a=p.parse_args()
    for label in a.labels:analyze(OUT/label)
