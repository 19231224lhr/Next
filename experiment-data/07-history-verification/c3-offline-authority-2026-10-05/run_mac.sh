#!/bin/sh
# Invoke from the fresh directory containing source.tgz and source-manifest.json.
set -eu
ROOT=$(pwd -P)
export PATH=/usr/local/go/bin:$PATH
export GOMAXPROCS=16
export GOGC=100
mkdir "$ROOT/source" "$ROOT/results"
tar -xzf "$ROOT/source.tgz" -C "$ROOT/source"
python3 - <<'PY'
import hashlib, json, pathlib
r = pathlib.Path.cwd()
m = json.loads((r/'source-manifest.json').read_text())
for name, evidence in m['files'].items():
    b = (r/'source'/name).read_bytes()
    assert len(b) == evidence['bytes'] and hashlib.sha256(b).hexdigest() == evidence['sha256'], name
print('Verified actual source files:', len(m['files']))
PY
{
  date -u
  uname -a
  go version
  sysctl -n hw.model hw.ncpu hw.memsize machdep.cpu.brand_string
  uptime
  printf 'GOMAXPROCS=%s GOGC=%s\n' "$GOMAXPROCS" "$GOGC"
} > "$ROOT/results/environment.txt"
cp "$ROOT/source-manifest.json" "$ROOT/results/source-manifest.json"
cd "$ROOT/source"
go test -tags comet_v3 -c -o "$ROOT/receipt.test" ./internal/redaction
shasum -a 256 "$ROOT/receipt.test" > "$ROOT/results/binary-sha256.txt"
cd internal/redaction
"$ROOT/receipt.test" -test.run 'Receipt' -test.count=1 -test.v > "$ROOT/results/correctness.log" 2>&1
for RUN in 1 2 3; do
  UTXO_RECEIPT_EXPERIMENT="$ROOT/results/run$RUN" "$ROOT/receipt.test" -test.run '^TestOfflineReceiptExperiment$' -test.count=1 -test.v > "$ROOT/results/run$RUN.log" 2>&1
  printf 'run%s completed\n' "$RUN"
done
cd "$ROOT"
tar -czf results.tgz results
printf 'Completed: %s/results.tgz\n' "$ROOT"
