# 最终版本连续支付：论文替换段落

适用位置：实验章节的连续支付小节。以下文字由 GPT 根据本轮结果起草，Codex 对照逐笔记录及审计核对。用于下一轮稿件整合；本次没有覆盖 Overleaf 或已发布 PDF。正式稿应以本轮作为当前版本长链主证据，旧内存与同步成员结果保留其版本归属，不混成一组。

## English

On final build `721800c`, three independent wallets constructed 100-hop chains online using nine services on one Mac, synchronous bbolt member commits, NoSync wallets, and user-paid FUEL. Both modes were timed from the first send to the final recipient’s certificate verification and local recording, including retries. With the committee commit setting at 250 ms, three runs per mode yielded median times of 4.557 s for certified continuation versus 64.476 s for inter-hop public-confirmation waits (14.15×; 92.93% reduction). All 297 fast successors used their parent’s TXCer and were sent before the parent’s earliest recorded application-commit completion across all four committee replicas. Nine missing-input obligations were fulfilled by source execution without compensation. All 600 payments passed replica-agreement, CAL/FUEL-conservation, fee-closure, and outbox-drain audits.

## 中文

在最终版本 `721800c` 上，三个独立钱包在线构造 100 跳支付链，采用单台 Mac 上的九个服务、成员同步 bbolt 提交、NoSync 钱包及用户自付 FUEL。两种模式均从首次发送计时至最后收款人完成证书验证和本地记录，重试时间包含在内。委员会 commit 参数为 250 ms，每种模式运行三轮：证书续花的整链耗时中位数为 4.557 秒，逐跳等待公共确认则为 64.476 秒，后者为前者的 14.15 倍，耗时缩短 92.93%。快速组全部 297 个后继均使用父交易的 TXCer，并在四个委员会副本中最早记录的父交易应用提交完成之前发送。实际形成的九项缺失输入义务均由源交易执行履行，无需赔付。全部 600 笔付款通过副本一致性、CAL／FUEL 守恒、费用关闭及 outbox 排空审计。

## 整合时的对应修改

1. `evaluation.tex` / `zh-evaluation.tex` 连续支付小节加入此当前版本结果；同步更新构建分类。不要把旧 158.846 ms 的内存图重新标为本轮结果。
2. 补充材料用本报告六轮表，并说明三轮统计层级、AB/BA/AB 次序、相同创世配置及钱包 NoSync。
3. 图若替换，使用本轮 `results/cases.csv` 的 `fast_chain_ms`；标明单位和同步成员存储，两个模式必须同一终点。
4. 方法区区分本轮内部应用提交时间戳与驱动公共观察时间；不把最后一笔公共观察视为全部祖先完成。
5. 混合故障负载与读者对照完成后统一编译、同步中英文和 Overleaf。第②项本身不承诺这些后续证据已完成。
