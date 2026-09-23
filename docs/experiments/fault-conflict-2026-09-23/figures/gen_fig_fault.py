#!/usr/bin/env python3
"""Data-only E4 figures: fixed repeat 1 timeline, every formal repeat in dots."""
import csv
import json
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

ROOT=Path(__file__).resolve().parents[1]
OUT=Path(__file__).resolve().parent
COLORS=['#0072B2','#009E73','#D55E00']
NAMES={'A0':'Normal','A1':'One approval response +250 ms','A2':'One member paused'}
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':9,'axes.titlesize':10,
    'axes.spines.top':False,'axes.spines.right':False,'axes.grid':True,'grid.alpha':.16,
    'legend.frameon':False,'legend.fontsize':8,'pdf.fonttype':42,'savefig.dpi':300})

def save(fig,name):
    fig.savefig(OUT/(name+'.png'),dpi=300,bbox_inches='tight')
    fig.savefig(OUT/(name+'.pdf'),bbox_inches='tight');plt.close(fig)

def main():
    data=json.loads((ROOT/'summary.json').read_text())
    formal=[r for r in data['runs'] if r['label'].startswith('v5-A') and '-pilot' not in r['label']]
    assert len(formal)==9, 'Do not publish a partial formal matrix'
    with (ROOT/'curves.csv').open() as f:curves=list(csv.DictReader(f))
    fig,axes=plt.subplots(3,3,figsize=(11,7.8))
    for col,kind in enumerate(['A0','A1','A2']):
        run=next(r for r in formal if r['kind']==kind and r['label'].endswith('-r1'))
        rows=[r for r in curves if r['label']==run['label']]
        xs=[int(r['second']) for r in rows]
        axes[0,col].set_title(NAMES[kind],pad=11)
        for field,color,label in zip(['sent','ready','public'],COLORS,['Sent','Wallet READY','Public observed']):
            values=[int(r[field]) for r in rows]
            axes[0,col].plot(xs[5:],[(values[i]-values[i-5])/5 for i in range(5,len(values))],color=color,label=label,lw=1.2)
        axes[0,col].set_ylim(0,250)
        for field,color,label in zip(['public_pending','active_member_pending','all_member_pending'],COLORS,['Public pending','Active members pending','All members pending']):
            axes[1,col].plot(xs,[int(r[field]) for r in rows],color=color,label=label,lw=1.2)
        axes[2,col].plot(xs,[float(r['ready_p95_ms']) if r['ready_p95_ms'] else float('nan') for r in rows],color=COLORS[0],lw=1.3)
        for ax in axes[:,col]:
            ax.set_xlim(0,max(xs));ax.set_xlabel('Elapsed time (s)')
            if kind!='A0':ax.axvspan(run['fault_start_s'],run['fault_end_s'],color='#E9C46A',alpha=.23,zorder=0)
        axes[0,col].legend(loc='lower right')
        axes[1,col].legend(loc='upper left')
    for i,label in enumerate(['Events/s (trailing 5 s)','Outstanding payments','READY P95 (ms, 10 s cohort)']):axes[i,0].set_ylabel(label)
    fig.suptitle('E4 fault timeline — fixed first repeat, 200 planned payments/s',fontsize=13,y=1.01)
    fig.tight_layout(h_pad=1.7,w_pad=1.4);save(fig,'fault-timeline')

    fig,axes=plt.subplots(1,3,figsize=(10,3.2))
    for col,(metric,title) in enumerate([('ready_p50_ms','Wallet READY P50'),('ready_p95_ms','Wallet READY P95'),('public_p50_ms','Public observation P50')]):
        for k,kind in enumerate(['A0','A1','A2']):
            for j,run in enumerate(sorted([r for r in formal if r['kind']==kind],key=lambda r:r['label'])):
                for phase,mark,offset in [('before','o',-.22),('fault','s',0),('after','^',.22)]:
                    p=next(p for p in run['phases'] if p['phase']==phase)
                    axes[col].scatter(k+offset+(j-1)*.055,p[metric],color=COLORS[k],marker=mark,s=28,alpha=.85,
                        label={'before':'Before','fault':'Fault window','after':'After'}[phase] if k==0 and j==0 else None)
        axes[col].set_xticks(range(3),['A0 normal','A1 response','A2 pause']);axes[col].set_title(title);axes[col].set_ylabel('ms')
        axes[col].set_ylim(bottom=0)
    axes[0].legend(loc='best');fig.suptitle('Each dot is one run and one send-time cohort; no averaged percentiles',y=1.04,fontsize=11)
    fig.tight_layout();save(fig,'phase-comparison')

if __name__=='__main__':main()
