"""Reproduce relay work counts from the existing instrumented B run; no new load."""
import json
import statistics
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
DATA = ROOT / "group-trace-b/reports/trace100"


def load(node):
    return sorted(json.loads((DATA / f"{node}-timeline.json").read_text()),
                  key=lambda event: event["UnixNS"])


def matching_end(events, start, stage, deadline=None):
    matches = [event for event in events
               if event["Stage"] == stage
               and event["Fields"].get("spend") == start["Fields"]["spend"]
               and event["UnixNS"] >= start["UnixNS"]
               and (deadline is None or event["UnixNS"] <= deadline)]
    assert matches, (stage, start)
    return matches[0]


nodes = ["gateway0"] + [f"org0-member{i}" for i in range(4)]
work = {}
for node in nodes:
    events = load(node)
    counts = Counter(event["Stage"] for event in events)
    row = {stage: counts[stage] for stage in
           ["relay_enter", "relay_update_requested", "submit_start", "submit_done"]}
    installs = [event for event in events if event["Stage"] == "install_enter"]
    if installs:
        before, store = [], []
        for start in installs:
            entering = matching_end(events, start, "install_store_start")
            done = matching_end(events, entering, "install_store_done")
            before.append((entering["UnixNS"] - start["UnixNS"]) / 1e6)
            store.append((done["UnixNS"] - entering["UnixNS"]) / 1e6)
        row.update(install_count=len(installs),
                   install_before_store_p50_ms=statistics.median(before),
                   install_store_p50_ms=statistics.median(store))
    work[node] = row

events = load("gateway0")
service_ns = actual_capacity_ns = four_slot_capacity_ns = groups = 0
for start in events:
    if start["Stage"] != "relay_group_start":
        continue
    end = next(event for event in events
               if event["Stage"] == "relay_group_done"
               and all(event["Fields"][key] == start["Fields"][key]
                       for key in ["scan_ns", "offset"]))
    tasks = [event for event in events if event["Stage"] == "relay_enter"
             and start["UnixNS"] <= event["UnixNS"] <= end["UnixNS"]]
    assert len(tasks) == int(start["Fields"]["count"])
    for task in tasks:
        done = matching_end(events, task, "relay_update_returned", end["UnixNS"])
        service_ns += done["UnixNS"] - task["UnixNS"]
    duration = end["UnixNS"] - start["UnixNS"]
    actual_capacity_ns += duration * len(tasks)
    four_slot_capacity_ns += duration * 4
    groups += 1

result = {
    "source": "group-trace-b/reports/trace100; 100 payments, concurrency 64",
    "nodes": work,
    "total_submit_calls": sum(row["submit_start"] for row in work.values()),
    "total_relay_passes": sum(row["relay_enter"] for row in work.values()),
    "groups": groups,
    "group_idle_fraction_actual_tasks": 1 - service_ns / actual_capacity_ns,
    "group_idle_fraction_four_slots": 1 - service_ns / four_slot_capacity_ns,
    "interpretation": "Submit calls are not successful admissions or executions. "
                      "Update requests are not physical commits. Idle fractions "
                      "describe observed group slots, not predicted speedup."
}
(ROOT / "relay-work.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
print(json.dumps(result, indent=2))
