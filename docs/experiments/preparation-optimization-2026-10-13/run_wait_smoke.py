#!/usr/bin/env python3
"""Exercise the optimized chain's real public-finality gate using the existing lab runner."""
import argparse
import importlib.util
import json
import pathlib

HERE = pathlib.Path(__file__).resolve().parent
BASELINE = HERE.parent / "baseline-design-2026-10-13" / "run_next.py"
spec = importlib.util.spec_from_file_location("next_baseline", BASELINE)
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
original_command = native.run_command


def public_wait_command(argv, *args, **kwargs):
    if len(argv) > 1 and argv[1] == "chain-v4" and "--prepare" not in argv:
        argv = [*argv, "--wait-final"]
    return original_command(argv, *args, **kwargs)


def public_wait_result(report, length):
    hops = report["Hops"]
    assert report["Mode"] == "wait_final" and report["Requested"] == len(hops) == length
    assert report["SavedRequestBytesReused"] and report["CommitNotifications"] and not report.get("Error")
    for i, h in enumerate(hops):
        assert h["Hop"] == i + 1 and h["Attempts"] >= 1
        assert not h["CertificateInput"]
        assert h["ReadyUnixNS"] > h["SentUnixNS"] and h["FinalUnixNS"] and h["MemberClosedUnixNS"]
        assert h["OutputData"]["Asset"] == 1 and h["OutputData"]["Amount"] == 100
        if i:
            assert h["Input"] == hops[i - 1]["Output"] and h["BuildStartUnixNS"] >= hops[i - 1]["FinalUnixNS"]
    return {"completed":length,"receipt_status":"complete","closure_status":"complete",
            "closure_errors":[],"samples_ms":[h["FastMS"] for h in hops],"chain_total_ms":report["FastChainMS"],
            "public_finality_gate_verified":True,"http_attempts":[h["Attempts"] for h in hops],
            "retry_count":sum(h["Attempts"] - 1 for h in hops)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin", required=True, type=pathlib.Path)
    parser.add_argument("--run-dir", required=True, type=pathlib.Path)
    parser.add_argument("--read-existing", action="store_true", help="validate a completed native run after adapter validation repair")
    args = parser.parse_args()
    args.workload, args.length, args.count, args.storage, args.rate = "chain", 5, 5, "memory", 0.0
    if args.read_existing:
        path = args.run_dir / "reports" / "chain-v4.json"
        result = public_wait_result(json.loads(path.read_text()), 5)
        result.update(run_dir=str(args.run_dir),native_report=str(path),native_exit_code=None,
                      adapter_recovery_note="Original native run completed all five payments. Adapter initially rejected retry counts greater than one; corrected validation reads the same preserved run, without replacing it. Native exit code was not retained after the adapter assertion.")
        print(json.dumps(result,sort_keys=True))
        return
    native.run_command, native.chain_result = public_wait_command, public_wait_result
    print(json.dumps(native.run(args),sort_keys=True))


if __name__ == "__main__": main()
