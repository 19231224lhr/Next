# Final speed-change review

Reviewed Task 3 independently and checked integration across Tasks 1–4 against the saved pre-change baseline. Existing delivery/pool functionality and unrelated manuscript changes are outside this review. No tests were run during the parent's benchmarks.

## Verdict

**Approved within the reviewed scope; no outstanding material findings.** The Task 3 installation lifecycle finding below is resolved. No completion-security defect found in the reviewed changes.

## Resolved P2: Cancel surplus installation RPCs after the two-ACK boundary

Locations: `internal/wallet/pool.go:136-160` and `internal/gateway/pool.go:118-142` (the latter becomes affected by the Task 3 wrapper at lines 19–24).

The initial Task 3 split left installation outside the old enclosing cancellation scope. Both helpers could return after two successful acknowledgments while surplus RPCs retained the caller's long-lived context.

Both helpers now derive a local context and defer cancellation, preserving the two-success boundary and four-buffer result channel. The gateway and wallet regressions use two successful installers and two blocked on `ctx.Done()`, and require the blocked calls to observe cancellation after success while the parent context remains live. This resolves the finding. The parent reports red/green and race validation; this re-review inspected the final source and tests without rerunning them during benchmarks.

## Task 3 security and CLI checks

- `ReceivePool` verifies the complete QC and receiving owner before persistence. The durable head and runtime readiness are protected by the same mutex used by `SavePoolRequest`.
- The latest implementation keys readiness by complete serialized certificate identity, including action signature and ordered votes. Newer receipts and a different QC for the same action invalidate readiness. An older in-flight completion cannot mark a replacement receipt ready.
- Readiness is set only after receipt persistence and two successful calls to configured member slots. Store errors and insufficient installation acknowledgments return errors. Restart constructs an empty readiness map, and identical duplicate confirmation adds no receipt write or ready-marker write.
- `poolctl -receive` completes the receipt; a new sending invocation reconfirms an internal-transfer parent before reserving its successor. Deposit handling retains the existing member-enforced public-finality boundary.
- `CollectPoolCertificate` explicitly exposes collection without installation; legacy `CollectPool` still performs installation before success. Serial and overlapped lab paths use the same readiness gate.
- This review assumes the existing configuration contract that the four member slots correspond to their configured distinct members. It does not claim the unsigned install HTTP response adds cryptographic endpoint identity.

## Integration checks

- Task 1 preserves durable pending-to-done transitions, bounded scanning, four active sends, and one active request per owner. The reviewed fairness fixes allow later records for the same owner to proceed after a rejected record. No new material issue found beyond the existing Task 1 review.
- Task 2's archive shortcut compares the full stored action and every ordered vote; altered/unknown proofs retain full verification. Approval-first collection and fallback recovery still produce a verified QC before Task 3's completion step.
- Task 4 uses the existing three-vote direct collector, retains complete payment material and the public relay task before saving local wallet outputs, and acknowledges only after those operations. Its owned wallet also serves the owner-signed prepare path.
- Receiver relay restart is wired to the same persistent gateway DB. `Relay.runDirect` starts both lanes with scanning enabled and nil cursors, and scans `KeyOutbox` independently of in-memory Early/Wake notifications. `PersistDirect` atomically saves `KeyCollected` and `KeyOutbox`. The revised recipient test reopens the store, starts a fresh relay with valid trust and no volatile inbox or wake notification, observes an actual public-client Submit call, and compares its bytes against the retained payment's submission. This verifies relay resumption through submission, without claiming public finality.
- The receiver experiment now rejects a receipt whose certificate transaction ID differs from the timed payment or whose timestamp precedes the start. This prevents silently measuring a stale or unrelated receipt.
- Benchmark wording correctly distinguishes ordinary gateway-to-recipient delivery from the optional receiving collector. The added 20 ms is an imposed link delay; baseline public relay persistence is background, while the recipient path awaits it. These measurements must retain those qualifications and cannot be described as an isolated equivalent-fsync RPC comparison.

Reported tests cover receipt/install ordering, persistence failure, restart reconfirmation, duplicate-write avoidance, replacement QCs, concurrent newer arrival and surplus-RPC cancellation. The parent reports the new restart-relay submission test passing. This final re-review ran no tests, preserving the quiet benchmark window.
