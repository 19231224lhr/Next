import json, sys, urllib.request
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor

labdir=Path(sys.argv[1])
lab=json.loads((labdir/'lab.json').read_text())
out=labdir/'reports'/'trace100'
out.mkdir(exist_ok=True)
def get(url):
    with urllib.request.urlopen(url, timeout=20) as r: return json.load(r)
report=json.loads((labdir/'reports'/'bench-v4-0.json').read_text())
jobs=[]
for n in lab['Nodes']:
    suffix='/debug/consensus' if n['Binary']=='committee' else '/debug/timeline'
    data=get(n['URL']+suffix)
    (out/(n['Name']+'-timeline.json')).write_text(json.dumps(data,indent=2))
    if n['Binary']=='committee':
        for s in report['Samples']:
            if s['Fact']: jobs.append((n['Name'],s['Fact'], n['URL']+'/debug/settlement/'+s['Fact']))
def collect(job):
    node,fact,url=job
    return node,fact,get(url)
results={}
with ThreadPoolExecutor(max_workers=4) as pool:
    for node,fact,data in pool.map(collect,jobs): results.setdefault(node,{})[fact]=data
(out/'settlement.json').write_text(json.dumps(results,indent=2))
(out/'benchmark.json').write_text(json.dumps(report,indent=2))
print('Captured', len(jobs),'settlement lookups; node timelines:',len(lab['Nodes']))
