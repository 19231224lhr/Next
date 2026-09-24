#!/usr/bin/env python3
"""Frozen E8 matrix; one trial at a time on the same host."""
import json, time
import reproduce as lab
from analyze import analyze

cases=[]
for repeat,order in enumerate(['ABC','BCA','CAB'],1):
    for mode in order:cases.append((f'formal-{mode.lower()}{repeat}',mode,300,False,23+repeat*101))
for repeat in range(1,4):cases.append((f'burst-c{repeat}','C',195,True,23+repeat*101))

if __name__=='__main__':
    for label,mode,duration,surge,seed in cases:
        dest=lab.OUT/label
        if (dest/'passed.json').exists():
            print(json.dumps({'resume_skip':label}),flush=True)
            analyze(dest)
            continue
        if dest.exists():raise RuntimeError(f'{label}: inspect incomplete evidence before retry')
        print(json.dumps({'start':label,'time':time.time()}),flush=True)
        lab.run(label,mode,500,duration,seed=seed,warm=2048,surge=surge)
        analyze(dest)
    lab.write(lab.OUT/'suite-complete.json',{'cases':[x[0] for x in cases],'completed_at':time.time()})
