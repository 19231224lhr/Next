# Selected laboratory observations

- [Database optimization and controlled comparisons](database-optimization-2026-09-20/README.md): four changes, 9,500 performance-test payments, real repair/restart validation, intermediate regressions and separate fresh/grown-database results.

These are single-host diagnostic samples from the Mac Studio prototype, not
production or sustained-throughput claims. Raw reports retain phase timestamps,
delivery-attempt identities and limitations. No laboratory keys or databases are
included. See the project README for the laboratory commands and timing scopes.

- single-transfer-repeat.json: three isolated finalized-UTXO payments after moving gateway persistence off the response path.
- single-transfer-settlement.json / .md: one payment traced through backend delivery and committee commitment.
- single-transfer-async.md: initial trace after the gateway response change.
- backend-review-2026-09-18.md: two-round review, confirmed delivery amplification, and the minimal measurement/fix sequence; no performance fix is claimed yet.

The backend trace observed 28 delivery attempts for one immutable payment.
Its longest measured interval was committee admission to block execution;
individual Comet consensus phases had not yet been instrumented.

- [35-payment phase diagnosis](latency-phase-2026-09-18/README.md): reversible propagation-parameter experiments, raw four-node traces and scripts. Defaults and delivery logic remain unchanged.

- [Fresh-genesis retry repair](relay-retry-2026-09-18/README.md): tracing-off paired runs, stable retry envelopes, offline financial audit, and wallet restart/drain.
