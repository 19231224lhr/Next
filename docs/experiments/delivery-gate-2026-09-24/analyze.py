#!/usr/bin/env python3
"""Reconstruct per-payment timing and backlog from E5 observations, never fill missing with zero."""
import csv
import gzip
import json
import math
from pathlib import Path
import statistics

OUT=Path(__file__).resolve().parent

def key(value):return bytes(value).hex() if isinstance(value,list) else value
def quantile(values,q):
    if not values:return None
    a=sorted(values);i=(len(a)-1)*q;lo=int(i);hi=math.ceil(i)
    return a[lo]+(a[hi]-a[lo])*(i-lo)
def dist(values):return {name:quantile(values,q) for name,q in [('p50',.5),('p95',.95),('p99',.99),('max',1)]}
def write_csv(path,rows):
    if not rows:return
    fields=list(dict.fromkeys(k for row in rows for k in row))
    with path.open('w',newline='') as f:
        w=csv.DictWriter(f,fieldnames=fields);w.writeheader();w.writerows(rows)

def analyze_case(path):
    config=json.loads((path/'configuration.json').read_text())
    is_chain=config.get('chain',False)
    gate={key(x['Tx']):x for x in json.loads((path/'gate.json').read_text())} if (path/'gate.json').exists() else {}
    timing={}
    submit={}
    dropped=0
    for file in path.glob('*-timing.json'):
        d=json.loads(file.read_text());dropped+=d['Dropped']
        if file.name.startswith('gateway'):timing=d['Facts']
        for fact,t in d['Facts'].items():submit[fact]=submit.get(fact,0)+t['Count'].get('submit_start',0)
    assert dropped==0, 'E5 observation capacity overflow'
    rows=[]
    if is_chain:
        report=json.loads((path/'reports/chain-v4.json').read_text());start=report['StartedUnixNS'];finish=start+int(report['ClosedChainMS']*1e6)
        for h in report['Hops']:
            rows.append(dict(index=h['Hop'],tx=key(h['Tx']),fact=key(h['Fact']),scheduled_ns=h['SentUnixNS'],sent_ns=h['SentUnixNS'],ready_ns=h['ReadyUnixNS'],public_ns=h['FinalUnixNS'],members_ns=h['MemberClosedUnixNS'],attempts=h['Attempts'],certificate_input=h['CertificateInput'],build_ms=h['BuildMS']))
    else:
        report=json.loads((path/'reports/fault-v4.json').read_text());start=report['StartedNS'];finish=report['FinishedNS']
        for s in report['Samples']:
            cert=s.get('Certificate')
            rows.append(dict(index=s['Index'],tx=key(cert['Summary']['Tx']) if cert else '',fact=key(cert['QC']['Fact']) if cert else '',scheduled_ns=s['ScheduledNS'],sent_ns=s['SentNS'],ready_ns=s['ReadyNS'],public_ns=s['PublicNS'],members_ns=max(s['MemberNS']) if all(s['MemberNS']) else 0,attempts=s['Attempts'],error=s.get('Error','')))
    for r in rows:
        if r['sent_ns']:
            r['lag_ms']=(r['sent_ns']-r['scheduled_ns'])/1e6
            for stage in ['ready','public','members']:
                if r[stage+'_ns']:r[stage+'_ms']=(r[stage+'_ns']-r['sent_ns'])/1e6
        g=gate.get(r['tx'])
        if g:
            r.update(upstream_ns=g['QCNS'],release_ns=g['ReleaseNS'],install3_ns=g['Install3NS'],gate_public_ns=g['PublicNS'],gate_reason=g['Reason'],install_calls=sum(g.get('InstallCalls',[])),observed_noops=sum(g.get('AlreadyObserved',[])))
            copies=[v for v in g.get('StoredNS',[]) if v]
            if copies:r['first_copy_ns']=min(copies)
            due=[v for v in [g['Install3NS'],g['PublicNS']] if v]
            if due:
                r['gate_ns']=min(due)
                if r['ready_ns']:r['window_ms']=max(0,(min(due)-r['ready_ns'])/1e6)
                if config['mode']=='B':assert g['ReleaseNS']>=min(due),'B released before gate'
            if g['ReleaseNS'] and g['QCNS']:r['proxy_hold_ms']=(g['ReleaseNS']-g['QCNS'])/1e6
            if g.get('EligibleNS') and due:
                r['condition_wait_ms']=max(0,(min(due)-g['EligibleNS'])/1e6)
                if config['mode']=='B':r['release_schedule_ms']=(g['ReleaseNS']-max(min(due),g['EligibleNS']))/1e6
        t=timing.get(r['fact'])
        if t:
            first=t['First'];r['qc_ns']=first.get('certificate_ready',0);r['background_ns']=first.get('persist_background_queued',0);r['submit_ns']=first.get('submit_start',0)
            if r['qc_ns'] and r['ready_ns']:r['qc_to_ready_ms']=(r['ready_ns']-r['qc_ns'])/1e6
        if r['fact'] in submit:r['submit_calls']=submit[r['fact']]
    sent=[r for r in rows if r['sent_ns']]
    summary=dict(case=path.name,**config,planned=len(rows),sent=len(sent),ready=sum(bool(r['ready_ns']) for r in rows),public=sum(bool(r['public_ns']) for r in rows),members=sum(bool(r['members_ns']) for r in rows),elapsed_s=(finish-start)/1e9,attempts=sum(r['attempts'] for r in rows),submit_calls=sum(r.get('submit_calls',0) for r in rows),install_calls=sum(r.get('install_calls',0) for r in rows))
    for metric in ['ready_ms','public_ms','members_ms','lag_ms','proxy_hold_ms','window_ms','qc_to_ready_ms','condition_wait_ms','release_schedule_ms']:
        for q,v in dist([r[metric] for r in rows if metric in r]).items():summary[metric+'_'+q]=v
    if sent:
        last=max(r['sent_ns'] for r in sent);summary['send_span_s']=(last-min(r['sent_ns'] for r in sent))/1e9
        complete=[r['members_ns'] for r in rows if r['members_ns']]
        summary['drain_from_last_send_s']=(max(complete)-last)/1e9 if complete else None
    summary['gate_install3']=sum(r.get('gate_reason')=='install3' for r in rows)
    summary['gate_public']=sum(r.get('gate_reason')=='public' for r in rows)
    summary['window_nonzero']=sum(r.get('window_ms',0)>0 for r in rows)
    if is_chain:
        summary['certificate_inputs']=sum(r['certificate_input'] for r in rows)
        summary['chain_ready_ms']=report['FastChainMS']
    curves=[]
    for sec in range(math.ceil((finish-start)/1e9)+1):
        now=start+sec*10**9
        inflight=[r for r in sent if r['sent_ns']<=now]
        public=[r for r in inflight if not r['public_ns'] or r['public_ns']>now]
        members=[r for r in inflight if not r['members_ns'] or r['members_ns']>now]
        curves.append(dict(case=path.name,second=sec,sent=len(inflight),public_pending=len(public),members_pending=len(members),oldest_members_s=(now-min(r['sent_ns'] for r in members))/1e9 if members else 0))
    with gzip.open(path/'payments.jsonl.gz','wt') as f:
        for r in rows:f.write(json.dumps(r,separators=(',',':'))+'\n')
    return summary,curves

if __name__=='__main__':
    summaries=[];curves=[]
    for path in sorted(OUT.glob('v2-*')):
        if not (path/'passed.json').exists():continue
        summary,curve=analyze_case(path);summaries.append(summary);curves+=curve
        print(json.dumps(summary),flush=True)
    write_csv(OUT/'matrix.csv',summaries);write_csv(OUT/'curves.csv',curves)
    (OUT/'summary.json').write_text(json.dumps(summaries,indent=2)+'\n')
