# Internal pool integration laboratory

The executable is `cmd/pool-lab`; its shared harness and adversarial tests are in `internal/poollab`. This experiment uses four real member HTTP handlers on loopback and three distinct wallet owners bound to the same organization. Pool support is explicitly enabled. It uses deterministic laboratory identities and the repository's laboratory chameleon key.

## Reproduction

From the repository root:

```powershell
go test ./internal/poollab -count=1 -v
go test -race ./internal/poollab -count=1
go run ./cmd/pool-lab -transfers 100
$poolDirectory = Join-Path $env:TEMP ('next-pool-' + [guid]::NewGuid().ToString())
go run ./cmd/pool-lab -transfers 100 -store-dir $poolDirectory
go run ./cmd/pool-lab -ordinary -transfers 100
$ordinaryDirectory = Join-Path $env:TEMP ('next-ordinary-' + [guid]::NewGuid().ToString())
go run ./cmd/pool-lab -ordinary -transfers 100 -store-dir $ordinaryDirectory
```

The directory must be fresh. `-store-dir` uses synchronous Bolt for each member, wallet, and the public engine; omission uses memory. No private production keys or live account balances are involved. CLI output is JSON with every transfer latency sample.

## Funds and timing boundary

The genesis fixture contains a real final CAL UTXO worth 100. Deposit obtains a three-member QC, passes `Engine.Check`, and commits through `Engine.ExecuteAt`. Every member and wallet then applies the resulting block with `blockfollow.Commit` and its own `PrepareBlock` method. Only then can internal transfers obtain approval. The public escrow is read after every transfer and must remain exactly 100. No test injects an escrow balance or constructs member votes to bypass approval.

The 100 transfers rotate ownership A → B → C → A. The timed interval begins at sender `SavePoolRequest`, includes HTTP quorum collection and the two full-certificate installation acknowledgments required before collection succeeds, and ends after recipient `ReceivePool` has verified and stored the receipt. Request construction and signing precede that interval. A separate exit obtains a QC and executes publicly; followers create the recipient's ordinary final coin. That owner then reserves the coin through `SaveDirectRequest` and obtains three HTTP ordinary-payment approvals. The subsequent ordinary payment is approved but is not publicly submitted in this experiment.

Public blocks are made by `testkit.Block`, using deterministic laboratory validator keys, and verified by `finality.VerifyBlock`. These are authenticated synthetic block fixtures, **not a running four-validator consensus network**. Public execution is genuine application execution, but neither public block time nor consensus latency is measured.

## Recorded measurements

Recorded on 2026-10-12 with Go 1.27.1, Windows/amd64. Each row is one local run of 100 sequential transfers, without an isolated-machine or statistical confidence claim. All four reports contain the raw samples.

| Path / storage | p50 (ms) | p95 (ms) | CAL accounting after 100 transfers |
| --- | ---: | ---: | --- |
| Pool / memory | 5.6821 | 7.5388 | Public escrow 100; ordinary member reservation `[0,0,0,0]` |
| Pool / synchronous Bolt | 12.8788 | 14.9952 | Public escrow 100; ordinary member reservation `[0,0,0,0]` |
| Ordinary / memory | 3.4913 | 5.2730 | Member CAL reservation `[10000,10000,10000,10000]` |
| Ordinary / synchronous Bolt | 9.0345 | 11.0301 | Member CAL reservation `[10000,10000,10000,10000]` |

The pool includes full-QC persistence acknowledgments; the ordinary path uses the existing `gateway.CollectDirect` and immediate full parent certificate, with sender `SaveDirectRequest` and recipient `ReceiveDirect`. Both rotate the same three owners, use the same HTTP members and storage configuration, and sign requests before the timed interval. Neither starts background public submission during its transfer loop. Pool latency includes its additional availability phase; these measurements do not establish a latency improvement.

