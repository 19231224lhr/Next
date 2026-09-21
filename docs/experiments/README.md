# Selected laboratory observations

- [Full fast-payment path at 500 and 600 TPS](full-path-opt-2026-09-22/FINAL.md): fresh all-source 90,000-payment runs at499.93/599.15 TPS, zero payment failures, equal committee states and drained outboxes; explicit member memory and wallet NoSync. 800 target not sustained. Reproduction tool and phase distributions included.

- [Admission to proposal: real mempool availability and broadcast](admission-wait-2026-09-21/README.md): 2,004 new audited payments plus offline analysis of the prior comparison. Both 1,000-payment traces select every transaction at its first actually available proposal opportunity; most delay is waiting for the previous height. A targeted broadcast trace locates 66.2 ms mean queue-to-receive delay in 124 payments that missed the preceding window. No production performance patch; three GPT review rounds and a narrowed follow-up plan.

- [Single payment versus 100 TPS for ten seconds](single-vs-100tps-2026-09-21/README.md): same journal-buffer candidate and full phase tracing; 1,003 audited payments including warmups. Fast receipt grows from 51.85 ms to mean 107.57 ms, mainly member persistence and shared benchmark-wallet saving. Additional backend latency concentrates in proposal waiting, consensus/signing-state persistence and subsequent-height header availability. All 1,000 load payments drain 0.912 s after the final send; no protocol changes or long-term throughput claim.

- [Bounded journal write coalescing candidate](journal-buffer-2026-09-21/README.md): 24,006 audited payments; fixed-rate 200/s ABBA lowers mean-of-run block-observation P50 by 23.3% and unfinished peaks by 28.8%, while total closure and final drain do not improve. A diagnostic pair reduces cumulative BlockStore write time with existing Sync boundaries preserved. Private candidate only; default binaries unchanged.

- [Actual Write/WriteAt calls and asynchronous persistence boundaries](write-calls-2026-09-21/README.md): 8,002 audited payments. The corrected trace attributes over 99% of the measured write-loop intervals to underlying write calls; bbolt emits 16 KiB pages and LevelDB mostly 32 KiB sequential journal writes. A small equal-data/equal-Sync ABBA file probe reduces calls twelvefold but improves total time only 4.3%, with more Sync waiting. No production optimization or durability relaxation.

- [Committee persistence and inter-block progression](committee-persistence-2026-09-21/README.md): 12,003 audited payments, three diagnostic runs. BlockStore synchronous batches grow from 25.0 to 90.2 ms per block in the paired trace; a further 200/s run attributes 59.7 of 76.8 ms to journal writing and 16.8 ms to Sync. bbolt spikes fall primarily in data-page write loops, not uniformly in Sync. Production behavior unchanged; lower-level I/O versus runtime causality remains open.

- [20 vs 200 TPS stage comparison](load-stage-2026-09-20/README.md): 16,013 audited payments including controls and warmups. In the diagnostic pair, admission-to-proposal grows by 110.88 ms and proposal-to-execution by 88.91 ms on average. Most extra proposal waiting precedes the previous height's app commit; a slow block records 640.47 ms in bbolt write/sync. Plain reverse-order controls retain the high-load tail/backlog trend, with variable magnitudes. No new production optimization.

- [Gateway handler persistence repair](gateway-handler-2026-09-20/README.md): bounded background saves remove the HTTP/1 connection dependency. At 200/s, four fresh 12,000-payment ABBA rounds show mean-of-run P50 154.72 → 113.88 ms; P95 279.82 → 249.20 ms. Background pending peaks and observation queries did not improve.
- [Foreground latency at 200/s](foreground-load-2026-09-20/README.md): preceding investigation, 52,026 audited payments including warmups; query-frequency ABBA gives no consistent tail improvement. Same-connection traces confirm response-afterwork blocks subsequent HTTP/1 requests; physical batch timing locates member and wallet waits in write/sync. Diagnostic overlays only in that investigation; the subsequent gateway fix is linked above.

- [Decoupled benchmark admission results](dispatch-lag-2026-09-20/RESULTS.md): 256 fast-receipt permits and 2048 total tasks; 24,000-payment candidate sends at 200/s with dispatch P95 0.575 ms. Background observation peaks at 1401 and fast P95 rises; the final coupled control retains 11 gateway 429 rejections. This corrects benchmark admission, not service-side throughput.

- [Paced sender lag investigation](dispatch-lag-2026-09-20/README.md): offline reconstruction of 54,000 existing samples. At 200/s, all 256 complete-lifecycle benchmark slots were occupied for 87.92% of the send window; capacity-conditioned residual dispatch wait P95 was 0.32 ms. No protocol changes or new load run.

- [Committee result batching and sustained load](committee-storage-2026-09-20/README.md): 70,207 audited payments; one synchronous batch replaces two independent result writes. Four 4,000-payment trials show a small 1.6% mean closure reduction. Two-minute 100/150/200 sends-per-second trials all drain; the 200/s trial shows growing sender lag, so no long-term 200 TPS claim. Tested version activated on the preserved laboratory data.

- [Consensus waiting, persistence and bandwidth trials](consensus-propagation-2026-09-20/README.md): 17,775 audited payments; proposer-local waiting and synchronous persistence traced. Shorter propagation intervals and a higher P2P bandwidth cap did not establish an end-to-end gain, so defaults remain unchanged. Adds optional bandwidth configuration and narrow diagnostic hooks.

- [Member GC and scheduling parameter trials](member-runtime-tuning-2026-09-20/README.md): 12,602 audited payments; lower member parallelism rejected, GOGC=200 adopted for the Mac lab. Repeated 100-payment comparisons and separate three-minute 30/s runs show lower foreground latency with a measured memory cost; no throughput-ceiling claim.

- [Fresh genesis, 100 payments and gateway dispatch](gateway-dispatch-2026-09-20/README.md): retired the active height-20840 runtime, 100/100 successful in 1.951 s at height 7; HTTP and selected-quorum timestamps, plus a separate 100-payment native runtime trace locating member-side GC and scheduling waits. Diagnostic changes only.

- [Foreground latency investigation](foreground-diagnosis-2026-09-20/README.md): unchanged binaries, 1,400 new audited payments; first-burst attribution and controlled send-rate comparisons. No production optimization added; a stable foreground penalty from WAL batching is not established.

- [Batch already-ready local proposals and parts](proposal-batch-2026-09-20/README.md): bounded WAL batching, 1,408 audited payments including a paused-committee check; six plain pairs show 4.8% shorter closure, with a measured foreground-latency regression retained in the report.

- [Skip redundant WAL synchronization](wal-sync-2026-09-20/README.md): minimal AutoFile change, 800 audited payments, six alternating plain runs and two operation profiles; required durability preserved.

- [Consensus and block-following review](consensus-review-2026-09-20/README.md): two GPT review rounds, current-binary operation profiling, actual remaining timers, header availability and narrow WAL optimization candidates.
- [Current version: 100-payment phase retest](stage-profile-100-2026-09-20/README.md): identical traced binary, full phase timings, per-block consensus breakdown, send window and drain comparison.
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
