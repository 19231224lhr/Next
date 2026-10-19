"""Summarize the fixed four-arm experiments; pilots are intentionally excluded."""
import json
import statistics as s
from pathlib import Path

ROOT = Path(__file__).parent
FILES = ["same-memory.json", "cross-memory.json", "same-paced-1mbps.json",
         "same-paced-10mbps.json", "cross-paced-10mbps.json", "same-durable.json"]
MODES = ["baseline", "continuation", "compact-response", "combined"]
def percentile(xs, q):
    xs = sorted(xs)
    return xs[min(len(xs)-1, int((len(xs)-1)*q))]

out = {}
lines = ["# Four-arm measurement summary", "",
         "Each cell is the median of eight run-level statistics; each run has 100 dependent payments.",
         "All variants within a condition share exactly the same presigned workload. No INSTALL prewarming.",
         "Latency includes collector quorum collection and receiving-wallet verification/commit; transaction construction and sender request saving are outside this timer.",
         "Paced conditions are synthetic independent per-member payload links, not a WAN deployment.", ""]
for filename in FILES:
    path = ROOT / filename
    if not path.exists():
        continue
    rows = json.loads(path.read_text())
    assert len(rows) == 32, (filename, len(rows))
    assert len({r["WorkloadHash"] for r in rows}) == 1, filename
    assert all(len(r["ReadyMS"]) == 100 for r in rows), filename
    baseline = {r["Round"]: s.median(r["ReadyMS"]) for r in rows if r["Mode"] == "baseline"}
    data = {}
    for mode in MODES:
        runs = [r for r in rows if r["Mode"] == mode]
        assert len(runs) == 8
        paired = [100*(1-s.median(r["ReadyMS"])/baseline[r["Round"]]) for r in runs]
        data[mode] = {
            "run_p50_ms": s.median(s.median(r["ReadyMS"]) for r in runs),
            "run_p95_ms": s.median(percentile(r["ReadyMS"], .95) for r in runs),
            "run_mean_ms": s.median(s.mean(r["ReadyMS"]) for r in runs),
            "paired_p50_reduction_percent": s.median(paired),
            "paired_range_percent": [min(paired), max(paired)],
            "faster_rounds": sum(p>0 for p in paired),
            "request_bytes_per_100": s.median(r["Bytes"] for r in runs),
            "response_bytes_per_100": s.median(r["ResponseBytes"] for r in runs),
            "fallbacks": sum(r["Fallbacks"] for r in runs),
            "request_count_range": [min(r["Requests"] for r in runs), max(r["Requests"] for r in runs)],
            "payments": sum(len(r["ReadyMS"]) for r in runs),
        }
    out[filename] = data
    lines += [f"## {filename}", "", "| Mode | p50 ms | p95 ms | Paired p50 reduction | Faster rounds | Request bytes/100 | Response bytes/100 | Fallbacks |",
              "|---|---:|---:|---:|---:|---:|---:|---:|"]
    for mode, r in data.items():
        lines.append(f'| {mode} | {r["run_p50_ms"]:.4f} | {r["run_p95_ms"]:.4f} | {r["paired_p50_reduction_percent"]:.2f}% | {r["faster_rounds"]}/8 | {r["request_bytes_per_100"]:,.0f} | {r["response_bytes_per_100"]:,.0f} | {r["fallbacks"]} |')
    lines.append("")
(ROOT / "summary.json").write_text(json.dumps(out, indent=2), encoding="utf8")
(ROOT / "MEASUREMENTS.md").write_text("\n".join(lines), encoding="utf8")
print(json.dumps(out, indent=2))
