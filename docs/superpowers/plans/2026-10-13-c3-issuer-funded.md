# Issuer-funded C3 implementation and experiments

> Execute with subagent-driven-development. The user supplied and approved the design and explicitly requested continuous implementation, tests, and real-service experiments.

**Goal:** Fixed user quotes and issuer-funded, certificate-bound processing budgets, integrated into the existing one-round Direct protocol.

**Architecture:** Preserve legacy behavior for comparison; activate C3 by an authenticated protocol extension and genesis policy. Maintain an independent per-certificate fee state, reuse atomic overlays, signed admission reservations, verified-block replay and direct obligations. Bind the same fee commitment in the owner-authorized transaction and the member-certified output summary so a missing source transaction's certificate suffices to fund its direct liabilities.

**Tech stack:** Go 1.27.1, current Direct protocol, Ed25519 votes, Bolt/memory stores, CometBFT, Python analysis and Linux WSL service runs.

## Global constraints

- Authoritative design: docs/superpowers/specs/2026-10-13-c3-issuer-funded-design.md, copied byte-for-byte from the user's document.
- User quote q is charged only at the payment's first successful public execution. Issuer processing budget M is separately reserved from funded FUEL before member signing.
- M covers register + fixed attach/execute/output-resolution rewards + each direct output's open/close/compensate/repay fees. Include bounded resource-dependent costs and checked arithmetic.
- First public use of a certificate (source or successor) creates exactly one fee escrow identified by stable certified fact, independent of signature subset. All transitions are atomic with principal execution.
- Direct source issuer pays missing-source processing. No downstream surcharge or recursive descendant fee obligations.
- Stages are once-only; fulfillment and compensation share one resolution reward. Extra stage charges burn, not reward. Compensation retains repayment and not-yet-executed source budgets. Spent FUEL never restores capacity.
- Hidden and partially approved certificates remain in local reservations; release only authenticated unused amounts. Preserve current safe partial-approval reclamation rules.
- No extra certification round; preserve CAL safety, C1/C2, source recovery and authorized historical repair. No unrelated refactoring or manuscript changes.
- Preserve old policy/wire behavior and make C3 selection explicit, with old fee plans still valid under their authenticated version. No silent reinterpretation of existing transactions/stores.
- All experiments use real transactions/signatures and actual services/state transitions. Raw failures and retries are recorded. Do not substitute expected numbers for observations.
- Baseline source HEAD: 909ac2c. Isolated existing checkout .e8-worktree, branch fix/partial-approval-reclamation. Unrelated working changes are preserved; stage only owned files. No push requested.

## Tasks

### 1. Protocol commitment and pure fee state machine
- [x] Add canonical bounded fee policy/commitment types, integer budget calculation, transaction + certificate binding and roundtrip/tamper tests. Reuse reserved extension where safely possible; reject unsupported/malformed extensions.
- [x] Add standalone per-certificate state transitions for attach, execute, open, fulfillment, compensation and repayment; records obey M=spent+held+released and reward ceiling.
- [x] Test the design's 6 FUEL example, multiple outputs, duplicate stages, overflow, invalid stage ordering and immutable reward destinations.
- [x] Run protocol/rules tests, commit task, independent scoped review.

### 2. Member, public ledger, wallet and configuration integration
- [x] Hook budget recomputation, funded grants and atomic CAL/FUEL reservation before vote; add hidden/concurrent/partial certificate tests.
- [x] Hook first-use escrow, fixed quote collection to operating account, protected issuer pools, direct-output stage charges, compensation and repayment with public event deduplication.
- [x] Replay verified outcomes into local release accounting without resurrecting spent FUEL. Retain budgets after compensation and through history repair/restart.
- [x] Add genuine funded top-up support or reuse verified grant replenishment with strict conservation. Keep policy versions and migrated wallet responsibilities bound to original issuer/pool.
- [x] Wire optional C3 fixture/config/wallet builders and add original-flow, sponsor, multi-output, cross-organization and restart tests.
- [x] Run affected regressions and F1-F4 functional cases, commit and scoped review.

### 3. Real-service experiment harness
- [x] Preserve/rebuild baseline binaries from the recorded commit; capture source/compiler/server hashes and CPU/config settings.
- [x] Add a repeatable payctl C3 experiment using real HTTP members, gateway and public committee. Record events and balances as observed from committed state.
- [x] Experiment 1: three execution orders, identical funding and quotes; at least three repeats, raw CSV balances/rewards/burn/held/release and invariants.
- [x] Experiment 2: multiple source delays under identical workload and at least three repeats; record completion, closure, actual compensation ratios, revenues, costs and budget peaks.
- [x] Experiment 3: at least five alternating baseline/C3 repetitions of 100 dependent payments; repeated mixed workload; receipt p50/p95, chain time, certificate size, reserve timing, execution cost, throughput and completion.
- [x] Capture failures without overwriting; no concurrent experiment loads; retain scripts/configuration and raw run identifiers.
- [x] Review harness correctness before measured runs; run and validate all three groups.

### 4. Results, proof obligations and final verification
- [x] Generate CSV summary, variability and vector PDF figures from raw observations.
- [x] Write concise Chinese experiment report and implementation summary, map tests to F1-F4 and distinguish tests from remaining formal proofs.
- [x] Run final appropriate full regressions, review complete source diff, resolve material findings, verify artifact hashes and clean up only started services.
- [x] Local commits and final actual results with repeat commands; no unsolicited push or manuscript changes.
