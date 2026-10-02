"""Publication-exportable figures from the independently checked CSV tables."""
import csv
from pathlib import Path
from statistics import median
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

root = Path(__file__).resolve().parent
rows = [r for r in csv.DictReader((root / 'windows.csv').open()) if int(r['start_s']) < 70]
targets = list(csv.DictReader((root / 'targets.csv').open()))
plt.rcParams.update({'font.family': 'DejaVu Serif', 'font.size': 9,
                     'pdf.fonttype': 42, 'ps.fonttype': 42})
colors = {'N': '#35618f', 'R': '#21836d', 'C': '#c16d2a', 'P': '#8e518b'}
fig, axes = plt.subplots(1, 2, figsize=(10.2, 3.8), constrained_layout=True)
ax = axes[0]
for mode in 'NRCP':
    xs = sorted({int(r['start_s']) for r in rows if r['mode'] == mode})
    groups = [[float(r['fast_p95_ms']) for r in rows if r['mode'] == mode and int(r['start_s']) == x] for x in xs]
    centers = [x + 5 for x in xs]
    ax.plot(centers, [median(g) for g in groups], marker='o', markersize=3, label=mode, color=colors[mode])
    ax.fill_between(centers, [min(g) for g in groups], [max(g) for g in groups], color=colors[mode], alpha=.10)
ax.set(xlabel='Seconds after first ordinary send (10-s windows)', ylabel='Ordinary receipt P95 (ms)', title='(a) Ordinary-payment interference')
ax.legend(ncol=4, frameon=False)
ax.grid(axis='y', alpha=.2)
ax = axes[1]
subset = [r for r in targets if r['case'] == 'r1-P']
stages = [('child_ready_s', 'Child receipt', 'o', '#35618f'),
          ('compensation_s', 'Compensation', '^', '#c16d2a'),
          ('repaid_s', 'Repayment', 's', '#21836d'),
          ('materialized_s', 'Materialization', 'D', '#8e518b')]
for r in subset:
    ax.plot([float(r['child_ready_s']), float(r['materialized_s'])], [int(r['pair']) + 1]*2, color='.80', linewidth=1)
for field, label, marker, color in stages:
    ax.scatter([float(r[field]) for r in subset], [int(r['pair']) + 1 for r in subset],
               label=label, marker=marker, color=color, s=20, zorder=3)
ax.axvline(float(subset[0]['resume_s']), color='.35', linestyle='--', linewidth=1, label='Adaptation resumed')
ax.set(xlabel='Seconds after first ordinary send', ylabel='Source–child pair', title='(b) Paused adaptation: run 1', yticks=list(range(1,9)), xlim=(0,70))
ax.legend(fontsize=7, loc='upper center', bbox_to_anchor=(.5, -.22), ncol=3, frameon=False)
ax.grid(axis='x', alpha=.2)
fig.savefig(root / 'mixed-load.pdf')
fig.savefig(root / 'mixed-load.png', dpi=180)
