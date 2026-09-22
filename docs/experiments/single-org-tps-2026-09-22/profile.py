from pathlib import Path
import sys,re,time,urllib.request,json
from concurrent.futures import ThreadPoolExecutor
out=Path(sys.argv[1]);port=int(sys.argv[2]);until=time.time()+600
while time.time()<until:
 p=out/'bench.log';s=p.read_text() if p.exists() else '';m=re.search(r'LOAD_STARTED (\d+)',s)
 if m and time.time()>=int(m.group(1))/1e9+8:break
 time.sleep(.5)
else:raise TimeoutError('load not started')
def capture(item):
 name,endpoint=item;started=time.time_ns()
 with urllib.request.urlopen(f'http://127.0.0.1:{endpoint}/debug/pprof/profile?seconds=12',timeout=25) as f:(out/(name+'.cpu')).write_bytes(f.read())
 return dict(node=name,started=started,ended=time.time_ns())
with ThreadPoolExecutor(max_workers=3) as pool:rows=list(pool.map(capture,[('member0',port),('gateway0',port+300),('committee0',port+100)]))
(out/'profile.json').write_text(json.dumps(rows))
