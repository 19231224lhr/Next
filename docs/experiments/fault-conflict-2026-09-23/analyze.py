#!/usr/bin/env python3
"""Summarize finite E4 cohorts without dropping failed or undispatched samples."""
import bisect
import csv
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parent

def quantile(xs,q):
    xs=sorted(xs)
    return xs[int((len(xs)-1)*q)] if xs else None

def summarize(dest):
    config=json.loads((dest/'configuration.json').read_text())
    report=json.loads((dest/'reports/fault-v4.json').read_text())
    events=json.loads((dest/'events.json').read_text())
    samples=report['Samples'];start=report['StartedNS'];finish=report['FinishedNS']
    begin=next((e['NS'] for e in events if e['Event']=='fault_window_started'),start+int(config['phases'][0]*1e9))
    end=next((e['NS'] for e in events if e['Event']=='fault_window_ended'),begin+int(config['phases'][1]*1e9))
    sent=[s for s in samples if s['SentNS']];ready=[s for s in samples if s['ReadyNS']];public=[s for s in samples if s['PublicNS']]
    closed=[s for s in samples if all(s['MemberNS'])]
    fast=[s['FastMS'] for s in ready];lag=[(s['SentNS']-s['ScheduledNS'])/1e6 for s in sent]
    result={'label':dest.name,'kind':config['kind'],'seed':config['seed'],'planned':len(samples),'sent':len(sent),'ready':len(ready),'public':len(public),'all_members':len(closed),
            'errors':sum(bool(s.get('Error')) for s in samples),'attempts':sum(s['Attempts'] for s in samples),'elapsed_s':(finish-start)/1e9,
            'ready_p50_ms':quantile(fast,.5),'ready_p95_ms':quantile(fast,.95),'ready_p99_ms':quantile(fast,.99),'ready_max_ms':max(fast,default=None),
            'dispatch_p95_ms':quantile(lag,.95),'public_p50_ms':quantile([(s['PublicNS']-s['SentNS'])/1e6 for s in public],.5),
            'progress_errors':report['ProgressErrors'],'wallet_error':report['WalletError'],'phases':[]}
    result['fault_start_s']=(begin-start)/1e9;result['fault_end_s']=(end-start)/1e9
    result['forced_stop_events']=[e for e in events if e['Event']=='forced_stop']
    last_send=max((s['SentNS'] for s in sent),default=start)
    result['send_span_s']=(last_send-min((s['SentNS'] for s in sent),default=start))/1e9
    result['drain_after_last_send_s']=(finish-last_send)/1e9
    active=[i for i in range(4) if config['kind']!='A2' or i!=config['seed']%4]
    active_done=[max(s['MemberNS'][i] for i in active) for s in samples if all(s['MemberNS'][i] for i in active)]
    for name,low,high in [('before',start,begin),('fault',begin,end),('after',end,finish)]:
        ss=[s for s in sent if low<=s['SentNS']<high];xs=[s['FastMS'] for s in ss if s['ReadyNS']]
        result['phases'].append({'phase':name,'sent':len(ss),'ready':sum(s['ReadyNS']>0 for s in ss),'public':sum(s['PublicNS']>0 for s in ss),
                                 'window_s':(high-low)/1e9,'actual_send_tps':len(ss)/((high-low)/1e9),
                                 'ready_events':sum(low<=s['ReadyNS']<high for s in ready),'public_events':sum(low<=s['PublicNS']<high for s in public),
                                 'active_member_events':sum(low<=t<high for t in active_done),
                                 'target_in_qc':sum(any(v['Member']==config['seed']%4 for v in s['Certificate']['QC']['Votes']) for s in ss if s.get('Certificate')),
                                 'dispatch_p95_ms':quantile([(s['SentNS']-s['ScheduledNS'])/1e6 for s in ss],.95),
                                 'ready_p50_ms':quantile(xs,.5),'ready_p95_ms':quantile(xs,.95),
                                 'public_p50_ms':quantile([(s['PublicNS']-s['SentNS'])/1e6 for s in ss if s['PublicNS']],.5)})
    streams={'sent':sorted(s['SentNS'] for s in sent),'ready':sorted(s['ReadyNS'] for s in ready),'public':sorted(s['PublicNS'] for s in public),
             'all_members':sorted(max(s['MemberNS']) for s in closed)}
    streams['active_members']=sorted(active_done)
    curve=[]
    for sec in range(int((finish-start)/1e9)+2):
        now=start+int(sec*1e9);c={'label':dest.name,'second':sec}
        for name,times in streams.items():c[name]=bisect.bisect_right(times,now)
        c['public_pending']=c['sent']-c['public'];c['all_member_pending']=c['ready']-c['all_members']
        c['active_member_pending']=c['ready']-c['active_members']
        pending=[s['SentNS'] for s in sent if s['SentNS']<=now and (not s['PublicNS'] or s['PublicNS']>now)]
        c['oldest_public_s']=(now-min(pending))/1e9 if pending else 0
        window=[s['FastMS'] for s in ready if now-10e9<s['SentNS']<=now]
        c['ready_p95_ms']=quantile(window,.95)
        curve.append(c)
    result['peak_public_pending']=max(c['public_pending'] for c in curve)
    result['peak_all_member_pending']=max(c['all_member_pending'] for c in curve)
    result['peak_active_member_pending']=max(c['active_member_pending'] for c in curve)
    last=next(e for e in events if e['Event']=='load_end')
    result['proxy_counts']=last['proxy']['Counts']
    if config['kind']=='A2':
        # Observation upper bound: all faults sent before resume have caught up.
        target=config['seed']%4
        prior=[s for s in ready if s['SentNS']<end]
        result['paused_member_catchup_observed_s']=(max(s['MemberNS'][target] for s in prior)-end)/1e9 if all(s['MemberNS'][target] for s in prior) else None
    if config['kind'].startswith('B'):
        b=next(e for e in events if e['Event']=='boundary');e=next(e for e in events if e['Event']=='stopped_window_end')
        result['pause_window_s']=(e['NS']-b['NS'])/1e9;result['ready_during_stop']=e['ready'];result['public_during_stop']=e['public']
        if config['kind']=='B2':result['install_to_public_observed_ms']=(samples[0]['PublicNS']-b['proxy']['Counts']['0/v3/certificates']['LastOKNS'])/1e6
    if (dest/'reports/fault-audit.json').exists():
        audit=json.loads((dest/'reports/fault-audit.json').read_text())
        result['fuel_paid']=sum(r['Fee']['Rewards']+r['Fee']['Burned'] for r in audit)
        result['fuel_refund']=sum(r['Fee']['Refunded'] for r in audit)
    if (dest/'samples.jsonl').exists():
        minima={};limits={}
        for line in (dest/'samples.jsonl').read_text().splitlines():
            for node in json.loads(line)['Nodes']:
                status=node.get('Status',{})
                if 'Limited' in status:limits[node['Node']]=max(limits.get(node['Node'],0),sum(status['Limited']))
                for resource in status.get('Resources',[]):
                    k=str(resource['Key']['Kind'])
                    for slot in resource['Slices']:minima[k]=min(minima.get(k,slot['Available']),slot['Available'])
        result['min_worker_available_by_resource']=minima;result['budget_denials_peak_by_member']=limits
    return result,curve

