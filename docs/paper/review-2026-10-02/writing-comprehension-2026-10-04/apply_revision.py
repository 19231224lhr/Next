from pathlib import Path
import json, re, shutil

p = Path(__file__).resolve().parents[1]
out = p / 'writing-revision-2026-10-04'
out.mkdir(exist_ok=True)
before = out / 'before'
before.mkdir(exist_ok=True)
for f in p.glob('*.tex'):
    if not (before / f.name).exists():
        shutil.copy2(f, before / f.name)

def read(name): return (p / name).read_text(encoding='utf-8')
def write(name, text): (p / name).write_text(text.strip()+'\n', encoding='utf-8')
def replace(name, old, new):
    text=read(name)
    assert old in text, (name,old[:100])
    write(name,text.replace(old,new,1))

blocks = [x.split('latex',1)[1] for x in json.loads((p/'writing-comprehension-2026-10-04/claude-drafting-blocks.json').read_text(encoding='utf-8'))]
abstract = blocks[0].replace('a deadline-triggered\npublic decision compensates', 'an included, valid post-deadline\npublic decision compensates')
abstract = abstract.replace('On one host with synchronous member commits, a', 'On one host with synchronous member commits and NoSync wallets, a')
write('abstract.tex', abstract)
write('zh-abstract.tex', r'''\begin{abstract}
UTXO 接收方若须等待公开执行后才能花费，每一跳支付都会增加确认等待。Next 让接收方在一轮认证后继续付款。核心思想是：随输出携带的证书已指明缺失来源的责任承担者，即其直接发行方。消费组织成员锁定输入，并为全部认证输出预留额度；接收方核验法定人数证书（TXCer）后，可以请求后继付款，后继仍须满足自身的准入与额度条件。若后继先执行，公开执行原子地消费认证输出，并登记有资金支持的直接发行方义务。来源执行则履行该义务；否则，入块且有效的到期后公开决定从发行方备付金中赔付缺口，迟到来源偿还备付，始终不执行的来源则由发行方承担损失。授权历史修复在原区块身份下将扣款引用写入消费输入，无需重新执行支付，也不阻塞经济闭合。在每个四成员组织及四验证者委员会至多有一名静态拜占庭参与者的模型下，我们证明消费唯一性、每份证书未结责任的资金覆盖及 CAL 守恒；对于有限、无环、来源闭合、无冲突且每笔均成功执行一次的支付集合，终态 CAL 持有量与执行次序无关。在单机、成员同步提交、钱包 NoSync 的设置下，100 跳链到达最后一笔认证到账的中位时间为 4.707\,s，逐跳等待模式为 64.479\,s。
\end{abstract}''')

