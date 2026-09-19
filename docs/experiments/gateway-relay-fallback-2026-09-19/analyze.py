"""Compare the member fallback experiments. Pass the directory containing group-relay2-*.

The primary start is physical commit return, not the later handler log: a wakeup
can legitimately start work before that handler has logged its return.
"""
import json
import statistics
import sys
from collections import Counter
from pathlib import Path

root = Path(sys.argv[1])


def stats(values):
    values = sorted(values)
    return {"n": len(values), "p50": statistics.median(values),
            "p95": values[int((len(values)-1)*.95)], "max": max(values)} if values else {}


results = []
for directory in sorted(root.glob("group-relay2-*")):
    report = directory / "reports"
    if not (report / "experiment.json").exists():
        continue
    bench = json.loads((report / "bench-v4-0.json").read_text())
    audit = json.loads((report / "audit.json").read_text())
    count = bench["Summary"]["count"]
    assert bench["Summary"]["failed"] == 0 and len(bench["Samples"]) == count
    assert all(row["Pending"] == 0 for row in audit)
    committees = [row for row in audit if row["Name"].startswith("committee")]
    assert len(committees) == 4 and len({row["StateHash"] for row in committees}) == 1
    assert all(row["Closed"] == count and row["Gap"] == "0" and row.get("PrivateProofRecords", 0) == 0 for row in committees)
    result = {"run": directory.name, "summary": bench["Summary"], "audit": "passed"}
    if bench["Summary"]["trace"]:
        trace = report / "trace100"
        timelines = {path.name.removesuffix("-timeline.json"): json.loads(path.read_text())
                     for path in trace.glob("*-timeline.json")}
        events = timelines["gateway0"]
        assert len(events) < 4096, "gateway timeline truncated"
        stores = [event["Fields"] for event in events if event["Stage"] == "store_update"]
        assert all(event["failed"] == "false" for event in stores)
        settlement = json.loads((trace / "settlement.json").read_text())

        def first(stage, fact):
            return min(event["UnixNS"] for event in events
                       if event["Stage"] == stage and event["Fields"].get("spend") == fact)

        rows = []
        for sample in bench["Samples"]:
            fact = sample["Fact"]
            callback = first("persist_callback", fact)
            commits = [event for event in stores if int(event["callback_ns"]) <= callback <= int(event["evaluated_ns"])]
            assert len(commits) == 1
            durable = int(commits[0]["returned_ns"])
            records = settlement["committee0"][fact]
            commit = min(row["CommittedUnixNS"] for row in records if row["CommittedUnixNS"] > 0)
            received = min(row["ReceivedUnixNS"] for row in records if row["ReceivedUnixNS"] > 0)
            submit = first("submit_start", fact)
            rows.append({"fact": fact,
                         "save_ms": (first("outbox_persist_done", fact)-first("outbox_persist_start", fact))/1e6,
                         "handler_save_to_relay_ms": (first("relay_enter", fact)-first("outbox_persist_done", fact))/1e6,
                         "durable_to_submit_ms": (submit-durable)/1e6,
                         "durable_to_committee_ms": (received-durable)/1e6,
                         "submit_to_return_ms": (first("submit_done", fact)-submit)/1e6,
                         "submit_return_to_update_return_ms": (first("relay_update_returned", fact)-first("submit_done", fact))/1e6,
                         "send_to_commit_ms": (commit-sample["SentUnixNS"])/1e6})
        result["stages"] = {key: stats([row[key] for row in rows]) for key in rows[0] if key.endswith("_ms")}
        result["calls"] = {name: dict(Counter(event["Stage"] for event in timeline))
                           for name, timeline in timelines.items() if name == "gateway0" or name.startswith("org0-member")}
        result["member_update_requests"] = sum(counts.get("relay_update_requested", 0) for name, counts in result["calls"].items() if name.startswith("org0-member"))
        result["submit_calls"] = sum(counts.get("submit_start", 0) for counts in result["calls"].values())
        result["gateway_storage"] = {"physical_updates": len(stores),
                                     "changed": sum(event["no_changes"] == "false" for event in stores),
                                     "write_sync_ms": sum(int(event["write_ns"]) for event in stores)/1e6}
        if (report / "gateway-rejections.json").exists():
            rejected = json.loads((report / "gateway-rejections.json").read_text())
            assert all(row["path"] == "/v1/commands" for row in rejected)
            assert len(rejected) == result["calls"]["gateway0"]["submit_start"]
            covered, delays = set(), {}
            for name, timeline in timelines.items():
                if not name.startswith("org0-member"): continue
                submissions = [event for event in timeline if event["Stage"] == "submit_start"]
                starts = []
                for fact in {event["Fields"]["spend"] for event in submissions}:
                    submitted = min(event["UnixNS"] for event in submissions if event["Fields"]["spend"] == fact)
                    installed = min(event["UnixNS"] for event in timeline if event["Stage"] == "install_store_start" and event["Fields"]["spend"] == fact)
                    delay = (submitted-installed)/1e6
                    assert delay >= 2000 + int(name[-1])*250
                    starts.append(delay)
                    covered.add(fact)
                if starts: delays[name] = min(starts)
            assert len(covered) == count
            result["fallback"] = {"gateway_rejections": len(rejected), "member_covered_payments": len(covered),
                                  "minimum_ms_after_install_store_start": delays}
        (report / "relay-stages.json").write_text(json.dumps(rows, indent=2)+"\n")
    results.append(result)
(root / "relay-step2-comparison.json").write_text(json.dumps(results, indent=2)+"\n")
for result in results:
    print(result["run"], json.dumps({"summary": result["summary"], "stages": result.get("stages"),
                                  "submits": result.get("submit_calls"), "member_updates": result.get("member_update_requests"), "storage": result.get("gateway_storage")}))
