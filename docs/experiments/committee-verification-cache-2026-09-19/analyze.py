"""Validate cache experiments and reuse the existing stage analyzer."""
import json, runpy, statistics, sys, re
from pathlib import Path
root=Path(__file__).resolve().parent
base=root.parent/'parallel-relay-2026-09-19'
result={'runs':{},'traces':{},'fixed_block':{}}
for directory in sorted(root.glob('cache-*')):
    if not directory.is_dir(): continue
    reports=directory/'reports'
    b=json.loads((reports/'bench-v4-0.json').read_text())
    audit=json.loads((reports/'audit.json').read_text())
    committees=[n for n in audit if n['Name'].startswith('committee')]
    assert b['Summary']['failed']==0 and len(b['Samples'])==100
    assert len(audit)==14 and all(n['Pending']==0 for n in audit)
    assert len(committees)==4 and len({n['StateHash'] for n in committees})==1
    assert all(n['Payments']==n['Closed']==100 and n['Gap']=='0' and n['CAL']=='2000000025600' and n['FUEL']=='2000000000000' and n['Rewards']=='8400' and n['Burned']=='1000' for n in committees)
    samples=b['Samples']
    first,last=min(s['SentUnixNS'] for s in samples),max(s['SentUnixNS'] for s in samples)
    result['runs'][directory.name]={'summary':b['Summary'],'send_window_s':(last-first)/1e9,'audit':'14 outboxes empty; 4 identical committee states; balances/fees exact','experiment':json.loads((reports/'experiment.json').read_text())}
    if not (reports/'trace100').exists(): continue
    sys.argv=[str(base/'analyze.py'),str(directory)]
    d=runpy.run_path(str(base/'analyze.py'),run_name='__main__')
    timeline=d['timeline']; ns=d['ns']; stats=d['stats']; delta=d['delta']
    verifies={}
    for node in ['committee0','committee1','committee2','committee3']:
        es=[e for e in timeline[node] if e['Stage']=='direct_verify']
        fields=[e['Fields'] for e in es]
        assert fields
        counts={k:max(int(f[k]) for f in fields) for k in ['hits','misses','evictions','oversized','entries','bytes']}
        assert len(es)==counts['hits']+counts['misses'], 'lost verification events'
        assert sum(f['cached']=='true' for f in fields)==counts['hits']
        counts['hit_ms']=stats([int(f['duration_ns'])/1e6 for f in fields if f['cached']=='true'])
        counts['miss_ms']=stats([int(f['duration_ns'])/1e6 for f in fields if f['cached']=='false'])
        verifies[node]=counts
    commits=[]
    for b in d['blocks']:
        h=b['height']
        start=ns('committee0','commit_start',h)
        update=ns('committee0','commit_update_start',h)
        callback=ns('committee0','commit_update_callback',h)
        returned=ns('committee0','commit_update_return',h)
        done=ns('committee0','commit_done',h)
        commits.append({'height':h,'transactions':b['payments'],
          'prepare_changes_ms':delta(update,start),'wait_db_callback_ms':delta(callback,update),
          'db_apply_and_commit_ms':delta(returned,callback),'finish_ms':delta(done,returned),
          'total_ms':delta(done,start)})
        physical=[e['Fields'] for e in timeline['committee0'] if e['Stage']=='store_update' and update <= int(e['Fields']['requested_ns']) <= returned]
        if physical:
            assert len(physical)==1
            f=physical[0]
            commits[-1]['bolt']={'wait_lock_ms':delta(int(f['locked_ns']),int(f['requested_ns'])),
                'begin_ms':delta(int(f['callback_ns']),int(f['locked_ns'])),
                'build_changes_ms':delta(int(f['evaluated_ns']),int(f['callback_ns'])),
                'apply_kv_ms':delta(int(f['writes_ns']),int(f['evaluated_ns'])),
                'commit_ms':delta(int(f['returned_ns']),int(f['writes_ns'])),
                'native_write_ms':int(f['write_ns'])/1e6,'spill_ms':int(f['spill_ns'])/1e6,
                'rebalance_ms':int(f['rebalance_ns'])/1e6,'write_count':int(f['write_count'])}
    rows=d['rows']
    result['traces'][directory.name]={
      'verification':verifies,'commit_parts':commits,'blocks':d['blocks'],
      'send_window_s':(last-first)/1e9,
      'first_send_to_last_commit_s':(max(r['commit_ns'] for r in rows)-first)/1e9,
      'last_send_to_last_commit_s':(max(r['commit_ns'] for r in rows)-last)/1e9,
      'last_send_to_last_member_observation_s':(max(r['members_observed_ns'] for r in rows)-last)/1e9,
      'rounds':{n:sorted({e['Fields']['round'] for e in es if e['Stage']=='entering new round'}) for n,es in timeline.items() if n.startswith('committee')}
    }
    if directory.name in ['cache-profile-b','cache-store-profile']:
        sys.argv=[str(base/'analyze_profile.py'),str(directory)]
        runpy.run_path(str(base/'analyze_profile.py'),run_name='__main__')
pattern=r'mode=(\w+) txs=(\d+) full=(\d+) prepare_ms=([\d.]+) process_ms=([\d.]+) finalize_ms=([\d.]+)'
for mode,n,full,prepare,process,finalize in re.findall(pattern,(root/'fixed-block.txt').read_text()):
    result['fixed_block'].setdefault(mode,[]).append({'transactions':int(n),'full_verifications':int(full),'prepare_ms':float(prepare),'process_ms':float(process),'finalize_ms':float(finalize)})
result['untraced_groups']={}
for mode in ['a','b']:
    reports=[json.loads(p.read_text()) for p in sorted(root.glob('cache-plain-'+mode+'*/reports/bench-v4-0.json'))]
    fast=sorted((s['FastUnixNS']-s['SentUnixNS'])/1e6 for b in reports for s in b['Samples'])
    result['untraced_groups'][mode]={'rounds':len(reports),'transactions':len(fast),
        'mean_elapsed_s':statistics.mean(b['Summary']['elapsed_s'] for b in reports),
        'pooled_fast_p50_ms':statistics.median(fast),'pooled_fast_p95_ms':fast[int((len(fast)-1)*.95)]}
for t in result['traces'].values():
    assert all(rounds==['0'] for rounds in t['rounds'].values()), 'nonzero consensus round'
(root/'comparison.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8')
print(json.dumps({k:{'summary':v['summary'],'send_window_s':v['send_window_s']} for k,v in result['runs'].items()},indent=2))
