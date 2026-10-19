import unittest
import tempfile
import pathlib
import json
from metrics import mixed_metrics, quantile, trace_spans
from analyze import escrow_columns
from run_c3 import clone_fixture


class MetricsTest(unittest.TestCase):
    def test_budget_conservation_rejects_corrupt_report(self):
        snapshot={'Record':{'QuoteCollected':True,'Escrow':{'Commitment':{'Quote':10,'Budget':6},
                  'Held':0,'Rewards':2,'Burned':0,'Released':4}}}
        self.assertEqual(escrow_columns(snapshot)['released'],4)
        snapshot['Record']['Escrow']['Burned']=1
        with self.assertRaises(ValueError):
            escrow_columns(snapshot)

    def test_fixture_clone_preserves_keys_and_rewrites_only_paths(self):
        with tempfile.TemporaryDirectory() as temp:
            root=pathlib.Path(temp); source=root/'source'; dest=root/'copy'; source.mkdir()
            (source/'c3-fees-fixture.json').write_text('{}')
            (source/'reports').mkdir()
            (source/'private.key').write_bytes(b'\x00\x01\xff')
            # Linux paths are unescaped in JSON. Use a literal text config to
            # test path substitution on both local host platforms.
            (source/'config.toml').write_text('root='+str(source)+'\nidentity=abc')
            clone_fixture(source,dest)
            self.assertEqual((dest/'private.key').read_bytes(),b'\x00\x01\xff')
            self.assertEqual((dest/'config.toml').read_text(),'root='+str(dest)+'\nidentity=abc')
            with self.assertRaises(ValueError):
                clone_fixture(source,dest)

    def test_quantile_interpolates_and_rejects_empty(self):
        self.assertEqual(quantile([4, 1, 2, 3], .5), 2.5)
        self.assertAlmostEqual(quantile([1, 2, 3, 4], .95), 3.85)
        with self.assertRaises(ValueError):
            quantile([], .5)

    def test_mixed_excludes_warm_and_keeps_unsent_failures(self):
        report = {'Options':{'Rate':20000000}, 'SkippedPacing':7, 'Samples': [
            {'ScheduledNS': 90, 'SentNS': 91, 'ReadyNS': 110, 'PublicNS': 120,
             'MemberNS': [130]*4, 'Monotonic': {'FastNS': 19}},
            {'ScheduledNS': 100, 'SentNS': 101, 'ReadyNS': 150, 'PublicNS': 210,
             'MemberNS': [220]*4, 'Monotonic': {'FastNS': 49}, 'CertificateBytes': 500},
            {'ScheduledNS': 190, 'SentNS': 191, 'ReadyNS': 220, 'PublicNS': 230,
             'MemberNS': [0]*4, 'Monotonic': {'FastNS': 29}, 'CertificateBytes': 600},
            {'ScheduledNS': 195, 'SentNS': 0, 'Error': 'not sent: window ended'}]}
        result = mixed_metrics(report, {'FormalNS': 100, 'EndSendNS': 200})
        self.assertEqual(result['formal_samples'], 3)
        self.assertEqual(result['attempted'], 2)
        self.assertEqual(result['receipts'], 2)
        self.assertEqual(result['closed'], 1)
        self.assertEqual(result['in_window_receipts'], 1)
        self.assertEqual(result['unsent'], 1)
        self.assertEqual(result['completion_fraction'], .5)
        self.assertEqual(result['nominal_formal_offered'], 2)
        self.assertEqual(result['offered_receipt_fraction'], 1)
        self.assertEqual(result['skippedpacing_including_warmup'], 7)

    def test_trace_never_subtracts_different_nodes_or_missing_events(self):
        events = [{'node': 'a', 'stage': 'update_started', 'local_ns': 10},
                  {'node': 'b', 'stage': 'update_evaluated', 'local_ns': 100},
                  {'node': 'a', 'stage': 'update_evaluated', 'local_ns': 30}]
        spans = trace_spans(events)
        self.assertEqual(len(spans), 1)
        self.assertEqual(spans[0]['span'], 'atomic_evaluation')
        self.assertEqual(spans[0]['duration_ns'], 20)


if __name__ == '__main__':
    unittest.main()
