import csv, json, statistics, sys
from collections import Counter
from pathlib import Path

root = Path(sys.argv[1])
def stats(xs):
    xs = sorted(xs)
    return {'n':len(xs), 'median':statistics.median(xs), 'p95':xs[int((len(xs)-1)*.95)], 'max':max(xs), 'sum':sum(xs)} if xs else {}
def csvwrite(path, rows):
    with path.open('w', newline='') as f:
        w=csv.DictWriter(f,fieldnames=list(rows[0]),lineterminator=chr(10)); w.writeheader(); w.writerows(rows)

allruns=[]
for folder in sorted(root.glob('group-*')):
    report=folder/'reports'
    if not (report/'experiment.json').exists(): continue
    bench=json.loads((report/'bench-v4-0.json').read_text())
    result={'experiment':json.loads((report/'experiment.json').read_text()), 'benchmark':bench['Summary']}
    audit=json.loads((report/'audit.json').read_text())
    assert bench['Summary']['failed']==0 and len(bench['Samples'])==100
    assert all(a['Pending']==0 for a in audit), audit
    assert all(a['Closed']==100 for a in audit if a['Name'].startswith('committee')), audit
    committees=[a for a in audit if a['Name'].startswith('committee')]
    assert len(committees)==4 and len({a['StateHash'] for a in committees})==1, audit
    assert all(a['Gap']=='0' and a.get('PrivateProofRecords',0)==0 for a in committees), audit
    result['audit']='100 completed at every committee; all outboxes empty; supply audit passed'
    samples=bench['Samples']
    for label,start,end in [('fast_ms','SentUnixNS','FastUnixNS'),('block_observed_ms','SentUnixNS','BlockObservedUnixNS'),('member_observed_ms','SentUnixNS','MemberObservedUnixNS')]:
        result[label]=stats([(s[end]-s[start])/1e6 for s in samples])
    result['send_to_response_ms']=stats([(s['CertificateReceivedUnixNS']-s['SentUnixNS'])/1e6 for s in samples])
    result['wallet_verify_save_ms']=stats([(s['FastUnixNS']-s['CertificateReceivedUnixNS'])/1e6 for s in samples])
    if result['experiment']['trace']:
        events=json.loads((report/'trace100'/'gateway0-timeline.json').read_text())
        assert len(events)<4096
        labels={'persist_callback':'persist_update_requested','relay_callback':'relay_update_requested','follow_update_started':'follow_update_requested'}
        stores=[]; logical=[]
        for event in events:
            if event['Stage']!='store_update':continue
            f=event['Fields']; assert f['failed']=='false'
            marks=[e for e in events if e['Stage'] in labels and int(f['callback_ns'])<=e['UnixNS']<=int(f['evaluated_ns'])]
            assert marks, f
            row={'transaction':len(stores),'logical_updates':len(marks),'changed':f['no_changes']=='false','write_sync_ms':int(f['write_ns'])/1e6,'commit_ms':(int(f['returned_ns'])-int(f['writes_ns']))/1e6}
            stores.append(row)
            for m in marks:
                identity='spend' if m['Stage']!='follow_update_started' else 'height'
                req=max(e['UnixNS'] for e in events if e['Stage']==labels[m['Stage']] and e['Fields'][identity]==m['Fields'][identity] and e['UnixNS']<=m['UnixNS'])
                logical.append({'kind':m['Stage'],'identity':m['Fields'][identity],'transaction':row['transaction'],'wait_to_callback_ms':(m['UnixNS']-req)/1e6})
        def first(stage,fact):return min(e['UnixNS'] for e in events if e['Stage']==stage and e['Fields'].get('spend')==fact)
        payments=[]
        for s in samples:
            fact=s['Fact'];start=first('outbox_persist_start',fact);end=first('outbox_persist_done',fact)
            payments.append({'index':s['Index'],'fact':fact,'save_ms':(end-start)/1e6,'save_to_relay_ms':(first('relay_enter',fact)-end)/1e6,'save_wait_ms':(first('persist_callback',fact)-first('persist_update_requested',fact))/1e6})
        for field in ['save_ms','save_to_relay_ms','save_wait_ms']:result[field]=stats([p[field] for p in payments])
        result['physical_updates']=len(stores)
        result['changed_transactions']=sum(s['changed'] for s in stores)
        result['logical_updates']=len(logical)
        result['logical_by_kind']=dict(Counter(s['kind'] for s in logical))
        result['batch_sizes']=dict(sorted(Counter(s['logical_updates'] for s in stores).items()))
        result['write_sync_ms']=sum(s['write_sync_ms'] for s in stores)
        result['events']=len(events)
        csvwrite(report/'physical-commits.csv',stores);csvwrite(report/'logical-updates.csv',logical);csvwrite(report/'payment-waits.csv',payments)
        settlement=json.loads((report/'trace100'/'settlement.json').read_text())
        result['committee_records']={name:len(payments) for name,payments in settlement.items()}
        committed=[]
        for s in samples:
            entries=settlement['committee0'][s['Fact']]
            when=min(e['CommittedUnixNS'] for e in entries if e['CommittedUnixNS']>0)
            committed.append((when-s['SentUnixNS'])/1e6)
        result['committee_commit_ms']=stats(committed)
    (report/'comparison.json').write_text(json.dumps(result,indent=2),newline='\n')
    allruns.append(result)
(root/'group-comparison.json').write_text(json.dumps(allruns,indent=2),newline='\n')
print(json.dumps(allruns,indent=2))
