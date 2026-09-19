"""Reproduce per-payment, per-block and queue timings from this 100-payment run.

P50 is the median; P95 uses the benchmark's floor((n-1)*.95) convention.
All timestamps come from processes on the same Mac. Post-Update observation
offsets may be signed because readers can run before the logging goroutine.
"""
import csv
import json
import statistics as st
import sys
from collections import Counter
from pathlib import Path

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parent
p = root / 'reports/trace100'
b = json.loads((p / 'benchmark.json').read_text())
settlements = json.loads((p / 'settlement.json').read_text())
audit = json.loads((root / 'reports/audit.json').read_text())
assert b['Summary']['count'] == 100 and b['Summary']['failed'] == 0
assert len(b['Samples']) == 100 and len({s['Fact'] for s in b['Samples']}) == 100
assert all(r['Pending'] == 0 for r in audit)
committees = [r for r in audit if r['Name'].startswith('committee')]
assert len(committees) == 4 and len({r['StateHash'] for r in committees}) == 1
assert all(r['Payments'] == r['Closed'] == 100 and r['Gap'] == '0' for r in committees)
timeline = {f.name.removesuffix('-timeline.json'): json.loads(f.read_text())
            for f in p.glob('*-timeline.json')}
timeline['wallet'] = b['Timeline']
assert all(len(es) < 4096 for es in timeline.values()), 'possibly truncated timeline'


def ns(node, stage, h=None, fact=None, target=None):
    es = [e['UnixNS'] for e in timeline[node] if e['Stage'] == stage
          and (h is None or e['Fields'].get('height') == str(h))
          and (fact is None or e['Fields'].get('spend') == fact)
          and (target is None or e['Fields'].get('target') == str(target))]
    assert es, (node, stage, h, fact, target)
    return min(es)


def delta(end, start):
    return (end-start)/1e6


def stats(xs):
    xs = sorted(xs)
    return {'n': len(xs), 'p50': st.median(xs), 'p95': xs[int((len(xs)-1)*.95)],
            'min': min(xs), 'max': max(xs), 'mean': st.mean(xs)} if xs else {}


def writecsv(name, rows):
    with (root / name).open('w', newline='', encoding='utf-8-sig') as out:
        writer = csv.DictWriter(out, fieldnames=list(rows[0]), lineterminator='\n')
        writer.writeheader()
        writer.writerows(rows)