Ordinary certificates retain ordinary reserve-backed liability and fee semantics. The pool moves ownership of one already locked unit without issuing a new ordinary certificate at every hop. The ordinary baseline deliberately does not publicly settle its chain: it produces certificates with total face amount 10,000 CAL and then drains HTTP handlers before reading the four actual local reservation counters. Those counters are **replica-local accounting**, not four independent global capital requirements, and certificate face amount is not newly created money. Pool escrow and ordinary reservations represent different commitments. The observed reduction in ordinary reservations therefore reflects changed liability semantics, not a free optimization of identical guarantees.

Both pool runs finish with escrow zero, an ordinary exit coin of 100 CAL, and successful subsequent ordinary-payment approval. Pool entry and exit use two public fixture blocks. The ordinary baseline uses zero public blocks and leaves its reservations outstanding.

Raw results: [pool memory](pool-memory-100.json), [pool Bolt](pool-bolt-100.json), [ordinary memory](ordinary-memory-100.json), [ordinary Bolt](ordinary-bolt-100.json). Test output: [integration tests](pool-integration-tests.txt), [race tests](pool-integration-race.txt).

The final run of `go test ./internal/poollab ./cmd/pool-lab -count=1 -v` passed all 14 top-level integration tests and compiled the CLI. `go test -race ./internal/poollab -count=1` also passed. The recovery and two-install-acknowledgment regressions are included in both runs.

## Safety coverage

| Test | Required result |
| --- | --- |
| `TestPoolClosedFundsLifecycle` | 100 transfers among three owners; escrow remains 100; exit zeroes escrow, creates 100 ordinary CAL, and supports subsequent ordinary approval |
| `TestOrdinaryCapitalBaseline` | 100 ordinary certificates without public settlement retain at least three member reservations of 100 per certificate |
| `TestPoolDirectDepositShareSpendLock` | Ordinary payment and deposit conflict in both orders, at member and wallet |
| `TestPoolMustObservePublicDeposit` | A deposit QC alone does not enable transfers; public execution and block following are required |
| `TestPoolPersistentVoteAndPendingLock` | Synchronous member store close/reopen preserves the exact vote and blocks a competing exit |
| `TestPoolStaleOwnerAndReceiptsCannotRedeem` | Old owner cannot get an exit QC after transfer; a transfer receipt fails public check and execution; duplicate exit is idempotent |
| `TestPoolMinorityDepositFollowsOrdinaryWinner` | One member's losing deposit vote cannot prevent following the ordinary public winner or be reissued afterward |
| `TestPoolMinorityOrdinaryFollowsDepositAndReleasesDebits` | A losing ordinary minority vote releases its reserved CAL after public deposit and cannot be reissued |
| `TestPoolTransferExitConcurrentQuorumExclusion` | Concurrent transfer and exit never both obtain QCs, repeated ten times |
| `TestPoolMinorityConflictCatchesUpFromFullQC` | Member with conflicting minority vote catches up from the other three members' valid full QC |
| `TestPoolUnavailableMembers` | One stopped HTTP member permits completion; two stopped members prevent completion |
| `TestPoolAuthenticityAndDuplicateVotes` | Wrong network, configuration, member key, misplaced signer, and repeated signer cannot satisfy quorum |
| `TestPoolRequiresTwoCertificateInstallAcknowledgments` | A valid three-vote QC with only one successful full-certificate install is not reported as completion |
| `TestPoolFullCertificateRecoveryAfterRecipientCrash` | With recipient receipt absent, a signer withholding its vote, and the fourth member holding a conflicting minority vote, an honest full QC survives store restart and supports retry and continuation |

Contention safety does not imply contention liveness: simultaneous conflicting requests may split votes 2:2 and both fail to produce a QC. The concurrency test accepts that outcome and requires at most one successful QC.

## Regression development record

Two additional tests cover cross-protocol minority reconciliation. The ordinary-minority/public-deposit test was observed failing before the fix: `Available=666666566, Reserved=100` remained unchanged after public deposit. It passes after reconciliation returns that reservation. The reverse test verifies that a losing deposit voter follows the ordinary public winner and rejects retrying the obsolete deposit. Its harness carries the real `Engine.ExecuteAt` result data into the signed block; omitting that data would cause the ordinary follower to skip the transaction and would not test reconciliation. Final test output and experiment results are recorded alongside this document after the final implementation is verified.
