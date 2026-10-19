"""Export allowlisted experiment evidence, excluding configs, keys and state."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import time

BLOCKED_DIRS = {"keys", "config", "configs", "state", "db", "data", "genesis", "fixtures",
                "bin", "binaries", "target", "node_modules", ".git", "keystore"}
BLOCKED_NAME = re.compile(r"(?:key|secret|private|config|genesis|fixture|state|database|\.db(?:\.|$)|\.pem$|\.key$)", re.I)
CONFIG_NAMES = {"accounts.json", "roles.json", "committee.json", "initial_accounts.txt", "network.json", "lab.json"}
NODE_CONFIG = re.compile(r"(?:server\d*|committee\d*|gateway\d*|org\d+-member\d+)\.json$", re.I)
DIAGNOSTIC_DIRS = {"profiles", "profile", "diagnostics"}


def allowed(relative, diagnostics):
    parts = relative.parts
    name = relative.name
    if any(part.lower() in BLOCKED_DIRS for part in parts[:-1]) or BLOCKED_NAME.search(name):
        return False
    if name.lower() in CONFIG_NAMES or NODE_CONFIG.fullmatch(name):
        return False
    diagnostic_path = any(part.lower() in DIAGNOSTIC_DIRS for part in parts[:-1])
    if diagnostic_path and not diagnostics:
        return False
    if relative.suffix.lower() in (".log", ".stdout", ".stderr"):
        return True
    if name == "status.jsonl" or name in ("orders-status.jsonl", "delays-status.jsonl"):
        return True
    if relative.suffix.lower() not in (".json", ".csv"):
        return False
    if any(part.lower() in ("reports", "results") for part in parts[:-1]):
        return True
    # Dataset root / run root metadata only; never recurse into service storage.
    return len(parts) <= 3 or diagnostic_path


def load_json(path):
    try:
        return json.loads(path.read_text()), None
    except (OSError, ValueError) as error:
        return None, f"{type(error).__name__}: {error}"


def dataset_metadata(root, directory):
    result = {"path": directory.relative_to(root).as_posix(), "status": "unclassified",
              "diagnostic": None, "errors": [], "protocol_completion": "consult exported raw reports"}
    plan_path = directory / "plan.json"
    plan, error = load_json(plan_path) if plan_path.exists() else (None, None)
    if error:
        result["errors"].append(error)
    if isinstance(plan, dict):
        arguments = plan.get("arguments", {})
        explicit = plan.get("diagnostic", arguments.get("trace") if isinstance(arguments, dict) else None)
        if explicit is not None:
            result["diagnostic"] = str(explicit).lower() in ("true", "1", "yes")
        jobs = plan.get("jobs")
        if isinstance(jobs, list):
            result["planned_jobs"] = len(jobs)
            records = []
            status_path = directory / "status.jsonl"
            if status_path.exists():
                for number, line in enumerate(status_path.read_text().splitlines(), 1):
                    try:
                        records.append(json.loads(line))
                    except ValueError:
                        result["errors"].append(f"status.jsonl line {number} is incomplete or malformed")
            result["recorded_jobs"] = len(records)
            result["failed_jobs"] = sum(record.get("exit") not in (0, None) or bool(record.get("error")) for record in records)
            def identity(job):
                return (job.get("repeat", job.get("trial")), job.get("arm"))
            expected = {identity(job) for job in jobs}
            observed = {identity(record) for record in records if record.get("exit") is not None}
            if result["failed_jobs"]:
                result["status"] = "recorded_failure"
            elif result["errors"] or not expected.issubset(observed):
                result["status"] = "incomplete_or_unrecorded"
            elif jobs and all(record.get("exit") == 0 for record in records):
                result["status"] = "all_planned_processes_recorded_successful"
            else:
                result["status"] = "unclassified"
    # Do not infer completion or diagnostic status from a stage/pilot directory name.
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="WSL experiment root")
    parser.add_argument("--output", type=Path, required=True, help="new Windows-mounted output directory")
    parser.add_argument("--include-diagnostics", action="store_true", help="include diagnostic JSON/CSV, never raw binary profiles")
    parser.add_argument("--dry-run", action="store_true", help="print inventory without writing/copying")
    args = parser.parse_args()
    source, output = args.source.resolve(), args.output.resolve()
    if not source.is_dir() or source == output or source in output.parents or output in source.parents:
        parser.error("source must exist; output must be disjoint from source")
    if output.exists() and not args.dry_run:
        raise FileExistsError(output)
    candidates = []
    for directory, subdirs, names in os.walk(source, followlinks=False):
        parent = Path(directory)
        subdirs[:] = [name for name in subdirs if name.lower() not in BLOCKED_DIRS
                      and not (parent / name).is_symlink()]
        for name in names:
            path = parent / name
            relative = path.relative_to(source)
            if not path.is_symlink() and allowed(relative, args.include_diagnostics):
                candidates.append((path, relative))
    candidates.sort(key=lambda item: item[1].as_posix())
    datasets = sorted({source / relative.parts[0] for _, relative in candidates if len(relative.parts) > 1})
    manifest = {"source": str(source), "created_ns": time.time_ns(), "dry_run": args.dry_run,
                "include_diagnostics": args.include_diagnostics,
                "completion_policy": "process statuses come from plan/status; acceptance endpoints remain in raw reports",
                "datasets": [dataset_metadata(source, directory) for directory in datasets], "files": []}
    if not args.dry_run:
        output.mkdir(parents=True)
    for path, relative in candidates:
        before = path.stat()
        if args.dry_run:
            manifest["files"].append({"path": relative.as_posix(), "bytes": before.st_size})
            continue
        # Hash exactly the bytes written; metadata records changes during export.
        destination = output / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        digest = hashlib.sha256()
        size = 0
        with path.open("rb") as incoming, destination.open("xb") as outgoing:
            for block in iter(lambda: incoming.read(1 << 20), b""):
                outgoing.write(block)
                digest.update(block)
                size += len(block)
        after = path.stat()
        manifest["files"].append({"path": relative.as_posix(), "bytes": size,
            "sha256": digest.hexdigest(), "source_changed_during_copy":
                (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns)})
    manifest["file_count"] = len(manifest["files"])
    manifest["bytes"] = sum(item["bytes"] for item in manifest["files"])
    if args.dry_run:
        print(json.dumps(manifest, indent=2))
    else:
        raw = (json.dumps(manifest, indent=2) + "\n").encode()
        (output / "export-manifest.json").write_bytes(raw)
        (output / "export-manifest.sha256").write_text(hashlib.sha256(raw).hexdigest() + "  export-manifest.json\n")
        print(json.dumps({"output": str(output), "files": manifest["file_count"], "bytes": manifest["bytes"]}))


if __name__ == "__main__":
    main()
