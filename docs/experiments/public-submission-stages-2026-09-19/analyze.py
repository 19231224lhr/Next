"""Reproduce the latest 100-payment stage report using the existing analyzer."""
import json,runpy,sys
from pathlib import Path
root=Path(__file__).resolve().parent
sys.argv=[str(root/'analyze.py'),str(root/'trace')]
data=runpy.run_path(str(root.parent/'parallel-relay-2026-09-19/analyze.py'),run_name='__main__')
rows,timeline,settlements=data['rows'],data['timeline'],data['settlements']
ns,stats,delta=data['ns'],data['stats'],data['delta']
extra=[]
for row in rows:
    f,h,node=row['fact'],row['height'],row['proposer']
    sample=next(s for s in data['b']['Samples'] if s['Fact']==f)
    ready=next(e['unix_ns'] for e in sample['Foreground'] if e['node']=='gateway' and e['stage']=='response_ready')
    scheduled=ns('gateway0','relay_action_scheduled',fact=f,target=-1)
    checked=next(r['MempoolCheckedUnixNS'] for r in settlements[node][f] if r['PreparedHeight']==h)
    prepare=ns(node,'prepare_enter',h)
    assert row['received_ns'] <= checked <= prepare
    previous=ns('committee0','commit_done',h-1)
    extra.append({'fact':f,
        'ready_to_scheduled_ms':delta(scheduled,ready),
        'scheduled_to_submit_ms':delta(row['submitted_ns'],scheduled),
        'submit_to_receive_ms':delta(row['received_ns'],row['submitted_ns']),
        'entry_receive_to_proposer_checked_ms':delta(checked,row['received_ns']),
        'proposer_checked_to_prepare_ms':delta(prepare,checked),
        'proposal_wait_overlap_previous_commit_ms':max(0,delta(min(previous,prepare),next(r['AcceptedUnixNS'] for r in settlements['committee0'][f] if r['CommittedUnixNS'])))})

def event(node,height,stage,vote=''):
    found=[e['UnixNS'] for e in timeline[node] if e['Stage']==stage and e['Fields'].get('height')==str(height) and vote in e['Fields'].get('vote','')]
    assert len(found)==1,(node,height,stage,vote,len(found))
    return found[0]

blocks=[]
names=['prepare_to_signed_proposal','proposal_delivery','received_to_process','process_proposal','process_to_signed_prevote','signed_prevote_to_precommit','precommit_to_signed_precommit','signed_precommit_to_commit_step','commit_step_to_finalize']
for block in data['blocks']:
    h,node=block['height'],block['proposer']
    stamps=[event(node,h,'prepare_done'),event(node,h,'signed proposal'),event('committee0',h,'received complete proposal block'),event('committee0',h,'process_enter'),event('committee0',h,'process_done'),event('committee0',h,'signed and pushed vote','TYPE_PREVOTE'),event('committee0',h,'entering precommit step'),event('committee0',h,'signed and pushed vote','TYPE_PRECOMMIT'),event('committee0',h,'entering commit step'),event('committee0',h,'finalize_enter')]
    row={'height':h,'payments':block['payments']}
    row.update({name+'_ms':delta(end,start) for name,start,end in zip(names,stamps,stamps[1:])})
    assert all(row[n+'_ms']>=0 for n in names)
    assert abs(sum(row[n+'_ms'] for n in names)-block['prepare_to_finalize_ms'])<.00001
    blocks.append(row)

first,last=min(r['sent_ns'] for r in rows),max(r['sent_ns'] for r in rows)
result={'origin':'wallet HTTP send; all processes on one Mac',
    'send_window_s':(last-first)/1e9,
    'last_send_to_last_commit_s':(max(r['commit_ns'] for r in rows)-last)/1e9,
    'last_send_to_last_member_observation_s':(max(r['members_observed_ns'] for r in rows)-last)/1e9,
    'extra_stages':{k:stats([r[k] for r in extra]) for k in extra[0] if k.endswith('_ms')},
    'consensus_block_stats':{n:stats([r[n] for r in blocks]) for n in blocks[0] if n.endswith('_ms')},
    'consensus_blocks':blocks,'transactions':extra,
    'rounds':{n:sorted({e['Fields']['round'] for e in es if e['Stage']=='entering new round'}) for n,es in timeline.items() if n.startswith('committee')}}
assert all(v==['0'] for v in result['rounds'].values())
for mode in ['trace','control']:
    report=json.loads((root/mode/'reports/bench-v4-0.json').read_text())
    audit=json.loads((root/mode/'reports/audit.json').read_text())
    committees=[n for n in audit if n['Name'].startswith('committee')]
    assert report['Summary']['failed']==0 and len(report['Samples'])==100
    assert len(audit)==14 and all(n['Pending']==0 for n in audit)
    assert len(committees)==4 and len({n['StateHash'] for n in committees})==1
    assert all(n['Payments']==n['Closed']==100 and n['Gap']=='0' and n['CAL']=='2000000025600' and n['FUEL']=='2000000000000' and n['Rewards']=='8400' and n['Burned']=='1000' for n in committees)
result['audit']='both runs passed'
(root/'details.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8')
