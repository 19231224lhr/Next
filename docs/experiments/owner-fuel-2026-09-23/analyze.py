#!/usr/bin/env python3
"""Recompute owner-FUEL results using E2's unchanged accounting definitions."""
import argparse
import csv
import importlib.util
import json
from pathlib import Path

OUT=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('base_analysis',OUT.parent/'finite-budget-2026-09-23/analyze.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)

def collect(prefix):
    summaries=[];curves=[]
    for path in sorted(OUT.glob(prefix+'*')):
        if not (path/'summary.json').exists():continue
        s,c=base.analyze_case(path)
        audit=base.read(path/'reports/budget-audit.json')
        public=[p for p in audit['Payments'] if p['Public']]
        assert all(p['FeeSource']==2 for p in public)
        s['user_fee_inputs']=sum(p['FeeInputAmount'] for p in public)
        s['user_change']=sum(p['Change'] for p in public)
        s['user_refund']=sum(p['Refund'] for p in public)
        s['user_fee_actual']=s['fee']['Rewards']+s['fee']['Burned']
        s['organization_FUEL_debit']=0
        if all('FeeLockedMembers' in p for p in audit['Payments']):
            locked={tuple(p['FeeInput']):p for p in audit['Payments'] if not p['Public'] and p['FeeLockedMembers']>0}
            wallet_locked={tuple(p['FeeInput']):p for p in audit['Payments'] if not p['Public'] and p['WalletFeeLocked']}
            s['nonpublic_fee_inputs_locked_by_members']=len(locked)
            s['nonpublic_fee_amount_locked_by_members']=sum(p['FeeInputAmount'] for p in locked.values())
            s['nonpublic_fee_inputs_locked_by_wallets']=len(wallet_locked)
            s['nonpublic_fee_amount_locked_by_wallets']=sum(p['FeeInputAmount'] for p in wallet_locked.values())

        assert s['user_fee_inputs']==s['user_change']+s['user_refund']+s['user_fee_actual']+s['fee']['Held']
        assert s['final_actual_FUEL_balance']==0
        summaries.append(s);curves+=c
    (OUT/'analysis.json').write_text(json.dumps(summaries,ensure_ascii=False,indent=2)+'\n')
    fields=['case','grant','delay_s','offered_units','admitted_units','closed_units','public_payments','not_started','partial_approval_payments','actual_obligations','actual_obligation_fraction','user_fee_actual','organization_FUEL_debit','elapsed_s']
    with (OUT/'summary.csv').open('w',newline='') as f:
        w=csv.DictWriter(f,fieldnames=fields);w.writeheader()
        for s in summaries:w.writerow({k:s[k] for k in fields})
    if curves:
        with (OUT/'curves.csv').open('w',newline='') as f:
            w=csv.DictWriter(f,fieldnames=list(curves[0]));w.writeheader();w.writerows(curves)
    return summaries,curves

def plot(summaries,curves,prefix):
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    from matplotlib import font_manager
    fonts={f.name for f in font_manager.fontManager.ttflist}
    font=next((x for x in ['PingFang SC','Microsoft YaHei','Noto Sans CJK SC'] if x in fonts),'DejaVu Sans')
    plt.rcParams.update({'font.family':font,'font.size':10,'axes.spines.top':False,'axes.spines.right':False,'axes.unicode_minus':False,'pdf.fonttype':42})
    colors=['#D55E00','#0072B2','#009E73'];styles=['--','-',':']
    fig,axes=plt.subplots(1,2,figsize=(9,3.1),layout='constrained')
    for ax,kind,title in zip(axes,[1,3],['CAL 签署权限占用','Execution 工作权限占用']):
        for level,name,color,style in zip(['low','medium','high'],['低','中','高'],colors,styles):
            groups={};case=f'{prefix}k{kind}-{level}'
            for row in curves:
                if row['case']!=case or row['kind']!=kind or not row['node'].startswith('org0-member'):continue
                slot=round(row['seconds']*2)/2;share=(row['grant']*2)//3
                groups[slot]=max(groups.get(slot,0),row['reserved']/share)
            if groups:ax.plot(sorted(groups),[groups[t]*100 for t in sorted(groups)],label=name,color=color,ls=style,lw=1.1)
        ax.axvline(300,color='#777',lw=.8,ls=':');ax.set(title=title,xlabel='时间（秒）',ylabel='四成员中最高占用率（%）',xlim=(0,365),ylim=(0,105));ax.grid(alpha=.18);ax.legend(frameon=False)
    for ext in ['png','pdf']:fig.savefig(OUT/f'owner-budget-turnover.{ext}',dpi=300)
    plt.close(fig)
    fig,axes=plt.subplots(1,2,figsize=(9,3.1),layout='constrained')
    for ax,kind,title in zip(axes,[1,3],['CAL 三档','Execution 三档']):
        ax.plot([0,300],[0,6000],color='#888',ls=':',lw=.8,label='计划单位')
        for level,name,color,style in zip(['low','medium','high'],['低','中','高'],colors,styles):
            path=OUT/f'{prefix}k{kind}-{level}'
            if not (path/'summary.json').exists():continue
            r=base.read(path/'reports/budget-v4.json');start=r['StartedUnixNS']
            times=sorted((max(u['Parent']['MemberClosedUnixNS'],u['Child']['MemberClosedUnixNS'])-start)/1e9 for u in r['Units'] if u['Parent']['MemberClosedUnixNS'] and u['Child']['MemberClosedUnixNS'])
            end=(r['StoppedUnixNS']-start)/1e9
            ax.step([0,*times,end],[0,*range(1,len(times)+1),len(times)],where='post',color=color,ls=style,lw=1.1,label=name)
        ax.set(title=title,xlabel='时间（秒）',ylabel='累计闭环两跳单位',xlim=(0,365),ylim=(0,6300));ax.grid(alpha=.18);ax.legend(frameon=False);ax.axvline(300,color='#777',ls=':',lw=.8)
    for ext in ['png','pdf']:fig.savefig(OUT/f'owner-completed-units.{ext}',dpi=300)
    plt.close(fig)

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--prefix',default='owner-r3-');p.add_argument('--plot',action='store_true');a=p.parse_args()
    s,c=collect(a.prefix)
    if a.plot:plot(s,c,a.prefix)
    for r in s:print(r['case'],r['closed_units'],r['partial_approval_payments'],r['actual_obligations'],r['user_fee_actual'])
