# Next ordinary-payment adapter — 2026-10-13

The adapter is `run_next.py`, invoked by Linux Python 3 with only standard-library dependencies. It launches real `payctl init-lab`, `lab-run`, `chain-v4` or `bench-v4` against the Go binaries from source baseline `f6d0387`. One benchmark-client grant filter was corrected in `cmd/payctl/direct_bench.go`: a payer no longer adds the receiver's `ResourcePolicy` grant to its signed request. The protocol and server implementations were not changed.

## Build and invocation

From `C:\Users\Bocchi\Desktop\Next\.e8-worktree` in PowerShell:

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -tags=comet_v3 -o .scratch/baseline-bin/ ./cmd/committee ./cmd/member ./cmd/gateway ./cmd/payctl
wsl.exe -d Ubuntu -- python3 /mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree/docs/experiments/baseline-design-2026-10-13/run_next.py --bin /mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree/.scratch/baseline-bin --run-dir /home/qyinf/next-adapter-smoke-final-chain-1 --workload chain --length 2 --count 3 --storage memory
wsl.exe -d Ubuntu -- python3 /mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree/docs/experiments/baseline-design-2026-10-13/run_next.py --bin /mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree/.scratch/baseline-bin --run-dir /home/qyinf/next-adapter-review-paced-1 --workload independent --length 1 --count 5 --storage memory --rate 2
```

Each `--run-dir` must be a new path on Linux ext4. Binaries may reside on a mounted source directory; native services, wallet stores, reports, and logs reside in the ext4 run directory. The adapter selects free loopback ports, writes one JSON result to stdout, and retains native reports and service logs. On normal exit or Python SIGTERM it stops the lab supervisor, terminates any remaining members of its dedicated process group, and verifies that no live run process remains. `--rate` is optional for independent payments; default 0 means native closed-loop scheduling.

## Deployment and endpoint

The running payment route uses four org0 members, one gateway, and four genuine committee services: **nine server processes**, plus the `lab-run` supervisor and one measurement client. Native `init-lab` initially describes two organizations; the adapter saves its original node inventory, then omits the idle org1 member and gateway processes from `lab.json`. The native network and committee topology remain intact. The second owner's genesis CAL outputs are removed before servers start, and chain-c has none. `init-lab` gives the payer one 100-CAL origin for a chain, or one 100-CAL origin per independent payment; every chain hop transfers the same 100 CAL and spends exactly the preceding hop's certified output. These are normal same-organization payments with normal background public execution, not pool payments.

`chain-v4` and `bench-v4` both report `FastMS` using Go's monotonic clock: start immediately before the payer's gateway HTTP POST; stop only after the receiving wallet decodes and verifies the output certificate and `ReceiveDirect` accepts it. Request construction and owner signing are outside every `FastMS` sample. Independent requests are all prebuilt and signed before measurement. Chain requests are built one hop at a time, with `BuildMS` recorded separately. `FastChainMS` is first send to final receiving-wallet acceptance and therefore **includes construction/signing of later chain hops**. `PublicChainMS`, `ClosedChainMS`, and independent public-closure observations are retained in raw reports as different endpoints; they are not labeled receipt latency.

Independent runs use `--concurrency 1 --max-pending count`. The send permit is released at receiving-wallet receipt acceptance, while public closure continues under a separate pending bound. Paced JSON exposes every native `SentUnixNS`, dispatch lag, and the realized rate from first to last send. A requested rate alone is not treated as achieved. The adapter accepts a sample with a verified fast receipt even if later public closure fails; it reports `receipt_status` and `closure_status` separately, retains closure errors and the native client exit code, and rejects missing receipts or send/authorization failures. Native reports and client logs remain available when the client exits nonzero after closure failure.

`--storage memory` sets the four documented experimental switches for member, gateway, and committee application state plus Comet's **blockstore, state, and evidence MemDBs**. Other Comet artifacts may still write to disk. The measurement wallet remains no-sync Bolt. `--storage sync` sets synchronous member storage and uses the native synchronous gateway, committee, and Comet stores. Independent sync runs use the default synchronous wallet; `chain-v4` hardcodes a no-sync wallet even in the sync arm, which is disclosed in JSON `storage_detail`. Both modes retain service logs and native reports. The run directory cannot be on WSL's Windows-mounted 9p filesystem.

## Verification

- `go test -tags=comet_v3 ./cmd/payctl` passed.
- `python3 -m unittest discover -s .../baseline-design-2026-10-13 -p test_run_next.py -v` passed eight focused checks. The new receipt/closure, missing amount/identity/timestamp, nonzero-client-log, and process-group checks were observed failing before their fixes, then passed.
- The red same-org independent smoke at `/home/qyinf/next-adapter-smoke-independent-1` produced five member `INVALID_REQUEST` rejections: `bench-v4` included the receiver's policy grant in payer requests. After the one-condition filter, `/home/qyinf/next-adapter-smoke-independent-2` completed five of five real receiver receipts.
- The nine-service memory chain at `/home/qyinf/next-adapter-smoke-nine-1` completed two of two hops, with each later input equal to the preceding certified output and `CertificateInput=true`. The nine-service paced independent smoke at `/home/qyinf/next-adapter-smoke-nine-independent-1` completed three of three receipts at requested rate 2/s.
- The 14-service sync preliminary checks completed two independent payments and two chain hops before the idle-node launch filter. Their raw runs are `/home/qyinf/next-adapter-smoke-sync-1` and `/home/qyinf/next-adapter-smoke-sync-chain-1`.
- The final nine-service sync independent smoke at `/home/qyinf/next-adapter-smoke-final-sync-1` completed two of two payments at requested rate 1/s, with zero receiver genesis CAL outputs and the run filesystem recorded as ext4.
- The final nine-service memory chain at `/home/qyinf/next-adapter-smoke-final-chain-1` completed two of two hops from exactly one payer genesis CAL origin, even though the shared interface received `--count 3`.
- The revised nine-service paced run at `/home/qyinf/next-adapter-review-paced-1` completed five of five receipts with `max_pending=5`, actual send timestamps, dispatch lags, and a realized rate of 2.001/s for a requested 2/s. This is a functional dispatch check, not a performance result. Earlier paced smokes used closure-coupled workers and do not establish offered-rate delivery.
- A live SIGTERM smoke stopped the Python adapter after the nine-service lab became ready; the adapter exited and no process bearing that run directory remained.
- A Windows-mounted run directory was rejected before initialization with filesystem type `9p`.

These are functional smokes only, not matched FastPay measurements or speed claims. The parent coordinator will perform repeated interleaved runs and gather comparable resource measurements.
