#!/usr/bin/env python3
"""Real A->B->C respending, fresh nine-process laboratory per case."""
import argparse
from datetime import datetime, timezone, timedelta
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
BIN = ROOT / '.run/continuous-respending-bin'
ENV = dict(os.environ, PATH='/usr/local/go/bin:/opt/homebrew/bin:' + os.environ['PATH'])
ENV.update(UTXO_EXPERIMENT_COMMIT='250ms', UTXO_EXPERIMENT_FLUSH='10ms',
           UTXO_EXPERIMENT_GOSSIP='10ms', UTXO_EXPERIMENT_MEM_BLOCKSTORE='1',
           UTXO_EXPERIMENT_COMMITTEE_MEMORY='1', UTXO_EXPERIMENT_MEMBER_MEMORY='1',
           UTXO_EXPERIMENT_GATEWAY_MEMORY='1', UTXO_SETTLEMENT_TRACE='1')
for key in ['UTXO_COMET_PROFILE', 'UTXO_TRACE_ALL', 'UTXO_RUNTIME_TRACE', 'GODEBUG']:
    ENV.pop(key, None)


def write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def get(url):
    # Member/gateway health is plain text; committee health is JSON.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(url, timeout=5) as response:
        raw = response.read()
        return {'alive': True} if raw.strip() == b'alive' else json.loads(raw)


def command(args, log):
    result = subprocess.run([str(x) for x in args], cwd=ROOT, env=ENV,
                            stdout=log, stderr=subprocess.STDOUT)
    if result.returncode:
        raise RuntimeError(f'command failed ({result.returncode}): {args}')


def build():
    BIN.mkdir(parents=True, exist_ok=True)
    with (OUT / 'build.log').open('w') as log:
        command(['python3', ROOT / 'third_party/cometbft/overlay.py'], log)
        for role in ['payctl', 'committee', 'gateway', 'member']:
            target = BIN / ('member-real' if role == 'member' else role)
            command(['go', 'build', '-tags=comet_v3', '-o', target, './cmd/' + role], log)
    member = BIN / 'member'
    member.write_text('#!/bin/sh\nexec env GOMAXPROCS=16 GOGC=200 "' + str(BIN / 'member-real') + '" "$@"\n')
    member.chmod(0o755)
    write(OUT / 'build.json', {
        'base_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'binaries': {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in BIN.iterdir()},
        'source_sha256': {name:hashlib.sha256((ROOT/name).read_bytes()).hexdigest() for name in ['cmd/payctl/main.go','cmd/payctl/direct_chain.go','cmd/payctl/direct_chain_test.go']},
        'experiment_env': {k:v for k,v in ENV.items() if k.startswith('UTXO_')},
        'wallet': 'three separate bbolt NoSync stores; authentic ReceiveDirect and block following',
        'poll_ms': 5,
    })
    (OUT / 'experiment.patch').write_text(subprocess.check_output(['git', 'diff', '--', 'cmd/payctl/main.go'], cwd=ROOT, text=True))


