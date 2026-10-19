"""Small bilingual notation edits; preserve all earlier manuscript changes."""
from pathlib import Path

root = Path(__file__).resolve().parents[3]
paper = root / 'docs/paper/review-2026-10-02'
for name in ('protocol.tex', 'zh-protocol.tex'):
    p = paper / name
    s = p.read_text(encoding='utf-8')
    s = s.replace(r'$\delta=(\mathit{net},x,h,j,k)$', r'$\Delta=(\mathit{net},x,h,j,k)$')
    s = s.replace(r'$\delta$;', r'$\Delta$;').replace(r'$\delta$ 时', r'$\Delta$ 时')
    s = s.replace("Compensated (the implementation's \\texttt{Repaired} status)", 'Compensated')
    s = s.replace('下文的 Compensated 状态对应实现中的 \\texttt{Repaired}，表示经济赔付。\n', '')
    old = r'a_T-\bigl(\widehat{p}_{m,T}-\widehat{\mathsf{Rec}}_{m,T}\bigr),'
    assert old in s, name
    s = s.replace(old, r'a_T,')
    if name == 'protocol.tex':
        s = s.replace('since that execution repays every compensated output of $T$;', 'since that execution repays every compensated output of $T$. Thus the net compensation term is zero whenever the restoration target is nonzero;')
    else:
        s = s.replace('因为该次执行偿还了 $T$ 的每个已赔付输出；', '因为该次执行偿还了 $T$ 的每个已赔付输出。因此，释放目标非零时，净赔付项已为零；')
    p.write_text(s, encoding='utf-8')

for name in ('evaluation.tex', 'zh-evaluation.tex'):
    p = paper / name
    s = p.read_text(encoding='utf-8')
    marker = r'\begin{table}[!t]'
    assert marker in s
    if name == 'evaluation.tex':
        text = ('The archived treatment labels distinguish normal source delivery (N), '
                'withheld delivery with automatic source recovery (R), compensation '
                'with normal adaptation (C), and compensation with adaptation paused (P). '
                'C and P disable automatic recovery and release each source after its compensation.\n\n')
    else:
        text = ('历史处理组的记号分别表示正常来源交付（N）、扣留交付但开启自动补交（R）、'
                '适配正常的赔付路径（C）和暂停适配的赔付路径（P）。C、P 均关闭自动补交，'
                '并在各来源对应的赔付发生后释放该来源。\n\n')
    s = s.replace(marker, text + marker, 1)
    p.write_text(s, encoding='utf-8')
print('Notation, restoration target and treatment labels updated in both languages.')
