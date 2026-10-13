"""Per-run statistics from real-service reports; no pooling across runs."""
import argparse
import csv
import json
import pathlib
import statistics

def percentile(values, fraction):
    values = sorted(values)
    position = (len(values)-1)*fraction
    lower = int(position)
    return values[lower]+(values[min(lower+1, len(values)-1)]-values[lower])*(position-lower)

def summarize(root):
    plan = json.loads((root/'plan.json').read_text())
    outcomes = {}
    for line in (root/'status.jsonl').read_text().splitlines():
        record = json.loads(line)
        outcomes[(record['repeat'], record['arm'])] = record['exit']
    expected_length = int(plan['arguments'].get('length', 100))
    rows = []
    for job in plan['jobs']:
        directory = root/f"r{job['repeat']}-{job['arm']}"
        file = directory/('native-client.json' if job['arm']=='fastpay' else 'result.json')
        if not file.exists():
            rows.append({**job, 'status':'missing'})
            continue
        data = json.loads(file.read_text())
        if job['arm']=='fastpay':
            samples = data['samples_ms']
            data.update(receipt_ms_p50=percentile(samples,.5), receipt_ms_p95=percentile(samples,.95))
            valid = data.get('probe_completed') and data.get('principal_conserved') and data.get('completed')==len(samples)==expected_length
        else:
            valid = data.get('native_exit_code')==0 and (data.get('closure_status')=='complete' if data['workload']=='chain' else data.get('completion_fraction')==1)
            if data['workload']=='chain':
                valid = valid and data.get('completed')==expected_length
        valid = valid and outcomes.get((job['repeat'],job['arm']))==0
        keys = ['chain_total_ms','receipt_ms_p50','receipt_ms_p95','build_total_ms','completed','attempted','receipts','closed','offered_closed_fraction','receipt_goodput_per_s','public_goodput_per_s']
        rows.append({**job, 'status':'complete' if valid else 'invalid', **{k:data[k] for k in keys if k in data}})
    fields = list(dict.fromkeys(k for row in rows for k in row))
    with (root/'runs.csv').open('w',newline='') as stream:
        writer=csv.DictWriter(stream,fieldnames=fields); writer.writeheader(); writer.writerows(rows)
    output = {'all_complete':all(r['status']=='complete' for r in rows), 'arms':{}}
    for arm in dict.fromkeys(r['arm'] for r in rows):
        selected=[r for r in rows if r['arm']==arm and r['status']=='complete']
        stats={}
        for key in fields:
            if key in ('repeat','arm','status'): continue
            values=[r[key] for r in selected if key in r]
            if values: stats[key]={'n':len(values),'median':statistics.median(values),'min':min(values),'max':max(values)}
        output['arms'][arm]=stats
    pairs=[]
    for repeat in sorted(set(r['repeat'] for r in rows)):
        pair={r['arm']:r for r in rows if r['repeat']==repeat and r['status']=='complete'}
        if 'before' in pair and 'after' in pair:
            pairs.append({'repeat':repeat, **{key:100*(1-pair['after'][key]/pair['before'][key]) for key in ('chain_total_ms','receipt_ms_p50','receipt_ms_p95') if key in pair['before'] and key in pair['after']}})
    output['paired_reduction_percent']=pairs
    (root/'summary.json').write_text(json.dumps(output,indent=2)+'\n')
    return output

if __name__=='__main__':
    parser=argparse.ArgumentParser(); parser.add_argument('root',type=pathlib.Path)
    print(json.dumps(summarize(parser.parse_args().root),indent=2))
