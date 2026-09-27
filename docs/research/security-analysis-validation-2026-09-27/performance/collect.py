import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import tarfile

lab = Path('/Users/richz/lab/man')
assert (lab/'security-performance.exit').read_text().strip() == '0'
spec = importlib.util.spec_from_file_location('analyze', lab/'security-e5-analyze.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
out = lab/'security-performance-results'
out.mkdir(exist_ok=True)
summaries, curves, manifest = [], [], {}
for label, directory in [('baseline','utxo-security-baseline-20260927'), ('candidate','utxo-security-review-20260927')]:
    for i in range(1,4):
        p = lab/directory/'.run/security-performance'/(label+'-r'+str(i))
        assert (p/'passed.json').exists()
        summary, curve = m.analyze_case(p)
        assert summary['sent'] == summary['ready'] == summary['public'] == summary['members']
        summaries.append(summary)
        curves.extend(curve)
        dest = out/p.name
        dest.mkdir(exist_ok=True)
        for name in ['configuration.json','build.json','passed.json','payments.jsonl.gz','resources.jsonl','reports/audit.json','audit.log']:
            shutil.copyfile(p/name, dest/Path(name).name)
        manifest[p.name] = {str(n.relative_to(p)): hashlib.sha256(n.read_bytes()).hexdigest() for n in p.rglob('*') if n.is_file()}
m.write_csv(out/'matrix.csv',summaries)
m.write_csv(out/'curves.csv',curves)
(out/'summary.json').write_text(json.dumps(summaries,indent=2)+'\n')
(out/'raw-file-sha256.json').write_text(json.dumps(manifest,indent=2)+'\n')
for src, dst in [('security-performance.py','run.py'),('security-performance-collect.py','collect.py'),('security-performance.log','run.log')]:
    shutil.copyfile(lab/src,out/dst)
with tarfile.open(lab/'security-performance-results.tgz','w:gz') as archive:
    archive.add(out,arcname='performance')
for s in summaries:
    print(json.dumps({k:s[k] for k in ['case','sent','ready','public','members','send_span_s','elapsed_s','ready_ms_p50','ready_ms_p95','public_ms_p50','members_ms_p50','drain_from_last_send_s']}))
