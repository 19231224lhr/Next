#!/usr/bin/env python3
"""Publication figures, each repeat shown separately; no pseudo-replication CI."""
import csv
import gzip
import json
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

OUT=Path(__file__).resolve().parent
FIG=OUT/'figures';FIG.mkdir(exist_ok=True)
rows=json.loads((OUT/'summary.json').read_text())
colors={'A':'#0072B2','B':'#D55E00','direct':'#555555'}
plt.rcParams.update({'font.size':9,'axes.spines.top':False,'axes.spines.right':False,'pdf.fonttype':42,'figure.dpi':130,'savefig.dpi':300,'axes.axisbelow':True})

def save(fig,name):
    fig.tight_layout();fig.savefig(FIG/(name+'.pdf'),bbox_inches='tight');fig.savefig(FIG/(name+'.png'),bbox_inches='tight');plt.close(fig)

fig,axs=plt.subplots(1,2,figsize=(7,2.8))
for ax,metric,label in zip(axs,['ready_ms_p50','ready_ms_p95'],['Wallet READY P50 (ms)','Wallet READY P95 (ms)']):
    for mode in ['A','B']:
        for repeat,marker in [(1,'o'),(2,'s'),(3,'^')]:
            rs=sorted([r for r in rows if r['window']==30 and r['mode']==mode and r['case'].endswith('-'+str(repeat))],key=lambda r:r['rate'])
            if rs:ax.plot([r['rate'] for r in rs],[r[metric] for r in rs],color=colors[mode],marker=marker,alpha=.8,label=f'{mode}, repeat {repeat}')
    ax.set_xticks([200,1000]);ax.set_xlabel('Offered payments/s');ax.set_ylabel(label);ax.set_ylim(bottom=0);ax.grid(axis='y',alpha=.2)
axs[1].legend(fontsize=7,ncol=2)
save(fig,'delivery-latency')

def payments(case):
    path=OUT/case/'payments.jsonl.gz'
    if not path.exists():path=OUT/'evidence'/(case+'-payments.jsonl.gz')
    with gzip.open(path,'rt') as f:return [json.loads(line) for line in f]

fig,ax=plt.subplots(figsize=(6,3))
for r in rows:
    if not r.get('chain'):continue
    ps=payments(r['case']);start=ps[0]['sent_ns']
    ax.plot([p['index'] for p in ps],[(p['ready_ns']-start)/1e6 for p in ps],color=colors[r['mode']],linestyle=['-','--',':'][int(r['case'][-1])-1],label=r['case'].replace('v3-chain-',''),alpha=.85)
ax.set_xlabel('Dependent payment hop');ax.set_ylabel('Cumulative wallet READY (ms)');ax.set_xlim(1,100);ax.set_ylim(bottom=0);ax.grid(alpha=.2);ax.legend(ncol=2,fontsize=8)
save(fig,'chain-cumulative')

fig,axs=plt.subplots(1,2,figsize=(7,2.8))
for ax,rate in zip(axs,[200,1000]):
    for r in rows:
        if r['window']!=30 or r['rate']!=rate or r['mode']!='A':continue
        values=sorted(p['window_ms'] for p in payments(r['case']) if 'window_ms' in p)
        ax.plot(values,[(i+1)/len(values) for i in range(len(values))],label='Repeat '+r['case'][-1])
    ax.set_title(f'{rate} offered payments/s');ax.set_xlabel('Observed extra-gate window after READY (ms)');ax.set_ylabel('CDF');ax.set_ylim(0,1);ax.grid(alpha=.2);ax.legend(fontsize=7)
save(fig,'availability-window')

curves=list(csv.DictReader((OUT/'curves.csv').open()))
fig,axs=plt.subplots(2,2,figsize=(7,4.8))
for row,rate in enumerate([200,1000]):
    for col,metric in enumerate(['public_pending','members_pending']):
        ax=axs[row,col]
        for r in rows:
            if r['window']!=30 or r['rate']!=rate:continue
            cs=[c for c in curves if c['case']==r['case']]
            ax.plot([float(c['second']) for c in cs],[float(c[metric]) for c in cs],color=colors[r['mode']],alpha=.65,linestyle=['-','--',':'][int(r['case'][-1])-1],label=r['mode']+'-'+r['case'][-1])
        ax.axvline(30,color='#555',lw=.8,ls=':');ax.set_title(f'{rate}/s: '+('public pending' if col==0 else 'member completion pending'));ax.set_xlabel('Time since first scheduled send (s)');ax.set_ylabel('Payments');ax.set_ylim(bottom=0);ax.grid(alpha=.2)
axs[0,1].legend(fontsize=6,ncol=3)
save(fig,'backlog')
