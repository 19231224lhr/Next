# Single-host baseline final review — 2026-10-13

Disposition after re-review: **approved for the scoped single-host implementation comparison**. All four findings below are resolved in the actual adapter/coordinator sources. No material issue remains in the changed areas reviewed. This approves the implementation and measurement definitions, not any numerical conclusion before the matched run artifacts are checked.

## Re-review of fixes

- The FastPay adapter no longer performs its own cryptographic `cert.check`. It retains sender/recipient/amount matching and delegates certificate verification to the unchanged official `receive_from_fastpay` path.
- FastPay now records each actual first-RPC offset, receipt offset, dispatch lag, realized send rate, and finite-window receipt goodput. The coordinator retains and aggregates realized rates and goodput. The serial sender still limits offered load; interpret results using achieved rates, not target rates alone. Next's aggregate rate/goodput derives from native Unix timestamps, while individual latency samples use native monotonic duration; FastPay uses monotonic offsets throughout.
- Independent coordinator commands now pass `--length 1` and the requested independent `--count`, eliminating the unrelated 100-hop validation limit.
- FastPay chain total is now exactly the last captured receiver endpoint minus the first captured RPC start; the excluded spendability probe remains outside this interval.
- The coordinator has a regression test that launches a real child process, triggers a timeout, and checks that the child's SIGTERM handler wrote its cleanup marker. This directly exercises cooperative termination. The implementation owner reports all 11 Python checks plus 45 upstream core, one adapter, and two network tests passing. I inspected the test and code; I did not rerun tests during the formal benchmarks.

The remainder records the earlier findings and interpretation limits for auditability. Pre-fix FastPay samples must remain separate from the corrected formal runs.

## Earlier findings (resolved)

1. **P2 — Redundant FastPay certificate verification is timed.** `fastpay_client.rs:87` calls `cert.check(&Committee::new(c.voting_rights()))` after the safe sender call and before `receive_from_fastpay`. Official `fastpay_core/src/client.rs:642` already calls `certificate.check(&self.committee)` at the start of `receive_from_fastpay`. Thus the adapter adds committee construction and a second receiver-side cryptographic verification to every reported latency. Remove the extra adapter check while retaining the identity assertions and official receiver call, or explicitly treat the retained measurements as including this extra audit. Do not silently reuse these FastPay samples after changing the timer workload; the cost has not been quantified.

2. **P2 — Offered-rate sweeps lack FastPay realized dispatch evidence.** The FastPay loop waits until each requested schedule time, then completes the entire transfer/receive before starting the next iteration. When service time exceeds the target interval, subsequent waits are already overdue and the realized rate falls below the target. Its JSON currently contains neither send timestamps nor dispatch lag nor realized send rate, while `compare.py` permits `--rates` and groups results by target rate. Add native per-send timing and realized rate before drawing matched offered-load or goodput conclusions. This does not invalidate the default rate-zero closed-loop run.

3. **P2 — Independent counts above 100 are rejected through an irrelevant chain argument.** `compare.py` passes `--length n` and `--count n` for both workloads. `run_next.py` validates `length <= 100` even for independent payments, although count is documented up to 30000. The default count 100 works. Pass a valid unused chain length for independent runs or make the adapter's length validation workload-specific.

4. **P3 — FastPay chain total stops after final bookkeeping.** `fastpay_client.rs:99` reads elapsed time after appending samples and validating final balances. Next uses the final receiving-wallet acceptance timestamp directly. Keep the final `endpoint` instant and compute final endpoint minus first RPC for an exact receipt-to-receipt span. This is a small instrumentation boundary discrepancy, not evidence of a material performance difference.

## Resolved during review

The original coordinator used `subprocess.run(..., timeout=600)`, which kills only the adapter on timeout and bypasses its cleanup handlers. Next servers and clients have independent sessions, so this could leave services running. The updated `run_managed` starts the adapter in a new session, sends SIGTERM on timeout/interruption, and gives its cleanup path 45 seconds before escalation; the entry point also handles SIGTERM. This addresses the normal cooperative cleanup path by inspection. A stuck Next adapter at forced escalation can still have independently sessioned descendants; successful-run isolation should be checked when collecting the final evidence.

## Confirmed properties and interpretation limits

