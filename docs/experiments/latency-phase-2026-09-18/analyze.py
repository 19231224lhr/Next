import json, pathlib, statistics

root = pathlib.Path('experiments/latency-phase-001/reports')
summary = {}
for folder in sorted(root.iterdir()):
    if not folder.is_dir() or not (folder/'done.json').exists(): continue
    rows = []
    for path in sorted(folder.glob('input-*.json')):
        data = json.loads(path.read_text())
        s = data['report']['Samples'][0]
        h = s['SettlementHeight']
        origin = next(e['unix_ns'] for e in s['Trace'] if e['stage'] == 'http_submit')
        ns = data['nodes'][0]
        attempts = ns['attempts']
        admitted = min(a['AcceptedUnixNS'] for a in attempts if a['AcceptedUnixNS'])
        selected = min(a['PreparedUnixNS'] for n in data['nodes'] for a in n['attempts'] if a.get('PreparedHeight') == h)
        done = min(a['CommittedUnixNS'] for a in attempts if a['Height'] == h and a['CommittedUnixNS'])
        execute = min(a['ExecuteStartUnixNS'] for a in attempts if a['Height'] == h and a['ExecuteStartUnixNS'])
        phases = {e['Stage']: e['UnixNS'] for e in ns['consensus'] if e['Fields'].get('height') == str(h)}
        def delta(a,b): return round((b-a)/1e6,3)
        row = dict(file=path.name, spend=s['Spend'], height=h,
            ready_ms=s['WalletReadyMicros']/1000, commit_ms=delta(origin,done), proof_ms=s['FinalProofMicros']/1000,
            admission_to_selected_ms=delta(admitted,selected),
            admission_to_execute_ms=delta(admitted,execute),
            selected_to_decision_ms=delta(selected,phases['finalizing commit of block']),
            selected_to_prevote_ms=delta(selected,phases['entering prevote step']),
            prevote_to_precommit_ms=delta(phases['entering prevote step'],phases['entering precommit step']),
            precommit_to_decision_ms=delta(phases['entering precommit step'],phases['finalizing commit of block']),
            decision_to_abci_ms=delta(phases['finalizing commit of block'],phases['finalize_enter']),
            abci_to_execute_ms=delta(phases['finalize_enter'],execute),
            execute_to_commit_ms=delta(execute,done),
            attempts=len([a for a in attempts if a['ReceivedUnixNS']]), executed=len([a for a in attempts if a['ExecuteStartUnixNS']]),
            rounds=sorted(set(e['Fields']['round'] for e in ns['consensus'] if e['Fields'].get('height') == str(h) and 'round' in e['Fields'])))
        rows.append(row)
    metrics = {k:dict(median=round(statistics.median(r[k] for r in rows),3), min=min(r[k] for r in rows),max=max(r[k] for r in rows)) for k in rows[0] if k.endswith('_ms') or k in ['attempts','executed']}
    summary[folder.name] = dict(rows=rows,metrics=metrics)
print(json.dumps(summary,indent=2))
(root/'summary.json').write_text(json.dumps(summary,indent=2))
