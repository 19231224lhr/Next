"""Recompute existing 721800c intervals; no inferred wallet timestamps."""
import csv,gzip,json,math
from pathlib import Path
from statistics import median

HERE=Path(__file__).resolve().parent
OLD=HERE.parent/'final-continuation-2026-10-02/results'
def read(path):
    return json.loads(path.read_text() if path.exists() else gzip.decompress(path.with_suffix(path.suffix+'.gz').read_bytes()))
def stats(values):
    v=sorted(values);return dict(n=len(v),p50_ms=median(v),p95_ms=v[math.ceil(.95*len(v))-1],sum_ms=sum(v))

cases=[];rows=[]
for folder in sorted(OLD.glob('r[123]-*')):
    report=read(folder/'chain-v4.json');events=read(folder/'settlements.json');local=[]
    for i,h in enumerate(report['Hops']):
        fact=bytes(h['Fact']).hex()
        commits={node:min(e['CommittedUnixNS'] for e in es[fact] if e['CommittedUnixNS']>0) for node,es in events.items()}
        earliest=min(commits.values());selected=commits['0']
        assert earliest<=selected<=h['FinalUnixNS']
        r=dict(case=folder.name,hop=i+1,mode=report['Mode'],earliest_commit_to_observation_ms=(h['FinalUnixNS']-earliest)/1e6,selected_commit_to_observation_ms=(h['FinalUnixNS']-selected)/1e6,selected_replica_lag_ms=(selected-earliest)/1e6)
        if report['Mode']=='wait_final' and i<99:
            r.update(ready_to_observation_ms=(h['FinalUnixNS']-h['ReadyUnixNS'])/1e6,
                     blocking_before_earliest_commit_ms=max(0,earliest-h['ReadyUnixNS'])/1e6,
                     blocking_after_earliest_commit_ms=(h['FinalUnixNS']-max(earliest,h['ReadyUnixNS']))/1e6,
                     observation_to_next_build_ms=(report['Hops'][i+1]['BuildStartUnixNS']-h['FinalUnixNS'])/1e6)
            assert r['observation_to_next_build_ms']>=0
        local.append(r);rows.append(r)
    keys=['earliest_commit_to_observation_ms','selected_commit_to_observation_ms','selected_replica_lag_ms']
    if report['Mode']=='wait_final':keys+=['ready_to_observation_ms','blocking_before_earliest_commit_ms','blocking_after_earliest_commit_ms','observation_to_next_build_ms']
    cases.append(dict(case=folder.name,metrics={k:stats([r[k] for r in local if k in r]) for k in keys}))
write=dict(build='721800c',wallet_internal_finality_timestamp_available=False,follower_poll_ms=25,driver_finality_poll_ms=5,retry_pause_ms=25,commit_selection='earliest of four and configured follower replica 0 reported separately',scope='Single-host, zero injected delay. Post-commit includes next-header availability, fetching, verification, local recording and observation; not pure polling.',cases=cases)
(HERE/'waiting-analysis.json').write_text(json.dumps(write,indent=2))
with (HERE/'waiting-hops.csv').open('w',newline='') as f:
    w=csv.DictWriter(f,list(dict.fromkeys(k for r in rows for k in r)));w.writeheader();w.writerows(rows)
for c in cases:print(c['case'],c['metrics'])
