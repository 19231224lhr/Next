"""Integrate frozen tables and narrowly audited editorial corrections."""
from pathlib import Path
import csv

p = Path(__file__).resolve().parents[1]
experiments = p.parents[1] / 'experiments'
def rows(path):
    return list(csv.DictReader(path.open(encoding='utf-8')))
def table(caption, label, headings, records):
    return ('\\begin{table}[htbp]\n\\centering\\small\n\\caption{' + caption + '}\n'
            '\\label{' + label + '}\n\\begin{tabular}{@{}' + 'l' + 'r'*(len(headings)-1) + '@{}}\n\\toprule\n'
            + ' & '.join(headings) + r' \\' + '\n\\midrule\n'
            + '\n'.join(' & '.join(r) + r' \\' for r in records)
            + '\n\\bottomrule\n\\end{tabular}\n\\end{table}\n')

chain=rows(experiments/'final-continuation-2026-10-02/results/cases.csv')
mixed=rows(experiments/'final-mixed-2026-10-02/runs.csv')
costs=rows(experiments/'history-reader-2026-10-02/costs.csv')
for zh in [False,True]:
    title='最终版本逐轮证据' if zh else 'Per-Run Evidence for the Final Build'
    intro=(r'本节记录正文使用的生产版本 \texttt{721800c}。连续支付和混合负载使用相同节点二进制及 Comet 源码。以下历史方法和旧版本表格保留各自构建身份，不作为此版本的吞吐测量。' if zh else
           r'This section records production build \texttt{721800c}, used by the main evaluation. Continuation and mixed-load trials share node binaries and Comet sources. The subsequent historical methods and archived tables retain their original build identities and are not throughput measurements of this build.')
    content='\\section{'+title+'}\n\\label{sec:supp-current}\n'+intro+'\n\n'
    records=[[r['case'],f"{float(r['fast_chain_ms'])/1000:.6f}",f"{float(r['all_public_observed_ms'])/1000:.6f}",f"{float(r['closed_chain_ms'])/1000:.6f}",r['attempts'],r['fulfilled_gaps']] for r in chain]
    content+=table('最终版本连续支付；各时间自首笔实际发送起计，单位秒。' if zh else 'Final-build continuation; times in seconds from first actual send.', 'tab:supp-final-chain', ['运行','末跳收款','全链公共观察','成员关闭','尝试','缺口'] if zh else ['Run','Last receipt','All public','Members closed','Attempts','Gaps'],records)
    records=[[r['case'],f"{float(r['fast_p50_ms']):.3f}",f"{float(r['fast_p95_ms']):.3f}",f"{float(r['dispatch_p95_ms']):.3f}",f"{float(r['send_span_s']):.3f}",f"{float(r['drain_after_last_send_s']):.3f}",r['total_limit_hits'],r['send_limit_hits']] for r in mixed]
    content+=table('混合负载逐轮普通付款；延迟单位 ms，跨度和排空单位 s。后两列为总未完成上限和快速并发上限命中计数。' if zh else 'Ordinary payments per mixed-load run; latencies in ms, span and drain in s. Final columns count outstanding-limit and fast-concurrency-limit hits.', 'tab:supp-final-mixed',['运行','收款P50','收款P95','派发P95','发送跨度','排空','总限','快限'] if zh else ['Run','Receipt P50','Receipt P95','Dispatch P95','Span','Drain','Total cap','Fast cap'],records)
    records=[[str(r['run']),r['payments'],r['repairs'],f"{float(r['representation_work_ms']):.3f}",r['added_representation_kv_bytes'],f"{float(r['decisions_finalize_ms'])+float(r['decisions_commit_ms']):.3f}"] for r in sorted(costs,key=lambda r:(int(r['payments']),int(r['repairs']),int(r['run']))) ]
    content+=table('历史表示逐进程成本；维护及共同决定执行/提交单位 ms，净新增 KV 单位字节。' if zh else 'Historical representation per process; maintenance and common decision execution/commit in ms, net additional KV in bytes.', 'tab:supp-final-reader',['进程','付款','赔付','表示维护','净新增KV','共同决定'] if zh else ['Process','Payments','Compensated','Representation','Net extra KV','Common decision'],records)
    content+=('\n'+(r'这些成本来自离线成功执行夹具。表示维护包含顺序适配、组装、Finalize、同步应用 Commit 与物化；共同赔付决定单列。网络、共识等待、公共块保存和提交签名构造不在此计时内。KV 是真实编码记录的逻辑增量，不是两种独立部署的物理磁盘空间差。' if zh else r'These costs come from offline successful-execution fixtures. Representation work includes sequential adaptation, assembly, Finalize, synchronous application Commit, and materialization; common compensation decisions are reported separately. Network and consensus waits, public-block saving, and commit-signature construction are outside this timer. KV is the logical increase in encoded records, not the physical disk-space difference between two deployments.')+'\n')
    (p/('zh-supplement-current.tex' if zh else 'supplement-current.tex')).write_text(content,encoding='utf-8')

# Figure paths are portable between the local figures/ directory and flat Overleaf uploads.
for name in ['evaluation.tex','zh-evaluation.tex']:
    f=p/name;s=f.read_text(encoding='utf-8').replace('{figures/','{')
    if name.startswith('zh-'): s=s.replace('{continuation-final.pdf}','{continuation-final-zh.pdf}')
    f.write_text(s,encoding='utf-8')
for name in ['supplement-methods.tex','zh-supplement-methods.tex']:
    f=p/name;s=f.read_text(encoding='utf-8')
    if name.startswith('zh-'):
        s=s.replace('\\section{补充实验方法}', '\\section{归档构建的实验方法}')
        s=s.replace('最终快照','该系列后期快照').replace('用户自付 FUEL；签名','显式授权的组织代付 FUEL；签名')
    else:
        s=s.replace('\\section{Supplementary Experimental Methods}', '\\section{Experimental Methods for Archived Builds}')
        s=s.replace('The final snapshot','The later snapshot of this series').replace('the final snapshot','the later snapshot of this series').replace('final snapshot \\texttt','later series snapshot \\texttt')
        s=s.replace('and user-paid FUEL; signatures','and explicitly authorized organization-funded FUEL; signatures')
    f.write_text(s,encoding='utf-8')
f=p/'data/check-manuscripts.py';s=f.read_text(encoding='utf-8');s=s.replace('"supplement-tables"]:', '"supplement-tables", "supplement-current", "supplement-archived"]:');f.write_text(s,encoding='utf-8')
