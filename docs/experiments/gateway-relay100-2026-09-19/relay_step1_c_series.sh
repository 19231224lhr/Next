#!/bin/sh
set -eu
cd /Users/richz/lab/man/utxo-fastpay-v12
exec > .run/relay-step1-c-series.log 2>&1
python3 .run/run_group_experiment.py relay1-trace-c relay-step1-c-bin trace
python3 .run/run_group_experiment.py relay1-plain-c1 relay-step1-c-bin plain
python3 .run/run_group_experiment.py relay1-plain-a4 group-b-bin plain
python3 .run/run_group_experiment.py relay1-plain-c2 relay-step1-c-bin plain
python3 .run/run_group_experiment.py relay1-plain-a5 group-b-bin plain
echo RELAY_STEP1_C_COMPLETE
