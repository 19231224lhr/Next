"""Plot the frozen C3 observations; no fitted or inferred event timestamps."""
from pathlib import Path
import csv
import hashlib
import json
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT.parents[1] / "experiments/c3-source-repair-2026-10-02"
rows = list(csv.DictReader((SOURCE / "functional.csv").open(encoding="utf-8")))
def plot(chinese=False):
    stem = "c3-resolution-zh" if chinese else "c3-resolution"
    plt.rcParams.update({"font.family": "Microsoft YaHei" if chinese else "serif", "font.serif": ["Times New Roman", "DejaVu Serif"],
                         "font.size": 9, "pdf.fonttype": 42, "ps.fonttype": 42,
                         "axes.spines.top": False, "axes.spines.right": False})
    fig, axes = plt.subplots(1, 2, figsize=(7.1, 3.05), layout="constrained",
                             gridspec_kw={"width_ratios": [1, 1.45]})
    blue, green, orange = "#0072B2", "#009E73", "#D55E00"
    ax = axes[0]
    for r in [r for r in rows if r["mode"] == "recovery"]:
        y = 4 - int(r["run"])
        child = float(r["child_block_observed_ms"]) / 1000
        source = float(r["autonomous_source_observed_ms"]) / 1000
        ax.plot([child, source], [y, y], color="0.65", lw=1.2)
        ax.scatter(child, y, color=blue, s=27, marker="o", label=('子付款公共成立' if chinese else 'Child public') if y == 3 else None)
        ax.scatter(source, y, color=green, s=32, marker="D", label=('来源公共成立' if chinese else 'Source executed') if y == 3 else None)
    ax.set(yticks=[3, 2, 1], yticklabels=[f"第 {i} 轮" if chinese else f"Run {i}" for i in (1,2,3)], ylim=(.5, 3.5),
           xlim=(0, 3), xlabel='自子付款发送起的时间（秒）' if chinese else 'Time from child send (s)', title='(a) 公开证据驱动的补交' if chinese else '(a) Evidence-driven resubmission')
    ax.legend(loc="lower left", bbox_to_anchor=(-.05, -0.5), frameon=False, fontsize=8)

    ax.grid(axis="x", alpha=.2)
    ax = axes[1]
    cases = [r for r in rows if r["mode"] != "recovery"]
    for i, r in enumerate(cases):
        y = 6-i
        paid = float(r["compensation_observed_ms"]) / 1000
        physical = float(r["physical_repair_observed_ms"]) / 1000
        ax.plot([paid, physical], [y, y], color="0.65", lw=1.2)
        ax.scatter(paid, y, color=orange, marker="s", s=29, label=('赔付决定' if chinese else 'Compensation') if i == 0 else None)
        ax.scatter(physical, y, color=green, marker="^", s=34, label=('历史表示物化' if chinese else 'Physical installation') if i == 0 else None)
    ax.axhline(3.5, color=".7", ls="--", lw=.7)
    ax.set(yticks=list(range(6, 0, -1)), yticklabels=[f"{('对照' if r['mode']=='control' else '暂停')} {r['run']}" if chinese else f"{('Control' if r['mode']=='control' else 'Paused')} {r['run']}" for r in cases],
           ylim=(.5, 6.5), xlim=(34.5, 44), xlabel='自子付款发送起的时间（秒）' if chinese else 'Time from child send (s)',
           title='(b) 赔付与表示修复' if chinese else '(b) Compensation and representation')
    ax.legend(loc="lower left", bbox_to_anchor=(-.05, -0.5), frameon=False, fontsize=8, ncol=2,
              columnspacing=1, handletextpad=.4)

    ax.grid(axis="x", alpha=.2)
    fig.savefig(ROOT / f"figures/{stem}.pdf", bbox_inches="tight")
    fig.savefig(ROOT / f"figures/{stem}.png", dpi=200, bbox_inches="tight")
    plt.close(fig)

plot()
plot(chinese=True)

manifest = {"source": "../../../experiments/c3-source-repair-2026-10-02/functional.csv",
            "sha256": hashlib.sha256((SOURCE / "functional.csv").read_bytes()).hexdigest(),
            "endpoint": "client observations from actual child-wallet HTTP send; polling included",
            "repayment_resume": "Order verified by paused-observations.json; no precise timestamp inferred.",
            "rows": rows}
(ROOT / "data/c3-figure-data.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
print("Generated C3 observation figure from 9 archived runs.")
