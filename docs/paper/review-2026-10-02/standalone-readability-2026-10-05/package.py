from pathlib import Path
import hashlib,json,zipfile
import fitz
n=Path(__file__).resolve().parent
p=n.parent
old=json.loads((p/'writing-revision-2026-10-04'/'manifest.json').read_text(encoding='utf-8'))
names={x.replace('\\','/') for x in old['source_files']}
names.update(['history.tex','zh-history.tex'])
manifest={'scope':'Standalone-readability revision; unchanged protocol, protected proofs and experimental values','source_files':{},'pdfs':{}}
with zipfile.ZipFile(n/'Next-standalone-readability-LaTeX.zip','w',zipfile.ZIP_DEFLATED) as z:
    for name in sorted(names):
        path=p/name
        data=path.read_bytes()
        manifest['source_files'][name]=hashlib.sha256(data).hexdigest()
        z.writestr(name,data)
for name in ['main','main-zh','supplement-main','supplement-main-zh']:
    f=n/'pdf'/(name+'.pdf')
    d=fitz.open(f)
    manifest['pdfs'][name]={'pages':len(d),'sha256':hashlib.sha256(f.read_bytes()).hexdigest()}
    (n/(name+'-text.txt')).write_text('\n\n'.join(page.get_text() for page in d),encoding='utf-8')
(n/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(manifest['pdfs'],indent=2))
