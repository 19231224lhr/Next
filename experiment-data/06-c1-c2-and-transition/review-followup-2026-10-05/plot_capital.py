"""Plot one-second maxima of observed member CAL occupancy for all repetitions."""
import csv
from collections import defaultdict
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

root=Path(__file__).resolve().parents[3]
src=root/'docs/experiments/current-capital-2026-10-05/final-audit-validated/curves.csv'
series=defaultdict(dict)
with src.open(newline='',encoding='utf-8') as stream:
    for row in csv.DictReader(stream):
        if row['resource_kind']!='1' or not row['member_occupancy_fraction']:
            continue
        t=float(row['seconds'])
        if t<0: continue
        second=int(t)
        d=series[row['case']]
        d[second]=max(d.get(second,0),float(row['member_occupancy_fraction']))
plt.rcParams.update({'font.family':'serif','font.size':8,'pdf.fonttype':42,
                     'axes.spines.top':False,'axes.spines.right':False})
fig, axes=plt.subplots(1,2,figsize=(3.45,2.0),sharey=True,layout='constrained')
for ax,delay in zip(axes,['normal','delay']):
    for budget,color in [('low','#a65b30'),('high','#286d8e')]:
        for rep,style in [(1,'-'),(2,'--'),(3,':')]:
            values=series[f'r{rep}-{budget}-{delay}']
            ax.plot(sorted(values),[values[t] for t in sorted(values)],color=color,
                    linestyle=style,linewidth=.9,alpha=.85,
                    label=('3,600 CAL' if budget=='low' else '28,800 CAL') if rep==1 else None)
    ax.axvline(60,color='#777777',linewidth=.6,linestyle=':')
    ax.set_title('Source delay '+('1 s' if delay=='normal' else '3 s'))
    ax.set_xlim(0,120);ax.set_ylim(0,1.06);ax.set_xticks([0,60,120]);ax.set_yticks([0,.5,1])
    ax.set_xlabel('Elapsed time (s)');ax.grid(alpha=.18)
axes[0].set_ylabel('Member CAL reservation / quota')
axes[1].legend(fontsize=6.5,loc='lower right',frameon=False)
p=root/'docs/paper/review-2026-10-02'
for suffix in ('','-zh'): fig.savefig(p/('capital-current'+suffix+'.pdf'))
fig.savefig(Path(__file__).with_name('capital-current.png'),dpi=200)