intro=read('introduction.tex')
a=intro.index('Economic closure leaves'); b=intro.index('On one M4 Max')
motivation=blocks[1].replace('{iaccf}','{ia-ccf}').replace('{cometbft-blockid}','{comet-blockid}')
contrib=blocks[2].replace('{tab:service}','{tab:model-contract}').replace(r'\textbf{C',r'\noindent\textbf{C')
contrib=contrib.replace('C1: Certification separated from public execution.', 'C1: A layered architecture for one-round continuation.')
contrib=contrib.replace('After\nthe deadline, a public decision compensates', 'After\nthe deadline, an included valid decision compensates')
write('introduction.tex',intro[:a]+motivation+'\n\nThe design has three complementary contributions.\n\n'+contrib+'\n\n'+intro[b:])
zi=read('zh-introduction.tex'); a=zi.index('经济闭合'); b=zi.index('在一台')
write('zh-introduction.tex',zi[:a]+r'''经济闭合之后还存在一个记录问题：原始区块中的已赔付输入仍引用被备付金替代的来源输出。本地例外复核与历史重新导出面向已存储的区块：归档副本重新检查输入，报告备付垫付、授权决定与偿还状态，并导出区块。授权历史修复让导出的正文在输入的原始坐标处直接记录备付扣款引用。决策索引能够从原始正文得到同样的经济答案；额外接口是一个可对照原始 commit 验证的修订正文，其修改授权来自副本已执行的公开前缀。账本审计通过已保存的执行证据复核历史记录~\cite{ia-ccf}，Reparo 通过旧交易哈希返回修复数据~\cite{reparo}；Next 授权替换正文，同时保留已有 commit 与后继区块头所引用的完整身份~\cite{comet-blockid}，而无需再次执行支付。

设计包含三个互补的贡献。

\noindent\textbf{C1：支持一轮续付的分层架构。}证书固定支付输出并标识发行方，公开执行决定账本接受哪笔支付。消费组织成员在签名前，以原子步骤锁定输入并为全部 CAL 输出（含找零）预留额度，一轮并行请求即可形成 TXCer。接收方核验并记录输出后，可以立即请求后继，仅携带直接输入的证书，而复制与公开提交在后台继续。表~\ref{tab:model-contract} 列出各阶段建立的保证。

\noindent\textbf{C2：直接责任驱动的来源解耦。}后继消费来源尚未执行的认证输出时，公开执行在同一原子步骤内记录消费与直接发行方义务。后继区块公布来源证书，使原签署者可以依据保留的批准材料重新提交来源。截止期之后，入块且有效的决定从发行方备付金中赔付缺口，迟到来源再予以偿还。我们证明，签名额度覆盖每份证书（包括尚未公开者）的未结 CAL 责任，将重叠的成员预算~\cite{stingray} 扩展至赔付与偿还。

\noindent\textbf{C3：授权历史修复。}赔付后，原始区块仍引用缺失来源，尽管备付金已提供资金。受限变色龙哈希命令把备付扣款引用写入该输入。每次替换由已提交的赔付决定和精确修订任务授权，保持所有者授权、输出及后继引用、完整 BlockID 不变，且仅写入表示状态。原始命令仍作为重放历史。归档副本因此可复核例外，并按已有身份重新导出修订正文；经济闭合则无需等待修复。

'''+zi[b:])

# Paper terminology is distinct from the implementation's enum name.
for f in p.glob('*.tex'):
    s=f.read_text(encoding='utf-8')
    s=s.replace('Repaired','Compensated').replace(r'f_{\mathrm{repair}}',r'f_{\mathrm{comp}}')
    s=s.replace('one repair fee per','one compensation fee per').replace('one repair charge','one compensation charge')
    write(f.name,s)

