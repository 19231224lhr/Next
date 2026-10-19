#!/usr/bin/env python3
"""Run ordinary Next's native single-host payment measurements on Linux."""

import argparse
import json
import math
import os
import pathlib
import random
import signal
import socket
import subprocess
import sys
import time


MEMORY_ENV = {
    "UTXO_EXPERIMENT_MEMBER_MEMORY": "1",
    "UTXO_EXPERIMENT_GATEWAY_MEMORY": "1",
    "UTXO_EXPERIMENT_COMMITTEE_MEMORY": "1",
    "UTXO_EXPERIMENT_MEM_BLOCKSTORE": "1",
}
STORAGE_KEYS = (*MEMORY_ENV, "UTXO_EXPERIMENT_MEMBER_SYNC")


def chain_result(report, length):
    hops = report.get("Hops") or []
    if report.get("Requested") != length or len(hops) != length:
        raise ValueError("incomplete native chain report")
    samples = []
    for i, hop in enumerate(hops):
        sent, ready = hop.get("SentUnixNS"), hop.get("ReadyUnixNS")
        if hop.get("Hop") != i + 1 or not positive_ms(hop.get("FastMS")) or not isinstance(sent, int) or not isinstance(ready, int) or sent <= 0 or ready <= sent:
            raise ValueError(f"hop {i + 1} lacks a verified receiver receipt")
        if not valid_output_id(hop.get("Input")) or not valid_output_id(hop.get("Output")):
            raise ValueError(f"hop {i + 1} has an invalid output identity")
        output = hop.get("OutputData") or {}
        if output.get("Asset") != 1 or output.get("Amount") != 100:
            raise ValueError(f"hop {i + 1} must transfer 100 CAL")
        if i and (hop.get("Input") != hops[i - 1].get("Output") or not hop.get("CertificateInput")):
            raise ValueError(f"hop {i + 1} did not spend the prior output certificate")
        samples.append(hop["FastMS"])
    if not positive_ms(report.get("FastChainMS")):
        raise ValueError("missing native chain total")
    error = report.get("Error") or ""
    return {"samples_ms": samples, "chain_total_ms": report["FastChainMS"], "completed": length,
            "receipt_status": "complete", "closure_status": "incomplete" if error else "complete",
            "closure_errors": [error] if error else []}


def independent_result(report, count):
    samples = report.get("Samples") or []
    if report.get("Summary", {}).get("count") != count or len(samples) != count:
        raise ValueError("incomplete native independent report")
    sent_times, lags, closure_errors, facts = [], [], [], set()
    for i, sample in enumerate(samples):
        sent, fast = sample.get("SentUnixNS"), sample.get("FastUnixNS")
        if sample.get("Index") != i or not positive_ms(sample.get("FastMS")) or not isinstance(sent, int) or not isinstance(fast, int) or sent <= 0 or fast <= sent:
            raise ValueError(f"sample {i + 1} lacks a verified receiver receipt")
        fact = sample.get("Fact")
        if not isinstance(fact, str) or len(fact) != 64 or any(char not in "0123456789abcdefABCDEF" for char in fact) or fact in facts:
            raise ValueError(f"sample {i + 1} has invalid receipt identity")
        facts.add(fact)
        lag = sample.get("DispatchLagMS", 0)
        if not isinstance(lag, (int, float)) or not math.isfinite(lag):
            raise ValueError(f"sample {i + 1} has invalid dispatch lag")
        sent_times.append(sent)
        lags.append(lag)
        if sample.get("Outcome") != "COMPLETE" or sample.get("Error"):
            closure_errors.append({"index": i, "error": sample.get("Error") or sample.get("Outcome") or "unknown"})
    span = (max(sent_times) - min(sent_times)) / 1e9 if count > 1 else 0
    if count > 1 and span <= 0:
        raise ValueError("native send timestamps did not advance")
    return {"samples_ms": [sample["FastMS"] for sample in samples], "completed": count,
            "receipt_status": "complete", "closure_status": "incomplete" if closure_errors else "complete",
            "closure_errors": closure_errors, "sent_unix_ns": sent_times, "dispatch_lag_ms": lags,
            "realized_send_rate_per_s": (count - 1) / span if span > 0 else None,
            "receipt_goodput_per_s": count * 1e9 / (max(sample["FastUnixNS"] for sample in samples) - min(sent_times))}


def positive_ms(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value) and value > 0


def valid_output_id(value):
    return isinstance(value, list) and len(value) == 32 and any(value) and all(type(byte) is int and 0 <= byte <= 255 for byte in value)


def run_command(argv, log_path, env, timeout=180, check=True):
    with log_path.open("w", encoding="utf-8") as log:
        process = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, env=env, start_new_session=True)
        try:
            code = process.wait(timeout=timeout)
        except BaseException:
            stop_process_group(process, signal.SIGTERM, 5)
            raise
    if check and code:
        raise RuntimeError(f"{argv[1]} exited {code}; see {log_path}")
    return code


