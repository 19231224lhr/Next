from pathlib import Path
import json,csv,statistics
D=Path(__file__).parent
rows=[];details=[]
for p in sorted(D.glob('*/summary.json')):
 s=json.loads(p.read_text());m=json.loads(p.with_name('metadata.json').read_text());a=json.loads(p.with_name('audit.json').read_text())
 r={k:s[k] for k in ['label','target_tps','count','successful','execution_failed','send_span_s','complete_observed_s','completed_batch_tps','drain_observed_s','max_block_transactions']}
 r.update(accepted=s['sender']['accepted'],http_failed=s['sender']['failed'],accepted_uncommitted=s['sender']['accepted']-s['successful'],actual_send_tps=s['sender']['actual_send_rate'],dispatch_p95_ms=s['sender']['dispatch_lag_p95_ms'],http_p95_ms=s['sender']['http_p95_ms'],arm=m['arm'],entrypoints=m['entrypoints'],route=m.get('route','round-robin'),storage=m.get('application_store','synchronous-bbolt'),trace=m['trace'],state_equal=s['committees_equal'])
 resources=[x for x in json.loads(p.with_name('resources.json').read_text()) if 'nodes' in x]
 totals=[sum(n['rss_kib'] for n in x['nodes'])/1024**2 for x in resources if len(x['nodes'])==4]
 if totals:r.update(rss_start_gib=totals[0],rss_peak_gib=max(totals),rss_end_gib=totals[-1])
 windows=[x for x in s['windows'] if x['complete_send_window']]
 if windows:r.update(backlog_sample_max=max(x['accepted_not_observed'] for x in windows),commit_window_min=min(x['commit_tps'] for x in windows),commit_window_max=max(x['commit_tps'] for x in windows))
 rows.append(r);details.append(dict(summary=s,metadata=m,audit=a,resources_summary={k:v for k,v in r.items() if k.startswith('rss_')}))
(D/'results.json').write_text(json.dumps(details,indent=2)+'\n')
fields=list(dict.fromkeys(k for r in rows for k in r))
with (D/'comparison.csv').open('w',newline='') as f:
 w=csv.DictWriter(f,fields);w.writeheader();w.writerows(rows)
print('summarized',len(rows),'completed runs')
for r in rows:
 print(r['label'],r['successful'],r['http_failed'],round(r['completed_batch_tps'],1),round(r['drain_observed_s'],3))
