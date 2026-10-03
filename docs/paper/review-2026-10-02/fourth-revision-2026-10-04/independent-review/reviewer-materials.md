# Next manuscript and reproducibility materials

This packet accompanies the English manuscript and supplementary material. The manuscript describes a native UTXO ledger with quorum-certified continuation, direct issuer obligations, public compensation and source repayment, and optional authorized historical block-body representation.

## Version identity

Production baseline: f856c7b3d6265f7275a15767bdced52d40dafada. The final-build chain and representative P runs use identical node binaries. Only client observation instrumentation was added for timing; six chain runs include those probes. A source manifest covers 309 files. The raw artifacts retain node/Comet/source hashes and runners.

## Current-build evidence

- Six runs: three fast and three wait 100-hop chains, 600 payments closed. Medians 4.706858875 s and 64.478895625 s from first actual send to last certified receipt. All 297 fast successors used a parent TXCer and were first sent before the earliest parent application-commit completion across four replicas. Actual child-before-source obligations: 2, 4, and 1, subsequently fulfilled.
- Three representative paused-adaptation P runs: each schedules 7,000 ordinary payments over 70 s, plus eight cross-organization parent/child pairs. All 21,048 payments closed. In every run 800 CAL was paid and recovered. All 96 replica/target observations established Recovered while representation Committed and Materialized remained false before adaptation resumed.
- Ordinary receipt P50/P95 across run statistics: 54.538/147.549 ms. All three runs reached the driver cap of 256 outstanding payments. Dispatch P95 reached 780.727 ms; scheduled-to-receipt P95 reached 851.742 ms. Both are explicitly disclosed. This finite workload is not a sustained-throughput claim.
- Actual encoded negative submissions enter CheckTx, ProcessProposal and FinalizeBlock directly and are rejected without business-state changes. Existing eight-package regressions pass. These are functional checks, not mechanized or exhaustive security proofs.

## Other evidence retains its original version

The 721800c full N/R/C/P matrix and timed historical-read comparison remain archived. The 9879f64 network-delay and boundary/restart experiments retain their recorded build and scope. The current same-consumer test compares index A, plain materialized funding row M, and authorized body B at prefixes before/after actual repayment. All return the same economic answer; B additionally puts the revised funding representation in a body compatible with the saved original complete BlockID/commit. M has no timing claim. The reader is an executed local replica, not an untrusted remote server.

## Supporting paths

- docs/design/payment-service-contract.md
- docs/design/historical-reader-contract.md
- docs/research/proof-code-map-wire4.md
- docs/experiments/final-evidence-2026-10-04/README.md
- docs/experiments/final-evidence-2026-10-04/source-manifest.json
- docs/paper/review-2026-10-02/security.tex

Additional implementation or source snippets can be supplied for a specific question. Please distinguish demonstrated behavior, model assumptions, and proposed generalizations when reviewing.
