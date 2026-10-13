# Long-chain receipt and observer overlap

Read-only reduction of stage7-long-confirm (seven pairs) and stage7-long-diagnostic/chain. analyze_long_chain.py emits no transaction/output/fact identifiers. It compares only the payctl client's chain timestamps and its three wallet followers; no gateway/member/committee clocks are subtracted. UnixNS assumes no client clock discontinuity. Raw inputs remain private.

## Seven-trial median by 100-hop band

Each entry is the median across seven trials of that trial's band statistic. Before uses frozen services; after uses optimized services. Both use CPU 0–3 and GOMAXPROCS=1.

| Hops | Before receipt P50/P95 ms | After receipt P50/P95 ms | Before/after preparation sum ms |
|---|---|---|---|
|1–100|6.47 / 10.62|4.91 / 7.54|49.5 / 51.9|
|101–200|10.17 / 20.48|5.86 / 11.53|58.7 / 59.0|
|201–300|8.68 / 18.36|6.09 / 16.45|51.6 / 57.7|
|301–400|8.64 / 17.72|5.98 / 14.85|53.8 / 66.4|
|401–500|8.76 / 19.85|5.40 / 13.07|58.3 / 62.8|
|501–600|8.55 / 15.44|5.43 / 11.64|53.3 / 61.4|
|601–700|8.53 / 16.07|5.42 / 11.88|51.5 / 62.4|
|701–800|8.74 / 17.11|5.42 / 18.01|58.6 / 67.3|
|801–900|8.64 / 18.55|5.20 / 12.23|57.4 / 60.0|
|901–1000|8.70 / 16.57|5.26 / 13.27|54.3 / 60.8|

The speed improvement persists after several public blocks. The after 701–800 band retains worse P95 than before despite lower P50. Preparation did not supply the measured service improvement.

## Observer peak and block overlap

Actual concurrent observers (received receipt but not yet all-member-closed) peak at median 172 before, 261 after. Before range 163–177; after 221–350. Faster foreground completion accumulates more live observers. These counts are derived from receipt-ready and all-member-close events, not the sum of maxima of separate backlog classes. The JSON separately retains previous receipts pending public observation and previous public observations pending member closure at each selected slow hop, with band maxima.

First public observation occurs at 1.64–1.69 seconds before and 1.79–1.88 seconds after, not approximately three seconds in these runs. After trials r0/r3 reach their larger observer peaks around 2.75/2.71 seconds, after the first public observation. First wallet-block commit phases last roughly 8–62 ms after, not three seconds. A ~three-second finality value for a particular later receipt must not be interpreted as a three-second client block application.

69 of the 70 top-ten receipt intervals in each arm overlap some wallet follower fetch/verify/commit interval. However only 8/70 after and 9/70 before overlap height 1. The worst tails recur across later block boundaries; they are not solely a first-public-block effect. The diagnostic's largest tail is hop 315 at 80.38 ms and overlaps later follower phases; its second largest is hop 244 at 67.89 ms. Hop 316 remains slow (56.07 ms) without follower overlap.

In the diagnostic, 92/1000 receipts overlap follower work: P50 12.66 ms / P95 50.68 ms, versus 908 nonoverlapping receipts P50 5.47 ms / P95 10.04 ms. Fetch overlap covers 89 receipts. Verify overlap covers 16 (P50 44.23 ms), commit overlap 19 (P50 43.38 ms). Categories overlap and cannot be added. This is descriptive correlation and is length-biased: a long receipt interval is more likely to intersect a background interval. It does not isolate follower work as the cause.

These observations support investigating foreground/background CPU and scheduler contention around recurring block fetch/verify/apply boundaries. They do not justify weakening block validation, pausing settlement, changing the receipt boundary, or blaming observer count alone. Client CPU 2.30 seconds with blockfollow 0.46 seconds over 8.74 seconds and gateway CPU 4.04/refill 1.12 over six seconds remain separate process profiles; their durations cannot be combined into a cross-process critical path.

Reproduce: `python analyze_long_chain.py ROOT OUTPUT.json`. stage7-long-bands.json and stage7-long-diagnostic-bands.json include receipt/build sums and P50/P95, top five per band, retries, FinalChecks, backlog counts and follower overlap phases. No tests, builds, benchmarks or uploads were run.
