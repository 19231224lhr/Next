#!/usr/bin/env python3
"""Fresh-state repetitions, sequential on one machine, with explicit node shutdown."""
import argparse
import subprocess
from pathlib import Path
from run import trial

ROOT=Path(__file__).resolve().parents[2]
def main():
    p=argparse.ArgumentParser()
    p.add_argument('--topology',choices=['direct','routed','chain'],default='direct')
    p.add_argument('--rate',type=float,default=5)
    p.add_argument('--repeats',type=int,default=3)
    p.add_argument('--latency-only',action='store_true')
    args=p.parse_args()
    for repeat in range(1,args.repeats+1):
        name=f'{args.topology}-latency-r{repeat}' if args.latency_only else f'{args.topology}-{args.rate:g}-r{repeat}'
        lab=['python3','tools/lnbench/lab.py']
        setup=lab+['start',name]
        if args.topology=='routed':setup+=['--routed']
        if args.topology=='chain':setup+=['--capacity','1000000','--push','20000']
        try:
            subprocess.run(setup,cwd=ROOT,check=True)
            if args.topology=='chain':
                trial(name,'chain100',['-chain','-amount','950000','-count','100'])
            else:
                trial(name,'warmup',['-count','20'])
                trial(name,'serial100',['-count','100'])
                if not args.latency_only:
                    trial(name,'sustained',['-rate',str(args.rate),'-duration','180s'])
        finally:
            if (ROOT/'.run'/('ln-'+name)/'manifest.json').exists():
                subprocess.run(lab+['stop',name],cwd=ROOT,check=True,stdout=subprocess.DEVNULL)
        print('FINISHED',name,flush=True)

if __name__=='__main__':main()
