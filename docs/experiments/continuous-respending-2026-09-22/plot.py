#!/usr/bin/env python3
"""Render all three formal repetitions; no selection of the best run."""
import csv
from pathlib import Path
from statistics import median
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

ROOT=Path(__file__).resolve().parent
cases=[r for r in csv.DictReader((ROOT/'cases.csv').open()) if r['case'].startswith('v3-') and 'diagnostic' not in r['case']]
hops=[r for r in csv.DictReader((ROOT/'hops.csv').open()) if r['case'].startswith('v3-') and 'diagnostic' not in r['case']]
assert len(cases)==18
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':9,'axes.spines.top':False,'axes.spines.right':False,'legend.frameon':False,'savefig.bbox':'tight','pdf.fonttype':42})
colors={'fast':'#0072B2','wait_final':'#D55E00'}
names={'fast':'Spend on fast receipt','wait_final':'Wait for public confirmation'}
fig,axes=plt.subplots(1,2,figsize=(8,3.2),layout='constrained')
for ax,metric,title in zip(axes,['fast_chain_ms','public_chain_ms'],['Last wallet fast receipt','Last wallet public confirmation']):
    for mode in colors:
        ys=[]
        for length in [1,10,100]:
            vals=[float(r[metric]) for r in cases if r['mode']==mode and int(r['length'])==length]
            assert len(vals)==3
            ys.append(median(vals))
            ax.scatter([length]*3,vals,s=20,alpha=.45,color=colors[mode],marker='o' if mode=='fast' else 's')
        ax.plot([1,10,100],ys,color=colors[mode],label=names[mode],marker='o' if mode=='fast' else 's')
    ax.set(xscale='log',yscale='log',xlabel='Dependent payments',ylabel='Elapsed time (ms)',title=title)
    ax.set_xticks([1,10,100],['1','10','100']);ax.grid(alpha=.2)
axes[0].legend(fontsize=7.5)
for extension in ['pdf','png']:fig.savefig(ROOT/f'chain-length.{extension}',dpi=200)
plt.close(fig)
fig,axes=plt.subplots(1,2,figsize=(8,3.2),layout='constrained')
for ax,metric,title in zip(axes,['fast_ms','respending_gap_ms'],['Per-hop fast receipt latency','Gap before sending next payment']):
    for mode in colors:
        relevant=[r for r in hops if r['mode']==mode and int(r['length'])==100 and r[metric]!='']
        for case in sorted({r['case'] for r in relevant}):
            rr=[r for r in relevant if r['case']==case]
            ax.plot([int(r['hop']) for r in rr],[float(r[metric]) for r in rr],color=colors[mode],alpha=.22,linewidth=.8)
        indices=sorted({int(r['hop']) for r in relevant})
        ax.plot(indices,[median(float(r[metric]) for r in relevant if int(r['hop'])==i) for i in indices],color=colors[mode],linewidth=1.4,label=names[mode])
    ax.set(yscale='log',xlabel='Hop index',ylabel='Time (ms)',title=title);ax.grid(alpha=.2)
handles,labels=axes[0].get_legend_handles_labels()
fig.legend(handles,labels,loc='lower center',bbox_to_anchor=(.5,-.1),ncol=2,fontsize=8)
for extension in ['pdf','png']:fig.savefig(ROOT/f'per-hop.{extension}',dpi=200)