- Both implementations use real services and native monotonic clocks; process startup is outside payment samples. The Next source difference is the benchmark payer-policy grant filter. Official FastPay protocol source has no tracked diff.
- FastPay uses safe `transfer_to_fastpay` and official `receive_from_fastpay`, including its receiver verification and synchronization. It validates sender/recipient/amount, conserves principal, and executes an excluded final recipient spend. No unsafe-unconfirmed path is used.
- The chain initializes one 100-unit principal, rotates among three owners, and checks received-value dependency. Next validates exact preceding output identity, certificate-input use, asset, amount, and receipt timestamps. Receiver genesis payment principal is removed before startup.
- The normal Next timer stops after `ReceiveDirect`, not after public settlement. Its later closure outcomes are retained separately. The chain total includes later construction/signing; per-hop `FastMS` excludes it. FastPay construction before first RPC is separately recorded.
- Default independent runs are serial at the receipt endpoint. They measure closed-loop latency, not saturation throughput. Next background observation and settlement continue concurrently; this is part of its deployed client/system cost and should remain disclosed.
- Four shared logical CPUs constrain both deployments. FastPay has four authority processes; Next has nine servers plus a supervisor and more background work. Native TCP versus HTTP and in-memory wallet versus no-sync Bolt remain implementation differences. This is a same-host implementation comparison, not a transport-controlled protocol-only comparison or a resource-equivalent deployment.
- Grouping by system/workload/count/target rate and taking medians of per-run percentiles is internally consistent. State explicitly that the result is the median of run p50/p95 values. For length 1, each run's p50 and p95 are the same single observation; five such runs do not establish a tail-latency distribution.
- Process inventory and CPU affinity are recorded; this code alone does not measure per-system CPU consumption or peak RSS. Avoid claiming resource efficiency from process count alone.
- Keep the unexplained UDP smoke separate. Do not infer TCP/UDP superiority or general network performance from that WSL anomaly.

Scope: reviewed the plan, scoped diff, prior Next report/review, all four adapters/coordinator files, the payer grant-filter diff, and relevant official/native client paths. No benchmarks, builds, or heavy tests were run in this review. Existing test-pass claims were supplied by the implementation owner, not independently rerun here.

## Additional client-path fairness judgment

The normal-client approval above concerns `transfer_to_fastpay` followed by `receive_from_fastpay`. A separately labeled **funding-checked, receiver-confirmed client** is also legitimate for this serialized funded workload: call `get_spendable_amount`, enforce `amount <= spendable`, then call `transfer_to_fastpay_unsafe_unconfirmed` and immediately complete official `receive_from_fastpay` before the next payment.

This judgment follows the exact upstream implementation. Normal `transfer` performs that same funding guard, constructs/signs the order, and invokes `execute_transfer(..., true)`. The unconfirmed method constructs/signs the same order and invokes `execute_transfer(..., false)`. Both obtain the normal authority-vote certificate and update sender state. Official receiver processing still verifies the certificate and recipient, synchronizes the sender sequence with an authority quorum, sends any missing confirmation certificate, and only then credits the recipient. Restoring the funding guard and retaining complete receiver processing removes the omitted safeguards relevant to this workload without modifying the protocol.

Keep the existing normal-client results as their own mode. This tuned mode better addresses a request for a fast legitimate competitor path, but its performance must be measured; this inspection does not establish global optimality. Reset the shared timer before the funding query so recovery RPCs, if any, are included. Preserve the serialized ownership discipline, unfunded intermediate recipients, exact conservation checks, and excluded final spendability probe.

Describe the optimization as removing a redundant synchronization phase. The normal receiver does not necessarily write the certificate a second time: `communicate_transfers` queries authority sequence numbers and sends only missing certificates. The benefit is therefore not a guaranteed fixed number of certificate writes or a predicted speedup.

## Implemented tuned mode and startup fix: source re-review

**Approved by inspection for the same scoped comparison.** The implemented `transfer` helper restores `get_spendable_amount` and `amount <= spendable` before invoking the unconfirmed sender API. The shared clock resets before this helper, so a recovery RPC cannot escape the measured interval. The caller always completes official `receive_from_fastpay` before the next iteration. The final spendability probe uses the same selected mode, and identity, balance, and conservation checks remain present. Mode is recorded in native JSON, coordinator environment, and retained command arguments; keep separate output roots for each mode.

The TCP startup path now requires an actual successful connection to all four authority endpoints before launching the measurement client, and checks child liveness during that wait. For these generated one-shard loopback configurations this is the correct readiness boundary. It is outside all native payment timers. The focused test inspects a real bound-but-not-listening socket, then enables listening and verifies readiness. This directly covers the reported race in which the upstream log appeared before socket binding; the review did not rerun it or reproduce the benchmark failure.

