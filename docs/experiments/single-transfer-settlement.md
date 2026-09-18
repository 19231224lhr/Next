# Single-transfer settlement timeline

One 100-CAL payment from an unused finalized UTXO (input 4). Same-host 14-process Mac lab; restarted to enable UTXO_SETTLEMENT_TRACE=1. Four committee health checks reported the same height before submission. No relay or consensus parameters changed.

Spend: e53d03916a4fdd765219a574b5e9f141ced3dc6578d6dafb57c66bb6e0e523ac
Settlement block: 2768; proof header: 2769.
Selected delivery attempt: 2e83fab9fe59be58033edd9164345a5a83ded58e06e011b507a28b4ad7186a4b (sender gateway); this is the first command executing in the authenticated settlement block.

| Stage | Milliseconds from wallet HTTP submit |
|---|---:|
| Wallet READY | 44.584 |
| DeliveredUnixNS | 31.177 |
| ReceivedUnixNS | 31.416 |
| AcceptedUnixNS | 32.323 |
| ExecuteStartUnixNS | 756.359 |
| ExecuteDoneUnixNS | 756.477 |
| FinalizeDoneUnixNS | 756.574 |
| CommitStartUnixNS | 764.427 |
| CommittedUnixNS | 778.387 |
| Committed state observed by polling | 782.480 |
| Final output proof verified | 1188.861 |

## Diagnosis

{
  "attempts_at_snapshot": 28,
  "executed_by_snapshot": 21,
  "attempts_in_settlement_block": 5,
  "receive_to_execute_ms": 724.943,
  "accepted_to_execute_ms": 724.036,
  "execute_function_ms": 0.118,
  "block_finalize_remainder_ms": 0.215,
  "finalize_to_commit_call_ms": 7.853,
  "app_commit_ms": 13.96
}

The dominant observed interval is after committee acceptance and before block business execution. It includes proposal/consensus scheduling and any intervening waits; this instrumentation does not subdivide the Comet consensus stages. The rule-function duration excludes earlier authentication/prechecks and surrounding state application. Commit timing is committee 0 application Commit, not all validators finishing and not all consensus persistence. Cross-process offsets use a same-host wall clock; end-to-end wallet measurements use a monotonic clock. Delivery timestamps are diagnostic sender headers, not authenticated protocol evidence.

Repeated submission attempts use distinct delivery nonces while sharing one immutable payment identity. The trace demonstrates duplicate consensus work, not multiple payments or multiple fees. Snapshot counts include queued retries; the final proof may become available before every duplicate is processed. No duplicate-submission optimization was made in this experiment.
