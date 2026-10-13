"""CPU samples from live services; diagnostic only, never used as headline timing."""
import argparse, concurrent.futures, json, os, pathlib, subprocess, sys, time, urllib.request

p=argparse.ArgumentParser()
p.add_argument('--root',type=pathlib.Path,required=True)
p.add_argument('--bin',type=pathlib.Path,required=True)
p.add_argument('--runtime-trace',action='store_true')
p.add_argument('--fixture',type=pathlib.Path)
p.add_argument('--gomaxprocs',type=int,default=4)
a=p.parse_args()
if a.gomaxprocs < 1:p.error('gomaxprocs must be positive')
a.root.mkdir(parents=True,exist_ok=False)
runner=pathlib.Path(__file__).resolve().parent.parent/'c3-issuer-funded-2026-10-13'/'run_c3.py'
run=a.root/'mixed'
env=dict(os.environ,GOMAXPROCS=str(a.gomaxprocs))
(a.root/'configuration.json').write_text(json.dumps({'gomaxprocs':a.gomaxprocs,'affinity':sorted(os.sched_getaffinity(0)),'binary_directory':str(a.bin),'diagnostic_only':True},indent=2))
with (a.root/'runner.log').open('w') as log:
    command=[sys.executable,str(runner),'--bin',str(a.bin),'--run-dir',str(run),'--variant','c3','--workload','mixed','--duration','30','--rate','100']
    if a.fixture:command.extend(['--fixture' if a.fixture.exists() else '--save-fixture',str(a.fixture)])
    process=subprocess.Popen(command,stdout=log,stderr=subprocess.STDOUT,env=env)
    deadline=time.monotonic()+90
    while not (run/'reports'/'e8-start.json').exists():
        if process.poll() is not None or time.monotonic()>deadline:
            raise RuntimeError('runner did not reach workload; see runner.log')
        time.sleep(.1)
    nodes=json.loads((run/'lab.json').read_text())['Nodes']
    selected=[n for n in nodes if n['Name'] in ('gateway0','org0-member0','committee0')]
    if not selected:
        selected=[next(n for n in nodes if n['Binary']==role) for role in ('gateway','member','committee')]
    def capture(n):
        opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
        url=n['URL'].rstrip('/')+'/debug/pprof/profile?seconds=10'
        try:
            with opener.open(url,timeout=14) as response:
                (a.root/(n['Name']+'.pprof')).write_bytes(response.read())
            return {'node':n['Name'],'url':url,'ok':True}
        except Exception as e:
            return {'node':n['Name'],'url':url,'error':str(e)}
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        records=list(pool.map(capture,selected))
    if a.runtime_trace:
        def trace(n):
            url=n['URL'].rstrip('/')+'/debug/pprof/trace?seconds=3'
            try:
                with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(url,timeout=8) as response:
                    (a.root/(n['Name']+'.trace')).write_bytes(response.read())
                return {'node':n['Name'],'url':url,'ok':True,'type':'runtime-trace'}
            except Exception as e:
                return {'node':n['Name'],'url':url,'error':str(e),'type':'runtime-trace'}
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            records.extend(pool.map(trace,selected))
    (a.root/'profiles.json').write_text(json.dumps(records,indent=2))
    code=process.wait()
    print(json.dumps({'exit':code,'profiles':records}))
    sys.exit(code)
