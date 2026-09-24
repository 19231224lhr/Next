# Pinned CometBFT adaptation

Run `python3 third_party/cometbft/overlay.py` with Go on PATH before the first
build. It materializes CometBFT v0.38.26 into `.scratch/comet-src`, checks every
upstream patch anchor and sets the root module's local replace. It does not edit
the module cache. Source additions are kept here; generated upstream code is
ignored. Build the payment application with `-tags=comet_v3`.

The fork changes transaction commitments, block-part commitments/wire encoding,
authenticated historical BlockStore revision, original-version replay and
catch-up, and the node's pre-replay application hook. It preserves normal BFT
rounds and quorum rules. It is not wire-compatible with an unmodified network.

Only an application-committed exact RepairInput can authorize ReviseBlock.
Original bytes are retained for replay; current parts are genuinely rewritten.
Monetary changes execute at the new repair height, never retrospectively.

The cryptographic package is supplied by this application's root module. This
directory is a patch source bundle, not a standalone Comet distribution.
See [implementation and evidence](../../docs/archive/development/implementation-v1.2.md).

For local profiling, set both `UTXO_SETTLEMENT_TRACE=1` and
`UTXO_COMET_PROFILE=1` before starting committee processes. The optional
`libs/operationtrace` hook records proposal construction, WAL synchronization,
validator signing-state saves, and BlockStore saves in the existing bounded
in-memory timeline. It adds no diagnostic disk writes and changes no durability
or consensus rule. Signature timing includes the nested signing-state save;
do not add the two measurements together. The hook stays nil in normal runs.

The hook also records actual flow-rate limiter waits (only when its wait branch
executes), FinalizeBlock response persistence, consensus-state persistence, and
mempool lock/flush waits. Concurrent or nested operation durations must not be
summed as critical-path latency. Committee JSON accepts optional `P2PSendRate`
and `P2PRecvRate` in bytes/second: zero keeps Comet's default; negative values are
rejected before either override is applied. Earlier low-load trials retained
5,120,000 bytes/second and 10/10 ms flush/gossip after
[controlled trials](../../docs/experiments/consensus-propagation-2026-09-20/README.md)
found no stable total-completion benefit from smaller intervals or higher limits.
The later sustained-load trials found real bandwidth limiting at higher rates;
new loopback v4 laboratories use 51,200,000 bytes/second and 10/10 ms. Other
deployments retain the configured rate or the upstream default when unset.
See [TPS trials](../../docs/experiments/consensus-tps-2026-09-21/README.md).

OriginalBlockPart holds the revision read lock through its decision and read.
For blocks with no historical revision it reads the requested stored part
directly, instead of decoding and rebuilding every part of the entire block.
Revised blocks still reconstruct immutable original execution bytes for replay
and catch-up; no current rewritten part is substituted into that path.

FinalizeBlock history and its latest recovery record use one database batch and
`WriteSync` per height, before App.Commit. Keys and encoded values are unchanged;
discard mode still writes only the recovery record. This removes a separate
database write operation, not a second fsync (the original path had only one
explicit sync). A write error is returned without rollback or retry: storage may
already contain the batch, so existing halt/handshake recovery rules still apply.
See [storage trials](../../docs/experiments/committee-storage-2026-09-20/README.md).

Optional profiling also splits response encoding, database writes, FilePV
encoding, and atomic-file open / O_SYNC write / close / rename. The synchronous
write measurement combines kernel write and sync costs. No signing-state
durability or atomic-replacement rule is changed.

AutoFile avoids repeating Sync on the same open handle after a successful Sync
with no intervening Write. Writes (including errors) and reopening invalidate
that state under the existing mutex. Group still flushes first, so an empty
buffer is never mistaken for durable data. WAL ordering/format and all syncs
covering new writes are preserved. This assumes the WAL has its normal exclusive
process writer; external modification of an active WAL is unsupported.
See [measurement and validation](../../docs/experiments/wal-sync-2026-09-20/README.md).

The local consensus queue batches already-ready proposal/part records only in
the current Propose step, bounded by 16 messages and 512 KiB of part bodies.
Actual missing-part count limits each batch so block completion can happen only
at its tail, preserving intervening state-record order on replay. Records are
synced before handling; partial durable prefixes remain replayable after errors.
Votes and all other messages keep the single-record durable path. There is no
batch-fill timer. See [tests and paired results](../../docs/experiments/proposal-batch-2026-09-20/README.md),
including the foreground latency tradeoff in this single-host workload.
