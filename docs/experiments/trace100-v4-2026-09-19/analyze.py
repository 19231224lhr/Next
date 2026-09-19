import json, csv, statistics as st
from pathlib import Path
from collections import defaultdict
p=Path(__file__).resolve().parent
load=lambda n:json.loads((p/n).read_text())
b=load('benchmark.json'); settlements=load('settlement.json')
timeline={f.name.replace('-timeline.json',''):json.loads(f.read_text()) for f in p.glob('*-timeline.json')}
timeline['wallet']=b['Timeline']
def ns(node,stage,h=None,fact=None):
    es=[e['UnixNS'] for e in timeline[node] if e['Stage']==stage and (h is None or e['Fields'].get('height')==str(h)) and (fact is None or e['Fields'].get('spend')==fact)]
    return min(es) if es else None
def delta(end,start):return (end-start)/1e6 if end is not None and start is not None else None
def stats(xs):
    xs=sorted(x for x in xs if x is not None)
    return {'n':len(xs),'p50':st.median(xs),'p95':xs[int((len(xs)-1)*.95)],'max':max(xs),'mean':st.mean(xs)} if xs else {}
rows=[]
for s in sorted(b['Samples'],key=lambda x:x['SentUnixNS']):
    f=s['Fact']; rec=settlements['committee0'][f][0]; h=rec['Height']
    fg={e['stage']:e['unix_ns'] for e in s['Foreground'] if e['node']=='gateway'}
    prep=min([(r['PreparedUnixNS'],node) for node,ss in settlements.items() for r in ss[f] if r['PreparedUnixNS']])
    proposer=prep[1]
    firstrelay=ns('gateway0','relay_enter',fact=f)
    submits=[(ns(node,'submit_start',fact=f),node) for node in timeline if node.startswith('org0-') or node=='gateway0']
    firstsubmit=min((t,n) for t,n in submits if t)
    received=rec['ReceivedUnixNS']; accepted=rec['AcceptedUnixNS']; commit=rec['CommittedUnixNS']
    fetches=[e for e in timeline['wallet'] if e['Stage']=='follow_fetch' and e['Fields'].get('height')==str(h)]
    header=next(e for e in fetches if e['Fields']['path'].startswith('/commit'))
    durable=ns('wallet','follow_committed',h)
    members=max(ns('org0-member'+str(i),'follow_committed',h) for i in range(4))
    r={'order':len(rows)+1,'index':s['Index'],'height':h,'fact':f,'proposer':proposer,'first_submitter':firstsubmit[1],
       'sent_ns':s['SentUnixNS'],'fast_ns':s['FastUnixNS'],'received_ns':received,'commit_ns':commit,'wallet_durable_ns':durable,'members_durable_ns':members,
       'send_offset_ms':delta(s['SentUnixNS'],b['Summary']['started_unix_ns']),
       'fast_ms':s['FastMS'],'wallet_to_gateway_ms':delta(fg['http_handler_enter'],s['SentUnixNS']),
       'gateway_collect_ms':delta(fg['response_ready'],fg['http_handler_enter']),
       'response_transfer_ms':delta(s['CertificateReceivedUnixNS'],fg['response_ready']),
       'wallet_receive_verify_save_ms':delta(s['FastUnixNS'],s['CertificateReceivedUnixNS']),
       'gateway_persist_ms':delta(ns('gateway0','outbox_persist_done',fact=f),ns('gateway0','outbox_persist_start',fact=f)),
       'gateway_queue_ms':delta(firstrelay,ns('gateway0','outbox_persist_done',fact=f)),
       'install_fanout_ms':delta(ns('gateway0','relay_ready',fact=f),ns('gateway0','install_fanout_start',fact=f)),
       'response_to_committee_ms':delta(received,fg['response_ready']),
       'fast_to_committee_ms':delta(received,s['FastUnixNS']),
       'delivery_ms':delta(received,rec['DeliveredUnixNS']),
       'admission_ms':delta(accepted,received),
       'accepted_to_prepare_enter_ms':delta(ns(proposer,'prepare_enter',h),accepted),
       'prepare_ms':delta(ns(proposer,'prepare_done',h),ns(proposer,'prepare_enter',h)),
       'prepare_done_to_finalize_ms':delta(ns('committee0','finalize_enter',h),ns(proposer,'prepare_done',h)),
       'prior_transactions_ms':delta(rec['FinalCheckStartUnixNS'],ns('committee0','finalize_enter',h)),
       'final_check_ms':delta(rec['FinalCheckDoneUnixNS'],rec['FinalCheckStartUnixNS']),
       'execute_ms':delta(rec['ExecuteDoneUnixNS'],rec['ExecuteStartUnixNS']),
       'remaining_block_ms':delta(rec['FinalizeDoneUnixNS'],rec['ExecuteDoneUnixNS']),
       'finalize_to_commit_start_ms':delta(rec['CommitStartUnixNS'],rec['FinalizeDoneUnixNS']),
       'commit_ms':delta(commit,rec['CommitStartUnixNS']),
       'send_to_commit_ms':delta(commit,s['SentUnixNS']),
       'next_header_wait_fetch_ms':delta(header['UnixNS'],commit),
       'block_results_fetch_ms':delta(ns('wallet','follow_verify_start',h),header['UnixNS']),
       'wallet_block_verify_ms':delta(ns('wallet','follow_verify_done',h),ns('wallet','follow_verify_start',h)),
       'wallet_update_queue_ms':delta(ns('wallet','follow_update_started',h),ns('wallet','follow_update_requested',h)),
       'wallet_block_apply_ms':delta(ns('wallet','follow_apply_done',h),ns('wallet','follow_apply_start',h)),
       'wallet_update_flush_ms':delta(durable,ns('wallet','follow_apply_done',h)),
       'wallet_observe_lag_ms':delta(s['BlockObservedUnixNS'],durable),
       'member_observe_lag_ms':delta(s['MemberObservedUnixNS'],members),
       'member_apply_from_commit_ms':delta(members,commit),
       'block_observed_ms':s['BlockObservedMS'],'members_observed_ms':s['MemberAppliedMS']}
    rows.append(r)
