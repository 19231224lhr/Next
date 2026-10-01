import importlib.util,json,subprocess,hashlib
from pathlib import Path
ROOT=Path('/Users/richz/lab/man/utxo-review-20261002')
OUT=ROOT/'review-owner-recovery-results';OUT.mkdir(exist_ok=True)
spec=importlib.util.spec_from_file_location('owner',ROOT/'docs/experiments/owner-fuel-2026-09-23/reproduce.py')
owner=importlib.util.module_from_spec(spec);spec.loader.exec_module(owner)
e2=owner.e2;e2.OUT=OUT;e2.BIN=ROOT/'bin-review2';e2.labutil.OUT=OUT;e2.labutil.BIN=e2.BIN
e2.ENV.update(UTXO_EXPERIMENT_MEM_BLOCKSTORE='0',UTXO_EXPERIMENT_COMMITTEE_MEMORY='0',UTXO_EXPERIMENT_SERIAL_DIRECT='0')
base_configure=e2.configure;base_command=e2.command
PERCENT=0;SEED=23

def configure(label,count,kind=None,grant=None):
 d,r,l,n=base_configure(label,count,1,60000)
 e2.write(d/'recovery-config.json',{'percent':PERCENT,'seed':SEED,'parent_release':'after four verified materialized repairs','rules':'DIRECT_ACCOUNTING_V4_SOURCE_RECOVERY','member_state':'memory','committee_state':'disk','user_fuel':True})
 return d,r,l,n

def command(args,log):
 if len(args)>1 and args[1]=='budget-v4':args=list(args)+['-repair-percent',PERCENT,'-repair-seed',SEED]
 return base_command(args,log)

e2.configure=configure;e2.command=command
for seed,order in [(23,[0,1,5]),(37,[1,5,0]),(59,[5,0,1])]:
 for pct in order:
  PERCENT=pct;SEED=seed
  dest=owner.run(f's{seed}-p{pct}',duration=60,delay=1,drain=90,disk=True)
  report=json.loads((dest/'reports/budget-v4.json').read_text())
  audit=json.loads((dest/'reports/audit.json').read_text())
  committees=[r for r in audit if r['Name'].startswith('committee')]
  expected=sum(bool(u.get('RepairExpected')) for u in report['Units'])
  assert len(committees)==4 and len({r['StateHash'] for r in committees})==1
  assert all(r['Pending']==0 for r in audit)
  assert report['Admitted']==1200 and report['NotStarted']==0
  for r in committees:
   a=r['Recovery'];assert a['Open']==a['Repaired']==0 and a['Recovered']==expected,(expected,a)
   assert a['PaidCAL']==a['RecoveredCAL']==100*expected
   assert all(x['Spent']==x['Reserved']==0 for x in a['Reserves'])
   assert r['Payments']==r['Closed']==2400 and r['Gap']=='0'
  print('RECOVERY PASS',dest.name,expected,flush=True)
print('ALL OWNER-FUEL MATRIX PASS',flush=True)