def find_ports():
    for _ in range(100):
        base = random.randrange(20000, 58000)
        sockets = []
        try:
            for offset in (*range(8), *range(100, 104), *range(200, 204), 300, 301):
                sock = socket.socket()
                sock.bind(("127.0.0.1", base + offset))
                sockets.append(sock)
            return base
        except OSError:
            pass
        finally:
            for sock in sockets:
                sock.close()
    raise RuntimeError("could not find free loopback lab ports")


def wait_lab(process, log_path):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"lab-run exited {process.returncode}; see {log_path}")
        if log_path.exists() and "Laboratory running:" in log_path.read_text(encoding="utf-8", errors="replace"):
            return
        time.sleep(0.2)
    raise TimeoutError(f"lab-run did not become ready; see {log_path}")


def process_group_alive(pgid):
    for stat in pathlib.Path("/proc").glob("[0-9]*/stat"):
        try:
            fields = stat.read_text(encoding="utf-8").rsplit(")", 1)[1].split()
            if int(fields[2]) == pgid and fields[0] != "Z":
                return True
        except (OSError, IndexError, ValueError):
            pass
    return False


def signal_process_group(pgid, sig):
    try:
        os.killpg(pgid, sig)
    except ProcessLookupError:
        pass


def stop_process_group(process, sig, timeout):
    if process_group_alive(process.pid):
        signal_process_group(process.pid, sig)
    deadline = time.monotonic() + timeout
    while process_group_alive(process.pid) and time.monotonic() < deadline:
        time.sleep(0.1)
    if process_group_alive(process.pid):
        signal_process_group(process.pid, signal.SIGKILL)
    process.wait()
    deadline = time.monotonic() + 5
    while process_group_alive(process.pid) and time.monotonic() < deadline:
        time.sleep(0.1)
    if process_group_alive(process.pid):
        raise RuntimeError(f"spawned process group {process.pid} did not stop")


def stop_lab(process):
    if process.poll() is None:
        process.send_signal(signal.SIGINT)
    deadline = time.monotonic() + 30
    while process.poll() is None and process_group_alive(process.pid) and time.monotonic() < deadline:
        time.sleep(0.1)
    if process_group_alive(process.pid):
        stop_process_group(process, signal.SIGTERM, 5)
    else:
        process.wait()


def filesystem_type(path):
    parent = path.resolve().parent
    matches = []
    for line in pathlib.Path("/proc/mounts").read_text(encoding="utf-8").splitlines():
        fields = line.split()
        mount = pathlib.Path(fields[1].replace("\\040", " "))
        if mount == parent or mount in parent.parents:
            matches.append((len(str(mount)), fields[2]))
    return max(matches)[1] if matches else "unknown"


