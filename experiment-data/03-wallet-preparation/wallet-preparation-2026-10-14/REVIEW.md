# Implementation review

Two independent read-only reviews of the changes found no actionable correctness
issues. The reviewer did not execute workloads during performance testing.

1. Cache review: each use reads current bytes from the caller's database
   snapshot. A hit requires exact equality with an owned record that previously
   underwent complete JSON decoding. Snapshot reads that insert entries out of
   order can only cause later misses, not stale acceptance. The cache carries
   Final/Recovered values, not authorization. The public reader returns a fresh
   decoded object. The existing atomic journal and spend-reservation update
   still completes before the request bytes are returned for sending.
2. Commitment review: the sampled integer fits losslessly into the fixed-width
   opening because NewPublic requires the existing 2048-bit modulus. value
   checks the same unit predicate before the same exponentiation and
   full-domain hashing. Failed candidates are retried with the unchanged
   distribution. Returning Opening{} on entropy failure preserves the old
   error-path result as well.

Rejected alternatives: partial JSON decoding would weaken malformed-record
checks; moving request persistence after sending would change crash/retry
behavior. Neither was implemented. No cache of successful payment authorization
or reduction in signature/security checks was introduced.

Verification logs retain both passing and failed invocations. The initial
full-suite run after adding source snapshots failed because ordinary `source/`
directories were discovered as incomplete Go packages. Renaming the artifact
directory to `_source/` fixed that packaging mistake; the final full suite and
wallet/chameleon race suites were then rerun. The retained failure log is
`snapshot-layout-test.log`, and the final results are in `final-full-test.log`
and `final-race.log`.
