#!/usr/bin/env python3
"""Cross-check stopped reports; incomplete low-budget business is not success."""
import argparse
import json
from analyze import OUT, read


def verify(path):
    summary=read(path/'summary.json')
    audit=read(path/'reports/audit.json')
    payments=read(path/'reports/budget-audit.json')['Payments']
    report=read(path/'reports/budget-v4.json')
    committee=[r for r in audit if r['Name'].startswith('committee')]
    assert len(committee)==4, path
    assert len({r['StateHash'] for r in committee})==1, path
    assert all(r['Gap']=='0' and r['Closed']==r['Payments']==summary['payments'] for r in committee),path
    assert summary['offered']==summary['admitted']+summary['not_started']==report['Offered'],path
    assert len(report['Units'])==summary['admitted'],path
    public=[p for p in payments if p['Public']]
    assert len(public)==summary['payments'],path
    assert sum(p['Ready'] for p in payments)==summary['parent_ready']+summary['child_ready'],path
    assert all(p['Signers']>=3 for p in payments if p['Ready']),path
    assert all(p['ClosedSigners']==p['Signers'] for p in public),path
    for p in public:
        fee=p['Fee']
        assert fee['Closed'] and fee['Held']==0,path
        assert fee['Maximum']==fee['Rewards']+fee['Burned']+fee['Refunded'],path
    assert sum(p['Fee']['Rewards'] for p in public)==int(committee[0]['Rewards']),path
    assert sum(p['Fee']['Burned'] for p in public)==int(committee[0]['Burned']),path
    partial=sum(0<p['Signers']<3 and not p['Ready'] for p in payments)
    return {'case':path.name,'state_consistent':True,'public_gap':0,'public_payments':len(public),
            'closed_units':summary['closed_units'],'offered_units':summary['offered'],
            'partial_approval_transactions':partial,'revisions':committee[0].get('Revisions',0)}


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--prefix',default='r3-');args=p.parse_args()
    cases=[d for d in sorted(OUT.glob(args.prefix+'*')) if d.is_dir() and (d/'summary.json').exists()]
    if not cases:raise SystemExit('No completed cases')
    result=[verify(d) for d in cases]
    for d in cases:
        if not d.name.endswith('-serial'):continue
        other=d.with_name(d.name.removesuffix('-serial')+'-parallel')
        if not (other/'summary.json').exists():continue
        serial=read(d/'configuration.json');parallel=read(other/'configuration.json')
        assert serial.pop('serial_signing') is True and parallel.pop('serial_signing') is False,d
        assert serial==parallel,('mismatched pair configuration',d)
        sn=read(d/'network.json');pn=read(other/'network.json')
        sn.pop('GenesisTime');pn.pop('GenesisTime')
        assert sn==pn,('mismatched pair genesis',d)
    (OUT/(args.prefix+'verification.json')).write_text(json.dumps(result,indent=2)+'\n')
    print('Verified',len(result),'completed case reports; public payments:',sum(r['public_payments'] for r in result))
