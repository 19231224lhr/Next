"""Recheck raw final-build chains and produce run-level and per-hop tables."""
import argparse
import csv
import gzip
import hashlib
import json
from pathlib import Path
from statistics import median


def read_json(path):
    if path.exists():
        return json.loads(path.read_text())
    return json.loads(gzip.decompress(path.with_suffix(path.suffix + '.gz').read_bytes()))


def csv_write(path, rows):
    with path.open('w', newline='') as f:
        writer = csv.DictWriter(f, list(rows[0]), lineterminator='\n')
        writer.writeheader()
        writer.writerows(rows)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('results', type=Path)
    args = parser.parse_args()
    root = args.results
    genesis = read_json(root / 'genesis-template.json')
    totals = {asset: sum(o['Output']['Amount'] for o in genesis['Genesis']['Outputs'] if o['Output']['Asset'] == asset)
                    + sum(a['Balance'] for a in genesis['Accounts'] if a['Asset'] == asset) for asset in [1, 2]}
    cases, all_hops, configs = [], [], []
    for folder in sorted(root.glob('r[123]-*')):
        report = read_json(folder / 'chain-v4.json')
        config = json.loads((folder / 'configuration.json').read_text())
        events = read_json(folder / 'settlements.json')
        audit = json.loads((folder / 'audit.json').read_text())
        chain_audit = json.loads((folder / 'chain-audit.json').read_text())
        wait = config.pop('wait_final')
        configs.append(config)
        assert chain_audit['verified'] and chain_audit['hops'] == 100
        assert report['Requested'] == len(report['Hops']) == 100 and not report.get('Error')
        assert report['Mode'] == ('wait_final' if wait else 'fast')
        committees = [n for n in audit if n['Name'].startswith('committee')]
        assert len(committees) == 4 and len({n['StateHash'] for n in committees}) == 1
        assert all(n.get('Pending', 0) == 0 for n in audit)
        for n in committees:
            assert n['Payments'] == n['Closed'] == 100 and n['Gap'] == '0'
            assert n['Rewards'] == '8400' and n['Burned'] == '1000'
            assert int(n['CAL']) == totals[1] and int(n['FUEL']) == totals[2]
            assert n['Recovery']['PaidCAL'] == n['Recovery']['RecoveredCAL'] == 0
            assert n['Recovery']['Open'] == n['Recovery']['Repaired'] == 0
            assert all(r['Spent'] == r['Reserved'] == 0 for r in n['Recovery']['Reserves'])
        hops = report['Hops']
        commits = {}
        for h in hops:
            fact = bytes(h['Fact']).hex()
            assert len(events) == 4 and all(any(e.get('CommittedUnixNS', 0) > 0 for e in (node.get(fact) or []))
                                           for node in events.values()), ('missing replica commit', folder.name, h['Hop'])
            stamps = [e['CommittedUnixNS'] for node in events.values()
                      for e in (node.get(fact) or []) if e.get('CommittedUnixNS', 0) > 0]
            assert stamps, ('missing application commit', folder.name, h['Hop'])
            commits[fact] = min(stamps)
        rows = []
        for i, h in enumerate(hops):
            fact = bytes(h['Fact']).hex()
            prior = hops[i - 1] if i else None
            assert h['SentUnixNS'] <= h['ReadyUnixNS'] <= h['FinalUnixNS'] <= h['MemberClosedUnixNS']
            if prior:
                assert h['Input'] == prior['Output'] and h['BuildStartUnixNS'] >= prior['ReadyUnixNS']
                if wait:
                    assert h['SentUnixNS'] >= prior['FinalUnixNS'] and not h['CertificateInput']
            row = {'case': folder.name, 'hop': i + 1, 'certificate_input': h['CertificateInput'],
                   'fast_ms': h['FastMS'], 'build_ms': h['BuildMS'], 'attempts': h['Attempts'],
                   'sent_offset_ms': h['SentOffsetMS'], 'ready_offset_ms': h['ReadyOffsetMS'],
                   'public_observed_ms': h['FinalOffsetMS'], 'member_observed_ms': h['MemberOffsetMS'],
                   'application_commit_offset_ms': (commits[fact] - report['StartedUnixNS']) / 1e6,
                   'before_parent_application_commit': h['SentUnixNS'] < commits[bytes(prior['Fact']).hex()] if prior else None,
                   'before_parent_wallet_observation': h['SentUnixNS'] < prior['FinalUnixNS'] if prior else None,
                   'parent_commit_lead_ms': (commits[bytes(prior['Fact']).hex()] - h['SentUnixNS']) / 1e6 if prior else None,
                   'request_bytes': h['RequestBytes'], 'certificate_bytes': h['CertificateBytes']}
            rows.append(row)
        all_hops.extend(rows)
        times = sorted(h['FastMS'] for h in hops)
        cases.append({'case': folder.name, 'mode': report['Mode'],
                      'fast_chain_ms': report['FastChainMS'], 'public_chain_ms': report['PublicChainMS'],
                      'closed_chain_ms': report['ClosedChainMS'], 'hop_p50_ms': median(times),
                      'all_public_observed_ms': max(h['FinalOffsetMS'] for h in hops),
                      'hop_p95_ms': times[94], 'attempts': sum(h['Attempts'] for h in hops),
                      'certificate_successors': sum(h['CertificateInput'] for h in hops[1:]),
                      'precommit_successors': sum(r['before_parent_application_commit'] is True for r in rows[1:]),
                      'preobservation_successors': sum(r['before_parent_wallet_observation'] is True for r in rows[1:]),
                      'certified_precommit_successors': sum(r['certificate_input'] and r['before_parent_application_commit'] is True for r in rows[1:]),
                      'min_precommit_lead_ms': min((r['parent_commit_lead_ms'] for r in rows[1:] if r['before_parent_application_commit']), default=None),
                      'fulfilled_gaps': committees[0]['Recovery']['Fulfilled'],
                      'cal_total': committees[0]['CAL'], 'fuel_total_including_fee_sinks': committees[0]['FUEL'],
                      'reward_fuel': committees[0]['Rewards'], 'burn_fuel': committees[0]['Burned'],
                      'audit': 'passed'})
    assert len(cases) == 6 and all(c == configs[0] for c in configs)
    medians = {mode: {k: median(r[k] for r in cases if r['mode'] == mode)
                     for k in ['fast_chain_ms', 'public_chain_ms', 'closed_chain_ms', 'hop_p50_ms', 'hop_p95_ms']}
               for mode in ['fast', 'wait_final']}
    summary = {'runs': cases, 'medians': medians,
               'receipt_chain_speedup': medians['wait_final']['fast_chain_ms'] / medians['fast']['fast_chain_ms'],
               'payments': 600, 'same_configuration_except_wait': True,
               'fast_successors': {key: sum(r[key] for r in cases if r['mode'] == 'fast')
                                   for key in ['certificate_successors', 'precommit_successors', 'preobservation_successors']}}
    csv_write(root / 'cases.csv', cases)
    csv_write(root / 'hops.csv', all_hops)
    (root / 'summary.json').write_bytes((json.dumps(summary, indent=2) + '\n').encode())
    paths = [p for p in root.rglob('*') if p.is_file() and p.name != 'manifest.json']
    (root / 'manifest.json').write_bytes((json.dumps({str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
                                                   for p in sorted(paths)}, indent=2) + '\n').encode())
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    main()
