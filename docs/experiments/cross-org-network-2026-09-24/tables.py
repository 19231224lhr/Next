#!/usr/bin/env python3
"""Generate E7 evidence tables from audited runs, preserving run-level repeats."""
import json
from pathlib import Path
from analyze import read, summarize, quantiles

ROOT = Path(__file__).resolve().parent


def resource_summary(case, sent):
    samples = [json.loads(s) for s in (case/'resources.jsonl').read_text().splitlines()]
    samples = [s for s in samples if s['phase'] == 'formal']
    def snapshot(s):
        names = {str(pid): name for name, pid in s['pids'].items()}
        result = {}
        for line in s['ps'].splitlines():
            pid, cpu, rss = line.split()
            seconds = 0.0
            for part in cpu.split(':'):
                seconds = seconds * 60 + float(part)
            result[names[pid]] = (seconds, int(rss))
        return result
    snapshots = [snapshot(s) for s in samples]
    seconds = (samples[-1]['NS'] - samples[0]['NS']) / 1e9
    groups = {'services': lambda n: n != 'proxy' and not n.startswith('wallet'),
              'wallets': lambda n: n.startswith('wallet'), 'proxy': lambda n: n == 'proxy'}
    result = {'sample_seconds': seconds}
    for group, match in groups.items():
        cpu = sum(v[0] - snapshots[0][n][0] for n, v in snapshots[-1].items() if match(n))
        peak = max(sum(v[1] for n, v in s.items() if match(n)) for s in snapshots)
        result[group] = {'cpu_seconds': cpu, 'average_cores': cpu / seconds,
                         'cpu_ms_per_payment': cpu * 1000 / sent, 'peak_rss_mib': peak / 1024}
    before, after = read(case/'formal-proxy-start.json'), read(case/'proxy-stats.json')
    counters = ['Requests', 'Forwarded', 'Completed', 'Cancelled', 'Errors', 'RequestBytes', 'ResponseBytes']
    result['http'] = {k: sum(v[k] - before.get(route, {}).get(k, 0)
                                   for route, v in after.items() if not route.endswith('/healthz')) for k in counters}
    targets = read(case/'configuration.json')['targets']
    routes = {}
    for route, values in after.items():
        source, target, endpoint = route.split('/', 2)
        if endpoint == 'healthz':
            continue
        key = f"{source}->{targets[int(target)]['Site']}/{endpoint}"
        totals = routes.setdefault(key, {k: 0 for k in counters})
        for k in counters:
            totals[k] += values[k] - before.get(route, {}).get(k, 0)
    result['routes'] = routes
    return result


