#!/usr/bin/env python3
"""Native, loopback-only regtest lab. CLI setup is outside measured payments."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
TOOLS = ROOT / '.run/ln-tools'
LND = TOOLS / 'lnd-darwin-arm64-v0.21.3-beta/lnd'
LNCLI = LND.with_name('lncli')
BTC = TOOLS / 'bitcoin-31.1/bin/bitcoind'
BTCCLI = BTC.with_name('bitcoin-cli')

def command(args):
    p = subprocess.run([str(x) for x in args], text=True, capture_output=True, timeout=60)
    if p.returncode:
        raise RuntimeError(p.stderr.strip() or p.stdout.strip())
    try:
        return json.loads(p.stdout)
    except json.JSONDecodeError:
        return p.stdout.strip()

def wait(fn, seconds=120):
    deadline = time.monotonic() + seconds
    error = None
    while time.monotonic() < deadline:
        try:
            result = fn()
            if result:
                return result
        except (RuntimeError, subprocess.TimeoutExpired) as e:
            error = e
        time.sleep(.5)
    raise RuntimeError(f'wait expired: {error}')

def write_json(path, data):
    path.write_text(json.dumps(data, indent=2))

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('action', choices=['start', 'stop', 'status'])
    ap.add_argument('name')
    ap.add_argument('--routed', action='store_true')
    ap.add_argument('--capacity', type=int, default=16000000)
    ap.add_argument('--push', type=int, default=8000000)
    ap.add_argument('--commit-interval', choices=['50ms','10ms'], default='50ms')
    args = ap.parse_args()
    if not args.name.replace('-', '').isalnum():
        raise ValueError('case name must be alphanumeric/hyphen')
    run = ROOT / '.run' / ('ln-' + args.name)
    evidence = ROOT / 'docs/experiments/lightning-2026-09-24' / args.name
    bdir = run / 'bitcoin'
    def bitcoin(*a):
        return command([BTCCLI, '-regtest', '-datadir=' + str(bdir), '-rpcwallet=lab', *a])
    def ln(i, *a):
        return command([LNCLI, '--network=regtest', '--lnddir=' + str(run / f'node{i}'),
                        f'--rpcserver=127.0.0.1:{19009+i}', *a])
    if args.action != 'start':
        manifest = json.loads((run / 'manifest.json').read_text())
        for i in range(manifest['nodes']):
            try:
                print(ln(i, 'stop' if args.action == 'stop' else 'getinfo'))
            except RuntimeError as e:
                print(e)
        if args.action == 'stop':
            print(bitcoin('stop'))
        return
    if run.exists():
        raise RuntimeError('case already exists; choose a new case name')
    bdir.mkdir(parents=True)
    evidence.mkdir(parents=True, exist_ok=True)
    conf = '''regtest=1
server=1
listen=0
discover=0
dnsseed=0
rpcuser=regtestlab
rpcpassword=regtest-only-local
fallbackfee=0.00002000
zmqpubrawblock=tcp://127.0.0.1:19332
zmqpubrawtx=tcp://127.0.0.1:19333
[regtest]
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
rpcport=19443
'''
    (bdir / 'bitcoin.conf').write_text(conf)
    (evidence / 'bitcoin.conf').write_text(conf)
    procs = []
    def start(cmd, log):
        with log.open('w') as f:
            p = subprocess.Popen([str(x) for x in cmd], stdout=f, stderr=subprocess.STDOUT, start_new_session=True)
        procs.append(p.pid)
        return p
    start([BTC, '-datadir=' + str(bdir)], run / 'bitcoin.stdout.log')
    wait(lambda: command([BTCCLI, '-regtest', '-datadir=' + str(bdir), 'getblockchaininfo']))
    command([BTCCLI, '-regtest', '-datadir=' + str(bdir), 'createwallet', 'lab'])
    mine_address = bitcoin('getnewaddress')
    bitcoin('generatetoaddress', '110', mine_address)
    n = 3 if args.routed else 2
    write_json(run / 'manifest.json', {'nodes': n, 'name': args.name})
    for i in range(n):
        nd = run / f'node{i}'
        nd.mkdir()
        lc = f'''[Application Options]
alias=lnbench-{i}
noseedbackup=1
listen=127.0.0.1:{19735+i}
rpclisten=127.0.0.1:{19009+i}
restlisten=127.0.0.1:{18080+i}
debuglevel=info
channel-commit-interval={args.commit_interval}
[Bitcoin]
bitcoin.regtest=1
bitcoin.node=bitcoind
[Bitcoind]
bitcoind.rpchost=127.0.0.1:19443
bitcoind.rpcuser=regtestlab
bitcoind.rpcpass=regtest-only-local
bitcoind.zmqpubrawblock=tcp://127.0.0.1:19332
bitcoind.zmqpubrawtx=tcp://127.0.0.1:19333
'''
        (nd / 'lnd.conf').write_text(lc)
        (evidence / f'node{i}.conf').write_text(lc)
        start([LND, '--lnddir=' + str(nd)], run / f'node{i}.stdout.log')
    keys = []
    for i in range(n):
        info = wait(lambda: ln(i, 'getinfo'))
        keys.append(info['identity_pubkey'])
        addr = ln(i, 'newaddress', 'p2wkh')['address']
        bitcoin('sendtoaddress', addr, '1')
    bitcoin('generatetoaddress', '6', mine_address)
    for i in range(n):
        wait(lambda: ln(i, 'getinfo')['synced_to_chain'])
        wait(lambda: int(ln(i, 'walletbalance')['confirmed_balance']) >= 100000000)
    for i in range(n-1):
        ln(i, 'connect', keys[i+1] + f'@127.0.0.1:{19736+i}')
        ln(i, 'openchannel', '--node_key=' + keys[i+1], '--local_amt=' + str(args.capacity),
           '--push_amt=' + str(args.push), '--sat_per_vbyte=2')
    bitcoin('generatetoaddress', '6', mine_address)
    for i in range(n):
        expected = 2 if args.routed and i == 1 else 1
        wait(lambda: len([c for c in ln(i, 'listchannels')['channels'] if c['active']]) == expected)
    wait(lambda: ln(0, 'queryroutes', keys[-1], '10000')['routes'])
    wait(lambda: ln(n-1, 'queryroutes', keys[0], '10000')['routes'])
    write_json(run / 'endpoints.json', [{'Address': f'127.0.0.1:{19009+i}', 'Directory': str(run / f'node{i}')} for i in [0,n-1]])
    write_json(evidence / 'deployment.json', {'nodes': n, 'capacity_sat': args.capacity, 'push_sat': args.push,
                 'pids': procs, 'bitcoin_version': command([BTC, '--version']), 'lnd_version': command([LND, '--version']),
                 'channel_commit_interval': args.commit_interval, 'channel_commit_batch_size': '10 (default)',
                 'persistence': 'LND defaults; no NoSync', 'topology': 'routed' if args.routed else 'direct'})
    for i in range(n):
        write_json(evidence / f'channels-{i}.json', ln(i, 'listchannels'))
    print('READY', run / 'endpoints.json', flush=True)

if __name__ == '__main__':
    main()
