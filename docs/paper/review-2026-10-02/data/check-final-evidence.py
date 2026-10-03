from pathlib import Path
import json,csv,hashlib,statistics
r=Path(__file__).resolve().parents[1];e=r.parents[1]/'experiments/final-evidence-2026-10-04';repo=r.parents[2]
def js(f):return json.loads(f.read_text(encoding='utf-8'))
def csvs(f):return list(csv.DictReader(f.open(encoding='utf-8')))
a=js(e/'results/build.json');b=js(e/'results-mixed/build.json');manifest=js(e/'source-manifest.json')
assert all(manifest['files'][f]==h for f,h in a['source'].items())
assert b['base']==a and b['changed_client_files']==[]
assert a['runner_sha256']==manifest['files']['docs/experiments/final-evidence-2026-10-04/run_chain.py']
assert b['runner_sha256']==manifest['files']['docs/experiments/final-evidence-2026-10-04/run_mixed.py']
for f,h in manifest['files'].items():assert hashlib.sha256((repo/f).read_bytes()).hexdigest()==h,f
for f,h in b['actual_binary_hashes'].items():assert a['binaries'][f]==h,f
c=csvs(e/'results/cases.csv');p=csvs(e/'results-mixed/runs.csv')
f=[x for x in c if x['mode']=='fast'];w=[x for x in c if x['mode']=='wait_final'];median=statistics.median
fm=median(float(x['fast_chain_ms']) for x in f)/1000;wm=median(float(x['fast_chain_ms']) for x in w)/1000
assert round(fm,3)==4.707 and round(wm,3)==64.479
assert len(c)==6 and all(x['audit']=='passed' for x in c)
assert sum(int(x['certified_precommit_successors']) for x in f)==297
assert sum(int(x['payments']) for x in p)==21048
assert sum(int(x['cal_paid']) for x in p)==sum(int(x['cal_recovered']) for x in p)==2400
assert all(int(x['fee_paid'])==659544 and int(x['peak_sent_unfinished'])==256 for x in p)
out={'baseline':manifest['base'],'source_files_matched':len(manifest['files']),'node_binary_hashes_match':True,'chain_payments':600,'fast_seconds':fm,'wait_seconds':wm,'ratio':wm/fm,'mixed_payments':21048,'paid_and_recovered_CAL':2400}
(r/'fourth-revision-2026-10-04/measurement-checks.json').write_text(json.dumps(out,indent=2),encoding='utf-8');print(json.dumps(out,indent=2))
