# Independent direct-delivery review

Reviewed on 2026-10-12 against `delivery-review.diff` (the original 10 delivery files), `DELIVERY.md`, and subsequent retry/TLS fixes in the current source. Pool changes were excluded. Source was read only; this report and `DELIVERY.md` were updated. Code discovery first used the `next-e8-worktree` graph; new delivery/E7 symbols were absent, so direct source reading was used. No Git operations were performed.

## Verdict

The direct-delivery path satisfies the intended certificate flow: the gateway durably queues complete payments before responding, recipients independently verify and persist their outputs, and E7 continuation can start without sender forwarding. Existing QC requirements and public relay paths remain intact. The original P2 retry integration issue is resolved in the current source. No remaining blocking findings were identified in this re-review; the deployment and experiment scope limits below still apply.

## Resolved finding

### P2 — Retry the gateway's new post-QC delivery persistence failure — resolved

Location in changed code: `cmd/gateway/direct.go:105-110`; client integration: `cmd/payctl/e7_run.go:308-310`. Supporting existing retry predicate: `cmd/payctl/direct_chain.go:582-583`.

The original client retried only `QUORUM_UNAVAILABLE`, so one post-QC outbox failure ended E7 even when the same request could succeed on retry. `submitChain` now also recognizes HTTP 503 `DELIVERY_PERSIST_FAILED_RETRY_SAME_REQUEST`, preserving the signed bytes and existing 10-second context/backoff. The E7 metadata transport is retained across attempts, so metadata is attached again. Inputs are never rolled back by this path.

`TestDeliveryEnqueueFailureRetriesIdenticalRequest` independently passed in the fresh package/race run: first response is the new failure, second is success, and request bodies are identical. It tests the retry transport with a placeholder response, not full certificate validation/continuation; those are covered by separate E7 tests. Gateway enqueue-failure/public-evidence preservation remains established by code inspection rather than a new injected-store-failure test.

## Compliance and counterexamples checked

* Routing comes exclusively from operator configuration and signed output ownership. Sender metadata does not supply callback URLs. Duplicate owners, unsupported schemes, credentials and fragments are rejected; redirects cannot escape the configured route.
* Payment serialization checks the transaction/summary binding; the receiver decodes the complete payment and uses `Wallet.ReceiveDirect`, which verifies network, organization, owner, output binding and the existing QC. The HTTP success body follows the wallet update and the optional continuation callback.
* Delivery records and completion tombstones are written transactionally. Re-enqueue cannot resurrect a completed output. Restart scans retain pending offline deliveries; completion after an acknowledgement is atomic with removal of the pending record. Lost acknowledgements permit safe duplicate wallet acceptance.
* E7 metadata includes the transaction identity and validates recipient ownership before workload continuation. Nonzero output indices do not advance the chain. Duplicate receipt acceptance is synchronized and fact-bound. Readiness polling is an observation channel, not a sender-forwarding prerequisite.
* Gateway direct responses wait for durable enqueue, not recipient HTTP completion. Public relay offering/persistence remains present after response, with synchronous public persistence attempted on recipient enqueue failure.
* Four-item pages wait for all workers before scanning the next page. An unresponsive endpoint can delay later pages by the two-second timeout; this is a real head-of-line latency limitation, but not permanent starvation for a finite queue. No throughput guarantee was stated, so it is not a separate blocking finding.

## Deployment and scope limits

The outbound TLS capability gap is resolved: `delivery.NewWithClient` copies the supplied client, retains its transport, and forcibly disables redirects. `cmd/gateway/recipient_tls.go` loads all three `RecipientTLS` fields (`Certificate`, `Key`, `CA`), verifies server roots and hostnames through standard TLS, presents a client certificate, and requires TLS 1.3. Partial TLS configuration fails startup. The gateway wires that client into the outbox. There is no `InsecureSkipVerify` bypass. Operator routes must use `https://` to use TLS.

`TestRecipientTLSClientTransport` passes a configured TLS client through the actual outbox and receiver, proving private server trust and client-certificate presentation survive the integration. Its test server uses `RequireAnyClientCert`; it does not independently test CA validation of the client certificate, PEM loading in `recipientHTTP`, or negative TLS configurations. Those boundaries were inspected statically, so the test must not be described as a full deployment validation.

The supplied E7 receiver still uses `cfg.HTTP(..., cfg.TLS{})` (`cmd/payctl/e7_wallet.go:216`); `cfg.TLS.Server` requires mTLS for non-loopback listeners. E7 therefore remains a loopback lab integration, or needs a separately configured authenticated tunnel/proxy. Outbound gateway TLS support alone does not add E7 inbound TLS configuration or establish a complete cross-host E7 deployment.

The E7 workload scheduler/ack table is intentionally in memory. A wallet restart restores financial outputs but does not resume a completed-and-tombstoned workload continuation. The report explicitly disclaims durable workload scheduling, so this was not treated as a hidden guarantee. Lab metadata is not authenticated and must not be described as a production public scheduling API.

## Verification evidence

Independent fresh runs passed:

```text
go test ./internal/delivery ./cmd/gateway ./cmd/payctl ./internal/wallet -count=1
ok utxo/internal/delivery 3.126s
ok utxo/cmd/gateway 3.234s
ok utxo/cmd/payctl 4.146s
ok utxo/internal/wallet 2.276s

go test -race ./internal/delivery ./cmd/gateway ./cmd/payctl -count=1
ok utxo/internal/delivery 4.916s
ok utxo/cmd/gateway 5.953s
ok utxo/cmd/payctl 5.732s
```

Fresh post-fix re-review run:

```text
go test -race ./internal/delivery ./cmd/gateway ./cmd/payctl ./internal/wallet -count=1
ok utxo/internal/delivery 4.857s
ok utxo/cmd/gateway 5.948s
ok utxo/cmd/payctl 6.025s
ok utxo/internal/wallet 2.732s
```

Tests cover restart of the durable outbox, duplicate enqueue, owner/QC rejection, metadata bounds, unknown routes, redirect rejection, bounded dispatcher concurrency, sender-independent delivery, direct E7 continuation, retry of the new persistence response, and configured TLS transport. The gateway ordering test uses memory storage; durable outbox behavior is covered separately. The visible restart test reopens the outbox, not the recipient wallet; `DELIVERY.md` now reflects this narrower evidence.

The RED-before-implementation sequence is recorded in `DELIVERY.md`; this review can independently confirm current GREEN and race results, but cannot reconstruct or certify historical TDD order from the final diff. The opt-in handoff benchmark was inspected, not rerun. Its stated post-QC, loopback, synchronous-storage scope is appropriate and does not establish a full E7 or WAN speedup.
