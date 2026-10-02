# 第④项：论文段落与图表整合

依据本目录三进程、十二夹具数据；GPT 起草，Codex 核对测量时点、成本边界后修订。当前交付为正文候选与证据，尚未改写 TeX、PDF 或 Overleaf。

## English

**Authorized historical reads.** We compared an optimized decision-index reader with Canonical historical reads under the same executed prefix and economic query contract. Three independent processes performed 21,600 interleaved local queries over bbolt and BlockStore, using warm storage pages without decoded-block or result caches. Functional checks confirmed equivalent economic answers before representation, after materialization, and after source repayment. Timed queries used the represented and materialized snapshot: across blocks containing 32 or 256 payments and one or eight compensated inputs, Canonical reduced compensated-slot point-query P50 by 7.44–12.32%; whole-block differences were 1.49–3.88%. Most savings came from block reconstruction and decoding. Representation added 0.28–2.19 MiB of net logical records per repaired block and 43–214 ms of one-time local sequential maintenance, excluding network and consensus waits. These measurements quantify the cost of providing an authorized funding representation at stable historical coordinates, alongside the decision index that records economic closure.

## 中文对应

**授权历史读取。** 我们在相同已执行前缀和经济查询契约下，对比优化决定索引读取与 Canonical 历史读取。三个独立进程基于 bbolt 和 BlockStore 执行 21,600 次交错本地查询，采用热存储页，不使用已解码块或结果缓存。功能检查确认，在表示前、物化后及来源回款后，两种路径均返回相同经济解释。性能计时固定在已表示且已物化的快照：对包含 32 或 256 笔付款、其中一或八项输入已赔付的区块，Canonical 的已赔槽点读 P50 低 7.44%–12.32%，整块读取差异为 1.49%–3.88%，主要来自块重建与解码。相应代价是每个修复块增加 0.28–2.19 MiB 净逻辑记录，以及 43–214 ms 的一次性本地顺序维护工作，不含网络及共识等待。这些结果量化了在稳定历史坐标下提供已授权资金表示的成本；经济闭合事实则由决定索引记录。

## 图表与正文位置

- 放入实验章的“授权历史表示成本”小节，与②连续支付、③混合来源闭合分别承担论证职责。②说明可续花，③说明经济处理与适配可用性分离，④说明取得规范历史表示的读取收益及维护代价。
- [reader-cost.pdf](reader-cost.pdf) 是四面板矢量图：点读、整块读取、主要新增记录、一次维护。图 a/b 误差线为三个运行 P50 的最小—最大；c/d 为三轮均值，维护阶段使用均值堆叠，不能写成总耗时中位数。
- 方法部分必须保留本地已执行前缀读者、原坐标、同等授权、热页无对象缓存、同步 bbolt、真实 GoLevelDB、三次进程重复。主测依赖成功的公共执行，不逐次重做全部密码学；额外重验分项放补充材料。
- 正文不能称历史改写是赔付成立的必要条件，也不能把本地约 0.01–0.12 ms 的点读差别当成快速付款端到端加速。收益归因于当前布局，完整 BlockID 保持来自授权修复机制的功能检查。
- 对照没有删除原始执行历史；净逻辑字节包含表示相关应用记录与 BlockStore 增量、扣除已删 Pending，应用提交元数据另列。不是全系统物理空间对照或裁剪后存储上界。

## 联合判断

GPT 认为本项可以结项，无需为现有局部布局主张扩大缓存实验。Codex 同意：当前 Read／Resolve 分段已说明主要成本来源，下一步应统一整合论文，而不是继续增设机制。若未来主张普遍历史查询性能优势，再增加同等缓存条件下的敏感性评价。

[完整报告](README.md) · [方法评审](gpt-method-review.txt) · [结果评审](gpt-result-review.txt)
