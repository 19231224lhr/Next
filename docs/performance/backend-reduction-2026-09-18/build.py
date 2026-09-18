"""Build comparable server variants. All use the same split-observer bench."""
import json, pathlib, subprocess, sys
root=pathlib.Path.cwd()
variant=sys.argv[1]
assert variant in ('baseline','reduced','parallel','dual')
base='a8e2bbf930f5cda40b28fa3679c42ae44394f3ac'
out=root/'.scratch/backend-reduction-bin'/variant
out.mkdir(parents=True,exist_ok=True)
mapping={}
if variant=='baseline':
    for relative in ['cmd/member/main.go','internal/gateway/relay.go','internal/member/credit.go','internal/member/member.go','internal/rules/prepare.go']:
        target=out/(relative.replace('/','_'))
        target.write_bytes(subprocess.check_output(['git','show',base+':'+relative]))
        mapping[str(root/relative)]=str(target)
if variant=='reduced':
    relative='internal/gateway/relay.go'
    old=subprocess.check_output(['git','show',base+':'+relative],text=True)
    source=(root/relative).read_text()
    start='func (r *Relay) Run('
    end='func (r *Relay) deliver('
    source=source[:source.index(start)]+old[old.index(start):old.index(end)]+source[source.index(end):]
    target=out/'serial_relay.go';target.write_text(source)
    mapping[str(root/relative)]=str(target)
if variant=='dual':
    relative='internal/gateway/relay.go'
    source=(root/relative).read_text()
    assert 'start += 4' in source and 'min(start+4' in source
    source=source.replace('start += 4','start += 2').replace('min(start+4','min(start+2')
    target=out/'dual_relay.go';target.write_text(source)
    mapping[str(root/relative)]=str(target)
overlay=out/'overlay.json'
overlay.write_text(json.dumps({'Replace':mapping}))
for name in ['member','gateway','committee','payctl','bench']:
    subprocess.run(['/usr/local/go/bin/go','build','-overlay',str(overlay),'-o',str(out/name),'./cmd/'+name],check=True)
print('BUILT',variant,flush=True)
