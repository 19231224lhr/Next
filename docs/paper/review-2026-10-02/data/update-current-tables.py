from pathlib import Path
import json

root = Path(__file__).resolve().parents[1]
rows = []
for x in json.loads((root / 'data/recovery-runs.json').read_text(encoding='utf-8')):
    seed, rate = x['case'][1:].split('-p')
    t = x['timing']
    repair = t.get('deadline_to_all_physical_observed_ms', {})
    r = '--' if not repair.get('n', 0) else f"{repair['p50']/1000:.3f}"
    rows.append(f"{seed} & {rate}\\% & {x['actual_repairs']}/{x['actual_recoveries']} & {x['elapsed_s']:.3f} & {t['normal_child_fast_ms']['p50']:.3f} & {t['normal_child_fast_ms']['p95']:.3f} & {r} \\\\")
assert len(rows) == 9
for language in ['', 'zh-']:
    path = root / (language + 'supplement-tables.tex')
    s = path.read_text(encoding='utf-8')
    label = s.index('\\label{tab:supp-liability}')
    a = s.rindex('\\begin{table}', 0, label)
    b = s.index('\\end{table}', label) + len('\\end{table}')
    if language:
        caption = '当前来源回款矩阵的逐轮记录。每轮在 60 秒内提供 1,200 个两跳单元（2,400 笔付款）。收款分位数为正常子付款的毫秒数；总耗时及截止至全部副本物理安装的观察中位数为秒，后者含 250 毫秒轮询。每轮结束时备付余额为 60,000 CAL，净支出及缺口为零。'
        head = '种子 & 扣留率 & 赔付/回款 & 总耗时 & 子 P50 & 子 P95 & 安装 P50'
    else:
        caption = 'Current source-recovery runs. Each offers 1,200 two-hop units (2,400 payments) over 60 seconds. Receipt quantiles for normal child payments are in ms; elapsed and deadline-to-all-replica-installation observation median are in seconds. The latter includes 250-ms polling. Every run ends with a 60,000-CAL reserve, zero net expenditure, and zero gap.'
        head = 'Seed & Withheld & Paid/recovered & Elapsed & Child P50 & Child P95 & Install P50'
    table = '\n'.join(['\\begin{table}[!ht]', '\\centering\\small', '\\caption{' + caption + '}', '\\label{tab:supp-liability}', '\\begin{tabular}{@{}rrrrrrr@{}}', '\\toprule', head + ' \\\\', '\\midrule', *rows, '\\bottomrule', '\\end{tabular}', '\\end{table}'])
    path.write_text(s[:a] + table + s[b:], encoding='utf-8')
print('\n'.join(rows))
