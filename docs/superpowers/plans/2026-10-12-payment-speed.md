# Payment speed implementation plan

> For agentic workers: use subagent-driven-development with task-scoped review and tests.

**Goal:** Improve time until a recipient can safely continue payment, against the current direct-delivery and pool implementations.

**Architecture:** Eliminate redundant work and queue blocking first; overlap independent durable operations second; evaluate recipient collection last. Existing protocol validity rules remain unchanged.

**Tech stack:** Go, HTTP, synchronous Bolt storage, four members with three-vote certificates.

## Global constraints
- Work in `C:/Users/Bocchi/Desktop/Next/.e8-worktree`; preserve unrelated dirty manuscript files and pre-existing implementation.
- Before snapshot is `.scratch/speed-baseline-20261012`. No commits or staging by task agents; scoped final review uses snapshot diffs.
- Pool success requires two distinct member durable full-QC acknowledgments AND recipient verification plus durable wallet receipt. Do not add per-payment ready-marker fsync.
- Runtime readiness is exact-receipt scoped; restart requires confirming/reinstalling two member copies before onward payment or exit.
- Availability is an honest-client completion condition; no new protocol-level availability proof.
- Reuse only an exact full QC independently verified and durably installed by this same member. Keep new action authorization, recipient, state, sequence and conflict validation.
- Direct delivery comparison baseline is current gateway-to-recipient delivery. Never substitute old sender forwarding.
- No performance claims without measured data; same-organization funds reuse and generic routing speed are distinct effects.

## Task 1: Work-conserving delivery
Files: internal/delivery/delivery.go and delivery tests.
- [x] Add regression with one blocked recipient and multiple healthy recipients; healthy jobs beyond first four must proceed before blocked release.
- [x] Run test against old scheduler, observe failure.
- [x] Replace batch wait with four bounded continuously occupied slots, one in-flight job per recipient, bounded scan, retries with backoff, durable pending/tombstone behavior preserved.
- [x] Cover restart, idempotent delivery, cancellation and bounded concurrency; run `go test ./internal/delivery` and targeted race tests.
- [x] Write task1-report.md with tests and exact changed files; obtain independent review.

## Task 2: Pool approval fast path
Files: internal/gateway/pool.go, internal/member/pool.go, internal/member/pool_certificate.go and tests.
- [x] Test fresh successful collection performs no historical-QC GET; retry/failure still recovers full QC when votes unavailable.
- [x] Test exact installed parent reuse, unknown parent fallback, tampered QC, wrong owner/sequence/conflict rejection.
- [x] Run regressions before change; implement approve-first recovery fallback and exact trusted parent reuse.
- [x] Preserve two-member durable installation, old historical vote archives and recovery tests.
- [x] Run gateway/member/poollab package tests; report and independent review.

## Task 3: Parallel durable completion
Files: internal/gateway/pool.go, internal/wallet/pool.go and wallet state; internal/poollab/lab.go; cmd/poolctl/main.go; related tests.
- [x] Add tests proving recipient can save while member installation blocked, but cannot spend/exit or report ready until both succeed; failures and restart must remain gated.
- [x] Implement completion overlap and runtime readiness without extra fsync. Keep serial mode for benchmark comparisons. Integrate public wallet/CLI/lab entrypoints.
- [x] Test recovery after wallet restart and conflicting/older receipt readiness behavior.
- [x] Run affected package tests and race checks; report and independent review.

## Task 4: Recipient collection experiment
Files: focused receiver collector implementation and real HTTP lab/benchmark integration.
- [x] Define authorized payer-signed request delivery to recipient; recipient collects member votes and full QC, maintaining recovery/public relay access.
- [x] Add tests rejecting mixed-fact/duplicate/bad votes and proving safe receipt readiness; no arbitrary callback URLs.
- [x] Implement an optional path using existing member endpoints and recipient collector; keep current gateway direct path available.
- [x] Compare equivalent durability and completion boundaries over real HTTP, local and explicitly imposed link delays. Report as experimental if no production routing integration.
- [x] Obtain independent review.

## Task 5: Verify and report
- [x] Run `go test ./...`, `go build ./cmd/...`, changed concurrency package race checks.
- [x] Run interleaved matched pool old/new benchmarks from snapshot/current binaries on fresh stores, at least three repeats; record raw data, environment and percentile endpoint.
- [x] Measure blocked-recipient queue regression separately from isolated latency; do not confuse synthetic delays with WAN measurement.
- [x] Final broad code review; fix material issues with covering tests.
- [x] Summarize implemented changes, measured benefits/non-benefits and remaining deployment scope in plain Chinese.
