"""Sequential confirmation of the final rebuilt binaries; no concurrent work."""
import pathlib
import subprocess
import sys

HERE = pathlib.Path(__file__).resolve().parent
LAB = pathlib.Path('/home/qyinf/next-c3-speed-20261014')
FASTPAY = pathlib.Path('/home/qyinf/.local/share/next-baseline-tools/fastpay-runtime-sensitivity-target/release')

for length, repeats in ((100, 9), (1000, 7)):
    root = LAB / f'final-build-chain{length}-{repeats}triples'
    command = [sys.executable, str(HERE/'run_compare.py'), '--root', str(root),
               '--before', str(LAB/'frozen-final-client-bin'),
               '--after', str(LAB/'final-compact-bin'), '--fastpay', str(FASTPAY),
               '--fastpay-runtime-threads', '1', '--length', str(length),
               '--repeats', str(repeats), '--fixture', str(LAB/'fixtures/chain1000'),
               '--before-gomaxprocs', '1', '--after-gomaxprocs', '1']
    subprocess.run(command, check=True)
    with (root/'summary-output.log').open('x') as log:
        subprocess.run([sys.executable, str(HERE/'summarize.py'), str(root)],
                       check=True, stdout=log, stderr=subprocess.STDOUT)
    print('COMPLETE ' + str(root), flush=True)
