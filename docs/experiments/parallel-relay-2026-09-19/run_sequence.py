"""Serial fresh-genesis comparisons. Run alone: no builds or other laboratories."""
import subprocess
from pathlib import Path

root = Path('/Users/richz/lab/man/utxo-fastpay-v12')
runner = root / 'docs/experiments/stages100-2026-09-19/run_experiment.py'
for label, binaries in [
    ('parallel-a-plain1', 'relay-memory-a-bin'),
    ('parallel-b-plain1', 'relay-parallel-b-bin'),
    ('parallel-b-plain2', 'relay-parallel-b-bin'),
    ('parallel-a-plain2', 'relay-memory-a-bin'),
    ('parallel-base-plain2', 'relay-step2-bin'),
]:
    subprocess.run(['python3', str(runner), label, binaries, 'plain'], cwd=root, check=True)
