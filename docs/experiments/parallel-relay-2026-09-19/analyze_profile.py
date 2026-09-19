"""Summarize actual Comet calls; nested FilePV/sign and build/ABCI times overlap."""
import json, statistics, sys
from collections import defaultdict
from pathlib import Path
root=Path(sys.argv[1])
summary=json.loads((root/'stage-summary.json').read_text())
heights={row['height'] for row in summary['blocks']}
settlement=json.loads((root/'reports/trace100/settlement.json').read_text())
all_nodes={}
for path in (root/'reports/trace100').glob('committee*-timeline.json'):
    events=json.loads(path.read_text())
    assert len(events)<4096, 'truncated profile'
    operations=defaultdict(list)
    blocks=defaultdict(lambda:defaultdict(float))
    for e in events:
        f=e['Fields']; h=int(f.get('height',0))
        if h not in heights: continue
        if e['Stage']=='comet_operation':
            op=f['operation']; duration=int(f['elapsed_ns'])/1e6
            operations[op].append(duration); blocks[h][op]+=duration
    all_nodes[path.stem]={
        'per_call_ms':{k:{'n':len(v),'p50':statistics.median(v),'max':max(v),'sum':sum(v)} for k,v in operations.items()},
        'per_business_block_sums_ms':blocks,
    }
checks=[]
for row in summary['blocks']:
    node=row['proposer']; h=row['height']
    events=json.loads((root/f'reports/trace100/{node}-timeline.json').read_text())
    prep=min(e['UnixNS'] for e in events if e['Stage']=='prepare_enter' and e['Fields'].get('height')==str(h))
    for fact, records in settlement['committee0'].items():
        committed=[r for r in records if r['Height']==h and r['CommittedUnixNS']]
        if not committed: continue
        entry=committed[0]
        proposer=[r for r in settlement[node][fact] if r['PreparedHeight']==h][0]
        checked=proposer['MempoolCheckedUnixNS']
        assert checked and checked<=prep
        checks.append({'fact':fact,'height':h,'proposer':node,
                       'entry_receive_to_proposer_checked_ms':(checked-entry['ReceivedUnixNS'])/1e6,
                       'proposer_checked_to_prepare_ms':(prep-checked)/1e6})
result={'nodes':all_nodes,'proposal_queue':checks,
        'proposal_queue_p50_ms':{k:statistics.median(r[k] for r in checks) for k in checks[0] if k.endswith('_ms')}}
(root/'profile-summary.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'committee0':all_nodes['committee0-timeline']['per_call_ms'],'proposal_queue':result['proposal_queue_p50_ms']},indent=2))