# Move the state explanation to first use, then reorder model subsections.
for prefix in ['', 'zh-']:
    prot=read(prefix+'protocol.tex')
    marker=r'\noindent\textbf{Four state dimensions.}' if not prefix else r'\noindent\textbf{四个状态维度。}'
    if marker not in prot:
        marker=re.search(r'\\noindent\\textbf\{[^\n{}]*四[^\n{}]*\}',prot).group()
    start=prot.index(marker); end=prot.index(r'\par}',start)+len(r'\par}')
    table=prot[start:end]
    write(prefix+'protocol.tex',prot[:start]+prot[end:])
    m=read(prefix+'model.tex')
    parts=re.split(r'(?=\\subsection\{)',m)
    head,participants,certificates,layers,example,contract=parts
    assert 'sec:model-example' in example and 'sec:model-contract' in contract
    if not prefix:
        example=r'''\subsection{A Continuous-Payment Example}
\label{sec:model-example}
'''+blocks[3].split('\n',1)[1].replace('{tab:service}','{tab:model-contract}')
        example=example.replace('and the ledger holds\nno claim against $G_A$. If Alice\'s payment never executes and no successor\nof Bob\'s executes, Bob has no certificate-only redemption path.', 'before any public compensation obligation is created.')
        example=example.replace('(committed revisions, tasks, and their queue entries)',r'(defined in Section~\ref{sec:security-decision})')
        contract=contract.replace('Authorization, fees, consumption state, and Intent must pass the\npublic checks.', 'Authorization, fees, consumption state, Intent, and coverage must pass the\npublic checks. Under the stated fault and state model, valid certificates have sufficient CAL authorization (Theorem~\\ref{thm:security-coverage}).')
        contract+='\nSection~\\ref{sec:security-composition} quantifies this exposure and the conditions for continued admission.\n\n'
        table=table.replace('These dimensions describe different objects and advance separately.', 'These dimensions describe different objects and advance separately. Representation is the authorized historical record layer; its logical revision is separate from physical installation.')
    else:
        example=r'''\subsection{连续支付示例}
\label{sec:model-example}
Alice 通过组织 $G_A$ 向 Bob 支付 100~CAL，创建路由到 $G_B$ 的输出 $x$。支付经历三个阶段，其保证见表~\ref{tab:model-contract}。

\emph{认证到账。} $G_A$ 成员锁定 Alice 的输入，为其全部 CAL 输出（含找零）预留签名额度并签名；三个匹配签名形成 TXCer。Bob 核验并记录 $x$，随后可请求 $G_B$ 认证向 Carol 的支付，后者须通过 $G_B$ 自身的输入、费用及额度检查。此时 Bob 获得经认证的续付请求权利，尚未建立公开赔付义务。

\emph{公开消费。} 若 Bob 的支付先于 Alice 执行，委员会在同一转换内消费 $(x,0)$、针对 $G_A$ 记录 100 CAL 义务，并将 Bob 支付的输出创建为最终输出。Carol 按普通最终输入规则花费其输出。同一区块携带 Alice 支付的证书，因此批准过该支付的 $G_A$ 成员可以在 Alice 钱包未提交时重新提交。

\emph{经济闭合。} 若 Alice 的支付在义务处于 Open 时执行，则履行义务。公开区块时间确定的截止期过后，公开决定可从 $G_A$ 备付金中扣除 100~CAL，并将义务标记为 Compensated（实现中的 \texttt{Repaired} 状态）。Carol 不会重复获得本金，Bob 的支付也只执行一次。Alice 的支付之后执行时，向 $G_A$ 偿还而不重新创建 $x$；始终不执行时，则由 $G_A$ 承担这 100~CAL。

\emph{授权历史修复。} 独立于上述阶段，后续命令可以把已存储的 Bob 输入资金引用替换为备付扣款。这类命令改变\emph{表示}状态（定义见第~\ref{sec:security-decision}~节），不改变余额、义务或闭合。

'''
        contract=contract.replace('授权、费用、消费状态和 Intent 均须通过公开检查。','授权、费用、消费状态、Intent 和覆盖均须通过公开检查。在给定故障与状态模型下，合法证书具有充足的 CAL 授权（定理~\\ref{thm:security-coverage}）。')
        contract+='\n第~\\ref{sec:security-composition}~节量化该风险，并给出持续准入的条件。\n\n'
        table=table.replace('这些维度描述不同的对象，并分别推进。','这些维度描述不同对象，并分别推进。表示是经授权的历史记录层，其逻辑修订与物理安装相互分离。')
    write(prefix+'model.tex',head+participants+example+'\n'+contract+'\n'+table+'\n\n'+certificates+layers)

replace('protocol.tex', 'A source still absent at its deadline closes through a public compensation decision without threshold adaptation,', 'An included valid decision can close a source obligation after its deadline without threshold adaptation,')
replace('protocol.tex','and public block time anchors its deadline.',r'''and public block time anchors its deadline. For an obligation created at height $h$, BeginBlock at $h+1$ fixes the deadline to that block's time plus the configured timeout, provided the obligation is still Open. The current chain and mixed-load runs use 30\,s. A public clock-tick command can advance block time during idle periods; deadline expiry authorizes a valid decision but does not itself execute compensation.''')
zprot=read('zh-protocol.tex')
old='其截止期以公开区块时间为锚。'
new=old+r'''对在高度 $h$ 创建的义务，$h+1$ 的 BeginBlock 在该义务仍为 Open 时，将截止期固定为该块时间加上配置超时。当前连续链与混合负载运行采用 30\,s。空闲期间，公开时钟推进命令可推进区块时间；到期允许执行合法决定，但本身不执行赔付。'''
write('zh-protocol.tex',zprot.replace(old,new,1))

