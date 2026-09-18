import json, os, pathlib, signal, subprocess, sys, time, urllib.request

root=pathlib.Path.cwd()
label, flush, count=sys.argv[1:]
lab=root/'experiments'/label
env=dict(os.environ, UTXO_EXPERIMENT_FLUSH=flush, UTXO_EXPERIMENT_GOSSIP=flush)
env.pop('UTXO_SETTLEMENT_TRACE',None)
subprocess.run([str(root/'bin/payctl'),'init-lab','-dir',str(lab),'-port','22000','-outputs','64'],check=True)
assert not (lab/'committee0/committee.db').exists()
log=(lab/'supervisor.log').open('w')
proc=subprocess.Popen([str(root/'bin/payctl'),'lab-run','-dir',str(lab),'-bin',str(root/'bin')],env=env,stdout=log,stderr=log)
def get(url):
    with urllib.request.urlopen(url,timeout=2) as r:return json.load(r)
samples=[]
try:
    for _ in range(150):
        if proc.poll() is not None:raise RuntimeError('lab exited')
        try:
            heights=[int(get(f'http://127.0.0.1:{22100+n}/healthz')['height']) for n in range(4)]
            with urllib.request.urlopen('http://127.0.0.1:22300/healthz',timeout=2) as r:assert r.status==200
            if min(heights)>=3:break
        except (OSError,ValueError):pass
        time.sleep(.2)
    else:raise RuntimeError('not ready')
    time.sleep(3)
    for i in range(int(count)):
        p=subprocess.run([str(root/'bin/payctl'),'demo','-dir',str(lab),'-hops','1','-input',str(i),'-observe-block'],env=env,capture_output=True,text=True,timeout=100,check=True)
        report=pathlib.Path(p.stdout.rsplit('Report: ',1)[1].strip())
        sample=json.loads(report.read_text())['Samples'][0]
        assert 'Trace' not in sample and 'BackendTrace' not in sample
        samples.append(sample)
        print(label,i,'ready',sample['WalletReadyMicros']/1000,'commit_observed',sample['CommitObservedMicros']/1000,'proof',sample['FinalProofMicros']/1000,'height',sample['SettlementHeight'],flush=True)
        time.sleep(.3)
    # Let persisted relay work drain before shutdown, without affecting timings.
    time.sleep(2)
    heights=[int(get(f'http://127.0.0.1:{22100+n}/healthz')['height']) for n in range(4)]
finally:
    proc.send_signal(signal.SIGINT)
    proc.wait(timeout=35)
    log.close()
out=dict(label=label,flush=flush,gossip=flush,tracing=False,new_genesis=True,final_heights=heights,samples=samples)
(lab/'result.json').write_text(json.dumps(out,indent=2))
print('COMPLETE',label,flush=True)
