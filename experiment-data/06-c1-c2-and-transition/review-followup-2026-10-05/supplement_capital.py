"""Append final raw-audited capital records in both languages."""
import csv
from pathlib import Path
root=Path(__file__).resolve().parents[3]
p=root/'docs/paper/review-2026-10-02'
with (root/'docs/experiments/current-capital-2026-10-05/final-audit-validated/runs.csv').open(encoding='utf-8',newline='') as f: rows=list(csv.DictReader(f))
assert len(rows)==12
values=[]
for r in rows:
    label=r['case'].replace('normal','1s').replace('delay','3s')
    values.append(label+' & '+' & '.join(r[k] for k in ['admitted_units','closed_units_within_input_window','closed_units','not_started','partial_approval_payments','actual_obligations','user_fee_actual'])+r' \\')
table='\n'.join(values)
en=r'''\subsection{Current Finite-Capital Records}
The same source archive as the three-arm chain experiment runs twelve
capital--delay cases after one calibration case. The calibration registers
one actual 100-CAL obligation and closes it from its delayed source. All
matrix cells offer 1,200 two-payment units; the principal inputs are independent,
not 1,200 distinct users. Three wallets hold the inputs. Parent publication
is independently scheduled from parent certification, so a child's failed
approval cannot prevent its certified parent from being submitted.

\begin{table}[htbp]\centering\small
\caption{Capital records. Closed60 is full unit closure within the 60-second
admission window; Closed includes bounded drain. Partial counts candidates,
Gaps counts actual obligations, and Fee is executed-payment FUEL expenditure.}
\label{tab:followup-capital-runs}
\begin{tabular}{lrrrrrrr}\toprule
Run & Started & Closed60 & Closed & Skipped & Partial & Gaps & Fee \\\midrule
'''+table+r'''
\bottomrule\end{tabular}\end{table}

All four replicas agree at the end of every run. CAL and FUEL conservation,
public fee closure, and zero final public gap are checked against stopped
state. Every public payment uses OwnerFinalUTXO, pays 94 FUEL, and has its
change and refund bound to the original payment; organization FUEL debit is
zero. Unpublished fee inputs that remain locked are counted separately from
expenditure. Final-source delays are below the 30-second compensation timeout;
all actual obligations are Fulfilled, with no compensation in this matrix.

In each low-budget run, all four CAL slices end at Available=0 and
Reserved=2400. Cross-member approval records contain exactly 96 retained
signatures distributed over candidates with one or two signatures, and no
unpublished candidate with three or four. Multiplying these local approvals
by 100 CAL explains the local reservations; it does not turn their overlapping
sum into organization backing. Completed payments have released their own
debits. No input conflicts exist between the remaining candidates, so the
specified conflict-triggered reclamation has no trigger. The high-budget
runs end with no temporary CAL, Execution or Bytes reservations.

The artifact's per-member time series distinguishes local Reserved from
public registered coverage and records resource refusals as member calls,
including repeated requests. It does not label such calls as distinct failed
payments. All twelve genesis configurations normalize identically after
removing genesis time and the selected organization CAL grant/account.
The driver has a bounded 128-unit window; skipped planned starts are included
in the denominator. Unfinished requests are right-censored at their actual
stop, not assigned a completion time. Child READY timing starts at the
post-construction client checkpoint and includes vector preparation,
dispatch, all retries and wallet receipt, rather than claiming an HTTP.Do
checkpoint. These memory-store timings are not pooled with disk-backed chain
latencies. Full curves, per-unit progress, remaining fee locks and assertions
are retained in the \texttt{current-capital-2026-10-05} artifact.

'''
zh=r'''\subsection{当前有限资本记录}
与三臂连续链相同的源码归档执行十二个资本--延迟案例，先做一轮校准。
校准确实登记了一项 100 CAL 义务，并由延迟来源正常关闭。
每格计划 1,200 个两付款单元，三个钱包持有独立本金输入，
不代表 1,200 名不同用户。父证书就绪后独立定时提交，
子批准失败不会阻止已经成证的父交易提交。

\begin{table}[htbp]\centering\small
\caption{资本记录。闭合60表示60秒输入窗口内完整单元闭合，闭合包含有界排空。
部分按候选计数，义务为实际登记项，费用为已执行付款的 FUEL 支出。}
\label{tab:followup-capital-runs}
\begin{tabular}{lrrrrrrr}\toprule
运行 & 启动 & 闭合60 & 闭合 & 跳过 & 部分 & 义务 & 费用 \\\midrule
'''+table+r'''
\bottomrule\end{tabular}\end{table}

每轮结束四副本一致，停止状态核对 CAL/FUEL 守恒、公开费用闭合与公共缺口归零。
每笔公开付款使用 OwnerFinalUTXO，实际费用 94 FUEL，找零与退款绑定原付款，组织 FUEL 扣减为零。
未公开付款仍锁定的费用输入与支出分开计数。
来源延迟低于 30 秒赔付期限，本矩阵全部实际义务为 Fulfilled，未发生赔付。

每个低预算运行的四份 CAL 切片最终均 Available=0、Reserved=2400。
跨成员批准记录恰含 96 份保留签名，分布在一票或两票候选中，
不存在三票或四票未公开候选。乘以每票 100 CAL 可解释本地预留，
但重叠切片总和不代表组织备付。已完成付款释放了各自扣减；
残余候选之间无输入冲突，因此指定的冲突触发回收没有触发条件。
高预算运行最终 CAL、Execution 和 Bytes 临时预留均为零。

逐成员时间序列区分本地 Reserved 与公共注册覆盖量，
资源拒绝按成员调用（包含重试）计数，不将其当作不同失败付款。
删除创世时间及所选组织的 CAL 授权/账户字段后，十二份创世配置归一相同。
驱动采用 128 单元有界窗口，跳过的计划启动计入分母。
未完成请求在真实停止点右删失，不赋予完成时间。
子 READY 计时从构造后的客户端检查点开始，包含向量准备、派发、全部重试与钱包接收，
不声称起于 HTTP.Do；内存存储计时不与磁盘连续链延迟混合。
完整曲线、逐单元进度、剩余费用锁与断言保留于 \texttt{current-capital-2026-10-05} 实验材料。

'''
for prefix,text,mark in [('',en,'\\subsection{Archived Chain and Retained Mixed-Load Records}'),('zh-',zh,'\\subsection{归档连续链与保留的混合负载记录}')]:
    path=p/(prefix+'supplement-final-evidence.tex');s=path.read_text(encoding='utf-8')
    assert '\\label{tab:followup-capital-runs}' not in s
    path.write_text(s.replace(mark,text+mark,1),encoding='utf-8')
