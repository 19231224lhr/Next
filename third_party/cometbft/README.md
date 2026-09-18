# Pinned CometBFT adaptation

Run `python3 third_party/cometbft/overlay.py` with Go on PATH before the first
build. It materializes CometBFT v0.38.26 into `.scratch/comet-src`, checks every
upstream patch anchor and sets the root module's local replace. It does not edit
the module cache. Source additions are kept here; generated upstream code is
ignored. Build the payment application with `-tags=comet_v3`.

The fork changes transaction commitments, block-part commitments/wire encoding,
authenticated historical BlockStore revision, original-version replay and
catch-up, and the node's pre-replay application hook. It preserves normal BFT
rounds and quorum rules. It is not wire-compatible with an unmodified network.

Only an application-committed exact RepairInput can authorize ReviseBlock.
Original bytes are retained for replay; current parts are genuinely rewritten.
Monetary changes execute at the new repair height, never retrospectively.

The cryptographic package is supplied by this application's root module. This
directory is a patch source bundle, not a standalone Comet distribution.
See [implementation and evidence](../../docs/implementation-v1.2.md).