def main():
    runs=[];curves=[];controls=[];excluded=[]
    for dest in sorted(ROOT.glob('v*-*')):
        if dest.name in ['v4-conflicts','v5-conflicts']:
            excluded.append({'label':dest.name,'reason':'debug/control predecessor; v6 retains tampered wire and checks parse, payer authorization and exact INVALID_AUTH response'})
            continue
        if not (dest/'passed.json').exists():
            excluded.append({'label':dest.name,'reason':'no passed marker; inspect retained setup/load/audit logs'})
            continue
        if (dest/'reports/fault-v4.json').exists():
            summary,curve=summarize(dest);runs.append(summary);curves+=curve
        elif (dest/'reports/fault-conflict-audit.json').exists():
            rows=json.loads((dest/'reports/fault-conflicts.json').read_text());audit=json.loads((dest/'reports/fault-conflict-audit.json').read_text())
            for kind in sorted(set(r['Kind'] for r in rows)):
                rr=[r for r in rows if r['Kind']==kind];aa=[r for r in audit if r['Kind']==kind]
                controls.append({'label':dest.name,'kind':kind,'cases':len(rr),'public':sum(r['Public'] for r in aa),
                                 'zero_qc':sum(not any(r['Certificates'] or []) for r in rr),
                                 'rejected_binding_checks':sum(r['RejectedChecks'] for r in rr),
                                 'partial_member_approvals':sum(len(r.get('Partial') or []) for r in aa),
                                 'partial_resources_overlap':{str(k):sum(d['Cap'] for r in aa for a in (r.get('Partial') or []) for d in a['Remaining'] if d['Key']['Kind']==k) for k in [1,2,3,4,5]},
                                 'invalid_auth_http_responses':sum('HTTP 400: INVALID_AUTH' in e for r in rr for e in (r['Errors'] or [])),
                                 'valid_install_controls':sum(r.get('ValidInstallChecks',0) for r in rr),
                                 'fuel_paid':sum(f['Rewards']+f['Burned'] for r in aa for f in (r['Fees'] or []))})
    (ROOT/'summary.json').write_text(json.dumps({'runs':runs,'conflicts':controls,'excluded':excluded},indent=2)+'\n')
    if curves:
        with (ROOT/'curves.csv').open('w',newline='') as f:w=csv.DictWriter(f,fieldnames=list(curves[0]));w.writeheader();w.writerows(curves)
    formal=[r for r in runs if r['planned']==24000 and r['kind'] in ['A0','A1','A2']]
    if formal:
        fields=['label','kind','seed','planned','sent','ready','public','all_members','errors','attempts','elapsed_s','ready_p50_ms','ready_p95_ms','ready_p99_ms','ready_max_ms','dispatch_p95_ms','public_p50_ms','peak_public_pending','peak_active_member_pending','peak_all_member_pending','paused_member_catchup_observed_s','fuel_paid','fuel_refund']
        with (ROOT/'matrix.csv').open('w',newline='') as f:
            w=csv.DictWriter(f,fieldnames=fields);w.writeheader();w.writerows({k:r.get(k) for k in fields} for r in formal)
        phases=[dict(label=r['label'],kind=r['kind'],**p) for r in formal for p in r['phases']]
        with (ROOT/'phase-matrix.csv').open('w',newline='') as f:w=csv.DictWriter(f,fieldnames=list(phases[0]));w.writeheader();w.writerows(phases)
    print(json.dumps({'runs':len(runs),'controls':controls},indent=2))

if __name__=='__main__':main()
