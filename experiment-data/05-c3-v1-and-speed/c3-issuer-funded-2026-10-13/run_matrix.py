#!/usr/bin/env python3
"""Sequential, predeclared C3 measurements. Never overwrite or discard a run."""
import argparse
import json
import os
import pathlib
import signal
import subprocess
import sys
import time

HERE = pathlib.Path(__file__).resolve().parent


def matrix(suite, repeats, rate, duration, delays):
    jobs = []
    if suite == 'orders':
        for repeat in range(1, repeats+1):
            for case in ('source-first','child-first','compensated'):
                jobs.append({'name':f'orders-r{repeat}-{case}', 'repeat':repeat, 'variant':'c3', 'workload':'fees',
                             'extra':['--case',case,'--count','1'], 'fixture_key':f'r{repeat}', 'first':case == 'source-first'})
    elif suite == 'delays':
        for repeat in range(1, repeats+1):
            # Rotate the condition order across rounds without selecting results.
            shift = (repeat-1) % len(delays)
            for index,delay in enumerate(delays[shift:]+delays[:shift]):
                jobs.append({'name':f'delays-r{repeat}-{delay}ms', 'repeat':repeat, 'variant':'c3','workload':'fees',
                             'extra':['--case','delay','--count','20','--delay-ms',str(delay)],
                             'fixture_key':f'r{repeat}','first':index==0})
    else:
        workload = 'mixed' if suite == 'mixed' else 'chain'
        for repeat in range(1,repeats+1):
            variants = ('baseline','c3') if repeat % 2 else ('c3','baseline')
            for variant in variants:
                extra = ['--trace'] if suite == 'diagnostic' else []
                if workload == 'mixed':
                    extra += ['--rate',str(rate),'--duration',str(duration)]
                jobs.append({'name':f'{suite}-r{repeat}-{variant}', 'repeat':repeat, 'variant':variant,
                             'workload':workload, 'extra':extra,'fixture_key':variant,'first':repeat==1})
    return jobs


def main():
    def terminated(_signal, _frame):
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        raise InterruptedError('matrix terminated')
    signal.signal(signal.SIGTERM, terminated)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root',type=pathlib.Path,required=True)
    p.add_argument('--baseline-bin',type=pathlib.Path,required=True)
    p.add_argument('--current-bin',type=pathlib.Path,required=True)
    p.add_argument('--suite',choices=('orders','delays','chain','mixed','diagnostic'),required=True)
    p.add_argument('--repeats',type=int,required=True)
    p.add_argument('--rate',type=int,default=100)
    p.add_argument('--duration',type=int,default=30)
    p.add_argument('--delays',default='0,100,500,1500,4000')
    args=p.parse_args()
    if args.repeats < 1:
        p.error('repeats must be positive')
    root=args.root.resolve()
    root.mkdir(parents=True,exist_ok=True)
    plan=root/(args.suite+'-plan.json')
    if plan.exists():
        raise FileExistsError('use a fresh suite root; existing measurements are preserved')
    jobs=matrix(args.suite,args.repeats,args.rate,args.duration,[int(d) for d in args.delays.split(',')])
    plan.write_text(json.dumps({'jobs':jobs,'created_unix_ns':time.time_ns(),'affinity':sorted(os.sched_getaffinity(0))},indent=2)+'\n')
    with (root/(args.suite+'-status.jsonl')).open('x',encoding='utf-8') as status:
        for job in jobs:
            binary=args.baseline_bin if job['variant']=='baseline' else args.current_bin
            command=[sys.executable,str(HERE/'run_c3.py'),'--bin',str(binary),'--run-dir',str(root/job['name']),
                     '--variant',job['variant'],'--workload',job['workload'],*job['extra']]
            if 'fixture_key' in job:
                fixture=root/f"fixture-{args.suite}-{job['fixture_key']}"
                command += ['--save-fixture' if job['first'] else '--fixture',str(fixture)]
            start=time.time_ns()
            print('START '+job['name'],flush=True)
            with (root/(job['name']+'-runner.log')).open('x',encoding='utf-8') as log:
                process=subprocess.Popen(command,stdout=log,stderr=subprocess.STDOUT)
                try:
                    process.wait()
                except BaseException:
                    process.terminate()
                    try:
                        process.wait(timeout=50)
                    except subprocess.TimeoutExpired:
                        process.kill(); process.wait()
                    raise
            outcome={'name':job['name'],'repeat':job['repeat'],'returncode':process.returncode,'start_ns':start,'end_ns':time.time_ns()}
            status.write(json.dumps(outcome)+'\n'); status.flush()
            print('END '+json.dumps(outcome),flush=True)
            # Preserve failure; abort rather than silently spending a whole matrix
            # on a broken harness or treating missing results as successes.
            if process.returncode:
                raise SystemExit(process.returncode)


if __name__=='__main__':
    main()
