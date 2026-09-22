#!/usr/bin/env python3
"""Plot completed paired E2 runs; never infer completion from live samples."""
import argparse
import json
from pathlib import Path
from analyze import OUT, read


def main(prefix):
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    from matplotlib import font_manager
    fonts={f.name for f in font_manager.fontManager.ttflist}
    font=next((f for f in ['Microsoft YaHei','PingFang SC','Noto Sans CJK SC'] if f in fonts),'DejaVu Sans')
    plt.rcParams.update({'font.family':font,'font.size':10,'axes.spines.top':False,'axes.spines.right':False,'pdf.fonttype':42,'axes.unicode_minus':False})
    cases=['cal-low','work-low','fuel-low','cal-medium','cal-delay3']
    labels=['CAL 低档','工作权限低档','FUEL 低档','CAL 中档 · 1 秒','CAL 中档 · 3 秒']
    modes=['parallel','serial'];colors=['#496CAD','#2A9D8F'];names=['并行取证','单入口顺序取证']
    data={}
    for case in cases:
        for mode in modes:
            path=OUT/(prefix+case+'-'+mode)/'analysis.json'
            if not path.exists():
                raise SystemExit('Complete and analyze all pairs first: '+str(path))
            data[case,mode]=json.loads(path.read_text())
    fig,axes=plt.subplots(1,2,figsize=(12,3.8),layout='constrained')
    for ax,key,title in [(axes[0],'closed_units','完整两跳单位（计划每组 6,000）'),(axes[1],'partial_approval_payments','结束时未成证的部分批准交易')]:
        for i,(mode,color,name) in enumerate(zip(modes,colors,names)):
            values=[data[c,mode][key] for c in cases]
            bars=ax.bar([j+(i-.5)*.36 for j in range(len(cases))],values,width=.34,color=color,label=name)
            ax.bar_label(bars,padding=3,fontsize=8)
        ax.set_xticks(range(len(labels)),labels,rotation=15,ha='right')
        ax.set_title(title);ax.set_ylim(0,max([data[c,m][key] for c in cases for m in modes])*1.18+1)
        ax.grid(axis='y',alpha=.18);ax.set_axisbelow(True);ax.legend(frameon=False)
    for ext in ['png','pdf']:fig.savefig(OUT/f'admission-comparison.{ext}',dpi=220)
    plt.close(fig)
    fig,axes=plt.subplots(1,3,figsize=(12,3.4),layout='constrained')
    for ax,case,title in zip(axes,cases[:3],labels[:3]):
        for mode,color,name in zip(modes,colors,names):
            report=read(OUT/(prefix+case+'-'+mode)/'reports/budget-v4.json')
            start=report['StartedUnixNS']
            times=sorted((max(u['Parent']['MemberClosedUnixNS'],u['Child']['MemberClosedUnixNS'])-start)/1e9 for u in report['Units'] if u['Parent']['MemberClosedUnixNS'] and u['Child']['MemberClosedUnixNS'])
            end=(report['StoppedUnixNS']-start)/1e9
            ax.step([0,*times,end],[0,*range(1,len(times)+1),len(times)],where='post',label=name,color=color,lw=1.2)
        ax.axvline(300,color='#999',lw=.8,ls=':');ax.set(title=title,xlabel='时间（秒）',ylabel='累计闭环两跳单位',xlim=(0,365));ax.grid(alpha=.18);ax.legend(frameon=False)
    for ext in ['png','pdf']:fig.savefig(OUT/f'admission-progress.{ext}',dpi=220)
    plt.close(fig)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--prefix',default='gate-r1-');args=p.parse_args()
    main(args.prefix)
