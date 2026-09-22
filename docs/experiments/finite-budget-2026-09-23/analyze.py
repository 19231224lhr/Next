#!/usr/bin/env python3
"""Recompute E2 tables/curves from recorded state and offline audits."""
import argparse
import csv
import gzip
import json
from pathlib import Path

OUT=Path(__file__).resolve().parent


def lines(path):
    if path.exists():return path.open(encoding='utf-8')
    return gzip.open(str(path)+'.gz','rt',encoding='utf-8')
def read(path):
    with lines(path) as f:return json.load(f)
def percentile(values,p):
    if not values:return None
    values=sorted(values);return values[min(len(values)-1,int((len(values)-1)*p))]

def sampled_mean(points,end):
    # Trapezoidal sample estimate on the input window, including partial edges.
    area=covered=0
    for (ta,va),(tb,vb) in zip(points,points[1:]):
        a=max(0,ta);b=min(end,tb)
        if b<=a or tb<=ta:continue
        x=va+(vb-va)*(a-ta)/(tb-ta);y=va+(vb-va)*(b-ta)/(tb-ta)
        area+=(x+y)*(b-a)/2;covered+=b-a
    return area/covered if covered else None


def analyze_case(path):
    report=read(path/'reports/budget-v4.json');audit=read(path/'reports/budget-audit.json')
    config=read(path/'configuration.json');last=read(path/'last-snapshots.json')
    units=report['Units'];payments=audit['Payments'];duration=report['DurationSeconds']
    start=report['StartedUnixNS'];elapsed=(report['StoppedUnixNS']-start)/1e9
    child_public=sum(p['Role']=='child' and p['Public'] for p in payments)
    obligations=[p['Obligation'] for p in payments if p.get('Obligation')]
    fulfilled=sum(o['Status']==1 for o in obligations);repaired=sum(o['Status']==2 for o in obligations)
    temporary={};residual={};released={}
    for node,snapshot in last.items():
        if not node.startswith('org0-member'):continue
        for d in snapshot['Detail'].get('Debits',[]):
            kind=d['Key']['Kind'];temporary[kind]=max(temporary.get(kind,0),d['Charged']-d['Released']-d['NetSpent'])
            residual[kind]=max(residual.get(kind,0),d['NetSpent']);released[kind]=max(released.get(kind,0),d['Released'])
    attempts=[u[k]['Attempts'] for u in units for k in ['Parent','Child'] if u[k]['SentUnixNS']]
    retries=sum(a>1 for a in attempts)
    kinds={str(i):sum(s['Limited'][i] for n,s in last.items() if n.startswith('org0-member')) for i in range(1,6)}
    # Failure maps retain the full transport reason. A resource counter diagnoses
    # the rejection stage; transport 503 alone is not labelled budget failure.
    reasons={}
    for u in units:
        for field in ['ParentFailures','ChildFailures']:
            for reason,n in u[field].items():reasons[reason]=reasons.get(reason,0)+n
    calgrant=next(r['Grant'] for r in last['committee0']['Resources'] if r['Key']['Kind']==1)
    observed_children=[u for u in units if 0<u['Child']['FinalUnixNS']<=start+duration*1e9]
    obligated_units={p['Unit'] for p in payments if p.get('Obligation')}
    summary={
        'case':path.name,'kind':config['kind'],'grant':config['grant'],'delay_s':config['delay_s'],
        'offered_units':report['Offered'],'admitted_units':report['Admitted'],'not_started':report['NotStarted'],
        'not_started_ratio':report['NotStarted']/report['Offered'],
        'ready_parents':sum(u['Parent']['ReadyUnixNS']>0 for u in units),
        'ready_children':sum(u['Child']['ReadyUnixNS']>0 for u in units),
        'ready_children_within_window':sum(0<u['Child']['ReadyUnixNS']<=start+duration*1e9 for u in units),
        'child_final_observed_within_window':len(observed_children),
        'child_CAL_observed_within_window':sum(u['Child']['OutputData']['Amount'] for u in observed_children),
        'CAL_per_backing_CAL_per_s_input_window':sum(u['Child']['OutputData']['Amount'] for u in observed_children)/calgrant/duration,
        'actually_guaranteed_children_observed_within_window':sum(u['Index'] in obligated_units for u in observed_children),
        'public_payments':sum(p['Public'] for p in payments),'public_children':child_public,
        'closed_payments':sum(p['Public'] and p['Fee']['Closed'] for p in payments),
        'partial_approval_payments':audit['PartialApprovals'],
        'unsigned_or_partial_nonready':{str(i):sum(not p['Ready'] and not p['Public'] and p['Signers']==i for p in payments) for i in range(5)},
        'signed_without_public':sum(p['Signers']>0 and not p['Public'] for p in payments),
        'actual_obligations':len(obligations),'fulfilled':fulfilled,'repaired':repaired,
        'actual_obligation_fraction':len(obligations)/child_public if child_public else 0,
        'elapsed_s':elapsed,'whole_run_payment_tps':sum(p['Public'] for p in payments)/elapsed,
        'guaranteed_CAL_per_backing_CAL_per_s':sum(o['Amount'] for o in obligations if o['Status']==1)/calgrant/elapsed,
        'retry_payment_fraction':retries/len(attempts) if attempts else 0,
        'max_unready_payment_age_at_stop_s':max([(report['StoppedUnixNS']-u[k]['SentUnixNS'])/1e9 for u in units for k in ['Parent','Child'] if u[k]['SentUnixNS'] and not u[k]['ReadyUnixNS']],default=0),
        'max_ready_unclosed_payment_age_at_stop_s':max([(report['StoppedUnixNS']-u[k]['ReadyUnixNS'])/1e9 for u in units for k in ['Parent','Child'] if u[k]['ReadyUnixNS'] and not u[k]['MemberClosedUnixNS']],default=0),
        'resource_rejection_attempts':kinds,'transport_failures':reasons,
        'max_member_temporary_remaining':temporary,'max_member_net_spent':residual,
        'max_member_cumulative_released':released,
        'child_fast_p50_ms':percentile([u['Child']['FastMS'] for u in units if u['Child']['ReadyUnixNS']],.5),
        'child_fast_p95_ms':percentile([u['Child']['FastMS'] for u in units if u['Child']['ReadyUnixNS']],.95),
        'parent_actual_publication_delay_p50_s':percentile([u['FirstSubmitDelayNS']/1e9 for u in units if u['FirstSubmitUnixNS']],.5),
        'parent_actual_publication_delay_p95_s':percentile([u['FirstSubmitDelayNS']/1e9 for u in units if u['FirstSubmitUnixNS']],.95),
        'fee':last['committee0']['Detail']['Fee'],
    }
    curves=[]
    reads={'regular':[],'detailed':[]};sample_errors=0;oldest_max=0;previous_release={}
    timeout=read(path/'network.json')['Direct']['TimeoutSeconds']
    for line in lines(path/'samples.jsonl'):
        row=json.loads(line);s=row.get('snapshot')
        if not s:sample_errors+=1;continue
        reads['detailed' if s.get('Detail') else 'regular'].append(s['ReadMS'])
        t=(s['UnixNS']-start)/1e9
        oldest=s.get('Oldest',{})
        age=max(0,s['UnixNS']/1e9-oldest['Deadline']+timeout) if oldest.get('Deadline',0)>0 else None
        if age is not None:oldest_max=max(oldest_max,age)
        for r in s['Resources']:
            kind=r['Key']['Kind']
            if kind>3:continue
            slices=r.get('Slices',[])
            debit=next((d for d in s.get('Detail',{}).get('Debits',[]) if d['Key']==r['Key']),None)
            release_rate=None
            if debit:
                key=(row['node'],kind);previous=previous_release.get(key)
                if previous and t>previous[0]:release_rate=(debit['Released']-previous[1])/(t-previous[0])
                previous_release[key]=(t,debit['Released'])
            curves.append({'case':path.name,'node':row['node'],'seconds':t,'kind':kind,'grant':r['Grant'],
                'available':sum(a['Available'] for a in slices),'reserved':sum(a['Reserved'] for a in slices),
                'public_reserved':r['Usage']['Reserved'],'public_spent':r['Usage']['Spent'],
                'limited':s['Limited'][kind],'read_ms':s['ReadMS'],
                'actual_CAL_balance':s['CAL'] if row['node']=='committee0' else None,
                'actual_FUEL_balance':s['FUEL'] if row['node']=='committee0' else None,
                'released_total':debit['Released'] if debit else None,
                'released_per_s':release_rate,
                'net_spent':debit['NetSpent'] if debit else None,
                'temporary_reserved':debit['Charged']-debit['Released']-debit['NetSpent'] if debit else None,
                'oldest_output':bytes(oldest['Output']).hex() if oldest else '',
                'oldest_anchor_height':oldest.get('AnchorHeight'),
                'oldest_deadline':oldest.get('Deadline'),'oldest_anchor_age_s':age})
    summary['sampling']={'errors':sample_errors,**{k:{'count':len(v),'p50_ms':percentile(v,.5),'p95_ms':percentile(v,.95),'max_ms':max(v,default=0)} for k,v in reads.items()}}
    summary['max_observed_oldest_anchor_age_s']=oldest_max
    summary['final_actual_CAL_balance']=last['committee0']['CAL']
    summary['final_actual_FUEL_balance']=last['committee0']['FUEL']
    summary['closed_units']=sum(u['Parent']['MemberClosedUnixNS']>0 and u['Child']['MemberClosedUnixNS']>0 for u in units)
    summary['certificate_input_children']=sum(u['Child']['CertificateInput'] for u in units)
    summary['child_sent_before_parent_submit']=sum(0<u['Child']['SentUnixNS']<u['FirstSubmitUnixNS'] for u in units)
    summary['ready_child_fraction_of_offered']=summary['ready_children']/report['Offered']
    summary['occupancy']={}
    for kind in [1,2,3]:
        public=[r for r in curves if r['node']=='committee0' and r['kind']==kind]
        per_member={n:[r['reserved'] for r in curves if r['node']==n and r['kind']==kind and 0<=r['seconds']<=duration] for n in last if n.startswith('org0-member')}
        summary['occupancy'][str(kind)]={
            'public_reserved_mean_input_window':sampled_mean([(r['seconds'],r['public_reserved']) for r in public],duration),
            'public_reserved_max':max((r['public_reserved'] for r in public),default=0),
            'max_member_reserved_p95_input_window':max((percentile(v,.95) or 0 for v in per_member.values()),default=0),
            'max_member_reserved':max((max(v,default=0) for v in per_member.values()),default=0)}
    with (path/'analysis.json').open('w') as f:json.dump(summary,f,indent=2);f.write('\n')
    return summary,curves


