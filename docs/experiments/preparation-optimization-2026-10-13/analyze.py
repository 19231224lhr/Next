#!/usr/bin/env python3
"""Validate the matched runs and report paired changes without merging old datasets."""
import json
import math
import pathlib
import statistics

HERE = pathlib.Path(__file__).resolve().parent


def percentile(values, q):
    ordered = sorted(values)
    rank = (len(ordered) - 1) * q
    low = math.floor(rank)
    high = math.ceil(rank)
    return ordered[low] + (ordered[high] - ordered[low]) * (rank - low)


def next_chain(round_index, arm):
    prefix = HERE / "results" / f"r{round_index}-{arm}"
    result = json.loads(prefix.with_suffix(".json").read_text())
    r = json.loads(pathlib.Path(str(prefix) + "-native.json").read_text())
    hs = r["Hops"]
    assert r["Requested"] == len(hs) == result["completed"] == 100 and not r.get("Error")
    assert result["native_exit_code"] == 0 and result["closure_status"] == "complete" and not result["closure_errors"]
    assert result["processes"] == {"client":1,"committee":4,"gateway":1,"lab_supervisor":1,"member":4,"total_servers":9}
    assert result["receiver_genesis_cal_outputs"] == 0 and r["Mode"] == "fast"
    assert r["WalletNoSync"] and not r["WaitMemberInputs"] and not r["RecordAttempts"]
    assert r.get("SavedRequestBytesReused",False) == r.get("CommitNotifications",False) == (arm == "after")
    for i,h in enumerate(hs):
        assert h["Hop"] == i + 1 and h["Sender"] == i % 3 and h["Receiver"] == (i + 1) % 3
        assert h["Attempts"] == 1 and h["ReadyUnixNS"] > h["SentUnixNS"] > h["BuildDoneUnixNS"]
        assert h["FinalUnixNS"] and h["MemberClosedUnixNS"]
        assert h["OutputData"]["Asset"] == 1 and h["OutputData"]["Amount"] == 100
        if i:
            assert h["Input"] == hs[i - 1]["Output"] and h["CertificateInput"]
            assert h["BuildStartUnixNS"] > hs[i - 1]["ReadyUnixNS"]
        assert all(math.isfinite(v) and v >= 0 for v in h["BuildStagesMS"].values())
        assert h["BuildMS"] >= sum(h["BuildStagesMS"].values()) - 1e-8
    receipts = [h["FastMS"] for h in hs]
    build99 = sum(h["BuildMS"] for h in hs[1:])
    assert r["FastChainMS"] >= sum(receipts) + build99
    assert receipts == result["samples_ms"]
    return {"chain_ms":r["FastChainMS"],"build99_ms":build99,"receipt_sum_ms":sum(receipts),
            "receipt_p50_ms":statistics.median(receipts),"receipt_p95_ms":percentile(receipts,.95),
            "encode99_ms":sum(h["BuildStagesMS"]["encode_request"] for h in hs[1:]),
            "final_checks":sum(h["FinalChecks"] for h in hs),
            "stages99_ms":{k:sum(h["BuildStagesMS"][k] for h in hs[1:]) for k in hs[0]["BuildStagesMS"]},
            "save_stages99_ms":{k:sum(h["BuildSaveStagesMS"][k] for h in hs[1:]) for k in hs[0]["BuildSaveStagesMS"]}}


def fastpay_chain(round_index):
    r = json.loads((HERE / "results" / f"r{round_index}-fastpay.json").read_text())
    receipts, builds = r["samples_ms"], r["build_ms"]
    assert r["completed"] == len(receipts) == len(builds) == 100
    assert r["amount"] == 100 and r["client_mode"] == "receiver-confirmed" and r["transport"] == "Tcp"
    assert r["principal_conserved"] and r["probe_completed"] and r["authorities"] == 4
    assert all(math.isfinite(v) and v > 0 for v in receipts)
    assert all(math.isfinite(v) and v >= 0 for v in builds)
    assert r["chain_total_ms"] >= sum(receipts) + sum(builds[1:]) - 1e-7
    return {"chain_ms":r["chain_total_ms"],"build99_ms":sum(builds[1:]),
            "receipt_sum_ms":sum(receipts),"receipt_p50_ms":statistics.median(receipts),
            "receipt_p95_ms":percentile(receipts,.95)}


def reduction(before, after): return 100 * (1 - after / before)


def main():
    rounds = []
    for i in range(5):
        before, after, fastpay = next_chain(i,"before"), next_chain(i,"after"), fastpay_chain(i)
        rounds.append({"round":i,"before":before,"after":after,"fastpay":fastpay,
                       "preparation_reduction_pct":reduction(before["build99_ms"],after["build99_ms"]),
                       "chain_reduction_pct":reduction(before["chain_ms"],after["chain_ms"]),
                       "after_vs_fastpay_chain_reduction_pct":reduction(fastpay["chain_ms"],after["chain_ms"])})
    medians = {arm:{key:statistics.median(r[arm][key] for r in rounds)
                    for key in ("chain_ms","build99_ms","receipt_sum_ms","receipt_p50_ms","receipt_p95_ms")}
               for arm in ("before","after","fastpay")}
    median_changes = {key:statistics.median(r[key] for r in rounds)
                      for key in ("preparation_reduction_pct","chain_reduction_pct","after_vs_fastpay_chain_reduction_pct")}
    independent = json.loads((HERE / "results" / "independent-smoke.json").read_text())
    assert independent["completed"] == 30 and independent["receipt_status"] == independent["closure_status"] == "complete"
    assert not independent["closure_errors"] and independent["native_exit_code"] == 0
    wait = json.loads((HERE / "results" / "wait-smoke.json").read_text())
    assert wait["completed"] == 5 and wait["public_finality_gate_verified"]
    assert wait["receipt_status"] == wait["closure_status"] == "complete" and not wait["closure_errors"]
    analysis = {"rounds":rounds,"medians_of_per_run_statistics":medians,"median_paired_reductions_pct":median_changes,
                "median_chain_ratio_reduction_pct":reduction(medians["before"]["chain_ms"],medians["after"]["chain_ms"]),
                "median_preparation_ratio_reduction_pct":reduction(medians["before"]["build99_ms"],medians["after"]["build99_ms"]),
                "median_after_vs_fastpay_chain_ratio_reduction_pct":reduction(medians["fastpay"]["chain_ms"],medians["after"]["chain_ms"]),
                "verified_next_receipts":1035,"verified_fastpay_receipts":500,
                "independent_smoke":independent,"wait_smoke":wait}
    (HERE / "analysis.json").write_text(json.dumps(analysis,indent=2) + "\n",encoding="utf-8")
    print(json.dumps({k:v for k,v in analysis.items() if k not in ("rounds","independent_smoke","wait_smoke")},indent=2))


if __name__ == "__main__": main()
