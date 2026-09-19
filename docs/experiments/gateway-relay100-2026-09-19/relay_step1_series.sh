#!/bin/sh
set -eu
cd /Users/richz/lab/man/utxo-fastpay-v12
exec > .run/relay-step1-series.log 2>&1
python3 .run/run_group_experiment.py relay1-trace-a1 group-b-bin trace
python3 .run/run_group_experiment.py relay1-trace-b relay-step1-bin trace
python3 .run/run_group_experiment.py relay1-trace-a2 group-b-bin trace
python3 .run/run_group_experiment.py relay1-plain-a1 group-b-bin plain
python3 .run/run_group_experiment.py relay1-plain-b relay-step1-bin plain
python3 .run/run_group_experiment.py relay1-plain-a2 group-b-bin plain
echo RELAY_STEP1_COMPLETE
