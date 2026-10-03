"""Verify fault-window coverage from the offline, signature-checked export."""
import json
from pathlib import Path
E=Path(__file__).resolve().parent
rows=[]
for i in (1,2,3):
    d=E/f'matrix-fd16384/L-C-{i}'
    x=json.loads((E/f'consensus-history-L-C-{i}.json').read_text())
    events=json.loads((d/'events.json').read_text())
    pause=next(e['ns'] for e in events if e['action']=='pause')
    resume=next(e['ns'] for e in events if e['action']=='resume')
    commits=[b for b in x['Blocks'] if pause+1_000_000_000<b['CommitNS']<resume]
    missed=[b for b in commits if b['RoundZeroProposer']==x['Committee3']]
    assert missed and all(b['Round']>0 and x['Committee3'] not in b['Signers'] for b in missed)
    rows.append(dict(run=d.name,verified_blocks=len(x['Blocks']),fault_commits=len(commits),fault_proposer_opportunities=len(missed),heights=[b['Height'] for b in missed],rounds=[b['Round'] for b in missed]))
(E/'consensus-fault-coverage.json').write_text(json.dumps(rows,indent=2)+'\n')
print(json.dumps(rows,indent=2))
