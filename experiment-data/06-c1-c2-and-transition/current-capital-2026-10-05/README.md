# Current-build finite CAL comparison

实际 Mac 运行已完成，结果见[本轮完整报告](../review-followup-2026-10-05/README.md)。以下保留方法及复现说明。

This is a bounded follow-up to archived E2. It measures finite signing capacity under controlled source publication, with user-funded fees. It introduces no payment, abort, or Intent rules. Results do not establish minimum capital, general availability, crash durability, or an unconditional holder exit.

The design is four fresh nine-process labs: low 3,600 / high 28,800 CAL, crossed with parent publication delays 1 / 3 seconds. Each arm offers 1,200 independent 100-CAL A→B→C units at 20 units/s for 60 seconds, with 128 unfinished slots and at most 60 seconds drain. Execution/Bytes grants remain at least 10^12; each member has one Worker. User principal is separate from organization reserve: the fresh template contains 1,200 independent CAL origins and each of two payers has 1,200 independent final 10,000-FUEL origins. Every public payment must use OwnerFinalUTXO; fee maximum is 1,000. No adaptive funding, serial collection, runtime recharge, or lock clearing occurs.

The existing automatic source-resubmission hook is disabled identically in all arms. Otherwise child execution exposes the parent QC and defeats timer-controlled delay. This is controlled withholding, not a claim about production-default recovery latency. The pilot offers one unit with a 3-second parent delay and checks real public 100-CAL registration, a real Open obligation, member occupancy, closure, and user fee accounting before the matrix.

The shared CAL grant/account belongs to the organization; user principal and FUEL inputs are separate. A formula such as `min(B, rate × amount × delay)` cannot predict fragmented member approvals: both payments reserve capacity, and signing/public execution/local observation have different times. Retry follows the existing 200-ms client policy and failure reasons are retained. The 128-unit pending window skips planned starts when full, so stressed arms are not pure open-loop input. Delays 1/3 seconds are below the 30-second compensation timeout and test normal source closure; partial approvals can remain indefinitely, and a bounded drain does not presume they clear. The same pristine genesis fixture is cloned for every arm before changing only organization CAL authorization/backing and genesis start time.

## Run from a fresh frozen repository root on macOS

```sh
python3 docs/experiments/current-capital-2026-10-05/run.py --output /absolute/new-results --case all
```

The output directory must not exist. Everything, including binaries and a unique pristine template, is below that directory; the old `.run/budget-pristine-template` is never used. Default `--repeats 1` runs four arms once. `--repeats 3` runs twelve arms, reversing cell order on repeat 2. Worst case is 24 minutes of driver admission/drain plus pilot, build, health, exports and audits; typical successful arms finish shortly after 60 seconds. Thus three repetitions may exceed 25 minutes including build or slow exports; choose one initially when the wall-time limit matters. `--case pilot` or `--case matrix` permits a standalone fresh suite. The matrix-only option does not claim the pilot was run.

The runner rebuilds all roles from the current source with Comet overlay and `-tags=comet_v3`. It uses the existing E2 driver, sampler and stopped-state audits. Go and Python must already be available. No Mac run is performed by authoring this runner.

## Freeze and transfer

```sh
python3 docs/experiments/current-capital-2026-10-05/freeze.py --output /absolute/new-current-source-package
```

Extract `source.tgz` into a new empty directory, then run the command above from that frozen root. The archive contains all production Go files (and package tests), go.mod/go.sum, the actual `.scratch/comet-src` including non-Go source/fixtures, complete Comet overlay inputs, this runner/documentation, four imported helper files, and the small wait-baseline experiment folder. `--extra PATH` adds another repository-relative file/directory. It contains no reused runtime/database/template. `source-bundle-manifest.json` lists SHA256 by repository-relative path; Git status and the dirty patch are preserved. Do not claim a bundle Git HEAD if Git metadata is absent; the runtime manifest records exact source hashes instead. The overlay downloads its pinned Comet module and reapplies deterministic changes; the runner records materialized Go hashes before/after build plus changed paths rather than silently ignoring an overlay change.

## Evidence and interpretation

`manifest.json` freezes source/helper hashes, actual experimental environment, Go version, binary hashes, member wrapper GOMAXPROCS/GOGC, and every materialized Comet Go hash. It records Git HEAD/status/patch when available. Source changes after freezing abort execution. Per-arm `environment.json`, `configuration.json`, `network.json`, driver details, snapshots, sampler output, health, node logs and public/member/fee audits are retained. Top-level `analysis.json`, `summary.csv` and `curves.csv` reuse E2 definitions.

Low-capital unfinished/partial approvals are measured outcomes, not discarded errors. `runner-failures.json` records actual execution or accounting failures; high-arm incompletion is reported in `completion.json`, not silently converted into success or removed. Compare offered/admitted/closed units, skipped units, actual missing obligations and fraction, partial/signed-unpublished residuals, member resource refusal calls, failure reasons, final public gap and local temporary reservations, fees, and timings among successes with the failed denominator. Member capacities overlap; do not sum four member slices as backing funds. Count effective principal once per child and distinguish actual obligations from all certificate inputs. One repetition yields descriptive evidence, not confidence intervals.

The scripts have syntax/import/CLI and archive dependency checks on Windows. Actual process compatibility and economic assertions require the Mac pilot; no performance results are implied by those local checks.

Incomplete admitted units are explicitly right-censored at the bounded stop. Their ages are reported; no timeout is imputed as completion and timing percentiles include only observed successes. Curves include CAL, Execution and Bytes independently; user FUEL locks and spending are distinct from organization resource occupancy.