def run_case(label, length, mode, diagnostic=False):
    dest = OUT / label
    dest.mkdir()  # Refuse to overwrite evidence or reuse a genesis.
    labdir = ROOT / '.run' / ('chain-' + label)
    template = ROOT / '.run/chain-pristine-template'
    with (dest / 'setup.log').open('w') as log:
        if not template.exists():
            command([BIN/'payctl', 'init-lab', '-dir', template, '-outputs', 1, '-port', 26000, '-v4'], log)
            command([BIN/'payctl', 'chain-v4', '-dir', template, '-prepare'], log)
            (template/'prepared').write_text('pristine: no node or payment has run here\n')
        assert (template/'prepared').exists()
        shutil.copytree(template, labdir)
        for path in [labdir/'lab.json', *list((labdir/'config').glob('*.json'))]:
            path.write_text(path.read_text().replace(str(template),str(labdir)))
    lab = json.loads((labdir/'lab.json').read_text())
    lab['Nodes'] = [n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')]
    assert len(lab['Nodes']) == 9
    write(labdir/'lab.json',lab)
    network = json.loads(Path(lab['Network']).read_text())
    network['GenesisTime']=(datetime.now(timezone.utc)-timedelta(seconds=1)).isoformat()
    write(Path(lab['Network']),network)
    write(dest/'network.json',network)
    write(dest/'configuration.json',{'length':length,'mode':mode,'diagnostic':diagnostic,'lab':lab})
    node_log = (dest/'nodes.log').open('w')
    node_env = dict(ENV, UTXO_TRACE_ALL='1' if diagnostic else '0')
    process = subprocess.Popen([str(BIN/'payctl'),'lab-run','-dir',str(labdir),'-bin',str(BIN)],cwd=ROOT,env=node_env,stdout=node_log,stderr=subprocess.STDOUT)
    try:
        deadline=time.monotonic()+45
        while True:
            if process.poll() is not None: raise RuntimeError('laboratory stopped during startup')
            try:
                health={n['Name']:get(n['URL']+'/healthz') for n in lab['Nodes']}
                if all(int(h.get('height',0))>=1 for name,h in health.items() if name.startswith('committee')):
                    break
            except (OSError,ValueError):
                pass
            if time.monotonic()>deadline: raise TimeoutError('laboratory health')
            time.sleep(.2)
        time.sleep(3)  # Same warm-up for every fresh run; no payment is pre-generated.
        write(dest/'ready.json',health)
        args=[BIN/'payctl','chain-v4','-dir',labdir,'-length',length]
        if mode=='wait_final':args+=['-wait-final']
        if diagnostic:args+=['-trace']
        with (dest/'chain.log').open('w') as log:command(args,log)
        report=json.loads((labdir/'reports/chain-v4.json').read_text())
        settlements={}
        for i,url in enumerate(network['CommitteeURLs']):
            settlements[str(i)]={bytes(h['Fact']).hex():get(url+'/debug/settlement/'+bytes(h['Fact']).hex()) for h in report['Hops']}
        write(dest/'settlements.json',settlements)
        write(dest/'health-after.json',{n['Name']:get(n['URL']+'/healthz') for n in lab['Nodes']})
        time.sleep(.5)
    finally:
        if process.poll() is None:process.send_signal(signal.SIGINT)
        try:process.wait(timeout=45)
        except subprocess.TimeoutExpired:
            process.kill();process.wait();raise
        node_log.close()
        if (labdir/'reports').exists():shutil.copytree(labdir/'reports',dest/'reports',dirs_exist_ok=True)
        shutil.copytree(labdir/'logs',dest/'node-logs',dirs_exist_ok=True)
    with (dest/'audit.log').open('w') as log:
        command([BIN/'payctl','audit','-dir',labdir],log)
        command([BIN/'payctl','chain-v4','-dir',labdir,'-audit'],log)
    shutil.copytree(labdir/'reports',dest/'reports',dirs_exist_ok=True)
    audits=json.loads((dest/'reports/audit.json').read_text())
    assert all(n['Pending']==0 for n in audits), 'outbox not empty'
    committees=[n for n in audits if n['Name'].startswith('committee')]
    assert len({n['StateHash'] for n in committees})==1
    assert all(n['Payments']==length and n['Closed']==length and n['Gap']=='0' and not n.get('Revisions',0) for n in committees)
    print(json.dumps({'label':label,'mode':mode,'length':length,'fast_ms':report['FastChainMS'],'public_ms':report['PublicChainMS'],'closed_ms':report['ClosedChainMS'],'audit':'passed'}),flush=True)


if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('--suite',choices=['diagnostic','formal'],default='diagnostic')
    parser.add_argument('--skip-build',action='store_true')
    parser.add_argument('--prefix',default='',help='separate evidence when revising the experiment driver')
    args=parser.parse_args()
    OUT.mkdir(parents=True,exist_ok=True)
    if not args.skip_build:build()
    if args.suite=='diagnostic':run_case(args.prefix+'diagnostic10',10,'fast',True)
    else:
        for repeat in range(1,4):
            modes=['fast','wait_final'] if repeat%2 else ['wait_final','fast']
            for length in [1,10,100]:
                for mode in modes:run_case(f'{args.prefix}{mode}-{length}-r{repeat}',length,mode)
