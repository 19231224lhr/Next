"""Generate bilingual current-chain records; retain and label earlier evidence."""
import json
from pathlib import Path
root=Path(__file__).resolve().parents[3]
p=root/'docs/paper/review-2026-10-02'
d=json.loads((root/'docs/experiments/wait-baseline-2026-10-05/stage-analysis.json').read_text())
rows=[]
for r in d['runs']:
    v=r['disjoint_stage_totals_ms']
    values=[r['chain_ms'],v['http_success_service_ms'],v['http_failed_service_ms'],v['retry_sleep_ms'],v['public_wait_99_ms'],v['member_gate_99_ms'],r['remaining_construction_recording_and_scheduling_ms']]
    label=r['case'].replace('wallet-wait','W').replace('member-wait','M').replace('fast','F')
    rows.append(label+' & '+' & '.join(f'{n/1000:.3f}' for n in values)+r' \\')
tab='\n'.join(rows)
en=r'''\subsection{Current Three-Arm Continuation}
The source archive SHA-256 begins \texttt{8937df90}; this is an archive identity,
not a Git commit. The frozen manifest retains the dirty tree and actual source
bytes. Each of the nine trials has its own raw requests, attempt timings,
member progress samples, public records, stopped-state audits, and fingerprints.
All 120 entries of the original result manifest verify. Three repeats rotate
F/W/M, W/M/F, M/F/W; each mode starts from the same unused genesis and keys.
F continues after certification; W waits for wallet-verified public execution;
M additionally requires three members to report the precise parent as observed.
Its five-millisecond polling gate rejects timeout, excludes the initial genesis
bootstrap from the chain endpoint, and includes all 99 successor gates.

\begin{table}[htbp]\centering\small
\caption{Current chain-stage totals, seconds. Within each row the six stage
columns sum to Chain before rounding. Other includes successor construction,
receipt processing and client dispatch/bookkeeping.}
\label{tab:followup-chain-stages}
\begin{tabular}{lrrrrrrr}\toprule
Run & Chain & HTTP OK & HTTP fail & Retry sleep & Public wait & Gate & Other \\\midrule
'''+tab+r'''
\bottomrule\end{tabular}\end{table}

All stage durations in Table~\ref{tab:followup-chain-stages} use client
monotonic measurements; the two waits concern 99 intermediate hops. Concurrent
wallet following is not added again. W has 23, 12, and 25 failed attempts,
each reported as HTTP~503 \texttt{QUORUM\_UNAVAILABLE}; this response does not
identify which member input check failed. F and M have no failed attempts.
The M/F ratios in paired repetitions are 13.691, 13.995, and 13.988; the
ratio of mode medians is 13.9815. M minus W differs by $+68.271$, $+170.574$,
and $-54.324$\,ms, so these three runs do not establish a stable W/M ranking.

All 297 F successors are sent before the earliest parent application-commit
completion across the four replicas, with a minimum lead of 201.161\,ms.
The 2, 3, and 5 actual out-of-order obligations are separately matched to their
parent output and consumer; all are Fulfilled. All 900 payments close, replicas
agree, and outboxes empty. Each chain spends 9,400 FUEL (8,400 rewards and
1,000 burned). The certificate is 779 B; F requests are 1,979 B initially and
2,766 B thereafter, while W/M requests remain 1,979 B. F's last-payment public
observation and all-payment member closure have medians 5.231 and 5.289\,s.

Following checkpoints match all 900 payments by fact, height, and recipient
wallet. In M, the median of per-run P50/P95 commit-to-complete-fetch is
285.011/308.528\,ms; subsequent P95s are 0.208\,ms through verification,
5.217\,ms through preparation/local recording, and 5.051\,ms to driver observation.
The fetch source is committee0. Fetch includes the signed successor header,
block and results, so its delay includes evidence formation, replica progress,
polling and retrieval. These quantiles are not additive. Cross-process matching
uses same-host Unix timestamps; their first-to-last spans differ from the
monotonic chain durations by 0.033--6.241\,ms. NoSync local recording is not a
durability-flush endpoint.

\subsection{Archived Chain and Retained Mixed-Load Records}
The following six-chain table and its client-stage measurements belong to the
earlier \texttt{f856c7b} series. They remain as versioned evidence and do not
supply the current three-arm headline. P/R records retain that same earlier
build and their original experiment scope.
'''
zh=r'''\subsection{当前三臂连续支付}
源码归档 SHA-256 前缀为 \texttt{8937df90}，这是归档身份而非 Git 提交。
冻结清单保留工作树变更及实际源码字节；九轮各自保存原始请求、尝试计时、
成员进度样本、公开记录、停止状态审计及指纹。原结果清单的 120 项摘要全部核对通过。
三轮按 F/W/M、W/M/F、M/F/W 轮换，各模式均从相同未运行创世和密钥开始。
F 认证后续付，W 等待钱包验证公开执行，M 还要求三名成员报告已观察到精确父交易。
门控每五毫秒轮询，超时不放行；链端点排除首次创世启动检查，计入全部 99 次后继门控。

\begin{table}[htbp]\centering\small
\caption{当前链阶段总量，单位秒。每行六个阶段列在舍入前加回链总量。
其他项包括后继构造、收据处理及客户端派发与记账。}
\label{tab:followup-chain-stages}
\begin{tabular}{lrrrrrrr}\toprule
运行 & 链总量 & HTTP成功 & HTTP失败 & 重试休眠 & 公开等待 & 门控 & 其他 \\\midrule
'''+tab+r'''
\bottomrule\end{tabular}\end{table}

表~\ref{tab:followup-chain-stages} 各阶段均使用客户端单调时长；两类等待涉及 99 个中间跳，
并发的钱包跟随不重复加入。W 失败尝试分别为 23、12、25 次，
均记录为 HTTP~503 \texttt{QUORUM\_UNAVAILABLE}，该响应不定位具体成员输入检查。
F、M 均无失败尝试。三轮配对 M/F 比值为 13.691、13.995、13.988，
模式中位数之比为 13.9815。M 减 W 分别为 $+68.271$、$+170.574$、$-54.324$\,ms，
三轮数据不建立稳定的 W/M 排名。

全部 297 个 F 后继首次发送均早于四副本最早父应用提交，最小提前 201.161\,ms。
另行匹配实际错序义务与父输出、消费者，三轮分别为 2、3、5 项，全部 Fulfilled。
900 笔全部闭合，副本一致且 outbox 排空。每链费用 9,400 FUEL，含奖励 8,400 与销毁 1,000。
证书为 779 B，F 请求首笔 1,979 B、后续 2,766 B，W/M 均为 1,979 B。
F 最后一笔公开观察和全部付款成员闭合中位数分别为 5.231、5.289\,s。

按事实、高度及收款钱包匹配全部 900 笔跟随检查点。
M 的提交至完整取数轮内 P50/P95 的跨轮中位数为 285.011/308.528\,ms；
后续验证、准备及本地记录、至驱动观察的 P95 分别为 0.208、5.217、5.051\,ms。
取数端为 committee0；取数包含后继签名头、区块与结果，故其延迟包含证据形成、
副本推进、轮询和获取。分位数不可相加。跨进程匹配使用同机 Unix 时间戳，
其首末跨度与单调链时长相差 0.033--6.241\,ms。
NoSync 本地记录并非耐久刷盘端点。

\subsection{归档连续链与保留的混合负载记录}
下方六轮链表与客户端阶段数据属于较早的 \texttt{f856c7b} 系列，作为带版本证据保留，
不用于当前三臂结果摘要。P/R 保留相同的较早构建及原实验范围。
'''
for prefix,text in [('',en),('zh-',zh)]:
    path=p/(prefix+'supplement-final-evidence.tex')
    s=path.read_text(encoding='utf-8');marker='\\label{supp:final-evidence}'
    i=s.index(marker)+len(marker)
    assert '\\label{tab:followup-chain-stages}' not in s
    s=s[:i]+'\n'+text+'\n'+s[i:]
    path.write_text(s,encoding='utf-8')
