# 局部批准回收修订：编译与交付核查（2026-10-03）

本轮代码补丁为 `bf71c4d`。论文增加局部批准失效转换及窄引理，修正引言与剩余容量论证，保留主实验冻结评估构建 `721800c` 的所有测量。新回归与旧性能结果分开。

## 编译

在现有 Overleaf 修订副本 `6abeb5a49948f44270284a06` 使用 TeX Live 2026，选择并载入每个根文件后分别编译、下载四份 PDF。

| 根文件 | 引擎 | 页数 | Errors / Warnings | Overfull |
| --- | --- | ---: | --- | --- |
| main.tex | pdfLaTeX + BibTeX | 18 | 0 / 0 | 0 |
| main-zh.tex | XeLaTeX + BibTeX | 19 | 0 / 0 | 0 |
| supplement-main.tex | pdfLaTeX | 16 | 0 / 0 | 0 |
| supplement-main-zh.tex | XeLaTeX | 17 | 0 / 0 | 0 |

本轮日志在 `rereview-2026-10-03/compile-*.txt`。仍有 Underfull 排版提示；没有通过缩小字号、栏宽或页面尺寸压缩篇幅。此项确认编译与版面，不等于期刊篇幅和投稿资格审查。

## 源码与证据

- `rereview-2026-10-03/check_artifacts.py` 重新展开四个根文件核对标签、引用、文献和图表，未发现悬空引用、重复标签、未知文献键或缺图，见 `source-checks.json`。
- 双语的标签、引用、文献及证明环境对应，见 `bilingual-checks.json`；新增引理页逐段目视核对。
- 下载在线源包，规范化换行与行末空白后比较 52 个 TeX、BibTeX、类文件和图表，全部一致，见 `overleaf-checks.json`。
- 四份 PDF 均为 US Letter（612 × 792 pt），无空白页、未解析问号或越出页面的文字，均可检索到新增回收规则及 `bf71c4d` 版本。逐页联系表和中英引理页面已目视检查，见 `pdf-checks.json` 与 `rereview-2026-10-03/*-sheet-*.png`。
- 原实验数据未修改，上一轮独立重算结果继续见 `measurement-checks.json`。新短回归另见 `docs/experiments/partial-reclaim-2026-10-03/`，不据此主张旧性能在新构建上不变。

Overleaf 已恢复英文 `main.tex`、pdfLaTeX。四份 PDF 位于 `pdf/`；可移植源码包为 `Next-review-2026-10-03-LaTeX.zip`，摘要由 `SHA256.json` 标识。

GPT 两轮代码与论文复核已完成；五项最终文字修正已落实。Claude 服务过期，未取得实施后的新意见，旧计划评议单独保留。作者单位与通讯信息仍按原约定留待正式投稿填写。
