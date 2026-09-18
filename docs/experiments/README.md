# Selected laboratory observations

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
