"""A/B actual applyPayment via Go source overlay; never edits the checkout."""
import argparse, hashlib, json, os, pathlib, platform, subprocess, time

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--repo',type=pathlib.Path,default=pathlib.Path(__file__).resolve().parents[3])
p.add_argument('--out',type=pathlib.Path,required=True)
p.add_argument('--repeats',type=int,default=5)
p.add_argument('--gomaxprocs',type=int,default=1)
p.add_argument('--benchtime',default='1s')
p.add_argument('--prepare-only',action='store_true')
p.add_argument('--before-source-dir',type=pathlib.Path,help='optional retained blocks.go.txt/c3.go.txt snapshot instead of HEAD')
a=p.parse_args()
if a.repeats<1 or a.gomaxprocs<1:p.error('positive repeats and GOMAXPROCS required')
repo=a.repo.resolve();out=a.out.resolve();out.mkdir(parents=True,exist_ok=False)
def git(*args):return subprocess.check_output(['git',*args],cwd=repo)
head=git('rev-parse','HEAD').decode().strip()
replacement={};sources={}
for name in ['internal/member/blocks.go','internal/member/c3.go']:
    before=(a.before_source_dir/(pathlib.Path(name).name+'.txt')).read_bytes() if a.before_source_dir else git('show',f'{head}:{name}')
    target=out/(pathlib.Path(name).name+'.txt');target.write_bytes(before)
    source=(repo/name).resolve();replacement[str(source)]=str(target)
    sources[name]=dict(before_sha256=hashlib.sha256(before).hexdigest(),after_sha256=hashlib.sha256(source.read_bytes()).hexdigest())
bench=repo/'internal/member/blocks_approval_reuse_test.go'
# The actual-lookup metadata unit test calls a new private helper signature.
# Remove this non-running unit test from BOTH compiled arms; keep benchmark and
# all other test code identical so older source snapshots still compile.
benchmark_source=bench.read_bytes().split(b'\nfunc TestC3ApprovalReuseReturnsActualLookupFact',1)[0]
common_test=out/'blocks_approval_reuse_test.go.txt';common_test.write_bytes(benchmark_source)
replacement[str(bench.resolve())]=str(common_test)
overlay=out/'overlay.json';overlay.write_text(json.dumps({'Replace':replacement},indent=2)+'\n')
current_overlay=out/'current-overlay.json';current_overlay.write_text(json.dumps({'Replace':{str(bench.resolve()):str(common_test)}},indent=2)+'\n')
env=dict(os.environ,GOMAXPROCS=str(a.gomaxprocs))
common=['go','test','./internal/member','-run','^$','-bench','^BenchmarkC3ApplyPaymentSelfApproval$','-benchmem','-count=1',f'-benchtime={a.benchtime}']
commands={'before':common+['-overlay',str(overlay)],'after':common+['-overlay',str(current_overlay)]}
manifest=dict(head=head,source_overrides=sources,benchmark_sha256=hashlib.sha256(bench.read_bytes()).hexdigest(),
              script_sha256=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
              platform=platform.platform(),gomaxprocs=a.gomaxprocs,affinity=sorted(os.sched_getaffinity(0)) if hasattr(os,'sched_getaffinity') else None,
              compiled_benchmark_sha256=hashlib.sha256(benchmark_source).hexdigest(),
              before_source_dir=str(a.before_source_dir) if a.before_source_dir else None,
              commands=commands,repeats=a.repeats,prepare_only=a.prepare_only,
              scope='CPU time and allocations for actual applyPayment against unchanged in-memory approved snapshot; no persistence, network or end-to-end claim.')
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
if not a.prepare_only:
    with (out/'status.jsonl').open('x') as status:
        for repeat in range(a.repeats):
            for arm in (['before','after'] if repeat%2==0 else ['after','before']):
                started=time.time_ns()
                with (out/f'r{repeat}-{arm}.log').open('x') as log:
                    result=subprocess.run(commands[arm],cwd=repo,env=env,stdout=log,stderr=subprocess.STDOUT)
                status.write(json.dumps(dict(repeat=repeat,arm=arm,exit=result.returncode,start_ns=started,end_ns=time.time_ns()))+'\n');status.flush()
                if result.returncode:raise SystemExit(result.returncode)
