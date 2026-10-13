"""Derive final tables from retained reports after timed jobs have ended."""
import json
import pathlib
import subprocess
import sys

here = pathlib.Path(__file__).resolve().parent
lab = pathlib.Path('/home/qyinf/next-c3-speed-20261014')
jobs = [('summarize.py', [str(lab/'final-build-mixed5pairs')])]
for dataset in ('final-build-chain1000-7triples', 'final-eightcpu-chain1000-7triples'):
    root = lab/dataset
    jobs += [('timing_breakdown.py', [str(root)]),
             ('check_chain_background.py', [str(root), str(root/'background-check.json')]),
             ('analyze_chain_bands.py', [str(root)])]
status = []
for index, (script, arguments) in enumerate(jobs):
    log = lab/f'final-analysis-{index}.log'
    with log.open('x') as output:
        command = [sys.executable, str(here/script), *arguments]
        run = subprocess.run(command, stdout=output, stderr=subprocess.STDOUT)
    status.append(dict(command=command, exit=run.returncode, log=str(log)))
    (lab/'final-analysis-status.json').write_text(json.dumps(status, indent=2)+'\n')
    if run.returncode:
        raise SystemExit(run.returncode)
print('Completed seven report analyses; source timing samples unchanged.')
