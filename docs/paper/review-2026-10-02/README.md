# Next 论文评审修订稿

**Next: Enabling Continuous UTXO Payments with Direct Guarantees**

**2026-10-03 完整大修补证版。** 本版本描述公开后继证据触发的来源补交、独立公共赔付决定，以及保持完整区块身份的授权历史表示。连续支付、混合负载与历史读取主测量仍对应 `721800c`；新增边界、持久化组合与12轮网络补证对应生产快照 `9879f64`，测试/驱动另存源码指纹。两组证据没有混用版本。

**本地交付已更新：** 英文正文18页、中文阅读版19页，英文/中文补充材料20/21页；四份PDF及可移植LaTeX包已重新编译、核对。**Overleaf最后同步待恢复浏览器工具后完成，本地文件是本轮权威副本。** [补证报告](../../experiments/major-revision-2026-10-03/README.md) · [逐项关闭表](../../experiments/major-revision-2026-10-03/review-closure.md) · [编译与交付检查](build-verification.md) · [源码包](Next-review-2026-10-03-LaTeX.zip)

旧构建 `d99ec92`、早期 decision-first 补强与 E1–E8 移入补充材料，保留各自原始配置，未追溯改写数字。参见[本轮写作裁决](revision-2026-10-03/decisions.md)、[最终版本安全边界审查](../../research/review-step1-2026-10-02/README.md)、[读者契约](../../design/historical-reader-contract.md)和[编译检查](build-verification.md)。

## 局部批准回收修订

代码补丁 `bf71c4d` 增加由同输入公共成功冲突证明授权的局部额度回收。双语协议、窄引理、剩余额度证明及补充材料已同步，主实验仍归冻结构建 `721800c`；新增定点测试、100 TPS × 10 s 和 100 跳功能回归单列，不替换旧性能数据。[修复报告](../../experiments/partial-reclaim-2026-10-03/README.md) · [复核过程](rereview-2026-10-03/README.md)。

## 阅读与源码

正文围绕三组新证据组织，各有明确论证职责：

| 实验与报告 | 正文结果 | 论证职责 |
| --- | --- | --- |
| [当前构建的网络与故障补证](../../experiments/major-revision-2026-10-03/network-report.md) | H/L/L-M/L-C各三轮，15,840笔闭合、四副本一致；注入每方向25 ms并分别暂停一个成员或委员30 s | 区分认证进展、公共等待和全成员观察；实际覆盖故障委员提案机会 |
| [② 最终版本 100 跳连续支付](../../experiments/final-continuation-2026-10-02/README.md) | 3 轮中位数 4.557 s，对照逐跳等待 64.476 s；297 个后继均早于父交易最早应用提交完成发出 | 证明在线构造的真实连续续花；采用同步成员写盘、NoSync 钱包 |
| [③ 混合负载 12 轮](../../experiments/final-mixed-2026-10-02/README.md) | 84,192 笔闭合；24 个扣留来源自动补交；48 次赔付及回款；暂停适配仍完成经济闭合 | 检验正常支付与故障路径共存，并报告并发背压及实际发送跨度 |
| [④ 同授权历史读取](../../experiments/history-reader-2026-10-02/README.md) | 21,600 次查询；点读 P50 降低 7.44%–12.32%；额外逻辑 KV 0.28–2.19 MiB/块、本地维护 43–214 ms/块 | 说明稳定原坐标下授权资金表示的具体读取收益及代价 |

原连续支付与混合负载使用相同 `721800c` 节点二进制；新增网络补证独立标记 `9879f64`。各自披露费用与持久化设置。历史读取实验以已验证并执行前缀的诚实本地副本为对象；有限运行不作为持续吞吐或远端无状态认证测量。

| 文件 | 用途 |
| --- | --- |
| [英文正文 PDF](pdf/Next-review-English.pdf) | IEEEtran 双栏英文主稿 |
| [中文正文 PDF](pdf/Next-review-Chinese.pdf) | 对应中文阅读版 |
| [英文补充材料](pdf/Next-review-supplement-English.pdf) | 证明细节、构建与测量方法、逐轮表 |
| [中文补充材料](pdf/Next-review-supplement-Chinese.pdf) | 对应中文补充材料 |
| [main.tex](main.tex) / [main-zh.tex](main-zh.tex) | 两种正文根文件 |
| [supplement-main.tex](supplement-main.tex) / [supplement-main-zh.tex](supplement-main-zh.tex) | 两种补充材料根文件 |
| [数据](data/) / [图表](figures/) | 图表数据及可直接编译的矢量 PDF |
| [来源与裁决报告](../../research/reviewer-revision-2026-10-02/review-decisions.md) | 评审意见如何核验、采纳或修正 |
| [完整实验报告](../../research/reviewer-revision-2026-10-02/experiment-report.md) | 配置、实验边界及原始证据索引 |
| [C3 补强实验](../../experiments/c3-source-repair-2026-10-02/README.md) | 9 轮功能实验、6 轮普通路径对照及源码摘要 |
| [文字修订记录](editorial-review.md) | 专业论文参照、联合校读及代表性改句 |

