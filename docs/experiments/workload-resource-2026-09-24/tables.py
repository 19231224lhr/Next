#!/usr/bin/env python3
"""Render compact Chinese tables from per-run summaries, preserving repetition."""
import json,statistics
from pathlib import Path
OUT=Path(__file__).resolve().parent
def read(path):return json.loads(path.read_text())
def span(xs,scale=1,digits=3):
    ys=[x/scale for x in xs]
    return f'{statistics.median(ys):.{digits}f} [{min(ys):.{digits}f}–{max(ys):.{digits}f}]'
def table(headers,rows):return '\n'.join(['| '+' | '.join(headers)+' |','| '+' | '.join(['---']*len(headers))+' |',*['| '+' | '.join(map(str,r))+' |' for r in rows]])
def main():
    runs=[read(p) for p in sorted(OUT.glob('formal-*/summary.json'))];assert len(runs)==9
    agg=read(OUT/'aggregate.json');text=['# E8 完整统计表','以下以独立运行为重复单位，`中位数 [最小–最大]` 不是置信区间。逐笔原始记录可重算。']
    rows=[]
    for mode in 'ABC':
        rs=[r for r in runs if r['mode']==mode]
        rows.append([mode,sum(r['sent'] for r in rs),span([r['actual_tps'] for r in rs],digits=2),span([r['fast_p50_ms'] for r in rs]),span([r['fast_p95_ms'] for r in rs]),span([r['fast_p99_ms'] for r in rs])])
    text+=['## 快速到账',table(['配置','三轮正式付款数','实际 TPS','P50 / ms','P95 / ms','P99 / ms'],rows)]
    rows=[]
    for mode in 'ABC':
        rs=[r for r in runs if r['mode']==mode]
        rows.append([mode,*[span([r[k] for r in rs]) for k in ['build_p95_ms','wallet_p95_ms','public_p95_ms','member_p95_ms','closed_p95_ms']]])
    text+=['## 阶段 P95',table(['配置','构造 / ms','钱包接收处理 / ms','公共观察 / ms','成员收尾 / ms','完整闭环观察 / ms'],rows),'计划→发送的原始差值混用投影墙钟和当前墙钟，出现负值及偏移，不作为排队性能指标。实际事件间隔亦来自墙钟，见报告计时限制；没有将负值截零。']
    rows=[]
    for mode in 'ABC':
        a=agg[mode]
        cells=[]
        for key,scale in [('service_core_ms_per_completion',1),('driver_core_ms_per_completion',1),('http_payload_bytes_per_ready',1024),('logical_service_bytes_per_payment',1024),('service_peak_rss_gib',1)]:
            v=a[key];cells.append(f"{v['median']/scale:.2f} [{v['min']/scale:.2f}–{v['max']/scale:.2f}]")
        rows.append([mode,*cells])
    text+=['## 资源',table(['配置','九服务 core-ms/闭环','驱动 core-ms/闭环','HTTP KiB/付款','九服务应用 KV KiB/付款','九服务 RSS 峰值 GiB'],rows),'HTTP 不含共识 P2P；应用 KV 不含 CometBFT 块存储。KV 含预热；CPU 分母使用同采样窗口闭环完成数。RSS 总峰值为同时刻九服务之和，驱动另列在原始摘要。']
    rows=[]
    for mode in 'ABC':
        rs=[r for r in runs if r['mode']==mode]
        rows.append([mode,rs[0]['origin_outputs'],span([r['preparation_to_warmup_s'] for r in rs],digits=1),span([r['initial_service_rss_bytes'] for r in rs],scale=2**30,digits=2),span([r['peak_rss_bytes']['load'] for r in rs],scale=2**30,digits=2)])
    text+=['## 准备成本',table(['配置','创世输出数','开始准备→预热 / s','初始九服务 RSS / GiB','驱动 RSS 峰值 / GiB'],rows),'创世输出数包含 CAL 和 FUEL；准备包括夹具构造、密钥/输入生成、节点启动与初始统计，不在快速到账计时内。三组使用相同规模的创世池，C 的额外根保持未使用。']
    rows=[]
    for name,prefix in [('网关','gateway'),('四成员合计','org0-member'),('四委员合计','committee')]:
        for mode in 'ABC':
            hs=[];vs=[]
            for r in runs:
                if r['mode']!=mode:continue
                nodes=[v for k,v in r['nodes'].items() if k.startswith(prefix)]
                hs.append(sum(n['Hits'] for n in nodes)/sum(n['Queries'] for n in nodes)*100)
                vs.append(sum(n['Verifications'] for n in nodes)/r['ready'])
            rows.append([name,mode,span(hs,digits=2),span(vs)])
    text+=['## 接收描述符缓存',table(['角色','配置','命中率 / %','实际描述符验签次数/付款'],rows),'这是描述符签名验证，不是用户交易、QC 与区块签名的总验签数。']
    rows=[]
    for r in runs:
        rows.append([r['case'],r['sent'],f"{r['actual_tps']:.2f}",r['peak_pending'],f"{r['drain_s']:.3f}",r['skipped_pacing_including_warm'],r['no_ready_including_warm'],r['pending_full_including_warm'],r['extra_submit_attempts']])
    text+=['## 逐轮完成与发送阻塞',table(['轮次','正式已发且完成','TPS','正式待办峰值','排空 / s','跳过时隙¹','槽均忙¹','总上限阻塞¹','额外首投尝试'],rows),'¹ 原计数器覆盖预热及正式期；正式期均已发全成，未发机会不算失败交易，也不算完成量。']
    rows=[]
    for r in runs:
        if r['mode']=='C':rows.append([r['case'],r['chain_edges'],r['certificate_inputs'],r['before_parent_commit'],f"{100*r['before_parent_commit']/r['chain_edges']:.2f}%",r['missing_inputs'],r['root_cal_inputs_used']])
    text+=['## 真正续花与公共缺失',table(['轮次','后继边','证书输入','父 Commit 前首发¹','占后继比例¹','执行时缺失输入','正式根 CAL 数'],rows),'¹ 墙钟匹配观测，未记录时钟误差上界，不作为严格先后证明。指定父输出消费及 MissingInputs 来自独立的业务审计；记录可匹配不等于事件顺序无不确定性。']
    bursts=[read(p) for p in sorted(OUT.glob('burst-*/summary.json'))]
    if len(bursts)==3:
        rows=[]
        for r in bursts:
            for p in r['burst_phases']:rows.append([r['case'],f"{p['start_s']}–{p['end_s']} s",p['target_tps'],f"{p['actual_tps']:.2f}",f"{p['fast_p95_ms']:.3f}",f"{p['closed_p95_ms']:.3f}"])
        text+=['## 短时突增',table(['轮次','阶段','目标 TPS','实际 TPS','快速 P95 / ms','闭环观察 P95 / ms'],rows),table(['轮次','已发/快速可用','待办峰值','恢复时刻前付款全部闭环 / s','最终排空 / s'],[[r['case'],f"{r['sent']}/{r['ready']}",r['peak_pending'],f"{r['pre_recovery_cohort_drain_s']:.3f}",f"{r['drain_s']:.3f}"] for r in bursts]),'恢复队列时间：输入恢复为 500 TPS 后，等待首发在第 75 秒之前的所有付款完整闭环；后续新付款仍继续发送。它不是停止全部输入后的排空时间。']
    (OUT/'TABLES.md').write_text('\n\n'.join(text)+'\n',encoding='utf-8')
if __name__=='__main__':main()
