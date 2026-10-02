"""Render archived measurements with the manuscript's EN/ZH terminology."""
from pathlib import Path
import csv
import statistics
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

ROOT = Path(__file__).resolve().parents[1]
delivery = list(csv.DictReader((ROOT / "data/delivery.csv").open(encoding="utf-8")))
throughput = list(csv.DictReader((ROOT / "data/throughput.csv").open(encoding="utf-8")))

for zh in (False, True):
    plt.rcParams.update({"font.family": "Microsoft YaHei" if zh else "serif",
                         "font.serif": ["Times New Roman", "DejaVu Serif"],
                         "font.size": 9, "pdf.fonttype": 42,
                         "axes.spines.top": False, "axes.spines.right": False})
    suffix = "-zh" if zh else ""
    fig, axes = plt.subplots(1, 2, figsize=(7.1, 2.55), layout="constrained")
    for ax, prefix, field, title, ylabel in zip(
        axes, ["v3-200-", "v3-chain-"], ["ready_ms_p50", "chain_ready_ms"],
        ["(a) 每秒 200 笔支付" if zh else "(a) Matched 200 payments/s",
         "(b) 真实连续续花" if zh else "(b) Consecutive spending"],
        ["认证接收 P50（毫秒）" if zh else "Certified-receipt P50 (ms)",
         "100 跳链用时（毫秒）" if zh else "100-hop chain time (ms)"]):
        for x, mode, color in [(0, "A", "#0072B2"), (1, "B", "#D55E00")]:
            values = [float(r[field]) for r in delivery
                      if r["case"].startswith(prefix + mode + "-")]
            assert len(values) == 3
            ax.bar(x, statistics.median(values), color=color, alpha=.82, width=.58)
            ax.scatter([x-.12, x, x+.12], values, color="black", s=13, zorder=3)
        ax.set(xticks=[0, 1], xticklabels=["立即交付", "交付门控"] if zh else
               ["Immediate delivery", "Delivery gate"], title=title, ylabel=ylabel)
        ax.set_axisbelow(True)
        ax.grid(axis="y", alpha=.2)
    for ext in ("pdf", "png"):
        fig.savefig(ROOT / f"figures/delivery-gate{suffix}.{ext}", dpi=200, bbox_inches="tight")
    plt.close(fig)

    fig, axes = plt.subplots(2, 1, figsize=(4.4, 4.3), layout="constrained")
    target = [float(r["target"]) for r in throughput]
    axes[0].plot([2000, 2400], [2000, 2400], color=".6", ls="--", lw=.8,
                 label="目标输入速率" if zh else "Offered rate")
    axes[0].scatter(target, [float(r["completed"]) for r in throughput], color="#0072B2", s=26)
    axes[0].set_ylabel("完成吞吐量（笔/秒）" if zh else "Completion throughput\n(payments/s)")
    axes[0].legend(frameon=False, fontsize=8)
    for field, label, color, marker in [("fast50", "P50", "#0072B2", "o"),
                                       ("fast95", "P95", "#D55E00", "s")]:
        axes[1].scatter(target, [float(r[field]) for r in throughput],
                        label=label, color=color, marker=marker, s=26)
    axes[1].set_ylabel("认证接收延迟（毫秒）" if zh else "Certified-receipt latency (ms)")
    axes[1].legend(frameon=False, fontsize=8)
    for ax in axes:
        ax.set_xlabel("目标输入速率（笔/秒）" if zh else "Target offered load (payments/s)")
        ax.grid(alpha=.2)
        ax.set_axisbelow(True)
    for ext in ("pdf", "png"):
        fig.savefig(ROOT / f"figures/operating-point{suffix}.{ext}", dpi=200, bbox_inches="tight")
    plt.close(fig)

print("Rendered delivery and operating-point figures from frozen CSV measurements.")
