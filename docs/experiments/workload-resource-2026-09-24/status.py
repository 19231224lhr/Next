#!/usr/bin/env python3
import json,time
from pathlib import Path
OUT=Path(__file__).resolve().parent;ROOT=OUT.parents[2]
log=ROOT/'.run/e8-suite.log'
case=None
for line in log.read_text().splitlines():
    try:row=json.loads(line)
    except ValueError:continue
    if 'start' in row:case=row['start']
out={'current':case,'audited':sorted(p.parent.name for p in OUT.glob('*/passed.json') if p.parent.name.startswith(('formal-','burst-'))),'complete':(OUT/'suite-complete.json').exists()}
summaries=list(OUT.glob('formal-*/summary.json'))+list(OUT.glob('burst-*/summary.json'))
if summaries:
    result=json.loads(max(summaries,key=lambda p:p.stat().st_mtime).read_text())
    out['last_result']={k:result.get(k) for k in ['case','sent','ready','actual_tps','fast_p50_ms','fast_p95_ms','fast_p99_ms','before_parent_commit','chain_edges','missing_inputs']}
if case:
    runtime=ROOT/'.run'/('e8-'+case)
    start=runtime/'reports/e8-start.json';timeline=runtime/'reports/e8-timeline.jsonl'
    if start.exists():out['formal_elapsed_s']=round((time.time_ns()-json.loads(start.read_text())['FormalNS'])/1e9,1)
    if timeline.exists():
        lines=timeline.read_text().splitlines()
        if lines:out['latest']=json.loads(lines[-1])
    out['runtime_exists']=runtime.exists()
print(json.dumps(out))
