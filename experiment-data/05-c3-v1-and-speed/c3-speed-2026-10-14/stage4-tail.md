# Stage 4 receipt tail diagnostic

Source: stage4-chain-trace, 100 hops, opt-in tracing. Reproduce with `python analyze_tail.py LAB_DIRECTORY stage4-tail.json`.

Receipt median 5.173 ms; maximum 13.880 ms. Gateway handler median 3.278 ms; fanout median 2.836 ms; final QC median 0.109 ms. Preparation mean 0.734 ms (73.45 ms total). This trace is diagnostic, not an unbiased performance comparison.

| Hop | Receipt ms | Gateway handler ms | Fanout ms | Fresh member connections | Third selected member | Third member handler ms | Third member writer queue ms |
|---|---:|---:|---:|---:|---|---:|---:|
|95|13.880|5.951|5.628|1|1|0.606|0.012|
|97|9.281|6.361|4.251|0|2|1.564|0.009|
|94|9.182|5.843|3.624|0|2|0.632|0.006|
|100|8.956|4.731|4.430|0|0|1.588|0.005|
|13|8.355|2.802|2.060|0|2|0.514|0.024|
|96|7.416|5.479|5.165|0|3|0.816|0.009|
|55|7.357|4.676|4.394|0|2|1.928|0.046|
|89|7.222|4.105|3.683|1|3|0.794|0.005|
|46|7.213|4.488|4.039|1|2|2.779|0.021|
|31|7.200|5.660|5.034|1|2|0.665|0.004|

The largest tail is not a member writer bottleneck. Hop 95's three completed HTTP exchanges total 5.19–5.46 ms; their member handlers take 0.58–0.64 ms and writer queues about 0.01 ms. The fresh connection acquisition takes 0.692 ms, but reused connections are similarly slow. Hop 97's third vote uses a reused connection: HTTP total 4.04 ms, member handler 1.56 ms, writer queue 0.009 ms. Hop 13's gateway handler is only 2.80 ms despite an 8.36 ms client receipt: a separate client/transport/scheduling tail exists.

Connection churn is measurable but insufficient as the main explanation: 31 fresh versus 369 reused acquisitions. The 27 hops with any fresh connection have receipt median 5.922 ms; other 73 hops have median 5.038 ms. Six of the ten slowest hops use only reused connections. Association is not causation.

Next high-value target is foreground HTTP scheduling and wakeup delay, especially the write-to-first-byte interval and client receipt remainder, rather than further approval JSON/writer tuning. Instrument semaphore admission before the member handler, HTTP response encoding/write completion, and gateway body-read-to-vote selection. These measurements can distinguish foreground admission delay, response completion and goroutine scheduling before changing behavior. Collector's cancellation of outstanding fourth requests is a concrete connection-churn candidate; a bounded completion/drain experiment should preserve deadlines, stop behavior and the three-vote receipt boundary, and must be tested against background load. It cannot explain all tails by itself.

No timestamps are subtracted across processes. Gateway dispatch/HTTP/selection use gateway local_ns; member handlers use their own local_ns, even though their node label is the same HTTP URL. Committee ExecuteStart/ExecuteDone are compared only within their own records. Settlement records permit per-hop execution duration but lack a common monotonic clock with foreground; they do not establish simultaneous public-execution interference. Five of the ten slowest receipts occur at hops 94–100, which is a temporal cluster, not proof of its cause.

Artifacts: analyze_tail.py and stage4-tail.json retain all 100 hops, per-member connection status, HTTP local spans, member local spans, third-vote selection and per-record committee execution durations. No benchmark, tests or build were run for this analysis.
