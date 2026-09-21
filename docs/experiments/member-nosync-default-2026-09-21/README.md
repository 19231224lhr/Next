# 成员默认关闭磁盘同步（2026-09-21）

按用户确认，将独立成员进程默认改用 `store.OpenNoSync`，不需要额外开关。复用同一bbolt实现，仅设置NoSync=true；普通Open仍同步，网关、钱包和委员会应用调用不变。启动日志显示storage_no_sync=true。

正常运行仍保留原子更新、隔离、消费检查、额度处理和提交完成后签名；成员库不保证崩溃一致性或持久性。每轮从新状态开始，发生故障结束该轮。NoSync仍写文件，不是内存数据库。

## 改动

- internal/store/bolt.go：共用openBolt，增加OpenNoSync。
- cmd/member/main.go：成员默认NoSync，日志标记模式。
- internal/store/store_test.go：复用存储契约，增加NoSync及Group-NoSync覆盖。
- 成员/Group注释和README：准确说明当前提交/持久语义。
- 没有调整压测器补赶、共识参数、并发名额或签名验证。

## 验证

测试先红后绿。store/member/gateway/blockfollow及成员命令检查通过；store/member race通过；相关go vet通过。具体命令与输出见targeted.log、race.log、vet.log。

当前真实成员源码构建，不带诊断探针或NoSync补丁overlay。为读取相同大创世，仅保留既有测试配置读取上限overlay。

14进程、新状态，目标500TPS、5000笔独立最终UTXO跨组织收款，快速并发256、后台未完成上限2048：

| 指标 | 结果 |
|---|---:|
| 实际发送速率 | 500.00 TPS |
| 首末发送区间 | 9.998 s |
| 快速到账/公共执行/成员完成 | 5000 / 5000 / 5000 |
| 失败 | 0 |
| 钱包快速到账P50 / P95 | 43.83 / 104.63 ms |
| 全部观察完成 | 10.912 s |
| 发送滞后P95 | 3.78 ms |
| 未完成观察峰值 | 1747 |
| 总未完成上限触发 | 0 |

八个成员日志均确认NoSync。四委员状态一致，5000笔执行均成功，资金/原始占用审计通过，所有outbox排空。测试节点已停止。

这是约10秒输入窗口的功能及性能回归，不作为长期稳定500TPS结论。原始数据在reports/bench-v4-0.json、reports/audit.json和blocks.json；本次增量在change.patch，保留原有未提交工作。
