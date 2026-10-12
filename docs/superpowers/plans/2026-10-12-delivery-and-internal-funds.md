# Certificate delivery and closed internal funds

Baseline: 7a2ab17, existing isolated `.e8-worktree`, branch fix/partial-approval-reclamation. Preserve unrelated paper changes. User authorized implementation of both features; no deployment or paper claim changes.

## Global constraints
- Existing DirectPayment rules, full QC verification, quorum and reserve accounting remain intact.
- Direct delivery endpoints come from operator-configured recipient routes or recipient-authenticated subscriptions, never arbitrary sender callbacks. Recipient verifies and persists before acknowledging.
- Durable delivery records survive offline recipients/restart; bounded dispatch does not wait for recipient on sender response path. Preserve existing public submission/evidence recovery.
- Internal funds are a distinct protocol: finalized CAL locked once, indivisible unit, fixed organization/config, owner-signed transfer and exit, three of four member attestations, persist vote locks before signing. Ordinary internal receipts cannot redeem publicly. Exit competes with transfer for the same predecessor state, freezes funds, and public redemption is once only.
- No real funds, Mac deployment, database tuning or invented performance claims.

## Task 1: Direct certificate delivery
Implement delivery package/integration in internal/gateway, internal/wallet, cmd/gateway and E7 client as appropriate. Start with red tests for sender-independent delivery, recipient validation, offline/restart, duplicates and routing. Add opt-in command configuration. Retain existing sender forwarding baseline. Test a networked handoff with identical recipient verification/storage boundary. Document actual commands and results, including whether acceleration is observed.

## Task 2: Closed internal funds
Inspect public dispatcher/state and wallet bindings first, finalize protocol design with sidebar GPT. Add separate wire types and member state machine; public entry consumes a real existing finalized UTXO and locks its full value. Public exit checks a domain-separated quorum exit certificate and creates exactly one ordinary spendable output. Fixed config and indivisible funds in v1; no timeout withdrawal from stale state. Red tests: insufficient signatures, stale-owner transfer/exit, conflicting children, transfer/exit race, replay, wrong organization/network, crash restart, missing installed parent QC. Exercise through existing committee execution and a runnable client/lab. Compare 100 transfers against ordinary path capital usage and measured latency.

## Task 3: Review and reporting
Independent spec/code review of each change; fix concrete findings. Run targeted tests then full Go suite/build and relevant race tests. Discuss measured results with sidebar GPT. Save implementation guide, raw measurements and limitations. Do not attribute direct-delivery benefit exclusively to same-org operation.

## Completion record

All three tasks completed locally on 2026-10-12. Direct delivery and closed
internal funds are separately enabled. Sidebar GPT reviewed the design in two
rounds; its complete-QC recoverability finding led to two-member installation
acknowledgments and retrieval/restart tests. Independent code reviews also led
to delivery-failure retry, wallet prevalidation, and bidirectional minority-vote
reconciliation fixes.

The pool test confirms constant 100 CAL escrow through 100 transfers and a
spendable public exit. Its measured local latency is higher than the ordinary
baseline, so no speedup is claimed. Direct delivery helps when the sender return
path imposes waiting and adds overhead when it does not. Reports and raw data:
`docs/experiments/delivery-pool-2026-10-12/README.md`. Full suite, command build,
targeted race checks and independent reviews passed. No commit/push, deployment
or paper edits were performed for this task.
