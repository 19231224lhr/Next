# Same-organization continuation implementation plan

User authorizes design, implementation, measurement and sidebar GPT review within three hours. Existing dirty worktree is the baseline; no unrelated changes or paper claims.

## Design

For a parent certified by the same organization, its signers already retained its transaction and admission vector before voting. Send them the child transaction plus the parent's complete QC and output index. Reconstruct the parent summary from that retained approval, then run the existing certificate/input/lock/budget checks. Send the full request directly to members absent from any parent QC. Missing retained material falls back to the full request. Returned certificates and stored recovery evidence remain complete and compatible. This removes an optimization's dependence on background INSTALL, not a round from the existing protocol.

No local vote substitutes for a QC. No reserve netting, fee changes, database tuning or durability changes. A bounded memory copy may accelerate retained-summary lookup; misses use the existing persistent approval. Immutable copies only, populated after successful approval commit.

## Steps

1. Red/green integration test: immediate dependent child succeeds without INSTALL; full QC still required; retained full parent evidence.
2. Add bounded compact wire encoding and targeted gateway dispatch with safe transport fallback.
3. Adversarial tests: altered QC, wrong parent, duplicate parent, missing state, unsupported peer, restart, double spend, quota, public execution.
4. Compare baseline and compact mode on identical signed workloads, no INSTALL prewarming, memory and durable stores measured separately; collect latency, bytes, fallback counts. Do not call generic savings same-group novelty.
5. Send implementation/results to sidebar GPT, challenge assumptions, fix justified findings, rerun focused checks.
6. Write measured report, scope and rejected alternatives; keep production opt-in pending real deployment evaluation.

## Related designs consulted

- FastPay Fig. 2: local recipient updates versus cross-shard notification (https://sonnino.com/papers/fastpay.pdf).
- Sui Lutris dependency availability: certifying validators hold required inputs; dependencies can be recovered from signers (https://docs.sui.io/paper/sui-lutris.pdf).
- Stingray concurrent bounded-counter operations address a different workload/state model (https://arxiv.org/abs/2501.06531).

The above inform state-locality and dependency handling, not a claim that Next matches their protocols or performance.

## Completion
Implemented opt-in source continuation and compact responses. Completed four-arm, six-condition measurements (19,200 payments), full regression, race checks and sidebar GPT review. Results and activation instructions: ../../experiments/same-org-design-2026-10-12/REPORT.zh-CN.md. No paper edits or running-service changes. Current source and raw-result hashes are recorded in manifest.json.
