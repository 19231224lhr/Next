"""Archived first integration step; final TeX includes subsequent reviewer corrections.
Do not rerun over the final manuscript: use the frozen source package for reproduction.
"""
from pathlib import Path
import re

out = Path(__file__).resolve().parent
p = out.parent
draft = (out/'independent-review/claude-revision-snippets.tex').read_text(encoding='utf-8').replace('\u00a0','')

def section(key, language):
    part = draft.split('% ['+key+']',1)[1].split('% ---------------------------------------------------------------------',2)[1]
    # A numbered block contains exactly one EN and one CN text.
    part = part.split('% --- '+language+' ---',1)[1]
    part = part.split('% --- ',1)[0].split('% ---------------------------------------------------------------------',1)[0]
    return part.strip()+'\n'

def append_unique(name, marker, content):
    path=p/name
    text=path.read_text(encoding='utf-8')
    if marker in text: text=text.split(marker)[0].rstrip()+'\n'
    path.write_text(text+'\n'+marker+'\n'+content,encoding='utf-8')

for zh in (False,True):
    prefix='zh-' if zh else ''
    lang='CN' if zh else 'EN'
    # Terminal boundary belongs with the existing service contract. Avoid repeating Intent.
    model=p/(prefix+'model.tex')
    text=model.read_text(encoding='utf-8')
    if not zh:
        old='but at most one executes publicly.'
        new='but at most one TxID executes publicly. This deduplicates transaction execution, not economic receipts from separately executed successors of conflicting QCs.'
        text=text.replace(old,new)
        terminal=r'''\noindent\textbf{Terminal holders.}
A verified TXCer alone creates no redeemable public obligation. If its source
does not execute and the holder cannot obtain a valid successor that executes
publicly, the protocol offers no certificate-only redemption or unconditional exit;
waiting does not create one.
'''
        anchor=r'\noindent\textbf{Admission and stoppage.}'
    else:
        old='但至多一笔在公开层执行。'
        new='但至多一个 TxID 公开执行。该语义对交易执行去重，并不将冲突 QC 的不同后继所产生的经济给付合并成一次。'
        assert old in text or new in text
        text=text.replace(old,new)
        terminal=r'''\noindent\textbf{终端持证人。}
仅持有已验证的 TXCer 不产生可请求兑付的公开义务。若其来源交易不执行，且持证人无法获得并公开执行一个合法后继，
协议不提供仅凭证书的直接兑付或无条件退出路径；等待本身也不会产生该权利。
'''
        anchor=r'\noindent\textbf{准入与停止签发。}'
    if terminal not in text:
        assert anchor in text,anchor
        text=text.replace(anchor,terminal+'\n'+anchor)
    model.write_text(text,encoding='utf-8')

    table=section('2',lang)
    table=table.replace('table*','table').replace('tab:sup-checks-cn','tab:sup-checks')
    table=table.replace('YYY@{}','>{\\raggedright\\arraybackslash}X>{\\raggedright\\arraybackslash}X>{\\raggedright\\arraybackslash}X@{}')
    table=table.replace(r'\begin{tabularx}{\textwidth}',r'\begin{tabular}').replace(r'\end{tabularx}',r'\end{tabular}')
    table=table.replace('p{2.6cm}',r'p{0.15\textwidth}').replace('}X',r'}p{0.25\textwidth}')
    table=table.replace('Theorem~1 and Eq.~(7)','the CAL coverage theorem').replace('定理~1 与式~(7)','CAL 覆盖定理')
    table=table.replace('one certificate per certificate input, no duplicate or unused certificate', 'one certificate--index entry per certified input; duplicate OutputIDs and unused entries rejected')
    table=table.replace('每个证书输入恰一个证书，无重复、无多余证书','每个认证输入恰有一个证书与索引条目；拒绝重复 OutputID 和多余条目')
    table=table.replace('no corresponding claim.', 'remain independent execution conditions; CAL coverage does not imply their availability.')
    table=table.replace('无对应主张。','仍为独立执行条件；CAL 覆盖不推出它们必然可用。')
    # Avoid tying the proof to unstable printed lemma numbers in a separate PDF.
    table=table.replace('Lemma~2:','Quorum intersection:').replace('(Lemma~5)','(the global Intent counterexample)')
    table=table.replace('引理~2：','法定人数交叠：').replace('（引理~5）','（全局 Intent 反例）')
    # Each fee instance is covered by the same conflict exclusion; resource availability is separate.
    table=table.replace('The same checks, plus network equality;', 'The same bindings;')
    table=table.replace('同样检查，另加网络相等；','同样的绑定检查；')
    table=table.replace(r'\footnotesize',r'\small')
    for token, split in [('ApproveDirectBytes',r'Approve\allowbreak DirectBytes'), ('VerifyDirectSubmission',r'VerifyDirect\allowbreak Submission'), ('EvaluateDirectPaymentAt',r'EvaluateDirect\allowbreak PaymentAt'), ('PrepareDirectVector',r'PrepareDirect\allowbreak Vector')]:
        table=table.replace(token,split)
    head, body=table.split(r'\begin{tabular}',1)
    caption=head[head.index(r'\caption'):head.index(r'\small')].strip()
    columns, body=body.split('\n',1)
    table='\\begingroup\\small\n\\renewcommand{\\arraystretch}{1.15}\n\\begin{longtable}'+columns+'\n'+caption+'\\\\\n'+body
    table=table.replace(r'\end{tabular}',r'\end{longtable}').replace(r'\end{table}',r'\endgroup')
    append_unique(prefix+'supplement-final-evidence.tex','% BEGIN FINAL REVIEW ADDITIONS',
        (r'\subsection{Approval and Execution Conditions}' if not zh else r'\subsection{批准与执行条件}')+'\n'+table+'\n')

    recovery=section('3',lang).replace('the collector\nstays alive and observes','the client\nstays alive and observes').replace('收集器保持在线并继续观察','客户端保持在线并继续观察').replace(r'\footnotesize',r'\small').replace('[!t]','[htbp]')
    recovery=(r'\subsection{Recovery and Boundary Evidence}' if not zh else r'\subsection{恢复与边界证据}')+'\n'+recovery
    recovery=recovery.replace('storage reservations','byte-budget reservations').replace('执行与存储预留','执行与字节额度预留')
    recovery=recovery.replace('tab:sup-r-cn','tab:sup-r')
    rotation=section('3b',lang).replace('the issuer is compensated','the issuer reserve is debited').replace('并赔付发行方','并扣减发行方备付')
    rotation=rotation.replace('Table~S4',r'Table~\ref{tab:s3-ledger}').replace('表~S4',r'表~\ref{tab:s3-ledger}')
    versions=section('5',lang)
    versions=versions.replace('finality and Comet production','finality, node entry points, dependencies and Comet production')
    versions=versions.replace('终局与 Comet 生产文件','终局、节点入口、依赖及 Comet 生产文件')
    versions=versions.replace('at least 5.7', '5.7').replace('至少含 5.7','含 5.7')
    extra=(r'''\paragraph{Manifest scope.}
The 309-entry evidence manifest comprises 305 build-source entries plus
\texttt{go.mod}, \texttt{go.sum}, and the two experiment runners.
Chain, P, and R node binaries, the client, and expanded Comet sources match exactly.
The \texttt{member} wrapper executes \texttt{member-real}.
It sets \texttt{GOMAXPROCS} to 16 and \texttt{GOGC} to 200.

\paragraph{Dispatch-slot interpretation.}
The driver acquires a fast slot before serialized pacing and releases it after
certified receipt. In P run 3, 63.31\% of aggregate slot time precedes HTTP send;
its P95 is 589.422\,ms, compared with 589.418\,ms of recorded pacing wait.
At the 917 fast-limit checks, timestamp reconstruction gives medians of 59
acquired-but-unsent tasks and five sent tasks awaiting receipt.
The reconstruction uses the receipt timestamp immediately before slot release.
Thus a fast-limit hit does not identify a slow member request. Total unfinished-task
backpressure remains in the results; no runtime trace was collected to attribute
its underlying service delay to CPU scheduling, storage, or block following.
''' if not zh else r'''\paragraph{清单范围。}
309 项证据清单由 305 项构建源码、\texttt{go.mod}、\texttt{go.sum} 与两个实验驱动组成。
连续链、P 与 R 的节点二进制、客户端及展开后的 Comet 源码完全一致。
\texttt{member} 入口是一个 shell 包装，以 \texttt{GOMAXPROCS=16} 和 \texttt{GOGC=200}
执行 \texttt{member-real}。

\paragraph{发送名额口径。}
驱动先获取快速名额，再等待串行限速发送，到凭证到账后释放。P 第三轮中，全部名额占用时间的
63.31\% 发生在 HTTP 发送之前；该阶段 P95 为 589.422\,ms，记录的限速等待为 589.418\,ms。
在 917 次快速上限检查点，由时间戳重建的已取名额但尚未发送任务中位数为 59，已发送且等待到账的中位数为 5。
重建采用紧邻释放之前的到账时间戳。因此上限命中不能被解释为成员请求缓慢。
总未完成任务的回压仍保留在结果中；本轮未采集运行时跟踪，不把其底层服务延迟归因于 CPU 调度、存储或跟块中的某一项。
''')
    path=p/(prefix+'supplement-final-evidence.tex')
    with path.open('a',encoding='utf-8') as f:f.write('\n'+recovery+'\n'+rotation+'\n'+versions+'\n'+extra)
    root=p/('supplement-main-zh.tex' if zh else 'supplement-main.tex')
    text=root.read_text(encoding='utf-8').replace('array,tabularx,longtable','array,longtable')
    root.write_text(text,encoding='utf-8')

    conclusion=section('4',lang)
    if not zh:
        conclusion=conclusion.replace('Terminal\nholders have no direct redemption; open deployment accepts bounded reserve loss.',
            'Each path advances on its own evidence while preserving the authorization and outputs of completed payments.')
    else:
        conclusion=conclusion.replace('终端持证人没有直接兑付路径；开放部署接受有界的备付损失。',
            '三条路径依据各自的证据推进，并保持已完成付款的授权与输出。')
    path=p/(prefix+'conclusion.tex')
    path.write_text(('\\section{结论}' if zh else '\\section{Conclusion}')+'\n\\label{sec:conclusion}\n\n'+conclusion,encoding='utf-8')

print('Integrated reviewed snippets with source, accounting, and scope corrections.')
