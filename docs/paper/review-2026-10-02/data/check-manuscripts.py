"""Check the four manuscript roots without substituting for TeX compilation."""
from pathlib import Path
from collections import Counter
import json
import re

root = Path(__file__).resolve().parents[1]
bib = set(re.findall(r"@\w+\s*\{\s*([^,]+),", (root / "references.bib").read_text(encoding="utf-8")))

def expand(path, chain=()):
    if path in chain:
        raise ValueError(f"Cyclic input: {path}")
    source = path.read_text(encoding="utf-8")
    return re.sub(r"\\input\{([^}]+)\}",
                  lambda m: expand(root / (m[1] if m[1].endswith('.tex') else m[1] + '.tex'), chain + (path,)), source)

checks = []
for name in ["main.tex", "main-zh.tex", "supplement-main.tex", "supplement-main-zh.tex"]:
    text = expand(root / name)
    labels = Counter(re.findall(r"\\label\{([^}]+)\}", text))
    refs = set(re.findall(r"\\(?:ref|eqref|pageref)\{([^}]+)\}", text))
    cites = {key.strip() for group in re.findall(r"\\cite(?:\[[^]]*\])*\{([^}]+)\}", text) for key in group.split(',')}
    figures = re.findall(r"\\includegraphics(?:\[[^]]*\])?\{([^}]+)\}", text)
    missing_figures = [x for x in figures if not (root / x).exists() and not (root / 'figures' / x).exists()]
    row = {"root": name, "labels": len(labels), "references": len(refs), "citation_keys": len(cites), "unresolved_references": sorted(refs - labels.keys()),
           "duplicate_labels": [k for k, n in labels.items() if n > 1],
           "unknown_citations": sorted(cites - bib), "missing_figures": missing_figures}
    checks.append(row)
(root / 'source-checks.json').write_text(json.dumps(checks, indent=2), encoding='utf-8')
print(json.dumps(checks, indent=2))
if any(c[k] for c in checks for k in ['unresolved_references','duplicate_labels','unknown_citations','missing_figures']):
    raise SystemExit(1)

# A translation may reorder references within a sentence, but must not lose them.
parity = []
for stem in ["abstract", "introduction", "related", "model", "protocol", "security", "evaluation", "conclusion", "supplement-security", "supplement-methods", "supplement-tables", "supplement-current", "supplement-archived", "network-current", "supplement-revision"]:
    en = (root / (stem + ".tex")).read_text(encoding="utf-8")
    zh = (root / ("zh-" + stem + ".tex")).read_text(encoding="utf-8")
    for command in ["label", "ref", "eqref", "cite"]:
        pattern = r"\\" + command + r"\{([^}]+)\}"
        assert Counter(re.findall(pattern, en)) == Counter(re.findall(pattern, zh)), (stem, command)
    for env in ["equation", "align", "align*"]:
        pattern = r"\\begin\{" + re.escape(env) + r"\}(.*?)\\end\{" + re.escape(env) + r"\}"
        def normalize(text):
            # Only these two reviewed prose labels differ inside a display.
            text = text.replace(r"\text{若 settled}", r"\text{if settled}")
            text = text.replace(r"\text{否则}", r"\text{otherwise}")
            return [re.sub(r"\s+", "", x) for x in re.findall(pattern, text, re.S)]
        assert normalize(en) == normalize(zh), (stem, env)
    counts = {}
    for env in ["lemma", "theorem", "IEEEproof", "algorithmic", "tabular"]:
        marker = "\\begin{" + env + "}"
        assert en.count(marker) == zh.count(marker), (stem, env)
        counts[env] = en.count(marker)
    tables = lambda text: re.findall(r"\\begin\{tabular\}.*?\\end\{tabular\}", text, re.S)
    numbers = lambda text: re.findall(r"(?<![a-zA-Z])\d+(?:[,.]\d+)*", text)
    for index, (left, right) in enumerate(zip(tables(en), tables(zh))):
        assert numbers(left) == numbers(right), (stem, "table", index)
    parity.append({"section": stem, "matched": True, "environments": counts})
(root / "bilingual-checks.json").write_text(json.dumps(parity, indent=2), encoding="utf-8")
print("Bilingual references, display equations, proof counts and table numbers match.")
