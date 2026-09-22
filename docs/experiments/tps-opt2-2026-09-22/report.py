
from pathlib import Path
import json,csv
D=Path(__file__).parent
labels=['base','gmem','gmem1000','cursor','hold800','hold1000','profile','slots8','slots4b','confirm1000','final1100','final1100b','limit1100b','fg256','fg256b']
rows=[];windows=[]
for name in labels:
 p=D/name/'summary.json'
 if not p.exists():continue
 o=json.loads(p.read_text());s=o['summary'];m=json.loads((D/name/'metadata.json').read_text())
 row=dict(run=name,member_foreground=m['arguments'].get('member_foreground',128),gateway_memory=m['arguments'].get('gateway_memory',False),trace=not m['arguments'].get('no_trace',False),public_slots=m['arguments'].get('submit_slots',4),count=s['count'],target_tps=m['arguments']['rate'],actual_send_tps=o['actual_send_tps'],successful_closure_tps=(s['count']-s['failed'])/s['elapsed_s'],elapsed_s=s['elapsed_s'],fast_p50_ms=s['fast_p50_ms'],fast_p95_ms=s['fast_p95_ms'],wallet_final_p50_ms=s['block_observed_p50_ms'],members_done_p50_ms=s['member_applied_p50_ms'],failed=s['failed'],pending_limit_hits=s['total_limit_hits'],peak_unfinished=s.get('sampled_peak_unfinished'),oldest_unfinished_ms=s.get('sampled_oldest_unfinished_max_ms'),committee_equal=o['committee_equal'],outbox_pending=o['outbox_pending'],successful_executions=o['successful_executions'],failed_executions=o['failed_executions'],export_s=m.get('shutdown_and_audit_export_s'))
 rows.append(row)
 for w in o['windows']:windows.append(dict(run=name,end_s=w['end_s'],sent_tps=w['sent_tps'],observed_public_tps=w['observed_commit_tps'],unfinished=w['active'],fast_p50_ms=(w.get('fast_ms') or {}).get('p50'),fast_p95_ms=(w.get('fast_ms') or {}).get('p95')))
for filename,data in [('runs.csv',rows),('windows.csv',windows)]:
 with (D/filename).open('w',newline='') as f:
  w=csv.DictWriter(f,fieldnames=list(data[0]),lineterminator=chr(10));w.writeheader();w.writerows(data)
(D/'comparison.json').write_text(json.dumps(rows,indent=2))
lines=['# 整体快速转账 TPS 优化（2026-09-22）','',
'实验存储条件：成员及网关使用显式内存模式，钱包bbolt NoSync，委员会应用仍同步bbolt，Comet区块/状态使用已有MemDB配置，共识WAL和验证规则保留。', '',
'本轮以 3d091b0 为基线，面向 Mac Studio 上从新创世启动的实验模式。付款均为独立已最终 UTXO、跨组织收款，14 个本机进程；不是 WAN、连续父子续花或故障恢复测试。','',
'## 保留的改动','',
'- 网关增加 UTXO_EXPERIMENT_GATEWAY_MEMORY=1，复用已有有序 B-tree 原子存储。运行时不写网关数据库，退出导出仅供审计的文件；默认仍为同步 bbolt。成员仍使用已有内存模式，钱包仍为 bbolt NoSync。没有取消验签、输入消费检查、额度事务。',
'- 公共 Submit 同时处理数量从 4 改为 8，INSTALL 保持 4 个目标 RPC；完成通知缓冲由两者上限相加得到。保留去重、重试、公共终态重查与有限资源边界。',
'- Submit 槽满时不再越过未获得调度机会的记录。确定性测试证明扫描接续得到修正；短测没有证明这项修正本身提高 TPS。',
'- 成员前台128入口在两轮1100目标下产生quorum失败。诊断证明入口拒绝而非业务额度不足；单变量验证前台256、后台仍32，结果按表列出。',
'- 委员会应用数据库、CometBFT 共识参数、交易池、快速请求 256 名额和后台未完成 2048 上限均未调整。','',
'## 测量结果','',
'实际发送 TPS 按第一笔到最后一笔真正发出的时间计算；闭环 TPS 按全部付款完成快速到账、钱包跟块及成员完成观察的总时长计算。最终审计在计时结束后执行。','',
'|运行|笔数 / 目标TPS|实际发送TPS|成功闭环TPS|总秒数|快速P50 / P95 ms|后台上限触发|失败|',
'|---|---:|---:|---:|---:|---:|---:|---:|']
for x in rows:
 lines.append(f"|{x['run']}|{x['count']} / {x['target_tps']}|{x['actual_send_tps']:.2f}|{x['successful_closure_tps']:.2f}|{x['elapsed_s']:.3f}|{x['fast_p50_ms']:.3f} / {x['fast_p95_ms']:.3f}|{x['pending_limit_hits']}|{x['failed']}|")
