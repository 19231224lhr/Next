"""Non-overlapping local-clock spans from opt-in chain traces."""
import argparse, collections, json, pathlib, statistics
p=argparse.ArgumentParser();p.add_argument('report',type=pathlib.Path);a=p.parse_args()
r=json.loads(a.report.read_text()); d=collections.defaultdict(list)
member=[('http_handler_enter','request_decoded'),('request_decoded','vector_prepared'),('vector_prepared','parents_verified'),('parents_verified','validation_complete'),('validation_complete','commit_requested'),('commit_requested','update_started'),('update_started','update_evaluated'),('update_evaluated','commit_returned'),('commit_returned','vote_signed'),('vote_signed','response_ready')]
gateway=[('http_handler_enter','request_decoded'),('collect_enter','owner_verified'),('fanout_ready','quorum_collected'),('quorum_collected','certificate_verified'),('certificate_verified','response_ready')]
for h in r['Hops']:
    nodes=collections.defaultdict(dict)
    for x in h.get('Foreground',[]): nodes[x['node']][x['stage']]=x['local_ns']
    for node,s in nodes.items():
        role='gateway' if node=='gateway' else 'member'
        if role=='member' and 'update_started' not in s: continue
        for lo,hi in gateway if role=='gateway' else member:
            if lo in s and hi in s and s[hi]>=s[lo]: d[role+':'+lo+'->'+hi].append((s[hi]-s[lo])/1e6)
def summary(v):
    v=sorted(v); i=.95*(len(v)-1); lo=int(i); p95=v[lo]+(v[min(lo+1,len(v)-1)]-v[lo])*(i-lo)
    return {'n':len(v),'p50_ms':statistics.median(v),'p95_ms':p95,'mean_ms':statistics.mean(v)}
print(json.dumps({'chain_ms':r['FastChainMS'],'spans':{k:summary(v) for k,v in d.items()},'build99_sum_ms':{k:sum(h.get('BuildStagesMS',{}).get(k,0) for h in r['Hops'][1:]) for k in r['Hops'][1].get('BuildStagesMS',{})}},indent=2))
