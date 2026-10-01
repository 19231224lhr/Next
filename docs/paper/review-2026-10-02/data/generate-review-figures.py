from pathlib import Path
import json,statistics
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np
R=Path(__file__).resolve().parents[1]
D=R/'data'
summary=json.loads((D/'review-summary.json').read_text())
plt.rcParams.update({'font.family':'serif','font.serif':['Times New Roman','DejaVu Serif'],'font.size':9,'axes.labelsize':9,'axes.titlesize':9,'legend.fontsize':8,'pdf.fonttype':42,'ps.fonttype':42,'axes.spines.top':False,'axes.spines.right':False,'savefig.bbox':'tight'})
blue,orange,green='#0072B2','#D55E00','#009E73'
fig,ax=plt.subplots(figsize=(3.5,2.25),layout='constrained')
for i,mode in enumerate(['fast','wait_final']):
 vals=[x['FastChainMS'] for x in summary['chains_memory'] if x['Mode']==mode]
 med=statistics.median(vals);color=[blue,orange][i]
 ax.scatter(np.array([i-.055,i,i+.055]),vals,s=28,c=color,marker='o',zorder=4)
 ax.hlines(med,i-.22,i+.22,color=color,lw=2)
 ax.text(i,med*1.7,f'{med:,.2f} ms',ha='center',fontsize=9,color=color)
ax.set_yscale('log');ax.set_ylim(70,200000);ax.set_xticks([0,1],['Certified continuation','Wait between hops']);ax.set_ylabel('100-hop chain time (ms, log)');ax.grid(axis='y',alpha=.2);ax.set_xlim(-.6,1.6)
fig.savefig(R/'figures/continuation-current.pdf');fig.savefig(R/'figures/continuation-current.png',dpi=250);plt.close(fig)
series=json.loads((D/'reserve-series.json').read_text())
fig,axes=plt.subplots(3,1,figsize=(3.5,3.3),sharex=True,sharey=True,layout='constrained')
for ax,(name,rows),color in zip(axes,sorted(series.items()),[blue,orange,green]):
 start=rows[0]['snapshot']['UnixNS'];xs=[(x['snapshot']['UnixNS']-start)/1e9 for x in rows];ys=[x['snapshot']['CAL'] for x in rows]
 ax.step(xs,np.array(ys)/1000,where='post',color=color,lw=1.2,label='Seed '+name.split('-')[0][1:]);ax.axhline(60,color='#888888',ls='--',lw=.7);ax.set_ylim(59.5,60.08);ax.set_yticks([59.6,60]);ax.legend(loc='lower left');ax.grid(alpha=.15)
axes[1].set_ylabel('Issuer reserve (thousands of CAL)');axes[-1].set_xlabel('Elapsed observation time (s)')
fig.savefig(R/'figures/recovery-reserve.pdf');fig.savefig(R/'figures/recovery-reserve.png',dpi=250);plt.close(fig)
# Keep plot inputs beside the generator; no private keys or full node state.
(R/'data/review-summary.json').write_text(json.dumps(summary,indent=2))
(R/'data/reserve-series.json').write_text(json.dumps(series))
print('Generated two PDF/PNG figures from archived samples.')
