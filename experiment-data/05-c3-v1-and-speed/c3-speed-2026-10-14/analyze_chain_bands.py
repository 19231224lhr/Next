"""Within-run chain bands; report actual sums and separate closure endpoints."""
import argparse, csv, json, pathlib, statistics
from summarize import percentile

def analyze(root):
    plan=json.loads((root/'plan.json').read_text())
    rows=[]
    for job in plan['jobs']:
        run=root/f"r{job['repeat']}-{job['arm']}"
        if job['arm']=='fastpay':
            data=json.loads((run/'native-client.json').read_text())
            samples=data['samples_ms']; builds=data.get('build_ms')
            if builds is not None and len(builds)!=len(samples):raise ValueError('FastPay build sample count mismatch')
            endpoints={'chain_ms':data['chain_total_ms']}
        else:
            data=json.loads((run/'reports/chain-v4.json').read_text())
            samples=[h['FastMS'] for h in data['Hops']]
            builds=[h['BuildMS'] for h in data['Hops']]
            endpoints={'chain_ms':data['FastChainMS'],'public_ms':data['PublicChainMS'],'closed_ms':data['ClosedChainMS']}
        bands=[(1,100),(101,500),(501,len(samples)-100),(max(101,len(samples)-99),len(samples))] if len(samples)>500 else [(1,len(samples))]
        for first,last in bands:
            if last<first:continue
            values=samples[first-1:last]
            rows.append({**job,'first_hop':first,'last_hop':last,'n':len(values),'receipt_p50_ms':statistics.median(values),'receipt_p95_ms':percentile(values,.95),'receipt_max_ms':max(values),'receipt_sum_ms':sum(values),'top10_sum_ms':sum(sorted(values,reverse=True)[:10]),'build_sum_ms':sum(builds[first-1:last]) if builds is not None else None,'build_available':builds is not None,**endpoints})
    fields=list(dict.fromkeys(k for row in rows for k in row))
    with (root/'bands.csv').open('w',newline='') as out:
        writer=csv.DictWriter(out,fieldnames=fields);writer.writeheader();writer.writerows(rows)
    return rows

if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('root',type=pathlib.Path)
    print(json.dumps(analyze(parser.parse_args().root),indent=2))
