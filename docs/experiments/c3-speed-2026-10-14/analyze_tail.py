"""Receipt tails: subtract timestamps only within the same process trace."""
import argparse, collections, json, pathlib, statistics

p = argparse.ArgumentParser()
p.add_argument('directory', type=pathlib.Path)
p.add_argument('output', type=pathlib.Path)
a = p.parse_args()
r = json.loads((a.directory / 'reports/chain-v4.json').read_text())
lab = json.loads((a.directory / 'lab.json').read_text())
member_urls = {x['Name'].split('org0-')[-1].replace('member','member-'):x['URL'] for x in lab['Nodes'] if x['Name'].startswith('org0-member')}
settlement = {x['hop']: x['records'] for x in json.loads((a.directory / 'reports/settlement-traces.json').read_text())}

def span(stages, start, end):
    if start in stages and end in stages and stages[end] >= stages[start]:
        return (stages[end] - stages[start]) / 1e6
    return None

rows = []
for h in r['Hops']:
    nodes = collections.defaultdict(dict)
    for e in h.get('Foreground', []):
        nodes[e['node']][e['stage']] = e['local_ns']
    g = nodes['gateway']
    row = dict(hop=h['Hop'], receipt_ms=h['FastMS'], build_ms=h['BuildMS'],
               build=h.get('BuildStagesMS'), save=h.get('BuildSaveStagesMS'),
               gateway_handler_ms=span(g, 'http_handler_enter', 'response_ready'),
               fanout_ms=span(g, 'fanout_ready', 'quorum_collected'),
               final_qc_ms=span(g, 'quorum_collected', 'certificate_verified'))
    row['selected_members'] = sorted([
        dict(node=n, selected_gateway_ms=s['vote_selected']/1e6,
             dispatch_to_selection_ms=span(s, 'dispatch_started', 'vote_selected'),
             member_handler_ms=span(nodes[member_urls[n]], 'http_handler_enter', 'response_ready'),
             member_update_ms=span(nodes[member_urls[n]], 'update_started', 'update_evaluated'),
             member_writer_queue_ms=span(nodes[member_urls[n]], 'commit_requested', 'update_started'))
        for n,s in nodes.items() if n.startswith('member-') and 'vote_selected' in s
    ], key=lambda x:x['selected_gateway_ms'])
    row['http'] = [dict(node=n, reused='http_got_conn_reused' in s,
        fresh='http_got_conn_fresh' in s,
        connection_ms=span(s,'http_get_conn','http_got_conn_fresh' if 'http_got_conn_fresh' in s else 'http_got_conn_reused'),
        response_wait_ms=span(s,'http_request_written','http_first_byte'),
        total_ms=span(s,'http_do_start','http_body_read'),
        member_handler_ms=span(s,'http_handler_enter','response_ready')) for n,s in nodes.items() if n.startswith('http')]
    row['committee_execute_ms'] = [
        (s['ExecuteDoneUnixNS']-s['ExecuteStartUnixNS'])/1e6 for s in settlement.get(h['Hop'],[])
        if s.get('ExecuteStartUnixNS') and s.get('ExecuteDoneUnixNS')]
    rows.append(row)

slow = sorted(rows, key=lambda x:x['receipt_ms'], reverse=True)[:10]
def stats(values):
    v=sorted(x for x in values if x is not None)
    return dict(n=len(v), median=statistics.median(v), maximum=max(v), mean=statistics.mean(v)) if v else {}
out = dict(source=str(a.directory), clock_rule='Gateway dispatch, HTTP and selection use gateway local_ns. Member handler spans use member local_ns. Committee execution spans are internal to each record. No clocks subtracted across processes.',
           limitation='No common monotonic clock between foreground and settlement records: temporal overlap with public execution cannot be established.',
           top10=slow, all_hops=rows,
           summary={k:stats(x[k] for x in rows) for k in ['receipt_ms','build_ms','gateway_handler_ms','fanout_ms','final_qc_ms']},
           connection_groups={label:stats(x['receipt_ms'] for x in rows if any(y['fresh'] for y in x['http']) == fresh) for label,fresh in [('any_fresh',True),('all_reused',False)]})
a.output.write_text(json.dumps(out,indent=2)+'\n')
print(json.dumps({k:v for k,v in out.items() if k not in ['all_hops','top10']},indent=2))
for x in slow:
    print('hop',x['hop'],'receipt',round(x['receipt_ms'],3),'gateway',round(x['gateway_handler_ms'],3),'fanout',round(x['fanout_ms'],3),'fresh',sum(y['fresh'] for y in x['http']), 'third',x['selected_members'][-1])