lines+=['',
'base→gmem 为同一 48k 创世、目标800的单变量网关存储对照。gmem1000→cursor 用于确认扫描修正，没有明确性能收益。slots8→slots4b 是同一180k创世、90k付款、目标1200的并发对照。profile 含10秒网关CPU采样，仅用于定位。confirm1000 关闭诊断，不把它与开启诊断的轮次视为纯并发单变量比较。','',
'## 证据与取舍','',
'基线网关数据库读取 P50 仅0.013ms；保存耗时 P50/P95 26.47/243.65ms，凭证就绪到首投118/3332ms。网关内存模式对应降至保存0.224/1.216ms、首投0.91/125.58ms。不能将全部收益归给单次 fsync 或读锁。','',
'1000目标下，四槽 scheduled→HTTP返回平均3.58ms，尚未计入完成通知被处理的时间；1000次/秒需要约3.58个槽，余量有限。10秒CPU样本没有支持“JSON/扫描占主导”，因此没有实施 INSTALL 扫描性能重构。','',
'1200目标下，8槽相较4槽，首投P50从479.61降到115.64ms，P95从1268.75降到432.71ms；实际发送与完整闭环同时改善。8槽实际承载更高，快速P95也上升，不能宣传所有延迟同时改善。两轮均未跟满1200目标。','',
'发送器最多补回3个发送间隔的配额，长暂停会使计划速率与实发速率产生小差距；本轮没有修改发送器。total_limit_hits/send_limit_hits 是每任务首次取额度受阻的布尔标志之和；send 名额在节拍等待之前取得，不能将该计数解释为同等数量已到网关的慢请求。采样峰值仅统计真正发出且未完成的交易，不含尚在等待发送的任务。','','## 验证与复现','',
'干净源码目录完成 go test -tags=comet_v3 ./...、go vet -tags=comet_v3 ./...，并对 store/gateway/cmd-gateway 完成 -race。回归测试分别先捕获扫描跳过问题、原版仅启动4个公共槽，再验证修复和12个在途动作的退出。见 verification-final.log；包含最终成员容量配置及 transport 的竞态检查。','',
'在仓库根目录运行（实验从零开始，目录名必须新建）：','',
'~~~sh','export PATH=/usr/local/go/bin:$PATH',
'python3 docs/experiments/tps-opt2-2026-09-22/reproduce.py my1100 --count 198000 --rate 1100 --member-memory --wallet-no-sync --gateway-memory --no-trace',
'~~~','',
'诊断对照去掉 --no-trace，使用 --submit-slots 4 / 8、--member-foreground 128 / 256。该选择仅是实验构建覆盖；正式默认公共投递并发为8。脚本从当前源码构建所有角色，并在计时前准备有限资金与请求。198k的计数/配置读取上限、采样、退出导出等待只存在于实验覆盖文件。','',
'原始逐笔记录、节点诊断、四委员审计与二进制哈希保存在本机实验目录；本报告、汇总CSV和源码补丁用于分享。已结束运行的临时数据库按 cleanup.json 清理，测量文件保留。此模式没有崩溃恢复承诺，数据集也不证明无限运行或其他网络环境的容量。','']

if (D/'fg256b/summary.json').exists():
 final=json.loads((D/'fg256b/summary.json').read_text()); fs=final['summary']
 conclusion=['## 最终确认','', '代码提交：a459c2347e3b058734b02515f50d4fd74eb90a91。', '',f"最终生产代码的两轮198000笔确认：阶段诊断轮1051.43成功闭环TPS；无阶段诊断轮{(fs['count']-fs['failed'])/fs['elapsed_s']:.2f}成功闭环TPS、实发{final['actual_send_tps']:.2f}TPS，失败{fs['failed']}笔。无诊断轮快速到账P50/P95为{fs['fast_p50_ms']:.3f}/{fs['fast_p95_ms']:.3f}ms。",'',
 '每轮计划发送180秒，共198000笔；含收尾的实际闭环187～188秒。两轮均完成全部198000笔快速到账、公共成功执行和成员完成观察，四委员最终哈希一致、outbox为0、退出后审计返回0。无阶段诊断轮仍有52次单成员入口拒绝，均未造成整笔付款失败。', '',
 '前台128→256的同负载诊断对照：成员入口拒绝2633→40，失去三票的付款3→0；快速P95 44.493→44.557ms。容量调整减少的是短时受理拒绝，没有提高每次业务执行速度，也没有消除所有内部拒绝。','',
 '两轮均以目标1100发送，未跟满1100；约千笔以上是实际完成率。2048后台上限仍可能短时触发，所以保留发送滞后、待办和最老年龄，不能称为完全没有排队。三分钟内队列波动，最终排空；不据此承诺任意负载或无限时长。','',
 '|无诊断轮窗口末秒数|实际发送TPS|公共成功观察TPS|采样未完成|','|---:|---:|---:|---:|']
 for w in final['windows']:
  if w['end_s']>180:continue
  conclusion.append(f"|{w['end_s']:.0f}|{w['sent_tps']:.2f}|{w['observed_commit_tps']:.2f}|{w['active']}|")
 conclusion+=['','公共成功观察通过轮询区块统计，窗口可能跨过块提交边界。表中仅列完整30秒窗口，余下尾段另计入总闭环时间。快速到账从付款钱包开始提交HTTP请求到收款钱包验签并完成本地事务；无耐久刷盘承诺。分阶段对照见 STAGES.md，完整阶段数据见 stages.csv，全部窗口见 windows.csv。','']
 lines=lines[:2]+conclusion+lines[2:]
(D/'RESULTS.md').write_text(chr(10).join(lines))
print(D/'RESULTS.md')
