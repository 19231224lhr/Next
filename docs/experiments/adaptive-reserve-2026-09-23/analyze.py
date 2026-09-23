#!/usr/bin/env python3
"""Recompute adaptive E2 evidence; no dependency on a plotting library unless --plot."""
import argparse
import csv
import importlib.util
import json
from pathlib import Path

OUT=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('base',OUT.parent/'finite-budget-2026-09-23/analyze.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)

def collect(prefix):
    summaries=[];curves=[]
    for path in sorted(OUT.glob(prefix+'*')):
        if not (path/'summary.json').exists():continue
        s,c=base.analyze_case(path)
        r=base.read(path/'reports/budget-v4.json');a=base.read(path/'reports/budget-audit.json')
        policy=base.read(path/'adaptive-policy.json');last=base.read(path/'last-snapshots.json')
        assert 'reserve controller:' not in (path/'driver.log').read_text(encoding='utf-8')
        public=[x for x in a['Payments'] if x['Public']]
        assert all(x['FeeSource']==2 for x in public)
        fee=s['fee'];s['user_fee_actual']=fee['Rewards']+fee['Burned']
        assert sum(x['FeeInputAmount'] for x in public)==sum(x['Change']+x['Refund'] for x in public)+s['user_fee_actual']+fee['Held']
        assert last['committee0']['FUEL']==0
        s['organization_FUEL_debit']=0
        balances=[(x['seconds'],x['actual_CAL_balance']) for x in c if x['node']=='committee0' and x['kind']==1]
        balances.sort()
        # Cover edges with nearest samples; report this as sampled, not exact integration.
        if balances[0][0]>0:balances.insert(0,(0,s['grant']))
        if balances[-1][0]<s['elapsed_s']:balances.append((s['elapsed_s'],balances[-1][1]))
        mean=base.sampled_mean(balances,s['elapsed_s'])
        s['reserve_mean_CAL']=mean;s['reserve_integral_CAL_seconds']=mean*s['elapsed_s']
        s['reserve_mean_input_window_CAL']=base.sampled_mean(balances,r['DurationSeconds'])
        s['reserve_final_CAL']=last['committee0']['CAL'];s['reserve_injected_CAL']=s['reserve_final_CAL']-s['grant']
        assert s['repaired']==0
        assert next(x['Grant'] for x in last['committee0']['Resources'] if x['Key']['Kind']==1)==s['reserve_final_CAL']
        s['reserve_cap']=policy['cap'];s['external_source_initial_CAL']=policy['cap']-s['grant'] if policy['enabled'] else 0
        s['external_source_final_CAL_inferred']=s['external_source_initial_CAL']-s['reserve_injected_CAL']
        s['dedicated_total_capital_CAL']=s['grant']+s['external_source_initial_CAL']
        s['dedicated_total_capital_integral_CAL_seconds']=s['dedicated_total_capital_CAL']*s['elapsed_s']
        s['allocated_reserve_efficiency_per_s']=sum(x['Obligation']['Amount'] for x in a['Payments'] if x.get('Obligation') and x['Obligation']['Status']==1)/s['reserve_integral_CAL_seconds']
        for key in ['CAL_per_backing_CAL_per_s_input_window','guaranteed_CAL_per_backing_CAL_per_s']:s.pop(key,None)
        s['member_final_grants']=[next(x['Grant'] for x in v['Resources'] if x['Key']['Kind']==1) for n,v in last.items() if n.startswith('org0-member')]
        assert all(g==s['reserve_final_CAL'] for g in s['member_final_grants'])
        times=[u[h]['FastMS'] for u in r['Units'] for h in ['Parent','Child'] if u[h]['ReadyUnixNS']]
        s['fast_p50_ms']=base.percentile(times,.5);s['fast_p95_ms']=base.percentile(times,.95);s['fast_p99_ms']=base.percentile(times,.99)
        ctl=path/'reports/reserve-control.jsonl'
        events=list(map(json.loads,base.lines(ctl))) if ctl.exists() or Path(str(ctl)+'.gz').exists() else []
        if policy['enabled']:assert any(x['event']=='sample' for x in events)
        s['refills']=[x for x in events if x['event']=='increase']
        s['refill_effective']=[x for x in events if x['event']=='effective']
        s['refills_over_planned_800ms']=sum(x['latency_ms']>800 for x in s['refill_effective'])
        s['controller_errors']=[x for x in events if x['event'] in ['sample_error','submit_error','stopped_old_request']]
        s['low_watermark_at_cap_samples']=sum(x['event']=='sample' and x['grant']==policy['cap'] and x['available']<x['low'] for x in events)
        s['max_uncertified_age_ms']=max((x.get('oldest_ms',0) for x in events),default=0)
        assert sum(x['delta'] for x in s['refills'])==s['reserve_injected_CAL']
        assert len(s['refills'])==len(s['refill_effective'])
        s['passed_workload']=s['closed_units']==s['offered_units'] and s['partial_approval_payments']==0 and all(v==0 for v in s['max_member_temporary_remaining'].values())
        (path/'analysis.json').write_text(json.dumps(s,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        summaries.append(s);curves+=c
    (OUT/'analysis.json').write_text(json.dumps(summaries,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    fields=['case','grant','reserve_final_CAL','reserve_mean_input_window_CAL','reserve_injected_CAL','offered_units','closed_units','not_started','partial_approval_payments','actual_obligations','actual_obligation_fraction','fast_p50_ms','fast_p95_ms','user_fee_actual','elapsed_s']
    with (OUT/'summary.csv').open('w',newline='',encoding='utf-8') as f:
        w=csv.DictWriter(f,fieldnames=fields);w.writeheader()
        for s in summaries:w.writerow({k:s[k] for k in fields})
    if curves:
        with (OUT/'curves.csv').open('w',newline='',encoding='utf-8') as f:
            w=csv.DictWriter(f,fieldnames=list(curves[0]));w.writeheader();w.writerows(curves)
    return summaries,curves

def plot(summaries,curves,prefix):
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    from matplotlib import font_manager
    fonts={f.name for f in font_manager.fontManager.ttflist}
    font=next((n for n in ['Microsoft YaHei','PingFang SC','Noto Sans CJK SC'] if n in fonts),'DejaVu Sans')
    plt.rcParams.update({'font.family':font,'font.size':10,'axes.spines.top':False,'axes.spines.right':False,'axes.unicode_minus':False,'pdf.fonttype':42})
    fig,axes=plt.subplots(2,3,figsize=(12,6),layout='constrained')
    for j,(fixed,dynamic,title) in enumerate([('high','adaptive','恒定输入：20 两跳/秒'),('step-high','step','负载阶跃：10 → 30 两跳/秒'),('delay-high','delay','父交易延迟：3 秒')]):
        series=[(fixed,'固定足额','#0072B2','--'),(dynamic,'动态补资','#D55E00','-')]
        if j==0:series.insert(0,('low','固定低额','#777777',':'))
        for suffix,label,color,style in series:
            case=prefix+suffix
            points=[r for r in curves if r['case']==case and r['node']=='committee0' and r['kind']==1]
            if not points:continue
            axes[0,j].plot([r['seconds'] for r in points],[r['actual_CAL_balance'] for r in points],label=label,color=color,ls=style,lw=1.2)
            r=base.read(OUT/case/'reports/budget-v4.json');start=r['StartedUnixNS']
            ts=sorted((max(u['Parent']['MemberClosedUnixNS'],u['Child']['MemberClosedUnixNS'])-start)/1e9 for u in r['Units'] if u['Parent']['MemberClosedUnixNS'] and u['Child']['MemberClosedUnixNS'])
            end=(r['StoppedUnixNS']-start)/1e9
            axes[1,j].step([0,*ts,end],[0,*range(1,len(ts)+1),len(ts)],where='post',label=label,color=color,ls=style,lw=1.2)
        axes[0,j].set(title=title,ylabel='组织账户实际 CAL',xlabel='时间（秒）',ylim=(0,None))
        axes[1,j].set(ylabel='累计闭环两跳单位',xlabel='时间（秒）',ylim=(0,6300))
        for ax in axes[:,j]:ax.set_xlim(0,310);ax.grid(alpha=.18);ax.legend(frameon=False)
    for ext in ['png','pdf']:fig.savefig(OUT/f'adaptive-reserve.{ext}',dpi=300)
    plt.close(fig)

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--prefix',default='adaptive-r2-');p.add_argument('--plot',action='store_true');a=p.parse_args()
    summaries,curves=collect(a.prefix)
    if a.plot:plot(summaries,curves,a.prefix)
    for s in summaries:print(s['case'],s['closed_units'],s['reserve_final_CAL'],round(s['reserve_mean_input_window_CAL'],1),s['partial_approval_payments'])
