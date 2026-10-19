"""Freeze the actual Go source for the offline receipt experiment (no Git mutations)."""
import hashlib
import json
import pathlib
import subprocess
import sys
import tarfile

root = pathlib.Path(__file__).resolve().parents[3]
out = pathlib.Path(sys.argv[1]).resolve()
out.mkdir(parents=True, exist_ok=False)
files = [root / "go.mod", root / "go.sum"]
for folder in ("cmd", "crypto", "finality", "internal", "protocol", ".scratch/comet-src"):
    for path in (root / folder).rglob("*"):
        if path.is_file() and ".git" not in path.parts and not any(p in path.parts for p in ("node_modules", "__pycache__")):
            if path.suffix in (".go", ".mod", ".sum", ".proto", ".tmpl") or "testdata" in path.parts:
                files.append(path)
files = sorted(set(files))
manifest = {
    "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
    "scope": "actual on-disk Go source, dependencies and test fixtures; includes uncommitted experiment changes",
    "files": {p.relative_to(root).as_posix(): {"bytes": p.stat().st_size, "sha256": hashlib.sha256(p.read_bytes()).hexdigest()} for p in files},
}
(out / "source-manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
with tarfile.open(out / "source.tgz", "w:gz") as archive:
    for path in files:
        archive.add(path, arcname=path.relative_to(root).as_posix(), recursive=False)
print(out)
print(f"{len(files)} files; archive {(out / 'source.tgz').stat().st_size} bytes")
