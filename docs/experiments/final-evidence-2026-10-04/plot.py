"""Current-build paper figures; all dots/bands represent the three actual runs."""
import csv
import json
from pathlib import Path
from statistics import median
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

root = Path(__file__).resolve().parent
out = root.parents[1] / 'paper' / 'review-2026-10-02' / 'figures'
chain = json.loads((root / 'results/summary.json').read_text())
windows = list(csv.DictReader((root / 'results-mixed/windows.csv').open()))
targets = list(csv.DictReader((root / 'results-mixed/targets.csv').open()))
plt.rcParams.update({'font.family': 'serif', 'font.serif': ['Times New Roman'],
    'font.size': 9, 'axes.labelsize': 9, 'legend.fontsize': 8,
    'pdf.fonttype': 42, 'ps.fonttype': 42, 'axes.spines.top': False,
    'axes.spines.right': False, 'savefig.bbox': 'tight'})
colors = ['#0072B2', '#D55E00', '#009E73', '#CC79A7']

for zh in [False, True]:
    if zh:
        plt.rcParams.update({'font.family': 'sans-serif', 'font.sans-serif': ['Microsoft YaHei'], 'axes.unicode_minus': False})
    suffix = '-zh' if zh else ''
    fig, ax = plt.subplots(figsize=(3.5, 2.15), layout='constrained')
    for i, mode in enumerate(['fast', 'wait_final']):
        vals = [r['fast_chain_ms'] / 1000 for r in chain['runs'] if r['mode'] == mode]
        ax.scatter([i-.055, i, i+.055], vals, s=27, color=colors[i], zorder=3)
        ax.hlines(median(vals), i-.21, i+.21, color=colors[i], lw=1.8)
        ax.text(i, median(vals)*1.35, f'{median(vals):.3f} s', ha='center')
    ax.set_yscale('log'); ax.set_ylim(3, 110); ax.set_xlim(-.55, 1.55)
    ax.set_xticks([0, 1], ['凭证续花', '逐跳等待'] if zh else ['Certified continuation', 'Wait between hops'])
    ax.set_ylabel('100 跳链用时（秒，对数轴）' if zh else '100-hop chain time (s, log)')
    ax.grid(axis='y', alpha=.2)
    fig.savefig(out / f'continuation-final{suffix}.pdf')
    fig.savefig(root / f'continuation-final{suffix}.png', dpi=180); plt.close(fig)

    fig, axes = plt.subplots(1, 2, figsize=(7.1, 2.45), layout='constrained')
    xs = sorted({int(r['start_s']) for r in windows if int(r['start_s']) < 70})
    vals = [[float(r['fast_p95_ms']) for r in windows if int(r['start_s']) == x] for x in xs]
    axes[0].plot(xs, [median(v) for v in vals], marker='o', ms=3, color=colors[0])
    axes[0].fill_between(xs, [min(v) for v in vals], [max(v) for v in vals], alpha=.16, color=colors[0])
    axes[0].set_xlabel('10 秒窗口起点（秒）' if zh else 'Start of 10-s window (s)')
    axes[0].set_ylabel('普通付款到账 P95（ms）' if zh else 'Ordinary receipt P95 (ms)')
    axes[0].grid(alpha=.2); axes[0].set_ylim(bottom=0)
    ts = [r for r in targets if r['case'] == 'r1-P']
    labels = ['后继公共观察', '赔付观察', '回款观察', '物化观察'] if zh else ['Successor public', 'Compensation', 'Repayment', 'Materialized']
    for k, label, color in zip(['child_public_s', 'compensation_s', 'repaid_s', 'materialized_s'], labels, colors):
        axes[1].scatter([float(r[k]) for r in ts], [int(r['pair'])+1 for r in ts], label=label, s=16, color=color)
    axes[1].axvline(float(ts[0]['resume_s']), color='#555555', ls='--', lw=.9, label='恢复适配' if zh else 'Adaptation resumes')
    axes[1].set_xlabel('自首次普通付款发送起（秒）' if zh else 'Time from first ordinary send (s)')
    axes[1].set_ylabel('目标编号' if zh else 'Target pair'); axes[1].set_yticks([1, 4, 8])
    axes[1].legend(fontsize=7, loc='lower center', bbox_to_anchor=(.5, 1.0), ncol=3, frameon=False)
    axes[1].grid(alpha=.15)
    fig.savefig(out / f'mixed-load{suffix}.pdf')
    fig.savefig(root / f'mixed-load{suffix}.png', dpi=180); plt.close(fig)
print('Generated four current-build vector figures.')
