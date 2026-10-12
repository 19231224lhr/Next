# Direct certificate delivery — implementation and measurements

Date: 2026-10-12. Baseline: `7a2ab17`, existing `.e8-worktree`. No commit created. This report covers delivery only; the separate internal-funds protocol is outside this patch.

## Result

An opt-in gateway path durably enqueues the complete `DirectPayment` before replying to the sender. A bounded dispatcher concurrently posts it to operator-configured recipient endpoints. The receiver decodes the full payment and calls the existing `Wallet.ReceiveDirect`, preserving output binding, organization/network checks, the existing three-of-four QC verification, and wallet persistence. Public relay/evidence recovery continue independently.

Offline recipients remain queued across gateway restarts. Four concurrent requests, a four-record scan page, two-second per-request timeout and a 100ms idle/retry sweep bound active work. The scan cursor advances past failed records. Successful output IDs receive durable completion tombstones; repeated enqueue does not reopen completed deliveries. Lost acknowledgements can cause repeated POSTs; wallet acceptance is idempotent. Pending records and tombstones consume disk until an operator retention policy is added; there is no automatic expiry that silently loses delivery.

Routes are fixed configuration entries keyed by recipient public key. Every output must have a route before quorum collection. Sender metadata cannot select a URL. Only HTTP(S) routes without credentials/fragments are accepted, and delivery redirects are disabled. An enqueue failure after a valid QC preserves the existing public outbox and returns `DELIVERY_PERSIST_FAILED_RETRY_SAME_REQUEST`; callers must retain and retry the same signed request, never roll back its reserved inputs. The supplied E7/chain submission helper now retries this response with identical signed bytes and unchanged metadata, within its existing 10-second context.

## Running

Gateway JSON accepts `RecipientRoutes`, an array of `{"Owner": <public-key value>, "URL": "http://recipient:port/v3/receive"}`. Copy each `Owner` value exactly from the existing network genesis receive descriptor, including routes for sender change outputs. Existing project public-key JSON encoding is retained.

For a recipient endpoint requiring mutual TLS, use an `https://` route and add gateway configuration such as:

```json
"RecipientTLS": {
  "Certificate": "C:/certs/gateway-client.pem",
  "Key": "C:/certs/gateway-client-key.pem",
  "CA": "C:/certs/recipient-server-ca.pem"
}
```

All three fields must be supplied together. `CA` trusts the recipient server; the recipient server must separately trust the gateway client certificate. Server hostname verification remains enabled, TLS 1.3 is required, and redirects remain disabled. `RecipientTLS` configures outbound delivery independently of the gateway's existing inbound `TLS` setting. `delivery.NewWithClient` also permits embedding the outbox with an explicitly configured HTTP transport.

Start the gateway with `go run ./cmd/gateway -config <gateway.json> -direct-recipient-delivery`. The separate, synchronous `recipient-delivery.db` is created in its configured DataDir; even the existing experimental memory-gateway mode cannot make this outbox ephemeral.

For E7 wallets, set `DirectDelivery: true` in **both** participating wallet configurations and retain their existing `Receivers` base URLs. The opt-in wallet exposes `POST /v3/receive` and `GET /v3/receive/ack?index=N`, and uses synchronous storage. Start the existing `payctl e7-wallet -config <wallet.json>` command and run the normal E7 workload. The sender includes bounded workload metadata with its request. The gateway persists and forwards that metadata, while always selecting the destination from signed output ownership and operator routes. After verification and persistence, the recipient directly queues the next chain hop; it does not wait for a sender-forwarded certificate or metadata message. Nonzero change-output indices are persisted without advancing the workload chain.

The supplied E7 server still has no inbound TLS configuration and must bind a loopback address. Native non-loopback E7 listeners are rejected by the existing server configuration. Cross-host E7 needs a separately configured authenticated tunnel/proxy or additional inbound TLS support; outbound `RecipientTLS` alone does not supply that deployment.

`DirectDelivery` defaults to false, preserving the sender-forwarding baseline. Set the new independent `Durable: true` on baseline E7 wallets when comparing identical durable storage. Direct mode implies durable storage. E7 `ReadyNS` remains conservative: the sender learns readiness via a fact-bound acknowledgement poll, so its duration includes response/poll transport. Direct-mode `ReceiveNS` is zero because verification has already finished before E7 bookkeeping starts. Use the experiment below for the precise receiving-wallet commit boundary. E7 workload metadata and in-memory phase/continuation bookkeeping are laboratory facilities, not a durable workload scheduler or an authenticated public notification protocol; financial acceptance remains certificate-bound.

## TDD and verification evidence

RED commands before implementation:

* `go test ./internal/delivery`: failed with missing `Handler`, `New`, `Route`, and `send` APIs (new package behavior absent).
* `go test ./cmd/gateway -run TestDirectDelivery`: failed with missing `newDirectPaymentHandlerWithDelivery`.
* `go test ./cmd/payctl -run TestE7DirectMetadata`: failed with missing metadata/client/ack APIs.

GREEN commands:

* `go test ./internal/delivery`: passed after implementation. A fixture initially omitted required durable-store identity fields; those test setup fields were corrected before the passing run.
* `go test ./cmd/gateway -run TestDirectDelivery`: passed. A blocked receiving endpoint does not delay the sender response, and an outbox row exists before response completion (this ordering test uses memory storage; durable storage is tested separately).
* `go test ./cmd/payctl -run TestE7`: passed, including metadata attachment, fact-bound acknowledgement and unchanged E7 behavior.
* `go test ./cmd/payctl -run TestE7DirectReceive`: passed. Direct receipt queues the next hop without any sender forwarding; repeated receipt does not queue another hop.
* `go test ./internal/delivery ./cmd/gateway ./cmd/payctl ./internal/wallet`: all four packages passed.
* `go test -race ./internal/delivery ./cmd/gateway ./cmd/payctl`: all three packages passed before final additional boundary tests; final rerun recorded below.

