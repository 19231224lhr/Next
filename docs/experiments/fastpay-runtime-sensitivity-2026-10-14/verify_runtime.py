"""Release tests and invalid runtime-setting checks; no benchmark execution."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(root):
    return {str(path.relative_to(root)): sha(path) for path in root.rglob("*") if path.is_file()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--bin", type=Path, required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    for name in ("source", "bin", "config", "output"):
        setattr(args, name, getattr(args, name).resolve())
    args.output.mkdir(parents=True, exist_ok=False)
    tools = Path(os.environ.get("NEXT_BASELINE_TOOLS", str(Path.home() / ".local/share/next-baseline-tools")))
    env = dict(os.environ, CARGO_HOME=str(tools / "cargo"), RUSTUP_HOME=str(tools / "rustup"),
               CARGO_TARGET_DIR=str(args.bin.parent), RUSTFLAGS="--cap-lints=allow")
    env["PATH"] = str(tools / "cargo/bin") + os.pathsep + env["PATH"]
    env.pop("FASTPAY_RUNTIME_THREADS", None)
    records = []

    def execute(name, command, runenv, timeout):
        record = {"name": name, "command": command, "cwd": str(args.source),
                  "start_ns": time.time_ns(), "runtime_threads": runenv.get("FASTPAY_RUNTIME_THREADS")}
        with (args.output / (name + ".stdout")).open("w") as stdout, \
                (args.output / (name + ".stderr")).open("w") as stderr:
            run = subprocess.run(command, cwd=args.source, env=runenv, stdout=stdout, stderr=stderr, timeout=timeout)
        record.update(exit=run.returncode, end_ns=time.time_ns())
        records.append(record)
        (args.output / "commands.json").write_text(json.dumps(records, indent=2) + "\n")
        return run.returncode

    result = {"source": str(args.source), "config": str(args.config),
              "environment": {name: env[name] for name in
                  ("CARGO_HOME", "RUSTUP_HOME", "CARGO_TARGET_DIR", "RUSTFLAGS")},
              "source_hashes": snapshot(args.source / "fastpay/src"),
              "adapter_sha256": sha(args.source / "fastpay/examples/baseline_client.rs"),
              "lock_sha256": sha(args.source / "Cargo.lock"),
              "runtime_provenance": json.loads((args.source / "runtime-provenance.json").read_text()),
              "binary_hashes": {name: sha(args.bin / name) for name in ("server", "examples/baseline_client")},
              "checks": {}}
    test_exit = execute("release-tests", [str(tools / "cargo/bin/cargo"), "+1.99.0", "test", "--locked",
        "--release", "-p", "fastpay", "--lib", "--example", "baseline_client"], env, 900)
    result["checks"]["release_tests"] = test_exit == 0
    before = snapshot(args.config)
    for name, setting in (("zero", "0"), ("text", "not-a-number"), ("overflow", "18446744073709551616")):
        command = [str(args.bin / "examples/baseline_client"), "--dir", str(args.config),
                   "--workload", "chain", "--count", "100", "--amount", "100", "--rate", "0",
                   "--client-mode", "receiver-confirmed"]
        exitcode = execute("invalid-" + name, command, dict(env, FASTPAY_RUNTIME_THREADS=setting), 15)
        error = (args.output / ("invalid-" + name + ".stderr")).read_text()
        result["checks"]["invalid_" + name] = exitcode != 0 and "FASTPAY_RUNTIME_THREADS must be a positive integer" in error
    result["checks"]["config_unchanged"] = snapshot(args.config) == before
    result["checks"]["source_unchanged"] = snapshot(args.source / "fastpay/src") == result["source_hashes"]
    result["checks"]["binaries_unchanged"] = all(sha(args.bin / name) == value for name, value in result["binary_hashes"].items())
    result["ready"] = all(result["checks"].values())
    result["finished_ns"] = time.time_ns()
    (args.output / "verification.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result["checks"], indent=2), flush=True)
    return 0 if result["ready"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
