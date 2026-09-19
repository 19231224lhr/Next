"""One fresh 100/64 closed-loop run; retain reports, never export lab keys."""
import json, os, signal, subprocess, sys, time, urllib.request
from pathlib import Path

root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
label, binaries, tracing = sys.argv[1:]
tracing = tracing == 'trace'
bin_dir = root / '.run' / binaries
labdir = root / '.run' / ('group-' + label)
env = dict(os.environ, UTXO_EXPERIMENT_FLUSH='10ms', UTXO_EXPERIMENT_GOSSIP='10ms')
if tracing: env['UTXO_SETTLEMENT_TRACE'] = '1'
else: env.pop('UTXO_SETTLEMENT_TRACE', None)
ctl = str(bin_dir / 'payctl')
def run(*args, **kwargs):
    return subprocess.run(args, env=env, check=True, **kwargs)

run(ctl, 'init-lab', '-v4', '-dir', str(labdir), '-port', '28000', '-outputs', '128')
lab = json.loads((labdir / 'lab.json').read_text())
with (labdir / 'runner.log').open('w') as log:
    proc = subprocess.Popen([ctl, 'lab-run', '-dir', str(labdir), '-bin', str(bin_dir)], env=env, stdout=log, stderr=log)
    try:
        deadline = time.monotonic() + 45
        while 'Laboratory running:' not in (labdir / 'runner.log').read_text():
            if proc.poll() is not None: raise RuntimeError('lab exited')
            if time.monotonic() > deadline: raise TimeoutError('lab readiness')
            time.sleep(.1)
        time.sleep(3)
        args = [ctl, 'bench-v4', '-dir', str(labdir), '-start', '0', '-count', '100', '-concurrency', '64']
        if tracing: args.append('-trace')
        with (labdir / 'reports' / 'bench-output.txt').open('w') as out:
            run(*args, stdout=out, stderr=subprocess.STDOUT, timeout=180)
        if tracing:
            run('python3', str(root / '.run' / 'collect_trace100.py'), str(labdir), timeout=90)
    finally:
        proc.send_signal(signal.SIGINT)
        proc.wait(timeout=35)
with (labdir / 'reports' / 'audit-output.txt').open('w') as out:
    run(ctl, 'audit', '-dir', str(labdir), stdout=out, stderr=subprocess.STDOUT, timeout=30)
bench = json.loads((labdir / 'reports' / 'bench-v4-0.json').read_text())
(labdir / 'reports' / 'experiment.json').write_text(json.dumps({'label': label, 'binaries': binaries, 'trace': tracing, 'count': 100, 'concurrency': 64, 'flush': '10ms', 'gossip': '10ms'}, indent=2))
print(label, json.dumps(bench['Summary']), flush=True)
