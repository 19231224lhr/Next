# Task 2: pool approval fast path

## Change

- `CollectPool` sends approvals immediately. It reads archived full QCs only after an approval error, an invalid vote, or a 200 ms slow-path timer. It still verifies a recovered QC and requires two successful `InstallPool` acknowledgments before success. The caller's context continues to bound unresolved approval or recovery calls.
- `ApprovePool` compares the complete parent action (including owner signature) and every vote, in order, with the member's durable key-182 QC archive. Only an exact archived match skips full parent QC verification. Unknown or changed proofs use `PoolCertificate.Verify`; new action authorization, parent relation, and persisted conflict checks remain in place.

## Red and green evidence

- Added the fresh-flow and withheld-approval gateway tests first. Before the gateway change, `go test ./internal/gateway -run 'TestPoolCollectionFreshHasNoHistoricalReads|TestPoolCollectionRecoversWithWithheldApprovals' -count=1 -timeout=10s` failed: `member 0: 1 historical reads`.
- Added exact-archive and modified-parent member tests first. Before the member change, `go test ./internal/member -run 'TestPoolCertificateInstallPersistsAndSupersedesMinority' -count=1` failed to compile with `undefined: poolParentArchived`.
- The first gateway implementation failed four existing `internal/poollab` tests because recovery with no archived QC returned before three pending healthy votes. The collector now continues accepting votes after the recovery reads finish; the required package run passes.
- Final: `go test ./internal/gateway ./internal/member ./internal/poollab` passed.

Gateway tests cover fresh success with zero historical reads and two installations, archived QC recovery when votes fail or are invalid, and timer-triggered recovery when approvals are withheld. Member tests cover exact archive matching after restart, tampered same-ID owner signature and vote rejection, changed vote order falling back to full verification, an unknown valid QC, and wrong owner, sequence, and conflict rejection. Existing poollab recovery and exit tests pass.

## Changed files

- `internal/gateway/pool.go`
- `internal/gateway/pool_certificate_test.go`
- `internal/member/pool.go`
- `internal/member/pool_certificate.go`
- `internal/member/pool_certificate_test.go`
- `docs/experiments/sameorg-speed-2026-10-12/task2-report.md` (this report)

`task2.diff` compares these files with `.scratch/speed-baseline-20261012`. The diff artifact itself is excluded to avoid self-reference. No files were staged or committed. No latency claim is made without benchmark measurements.
