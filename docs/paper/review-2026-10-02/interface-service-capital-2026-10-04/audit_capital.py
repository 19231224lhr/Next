"""Audit recorded P endpoints; these are not continuous or hidden-QC observations."""
from pathlib import Path
import hashlib
import json

out = Path(__file__).resolve().parent
repo = out.parents[3]
runs = repo / "docs/experiments/final-evidence-2026-10-04/results-mixed"
result = []
for run in sorted(runs.glob("r*-P")):
    source = run / "final-resources.json"
    members = []
    for node, record in json.loads(source.read_text()).items():
        if "member" not in node:
            continue
        value = record["value"]
        resources = value["Resources"]
        cal = next(r for r in resources if r["Key"]["Kind"] == 1)
        members.append({
            "node": node,
            "Limited": value["Limited"],
            "CAL_grant": cal["Grant"],
            "CAL_reserved": sum(s["Reserved"] for s in cal["Slices"]),
            "work_reserved": sum(s["Reserved"] for r in resources
                                 if r["Key"]["Kind"] in (3, 4) for s in r["Slices"]),
            "FUEL_retained": sum(s["Reserved"] for r in resources
                                 if r["Key"]["Kind"] == 2 for s in r["Slices"]),
        })
    assert len(members) == 8
    assert all(not any(m["Limited"]) and m["CAL_reserved"] == m["work_reserved"] == 0
               for m in members)
    result.append({"run": run.name,
                   "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
                   "members": members})
assert len(result) == 3
(out / "capital-audit.json").write_text(json.dumps(result, indent=2), encoding="utf-8")
print("Three P runs, eight members each: zero Limited counters; zero final CAL/work reservations.")
