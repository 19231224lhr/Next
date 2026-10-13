# Stage 9 actual applyPayment microbenchmark

Five alternating sequential pairs on Windows amd64, AMD Ryzen AI 9 HX 370, GOMAXPROCS=1, benchtime=1s. All ten Go benchmark commands succeeded. Fixture creation, initial approval/reservation, and read-view acquisition occur outside the timer. Each measured iteration runs actual applyPayment against a new overlay on the same unchanged in-memory approved C3 snapshot, discarding writes.

| Median | Before | After |
|---|---:|---:|
|ns/op|201477|146463|
|B/op|141375|130190|
|allocs/op|501|512|

CPU time decreases 27.3%; allocated bytes decrease 7.9%; allocation count increases by 11. Before timings range 199960–203926 ns/op, after 144507–147160. These are micro CPU/allocation findings, not a receipt or chain speed claim. No persistence, network or competing services are included. End-to-end stage9 pilots remain mixed.

The before arm uses Go -overlay to replace only internal/member/blocks.go and internal/member/c3.go with exact HEAD bytes extracted by git show. All other current optimizations and the benchmark are identical. No checkout/reset was performed. Manifest retains HEAD, both source hashes, benchmark/script hashes, exact commands, platform and GOMAXPROCS; each raw log records CPU identity. All source overrides and all ten logs/status records are retained under stage9-approval-micro.

Possible stage9b proposal only: c3EventWithApproval already computes the actual approval lookup fact. Return that value as an additional result and compare it directly with pay.Authorization.Fact, retaining the divergent-key fallback. This avoids recomputing summary.Fact for the guard and may remove some new allocations. The fact guard must remain. No stage9b code changes were made during this benchmark.

Reproduce with bench_approval_reuse.py --out NEW_DIRECTORY --repeats 5 --gomaxprocs 1 --benchtime 1s. Run when formal service measurements are idle; use the same CPU affinity for both arms when adding affinity control.
