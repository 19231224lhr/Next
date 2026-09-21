from pathlib import Path
import json,statistics,collections,math,bisect
D=Path(__file__).parent

def quant(xs):
 xs=sorted(xs)
 return dict(n=len(xs),mean=statistics.mean(xs),p50=xs[int((len(xs)-1)*.5)],p95=xs[int((len(xs)-1)*.95)],p99=xs[int((len(xs)-1)*.99)],max=xs[-1],negative=sum(x<0 for x in xs)) if xs else None

def analyze(name):
 E=D/name;b=json.loads((E/'reports/bench-v4-0.json').read_text());rows=b['Samples'];s=b['Summary'];audit=json.loads((E/'reports/audit.json').read_text());blocks=json.loads((E/'blocks.json').read_text())
 sent=sorted(r['SentUnixNS'] for r in rows if r.get('SentUnixNS',0)>0);span=(sent[-1]-sent[0])/1e9
 out=dict(summary=s,send_span_s=span,actual_send_tps=(len(sent)-1)/span if span else None,successful_executions=sum(x['successful'] for x in blocks),failed_executions=sum(x['transactions']-x['successful'] for x in blocks),committee_equal=len({n['StateHash'] for n in audit if n['Name'].startswith('committee')})==1,outbox_pending=sum(n['Pending'] for n in audit),errors=dict(collections.Counter(r['Error'] for r in rows if r.get('Error'))))
 out['all_stages_ms']={}
 for label,a,z in [('send_to_response','SentUnixNS','CertificateReceivedUnixNS'),('wallet_verify_save','CertificateReceivedUnixNS','FastUnixNS'),('send_to_fast','SentUnixNS','FastUnixNS'),('fast_to_wallet_block','FastUnixNS','BlockObservedUnixNS'),('send_to_wallet_block','SentUnixNS','BlockObservedUnixNS'),('wallet_block_to_members','BlockObservedUnixNS','MemberObservedUnixNS'),('send_to_member_completion','SentUnixNS','MemberObservedUnixNS')]:
  out['all_stages_ms'][label]=quant([(r[z]-r[a])/1e6 for r in rows if r.get('FastUnixNS',0)>0 and r.get(a,0)>0 and r.get(z,0)>0])
 traced=[r for r in rows if r.get('Foreground')];selected=collections.defaultdict(list);critical=collections.defaultdict(list);gateway=collections.defaultdict(list);http=collections.defaultdict(list);negative=[];complete=[];sample_by_index={}
 urls=json.loads((E/'metadata.json').read_text()).get('member_urls',['http://127.0.0.1:'+str(24000+i) for i in range(4)])
 def add(dest,key,a,z):
  if a is not None and z is not None:dest[key].append((z-a)/1e6)
 for r in traced:
  events={(e['node'],e['stage']):e['unix_ns'] for e in r['Foreground']}
  def g(stage):return events.get(('gateway',stage))
  add(gateway,'send_to_gateway',r['SentUnixNS'],g('http_handler_enter'))
  add(gateway,'gateway_decode',g('http_handler_enter'),g('request_decoded'))
  add(gateway,'gateway_authorize',g('collect_enter'),g('owner_verified'))
  add(gateway,'gateway_pre_fanout',g('http_handler_enter'),g('fanout_ready'))
  add(gateway,'fanout_to_quorum',g('fanout_ready'),g('quorum_collected'))
  add(gateway,'quorum_to_response',g('quorum_collected'),g('response_ready'))
  add(gateway,'gateway_response_to_wallet',g('response_ready'),r['CertificateReceivedUnixNS'])
  votes=sorted((e for e in r['Foreground'] if e['stage']=='vote_selected'),key=lambda e:e['unix_ns'])
  per={}
  for vote in votes:
   alias=vote['node'];url=urls[int(alias.split('-')[1])]
   def m(stage):return events.get((url,stage))
   def n(stage):return events.get((alias,stage))
   parts=[('dispatch_schedule',n('dispatch_ready'),n('dispatch_started')),('dispatch_to_handler',n('dispatch_started'),m('http_handler_enter')),('member_decode',m('http_handler_enter'),m('request_decoded')),('member_validation',m('request_decoded'),m('validation_complete')),('validation_to_commit_request',m('validation_complete'),m('commit_requested')),('member_update_queue',m('commit_requested'),m('update_started')),('member_update_callback',m('update_started'),m('update_evaluated')),('member_update_finish',m('update_evaluated'),m('commit_returned')),('member_sign',m('commit_returned'),m('vote_signed')),('member_sign_to_response',m('vote_signed'),m('response_ready')),('member_response_to_selected',m('response_ready'),n('vote_selected')),('member_handler_total',m('http_handler_enter'),m('response_ready'))]
   for label,a,z in parts:
    add(selected,label,a,z)
    if vote is votes[-1]:
     add(critical,label,a,z)
     if a is not None and z is not None:per[label]=(z-a)/1e6
   for label,a,z in [('serialize',m('client_approve_enter'),m('request_serialized')),('get_connection',m('http_get_conn'),m('http_got_conn_reused') or m('http_got_conn_fresh')),('connection_to_written',m('http_got_conn_reused') or m('http_got_conn_fresh'),m('http_request_written')),('written_to_handler',m('http_request_written'),m('http_handler_enter')),('response_to_first_byte',m('response_ready'),m('http_first_byte')),('first_byte_to_approval_decoded',m('http_first_byte'),m('approval_decoded')),('decoded_to_selected',m('approval_decoded'),n('vote_selected'))]:add(http,label,a,z)
  if len(votes)==3 and len(per)==12:complete.append(r['Index'])
  sample_by_index[r['Index']]=per
 out['trace_coverage']=dict(expected=(len(rows)+99)//100,received=len(traced),three_selected_complete=len(complete),event_counts=dict(collections.Counter(len(r['Foreground']) for r in traced)),selected_votes=sum(len(v) for k,v in selected.items() if k=='member_validation'))
 out['gateway_ms']={k:quant(v) for k,v in gateway.items()};out['selected_member_ms']={k:quant(v) for k,v in selected.items()};out['critical_third_vote_ms']={k:quant(v) for k,v in critical.items()};out['selected_http_ms']={k:quant(v) for k,v in http.items()}
 out['traced_fast_ms']=quant([r['FastMS'] for r in traced if r['FastUnixNS']>0]);out['untraced_fast_ms']=quant([r['FastMS'] for r in rows if not r.get('Foreground') and r['FastUnixNS']>0])
 out['windows']=[];base=s['started_unix_ns'];prevsent=prevblock=0
 for sec in range(30,math.ceil(s['elapsed_s']/30)*30+1,30):
  t=base+sec*10**9;total=bisect.bisect_right(sent,t);block=sum(x['successful'] for x in blocks if x['observed_ns']<=t)
  batch=[r for r in rows if t-30*10**9<r['SentUnixNS']<=t and r['FastUnixNS']>0]
  active=[r for r in rows if 0<r['SentUnixNS']<=t and (r.get('TaskDoneUnixNS',0)==0 or r['TaskDoneUnixNS']>t)]
  pending=collections.Counter('fast' if not 0<r['FastUnixNS']<=t else 'wallet_block' if not 0<r['BlockObservedUnixNS']<=t else 'members' for r in active)
  out['windows'].append(dict(end_s=sec,sent_tps=(total-prevsent)/30,observed_commit_tps=(block-prevblock)/30,active=len(active),pending=dict(pending),fast_ms=quant([r['FastMS'] for r in batch]),critical_member_ms={k:quant([sample_by_index[r['Index']][k] for r in batch if k in sample_by_index.get(r['Index'],{})]) for k in critical}))
  prevsent=total;prevblock=block
 (E/'summary.json').write_text(json.dumps(out,indent=2));return out
import sys
results={}
for n in sys.argv[1:]:
 if (D/n/'reports/audit.json').exists():
  o=analyze(n);results[n]=o
  print(n, {k:o['summary'][k] for k in ['count','failed','elapsed_s','fast_p50_ms','fast_p95_ms','block_observed_p50_ms','member_applied_p50_ms','dispatch_lag_p95_ms','total_limit_hits','send_limit_hits']}, 'actual_send_tps',o['actual_send_tps'],'audit',o['committee_equal'],o['outbox_pending'])
(D/'latest-comparison.json').write_text(json.dumps(results,indent=2))
