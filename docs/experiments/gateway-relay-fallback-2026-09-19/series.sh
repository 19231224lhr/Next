#!/bin/sh
set -eu
cd /Users/richz/lab/man/utxo-fastpay-v12
exec > .run/relay-step2-series.log 2>&1
python3 .run/run-relay-step2.py relay2-trace-a1 relay-step1-c-bin trace
python3 .run/run-relay-step2.py relay2-trace-b relay-step2-bin trace
python3 .run/run-relay-step2.py relay2-trace-a2 relay-step1-c-bin trace
python3 .run/run-relay-step2.py relay2-plain-a1 relay-step1-c-bin plain
python3 .run/run-relay-step2.py relay2-plain-b1 relay-step2-bin plain
python3 .run/run-relay-step2.py relay2-plain-a2 relay-step1-c-bin plain
python3 .run/run-relay-step2.py relay2-plain-b2 relay-step2-bin plain
python3 .run/run-relay-step2.py relay2-plain-a3 relay-step1-c-bin plain
python3 .run/run-relay-step2.py relay2-fault relay-step2-bin trace reject-gateway
echo RELAY_STEP2_COMPLETE
