"""Check native fee-order observations; this is an empirical regression check."""
import argparse
import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import struct
import sys

BASE = Path(__file__).resolve().parent.parent / "c3-issuer-funded-2026-10-13"
sys.path.insert(0, str(BASE))
spec = importlib.util.spec_from_file_location("c3_fee_analysis", BASE / "analyze.py")
analysis = importlib.util.module_from_spec(spec)
spec.loader.exec_module(analysis)
CASES = {"source-first": "source-first", "child-first": "child-first-fulfilled",
         "compensated": "compensated-repaid"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def signed_section(payment):
    raw = base64.b64decode(payment["RequestRaw"], validate=True)
    require(len(raw) >= 6 and struct.unpack_from(">H", raw)[0] == 306, "unexpected native request encoding")
    length = struct.unpack_from(">I", raw, 2)[0]
    tx = raw[6:6 + length]
    require(len(tx) == length and len(tx) >= 16 and tx[:8] == b"UTXO5CH\0", "invalid transaction envelope")
    fixed, mutable = struct.unpack_from(">II", tx, 8)
    require(16 + fixed + mutable == len(tx), "invalid transaction section lengths")
    # The fixed section includes body, claims, commitments and owner signatures;
    # detached parent QC variants and mutable funding are deliberately excluded.
    return hashlib.sha256(tx[16:16 + fixed]).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True, help="new CSV/verification output directory")
    parser.add_argument("--repeats", type=int, default=3)
    args = parser.parse_args()
    if args.repeats < 1:
        parser.error("positive repeats required")
    root, output = args.root.resolve(), args.out.resolve()
    if output.exists():
        raise FileExistsError(output)
    plan = analysis.read(root / "orders-plan.json")
    statuses = [json.loads(line) for line in (root / "orders-status.jsonl").read_text().splitlines()]
    jobs = plan["jobs"]
    output.mkdir(parents=True)
    errors, stages, pairs, budgets, runs, checks, hashes = [], [], [], [], [], [], {}
    for filename in ("orders-plan.json", "orders-status.jsonl"):
        hashes[filename] = hashlib.sha256((root / filename).read_bytes()).hexdigest()
    expected = {(repeat, case) for repeat in range(1, args.repeats + 1) for case in CASES}
    actual = set()
    identities = {}
    by_name = {}
    for status in statuses:
        by_name.setdefault(status["name"], []).append(status)
    if len(jobs) != len(expected) or len({job["name"] for job in jobs}) != len(jobs):
        errors.append({"run": "matrix", "error": "declared job count/names differ from expected order matrix"})
    for job in jobs:
        name = job["name"]
        row = {"run": name, "repeat": job.get("repeat"), "passed": False}
        try:
            case = job["extra"][job["extra"].index("--case") + 1]
            row["case"] = case
            require((job["repeat"], case) not in actual, "duplicate round/case")
            actual.add((job["repeat"], case))
            require(case in CASES and job["workload"] == "fees" and job["variant"] == "c3", "unexpected case/workload/variant")
            require(job["extra"][job["extra"].index("--count") + 1] == "1", "unexpected planned pair count")
            status = by_name.get(name, [])
            require(len(status) == 1 and status[0]["returncode"] == 0, "missing/duplicate/nonzero runner status")
            directory = root / name
            paths = (directory / "result.json", directory / "reports/c3-fees.json")
            for path in paths:
                hashes[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
            result, native = (analysis.read(path) for path in paths)
            require(result["native_exit_code"] == 0 and not result.get("error"), "native failure/error")
            require(result["count"] == 1 and result["case"] == case, "runner count/case mismatch")
            require(native["Case"] == case and native["Count"] == 1 and not native.get("Error"), "native case/count/error mismatch")
            require(not native.get("FinalSnapshotError"), "final pool snapshot error")
            require(len(native["Pairs"]) == 1 and native["Pairs"][0] is not None, "incomplete native pairs")
            pair = native["Pairs"][0]
            replay = pair.get("Replay") or {}
            replay_status = replay.get("HTTPStatus")
            row.update(replay_error=pair.get("ReplayError", ""),
                       replay_evidence_error=replay.get("Error", ""),
                       replay_outcome=replay.get("Outcome", ""),
                       replay_http_status=replay_status,
                       replay_response_body=replay.get("ResponseBody", ""),
                       replay_scope="recorded HTTP rejection and unchanged economic snapshots; no ledger inclusion or execution-idempotence proof")
            require(not pair.get("Error") and not replay.get("Error"), "pair/replay transport or observation error")
            require(replay.get("Outcome") == "http-rejected" and type(replay_status) is int
                    and 400 <= replay_status <= 599, "replay lacks explicit HTTP rejection evidence")
            require(not pair.get("ReplayError") or pair["ReplayError"] ==
                    f"HTTP {replay_status}: {replay.get('ResponseBody', '')}", "replay error differs from recorded HTTP rejection")
            require(pair.get("ReplayBefore") is not None and pair.get("ReplayAfter") is not None
                    and pair["ReplayBefore"] == pair["ReplayAfter"], "missing replay snapshots or changed economic state/pool")
            a, b, c, d = analysis.fee_rows(name, native)
            for replay_field in ("replay_error", "replay_evidence_error", "replay_outcome",
                                 "replay_http_status", "replay_response_body", "replay_scope"):
                b[0][replay_field] = row[replay_field]
            stages.extend(a); pairs.extend(b); budgets.extend(c); runs.append(d)
            require(len(b) == 1 and b[0]["closed"] and b[0]["observed_order"] == CASES[case], "missing or unexpected closed order")
            require(d["closed_pairs"] == d["requested_pairs"] == 1 and not d["error"], "closed pair count/error")
            require(d["pool_reconciliation_available"] and not d["pool_snapshot_error"], "pool reconciliation unavailable")
            closed = next(stage for stage in pair["Stages"] if stage["Name"] == "closed")
            initial = {pool["Grant"]["ID"]: pool for pool in native["InitialPools"]}
            final = {pool["Grant"]["ID"]: pool for pool in native["FinalPools"]}
            require(len(initial) == len(final) == 2 and initial.keys() == final.keys(), "pool identities changed/missing")
            for pool in (*initial.values(), *final.values()):
                require(pool["Funding"] == pool["Free"] + pool["Held"] + pool["Spent"], "pool funding conservation failed")
            for role in ("Source", "Child"):
                prefix = role.lower()
                require(b[0][prefix + "_user_fee"] == 10 and b[0][prefix + "_quote"] == 10, "observed user fee/quote differs from 10")
                require(b[0][prefix + "_held"] == 0 and b[0][prefix + "_rewards"] == 2, "held budget or rewards mismatch")
                require(b[0][prefix + "_refund_checked"] is True, "refund existence not checked")
                snapshot = closed[role]
                pool_id = snapshot["Pool"]["Grant"]["ID"]
                require(pool_id in initial, "closed role has unknown issuer pool")
                before, after = initial[pool_id], final[pool_id]
                require(after["Held"] == 0, "issuer pool retains held budget")
                deltas = {field: after[field] - before[field] for field in
                          ("Spent", "Revenue", "CumulativeFees", "PoolRewards", "PoolBurned", "Released")}
                for field, payment_field in (("Revenue", "user_fee"), ("CumulativeFees", "user_fee"),
                                             ("PoolRewards", "rewards"), ("PoolBurned", "burned"), ("Released", "released")):
                    require(deltas[field] == b[0][prefix + "_" + payment_field], f"{role} issuer pool {field} reconciliation failed")
                require(deltas["Spent"] == deltas["PoolBurned"] + deltas["PoolRewards"], "spent differs from burned plus rewards")
                row.update({prefix + "_" + field + "_delta": value for field, value in deltas.items()})
                row.update({prefix + "_" + field: b[0][prefix + "_" + field]
                            for field in ("user_fee", "held", "rewards", "burned", "released")})
                row[prefix + "_tx"] = b[0][prefix + "_tx"]
                row[prefix + "_signed_fixed_sha256"] = signed_section(pair[role])
            require(b[0]["replay_unchanged"] is True, "missing replay observation or changed economic state")
            if case == "compensated":
                intermediate = [stage for stage in a if stage["stage"] == "compensated-before-source" and stage["role"] == "Source"]
                require(len(intermediate) == 1, "missing compensation-before-source observation")
                observed = intermediate[0]
                require((observed["burned"], observed["rewards"], observed["held"], observed["released"], observed["quote_collected"])
                        == (3, 2, 1, 0, False), "unpaid repayment budget observation changed")
            identities.setdefault(job["repeat"], []).append(row)
            row["passed"] = True
        except (OSError, ValueError, KeyError, IndexError, TypeError, StopIteration) as error:
            row["error"] = str(error)
            errors.append({"run": name, "error": str(error)})
        checks.append(row)
    if actual != expected:
        errors.append({"run": "matrix", "error": "missing/unexpected round/case combinations"})
    for repeat in range(1, args.repeats + 1):
        selected = identities.get(repeat, [])
        for role in ("source", "child"):
            for field in ("tx", "signed_fixed_sha256"):
                if len(selected) != 3 or len({row[role + "_" + field] for row in selected}) != 1:
                    errors.append({"run": f"round-{repeat}", "error": f"{role} {field} differs or a case is incomplete"})
    for filename, rows in (("fee_stages", stages), ("fee_pairs", pairs), ("budget_samples", budgets),
                           ("fee_runs", runs), ("fee_checks", checks), ("errors", errors)):
        analysis.write_csv(output / (filename + ".csv"), rows)
    verification = {"status": "PASS" if not errors else "FAIL", "scope": "native real-service observations; not a formal proof",
                    "replay_scope": "explicit HTTP rejection with unchanged complete escrow/pool snapshots; no ledger inclusion or execution-idempotence proof",
                    "expected_runs": len(expected), "checked_runs": len(checks), "errors": errors,
                    "source_sha256": hashes, "validator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                    "signed_identity": "reported TxID and SHA256 of owner-signed fixed transaction section"}
    (output / "verification.json").write_text(json.dumps(verification, indent=2) + "\n")
    print(json.dumps(verification, indent=2))
    return 0 if not errors else 1


if __name__ == "__main__":
    raise SystemExit(main())
