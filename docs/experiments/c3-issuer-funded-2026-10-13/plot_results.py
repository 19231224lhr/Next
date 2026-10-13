#!/usr/bin/env python3
"""Single-panel vector figures from measured CSVs; retain every valid repetition."""
import argparse
import csv
import pathlib
import re
import statistics
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

plt.rcParams.update({'font.family':'sans-serif','font.sans-serif':['Arial','DejaVu Sans'],
                     'font.size':8,'axes.labelsize':8,'legend.fontsize':7,'xtick.labelsize':7,'ytick.labelsize':7,
                     'pdf.fonttype':42,'svg.fonttype':'none','axes.spines.top':False,'axes.spines.right':False,
                     'axes.linewidth':.7,'legend.frameon':False})


def rows(path):
    with path.open(encoding='utf-8-sig',newline='') as f:
        return list(csv.DictReader(f))


def export(fig,path):
    fig.savefig(path.with_suffix('.pdf'))
    fig.savefig(path.with_suffix('.svg'))
    fig.savefig(path.with_suffix('.png'),dpi=300)
    plt.close(fig)


def paired(data,metric,label,path):
    by_round={}
    for r in data:
        if r['workload']!='chain' or r['trace']!='False' or int(r['native_exit_code']):
            continue
        number=int(re.search(r'-r(\d+)-',r['run']).group(1))
        by_round.setdefault(number,{})[r['variant']]=float(r[metric])
    if not by_round or any(set(pair)!= {'baseline','c3'} for pair in by_round.values()):
        raise ValueError('paired plot requires every baseline/C3 pair')
    fig,ax=plt.subplots(figsize=(3.5,2.7))
    fig.subplots_adjust(left=.2,right=.96,bottom=.19,top=.88)
    for pair in by_round.values():
        ax.plot([0,1],[pair['baseline'],pair['c3']],color='#b5b5b5',lw=.8,zorder=1)
        ax.scatter([0,1],[pair['baseline'],pair['c3']],c=['#666666','#3976a8'],s=22,zorder=2)
    ax.set(xticks=[0,1],xticklabels=['Previous Next','Next + C3'],ylabel=label,xlim=(-.3,1.3),ylim=(0,None))
    ax.text(0,1.045,f'{len(by_round)} paired runs; each line is one pair',transform=ax.transAxes,fontsize=7)
    export(fig,path)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--csv',type=pathlib.Path,required=True)
    p.add_argument('--out',type=pathlib.Path,required=True)
    a=p.parse_args(); a.out.mkdir(parents=True,exist_ok=True)
    fees=[r for r in rows(a.csv/'fee_runs.csv') if r['case']=='delay']
    if not fees or any(r['error'] or r['closed_pairs']!=r['requested_pairs'] for r in fees):
        raise ValueError('delay figure requires all intended fee runs to be complete; inspect raw errors')
    delays=sorted({float(r['delay_ms']) for r in fees})
    fig,ax=plt.subplots(figsize=(3.5,2.9))
    fig.subplots_adjust(left=.17,right=.97,bottom=.18,top=.77)
    for field,label,color,marker in [('source_user_fee','User fee','#555555','o'),
                                   ('source_cost','Issuer expense','#3976a8','s'),
                                   ('source_rewards','Committee reward','#a56b1e','^')]:
        centers=[]; lows=[]; highs=[]
        for delay in delays:
            selected=[r for r in fees if float(r['delay_ms'])==delay]
            values=[(float(r['source_burned'])+float(r['source_rewards']) if field=='source_cost' else float(r[field]))/int(r['requested_pairs']) for r in selected]
            center=statistics.median(values)
            centers.append(center); lows.append(center-min(values)); highs.append(max(values)-center)
        ax.errorbar(range(len(delays)),centers,yerr=[lows,highs],label=label,color=color,marker=marker,
                    markersize=3.5,lw=1,capsize=2)
    ax.set(xlabel='Source submission delay (s; discrete conditions)',
           xticks=range(len(delays)),xticklabels=[f'{d/1000:g}' for d in delays],
           ylabel='FUEL per source payment',ylim=(0,11.2))
    ax.legend(loc='lower left',bbox_to_anchor=(-.02,1.015),ncol=2,columnspacing=.8,handlelength=1.4)
    export(fig,a.out/'source-delay-cost')
    perf=rows(a.csv/'performance_runs.csv')
    paired(perf,'chain_total_ms','100-payment chain completion (ms)',a.out/'chain-completion')
    paired(perf,'receipt_ms_p50','Receipt latency P50 (ms)',a.out/'receipt-latency')


if __name__=='__main__':
    main()
