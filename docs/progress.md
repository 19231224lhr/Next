# Committee verification reuse (2026-09-19)

Direct public submissions now reuse successful immutable verification results by
the hash of their complete wire bytes within one fixed-policy Engine. Ledger
checks still run on every execution. Sync, organization authorization, owner
signatures, input guarantees and Comet rules are unchanged. Trace-only committee
storage breakdowns were added separately after the cache comparison.

Thirteen fresh-genesis rounds (1,300 payments) passed state, balance, fee and
outbox audits. Four untraced rounds per mode reduced mean 100-payment closed-loop
time from 2.406 to 2.241 seconds (6.8%). Foreground pooled P95 increased from
252.557 to 274.281 ms; its cause remains unresolved. Tagged full tests, focused
race tests, vet, build and warm-cache historical-repair tests passed.

See [implementation, stage comparisons and persistence findings](experiments/committee-verification-cache-2026-09-19/README.md).
These are bounded experiments, not a sustainable throughput claim.

# Earlier wire4 increments (2026-09-19)

Wallets and members follow verified public blocks. The gateway now wakes after
durable outbox persistence and runs public submission and member INSTALL in
independent rolling lanes (4 submissions, 4 target INSTALL RPCs). Members persist
a first fallback deadline of 2 seconds plus 250 ms per member index; duplicate
INSTALL and restart preserve it. Only verified public completion ends retries.

See [gateway scheduling results](experiments/gateway-relay100-2026-09-19/README.md)
and [member fallback implementation and results](experiments/gateway-relay-fallback-2026-09-19/README.md).
These are bounded batch experiments, not a sustained TPS claim. Retry timing
remains durable; in-memory retry pacing is still a separate proposed optimization.

The v1.2 and v1.1 entries below are historical checkpoints.

# Direct-liability v1.2 implementation (2026-09-19)

The isolated wire-v3 branch now runs detached TXCer, one-round approval,
background INSTALL, child-before-parent settlement, direct CAL coverage,
automatic compensation, real Comet block/part revision, original-history replay
and independently spendable late-output instances. Existing v1.1 data is intact.

Full default/v3 tests, v3 race tests, vet and builds passed. A 14-process network
and stopped audits verified 588 closed payments, zero pending outboxes/gaps,
identical application state on all four committee nodes, balanced CAL/FUEL and
one real historical revision per node. Application reconstruction from genesis
against already-redacted Comet history also passed.

Short closed-loop experiments complete about 13 payments/s; they do not establish
high TPS. Latest-revision light-client proofs, v3 retail, real refill/reward
operations, physical archive and sustained/fault-matrix tests remain incomplete.
See [implementation, reproducible commands and raw reports](implementation-v1.2.md).

The entries below are retained v1.1 history, not current v3 behavior.

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

## Single-payment diagnostic instrumentation

Added opt-in `payctl demo -trace` stage timestamps across wallet, gateway and
member HTTP approval. No extra quorum wait or per-event disk log. Race tests for
member/transport/gateway/tracing pass; diagnostic mode also covers one-offline
member quorum. First real-process single finalized-UTXO sample after restart:
54.275 ms certificate verified, 81.256 ms recipient persisted, 1.410354 s final
proof observed. Raw trace: `experiments/profile-001/reports/demo-1789697834159121000.json`.
Timing definitions and limitations are in README. This adds measurement only;
no fast-path optimization is claimed.

## Gateway persistence removed from foreground

Collector now returns after quorum verification. HTTP sends an exact-length full
body and flushes before background Persist, reusing its existing bounded handler.
Wallet demo/bench start the existing relay; standalone wallet-relay resumes saved
outboxes without the original gateway. Protocol, architecture and execution-plan
copies are synchronized with the Windows documents.

Validation: full race suite and four-member Comet integration passed; after HTTP
handler extraction, affected race tests and command vet/build passed again.
Tests cover return with failed gateway storage, complete HTTP body while gateway
storage is blocked, persisted replay, and wallet-store reopen followed by relay
delivery without installation acknowledgements.
A new single-payment diagnostic sample measured 21.156 ms first verification,
36.328 ms wallet READY and 1.060992 s observed final proof. Report:
experiments/single-async-001/reports/demo-1789698416865072000.json.
This is one new-lab sample, not a controlled speedup or sustained-TPS claim.

## Single finalized-UTXO block observation

One new transaction on single-async-001 (input 3): wallet READY 50.240 ms,
committee 0 committed SETTLED first observed at 796.527 ms, output proof verified
at 1304.771 ms. Authenticated settlement height 1405; proof header height 1406.
Observation includes 10 ms polling plus query delay and all delivery/consensus/
storage time from wallet submission. This is not pure BFT execution time.
Raw report: experiments/single-async-001/reports/demo-1789699007412202000.json.
The command observer test, race check, vet and build passed. No performance or
consensus parameters were changed for this measurement.

## Backend stage timing: one payment

Opt-in bounded in-memory timing now links delivery attempts through committee
reception, admission, rule execution and application commit. Enabled-mode
four-validator integration, affected race tests and vet/build passed.
One single-UTXO run: delivery 31.177 ms, committee receive
31.416 ms, execution 756.359 ms,
application commit 778.387 ms; observed final proof
1188.861 ms. Receive-to-execution is
724.943 ms, rule execution
0.118 ms, application commit
13.960 ms. Dominant interval is before business execution;
individual consensus stages remain unmeasured.
Snapshot shows 28 attempts for the same payment, 21
executed by then; duplicate delivery is a concrete optimization candidate,
not evidence of extra payments. No timing parameters or retries changed.
Evidence: experiments/single-async-001/reports/demo-1789699621265965000.json
and single-transfer-settlement.md.


## Backend reduction and bounded parallel relay (2026-09-18)

Implemented shared-overlay receipt application, single verification inside the
member trust boundary, INSTALL hint-based transport suppression, and preservation
of delivery progress on concurrent/first INSTALL and repeated parent admission.
One completed payment now applies its four credits, custody and outbox retirement
in one logical Store.Update. All durability and financial checks remain enabled.
Four relay tasks may overlap; the next scan does not overlap the same task.

Full race tests, vet, command builds and real four-validator integration passed.
Eight fixed-load runs all completed 128 payments; average whole-run times were
23.393 s baseline, 12.335 s reduced serial, 11.985 s reduced dual, 10.345 s reduced
four-way. The four-way setting increased READY latency in this single-host test;
it is retained for the current backend-drain objective, not claimed to improve
all latency metrics. A 30 s closed-loop workload completed 454 payments and
finished observing credits 3.314 s after generation ended. All nine stopped-state
audits passed and every outbox was empty.

Proof and credit observers are now independent. Their remaining queue/polling
delays are explicit; precise consensus commit time is not inferred from these
observations. Role-specific minimal receipt queries and committed-block proof
reuse remain separate future experiments. Root fulfilment, archive, actual
refills/reward claims and sustained high-TPS validation are still incomplete.
See [implementation report and reproducible data](performance/backend-reduction-2026-09-18/README.md).
