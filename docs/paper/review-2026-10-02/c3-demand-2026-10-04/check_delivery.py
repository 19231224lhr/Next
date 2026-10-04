"""Check and package the C3 prose revision without changing experiment data."""
from pathlib import Path
import hashlib
import json
import re
import zipfile
import fitz
from PIL import Image, ImageDraw

out = Path(__file__).resolve().parent
root = out.parent
names = [p.name for p in (out / "before").iterdir()]
for name in names:
    p = root / name
    p.write_text(p.read_text(encoding="utf-8"), encoding="utf-8", newline="\n")

checks = []
for stem in ("main", "main-zh"):
    build = json.loads((out / f"build-{stem}.json").read_text(encoding="utf-8-sig"))
    assert build["exitCode"] == 0 and build["pdfExists"], stem
    doc = fitz.open(out / "pdf" / f"{stem}.pdf")
    text = "\n".join(p.get_text() for p in doc)
    assert "??" not in text and "\ufffd" not in text, stem
    outside = []
    for i, page in enumerate(doc):
        assert len(page.get_text().strip()) > 20
        for w in page.get_text("words"):
            if w[0] < -1 or w[1] < -1 or w[2] > page.rect.width+1 or w[3] > page.rect.height+1:
                outside.append([i+1, w[:5]])
    assert not outside, outside[:5]
    overfull = re.findall(r"Overfull[^\n]*", build.get("log", ""))
    checks.append(dict(root=stem, pages=len(doc), out_of_page=outside, overfull=overfull,
                       undefined_references="undefined references" in build.get("log", "").lower()))
    # Contact sheet: introduction, historical-reader discussion, evaluation, references.
    selected = {0, 1, len(doc)-1}
    for i, p in enumerate(doc):
        pt = p.get_text().lower()
        if "exception review and historical" in pt or "decision-derived checks" in pt or "异常复核与历史" in pt or "decisions and obligations" in pt:
            selected.add(i)
    selected = sorted(selected)
    canvas = Image.new("RGB", (930, ((len(selected)+2)//3)*440), "#dddddd")
    draw = ImageDraw.Draw(canvas)
    for k, i in enumerate(selected):
        pix = doc[i].get_pixmap(matrix=fitz.Matrix(.55, .55), alpha=False)
        im = Image.frombytes("RGB", [pix.width, pix.height], pix.samples)
        im.thumbnail((300,410))
        x, y = (k%3)*310+5, (k//3)*440+22
        canvas.paste(im, (x,y))
        draw.text((x,y-18), f"{stem}: page {i+1}", fill="black")
    canvas.save(out / f"{stem}-contact.png")

(out / "pdf-checks.json").write_text(json.dumps(checks, indent=2), encoding="utf-8")
files = sorted([*root.glob("*.tex"), *root.glob("*.bib"), *root.glob("*.cls"), *(root/"figures").glob("*.pdf")])
with zipfile.ZipFile(out/"Next-C3-revision-LaTeX.zip", "w", zipfile.ZIP_DEFLATED) as z:
    for p in files:
        z.write(p, p.relative_to(root).as_posix())
hashes = {str(p.relative_to(out)): hashlib.sha256(p.read_bytes()).hexdigest()
          for p in [out/"pdf/main.pdf", out/"pdf/main-zh.pdf", out/"Next-C3-revision-LaTeX.zip"]}
(out/"SHA256.json").write_text(json.dumps(hashes, indent=2), encoding="utf-8")
print(json.dumps({"checks":checks, "source_files":len(files)}, indent=2))