Tests additionally cover synchronous receiving-wallet commits and outbox store reopen, offline HTTP failure then restart recovery, duplicate enqueue, wrong recipient, invalid QC, excessive metadata, missing routes, redirect rejection, and four-worker concurrency while recipients are blocked. The new restart test reopens the outbox, not the recipient wallet. Existing gateway and wallet tests remain intact.

Post-review fixes add `TestDeliveryEnqueueFailureRetriesIdenticalRequest` (503 persistence failure followed by success, preserving request bytes) and `TestRecipientTLSClientTransport` (real TLS outbox delivery with private server trust and a required client certificate). The TLS fixture requires a certificate but does not validate its issuing CA; PEM configuration loading and TLS failure cases were reviewed statically rather than covered by that fixture. Independent post-fix verification passed: `go test -race ./internal/delivery ./cmd/gateway ./cmd/payctl ./internal/wallet -count=1` — delivery 4.857s, gateway 5.948s, payctl 6.025s, wallet 2.732s. The original P2 retry finding is resolved; see `DELIVERY-REVIEW.md` for the re-review and scope limits.

## Networked handoff experiment

Run from the worktree in PowerShell:

```powershell
$env:UTXO_DELIVERY_EXPERIMENT='1'
go test ./internal/delivery -run TestNetworkHandoffExperiment -count=1 -v |
  Tee-Object docs/experiments/delivery-pool-2026-10-12/delivery-handoff-raw.txt
Remove-Item Env:UTXO_DELIVERY_EXPERIMENT
```

This uses real loopback HTTP gateway and recipient servers, valid owner-signed payments and three-member QCs, fresh output identities per sample, and synchronous Bolt wallet commits in both modes. Direct mode additionally synchronously commits the outbox. Timing starts at gateway HTTP invocation with the QC already prepared and ends after the identical receiving handler completes full verification and wallet persistence. Sender response delay models the extra gateway-to-sender hop before forwarding; recipient delay is applied equally to either receiving request. Twenty samples per mode/case, 240 samples total. It is a post-QC handoff experiment, not a full committee benchmark or measured real-WAN deployment. Initial connections are included; no warmup samples are discarded. One experiment harness setup failure caused by changing transaction nonce without refreshing owner authentication was fixed before the recorded run.

| Sender response delay | Recipient delay | Sender-forward median / p95 | Direct median / p95 |
|---:|---:|---:|---:|
| 0 ms | 0 ms | 2.757 / 4.727 ms | 5.044 / 7.018 ms |
| 0 ms | 20 ms | 24.597 / 25.819 ms | 27.949 / 30.104 ms |
| 20 ms | 0 ms | 25.429 / 27.334 ms | 7.064 / 8.623 ms |
| 20 ms | 20 ms | 45.171 / 46.210 ms | 28.661 / 29.913 ms |
| 50 ms | 0 ms | 56.941 / 62.179 ms | 8.712 / 11.135 ms |
| 50 ms | 20 ms | 76.810 / 81.646 ms | 30.004 / 32.040 ms |

Observed direct delivery is slower by approximately 2–3 ms without sender-path delay, consistent with extra durable enqueue work. With 20/50ms sender-path delays, it removes most of that delay from recipient readiness. This benefit does not depend on same-organization operation: it follows from eliminating the sender-forwarding dependency. No throughput, full-chain E7 speedup, reserve saving, public finality speedup, or production-WAN claim is made. Raw per-sample data: `delivery-handoff-raw.txt`.

## Owned files and self-review

* New: `internal/delivery/delivery.go`, `delivery_test.go`, `handoff_test.go`.
* Changed: `cmd/gateway/direct.go`, `direct_test.go`, `main.go`.
* New: `cmd/payctl/e7_direct_delivery.go`, `e7_direct_delivery_test.go`.
* Changed: `cmd/payctl/e7_wallet.go`, `e7_run.go`.
* Post-review additions: `cmd/gateway/recipient_tls.go`, `internal/delivery/tls_test.go`, `cmd/payctl/delivery_retry_test.go`; further changes to `cmd/payctl/direct_chain.go`, gateway configuration, and the delivery constructor.
* Artifacts: this report, `delivery-handoff-raw.txt`, `delivery-review.diff`.

No pool-related paths or paper files were edited. Self-review retained full validation, prohibited redirect-based route escape, preserved public recovery on delivery storage failure, prevented completed-job resurrection, separated lab acknowledgement observation from actual receiving commit time, and preserved baseline opt-in behavior. Publicly exposing lab metadata/ack endpoints, workload restart/resumption, retry backoff/operational delivery dashboards, and tombstone compaction are not implemented. Full repository suite/build and independent review are owned by the parent task, with this exact source patch supplied for review.

Original implementation race rerun: `go test -race ./internal/delivery ./cmd/gateway ./cmd/payctl` passed: delivery 4.861s, gateway 5.800s, payctl 5.822s. The original source-only patch, including new files, was saved as `delivery-review.diff` (10 files); the current-source retry/TLS fixes were reviewed separately as documented above.
