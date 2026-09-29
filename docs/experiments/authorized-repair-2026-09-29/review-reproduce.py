"""Reproduce the 3bb9ceb review findings without editing production or test sources.

Historical reproducer: run only against a clean 3bb9ceb checkout. Two deliberately
stronger liveness assertions fail there. On the fixed version use the normal Go
regressions linked in fix-report.md instead. No generated overlay enters CI.
"""

import json
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[3]
reviewed = '3bb9ceb8838839d3ded9b4e44a08648d577f4d94'
head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
dirty = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=root, text=True)
if head != reviewed or dirty.strip():
    raise SystemExit('Historical reproducer requires clean 3bb9ceb. Use the fixed-version regressions in fix-report.md.')
source = root / "internal/redaction/batch_test.go"
text = source.read_text(encoding="utf-8")
needle = "\t\tc, e = redaction.CompleteBatchParts(v, blocks, policy, c, 1700000032, votes)"
assert text.count(needle) == 1, "Fixture changed: review the injection point first"
injected = r'''
        if count == 2 {
            t.Run("ReviewThreeHonestPlusEmptyFourth", func(t *testing.T) {
                rows := append(append([][]chameleon.Contribution(nil), votes...), nil)
                _, err := redaction.CompleteBatchParts(v, blocks, policy, c, 1700000032, rows)
                if err != nil { t.Fatalf("three honest complete rows must survive one empty Byzantine response: %v", err) }
            })
            t.Run("ReviewThreeHonestPlusDuplicateIndex", func(t *testing.T) {
                rows := append(append([][]chameleon.Contribution(nil), votes...), votes[0])
                _, err := redaction.CompleteBatchParts(v, blocks, policy, c, 1700000032, rows)
                if err != nil { t.Fatalf("three honest complete rows must survive one replayed signer index: %v", err) }
            })
            t.Run("ReviewInsufficientReservePreflight", func(t *testing.T) {
                limited := state.NewOverlay(v)
                if err := state.Put(limited, rules.AccountKey(f.Org.Org, protocol.AssetCAL), uint64(99)); err != nil {t.Fatal(err)}
                candidate, err := redaction.BuildBatch(limited, blocks, policy, singles, 1700000032)
                if err != nil {t.Fatalf("unexpected preflight rejection: %v", err)}
                _, err = redaction.BatchPartShares(limited, blocks, policy, signers[0], candidate, 1700000032)
                if err != nil {t.Fatalf("unexpected part signing rejection: %v", err)}
                t.Log("100 CAL batch constructed and threshold part share produced against 99 CAL reserve; no economic preflight")
            })
        }
'''
out = root / ".run/authorized-repair-review"
out.mkdir(parents=True, exist_ok=True)
replacement = out / "batch_review_test.go"
replacement.write_text(text.replace(needle, injected + needle), encoding="utf-8")
overlay = out / "overlay.json"
overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}), encoding="utf-8")
result = subprocess.run(
    ["go", "test", "-overlay", str(overlay), "-tags=comet_v3", "./internal/redaction",
     "-run", "^TestAtomicRepairBatchTwoInputs$", "-count=1", "-v"],
    cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
)
log = result.stdout.decode("utf-8", errors="replace")
(out / "findings.txt").write_text(log, encoding="utf-8")
print(log)
raise SystemExit(result.returncode)