For an ordinary nonzero native client exit, the adapter now writes captured stdout and stderr before raising. This preserves the concrete connection-refused diagnostic. Timeout exceptions still occur before those writes, so this change should not be described as guaranteeing logs for every failure type. The existing successful default-mode run, partial/failed tuned runs, and fresh readiness-validated tuned runs should remain separate; report the startup defect and correction without mixing their samples.

The implementation owner reports real 10/100-hop tuned smokes and the readiness test passing. Those are supplied execution results, not tests rerun by this reviewer. The fresh five-round matched run and subsequent paced sweep still require artifact/count/achieved-rate verification before numerical conclusions. No new measurement blocker was found in these changed areas.

## Completed primary artifact audit

**Approved: the retained `validated-results` artifacts support the scoped descriptive conclusions below.** This audit independently parsed `environment.json`, `summary.json`, `records.json`, every per-run command/result/stdout, and all 40 retained native reports. It used only small local JSON arithmetic; no service, benchmark, build, or heavy test was started. The unfinished paced-load files were not reviewed.

The directory contains exactly 40 unique expected run identities: five rounds, each containing chain lengths 1, 10, and 100 plus 100 independent payments, for both systems. Record order matches the alternating system order in the coordinator. Counts sum to **2,110 measured receipts: 1,055 per system**. All result JSON files agree with their captured stdout, and each measured sample is positive and finite. The excluded FastPay continuation probes are additional transfers and are not included in this receipt count.

The recorded environment is WSL2 Linux, affinity CPUs 0–3, GOMAXPROCS 4, Next experimental memory storage, FastPay TCP, and `receiver-confirmed` client mode. Every FastPay command and native report selects that mode. All 20 FastPay reports record successful continuation probes and principal conservation, four authorities, and amount 100. Native sample arrays, construction times, send/receipt offsets, exact chain endpoints, achieved send rates, and finite-window goodput are arithmetically consistent.

All 20 Next reports contain complete receipts, complete closure, no closure errors, and native exit code zero. The retained raw chain reports have the requested number of hops, rotating sender/receiver identities, 100 CAL per output, nonzero input/output identities, sequential receipt/send timestamps, and exact preceding-output certificate inputs after the first hop. Their reported chain totals equal the last receipt offset. Raw independent reports contain 100 distinct receipt identities each, all `COMPLETE`, and count 100 for fast receipts, wallet block observation, and member completion, with zero failed/not-sent/unknown payments. Thus the current primary results do not hide any recorded later closure failures. These are checks of the retained reports against the already reviewed native verification paths, not a new cryptographic replay of every certificate.

Every per-run p50 was independently recomputed as the sample median and p95 as nearest-rank order statistic `ceil(0.95*n)`. Every aggregate field was then recomputed as the median across five run-level values. The summary, records, and environment copies agree. These are **medians of per-run percentiles**, not percentiles of a pooled cross-run sample.

| Workload | Next median of run p50 (ms) | FastPay median of run p50 (ms) | Next median of run p95 (ms) | FastPay median of run p95 (ms) | Median chain total, Next / FastPay (ms) |
| --- | ---: | ---: | ---: | ---: | ---: |
| Fresh chain, 1 payment | 10.210445 | 7.007319 | 10.210445 | 7.007319 | 10.210445 / 7.007319 |
| Chain, 10 payments | 4.753186 | 6.351643 | 10.712551 | 7.107526 | 63.066309 / 63.687665 |
| Chain, 100 payments | 5.123919 | 6.426653 | 9.051111 | 6.778368 | 648.051345 / 646.934670 |
| Independent, 100 payments | 3.950577 | 6.333439 | 5.651626 | 6.798387 | Not a dependency-chain measurement |

Supported wording: in this same-host implementation experiment, Next has a lower median receipt latency for serial independent payments, while FastPay has a lower latency for a fresh single-payment chain. For 100-hop chains, Next has lower per-hop p50 but higher per-hop p95, and the total-chain medians are numerically close. Those totals measure first native send/RPC through final receiver acceptance, including construction/signing of later hops and other between-hop client work; multiplying a median hop latency by 100 does not recover the chain total.

