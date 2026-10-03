"""Check the four local TeX roots and downloaded PDFs, then package sources."""
from collections import Counter
from pathlib import Path
import hashlib
import json
import re
import zipfile
import fitz

ROOT = Path(__file__).resolve().parent.parent
OUT = Path(__file__).resolve().parent


def save(name, data):
    (ROOT / name).write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')


def expanded(path):
    text = path.read_text(encoding='utf-8')
    text = re.sub(r'(?<!\\)%[^\n]*', '', text)
    return re.sub(r'\\input\{([^}]+)\}', lambda m: expanded(ROOT / (m[1] + '.tex')), text)


checks = []
keys = set(re.findall(r'@\w+\{([^,]+),', (ROOT / 'references.bib').read_text(encoding='utf-8')))
for name in ('main.tex', 'main-zh.tex', 'supplement-main.tex', 'supplement-main-zh.tex'):
    text = expanded(ROOT / name)
    labels = re.findall(r'\\label\{([^}]+)\}', text)
    refs = re.findall(r'\\(?:ref|eqref|autoref)\{([^}]+)\}', text)
    cites = {k.strip() for group in re.findall(r'\\cite(?:\[[^]]*\])?\{([^}]+)\}', text) for k in group.split(',')}
    figures = re.findall(r'\\includegraphics(?:\[[^]]*\])?\{([^}]+)\}', text)
    row = dict(root=name, labels=len(labels), references=len(refs), citation_keys=len(cites),
               unresolved_references=sorted(set(refs)-set(labels)),
               duplicate_labels=[k for k,v in Counter(labels).items() if v>1],
               unknown_citations=sorted(cites-keys),
               missing_figures=[f for f in figures if not any((d / (f+s)).exists() for d in (ROOT,ROOT/'figures') for s in ('','.pdf','.png'))])
    checks.append(row)
save('source-checks.json', checks)
assert not any(r[k] for r in checks for k in ('unresolved_references','duplicate_labels','unknown_citations','missing_figures')), checks

bilingual = []
for zh in sorted(ROOT.glob('zh-*.tex')):
    en = ROOT / zh.name.removeprefix('zh-')
    if not en.exists():
        continue
    a,b = en.read_text(encoding='utf-8'),zh.read_text(encoding='utf-8')
    def structural(s):
        return {k: re.findall(pattern,s) for k,pattern in {
            'labels':r'\\label\{([^}]+)\}', 'refs':r'\\(?:ref|eqref)\{([^}]+)\}',
            'citations':r'\\cite\{([^}]+)\}',
            'proofs':r'\\begin\{(lemma|theorem|IEEEproof|algorithmic|tabular)\}'}.items()}
    sa,sb=structural(a),structural(b)
    mismatch=[k for k in sa if Counter(sa[k])!=Counter(sb[k])]
    bilingual.append(dict(section=en.stem,matched=not mismatch,mismatches=mismatch))
save('bilingual-checks.json',bilingual)
assert all(r['matched'] for r in bilingual), bilingual

pdfs=[]
for path in sorted((ROOT/'pdf').glob('*.pdf')):
    doc=fitz.open(path)
    text='\n'.join(p.get_text() for p in doc)
    outside=[]
    blank=[]
    for n,page in enumerate(doc):
        if not page.get_text().strip(): blank.append(n+1)
        for w in page.get_text('words'):
            if w[0]<-1 or w[1]<-1 or w[2]>page.rect.width+1 or w[3]>page.rect.height+1:
                outside.append(dict(page=n+1,text=w[4]))
    pdfs.append(dict(file=path.name,pages=len(doc),page_sizes=sorted(set((p.rect.width,p.rect.height) for p in doc)),
                     text_outside_page=outside,blank_pages=blank,unresolved_question_marks='??' in text,
                     has_new_rule=('Superseded partial approval' in text or '局部批准' in text or 'Local invalidation' in text),
                     has_patch_version='bf71c4d' in text))
    (OUT/(path.stem+'.txt')).write_text(text,encoding='utf-8')
    # Contact sheet: all pages, four columns, readable page numbers.
    from PIL import Image,ImageDraw
    thumbs=[]
    for i,page in enumerate(doc):
        pix=page.get_pixmap(matrix=fitz.Matrix(.48,.48),alpha=False)
        im=Image.frombytes('RGB',[pix.width,pix.height],pix.samples)
        thumbs.append(im)
    for start in range(0,len(thumbs),12):
        group=thumbs[start:start+12]
        w,h=group[0].size
        sheet=Image.new('RGB',(4*(w+10),((len(group)+3)//4)*(h+28)),'#ddd')
        draw=ImageDraw.Draw(sheet)
        for j,im in enumerate(group):
            x,y=(j%4)*(w+10),(j//4)*(h+28)
            sheet.paste(im,(x,y+20));draw.text((x+5,y+3),str(start+j+1),fill='black')
        sheet.save(OUT/(path.stem+f'-sheet-{start//12+1}.png'))
save('pdf-checks.json',pdfs)
assert all(not r['text_outside_page'] and not r['blank_pages'] and not r['unresolved_question_marks'] and r['has_new_rule'] and r['has_patch_version'] for r in pdfs),pdfs

files=sorted([*ROOT.glob('*.tex'),*ROOT.glob('*.bib'),*ROOT.glob('*.cls'),*(ROOT/'figures').glob('*.pdf')])
package=ROOT/'Next-review-2026-10-03-LaTeX.zip'
with zipfile.ZipFile(package,'w',zipfile.ZIP_DEFLATED) as z:
    for p in files: z.write(p,p.relative_to(ROOT).as_posix())
save('SHA256.json',{p.relative_to(ROOT).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in [*files,*sorted((ROOT/'pdf').glob('*.pdf')),package]})
print(json.dumps(dict(source=checks,bilingual_ok=True,pdfs=pdfs),ensure_ascii=False,indent=2))
