"""Recompute manuscript aggregates from frozen experiment CSVs."""
import csv
import json
from pathlib import Path
from statistics import median

root = Path(__file__).resolve().parents[1]
experiments = root.parents[1]/'experiments'
def rows(path):
    return list(csv.DictReader(path.open(encoding='utf-8-sig',newline='')))
chain = rows(experiments/'final-continuation-2026-10-02/results/cases.csv')
mixed = rows(experiments/'final-mixed-2026-10-02/runs.csv')
reads = rows(experiments/'history-reader-2026-10-02/summary.csv')
costs = rows(experiments/'history-reader-2026-10-02/costs.csv')
fast = [r for r in chain if r['mode']=='fast']
wait = [r for r in chain if r['mode']=='wait_final']
f = median(float(r['fast_chain_ms']) for r in fast)
w = median(float(r['fast_chain_ms']) for r in wait)
assert round(f/1000,3)==4.557 and round(w/1000,3)==64.476
assert sum(int(r['certified_precommit_successors']) for r in fast)==297
assert all(r['audit']=='passed' for r in chain)
assert sum(int(r['payments']) for r in mixed)==84192
assert sum(int(r['cal_paid']) for r in mixed)==4800
assert sum(int(r['cal_recovered']) for r in mixed)==4800
out={'production_build':'721800c','chain':{'fast_median_s':f/1000,'wait_median_s':w/1000,'ratio':w/f,'reduction_percent':100*(w-f)/w,'successors_before_earliest_app_commit':297},'mixed':{},'reader':[]}
for mode in 'NRCP':
    rr=[r for r in mixed if r['mode']==mode]
    assert len(rr)==3
    out['mixed'][mode]={k:median(float(r[k]) for r in rr) for k in ['fast_p50_ms','fast_p95_ms','fast_p99_ms','send_span_s','drain_after_last_send_s']}
    out['mixed'][mode]['max_dispatch_p95_ms']=max(float(r['dispatch_p95_ms']) for r in rr)
    assert all(int(r['fee_paid'])==(659504 if mode in 'NR' else 659544) for r in rr)
for r in reads:
    if r['workload']!='point_repaired': continue
    rr=[c for c in costs if (c['payments'],c['repairs'])==(r['payments'],r['repairs'])]
    a,b=float(r['index_total_p50_us']),float(r['canonical_total_p50_us'])
    reduction=100*(a-b)/a
    assert abs(reduction-float(r['median_reduction_percent']))<1e-9
    out['reader'].append({'payments':int(r['payments']),'repairs':int(r['repairs']),'index_us':a,'canonical_us':b,'reduction_percent':reduction,'extra_KV_bytes':median(float(c['added_representation_kv_bytes']) for c in rr),'local_work_ms':median(float(c['representation_work_ms']) for c in rr)})
(root/'measurement-checks.json').write_text(json.dumps(out,indent=2),encoding='utf-8')
print(json.dumps(out,indent=2))
