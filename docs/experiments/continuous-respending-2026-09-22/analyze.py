#!/usr/bin/env python3
"""Join wallet events with same-host committee commit events; never infer from polling lag."""
import csv
import argparse
import json
from pathlib import Path
from statistics import median

ROOT=Path(__file__).resolve().parent


def analyze(case):
    report=json.loads((case/'reports/chain-v4.json').read_text())
    events=json.loads((case/'settlements.json').read_text())
    commits={}
    heights={}
    for h in report['Hops']:
        h['Fact']=bytes(h['Fact']).hex()
        ts=[e['CommittedUnixNS'] for node in events.values() for e in (node.get(h['Fact']) or []) if e['CommittedUnixNS']>0]
        if ts:commits[h['Fact']]=min(ts)
        hh=[e['Height'] for node in events.values() for e in (node.get(h['Fact']) or []) if e['CommittedUnixNS']>0]
        if hh:heights[h['Fact']]=min(hh)
    hop_rows=[]
    for i,h in enumerate(report['Hops']):
        previous=report['Hops'][i-1] if i else None
        parent_commit=commits.get(previous['Fact']) if previous else None
        unconfirmed=None if parent_commit is None else h['SentUnixNS']<parent_commit
        row=dict(case=case.name,mode=report['Mode'],length=report['Requested'],hop=i+1,
                 build_ms=h['BuildMS'],fast_ms=h['FastMS'],ready_offset_ms=h['ReadyOffsetMS'],
                 final_offset_ms=h['FinalOffsetMS'],member_offset_ms=h['MemberOffsetMS'],
                 respending_gap_ms=(h['SentOffsetMS']-previous['ReadyOffsetMS'] if 'SentOffsetMS' in h else (h['SentUnixNS']-previous['ReadyUnixNS'])/1e6) if previous else None,
                 attempts=h.get('Attempts',1),
                 progress_errors=h.get('ProgressErrors',0),
                 certificate_input=h['CertificateInput'],parent_uncommitted_at_send=unconfirmed,
                 request_bytes=h['RequestBytes'],certificate_bytes=h['CertificateBytes'],
                 commit_offset_ms=(commits[h['Fact']]-report['StartedUnixNS'])/1e6 if h['Fact'] in commits else None)
        hop_rows.append(row)
    successors=hop_rows[1:]
    fast=sorted(h['fast_ms'] for h in hop_rows)
    audit_path=case/'reports/chain-audit.json'
    obligations=(json.loads(audit_path.read_text()).get('obligations') or []) if audit_path.exists() else []
    intervals=[]
    for ob in obligations:
        child=heights.get(bytes(ob['Consumer']).hex())
        parent=heights.get(bytes(ob['Certificate']).hex())
        if child is not None and parent is not None:intervals.append((child,parent))
    peak=max((sum(a<=height<b for a,b in intervals) for height in heights.values()),default=0)
    row=dict(case=case.name,mode=report['Mode'],length=report['Requested'],
             fast_chain_ms=report['FastChainMS'],public_chain_ms=report['PublicChainMS'],closed_chain_ms=report['ClosedChainMS'],
             fast_p50_ms=median(fast),fast_p95_ms=fast[int((len(fast)-1)*.95)] if len(fast)>1 else None,
             first_hop_ms=hop_rows[0]['fast_ms'],successor_count=len(successors),
             certificate_inputs=sum(h['certificate_input'] for h in successors),
             submit_attempts=sum(h['attempts'] for h in hop_rows),
             progress_errors=sum(h['progress_errors'] for h in hop_rows),
             missing_input_obligations=len(obligations),peak_open_at_block_end=peak,
             parent_uncommitted=sum(h['parent_uncommitted_at_send'] is True for h in successors),
             unknown_parent_commit=sum(h['parent_uncommitted_at_send'] is None for h in successors),
             successor_gap_p50_ms=median(h['respending_gap_ms'] for h in successors) if successors else None,
             request_min_bytes=min(h['request_bytes'] for h in hop_rows),request_max_bytes=max(h['request_bytes'] for h in hop_rows),
             certificate_min_bytes=min(h['certificate_bytes'] for h in hop_rows),certificate_max_bytes=max(h['certificate_bytes'] for h in hop_rows),
             error=report.get('Error',''))
    return row,hop_rows


def diagnostic_stages(case):
    report=json.loads((case/'reports/chain-v4.json').read_text())
    pairs=[('gateway','http_handler_enter','request_decoded'),('gateway','request_decoded','owner_verified'),
           ('gateway','fanout_ready','quorum_collected'),('gateway','quorum_collected','certificate_verified'),
           ('member','request_decoded','validation_complete'),('member','commit_requested','update_started'),
           ('member','update_started','update_evaluated'),('member','update_evaluated','commit_returned'),
           ('member','commit_returned','vote_signed')]
    result=[]
    for kind,before,after in pairs:
        values=[]
        for h in report['Hops']:
            nodes=['gateway'] if kind=='gateway' else [f'http://127.0.0.1:{26000+i}' for i in range(4)]
            for node in nodes:
                events={e['stage']:e['local_ns'] for e in h.get('Foreground',[]) if e['node']==node}
                if before in events and after in events:values.append((events[after]-events[before])/1e6)
        if values:result.append(dict(role=kind,start=before,end=after,samples=len(values),median_ms=median(values),max_ms=max(values)))
    return result


def csv_write(path,rows):
    with path.open('w',newline='') as out:
        writer=csv.DictWriter(out,fieldnames=list(rows[0]),lineterminator='\n');writer.writeheader();writer.writerows(rows)


if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('--prefix',default='v3-',help='only analyze this experiment revision')
    args=parser.parse_args()
    cases=[];hops=[]
    for case in sorted(ROOT.iterdir()):
        if case.name.startswith(args.prefix) and (case/'settlements.json').exists():
            row,hh=analyze(case);cases.append(row);hops.extend(hh)
    csv_write(ROOT/'cases.csv',cases);csv_write(ROOT/'hops.csv',hops)
    (ROOT/'analysis.json').write_text(json.dumps(cases,indent=2)+'\n')
    if (ROOT/'v3-diagnostic10/reports/chain-v4.json').exists():
        csv_write(ROOT/'diagnostic-stages.csv',diagnostic_stages(ROOT/'v3-diagnostic10'))
    print(json.dumps(cases,indent=2))
