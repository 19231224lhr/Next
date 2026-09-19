import subprocess
from pathlib import Path
root=Path('/Users/richz/lab/man/utxo-fastpay-v12')
probe=root/'.run/parallel-relay/run_probe.py'
for rate,count in [(20,400),(40,800)]:
    subprocess.run(['python3',str(probe),f'parallel-final-rate{rate}','relay-final-bin',str(rate),str(count)],cwd=root,check=True)
