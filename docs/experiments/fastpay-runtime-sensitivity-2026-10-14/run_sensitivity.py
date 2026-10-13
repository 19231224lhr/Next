"""Sequential official/default/worker-count FastPay comparison, on Linux only."""
import argparse
import csv
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

ARMS = ("official", "unset", "threads1", "threads2", "threads4")


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def provenance(source):
    paths = ("fastpay/src/server.rs", "fastpay/examples/baseline_client.rs", "Cargo.lock")
    result = {"path": str(source), "files": {path: digest(source / path) for path in paths}}
    generated = source / "runtime-provenance.json"
    if generated.exists():
        result["runtime_provenance"] = json.loads(generated.read_text())
        result["runtime_provenance_sha256"] = digest(generated)
        for path, hashes in result["runtime_provenance"]["files"].items():
            if digest(source / path) != hashes["variant_sha256"]:
                raise ValueError(f"variant source changed after preparation: {path}")
        if digest(source / "Cargo.lock") != result["runtime_provenance"]["cargo_lock_sha256"]:
            raise ValueError("variant lock changed after preparation")
        if digest(source / "runtime-only.patch") != result["runtime_provenance"]["patch_sha256"]:
            raise ValueError("variant patch changed after preparation")
    else:
        result["revision"] = subprocess.check_output(
            ["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    return result


def process_sample(pid, observations):
    """Sample this harness and descendants; counts are observed, not configured."""
    todo = [pid]
    while todo:
        current = todo.pop()
        base = Path("/proc") / str(current)
        try:
            todo.extend(int(child) for child in (base / "task" / str(current) / "children").read_text().split())
            executable = os.readlink(base / "exe")
            argv = (base / "cmdline").read_bytes().decode(errors="replace").rstrip("\0").split("\0")
            threads = len(list((base / "task").iterdir()))
            affinity = sorted(os.sched_getaffinity(current))
        except (OSError, ProcessLookupError):
            continue
        if Path(executable).name not in ("server", "baseline_client"):
            continue
        if Path(executable).name == "server" and "run" not in argv:
            continue
        if "--prepare" in argv:
            continue
        record = observations.setdefault(str(current), {"executable": executable,
            "argv": argv,
            "thread_min": threads, "thread_max": threads, "samples": 0,
            "affinities": [], "first_ns": time.time_ns()})
        record["samples"] += 1
        record["thread_min"] = min(record["thread_min"], threads)
        record["thread_max"] = max(record["thread_max"], threads)
        record["last_ns"] = time.time_ns()
        if affinity not in record["affinities"]:
            record["affinities"].append(affinity)


def percentile(values, fraction):
    ordered = sorted(values)
    if not ordered:
        return None
    position = (len(ordered) - 1) * fraction
    lower = int(position)
    upper = min(lower + 1, len(ordered) - 1)
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def interrupted(signum, _frame):
    raise InterruptedError(f"interrupted by signal {signum}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--official-bin", type=Path, required=True)
    parser.add_argument("--variant-bin", type=Path, required=True)
    parser.add_argument("--official-source", type=Path, required=True)
    parser.add_argument("--variant-source", type=Path, required=True)
    parser.add_argument("--count", type=int, choices=(100, 1000), default=100)
    parser.add_argument("--repeats", type=int, default=5)
    parser.add_argument("--cpus", default="0-3")
    parser.add_argument("--thread-sample-ms", type=int, default=200,
                        help="/proc sampling interval; zero disables diagnostic sampling")
    parser.add_argument("--job-timeout", type=int, default=600)
    args = parser.parse_args()
    if sys.platform != "linux" or args.repeats < 1 or args.thread_sample_ms < 0 or args.job_timeout < 1:
        parser.error("Linux, positive repeats/timeout and nonnegative sampling interval required")
    for name in ("root", "official_bin", "variant_bin", "official_source", "variant_source"):
        setattr(args, name, getattr(args, name).resolve())
    if args.root.exists():
        raise FileExistsError(args.root)
    here = Path(__file__).resolve().parent
    baseline = here.parent / "baseline-design-2026-10-13" / "run_fastpay.py"
    sources = {"official": provenance(args.official_source), "variant": provenance(args.variant_source)}
    if "runtime_provenance" not in sources["variant"]:
        raise ValueError("variant must have preparation provenance")
    generated = sources["variant"]["runtime_provenance"]
    if sources["official"].get("revision") != generated["upstream_revision"]:
        raise ValueError("official source revision differs from variant base")
    for path, hashes in generated["files"].items():
        # Upstream checkout CRLF can differ from git archive LF without a source change.
        original = (args.official_source / path).read_bytes()
        candidates = (hashlib.sha256(original).hexdigest(),
                      hashlib.sha256(original.replace(b"\r\n", b"\n")).hexdigest())
        if hashes["original_sha256"] not in candidates:
            raise ValueError(f"official source differs from the variant base: {path}")
    if sources["official"]["files"]["Cargo.lock"] != sources["variant"]["files"]["Cargo.lock"]:
        raise ValueError("official and variant Cargo.lock differ")
    jobs = []
    for trial in range(args.repeats):
        # Each arm occupies every order position once over five repeats.
        order = list(ARMS[trial % len(ARMS):] + ARMS[:trial % len(ARMS)])
        jobs.extend({"trial": trial, "position": position, "arm": arm}
                    for position, arm in enumerate(order))
    manifest = {"created_ns": time.time_ns(), "planned_jobs": len(jobs), "jobs": jobs,
                "arguments": {name: str(value) for name, value in vars(args).items()},
                "inherited_affinity": sorted(os.sched_getaffinity(0)), "sources": sources,
                "harness_sha256": digest(Path(__file__)), "baseline_harness_sha256": digest(baseline),
                "binaries": {name: {path: digest(directory / path) for path in
                    ("server", "examples/baseline_client")} for name, directory in
                    (("official", args.official_bin), ("variant", args.variant_bin))}}
    args.root.mkdir(parents=True)
    (args.root / "plan.json").write_text(json.dumps(manifest, indent=2) + "\n")
    (args.root / "run_sensitivity.py").write_bytes(Path(__file__).read_bytes())
    (args.root / "run_fastpay.py").write_bytes(baseline.read_bytes())
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    fields = ("trial", "position", "arm", "planned", "completed", "exit", "verified",
              "chain_total_ms", "receipt_goodput_per_s", "receipt_p50_ms", "receipt_p95_ms",
              "request_to_receipt_p50_ms", "request_to_receipt_p95_ms", "probe_completed",
              "principal_conserved", "server_thread_max", "client_thread_max", "error")
    failures = 0
    with (args.root / "metrics.csv").open("x", newline="") as csvfile, \
            (args.root / "status.jsonl").open("x") as status:
        writer = csv.DictWriter(csvfile, fieldnames=fields)
        writer.writeheader()
        for job in jobs:
            arm = job["arm"]
            label = f"r{job['trial']}-{arm}"
            directory = args.root / label
            binary = args.official_bin if arm == "official" else args.variant_bin
            env = dict(os.environ)
            env.pop("FASTPAY_RUNTIME_THREADS", None)
            if arm.startswith("threads"):
                env["FASTPAY_RUNTIME_THREADS"] = arm.removeprefix("threads")
            command = ["taskset", "-c", args.cpus, sys.executable, str(baseline),
                       "--bin", str(binary), "--run-dir", str(directory), "--workload", "chain",
                       "--length", str(args.count), "--count", str(args.count),
                       "--protocol", "Tcp", "--client-mode", "receiver-confirmed"]
            item = {**job, "command": command, "start_ns": time.time_ns(), "run_dir": str(directory),
                    "environment": {name: env.get(name) for name in
                        ("FASTPAY_RUNTIME_THREADS", "GOMAXPROCS", "RUST_LOG", "RUSTFLAGS", "CARGO_HOME", "RUSTUP_HOME")},
                    "requested_cpus": args.cpus, "processes": {}}
            error, report, exitcode = "", {}, None
            proc = None
            try:
                print("START " + label, flush=True)
                with (args.root / (label + ".stdout")).open("x") as stdout, \
                        (args.root / (label + ".stderr")).open("x") as stderr:
                    proc = subprocess.Popen(command, stdout=stdout, stderr=stderr,
                                            env=env, start_new_session=True)
                    deadline = time.monotonic() + args.job_timeout
                    while proc.poll() is None:
                        if args.thread_sample_ms:
                            process_sample(proc.pid, item["processes"])
                        if time.monotonic() >= deadline:
                            raise TimeoutError("job timeout")
                        time.sleep(args.thread_sample_ms / 1000 if args.thread_sample_ms else 0.2)
                    exitcode = proc.returncode
                native = directory / "native-client.json"
                if native.exists():
                    report = json.loads(native.read_text())
                if exitcode != 0:
                    raise RuntimeError(f"baseline harness exited {exitcode}")
                if report.get("completed") != args.count or len(report.get("samples_ms", [])) != args.count \
                        or len(report.get("request_to_receipt_ms", [])) != args.count \
                        or report.get("probe_completed") is not True or report.get("principal_conserved") is not True:
                    raise ValueError("incomplete or unverified receiver-confirmed workload")
            except (KeyboardInterrupt, InterruptedError):
                error = "interrupted"
                raise
            except Exception as exc:
                error = f"{type(exc).__name__}: {exc}"
                failures += 1
            finally:
                if proc is not None and proc.poll() is None:
                    try:
                        os.killpg(proc.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                    try:
                        proc.wait(timeout=12)
                    except subprocess.TimeoutExpired:
                        os.killpg(proc.pid, signal.SIGKILL)
                        proc.wait()
                    exitcode = proc.returncode
                item.update(end_ns=time.time_ns(), exit=exitcode, error=error,
                            planned=args.count, completed=report.get("completed"), verified=not error)
                (args.root / (label + ".metadata.json")).write_text(json.dumps(item, indent=2) + "\n")
                status.write(json.dumps(item) + "\n")
                status.flush()
                row = {**job, "planned": args.count, "completed": report.get("completed"),
                       "exit": exitcode, "verified": not error, "error": error}
                for name in ("chain_total_ms", "receipt_goodput_per_s", "probe_completed", "principal_conserved"):
                    row[name] = report.get(name)
                for prefix, key in (("receipt", "samples_ms"), ("request_to_receipt", "request_to_receipt_ms")):
                    for suffix, fraction in (("p50", .5), ("p95", .95)):
                        row[f"{prefix}_{suffix}_ms"] = percentile(report.get(key, []), fraction)
                for kind, executable in (("server", "server"), ("client", "baseline_client")):
                    row[kind + "_thread_max"] = max((record["thread_max"] for record in item["processes"].values()
                        if Path(record["executable"]).name == executable), default=None)
                writer.writerow(row)
                csvfile.flush()
                print("END " + label + " " + (error or "verified"), flush=True)
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
