Stage10 candidate preserved before restoring prior Stage9b gateway sources.
Complete source+test snapshots are in this directory as direct_relay.go.txt,
direct_outbox_cache.go.txt, direct_install_bytes_test.go.txt and
direct_outbox_cache_test.go.txt. The .txt suffix prevents Go package discovery;
path-relocations.json records original paths and unchanged source hashes.
direct_relay-cumulative.patch includes earlier accepted relay stages relative to repository HEAD; it is not a Stage10-only patch.
A Stage10-only reverse/forward patch will be added after scoped restoration.
Stage10 frozen Linux gateway remains /home/qyinf/next-c3-speed-20261014/stage10-bin/gateway.
Pilot rejected retention: all 3 totals regressed versus Stage9b, approximately 5.39%, 1.81%, 1.77%.
