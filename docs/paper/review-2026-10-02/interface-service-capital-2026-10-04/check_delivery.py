from pathlib import Path
import json, hashlib, re, zipfile, subprocess
import fitz
from PIL import Image, ImageDraw
out=Path(__file__).resolve().parent
root=out.parent
repo=root.parents[2]
checks=[]
for stem in ('main','main-zh','supplement-main','supplement-main-zh'):
 b=json.loads((out/f'build-{stem}.json').read_text(encoding='utf-8-sig'))
 assert b['exitCode']==0 and b['pdfExists']
 doc=fitz.open(out/'pdf'/f'{stem}.pdf')
 tx='\n'.join(p.get_text() for p in doc)
 assert '??' not in tx and '\ufffd' not in tx
 outside=[]
 for i,p in enumerate(doc):
  for w in p.get_text('words'):
   if w[0]<-1 or w[1]<-1 or w[2]>p.rect.width+1 or w[3]>p.rect.height+1:
    outside.append([i+1,w[:5]])
 assert not outside,outside[:5]
 overfull=re.findall(r'Overfull[^\n]*',b.get('log',''))
 assert not overfull,(stem,overfull)
 checks.append(dict(root=stem,pages=len(doc),out_of_page=outside,overfull=overfull))
 selected={0}
 needles=('capital and admission','capitalandadmission','资本与准入','fee closure','fees and','费用闭合','original material','共识消息','interface and fee','held','polka')
 for i,p in enumerate(doc):
  t=p.get_text().lower()
  if any(n in t for n in needles):selected.add(i)
 selected=sorted(selected)
 canvas=Image.new('RGB',(900,((len(selected)+2)//3)*420),'#dddddd');draw=ImageDraw.Draw(canvas)
 for k,i in enumerate(selected):
  pix=doc[i].get_pixmap(matrix=fitz.Matrix(.5,.5),alpha=False)
  im=Image.frombytes('RGB',[pix.width,pix.height],pix.samples);im.thumbnail((290,390))
  x,y=(k%3)*300+5,(k//3)*420+22;canvas.paste(im,(x,y));draw.text((x,y-18),f'{stem} p{i+1}',fill='black')
 canvas.save(out/f'{stem}-contact.png')
 if stem=='supplement-main':doc[2].get_pixmap(matrix=fitz.Matrix(1.3,1.3),alpha=False).save(out/'supp-page-3.png')
(out/'pdf-checks.json').write_text(json.dumps(checks,indent=2),encoding='utf8')
files=sorted([*root.glob('*.tex'),*root.glob('*.bib'),*root.glob('*.cls'),*(root/'figures').glob('*.pdf')])
with zipfile.ZipFile(out/'Next-interface-service-capital-LaTeX.zip','w',zipfile.ZIP_DEFLATED) as z:
 for p in files:z.write(p,p.relative_to(root).as_posix())
# Current artifact fingerprints distinguish test additions from node production source.
tracked=subprocess.check_output(['git','ls-files'],cwd=repo,text=True).splitlines()
production=[p for p in tracked if (p.endswith('.go') and not p.endswith('_test.go')) or p=='third_party/cometbft/overlay.py']
tests=['internal/committee/submission_boundary_test.go','internal/rules/fee_closure_evidence_test.go','internal/member/blocks_test.go','third_party/cometbft/consensus/block_identity_test.go']
def h(p):return hashlib.sha256(p.read_bytes()).hexdigest()
manifest={'baseline':'f856c7b3d6265f7275a15767bdced52d40dafada','checkout':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip(),'production':{p:h(repo/p) for p in production},'current_test_files':{p:h(repo/p) for p in tests}}
changed=[]
for p in production:
 old=subprocess.run(['git','show',manifest['baseline']+':'+p],cwd=repo,capture_output=True)
 if old.returncode or old.stdout.replace(b'\r\n',b'\n')!=(repo/p).read_bytes().replace(b'\r\n',b'\n'):changed.append(p)
manifest['production_differences_from_measured_baseline']=changed
assert set(changed)=={'cmd/payctl/chain_timing.go','cmd/payctl/direct_chain.go'},changed
manifest['no_member_restart_evidence']={'launcher':'cmd/payctl/lab.go:runLab launches once, exits on child termination, no restart loop','runner':'docs/experiments/final-evidence-2026-10-04/run_mixed.py launches each lab once; no member-restart action','logs':{f'r{i}-P':h(repo/f'docs/experiments/final-evidence-2026-10-04/results-mixed/r{i}-P/lab.log') for i in range(1,4)}}
(out/'final-source-manifest.json').write_text(json.dumps(manifest,indent=2),encoding='utf8')
outputs=[*sorted((out/'pdf').glob('*.pdf')),out/'Next-interface-service-capital-LaTeX.zip',out/'final-source-manifest.json',*sorted(out.glob('*final.log'))]
(out/'SHA256.json').write_text(json.dumps({str(p.relative_to(out)):h(p) for p in outputs},indent=2),encoding='utf8')
print(json.dumps({'checks':checks,'source_files':len(files),'production_changes_from_baseline':changed},indent=2))
