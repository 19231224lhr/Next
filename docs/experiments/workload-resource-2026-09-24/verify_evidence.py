#!/usr/bin/env python3
"""Check cohort/audit agreement and fingerprint immutable experiment evidence."""
import hashlib,json
from pathlib import Path
OUT=Path(__file__).resolve().parent

def read(p):return json.loads(p.read_text())

def main():
    cases=read(OUT/'suite-complete.json')['cases']
    assert len(cases)==12 and len(set(cases))==12
    sent=warm=0
    for name in cases+['timing-c500']:
        path=OUT/name;s=read(path/'summary.json');a=read(path/'reports/e8-audit.json')
        assert read(path/'passed.json')['correctness_audit']
        assert s['sent']==s['ready'] and s['errors']==s['progress_errors']==0,name
        assert a['Payments']==s['sent']+s['warm_sent'],name
        assert a['Refunds']==a['Payments'] and a['Signers']>=3*a['Payments'],name
        audit=read(path/'reports/audit.json')
        committee=[x for x in audit if x['Name'].startswith('committee')]
        assert len(committee)==4 and len({x['StateHash'] for x in committee})==1,name
        assert all(x['Pending']==0 for x in audit),name
        assert (path/'reports/e8-report.json.gz').is_file(),name
        if name!='timing-c500':sent+=s['sent'];warm+=s['warm_sent']
    evidence={}
    for path in sorted(OUT.rglob('*')):
        if not path.is_file() or len(path.relative_to(OUT).parts)<2:continue
        case=path.relative_to(OUT).parts[0]
        if not case.startswith(('formal-','burst-','probe-','cal-','timing-')):continue
        if path.name in ['summary.json','timeline.csv']:continue
        evidence[str(path.relative_to(OUT)).replace('\\','/')]={'bytes':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
    result={'formal_and_burst_sent':sent,'warm_sent':warm,'passed_cases':cases,'timing_calibration':'timing-c500','evidence':evidence}
    (OUT/'evidence-manifest.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({'sent':sent,'warm':warm,'audited_cases':len(cases)+1,'files':len(evidence)}))

if __name__=='__main__':main()