def plot(summaries,curves,prefix):
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    from matplotlib import font_manager
    available={f.name for f in font_manager.fontManager.ttflist}
    font=next((f for f in ['PingFang SC','Microsoft YaHei','Noto Sans CJK SC','Heiti TC'] if f in available),'DejaVu Sans')
    plt.rcParams.update({'font.family':font,'font.size':10,'axes.spines.top':False,'axes.spines.right':False,'pdf.fonttype':42,'axes.unicode_minus':False})
    colors=['#2A9D8F','#E9A23B','#496CAD'];names=['低','中','高']
    fig,axes=plt.subplots(1,3,figsize=(12,3.2),layout='constrained')
    for ax,kind,title in zip(axes,[1,2,3],['CAL 签署权限占用','FUEL 权限占用（含已支出）','Execution 工作权限占用']):
        for level,name,color in zip(['low','medium','high'],names,colors):
            case=f'{prefix}k{kind}-{level}';groups={}
            for row in curves:
                if row['case']!=case or row['kind']!=kind or not row['node'].startswith('org0-member'):continue
                slot=round(row['seconds']*2)/2;share=(row['grant']*2)//3
                groups[slot]=max(groups.get(slot,0),row['reserved']/share)
            if groups:ax.plot(sorted(groups),[groups[t]*100 for t in sorted(groups)],label=name,lw=1,color=color)
        ax.axhline(100,color='#777',lw=.7,ls='--');ax.axvline(300,color='#999',lw=.7,ls=':')
        ax.set(title=title,xlabel='时间（秒）',ylabel='四成员中最高占用率（%）',ylim=(0,105),xlim=(0,365));ax.grid(alpha=.18);ax.legend(frameon=False)
    for ext in ['png','pdf']:fig.savefig(OUT/f'budget-turnover.{ext}',dpi=220)
    plt.close(fig)
    fig,axes=plt.subplots(1,2,figsize=(9,3.2),layout='constrained')
    for case,name,color in [(prefix+'k1-medium','父投递延迟 1 秒',colors[0]),(prefix+'k1-medium-delay3','父投递延迟 3 秒',colors[2])]:
        data=[r for r in curves if r['case']==case and r['node']=='committee0' and r['kind']==1]
        if data:axes[0].plot([r['seconds'] for r in data],[r['public_reserved'] for r in data],label=name,lw=1,color=color)
        data=[r for r in curves if r['case']==case and r['node']=='org0-member0' and r['kind']==1]
        if data:axes[1].plot([r['seconds'] for r in data],[r['available'] for r in data],label=name,lw=1,color=color)
    for ax,title in zip(axes,['委员会实际 CAL 责任占用','成员 0 可用 CAL 签署权限']):
        ax.set(title=title,xlabel='时间（秒）',ylabel='CAL');ax.legend(frameon=False);ax.grid(alpha=.18);ax.axvline(300,color='#999',lw=.7,ls=':')
    for ext in ['png','pdf']:fig.savefig(OUT/f'delay-contrast.{ext}',dpi=220)
    plt.close(fig)
    fig,axes=plt.subplots(1,3,figsize=(12,3.2),layout='constrained')
    for ax,kind,title in zip(axes,[1,2,3],['CAL 三档','FUEL 三档','Execution 三档']):
        ax.plot([0,300],[0,6000],color='#999',ls='--',lw=.8,label='计划单位')
        for level,name,color in zip(['low','medium','high'],names,colors):
            case=OUT/f'{prefix}k{kind}-{level}'
            if not (case/'analysis.json').exists():continue
            report=read(case/'reports/budget-v4.json');start=report['StartedUnixNS']
            times=sorted((max(u['Parent']['MemberClosedUnixNS'],u['Child']['MemberClosedUnixNS'])-start)/1e9 for u in report['Units'] if u['Parent']['MemberClosedUnixNS'] and u['Child']['MemberClosedUnixNS'])
            end=(report['StoppedUnixNS']-start)/1e9
            ax.step([0,*times,end],[0,*range(1,len(times)+1),len(times)],where='post',color=color,lw=1,label=name)
        ax.set(title=title,xlabel='时间（秒）',ylabel='累计闭环两跳单位',xlim=(0,365));ax.grid(alpha=.18);ax.legend(frameon=False);ax.axvline(300,color='#999',lw=.7,ls=':')
    for ext in ['png','pdf']:fig.savefig(OUT/f'completed-units.{ext}',dpi=220)
    plt.close(fig)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--prefix',default='r1-');p.add_argument('--plot',action='store_true');args=p.parse_args()
    summaries=[];curves=[]
    for path in sorted(OUT.glob(args.prefix+'*')):
        if not (path/'reports/budget-audit.json').exists() and not (path/'reports/budget-audit.json.gz').exists():continue
        s,c=analyze_case(path);summaries.append(s);curves+=c
    (OUT/(args.prefix+'analysis.json')).write_text(json.dumps(summaries,indent=2)+'\n')
    fields=['case','kind','grant','delay_s','offered_units','admitted_units','not_started','not_started_ratio','closed_units','public_payments','partial_approval_payments','actual_obligations','actual_obligation_fraction','fulfilled','repaired','retry_payment_fraction','child_fast_p50_ms','child_fast_p95_ms','max_unready_payment_age_at_stop_s','max_ready_unclosed_payment_age_at_stop_s','elapsed_s','CAL_per_backing_CAL_per_s_input_window']
    with (OUT/(args.prefix+'summary.csv')).open('w',newline='') as f:
        w=csv.DictWriter(f,fieldnames=fields);w.writeheader()
        for s in summaries:w.writerow({k:s[k] for k in fields})
    if curves:
        with (OUT/(args.prefix+'curves.csv')).open('w',newline='') as f:w=csv.DictWriter(f,fieldnames=list(curves[0]));w.writeheader();w.writerows(curves)
    if args.plot:plot(summaries,curves,args.prefix)
    for s in summaries:print(s['case'],s['public_payments'],s['partial_approval_payments'],round(s['actual_obligation_fraction'],3),round(s['whole_run_payment_tps'],2))
