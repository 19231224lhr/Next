#!/usr/bin/env python3
"""Aggregate independent runs without treating payments as independent trials."""
import csv,json,statistics
from pathlib import Path
OUT=Path(__file__).resolve().parent
METRICS=['actual_tps','fast_p50_ms','fast_p95_ms','fast_p99_ms','public_p95_ms','closed_p95_ms','dispatch_p95_ms','peak_pending','drain_s','service_core_ms_per_completion','driver_core_ms_per_completion','http_payload_bytes_per_ready','max_oldest_observed_ms']
def main():
    rows=[json.loads(p.read_text()) for p in sorted(OUT.glob('formal-*/summary.json'))]
    assert len(rows)==9,'formal matrix incomplete'
    for r in rows:
        assert (OUT/r['case']/'passed.json').exists(),r['case']
        assert r['sent']==r['ready'] and r['errors']==0,r['case']
        n=r['sent']+r['warm_sent']
        r['logical_service_bytes_per_payment']=sum(x['added_logical_bytes'] for x in r['state_growth_including_warm'].values())/n
        r['service_peak_rss_gib']=r['peak_service_rss_bytes']/2**30
        r['descriptor_verifications_per_ready']=sum(x['Verifications'] for x in r['nodes'].values())/r['ready']
        r['descriptor_hit_rate']=sum(x['Hits'] for x in r['nodes'].values())/sum(x['Queries'] for x in r['nodes'].values())
    metrics=METRICS+['logical_service_bytes_per_payment','service_peak_rss_gib','descriptor_verifications_per_ready','descriptor_hit_rate']
    fields=['case','mode','sent','ready','active_senders','active_receivers','root_cal_inputs_used','certificate_inputs','chain_edges','before_parent_commit','missing_inputs','extra_submit_attempts','unsent_constructed','skipped_pacing_including_warm']+metrics
    with (OUT/'per-run.csv').open('w',newline='',encoding='utf-8') as f:
        w=csv.DictWriter(f,fields,extrasaction='ignore');w.writeheader();w.writerows(rows)
    by={}
    for mode in 'ABC':
        group=[r for r in rows if r['mode']==mode];assert len(group)==3
        by[mode]={k:{'median':statistics.median(r[k] for r in group),'min':min(r[k] for r in group),'max':max(r[k] for r in group)} for k in metrics}
        by[mode]['sent']=sum(r['sent'] for r in group)
        by[mode]['ready']=sum(r['ready'] for r in group)
    (OUT/'aggregate.json').write_text(json.dumps(by,indent=2)+'\n')
    print(json.dumps(by,indent=2))
if __name__=='__main__':main()
