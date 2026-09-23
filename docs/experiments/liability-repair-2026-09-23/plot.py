#!/usr/bin/env python3
"""Reproducible E3 figures; points are independent runs, not confidence bounds."""
import json
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

ROOT=Path(__file__).resolve().parent
OUT=ROOT/'figures';OUT.mkdir(exist_ok=True)
plt.rcParams.update({'font.family':'sans-serif','font.sans-serif':['Microsoft YaHei','Arial','DejaVu Sans'],
    'font.size':9,'axes.titlesize':10,'axes.spines.top':False,'axes.spines.right':False,
    'axes.grid':True,'grid.alpha':.15,'legend.frameon':False,'savefig.bbox':'tight',
    'pdf.fonttype':42,'ps.fonttype':42,'axes.unicode_minus':False})
COLORS=['#0072B2','#009E73','#D55E00']


def save(fig,name):
    fig.savefig(OUT/(name+'.pdf'));fig.savefig(OUT/(name+'.png'),dpi=220);plt.close(fig)


def main():
    matrix=json.loads((ROOT/'matrix-summary.json').read_text())
    if len(matrix)!=9:
        raise ValueError('formal nine-run matrix incomplete')
    fig,axes=plt.subplots(1,3,figsize=(10,3.1),layout='constrained')
    for i,percent in enumerate([0,1,5]):
        group=[s for s in matrix if s['case'].endswith(f'p{percent}')]
        for j,s in enumerate(group):
            x=i+(j-1)*.09
            t=s['timing'];e=s['extra']
            axes[0].scatter(x,t['normal_child_fast_ms']['p50'],color=COLORS[i],marker='o',s=24)
            axes[0].scatter(x,t['normal_child_fast_ms']['p95'],color=COLORS[i],marker='^',s=32)
            if percent:
                axes[1].scatter(x,t['deadline_to_all_physical_observed_ms']['p50']/1000,color=COLORS[i],marker='o',s=24)
                axes[1].scatter(x,t['deadline_to_all_physical_observed_ms']['p95']/1000,color=COLORS[i],marker='^',s=32)
            axes[2].scatter(x,e['cpu_total_s']/s['offered_units'],color=COLORS[i],marker='s',s=26)
    axes[0].set(title='正常子付款：钱包可续花',ylabel='快速到账延迟（ms）')
    axes[0].scatter([],[],color='#333333',marker='o',label='P50');axes[0].scatter([],[],color='#333333',marker='^',label='P95');axes[0].legend()
    axes[1].set(title='期限后：四节点物化完成',ylabel='首次观察延迟（s）')
    axes[1].text(0,.1,'无赔付',ha='center',color='#666666')
    axes[2].set(title='整轮节点 CPU 成本',ylabel='CPU 秒／两跳单位')
    for ax in axes:
        ax.set_xticks([0,1,2],['0%','1%','5%']);ax.set_xlabel('受控父不投递比例');ax.set_ylim(bottom=0)
    save(fig,'mixed-load')
    # A representative completed run is explicitly named, not an averaged
    # synthetic timeline. Each event has its own labelled row.
    case=ROOT/'r1-repair-0'
    report=json.loads((case/'reports/budget-v4.json').read_text());u=report['Units'][0]
    start=u['Parent']['SentUnixNS'];t=u['Repair']
    events=[('父付款 READY',u['Parent']['ReadyUnixNS']),('子付款 READY',u['Child']['ReadyUnixNS']),
            ('子付款上链观察',u['Child']['FinalUnixNS']),('共识锚定期限',t['DeadlineUnix']*1e9),
            ('修复已上链观察',min(n['CommittedUnixNS'] for n in t['Nodes'])),
            ('四节点物化观察',max(n['MaterializedUnixNS'] for n in t['Nodes'])),
            ('迟到父实际投递',u['FirstSubmitUnixNS']),('迟到父上链观察',u['Parent']['FinalUnixNS']),
            ('成员完成观察',u['Parent']['MemberClosedUnixNS'])]
    fig,ax=plt.subplots(figsize=(8,3.5),layout='constrained')
    for i,(label,stamp) in enumerate(events):
        x=(stamp-start)/1e9
        ax.hlines(i,0,x,color='#cccccc',lw=1)
        ax.scatter(x,i,color=COLORS[2 if i>=3 else 0],s=28)
        ax.text(x+.35,i,f'{x:.3f}s',va='center',fontsize=8)
    ax.set_yticks(range(len(events)),[e[0] for e in events]);ax.invert_yaxis()
    ax.set(xlabel='相对父付款发送时间（s）',title='真实单例时间线：r1-repair-0',xlim=(-.5,36))
    save(fig,'repair-timeline')


if __name__=='__main__':
    main()
