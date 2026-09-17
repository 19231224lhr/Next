# Implementation progress

Baseline: protocol v1.1, ProtocolVersion=2 / WireVersion=2.
Primary checkout: /Users/richz/lab/man/utxo-fastpay (Mac Studio).
Branch: implementation/core.
This ledger records actual capability; unimplemented items are not test passes.

| Module | Status | Evidence / remaining work |
|---|---|---|
| Protocol, engineering and storage | in progress | Explicit amount/codec/identity/transaction/certificate types; memory+bbolt atomicity/restart contracts. Full schema/golden corpus still expanding. |
| Organization signing and background INSTALL | in progress | Durable approval, no double vote, parent import, no-ACK child approval, INSTALL, fixed Worker slices. Bounded group commit, HTTP interfaces and proof-driven credit wired; quota rebalancing remains. |
| Committee settlement and guarantees | in progress | Real Comet ABCI types; pending/committed isolation, deterministic state writes, replay. Typed facts, authenticated h+1 proofs, real four-node payment settlement, deferred child registration and retail direct execution tested. Root and funding commands remain. |
| Fees, cumulative credit and refill | in progress | Pure residual/credit and fee escrow rules tested. Finite reserve accounts and fee stages wired; proof-driven original-member credit tested. Real refill/reward claims and B handoff remain. |
| Gateway, peers and wallet | in progress | Trusted genesis configurations, HTTP quorum collector, durable intents/received certificates and independent gateway processes. Dynamic first-contact registration and wallet retry UI remain. |
| Scheduling, archive and operations | in progress | Durable paged outbox relay, separate HTTP foreground/background capacity and public custody replacement. Physical archive, capacity feedback and optimized dependency scheduling remain. |
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
- Root issuance/fulfilment and fast self-funded fees are not yet wired; retail direct fees are self-funded.
- Final inputs originate from finite trusted genesis or verified public output-creation proofs.
- Fixed Worker partitions; no automatic quota redistribution yet.
- B release now atomically persists a verified public-custody replacement binding the certificate object and effects. Local original data remains as history; physical archive and disk limits are still required.
- Laboratory HTTP listeners exist; no automatic cancellation, retirement release, state repair or online migration.
- Current byte reservations use a conservative certificate-envelope upper bound independent of QC subset.
  This is deterministic and bounded; measure and refine before claiming capital/storage efficiency.
- Reward totals currently accumulate in explicit organization/committee reward accounts; individual beneficiary allocation/claim commands remain.
- No end-to-end TPS result exists.

## Environment
- Go 1.27.1 on macOS arm64; bbolt v1.5.0 and CometBFT v0.38.26.
- Default module proxy timed out from the Mac; reachable per-command GOPROXY used with checksum verification.
- Linux deployment validation must use matching Go; Windows remains the control endpoint.


## Second implementation increment
- Real four-validator CometBFT, separate identities and bbolt application stores: certificate settlement at h, authenticated receipt at h+1, original member applies only six unused FUEL units.
- Synthetic proof tests additionally reject foreign committees, wrong heights, altered facts, insufficient signatures and wrong original caps.
- INSTALL-only members cannot claim credit or establish a first debit after observing public completion.
- Public payment registration survives a missing parent, then settlement/close run once when dependencies arrive.
- Direct committee transactions consume principal and fee inputs atomically, preserve route restrictions and produce deterministic FUEL change without TXCer.
- Genesis funds and fixed authorization/configuration are bound into persistent identities and the initial application root.
- Integration caught a local-metadata/business-key namespace collision; a dedicated regression test now separates them.
- This integration runs four nodes inside one Go test process. Separate executable processes and network service acceptance are still required.


## Multiprocess laboratory increment
- Added member, committee, gateway and payctl executables; init-lab, lab-run, demo and stopped-database audit.
- Actual 14-process, two-organization lab completed 8 alternating transfers, then restarted the same databases and completed another 8.
- First functional run wallet READY observations: approximately 52–225 ms. These are sequential smoke measurements, not a sustained throughput result.
- Initial stopped-state audit: every committee had 8 closed payments, 672 FUEL rewards and 80 burned; CAL supply 12,800 and total FUEL accounting 2,000,000,000,000 remained conserved.
- The audit exposed unnecessary repeated INSTALL enqueueing after public completion. INSTALL now returns idempotently without reopening a completed outbox; a regression test covers it.
- Validated opportunistic group commit: 32 successful concurrent operations and one business rejection used two underlying commits; signatures still wait for durable success.
- Added delivery-attempt envelopes to retry a deferred command without changing its payment or fee identity despite Comet mempool byte caching.
- Existing laboratory origin inputs 0 and 1 were reserved by failed demo attempts before the first service startup; no locks or databases were cleared. Demo now checks gateway availability before reserving a new input. General wallet resume remains to be implemented.
- Current relay is correctness-oriented and still polls/duplicates work more than desired. Its throughput is not yet the target architecture's optimized backend.
