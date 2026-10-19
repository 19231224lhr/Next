#!/usr/bin/env python3
"""Check the declared measurement matrix and cross-run comparison identities."""
import argparse
import csv
import json
import pathlib


def rows(directory, name):
    with (directory / (name + '.csv')).open(encoding='utf-8-sig', newline='') as f:
        return list(csv.DictReader(f))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--csv', type=pathlib.Path, required=True)
    a = p.parse_args()
    analysis = json.loads((a.csv / 'analysis.json').read_text())
    assert not analysis['missing_runs'], analysis['missing_runs']
    perf, fees = rows(a.csv, 'performance_runs'), rows(a.csv, 'fee_runs')
    assert len(perf) == 26 and len(fees) == 27, (len(perf), len(fees))
    assert all(int(r['native_exit_code']) == 0 for r in perf)
    assert all(not r['error'] and r['closed_pairs'] == r['requested_pairs'] for r in fees)
    pairs = rows(a.csv, 'fee_pairs')
    assert len(pairs) == 369, len(pairs)
    assert all(r['source_user_fee'] == r['child_user_fee'] == '10' for r in pairs)
    assert all(r['source_refund_checked'] == r['child_refund_checked'] == 'True' for r in pairs)
    for repeat in range(1, 4):
        selected = [r for r in pairs if r['run'].startswith(f'orders-r{repeat}-')]
        assert len(selected) == 3
        assert len({r['source_tx'] for r in selected}) == len({r['child_tx'] for r in selected}) == 1
        assert {r['observed_order'] for r in selected} == {'source-first', 'child-first-fulfilled', 'compensated-repaid'}
    assert all(r['source_held'] == r['child_held'] == '0' for r in pairs)
    assert all(r['source_rewards'] == r['child_rewards'] == '2' for r in pairs)
    intermediate = [r for r in rows(a.csv, 'fee_stages')
                    if r['stage'] == 'compensated-before-source' and r['role'] == 'Source']
    assert len(intermediate) == 3
    assert all(r['burned'] == '3' and r['rewards'] == '2' and r['held'] == '1'
               and r['released'] == '0' and r['quote_collected'] == 'False' for r in intermediate)
    assert all(r['pool_reconciliation_available'] == 'True' for r in fees)
    for r in fees:
        assert int(r['source_first']) + int(r['child_first_fulfilled']) + int(r['compensated_repaid']) == int(r['closed_pairs'])
        for org, role in [(0, 'source'), (1, 'child')]:
            assert int(r[f'org{org}_Revenue_delta']) == int(r[f'{role}_user_fee'])
            assert int(r[f'org{org}_Spent_delta']) == int(r[f'{role}_burned']) + int(r[f'{role}_rewards'])
    result = {'status': 'PASS', 'performance_runs': len(perf), 'fee_runs': len(fees), 'fee_pairs': len(pairs),
              'checks': ['declared run counts', 'no native failures', 'identical signed order-comparison payments',
                         'fixed observed user fees', 'refund existence checked', 'all fee pairs closed',
                         'constant committee rewards', 'compensation retains unpaid repayment budget',
                         'issuer pool income/expense reconciliation']}
    (a.csv / 'evidence-check.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
