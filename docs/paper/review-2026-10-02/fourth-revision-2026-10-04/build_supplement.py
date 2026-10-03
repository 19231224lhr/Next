from pathlib import Path
import json,csv
p=Path('docs/paper/review-2026-10-02'); e=Path('docs/experiments/final-evidence-2026-10-04')
chain=list(csv.DictReader((e/'results/cases.csv').open(encoding='utf-8')))
mix=list(csv.DictReader((e/'results-mixed/runs.csv').open(encoding='utf-8')))
timing=json.loads((e/'results/follow-timing-summary.json').read_text(encoding='utf-8')); print(timing.keys())
for zh in (False,True):
    title='当前构建补证与实现对应' if zh else 'Current-Build Evidence and Implementation Correspondence'
    s='\\section{'+title+'}\n\\label{supp:final-evidence}\n'
    s+= ('生产基线为 \\texttt{f856c7b}。六轮连续付款与三轮代表性 P 使用相同节点二进制和 Comet 源码；新增 \\texttt{payctl} 跟块检查点在全部连续付款轮次启用。工件 \\texttt{final-evidence-2026-10-04} 保存源码、节点指纹、逐笔记录、审计、分析脚本与回归日志。表格由原始 CSV/JSON 生成。\n' if zh else 'The production baseline is \\texttt{f856c7b}. Six chain runs and three representative P runs share node binaries and Comet sources. Additional \\texttt{payctl} following checkpoints are enabled in every chain run. The \\texttt{final-evidence-2026-10-04} artifact retains source and binary fingerprints, payment records, audits, analysis scripts, and regression logs. Tables below are generated from their CSV/JSON records.\n')
    s+='\\begin{table}[htbp]\n\\centering\\small\n\\caption{'+('六轮连续付款；时间自首笔实际发送起计，单位秒。公共列为第 100 笔的公共观察，闭合列为全部付款的成员观察。' if zh else 'Six chain runs. Times start at the first actual send, in seconds. Public denotes observation of payment 100; closure denotes all-payment member observation.')+'}\n\\label{tab:final-evidence-chain}\n\\begin{tabular}{lrrrrr}\\toprule\n'
    s+=('轮次 & 到账链 & 公共 & 闭合 & 尝试 & 缺口 \\\\\midrule\n' if zh else 'Run & Receipt chain & Public & Closure & Attempts & Gaps \\\\\midrule\n')
    for r in chain:s+=f"{r['case']} & {float(r['fast_chain_ms'])/1000:.3f} & {float(r['public_chain_ms'])/1000:.3f} & {float(r['closed_chain_ms'])/1000:.3f} & {r['attempts']} & {r['fulfilled_gaps']} \\\\\n"
    s+='\\bottomrule\\end{tabular}\n\\end{table}\n'
    s+= ('\\noindent\\textbf{阶段口径。}一般阶段每轮 100 个付款，跳间过渡每轮 99 个；分别在轮内计算 P50/P95，再取三轮对应分位数的中位数。同一钱包、高度的观测点按付款展开，不作为独立高度样本。区间采用同机 Unix 纳秒时间戳，整链时长采用单调时钟。验证检查点在成功 VerifyBlock 后、PrepareBlock 入口；后事务回调在 Update 返回后，不代表同步刷盘。负值保留为执行重叠。成功取数耗时是提交至完整取数的一部分；各分位数不能相加，返回时间不标识证据最早可得时刻。\n' if zh else '\\noindent\\textbf{Stage definitions.} Ordinary intervals use 100 payments per run and inter-hop intervals use 99 transitions. We calculate P50/P95 within each run, then take the median of the corresponding three quantiles. Shared wallet/height checkpoints are expanded by payment, not treated as independent height samples. Intervals use same-host Unix-nanosecond timestamps; whole-chain duration uses a monotonic clock. The verification checkpoint follows successful VerifyBlock at PrepareBlock entry. The post-transaction callback follows Update return, not a synchronous flush. Negative differences retain execution overlap. Successful-fetch time is contained in commit-to-complete-fetch; neither overlapping intervals nor quantiles are added, and a successful return does not identify the earliest evidence availability.\n')
    s+='\\begin{table}[htbp]\n\\centering\\small\n\\caption{'+('客户端阶段；表项为三轮轮内分位数的中位数，单位毫秒。' if zh else 'Client stages: medians of three within-run quantiles, in milliseconds.')+'}\n\\label{tab:final-evidence-stages}\n\\begin{tabular}{lrrrr}\\toprule\n'
    s+=('阶段 & fast P50 & fast P95 & wait P50 & wait P95 \\\\\midrule\n' if zh else 'Interval & Fast P50 & Fast P95 & Wait P50 & Wait P95 \\\\\midrule\n')
    stages=[('send_to_commit_ms','发送至应用提交','Send to application commit'),('commit_to_complete_fetch_ms','提交至完整取数','Commit to complete fetch'),('verify_ms','取数至验证检查点','Fetch to verification checkpoint'),('prepare_and_record_ms','检查点至后事务回调','Checkpoint to post-transaction callback'),('record_to_driver_observe_ms','回调至驱动观察','Callback to driver observation'),('successful_fetch_ms','成功取数调用（重叠）','Successful fetch call (overlapping)'),('ready_to_next_send_ms','到账至下一跳发送','Receipt to next send'),('driver_observe_to_next_build_ms','观察至下一跳构造','Observation to next build'),('next_build_to_send_ms','下一跳构造至发送','Next build to send')]
    agg=timing['medians_of_run_quantiles']
    if agg is None: print(timing.keys());raise SystemExit('unknown aggregate')
    for key,cn,en in stages:
        a=agg['fast'][key];b=agg['wait_final'][key];s+=(cn if zh else en)+f" & {a['p50']:.3f} & {a['p95']:.3f} & {b['p50']:.3f} & {b['p95']:.3f} \\\\\n"
    s+='\\bottomrule\\end{tabular}\n\\end{table}\n'
    s+='\\begin{table}[htbp]\n\\centering\\small\n\\caption{'+('P 的驱动回压；上限命中次数为驱动事件计数，不是拒绝付款数。全部付款闭合。' if zh else 'P driver backpressure. Limit hits count driver events, not rejected payments. All payments close.')+'}\n\\label{tab:final-evidence-pressure}\n\\begin{tabular}{lrrrr}\\toprule\n'
    s+=('轮次 & 未完成峰值 & 总上限命中 & 快速上限命中 & 计划至到账 P95 (ms) \\\\\midrule\n' if zh else 'Run & Peak outstanding & Total-limit hits & Fast-limit hits & Scheduled receipt P95 (ms) \\\\\midrule\n')
    for r in mix:s+=f"{r['case']} & {r['peak_sent_unfinished']} & {r['total_limit_hits']} & {r['send_limit_hits']} & {float(r['scheduled_receipt_p95_ms']):.3f} \\\\\n"
    s+='\\bottomrule\\end{tabular}\n\\end{table}\n\\clearpage\n'
    (p/(('zh-' if zh else '')+'supplement-final-evidence.tex')).write_text(s,encoding='utf-8')
