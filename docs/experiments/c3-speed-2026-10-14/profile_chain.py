#!/usr/bin/env python3
"""Diagnostic-only real 1000-hop chain; never use these timings as comparisons.

Reuses run_c3 unchanged, including fixture cloning, service inventory, receipt
waiting, background delivery, settlement diagnostics, and shutdown/draining.
"""
import argparse
import concurrent.futures
import hashlib
import importlib.util
import json
import os
import pathlib
import shutil
import sys
import threading
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=pathlib.Path, required=True)
    parser.add_argument('--bin', type=pathlib.Path, required=True, help='stage6 services directory')
    parser.add_argument('--client', type=pathlib.Path, required=True, help='frozen common payctl executable')
    parser.add_argument('--fixture', type=pathlib.Path, required=True, help='existing unstarted chain fixture')
    parser.add_argument('--gomaxprocs', type=int, default=1)
    parser.add_argument('--runtime-trace', action='store_true')
    args = parser.parse_args()
    if sys.platform != 'linux':
        parser.error('run under WSL/Linux on ext4, after all timing comparisons stop')
    if args.gomaxprocs < 1:
        parser.error('gomaxprocs must be positive')
    root, services, client, fixture = (p.resolve() for p in (args.root, args.bin, args.client, args.fixture))
    for path in [client, *(services/n for n in ('member', 'gateway', 'committee'))]:
        if not path.is_file():
            raise FileNotFoundError(path)
    if not fixture.is_dir():
        raise FileNotFoundError(fixture)
    root.mkdir(parents=True, exist_ok=False)
    binary = root/'bin'
    binary.mkdir()
    sources = {'payctl': client, **{n: services/n for n in ('member', 'gateway', 'committee')}}
    for name, source in sources.items():
        shutil.copy2(source, binary/name)
    variant_path = services/'variant.json'
    service_variant = json.loads(variant_path.read_text()) if variant_path.is_file() else None
    if variant_path.is_file():
        shutil.copy2(variant_path, binary/'variant.json')
        shutil.copy2(variant_path, root/'original-variant.json')
    here = pathlib.Path(__file__).resolve().parent
    runner_path = here.parent/'c3-issuer-funded-2026-10-13'/'run_c3.py'
    sys.path.insert(0, str(runner_path.parent))
    spec = importlib.util.spec_from_file_location('chain_diagnostic_runner', runner_path)
    runner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(runner)
    os.environ['GOMAXPROCS'] = str(args.gomaxprocs)
    # Keep profiling settings separate from any inherited client CPU experiments.
    for key in ('UTXO_CHAIN_CPU_PROFILE', 'UTXO_CHAIN_BUILD_PROFILE', 'UTXO_CHAIN_CPU_FOREGROUND_ONLY'):
        os.environ.pop(key, None)
    os.environ['UTXO_CHAIN_CPU_PROFILE'] = str(root/'client.pprof')
    os.environ['UTXO_CHAIN_BUILD_PROFILE'] = '1'
    configuration = {
        'diagnostic_only': True, 'length': 1000, 'trace': True,
        'cpu_profile_seconds': 6, 'runtime_trace_seconds': 2 if args.runtime_trace else None,
        'runtime_trace_delay_seconds': 2.5 if args.runtime_trace else None,
        'client_cpu_profile': str(root/'client.pprof'), 'client_cpu_scope': 'whole chain including observer drain',
        'client_build_profile': True, 'service_variant': service_variant,
        'gomaxprocs': args.gomaxprocs, 'affinity': sorted(os.sched_getaffinity(0)),
        'fixture': str(fixture), 'services': str(services), 'common_client': str(client),
        'binary_sha256': {n: hashlib.sha256(p.read_bytes()).hexdigest() for n,p in sources.items()},
        'driver_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
        'runner_sha256': hashlib.sha256(runner_path.read_bytes()).hexdigest(),
        'started_unix_ns': time.time_ns(),
    }
    runner.save(root/'configuration.json', configuration)
    shutil.copy2(__file__, root/'profile_chain.py')
    run = root/'chain'
    original_command = runner.base.run_command
    records, futures, started = [], [], []

    def capture(node, kind, seconds, ready):
        if kind == 'runtime':
            ready.set() # Thread is ready; dispatch is not blocked by trace delay.
            time.sleep(2.5)
        url = node['URL'].rstrip('/')+'/debug/pprof/'+('profile' if kind == 'cpu' else 'trace')+f'?seconds={seconds}'
        record = {'node': node['Name'], 'kind': kind, 'url': url, 'request_unix_ns': time.time_ns()}
        try:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            request = urllib.request.Request(url)
            ready.set()
            with opener.open(request, timeout=15) as response:
                path = root/(node['Name']+('.pprof' if kind == 'cpu' else '.trace'))
                path.write_bytes(response.read())
                record.update(ok=True, file=str(path), bytes=path.stat().st_size)
        except Exception as exc:
            record.update(ok=False, error=repr(exc))
        finally:
            ready.set()
        record['completed_unix_ns'] = time.time_ns()
        return record

    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        def profiled_command(command, *positional, **keywords):
            if 'chain-v4' in command and '--prepare' not in command:
                if futures:
                    raise RuntimeError('more than one chain workload encountered')
                lab = runner.read(run/'lab.json')
                selected = [next(n for n in lab['Nodes'] if n['Binary'] == role) for role in ('gateway', 'member', 'committee')]
                for node in selected:
                    for kind, seconds in ([('cpu',6),('runtime',2)] if args.runtime_trace else [('cpu',6)]):
                        ready = threading.Event()
                        started.append(ready)
                        futures.append(pool.submit(capture, node, kind, seconds, ready))
                for ready in started:
                    if not ready.wait(5):
                        raise RuntimeError('profile requests failed to dispatch before chain')
                # Diagnostic-only lead-in gives the local servers time to begin
                # sampling. Profiles start after service readiness, before hop 1.
                time.sleep(.15)
                configuration['workload_dispatch_unix_ns'] = time.time_ns()
                configuration['workload_command'] = command
                runner.save(root/'configuration.json', configuration)
            return original_command(command, *positional, **keywords)

        runner.base.run_command = profiled_command
        workload = argparse.Namespace(bin=binary, run_dir=run, variant='c3', workload='chain',
            storage='memory', trace=True, length=1000, count=1, case='source-first',
            delay_ms=0, rate=100, duration=30, fixture=fixture, save_fixture=None)
        try:
            result = runner.run(workload)
            if not futures:
                raise RuntimeError('workload never reached profiling hook')
            runner.save(root/'diagnostic-result.json', {'diagnostic_only':True, **result})
        except Exception as exc:
            runner.save(root/'driver-error.json', {'error':repr(exc), 'diagnostic_only':True})
            raise
        finally:
            runner.base.run_command = original_command
            records = [future.result() for future in futures]
            runner.save(root/'profiles.json', records)
    print(json.dumps({'diagnostic_only':True, 'native_exit_code':result['native_exit_code'], 'profiles':records}))
    return result['native_exit_code'] or (1 if any(not record['ok'] for record in records) else 0)


if __name__ == '__main__':
    sys.exit(main())
