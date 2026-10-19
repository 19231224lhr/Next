"""Aggregate independent runs; never treat repeated queries as independent runs."""
import json
import pathlib
import statistics
import sys

root = pathlib.Path(sys.argv[1])
reports = [json.loads(p.read_text()) for p in sorted(root.glob('run*/report.json'))]
assert len(reports) == 3, len(reports)

def summary(values):
    return {'median': statistics.median(values), 'min': min(values), 'max': max(values), 'runs': values}

out = {'runs': 3, 'aggregation': 'median and range across three process-level medians', 'configurations': []}
for n, m in ((32, 1), (32, 8), (256, 1), (256, 8)):
    configs = [next(x for x in r['configurations'] if x['n'] == n and x['m'] == m) for r in reports]
    assert all(c['independentReceiver'] for c in configs)
    row = {'n': n, 'm': m, 'methods': [], 'setupMS': {}}
    for key in configs[0]['setupTimingNS']:
        row['setupMS'][key] = summary([c['setupTimingNS'][key] / 1e6 for c in configs])
    for method in ('N*-lean', 'B-lean'):
        item = {'method': method}
        for metric in ('exportNS', 'verifyNS', 'totalNS'):
            values = []
            for c in configs:
                samples = [s[metric] for s in c['samples'] if s['method'] == method]
                assert len(samples) == 30
                values.append(statistics.median(samples) / 1e6)
            item[metric.replace('NS', 'MS')] = summary(values)
        values = []
        for c in configs:
            samples = [s['cachedLookupNS'] for s in c['cachedSamples'] if s['method'] == method]
            assert len(samples) == 100
            values.append(statistics.median(samples) / 1e3)
        item['cacheUS'] = summary(values)
        methods = [next(x for x in c['methods'] if x['method'] == method) for c in configs]
        for key in ('packetBytes', 'packetBytesPerSlot', 'retainedCacheBytes'):
            item[key] = summary([x[key] for x in methods])
        for x in methods:
            assert sum(x['components'].values()) == x['packetBytes']
        row['methods'].append(item)
    out['configurations'].append(row)
(root / 'summary.json').write_text(json.dumps(out, indent=2), encoding='utf-8')
lines = ['| n,m | Method | Export ms | Verify ms | Total ms [run range] | Packet KiB | Cache lookup us |',
         '| --- | --- | ---: | ---: | ---: | ---: | ---: |']
for c in out['configurations']:
    for x in c['methods']:
        total = x['totalMS']
        lines.append(f"| {c['n']},{c['m']} | {x['method']} | {x['exportMS']['median']:.3f} | {x['verifyMS']['median']:.3f} | {total['median']:.3f} [{total['min']:.3f}, {total['max']:.3f}] | {x['packetBytes']['median']/1024:.2f} | {x['cacheUS']['median']:.3f} |")
text = '\n'.join(lines) + '\n'
(root / 'summary-table.md').write_text(text, encoding='utf-8')
print(text)
