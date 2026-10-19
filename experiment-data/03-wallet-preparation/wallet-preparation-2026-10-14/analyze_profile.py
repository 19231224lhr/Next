"""Diagnostic stage sums; not an unprofiled performance comparison."""
import argparse
import json
import pathlib

p = argparse.ArgumentParser()
p.add_argument('lab', type=pathlib.Path)
p.add_argument('output', type=pathlib.Path)
a = p.parse_args()
result = {'diagnostic_only': True, 'runs': {}}
for name in ('baseline-diagnostic', 'cache-diagnostic', 'final-diagnostic'):
    report = json.loads((a.lab/name/'chain/reports/chain-v4.json').read_text())
    rows = report['Hops'][1:]
    result['runs'][name] = {group: {key: sum(h[group].get(key, 0) for h in rows)
                                  for key in rows[0][group]}
                           for group in ('BuildStagesMS', 'BuildSaveStagesMS')}
    result['runs'][name]['next_build_ms'] = sum(h['BuildMS'] for h in rows)
a.output.write_text(json.dumps(result, indent=2)+'\n')
print(json.dumps(result, indent=2))
