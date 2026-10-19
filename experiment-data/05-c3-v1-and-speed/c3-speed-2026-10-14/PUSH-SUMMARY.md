# New C3 speed optimization: retained result

The selected implementation is Stage 9b. It keeps issuer-funded C3, CAL/FUEL
reservation, recipient receipt verification, and background settlement active.
It reduces duplicate validation/encoding, writer-lock work, repeated certificate
delivery preparation, unnecessary outbox rescans, and repeated approval reads.
Stage 10 was reverted after three paired performance regressions.

| Shared WSL CPU budget | Payments / repetitions | Next median | Tuned FastPay median | Time reduction |
|---|---|---:|---:|---:|
| 0–3 (guest topology: 2 cores / 4 threads) | 100 / 9 | 471.374 ms | 639.286 ms | 26.3% |
| 0–3 | 1000 / 7 | 6349.661 ms | 6611.357 ms | 4.0% |
| 0–7 (guest topology: 4 cores / 8 threads) | 1000 / 7 | 5236.536 ms | 6832.863 ms | 23.4% |

The two-core long-chain result is close to parity across independent batches;
the earlier batch was 1.1% slower than FastPay. Short-chain and four-core long-chain
comparisons won every final repetition. These are single-host receiver-ready
measurements, with later public/member closure recorded separately.

Five paired mixed-load runs reduced receipt P50 from 9.758 to 2.988 ms and P95
from 39.413 to 21.961 ms relative to frozen C3. All actually submitted payments
closed. Nine real-service fee-order cases kept the user fee fixed. Full tagged
Go tests and the affected race suites passed during final verification.

See [REPORT.md](REPORT.md), [REPRODUCE.md](REPRODUCE.md),
[DECISIONS.md](DECISIONS.md), and [final-results.csv](final-results.csv).
The repository includes code, tests, runners, final verification records and
per-series plans, summaries, run metrics, timing breakdowns, and export hashes.
The full 1.09 GB evidence archive, including detailed per-payment JSON and logs,
remains locally under this directory's `raw-results/` and in
`/home/qyinf/next-c3-speed-20261014`; those large raw files are not all committed.
The export manifest identifies every retained local file and its SHA-256.

Next useful targets are wallet request preparation and long-chain receipt tails.
No manuscript edits are part of this optimization commit.
