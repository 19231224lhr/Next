#!/usr/bin/env python3
"""Bounded current-build CAL experiment; reuse E2 payment and audit paths."""
import argparse
import csv
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import time

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
DEPENDENCIES = [
    'docs/experiments/owner-fuel-2026-09-23/reproduce.py',
    'docs/experiments/finite-budget-2026-09-23/reproduce.py',
    'docs/experiments/finite-budget-2026-09-23/analyze.py',
    'docs/experiments/continuous-respending-2026-09-22/reproduce.py',
]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n', encoding='utf-8')


def source_hashes():
    files = [ROOT / 'go.mod', ROOT / 'go.sum']
    for directory in ['cmd', 'internal', 'protocol', 'crypto', 'finality']:
        files.extend((ROOT / directory).rglob('*.go'))
    files.extend(p for p in (ROOT / 'third_party/cometbft').rglob('*')
                 if p.is_file() and '__pycache__' not in p.parts)
    files.extend(ROOT / p for p in DEPENDENCIES)
    files.extend(HERE / p for p in ['run.py', 'freeze.py', 'README.md'])
    return {p.relative_to(ROOT).as_posix(): sha(p) for p in sorted(set(files))}


def comet_hashes():
    return {p.relative_to(ROOT).as_posix(): sha(p)
            for p in sorted((ROOT / '.scratch/comet-src').rglob('*'))
            if p.is_file() and not any(x in p.parts for x in ['.git', '__pycache__', 'bin'])}


