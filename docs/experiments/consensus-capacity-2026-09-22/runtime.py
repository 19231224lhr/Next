from pathlib import Path
import subprocess,time

def resources(lab):
    rows = []
    for line in subprocess.check_output(['ps', '-axo', 'pid=,rss=,time=,command='], text=True).splitlines():
        if str(lab)+'/config/' not in line:
            continue
        pid, rss, cpu, command = line.strip().split(None, 3)
        name = Path(command.split()[-1]).stem
        rows.append({'node': name, 'pid': int(pid), 'rss_kib': int(rss), 'cpu_time': cpu})
    return {'unix_ns': time.time_ns(), 'nodes': rows}
