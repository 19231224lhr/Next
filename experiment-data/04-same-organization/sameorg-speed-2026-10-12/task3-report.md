# Task 3: parallel durable completion

Implemented in `C:/Users/Bocchi/Desktop/Next/.e8-worktree` without staging or committing. The scoped source diff against `.scratch/task3-start` is `task3.diff` in this directory.

## API and behavior

- `gateway.CollectPoolCertificate(ctx, org, members, request)` collects/retrieves a verified full QC without installing it. Existing `gateway.CollectPool` calls it and still requires two member installation acknowledgments before returning success to non-receiver callers. Task 2 approve-first recovery remains in the collection core.
- `wallet.CompletePool(ctx, certificate, [4]PoolInstaller)` overlaps full-QC recipient verification and durable receipt save with installation on configured member slots. It returns success only after receipt persistence and two successful member installs. `CompletePoolSerial` performs the same operations serially for comparison.
- Both gateway and wallet installation helpers use a local cancelable context. They cancel remaining member RPCs once two durable acknowledgments arrive or the helper returns with an error.
- Wallet readiness is a mutex-protected in-memory map from pool to exact serialized certificate identity. `SavePoolRequest` requires the received transfer parent to equal the durable receipt head and that head to be ready. A newer receipt or a different valid QC for the same action clears readiness, and a new wallet instance starts unready. Identical duplicate receipts use a read-only fast path and add no wallet write. Deposit parents retain their previous handling; member approval enforces public deposit finality.
- `poollab.Run` now uses certificate collection followed by `CompletePool`; `RunSerial` uses the same readiness gate. `cmd/pool-lab -serial-completion` selects serial mode. `poolctl -receive` confirms member availability as it stores the receipt, and a sending command reconfirms a supplied internal transfer parent after its per-command wallet restart, before `SavePoolRequest`.

## Validation

- Red tests first: the new gateway certificate-only test failed with missing `CollectPoolCertificate`; the wallet completion tests failed with missing `CompletePool`/`PoolInstaller`; the serial lab test failed with missing `RunSerial`.
- `go test ./internal/wallet ./internal/poollab ./internal/gateway ./cmd/poolctl ./cmd/pool-lab -count=1` passed.
- `go test -race ./internal/gateway ./internal/wallet ./internal/poollab -count=1` passed.
- `go run ./cmd/pool-lab -h` confirmed `-serial-completion`.
- Regressions cover durable receipt saved during blocked member installation while exit is rejected; one install failure; wallet persistence failure propagation; no duplicate wallet update; restart requiring two fresh confirmations; older/conflicting receipt handling; a different QC for the same action; concurrent newer arrival during older completion; and cancellation of two blocked installation RPCs after two fast acknowledgments in both gateway and wallet paths.

## Changed paths

`internal/gateway/pool.go`, `internal/gateway/pool_certificate_test.go`, `internal/wallet/pool.go`, `internal/wallet/wallet.go`, `internal/wallet/pool_completion_test.go` (new), `internal/poollab/lab.go`, `internal/poollab/lab_test.go`, `cmd/poolctl/main.go`, `cmd/pool-lab/main.go`.
