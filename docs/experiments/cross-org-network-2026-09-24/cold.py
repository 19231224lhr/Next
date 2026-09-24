#!/usr/bin/env python3
"""Fresh-process first payment vs subsequent payments; not pure crypto cache cost."""
import json
from reproduce import OUT,run
from analyze import read,quantiles

rows=[]
for repeat in range(10):
    label=f'cold-r{repeat+1}'
    if not (OUT/label/'passed.json').exists():
        run(label,'B',4,2,0,23,0,65000,lanes=2)
    for site in range(2):
        r=read(OUT/label/'reports'/f'e7-formal-{site}.json')
        ss=sorted((s for s in r['Samples'] if s['ReadyNS']),key=lambda s:s['SentElapsedNS'])
        assert len(ss)>=2
        rows.append(dict(repeat=repeat+1,site=site,first=ss[0]['Monotonic'],following=[s['Monotonic'] for s in ss[1:]]))
result=dict(scope='Fresh processes and connections, preconfigured trust; genesis may populate caches. Not isolated cryptographic cache cost.',rows=rows,first_fast_ms=quantiles([x['first']['FastNS'] for x in rows]),following_fast_ms=quantiles([s['FastNS'] for x in rows for s in x['following']]))
(OUT/'cold-summary.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True)
