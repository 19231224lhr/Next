"""Offline tests of the evidence auditor; these are not network measurements."""
import base64
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from analyze import audit_run


class EvidenceAuditTests(unittest.TestCase):
    def fixture(self, root, member_wait):
        folder = root / ('r1-member-wait' if member_wait else 'r1-wallet-wait')
        folder.mkdir()
        origins, samples, hops = [], [], []
        raw = b'signed request bytes'
        digest = hashlib.sha256(raw).hexdigest()
        for index in range(100):
            fee = [index + 1]
            origins.append({'ID': fee, 'Fact': 'fee-fact'})
            samples.append({'Request': {'Tx': {'Body': {'Inputs': [{'Output': [1]}],
                'Fee': {'Inputs': [{'Output': fee, 'Evidence': 'fee-fact'}]}}}}})
            hop = {'Input': [1], 'Fact': [index], 'RequestRaw': base64.b64encode(raw).decode(),
                   'RequestBytes': len(raw), 'Attempts': 1, 'SentUnixNS': index * 100 + 20,
                   'FinalUnixNS': index * 100 + 30, 'PublicWaitDoneUnixNS': index * 100 + 40,
                   'PublicWaitMS': 20, 'HTTPAttempts': [{'Attempt': 1,
                   'RequestSHA256': digest, 'StartedUnixNS': index * 100 + 20,
                   'HTTPStatus': 200, 'ServiceMS': 5, 'RetrySleepMS': 0}]}
            if member_wait:
                hop['MemberInputGate'] = {'GenesisFeeValidated': True, 'Quorum': [0, 1, 2],
                    'DoneUnixNS': index * 100 + 10, 'RequiresParentObservation': index > 0,
                    'Parent': [index - 1], 'MS': 5, 'Samples': [
                        {'Member': i, 'Status': {'Observed': index > 0}} for i in range(3)]}
            hops.append(hop)
        documents = {'chain-v4.json': {'Hops': hops, 'RecordAttempts': True,
            'WaitMemberInputs': member_wait, 'Mode': 'wait_member_inputs' if member_wait else 'wait_final',
            'FastChainMS': 123, 'StartedUnixNS': 20}, 'fault-v4.json': {'Samples': samples},
            'network.json': {'Genesis': {'Outputs': origins}}}
        for name, document in documents.items():
            (folder / name).write_text(json.dumps(document))
        return folder, documents

    def test_both_wait_arms_and_ninety_nine_intermediate_segments(self):
        with tempfile.TemporaryDirectory() as temp:
            for member_wait in (False, True):
                folder, _ = self.fixture(Path(temp), member_wait)
                row, _ = audit_run(folder)
                self.assertEqual(row['intermediate_wallet_wait_ms'], 99 * 20)
                self.assertEqual(row['intermediate_gate_ms'], 99 * 5 if member_wait else 0)
                self.assertEqual(row['attempts'], 100)

    def test_unobserved_selected_member_fails_audit(self):
        with tempfile.TemporaryDirectory() as temp:
            folder, documents = self.fixture(Path(temp), True)
            documents['chain-v4.json']['Hops'][1]['MemberInputGate']['Samples'][0]['Status']['Observed'] = False
            (folder / 'chain-v4.json').write_text(json.dumps(documents['chain-v4.json']))
            with self.assertRaises(AssertionError):
                audit_run(folder)

    def test_http_200_read_failure_is_still_a_failed_attempt(self):
        with tempfile.TemporaryDirectory() as temp:
            folder, documents = self.fixture(Path(temp), False)
            hop = documents['chain-v4.json']['Hops'][0]
            failed = dict(hop['HTTPAttempts'][0], Error='unexpected EOF',
                          ServiceMS=3, RetrySleepMS=50)
            succeeded = dict(hop['HTTPAttempts'][0], Attempt=2, StartedUnixNS=100)
            hop['HTTPAttempts'] = [failed, succeeded]
            hop['Attempts'] = 2
            (folder / 'chain-v4.json').write_text(json.dumps(documents['chain-v4.json']))
            row, _ = audit_run(folder)
            self.assertEqual(row['failed_attempts'], 1)
            self.assertEqual(row['failed_service_ms'], 3)
            self.assertEqual(row['retry_sleep_ms'], 50)


if __name__ == '__main__':
    unittest.main()
