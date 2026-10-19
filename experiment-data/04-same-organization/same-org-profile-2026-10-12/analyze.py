"""Summarize monotonic per-payment traces; durations are milliseconds."""
import json
from pathlib import Path
from statistics import mean, median
from collections import defaultdict

root = Path(__file__).resolve().parent
runs = json.loads((root / "profiles-monotonic.json").read_text())
group = defaultdict(lambda: defaultdict(list))
summary = []
baseline_rows = []

def stats(xs):
    xs = sorted(xs)
    return dict(mean=mean(xs), p50=median(xs), p95=xs[(len(xs)*95-1)//100])

for run in runs:
    rows = []
    for p in run["Payments"]:
        row = {"total": (p["Ready"]-p["Start"])/1e6,
               "collect": (p["Collected"]-p["Start"])/1e6,
               "wallet": (p["Ready"]-p["Collected"])/1e6}
        ws = [s for s in p["Stores"] if s["Role"] == f'wallet{1-p["Index"]%2}']
        assert ws
        s = ws[-1]  # tick ties can include a prior store span at Start
        row.update(wallet_db=(s["End"]-s["Start"])/1e6,
                   wallet_db_callback=(s["Evaluated"]-s["Callback"])/1e6,
                   wallet_db_after_callback=(s["End"]-s["Evaluated"])/1e6,
                   wallet_before_db=(s["Start"]-p["Collected"])/1e6)
        events = p["Events"] or []
        if events:
            # Client recorder is shared; server LocalNS belongs to each server recorder.
            # Only subtract two LocalNS values from the SAME recorder.
            votes = [e for e in events if e["stage"] == "vote_selected"]
            assert len(votes) == 3
            third = votes[-1]  # recorder order resolves equal-tick timestamps
            idx = int(third["node"].split("-")[-1])
            url = run["MemberURLs"][idx]
            server = {e["stage"]: e["local_ns"] for e in events if e["node"] == url}
            gw = {e["stage"]: e["local_ns"] for e in events if e["node"] == "gateway"}
            def dur(a, b): return (server[b]-server[a])/1e6
            row.update(
                member_decode=dur("http_handler_enter", "request_decoded"),
                member_vector=dur("request_decoded", "vector_prepared"),
                member_parent=dur("vector_prepared", "parents_verified"),
                member_outputs=dur("parents_verified", "validation_complete"),
                member_db_wait=dur("commit_requested", "update_started"),
                member_db_callback=dur("update_started", "update_evaluated"),
                member_db_after_callback=dur("update_evaluated", "commit_returned"),
                member_db=dur("commit_requested", "commit_returned"),
                member_sign=dur("commit_returned", "response_ready"),
                member_total=dur("http_handler_enter", "response_ready"),
                gateway_prep=(gw["fanout_ready"]-gw["collect_enter"])/1e6,
                gateway_final=(gw["certificate_verified"]-gw["quorum_collected"])/1e6,
            )
            row["other_collect"] = row["collect"]-row["gateway_prep"]-row["member_total"]-row["gateway_final"]
            assert row["other_collect"] > -0.05, row
        rows.append(row)
        if run["Name"] == "durable": baseline_rows.append(row)
        for k, value in row.items(): group[run["Name"]][k].append(value)
    summary.append(dict(name=run["Name"], round=run["Round"],
                        total=stats([r["total"] for r in rows]),
                        primitives_ms={k: v/1e6 for k,v in run["PrimitiveNS"].items()}))
assert len({r["WorkloadHash"] for r in runs}) == 1
slow = sorted(baseline_rows, key=lambda r:r["total"])[-15:]
out = dict(runs=summary, baseline_slowest_5pct={k:mean(r[k] for r in slow) for k in slow[0]},
           baseline_max=max(baseline_rows,key=lambda r:r["total"]),
           pooled={name:{k:stats(v) for k,v in metrics.items()} for name,metrics in group.items()})
(root / "summary.json").write_text(json.dumps(out, indent=2), encoding="utf-8")
print(json.dumps(out, indent=2))
