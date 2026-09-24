#!/usr/bin/env python3
"""Fixed paired E7 matrix. Fail fast and keep evidence; never overwrite a run."""
import json
from reproduce import OUT,run
from analyze import summarize

cases=[('A',0),('B',0),('C',0),('D',0),('C',20),('D',20),('C',100),('D',100)]
for repeat in range(3):
    order=cases[repeat*3:]+cases[:repeat*3]
    for mode,rtt in order:
        label=f'formal-r{repeat+1}-{mode.lower()}-{rtt}'
        if (OUT/label/'passed.json').exists():continue
        run(label,mode,100,300 if mode=='D' and rtt==100 else 120,rtt,23+repeat)
        result=summarize(OUT/label)
        (OUT/label/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
        print(json.dumps(result),flush=True)
