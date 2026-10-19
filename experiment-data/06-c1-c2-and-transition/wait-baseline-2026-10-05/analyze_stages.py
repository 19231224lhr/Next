"""Read-only deep audit of the nine current-build continuation runs.

Outputs go beside the results directory, never into raw evidence. Duration
totals use disjoint client critical-path segments. Follow timing is separate
because public observation runs concurrently with receipt certification.
"""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
from statistics import mean, median
from analyze import audit_run, read

MODES = ('fast', 'wallet-wait', 'member-wait')


def quantiles(values):
    values = sorted(values)
    assert values
    return {'n': len(values), 'mean': mean(values), 'p50': median(values),
            'p95': values[(95 * len(values) + 99) // 100 - 1],
            'min': values[0], 'max': values[-1]}


def commit_for(events, fact):
    matches = [e for e in events[bytes(fact).hex()] if e.get('CommittedUnixNS', 0)]
    assert matches and len({e['Height'] for e in matches}) == 1
    return min(matches, key=lambda e: e['CommittedUnixNS'])


def analyze_run(folder):
    basic, report = audit_run(folder)
    mode = folder.name.split('-', 1)[1]
    hops = report['Hops']
    wall_span = (hops[-1]['ReadyUnixNS'] - hops[0]['SentUnixNS']) / 1e6
    # UnixNS and monotonic elapsed are different clock domains. Retain their
    # observed discrepancy; never replace the reported monotonic endpoint.
    assert wall_span > 0
    totals = {'http_success_service_ms': 0., 'http_failed_service_ms': 0.,
              'retry_sleep_ms': 0., 'public_wait_99_ms': 0.,
              'member_gate_99_ms': 0., 'build_2_to_100_ms': 0.}
    failures = Counter()
    fast_sum = sum(h['FastMS'] for h in hops)
    for index, hop in enumerate(hops):
        attempts = hop['HTTPAttempts']
        for number, attempt in enumerate(attempts):
            assert attempt['StartedUnixNS'] <= attempt['DoneUnixNS'] <= hop['ReadyUnixNS']
            if number:
                assert attempts[number-1]['DoneUnixNS'] <= attempt['StartedUnixNS']
            key = 'http_failed_service_ms' if attempt.get('Error') else 'http_success_service_ms'
            totals[key] += attempt['ServiceMS']
            totals['retry_sleep_ms'] += attempt['RetrySleepMS']
            if attempt.get('Error'):
                failures[f"HTTP {attempt['HTTPStatus']}: {attempt.get('ResponseBody', '').strip()}"] += 1
        if index:
            previous = hops[index-1]
            assert previous['ReadyUnixNS'] <= hop['BuildStartUnixNS'] <= hop['BuildDoneUnixNS'] <= hop['SentUnixNS']
            totals['build_2_to_100_ms'] += hop['BuildMS']
            if mode != 'fast':
                assert previous['PublicWaitDoneUnixNS'] <= hop['BuildStartUnixNS']
                totals['public_wait_99_ms'] += previous['PublicWaitMS']
            if mode == 'member-wait':
                gate = hop['MemberInputGate']
                assert hop['BuildDoneUnixNS'] <= gate['StartedUnixNS'] <= gate['DoneUnixNS'] <= hop['SentUnixNS']
                totals['member_gate_99_ms'] += gate['MS']
    http_total = sum(totals[k] for k in ('http_success_service_ms', 'http_failed_service_ms', 'retry_sleep_ms'))
    totals['client_receipt_processing_and_dispatch_overhead_ms'] = fast_sum - http_total
    totals['other_driver_bookkeeping_and_scheduling_ms'] = report['FastChainMS'] - fast_sum - sum(
        totals[k] for k in ('public_wait_99_ms', 'member_gate_99_ms', 'build_2_to_100_ms'))
    assert totals['client_receipt_processing_and_dispatch_overhead_ms'] >= -.001
    assert totals['other_driver_bookkeeping_and_scheduling_ms'] >= -.001
    assert abs(sum(totals.values()) - report['FastChainMS']) < 1e-6
    residual = sum(totals[k] for k in ('build_2_to_100_ms',
                   'client_receipt_processing_and_dispatch_overhead_ms', 'other_driver_bookkeeping_and_scheduling_ms'))

    settlements = read(folder / 'settlements.json')
    assert set(settlements) == {'0', '1', '2', '3'}
    commits = [{bytes(h['Fact']).hex(): commit_for(settlements[str(replica)], h['Fact'])
                for h in hops} for replica in range(4)]
    leads = []
    for index, hop in enumerate(hops[1:], 1):
        parent = hops[index-1]
        parent_commits = [events[bytes(parent['Fact']).hex()]['CommittedUnixNS'] for events in commits]
        leads.append((min(parent_commits) - hop['SentUnixNS']) / 1e6)
        if mode == 'fast':
            assert hop['CertificateInput'] and leads[-1] > 0
        else:
            assert not hop['CertificateInput'] and leads[-1] < 0
    assert not hops[0]['CertificateInput']

    audit = read(folder / 'audit.json')
    chain_audit = read(folder / 'chain-audit.json')
    assert chain_audit['verified'] and chain_audit['hops'] == 100
    obligations = chain_audit['obligations'] or []
    hop_facts = {tuple(h['Fact']): h for h in hops}
    output_parent = {tuple(h['Output']): h for h in hops}
    for obligation in obligations:
        assert obligation['Status'] == 1 and obligation['Amount'] == 100
        parent = output_parent[tuple(obligation['Output'])]
        child = hop_facts[tuple(obligation['Consumer'])]
        assert child['Hop'] == parent['Hop'] + 1
        assert obligation['Certificate'] == parent['Fact']
        assert obligation['Transaction'] == child['Tx']
    committees = [a for a in audit if a['Name'].startswith('committee')]
    assert len(committees) == 4 and len({a['StateHash'] for a in committees}) == 1
    network = read(folder / 'network.json')
    genesis = network['Genesis']
    cal_total = sum(o['Output']['Amount'] for o in genesis['Outputs'] if o['Output']['Asset'] == 1)
    cal_total += sum(g['Amount'] for g in genesis['Grants'] if g['Key']['Kind'] == 1)
    fuel_total = sum(o['Output']['Amount'] for o in genesis['Outputs'] if o['Output']['Asset'] == 2)
    for committee in committees:
        recovery = committee['Recovery']
        assert committee['Payments'] == committee['Closed'] == 100 and committee['Gap'] == '0'
        assert int(committee['CAL']) == cal_total and int(committee['FUEL']) == fuel_total
        assert int(committee['Rewards']) == 8400 and int(committee['Burned']) == 1000
        assert recovery['Fulfilled'] == len(obligations)
        assert all(recovery[k] == 0 for k in ('Open', 'Repaired', 'Recovered', 'PaidCAL', 'RecoveredCAL'))
        assert all(r['Spent'] == r['Reserved'] == 0 for r in recovery['Reserves'])
    assert all(a.get('Pending', 0) == 0 for a in audit)
    assert all(h['FinalUnixNS'] > h['ReadyUnixNS'] and h['MemberClosedUnixNS'] > 0
               and h['ProgressErrors'] == 0 for h in hops)
    assert max(h['MemberOffsetMS'] for h in hops) == report['ClosedChainMS']

    followers = read(folder / 'chain-follow-timing.json')
    assert len(followers) == 3
    assert all(len({r['Height'] for r in rows}) == len(rows) for rows in followers)
    followers = [{r['Height']: r for r in rows} for rows in followers]
    follow_rows = []
    for index, hop in enumerate(hops):
        fact = bytes(hop['Fact']).hex()
        event = commits[0][fact]
        assert len({replica[fact]['Height'] for replica in commits}) == 1
        follow = followers[hop['Receiver']][event['Height']]
        assert follow['FetchStartNS'] <= follow['FetchDoneNS'] <= follow['VerifiedNS'] <= follow['CommittedNS']
        boundaries = {'committee0_commit_to_complete_fetch_ms': (event['CommittedUnixNS'], follow['FetchDoneNS']),
                      'complete_fetch_to_verified_ms': (follow['FetchDoneNS'], follow['VerifiedNS']),
                      'verified_to_local_record_callback_ms': (follow['VerifiedNS'], follow['CommittedNS']),
                      'callback_to_driver_observation_ms': (follow['CommittedNS'], hop['FinalUnixNS'])}
        row = {'hop': index+1, 'height': event['Height'], 'receiver': hop['Receiver'],
               'fetch_attempts': follow['Attempts']}
        row.update({k: (end-start)/1e6 for k, (start, end) in boundaries.items()})
        # Preserve signed callback-to-driver values. Its callback is immediately
        # after commit, but the driver's independent read can precede scheduling.
        assert row['committee0_commit_to_complete_fetch_ms'] >= 0
        follow_rows.append(row)
    metrics = sorted(k for k in follow_rows[0] if k.endswith('_ms'))
    return {'case': folder.name, 'mode': mode, 'chain_ms': report['FastChainMS'],
            'attempts': basic['attempts'], 'failed_attempts': basic['failed_attempts'],
            'literal_failure_counts': dict(failures), 'disjoint_stage_totals_ms': totals,
            'remaining_construction_recording_and_scheduling_ms': residual,
            'wall_span_minus_monotonic_chain_ms': wall_span-report['FastChainMS'],
            'fast_ms_quantiles': quantiles([h['FastMS'] for h in hops]),
            'build_ms_all_100_quantiles': quantiles([h['BuildMS'] for h in hops]),
            'build_ms_measured_99_quantiles': quantiles([h['BuildMS'] for h in hops[1:]]),
            'early_send': {'n': 99, 'positive_leads': sum(x > 0 for x in leads),
                           'lead_ms_quantiles': quantiles(leads)},
            'actual_fulfilled_obligations': len(obligations),
            'state_audit': {'replicas': 4, 'hashes_equal_within_run': True, 'payments': 100,
                           'closed': 100, 'CAL': cal_total, 'FUEL': fuel_total,
                           'rewards': 8400, 'burned': 1000, 'fuel_spent': 9400,
                           'outboxes_empty': True, 'compensation_CAL': 0},
            'public_last_observation_ms': report['PublicChainMS'],
            'all_member_closure_ms': report['ClosedChainMS'],
            'request_bytes_by_hop': [h['RequestBytes'] for h in hops],
            'certificate_bytes_distinct': sorted({h['CertificateBytes'] for h in hops}),
            'follow_quantiles': {k: quantiles([r[k] for r in follow_rows]) for k in metrics},
            'follow_rows': follow_rows}


def main(results, output):
    manifest = read(results / 'manifest.json')
    for name, digest in manifest.items():
        path = results.parent / name
        assert path.resolve().is_relative_to(results.resolve())
        assert hashlib.sha256(path.read_bytes()).hexdigest() == digest
    runs = [analyze_run(results / f'r{repeat}-{mode}') for repeat in range(1, 4) for mode in MODES]
    configurations = []
    networks = []
    for run in runs:
        folder = results / run['case']
        network = read(folder / 'network.json')
        network.pop('GenesisTime')
        networks.append(network)
        config = read(folder / 'configuration.json')
        assert config['nodes'] == 9 and config['hops'] == 100
        assert config['owner_fuel'] and config['member_sync'] and config['wallet_no_sync']
        assert config['record_chain_attempts'] and config['wait_final'] == (run['mode'] != 'fast')
        assert config['wait_member_inputs'] == (run['mode'] == 'member-wait')
        configurations.append({k: config[k] for k in ('env', 'member_GOMAXPROCS', 'member_GOGC')})
    assert all(n == networks[0] for n in networks)
    assert all(c == configurations[0] for c in configurations)
    summaries = {}
    for mode in MODES:
        selected = [r for r in runs if r['mode'] == mode]
        summary = {'chain_ms': quantiles([r['chain_ms'] for r in selected]),
                   'median_stage_total_ms': {k: median(r['disjoint_stage_totals_ms'][k] for r in selected)
                                            for k in selected[0]['disjoint_stage_totals_ms']},
                   'median_remaining_construction_recording_and_scheduling_ms': median(
                       r['remaining_construction_recording_and_scheduling_ms'] for r in selected),
                   'attempts_total': sum(r['attempts'] for r in selected),
                   'failed_attempts_total': sum(r['failed_attempts'] for r in selected),
                   'median_public_last_observation_ms': median(r['public_last_observation_ms'] for r in selected),
                   'median_all_member_closure_ms': median(r['all_member_closure_ms'] for r in selected),
                   'actual_fulfilled_obligations_by_run': [r['actual_fulfilled_obligations'] for r in selected]}
        for key in ('fast_ms_quantiles', 'build_ms_all_100_quantiles', 'build_ms_measured_99_quantiles'):
            summary['median_run_' + key] = {q: median(r[key][q] for r in selected) for q in ('mean', 'p50', 'p95', 'min', 'max')}
        summary['median_run_follow_quantiles'] = {metric: {q: median(r['follow_quantiles'][metric][q] for r in selected)
                                                        for q in ('mean', 'p50', 'p95', 'min', 'max')}
                                                 for metric in selected[0]['follow_quantiles']}
        summaries[mode] = summary
    paired = []
    for repeat in range(1,4):
        selected = {r['mode']: r for r in runs if r['case'].startswith(f'r{repeat}-')}
        paired.append({'repeat': repeat, 'wallet_wait_over_fast': selected['wallet-wait']['chain_ms']/selected['fast']['chain_ms'],
                       'member_wait_over_fast': selected['member-wait']['chain_ms']/selected['fast']['chain_ms'],
                       'member_wait_minus_wallet_wait_ms': selected['member-wait']['chain_ms']-selected['wallet-wait']['chain_ms']})
    result = {'build': read(results / 'build.json')['commit'], 'runs': runs, 'modes': summaries,
              'matched_configuration': {'same_network_except_genesis_time': True,
                                        'same_runtime_and_storage': True, 'configuration': configurations[0]},
              'paired_comparisons': paired,
              'ratios_of_chain_medians': {mode + '_over_fast': summaries[mode]['chain_ms']['p50']/summaries['fast']['chain_ms']['p50']
                                         for mode in MODES[1:]},
              'global_checks': {'raw_manifest_hashes_verified': len(manifest), 'payments_closed': 900,
                                'fast_successors_sent_before_earliest_parent_commit': 297,
                                'minimum_fast_send_lead_ms': min(r['early_send']['lead_ms_quantiles']['min'] for r in runs if r['mode']=='fast'),
                                'actual_fulfilled_obligations': sum(r['actual_fulfilled_obligations'] for r in runs),
                                'member_wait_failed_attempts': summaries['member-wait']['failed_attempts_total'],
                                'wallet_wait_failed_attempts': summaries['wallet-wait']['failed_attempts_total']},
              'aggregation': 'Per-run phase duration sums; per-mode median of three run sums or run quantiles. Phase medians and percentiles must not be added.',
              'critical_path': 'Sent1 to Ready100; excludes Build1, initial bootstrap gate and final public/member observation. Only 99 public waits and 99 successor member gates counted.',
              'follow_scope': 'Separate concurrent observation analysis, not additive critical-path stages. committee0 actual fetch source; complete BlockData includes next-height signed header plus block/results. No earliest-availability or pure-polling inference; signed callback-to-driver differences preserved.',
              'failure_scope': 'Literal gateway errors; no inference of specific principal/fee ErrMissing check.',
              'raw_evidence_sha256': {str(p.relative_to(results)): hashlib.sha256(p.read_bytes()).hexdigest()
                                      for p in sorted(results.rglob('*')) if p.is_file() and p.name != 'member-gate-analysis.json'}}
    output.write_text(json.dumps(result, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'output': str(output), 'modes': summaries}, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('results', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    destination = args.output or args.results.parent / 'stage-analysis.json'
    assert args.results.resolve() not in destination.resolve().parents
    main(args.results, destination)
