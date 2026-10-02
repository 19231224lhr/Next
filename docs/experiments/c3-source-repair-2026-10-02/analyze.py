"""Validate archived C3 reports and regenerate compact, per-run evidence."""
import csv
import gzip
import json
from pathlib import Path
from statistics import median

ROOT = Path(__file__).resolve().parent


def read(path):
    if path.suffix == ".gz":
        with gzip.open(path, "rt", encoding="utf-8") as f:
            return json.load(f)
    return json.loads(path.read_text(encoding="utf-8"))


def audit(directory):
    rows = read(directory / "audit.json")
    committee = [x for x in rows if x["Name"].startswith("committee")]
    assert len(committee) == 4
    assert len({x["StateHash"] for x in committee}) == 1
    assert all(x.get("Pending", 0) == 0 for x in rows)
    assert all(x["Gap"] == "0" and x["Payments"] == x["Closed"] for x in committee)
    return committee[0]


def bench(path):
    data = read(path)
    s = data["Summary"]
    assert s["count"] == s["fast_completed"] == s["wallet_block_observed"] == s["member_completed"] == 300
    assert s["failed"] == s["not_sent"] == s["final_unfinished"] == s["unknown"] == 0
    assert len(data["Samples"]) == 300
    assert all(x["Outcome"] == "COMPLETE" for x in data["Samples"])
    times = [x["SentUnixNS"] for x in data["Samples"]]
    return {**{k: s[k] for k in ("elapsed_s", "fast_p50_ms", "fast_p95_ms", "block_observed_p50_ms", "dispatch_lag_p95_ms")},
            "send_span_s": (max(times) - min(times)) / 1e9}


def main():
    functional, costs = [], []
    normal_count = 0
    for mode in ("recovery", "control", "paused"):
        for run in range(1, 4):
            directory = ROOT / "c3-results" / f"{mode}-{run}"
            d, a = read(directory / "direct-0.json"), audit(directory)
            liability = a["Recovery"]
            assert liability["Open"] == liability["Repaired"] == 0
            if mode == "recovery":
                assert d["compensation_avoided"] and liability["Fulfilled"] == 1
                assert liability["PaidCAL"] == liability["RecoveredCAL"] == 0
            else:
                assert d["late_parent_reserve_recovered"] and liability["Recovered"] == 1
                assert liability["PaidCAL"] == liability["RecoveredCAL"] == 100
                nodes = d["physical_repair"]["Nodes"]
                assert len(nodes) == 4
                assert all(all(n["Snapshot"][k] for k in ("Committed", "Materialized", "IdentityStable", "BytesChanged")) for n in nodes)
            if mode == "paused":
                paused = read(directory / "paused-observations.json")
                assert len(paused["nodes"]) == 4
                assert all(n["obligation"]["Status"] == 3 and not n["repair"]["Committed"] and not n["repair"]["Materialized"] for n in paused["nodes"])
            else:
                bench(next(directory.glob("bench-v4-*.json*")))
                normal_count += 300
            row = {"mode": mode, "run": run, **{k: v for k, v in d.items() if k.endswith("_ms")}}
            if mode == "recovery":
                row["source_minus_child_observation_ms"] = d["autonomous_source_observed_ms"] - d["child_block_observed_ms"]
            functional.append(row)
    for variant in ("baseline", "candidate"):
        for run in range(1, 4):
            directory = ROOT / "cost-results" / f"{variant}-{run}"
            audit(directory)
            costs.append({"variant": variant, "run": run, **bench(next(directory.glob("bench-v4-*.json*")))})
            normal_count += 300
    medians = {}
    for variant in ("baseline", "candidate"):
        subset = [x for x in costs if x["variant"] == variant]
        medians[variant] = {k: median(x[k] for x in subset) for k in subset[0] if k not in ("variant", "run")}
    result = {"functional_cases_passed": len(functional), "cost_runs_passed": len(costs),
              "normal_payments_completed": normal_count, "fault_scenario_payments": 18,
              "functional": functional, "cost": costs, "median_of_run_metrics": medians}
    (ROOT / "analysis.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    for name, rows in (("functional", functional), ("cost", costs)):
        keys = list(dict.fromkeys(k for r in rows for k in r))
        with (ROOT / f"{name}.csv").open("w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=keys)
            writer.writeheader()
            writer.writerows(rows)
    print(json.dumps({k: v for k, v in result.items() if k not in ("functional", "cost")}, indent=2))


if __name__ == "__main__":
    main()
