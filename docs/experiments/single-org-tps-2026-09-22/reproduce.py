#!/usr/bin/env python3
"""Build every node from the checkout and run a fresh full-payment experiment.
No frozen binaries or old experimental scripts are required. Diagnostic overlays
only enlarge genesis/count limits, prepare fixtures before timing, sample traces,
and keep independent-input failures visible without canceling the remaining load.
"""
import argparse, hashlib, json, os, re, shutil, signal, subprocess, sys, time, urllib.request
from pathlib import Path
R=Path(__file__).resolve().parents[3]
D=Path(__file__).resolve().parent

def replace_once(text, old, new):
    if text.count(old)!=1: raise RuntimeError("source anchor changed: "+old[:90])
    return text.replace(old,new,1)

def main():
    ap=argparse.ArgumentParser(description=__doc__)
    ap.add_argument("label");ap.add_argument("--count",type=int,default=48000)
    ap.add_argument("--rate",type=float,default=500);ap.add_argument("--port",type=int,default=24000)
    ap.add_argument("--trace-every",type=int,default=100)
    ap.add_argument("--template",type=Path)
    ap.add_argument("--member-memory",action="store_true")
    ap.add_argument("--wallet-no-sync",action="store_true")
    ap.add_argument("--gateway-memory",action="store_true")
    ap.add_argument("--no-trace",action="store_true")
    ap.add_argument("--unbatched-progress",action="store_true")
    ap.add_argument("--max-pending",type=int,default=2048)
    ap.add_argument("--profile",action="store_true")
    ap.add_argument("--conn-stats",action="store_true")
    ap.add_argument("--no-descriptor-cache",action="store_true")
    ap.add_argument("--committee-memory",action="store_true")
    ap.add_argument("--commit-ms",type=int,choices=[250,500],default=500)
    ap.add_argument("--member-foreground",type=int,choices=[128,256],default=256)
    a=ap.parse_args()
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,40}",a.label) or not 1<=a.count<=198000 or not 0<a.rate<=10000 or a.trace_every<1:
        ap.error("invalid label, count, rate or sampling")
    if a.count/a.rate>240: ap.error("keep the offered interval below the benchmark's five-minute deadline")
    E=D/a.label;E.mkdir();B=R/'.run'/('repro-'+a.label+'-bin');B.mkdir(parents=True)
    L=R/'.run'/('repro-'+a.label)
    go=shutil.which('go') or '/usr/local/go/bin/go'
    env=dict(os.environ);env['PATH']=str(Path(go).parent)+os.pathsep+env.get('PATH','')
    def command(args, log): return subprocess.run([str(x) for x in args],cwd=R,env=env,stdout=log,stderr=log,check=True)
    with (E/'build.log').open('w') as log:
        command([sys.executable,R/'third_party/cometbft/overlay.py'],log)
        config=replace_once((R/'cmd/internal/config/config.go').read_text(),'io.LimitReader(file, 64<<20)','io.LimitReader(file, 2048<<20)')
        (E/'config.go.txt').write_text(config)
        s=(R/'cmd/payctl/direct_bench.go').read_text()
        s=replace_once(s,'"sort"','"sort"\n"sync"')
        s=replace_once(s,'*count > 30000','*count > 198000')
        s=replace_once(s,'if *trace {\n\t\t\t\treq.Header.Set(requesttrace.HeaderName, "1")',f'if *trace && i%{a.trace_every} == 0 {{\n\t\t\t\treq.Header.Set(requesttrace.HeaderName, "1")')
        s=replace_once(s,'\t\tif err = sender.SaveDirectRequest(requests[i]); err != nil {\n\t\t\treturn err\n\t\t}','')
        prep="""var prepWG sync.WaitGroup
prepErrors:=make(chan error,64)
for worker:=0;worker<64;worker++ {prepWG.Add(1);go func(worker int){defer prepWG.Done();for i:=worker;i<len(requests);i+=64 {if e:=sender.SaveDirectRequest(requests[i]);e!=nil{prepErrors<-e;return}}}(worker)}
prepWG.Wait();close(prepErrors);for e:=range prepErrors{if e!=nil{return e}}
fmt.Println("PREPARATION_COMPLETE")
"""
        s=replace_once(s,'samples := make([]directBenchSample, *count)',prep+'samples := make([]directBenchSample, *count)')
        s=replace_once(s,'\t\t\tif *maxPending > 0 {\n\t\t\t\tcancel()\n\t\t\t} // Stop adding load after an uncertain outcome.','// Independent final inputs: retain each failure without retry or global cancellation.')
        s=replace_once(s,'began := time.Now()','began := time.Now()\nfmt.Println("LOAD_STARTED",began.UnixNano())')
        s=replace_once(s,'"net/http"','"net/http"\n"net/http/httptrace"')
        s=replace_once(s,'pacingStart := time.Now()','''var diagnostic context.Context
if req.Header.Get(requesttrace.HeaderName)=="1" {
 diagnostic=requesttrace.Start(ctx,"payer")
 req=req.WithContext(httptrace.WithClientTrace(req.Context(),&httptrace.ClientTrace{
 GetConn:func(string){requesttrace.Mark(diagnostic,"payer_get_conn")},
 GotConn:func(httptrace.GotConnInfo){requesttrace.Mark(diagnostic,"payer_got_conn")},
 WroteRequest:func(httptrace.WroteRequestInfo){requesttrace.Mark(diagnostic,"payer_wrote")},
 GotFirstResponseByte:func(){requesttrace.Mark(diagnostic,"payer_first_byte")},
 }))
}
pacingStart := time.Now()''')
        s=replace_once(s,'samples[i].Foreground = requesttrace.Events(traceCtx)','samples[i].Foreground = requesttrace.Events(traceCtx)\nif diagnostic!=nil {samples[i].Foreground=append(samples[i].Foreground,requesttrace.Events(diagnostic)...)}')
        (E/'bench.go.txt').write_text(s)
        overlay={'Replace':{str(R/'cmd/internal/config/config.go'):str(E/'config.go.txt'),str(R/'cmd/payctl/direct_bench.go'):str(E/'bench.go.txt')}}
        labcode=replace_once((R/'cmd/payctl/lab.go').read_text(),'*outputs > 100000','*outputs > 198000')
        labcode=replace_once(labcode,'time.NewTimer(25 * time.Second)','time.NewTimer(120 * time.Second)')
        labcode=replace_once(labcode,'deadline := time.Now().Add(30 * time.Second)','deadline := time.Now().Add(120 * time.Second)')
        (E/'lab.go.txt').write_text(labcode)
        overlay['Replace'][str(R/'cmd/payctl/lab.go')]=str(E/'lab.go.txt')
        transport=(R/'internal/transport/member.go').read_text()
        transport=replace_once(transport,'"io"','"io"\n"sync/atomic"'+('\n"log"' if not a.no_trace else ''))
        transport=replace_once(transport,'fg := make(chan struct{}, foreground)','fg := make(chan struct{}, foreground)\nvar foregroundRejected atomic.Uint64\nmux.HandleFunc("GET /debug/admission",func(w http.ResponseWriter,r *http.Request){_ = json.NewEncoder(w).Encode(map[string]uint64{"foreground_rejected":foregroundRejected.Load()})})')
        diagnostic='if tokens == fg { foregroundRejected.Add(1);'+('log.Printf("ADMISSION_FULL path=%s capacity=%d", r.URL.Path, cap(tokens))' if not a.no_trace else '')+' }\nhttp.Error(w, "LIMITED", http.StatusTooManyRequests)'
        transport=replace_once(transport,'http.Error(w, "LIMITED", http.StatusTooManyRequests)',diagnostic)
        if a.conn_stats:
            transport=transport.replace('"net/http"','"net/http"\n"net/http/httptrace"')
            transport+='\nvar approvalFresh,approvalReused,approvalCanceled,approvalRequests atomic.Uint64\nfunc ApprovalConnectionStats() [4]uint64 {return [4]uint64{approvalRequests.Load(),approvalFresh.Load(),approvalReused.Load(),approvalCanceled.Load()}}\n'
            transport=replace_once(transport,'requesttrace.MarkNode(ctx, c.BaseURL, "http_do_start")',"""if path == "/v3/transactions" {
 approvalRequests.Add(1)
 request=request.WithContext(httptrace.WithClientTrace(request.Context(),&httptrace.ClientTrace{GotConn:func(i httptrace.GotConnInfo){if i.Reused{approvalReused.Add(1)}else{approvalFresh.Add(1)}}}))
}
requesttrace.MarkNode(ctx, c.BaseURL, "http_do_start")""")
            transport=replace_once(transport,'requesttrace.MarkNode(ctx, c.BaseURL, "http_do_done")','if path=="/v3/transactions" && errors.Is(e,context.Canceled){approvalCanceled.Add(1)}\nrequesttrace.MarkNode(ctx, c.BaseURL, "http_do_done")')
            transport=replace_once(transport,'requesttrace.MarkNode(ctx, c.BaseURL, "http_body_read")','if path=="/v3/transactions" && errors.Is(e,context.Canceled){approvalCanceled.Add(1)}\nrequesttrace.MarkNode(ctx, c.BaseURL, "http_body_read")')
            code=(R/'cmd/gateway/main.go').read_text()
            code=replace_once(code,'func main() {','func main() {\ndefer func(){slog.Info("APPROVAL_CONNECTIONS", "requests_fresh_reused_canceled",transport.ApprovalConnectionStats())}()')
            (E/'gateway-main.go.txt').write_text(code)
            overlay['Replace'][str(R/'cmd/gateway/main.go')]=str(E/'gateway-main.go.txt')
        (E/'transport-member.go.txt').write_text(transport)
        overlay['Replace'][str(R/'internal/transport/member.go')]=str(E/'transport-member.go.txt')
        membercode=(R/'internal/member/direct.go').read_text()
        membercode=replace_once(membercode,'"context"','"context"\n"log"')
        membercode=replace_once(membercode,'if !found || slice.Available < allocation.Cap {','if !found || slice.Available < allocation.Cap {\nlog.Printf("SLICE_LIMITED index=%d key=%v available=%d need=%d found=%v",m.cfg.Index,key,slice.Available,allocation.Cap,found)')
        (E/'member-direct.go.txt').write_text(membercode)
        overlay['Replace'][str(R/'internal/member/direct.go')]=str(E/'member-direct.go.txt')
        membermain=(R/'cmd/member/main.go').read_text()
        membermain=replace_once(membermain,'transport.MemberHandler(m, 256, 32)','transport.MemberHandler(m, '+str(a.member_foreground)+', 32)')
        (E/'member-main.go.txt').write_text(membermain)
        overlay['Replace'][str(R/'cmd/member/main.go')]=str(E/'member-main.go.txt')
        if a.profile:
            for original in ['internal/requesttrace/timeline.go','cmd/committee/main.go']:
                code=Path(overlay['Replace'].get(str(R/original),str(R/original))).read_text().replace('"net/http"','"net/http"\n"net/http/pprof"')
                anchor='func RegisterTimeline(mux *http.ServeMux) {' if 'timeline' in original else 'mux := http.NewServeMux()'
                code=replace_once(code,anchor,anchor+'\nmux.HandleFunc("GET /debug/pprof/profile",pprof.Profile)')
                target=E/(Path(original).stem+'-profile.go.txt');target.write_text(code)
                overlay['Replace'][str(R/original)]=str(target)
        if a.no_descriptor_cache:
            code=(R/'protocol/organization.go').read_text()
            code=replace_once(code,"\tif verifiedReceiveDescriptors.Contains(d) {\n\t\treturn nil\n\t}","")
            code=replace_once(code,"\tverifiedReceiveDescriptors.Add(d, struct{}{})","")
            (E/'organization.go.txt').write_text(code)
            overlay['Replace'][str(R/'protocol/organization.go')]=str(E/'organization.go.txt')
        (E/'overlay.json').write_text(json.dumps(overlay,indent=2))
        for role in ['payctl','committee','gateway','member']:
            target=B/('member-real' if role=='member' else role)
            command([go,'build','-tags=comet_v3','-overlay',E/'overlay.json','-o',target,'./cmd/'+role],log)
        (B/'member').write_text('#!/bin/sh\nexec env GOMAXPROCS=16 GOGC=200 "'+str(B/'member-real')+'" "$@"\n');(B/'member').chmod(0o755)
        if a.template:
            S=a.template.resolve();L.mkdir(mode=0o700)
            for f in ['config','keys']:shutil.copytree(S/f,L/f)
            for f in ['logs','reports']:(L/f).mkdir()
            for i in range(4):
                p=Path(f'committee{i}/comet/config/node_key.json');(L/p).parent.mkdir(parents=True);shutil.copy2(S/p,L/p)
            def relocate(x):
                if isinstance(x,str):return x.replace(str(S),str(L))
                if isinstance(x,list):return [relocate(y) for y in x]
                if isinstance(x,dict):return {k:relocate(v) for k,v in x.items()}
                return x
            lab=relocate(json.loads((S/'lab.json').read_text()));(L/'lab.json').write_text(json.dumps(lab))
            for p in (L/'config').glob('*.json'):p.write_text(json.dumps(relocate(json.loads(p.read_text())),separators=(',',':')))
        else:
            command([B/'payctl','init-lab','-dir',L,'-outputs',a.count,'-port',a.port,'-v4'],log)
            lab=json.loads((L/'lab.json').read_text())
    lab['Nodes']=[n for n in lab['Nodes'] if n['Binary']=='committee' or n['Name']=='gateway0' or n['Name'].startswith('org0-member')]
    assert len(lab['Nodes'])==9
    (L/'lab.json').write_text(json.dumps(lab))
    env.update(UTXO_EXPERIMENT_COMMIT=str(a.commit_ms)+'ms',UTXO_EXPERIMENT_FLUSH='10ms',UTXO_EXPERIMENT_GOSSIP='10ms',UTXO_EXPERIMENT_MEM_BLOCKSTORE='1')
    for key in ['UTXO_SETTLEMENT_TRACE','UTXO_COMET_PROFILE','UTXO_STAGE_DEEP','GODEBUG','UTXO_EXPERIMENT_MEMBER_MEMORY','UTXO_EXPERIMENT_GATEWAY_MEMORY']:env.pop(key,None)
    env.pop('UTXO_EXPERIMENT_COMMITTEE_MEMORY',None)
    if a.committee_memory:env['UTXO_EXPERIMENT_COMMITTEE_MEMORY']='1'
    if a.member_memory:env['UTXO_EXPERIMENT_MEMBER_MEMORY']='1'
    if a.gateway_memory:env['UTXO_EXPERIMENT_GATEWAY_MEMORY']='1'
    if not a.no_trace:
        env['UTXO_SETTLEMENT_TRACE']='1';env['UTXO_COMET_PROFILE']='1';env['UTXO_TRACE_ALL']='1' if a.count==1 else '0'
    ctl=B/'payctl';comm=[n for n in lab['Nodes'] if n['Binary']=='committee']
    args=[str(ctl),'bench-v4','-dir',str(L),'-start','0','-count',str(a.count),'-concurrency','256','-max-pending',str(a.max_pending),'-rate',str(a.rate),'-same-org','-trace']
    if a.wallet_no_sync:args+=['-wallet-no-sync']
    if a.unbatched_progress:args+=['-batch-progress=false']
    if a.no_trace:args.remove('-trace')
    metadata=dict(member_urls=[n["URL"] for n in sorted(lab["Nodes"],key=lambda n:n["Name"]) if n["Name"].startswith("org0-member")],arguments=vars(a),bench_args=args,genesis_sha256=hashlib.sha256(Path(lab['Network']).read_bytes()).hexdigest(),binaries={n:hashlib.sha256((B/n).read_bytes()).hexdigest() for n in ['payctl','member-real','gateway','committee']},source_head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip(),nodes=len(lab['Nodes']),member_memory=a.member_memory,gateway_memory=a.gateway_memory,wallet_no_sync=a.wallet_no_sync,committee_mem_blockstore=True,trace_every=a.trace_every,concurrency=256,max_pending=a.max_pending)
    (E/'production.patch').write_text(subprocess.check_output(['git','diff','--','cmd','internal','protocol','crypto','finality'],cwd=R,text=True))
    new_sources=subprocess.check_output(['git','ls-files','--others','--exclude-standard','--','cmd','internal','protocol','crypto','finality'],cwd=R,text=True).splitlines()
    for name in new_sources:
        target=E/'new-source'/(name+'.txt');target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(R/name,target)
    metadata['new_source_sha256']={name:hashlib.sha256((R/name).read_bytes()).hexdigest() for name in new_sources}
    (E/'metadata.json').write_text(json.dumps(metadata,default=str,indent=2))
    def get(url):
        with urllib.request.urlopen(url,timeout=2) as f:
            raw=f.read();return {'alive':True} if raw.strip()==b'alive' else json.loads(raw)
    blocks=[];resources=[];height=0;total=0;sender=None;proc=None;profiler=None
    trace_nodes=comm+[n for n in lab['Nodes'] if n['Name']=='gateway0'];timeline_seen=[set() for _ in trace_nodes];timeline_files=[(E/(f'committee{i}-timeline.jsonl' if i<4 else 'gateway-timeline.jsonl')).open('w') for i in range(len(trace_nodes))];next_trace=0
    with (E/'nodes.log').open('w') as log,(E/'bench.log').open('w') as out:
        proc=subprocess.Popen([str(ctl),'lab-run','-dir',str(L),'-bin',str(B)],stdout=log,stderr=log,env=env)
        try:
            deadline=time.monotonic()+300
            while True:
                if proc.poll() is not None:raise RuntimeError('nodes stopped during startup')
                try:
                    if all(get(n['URL']+'/healthz') is not None for n in lab['Nodes']):break
                except (OSError,ValueError):pass
                if time.monotonic()>deadline:raise TimeoutError('readiness')
                time.sleep(.5)
            print('ALL NODES READY',flush=True);time.sleep(20)
            assert all(int(get(n['URL']+'/healthz')['height'])>=1 for n in comm)
            sender=subprocess.Popen(args,stdout=out,stderr=out,env=env)
            if a.profile:
                profiler=subprocess.Popen([sys.executable,str(D/'profile.py'),str(E),str(a.port)])
            deadline=time.monotonic()+600;progress=0;finished=None
            while time.monotonic()<deadline:
                try:
                    h=int(get(comm[0]['URL']+'/healthz')['height']);now=time.time_ns()
                    for v in range(height+1,h+1):
                        rows=get(comm[0]['URL']+f'/block_results?height={v}').get('txs_results') or [];ok=sum(int(r['code'])==0 for r in rows)
                        blocks.append(dict(height=v,observed_ns=now,transactions=len(rows),successful=ok));total+=ok;height=v
                except (OSError,ValueError) as e:print('OBSERVATION',str(e),flush=True)
                if not a.no_trace and time.monotonic()>=next_trace:
                    for ci,cn in enumerate(trace_nodes):
                        for ev in get(cn['URL']+('/debug/consensus' if ci<4 else '/debug/timeline')):
                            ident=json.dumps(ev,sort_keys=True)
                            if ident not in timeline_seen[ci]:timeline_seen[ci].add(ident);timeline_files[ci].write(ident+'\n')
                        timeline_files[ci].flush()
                    next_trace=time.monotonic()+2
                if time.monotonic()>=progress:
                    print('PROGRESS height',height,'committed',total,'bench',sender.poll(),flush=True)
                    ps=subprocess.check_output(['ps','-axo','pid,rss,time,command'],text=True);resources.append(dict(unix_ns=time.time_ns(),rows=[x for x in ps.splitlines() if str(L) in x]));progress=time.monotonic()+30
                if sender.poll() is not None:
                    if finished is None:finished=time.monotonic();print('BENCH EXIT',sender.returncode,flush=True)
                    if time.monotonic()-finished>5:break
                if proc.poll() is not None:raise RuntimeError('nodes stopped')
                time.sleep(.25)
            else:raise TimeoutError('load')
        finally:
            if sender is not None and sender.poll() is None:sender.terminate();sender.wait(timeout=30)
            for ci,cn in enumerate(comm if not a.no_trace else []):
                try:(E/f'committee{ci}-settlements.json').write_text(json.dumps(get(cn['URL']+'/debug/profile-settlements')))
                except Exception as e:print('TRACE_DUMP_ERROR',ci,str(e),flush=True)
                timeline_files[ci].close()
            admission={}
            for i in range(4):
                try:admission['member'+str(i)]=get('http://127.0.0.1:'+str(a.port+i)+'/debug/admission')
                except Exception as e:admission['member'+str(i)]={'error':str(e)}
            (E/'admission.json').write_text(json.dumps(admission,indent=2))
            stopped=time.monotonic();proc.send_signal(signal.SIGINT)
            try:proc.wait(timeout=150)
            except subprocess.TimeoutExpired:proc.kill();proc.wait();raise
            if profiler is not None:
                try:profiler.wait(timeout=30)
                except subprocess.TimeoutExpired:profiler.terminate();profiler.wait(timeout=10)
            metadata['node_exit_code']=proc.returncode
            metadata['shutdown_and_audit_export_s']=time.monotonic()-stopped
            (E/'metadata.json').write_text(json.dumps(metadata,default=str,indent=2))
            (E/'blocks.json').write_text(json.dumps(blocks));(E/'resources.json').write_text(json.dumps(resources))
            shutil.copytree(L/'reports',E/'reports',dirs_exist_ok=True)
    with (E/'audit.log').open('w') as log:
        audit=subprocess.run([str(ctl),'audit','-dir',str(L)],env=env,stdout=log,stderr=log,timeout=180)
    shutil.copytree(L/'reports',E/'reports',dirs_exist_ok=True)
    print('FINISHED committed',total,'audit',audit.returncode,flush=True)
    subprocess.run([sys.executable,D/'analyze.py',a.label],check=True)
    if audit.returncode==0 and proc.returncode==0:
        audit_rows=json.loads((E/'reports/audit.json').read_text())
        assert len({n['StateHash'] for n in audit_rows if n['Name'].startswith('committee')})==1
        assert all(n['Pending']==0 for n in audit_rows)
        for n in audit_rows:
            if n['Name'].startswith('committee'):
                assert n['Payments']==a.count and n['Closed']==a.count and n['Gap']=='0'
                assert int(n['Rewards'])==84*a.count and int(n['Burned'])==10*a.count

        shutil.copytree(L/'logs',E/'node-logs',dirs_exist_ok=True)
        for n in lab['Nodes']:
            target=(L/n['Name']).resolve();assert target.parent==L.resolve()
            if target.exists():shutil.rmtree(target)
        wallet_file=(L/'bench-v4-0.db').resolve();assert wallet_file.parent==L.resolve()
        if wallet_file.exists():wallet_file.unlink()
        (E/'retired.json').write_text(json.dumps({'lab':str(L),'reason':'stopped, reports and successful audit retained','free_bytes':shutil.disk_usage(R).free}))
    if sender.returncode or audit.returncode or proc.returncode:raise SystemExit(1)
if __name__=='__main__':main()