def analyze(base, dest):
    summary, curves = base.analyze_case(dest)
    # The historical analyzer only exports kinds 1–3. Include Bytes (kind 4)
    # from the same raw snapshots with the same curve columns and sampling times.
    report = base.read(dest / 'reports/budget-v4.json')
    byte_rows = []
    for line in base.lines(dest / 'samples.jsonl'):
        sample = json.loads(line)
        snapshot = sample.get('snapshot')
        if not snapshot:
            continue
        for resource in snapshot['Resources']:
            if resource['Key']['Kind'] != 4:
                continue
            debit = next((d for d in snapshot.get('Detail', {}).get('Debits', [])
                          if d['Key'] == resource['Key']), None)
            row = dict.fromkeys(curves[0])
            row.update(case=dest.name, node=sample['node'], kind=4,
                       seconds=(snapshot['UnixNS'] - report['StartedUnixNS']) / 1e9,
                       grant=resource['Grant'],
                       available=sum(s['Available'] for s in resource.get('Slices', [])),
                       reserved=sum(s['Reserved'] for s in resource.get('Slices', [])),
                       public_reserved=resource['Usage']['Reserved'],
                       public_spent=resource['Usage']['Spent'],
                       limited=snapshot['Limited'][4], read_ms=snapshot['ReadMS'],
                       released_total=debit['Released'] if debit else None,
                       net_spent=debit['NetSpent'] if debit else None,
                       temporary_reserved=(debit['Charged'] - debit['Released'] - debit['NetSpent'])
                                          if debit else None)
            byte_rows.append(row)
    curves.extend(byte_rows)
    public_bytes = [r for r in byte_rows if r['node'] == 'committee0']
    summary['occupancy']['4'] = {
        'public_reserved_mean_input_window': base.sampled_mean(
            [(r['seconds'], r['public_reserved']) for r in public_bytes], report['DurationSeconds']),
        'public_reserved_max': max((r['public_reserved'] for r in public_bytes), default=0),
        'max_member_reserved': max((r['reserved'] for r in byte_rows
                                   if r['node'].startswith('org0-member')), default=0),
    }
    audit = base.read(dest / 'reports/budget-audit.json')
    public = [p for p in audit['Payments'] if p['Public']]
    assert all(p['FeeSource'] == 2 for p in public), 'non-user fee source'
    assert all(p['Refund'] == 0 or p['WalletRefundFinal'] for p in public), 'nonfinal refund'
    summary['user_fee_actual'] = summary['fee']['Rewards'] + summary['fee']['Burned']
    summary['organization_FUEL_debit'] = 0
    summary['user_fee_inputs'] = sum(p['FeeInputAmount'] for p in public)
    summary['user_change'] = sum(p['Change'] for p in public)
    summary['user_refund'] = sum(p['Refund'] for p in public)
    assert summary['user_fee_inputs'] == (summary['user_change'] + summary['user_refund']
            + summary['user_fee_actual'] + summary['fee']['Held']), 'user fee conservation'
    assert summary['final_actual_FUEL_balance'] == 0, 'organization sponsor balance'
    if all('FeeLockedMembers' in p for p in audit['Payments']):
        locked = {tuple(p['FeeInput']): p for p in audit['Payments']
                  if not p['Public'] and p['FeeLockedMembers'] > 0}
        summary['nonpublic_fee_inputs_locked_by_members'] = len(locked)
        summary['nonpublic_fee_amount_locked_by_members'] = sum(p['FeeInputAmount'] for p in locked.values())
    # Expectations are reported, never used to discard a genuine stressed outcome.
    summary['all_offered_closed'] = summary['closed_units'] == summary['offered_units']
    summary['no_partial_residual'] = summary['partial_approval_payments'] == 0
    summary['sampling_errors'] = summary['sampling']['errors']
    summary['incomplete_units_at_bounded_stop'] = summary['admitted_units'] - summary['closed_units']
    summary['unfinished_times_are_right_censored'] = True
    write(dest / 'analysis.json', summary)
    return summary, curves


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True, help='new results directory; refuses overwrite')
    parser.add_argument('--case', choices=['pilot', 'matrix', 'all'], default='all')
    parser.add_argument('--repeats', type=int, choices=[1, 2, 3], default=1)
    args = parser.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    owner = load('current_capital_owner', ROOT / DEPENDENCIES[0])
    base = load('current_capital_analysis', ROOT / DEPENDENCIES[2])
    e2 = owner.e2
    binaries = out / 'bin'
    runtime_root = out / 'runtime'
    template = runtime_root / 'pristine'
    runtime_root.mkdir()
    e2.OUT = out
    e2.BIN = binaries
    e2.labutil.OUT = out
    e2.labutil.BIN = binaries
    # Clear inherited experimental knobs, then explicitly freeze this experiment.
    for key in list(e2.ENV):
        if key.startswith('UTXO_') or key in ['GODEBUG', 'GOMAXPROCS', 'GOGC']:
            del e2.ENV[key]
    e2.ENV.update({
        'UTXO_EXPERIMENT_COMMIT': '250ms', 'UTXO_EXPERIMENT_FLUSH': '10ms',
        'UTXO_EXPERIMENT_GOSSIP': '10ms', 'UTXO_EXPERIMENT_BUDGET': '1',
        'UTXO_EXPERIMENT_MEM_BLOCKSTORE': '1', 'UTXO_EXPERIMENT_COMMITTEE_MEMORY': '1',
        'UTXO_EXPERIMENT_MEMBER_MEMORY': '1', 'UTXO_EXPERIMENT_GATEWAY_MEMORY': '1',
        'UTXO_SETTLEMENT_TRACE': '1', 'UTXO_EXPERIMENT_SERIAL_DIRECT': '0',
        'UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY': '1',
    })
    frozen = source_hashes()
    manifest = {
        'source_sha256': frozen, 'platform': platform.platform(), 'python': sys.version,
        'environment': {k: v for k, v in e2.ENV.items() if k.startswith('UTXO_')
                        or k in ['GODEBUG', 'GOMAXPROCS', 'GOGC']},
        'member_wrapper': {'GOMAXPROCS': '16', 'GOGC': '200'},
        'plan': {'case': args.case, 'repeats': args.repeats, 'rate_units_s': 20,
                 'duration_s': 60, 'drain_s': 60, 'pending': 128,
                 'CAL_grants': [3600, 28800], 'parent_delays_s': [1, 3],
                 'origin_count': 1200, 'principal_per_origin': 100,
                 'fee_origins_per_payer': 1200, 'FUEL_per_origin': 10000,
                 'automatic_source_recovery': False},
    }
    try:
        manifest['head'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        manifest['git_status'] = subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True)
        (out / 'working-tree.patch').write_bytes(subprocess.check_output(
            ['git', 'diff', '--binary', '--', 'cmd', 'internal', 'crypto', 'finality',
             'protocol', 'third_party', 'go.mod', 'go.sum',
             'docs/experiments/current-capital-2026-10-05',
             'docs/experiments/wait-baseline-2026-10-05'], cwd=ROOT))
    except (OSError, subprocess.CalledProcessError):
        manifest['head'] = 'source bundle without Git metadata; use source_sha256'
    write(out / 'manifest.json', manifest)
    # Frozen bundles intentionally have no .git: use byte hashes, not the
    # historical builder's mandatory Git calls.
    binaries.mkdir()
    overlay_root = ROOT / '.scratch/comet-src'
    manifest['materialized_comet_prebuild_go_sha256'] = {
        p.relative_to(ROOT).as_posix(): sha(p) for p in sorted(overlay_root.rglob('*.go'))}
    manifest['materialized_comet_prebuild_files_sha256'] = comet_hashes()
    with (out / 'build.log').open('w') as log:
        e2.command(['python3', ROOT / 'third_party/cometbft/overlay.py'], log)
        for role in ['payctl', 'committee', 'gateway', 'member']:
            target = binaries / ('member-real' if role == 'member' else role)
            e2.command(['go', 'build', '-tags=comet_v3', '-o', target, './cmd/' + role], log)
    wrapper = binaries / 'member'
    wrapper.write_text('#!/bin/sh\nexec env GOMAXPROCS=16 GOGC=200 "'
                       + str(binaries / 'member-real') + '" "$@"\n')
    wrapper.chmod(0o755)
    manifest['go_version'] = subprocess.check_output(['go', 'version'], env=e2.ENV, text=True).strip()
    manifest['binaries'] = {p.name: sha(p) for p in binaries.iterdir() if p.is_file()}
    manifest['materialized_comet_go_sha256'] = {
        p.relative_to(ROOT).as_posix(): sha(p) for p in sorted(overlay_root.rglob('*.go'))}
    manifest['materialized_comet_files_sha256'] = comet_hashes()
    before = manifest['materialized_comet_prebuild_go_sha256']
    after = manifest['materialized_comet_go_sha256']
    manifest['overlay_changed_go_paths'] = sorted(
        p for p in set(before) | set(after) if before.get(p) != after.get(p))
    assert source_hashes() == frozen, 'source changed during build'
    write(out / 'manifest.json', manifest)

    def configure(label, count, kind=None, grant=None):
        dest = out / label
        dest.mkdir()
        runtime = runtime_root / label
        with (dest / 'setup.log').open('w') as log:
            if not template.exists():
                e2.command([binaries / 'payctl', 'init-lab', '-dir', template,
                            '-outputs', 1200, '-port', 27000, '-v4'], log)
                e2.command([binaries / 'payctl', 'chain-v4', '-dir', template, '-prepare'], log)
                (template / 'prepared').write_text('fresh current-capital; no nodes booted\n')
            assert (template / 'prepared').exists()
            shutil.copytree(template, runtime)
        for path in [runtime / 'lab.json', *list((runtime / 'config').glob('*.json'))]:
            path.write_text(path.read_text().replace(str(template), str(runtime)))
        lab = json.loads((runtime / 'lab.json').read_text())
        lab['Nodes'] = [n for n in lab['Nodes'] if n['Binary'] == 'committee'
                        or n['Name'] == 'gateway0' or n['Name'].startswith('org0-member')]
        assert len(lab['Nodes']) == 9
        write(runtime / 'lab.json', lab)
        network = json.loads(Path(lab['Network']).read_text())
        network['GenesisTime'] = (datetime.now(timezone.utc) - timedelta(seconds=1)).isoformat()
        org = network['Organizations'][0]['Org']
        config = network['Genesis']['Grants'][0]['Organization']
        cal = [g for g in network['Genesis']['Grants']
               if g['Organization'] == config and g['Key']['Kind'] == 1]
        assert len(cal) == 1
        cal[0]['Amount'] = grant
        accounts = [a for a in network['Accounts'] if a['Owner'] == org and a['Asset'] == 1]
        assert len(accounts) == 1
        accounts[0]['Balance'] = grant
        for g in network['Genesis']['Grants']:
            if g['Organization'] == config and g['Key']['Kind'] in [3, 4]:
                assert g['Amount'] >= 10**12, 'other budget not ample'
        for path in (runtime / 'config').glob('*.json'):
            node = json.loads(path.read_text())
            if 'Workers' in node:
                node['Workers'] = 1
                write(path, node)
        write(Path(lab['Network']), network)
        with (dest / 'owner-fuel-setup.log').open('w') as log:
            e2.command([binaries / 'payctl', 'budget-v4', '-dir', runtime, '-prepare-owner-fuel'], log)
        network = json.loads(Path(lab['Network']).read_text())
        assert all(g['Key']['Kind'] not in [2, 5] for g in network['Genesis']['Grants'])
        assert all(a['Asset'] != 2 for a in network['Accounts'])
        principal = [o for o in network['Genesis']['Outputs'] if o['Output']['Asset'] == 1]
        fuel = [o for o in network['Genesis']['Outputs'] if o['Output']['Asset'] == 2]
        assert all(o['Output']['Amount'] == 100 for o in principal)
        assert len(fuel) == 2400 and all(o['Output']['Amount'] == 10000 for o in fuel)
        encoded = json.dumps(network, separators=(',', ':')) + '\n'
        Path(lab['Network']).write_text(encoded)
        (dest / 'network.json').write_text(encoded)
        write(dest / 'environment.json', {k: v for k, v in e2.ENV.items()
              if k.startswith('UTXO_') or k in ['GODEBUG', 'GOMAXPROCS', 'GOGC']})
        return dest, runtime, lab, network

    e2.configure = configure
    summaries, curves = [], []
    failures = []
    started = time.monotonic()

    def run(label, grant, delay, duration, count=0, repetition=0):
        assert source_hashes() == frozen, 'source changed between runs'
        try:
            dest = owner.run(label, duration=duration, delay=delay, kind=1,
                             grant=grant, count=count, drain=60, serial=False)
            summary, rows = analyze(base, dest)
            summary['repetition'] = repetition
            summary['pilot'] = count == 1
            summaries.append(summary)
            curves.extend(rows)
            write(dest / 'analysis.json', summary)
            write(out / 'analysis.json', summaries)
            return summary
        except Exception as error:
            failures.append({'case': label, 'type': type(error).__name__, 'message': str(error)})
            write(out / 'runner-failures.json', failures)
            raise

    if args.case in ['pilot', 'all']:
        pilot = run('pilot-high-delay3', 28800, 3, 5, count=1)
        assert pilot['actual_obligations'] == 1 and pilot['closed_units'] == 1
        assert pilot['occupancy']['1']['public_reserved_max'] == 100
        assert pilot['occupancy']['1']['max_member_reserved'] >= 100
    if args.case in ['matrix', 'all']:
        cells = [('low-normal', 3600, 1), ('high-normal', 28800, 1),
                 ('low-delay', 3600, 3), ('high-delay', 28800, 3)]
        write(out / 'cell-order.json', [
            {'repetition': r, 'cells': cells if r % 2 else list(reversed(cells))}
            for r in range(1, args.repeats + 1)])
        for repeat in range(1, args.repeats + 1):
            order = cells if repeat % 2 else list(reversed(cells))
            for label, grant, delay in order:
                run(f'r{repeat}-{label}', grant, delay, 60, repetition=repeat)
    fields = ['case', 'repetition', 'pilot', 'grant', 'delay_s', 'offered_units',
              'admitted_units', 'closed_units', 'not_started', 'public_payments',
              'actual_obligations', 'actual_obligation_fraction', 'partial_approval_payments',
              'user_fee_actual', 'organization_FUEL_debit', 'elapsed_s', 'all_offered_closed']
    with (out / 'summary.csv').open('w', newline='') as handle:
        writer = csv.DictWriter(handle, fieldnames=fields)
        writer.writeheader()
        writer.writerows({k: row[k] for k in fields} for row in summaries)
    if curves:
        with (out / 'curves.csv').open('w', newline='') as handle:
            writer = csv.DictWriter(handle, fieldnames=list(curves[0]))
            writer.writeheader()
            writer.writerows(curves)
    assert source_hashes() == frozen, 'source changed during experiment'
    write(out / 'completion.json', {'wall_seconds_including_cases': time.monotonic() - started,
          'cases': len(summaries), 'failures': failures,
          'high_arm_incomplete': [s['case'] for s in summaries if s['grant'] == 28800
                                 and not s['all_offered_closed']]})


if __name__ == '__main__':
    main()
