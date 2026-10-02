"""Publication figures from run-level summaries; whiskers are min--max."""
import csv
from pathlib import Path
import statistics as st
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np

root=Path(__file__).resolve().parent
reads=list(csv.DictReader((root/'reads.csv').open()))
costs=list(csv.DictReader((root/'costs.csv').open()))
configs=[(32,1),(32,8),(256,1),(256,8)]
colors=['#335C81','#C97740']
plt.rcParams.update({'font.family':'DejaVu Sans','font.size':9,'axes.spines.top':False,
 'axes.spines.right':False,'pdf.fonttype':42,'ps.fonttype':42})
fig,axs=plt.subplots(2,2,figsize=(9,6),layout='constrained')
x=np.arange(4)
def subset(rows,n,m):return [r for r in rows if int(r['payments'])==n and int(r['repairs'])==m]
for ax,work,title in [(axs[0,0],'point_repaired','(a) One compensated input'),(axs[0,1],'block','(b) Whole-block provenance')]:
 for k,method in enumerate(['index','canonical']):
  vals=[[float(r['total_p50_us'])/1000 for r in subset(reads,n,m) if r['method']==method and r['workload']==work] for n,m in configs]
  med=np.array([st.median(v) for v in vals]);lo=np.array([min(v) for v in vals]);hi=np.array([max(v) for v in vals])
  ax.bar(x+(k-.5)*.35,med,.35,yerr=[med-lo,hi-med],capsize=3,color=colors[k],label=['Decision index','Canonical history'][k])
 ax.set_title(title,loc='left',fontweight='bold');ax.set_ylabel('Query latency (ms)');ax.grid(axis='y',alpha=.18);ax.set_axisbelow(True)
axs[0,0].legend(frameon=False,fontsize=8)
ax=axs[1,0]
layers=[('revision_body_record_bytes','Revision'),('batch_task_record_bytes','Batch task'),('blockstore_delta_bytes','BlockStore increment')]
bottom=np.zeros(4)
for (field,label),color in zip(layers,['#335C81','#81A4BB','#C97740']):
 vals=np.array([st.mean(float(r[field]) for r in subset(costs,n,m))/1048576 for n,m in configs])
 ax.bar(x,vals,.65,bottom=bottom,label=label,color=color);bottom+=vals
ax.set_title('(c) Principal representation records',loc='left',fontweight='bold');ax.set_ylabel('Logical encoded records (MiB)');ax.legend(frameon=False,fontsize=7.5)
ax=axs[1,1]
bottom=np.zeros(4)
layers=[(['input_adapt_ms'],'Input adaptation'),(['batch_build_ms','parts_adapt_ms'],'Batch / part adaptation'),(['representation_finalize_ms','representation_commit_ms'],'Application / commit'),(['materialization_prepare_ms','materialization_install_ms'],'Materialization')]
for (fields,label),color in zip(layers,['#335C81','#81A4BB','#C97740','#DCAF88']):
 vals=np.array([st.mean(sum(float(r[f]) for f in fields) for r in subset(costs,n,m)) for n,m in configs])
 ax.bar(x,vals,.65,bottom=bottom,label=label,color=color);bottom+=vals
ax.set_title('(d) One-time local maintenance work',loc='left',fontweight='bold');ax.set_ylabel('Time (ms; mean of three runs)');ax.legend(frameon=False,fontsize=7)
for ax in axs.flat:
 ax.set_xticks(x,[f'{n} / {m}' for n,m in configs]);ax.set_xlabel('Payments per block / repaired inputs')
fig.savefig(root/'reader-cost.png',dpi=180)
fig.savefig(root/'reader-cost.pdf')
