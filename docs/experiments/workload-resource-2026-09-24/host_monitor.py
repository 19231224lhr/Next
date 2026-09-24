#!/usr/bin/env python3
"""Supplementary host memory observations; starts after formal A1, not per-process I/O."""
import json,subprocess,time
from pathlib import Path
OUT=Path(__file__).resolve().parent
deadline=time.monotonic()+3*3600
with (OUT/'host-memory.jsonl').open('a') as f:
    while time.monotonic()<deadline and not (OUT/'suite-complete.json').exists():
        row={'NS':time.time_ns(),'swap':subprocess.check_output(['sysctl','vm.swapusage'],text=True),'vm_stat':subprocess.check_output(['vm_stat'],text=True)}
        f.write(json.dumps(row)+'\n');f.flush();time.sleep(30)
