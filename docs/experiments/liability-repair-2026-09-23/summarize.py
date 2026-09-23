#!/usr/bin/env python3
"""Derive compact E3 tables from retained reports, samples and trace logs."""
import csv
import json
from pathlib import Path
import shlex
from collections import defaultdict
from analyze import quantiles

ROOT = Path(__file__).resolve().parent


def read(path):
    return json.loads(path.read_text())


def peak_pending(units, stopped):
    events = []
    for u in units:
        if not u['StartedUnixNS']:
            continue
        events.append((u['StartedUnixNS'], 1))
        ends = [u[h]['MemberClosedUnixNS'] for h in ['Parent', 'Child']]
        events.append((max(ends) if all(ends) else stopped, -1))
    value = peak = 0
    for _, change in sorted(events):
        value += change
        peak = max(peak, value)
    return peak


def extra(path):
    report = read(path/'reports/budget-v4.json')
    start, stop = report['StartedUnixNS'], report['StoppedUnixNS']
    units = report['Units']
    traces = []
    for log in sorted((path/'node-logs').glob('committee*.log')):
        for line in log.read_text().splitlines():
            if ' INFO repair_trace ' not in line:
                continue
            pairs = [s.split('=', 1) for s in shlex.split(line.split(' INFO repair_trace ', 1)[1])]
            traces.append(dict(p for p in pairs if len(p) == 2))
    (path/'repair-events.jsonl').write_text(''.join(json.dumps(t)+'\n' for t in traces))
    stages = {}
    for stage in ['input_shares', 'part_shares', 'submit', 'materialize']:
        rows = [t for t in traces if t.get('stage') == stage]
        stages[stage] = {
            'calls': len(rows), 'errors': sum('error' in t for t in rows),
            'unique_repairs': len({t['repair'] for t in rows}),
            'call_ms': quantiles([int(t['duration_ns'])/1e6 for t in rows]),
        }
    submit = [t for t in traces if t.get('stage') == 'submit']
    unique_bytes = {}
    for t in submit:
        if int(t.get('code', -1)) == 0 and 'error' not in t:
            unique_bytes.setdefault(t['repair'], int(t['bytes']))
    stages['submit']['command_bytes_once_per_repair'] = quantiles(list(unique_bytes.values()))
    stages['submit']['command_bytes_total_unique'] = sum(unique_bytes.values())
    stages['submit']['command_bytes_all_attempts'] = sum(int(t.get('bytes', 0)) for t in submit)
    stages['submit']['cached_duplicate_calls'] = sum(t.get('error') == 'tx already exists in cache' for t in submit)
    deadlines = {t['repair']:int(t['deadline'])*1000000000 for t in traces if 'deadline' in t}
    first_submit = {}
    for t in submit:
        if 'error' not in t and int(t.get('code', -1)) == 0:
            first_submit.setdefault(t['repair'], []).append(int(t['unix_ns']))
    stages['submit']['deadline_to_first_accepted_call_ms'] = quantiles([
        (min(stamps)-deadlines[key])/1e6 for key,stamps in first_submit.items() if key in deadlines])
    # CPU deltas from ps are whole-process work in the sampled interval. They
    # include useful payments, consensus, background work and diagnostics.
    processes = defaultdict(list)
    process_errors = 0
    procfile = path/'processes.jsonl'
    if procfile.exists():
        for line in procfile.read_text().splitlines():
            row = json.loads(line)
            if 'error' in row:
                process_errors += 1
            else:
                processes[row['node']].append(row)
    cpu = {}
    for name, samples in processes.items():
        first, last = samples[0], samples[-1]
        if len({s['pid'] for s in samples}) != 1:
            raise ValueError('process restarted: '+name)
        cpu[name] = {'cpu_s': last['cpu_s']-first['cpu_s'],
                     'sample_span_s': (last['unix_ns']-first['unix_ns'])/1e9,
                     'peak_rss_mib': max(s['rss_kib'] for s in samples)/1024,
                     'samples': len(samples)}
    samples = [json.loads(l) for l in (path/'samples.jsonl').read_text().splitlines()]
    committee = [s['snapshot'] for s in samples if s['node']=='committee0' and 'snapshot' in s]
    member_samples = [s['snapshot'] for s in samples if s['node'].startswith('org0-member') and 'snapshot' in s]
    minimum_available = min((sum(x['Available'] for x in r['Slices']) for s in member_samples for r in s['Resources'] if r['Key']['Kind']==1),default=None)
    curves = []
    for s in committee:
        cal = next(r for r in s['Resources'] if r['Key']['Kind']==1)
        detail = s.get('Detail', {})
        curves.append({'seconds': (s['UnixNS']-start)/1e9, 'cal': s['CAL'],
            'reserved': cal['Usage']['Reserved'], 'spent': cal['Usage']['Spent'],
            'open': detail.get('Open'), 'repaired': detail.get('Repaired'),
            'oldest_deadline': detail.get('OldestDeadline')})
    if curves:
        with (path/'curves.csv').open('w', newline='') as f:
            writer=csv.DictWriter(f, fieldnames=curves[0]);writer.writeheader();writer.writerows(curves)
    timing = {}
    for exceptional in [False, True]:
        group = [u for u in units if u['RepairExpected']==exceptional]
        for hop in ['Parent', 'Child']:
            rows = [u[hop] for u in group]
            timing[f'{"exceptional" if exceptional else "normal"}_{hop.lower()}_ready_to_final_observed_ms'] = quantiles([
                (h['FinalUnixNS']-h['ReadyUnixNS'])/1e6 for h in rows if h['FinalUnixNS']])
    snapshots = read(path/'last-snapshots.json')
    member_accounting = {}
    for name,s in snapshots.items():
        if not name.startswith('org0-member'):
            continue
        debits = s['Detail']['Debits']
        member_accounting[name] = {'temporary':sum(d['Charged']-d['Released']-d['NetSpent'] for d in debits),
                                  'net_spent_cal':sum(d['NetSpent'] for d in debits if d['Key']['Kind']==1)}
    audit = read(path/'reports/budget-audit.json')
    repaired_units = {p['Unit'] for p in audit['Payments'] if p['Role']=='parent' and p.get('Obligation',{}).get('Status')==2}
    complete_checks = {'no_partial_approvals':audit['PartialApprovals']==0,
        'each_payment_fee_matches_its_own_repair':all(p['Fee']['Rewards']+p['Fee']['Burned']==94+(5 if p['Role']=='child' and p['Unit'] in repaired_units else 0) for p in audit['Payments'] if p['Public']),
        'each_selected_parent_has_repaired_obligation':repaired_units==set(report.get('RepairSelected') or []),
        'no_member_transient_residual':all(m['temporary']==0 for m in member_accounting.values()),
        'all_wallet_refunds_final':all(p['WalletRefundFinal'] for p in audit['Payments'] if p['Public']),
        'no_committee_transient_residual':all(r['Usage']['Reserved']==0 for n,s in snapshots.items() if n.startswith('committee') for r in s['Resources'])}
    history = read(path/'reports/audit.json')
    complete_checks['no_pending_public_outbox'] = all(n['Pending']==0 for n in history)
    revisions = {n['Name']:n.get('Revisions',0) for n in history if n['Name'].startswith('committee')}
    windows=[]
    for begin in range(0,300,60):
        values=[]
        for u in units:
            t=u.get('Repair') or {}; nodes=t.get('Nodes') or []
            offset=(u['StartedUnixNS']-start)/1e9
            if begin<=offset<begin+60 and len(nodes)==4 and all(n['MaterializedUnixNS'] for n in nodes):
                values.append((max(n['MaterializedUnixNS'] for n in nodes)-t['DeadlineUnix']*1000000000)/1e6)
        windows.append({'arrival_from_s':begin,'arrival_to_s':begin+60,'physical_delay_ms':quantiles(values)})
    result = {'case': path.name, 'additional_checks':complete_checks,'member_accounting':member_accounting,
        'different_revised_blocks_by_node':revisions,'minimum_sampled_member_available_cal':minimum_available,
        'member_limit_counters':{n:s['Limited'] for n,s in snapshots.items() if n.startswith('org0-member')},
        'repair_delay_by_arrival_window':windows,
        'peak_pending_units_reconstructed': peak_pending(units, stop),
        'start_lag_ms': quantiles([(u['StartedUnixNS']-u['PlannedUnixNS'])/1e6 for u in units if u['StartedUnixNS']]),
        'peak_committee_reserved_cal': max((c['reserved'] for c in curves), default=0),
        'peak_open_sampled': max((c['open'] for c in curves if c['open'] is not None), default=0),
        'cpu': cpu, 'cpu_total_s': sum(c['cpu_s'] for c in cpu.values()) if cpu else None,
        'process_probe_errors': process_errors, 'repair_stages': stages, 'timing': timing,
        'notes': ['Stage durations are individual calls, including retries, not additive end-to-end latency.',
                  'CPU is whole-node sampled CPU, not isolated repair cryptography.',
                  'Open responsibility is sampled every 10 seconds; CAL usage every 0.5 seconds.']}
    (path/'extra-summary.json').write_text(json.dumps(result, indent=2)+'\n')
    return result


