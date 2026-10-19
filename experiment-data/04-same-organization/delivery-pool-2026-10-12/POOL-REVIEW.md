# Independent pool security and requirements review

Reviewed 2026-10-12 in `.e8-worktree`, against the delivery/internal-funds plan with baseline `7a2ab17` and the current pool implementation, including the subsequent full-QC installation/recovery changes. The reviewer did not implement pool code. No core files or Git state were modified. Discovery first queried the `next-e8-worktree` graph; the new pool symbols were absent, so source inspection followed.

## Verdict

No blocking correctness or security findings identified within the stated v1 scope: an opt-in, fixed-configuration, indivisible publicly funded CAL unit; no pool fees, no automatic timeout withdrawal, and no independently redeemable transfer receipts. The implementation retains the existing three-of-four certificate threshold and shares the ordinary-payment spend lock at entry. The two acknowledged full-QC copies address recovery after successful collection; they do not provide liveness for an interrupted collection or conflicting proposals.

This is a code review plus the test evidence below, not a formal proof or production deployment certification.

## Safety and requirements checked

| Requirement | Implementation and assessment |
| --- | --- |
| Separate protocol and authorization domains | `protocol/pool.go`: separate wire tag, pool/action/vote/output domains, exact fixed organization configuration, network binding, owner signature, and three distinct valid member votes. Ordinary spend signatures cannot substitute for pool signatures. |
| Real public funding | `internal/rules/pool.go:ValidatePoolDeposit` requires a final CAL creation with the exact evidence, amount and receive descriptor. `EvaluatePool` consumes the original input and creates escrow atomically. Public entry rejects already consumed funding; member candidate state additionally prevents an ordinary-payment QC and a deposit QC from both forming. |
| No pre-finality pool spending | `internal/member/pool.go:ApprovePool` requires the public `PoolRecord` before transfer/exit. `InstallPool` archives a deposit QC without fabricating that record. Only the verified successful-block path creates the member's public deposit record. |
| Fixed amount and current owner | Member approval binds the child to the parent QC's pool, exact amount, next sequence, predecessor ID and recipient owner. Transfer stays in the fixed organization. Public exit rechecks amount/configuration against escrow. |
| Transfer/exit exclusion | `poolLocal.Pending` is written before signing; transfer and exit use the same predecessor lock. A newer valid QC can supersede a minority pending vote. Archived old votes reproduce only votes already signed and cannot produce a conflicting quorum under the existing one-fault assumption. |
| Once-only public exit | `EvaluatePool` accepts public deposit/exit only, rejects transfer receipts, zeroes escrow while creating the ordinary CAL output, and records the exit ID atomically. Exact replay is idempotent; a different exit is rejected. |
| Public follower correctness | `internal/member/blocks.go` handles authenticated successful pool entries. `partial_reclaim.go` reconciles a losing ordinary or pool deposit vote against the public winner without freeing an input for reuse. Losing ordinary admission debits are reclaimed through existing accounting checks. |
| Wallet distinction | `internal/wallet/pool.go` persists one signed request per predecessor and shares ordinary wallet spend reservation at deposit. `ReceivePool` verifies/stores an internal receipt without creating an ordinary `DirectCoin`. `internal/wallet/blocks.go` creates the ordinary exit coin only from a verified successful public block. |
| Opt-in and persisted configuration | Public check/execution and member APIs reject disabled support. Public genesis serialization and member initialization bind the enabled configuration to the stored identity. `cmd/member/main.go` rejects experimental memory members for enabled pools and forces synchronous storage. `cmd/poolctl` uses synchronous wallet storage. |
| Ordinary accounting preserved | Pool transfer does not allocate ordinary CAL admission resources. Integration tests exercise both orders of pool/ordinary entry conflict and reconciliation of each minority loser. Exit coins obtain subsequent ordinary wallet reservation and member approval. |

## Full-certificate availability re-review

Inspected `internal/member/pool_certificate.go`, `internal/gateway/pool.go`, `internal/transport/pool.go`, and their unit/integration tests after the former pool reviewer implemented the recovery fix.

`InstallPool` verifies the complete QC and stores it before acknowledging. For a transfer/exit it requires the known public deposit and matching configuration/value. A newer certificate advances local certified state and clears only pending proposals at or below that sequence. Historical installation does not roll state backward or reopen a public exit. Complete certificates survive store close/reopen and can be fetched by action ID.

`CollectPool` concurrently requests archived certificates and approvals. Each newly collected vote is tied to the expected configured member slot and independently checked. A recovered QC is also fully verified and identity-bound. Before returning success, collection waits for two successful installations on configured member slots, leaving at least one honest durable copy for at most one faulty member. A valid QC may accompany an installation error, but callers must not interpret that as completed collection. `poolctl` returns on this error and retries the same persisted proposal on a later invocation.

Install acknowledgements are transport responses, not signed receipts. The two-copy interpretation therefore assumes the operator has configured distinct member endpoints and the transport binds them to those identities. This is consistent with the existing client abstraction and loopback laboratory; it is not a new authenticated remote deployment mechanism.

## Limits deliberately retained

* A crash before two-copy installation completes can strand a QC if a signer later withholds its vote. A split of competing proposals can also freeze progress. No timeout unlock is introduced, because it would invalidate the safety argument. Successful-collection recovery is the tested guarantee.
* Fixed organization membership/configuration, one indivisible unit and zero pool fees are v1 restrictions. Partial withdrawal, membership migration, fee economics and a distributed contention-resolution protocol are not implemented.
* `poolctl` persists and submits proposals/certificate files; public submission acceptance is not finality. Its source does not implement a general block-following wallet daemon or automatic recovery scheduler. The separate integration laboratory exercises final public application, follower updates and subsequent ordinary spending.
* Member/QC archives grow without a retention protocol. Garbage collection must preserve the exact-proposal retry and certificate-availability guarantees.
* Public blocks in `internal/poollab` are authenticated synthetic testkit blocks after real `Engine.ExecuteAt`, not a running validator consensus network. The ordinary baseline measures an unsettled certificate chain, and member CAL reservations are separate replica-local ledger measurements, not values to sum as independent capital.

## Independent validation

Fresh run after the availability implementation:

```text
go test -race ./protocol ./internal/rules ./internal/member ./internal/wallet ./internal/gateway ./internal/transport ./internal/committee ./cmd/poolctl -run Pool -count=1
ok utxo/protocol 2.267s
ok utxo/internal/rules 2.494s
ok utxo/internal/member 4.507s
ok utxo/internal/wallet 2.760s
ok utxo/internal/gateway 3.519s
ok utxo/internal/transport 2.917s
ok utxo/internal/committee 3.693s [no tests to run]
?  utxo/cmd/poolctl [no test files]

go test -race ./internal/poollab -count=1
ok utxo/internal/poollab 15.395s
```

The integration run covers the 100-transfer lifecycle, constant escrow, public exit and ordinary reuse; wrong identities/quorums; disabled premature entry through absence of public deposit; stale-owner/public-receipt rejection; both ordinary/pool minority reconciliations; transfer/exit races; persistent vote locks; one/two unavailable members; insufficient install acknowledgements; and full-QC recovery after persistent-store restart when re-collecting the original three votes is impossible. It also runs the ordinary-capital baseline test.

Historical RED/GREEN order is reported by the implementers; this independent reviewer confirms current code and fresh passing tests only. No new performance measurement or speedup claim was made in this review.
