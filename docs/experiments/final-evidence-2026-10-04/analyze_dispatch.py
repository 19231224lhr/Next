"""Decompose retained P dispatch records; do not infer unrecorded runtime causes."""
import bisect
import json
import statistics
from pathlib import Path

root = Path(__file__).resolve().parent

def quantiles(values):
    values = sorted(values)
    return {"p50": statistics.median(values), "p95": values[int(.95 * (len(values)-1))], "max": max(values)}

out = {"scope": "Same-host timestamps. Fast slot acquisition precedes serialized pacing; release is approximated by FastUnixNS, recorded immediately before fastDone(). Population samples are not runtime traces.", "runs": []}
for path in sorted((root / "results-mixed").glob("r*-P/bench-v4-100.json")):
    data = json.loads(path.read_text())
    rows = data["Samples"]
    acquired = sorted(r["Dispatch"]["SendAcquiredUnixNS"] for r in rows)
    sent = sorted(r["SentUnixNS"] for r in rows)
    fast = sorted(r["FastUnixNS"] for r in rows)
    limited = [r for r in rows if r["Dispatch"]["SendLimited"]]
    populations = []
    for r in limited:
        t = r["Dispatch"]["TotalAcquiredUnixNS"]
        a, s, f = (bisect.bisect_right(v,t) for v in (acquired,sent,fast))
        populations.append({"presend": a-s, "sent_awaiting_receipt": s-f})
    pre = sum((r["SentUnixNS"]-r["Dispatch"]["SendAcquiredUnixNS"])/1e6 for r in rows)
    receiving = sum((r["FastUnixNS"]-r["SentUnixNS"])/1e6 for r in rows)
    assert len(limited) == data["Summary"]["send_limit_hits"]
    row = {"case": path.parent.name, "send_limit_hits":len(limited), "total_limit_hits":data["Summary"]["total_limit_hits"], "sum_fast_slot_presend_ms":pre, "sum_fast_slot_sent_to_receipt_ms":receiving, "presend_share_of_slot_time":pre/(pre+receiving), "presend_ms":quantiles([(r["SentUnixNS"]-r["Dispatch"]["SendAcquiredUnixNS"])/1e6 for r in rows]), "pacing_wait_ms":quantiles([r["PacingWaitMS"] for r in rows])}
    if populations:
        row["at_send_limit_check"] = {k:quantiles([p[k] for p in populations]) for k in populations[0]}
    out["runs"].append(row)
(root / "results-mixed/dispatch-analysis.json").write_text(json.dumps(out,indent=2)+"\n")
print(json.dumps(out,indent=2))
