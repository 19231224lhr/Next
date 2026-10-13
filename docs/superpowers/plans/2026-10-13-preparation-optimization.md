# Payment preparation optimization implementation plan

> **For agentic workers:** Use subagent-driven-development for the isolated wallet API task; integrate the CLI and measurements in this session.

**Goal:** Remove duplicate request serialization/signature verification and replace chain finality polling with committed-wallet notifications.

**Architecture:** Add `Wallet.SaveDirectRequestBytes(req protocol.DirectRequest) ([]byte, error)` containing the existing validation and atomic update. `SaveDirectRequest` delegates to it for compatibility. CLI senders use the returned bytes only on success. The existing verified-block follower notification becomes the default for `runChain`; `UTXO_CHAIN_COMMIT_NOTIFICATIONS=0` retains polling as an explicit experimental comparator.

**Tech stack:** Go 1.27.1, existing wallet Store/Group/Bolt, native Linux binaries built with `comet_v3`, Python benchmark wrappers.

## Global constraints

- Do not accept caller-provided prevalidated bytes or skip payment verification.
- Return nil bytes on any validation or store error; release bytes only after `Store.Update` succeeds.
- Preserve wallet binding, exact-request retries, spending locks, recovered-input rejection, and fee-input checks.
- Emit finality notification only after successful local commit of a verified block; subscribe before querying state.
- Preserve receipt timing boundaries, three-vote certificate checks, real public execution, and member closure.
- Keep previous raw comparison/profiling artifacts unchanged and never stage unrelated manuscript changes.

## Task 1: Wallet API

Files: `internal/wallet/direct.go`, `internal/wallet/direct_bytes_test.go`.

- [x] Add tests using a real signed Direct request and existing memory/Bolt stores: returned bytes equal the saved bytes, mutation of returned bytes cannot change saved data, exact retry works, changed opening with the same TxID is rejected, malformed authorization returns nil bytes, and store failure returns nil bytes. Add a gated Store test that proves no bytes return before update completion.
- [x] Run the focused tests and observe failure for the absent method.
- [x] Move the existing body into `SaveDirectRequestBytes`. Keep every check and callback unchanged; append `if err != nil { return nil, err }; return raw, nil` after `Update`. Keep the compatibility method as `_, err := w.SaveDirectRequestBytes(req); return err`.
- [x] Run wallet tests and existing member wallet regression tests. Review and commit only the two owned wallet files.

## Task 2: CLI integration and observer default

Files: `cmd/payctl/direct_chain.go`, `cmd/payctl/direct_bench.go`, `cmd/payctl/direct_fault.go`, `cmd/payctl/direct_liability.go`, `cmd/payctl/e7_run.go`, `cmd/payctl/e8.go`, `cmd/payctl/direct_budget.go`, `cmd/payctl/direct.go`, `cmd/payctl/chain_timing.go`, `cmd/payctl/chain_timing_test.go`.

- [x] Add a tested helper selecting notification mode by default, with `UTXO_CHAIN_COMMIT_NOTIFICATIONS=0` selecting polling. Extend commit-notification tests for multiple observers and commit-between-subscription-and-state-check behavior.
- [x] Replace adjacent `SaveDirectRequest` then `MarshalBinary` calls with `SaveDirectRequestBytes`. Preserve errors and transport endpoints. Demo/budget gateway sends reuse saved bytes; in-process collectors retain their ordinary validation.
- [x] Add chain report fields `SavedRequestBytesReused` and `CommitNotifications`; both must be true in the optimized default. Preserve opt-in preparation counters and identify the remaining encoding stage as byte handoff.
- [x] Run CLI tests and native builds after the wallet API is available. Review the combined task diff for safety and scope.

## Task 3: Controlled before/after measurements

Files: `docs/experiments/preparation-optimization-2026-10-13/`.

- [x] Preserve the prior CLI binary in `.scratch/preparation-before-bin` before edits. Build the new CLI in `.scratch/preparation-after-bin`; reuse unchanged server binaries.
- [x] Run five paired fresh 100-payment chains, alternating before/after order, with all processes on CPUs 0–3, GOMAXPROCS=4, the same memory/server and NoSync-wallet storage modes, and phase diagnostics enabled in both arms. Unset observer/profile override variables. Record per-chain preparation, receipt, whole-chain, exact input linking, single-submit counts, finality and member closure, and observation counts.
- [x] Run a short ordinary independent-payment smoke test on the new sender path and a public-wait chain exercising notification wakeups.
- [x] Validate raw results, calculate paired reductions, save source/binary hashes and report. Leave original competitor comparison untouched.
- [x] Mark this plan complete only after changes, checks, and measurements finish; report observed benefits and variation.
