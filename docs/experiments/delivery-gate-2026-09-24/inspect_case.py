import collections,json,sys
from pathlib import Path
p=Path(sys.argv[1]);r=json.loads((p/'reports/fault-v4.json').read_text());s=r['Samples']
print('counts',dict(total=len(s),sent=sum(x['SentNS']>0 for x in s),ready=sum(x['ReadyNS']>0 for x in s),public=sum(x['PublicNS']>0 for x in s),members=sum(all(x['MemberNS']) for x in s),errors=collections.Counter(x.get('Error','') for x in s)))
print('first/last sent',(min(x['SentNS'] for x in s if x['SentNS'])-r['StartedNS'])/1e9,(max(x['SentNS'] for x in s)-r['StartedNS'])/1e9)
for sec in range(0,30,5):
 a=[x for x in s if x['SentNS'] and sec<=(x['SentNS']-r['StartedNS'])/1e9<sec+5]
 fast=sorted(x['FastMS'] for x in a if x['ReadyNS'])
 lag=sorted((x['SentNS']-x['ScheduledNS'])/1e6 for x in a)
 print('window',sec,'sent',len(a),'ready p50/p95', [fast[int((len(fast)-1)*q)] for q in [.5,.95]] if fast else [],'lag p95',lag[int((len(lag)-1)*.95)] if lag else None)
if (p/'gate.json').exists():
 g=json.loads((p/'gate.json').read_text());print('gate reasons',collections.Counter(x['Reason'] for x in g));print('missing release',sum(x['ReleaseNS']==0 for x in g))
 hold=sorted((x['ReleaseNS']-x['QCNS'])/1e6 for x in g if x['ReleaseNS'] and x['QCNS'])
 print('proxy hold p50/p95/p99', [hold[int((len(hold)-1)*q)] for q in [.5,.95,.99]])
 print('latest slow', sorted([{'hold':(x['ReleaseNS']-x['QCNS'])/1e6,'reason':x['Reason'],'copies':x['Stored'],'install':x['InstallCalls'],'times':[x['QCNS'],x['Install3NS'],x['PublicNS'],x['ReleaseNS']]} for x in g if x['ReleaseNS'] and x['QCNS']],key=lambda x:x['hold'])[-5:])
