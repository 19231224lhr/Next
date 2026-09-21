# Gateway handler persistence repair

Goal: remove the verified HTTP/1 connection wait caused by response-afterwork, keeping the original durable outbox and payment semantics.

Approved scope: `cmd/gateway/direct.go`, its wiring in `main.go`, focused tests. No member, committee, wallet, consensus or database optimizations in this patch. Continue within the existing implementation checkout; preserve all earlier uncommitted work.

- [x] Reproduce: block the first save; a second request on the same TCP connection must still receive a valid certificate. Observe the current failure first.
- [x] Implement bounded handoff: 128 save slots; acquire before launching a background task; save synchronously when full. Keep Group.Update concurrency so batching remains possible. The task owns the decoded payment and never references request context/body or ResponseWriter.
- [x] Drain: close admission and wait for existing handlers before waiting for accepted background saves. Run this before relay/follower joins and DB.Close. Keep existing terminal-state checks and explicit error logging.
- [x] Validate: same-connection progress, persistence after handler context cancellation, full-bound fallback, shutdown draining, persistence failure visibility, and existing late-write/retransmission tests. Race tests, full project tests and vet.
- [x] Compare: fresh, identical genesis and fixed binaries except gateway; 200/s with 256 fast and 2048 total limits. Four 12,000-payment A/B/B/A trials with per-request tracing off; independent same-connection diagnostics, background save backlog and audits. Retain negative results. Restore or activate the tested gateway on the preserved laboratory data only after validation.

Measurements: fast receipt P50/P95, actual send rate/lag, unfinished tasks/drain, and diagnostic written-request-to-handler delay, bounded save occupancy/age/fallback. Memory handoff is not a durable receipt; restart still relies on persisted outbox or existing wallet/member replay.
