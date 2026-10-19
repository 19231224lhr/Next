"""Plot the three current arms from audited run totals, never old measurements."""
from pathlib import Path
import json
import statistics
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

root = Path(__file__).resolve().parents[3]
data = json.loads((root / 'docs/experiments/wait-baseline-2026-10-05/results/member-gate-analysis.json').read_text())
out = root / 'docs/paper/review-2026-10-02'
plt.rcParams.update({'font.family': 'serif', 'font.size': 9,
                     'pdf.fonttype': 42, 'axes.spines.top': False,
                     'axes.spines.right': False})
fig, ax = plt.subplots(figsize=(3.45, 1.75), layout='constrained')
colors = ['#286d8e', '#777777', '#ac6135']
for i, mode in enumerate(('fast', 'wallet-wait', 'member-wait')):
    values = [r['chain_ms'] / 1000 for r in data['runs'] if r['case'].endswith('-' + mode)]
    med = statistics.median(values)
    ax.barh(i, med, height=.50, color=colors[i], alpha=.30)
    ax.scatter(values, [i-.13, i, i+.13], s=17, color=colors[i], zorder=3)
    ax.text(med + 1, i, f'{med:.2f}', va='center', fontsize=8)
ax.set_yticks(range(3), ['F: certified', 'W: public', 'M: member-ready'])
ax.invert_yaxis()
ax.set_xlim(0, 73)
ax.set_xticks([0, 20, 40, 60])
ax.set_xlabel('100-hop chain time (s)')
ax.grid(axis='x', alpha=.2)
ax.set_axisbelow(True)
for suffix in ('', '-zh'):
    fig.savefig(out / ('continuation-current' + suffix + '.pdf'))
fig.savefig(Path(__file__).with_name('continuation-current.png'), dpi=180)
