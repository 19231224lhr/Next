from pathlib import Path

paper = Path(__file__).resolve().parents[3] / 'docs/paper/review-2026-10-02'
paragraphs = {
    'security.tex': r'''\noindent\textbf{Static quorum generalization.}
For a fixed $n=3f+1$ organization with at most $f$ static Byzantine
members, require $2f+1$ distinct signatures per QC and set
$s_f(B_g)=\lfloor(f+1)B_g/(2f+1)\rfloor$.
Retain the risk definition in Eq.~\eqref{eq:security-risk}, nondecreasing
authorization, and the residual invariants
$r_{m,c}\ge w_c\ge0$ and $L_{m,g}\le s_f(B_g)$, including partial approvals.
Choose any $2f+1$ honest members $H$; exactly $f$ members lie outside
$H$, so every QC has at least $f+1$ signers in $H$. Summing gives
\[
(f+1)Q_g\le\sum_{m\in H}L_{m,g}
\le(2f+1)s_f(B_g)\le(f+1)B_g.
\]
Thus $Q_g\le B_g$ for every fault count at most $f$.
This extends the static counting argument; the implemented and evaluated
organization remains four-member.

''',
    'zh-security.tex': r'''\noindent\textbf{静态门限推广。}
对固定的 $n=3f+1$ 成员组织，设静态 Byzantine 成员至多为 $f$，
每份 QC 要求 $2f+1$ 个不同签名，并令
$s_f(B_g)=\lfloor(f+1)B_g/(2f+1)\rfloor$。
保留公式~\eqref{eq:security-risk} 的风险定义、单调增加的授权，以及
$r_{m,c}\ge w_c\ge0$ 和 $L_{m,g}\le s_f(B_g)$ 的残余不变量，后者计入部分批准。
任选 $2f+1$ 名诚实成员组成 $H$；其外恰有 $f$ 名成员，
因此每份 QC 至少有 $f+1$ 名签名者位于 $H$。求和得到
\[
(f+1)Q_g\le\sum_{m\in H}L_{m,g}
\le(2f+1)s_f(B_g)\le(f+1)B_g.
\]
故实际故障数不超过 $f$ 时均有 $Q_g\le B_g$。
这是静态计数论证的推广；实际实现与实验仍采用四成员组织。

'''
}
for name, text in paragraphs.items():
    path = paper / name
    source = path.read_text(encoding='utf-8')
    start = source.index(r'\label{thm:security-coverage}')
    end = source.index(r'\end{IEEEproof}', start) + len(r'\end{IEEEproof}')
    path.write_text(source[:end] + '\n\n' + text + source[end:].lstrip('\n'), encoding='utf-8')
for name in ('protocol.tex', 'zh-protocol.tex'):
    path = paper / name
    source = path.read_text(encoding='utf-8')
    source = source.replace('For its CAL debit, member $m$ computes',
                            'For a noninvalidated approval\'s CAL debit, member $m$ computes')
    source = source.replace('对其 CAL 扣减，成员 $m$ 计算', '对未失效批准的 CAL 扣减，成员 $m$ 计算')
    path.write_text(source, encoding='utf-8')