The 100-hop total ranges across the five runs are Next **589.616586–675.347171 ms** and FastPay **626.462206–697.471001 ms**. The approximately 1.12 ms difference between their total medians does not establish either statistical equivalence or a reliable ranking. The single-payment row supplies only five latency observations, so its identical p50/p95 columns do not establish tail behavior. The independent arm is closed-loop and achieves different send rates; it does not establish equal-offered-load capacity or saturation throughput. No broad protocol superiority, production performance, WAN behavior, or resource-efficiency conclusion follows from these artifacts.

## Final paced-artifact and report audit

**Paced artifacts approved; final report statistics verified.** Independently parsed all 18 `load-results` command/result/stdout and native report sets, their environment, records, and summary. There are exactly three rounds at each target 10, 25, and 50 payments/s for both systems, with 30 receipts per run: **540 additional receipts, 270 per system**, bringing the audited primary-plus-paced total to **2,650 receipts**. All nine paced Next runs report complete receipts and complete closure with exit code zero, all 30 raw outcomes complete, and no failed/not-sent/unknown payments. All nine FastPay runs use `receiver-confirmed`, record successful final probes and principal conservation, and agree with their retained native JSON.

Each achieved send rate was recomputed from native send times, each finite-window receipt goodput from first send through final receipt, each run p50/p95 from the 30 native latency samples, and each summary field from the median of its three runs. Every value agrees with the saved summary. The maximum individual run's absolute relative send-rate deviation is **0.343307672643%**, supporting the report's rounded **0.34%**. The paced table's rates and latency figures all round correctly:

| Target payments/s | Next actual / FastPay actual | Next p50 / p95 (ms) | FastPay p50 / p95 (ms) |
| ---: | ---: | ---: | ---: |
| 10 | 10.006782 / 9.994912 | 3.413341 / 5.760124 | 7.991245 / 8.983275 |
| 25 | 25.039678 / 24.998313 | 3.016502 / 5.070026 | 8.164368 / 8.741275 |
| 50 | 50.158985 / 49.967006 | 3.259980 / 4.747534 | 7.542618 / 8.272397 |

The primary independent-payment p50 reduction is **37.623509%**, which correctly rounds to 37.6%. The quoted fresh-payment and 100-chain medians are also correct. Recomputed `chain-construction.jsonl` sums confirm **97.880645 ms** for Next and **5.028340 ms** for FastPay as medians of the sums of **all 100 builds**. A report precision correction was requested: these sums include the first build, which precedes the chain timer. For an explanation specifically about construction inside the chain total, the corresponding **later 99 builds** have median sums **95.559671 ms** and **4.976682 ms**. Either label the 100-build totals with that exclusion explicitly or use the 99-build figures; the measured latency and chain totals themselves need no change.

All **601 data SHA-256 entries** and **15 source SHA-256 entries** in the reviewed manifest match the actual files. Its verification object agrees with `verification.json`. The retained logs show 12 Python checks, a passing Go payctl test package, and FastPay test groups with 45 core, 2 network, and 1 adapter test passing. The reproduction metadata records matching rebuilt FastPay binaries and equal sets of 454 normalized Comet production/module files. This review inspected those records and script; it did not independently rebuild binaries or rerun the tests. The FastPay source gate uses CRLF normalization for the Windows checkout; the actual reviewed checkout has no staged protocol changes. Comparing against `HEAD` rather than only the index was suggested as a small reproducibility-script hardening improvement.

The final report appropriately limits these findings to same-host native implementations, distinguishes client modes and startup-failure attempts, discloses transport/storage/process differences, and avoids maximum-throughput and statistical-equivalence claims. Apart from the construction-time wording adjustment, no report correction or new measurement blocker was found. This audit used only read-only file inspection, hashing, and small JSON arithmetic; adapters and measurement data were not modified.

## Final disposition

**Approved; all review findings and final report corrections are resolved.** `RESULTS.md` now explicitly reports construction for the later 99 payments as 95.56 ms and 4.98 ms, and states that the first build occurs before the chain timer. Independently checked each new `later99_build_sum_ms` against the native report: the medians are 95.559671 ms and 4.976682 ms.

The FastPay build script now compares protocol source against `HEAD` with `core.autocrlf=true`, covering both staged and unstaged differences while normalizing checkout line endings. Its updated manifest hash matches the file. The refreshed retained build log records 2 network, 1 adapter, and 45 core tests passing. These narrow confirmations used file inspection and arithmetic only; no tests, builds, benchmarks, adapter edits, or broader repeat audit were performed.

Approval covers the completed single-host report, reproducibility material, primary artifacts, and paced artifacts under the previously stated interpretation limits.
