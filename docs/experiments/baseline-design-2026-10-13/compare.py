#!/usr/bin/env python3
"""Alternate real Next/FastPay runs on the same Linux host; keep all run data."""
import argparse
import json
import math
import os
import pathlib
import platform
import signal
import statistics
import subprocess
import sys
import time

def interrupted(signum, _frame):
    raise InterruptedError(f'comparison interrupted by signal {signum}')


def run_managed(command, timeout):
    process = subprocess.Popen(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=timeout)
        return subprocess.CompletedProcess(command, process.returncode, stdout, stderr)
    except BaseException:
        # Both adapters trap SIGTERM and clean up their actual service children.
        process.terminate()
        try:
            process.communicate(timeout=45)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.communicate()
        raise


def verify(report, system, workload, count):
    values = report.get('samples_ms', [])
    if report.get('system') != system or report.get('workload') != workload or report.get('completed') != count or len(values) != count:
        raise ValueError('system/workload/sample count mismatch')
    if any(not isinstance(v, (int,float)) or isinstance(v,bool) or not math.isfinite(v) or v <= 0 for v in values):
        raise ValueError('invalid native timing samples')
    if workload == 'chain' and (not math.isfinite(report.get('chain_total_ms',0)) or report.get('chain_total_ms',0) <= 0):
        raise ValueError('missing chain endpoint')
    if system == 'FastPay' and (not report.get('probe_completed') or not report.get('principal_conserved')):
        raise ValueError('FastPay receiver continuation/conservation not verified')


def summarize_run(report):
    values = sorted(report['samples_ms'])
    return dict(p50_ms=statistics.median(values), p95_ms=values[math.ceil(.95*len(values))-1],
                chain_total_ms=report.get('chain_total_ms'), completed=report['completed'],
                actual_send_rate=report.get('realized_send_rate_per_s'), receipt_goodput=report.get('receipt_goodput_per_s'),
                closure_status=report.get('closure_status'))


def run(args):
    if sys.platform != 'linux': raise RuntimeError('same-host comparison must run under Linux')
    cpus = sorted(os.sched_getaffinity(0))[:args.cpus]
    if len(cpus)!=args.cpus or args.rounds<1 or args.count<1: raise ValueError('invalid CPU/round/count allocation')
    os.sched_setaffinity(0, cpus)
    os.environ['GOMAXPROCS']=str(args.cpus)
    root=args.out.resolve(); root.mkdir(parents=True,exist_ok=False)
    here=pathlib.Path(__file__).resolve().parent
    environment={'platform':platform.platform(),'python':platform.python_version(),'cpu_affinity':cpus,
        'gomaxprocs':args.cpus,'next_source':'f6d0387 + benchmark payer policy-grant filter',
        'fastpay_source':'db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2','fastpay_transport':args.protocol,'fastpay_client_mode':args.client_mode,
        'rounds':args.rounds,'independent_count':args.count,'next_storage':args.storage,
        'memory_total_kb':int(pathlib.Path('/proc/meminfo').read_text().splitlines()[0].split()[1]),
        'clock':'per-system native monotonic timers; no CLI/server startup in samples'}
    (root/'environment.json').write_text(json.dumps(environment,indent=2))
    cases=[] if args.rates_only else [('chain',n,0) for n in args.lengths]
    if not args.chains_only and not args.rates_only: cases += [('independent',args.count,0)]
    cases += [('independent',args.count,rate) for rate in args.rates]
    records=[]
    for round_no in range(args.rounds):
        for workload,n,rate in cases:
            for system in (('Next','FastPay') if round_no%2==0 else ('FastPay','Next')):
                label=f'r{round_no}-{workload}{n}-rate{rate:g}-{system.lower()}'
                data_dir=root/label
                script='run_next.py' if system=='Next' else 'run_fastpay.py'
                binary=args.next_bin if system=='Next' else args.fastpay_bin
                command=[sys.executable,str(here/script),'--bin',str(binary),'--run-dir',str(data_dir),
                    '--workload',workload,'--length',str(n if workload=='chain' else 1),'--count',str(n),'--rate',str(rate)]
                command += ['--storage',args.storage] if system=='Next' else ['--protocol',args.protocol,'--client-mode',args.client_mode]
                (root/f'{label}-command.json').write_text(json.dumps(command))
                result=run_managed(command,timeout=600)
                (root/f'{label}-stdout.txt').write_text(result.stdout)
                (root/f'{label}-stderr.txt').write_text(result.stderr)
                if result.returncode: raise RuntimeError(f'{label} failed: '+result.stderr[-1500:])
                report=json.loads(result.stdout);verify(report,system,workload,n)
                (root/f'{label}-result.json').write_text(json.dumps(report,indent=2))
                row=dict(round=round_no,system=system,workload=workload,n=n,target_rate=rate,**summarize_run(report))
                records.append(row)
                (root/'records.json').write_text(json.dumps(records,indent=2))
                print(json.dumps(row),flush=True)
    comparisons=[]
    for workload,n,rate in cases:
        row={'workload':workload,'n':n,'target_rate':rate}
        for system in ('Next','FastPay'):
            runs=[r for r in records if r['system']==system and r['workload']==workload and r['n']==n and r['target_rate']==rate]
            row[system]={key:statistics.median(x[key] for x in runs) for key in ('p50_ms','p95_ms')}
            for key in ('actual_send_rate','receipt_goodput'):
                available=[x[key] for x in runs if x[key] is not None]
                if available: row[system][key]=statistics.median(available)
            if workload=='chain': row[system]['chain_total_ms']=statistics.median(x['chain_total_ms'] for x in runs)
        row['next_to_fastpay_p50_ratio']=row['Next']['p50_ms']/row['FastPay']['p50_ms']
        comparisons.append(row)
    (root/'summary.json').write_text(json.dumps({'environment':environment,'comparisons':comparisons,'records':records},indent=2))
    print(json.dumps(comparisons,indent=2))


if __name__=='__main__':
    signal.signal(signal.SIGTERM, interrupted)
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--next-bin',required=True,type=pathlib.Path)
    p.add_argument('--fastpay-bin',required=True,type=pathlib.Path)
    p.add_argument('--out',required=True,type=pathlib.Path)
    p.add_argument('--rounds',type=int,default=5)
    p.add_argument('--count',type=int,default=100)
    p.add_argument('--lengths',type=int,nargs='*',default=[1,10,100])
    p.add_argument('--rates',type=float,nargs='*',default=[])
    p.add_argument('--cpus',type=int,default=4)
    p.add_argument('--storage',choices=('memory','sync'),default='memory')
    p.add_argument('--protocol',choices=('Tcp','Udp'),default='Tcp')
    p.add_argument('--client-mode',choices=('normal','receiver-confirmed'),default='normal')
    p.add_argument('--chains-only',action='store_true')
    p.add_argument('--rates-only',action='store_true')
    run(p.parse_args())
