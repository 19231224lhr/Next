# Stage 9b self-approval lookup fact

c3EventWithApproval now returns the actual lookup fact it already computed. applyPayment compares that fact with Authorization.Fact instead of re-encoding the summary. Divergent facts still take the original second lookup; the guard remains. A focused unit test verifies the metadata comes from the lookup key even when the retained Approval.Fact field differs.

Focused tests passed (1.254s), full internal/member/internal/rules passed (2.362s/2.452s), scoped race passed (2.767s). Pre-9b source bytes and SHA256 hashes are retained in stage9-source-snapshot for a future direct 9-versus-9b comparison.

Five alternating sequential microbenchmark pairs succeeded, Windows amd64 / AMD Ryzen AI 9 HX 370 / GOMAXPROCS=1 / benchtime=1s. The before source overlay replaces only blocks.go/c3.go with HEAD bytes, while after uses 9b. Both compile the identical common benchmark test source. Only the new signature-specific, non-running metadata unit test is excluded from both benchmark compilations to keep HEAD source compilable; the full test/race commands above executed it normally.

| Median | Before HEAD | After 9b |
|---|---:|---:|
|ns/op|203549|144363|
|B/op|141327|122005|
|allocs/op|501|478|

Actual applyPayment evaluation CPU falls 29.1%, allocated bytes 13.7%, allocation count 4.6%. Setup and committed writes are outside the timer; every iteration discards a fresh overlay on an unchanged approved C3 snapshot. All ten logs, status records, source overrides, exact commands and hashes are saved in stage9b-approval-micro. This is a microbenchmark finding and does not establish an end-to-end gain.

The prior stage9 series recorded 512 allocations/op versus 478 here. That supports removal of the extra guard encoding allocation cost; timing comparisons across the two separate series are not a paired 9-versus-9b experiment.

Reproduce: `python bench_approval_reuse.py --out NEW_DIRECTORY --repeats 5 --gomaxprocs 1 --benchtime 1s`. Add `--before-source-dir stage9-source-snapshot` for a future direct comparison to stage9; do not infer such a result from the HEAD comparison.
