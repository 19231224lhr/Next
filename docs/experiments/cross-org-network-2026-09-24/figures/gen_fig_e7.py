#!/usr/bin/env python3
"""Publication figures from all 24 audited E7 repeats; no invented points."""
import json,sys
from pathlib import Path
import numpy as np
import matplotlib.pyplot as plt

ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT))

cases=sorted(ROOT.glob('formal-r*'))
assert len(cases)==24, f'expected 24 formal runs, found {len(cases)}'
assert all((p/'passed.json').exists() for p in cases), 'unaudited formal run'
rows=json.loads((ROOT/'summary-all.json').read_text())
assert {r['case'] for r in rows} == {p.name for p in cases}, 'summary/run mismatch'
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':9,'axes.spines.top':False,'axes.spines.right':False,'legend.frameon':False,'pdf.fonttype':42,'ps.fonttype':42,'figure.dpi':150,'savefig.dpi':300})
colors=['#0072B2','#D55E00'];labels={'C':'Same organization, 10 hops','D':'Cross organization, 10 hops'}
fig,axes=plt.subplots(1,2,figsize=(8,3),layout='constrained')
for mode,color in zip('CD',colors):
    for ax,metric,title in zip(axes,['FastNS','PublicNS'],['Receiver ACK round trip','Public confirmation observation']):
        points=[]
        for rtt in [0,20,100]:
            vals=[r['first120']['ms'][metric]['50'] for r in rows if r['config']['mode']==mode and r['config']['rtt']==rtt]
            assert len(vals)==3
            points.append(np.mean(vals));ax.scatter([rtt]*3,vals,s=16,color=color,alpha=.55)
        ax.plot([0,20,100],points,'o-',color=color,label=labels[mode],markersize=4)
        ax.set(title=title,xlabel='Injected cross-site HTTP RTT (ms)',ylabel='Per-run median (ms)',xticks=[0,20,100]);ax.grid(alpha=.2)
axes[0].legend(fontsize=7)
fig.savefig(ROOT/'figures/network-latency.pdf');fig.savefig(ROOT/'figures/network-latency.png');plt.close(fig)

fig,ax=plt.subplots(figsize=(6,3),layout='constrained')
for i,mode in enumerate('ABCD'):
    vals=[r['first120']['ms']['FastNS']['50'] for r in rows if r['config']['mode']==mode and r['config']['rtt']==0]
    ax.bar(i,np.mean(vals),width=.6,color='#56B4E9',alpha=.65)
    ax.scatter(np.array([i-.12,i,i+.12]),vals,color='#264653',s=20)
ax.set(xticks=range(4),xticklabels=['Same / final','Cross / final','Same / chain','Cross / chain'],ylabel='Receiver ACK round trip (ms)',title='Zero injected delay: three independent runs per case',ylim=(0,None));ax.grid(axis='y',alpha=.2)
fig.savefig(ROOT/'figures/local-comparison.pdf');fig.savefig(ROOT/'figures/local-comparison.png');plt.close(fig)

fig,axes=plt.subplots(1,3,figsize=(10,3),layout='constrained')
for r in rows:
    if r['config']['mode']!='D' or r['config']['rtt']!=100:continue
    p=ROOT/r['case'];samples=[json.loads(line) for line in (p/'resources.jsonl').read_text().splitlines()];samples=[x for x in samples if x['phase']=='formal'];start=samples[0]['NS']
    x=[(s['NS']-start)/1e9 for s in samples]
    axes[0].plot(x,[sum(v['Pending'] for v in s['status']) for s in samples],label=r['case'].split('-')[1])
    axes[1].plot(x,[max(v['OldestPendingNS'] for v in s['status'])/1e9 for s in samples])
    ws=[w for w in r['completion_windows'] if w['start']<300]
    axes[2].plot([w['start'] for w in ws],[w['public_tps'] for w in ws])
axes[0].set(xlabel='Elapsed time (s)',ylabel='Outstanding payments',title='Cross / 100 ms: background tasks');axes[0].legend()
axes[1].set(xlabel='Elapsed time (s)',ylabel='Oldest outstanding age (s)',title='Cross / 100 ms: oldest task',ylim=(0,None))
axes[2].set(xlabel='10 s completion-window start (s)',ylabel='Observed completions / s',title='Cross / 100 ms: public completions',ylim=(0,115));axes[2].axhline(100,ls='--',color='gray',lw=1,label='Actual send rate: 100/s');axes[2].legend(fontsize=7)
for ax in axes:ax.grid(alpha=.2)
fig.savefig(ROOT/'figures/sustained.pdf');fig.savefig(ROOT/'figures/sustained.png');plt.close(fig)
