from pathlib import Path
import importlib.util,os,sys,json,subprocess,hashlib,bisect,shutil,time
R=Path(__file__).resolve().parents[3];D=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('harness',D/'harness.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
h.DEST=D;h.helper.DEST=D;h.SOURCE=R/'.run/consensus-1000-rollback-template';h.FIXTURE=R/'docs/experiments/consensus-1000-rollback-2026-09-21/offline-independent-180000.jsonl';h.BIN=R/'.run/consensus-capacity-2026-09-22-bin';h.CTL=h.BIN/'payctl'
for key in list(os.environ):
 if key.startswith('COMMITTEE_') or key.startswith('UTXO_'):os.environ.pop(key)
os.environ.update(UTXO_EXPERIMENT_MEM_BLOCKSTORE='1',COMMITTEE_COMMIT_MS='500',COMMITTEE_PROGRESS='1',COMMITTEE_SAMPLE_RSS='1',COMMITTEE_DRAIN_SECONDS='120',COMMITTEE_WORKLOAD='Independent final genesis UTXOs, offline signatures, hash or single committee entrypoint as recorded, only four validators active')
for key,value in list(os.environ.items()):
 if key.startswith('BENCH_'): os.environ[key[len('BENCH_'):]]=value
h.FIXTURE=Path(os.environ.get('BENCH_FIXTURE',str(h.FIXTURE)))
h.SOURCE=Path(os.environ.get('BENCH_TEMPLATE',str(h.SOURCE)))
source={'commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip(),'fixture_sha256':hashlib.sha256(h.FIXTURE.read_bytes()).hexdigest(),'binaries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in h.BIN.iterdir() if p.is_file()},'storage':'Comet MemDB; application '+('ephemeral memory' if os.environ.get('UTXO_EXPERIMENT_COMMITTEE_MEMORY')=='1' else 'synchronous bbolt')+'; WAL/FilePV unchanged','mempool':10000,'commit_ms':500}
(D/'source.json').write_text(json.dumps(source,indent=2))
for job in sys.argv[1:]:
 label,rate,count=job.split(':');rate=float(rate);count=int(count)
 assert label.replace('-','').isalnum() and 1<=count<=540000 and rate>0
 print('START',label,rate,count,flush=True);h.trial(label,os.environ.get('BENCH_ARM','current'),rate,count)
 e=D/label;s=json.loads((e/'send.json').read_text());bs=json.loads((e/'blocks.json').read_text());a=json.loads((e/'audit.json').read_text());ss=sorted(s['Samples'],key=lambda x:x['sent_unix_ns']);first=ss[0]['sent_unix_ns'];last=ss[-1]['sent_unix_ns'];end=max(x['observed_ns'] for x in bs if x['transactions']);sent=[x['sent_unix_ns'] for x in ss];accepted=sorted(x['returned_unix_ns'] for x in ss if x['status']==202 and not x.get('error'));successful=sum(x['successful'] for x in bs);included=sum(x['transactions'] for x in bs)
 windows=[];previous_sent=previous_done=0;span=(last-first)/1e9
 for sec in range(10,int((end-first)/1e9)+11,10):
  t=first+sec*10**9;done=sum(x['successful'] for x in bs if x['observed_ns']<=t);sentn=bisect.bisect_right(sent,t);acc=bisect.bisect_right(accepted,t)
  windows.append(dict(end_s=sec,sent_tps=(sentn-previous_sent)/10,commit_tps=(done-previous_done)/10,sent=sentn,accepted=acc,committed=done,accepted_not_observed=max(0,acc-done),sent_not_observed=sentn-done,complete_send_window=sec<=span));previous_sent=sentn;previous_done=done
 errs={}
 for x in ss:
  if x.get('error'):errs[x['error']]=errs.get(x['error'],0)+1
 result=dict(label=label,target_tps=rate,count=count,sender=s['Summary'],send_span_s=span,complete_observed_s=(end-first)/1e9,completed_batch_tps=successful/((end-first)/1e9),drain_observed_s=max(0,(end-last)/1e9),successful=successful,execution_failed=included-successful,committees_equal=len({x['StateHash'] for x in a})==1,pending=sum(x['Pending'] for x in a),windows=windows,errors=errs,max_block_transactions=max(x['transactions'] for x in bs))
 (e/'summary.json').write_text(json.dumps(result,indent=2));print('RESULT',label,json.dumps({k:v for k,v in result.items() if k not in ['windows','errors']}),flush=True)
 assert result['committees_equal'] and not result['execution_failed']
 lab=R/'.run'/('group-'+label);shutil.copytree(lab/'logs',e/'node-logs',dirs_exist_ok=True)
 processes=subprocess.check_output(['ps','-axo','command'],text=True)
 for p in [lab,R/'.run'/('bin-'+label)]:
  assert p.resolve().parent==(R/'.run').resolve() and p.name in ['group-'+label,'bin-'+label]
  assert not any(str(p)+' ' in row or str(p)+'/' in row for row in processes.splitlines())
  shutil.rmtree(p)
 print('CLEANED',label,flush=True)
