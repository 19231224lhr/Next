from pathlib import Path
import subprocess,json,hashlib
R=Path('/Users/richz/lab/man/utxo-review-20261002');O=R/'review-paired-results';B=R/'bin-review2/payctl'
for d in sorted(O.iterdir()):
 if not d.is_dir():continue
 L=R/'.run'/d.name
 with (d/'audit.log').open('w') as f:subprocess.run([str(B),'audit','-dir',str(L)],stdout=f,stderr=f,check=True)
 (d/'audit.json').write_bytes((L/'reports/audit.json').read_bytes())
 for p in (L/'reports').glob('*.json'):
  if p.name!='audit.json':(d/p.name).write_bytes(p.read_bytes())
 if d.name.startswith('pair'):
  lab=json.loads((L/'lab.json').read_text());n=json.loads(Path(lab['Network']).read_text())
  roles={}
  for i,org in enumerate(n['Organizations']):
   owners={bytes(x['Output']['Recipient']['Owner']).hex() for x in n['Genesis']['Outputs'] if x['Output']['Recipient']['Route'].get('Org')==org['Org']}
   assert len(owners)==1,owners
   roles[f'owner-org{i}']=next(iter(owners))
  a=next(x['Recovery'] for x in json.loads((d/'audit.json').read_text()) if x['Name']=='committee0')
  normalized={'user_cal':{role:a['UserCAL'].get(key,0) for role,key in roles.items()},'reserve_balances':[x['Balance'] for x in a['Reserves']],'paid':a['PaidCAL'],'recovered':a['RecoveredCAL']}
  assert set(a['UserCAL']).issubset(set(roles.values()))
  (d/'cal-distribution.json').write_text(json.dumps(normalized,indent=2))
for i in range(3):
 a=json.loads((O/f'pair-{i}-normal/cal-distribution.json').read_text());b=json.loads((O/f'pair-{i}-repair/cal-distribution.json').read_text())
 assert a['user_cal']==b['user_cal'] and a['reserve_balances']==b['reserve_balances'],(a,b)
 print('DISTRIBUTION MATCH',i,a['user_cal'],b['paid'],b['recovered'])
(O/'reaudit-build.json').write_text(json.dumps({'audit_binary_sha256':hashlib.sha256(B.read_bytes()).hexdigest(),'note':'offline audit extended with CAL ownership totals; node execution binaries unchanged'},indent=2))
print('ALL PAIRED CAL DISTRIBUTIONS MATCH')
