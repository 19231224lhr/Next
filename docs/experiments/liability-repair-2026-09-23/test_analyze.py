"""Synthetic parser fixtures only: these are not E3 experimental results."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('analysis', Path(__file__).with_name('analyze.py'))
analysis = importlib.util.module_from_spec(spec)
spec.loader.exec_module(analysis)


class AnalysisTest(unittest.TestCase):
    def fixture(self, path):
        unit = {'Index': 0, 'RepairExpected': True, 'FirstSubmitUnixNS': 40_000_000_000,
                'Parent': {'MemberClosedUnixNS': 42_000_000_000, 'FinalUnixNS': 41_000_000_000, 'ReadyUnixNS': 1, 'FastMS': 1},
                'Child': {'MemberClosedUnixNS': 2_000_000_000, 'FinalUnixNS': 1_500_000_000, 'ReadyUnixNS': 2, 'FastMS': 1},
                'Repair': {'DeadlineUnix': 30, 'ProbeRequests': 4, 'ProbeErrors': 0,
                    'Nodes': [{'CommittedUnixNS': 31_000_000_000, 'MaterializedUnixNS': 32_000_000_000,
                        'Snapshot': {'Committed': True, 'Materialized': True, 'IdentityStable': True, 'BytesChanged': True}} for _ in range(4)]}}
        report = {'Offered': 1, 'Admitted': 1, 'NotStarted': 0, 'StartedUnixNS': 0,
                  'StoppedUnixNS': 42_000_000_000, 'Units': [unit], 'RepairSelected': [0]}
        payments = [{'Public': True, 'FeeSource': 2, 'FeeInputAmount': 10000, 'Change': 9000, 'Refund': refund,
            'Fee': {'Maximum': 1000, 'Held': 0, 'Rewards': 990-refund, 'Burned': 10, 'Refunded': refund, 'Closed': True}}
            for refund in [906, 901]]
        files = {'reports/budget-v4.json': report, 'reports/budget-audit.json': {'Payments': payments},
                 'reports/audit.json': [{'Name': 'committee'+str(i), 'StateHash': 'same', 'Gap': '0'} for i in range(4)],
                 'last-snapshots.json': {'committee0': {'CAL': 59900, 'FUEL': 0, 'Detail': {'Repaired': 1, 'Open': 0, 'Fulfilled': 0}}},
                 'e3-config.json': {'cal_grant': 60000}}
        for name, data in files.items():
            target = path/name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(json.dumps(data))
        return report

    def test_complete_gate_and_fee_audit(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            self.fixture(path)
            self.assertTrue(analysis.analyze(path)['passed'])

    def test_commit_without_four_physical_completions_cannot_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            report = self.fixture(path)
            report['Units'][0]['Repair']['Nodes'][3]['MaterializedUnixNS'] = 0
            (path/'reports/budget-v4.json').write_text(json.dumps(report))
            self.assertFalse(analysis.analyze(path)['passed'])

    def test_unstarted_units_remain_in_denominator(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            report = self.fixture(path)
            report['Offered'], report['NotStarted'] = 2, 1
            (path/'reports/budget-v4.json').write_text(json.dumps(report))
            result = analysis.analyze(path)
            self.assertFalse(result['passed'])
            self.assertEqual(result['closed_units'], 1)
            self.assertEqual(result['offered_units'], 2)


if __name__ == '__main__':
    unittest.main()
