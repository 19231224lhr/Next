# Captured on the experiment host after each laboratory is stopped.
from pathlib import Path
import json,statistics,hashlib,subprocess
R=Path('/Users/richz/lab/man/utxo-review-20261002')
out={'paired':[],'chains':[],'owner_matrix':[],'storage_tps':[]}
for d in sorted((R/'review-paired-results').iterdir()):
 if not d.is_dir():continue
 if (d/'bench-v4-100.json').exists():
  s=json.loads((d/'bench-v4-100.json').read_text())['Summary']
  out['paired'].append({'name':d.name,**s,'cal_distribution':json.loads((d/'cal-distribution.json').read_text())})
 elif (d/'chain-v4.json').exists():
  x=json.loads((d/'chain-v4.json').read_text())
  out['chains'].append({'name':d.name,**{k:v for k,v in x.items() if k!='Hops'},'hops':len(x['Hops']),'hop_fast_p50_ms':statistics.median(h['FastMS'] for h in x['Hops'])})
for d in sorted((R/'review-owner-recovery-results').iterdir()):
 if not d.is_dir() or not (d/'summary.json').exists():continue
 s=json.loads((d/'summary.json').read_text());a=json.loads((d/'reports/audit.json').read_text());c=next(x for x in a if x['Name']=='committee0')
 samples=[]
 for line in (d/'samples.jsonl').read_text().splitlines():
  x=json.loads(line)
  if x.get('node')=='committee0' and 'snapshot' in x:samples.append(x['snapshot'])
 row={'name':d.name,**s,'recovery':c['Recovery'],'min_reserve_cal':min(x['CAL'] for x in samples),'final_fee':json.loads((d/'last-snapshots.json').read_text())['committee0']['Detail']['Fee']}
 out['owner_matrix'].append(row)
for d in sorted((R/'review-storage-tps-results').glob('*')):
 if not d.is_dir() or not (d/'audit.json').exists():continue
 s=json.loads((d/'bench-v4-0.json').read_text())['Summary'];out['storage_tps'].append({'name':d.name,**s})
out['chains_memory']=[]
for d in sorted((R/'review-chain-memory-results').glob('*')):
 if not (d/'chain-v4.json').exists():continue
 x=json.loads((d/'chain-v4.json').read_text())
 out['chains_memory'].append({'name':d.name,**{k:v for k,v in x.items() if k!='Hops'},'hops':len(x['Hops']),'hop_fast_p50_ms':statistics.median(h['FastMS'] for h in x['Hops'])})
out['binary_manifest']={}
for folder in ['bin','bin-review2','bin-review3']:
 out['binary_manifest'][folder]={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (R/folder).glob('*') if p.is_file()}
(R/'review-summary.json').write_text(json.dumps(out,indent=2))
print(json.dumps({k:len(v) for k,v in out.items()}))
