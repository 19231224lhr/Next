from pathlib import Path
import csv, statistics
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
root=Path(__file__).resolve().parents[1]
rows=list(csv.DictReader((root.parents[1]/'experiments/final-continuation-2026-10-02/results/cases.csv').open()))
plt.rcParams.update({'font.family':'serif','font.serif':['Times New Roman','DejaVu Serif'],'font.size':9,'pdf.fonttype':42,'axes.spines.top':False,'axes.spines.right':False})
for zh in [False,True]:
 if zh: plt.rcParams.update({'font.family':'sans-serif','font.sans-serif':['Microsoft YaHei'],'axes.unicode_minus':False})
 fig,ax=plt.subplots(figsize=(3.5,2.2),layout='constrained')
 for i,mode in enumerate(['fast','wait_final']):
  vals=[float(r['fast_chain_ms'])/1000 for r in rows if r['mode']==mode]
  col=['#0072B2','#D55E00'][i]
  ax.scatter([i-.06,i,i+.06],vals,color=col,s=24,zorder=3)
  med=statistics.median(vals);ax.hlines(med,i-.2,i+.2,color=col,lw=2)
  ax.text(i,med+5,f'{med:.3f} s',ha='center',color=col)
 ax.set_xticks([0,1],['凭证续花','逐跳等待公共确认'] if zh else ['Certified continuation','Inter-hop public wait'])
 ax.set_ylabel('100 跳链耗时（秒）' if zh else '100-hop chain time (s)')
 ax.set_ylim(0,77);ax.set_xlim(-.55,1.55);ax.grid(axis='y',alpha=.2)
 name='continuation-final'+('-zh' if zh else '')
 fig.savefig(root/'figures'/f'{name}.pdf',bbox_inches='tight')
 fig.savefig(root/'figures'/f'{name}.png',dpi=200,bbox_inches='tight')
 plt.close(fig)