def writecsv(name,rows):
    with (p/name).open('w',newline='',encoding='utf-8-sig') as f:
        w=csv.DictWriter(f,fieldnames=list(rows[0])); w.writeheader(); w.writerows(rows)
writecsv('transactions.csv',rows)
summary={k:stats([r[k] for r in rows]) for k in rows[0] if k.endswith('_ms')}
(p/'stage-summary.json').write_text(json.dumps(summary,indent=2))
cohorts=[]
for start in range(0,100,20):
    rs=rows[start:start+20]; c={'send_order':f'{start+1}-{start+20}'}
    for k in ['send_offset_ms','fast_ms','gateway_queue_ms','response_to_committee_ms','accepted_to_prepare_enter_ms','send_to_commit_ms','block_observed_ms','members_observed_ms']:
        c[k]=st.median(r[k] for r in rs)
    cohorts.append(c)
writecsv('cohorts.csv',cohorts)
blocks=[]
for h in sorted({r['height'] for r in rows}):
    rs=[r for r in rows if r['height']==h]; proposer=rs[0]['proposer']
    blocks.append({'height':h,'payments':len(rs),'proposer':proposer,'prepare_ms':rs[0]['prepare_ms'],
                   'process_ms':delta(ns('committee0','process_done',h),ns('committee0','process_enter',h)),
                   'finalize_ms':delta(ns('committee0','finalize_done',h),ns('committee0','finalize_enter',h)),
                   'final_check_sum_ms':sum(r['final_check_ms'] for r in rs),'execute_sum_ms':sum(r['execute_ms'] for r in rs),
                   'commit_ms':rs[0]['commit_ms'],'prepare_to_finalize_ms':rs[0]['prepare_done_to_finalize_ms'],
                   'commit_offset_ms':delta(rs[0]['commit_ns'],b['Summary']['started_unix_ns'])})
writecsv('blocks.csv',blocks)
queues=[]
for i in range(25):
    stamp=b['Summary']['started_unix_ns']+i*250_000_000
    q={'elapsed_ms':i*250}
    for field in ['sent','fast','received','commit','wallet_durable','members_durable']:
        q[field]=sum(r[field+'_ns']<=stamp for r in rows)
    q['sent_not_received']=q['sent']-q['received']; q['received_not_committed']=q['received']-q['commit']
    queues.append(q)
writecsv('progress.csv',queues)
print(json.dumps(summary,indent=2)); print('COHORTS',json.dumps(cohorts,indent=2)); print('BLOCKS',json.dumps(blocks,indent=2));print('PROGRESS',json.dumps(queues,indent=2))
