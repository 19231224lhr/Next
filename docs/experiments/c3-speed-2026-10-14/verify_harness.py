"""Check invalid chain-length rejection before either runner creates output."""
import ast
import hashlib
import json
import pathlib
import subprocess
import sys

here = pathlib.Path(__file__).resolve().parent
target = here / 'invalid-length-output-must-not-exist'
assert not target.exists()
rows = []
for script in (here/'run_compare.py', here.parent/'c3-issuer-funded-2026-10-13/run_c3.py'):
    ast.parse(script.read_text(encoding='utf-8'))
    for length in ('0', '1001'):
        if script.name == 'run_compare.py':
            args = ['--root', str(target), '--before', 'unused', '--after', 'unused', '--fastpay', 'unused']
        else:
            args = ['--run-dir', str(target), '--bin', 'unused', '--variant', 'c3', '--workload', 'chain']
        command = [sys.executable, str(script), *args, '--length', length]
        run = subprocess.run(command, capture_output=True, text=True)
        assert run.returncode == 2 and 'error:' in run.stderr and not target.exists(), run.stderr
        rows.append(dict(script=str(script), sha256=hashlib.sha256(script.read_bytes()).hexdigest(),
                         length=length, exit=run.returncode, output_not_created=True, stderr=run.stderr))
output = here/'final-verification/harness-boundaries.json'
assert not output.exists()
output.write_text(json.dumps(dict(status='PASS', rows=rows), indent=2)+'\n')
print('PASS: four invalid-length cases, no output directory created; both runner ASTs parsed.')
