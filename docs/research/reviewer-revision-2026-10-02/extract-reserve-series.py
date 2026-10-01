from pathlib import Path
import json
R=Path('/Users/richz/lab/man/utxo-review-20261002')
data={}
for d in (R/'review-owner-recovery-results').glob('*-p5'):
 rows=[]
 for line in (d/'samples.jsonl').read_text().splitlines():
  x=json.loads(line)
  if x.get('node')=='committee0' and 'snapshot' in x:
   rows.append(x)
 data[d.name]=rows
(R/'reserve-series.json').write_text(json.dumps(data))
print({k:len(v) for k,v in data.items()})
