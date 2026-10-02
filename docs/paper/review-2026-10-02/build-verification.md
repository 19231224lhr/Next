# 第⑤轮编译与交付核查（2026-10-03）

本轮统一整合冻结生产构建 `721800c` 的连续支付、混合负载与历史读取实验；生产代码未修改。

## 编译

在现有 Overleaf 修订副本 `6abeb5a49948f44270284a06`，使用 TeX Live 2026 分别编译并下载四份 PDF。

| 根文件 | 引擎 | 页数 | Errors / Warnings | 栏宽溢出 |
| --- | --- | ---: | --- | --- |
| main.tex | pdfLaTeX + BibTeX | 17 | 0 / 0 | 0 |
| main-zh.tex | XeLaTeX + BibTeX | 18 | 0 / 0 | 0 |
| supplement-main.tex | pdfLaTeX | 15 | 0 / 0 | 0 |
| supplement-main-zh.tex | XeLaTeX | 16 | 0 / 0 | 0 |

日志保存在 `revision-2026-10-03/compile-*.txt`。各文档仍有少量 Underfull 排版提示；初次英文编译的读者返回值溢出已通过短符号元组及正文释义修正。未改字号、栏宽或页面尺寸来压缩页数。

## 证据与源码

- 从冻结 CSV 独立重算连续支付中位数、混合负载统计、读取改善率与成本，见 `measurement-checks.json`。
- 四个根文件均无悬空引用、重复标签、未知文献键或缺图；双语引用、公式、证明数量与表内数字对应，见 `source-checks.json` 和 `bilingual-checks.json`。
- 下载在线源码逐项比较 51 个 TeX、BibTeX 与图表文件，换行与行末空白规范化后均与本地一致，见 `overleaf-checks.json`。
- PDF 全部为 US Letter（612 × 792 pt），未发现未解析引用、空白页或越出页面的文字。逐页联系表已目视检查，正文首页、实验跨栏表和公式重点页面另外检查。见 `pdf-checks.json` 与 `revision-2026-10-03/*-sheet-*.png`。

四份 PDF 已同步到 `pdf/`。Overleaf 恢复为英文 `main.tex`、pdfLaTeX 并显示新主稿。可移植源码包为 `Next-review-2026-10-03-LaTeX.zip`。

这些检查确认本轮整合和交付的一致性，不等同于新的协议安全证明或期刊录用判断。作者真实单位和通讯信息仍按原约定留待投稿时填写。
