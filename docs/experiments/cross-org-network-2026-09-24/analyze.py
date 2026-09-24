#!/usr/bin/env python3
"""Summarize sender-monotonic E7 measurements; never subtract process clocks."""
import gzip,json,sys
from pathlib import Path

def read(path):
    if path.exists():return json.loads(path.read_text())
    with gzip.open(str(path)+'.gz','rt') as f:return json.load(f)
def quantiles(xs,scale=1e6):
    if not xs:return {}
    xs=sorted(xs)
    return {str(p):xs[min(len(xs)-1,int((len(xs)-1)*p/100))]/scale for p in [50,95,99]}
def summarize(case):
    config=read(case/'configuration.json')
    reports=[read(case/'reports'/f'e7-formal-{i}.json') for i in range(2)]
    samples=[s for r in reports for s in r['Samples']]
    sent=[s for s in samples if s['SentNS']>0]
    def cohort(rows):
        return dict(sent=len(rows),ready=sum(s['ReadyNS']>0 for s in rows),public=sum(s['PublicNS']>0 for s in rows),closed=sum(all(s['MemberNS']) for s in rows),certificate_inputs=sum(s['CertificateInput'] for s in rows),missing=sum(s['Missing'] for s in rows),errors=[s['Error'] for s in rows if s.get('Error')][:10],ms={k:quantiles([s['Monotonic'][k] for s in rows if s['ReadyNS']>0]) for k in ['BuildNS','ScheduleLagNS','ResponseNS','FastNS','PublicNS']}|{k:quantiles([s[k] for s in rows if s['ReadyNS']>0]) for k in ['DeliveryNS','ReceiveNS','ReadyQueueNS']})
    windows=[]
    for start in range(0,config['duration'],10):
        rows=[s for s in sent if start*1e9<=s['SentElapsedNS']<(start+10)*1e9]
        windows.append(dict(start=start,tps=len(rows)/min(10,config['duration']-start),fast_ms=quantiles([s['Monotonic']['FastNS'] for s in rows if s['ReadyNS']>0]),public_ms=quantiles([s['Monotonic']['PublicNS'] for s in rows if s['PublicNS']>0])))
    completed=[s['SentElapsedNS']+s['Monotonic']['PublicNS'] for s in sent if s['PublicNS']>0]
    completion_windows=[dict(start=start,public_tps=sum(start*1e9<=t<(start+10)*1e9 for t in completed)/10) for start in range(0,config['duration']+10,10)]
    resources=[json.loads(line) for line in (case/'resources.jsonl').read_text().splitlines()]
    resources=[x for x in resources if x['phase']=='formal']
    peak_pending=max((sum(s['Pending'] for s in x['status']) for x in resources),default=0)
    oldest=max((s.get('OldestPendingNS',0) for x in resources for s in x['status']),default=0)/1e9
    input_types={str(kind):cohort([s for s in sent if s['CertificateInput']==kind]) for kind in [False,True]}
    return dict(case=case.name,config={k:config[k] for k in ['mode','rate','duration','rtt','seed']},all=cohort(sent),first120=cohort([s for s in sent if s['SentElapsedNS']<120e9]),per_org=[cohort([s for s in sent if s['Sender']%2==site]) for site in range(2)],input_types=input_types,planned=config['rate']*config['duration'],unsent=config['rate']*config['duration']-len(sent),unsent_samples=len(samples)-len(sent),chain_ms=quantiles([x for r in reports for x in (r['ChainNS'] or [])]),chain_count=sum(len(r['ChainNS'] or []) for r in reports),driver={k:sum(r[k] for r in reports) for k in ['Scheduled','SkippedPacing','NoReady','PendingFull','WorkerFull','ProgressErrors']},peak_pending=peak_pending,oldest_pending_s=oldest,windows=windows,completion_windows=completion_windows)

if __name__=='__main__':
    root=Path(__file__).resolve().parent
    for label in sys.argv[1:]:
        result=summarize(root/label);(root/label/'summary.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)
