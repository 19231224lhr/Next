# 论文文字修订记录

2026-10-02 · 英文主稿、中文阅读版及补充材料

本轮目标是让稿件以具体的协议动作、可核查的性质和实验结果展开，而不是以研发过程或抽象评价展开。没有改变生产代码、重新解释实验数字或添加未验证的机制。

## 参考与联合校读

对照既有精读材料中的 Lutris、Zef、Blitz、Mysticeti，以及 TDSC 的 SDR、CryptoMaze、Web3 跨链系统、Escaping 等论文，复看开篇的问题表达、机制段的主体与动作、实验段的组织方式。重点是学习“问题如何落到机制，结果如何支持主张”，没有复制样本句子。

Claude 分别修订核心机制与证明、摘要引言结论、实验与相关工作，并给出中文对照。Codex 按状态转换、版本谱系、原始表格及证明结构核验后采用。最后把实际编译的英文 PDF 和补充材料交给 Claude 校读，检查纸面上的术语、指代、长句与图表名称。

## 代表性修改

以下旧句来自本轮中间稿；新句见同目录最终 TeX。这里记录修改理由，不把一般词汇当作“AI 检测规则”。

| 位置 | 中间稿表达 | 最终处理 | 原因 |
| --- | --- | --- | --- |
| 引言的问题定位 | “the funding of such payments” | 直接写已认证付款及其尚未执行的来源 | 紧邻支付通道段落，`such payments` 容易指错对象。 |
| 引言的额度覆盖 | “cover every certified payment” | “cover the outstanding CAL liability of every certified payment” | 对象是未清偿本金责任，不是对每张曾签证书永久重复占资。 |
| 模型与协议 | “member installation” / “first installation” | 组织内复制统一称 `INSTALL` | 避免与历史区块的物理安装混用。 |
| 历史表示 | `installed`、`materialized` 交替出现 | 定义物化为修订区块体在副本上完成物理安装 | 同一个事件在正文、图例和表格中可对照。 |
| 补交路径 | “the same transaction” | “the same atomic local update” | 同一句中的 payment transaction 与数据库事务不是一回事。 |
| 覆盖与进展 | 一段内混写安全性、可用性和最终资金归属 | 按 Safety / Progress / Economics 分段 | 条件和结论紧邻，避免用安全定理替代完成性条件。 |
| 关闭术语 | “close the account” | 关闭 obligation | 此处关闭的是直接义务，不是注销账户。 |
| 故障实验 | “so progress requires a surviving complete copy” | 直接报告保存一份完整材料的实验及备用提交时间 | 该次成功测量不能单独推出必要条件；新补交路径也不要求原成员预先持有完整 QC。 |
| 实验统计量 | 物理安装的 `1.257–1.761 s` | 明确为运行级中位数范围，并保留 P95 与轮询条件 | 使正文与逐轮表使用同一口径。 |
| 适配暂停实验 | 控制组、暂停组、结论挤在一个长段 | 三段依次陈述 | 每段只承担一个论证职责，不改变事件先后或测量值。 |
| 结论 | “account for what the evaluation shows” | “govern how a missing source is closed” | 避免把续花延迟收益归因于关闭路径中的两个性质。 |
| 图表标签 | `Active` / `Control`，`Full-closure throughput` / `Completion throughput` | 统一处理组和指标名称 | 图、表、正文指向同一测量对象。 |

## 保留的内容

- Alice/Bob 小例子放在一般问题之后，用于解释未确认输出的连续消费；例子本身不属于随意写作。
- `only` 等词若限定状态转换、唯一写集合或单变量对照，就保留。删除这些词会改变协议含义。
- 资金覆盖的固定成员与拜占庭条件、原子状态和密码学前提保留在模型与安全节，不以口号代替。
- `d99ec92` 基线、decision-first 和历史归档构建各自保留标识。普通路径短测不被改写为新构建的长期吞吐上限。
- 原始费用、赔付、回款和逐轮分位数没有因行文需要而删改。图表重新绘制时仍读取冻结 CSV，列明统计层级。

## 同步与交付

中英文定义、引用、展示公式、10 个引理、4 个定理、14 段证明及表内数字对应；检查结果见 [bilingual-checks.json](bilingual-checks.json)。标签、引用与图表路径见 [source-checks.json](source-checks.json)，实际编译与 PDF 检查见 [build-verification.md](build-verification.md)。

本轮交付是可继续审稿的论文修订稿，作者单位仍按用户要求保留 University 占位。文字校读不产生录用、首创或全实现机械化证明的保证。
