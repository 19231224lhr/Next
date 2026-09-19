#!/bin/sh
set -eu
cd /Users/richz/lab/man/utxo-fastpay-v12
exec > .run/relay-step1-followup.log 2>&1
python3 .run/run_group_experiment.py relay1-plain-b2 relay-step1-bin plain
python3 .run/run_group_experiment.py relay1-plain-a3 group-b-bin plain
echo RELAY_STEP1_FOLLOWUP_COMPLETE
