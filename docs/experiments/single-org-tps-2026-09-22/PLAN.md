# Single-organization whole-system throughput

Goal: increase successful end-to-end payments per second with bounded receipt latency and no growing observation backlog. Baseline commit e3870e1. Nine server processes: four committee, four members, one gateway; recipient descriptor uses the same organization. The genesis may include an unused registered second organization, but it has no running servers or signing load.

Development: fresh 45-60 second runs at 1100 and higher offered rates, stage samples 1%. Held-out: fresh longer runs with no diagnostic profiling. Keep all owner/member checks, quorum, atomic input/credit changes and public-block verification. Experimental memory stores have no crash durability. Compare committee application memory separately from baseline bbolt.

Each retained change needs a hypothesis, minimal implementation, regression checks, workload comparison and final audit. Record failures and actual send rate; target rate alone is not throughput. No builds or heavy analyses during measured load.

## Evidence and decisions
- 1100 bbolt vs Ephemeral: no throughput improvement at this rate; retain mode separation.
- Batch exact-fact progress: HTTP work fell to1904/49500 payments, no errors; 2048 cap still limited1500 offered to~1245 actual. Retain only if normal reverse test confirms no regression.
- Pending4096: actual~1382 but queue filled and Final latency rose; this is not sufficient optimization. One disk-full shutdown trial excluded entirely; reports retained.
- CPU diagnostic1500: gateway ReceiveDescriptor.Verify3.49CPU seconds in12.12sec sample; reconsider previously rejected successful descriptor cache now that storage bottleneck is removed. Reuse existing bounded LRU dependency; no transaction authorization or mutable-state cache. Compare fresh no-trace before/after/reverse.

- Descriptor cache A/B/A at1500: no-cache1444.94/1446.83 actual vs cache1489.21; fast median5.77/5.71 vs1.27ms. At2000, no-cache1514.41 vs cached1944.70/1947.47 actual, full audit passes. Applicability is repeated descriptors; cold/churn benchmark retained.
- Batch reverse at2000/500ms: unbatched1897.96 actual, fastP95116.84ms vs batch1944.70/1947.47,89.14/91.28ms. Observer workload reduction, not a new settlement rule.
- Commit500/250/500 at2000: actual1944.70/1971.95/1947.47; walletP501262/750/1267ms. Keep500 default, explicit experimental override for250.
- Cached2500 diagnostic: gateway JSON decode0.95% CPU, no binary transport change. Connection diagnostic:198000 approval calls,26704 fresh GotConn,169989 reused,32298 canceled. Do not equate each cancellation with a dial. Delayed cancellation would need bounded actual task lifetime and deadline handling; do not add it merely to chase a small profile percentage. No such production change made.
