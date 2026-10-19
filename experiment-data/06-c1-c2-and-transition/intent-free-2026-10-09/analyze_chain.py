"""Audit actual request/attempt/gate records; keep failure reasons literal."""
import argparse
import base64
import gzip
import hashlib
import json
from pathlib import Path
from statistics import mean, median


def read(path):
    if path.exists():
        return json.loads(path.read_text())
    with gzip.open(str(path) + '.gz', 'rt') as source:
        return json.load(source)


def audit_run(folder):
    report = read(folder / 'chain-v4.json')
    assert not report.get('Error') and len(report['Hops']) == 100
    assert report['RecordAttempts']
    wait = not folder.name.endswith('-fast')
    member_wait = folder.name.endswith('-member-wait')
    assert report['WaitMemberInputs'] == member_wait
    requests = read(folder / 'fault-v4.json')['Samples']
    assert len(requests) == 100
    genesis = read(folder / 'network.json')['Genesis']['Outputs']
    origins = {tuple(row['ID']): row for row in genesis}
    fees = set()
    row = {'case': folder.name, 'mode': report['Mode'],
           'chain_ms': report['FastChainMS'], 'attempts': 0, 'failed_attempts': 0,
           'failed_service_ms': 0., 'retry_sleep_ms': 0.,
           'intermediate_gate_ms': 0., 'gate_samples': 0,
           'intermediate_wallet_wait_ms': 0.,
           'diagnostic_scope': 'HTTP responses and member observations; no inferred approval-failure reason'}
    for index, hop in enumerate(report['Hops']):
        body = requests[index]['Request']['Tx']['Body']
        assert body['Inputs'][0]['Output'] == hop['Input']
        for fee in body['Fee']['Inputs']:
            identity = tuple(fee['Output'])
            assert identity not in fees and identity in origins
            assert fee['Evidence'] == origins[identity]['Fact']
            fees.add(identity)
        raw = base64.b64decode(hop['RequestRaw'], validate=True)
        assert len(raw) == hop['RequestBytes']
        digest = hashlib.sha256(raw).hexdigest()
        attempts = hop['HTTPAttempts']
        assert len(attempts) == hop['Attempts'] and attempts
        assert attempts[0]['StartedUnixNS'] == hop['SentUnixNS']
        assert attempts[-1]['HTTPStatus'] == 200 and not attempts[-1].get('Error')
        for number, attempt in enumerate(attempts, 1):
            assert attempt['Attempt'] == number and attempt['RequestSHA256'] == digest
            assert attempt['ServiceMS'] >= 0 and attempt['RetrySleepMS'] >= 0
            row['attempts'] += 1
            row['retry_sleep_ms'] += attempt['RetrySleepMS']
            if attempt.get('Error'):
                row['failed_attempts'] += 1
                row['failed_service_ms'] += attempt['ServiceMS']
        assert attempts[-1]['RetrySleepMS'] == 0
        if wait:
            assert hop['PublicWaitDoneUnixNS'] >= hop['FinalUnixNS']
            if index < 99:
                row['intermediate_wallet_wait_ms'] += hop['PublicWaitMS']
        if member_wait:
            gate = hop['MemberInputGate']
            assert gate['GenesisFeeValidated']
            assert len(gate['Quorum']) == len(set(gate['Quorum'])) == 3
            assert gate['DoneUnixNS'] <= hop['SentUnixNS']
            assert gate['RequiresParentObservation'] == (index > 0)
            if index:
                assert gate['Parent'] == report['Hops'][index - 1]['Fact']
                # Successful observations of the same exact parent, not height
                # or signing, are required for each selected routed member.
                for member in gate['Quorum']:
                    samples = [s for s in gate['Samples'] if s['Member'] == member]
                    assert samples and not samples[-1].get('Error')
                    assert samples[-1]['Status']['Observed']
                row['intermediate_gate_ms'] += gate['MS']
            else:
                assert gate['DoneUnixNS'] <= report['StartedUnixNS']
                row['initial_bootstrap_gate_ms_excluded'] = gate['MS']
            row['gate_samples'] += len(gate['Samples'])
        else:
            assert not hop.get('MemberInputGate')
    return row, report


def main(root):
    rows, reports = [], {}
    modes = ('fast', 'wallet-wait', 'member-wait')
    pairs = []
    for repeat in range(1, 4):
        for mode in modes:
            case = f'r{repeat}-{mode}'
            row, report = audit_run(root / case)
            rows.append(row)
            reports[case] = report
        fast = reports[f'r{repeat}-fast']
        for mode in modes[1:]:
            wait = reports[f'r{repeat}-{mode}']
            assert fast['Hops'][0]['Input'] == wait['Hops'][0]['Input']
            for left, right in zip(fast['Hops'], wait['Hops']):
                assert left['Sender'] == right['Sender'] and left['Receiver'] == right['Receiver']
                assert left['OutputData'] == right['OutputData']
        pairs.append({'repeat': repeat,
                      'wallet_wait_over_fast': reports[f'r{repeat}-wallet-wait']['FastChainMS']/fast['FastChainMS'],
                      'member_wait_over_fast': reports[f'r{repeat}-member-wait']['FastChainMS']/fast['FastChainMS'],
                      'member_wait_over_wallet_wait': reports[f'r{repeat}-member-wait']['FastChainMS']/reports[f'r{repeat}-wallet-wait']['FastChainMS']})
    aggregates = {}
    for mode in modes:
        values = [r['chain_ms'] for r in rows if r['case'].endswith('-' + mode)]
        aggregates[mode] = {'mean_ms': mean(values), 'median_ms': median(values),
                            'min_ms': min(values), 'max_ms': max(values)}
    summary = {'runs': rows, 'aggregates': aggregates, 'paired_ratios': pairs,
               'note': 'Different input evidence paths remain; gate timings include client polling.'}
    (root / 'member-gate-analysis.json').write_text(json.dumps(summary, indent=2) + '\n')
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('results', type=Path)
    main(parser.parse_args().results)
