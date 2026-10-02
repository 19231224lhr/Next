"""Inspect compiled PDF structure and render page contact sheets for visual review."""
from pathlib import Path
import json
import fitz
from PIL import Image, ImageDraw

root=Path(__file__).resolve().parents[1]
out=root/'revision-2026-10-03'
results=[]
for path in sorted((root/'pdf').glob('Next-review-*.pdf')):
    doc=fitz.open(path)
    text='\n'.join(page.get_text() for page in doc)
    bad=[]
    for i,page in enumerate(doc):
        for word in page.get_text('words'):
            if word[0]<-1 or word[1]<-1 or word[2]>page.rect.width+1 or word[3]>page.rect.height+1:
                bad.append([i+1,word[:5]])
    assert not bad,(path.name,bad[:5])
    assert '??' not in text,path.name
    assert all(len(p.get_text().strip())>20 for p in doc),path.name
    results.append({'file':path.name,'pages':len(doc),'page_sizes':sorted({(p.rect.width,p.rect.height) for p in doc}),'text_outside_page':bad,'unresolved_question_marks':False})
    tag=path.stem
    for start in range(0,len(doc),6):
        canvas=Image.new('RGB',(960,900),'#dddddd')
        draw=ImageDraw.Draw(canvas)
        for j in range(start,min(start+6,len(doc))):
            pix=doc[j].get_pixmap(matrix=fitz.Matrix(.5,.5),alpha=False)
            im=Image.frombytes('RGB',[pix.width,pix.height],pix.samples)
            im.thumbnail((310,420))
            x=(j-start)%3*320+5;y=(j-start)//3*450+23
            canvas.paste(im,(x,y));draw.text((x,y-18),f'Page {j+1}',fill='black')
        canvas.save(out/f'{tag}-sheet-{start//6+1}.png')
(root/'pdf-checks.json').write_text(json.dumps(results,indent=2),encoding='utf-8')
print(json.dumps(results,indent=2))
