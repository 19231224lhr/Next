#!/usr/bin/env python3
"""Matched E2 comparisons: both arms use the same parent collection endpoint."""
import argparse
import json
import reproduce as lab

if __name__=='__main__':
    p=argparse.ArgumentParser()
    p.add_argument('--case',choices=['smoke','pairs'],default='smoke')
    p.add_argument('--prefix',default='gate-r1-')
    p.add_argument('--skip-build',action='store_true')
    p.add_argument('--only',choices=['cal-low','work-low','fuel-low','cal-medium','cal-delay3'],help='repeat one predeclared pair using unchanged budgets and binaries')
    p.add_argument('--reverse',action='store_true',help='run the serial arm first in an independent repetition')
    args=p.parse_args()
    lab.BIN=lab.ROOT/'.run/budget-gated-bin'
    lab.labutil.BIN=lab.BIN
    if not args.skip_build:
        lab.labutil.OUT=lab.OUT/'gate-build'
        lab.labutil.OUT.mkdir(exist_ok=True)
        lab.labutil.build()
    if args.case=='smoke':
        lab.run_case(args.prefix+'normal',duration=5,delay=3,count=1,replay=True,gateway_parent=True,serial=True)
        lab.run_case(args.prefix+'repair',duration=45,delay=35,count=1,late=True,replay=True,gateway_parent=True,serial=True)
    else:
        grants=json.loads((lab.OUT/'calibration.json').read_text())['grants']
        # Fixed pre-calibrated budgets; never adjust a tier after seeing results.
        for label,kind,level,delay in [('cal-low',1,0,1),('work-low',3,0,1),('fuel-low',2,0,1),('cal-medium',1,1,1),('cal-delay3',1,1,3)]:
            if args.only and label!=args.only:continue
            modes=[('parallel',False),('serial',True)]
            if args.reverse:modes.reverse()
            for mode,serial in modes:
                lab.run_case(args.prefix+label+'-'+mode,kind=kind,grant=grants[str(kind)][level],delay=delay,gateway_parent=True,serial=serial)
