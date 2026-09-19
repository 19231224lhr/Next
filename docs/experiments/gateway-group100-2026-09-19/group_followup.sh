#!/bin/sh
set -eu
cd /Users/richz/lab/man/utxo-fastpay-v12
python3 .run/run_group_experiment.py plain-b2 group-b-bin plain
python3 .run/run_group_experiment.py plain-a3 group-a-bin plain
echo FOLLOWUP_COMPLETE
