#!/usr/bin/env python3
"""Recompute measurements from raw samples; no successful-only denominator."""
import json
import math
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2] / 'docs/experiments/lightning-2026-09-24'

def percentile(v, q):
    return sorted(v)[max(0, math.ceil(len(v)*q)-1)] if v else None

def summarize(directory):
    rows = json.loads((directory / 'payments.json').read_text())
    params = json.loads((directory / 'parameters.json').read_text())
    reconciliation = json.loads((directory / 'reconciliation.json').read_text())
    original = json.loads((directory / 'summary.json').read_text())
    audit = {r['hash']: r for r in reconciliation}
    sent = [r for r in rows if r['sent_ns'] > 0]
    ok = [r for r in sent if r['status'] == 'SUCCEEDED']
    recv = [r for r in sent if r['settled_ns'] > 0]
    lat = [(r['settled_ns']-r['sent_ns'])/1e6 for r in recv]
    span = (max(r['sent_ns'] for r in sent)-min(r['sent_ns'] for r in sent))/1e9 if len(sent)>1 else 0
    audit_ok = all(audit.get(r['hash'],{}).get('state') == 'SETTLED' and
                   audit[r['hash']].get('paid_msat') == params['amount_sat']*1000 for r in ok)
    result = dict(case=str(directory.relative_to(ROOT)),planned=len(rows),sent=len(sent),
                  sender_succeeded=len(ok),receiver_observed=len(recv),
                  unique_hashes=len({r['hash'] for r in rows}),invoice_audit_ok=audit_ok,
                  elapsed_seconds=original['ElapsedSeconds'],
                  success_tps_with_drain=original['SuccessTPS'],send_span_seconds=span,
                  actual_send_tps=(len(sent)-1)/span if span else None,
                  receiver_p50_ms=percentile(lat,.5),receiver_p95_ms=percentile(lat,.95),
                  receiver_p99_ms=percentile(lat,.99),sender_p50_ms=original['SenderMS']['P50'],
                  dispatch_p95_ms=original['DispatchMS']['P95'],
                  fee_msat=sum(r['fee_msat'] for r in ok))
    if params['duration_seconds']:
        end=params['start_offset_ns']+params['duration_seconds']*1e9
        result['success_in_send_window']=sum(r['success_ns']<=end for r in ok)
        result['drain_seconds']=max(0,original['ElapsedSeconds']-params['duration_seconds'])
    after=json.loads((directory/'after.json').read_text())
    result['pending_htlcs_after']=sum(len(c.get('pending_htlcs',[])) for n in after for c in n['channels'].get('channels',[]))
    result['failures']={}
    for r in sent:
        if r['status']!='SUCCEEDED':
            key=r.get('error',r['status']);result['failures'][key]=result['failures'].get(key,0)+1
    return result

if __name__=='__main__':
    results=[]
    for path in sorted(ROOT.glob('*/*/after.json')):
        results.append(summarize(path.parent))
    (ROOT/'analysis.json').write_text(json.dumps(results,indent=2))
    print(json.dumps(results,indent=2))
