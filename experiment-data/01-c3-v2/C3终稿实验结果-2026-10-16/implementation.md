# Final C3 implementation — 2026-10-16

## Scope and version boundary

Implemented the core fee rules in `C:/Users/Bocchi/Desktop/Next_C3_方案概述_终稿.md`, integrated with the existing direct transaction, member approval, public execution, compensation, and observation paths. Production source and tests were changed in the existing dirty checkout; no reset, staging, commit, or push was performed. The parent agent owns CLI/config/harness changes. Existing signer, continuation, cache, wallet, and history-repair optimizations remain in place.

`protocol.C3Version=1` retains the previous signed fee encoding and behavior. `protocol.C3FinalVersion=2` explicitly selects the final design. The version-2 commitment appends BaseFee, PriorityFee, PriorityReward and Tier after the existing fields. Version-1 bytes have no extension. Final payments use `FeeSource=CALService (3)`, no fee inputs, no FUEL change/refund, and bind the actual payer to the signed transaction subject. A zero CAL quote is valid only on the final path. CAL input value must equal principal outputs plus the quote, both at member approval and public execution. Only principal outputs consume CAL signature coverage.

Final fee plans reject Register, per-resource Register, Open and Close charges. Their bound is `rAtt + rExec + priority + outputs*(rRes + comp + repay)`. The existing event-mask escrow keeps registration/execution/output events unique by certification fact, independently of the QC subset. Final escrow uses Reflow instead of Burned. Normal source-first and child-first resolution incur the same fixed reward. Compensation spends one rRes and comp, retaining all remaining output liability, source execution/priority, and repayment. Late execution pays the remaining fixed execution/priority and repayment, then releases only unused budget. Member slices recover only Escrow.Released; past spending is never restored.

## Production files

- `protocol/c3.go`, `protocol/transaction.go`: final version, quote/tier binding, zero-user-FUEL fee source, checked plan, compatible versioned commitment extension.
- `protocol/execution.go`: canonical tag 414 for cumulative protected FUEL credits; old 412/413 results remain supported.
- `internal/rules/c3_policy.go`: frozen policy version, base fee and tier registry; validator/epoch/reservation settings and validation; final BindC3 selects protected issuer funding only.
- `internal/rules/c3.go`, `c3_ledger.go`: once-only final escrow events, final reflow counters, atomic usage/free/held/spent/released accounting, execution-only CAL operating revenue.
- `internal/rules/c3_rewards.go`: committee vote-weight sharing with deterministic largest remainder, canonical validator ID tie break and big-integer multiplication; execution/priority paid to actual includer.
- `internal/rules/c3_epoch.go`: deterministic epoch boundaries; block-weighted protected CAL; payer-tagged timeout pool; payer exclusion; floor division and attributed carry; protected FUEL credit/grant increase; retained epoch history and replay checks.
- `internal/rules/direct.go`, `internal/member/direct.go`: CAL quote included in value balance; resource reservation remains atomic with input locks before existing approval signature.
- `internal/member/blocks.go`: applies cumulative protected FUEL credit deltas only from verified public block execution data; duplicate block/credit observation is harmless.
- `internal/committee/app.go`, `direct.go`: trusted consensus proposer address passed to execution; epoch begin transition; public cumulative credit observations; scheduler hook.
- `internal/committee/c3_scheduler.go`: reserved source/compensation FIFO byte capacity, ordinary descending priority then candidate arrival order, idle capacity reused. Repair representation commands do not consume the reserved closure channel. ProcessProposal does not compare remote proposals with the validator's local mempool.
- `internal/committee/c3.go`: CALRevenue, PoolReflow, Epoch, individual RewardAccounts and total final committee Rewards exposed in snapshots.

The parent agent separately adapts `cmd/internal/config`, `cmd/committee`, and `cmd/payctl`. It binds the published C3 validator registry to the actual trusted consensus validator set and supplies the full finite mempool to PrepareProposal before selection, so pre-truncated candidate lists do not hide recovery commands.

## Configuration and harness contract

