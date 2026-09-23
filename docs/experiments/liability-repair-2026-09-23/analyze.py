#!/usr/bin/env python3
"""Keep offered denominators and failures; timestamps are observations, not proofs."""
import argparse
import json
from pathlib import Path


def quantiles(values):
    values = sorted(values)
    return {'n': len(values), **{name: values[int((len(values)-1)*q)] if values else None
            for name, q in [('p50', .5), ('p95', .95), ('max', 1)]}}


def analyze(path):
    read = lambda name: json.loads((path/name).read_text())
    report = read('reports/budget-v4.json')
    rows = read('reports/budget-audit.json')['Payments']
    audit = read('reports/audit.json')
    committee = [n for n in audit if n['Name'].startswith('committee')]
    snapshots = read('last-snapshots.json')
    final = snapshots['committee0']
    config = read('e3-config.json')
    units = report['Units']
    selected = report.get('RepairSelected') or []
    anomalies = [u for u in units if u['RepairExpected']]
    completed = [u for u in units if all(u[h]['MemberClosedUnixNS'] for h in ['Parent', 'Child'])]
    errors, gates, physical_delays, commit_delays, unfinished = [], [], [], [], []
    for u in units:
        for key in ['ParentError', 'ChildError', 'SubmitError']:
            if u.get(key): errors.append({'index': u['Index'], 'stage': key, 'error': u[key]})
        if not u['RepairExpected']:
            continue
        trial = u.get('Repair') or {}
        nodes = trial.get('Nodes') or []
        if not all(u[h]['MemberClosedUnixNS'] for h in ['Parent', 'Child']):
            if not trial.get('DeadlineUnix'):
                stage = 'no_anchored_deadline_observed'
            elif not any(n['CommittedUnixNS'] for n in nodes):
                stage = 'no_repair_commit_observed'
            elif len(nodes) != 4 or not all(n['MaterializedUnixNS'] for n in nodes):
                stage = 'physical_repair_incomplete'
            else:
                stage = 'late_parent_or_member_follow_incomplete'
            unfinished.append({'index': u['Index'], 'stage': stage,
                'deadline_age_s': max(0, report['StoppedUnixNS']/1e9-trial['DeadlineUnix']) if trial.get('DeadlineUnix') else None})
        done = (len(nodes) == 4 and all(n['MaterializedUnixNS'] and n['Snapshot']['Materialized']
                and n['Snapshot']['Committed'] and n['Snapshot']['IdentityStable'] and n['Snapshot']['BytesChanged'] for n in nodes))
        if done:
            latest = max(n['MaterializedUnixNS'] for n in nodes)
            earliest = min(n['CommittedUnixNS'] for n in nodes)
            gates.append(u['FirstSubmitUnixNS'] >= latest and 0 < u['Child']['FinalUnixNS'] < u['FirstSubmitUnixNS'])
            deadline = trial['DeadlineUnix']*1e9
            physical_delays.append((latest-deadline)/1e6)
            commit_delays.append((earliest-deadline)/1e6)
        else:
            gates.append(False)
    public = [p for p in rows if p['Public']]
    fee = {k: sum(p['Fee'][k] for p in public) for k in ['Maximum', 'Held', 'Rewards', 'Burned', 'Refunded']}
    repaired = final['Detail']['Repaired']
    cal_loss = config['cal_grant']-final['CAL']
    all_closed = len(completed) == report['Offered']
    checks = {
        'all_offered_started': report['NotStarted'] == 0,
        'all_units_closed': all_closed,
        'no_driver_errors': not errors,
        'all_selected_admitted': len(anomalies) == len(selected),
        'repair_count_matches_selected': repaired == len(selected),
        'physical_before_parent_and_child_already_final': all(gates),
        'committee_state_equal': len(committee) == 4 and len({n['StateHash'] for n in committee}) == 1,
        'gap_closed': all(int(n['Gap']) == 0 for n in committee),
        'all_public': len(public) == 2*report['Offered'],
        'cal_loss': cal_loss == 100*repaired,
        'no_organization_fuel': final['FUEL'] == 0 and all(p['FeeSource'] == 2 for p in public),
        'fee_conservation': fee['Maximum'] == sum(fee[k] for k in ['Held', 'Rewards', 'Burned', 'Refunded']),
        'owner_conservation': sum(p['FeeInputAmount']-p['Change']-p['Refund'] for p in public) == fee['Held']+fee['Rewards']+fee['Burned'],
        'all_fees_closed': fee['Held'] == 0 and all(p['Fee']['Closed'] for p in public),
        # This formula is evaluated only when the actual full workload closes.
        'full_fee_formula': all_closed and fee['Rewards']+fee['Burned'] == 188*len(completed)+5*repaired,
    }
    timing = {}
    for exceptional in [False, True]:
        group = [u for u in units if u['RepairExpected'] == exceptional]
        for hop in ['Parent', 'Child']:
            timing[f'{"exceptional" if exceptional else "normal"}_{hop.lower()}_fast_ms'] = quantiles([u[hop]['FastMS'] for u in group if u[hop]['ReadyUnixNS']])
    timing['deadline_to_first_commit_observed_ms'] = quantiles(commit_delays)
    timing['deadline_to_all_physical_observed_ms'] = quantiles(physical_delays)
    result = {'case': path.name, 'passed': all(checks.values()), 'checks': checks,
              'offered_units': report['Offered'], 'admitted_units': report['Admitted'],
              'not_started': report['NotStarted'], 'closed_units': len(completed),
              'selected_exceptions': len(selected), 'actual_repairs': repaired,
              'actual_obligations': sum(final['Detail'][k] for k in ['Open', 'Fulfilled', 'Repaired']),
              'physical_complete': sum(gates), 'cal_net_loss': cal_loss,
              'fee': fee, 'timing': timing, 'errors': errors, 'unfinished_exceptions': unfinished,
              'probe_requests': sum(u['Repair']['ProbeRequests'] for u in anomalies),
              'probe_errors': sum(u['Repair']['ProbeErrors'] for u in anomalies),
              'elapsed_s': (report['StoppedUnixNS']-report['StartedUnixNS'])/1e9,
              'timing_note': 'Local observation times include 250 ms polling, RPC and scheduling; no exact remote Commit latency claim.'}
    (path/'e3-summary.json').write_text(json.dumps(result, indent=2)+'\n')
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('case', type=Path)
    result = analyze(parser.parse_args().case)
    print(json.dumps(result, indent=2))
    raise SystemExit(0 if result['passed'] else 1)
