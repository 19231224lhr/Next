#!/usr/bin/env python3
"""Record the measured executables, source snapshot and original artifacts."""
import hashlib
import json
import pathlib
import shutil
import subprocess

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parents[2]
SOURCES = (
    "internal/wallet/direct.go", "internal/wallet/direct_bytes_test.go",
    "cmd/payctl/direct_chain.go", "cmd/payctl/direct_bench.go", "cmd/payctl/direct_fault.go",
    "cmd/payctl/direct_liability.go", "cmd/payctl/direct_budget.go", "cmd/payctl/direct.go",
    "cmd/payctl/e7_run.go", "cmd/payctl/e8.go", "cmd/payctl/chain_timing.go",
    "cmd/payctl/chain_timing_test.go", "cmd/payctl/chain_build_profile.go",
    "cmd/payctl/chain_build_profile_test.go", "cmd/payctl/chain_build_profile_bench_test.go",
    "go.mod", "go.sum",
)


def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    for name in SOURCES:
        output = HERE / "after-source" / name
        output.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(REPO / name, output)
    binaries = {arm:{name:sha(REPO / f".scratch/preparation-{arm}-bin" / name)
                     for name in ("payctl","member","gateway","committee")}
                for arm in ("before","after")}
    assert all(binaries["before"][name] == binaries["after"][name] for name in ("member","gateway","committee"))
    fp_release = pathlib.Path("//wsl.localhost/Ubuntu/home/qyinf/.local/share/next-baseline-tools/fastpay-target/release")
    original = HERE.parent / "baseline-design-2026-10-13"
    result = {"source_head_at_capture":subprocess.check_output(["git","rev-parse","HEAD"],cwd=REPO,text=True).strip(),
              "branch":subprocess.check_output(["git","branch","--show-current"],cwd=REPO,text=True).strip(),
              "go_version":subprocess.check_output(["go","version"],text=True).strip(),
              "next_binaries_sha256":binaries,
              "fastpay_binaries_sha256":{name:sha(fp_release / name) for name in ("server","examples/baseline_client")},
              "fastpay_source_commit":subprocess.check_output(["git","rev-parse","HEAD"],cwd=REPO / ".scratch/fastpay-upstream",text=True).strip(),
              "source_sha256":{name:sha(REPO / name) for name in SOURCES},
              "baseline_runner_sha256":{name:sha(original / name) for name in ("run_next.py","run_fastpay.py")},
              "shared_affinity_cpus":[0,1,2,3],"GOMAXPROCS":4,
              "run_order":[["before","after","fastpay"],["fastpay","after","before"],["before","fastpay","after"],["after","before","fastpay"],["fastpay","before","after"]],
              "conditions":{"workload":"100 real dependent transfers among 3 rotating wallets, only first funded",
                            "next":"4 members, 4 real committee services, 1 gateway; normal public execution/follow; memory server modes and ext4 NoSync Bolt wallets; ordinary Direct, short-source/compact approvals disabled, no Pool",
                            "fastpay":"4 authorities, 1 shard, quorum3, memory native server/wallet, TCP, receiver-confirmed client with funding guard, receiver sync and unmeasured spendability/conservation probe",
                            "diagnostics":"Build phase and Store callback times enabled in both Next arms; CPU profiles disabled; notification override unset so each binary uses its original/default mode"},
              "checks":{"next_receipts":1035,"fastpay_measured_receipts":500,"all_closures_complete":True,
                        "timed_next_requests_all_single_attempt":True,"public_wait_smoke_retries":3,
                        "public_wait_adapter_recovered_from_validation_error":True}}
    result["artifact_sha256"] = {p.relative_to(HERE).as_posix():sha(p) for p in HERE.rglob("*") if p.is_file() and p.name != "manifest.json"}
    (HERE / "manifest.json").write_text(json.dumps(result,indent=2) + "\n",encoding="utf-8")
    print(json.dumps({"head":result["source_head_at_capture"],"sources":len(SOURCES),"artifacts":len(result["artifact_sha256"]),"server_binaries_unchanged":True}))


if __name__ == "__main__": main()
