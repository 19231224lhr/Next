# Next 论文评审修订稿

**Next: Enabling Continuous UTXO Payments with Direct Guarantees**

本版本对应 `feat/review-source-recovery` 分支的来源回收机制，生产代码提交为 `d99ec92`。它保留归档实验，并将当前构建的回收、续花、同步存储和短吞吐证据单独标识。

> **版本提示：** 该 PDF／TeX 尚未全面吸收 `7a2517e` 的公开证据补交与独立赔付决定。当前机制的[安全审查](../../research/c3-security-review-2026-10-02/README.md)和[逐文件修改清单](../../research/c3-security-review-2026-10-02/paper-change-list.md)已经完成；完成英／中文同步和重编译前，本目录仍按上述旧构建阅读。

## 阅读与源码

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

Overleaf 修订副本：[Next - Review Revision 2026-10-02](https://www.overleaf.com/project/6abeb5a49948f44270284a06)。原项目没有覆盖。

## 编译

英文使用 **pdfLaTeX + BibTeX**；中文使用 **XeLaTeX + BibTeX**。本轮在 Overleaf TeX Live 2026 编译。字体与包由 TeX Live 提供，图表搜索路径兼容本目录的 `figures/` 和 Overleaf 根目录。

```sh
latexmk -pdf main.tex
latexmk -pdf supplement-main.tex
latexmk -xelatex main-zh.tex
latexmk -xelatex supplement-main-zh.tex
```

在 Overleaf 选择根文件及对应引擎，并打开该根文件后编译。重画本轮两张曲线使用 `python data/generate-review-figures.py`，需要 matplotlib 与 numpy。其余归档图表的冻结来源记录在 `data/manifest.json` 及相应实验报告中。

## 版本与投稿信息

- `source-checks.json` 检查四个文档的标签、交叉引用和文献键；`SHA256.json` 记录包内文件摘要。
- 英文稿与中文阅读版表达同一组规则和测量结果；中文不作为 IEEE 投稿排版标准。
- 作者沿用用户提供的 **Lu Hengrun**，单位暂为 **University**。正式投稿前填写真实单位、邮箱与期刊要求的作者信息。
- 主文给出模型条件下的安全论证；有限模型、代码回归和同机实验分别提供实现证据，不将它们称为全实现机械化证明。
- 原始 E1–E8 及历史性能工作点仍对应各自冻结构建。当前短负载不替代旧构建的长期吞吐结果。
