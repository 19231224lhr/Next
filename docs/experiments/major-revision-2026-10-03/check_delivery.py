"""Read-only PDF/source checks and reproducible paper source packaging."""
from pathlib import Path
import hashlib
import json
import re
import zipfile
import fitz
from PIL import Image, ImageDraw

E = Path(__file__).resolve().parent
P = E.parents[1] / 'paper' / 'review-2026-10-02'
rows = []
for path in sorted((P / 'pdf').glob('*.pdf')):
    doc = fitz.open(path)
    text = '\n'.join(page.get_text() for page in doc)
    outside = []
    blank = []
    for i, page in enumerate(doc):
        if not page.get_text().strip():
            blank.append(i + 1)
        for w in page.get_text('words'):
            if w[0] < -1 or w[1] < -1 or w[2] > page.rect.width + 1 or w[3] > page.rect.height + 1:
                outside.append([i + 1, w[4]])
    row = dict(file=path.name, pages=len(doc), sizes=sorted(set((p.rect.width,p.rect.height) for p in doc)),
               blank_pages=blank, outside=outside, unresolved='??' in text,
               replacement_character='\ufffd' in text, has_current_version='9879f64' in text)
    assert not blank and not outside and not row['unresolved'] and not row['replacement_character'], row
    assert row['has_current_version'], row
    if path.name == 'Next-review-English.pdf':
        assert len(doc) <= 18
    rows.append(row)
    (E / (path.stem + '.txt')).write_text(text, encoding='utf-8')
    for start in range(0, len(doc), 12):
        pages = list(doc)[start:start+12]
        thumbs = []
        for page in pages:
            pix = page.get_pixmap(matrix=fitz.Matrix(.6,.6), alpha=False)
            thumbs.append(Image.frombytes('RGB', [pix.width,pix.height],pix.samples))
        w,h = thumbs[0].size
        sheet = Image.new('RGB',(4*(w+10),((len(thumbs)+3)//4)*(h+28)), '#ddd')
        draw = ImageDraw.Draw(sheet)
        for j,im in enumerate(thumbs):
            x,y=(j%4)*(w+10),(j//4)*(h+28)
            sheet.paste(im,(x,y+20));draw.text((x+5,y+3),str(start+j+1),fill='black')
        sheet.save(E/(path.stem+f'-sheet-{start//12+1}.png'))

logs=[]
for path in sorted(E.glob('compile-local-*.log')):
    text=path.read_text(encoding='utf-8',errors='replace')
    row=dict(file=path.name,errors=len(re.findall(r'^!',text,re.M)),overfull=text.count('Overfull'),
             missing_characters=text.count('Missing character:'),undefined_references='There were undefined references' in text)
    assert not any(row[k] for k in ('errors','overfull','missing_characters','undefined_references')),row
    logs.append(row)

files=sorted([*P.glob('*.tex'),*P.glob('*.bib'),*P.glob('*.cls'),*(P/'figures').glob('*.pdf')])
package=P/'Next-review-2026-10-03-LaTeX.zip'
with zipfile.ZipFile(package,'w',zipfile.ZIP_DEFLATED) as z:
    for path in files:
        info=zipfile.ZipInfo(path.relative_to(P).as_posix(),(2026,10,3,0,0,0))
        info.compress_type=zipfile.ZIP_DEFLATED
        z.writestr(info,path.read_bytes())
checks=dict(compiler='Tectonic 0.17.0, bundle v33, XeTeX/BibTeX',pdfs=rows,logs=logs,
            online_sync='pending: CUA kernel asset path unavailable; local files authoritative')
(P/'pdf-checks.json').write_text(json.dumps(checks,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
hashes={p.relative_to(P).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in [*files,*sorted((P/'pdf').glob('*.pdf')),package]}
(P/'SHA256.json').write_text(json.dumps(hashes,indent=2)+'\n',encoding='utf-8')
print(json.dumps(checks,ensure_ascii=False,indent=2))