Overleaf 修订副本：[Next - Review Revision 2026-10-02](https://www.overleaf.com/project/6abeb5a49948f44270284a06)。原项目没有覆盖。

## 编译

Overleaf推荐英文 **pdfLaTeX + BibTeX**、中文 **XeLaTeX + BibTeX**。本轮最终四份PDF改用本地 Tectonic 0.17.0（XeTeX/BibTeX、bundle v33）编译，因浏览器工具失联无法完成在线交付。英文根显式选择OT1以保持IEEEtran字体；中文按字体文件名选择Fandol和TeX Gyre，避免依赖系统字体族名。图表路径兼容本目录的 `figures/` 和 Overleaf 根目录。

```sh
latexmk -pdf main.tex
latexmk -pdf supplement-main.tex
latexmk -xelatex main-zh.tex
latexmk -xelatex supplement-main-zh.tex
```

在 Overleaf 选择根文件及对应引擎，并打开该根文件后编译。重画续花和备付曲线使用 `python data/generate-review-figures.py`，C3 时间线使用 `python data/generate-c3-figures.py`，归档交付门控与运行点图使用 `python data/generate-archived-figures.py`，其测量数字不变，英文术语与正文统一并配有中文图。需要 matplotlib 与 numpy。图表生成器读取相应实验目录的冻结数据；其余归档图表的来源记录在 `data/manifest.json` 及相应实验报告中。

运行 `python data/check-manuscripts.py` 检查四个根文档及中英文引用、公式、证明数量和表内数字的一致性；它不替代 LaTeX 编译和语义审阅。

## 版本与投稿信息

- `source-checks.json` 检查标签、引用和图表文件；`bilingual-checks.json` 检查两种语言的结构与数字对应；`pdf-checks.json` 检查本轮四份PDF；`measurement-checks.json` 独立核算旧构建主要实测数字；`SHA256.json` 记录新源包和PDF摘要。`overleaf-checks.json` 是上一版在线核查，本轮尚未重新取得在线源包，不可作为最新同步证明。
- 英文稿与中文阅读版表达同一组规则和测量结果；中文不作为 IEEE 投稿排版标准。
- 作者沿用用户提供的 **Lu Hengrun**，单位暂为 **University**。正式投稿前填写真实单位、邮箱与期刊要求的作者信息。
- 主文给出模型条件下的安全论证；有限模型、代码回归和同机实验分别提供实现证据，不将它们称为全实现机械化证明。
- 原始 E1–E8 及历史性能工作点仍对应各自冻结构建。当前短负载不替代旧构建的长期吞吐结果。

## 本轮文字修订

本轮实验章由侧边栏 GPT 起草，摘要、引言、结论和边界段落由新会话 Claude 起草并再次精简，Codex 对照冻结报告与 CSV 审计后落入 TeX。此前精读的 Lutris、Zef、Blitz、Mysticeti 及 TDSC 的 SDR、CryptoMaze、Web3 跨链系统、Escaping 等论文用于参照叙事与句法，不复制原句。草稿、提示词、回答和采纳裁决保存在 [revision-2026-10-03](revision-2026-10-03/)。

- 引言先给出“收款可续花而来源尚未公开执行”的问题，再分配三项贡献的职责；Alice/Bob 例子保留为具体机制说明。
- 用明确的主体和动作说明补交、赔付和历史表示，减少抽象名词堆叠、重复对比和章节自我介绍。
- 把条件放在模型、定理和方法中。正文主动解释已证实的优势，不把未测量的范围写成新结论。
- 实验围绕论证职责组织，说明测量端点、统计量和构建版本；结果数字、表格和归档版本保持可追溯。
- 统一 obligation、reserve、representation、materialization、complete BlockID 等术语。`close the account` 改为关闭义务，避免被理解成销户；`final-part` 明确为受影响分片的最终表示。
- 中文版逐段对应英文，包括完整定理、证明、实验方法和逐轮表，不作为摘要替代。

文字精简没有删除安全模型的必要假设，也没有将模型论证、回归测试或有限模型检查改称整套代码的机械化证明。
