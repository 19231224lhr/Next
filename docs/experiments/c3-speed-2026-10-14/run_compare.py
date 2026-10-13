"""Sequential interleaved frozen C3 / optimized C3 / FastPay comparisons."""
import argparse, hashlib, json, os, pathlib, subprocess, sys, time
p=argparse.ArgumentParser();p.add_argument('--root',type=pathlib.Path,required=True);p.add_argument('--before',type=pathlib.Path,required=True);p.add_argument('--after',type=pathlib.Path,required=True);p.add_argument('--fastpay',type=pathlib.Path,required=True);p.add_argument('--repeats',type=int,default=5);p.add_argument('--suite',choices=['chain','mixed'],default='chain')
p.add_argument('--only-next',action='store_true');p.add_argument('--trace',action='store_true')
p.add_argument('--length',type=int,default=100);p.add_argument('--rate',type=int,default=100);p.add_argument('--duration',type=int,default=30)
p.add_argument('--fixture',type=pathlib.Path,help='fixed pre-service fixture; otherwise create a fresh shared fixture per pair')
p.add_argument('--before-gomaxprocs',type=int,default=4)
p.add_argument('--after-gomaxprocs',type=int,default=4)
p.add_argument('--fastpay-runtime-threads',type=int,help='runtime-only sensitivity binary; absent retains official constructor')
a=p.parse_args()
if min(a.before_gomaxprocs,a.after_gomaxprocs)<1: p.error('GOMAXPROCS must be positive')
if a.fastpay_runtime_threads is not None and a.fastpay_runtime_threads<1:p.error('FastPay runtime threads must be positive')
if not 1 <= a.length <= 1000: p.error('length must be 1..1000')
here=pathlib.Path(__file__).resolve().parent
if a.root.exists(): raise FileExistsError(a.root)
a.root.mkdir(parents=True)
orders=[['before','after','fastpay'],['fastpay','after','before'],['before','fastpay','after'],['after','before','fastpay'],['fastpay','before','after']]
jobs=[{'repeat':i,'arm':arm} for i in range(a.repeats) for arm in orders[i%len(orders)] if (a.suite=='chain' and not a.only_next) or arm!='fastpay']
env=dict(os.environ,GOMAXPROCS='4')
env.pop('FASTPAY_RUNTIME_THREADS',None)
for key in ['UTXO_CHAIN_CPU_PROFILE','UTXO_CHAIN_BUILD_PROFILE','UTXO_CHAIN_CPU_FOREGROUND_ONLY','UTXO_SETTLEMENT_TRACE']:env.pop(key,None)
manifest={'jobs':jobs,'created_ns':time.time_ns(),'affinity':sorted(os.sched_getaffinity(0)),'arguments':{k:str(v) for k,v in vars(a).items()},'paired_fixture':True,'harness_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'binaries':{arm:{n:hashlib.sha256((b/n).read_bytes()).hexdigest() for n in ['payctl','member','gateway','committee']} for arm,b in [('before',a.before),('after',a.after)]},'fastpay_binaries':{n:hashlib.sha256((a.fastpay/n).read_bytes()).hexdigest() for n in ['examples/baseline_client','server']}}
manifest['service_variants']={arm:json.loads((b/'variant.json').read_text()) for arm,b in [('before',a.before),('after',a.after)] if (b/'variant.json').exists()}
manifest['gomaxprocs']={'before':a.before_gomaxprocs,'after':a.after_gomaxprocs,'fastpay':'not a Go program'}
manifest['fastpay_runtime_threads']=a.fastpay_runtime_threads
(a.root/'run_compare.py').write_bytes(pathlib.Path(__file__).read_bytes())
(a.root/'plan.json').write_text(json.dumps(manifest,indent=2))
with (a.root/'status.jsonl').open('x') as status:
    for job in jobs:
        arm=job['arm'];name=f"r{job['repeat']}-{arm}";directory=a.root/name
        if arm=='fastpay':
            command=[sys.executable,str(here.parent/'baseline-design-2026-10-13/run_fastpay.py'),'--bin',str(a.fastpay),'--run-dir',str(directory),'--workload','chain','--length',str(a.length),'--count',str(a.length),'--protocol','Tcp','--client-mode','receiver-confirmed']
        else:
            command=[sys.executable,str(here.parent/'c3-issuer-funded-2026-10-13/run_c3.py'),'--bin',str(a.before if arm=='before' else a.after),'--run-dir',str(directory),'--variant','c3','--workload',a.suite]
            command.extend(['--length',str(a.length),'--rate',str(a.rate),'--duration',str(a.duration)])
            fixture=a.fixture or a.root/'fixtures'/f"r{job['repeat']}"
            command.extend(['--fixture' if fixture.exists() else '--save-fixture',str(fixture)])
            if a.trace:command.append('--trace')
        print('START '+name,flush=True);start=time.time_ns()
        runenv=dict(env,GOMAXPROCS=str(a.before_gomaxprocs if arm=='before' else a.after_gomaxprocs))
        if arm=='fastpay' and a.fastpay_runtime_threads is not None:runenv['FASTPAY_RUNTIME_THREADS']=str(a.fastpay_runtime_threads)
        with (a.root/(name+'.log')).open('x') as log:
            proc=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,env=runenv)
        item={**job,'start_ns':start,'end_ns':time.time_ns(),'exit':proc.returncode,'command':command}
        status.write(json.dumps(item)+'\n');status.flush();print('END '+json.dumps(item),flush=True)
        if proc.returncode:sys.exit(proc.returncode)
