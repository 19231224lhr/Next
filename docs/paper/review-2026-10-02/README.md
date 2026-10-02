# Next 论文评审修订稿

**Next: Enabling Continuous UTXO Payments with Direct Guarantees**

本版本完整描述公开后继证据触发的来源补交、独立公共赔付决定，以及保持完整区块身份的历史表示。机制实现为 `7a2517e`，公共输入证书校验修正与安全审查对应 `f185f73`。英文正文、中文阅读版和两份补充材料采用同一组定义与状态转换。

实验按实际构建分别呈现：`d99ec92` 来源回收基线、decision-first 补强构建及历史 E1–E8。代码修正没有追溯改变旧实验的版本或数字。参见[安全审查](../../research/c3-security-review-2026-10-02/README.md)、[逐文件修改清单](../../research/c3-security-review-2026-10-02/paper-change-list.md)和[本轮编译检查](build-verification.md)。

## 阅读与源码

2026-10-02 第②项已新增[最终版本 100 跳连续支付重测](../../experiments/final-continuation-2026-10-02/README.md)，以及[中英文替换段落与整合清单](../../experiments/final-continuation-2026-10-02/paper-snippet.md)。这部分尚待与后续代表性故障负载证据一起整合进 TeX；下列 PDF 和 Overleaf 尚未包含本轮新增数字。

第③项现已完成[最终版本混合负载十二轮对照](../../experiments/final-mixed-2026-10-02/README.md)，84,192 笔全部完成，并准备[对应英文与中文段落](../../experiments/final-mixed-2026-10-02/paper-snippet.md)。②③使用相同生产节点二进制；普通负载方法和费用模式分别披露。本文档仅增加材料入口，TeX/PDF/Overleaf 仍待统一整合，未将旧图表数字默默覆盖。

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

英文使用 **pdfLaTeX + BibTeX**；中文使用 **XeLaTeX + BibTeX**。本轮在 Overleaf TeX Live 2026 编译。字体与包由 TeX Live 提供，图表搜索路径兼容本目录的 `figures/` 和 Overleaf 根目录。

```sh
latexmk -pdf main.tex
latexmk -pdf supplement-main.tex
latexmk -xelatex main-zh.tex
latexmk -xelatex supplement-main-zh.tex
```

在 Overleaf 选择根文件及对应引擎，并打开该根文件后编译。重画续花和备付曲线使用 `python data/generate-review-figures.py`，C3 时间线使用 `python data/generate-c3-figures.py`，归档交付门控与运行点图使用 `python data/generate-archived-figures.py`，其测量数字不变，英文术语与正文统一并配有中文图。需要 matplotlib 与 numpy。图表生成器读取相应实验目录的冻结数据；其余归档图表的来源记录在 `data/manifest.json` 及相应实验报告中。

运行 `python data/check-manuscripts.py` 检查四个根文档及中英文引用、公式、证明数量和表内数字的一致性；它不替代 LaTeX 编译和语义审阅。

## 版本与投稿信息

- `source-checks.json` 检查标签、引用和图表文件；`bilingual-checks.json` 检查两种语言的结构与数字对应；`pdf-checks.json` 检查最终 PDF；`measurement-checks.json` 独立核算主要实测数字；`overleaf-checks.json` 核对下载的在线源码与本地版本；`SHA256.json` 记录包内文件摘要。
- 英文稿与中文阅读版表达同一组规则和测量结果；中文不作为 IEEE 投稿排版标准。
- 作者沿用用户提供的 **Lu Hengrun**，单位暂为 **University**。正式投稿前填写真实单位、邮箱与期刊要求的作者信息。
- 主文给出模型条件下的安全论证；有限模型、代码回归和同机实验分别提供实现证据，不将它们称为全实现机械化证明。
- 原始 E1–E8 及历史性能工作点仍对应各自冻结构建。当前短负载不替代旧构建的长期吞吐结果。

## 本轮文字修订

英文写作由侧边栏 Claude 起草和复核，Codex 对照协议、数据与源码审计后落入 TeX。参考 Lutris、Zef、Blitz、Mysticeti 及 TDSC 的 SDR、CryptoMaze、Web3 跨链系统、Escaping 等论文的开篇、机制说明和实验表达；本轮进一步对照 Lutris PDF 第 12–13 页、CryptoMaze 第 13 页、Escaping 第 13–14 页的实现与实验组织。学习的是叙事和句法，不复制原句。

- 引言先给出“收款可续花而来源尚未公开执行”的问题，再分配三项贡献的职责；Alice/Bob 例子保留为具体机制说明。
- 用明确的主体和动作说明补交、赔付和历史表示，减少抽象名词堆叠、重复对比和章节自我介绍。
- 把条件放在模型、定理和方法中。正文主动解释已证实的优势，不把未测量的范围写成新结论。
- 实验围绕论证职责组织，说明测量端点、统计量和构建版本；结果数字、表格和归档版本保持可追溯。
- 统一 obligation、reserve、representation、materialization、complete BlockID 等术语。`close the account` 改为关闭义务，避免被理解成销户；`final-part` 明确为受影响分片的最终表示。
- 中文版逐段对应英文，包括完整定理、证明、实验方法和逐轮表，不作为摘要替代。

文字精简没有删除安全模型的必要假设，也没有将模型论证、回归测试或有限模型检查改称整套代码的机械化证明。
