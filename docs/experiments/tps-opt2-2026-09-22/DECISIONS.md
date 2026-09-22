# Evidence log
- Start: 2026-09-22 04:14 UTC. Baseline source 3d091b0.
- H1 gateway memory: retained after isolated 48k@800 contrast and 144k@800 validation. Removes runtime gateway writes only in explicit fresh-start mode. No claim that cheap DB reads caused latency.
- Submit cursor: kept as a four-line scan-continuation fix; deterministic red/green test. No established TPS gain by itself.
- JSON/INSTALL scan hypothesis: rejected after 10s CPU profile; no performance patch implemented. The temporary proposed test was removed before implementation.
- Submit slots4->8: retained candidate after90k@1200 contrast (1092.14 vs1011.33actual send TPS,83.65 vs90.37s closure). INSTALL remains4; done buffer derives from both quotas. Test covers12in-flight cancellation.
- Committee NoSync/memory: considered but not implemented; no changes to committee application durability or Comet.
- confirm1000: explicit no-trace validation, actual982.08TPS; no claim of exact1000 achieved based only on this run. Three-minute target1100 validation added to test actual throughput above1000.
- Failed slots4 harness build: f-string brace error before any payment, fixed and rerun as slots4b. All successful run reports/audits retained.
- Gateway close errors propagate; existing memory-store audit snapshots cannot be used as resumable databases.

- final1100b: 197995/198000 completed in189.288s; 5 gateway quorum failures caused by member429 LIMITED. This is not an all-success capacity result. Foreground in-flight at the five failures was126,127,128,127,128; oldest outstanding requests113-121ms. LIMITED can originate in HTTP admission or budget, so experiment-only reason logging added before changing production limits.

- limit1100b: 198k@1100,197997 completed,3 quorum failures; exact foreground128 admission rejects2633 and slice rejects0. Distinguishes location of rejection, not all causes of occupied slots.
- Member foreground128->256: only bounded admission capacity changed, background32 unchanged. fg256 same198k@1100 completed198000 in188.315s (1051.43 closure TPS), fastP503.772/P9544.557ms vs128 3.773/44.493ms. Foreground rejects2633->40,0 quorum failures; all audit checks passed. No claim all internal429 eliminated or faster per-request execution.
- Final fg256b: identical production, no stage traces/no per-refusal logs; a lightweight foreground rejection counter read only after load. This is independent confirmation, not a pure trace-overhead estimate.

- Final independent no-trace confirmation fg256b:198000/198000 complete in187.3357s,1056.93closureTPS,1063.94actual sendTPS; fast3.752/P9545.010ms; 4committee hashes equal,outboxes0,audit0.52single-member entry refusals,0quorum failures. Six complete30s windows all observe1047-1083successfulTPS; sent unfinished fluctuates1165-1800 at window boundaries,final0. Freeze and publish; no more parameter changes.
