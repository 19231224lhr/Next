"""Apply audited bilingual passages from the saved Claude draft, once."""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
blocks = json.loads((root/'revision-2026-10-03/claude-code-blocks.json').read_text(encoding='utf-8'))

def passage(i, zh):
    en, cn = blocks[i].split('% --- ZH ---')
    return (cn if zh else en.split('% --- EN ---')[1]).strip()

def replace(text, old, new):
    assert text.count(old) == 1, old[:100]
    return text.replace(old,new)

for zh in [False, True]:
    prefix = 'zh-' if zh else ''
    for stem in ['model','protocol','security']:
        path = root/(prefix+stem+'.tex')
        s = path.read_text(encoding='utf-8')
        backup = root/'revision-2026-10-03'/('before-'+prefix+stem+'.tex')
        if backup.exists():
            s = backup.read_text(encoding='utf-8')
        else:
            backup.write_text(s,encoding='utf-8')
        if stem == 'model':
            marker = r'\subsection{输出、路由与证书}' if zh else r'\subsection{Outputs, Routes, and Certificates}'
            # Reader scope is detailed once in protocol; model names the trust boundary.
            reader = ('历史读取以已验证并执行公共前缀 $H$ 的诚实副本为对象，并在其状态 $V_H$ 的一次读事务内完成。第~\\ref{sec:protocol-reader} 节给出其接口语义。'
                      if zh else 'Historical reads concern an honest replica that has verified and executed public prefix $H$, using one read transaction on its state $V_H$. Section~\\ref{sec:protocol-reader} specifies the interface semantics.')
            s = replace(s,marker,reader+'\n\n'+marker)
            marker = ('该证书认证签发组织的担保，它既不是资产，也不是已登记的公共义务。' if zh else "The certificate authenticates the issuer's guarantee and is neither an asset nor a registered public obligation.")
            s = replace(s,marker,passage(13,zh))
            marker = ('有三类事件反复出现：' if zh else 'Three events recur:')
            s = replace(s,marker,passage(11,zh)+'\n\n'+marker)
            # Break the long identity definition across two mathematical lines.
            old = r'$\mathsf{IntentID}=\operatorname{Digest}(\texttt{INTENT},\mathit{Network},\mathit{Subject},\mathit{Nonce})$'
            formula = '\n'+r'\[\begin{aligned}'+'\n'+r'\mathsf{IntentID}=\operatorname{Digest}(&\texttt{INTENT},\mathit{Network},'+'\\\\\n'+r'&\mathit{Subject},\mathit{Nonce}).'+'\n'+r'\end{aligned}\]'+'\n'
            s = s.replace(old, formula)
        elif stem == 'protocol':
            marker = '验证缓存因此以完整命令字节为键。' if zh else 'Verification caches therefore key complete command bytes.'
            if zh and marker not in s:
                marker = next(p for p in s.split('\n\n') if 'eq:protocol-identities' not in p and '缓存' in p)
            s = replace(s,marker,marker+'\n\n'+passage(21,zh))
            marker = ('所有变更都发生在私有 overlay 中，失败则丢弃整个转换。' if zh else 'All changes occur in a private overlay, and a failure discards the entire transition.')
            if zh and marker not in s:
                marker = next(p for p in s.split('\n\n') if '私有' in p and 'overlay' in p)
            s = replace(s,marker,marker+'\n\n'+passage(23,zh))
            marker = ('工作资源则等待费用关闭。' if zh else 'and work resources wait for fee closure.')
            s = replace(s,marker,marker+' '+passage(25,zh))
            old = ('这条路径需要持有批准的原签署者、跟随链并能够联系委员会；期限届满时仍然缺失的源交易则转入赔付。' if zh else 'The path needs an original signer that holds the approval, follows the chain, and reaches the committee; a source still absent at its deadline falls to compensation.')
            s = replace(s,old,passage(27,zh))
            a = s.index(r'\label{sec:protocol-installation}')
            start = s.index('\n\n',a)+2
            end = s.index('\n\n',start)
            s = s[:start]+passage(29,zh)+s[end:]
            old = ('赔付决定作为追加式公共记录，即可在经济上关闭义务。' if zh else 'A compensation decision alone closes the obligation economically, as an append-only public record.')
            a = s.index(old)
            s = s[:a]+passage(31,zh)+'\n\n'+passage(34,zh)+'\n'
            s = s.replace(r'\ref{sec:eval-repair}',r'\ref{sec:eval-reader}')
            s = s.replace(r'(H,h,j,k,\mathit{Funding},\mathit{revision},\mathit{decision\text{-}or\text{-}absent},\mathit{obligation}_H)',r'(H,h,j,k,F,r,d,s_H)')
            if zh:
                s = s.replace('它是逻辑元组，而不是已部署的网络接口。','其中 $F$ 为资金表示，$r$ 为修订号，$d$ 为决定或缺失标记，$s_H$ 为义务状态。这一逻辑元组描述本地函数组合，并非已部署的网络接口。')
            else:
                s = s.replace('It is a logical tuple, not a deployed network interface.','Here $F$ is the funding representation, $r$ its revision, $d$ the decision or an absent marker, and $s_H$ the obligation state. This logical tuple describes local composition, not a deployed network interface.')
        else:
            # Keep the established proofs; add the verified cross-organization intent case.
            marker = (r'\begin{lemma}[赔付至多生效一次]' if zh else r'\begin{lemma}[Compensation takes effect at most once]')
            s = replace(s,marker,passage(39,zh)+'\n\n'+marker)
            old = ('满足普通执行规则的源支付。' if zh else 'a source payment that meets the ordinary execution rules.')
            new = ('满足普通执行规则（包括不存在阻止其执行的全局 Intent 冲突）的源支付。' if zh else 'a source payment that meets the ordinary execution rules, including the global intent check.')
            s = replace(s,old,new)
            marker = (r'\noindent\textbf{CAL 准入推论。}' if zh else r'\noindent\textbf{CAL admission consequence.}')
            if zh and marker not in s:
                marker = next(p for p in s.split('\n') if '准入' in p and '\\textbf' in p)
            s = replace(s,marker,passage(42,zh)+'\n\n'+marker)
            marker = (r'\subsection{受保护的续花与进展}' if zh else r'\subsection{Protected Continuation and Progress}')
            if zh and marker not in s:
                pos = s.index(r'\label{sec:security-composition}')
                marker = s[s.rfind('\\subsection',0,pos):pos].strip()
            s = replace(s,marker,passage(44,zh)+'\n\n'+marker)
            s = s.replace('[Delay neutrality]','[CAL delay neutrality]').replace('[延迟中立性]','[CAL 延迟中立性]')
        path.write_text(s,encoding='utf-8')
print('Applied audited model/protocol/security passages in both languages.')
