"""Run on Mac from the repository root; isolated genesis and ports, no tuning changes."""
import concurrent.futures, csv, json, os, pathlib, signal, subprocess, sys, time, urllib.request

root = pathlib.Path.cwd()
label = sys.argv[1]
variant=sys.argv[2]
count=int(sys.argv[3]) if len(sys.argv)>3 else 16
trace=False
lab = root / 'experiments' / label
bins = root / '.scratch/backend-reduction-bin' / variant
env = dict(os.environ, UTXO_EXPERIMENT_FLUSH='10ms', UTXO_EXPERIMENT_GOSSIP='10ms')
env.pop('UTXO_SETTLEMENT_TRACE', None)
if trace: env['UTXO_SETTLEMENT_TRACE'] = '1'
def fetch(port, path):
    with urllib.request.urlopen(f'http://127.0.0.1:{port}{path}', timeout=8) as response:
        return response.read()
subprocess.run([str(bins/'payctl'), 'init-lab', '-dir', str(lab), '-port', '24000', '-outputs', '64'], check=True)
log = (lab/'supervisor.log').open('w')
proc = subprocess.Popen([str(bins/'payctl'), 'lab-run', '-dir', str(lab), '-bin', str(bins)], env=env, stdout=log, stderr=log)
try:
    for _ in range(150):
        if proc.poll() is not None: raise RuntimeError('lab exited')
        try:
            for port in [24000,24007,24100,24101,24102,24103,24300,24301]: fetch(port, '/healthz')
            if int(json.loads(fetch(24100, '/healthz'))['height']) >= 1: break
        except OSError: pass
        time.sleep(.2)
    else: raise RuntimeError('lab not ready')
    time.sleep(1)
    started = time.time()
    with (lab/'bench.log').open('w') as output:
        bench = subprocess.Popen([str(bins/'bench'), '-dir', str(lab), '-duration', '30s', '-drain', '90s', '-lanes', '8', '-transactions-per-lane', str(count)], env=env, stdout=output, stderr=output)
        tick = 0
        while bench.poll() is None:
            if time.time()-started > 150: bench.kill(); raise RuntimeError('bench exceeded deadline')
            if trace:
                for name,port in [('committee',24100),('member',24000),('gateway',24300)]:
                    (lab/'reports'/f'{name}-stack-{tick}.txt').write_bytes(fetch(port, '/debug/pprof/goroutine?debug=1'))
            with (lab/'reports/cpu.txt').open('a') as output:
                output.write(f'ELAPSED {time.time()-started:.3f}\n')
                output.write(subprocess.check_output(['ps','-axo','pid=,%cpu=,time=,command='],text=True))
            tick += 1
            time.sleep(2)
    reports = sorted((lab/'reports').glob('bench-*.json'))
    print(reports[-1].read_text() if reports else (lab/'bench.log').read_text()[-2000:], flush=True)
    if bench.returncode: raise RuntimeError(f'bench exited {bench.returncode}')
    if trace:
        rows = list(csv.DictReader(next((lab/'reports').glob('bench-*.csv')).open()))
        def one(row):
            return row['spend'], json.loads(fetch(24100, '/debug/settlement/'+row['spend']))
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            timings = dict(pool.map(one, rows))
        (lab/'reports/settlement.json').write_text(json.dumps(timings,indent=2))
        (lab/'reports/consensus.json').write_bytes(fetch(24100, '/debug/consensus'))
    time.sleep(2)
finally:
    proc.send_signal(signal.SIGINT)
    proc.wait(timeout=35)
    log.close()
subprocess.run(['/usr/local/go/bin/go','run','.scratch/audit-fresh.go',str(lab)], check=True, stdout=(lab/'reports/accounting.json').open('w'))
print('COMPLETE',label,flush=True)
