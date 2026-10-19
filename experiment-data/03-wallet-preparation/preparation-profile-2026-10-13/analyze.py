#!/usr/bin/env python3
"""Validate native chains and summarize exclusive preparation stages."""
import json
import pathlib
import statistics

ROOT = pathlib.Path(__file__).resolve().parent


def summarize(path):
    r = json.loads(path.read_text(encoding="utf-8"))
    hs = r["Hops"]
    assert r["Requested"] == len(hs) == 100 and not r.get("Error"), path
    for i, h in enumerate(hs):
        assert h["Hop"] == i + 1 and h["Attempts"] == 1
        assert h["ReadyUnixNS"] > h["SentUnixNS"] > h["BuildDoneUnixNS"]
        assert h["FinalUnixNS"] and h["MemberClosedUnixNS"]
        assert h["OutputData"]["Asset"] == 1 and h["OutputData"]["Amount"] == 100
        if i:
            assert h["Input"] == hs[i - 1]["Output"] and h["CertificateInput"]
            assert h["BuildStartUnixNS"] > hs[i - 1]["ReadyUnixNS"]
        assert all(v >= 0 for v in h["BuildStagesMS"].values())
        assert h["BuildMS"] - sum(h["BuildStagesMS"].values()) >= -1e-8
        if "BuildSaveStagesMS" in h:
            s = h["BuildSaveStagesMS"]
            assert all(v >= 0 for v in s.values())
            assert abs(s["update_total"] - sum(s[k] for k in ("queue_wait", "validation_and_locks", "commit_and_resume"))) < 1e-8
            assert abs(h["BuildStagesMS"]["save_request"] - s["update_total"] - s["encode_before_update"]) < 1e-8
    receipt = sum(h["FastMS"] for h in hs)
    prep = sum(h["BuildMS"] for h in hs[1:])
    residual = r["FastChainMS"] - receipt - prep
    assert residual >= 0
    result = {"name":path.name,"build_99_ms":prep,"receipt_sum_ms":receipt,
              "chain_ms":r["FastChainMS"],"interhop_other_ms":residual,
              "build_unattributed_ms":sum(h["BuildMS"] - sum(h["BuildStagesMS"].values()) for h in hs[1:]),
              "stages_99_ms":{k:sum(h["BuildStagesMS"][k] for h in hs[1:]) for k in hs[0]["BuildStagesMS"]}}
    if "FinalChecks" in hs[0]:
        result["final_checks"] = sum(h["FinalChecks"] for h in hs)
    if "BuildSaveStagesMS" in hs[0]:
        result["save_stages_99_ms"] = {k:sum(h["BuildSaveStagesMS"][k] for h in hs[1:]) for k in hs[0]["BuildSaveStagesMS"]}
    return result


def main():
    timing = [summarize(ROOT / "results" / f"timing-{i}-native.json") for i in range(5)]
    pairs = []
    for i in range(3):
        before = summarize(ROOT / "results" / f"ab-{i}-polling-native.json")
        after = summarize(ROOT / "results" / f"ab-{i}-events-native.json")
        pairs.append({"round":i,"polling":before,"events":after,
                      "preparation_reduction_pct":100 * (1 - after["build_99_ms"] / before["build_99_ms"]),
                      "chain_reduction_pct":100 * (1 - after["chain_ms"] / before["chain_ms"])})
    all_paths = sorted((ROOT / "results").glob("*-native.json"))
    for p in all_paths: summarize(p)
    avg = {k:statistics.mean(r["stages_99_ms"][k] for r in timing) for k in timing[0]["stages_99_ms"]}
    total = statistics.mean(r["build_99_ms"] for r in timing)
    result = {"valid_native_runs":len(all_paths),"verified_receipts":len(all_paths) * 100,
              "initial_timing_runs":timing,"initial_mean_build_99_ms":total,"initial_mean_stages_99_ms":avg,
              "initial_mean_stage_share_pct":{k:100 * v / total for k,v in avg.items()},"observer_ab_pairs":pairs,
              "observer_ab_median_paired_preparation_reduction_pct":statistics.median(p["preparation_reduction_pct"] for p in pairs),
              "observer_ab_median_paired_chain_reduction_pct":statistics.median(p["chain_reduction_pct"] for p in pairs)}
    (ROOT / "analysis.json").write_text(json.dumps(result, indent=2) + "\n",encoding="utf-8")
    print(json.dumps(result,indent=2))


if __name__ == "__main__": main()
