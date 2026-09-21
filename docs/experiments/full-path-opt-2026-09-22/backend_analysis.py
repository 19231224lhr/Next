from pathlib import Path
import json,collections,statistics
D=Path(__file__).parent;E=D/'backendProfileRetry'
def stats(xs):
 xs=sorted(xs)
 return dict(n=len(xs),mean=statistics.mean(xs),p50=xs[int((len(xs)-1)*.5)],p95=xs[int((len(xs)-1)*.95)],max=xs[-1],negative=sum(x<0 for x in xs)) if xs else None
nodes=[];stores=[];operations=collections.defaultdict(list)
for i in range(4):
 by=collections.defaultdict(dict);bs={}
 for line in (E/f'committee{i}-timeline.jsonl').read_text().splitlines():
  ev=json.loads(line);f=ev['Fields'];h=int(f.get('height',0));stage=ev['Stage'];t=ev['UnixNS']
  if stage=='comet_operation':
   op=f['operation'];start=int(f['started_ns']);elapsed=int(f['elapsed_ns'])
   if i==0:operations[op].append(elapsed/1e6)
   if op=='blockstore_save':bs[h]=start+elapsed
  elif h>0:by[h].setdefault(stage,t)
 nodes.append(by);stores.append(bs)
wallet=collections.defaultdict(dict)
for ev in json.loads((E/'reports/wallet-timeline.json').read_text()):
 h=int(ev['Fields'].get('height',0))
 if h>0:wallet[h].setdefault(ev['Stage'],ev['UnixNS'])
records=collections.defaultdict(list)
for i in range(4):
 for r in json.loads((E/f'committee{i}-settlements.json').read_text()):records[r['Spend']].append((i,r))
bench=json.loads((E/'reports/bench-v4-0.json').read_text());samples={r['Fact']:r for r in bench['Samples'] if r.get('Fact')}
out=collections.defaultdict(list);coverage=collections.Counter();examples=[]
def add(name,a,z):
 if a and z:out[name].append((z-a)/1e6)
for fact, rs in records.items():
 sample=samples.get(fact)
 if not sample:continue
 executed=next(((i,r) for i,r in rs if i==0 and r['Height']>0),next(((i,r) for i,r in rs if r['Height']>0),(None,None)))
 if executed[1] is None:continue
 h=executed[1]['Height'];bn=nodes[0][h];wn=wallet[h]
 prepared=next(((i,r) for i,r in rs if r['PreparedUnixNS'] and r['PreparedHeight']==h),(None,None))
 if prepared[0] is None:continue
 pn=nodes[prepared[0]][h]
 def earliest(k):return min((r[k] for _,r in rs if r.get(k,0)>0),default=0)
 incoming=min((r for _,r in rs if r['ReceivedUnixNS'] and r['DeliveredUnixNS']),key=lambda r:r['DeliveredUnixNS'],default=None)
 add('send_to_first_mempool_enter',sample['SentUnixNS'],earliest('MempoolEnterUnixNS'))
 add('fast_to_first_mempool_enter',sample.get('FastUnixNS',0),earliest('MempoolEnterUnixNS'))
 if incoming:
  coverage['first_http_sender_'+incoming['Sender']]+=1
  add('send_to_public_delivery',sample['SentUnixNS'],incoming['DeliveredUnixNS'])
  add('public_delivery_to_receive',incoming['DeliveredUnixNS'],incoming['ReceivedUnixNS'])
  add('public_receive_to_mempool_enter',incoming['ReceivedUnixNS'],incoming['MempoolEnterUnixNS'])
  add('mempool_check',incoming['MempoolEnterUnixNS'],incoming['MempoolCheckedUnixNS'])
 accepted=earliest('AcceptedUnixNS') or earliest('MempoolCheckedUnixNS')
 add('accepted_to_prepare_enter',accepted,pn.get('prepare_enter',0))
 add('prepare_block',pn.get('prepare_enter',0),pn.get('prepare_done',0))
 add('prepare_done_to_commit_decision',pn.get('prepare_done',0),bn.get('entering commit step',0))
 add('decision_to_app_commit_done',bn.get('entering commit step',0),bn.get('commit_done',0))
 add('app_finalize',bn.get('finalize_enter',0),bn.get('finalize_done',0))
 add('app_commit',bn.get('commit_start',0),bn.get('commit_done',0))
 proof=stores[0].get(h+1,0)
 add('app_commit_to_next_header_stored',bn.get('commit_done',0),proof)
 add('next_header_stored_to_wallet_verify',proof,wn.get('follow_verify_start',0))
 add('wallet_verify',wn.get('follow_verify_start',0),wn.get('follow_verify_done',0))
 add('wallet_prepare',wn.get('follow_prepare_start',0),wn.get('follow_prepare_done',0))
 add('wallet_update_queue',wn.get('follow_update_requested',0),wn.get('follow_update_started',0))
 add('wallet_apply',wn.get('follow_apply_start',0),wn.get('follow_apply_done',0))
 add('wallet_commit_tail',wn.get('follow_apply_done',0),wn.get('follow_committed',0))
 add('wallet_committed_to_observed',wn.get('follow_committed',0),sample['BlockObservedUnixNS'])
 coverage['matched_payments']+=1
 if len(examples)<8:examples.append(dict(fact=fact,height=h,prepared_node=prepared[0],proof_header_stored=proof,wallet_block_commit=wn.get('follow_committed',0)))
result=dict(coverage=dict(coverage),stages_ms={k:stats(v) for k,v in out.items()},committee0_operations_ms={k:stats(v) for k,v in operations.items()},examples=examples,note='Diagnostic sampled payments. H+1 BlockStore Save completion is an availability proxy, not a separately observed HTTP publication time. Stage quantiles are not additive; negative gaps retain overlap/ordering information.')
(E/'backend-summary.json').write_text(json.dumps(result,indent=2));print(json.dumps({k:v for k,v in result.items() if k!='examples'},indent=2))
