"""Recompute mixed-load tables and check paths from archived observations."""
import argparse
import csv
import gzip
import hashlib
import json
import math
from pathlib import Path
from statistics import median


def read(path):
    data = path.read_bytes() if path.exists() else gzip.decompress(path.with_suffix(path.suffix + '.gz').read_bytes())
    return json.loads(data)


def lines(path):
    data = path.read_bytes() if path.exists() else gzip.decompress(path.with_suffix(path.suffix + '.gz').read_bytes())
    return [json.loads(x) for x in data.decode().splitlines()]


def q(values, percentile):
    values = sorted(values)
    return values[max(0, math.ceil(len(values) * percentile) - 1)]


def table(path, rows):
    with path.open('w', newline='') as f:
        w = csv.DictWriter(f, list(rows[0]), lineterminator='\n')
        w.writeheader(); w.writerows(rows)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('results', type=Path)
    args = ap.parse_args()
    root = args.results
    manifest = read(root.parent / 'manifest.json')
    for entry in manifest.values():
        data = (root.parent / entry['stored']).read_bytes()
        if entry['gzip']:
            data = gzip.decompress(data)
        assert len(data) == entry['bytes'] and hashlib.sha256(data).hexdigest() == entry['sha256']
    genesis = read(root / 'genesis-template.json')
    initial = {a: sum(x['Output']['Amount'] for x in genesis['Genesis']['Outputs'] if x['Output']['Asset'] == a)
               + sum(x['Balance'] for x in genesis['Accounts'] if x['Asset'] == a) for a in (1, 2)}
    org = genesis['Organizations'][0]['Org']
    initial_reserves = {x['Owner']: x['Balance'] for x in genesis['Accounts'] if x['Asset'] == 1}
    cases, windows, targets, retained, resources = [], [], [], [], []
    user_balances = None
    for folder in sorted(root.glob('r[123]-*')):
        config = read(folder / 'configuration.json')
        mode, pairs, count = config['mode'], config['pairs'], config['normal_count']
        benchmark = read(folder / 'bench-v4-100.json')
        samples, summary = benchmark['Samples'], benchmark['Summary']
        assert len(samples) == count and all(s['Outcome'] == 'COMPLETE' for s in samples)
        assert summary['failed'] == summary['unknown'] == summary['not_sent'] == summary['final_unfinished'] == 0
        assert len({s['Fact'] for s in samples}) == count
        start, last = min(s['SentUnixNS'] for s in samples), max(s['SentUnixNS'] for s in samples)
        done = max(s['TaskDoneUnixNS'] for s in samples)
        audit = read(folder / 'audit.json')
        nodes = [a for a in audit if a['Name'].startswith('committee')]
        assert len(nodes) == 4 and len({a['StateHash'] for a in nodes}) == 1
        for a in nodes:
            assert a['Payments'] == a['Closed'] == count + 2 * pairs and a['Gap'] == '0'
            assert int(a['CAL']) == initial[1] and int(a['FUEL']) == initial[2]
            recovery = a['Recovery']
            assert recovery['Open'] == recovery['Repaired'] == 0
            assert recovery['PaidCAL'] == recovery['RecoveredCAL'] == (pairs * 100 if mode in 'CP' else 0)
            assert recovery['Recovered'] == (pairs if mode in 'CP' else 0)
            if mode == 'R':
                assert recovery['Fulfilled'] == pairs
            for reserve in recovery['Reserves']:
                assert reserve['Balance'] == initial_reserves[reserve['Organization']]
                assert reserve['Reserved'] == reserve['Spent'] == 0
        assert all(a['Pending'] == 0 for a in audit)
        if user_balances is None:
            user_balances = nodes[0]['Recovery']['UserCAL']
        assert all(a['Recovery']['UserCAL'] == user_balances for a in nodes), 'treatments changed final user CAL allocation'
        for a in audit:
            if a['Name'].startswith(('org', 'gateway')):
                retained.append({'case': folder.name, 'node': a['Name'],
                    'approvals': a['Approvals'], 'payment_records': a.get('RetainedPayments', 0),
                    'payment_bytes': a.get('RetainedPaymentBytes', 0),
                    'own_certificate_bytes': a.get('RetainedCertificateBytes', 0)})
        eventmap = read(folder / 'events.json')
        status = read(folder / 'final-status.json')
        resume = read(folder / 'resume.json')['unix_ns'] if mode == 'P' else None
        heights = {x['health']['value']['height'] for pair in status.values() for x in pair}
        assert len(heights) == 1, 'final observations must share one quiescent height'
        if mode == 'P':
            paused = read(folder / 'paused-observations.json')
            assert len(paused) == pairs
            for pair in paused:
                assert len(pair['nodes']) == 4
                for n in pair['nodes']:
                    assert n['obligation']['value']['Status'] == 3
                    assert not n['repair']['value']['Committed'] and not n['repair']['value']['Materialized']
                    assert n['repair']['end_ns'] < resume
        for i in range(pairs):
            es = {e['stage']: e for e in eventmap[str(i)]}
            parent, child = es['parent_ready'], es['child_ready']
            assert start < parent['sent_unix_ns'] < last
            assert parent['unix_ns'] < child['sent_unix_ns']
            assert child['unix_ns'] <= es['child_public_observed']['unix_ns']
            record = {'case': folder.name, 'mode': mode, 'pair': i, 'parent_output': parent['output'],
                      'parent_ready_s': (parent['unix_ns'] - start) / 1e9,
                      'child_sent_s': (child['sent_unix_ns'] - start) / 1e9,
                      'child_ready_s': (child['unix_ns'] - start) / 1e9,
                      'child_public_s': (es['child_public_observed']['unix_ns'] - start) / 1e9,
                      'fulfilled_s': '', 'compensation_s': '', 'late_submit_s': '', 'repaid_s': '',
                      'resume_s': (resume - start) / 1e9 if resume else '', 'materialized_s': ''}
            if mode == 'R':
                assert 'late_source_submit_start' not in es and 'compensation_observed' not in es
                record['fulfilled_s'] = (es['source_fulfilled_observed']['unix_ns'] - start) / 1e9
            if mode in 'CP':
                stages = ['child_public_observed', 'compensation_observed', 'late_source_submit_start',
                          'source_repaid_observed', 'materialization_observed']
                ts = [es[s]['unix_ns'] for s in stages]
                assert ts == sorted(ts)
                assert start < ts[0] < ts[-1] < last, 'ordinary flow must span all fault stages'
                if resume:
                    assert ts[-2] < resume < ts[-1]
                for key, stage in [('compensation_s', stages[1]), ('late_submit_s', stages[2]),
                                   ('repaid_s', stages[3]), ('materialized_s', stages[4])]:
                    record[key] = (es[stage]['unix_ns'] - start) / 1e9
                ob = es['compensation_observed']['obligation']
                assert ob['Issuer'] == org and ob['Amount'] == 100 and ob['Status'] == 2
                assert es['compensation_observed']['unix_ns'] >= ob['Deadline'] * 10**9
            for n in status[str(i)]:
                if mode != 'N':
                    ob = n['obligation']['value']
                    assert ob['Issuer'] == org and ob['Amount'] == 100
                    assert ob['Status'] == (1 if mode == 'R' else 3)
                if mode in 'CP':
                    repair = n['repair']['value']
                    assert all(repair[k] for k in ['Committed', 'Materialized', 'IdentityStable', 'BytesChanged'])
            targets.append(record)
        for window in range(math.ceil((last - start) / 1e10)):
            ss = [s for s in samples if window * 10 <= (s['SentUnixNS'] - start) / 1e9 < (window + 1) * 10]
            if not ss:
                continue
            windows.append({'case': folder.name, 'mode': mode, 'start_s': window * 10, 'n': len(ss),
                'fast_p50_ms': q([s['FastMS'] for s in ss], .5), 'fast_p95_ms': q([s['FastMS'] for s in ss], .95),
                'fast_p99_ms': q([s['FastMS'] for s in ss], .99),
                'dispatch_p95_ms': q([s['DispatchLagMS'] for s in ss], .95),
                'public_p95_ms': q([s['BlockObservedMS'] for s in ss], .95)})
        observations = lines(folder / 'observations.jsonl')
        sampler_errors = sum(len(s['errors']) for s in observations)
        for name in [a['Name'] for a in audit if a['Name'].startswith('org')]:
            for kind in range(1, 6):
                series = [r for s in observations if name in s['nodes']
                          for r in s['nodes'][name]['value']['Resources'] if r['Key']['Kind'] == kind]
                resources.append({'case': folder.name, 'node': name, 'kind': kind,
                    'sampled_min_available': min(sum(x['Available'] for x in r['Slices']) for r in series),
                    'sampled_max_reserved': max(sum(x['Reserved'] for x in r['Slices']) for r in series)})
        final_resources = read(folder / 'final-resources.json')
        assert all(not any(s['value']['Limited']) for s in final_resources.values())
        for name, s in final_resources.items():
            if not name.startswith('org'):
                continue
            for resource in s['value']['Resources']:
                if resource['Key']['Kind'] in (1, 3, 4):
                    assert sum(x['Reserved'] for x in resource['Slices']) == 0
        # Recompute exact maximum outstanding ordinary jobs from measured timestamps.
        changes = [(s['SentUnixNS'], 1) for s in samples] + [(s['TaskDoneUnixNS'], -1) for s in samples]
        pending = peak = 0
        for _, d in sorted(changes):
            pending += d; peak = max(peak, pending)
        cases.append({'case': folder.name, 'mode': mode, 'count': count, 'payments': count + 2 * pairs,
            'fast_p50_ms': q([s['FastMS'] for s in samples], .5),
            'fast_p95_ms': q([s['FastMS'] for s in samples], .95),
            'fast_p99_ms': q([s['FastMS'] for s in samples], .99),
            'public_p50_ms': q([s['BlockObservedMS'] for s in samples], .5),
            'public_p95_ms': q([s['BlockObservedMS'] for s in samples], .95),
            'member_p95_ms': q([s['MemberAppliedMS'] for s in samples], .95),
            'dispatch_p95_ms': q([s['DispatchLagMS'] for s in samples], .95),
            'send_span_s': (last-start)/1e9, 'drain_after_last_send_s': (done-last)/1e9,
            'ordinary_complete_s': (done-start)/1e9, 'peak_sent_unfinished': peak,
            'sampled_oldest_ms': summary['sampled_oldest_unfinished_max_ms'],
            'total_limit_hits': summary['total_limit_hits'], 'send_limit_hits': summary['send_limit_hits'],
            'sampler_errors': sampler_errors, 'cal_paid': nodes[0]['Recovery']['PaidCAL'],
            'cal_recovered': nodes[0]['Recovery']['RecoveredCAL'],
            'rewards': int(nodes[0]['Rewards']), 'burned': int(nodes[0]['Burned']),
            'fee_paid': int(nodes[0]['Rewards']) + int(nodes[0]['Burned']),
            'committee_height': next(iter(heights))})
    assert len(cases) == 12
    for name, rows in [('runs', cases), ('windows', windows), ('targets', targets),
                       ('retained', retained), ('resources', resources)]:
        table(root.parent / (name + '.csv'), rows)
    grouped = {}
    for mode in 'NRCP':
        rows = [r for r in cases if r['mode'] == mode]
        assert len(rows) == 3
        grouped[mode] = {k: {'median': median(r[k] for r in rows),
                            'min': min(r[k] for r in rows), 'max': max(r[k] for r in rows)}
                         for k in rows[0] if k not in ('case', 'mode', 'committee_height')}
    result = {'runs': cases, 'groups': grouped, 'normal_payments': sum(r['count'] for r in cases),
              'verified_artifacts': len(manifest),
              'total_payments': sum(r['payments'] for r in cases),
              'recovered_sources_R': sum(r['mode'] == 'R' for r in targets),
              'compensated_and_repaid_sources_CP': sum(r['mode'] in 'CP' for r in targets),
              'checks': 'All per-run sample, path, replica, overlap and accounting assertions passed.'}
    (root.parent / 'analysis.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
