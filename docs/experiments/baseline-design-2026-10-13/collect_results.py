#!/usr/bin/env python3
"""Copy public measurement evidence, leaving generated private keys and DBs local."""
import argparse
import pathlib
import shutil

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('source',type=pathlib.Path)
p.add_argument('destination',type=pathlib.Path)
a=p.parse_args()
a.destination.mkdir(parents=True,exist_ok=False)
for f in a.source.iterdir():
    if f.is_file() and f.suffix in ('.json','.txt'):
        shutil.copy2(f,a.destination/f.name)
    elif f.is_dir() and f.name.startswith('r'):
        target=a.destination/f.name
        target.mkdir()
        for name in ('native-client.json','native-client.stderr'):
            if (f/name).is_file(): shutil.copy2(f/name,target/name)
        reports=f/'reports'
        if reports.exists():
            (target/'reports').mkdir()
            for name in ('chain-v4.json','chain-follow-timing.json','bench-v4-0.json'):
                if (reports/name).is_file(): shutil.copy2(reports/name,target/'reports'/name)
        logs=f/'adapter-logs'
        if logs.exists():
            (target/'adapter-logs').mkdir()
            for log in logs.iterdir():
                if log.is_file() and log.suffix in ('.json','.log'):
                    shutil.copy2(log,target/'adapter-logs'/log.name)
        for log in f.glob('server*.log'):
            shutil.copy2(log,target/log.name)
print(a.destination)