def main():
    sequences=[]
    for path in sorted(ROOT.glob('seq-r2-*')):
        r=read(path/'reports/liability-v4.json')
        a=read(path/'reports/liability-audit.json')
        history=[n for n in read(path/'reports/audit.json') if n['Name'].startswith('committee')]
        snapshots=read(path/'last-snapshots.json')
        residual={name:{str(d['Key']['Kind']):d['Charged']-d['Released']-d['NetSpent'] for d in s['Detail']['Debits']}
                  for name,s in snapshots.items() if name.startswith('org')}
        sequences.append({'case':path.name,'error':r.get('Error',''),
            'elapsed_s':(r['FinishedUnixNS']-r['StartedUnixNS'])/1e9,
            'fast_ms':[h['FastMS'] for h in r['Hops']],
            'final_observed_offset_ms':[h['FinalOffsetMS'] for h in r['Hops']],
            'public':a[0]['Public'],'cal':a[0]['CAL'],
            'fee':{k:a[0]['Fee'][k] for k in ['Maximum','Held','Rewards','Burned','Refunded']},
            'parent_published':r['ParentPublished'],'obligation_statuses':[o['Status'] for o in r['Obligations']],
            'committee_equal':len(history)==4 and len({n['StateHash'] for n in history})==1,
            'physical_complete':len(r['Repair']['Nodes'])==4 and all(n['MaterializedUnixNS'] for n in r['Repair']['Nodes']),
            'temporary_residual_by_member':residual,
            'note':'Member closure is checked at the end, not continuously timed in these causal sequence controls.'})
    (ROOT/'sequence-summary.json').write_text(json.dumps(sequences,indent=2)+'\n')
    summary=[]
    for p in sorted(ROOT.glob('r1-s*-p*/e3-summary.json')):
        row=read(p);row['extra']=extra(p.parent)
        row['passed']=row['passed'] and all(row['extra']['additional_checks'].values())
        summary.append(row)
    (ROOT/'matrix-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
    columns=['case','passed','offered_units','closed_units','actual_repairs','cal_net_loss','elapsed_s',
             'normal_child_fast_p50_ms','normal_child_fast_p95_ms','repair_physical_p50_ms','repair_physical_p95_ms',
             'peak_pending','start_lag_p95_ms','cpu_s']
    with (ROOT/'matrix.csv').open('w',newline='') as f:
        writer=csv.DictWriter(f,fieldnames=columns);writer.writeheader()
        for s in summary:
            e=s['extra'];t=s['timing']
            row={k:s[k] for k in columns[:7]}
            row.update(normal_child_fast_p50_ms=t['normal_child_fast_ms']['p50'],
                normal_child_fast_p95_ms=t['normal_child_fast_ms']['p95'],
                repair_physical_p50_ms=t['deadline_to_all_physical_observed_ms']['p50'],
                repair_physical_p95_ms=t['deadline_to_all_physical_observed_ms']['p95'],
                peak_pending=e['peak_pending_units_reconstructed'],start_lag_p95_ms=e['start_lag_ms']['p95'],cpu_s=e['cpu_total_s'])
            writer.writerow(row)
    print(json.dumps({'formal_runs':len(summary),'passing':sum(s['passed'] for s in summary)},indent=2))


if __name__=='__main__':
    main()
