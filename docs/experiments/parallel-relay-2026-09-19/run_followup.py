"""Isolated baseline repeat, failed gateway submission, then two paced probes."""
import subprocess
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-fastpay-v12')
standard=root/'docs/experiments/stages100-2026-09-19/run_experiment.py'
probe=root/'.run/parallel-relay/run_probe.py'
for args in [
    [standard,'parallel-base-trace2','relay-step2-bin','trace'],
    [standard,'parallel-b-fault','relay-parallel-b-bin','trace','reject-gateway'],
    [probe,'parallel-rate20','relay-profile-bin','20','400'],
    [probe,'parallel-rate40','relay-profile-bin','40','800'],
]:
    subprocess.run(['python3',*map(str,args)],cwd=root,check=True)
