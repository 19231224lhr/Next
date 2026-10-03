# C3：用途与消费者需求补强

本轮只处理第三项贡献的用途与对应证据，接续 `service-contract-2026-10-03`。不改 Intent、赔付协议或生产支付路径，不扩大吞吐实验。

## 审查问题

1. 经济查询为什么使用决定索引就足够？
2. 对需要区块正文的归档复核，保存原身份的指针与修订正文相容于原完整 BlockID 有什么区别？
3. 原委员会提交证据在哪一步被实际使用？该检查与后来执行的修订授权如何分开？
4. 哪些是已有实现和功能测试，哪些仍是可选部署场景？

## 材料与协作

双方收到相同的[原始材料与问题](joint-request.txt)：历史读者契约、真实导出测试、当前引言和完整协议章节。

- Claude：[当前对话](https://claudez.yuehanxai.com/chat/89c58d30-5daa-46ec-b8b6-36fd428dd3a3)，Opus 5.5 High，Pro；进入时会话使用 34%、周使用 19%。
- GPT：[当前对话](https://chatgpt.com/c/6ac11dfa-b5fc-83ec-9795-20d77d10a0e1)，页面模型 Pro。

代码核对确认：Comet 的 `CanonicalizeBlockID` 将 Hash 和 PartSetHeader 一同纳入投票签名；`verifyBasicValsAndCommit` 比较完整身份。原提交证据不授权后来的修订，成功执行的决定与精确 Task 才提供该授权。

## 写作结论

**C3 为本地区块归档复核提供一种保持原提交身份的授权正文表示。** 复核者在赔付后读取指定输入内的历史备付扣减引用，并让这份修订正文通过其已经保存的原始 commit 检查。检查同时覆盖区块 Hash 与 PartSetHeader；原 commit 检查身份相容性，后来成功执行的决定与表示命令及其精确 Task 授权新字节。

普通索引和物化资金视图可以提供相同经济答案、保留原身份指针、支持审计并保持后继引用。C3 增加的是“含备付引用的正文自身与原承诺相容”，不是更强的经济最终性，也不是免去永久决定、义务或修订授权记录。接受外部附注的消费者无需这一能力。

本轮写出了具体本地使用方式、可运行参考消费者和功能区别，没有把这些推导成已有客户的需求调查，也没有声称兼容未经修改的 CometBFT。停止后续表示服务不会停止经济闭合；当前构建仍保留 CH 交易与 part 编码。

## 双方意见如何处理

- [Claude 第一轮](claude-round1.txt)、[第二轮](claude-round2.txt)、[交叉复核](claude-round3.txt)：采用区块归档复核场景；纠正初稿中“无需关联记录”的暗示，保留永久决定与状态关联；不称普通视图成本更低，因为没有对应测量。
- [GPT 独立意见](gpt-round1.txt)：采用 A/M/B 三者同一经济答案的公平对照；保持原始读测的 A/B 标签；不以人为要求同 ID 的正文来证明现实部署需求。
- [GPT 最终复核](gpt-round2.txt)：按测试报告判断，综合稿没有实质能力越界或需求循环；与 Claude 一致要求修正 Task 主语及 requirement 用词，均已采纳。外部 AI 未独立运行代码。
- Codex 核对源码、实现功能对照、检查两种身份组成与实际提交验证；将“Task 被执行”的草稿说法纠正为“决定和表示命令成功执行，记录精确 Task”。

## 功能对照与验证

复用 `TestHistoricalExportConsumer` 的真实签名与 ABCI fixture，未改生产代码。

| 检查 | 结果 |
| --- | --- |
| A 原始正文＋决定索引、M 普通缓存资金行、B Canonical 在 H=4 的经济答案 | 相等 |
| 真实偿还后 H=5 的三者答案 | 相等，Repaired → Recovered |
| M 的不可变行、B 的授权正文、历史决定和备付引用 | 偿还前后保持不变 |
| 原始正文和授权正文对同一保存的原 commit 验证 | 均通过 |
| 同一修改正文配初始 part openings | Hash 相同，PartSetHeader 不同，因 wrong block ID 被拒绝 |

M 缓存由 A 推导的不可变资金与决定字段，第二次读取复用同一缓存字节，当前义务状态从 `V_H` 获取。它不是生产查询后端；本轮没有对 M 计时。原有 21,600 次 A/B 读取和成本结果保留原构建、原口径。

验证命令：

```powershell
go test -tags comet_v3 ./internal/redaction -run '^Test(HistoricalExportConsumer|HistoricalReaderEquivalence|RealBlockStoreRewriteAndOriginalReplay|PaymentRepairMonetaryReplay)$' -count=1 -v
```

四组及其子用例全部通过：[回归日志](regression.log) · [真实导出](history-consumer.json) · [精简断言结果](functional-summary.json)。原始提交来自真实 BlockStore 的 `LoadBlockCommit(1)`，不是根据待验证正文重造的签名。

## 论文修改位置

- 引言 C3：用途、正文能力、授权依据与经济闭合分工。
- 协议 IV-H：归档复核步骤、完整身份的作用和普通索引替代方式。
- 实验 VI-C：三路径功能表，与原 A/B 性能测量分开。
- 补充材料 S3：真实 commit、未适配分片反例、缓存行与偿还前后的具体断言。
- 摘要、结论、中英文对应稿以及设计读者契约同步。

本轮基于 `f856c7b3d6265f7275a15767bdced52d40dafada` 的工作树，包含此前尚未提交的服务契约修订。功能补丁仅触及两个测试文件，不构成新的支付性能版本。

## 阅读与交付

- [英文正文](Next-C3-English.pdf) · [中文阅读版](Next-C3-Chinese.pdf)
- [英文补充材料](Next-C3-supplement-English.pdf) · [中文补充材料](Next-C3-supplement-Chinese.pdf)
- [完整 LaTeX 源码包](Next-C3-usecase-LaTeX.zip)
- [设计读者契约](../../../design/historical-reader-contract.md)

四根本地与 Overleaf 编译通过：英文正文 19 页、中文正文 21 页、英文补充材料 21 页、中文补充材料 22 页。在线四根均为 Errors 0、Warnings 0，保留少量 Underfull 排版提示；新增段落与表格已目检。下载的 56 个在线源码／图表与本地一致（文本仅归一化换行）。详见 [交付记录](delivery.json) 和 [源码一致性检查](overleaf-source-check.json)。Overleaf 已恢复英文主稿作为默认编译入口。

[在线英文正文存档](Next-C3-overleaf-English.pdf) · [在线中文正文存档](Next-C3-overleaf-Chinese.pdf) · [在线英文补充材料](Next-C3-overleaf-supplement-English.pdf) · [在线中文补充材料](Next-C3-overleaf-supplement-Chinese.pdf)。本轮未提交或推送 Git。