stores = [e['Fields'] for e in timeline['gateway0'] if e['Stage'] == 'store_update']
assert all(e['failed'] == 'false' for e in stores)
rows, votes = [], []
for s in sorted(b['Samples'], key=lambda s: s['SentUnixNS']):
    f = s['Fact']
    committed = [r for r in settlements['committee0'][f] if r['CommittedUnixNS'] > 0]
    assert len(committed) == 1, 'duplicate execution record'
    rec = committed[0]
    h = rec['Height']
    assert all(sum(r['CommittedUnixNS'] > 0 for r in node[f]) == 1 for node in settlements.values())
    fg = {e['stage']: e['unix_ns'] for e in s['Foreground'] if e['node'] == 'gateway'}
    prep = [(r['PreparedUnixNS'], node) for node, ss in settlements.items() for r in ss[f]
            if r['PreparedUnixNS'] and r['PreparedHeight'] == h]
    proposer = min(prep)[1]
    callback = ns('gateway0', 'persist_callback', fact=f)
    physical = [r for r in stores if int(r['callback_ns']) <= callback <= int(r['evaluated_ns'])]
    assert len(physical) == 1
    physical = physical[0]
    durable = int(physical['returned_ns'])
    submit = ns('gateway0', 'submit_start', fact=f)
    received, accepted, commit = (rec[k] for k in ('ReceivedUnixNS', 'AcceptedUnixNS', 'CommittedUnixNS'))
    header = min(e['UnixNS'] for e in timeline['wallet'] if e['Stage'] == 'follow_fetch'
                 and e['Fields']['height'] == str(h) and e['Fields']['path'].startswith('/commit'))
    wallet = ns('wallet', 'follow_committed', h)
    members = max(ns('org0-member'+str(i), 'follow_committed', h) for i in range(4))
    q = lambda stage: ns('gateway0', stage, fact=f)
    w = lambda stage: ns('wallet', stage, h)
    r = {'order': len(rows)+1, 'index': s['Index'], 'fact': f, 'height': h, 'proposer': proposer,
         'sent_ns': s['SentUnixNS'], 'fast_ns': s['FastUnixNS'], 'durable_ns': durable,
         'submitted_ns': submit, 'received_ns': received, 'commit_ns': commit,
         'wallet_durable_ns': wallet, 'members_durable_ns': members,
         'members_observed_ns': s['MemberObservedUnixNS'],
         'send_offset_ms': delta(s['SentUnixNS'], b['Summary']['started_unix_ns']),
         'fast_ms': delta(s['FastUnixNS'], s['SentUnixNS']),
         'wallet_to_gateway_ms': delta(fg['http_handler_enter'], s['SentUnixNS']),
         'gateway_collect_ms': delta(fg['response_ready'], fg['http_handler_enter']),
         'response_transfer_ms': delta(s['CertificateReceivedUnixNS'], fg['response_ready']),
         'wallet_receive_verify_save_ms': delta(s['FastUnixNS'], s['CertificateReceivedUnixNS']),
         'response_to_persist_start_ms': delta(q('outbox_persist_start'), fg['response_ready']),
         'gateway_persist_ms': delta(q('outbox_persist_done'), q('outbox_persist_start')),
         'persist_to_callback_ms': delta(callback, q('outbox_persist_start')),
         'persist_callback_to_durable_ms': delta(durable, callback),
         'gateway_queue_legacy_ms': delta(q('relay_enter'), q('outbox_persist_done')),
         'durable_to_scheduled_ms': delta(ns('gateway0', 'relay_action_scheduled', fact=f, target=-1), durable),
         'durable_to_submit_ms': delta(submit, durable),
         'submit_roundtrip_ms': delta(q('submit_done'), submit),
         'submit_return_to_update_return_ms': delta(q('relay_update_returned'), q('submit_done')),
         'delivery_ms': delta(received, rec['DeliveredUnixNS']),
         'admission_ms': delta(accepted, received),
         'accepted_to_prepare_enter_ms': delta(ns(proposer, 'prepare_enter', h), accepted),
         'prepare_ms': delta(ns(proposer, 'prepare_done', h), ns(proposer, 'prepare_enter', h)),
         'prepare_done_to_finalize_ms': delta(ns('committee0', 'finalize_enter', h), ns(proposer, 'prepare_done', h)),
         'prior_transactions_ms': delta(rec['FinalCheckStartUnixNS'], ns('committee0', 'finalize_enter', h)),
         'final_check_ms': delta(rec['FinalCheckDoneUnixNS'], rec['FinalCheckStartUnixNS']),
         'execute_ms': delta(rec['ExecuteDoneUnixNS'], rec['ExecuteStartUnixNS']),
         'remaining_block_ms': delta(rec['FinalizeDoneUnixNS'], rec['ExecuteDoneUnixNS']),
         'finalize_to_commit_start_ms': delta(rec['CommitStartUnixNS'], rec['FinalizeDoneUnixNS']),
         'commit_ms': delta(commit, rec['CommitStartUnixNS']),
         'send_to_submit_ms': delta(submit, s['SentUnixNS']),
         'send_to_commit_ms': delta(commit, s['SentUnixNS']),
         'fast_to_commit_ms': delta(commit, s['FastUnixNS']),
         'committee_received_to_commit_ms': delta(commit, received),
         'next_header_wait_fetch_ms': delta(header, commit),
         'block_results_fetch_ms': delta(w('follow_verify_start'), header),
         'wallet_block_verify_ms': delta(w('follow_verify_done'), w('follow_verify_start')),
         'wallet_update_queue_ms': delta(w('follow_update_started'), w('follow_update_requested')),
         'wallet_block_apply_ms': delta(w('follow_apply_done'), w('follow_apply_start')),
         'wallet_update_flush_ms': delta(wallet, w('follow_apply_done')),
         'wallet_observe_lag_ms': delta(s['BlockObservedUnixNS'], wallet),
         'member_apply_from_commit_ms': delta(members, commit),
         'member_observe_lag_ms': delta(s['MemberObservedUnixNS'], members),
         'commit_to_wallet_observed_ms': delta(s['BlockObservedUnixNS'], commit),
         'block_observed_ms': delta(s['BlockObservedUnixNS'], s['SentUnixNS']),
         'members_observed_ms': delta(s['MemberObservedUnixNS'], s['SentUnixNS'])}
    foreground_sum = sum(r[k] for k in ['wallet_to_gateway_ms', 'gateway_collect_ms', 'response_transfer_ms', 'wallet_receive_verify_save_ms'])
    assert abs(foreground_sum - r['fast_ms']) < .00001
    assert s['SentUnixNS'] < durable <= submit <= received < commit
    # Each transaction in a block shares block-wide times; these are not 100
    # independent block executions. Keep a separate block table below.
    rows.append(r)
    for node in {e['node'] for e in s['Foreground']} - {'gateway'}:
        es = {e['stage']: e['local_ns'] for e in s['Foreground'] if e['node'] == node}
        vd = lambda end, start: delta(es[end], es[start])
        votes.append({'fact': f, 'member_url': node,
                      'validation_ms': vd('validation_complete', 'request_decoded'),
                      'storage_queue_ms': vd('update_started', 'commit_requested'),
                      'storage_callback_ms': vd('update_evaluated', 'update_started'),
                      'storage_return_ms': vd('commit_returned', 'update_evaluated'),
                      'vote_sign_ms': vd('vote_signed', 'commit_returned')})