`rules.C3Policy{Version:2, BaseFee, Tiers:[]C3Tier{ID,CAL,MinimumReward}, ID, Schedule}` selects new semantics. `C3Settings` additionally needs `Committee:[]C3Validator{ID,Address,Weight}`, `EpochBlocks>0` and `ReservedBPS` strictly between 0 and 10000. Addresses are 20-byte consensus validator addresses; the CLI uses `Digest("C3_VALIDATOR", address)` for their reward IDs. Policies and historical issuer funding remain genesis-frozen. Changing the current quote never reinterprets an older policy ID. Revenue must be separate from all protected FUEL pools and protected CAL backing accounts.

To select a tier before signing, set `body.Fee.C3.Tier`, then call BindC3. BindC3 selects the published quote/minimum reward and recomputes the budget and admission. The caller must fund the CAL quote in its value equation before constructing/signing FastTx; no output is automatically reduced inside protocol rules. Published minimum priority reward is checked at both wallet and ledger policy boundaries.

Wire the honest proposer with `app.SetProposalScheduler(engine.SelectProposal)`. `committee.BlockContext.ProposerAddress` must come from finalized consensus block metadata, never the executing node's identity. Epoch weights sample protected account balance at each block beginning. Epoch `(height-1)/EpochBlocks` settles before commands in its first block. A final payment, successful reserve increase, or clock tick publishes cumulative credits; members may conservatively retain the old smaller signing allowance until observing such a verified result. Clock tick credit results have Applied=false and do not create wallet outputs.

Read final accounting through C3Fee/C3Pool: `Record.Escrow.{Budget via Commitment,Held,Rewards,Reflow,Released}`, `QuoteCollected`, pool `Free,Held,Spent,Funding,CALRevenue,PoolReflow,RewardAccounts,Epoch`. Count global reward accounts once when summing multiple issuer snapshots. `Epoch.Pool` retains each original payer's dust. `C3EpochHistoryKey(e)` retains completed-epoch weights/accounting. Final global FUEL checks must include protected free+held, all committee reward accounts, remaining attributed pool, and any other genesis FUEL accounts. Cumulative pool Spent and cumulative Credits are audit counters, not extra assets.

Parent CLI adds `init-lab --c3-final` and `--c3-base-fee`; legacy `--c3` stays available. No formal performance loads were run by this implementer.

## Correctness evidence

All commands ran on Windows Go in this checkout. Logs here contain actual command output.

- Initial `go test ./protocol -run TestFinalC3 -count=1` failed as expected with unsupported final version: `protocol-red.txt`. After protocol implementation, focused C3 tests passed: `protocol-green.txt`.
- Initial ledger final test exposed the missing policy Version field: `ledger-red.txt`; subsequent final source/child/timeout tests passed.
- `go test ./...` passed across the whole repository: `integrated-tests.txt` (includes parent command-surface changes at that time).
- Fresh `go test ./protocol ./internal/rules ./internal/member ./internal/committee ./internal/wallet -count=1` passed: `final-correctness-tests.txt`.
- `go test ./internal/rules -run TestFinalC3 -count=1` passed with paid/free CAL, signed fee mutation rejection, execution-only revenue, duplicated execution, failed atomic transition, multi-output mixed resolution, different QC subsets, retained late-source holds, epoch exclusion/dust/carry/sole payer/replay/time weighting, member observed credit deltas, and uint64-limit weighted reward conservation: `final-rules-tests.txt`.
- `go test ./internal/committee -run TestFinalC3 -count=1` passed with real member approvals and ledger execution, hidden-budget rejection, retained hidden budgets and fee events after Bolt close/reopen, verified block replay, unused release, actual includer rewards, reserved source selection, ordinary priority and idle reserved reuse: `final-service-tests.txt`.
- Focused race command and results are in `final-race-tests.txt`; the protocol/member/rules/committee focused commands are also preserved in `vertical-tests.txt` and `final-focused-tests.txt`.

Tests added: `protocol/c3_final_test.go`, `internal/rules/c3_final_test.go`, `internal/committee/c3_final_test.go`.

## F1–F6 coverage and limits

