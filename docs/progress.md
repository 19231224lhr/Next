# Implementation progress

Baseline: protocol v1.1, ProtocolVersion=2 / WireVersion=2.
Primary checkout: /Users/richz/lab/man/utxo-fastpay (Mac Studio).
This ledger records actual delivered capability; unimplemented items are not test passes.

| Module | Status | Evidence |
|---|---|---|
| Protocol, engineering and storage | in progress | Initial repository; protocol and transaction storage first |
| Organization signing and background INSTALL | not started | |
| Committee settlement and guarantees | not started | |
| Fees, cumulative credit and refill | not started | |
| Gateway, peers and wallet | not started | |
| Scheduling, archive and operations | not started | |
| Tests, benchmarks and architecture reference tool | not started | |

## Decisions
- Go 1.27.1 on macOS arm64; Linux verification must use matching Go.
- Fixed membership and signature quorum; no candidate cancellation or retirement release.
- No unsupported incoming-deposit credit; bootstrap only finite real genesis inputs.
- Formal throughput requires sustained end-to-end evidence, not isolated signature throughput.
