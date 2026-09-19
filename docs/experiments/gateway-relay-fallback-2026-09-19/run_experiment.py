"""One fresh 100/64 closed-loop run; retain reports, never export lab keys."""
import json, os, signal, subprocess, sys, time, urllib.request
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
label, binaries, tracing = sys.argv[1:4]
fault = len(sys.argv) > 4 and sys.argv[4] == 'reject-gateway'
count, concurrency = (16, 8) if fault else (100, 64)
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
proxy = None
rejected = []
if fault:
    # Only gateways use this network copy. Members/wallets reach the real
    # committee. Block reads still work; every gateway command POST is rejected.
    network_path = labdir / 'config/network.json'
    network = json.loads(network_path.read_text())
    upstream = network['CommitteeURLs'][0]
    class RejectSubmission(BaseHTTPRequestHandler):
        def log_message(self, *args): pass
        def do_POST(self):
            self.rfile.read(int(self.headers.get('Content-Length', '0')))
            rejected.append({'unix_ns': time.time_ns(), 'path': self.path})
            self.send_error(503, 'injected gateway submission failure')
        def do_GET(self):
            try:
                with urllib.request.urlopen(upstream + self.path, timeout=10) as response:
                    body = response.read()
                    self.send_response(response.status)
                    self.send_header('Content-Type', response.headers.get('Content-Type', 'application/json'))
                    self.send_header('Content-Length', str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
            except Exception:
                self.send_error(502)
    proxy = ThreadingHTTPServer(('127.0.0.1', 0), RejectSubmission)
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    network['CommitteeURLs'][0] = f'http://127.0.0.1:{proxy.server_port}'
    gateway_network = labdir / 'config/gateway-network.json'
    gateway_network.write_text(json.dumps(network))
    for path in (labdir / 'config').glob('gateway[0-9].json'):
        config = json.loads(path.read_text())
        config['Network'] = str(gateway_network)
        path.write_text(json.dumps(config))
with (labdir / 'runner.log').open('w') as log:
    proc = subprocess.Popen([ctl, 'lab-run', '-dir', str(labdir), '-bin', str(bin_dir)], env=env, stdout=log, stderr=log)
    try:
        deadline = time.monotonic() + 45
        while 'Laboratory running:' not in (labdir / 'runner.log').read_text():
            if proc.poll() is not None: raise RuntimeError('lab exited')
            if time.monotonic() > deadline: raise TimeoutError('lab readiness')
            time.sleep(.1)
        time.sleep(3)
        args = [ctl, 'bench-v4', '-dir', str(labdir), '-start', '0', '-count', str(count), '-concurrency', str(concurrency)]
        if tracing: args.append('-trace')
        with (labdir / 'reports' / 'bench-output.txt').open('w') as out:
            run(*args, stdout=out, stderr=subprocess.STDOUT, timeout=180)
        if tracing:
            run('python3', str(root / '.run' / 'collect_trace100.py'), str(labdir), timeout=90)
    finally:
        proc.send_signal(signal.SIGINT)
        proc.wait(timeout=35)
        if proxy:
            proxy.shutdown()
            proxy.server_close()
with (labdir / 'reports' / 'audit-output.txt').open('w') as out:
    run(ctl, 'audit', '-dir', str(labdir), stdout=out, stderr=subprocess.STDOUT, timeout=30)
bench = json.loads((labdir / 'reports' / 'bench-v4-0.json').read_text())
(labdir / 'reports' / 'experiment.json').write_text(json.dumps({'label': label, 'binaries': binaries, 'trace': tracing, 'count': count, 'concurrency': concurrency, 'flush': '10ms', 'gossip': '10ms', 'reject_gateway_submit': fault}, indent=2))
if fault:
    assert rejected, 'fault injection did not reject any gateway submissions'
    (labdir / 'reports' / 'gateway-rejections.json').write_text(json.dumps(rejected, indent=2))
print(label, json.dumps(bench['Summary']), flush=True)
