from pathlib import Path
import re
p=Path(__file__).resolve().parents[1]
def get(n):return (p/n).read_text(encoding='utf-8')
def put(n,s):(p/n).write_text(s.strip()+'\n',encoding='utf-8')

en=r'''\noindent\textbf{Consensus overlay: preserved conditions.}
Table~\ref{tab:supp-consensus-map} separates semantic changes from their regression evidence. The overlay retains the upstream lock/POL argument under the paper's full-identity premise: a body and its part-set header form one identity, a current-round polka supports that identity, and the round/step guard permits at most one precommit. Storage changes preserve the order in which durable evidence becomes visible to execution.

\begin{longtable}{@{}p{0.22\textwidth}p{0.43\textwidth}p{0.29\textwidth}@{}}
\caption{Consensus and persistence changes mapped to preserved conditions.}\label{tab:supp-consensus-map}\\
\toprule
Change & Preserved condition & Existing regression evidence \\
\midrule\endfirsthead
\toprule Change & Preserved condition & Existing regression evidence \\
\midrule\endhead
Full BlockID and current-round polka &
Locked/Valid bodies remain paired with their part roots. A body available after a matching current-round polka can enter precommit, including from Propose, subject to the round/step guard; a late body cannot produce a second vote. &
\texttt{TestStableHashIdentity}: stale parts, commit target, POL, lock pairing, polka-before-body, and nil-precommit/late-body cases. \\
Proposal WAL batching &
Only already-ready local proposal material is grouped, up to 16 messages and 512\,KiB. All writes and \texttt{FlushAndSync} finish before the first \texttt{handleMsg}; any failure stops processing. Votes retain individual \texttt{WriteSync}. A crash can leave a replayable prefix, not an all-or-nothing WAL batch. &
\texttt{TestProposalBatch}: boundaries, pre-sync nonapplication, write/sync failures, real-file durable-prefix replay and signature reuse. \\
Finalize response batching &
Historical responses and the last-response recovery record are staged in one database batch and committed by \texttt{WriteSync}. The caller receives storage errors; an uncertain error after a durable write does not imply rollback. &
\texttt{results\_batch\_test.go}: pre-write failure publishes no partial record; uncertain sync returns an error; disk restart checks paired recovery data. \\
Autofile clean-sync elision &
Sync is skipped only after a successful sync of the same handle with no intervening write. Writes and reopen reset the flag, failed sync leaves it unset, and buffered flush precedes the decision. &
\texttt{sync\_test.go}: first sync, repeated clean sync, writes, failed sync, reopen, rotation and concurrent group writes. \\
Original execution and historical installation &
Original openings are validated at admission. Replay and catch-up retain original bodies and parts; revisions need exact committed Tasks and cannot become fresh payment execution. &
Relabelled-opening rejection, actual BlockStore rewrite/replay, and original-part catch-up tests. \\
\bottomrule
\end{longtable}

The bounds and batching policy change scheduling and storage work, not vote thresholds or payment authorization. The assertion logs retained with the artifact exercise these entry points; the safety argument remains the invariant argument, rather than a claim that tests exhaust all executions.

\noindent\textbf{Deadline wiring.}
Production \texttt{Engine.BeginBlock} calls \texttt{AnchorDirectDeadlines} with the current height and block time, and the committee entry point passes it to \texttt{NewTimedApp}. An Open obligation created at height $h$ is anchored at $h+1$; source closure before anchoring makes that entry inactive. Deadline checks use committed block time. The current chain, mixed-load, and recovery genesis templates all set \texttt{TimeoutSeconds=30}.

'''
zh=r'''\noindent\textbf{共识 overlay：保持的条件。}
表~\ref{tab:supp-consensus-map} 区分语义修改及其回归证据。在论文的完整身份前提下，overlay 保持上游的锁/POL 论证：正文与 part-set 头构成同一身份，当前轮 polka 支持该身份，轮次/步骤守卫使每轮至多产生一次 precommit。存储修改保持耐久证据对执行可见的先后顺序。

\begin{longtable}{@{}p{0.22\textwidth}p{0.43\textwidth}p{0.29\textwidth}@{}}
\caption{共识与持久化修改及其保持的条件。}\label{tab:supp-consensus-map}\\
\toprule
修改 & 保持的条件 & 现有回归证据 \\
\midrule\endfirsthead
\toprule 修改 & 保持的条件 & 现有回归证据 \\
\midrule\endhead
完整 BlockID 与当前轮 polka &
Locked/Valid 正文始终与其 part 根配对。匹配的当前轮 polka 之后正文可用时，在轮次/步骤守卫下可进入 precommit，包括从 Propose 进入；迟到正文不能产生第二次投票。 &
\texttt{TestStableHashIdentity}：旧 parts、commit 目标、POL、锁配对、polka 先到及 nil-precommit 后正文迟到。 \\
提案 WAL 合批 &
仅合并已就绪的本地提案材料，最多 16 条消息、512\,KiB。所有写入与 \texttt{FlushAndSync} 均在首个 \texttt{handleMsg} 之前完成；任一步失败即停止处理。投票保留独立的 \texttt{WriteSync}。崩溃可能留下可重放前缀，而非全成或全败的 WAL 批次。 &
\texttt{TestProposalBatch}：边界、同步前不应用、写入/同步失败、真实文件耐久前缀重放及签名复用。 \\
Finalize 响应合批 &
历史响应与最后响应恢复记录暂存于同一数据库批次，经 \texttt{WriteSync} 提交。存储错误返回调用者；耐久写入后的不确定错误不表示回滚。 &
\texttt{results\_batch\_test.go}：写入前失败不发布半份记录；不确定同步返回错误；磁盘重启检查配对的恢复数据。 \\
Autofile 清洁同步省略 &
仅在同一文件句柄已成功同步且之后没有写入时跳过 Sync。写入和重新打开会重置标志，同步失败不置位，缓冲刷新先于该判断。 &
\texttt{sync\_test.go}：首次同步、重复清洁同步、写入、失败、重新打开、轮换及并发组写入。 \\
原始执行与历史安装 &
准入验证原始 opening。重放和追块保留原始正文及 parts；修订须具有精确的已提交 Task，不能转化为新的支付执行。 &
重标记 opening 拒绝、真实 BlockStore 改写/重放与原始 part 追块测试。 \\
\bottomrule
\end{longtable}

边界与合批策略改变调度及存储工作，不改变投票阈值或支付授权。制品保留的断言日志覆盖这些入口；安全性仍由不变式论证支持，而不声称测试穷尽全部执行。

\noindent\textbf{期限入口接线。}
生产 \texttt{Engine.BeginBlock} 以当前高度和区块时间调用 \texttt{AnchorDirectDeadlines}，委员会入口将其传入 \texttt{NewTimedApp}。高度 $h$ 创建的 Open 义务在 $h+1$ 锚定；锚定之前来源已闭合时，该条目失效。截止期检查使用已提交区块时间。当前连续链、混合负载及恢复实验的创世模板均设置 \texttt{TimeoutSeconds=30}。

'''
for pre,addition in [('',en),('zh-',zh)]:
    n=pre+'supplement-security.tex'; s=get(n)
    marker=r'\noindent\textbf{Original material and application authentication.}' if not pre else None
    if pre:
        pos=s.index(r'\texttt{Test\allowbreak Consensus\allowbreak Rejects')
        pos=s.rfind(r'\noindent\textbf',0,pos)
    else:pos=s.index(marker)
    # Supplement is independently readable; give the implementation mapping here too.
    mapping=('In this supplement, Compensated denotes the economic obligation status named '+r'\texttt{Repaired}'+' in the implementation.\n\n') if not pre else '本补充材料中的 Compensated 表示实现里名为 '+r'\texttt{Repaired}'+' 的经济义务状态。\n\n'
    put(n,s[:pos]+addition+s[pos:])
    root=pre+'supplement-main.tex' if pre else 'supplement-main.tex'
    # Root names use -zh, rather than zh-.
    root='supplement-main-zh.tex' if pre else 'supplement-main.tex'
    s=get(root)
    nav=(r'''\noindent\textbf{Reading guide.} Current-build experiment records and the proof-to-entry-point map are in Section~\ref{supp:final-evidence}; the consensus and fee correspondence is in Section~\ref{supp:interface-fee-evidence}. Other sections preserve the explicitly labelled earlier builds and their measurements. ''' if not pre else r'''\noindent\textbf{阅读指南。} 当前构建实验记录及证明到实际入口的对应见第~\ref{supp:final-evidence}~节，共识与费用对应见第~\ref{supp:interface-fee-evidence}~节。其他章节保留明确标注的旧构建及其测量结果。 ''')+mapping
    put(root,s.replace(r'\maketitle',r'\maketitle'+'\n'+nav,1))

# Tighten the corresponding Chinese overview sentence and reader cross-reference.
n='zh-protocol.tex';s=get(n).replace('截止期时仍缺失的来源经由无需门限适配的公开赔付决定而关闭','入块且有效的决定可在截止期之后、不依赖门限适配地关闭来源义务')
put(n,s)
for pre in ['', 'zh-']:
    n=pre+'protocol.tex';s=get(n)
    marker='The reader therefore consults' if not pre else None
    if pre:
        # Match the original statement without changing its economic meaning.
        for phrase in ['因此，读取方','因此，读者','读取者因此']:
            if phrase in s: marker=phrase;break
    ref=('The state dimensions introduced in Section~\\ref{sec:model-contract} remain separate.\n' if not pre else '第~\\ref{sec:model-contract}~节介绍的状态维度保持相互分离。\n')
    if marker: s=s.replace(marker,ref+marker,1)
    else:
        # Place the cross-reference immediately before the retained decision/status paragraph.
        pos=s.index(r'\noindent\textbf{用途') if r'\noindent\textbf{用途' in s else s.index(r'\noindent\textbf{使用')
        s=s[:pos]+ref+'\n'+s[pos:]
    put(n,s)
print('Added bilingual implementation map and supplement reading guide.')
