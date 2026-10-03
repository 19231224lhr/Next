# 最新稿独立复审与交叉复核

本目录记录 2026-10-03 在线编译稿的独立评审。两位评审收到相同的四份材料与中立提示，未预先收到旧评审、关闭表或作者期望结论。它们均未读取项目代码、原始实验归档或复跑实验。AI 评审属于辅助意见，不是期刊决定或安全认证。

## 输入与原文

- `materials.json`：输入文件指纹。
- `submission.json`：实际提交文件、提示、模型和对话链接。
- `claude-review.md`：Claude Opus 5.5 High 独立初评。
- `claude-clarification-prompt.md` / `claude-clarification.md`：对照材料后的针对性复核。初评被撤回或收紧的判断不能脱离复核引用。
- `gpt-review.md`：GPT 独立初评，正文取自可见页面。
- `cross-review-to-claude.txt` / `cross-review-to-gpt.txt`：完整交叉意见及要求。
- `claude-cross-review.md` / `gpt-cross-review.md`：双方交叉复核后的最终答复。
- [综合评定](review-synthesis.md)：共同判断、仍有差别之处与建议完成标准。

双方独立初评均建议大修。Claude 的复核已撤回“近乎零成本”、未经测量的“35 秒 / 7–8 倍”估算，以及将同一 BlockID 的授权多表示视为本地读者契约内漏洞的表述。GPT 初评本身区分了安全反例、可用性边界与研究价值问题。

## Codex 已核对的材料事实

1. `supplement-revision.tex` 明确记载 A 承担 CAL 损失，而组织付费轨迹由 B 的授权账户支付 480 FUEL。赞助方的 Subject 授权不能直接视为所有 CAL 风险承担者的统一准入；该问题已提交双方交叉复核。
2. 同一文件及 `evaluation.tex` 将最早副本应用提交到驱动观察的区间列为后继头部、获取、验证、本地记录和观察的联合区间。不能全部归因轮询，也不能直接相减构造未经实现的安全等待基线。5/25 ms 配置周期本身也不能在调度拥塞下证明 30 ms 的严格上界。
3. `evaluation.tex` 明确区分 721800c 的主要性能、bf71c4d 的窄回收和 9879f64 的边界/网络验证；`supplement-archived.tex` 已以 Results from Earlier Builds 标识历史结果。可读性与最终版本性能对应仍可补强，但没有据此发现历史数据被改标。
4. `supplement-archived.tex` 确有旧章节引用及 “the supplement lists/plots” 残留；这些编辑问题值得修正。
5. 补偿决定使用 `\mathcal D_x`，缺口使用 `D_x`，源码符号不同。可讨论视觉辨识和下标重用，但不能直接把二者判成完全相同的数学符号。
6. 作者单位 University 是用户此前指定的占位；正式投稿需用户提供真实单位，不能自行编造。

## 范围

本轮收集与比较评审，没有修改生产代码、论文正文或实验数据，没有启动新实验。侧边栏操作规则已增加主会话可见页面同步要求。

交叉复核已完成。两边仍建议大修，但均不要求强制重新设计 Intent、扩展远端认证或重跑全部实验。综合评定是建议，不是已经实施的修订或已获接收的承诺。
