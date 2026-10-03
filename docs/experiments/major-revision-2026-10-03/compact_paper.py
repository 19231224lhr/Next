"""One-shot application of the reviewed editorial replacements (2026-10-03)."""
import json,re
from pathlib import Path
E=Path(__file__).resolve().parent; P=E.parents[1]/'paper/review-2026-10-02'
b=json.loads((E/'claude-final-blocks.json').read_text())
for name,i in [('related',0),('introduction',1)]: (P/(name+'.tex')).write_text(b[i]+'\n',encoding='utf-8')
supp=(P/'supplement-revision.tex').read_text()
p=(P/'protocol.tex').read_text()
start=p.index('A constructor reads');end=p.index('Public execution repeats',start)
extra=p[start:end];supp+='\n\\subsection{Representation Construction and Batch Checks}\n'+extra.replace('fig:protocol-batch','fig:supp-protocol-batch')
p=p[:start]+p[end:];p=p.replace(' (Fig.~\\ref{fig:protocol-batch})','')
(P/'protocol.tex').write_text(p,encoding='utf-8')
ev=(P/'evaluation.tex').read_text()
start=ev.index('From already decoded');end=ev.index('Representation adds',start)
supp+='\n\\subsection{Additional Historical Reader Costs}\n'+ev[start:end];ev=ev[:start]+ev[end:]
m=re.search(r'\\begin\{figure\*\}\[t\]\s*\\centering\s*\\includegraphics\[width=\\textwidth\]\{reader-cost.pdf\}.*?\\end\{figure\*\}',ev,re.S)
assert m;supp+=m.group().replace('tab:eval-reader','tab:supp-reader-cost')+'\n';ev=ev[:m.start()]+ev[m.end():]
# Avoid a dangling main-paper table reference in a separately compiled supplement.
supp=supp.replace('Table~\\ref{tab:supp-reader-cost}','the historical-reader table in the main paper')
start=ev.index('On production snapshot',ev.index('\\label{sec:eval-boundaries}'));end=ev.index('\\noindent\\textbf{Archived evidence.}',start)
ev=ev[:start]+r'''Deterministic tests on \texttt{9879f64} use actual member approvals,
ABCI execution, and authenticated follower updates. They reproduce
no-QC capacity fragmentation, a same-input 2/2 split, and two
Intent-conflict compensation rounds with complete CAL/FUEL accounting.
An external top-up resolves the first case but not the second. Real
threshold adaptation covers both representation/repayment orders;
retained-state restart reproduces each original command history.
A commit-before-signature interruption preserves locks and reservations
and permits an idempotent retry. The supplement gives the per-prefix
ledgers, negative-entry tests, and restart assertions.

'''+ev[end:]
ev=ev.replace('Separate deterministic boundary and replay regressions\nexercise candidate production snapshot \\texttt{9879f64}, with node implementation unchanged. Test and driver additions are fingerprinted separately.', 'Boundary/replay regressions and the injected-latency study use production snapshot \\texttt{9879f64}. Their test and driver additions are fingerprinted separately; node paths are unchanged.')
(P/'evaluation.tex').write_text(ev,encoding='utf-8')
m=(P/'model.tex').read_text();start=m.index('Next separates certification from public ordering');m=m[:start]
m=m.replace('&\\mathit{Subject},\\mathit{Nonce}).\n\\end{aligned}\\],','&\\mathit{Subject},\\mathit{Nonce}),\n\\end{aligned}\\]')
(P/'model.tex').write_text(m,encoding='utf-8')
supp=supp.replace('A trusted dealer generates the key, splits', 'A trusted dealer uses CIRCL \\texttt{GenerateKey} to generate two distinct safe primes and check the inverse of $e$ modulo $\\varphi(N)$, then splits')
(P/'supplement-revision.tex').write_text(supp,encoding='utf-8')
