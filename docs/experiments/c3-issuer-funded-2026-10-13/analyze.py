#!/usr/bin/env python3
"""Export auditable CSVs from native real-service reports; no synthetic results."""
import argparse
import base64
import csv
import json
import pathlib
from metrics import distribution, quantile, trace_spans


def read(path):
    return json.loads(path.read_text(encoding='utf-8'))


def write_csv(path, rows):
    if not rows:
        return
    columns = list(dict.fromkeys(k for row in rows for k in row))
    with path.open('w',newline='',encoding='utf-8-sig') as stream:
        writer=csv.DictWriter(stream,fieldnames=columns)
        writer.writeheader(); writer.writerows(rows)


def escrow_columns(snapshot):
    r=snapshot['Record']; e=r['Escrow']; c=e['Commitment']
    if c['Budget'] != sum(e[k] for k in ('Held','Rewards','Burned','Released')):
        raise ValueError('escrow conservation failed in measured report')
    return {'quote':c['Quote'],'budget':c['Budget'],'quote_collected':r['QuoteCollected'],
            **{k.lower():e[k] for k in ('Held','Rewards','Burned','Released')}}


def fee_rows(name, report):
    stages, pairs, budgets = [], [], []
    for p in report['Pairs']:
        if p is None:
            continue
        for stage in p.get('Stages') or []:
            for who in ('Source','Child'):
                snapshot=stage.get(who)
                if snapshot:
                    stages.append({'run':name,'pair':p['Index'],'stage':stage['Name'],'role':who,
                                   'offset_ns':stage['OffsetNS'],**escrow_columns(snapshot)})
        closed=next((s for s in p.get('Stages') or [] if s['Name']=='closed'),None)
        row={'run':name,'case':report['Case'],'delay_ms':report['SourceDelayNS']/1e6,'pair':p['Index'],
             'error':p.get('Error',''), 'closed':bool(closed)}
        if closed:
            ob=closed.get('Obligation')
            status=None if ob is None else ob['Status']
            classification={None:'source-first',1:'child-first-fulfilled',3:'compensated-repaid'}.get(status,'other')
            row.update({'observed_order':classification,'obligation_status':status,
                        'replay_unchanged':(p['ReplayBefore']['Record']==p['ReplayAfter']['Record']) if p.get('ReplayBefore') and p.get('ReplayAfter') else None,
                        'repair_observed':bool(p.get('Repair'))})
            replay=p.get('Replay') or {}
            row['replay_outcome']=replay.get('Outcome','legacy-observation-only')
            row['replay_http_status']=replay.get('HTTPStatus')
            row['replay_inclusion']=json.dumps(replay.get('Inclusion'),separators=(',',':'))
            if row['replay_unchanged'] is False:
                raise ValueError('replay changed economic state')
            for who in ('Source','Child'):
                payment=p[who]; e=escrow_columns(closed[who])
                if payment['FeeNet'] != e['quote'] or not e['quote_collected'] or e['held']:
                    raise ValueError('closed payment quote or held budget mismatch')
                row.update({who.lower()+'_'+k:v for k,v in e.items()})
                row[who.lower()+'_user_fee']=payment['FeeNet']
                row[who.lower()+'_refund_checked']=payment.get('RefundChecked')
                refund=payment.get('FeeRefund')
                row[who.lower()+'_refund']=refund['Output']['Amount'] if refund else 0 if payment.get('RefundChecked') else None
                row[who.lower()+'_submit_attempts']=payment['SubmitAttempts']
                row[who.lower()+'_execution_height']=payment.get('ExecutionHeight')
                row[who.lower()+'_execution_index']=payment.get('ExecutionIndex')
                row[who.lower()+'_receipt_ms']=payment['ReceiptNS']/1e6
                row[who.lower()+'_submission_delay_ms']=(payment['SubmitOffsetNS']-payment['CertifiedOffsetNS'])/1e6
                row[who.lower()+'_certificate_bytes']=len(base64.b64decode(payment['CertificateRaw']))
                row[who.lower()+'_tx']=json.dumps(payment['Tx'],separators=(',',':'))
        pairs.append(row)
    for sample in report.get('Samples') or []:
        row={'run':name,'offset_ns':sample['OffsetNS'],'sample_error':sample.get('Error','')}
        pools=sample.get('Pools') or []
        if not row['sample_error'] and len(pools)==2:
            row['public_held_sum']=sum(p['Held'] for p in pools)
            for i,pool in enumerate(pools):
                for field in ('Held','Spent','Released','CumulativeFees','PoolRewards','PoolBurned','Revenue','Free'):
                    row[f'org{i}_{field}']=pool[field]
        for index,member in enumerate((sample.get('Members') or []) if not row['sample_error'] else []):
            for resource in member['Snapshot'].get('Resources') or []:
                key=resource['Key']
                # ResourceFUEL is 2; ignore unrelated policy/CAL slices.
                if key.get('Kind')==2:
                    reserved=sum(s['Reserved'] for s in resource.get('Slices') or [])
                    row[f'member{index}_url']=member['URL']
                    row[f'member{index}_fuel_pool']=key['Account']
                    row[f'member{index}_fuel_reserved']=reserved
                    row['maximum_member_fuel_reserved']=max(row.get('maximum_member_fuel_reserved',0),reserved)
        budgets.append(row)
    run={'run':name,'case':report['Case'],'delay_ms':report['SourceDelayNS']/1e6,'requested_pairs':report['Count'],
         'closed_pairs':sum(p['closed'] for p in pairs),'error':report.get('Error',''),
         'sampled_public_held_peak':max((b['public_held_sum'] for b in budgets if 'public_held_sum' in b),default=None),
         'sampled_member_reserved_peak':max((b['maximum_member_fuel_reserved'] for b in budgets if 'maximum_member_fuel_reserved' in b),default=None),
         'valid_public_samples':sum('public_held_sum' in b for b in budgets),
         'totals_scope':'fees below sum only pairs reaching the closed snapshot; pool deltas include all activity',
         'source_first':sum(p.get('observed_order')=='source-first' for p in pairs),
         'child_first_fulfilled':sum(p.get('observed_order')=='child-first-fulfilled' for p in pairs),
         'compensated_repaid':sum(p.get('observed_order')=='compensated-repaid' for p in pairs)}
    for key in ('source_user_fee','child_user_fee','source_burned','child_burned','source_rewards','child_rewards','source_released','child_released'):
        run[key]=sum(p.get(key,0) for p in pairs)
    initial,final=report.get('InitialPools') or [],report.get('FinalPools') or []
    reconcile=len(initial)==len(final)==2
    run['pool_reconciliation_available']=reconcile
    run['pool_snapshot_error']=report.get('FinalSnapshotError','')
    for i,(before,after) in enumerate(zip(initial,final) if reconcile else []):
        if before['Grant']['ID'] != after['Grant']['ID']:
            raise ValueError('pool identity changed within a run')
        run[f'org{i}_grant']=before['Grant']['ID']
        for field in ('Spent','CumulativeFees','Released','PoolRewards','PoolBurned','Revenue'):
            run[f'org{i}_{field}_delta']=after[field]-before[field]
        if after['Funding'] != after['Free']+after['Held']+after['Spent']:
            raise ValueError('one-grant fixture funding conservation failed')
    return stages,pairs,budgets,run


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root',type=pathlib.Path,required=True)
    p.add_argument('--out',type=pathlib.Path,required=True)
    args=p.parse_args(); args.out.mkdir(parents=True,exist_ok=True)
    runs=[]; hops=[]; mixed=[]; stages=[]; pairs=[]; budgets=[]; fee_runs=[]; traces=[]; errors=[]; attempts=[]
    for manifest_path in sorted(args.root.rglob('manifest.json')):
        directory=manifest_path.parent
        if not (directory/'result.json').exists():
            errors.append({'run':directory.name,'error':'missing result.json'}); continue
        manifest=read(manifest_path); result=read(directory/'result.json'); name=directory.name
        result.pop('closure_errors',None)
        result.update({'run':name,'trace':manifest['trace']})
        if result['native_exit_code']:
            errors.append({'run':name,'error':f"native exit {result['native_exit_code']}; retained, excluded from successful-run summaries"})
        if manifest['workload']!='fees':
            runs.append(result)
        reports=directory/'reports'
        if manifest['workload']=='fees':
            native=read(reports/'c3-fees.json')
            a,b,c,d=fee_rows(name,native)
            stages+=a; pairs+=b; budgets+=c; fee_runs.append(d)
            for pair in native['Pairs']:
                if pair:
                    for role in ('Source','Child'):
                        for index,attempt in enumerate(pair[role].get('SubmitAttemptLog') or []):
                            attempts.append({'run':name,'pair':pair['Index'],'role':role,'attempt_index':index,**attempt})
        elif manifest['workload']=='chain':
            for h in read(reports/'chain-v4.json')['Hops']:
                hops.append({'run':name,'variant':manifest['variant'],'trace':manifest['trace'],
                             **{k:h.get(k) for k in ('Hop','BuildMS','FastMS','RequestBytes','CertificateBytes','Attempts')}})
                for span in trace_spans(h.get('Foreground')):
                    traces.append({'run':name,'variant':manifest['variant'],'hop':h['Hop'],**span})
            if (reports/'settlement-traces.json').exists():
                for record in read(reports/'settlement-traces.json'):
                    seen=set()
                    for item in record.get('records') or []:
                        key=(item['Attempt'],item['Height'])
                        if key in seen or item.get('CommittedUnixNS',0)<=0:
                            continue
                        seen.add(key)
                        ns=item['ExecuteDoneUnixNS']-item['ExecuteStartUnixNS']
                        if ns<0:
                            raise ValueError('negative diagnostic execution interval')
                        traces.append({'run':name,'variant':manifest['variant'],'hop':record['hop'],
                                       'node':'committee0','span':'committed_execute_wall','duration_ns':ns})
        else:
            start=read(reports/'e8-start.json')
            for s in read(reports/'e8-report.json')['Samples']:
                if start['FormalNS']<=s['ScheduledNS']<start['EndSendNS']:
                    mixed.append({'run':name,'variant':manifest['variant'],
                                  **{k:s.get(k) for k in ('Index','Lane','Hop','ScheduledNS','SentNS','ReadyNS','PublicNS','Error','Attempts','CertificateBytes')},
                                  **{'monotonic_'+k:v for k,v in (s.get('Monotonic') or {}).items() if not isinstance(v,list)}})
    known_errors={e['run'] for e in errors}
    for error_path in args.root.rglob('runner-error.json'):
        if error_path.parent.name not in known_errors:
            errors.append({'run':error_path.parent.name,'error':read(error_path)['error']})
            known_errors.add(error_path.parent.name)
    for status_path in args.root.rglob('*-status.jsonl'):
        for line in status_path.read_text().splitlines():
            item=json.loads(line)
            if item['returncode'] and item['name'] not in known_errors:
                errors.append({'run':item['name'],'error':f"runner exit {item['returncode']}; see native logs"})
                known_errors.add(item['name'])
    for filename,rows in [('performance_runs',runs),('chain_hops',hops),('mixed_payments',mixed),
                          ('fee_stages',stages),('fee_pairs',pairs),('budget_samples',budgets),
                          ('fee_runs',fee_runs),('diagnostic_spans',traces),('submission_attempts',attempts),('missing_runs',errors)]:
        write_csv(args.out/(filename+'.csv'),rows)
    summaries=[]
    for workload in ('chain','mixed'):
        for variant in ('baseline','c3'):
            selected=[r for r in runs if r['workload']==workload and r['variant']==variant and not r['trace'] and r['native_exit_code']==0]
            for metric in ('receipt_ms_p50','receipt_ms_p95','chain_total_ms','build_total_ms','certificate_bytes_p50','receipt_goodput_per_s','public_goodput_per_s','completion_fraction'):
                values=[r[metric] for r in selected if isinstance(r.get(metric),(int,float))]
                if values:
                    summaries.append({'workload':workload,'variant':variant,'metric':metric,'n_runs':len(values),
                                      'median':quantile(values,.5),'min':min(values),'max':max(values)})
    successful={r['run'] for r in runs if r['native_exit_code']==0}
    for variant in ('baseline','c3'):
        for span in sorted({t['span'] for t in traces}):
            per_run=[]
            for name in sorted({t['run'] for t in traces if t['variant']==variant and t['run'] in successful}):
                vals=[t['duration_ns']/1e3 for t in traces if t['run']==name and t['span']==span]
                if vals:
                    per_run.append(quantile(vals,.5))
            if per_run:
                summaries.append({'workload':'diagnostic','variant':variant,'metric':span+'_us_p50','n_runs':len(per_run),
                                  'median':quantile(per_run,.5),'min':min(per_run),'max':max(per_run)})
    write_csv(args.out/'summary.csv',summaries)
    (args.out/'analysis.json').write_text(json.dumps({'native_runs':len(runs)+len(fee_runs),'missing_runs':errors,
        'quantiles':'linear interpolation (n-1)*p; report per-run quantiles, then median/min/max across runs',
        'budget_peaks':'sampled lower bounds; pool snapshots queried sequentially, not atomic across organizations'},indent=2)+'\n')
    print(json.dumps({'performance_runs':len(runs),'fee_runs':len(fee_runs),'missing':len(errors)}))


if __name__=='__main__':
    main()
