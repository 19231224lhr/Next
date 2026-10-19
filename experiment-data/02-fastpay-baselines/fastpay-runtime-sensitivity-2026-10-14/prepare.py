"""Prepare an isolated runtime-only FastPay variant; never builds or runs it."""
import argparse
import difflib
import hashlib
import io
import json
from pathlib import Path
import subprocess
import zipfile

REVISION = "db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2"
ADAPTER_SHA256 = "c4b41d44ab3698c3613493359eab4851d347b177a5d136582e6ba5b8ba4f92f3"
HELPER = """
// Runtime-only sensitivity knob; unset retains the official runtime constructor.
fn sensitivity_runtime() -> std::io::Result<tokio::runtime::Runtime> {
    let value = match std::env::var("FASTPAY_RUNTIME_THREADS") {
        Ok(value) => value,
        Err(std::env::VarError::NotPresent) => return tokio::runtime::Runtime::new(),
        Err(_) => return Err(std::io::Error::new(std::io::ErrorKind::InvalidInput,
            "FASTPAY_RUNTIME_THREADS must be a positive integer")),
    };
    let threads = value.parse::<usize>().ok().filter(|n| *n > 0)
        .ok_or_else(|| std::io::Error::new(std::io::ErrorKind::InvalidInput,
            "FASTPAY_RUNTIME_THREADS must be a positive integer"))?;
    tokio::runtime::Builder::new().threaded_scheduler().core_threads(threads)
        .enable_all().build()
}

"""


def sha(data):
    return hashlib.sha256(data).hexdigest()


def git(source, *args):
    return subprocess.check_output(["git", "-C", str(source), *args])


def patch_runtime(raw, constructor):
    text = raw.decode("utf-8")
    if text.count(constructor) != 1 or text.count("fn main()") != 1:
        raise ValueError("unexpected source: runtime/main replacement must be unique")
    text = text.replace(constructor, "sensitivity_runtime()", 1)
    text = text.replace("fn main()", HELPER + "fn main()", 1)
    return text.encode("utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--adapter", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    source, adapter, destination = (p.resolve() for p in
        (args.source, args.adapter, args.destination))
    if destination.exists() or destination == source or source in destination.parents:
        raise ValueError("destination must be absent and outside the official source")
    revision = git(source, "rev-parse", "HEAD").decode().strip()
    if revision != REVISION:
        raise ValueError(f"unexpected upstream revision: {revision}")
    server_path = "fastpay/src/server.rs"
    server = git(source, "show", f"{revision}:{server_path}")
    if git(source, "-c", "core.autocrlf=true", "diff", "HEAD", "--", server_path):
        raise ValueError("official server source has local changes")
    adapter_raw = adapter.read_bytes()
    if sha(adapter_raw) != ADAPTER_SHA256:
        raise ValueError("adapter differs from the frozen receiver-confirmed source")
    lock = (source / "Cargo.lock").read_bytes()
    changes = {
        server_path: (server, patch_runtime(server, "Runtime::new()")),
        "fastpay/examples/baseline_client.rs":
            (adapter_raw, patch_runtime(adapter_raw, "tokio::runtime::Runtime::new()")),
    }
    archive = git(source, "archive", "--format=zip", revision)
    with zipfile.ZipFile(io.BytesIO(archive)) as zipped:
        for member in zipped.infolist():
            target = (destination / member.filename).resolve()
            if destination not in target.parents:
                raise ValueError("archive path escapes destination")
        zipped.extractall(destination)
    (destination / "Cargo.lock").write_bytes(lock)
    patch = []
    provenance = {"upstream_revision": revision, "official_source": str(source),
                  "adapter_source": str(adapter), "cargo_lock_sha256": sha(lock),
                  "archive_sha256": sha(archive), "files": {}}
    for path, (before, after) in changes.items():
        target = destination / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(after)
        provenance["files"][path] = {"original_sha256": sha(before), "variant_sha256": sha(after)}
        patch.extend(difflib.unified_diff(before.decode().splitlines(True),
                     after.decode().splitlines(True), fromfile="a/" + path, tofile="b/" + path))
    patch_raw = "".join(patch).encode()
    (destination / "runtime-only.patch").write_bytes(patch_raw)
    provenance["patch_sha256"] = sha(patch_raw)
    provenance["prepare_script_sha256"] = sha(Path(__file__).read_bytes())
    (destination / "runtime-provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    print(json.dumps(provenance, indent=2))


if __name__ == "__main__":
    main()
