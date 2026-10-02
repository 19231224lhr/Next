# 第③项混合负载：论文段落与整合位置

基于最终节点构建 `721800c` 的十二轮数据。侧边栏 GPT 提供英文初稿，Codex 核对实际事件、统计层级与二进制记录；以下不是对全文或完整实现的独立安全审计。

## English

**Economic closure under mixed traffic.** Economic closure proceeded during ordinary traffic with all adaptation endpoints paused. Twelve N/R/C/P runs on unchanged node build `721800c` used 14 colocated services, synchronous members, NoSync ordinary wallets, and authorized organization-paid FUEL. Each combined 7,000 ordinary payments targeting 100 payments/s for 70 s with eight cross-organization parent–child pairs. R automatically fulfilled all 24 withheld sources without compensation; C/P reserve debits and repayments each totaled 4,800 CAL. In P, all four replicas confirmed repayment of all eight targets per run before adaptation resumed, with no target representation committed or materialized; representations subsequently committed and materialized during ordinary traffic. All 84,192 payments passed closure and conservation audits. Some runs reached the 256-outstanding cap; two P runs also reached the 64-fast-request cap. The largest run-level dispatch-lag P95 was 895.879 ms. Background drain was observed 0.815–1.356 s after the last ordinary send.

## 中文对应

**混合流量下的经济闭合。** 在全部适配端点暂停时，经济闭合仍在普通付款流期间完成。十二轮 N/R/C/P 实验使用未改动的 `721800c` 节点构建，部署 14 个同机服务，采用成员同步提交、普通钱包 NoSync 及明确授权的组织代付 FUEL。每轮将计划以 100 笔/秒发送 70 秒的 7,000 笔普通付款，与八对跨组织父子付款混合运行。R 组全部 24 个扣留来源自动执行并履行义务，无需赔付；C/P 组备付扣款与回款分别合计 4,800 CAL。P 组每轮四副本均在恢复适配前确认八项目标全部回款，且目标表示尚未提交或物化；恢复后，表示提交和物化仍在普通付款流期间完成。全部 84,192 笔付款通过闭合与守恒审计。部分轮次触及 256 笔总未完成上限，P 组两轮还触及 64 笔快速请求并发上限；逐轮发送滞后 P95 的最大值为 895.879 ms。末笔普通付款发送后 0.815–1.356 s，观察到后台任务全部排空。

## 整合说明

- 放入实验章的来源恢复与独立赔付小节，与第②项同版本连续支付证据并列。方法部分定义 N/R/C/P、同步成员、两类钱包的存储差异、固定资金及明确授权的组织代付。
- 主表选各组三轮中位的普通到账 P50/P95、实际发送跨度和排空时间；逐轮 P99、发送滞后、上限计数、资源及留存快照进入补充材料。四组普通到账 P50 的中位数范围为 53.03–55.13 ms；不把 N/R/C/P 的微小差异当成统计上已证实的加速。
- [mixed-load.pdf](mixed-load.pdf) 为可复用矢量图。左图只画前 70 秒七个窗口；发送拖到 70 秒后的部分尾段留在 CSV 和报告中。右图展示第 1 轮 P 的独立观察事件。观察时间不写成内部 Commit 延迟。
- 将原本孤立功能轮的结果保留为历史证据，本轮负责“同一最终节点版本、普通流覆盖赔付／回款／适配恢复”的主张；旧内存模式 TPS 和本轮同步成员延迟不可直接对比。
- 本轮费用核对发现旧 C3 报告将 `demo-v4/bench-v4` 误写为用户付 FUEL，已按当前及旧基线实际代码更正为显式组织代付。该修订不改变旧实验原始数字。
- 当前交付为报告、数据、图和可用段落。TeX、PDF、Overleaf 尚未同步这些新增实验数据，后续统一整合时应同时更新中英文版本和证据索引。读者微基准属于下一项，不在这里声称完成。

## 联合审阅结论

GPT 根据提供的结果及复核说明，同意本项补足代表性组合负载证据，可结项。Codex 认同其关键区分：**经济闭合与适配可用性解耦，不等于二者性能完全隔离。** 正常组也出现瞬态背压，不能将全部背压归因于修复；赔付组额外费用和有限流的范围保留在结果中。

[方案评审原文](gpt-method-review.txt) · [结果评审原文](gpt-result-review.txt) · [实测报告](README.md)