def run(args):
    if sys.platform != "linux":
        raise RuntimeError("run_next.py must run under Linux")
    if args.length < 1 or args.length > 100 or args.count < 1 or args.count > 30000:
        raise ValueError("length must be 1..100 and count must be 1..30000")
    if not math.isfinite(args.rate) or args.rate < 0 or args.rate > 100000 or (args.workload == "chain" and args.rate):
        raise ValueError("rate must be 0..100000 for independent payments; chain is dependency paced")
    binary = args.bin.resolve()
    for name in ("payctl", "member", "gateway", "committee"):
        if not (binary / name).is_file():
            raise FileNotFoundError(binary / name)
    directory = args.run_dir.resolve()
    if directory.exists():
        raise FileExistsError(f"run directory must be fresh: {directory}")
    directory.parent.mkdir(parents=True, exist_ok=True)
    fs_type = filesystem_type(directory)
    if fs_type != "ext4":
        raise ValueError(f"run directory must be on Linux ext4, got {fs_type}: {directory}")
    logs = directory / "adapter-logs"
    env = os.environ.copy()
    for key in STORAGE_KEYS:
        env.pop(key, None)
    if args.storage == "memory":
        env.update(MEMORY_ENV)
    else:
        env["UTXO_EXPERIMENT_MEMBER_SYNC"] = "1"
    payctl = str(binary / "payctl")
    output_count = args.count if args.workload == "independent" else 1
    init_log = directory.parent / f"{directory.name}-init-lab.log"
    run_command([payctl, "init-lab", "--dir", str(directory), "--v4", "--outputs", str(output_count), "--port", str(find_ports())], init_log, env)
    logs.mkdir()
    init_log.replace(logs / "init-lab.log")
    run_command([payctl, "chain-v4", "--dir", str(directory), "--prepare"], logs / "prepare-chain.log", env)

    # The second owner starts with no CAL principal. The chain can only spend
    # the certified output actually delivered by the preceding payment.
    network_path = directory / "config" / "network.json"
    network = json.loads(network_path.read_text(encoding="utf-8"))
    owner1 = list((directory / "keys" / "owner1.key").read_bytes()[-32:])
    outputs = network["Genesis"]["Outputs"]
    network["Genesis"]["Outputs"] = [o for o in outputs if o["Output"]["Recipient"]["Owner"] != owner1]
    if len(outputs) - len(network["Genesis"]["Outputs"]) != output_count:
        raise ValueError("expected to remove every second-owner genesis CAL output")
    network_path.write_text(json.dumps(network, indent=2) + "\n", encoding="utf-8")

    lab_path = directory / "lab.json"
    lab = json.loads(lab_path.read_text(encoding="utf-8"))
    (logs / "native-node-inventory.json").write_text(json.dumps(lab["Nodes"], indent=2) + "\n", encoding="utf-8")
    lab["Nodes"] = [node for node in lab["Nodes"] if not node["Name"].startswith("org1-member") and node["Name"] != "gateway1"]
    lab_path.write_text(json.dumps(lab, indent=2) + "\n", encoding="utf-8")
    nodes = lab["Nodes"]
    counts = {name: sum(node["Binary"] == name for node in nodes) for name in ("member", "gateway", "committee")}
    if counts != {"member": 4, "gateway": 1, "committee": 4}:
        raise ValueError(f"unexpected native laboratory deployment: {counts}")
    lab_log_path = logs / "lab-run.log"
    with lab_log_path.open("w", encoding="utf-8") as lab_log:
        process = subprocess.Popen([payctl, "lab-run", "--dir", str(directory), "--bin", str(binary)], stdout=lab_log, stderr=subprocess.STDOUT, env=env, start_new_session=True)
        try:
            wait_lab(process, lab_log_path)
            if args.workload == "chain":
                exit_code = run_command([payctl, "chain-v4", "--dir", str(directory), "--length", str(args.length)], logs / "chain-v4.log", env, timeout=420, check=False)
                native_path = directory / "reports" / "chain-v4.json"
                result = chain_result(json.loads(native_path.read_text(encoding="utf-8")), args.length)
            else:
                bench = [payctl, "bench-v4", "--dir", str(directory), "--start", "0", "--count", str(args.count), "--concurrency", "1", "--max-pending", str(args.count), "--same-org"]
                if args.storage == "memory":
                    bench.append("--wallet-no-sync")
                if args.rate:
                    bench.extend(("--rate", str(args.rate)))
                exit_code = run_command(bench, logs / "bench-v4.log", env, timeout=420, check=False)
                native_path = directory / "reports" / "bench-v4-0.json"
                result = independent_result(json.loads(native_path.read_text(encoding="utf-8")), args.count)
            if exit_code and result["closure_status"] == "complete":
                raise RuntimeError(f"native client exited {exit_code} despite complete report; see {logs}")
        finally:
            stop_lab(process)
    result.update({
        "system": "Next", "workload": args.workload, "storage": args.storage,
        "run_filesystem": fs_type, "receiver_genesis_cal_outputs": 0,
        "endpoint": "payer native monotonic time immediately before gateway HTTP POST through receiving wallet ReceiveDirect certificate verification and acceptance",
        "client_construction_signing_included": False,
        "native_report": str(native_path), "native_exit_code": exit_code, "run_dir": str(directory),
        "processes": counts | {"total_servers": len(nodes), "client": 1, "lab_supervisor": 1},
        "deployment": "ordinary same-organization transfer, four routed members, four committee services, one gateway; second native organization remains in network config but its idle member/gateway processes are not launched",
        "amount_cal": 100, "target_send_rate": args.rate if args.workload == "independent" else None,
        "max_pending": args.count if args.workload == "independent" else None,
        "storage_detail": "experimental in-memory member/gateway/committee application stores and Comet blockstore/state/evidence MemDB; other Comet artifacts may use disk; client wallet no-sync Bolt" if args.storage == "memory" else ("synchronous member/gateway/committee stores and Comet disk; chain wallet remains no-sync Bolt" if args.workload == "chain" else "synchronous member/gateway/committee and receiver wallet stores with Comet disk"),
    })
    return result


def main():
    def terminated(_signal, _frame):
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        raise InterruptedError("adapter received SIGTERM")

    signal.signal(signal.SIGTERM, terminated)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin", type=pathlib.Path, required=True)
    parser.add_argument("--run-dir", type=pathlib.Path, required=True)
    parser.add_argument("--workload", choices=("chain", "independent"), required=True)
    parser.add_argument("--length", type=int, required=True)
    parser.add_argument("--count", type=int, required=True)
    parser.add_argument("--storage", choices=("memory", "sync"), required=True)
    parser.add_argument("--rate", type=float, default=0.0, help="optional independent payment send rate per second")
    args = parser.parse_args()
    print(json.dumps(run(args), sort_keys=True))


if __name__ == "__main__":
    main()