| Property | Implementation/test evidence |
|---|---|
| F1 | Existing overlap grant slices now reserve final issuer budget in the same member store transaction as CAL and input locks; hidden-budget, concurrent-approval, observed-credit-delta and durable restart tests. |
| F2 | CAL-only free/paid signed quote, exact output-plus-fee balance, execution-only revenue, owner signature mutation rejection; source/child/timeout tests. |
| F3 | Certification-fact escrow, immutable commitment comparison and unique event masks; different quorum subsets, multi-output mixed compensation/fulfillment, retained future source/repay cost, duplicates, atomic failure and restart. |
| F4 | Checked reward ceiling; fixed stage rewards, actual includer execution/priority and vote-weight sharing; normal/compensated paths, repeat execution and uint64-limit conservation. |
| F5a/b/c | Register/Open/Close zero final transfers; identical normal-path fixed costs; comp/repay sourced only from directly responsible issuer; payer excluded from its own epoch contributions; bounded committee reward. Scheduler is honest-proposer policy, not a proof of bounded inclusion under arbitrary delays. |
| F6 | Final ledger does not use burn for timeout fees; integer reflow redistributes existing pool balance into protected FUEL and grant totals; attributed dust/sole-payer retention, multiple epochs and observed credit replay tests. |

These are implementation/correctness checks, not formal security proofs or performance results. Optional fix fees and optional deployment mechanisms (CAL fee sharing, CAL/FUEL conversion, issuance, membership pricing) are deliberately outside this core implementation. The genesis registry is static as in the existing service; runtime addition of new organizations is not introduced. New performance claims require the parent agent's separate frozen-build service experiments and full conservation audits.

## Review follow-up

Independent review identified the consensus transaction framing boundary after full-mempool candidate gathering was enabled. `TestFinalC3ProposalRespectsProtoFraming` reproduced a 127-byte raw transaction exceeding a 128-byte consensus limit once protobuf framing is counted (129 bytes): `framing-red.txt`. App and scheduler now both charge `cmttypes.ComputeProtoSizeForTxs`, including length-prefix/tag overhead. The test and full committee/protocol/rules/member/wallet suites passed after the fix. `internal/committee/c3_framing_test.go` contains this regression check.

Additional final-only tests now cover concurrent hidden approvals, Bolt restart of ledger plus all three signing members while a source remains hidden, and retaining unpaid priority reward through compensation until late source execution. The focused race run passed with the final concurrency and restart tests; `internal/member` had no test matching that race filter, while its full uncached suite passed separately.


### Epoch credit before reserve increase (second review P1)

`TestFinalC3EpochCreditBeforeFirstTopupReplay` reproduced the follower failure before the fix (`epoch-topup-red.txt`). A final-mode successful reserve-increase result now includes authenticated cumulative epoch credits, using the existing tag-414 execution result. Member block preparation already orders those credit updates before command replay. The strict Previous check is preserved. Failed commands do not publish credit results; a successful duplicate command can safely repeat the cumulative credit.

The regression begins with an explicitly accounted timeout-pool fixture (payer free91 + pool9, grant100), runs real Engine.BeginBlock/ExecuteAt transitions, and feeds consensus-verified blocks into the recipient member's real block follower. An empty block is observed first, both ledger and member reopen Bolt stores, and the next relevant block's first command tops up grant109 by10 without an earlier payment/tick. The same block repeats that command and then adds a distinct top-up with Previous119 by3. Member grant ends122 and available signing share81; repeated delivery and member restart/replay preserve these totals. This isolates replay integration and does not claim a new full end-to-end timeout experiment.

The regression passed after the fix (`epoch-topup-green.txt`); the enlarged same-block/restart test plus final concurrent approvals passed under `go test -race ./internal/committee -run 'TestFinalC3(EpochCreditBeforeFirstTopupReplay|ConcurrentHiddenApprovals)' -count=1` (`epoch-topup-race.txt`). Broader package output is saved in `review-fixes-tests.txt`; an intermediate root-owned transport compile error is retained there as actual evidence. Fresh corrected output is recorded separately.
Fresh corrected verification: go test ./internal/committee ./internal/member ./internal/wallet ./internal/gateway ./internal/rules ./protocol -count=1 passed all six packages; see review-fixes-green.txt.
