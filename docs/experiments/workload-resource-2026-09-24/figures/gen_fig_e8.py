#!/usr/bin/env python3
"""Plot recorded E8 run-level results; no pooled-payment confidence intervals."""
import csv,json,statistics
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

OUT=Path(__file__).resolve().parents[1]
FIG=OUT/'figures'
COLORS={'A':'#0072B2','B':'#D55E00','C':'#009E73'}
STYLES=['-','--',':']
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':9,'axes.spines.top':False,'axes.spines.right':False,'axes.grid':True,'grid.alpha':.18,'legend.frameon':False,'pdf.fonttype':42,'savefig.bbox':'tight'})

def save(fig,name):
    fig.savefig(FIG/(name+'.pdf'))
    fig.savefig(FIG/(name+'.png'),dpi=240)
    plt.close(fig)

def run_points(ax,rows,key,ylabel):
    for i,mode in enumerate('ABC'):
        ys=[r[key] for r in rows if r['mode']==mode]
        ax.scatter([i-.08,i,i+.08],ys,color=COLORS[mode],s=24,zorder=3)
        ax.hlines(statistics.median(ys),i-.22,i+.22,color=COLORS[mode],lw=2)
    ax.set_xticks(range(3),['A: 2 identities','B: 2048 identities','C: 10-hop chains'],rotation=12)
    ax.set_ylabel(ylabel);ax.set_ylim(bottom=0)

def main():
    rows=list(csv.DictReader((OUT/'per-run.csv').open()))
    for r in rows:
        for k in r:
            if k not in ['case','mode']:r[k]=float(r[k])
    fig,axes=plt.subplots(2,2,figsize=(8,5.7),layout='constrained')
    for ax,key,label in zip(axes.flat,['fast_p50_ms','fast_p95_ms','fast_p99_ms','closed_p95_ms'],['Fast receipt P50 (ms)','Fast receipt P95 (ms)','Fast receipt P99 (ms)','Closure observation P95 (ms)']):run_points(ax,rows,key,label)
    save(fig,'e8-latency')
    fig,axes=plt.subplots(1,3,figsize=(10,3),layout='constrained')
    run_points(axes[0],rows,'service_core_ms_per_completion','9 services: core-ms / completion')
    for r in rows:r['payload_kib']=r['http_payload_bytes_per_ready']/1024;r['history_kib']=r['logical_service_bytes_per_payment']/1024
    run_points(axes[1],rows,'payload_kib','HTTP payload (KiB / payment)')
    run_points(axes[2],rows,'history_kib','Retained logical KV growth (KiB / payment)')
    save(fig,'e8-resources')
    fig,axes=plt.subplots(3,1,figsize=(8,6),sharex=True,layout='constrained')
    for ax,mode in zip(axes,'ABC'):
        for repeat in range(1,4):
            data=list(csv.DictReader((OUT/f'formal-{mode.lower()}{repeat}'/'timeline.csv').open()))
            ax.plot([int(x['Second']) for x in data],[int(x['Pending']) for x in data],STYLES[repeat-1],color=COLORS[mode],lw=1,label=f'Run {repeat}')
        ax.axvline(300,color='#777',ls=':',lw=.8);ax.set_ylabel(f'{mode}: pending');ax.set_ylim(bottom=0);ax.legend(ncol=3,loc='upper left')
    axes[-1].set_xlabel('Seconds from formal-window start')
    save(fig,'e8-pending')
    bursts=[OUT/f'burst-c{i}' for i in range(1,4)]
    if all((p/'timeline.csv').exists() for p in bursts):
        fig,axes=plt.subplots(2,1,figsize=(8,4.8),sharex=True,layout='constrained')
        for i,p in enumerate(bursts):
            data=list(csv.DictReader((p/'timeline.csv').open()));xs=[int(x['Second']) for x in data]
            axes[0].plot(xs,[int(x['Pending']) for x in data],STYLES[i],color=COLORS['C'],lw=1,label=f'Run {i+1}')
            width=5
            axes[1].plot(xs[width:],[(int(data[j]['Sent'])-int(data[j-width]['Sent']))/width for j in range(width,len(data))],STYLES[i],color=COLORS['C'],lw=1)
        for ax in axes:ax.axvspan(60,75,color='#E69F00',alpha=.18);ax.set_ylim(bottom=0)
        axes[0].set_ylabel('Pending closure');axes[0].legend(ncol=3)
        axes[1].set_ylabel('Actual sends / s (5 s bins)');axes[1].set_xlabel('Seconds from formal-window start')
        save(fig,'e8-burst')
if __name__=='__main__':main()