# Make the distinction between signing reservations and public grant usage explicit.
replace('protocol.tex', 'Registration raises public grant usage', r'''Here $\mathsf{Reserved}_g$ is public grant usage for registered coverage; members' local Reserved slices are signing reservations and may include unpublished certificates. Registration raises public grant usage''')
replace('zh-protocol.tex', '登记提高公开的 grant 用量', r'''这里 $\mathsf{Reserved}_g$ 是已登记覆盖占用的公共 grant；成员本地 Reserved 切片是签名预留，也可能包含尚未公开的证书。登记提高公开的 grant 用量''')

# Preserve the existing bound and add its tight trace and economic interpretation.
for prefix in ['', 'zh-']:
    s=read(prefix+'security.tex')
    marker='For a fixed grant without further funding,' if not prefix else '对不再补资的固定 grant，'
    start=s.index(marker)
    if not prefix:
        tail=r'''For a fixed grant without further funding, this bounds net unrepaid compensation, not lifetime gross Paid. The bound is reachable: in the rotating-signer regression of Supplement~S4-B(b), one Byzantine member ignores its local limit and the three honest members co-sign in rotating pairs. With $B_g=300$ and 100-CAL sources that lose a global Intent conflict, three valid successors induce compensation of 300~CAL. Honest reservations reach $(200,200,200)$, and the next request is refused.

If the same adversary controls the recipients and the successor outputs retain usable routes, its usable CAL need not decrease as the issuer loses the compensated amount. Each round still spends FUEL, and the losing sources' inputs, including user-funded fee inputs, remain locked. This distinguishes available payment principal from total tied-up assets and does not assign a CAL--FUEL exchange value. Once honest signing capacity under the grant is exhausted, new certification stops for that grant, including payments spending final outputs routed to the organization. A funded top-up adds the difference between the new and old member shares; it clears neither earlier reservations nor input locks. Sources that never execute lie outside Theorem~\ref{thm:security-neutrality}. Supplement~S3 provides the fixed-signer two-round ledger; the rotating-signer trace shows why its earlier stoppage is not a smaller universal reserve-loss bound.'''
    else:
        tail=r'''对不再补资的固定 grant，该式约束净未偿赔付，而非整个生命周期的赔付毛额 Paid。上界可以达到：补充材料~S4-B(b) 的轮换签署者回归中，一名拜占庭成员忽略本地上限，三名诚实成员轮换成对共同签署。$B_g=300$ 时，三笔各 100 CAL、在全局 Intent 冲突中落败的来源，其合法后继触发总计 300~CAL 的赔付。诚实成员预留达到 $(200,200,200)$，下一请求被拒绝。

若同一对手控制收款方，且后继输出保有可用路由，则发行方损失赔付金额时，对手可用的 CAL 支付本金可以不减少。每轮仍支付 FUEL，落败来源的输入（包括用户支付的费用输入）仍被锁定。这区分了可用支付本金与全部占用资产，并不假定 CAL--FUEL 兑换价值。该 grant 的诚实签名额度耗尽后，新认证停止，包括花费路由到该组织的最终输出的支付。有资金支持的补资增加新旧成员份额之差，但不清除已有预留或输入锁。始终不执行的来源不属于定理~\ref{thm:security-neutrality}。补充材料~S3 给出固定签署者的两轮账目；轮换签署者轨迹说明，其更早停止并不构成更小的普遍备付损失上界。'''
    write(prefix+'security.tex',s[:start]+tail)