def main():
    cases = sorted(ROOT.glob('formal-r*'))
    assert len(cases) == 24 and all((p/'passed.json').exists() for p in cases)
    rows = [summarize(p) for p in cases]
    lines = ['# E7 逐轮结果', '',
             '主延迟表仅统计首次发送位于前 120 秒的付款，单位 ms。每行是一轮独立运行；不将逐笔样本当作独立重复。', '',
             '| 轮次 | 实发 / 完成（全轮） | ACK P50 | ACK P95 | ACK P99 | 网关响应 P50 | 收款交付 P50 | 接收处理 P50 | 公共观察 P50 |',
             '| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |']
    for r in rows:
        a, m = r['all'], r['first120']['ms']
        vals = [m['FastNS'][p] for p in ['50', '95', '99']]
        vals += [m[k]['50'] for k in ['ResponseNS', 'DeliveryNS', 'ReceiveNS', 'PublicNS']]
        lines.append(f"| {r['case']} | {a['sent']} / {a['closed']} | " + ' | '.join(f'{x:.3f}' for x in vals) + ' |')
    lines += ['', '阶段分别计算分位数，不能将各列中位数相加。网关响应与交付在发送端计时；接收处理在收款进程内计时，不做跨进程时间戳相减。', '',
              '## 驱动、续花与后台', '',
              '| 轮次 | 未发 | 使用 TXCer 输入 | 完整十跳链 | 整链 P50（ms） | 待办采样峰值 | 最老待办峰值（s） | 组织 A / B 实发 |',
              '| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |']
    for r in rows:
        chain = r['chain_ms'].get('50')
        lines.append(f"| {r['case']} | {r['unsent']} | {r['all']['certificate_inputs']} | {r['chain_count']} | "
                     + (f'{chain:.3f}' if chain is not None else '—')
                     + f" | {r['peak_pending']} | {r['oldest_pending_s']:.3f} | "
                     + ' / '.join(str(x['sent']) for x in r['per_org']) + ' |')
    lines += ['', '未发为计划速率 × 持续时间减去实际首发数量，包含过期机会及名额限制。整链为全运行窗口内完成的十跳链，包含 lane 等待；截止时的合法短前缀不算完整十跳，但仍纳入付款与账务审计。待办来自约一秒采样，不能称为精确瞬时最大值。', '',
              '### 驱动限制原始计数', '',
              '| 轮次 | 过期机会 | 无就绪输入 | 后台名额满 | 快速名额满 | 进度查询错误 |',
              '| --- | ---: | ---: | ---: | ---: | ---: |']
    for r in rows:
        lines.append(f"| {r['case']} | " + ' | '.join(str(r['driver'][k]) for k in ['SkippedPacing', 'NoReady', 'PendingFull', 'WorkerFull', 'ProgressErrors']) + ' |')
    lines += ['',
              '## 审计（含预热）', '',
              '| 轮次 | 付款 | 退款 | 已核实签署记录 | 真实链边 | 未最终输入记录 | 四委员与账务审计 |',
              '| --- | ---: | ---: | ---: | ---: | ---: | --- |']
    for p in cases:
        a = read(p/'reports/e7-audit.json')
        lines.append(f"| {p.name} | {a['Payments']} | {a['Refunds']} | {a['Signers']} | {a['ChainEdges']} | {a['CertificateInputs']} | 通过 |")
    lines += ['', '正式计数不含每轮 500 笔预热；审计涵盖预热和正式付款。签署记录数按实际成员核对，不把成员重叠额度相加当作真实资本。', '',
              '## 跨站 HTTP 校准', '',
              '| 轮次 | 注入 RTT（ms） | 运行前实测 P50 / P95（ms） | 运行后实测 P50 / P95（ms） |',
              '| --- | ---: | ---: | ---: |']
    for p, r in zip(cases, rows):
        values = []
        for phase in ['before', 'after']:
            cs = read(p/f'calibration-{phase}.json')
            q = quantiles([v for c in cs if c['source'] != c['target'] for v in c['elapsed_ns']])
            values.append(f"{q['50']:.3f} / {q['95']:.3f}")
        lines.append(f"| {p.name} | {r['config']['rtt']} | " + ' | '.join(values) + ' |')
    lines += ['', '## 资源与 HTTP 载荷（全轮及收尾）', '',
              '| 轮次 | 服务 CPU ms/笔 | 钱包 CPU ms/笔 | 代理 CPU ms/笔 | 服务 RSS 峰值 MiB | HTTP 请求/笔 | HTTP 载荷 KiB/笔 |',
              '| --- | ---: | ---: | ---: | ---: | ---: | ---: |']
    resources = {}
    for p, r in zip(cases, rows):
        n = r['all']['sent']; a = resource_summary(p, n); resources[p.name] = a
        vals = [a[g]['cpu_ms_per_payment'] for g in ['services', 'wallets', 'proxy']]
        vals += [a['services']['peak_rss_mib'], a['http']['Requests']/n,
                 (a['http']['RequestBytes'] + a['http']['ResponseBytes'])/1024/n]
        lines.append(f'| {p.name} | ' + ' | '.join(f'{v:.3f}' for v in vals) + ' |')
    lines += ['', 'CPU 是正式阶段首末约一秒采样间的进程累计 CPU 差额，覆盖公共收尾，除以全轮付款数；服务为 14 个节点，钱包与代理单列。RSS 含固定创世及历史状态，不等于活动积压。HTTP 载荷只统计代理可见正文，不含 HTTP/TCP 头和委员会 P2P；计数窗口从正式阶段开始前至末尾校准后，排除 healthz 正文，但包含该额外时间内持续发生的后台查询；这是可见传输总量，不是每笔固有协议成本。代理 Errors 包括未就绪查询等 HTTP 错误，不等于付款失败。', '']
    (ROOT/'resource-summary.json').write_text(json.dumps(resources, indent=2)+'\n')
    (ROOT/'TABLES.md').write_text('\n'.join(lines)+'\n', encoding='utf-8')
    (ROOT/'summary-all.json').write_text(json.dumps(rows, indent=2)+'\n')


if __name__ == '__main__':
    main()
