"""Isolated one-payment diagnostics; invoke on the Mac repository root."""
import json, os, pathlib, signal, subprocess, sys, time, urllib.request

root = pathlib.Path.cwd()
lab = root / 'experiments/latency-phase-001'
label, first, count, flush, gossip = sys.argv[1:]
output = lab / 'reports' / label
output.mkdir(exist_ok=False)
env = dict(os.environ, UTXO_SETTLEMENT_TRACE='1', UTXO_EXPERIMENT_FLUSH=flush, UTXO_EXPERIMENT_GOSSIP=gossip)
meta = dict(label=label, first=int(first), count=int(count), flush=flush, gossip=gossip,
            code_base=subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip(),
            scope='single host; one finalized UTXO payment at a time; unchanged delivery retries; diagnostic instrumentation enabled')
(output/'metadata.json').write_text(json.dumps(meta, indent=2))
log = (output/'supervisor.log').open('w')
proc = subprocess.Popen([str(root/'bin/payctl'), 'lab-run', '-dir', str(lab), '-bin', str(root/'bin')], env=env, stdout=log, stderr=log)
def get(url):
    with urllib.request.urlopen(url, timeout=3) as r: return json.load(r)
try:
    for attempt in range(100):
        if proc.poll() is not None: raise RuntimeError('lab exited')
        try:
            heights = [int(get(f'http://127.0.0.1:{21100+n}/healthz')['height']) for n in range(4)]
            with urllib.request.urlopen('http://127.0.0.1:21300/healthz', timeout=3) as ready:
                if ready.status != 200: raise ValueError('gateway unavailable')
            if min(heights) >= 3 and max(heights)-min(heights) <= 1: break
        except (OSError, ValueError): pass
        time.sleep(.2)
    else: raise RuntimeError('lab not ready')
    # Allow startup/reconnect noise to settle equally in every phase.
    time.sleep(3)
    for i in range(int(first), int(first)+int(count)):
        result = subprocess.run([str(root/'bin/payctl'), 'demo', '-dir', str(lab), '-hops', '1', '-input', str(i), '-trace', '-observe-block'], env=env, text=True, capture_output=True, timeout=100, check=True)
        report_path = pathlib.Path(result.stdout.rsplit('Report: ', 1)[1].strip())
        report = json.loads(report_path.read_text())
        sample = report['Samples'][0]
        nodes = []
        for n in range(4):
            base = f'http://127.0.0.1:{21100+n}'
            nodes.append(dict(node=n, consensus=get(base+'/debug/consensus'), attempts=get(base+'/debug/settlement/'+sample['Spend'])))
        (output/f'input-{i}.json').write_text(json.dumps(dict(report=report,nodes=nodes), indent=2))
        print(label, i, 'ready_ms', sample['WalletReadyMicros']/1000, 'commit_observed_ms', sample['CommitObservedMicros']/1000, 'proof_ms', sample['FinalProofMicros']/1000, flush=True)
        time.sleep(.3)
finally:
    proc.send_signal(signal.SIGINT)
    try: proc.wait(timeout=35)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
        raise
    log.close()
(output/'done.json').write_text(json.dumps(dict(finished=time.time(), returncode=proc.returncode)))
