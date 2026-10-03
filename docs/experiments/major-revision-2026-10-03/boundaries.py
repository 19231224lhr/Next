"""Summarize exact ledger snapshots, retaining raw evidence separately."""
import base64, json
from pathlib import Path

here=Path(__file__).resolve().parent
case=''
out=[]
for line in (here/'boundaries-reviewed.log').read_text().splitlines():
    if line.startswith('=== RUN'): case=line.split()[-1]
    if 'BOUNDARY ' not in line: continue
    r=json.loads(line.split('BOUNDARY ',1)[1])
    fees=[]
    for row in r['ledger_rows']:
        key=base64.b64decode(row['Key'])
        if key[2]==103:
            fees.append(json.loads(base64.b64decode(row['Value']))['Fee'])
    sums={k:sum(f[k] for f in fees) for k in ['Maximum','Held','Rewards','Burned','Refunded']}
    assert sums['Maximum']==sum(sums[k] for k in ['Held','Rewards','Burned','Refunded'])
    out.append(dict(case=case,label=r['label'],height=r['height'],
        A_CAL=r['org0_CAL'],B_CAL=r['org1_CAL'],A_FUEL=r['org0_FUEL'],B_FUEL=r['org1_FUEL'],
        A_usage=r['org0_usage'],A_members=r['org0_member_CAL'],fee_states=fees,fee_totals=sums,
        asset_projection=r['money']))
(here/'boundary-summary.json').write_text(json.dumps(out,indent=2))
for r in out:
    print(r['case'],r['label'],r['A_CAL'],r['A_usage'],r['fee_totals'])