writecsv('transactions.csv', rows)
writecsv('foreground-member-votes.csv', votes)
summary = {k: stats([r[k] for r in rows]) for k in rows[0] if k.endswith('_ms')}
blocks = []
for h in sorted({r['height'] for r in rows}):
    rs = [r for r in rows if r['height'] == h]
    r = rs[0]
    blocks.append({'height': h, 'payments': len(rs), 'proposer': r['proposer'],
                   'prepare_ms': r['prepare_ms'], 'prepare_to_finalize_ms': r['prepare_done_to_finalize_ms'],
                   'process_ms': delta(ns('committee0', 'process_done', h), ns('committee0', 'process_enter', h)),
                   'finalize_ms': delta(ns('committee0', 'finalize_done', h), ns('committee0', 'finalize_enter', h)),
                   'final_check_sum_ms': sum(r['final_check_ms'] for r in rs),
                   'execute_sum_ms': sum(r['execute_ms'] for r in rs), 'commit_ms': r['commit_ms'],
                   'commit_offset_ms': delta(r['commit_ns'], b['Summary']['started_unix_ns'])})
writecsv('blocks.csv', blocks)
cohorts = []
for start in range(0, 100, 20):
    rs = rows[start:start+20]
    cohorts.append({'send_order': f'{start+1}-{start+20}', **{k: st.median(r[k] for r in rs) for k in
                    ['send_offset_ms', 'fast_ms', 'durable_to_submit_ms', 'accepted_to_prepare_enter_ms',
                     'send_to_commit_ms', 'block_observed_ms', 'members_observed_ms']}})
writecsv('cohorts.csv', cohorts)
progress = []
for offset in range(0, int(b['Summary']['elapsed_s']*1000)+251, 250):
    stamp = b['Summary']['started_unix_ns'] + offset*1000000
    r = {'elapsed_ms': offset}
    for stage in ['sent', 'fast', 'durable', 'submitted', 'received', 'commit', 'wallet_durable', 'members_durable']:
        r[stage] = sum(row[stage+'_ns'] <= stamp for row in rows)
    r['saved_not_submitted'] = r['durable']-r['submitted']
    r['received_not_committed'] = r['received']-r['commit']
    progress.append(r)
writecsv('progress.csv', progress)
counts = {node: dict(Counter(e['Stage'] for e in es)) for node, es in timeline.items()}
installs = []
for node in ['org0-member'+str(i) for i in range(4)]:
    facts = {e['Fields']['spend'] for e in timeline[node] if e['Stage'] == 'install_enter'}
    for f in sorted(facts):
        times = [[e['UnixNS'] for e in timeline[node] if e['Stage'] == stage and e['Fields']['spend'] == f]
                 for stage in ['install_enter', 'install_store_start', 'install_store_done']]
        assert len(times[0]) == len(times[1]) == len(times[2])
        previous_done = 0
        for attempt, (enter, start, done) in enumerate(zip(*times), 1):
            assert previous_done <= enter <= start <= done
            previous_done = done
            installs.append({'member': node, 'fact': f, 'attempt': attempt,
                             'validation_ms': delta(start, enter), 'store_ms': delta(done, start)})
writecsv('install-actions.csv', installs)
for key, values in summary.items():
    if key not in ['wallet_observe_lag_ms', 'member_observe_lag_ms', 'gateway_queue_legacy_ms']:
        assert values['min'] >= 0, (key, values)
result = {'benchmark': b['Summary'], 'audit': 'passed', 'stages': summary,
          'member_vote_stages': {k: stats([r[k] for r in votes]) for k in votes[0] if k.endswith('_ms')},
          'install_stages': {k: stats([r[k] for r in installs]) for k in installs[0] if k.endswith('_ms')},
          'calls': counts,
          'gateway_storage': {'physical_updates': len(stores),
                              'changed': sum(e['no_changes'] == 'false' for e in stores),
                              'write_sync_total_ms': sum(int(e['write_ns']) for e in stores)/1e6},
          'last_commit_from_first_send_ms': delta(max(r['commit_ns'] for r in rows), min(r['sent_ns'] for r in rows)),
          'last_fast_from_first_send_ms': delta(max(r['fast_ns'] for r in rows), min(r['sent_ns'] for r in rows)),
          'peak_saved_not_submitted': max(sum(r['durable_ns'] <= stamp < r['submitted_ns'] for r in rows)
                                          for stamp in {r['durable_ns'] for r in rows}),
          'peak_received_not_committed': max(sum(r['received_ns'] <= stamp < r['commit_ns'] for r in rows)
                                             for stamp in {r['received_ns'] for r in rows}),
          'cohorts': cohorts, 'blocks': blocks}
(root / 'stage-summary.json').write_text(json.dumps(result, indent=2)+'\n')
print(json.dumps({k: v for k, v in result.items() if k != 'calls'}, indent=2))