for prefix in ['', 'zh-']:
    e=read(prefix+'evaluation.tex'); start=e.index(r'\noindent\textbf'); end=e.index(r'\noindent\textbf',start+1)
    if not prefix:
        opening=blocks[5].replace('{tab:builds}','{tab:eval-builds}').replace("build,\npersistence mode, and endpoint", "build and measured outcome")
        table=r'''\begin{table}[!t]
\caption{Evidence groups and measured outcomes. Persistence and endpoints are specified with each experiment.}
\label{tab:eval-builds}
\centering\small
\begin{tabular}{@{}p{0.27\columnwidth}p{0.18\columnwidth}p{0.46\columnwidth}@{}}
\toprule
Evidence & Node build & Outcome \\
\midrule
Current continuation and P & \texttt{f856c7b} & Six chain totals; three mixed-load runs \\
Injected delay and faults & \texttt{9879f64} & Receipt and public completion \\
Historical reader and full N/R/C/P & \texttt{721800c} & Read, storage, maintenance costs; archived treatment matrix \\
\bottomrule
\end{tabular}
\end{table}

All nine current runs share the same node binaries. Additional timing probes are confined to \texttt{payctl} and enabled in all six chain runs. Chain results report medians and ranges of three run totals; mixed-load results use medians of three run-level statistics. Supplement~S4 supplies the current evidence and version bridge; older results retain their original build.

'''
    else:
        opening=r'''本节回答四个问题：（Q1）每跳在认证后继续，而非等待公开执行，100 跳链能提前多少到达最后一笔认证到账？（Q2）历史修复适配暂停时，赔付与偿还能否在普通流量中完成？（Q3）注入延迟及暂停单个成员或验证者如何影响到账与公开完成？（Q4）授权历史修复带来怎样的本地读取、存储与维护成本？所有系列均在单机运行，表~\ref{tab:eval-builds} 列出各系列的构建与测量结果类型。
'''
        table=r'''\begin{table}[!t]
\caption{证据组与测量结果类型。持久化及端点随各实验说明。}
\label{tab:eval-builds}
\centering\small
\begin{tabular}{@{}p{0.27\columnwidth}p{0.18\columnwidth}p{0.46\columnwidth}@{}}
\toprule
证据 & 节点构建 & 结果 \\
\midrule
当前连续链与 P & \texttt{f856c7b} & 六轮链总时长；三轮混合负载 \\
注入延迟与故障 & \texttt{9879f64} & 到账与公开完成 \\
历史读取与完整 N/R/C/P & \texttt{721800c} & 读取、存储、维护成本；历史处理矩阵 \\
\bottomrule
\end{tabular}
\end{table}

九轮当前实验使用相同节点二进制。新增计时点限于 \texttt{payctl}，六轮连续链均启用。连续链报告三轮总时长的中位数与范围；混合负载报告三轮轮内统计量的中位数。补充材料~S4 提供当前证据及版本对应，旧结果保留原构建标注。

'''
    write(prefix+'evaluation.tex',e[:start]+opening+'\n\n'+table+e[end:])

replace('supplement-revision.tex',r'\section{Boundary and Network Validation: Build 9879f64}',r'\section{Boundary, Timing, and Network Evidence}')
replace('supplement-revision.tex',r'\subsection{Waiting-Mode Timestamp Audit}',r'\subsection{Archived Waiting-Mode Timestamp Audit: Build 721800c}'+'\nThe following audit and 14.15-fold ratio belong to build \\texttt{721800c}; current-build chain totals appear in Section~\\ref{supp:final-evidence}.\n')
z=read('zh-supplement-revision.tex'); z=re.sub(r'\\section\{[^\n]+\}',lambda m:r'\section{边界、计时与网络证据}',z,count=1)
idx=z.index('14.15'); a=z.rfind(r'\subsection{',0,idx); b=z.index('}',a)
z=z[:a]+r'\subsection{历史等待模式时间戳审计：构建 721800c}'+'\n下列审计与 14.15 倍比值属于构建 \\texttt{721800c}；当前构建的连续链总时长见第~\\ref{supp:final-evidence}~节。\n'+z[b+1:]
write('zh-supplement-revision.tex',z)
print('Integrated Claude drafts, model order, terminology, deadline, risk, and evidence navigation.')
