# Source snapshot relocation

Eleven archived source/test copies from stage9-approval-micro, stage9b-approval-micro, stage9-source-snapshot and stage10-candidate now use .go.txt rather than .go. File bytes and SHA256 hashes are unchanged. This prevents normal Go package discovery from treating experiment snapshots as standalone packages.

Each affected directory contains path-relocations.json mapping original absolute paths to relocated paths and recording source hashes plus unchanged historical metadata hashes. Original manifest.json, hashes.json, overlay.json and current-overlay.json are preserved without modification; they describe the actual historical measurement.

For a new replay of a historical benchmark, use overlay.relocated.json and, where present, current-overlay.relocated.json. These are new replay helpers, not the files originally used in measurement. The manifests' historical commands still name their original overlay files; substitute the corresponding relocated overlay explicitly when replaying.

bench_approval_reuse.py now creates only .go.txt source overrides and reads .go.txt files from --before-source-dir. Stage10 README lists the relocated candidate copies. restore_stage9b.py intentionally operates on live internal/gateway/*.go files; its comment distinguishes those live paths from saved .go.txt snapshots. It was not executed.

Lightweight filesystem/hash inspection found no .go files remaining in this experiment directory. No Go tests, builds or benchmarks were run during relocation.
