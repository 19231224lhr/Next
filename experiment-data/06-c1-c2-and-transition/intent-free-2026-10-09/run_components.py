"""Refresh C3 components from a frozen v5 archive, after all major jobs pass.

This driver lives outside the extracted source and never edits that source.
Use --plan to inspect commands without running a build or an experiment.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import time


MAJOR_JOBS = {"single", "cross", "mixed", "capital"}
REDACTION_CHECKS = [
    "TestHistoricalReaderEquivalence",
    "TestHistoricalExportConsumer",
    "TestHistoricalReceiptRealExecution",
    "TestHistoricalReceiptRejectsAdversarialEvidence",
    "TestHistoricalReceiptRejectsRealNoopAndFailure",
    "TestLeanHistoricalAuthorization",
    "TestCompensationReceiptPrecedesRepresentation",
    "TestCompensationReceiptRejectsInvalidEconomicEvidence",
    "TestCompensationReceiptsShareBlockAuthentication",
    "TestOfflineReceiptMatchedRows",
    "TestOfflineReceiptRejectsInvalidAndCachesOnlyVerified",
    "TestOfflineReceiptIndependentReceiver",
    "TestOfflineReceiptTransportAndCacheIdentity",
    "TestAtomicRepairBatchTwoInputs",
    "TestRepairBatchAcrossParts",
    "TestMaterializationInterleavedQueue",
    "TestMaterializationRevisionOrderAndCursorReplay",
]
COMMITTEE_CHECKS = ["TestDirectVerificationCacheConcurrentAndFrozen"]
EXPERIMENT_ENV = (
    "UTXO_READER_RESULTS", "UTXO_RECEIPT_EXPERIMENT", "UTXO_HISTORY_EXPORT",
    "CH_COST_OUT", "CH_COST_FIXTURE", "UTXO_OFFLINE_RECEIVER_CONFIG",
    "UTXO_OFFLINE_RECEIVER_PACKET", "UTXO_OFFLINE_RECEIVER_METHOD",
    "UTXO_OFFLINE_RECEIVER_EXPECTED", "UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE",
)


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def write_json(path, value):
    Path(path).write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def regex(names):
    return "^(" + "|".join(re.escape(n) for n in names) + ")$"


def verify_source(source, manifest):
    for name, entry in manifest["files"].items():
        path = (source / name).resolve()
        path.relative_to(source.resolve())
        raw = path.read_bytes()
        if len(raw) != entry["bytes"] or hashlib.sha256(raw).hexdigest() != entry["sha256"]:
            raise RuntimeError("frozen source fingerprint mismatch: " + name)
    return len(manifest["files"])


def fixture_selection(candidate):
    """Screen old/malformed wire formats; Go still verifies accepted fixtures."""
    default = {"source": "cacheFixture(t, 256) from frozen current source", "path": None}
    if candidate is None:
        return default
    candidate = Path(candidate).resolve()
    try:
        rows = json.loads(candidate.read_text(encoding="utf-8"))
        if not isinstance(rows, list) or len(rows) != 256:
            raise ValueError("expected 256 base64 submissions")
        for index, row in enumerate(rows):
            raw = base64.b64decode(row, validate=True)
            if len(raw) < 16 or raw[:8] != b"UTXO5CH\0":
                raise ValueError(f"submission {index}: not a v5 direct envelope")
            fixed_len, mutable_len = struct.unpack(">II", raw[8:16])
            if len(raw) != 16 + fixed_len + mutable_len:
                raise ValueError(f"submission {index}: inconsistent envelope lengths")
            fixed = raw[16:16 + fixed_len]
            if len(fixed) < 6 or struct.unpack(">H", fixed[:2])[0] != 406:
                raise ValueError(f"submission {index}: not DirectSubmission")
            tx_len = struct.unpack(">I", fixed[2:6])[0]
            tx_fixed = fixed[6:6 + tx_len]
            if len(tx_fixed) != tx_len or len(tx_fixed) < 8:
                raise ValueError(f"submission {index}: incomplete fixed transaction")
            core_len = struct.unpack(">I", tx_fixed[:4])[0]
            core = tx_fixed[4:4 + core_len]
            if len(core) != core_len or len(core) < 4:
                raise ValueError(f"submission {index}: incomplete transaction core")
            body_len = struct.unpack(">I", core[:4])[0]
            body = core[4:4 + body_len]
            if len(body) != body_len or len(body) < 18 or struct.unpack(">HQQ", body[:18]) != (1, 5, 5):
                raise ValueError(f"submission {index}: incompatible payment body version")
        return {"source": "optional v5 fixture; production decoding and initial CH verification required",
                "path": str(candidate), "sha256": sha256(candidate), "submissions": len(rows)}
    except (OSError, ValueError, TypeError, struct.error) as err:
        default.update(skipped_candidate=str(candidate), skipped_reason=str(err))
        return default


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", type=Path, default=Path("/Users/richz/projects"))
    parser.add_argument("--prefix", default="intent-free-20261009-final")
    parser.add_argument("--root", type=Path, help="fresh component root; default BASE/PREFIX-components")
    parser.add_argument("--archive", type=Path)
    parser.add_argument("--freeze-record", type=Path)
    parser.add_argument("--progress", type=Path, help="major-run progress record")
    parser.add_argument("--ch-fixture", type=Path, help="optional saved submissions; incompatible wire formats are skipped")
    parser.add_argument("--plan", action="store_true")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", args.prefix):
        parser.error("prefix must be a simple directory-name prefix")
    base = args.base.resolve()
    root = (args.root or base / (args.prefix + "-components")).resolve()
    archive = (args.archive or base / (args.prefix + "-source-v5.tar.gz")).resolve()
    freeze_path = (args.freeze_record or base / (args.prefix + "-freeze-record.json")).resolve()
    progress_path = (args.progress or base / (args.prefix + "-progress.json")).resolve()
    source, results, binaries = root / "source", root / "results", root / "bin"
    redaction, committee = binaries / "redaction.test", binaries / "committee.test"
    plan = {
        "root": str(root), "archive": str(archive), "freeze_record": str(freeze_path),
        "major_progress": str(progress_path), "protocol": 5,
        "start_guard": "all single/cross/mixed/capital jobs passed; identical frozen archive SHA-256",
        "build": [
            ["go", "test", "-tags=comet_v3", "-c", "-o", str(redaction), "./internal/redaction"],
            ["go", "test", "-tags=comet_v3", "-c", "-o", str(committee), "./internal/committee"],
        ],
        "functional_redaction_tests": REDACTION_CHECKS,
        "functional_committee_tests": COMMITTEE_CHECKS,
        "reader": {"test": "TestHistoricalReaderExperiment", "runs": 3, "env": "UTXO_READER_RESULTS",
                   "output": str(results / "reader" / "run{1,2,3}"), "gomaxprocs": 16,
                   "configs": [[32, 1], [32, 8], [256, 1], [256, 8]], "queries_per_case": 300, "warmups": 20},
        "receipt": {"test": "TestOfflineReceiptExperiment", "runs": 3, "env": "UTXO_RECEIPT_EXPERIMENT",
                    "output": str(results / "receipt" / "run{1,2,3}"), "gomaxprocs": 16,
                    "cold_samples_per_method": 30, "cached_samples_per_method": 100, "warmups": 5},
        "ch": {"test": "TestCHCostMeasurement", "env": "CH_COST_OUT", "output": str(results / "ch"),
               "gomaxprocs": 1, "rounds": 5, "samples_per_operation_per_round": 1000, "warmups": 100,
               "fixture": fixture_selection(args.ch_fixture)},
    }
    if args.plan:
        print(json.dumps(plan, indent=2))
        return
    if platform.system() != "Darwin":
        raise RuntimeError("formal component timing must run on the designated Mac; use --plan elsewhere")
    freeze = json.loads(freeze_path.read_text(encoding="utf-8"))
    major = json.loads(progress_path.read_text(encoding="utf-8"))
    rows = major.get("jobs", [])
    if {r["name"] for r in rows} != MAJOR_JOBS or any(r["status"] != "passed" for r in rows):
        raise RuntimeError("major runs are incomplete or failed; refusing concurrent component measurement")
    archive_hash = sha256(archive)
    if archive_hash != freeze["archive_sha256"] or archive_hash != major["archive_sha256"]:
        raise RuntimeError("archive does not match both the final freeze record and major runs")
    if archive.stat().st_size != freeze["bytes"]:
        raise RuntimeError("frozen archive size mismatch")
    root.mkdir(parents=True, exist_ok=False)
    source.mkdir()
    results.mkdir()
    binaries.mkdir()
    with tarfile.open(archive) as tar:
        for member in tar.getmembers():
            (source / member.name).resolve().relative_to(source)
            if not member.isfile() and not member.isdir():
                raise ValueError("unexpected archive link or special entry")
        tar.extractall(source)
    manifest_path = source / "source-manifest.json"
    if sha256(manifest_path) != freeze["manifest_sha256"]:
        raise RuntimeError("embedded source manifest does not match final freeze record")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest["protocol"] != 5:
        raise RuntimeError("component refresh requires protocol v5")
    verify_source(source, manifest)
    shutil.copyfile(manifest_path, results / "source-manifest.json")
    shutil.copyfile(freeze_path, results / "freeze-record.json")
    env = dict(os.environ)
    env["PATH"] = "/usr/local/go/bin:" + env.get("PATH", "")
    env.update(GOMAXPROCS="16", GOGC="100")
    for name in EXPERIMENT_ENV:
        env.pop(name, None)
    state = {"schema": "next-v5-component-refresh-1", "protocol": 5, "status": "running",
             "start_unix": time.time(), "source_archive_sha256": archive_hash,
             "source_manifest_sha256": freeze["manifest_sha256"], "git_head_reference": manifest["git_head_reference"],
             "major_progress": str(progress_path), "plan": plan, "commands": [], "binaries": {},
             "driver_sha256": {Path(__file__).name: sha256(__file__),
                               "analyze_components.py": sha256(Path(__file__).with_name("analyze_components.py"))},
             "reader_legacy_build_field": "Hardcoded 721800c-production is retained verbatim in raw reports; this record identifies the actual build.",
             "archived_series": "Older network delay/fault runs are not refreshed or relabeled by this runner."}

    def save():
        write_json(results / "provenance.json", state)

    def command(argv, cwd, log, extra=None, required_tests=()):
        effective = dict(env)
        effective.update(extra or {})
        row = {"argv": [str(x) for x in argv], "cwd": str(cwd), "log": str(log.relative_to(results)),
               "env": {"GOMAXPROCS": effective["GOMAXPROCS"], "GOGC": effective["GOGC"], **(extra or {})},
               "start_unix": time.time(), "status": "running"}
        state["commands"].append(row)
        save()
        with log.open("w", encoding="utf-8") as handle:
            ran = subprocess.run(argv, cwd=cwd, env=effective, stdout=handle, stderr=subprocess.STDOUT)
        row.update(returncode=ran.returncode, end_unix=time.time(), status="passed" if ran.returncode == 0 else "failed")
        if ran.returncode == 0 and required_tests:
            passed = set(re.findall(r"^--- PASS: (\w+) ", log.read_text(encoding="utf-8"), re.M))
            missing = sorted(set(required_tests) - passed)
            if missing:
                row.update(status="failed", missing_pass_records=missing)
        save()
        if row["status"] != "passed":
            raise RuntimeError("command failed or required tests were skipped: " + str(log))
        print("PASS", log.name, flush=True)

    try:
        environment = {"platform": platform.platform(), "machine": platform.machine(),
                       "gomaxprocs_reader_receipt": 16, "gomaxprocs_ch": 1, "gogc": 100}
        for key, argv in [("go_version", ["go", "version"]), ("macos", ["sw_vers"]),
                          ("hardware", ["sysctl", "-n", "hw.model", "hw.ncpu", "hw.memsize", "machdep.cpu.brand_string"]),
                          ("uptime", ["uptime"])]:
            environment[key] = subprocess.check_output(argv, env=env, text=True).strip()
        state["environment"] = environment
        save()
        for name, argv in zip(("redaction", "committee"), plan["build"]):
            command(argv, source, results / ("build-" + name + ".log"))
            state["binaries"][name] = {"sha256": sha256(binaries / (name + ".test")), "bytes": (binaries / (name + ".test")).stat().st_size}
        command([str(redaction), "-test.run", regex(REDACTION_CHECKS), "-test.count=1", "-test.v"],
                source / "internal/redaction", results / "functional-redaction.log",
                {"UTXO_HISTORY_EXPORT": str(results / "functional-history-export.json")}, REDACTION_CHECKS)
        command([str(committee), "-test.run", regex(COMMITTEE_CHECKS), "-test.count=1", "-test.v"],
                source / "internal/committee", results / "functional-committee.log", required_tests=COMMITTEE_CHECKS)
        for folder, test, variable in [("reader", "TestHistoricalReaderExperiment", "UTXO_READER_RESULTS"),
                                       ("receipt", "TestOfflineReceiptExperiment", "UTXO_RECEIPT_EXPERIMENT")]:
            destination = results / folder
            destination.mkdir()
            for run in range(1, 4):
                path = destination / ("run" + str(run))
                command([str(redaction), "-test.run", "^" + test + "$", "-test.count=1", "-test.v"],
                        source / "internal/redaction", destination / ("run" + str(run) + ".log"),
                        {variable: str(path)}, [test])
        ch_env = {"CH_COST_OUT": str(results / "ch"), "GOMAXPROCS": "1"}
        if plan["ch"]["fixture"]["path"]:
            ch_env["CH_COST_FIXTURE"] = plan["ch"]["fixture"]["path"]
        command([str(committee), "-test.run", "^TestCHCostMeasurement$", "-test.count=1", "-test.v"],
                source / "internal/committee", results / "ch.log", ch_env, ["TestCHCostMeasurement"])
        state["ch_fixture_sha256"] = sha256(results / "ch" / "fixture-submissions.json")
        state["source_files_verified_after"] = verify_source(source, manifest)
        state.update(status="passed", end_unix=time.time())
        save()
    except Exception as err:
        state.update(status="failed", error=str(err), end_unix=time.time())
        state["source_files_verified_after"] = verify_source(source, manifest)
        save()
        raise
    print("Component refresh completed:", results, flush=True)
    print("Analyze with:", sys.executable, Path(__file__).with_name("analyze_components.py"), results, flush=True)


if __name__ == "__main__":
    main()
