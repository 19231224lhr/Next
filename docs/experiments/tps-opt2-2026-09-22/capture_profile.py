
from pathlib import Path
import time,re,urllib.request
d=Path(__file__).parent
p=d/'profile/bench.log'
deadline=time.time()+600
while time.time()<deadline:
 s=p.read_text() if p.exists() else ''
 m=re.search(r'LOAD_STARTED (\d+)',s)
 if m:
  due=int(m.group(1))/1e9+50
  if time.time()>=due:
   with urllib.request.urlopen('http://127.0.0.1:24300/debug/pprof/profile?seconds=10',timeout=20) as f:(d/'profile/gateway-cpu.pprof').write_bytes(f.read())
   print('CPU_PROFILE_SAVED',flush=True)
   break
 time.sleep(1)
else:raise TimeoutError('no load start')
