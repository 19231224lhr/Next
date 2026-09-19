"""Additional matched runs prompted by the unresolved foreground-latency tradeoff."""
import subprocess
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-fastpay-v12')
runner=root/'docs/experiments/stages100-2026-09-19/run_experiment.py'
for i in [3,4]:
    for name,binary in [('base','relay-step2-bin'),('b','relay-parallel-b-bin')]:
        subprocess.run(['python3',str(runner),f'parallel-{name}-plain{i}',binary,'plain'],cwd=root,check=True)
