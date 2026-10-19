# Stage 8 background completion check

Source: stage8-long-confirm7triples, seven alternating pairs, 1000 dependent receipts each, CPU 0–3, GOMAXPROCS=1. The before arm is the already optimized stage6 service set, not the frozen original C3. Manifest hashes show identical payctl/member/committee binaries across arms; only gateway differs. No new profiling or database-key audit was performed.

| Metric, median of seven runs | Before | After |
|---|---:|---:|
|Fast chain, ms|7296.58|6641.83|
|Last-hop public observation, ms|8255.42|7518.24|
|All-hop public observation maximum, ms|8255.71|7529.27|
|All-member closure maximum, ms|8283.45|7576.55|
|Public drain after fast chain, ms|841.77|893.65|
|Closure drain after fast chain, ms|865.27|920.97|
|Concurrent observer peak|267|306|
|Total FinalChecks|3104|3092|

Each paired trial improves fast completion, public completion and all-member closure. Median paired reductions: fast 8.74%, public 7.97%, closure 7.82%. Pairwise closure reductions range 1.21–9.77%. Therefore the foreground improvement is not explained solely by deferring completion beyond the measurement endpoint: all-member closure also finishes earlier in every pair.

There is a real backlog tradeoff. After's median observer peak rises by 39 and its median closure drain is about 56 ms longer. Closure drain grows in five pairs (80–346 ms), falls in two (5–284 ms). Faster foreground throughput accumulates more concurrent observers; the longer tail relative to fast completion must remain reported. It does not negate the earlier absolute closure endpoint.

All 14 C3 reports contain exactly 1000 hops with receipt-ready, wallet public-final and member-closed timestamps. Every run records exactly 1000 HTTP attempts, zero retried hops, zero progress errors, native exit zero, closure_status complete, no closure_errors and no chain Error. This verifies retained report completion; it is not an independent proof of economic conservation or an inspection of store keys. All seven FastPay reports record completed=1000, probe_completed=true and principal_conserved=true.

PublicChainMS refers to the last hop's public observation. The analyzer also computes max FinalOffsetMS over all hops, preventing an earlier hop observed later from disappearing from the background comparison. ClosedChainMS is already the maximum over all hops.

Only the existing stage7-long-diagnostic profiles were found; there is no matching stage8 profile capture here. A before/after profile attribution would therefore be unsupported. Stage7's client blockfollow and gateway refill costs remain hypotheses about background contention, not a measured stage8 explanation.

Artifacts: check_chain_background.py and stage8-background-check.json retain each pair's absolute endpoints, drain differences, observer peaks and closure counters without transaction identifiers. No tests, builds, benchmarks, uploads or architecture changes occurred during this reduction.
