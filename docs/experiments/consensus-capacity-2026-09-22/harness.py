"""Four real committee processes; replay public, already signed payments.

Independent genesis inputs are the default; cycling fixtures are labeled in metadata.
Only the workload preparation is offline. Every run starts with fresh databases,
cold verification caches and the source network's identical genesis and policy.
"""
import importlib.util
import json
import hashlib
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor

ROOT = Path(__file__).resolve().parents[3]
DEST = ROOT/'docs/experiments/consensus-tps-2026-09-21'
SOURCE = Path(os.environ.get('COMMITTEE_SOURCE', str(ROOT/'.run/group-cp-sustained-200')))
FIXTURE = DEST/os.environ.get('COMMITTEE_FIXTURE', 'public-payments.jsonl')
BIN = ROOT/'.run/consensus-tps-bin'
CTL = ROOT/'.run/committee-leveldb-bin/payctl'
spec = importlib.util.spec_from_file_location('runtime', Path(__file__).with_name('runtime.py'))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)
helper.DEST = DEST


def get(url, timeout=1):
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return json.load(response)


def trial(label, arm, rate, count):
    out = DEST/label
    out.mkdir()
    lab = ROOT/'.run'/('group-'+label)
    lab.mkdir(mode=0o700)
    for folder in ('config', 'keys'):
        shutil.copytree(SOURCE/folder, lab/folder)
    for folder in ('logs', 'reports'):
        (lab/folder).mkdir()
    for i in range(4):
        relative = Path(f'committee{i}/comet/config/node_key.json')
        (lab/relative).parent.mkdir(parents=True)
        shutil.copy2(SOURCE/relative, lab/relative)
    def relocate(x):
        if isinstance(x, str): return x.replace(str(SOURCE), str(lab))
        if isinstance(x, list): return [relocate(y) for y in x]
        if isinstance(x, dict): return {k: relocate(v) for k, v in x.items()}
        return x
    conf = relocate(json.loads((SOURCE/'lab.json').read_text()))
    conf['Nodes'] = [n for n in conf['Nodes'] if n['Binary'] == 'committee']
    assert len(conf['Nodes']) == 4
    (lab/'lab.json').write_text(json.dumps(conf))
    for p in (lab/'config').glob('*.json'):
        data = relocate(json.loads(p.read_text()))
        if p.name.startswith('committee') and os.environ.get('COMMITTEE_BANDWIDTH'):
            data.update(P2PSendRate=int(os.environ['COMMITTEE_BANDWIDTH']),P2PRecvRate=int(os.environ['COMMITTEE_BANDWIDTH']))
        p.write_text(json.dumps(data, separators=(',', ':')))
    assert not list(lab.rglob('priv_validator_state.json'))
    bins = ROOT/'.run'/('bin-'+label)
    bins.mkdir()
    binary = BIN/('committee-'+arm if arm not in ('file', 'db', 'flush', 'dbflush') else ('committee-flush' if 'flush' in arm else 'committee'))
    if os.environ.get('COMMITTEE_PROCS'):
        procs = int(os.environ['COMMITTEE_PROCS'])
        assert 1 <= procs <= 16
        (bins/'committee').write_text('#!/bin/sh\nexec env GOMAXPROCS='+str(procs)+' "'+str(binary)+'" "$@"\n')
        (bins/'committee').chmod(0o755)
    else:
        (bins/'committee').symlink_to(binary)
    env = dict(os.environ, UTXO_EXPERIMENT_FLUSH='10ms', UTXO_EXPERIMENT_GOSSIP='10ms',
               UTXO_EXPERIMENT_SIGNING_DB='1' if arm in ('db', 'dbflush') else '0')
    for name in ('UTXO_SETTLEMENT_TRACE', 'UTXO_COMET_PROFILE', 'UTXO_STAGE_DEEP', 'GODEBUG'):
        env.pop(name, None)
    profile_only = os.environ.get('COMMITTEE_PROFILE_ONLY') == '1'
    diagnostic = os.environ.get('COMMITTEE_DIAGNOSTIC') == '1' or profile_only
    if diagnostic:
        env['UTXO_COMET_PROFILE']='1'
        if not profile_only: env['UTXO_SETTLEMENT_TRACE']='1'
    metadata = dict(label=label, arm=arm, rate=rate, count=count, source=str(SOURCE),
                    binary=str(binary), mode='committee-only', fixture=FIXTURE.name,
                    concurrency=128, flush='10ms', gossip='10ms', trace=diagnostic,
                    bandwidth_override=int(os.environ.get('COMMITTEE_BANDWIDTH','0')),
                    committee_procs=os.environ.get('COMMITTEE_PROCS','default'),
                    cpu_profile=os.environ.get('COMMITTEE_CPU_PROFILE') == '1',
                    drain_seconds=float(os.environ.get('COMMITTEE_DRAIN_SECONDS','0')),
                    commit_timeout_ms=int(os.environ.get('COMMITTEE_COMMIT_MS','100')),
                    coast_seconds=float(os.environ.get('COMMITTEE_COAST_SECONDS','0')),
                    blockstore='memory' if os.environ.get('UTXO_EXPERIMENT_MEM_BLOCKSTORE') == '1' else 'persistent',
                    entrypoints=4 if os.environ.get('COMMITTEE_SPREAD') == '1' else 1)
    metadata.update(application_store='ephemeral-memory' if os.environ.get('UTXO_EXPERIMENT_COMMITTEE_MEMORY') == '1' else 'synchronous-bbolt',route=os.environ.get('COMMITTEE_ROUTE','round-robin'),
                    disk_free_before_bytes=shutil.disk_usage(ROOT).free,
                    workload=os.environ.get('COMMITTEE_WORKLOAD','independent final genesis inputs'),
                    binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                    fixture_origin='offline owner+three-member signatures' if FIXTURE.name.startswith('offline-') else 'exported successful public transactions')
    (out/'metadata.json').write_text(json.dumps(metadata, indent=2)+'\n')
    samples, blocks = [], []
    seen_diagnostics = set()
    next_diagnostic = 0
    next_resource = 0
    next_progress = 0
    trace_file = (out/'consensus-stream.jsonl').open('w') if diagnostic else None
    sender = None
    profile_pool = None
    profile = None
    with (out/'nodes.log').open('w') as log:
        process = subprocess.Popen([str(CTL), 'lab-run', '-dir', str(lab), '-bin', str(bins)],
                                   env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic()+60
            while True:
                if process.poll() is not None: raise RuntimeError('committee lab exited')
                try:
                    if all(int(get(n['URL']+'/healthz')['height']) >= 0 for n in conf['Nodes']): break
                except (OSError, ValueError): pass
                if time.monotonic() > deadline: raise TimeoutError('committee readiness')
                time.sleep(.1)
            # Exclude cold peer/block-sync startup from the measured load interval.
            time.sleep(20)
            ready_heights = [int(get(n['URL']+'/healthz')['height']) for n in conf['Nodes']]
            if not all(h >= 1 for h in ready_heights):
                raise RuntimeError(f'consensus not ready: {ready_heights}')
            readiness = []
            if os.environ.get('UTXO_BLOCK_PROFILE') == '1' or os.environ.get('COMMITTEE_READY_PROBE') == '1':
                readiness = [get(n['URL']+'/debug/readiness') for n in conf['Nodes']]
                if not all(not x['catching_up'] and x['peers']==3 and x['height']>=1 for x in readiness):
                    raise RuntimeError(f'consensus not ready: {readiness}')
                hashes={x.get('block_hash') for x in readiness}
                if len(hashes)>1: raise RuntimeError(f'readiness block mismatch: {hashes}')
            (out/'ready.json').write_text(json.dumps(dict(unix_ns=time.time_ns(), heights=ready_heights, settle_seconds=20, status=readiness)))
            endpoint = ','.join(n['URL'] for n in conf['Nodes']) if os.environ.get('COMMITTEE_SPREAD') == '1' else conf['Nodes'][0]['URL']
            sender = subprocess.Popen([str(BIN/'committee-load'), 'send', str(FIXTURE),
                                       endpoint, str(count), str(rate), str(out/'send.json')],
                                      stdout=log, stderr=log)
            if os.environ.get('COMMITTEE_CPU_PROFILE') == '1':
                def capture_cpu():
                    time.sleep(float(os.environ.get('COMMITTEE_PROFILE_DELAY','0')))
                    seconds=int(os.environ.get('COMMITTEE_PROFILE_SECONDS','10'))
                    with urllib.request.urlopen(conf['Nodes'][0]['URL']+f'/debug/pprof/profile?seconds={seconds}', timeout=seconds+10) as response:
                        (out/'committee0.cpu').write_bytes(response.read())
                profile_pool = ThreadPoolExecutor(max_workers=1)
                profile = profile_pool.submit(capture_cpu)
            observed_height = 0
            included = successful = 0
            deadline = time.monotonic()+max(40, count/max(rate, 1)*2+20) if rate else time.monotonic()+60
            sender_finished = None
            accepted = count
            while time.monotonic() < deadline:
                if diagnostic and time.monotonic() >= next_resource:
                    samples.append(helper.resources(lab))
                    if os.environ.get('UTXO_BLOCK_PROFILE') == '1':
                        try:
                            samples.append(dict(observed_ns=time.time_ns(),state=[get(n['URL']+'/debug/pool',.5) for n in conf['Nodes']]))
                        except (OSError,ValueError) as err:
                            samples.append(dict(observed_ns=time.time_ns(),pool_error=str(err)))
                    next_resource=time.monotonic()+1
                if diagnostic and time.monotonic() >= next_diagnostic:
                    try:
                        for entry in get(conf['Nodes'][0]['URL']+'/debug/consensus', 2):
                            key = (entry['UnixNS'],entry['Stage'])
                            if key not in seen_diagnostics:
                                seen_diagnostics.add(key)
                                trace_file.write(json.dumps(entry)+'\n')
                    except (OSError,ValueError): pass
                    next_diagnostic = time.monotonic()+.5
                try:
                    h = int(get(conf['Nodes'][0]['URL']+'/healthz', .5)['height'])
                    now = time.time_ns()
                    samples.append(dict(committee_height=h, height_observed_ns=now))
                    for height in range(observed_height+1, h+1):
                        result = get(conf['Nodes'][0]['URL']+f'/block_results?height={height}', 2)
                        rows = result.get('txs_results') or []
                        successes = sum(int(r['code']) == 0 for r in rows)
                        blocks.append(dict(height=height, observed_ns=now, transactions=len(rows), successful=successes))
                        included += len(rows)
                        successful += successes
                        observed_height = height
                    if os.environ.get('COMMITTEE_PROGRESS') == '1' and time.monotonic() >= next_progress:
                        print('PROGRESS',label,'included',included,'successful',successful,'height',h,flush=True)
                        if os.environ.get('COMMITTEE_SAMPLE_RSS') == '1':
                            samples.append(helper.resources(lab))
                        next_progress=time.monotonic()+30
                    if sender.poll() is not None:
                        if sender_finished is None:
                            sender_finished = time.monotonic()
                            drain_seconds=float(os.environ.get('COMMITTEE_DRAIN_SECONDS','0'))
                            if drain_seconds > 0:
                                deadline=min(deadline, sender_finished+drain_seconds)
                            sent = json.loads((out/'send.json').read_text())
                            accepted = sent['Summary']['accepted']
                    else:
                        accepted = count
                    if sender.poll() is not None and included >= accepted:
                        if all(int(get(n['URL']+'/healthz')['height']) >= h for n in conf['Nodes']): break
                except (OSError, ValueError) as error:
                    samples.append(dict(error=str(error), observed_ns=time.time_ns()))
                if process.poll() is not None: raise RuntimeError('committee lab exited during load')
                time.sleep(.05)
            else:
                print('INCOMPLETE', label, 'included', included, 'accepted', accepted, flush=True)
            if sender.wait(timeout=10) != 0: raise RuntimeError('sender failed; inspect send.json')
            if profile is not None:
                profile.result(timeout=20)
            # Observe idle compaction after load separately from payment latency.
            # Any later blocks are still captured by the quiescence check below.
            coast_until = time.monotonic()+float(os.environ.get('COMMITTEE_COAST_SECONDS','0'))
            while time.monotonic() < coast_until:
                samples.append(helper.resources(lab))
                time.sleep(min(5, max(0, coast_until-time.monotonic())))
            # Let the final proof block finish on all replicas before shutdown.
            # This wait is excluded from payment completion measurements.
            stable_tip = None
            stable_since = time.monotonic()
            deadline = time.monotonic()+20
            while time.monotonic() < deadline:
                heights = [int(get(n['URL']+'/healthz', 2)['height']) for n in conf['Nodes']]
                tip = heights[0] if len(set(heights)) == 1 else None
                if tip is None or tip != stable_tip:
                    stable_since = time.monotonic()
                stable_tip = tip
                if tip is not None and time.monotonic()-stable_since >= 1:
                    break
                time.sleep(.1)
            else:
                raise TimeoutError('replicas did not reach a common quiescent height')
            # A final catch-up block may appear during quiescence. Include its
            # results with the time actually observed, before the offline audit.
            for height in range(observed_height+1, stable_tip+1):
                result = get(conf['Nodes'][0]['URL']+f'/block_results?height={height}', 2)
                rows = result.get('txs_results') or []
                successes = sum(int(r['code']) == 0 for r in rows)
                blocks.append(dict(height=height, observed_ns=time.time_ns(), transactions=len(rows), successful=successes))
                included += len(rows)
                successful += successes
                observed_height = height
            if diagnostic:
                for i,n in enumerate(conf['Nodes']):
                    (out/f'consensus{i}.json').write_text(json.dumps(get(n['URL']+'/debug/consensus', 10)))
        finally:
            if sender is not None and sender.poll() is None: sender.terminate(); sender.wait(timeout=10)
            shutdown_started = time.time_ns()
            process.send_signal(signal.SIGINT)
            process.wait(timeout=40)
            (out/'shutdown.json').write_text(json.dumps(dict(seconds=(time.time_ns()-shutdown_started)/1e9,supervisor_exit=process.returncode,note='node shutdown plus audit export; outside transaction timing'))+'\n')
            (out/'resources.json').write_text(json.dumps(samples)+'\n')
            (out/'blocks.json').write_text(json.dumps(blocks)+'\n')
            if trace_file is not None: trace_file.close()
            if profile_pool is not None: profile_pool.shutdown()
    with (out/'audit.log').open('w') as log:
        subprocess.run([str(CTL), 'audit', '-dir', str(lab)], stdout=log, stderr=subprocess.STDOUT, check=True)
    shutil.copy2(lab/'reports/audit.json', out/'audit.json')
    audit = json.loads((out/'audit.json').read_text())
    assert len(audit) == 4 and len({n['StateHash'] for n in audit}) == 1
    assert all(n['Payments'] == n['Closed'] == successful and n['Gap'] == '0'
               and int(n['Rewards']) == successful*84 and int(n['Burned']) == successful*10 for n in audit)
    if os.environ.get('UTXO_EXPERIMENT_MEM_BLOCKSTORE') == '1':
        # MemDB is intentionally gone after shutdown; the durable application
        # audit above remains the source of truth for this experiment.
        (out/'committed-facts.jsonl').write_text(
            json.dumps({'skipped': 'blockstore is process-local MemDB'})+'\n')
    else:
        with (out/'committed-facts.jsonl').open('w') as output:
            subprocess.run([str(ROOT/'.run/committee-storage-sustained-bin/inspect-commits'), str(lab)], stdout=output, check=True)
    print('FINISHED', label, 'payments', successful, 'offered', count, 'blocks', len(blocks), flush=True)
