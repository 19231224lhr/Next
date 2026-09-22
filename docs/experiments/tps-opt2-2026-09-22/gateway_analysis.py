
import json,statistics,collections,sys
from pathlib import Path
D=Path(__file__).parent
def q(xs):
 xs=sorted(xs)
 return dict(n=len(xs),p50=xs[int((len(xs)-1)*.5)],p95=xs[int((len(xs)-1)*.95)],max=xs[-1]) if xs else None
for label in sys.argv[1:]:
 E=D/label;b=json.loads((E/'reports/bench-v4-0.json').read_text());events=[json.loads(l) for l in (E/'gateway-timeline.jsonl').read_text().splitlines()]
 facts=collections.defaultdict(dict);counts=collections.Counter();db=[]
 for ev in events:
  stage=ev['Stage'];f=ev['Fields'];counts[stage]+=1
  if 'spend' in f:
   facts[f['spend']].setdefault(stage,ev['UnixNS'])
  if stage=='store_update' and f.get('no_changes')=='false':
   db.append({k:float(f[k])/1e6 for k in ['write_ns','spill_ns','rebalance_ns']})
 values=collections.defaultdict(list)
 for fact,e in facts.items():
  for name,a,z in [('ready_to_offer','certificate_ready','early_offered'),('ready_to_task','certificate_ready','task_enter'),('task_database_read','task_enter','pending_loaded'),('task_verify','pending_loaded','pending_verified'),('ready_to_submit','certificate_ready','submit_start'),('public_http','submit_start','submit_done'),('save_total','outbox_persist_start','outbox_persist_done'),('save_queue','persist_update_requested','persist_callback')]:
   if a in e and z in e:values[name].append((e[z]-e[a])/1e6)
 for row in b['Samples']:
  es={(e['node'],e['stage']):e['unix_ns'] for e in row.get('Foreground',[])}
  pairs=[('payer_get_conn_wait',('payer','payer_get_conn'),('payer','payer_got_conn')),('payer_write',('payer','payer_got_conn'),('payer','payer_wrote')),('payer_written_to_handler',('payer','payer_wrote'),('gateway','http_handler_enter'))]
  for name,a,z in pairs:
   if a in es and z in es:values[name].append((es[z]-es[a])/1e6)
 out=dict(counts=dict(counts),stages_ms={k:q(v) for k,v in values.items()},store_ms={k:q([x[k] for x in db]) for k in ['write_ns','spill_ns','rebalance_ns']})
 (E/'gateway-summary.json').write_text(json.dumps(out,indent=2));print(label,json.dumps(out,indent=2))
