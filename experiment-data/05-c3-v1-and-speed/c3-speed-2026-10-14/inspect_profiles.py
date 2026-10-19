"""Export CPU and Go runtime-trace diagnostics without corrupting binary output."""
import argparse
import pathlib
import subprocess

p = argparse.ArgumentParser()
p.add_argument('root', type=pathlib.Path)
p.add_argument('--go', default='go')
p.add_argument('--bin', type=pathlib.Path, required=True)
a = p.parse_args()
for node, binary in [('gateway0', 'gateway'), ('org0-member0', 'member'), ('committee0', 'committee')]:
    cpu = a.root / (node + '.pprof')
    for suffix, options in [('cpu-flat', []), ('cpu-cum', ['-cum'])]:
        result = subprocess.run([a.go, 'tool', 'pprof', '-top', '-nodecount=60', *options,
                                 str(a.bin / binary), str(cpu)], capture_output=True, check=True)
        (a.root / (node + '-' + suffix + '.txt')).write_bytes(result.stdout)
    trace = a.root / (node + '.trace')
    if trace.exists():
        for kind in ['sched', 'sync', 'syscall', 'net']:
            profile = a.root / (node + '-' + kind + '.pprof')
            with profile.open('wb') as out:
                subprocess.run([a.go, 'tool', 'trace', '-pprof=' + kind, str(trace)], stdout=out, check=True)
            result = subprocess.run([a.go, 'tool', 'pprof', '-top', '-nodecount=30', str(profile)], capture_output=True, check=True)
            (a.root / (node + '-' + kind + '.txt')).write_bytes(result.stdout)
    print(node + ': exported CPU and available runtime profiles')
