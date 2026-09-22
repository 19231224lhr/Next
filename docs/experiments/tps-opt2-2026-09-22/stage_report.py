from pathlib import Path
import json,csv
D=Path(__file__).parent
rows=[]
for e in sorted(D.iterdir()):
 if not e.is_dir():continue
 for filename,groups in [("summary.json",["all_stages_ms","gateway_ms","selected_member_ms","critical_third_vote_ms","selected_http_ms"]),("backend-summary.json",["stages_ms","committee0_operations_ms"]),("gateway-summary.json",[])]:
  p=e/filename
  if not p.exists():continue
  o=json.loads(p.read_text())
  def walk(value,path):
   if not isinstance(value,dict):return
   if "p50" in value and "n" in value:
    rows.append(dict(run=e.name,source=filename,stage=path,**{k:value.get(k) for k in ["n","mean","p50","p95","p99","max","negative"]}));return
   for k,v in value.items():walk(v,path+"/"+k)
  if groups:
   for g in groups:walk(o.get(g,{}),g)
  else:walk(o,"")
with (D/"stages.csv").open("w",newline="") as f:
 w=csv.DictWriter(f,fieldnames=list(rows[0]),lineterminator=chr(10));w.writeheader();w.writerows(rows)
print("stage rows",len(rows))

labels=['base','gmem','hold800','fg256']
choices=[('钱包发起→网关进入','summary.json','gateway_ms/send_to_gateway'),('网关解码及授权→分发准备','summary.json','gateway_ms/gateway_pre_fanout'),('成员分发→收齐三票','summary.json','gateway_ms/fanout_to_quorum'),('关键第三票：分发→成员进入','summary.json','critical_third_vote_ms/dispatch_to_handler'),('关键第三票：业务校验','summary.json','critical_third_vote_ms/member_validation'),('关键第三票：等待本地更新','summary.json','critical_third_vote_ms/member_update_queue'),('关键第三票：状态修改','summary.json','critical_third_vote_ms/member_update_callback'),('关键第三票：签名','summary.json','critical_third_vote_ms/member_sign'),('钱包验签及本地保存','summary.json','all_stages_ms/wallet_verify_save'),('凭证准备→首次公共投递','gateway-summary.json','/stages_ms/ready_to_submit'),('网关后台保存','gateway-summary.json','/stages_ms/save_total'),('公共投递HTTP往返','gateway-summary.json','/stages_ms/public_http'),('接纳→准备提案','backend-summary.json','stages_ms/accepted_to_prepare_enter'),('准备提案→共识决定','backend-summary.json','stages_ms/prepare_done_to_commit_decision'),('整块Finalize','backend-summary.json','stages_ms/app_finalize'),('整块应用Commit','backend-summary.json','stages_ms/app_commit'),('Commit→钱包观察','backend-summary.json','stages_ms/app_commit_to_wallet_observed')]
lookup={(x['run'],x['source'],x['stage']):x for x in rows}
lines=['# 阶段耗时对照','','所有数字为P50 / P95，单位ms。前两列是相同48k/800目标的网关存储单变量对照；后两列是较长独立验证，负载与数据规模不同，不能直接作单变量因果比较。','','|阶段|基线48k@800|网关内存48k@800|144k@800|最终198k@1100|','|---|---:|---:|---:|---:|']
for title,source,stage in choices:
 vals=[]
 for label in labels:
  v=lookup.get((label,source,stage));vals.append(f"{v['p50']:.3f} / {v['p95']:.3f}" if v else '—')
 lines.append('|'+title+'|'+'|'.join(vals)+'|')
lines+=['','前台/后台并行，部分区间互相包含；分位数不能逐行相加。关键第三票取构成该次法定人数的最后一名成员。整块Finalize/Commit是全区块耗时，不是单笔CPU时间。样本数及覆盖率见各轮JSON与stages.csv。最终无阶段埋点确认只核对整体吞吐、到账分布及审计。','']
(D/'STAGES.md').write_text(chr(10).join(lines))
