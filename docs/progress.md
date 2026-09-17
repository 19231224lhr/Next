# Implementation progress

Baseline: protocol v1.1, ProtocolVersion=2 / WireVersion=2.
Primary checkout: /Users/richz/lab/man/utxo-fastpay (Mac Studio).
Branch: implementation/core.
This ledger records actual capability; unimplemented items are not test passes.

| Module | Status | Evidence / remaining work |
|---|---|---|
| Protocol, engineering and storage | in progress | Explicit amount/codec/identity/transaction/certificate types; memory+bbolt atomicity/restart contracts. Full schema/golden corpus still expanding. |
| Organization signing and background INSTALL | in progress | Durable approval, no double vote, parent import, no-ACK child approval, INSTALL, fixed Worker slices. Runtime queues, rebalancing, proof-driven credit and service interfaces remain. |
| Committee settlement and guarantees | in progress | Real Comet ABCI types; pending/committed isolation, deterministic state writes, replay. Payment execution, typed fact root/proof service and actual four-node consensus integration remain. |
| Fees, cumulative credit and refill | in progress | Pure residual/credit and fee escrow rules tested. Public accounts, actual funding/claims, proof application and refill still missing. |
| Gateway, peers and wallet | not started | Member verifier accepts only supplied trusted historical configurations; no network registry or wallet process yet. |
| Scheduling, archive and operations | not started | Durable outbox records exist; no dispatcher/archive or capacity feedback yet. |
| Tests, benchmarks and architecture reference tool | in progress | Foundational and member scenarios implemented; no complete sustained benchmark or archgen yet. |

## Verified increments
- Checked addition/subtraction/wide multiply-divide; independently computed primitive byte vector.
- Canonical round trips, malformed lengths/trailing bytes, network/config/route/output signatures.
- Quorum requires distinct signers; subsets share a fact; modified effects/vector rejected.
- Memory and bbolt rollback, concurrent read-check-write isolation, copied reads, identity mismatch and same-file restart.
- Cumulative 100/94/6/40 accounting; stale/duplicate credit, invariant/overflow rejection.
- Fee registration/settlement/close and replay; held escrow is drained by close rather than required to be zero first.
- ABCI Finalize does not publish; Commit persists result and identity; restart/replay and empty-block root behavior.
- Members retain signed locks on restart; underfunded approval rolls back all input/resource changes.
- Four members form a valid spendable certificate; child approval proceeds before parent INSTALL.
- Late parent installation does not undo child locks; conflicting local candidate survives without budget release.
- Cross-organization parent validation and child certification do not wait for parent installation or add CAL coverage.

These are scenario-level checks, not a claim that every requirement under the corresponding
architecture TestID has passed. The full C/M/B/R/F/P/A/X/S/L/O/Q matrix remains the acceptance authority.

## Current implementation limits
- Ordinary finalized-CAL inputs and valid parent certificates; fast fees use the configured reserve policy.
- Root issuance/fulfilment, fast self-funded fees and committee direct transfers are not yet wired.
- Final inputs currently originate from trusted finite genesis state; public proof import is pending.
- Fixed Worker partitions; no automatic quota redistribution yet.
- No completed public fact proofs or cumulative member-credit application yet.
- No production listeners, automatic cancellation, retirement release, state repair or online migration.
- Current byte reservations use a conservative certificate-envelope upper bound independent of QC subset.
  This is deterministic and bounded; measure and refine before claiming capital/storage efficiency.
- Comet adapter's fact-tree integration is pending; its current empty-fact root is only a commit-boundary scaffold,
  not a finality proof exposed to clients.
- No end-to-end TPS result exists.

## Environment
- Go 1.27.1 on macOS arm64; bbolt v1.5.0 and CometBFT v0.38.26.
- Default module proxy timed out from the Mac; reachable per-command GOPROXY used with checksum verification.
- Linux deployment validation must use matching Go; Windows remains the control endpoint.
