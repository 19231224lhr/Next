"""Recompute paired reader costs; samples remain nested within three runs."""
import csv
import hashlib
import json
import math
from pathlib import Path
import statistics as st

root = Path(__file__).resolve().parent
def q(xs, p=.5):
    xs = sorted(xs)
    return xs[max(0, math.ceil(len(xs)*p)-1)]
def write_csv(name, rows):
    with (root/name).open('w', newline='', encoding='utf-8') as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0]))
        w.writeheader(); w.writerows(rows)

rows, costs, identities = [], [], []
for run in range(1,4):
    status=json.loads((root/f'results/run{run}.status.json').read_text())
    assert status['returncode']==0
    paths=sorted((root/f'results/run{run}').glob('*.json'))
    assert len(paths)==4
    for path in paths:
        d=json.loads(path.read_text()); n,m=d['Payments'],d['Repairs']
        assert (n,m) in [(32,1),(32,8),(256,1),(256,8)]
        assert d['Prefix']==4 and len(d['Checks'])==8 and len(d['Cases'])==6
        s=d['Storage']
        assert s['before_original_kv_bytes']==s['before_revised_kv_bytes']==s['after_original_kv_bytes']
        for kind in [10,11,22,31,50,81,82,83,84,100,101,102,103,105,106,107,117]:
            assert s.get(f'before_app_kind_{kind}',0)==s.get(f'after_app_kind_{kind}',0)
        for c in d['Cases']:
            samples=c['Samples']; assert len(samples)==300
            assert len(c['CryptoNS'])==len(c['InputCHNS'])==15
            for x in samples:
                assert x['ReadNS']>0 and x['ResolveNS']>0
                assert x['TotalNS']>=x['ReadNS']+x['ResolveNS']
            row=dict(run=run,payments=n,repairs=m,workload=c['Workload'],method=c['Method'])
            for stage in ['ReadNS','ResolveNS','TotalNS']:
                for p,label in [(.5,'p50'),(.95,'p95'),(.99,'p99')]:
                    row[f'{stage[:-2].lower()}_{label}_us']=q([x[stage] for x in samples],p)/1000
            for where in ['App','Block']:
                for k in ['Gets','Scans','Bytes']:
                    values={x[where][k] for x in samples}
                    assert len(values)==1
                    row[f'{where.lower()}_{k.lower()}']=values.pop()
            row.update(alloc_bytes=c['AllocBytes'],allocs=c['Allocs'],
                       auth_recheck_p50_us=q(c['CryptoNS'])/1000,
                       input_ch_recheck_p50_us=q(c['InputCHNS'])/1000)
            rows.append(row)
        cost=dict(run=run,payments=n,repairs=m)
        cost.update({k+'_ms':v/1e6 for k,v in d['TimingNS'].items()})
        cost['representation_work_ms']=sum(d['TimingNS'][k] for k in ['input_adapt','batch_build','parts_adapt','representation_finalize','representation_commit','materialization_prepare','materialization_install'])/1e6
        cost['common_original_history_kv_bytes']=s['before_original_kv_bytes']
        cost['permanent_decision_index_bytes']=s.get('before_app_kind_117',0)
        cost['decision_record_bytes']=s.get('before_app_kind_105',0)
        cost['revision_body_record_bytes']=s['after_app_kind_108']
        cost['batch_task_record_bytes']=s['after_app_kind_114']
        cost['representation_app_records_bytes']=sum(s.get(f'after_app_kind_{k}',0) for k in [108,110,113,114,115])
        cost['removed_pending_bytes']=s.get('before_app_kind_116',0)
        cost['blockstore_delta_bytes']=s['after_revised_kv_bytes']-s['before_revised_kv_bytes']
        cost['added_representation_kv_bytes']=cost['representation_app_records_bytes']+cost['blockstore_delta_bytes']-cost['removed_pending_bytes']
        cost['repair_command_bytes']=s['repair_command_bytes']
        cost['part_openings']=s['part_openings']
        cost['shared_test_directory_before_bytes']=s['before_directory_bytes']
        cost['shared_test_directory_after_bytes']=s['after_directory_bytes']
        costs.append(cost)
        for method,times in d['IdentityNS'].items():
            assert len(times)==15
            identities.append(dict(run=run,payments=n,repairs=m,method=method,complete_block_identity_p50_us=q(times)/1000))

write_csv('reads.csv',rows);write_csv('costs.csv',costs);write_csv('identity.csv',identities)
groups=[]
for n,m in [(32,1),(32,8),(256,1),(256,8)]:
    for work in ['point_repaired','point_normal','block']:
        g=dict(payments=n,repairs=m,workload=work)
        for method in ['index','canonical']:
            rs=[x for x in rows if (x['payments'],x['repairs'],x['workload'],x['method'])==(n,m,work,method)]
            assert len(rs)==3
            for col in ['total_p50_us','total_p95_us','read_p50_us','resolve_p50_us','alloc_bytes','auth_recheck_p50_us','input_ch_recheck_p50_us']:
                g[method+'_'+col]=st.median(x[col] for x in rs)
            g[method+'_total_p50_min_us']=min(x['total_p50_us'] for x in rs)
            g[method+'_total_p50_max_us']=max(x['total_p50_us'] for x in rs)
        g['median_reduction_percent']=100*(1-g['canonical_total_p50_us']/g['index_total_p50_us'])
        groups.append(g)
write_csv('summary.csv',groups)
manifest={str(p.relative_to(root)):{'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
          for p in sorted((root/'results').rglob('*')) if p.is_file()}
(root/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
(root/'analysis.json').write_text(json.dumps({'independent_runs':3,'configurations_per_run':4,'read_cases':len(rows),'timed_queries':len(rows)*300,'checks':'all passed','group_summaries':groups},indent=2)+'\n')
print('PASS: 12 fixtures, 72 reader cases, 21600 timed queries; all checks and statistical grouping verified')
for g in groups:
    print(g['payments'],g['repairs'],g['workload'],round(g['index_total_p50_us'],2),round(g['canonical_total_p50_us'],2),round(g['median_reduction_percent'],2))
