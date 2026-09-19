#!/bin/sh
set -eu
cd /Users/richz/lab/man/utxo-fastpay-v12
python3 .run/run_group_experiment.py trace-a1 group-a-bin trace
python3 .run/run_group_experiment.py trace-b group-b-bin trace
python3 .run/run_group_experiment.py trace-a2 group-a-bin trace
python3 .run/run_group_experiment.py plain-a1 group-a-bin plain
python3 .run/run_group_experiment.py plain-b group-b-bin plain
python3 .run/run_group_experiment.py plain-a2 group-a-bin plain
echo SERIES_COMPLETE
