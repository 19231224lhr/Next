#!/usr/bin/env python3
"""Archive task-local sources and hashes without changing the repository index."""
import hashlib
import json
import pathlib
import shutil
import subprocess

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parents[2]
FILES = (
    "cmd/payctl/direct_chain.go", "cmd/payctl/chain_timing.go",
    "cmd/payctl/chain_build_profile.go", "cmd/payctl/chain_build_profile_test.go",
    "cmd/payctl/chain_build_profile_bench_test.go", "cmd/payctl/chain_timing_test.go",
    "cmd/payctl/direct_bench.go", "go.mod", "go.sum",
)


def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    snapshot = HERE / "source-snapshot"
    snapshot.mkdir(exist_ok=True)
    for relative in FILES:
        target = snapshot / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(REPO / relative, target)
    patch = subprocess.check_output(["git", "diff", "--", *FILES], cwd=REPO)
    (HERE / "tracked-source.patch").write_bytes(patch)
    result = {"head":subprocess.check_output(["git", "rev-parse", "HEAD"],cwd=REPO,text=True).strip(),
              "branch":subprocess.check_output(["git","branch","--show-current"],cwd=REPO,text=True).strip(),
              "sources":{relative:sha(REPO / relative) for relative in FILES},
              "binaries":{name:sha(REPO / ".scratch/preparation-profile-bin" / name) for name in ("payctl","member","gateway","committee","payctl-profile.test")},
              "shared_cpu_affinity":[0,1,2,3],"GOMAXPROCS":4,
              "original_comparison_data":"../baseline-design-2026-10-13/validated-results (unchanged)",
              "notes":["Only the payment CLI has diagnostic code; servers use the original comparison binaries.",
                       "direct_bench.go grant filtering predates this investigation.",
                       "Initial timing and full-window CPU runs used earlier revisions of the same diagnostic hooks; observer A/B uses save substage counters and the opt-in event switch.",
                       "Archived final CLI adds an optional foreground-only CPU cutoff. All default payment/verification/persistence paths remain unchanged.",
                       "The component benchmark binary was built before the foreground-only CPU cutoff addition; benchmark function and all measured protocol functions are identical."]}
    artifacts = [p for p in HERE.rglob("*") if p.is_file() and p.name != "manifest.json"]
    result["artifact_sha256"] = {str(p.relative_to(HERE)).replace("\\","/"):sha(p) for p in artifacts}
    (HERE / "manifest.json").write_text(json.dumps(result,indent=2) + "\n",encoding="utf-8")
    print(json.dumps({"head":result["head"],"sources":len(FILES),"artifacts":len(artifacts)}))


if __name__ == "__main__": main()
