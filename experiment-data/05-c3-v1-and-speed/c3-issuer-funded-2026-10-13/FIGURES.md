# Figure contract and captions

All three figures are single-panel quantitative comparisons, generated exclusively with Python/Matplotlib from the exported CSV files. No panel alignment comparison is applicable. PDF/SVG retain editable vector text; PNG is a preview. Final physical width is 3.5 inches, with 7–8 pt text. No journal-specific compliance claim is made.

1. **Source delay and fee responsibility.** Question: does slower source submission increase the issuer's expense while leaving the accepted user quote and service reward unchanged? Data: `fee_runs.csv`, delay suite only. Per run, divide source totals by its 20 source payments. Plot the median across three runs; whiskers show the minimum and maximum. These are configured FUEL units, not monetary prices or measurements of CPU cost. All completed delay-suite repetitions are included; the script stops on incomplete rows.
2. **100-payment chain completion.** Question: how much does C3 change the actual completion time of the existing dependent-payment workload? Data: `performance_runs.csv`, five non-traced baseline/C3 pairs. Each connected pair is one repetition; no observations are removed or pooled across repetitions.
3. **Receipt latency.** The same five run pairs, showing each run's P50 verified receipt latency. This endpoint starts before gateway submission and ends after the recipient wallet has verified and accepted the certificate. Whole-chain construction/preparation time is included in Figure 2 but not this per-request endpoint.

The performance comparison uses the same host, CPU affinity, storage mode and workload in both arms. The previous Next implementation is the baseline; these are not external-system comparisons. No hypothesis test or p-value is used for five local repetitions.

Delay conditions use equally spaced categorical positions, explicitly labelled in seconds. This keeps 0 and 0.1 s observations distinguishable; connecting lines guide the eye and do not assert linear interpolation across time. The 12 s condition was added after the initial five conditions produced no compensation; all six conditions and all repetitions are retained.

## Final QA

- CSV completeness and economic reconciliation: `csv/evidence-check.json`, PASS.
- Source preflight: `figure-qa/source.json`, no failures. The sole warning asks for TIFF; these are vector line figures delivered as PDF/SVG with 300 dpi PNG previews, so a TIFF submission raster is unnecessary.
- All three PDF text audits: minimum 7 pt, no undersized runs.
- All three rendered collision audits: zero FAIL and zero WARN. QA-only overlay PDFs are kept separately from the deliverable figures.
- Visual inspection of all three rendered figures: labels readable, no clipping; delay conditions separated to avoid overlapping 0 and 0.1 s markers. Single panels, hence multi-panel alignment is not applicable.
