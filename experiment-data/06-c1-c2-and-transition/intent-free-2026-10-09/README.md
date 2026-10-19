# Current protocol v5 experiments

Fresh source snapshot after removing payment Intent and enforcing immutable
wallet-to-organization bindings from genesis. The Git HEAD is a reference;
the source file manifest and archive SHA-256 identify the actual working tree.
Legacy `chain-v4`, `bench-v4`, `demo-v4`, and `budget-v4` command names remain
CLI aliases; their transactions in this snapshot use wire/version 5.

Experiment work was paused at the author's request on 9 October 2026.
The single-organization pilot completed all nine F/W/M runs. Cross-organization
F and W completed one run each; M timed out because the driver queried the
recipient organization for a source fact whose observation status is recorded
by its issuer. Mixed-load and finite-capital jobs were not started.
These pilots are not used as new measurements in the manuscript. The paper
retains earlier performance results under their original build identities.
No additional experiments are required for the current writing revision.

The current protocol regression passed `go test -tags=comet_v3 ./... -count=1`.
The source archive and manifest remain frozen; no runtime private keys are
exported. Before any future cross-organization M run, its observation gate
must query the parent issuer's members. This is an experiment-driver correction,
not a change to payment authorization.
