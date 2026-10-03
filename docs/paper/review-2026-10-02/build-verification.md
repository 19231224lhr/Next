# 完整大修补证：编译与交付核查（2026-10-03）

## 当前交付

本轮交付内容提交：`e99575aa54bc92bed828d3592c488ad4390ac408`。归档标签：`paper-major-revision-2026-10-03`；分支仍为 `fix/partial-approval-reclamation`，未擅自合并 `re`。该提交固定新测试、12轮记录、修改后的论文及四PDF；归档说明的后续提交不改变这些内容。

实验、代码回归、本地双语论文修订及 **Overleaf最后同步均已完成**。浏览器恢复后上传40个源码文件（含两份此前缺失的中文新增章节）；下载在线源包核对56个源码和图表，全部一致。四根均在 TeX Live 2026 在线编译成功，错误/警告为0/0；最后恢复英文 `main.tex` 与 pdfLaTeX。详见[在线同步记录](overleaf-sync-2026-10-03/README.md)。

| 根文件 | 最终本地引擎 | 页数 | 编译错误 / Overfull / 缺字 |
|---|---|---:|---|
| main.tex | Tectonic 0.17.0 XeTeX/BibTeX | 18 | 0 / 0 / 0 |
| main-zh.tex | 同上 | 19 | 0 / 0 / 0 |
| supplement-main.tex | 同上 | 20 | 0 / 0 / 0 |
| supplement-main-zh.tex | 同上 | 21 | 0 / 0 / 0 |

使用bundle v33。英文显式OT1；中文Fandol/TeX Gyre使用字体文件名。仍有Underfull及捆绑algorithmic源文件编码提示，没有未定义引用、PDF替换字符、空白页或页面外文字；未通过缩小字体/页边距挤页。正文18页不是投稿接受或当前收费政策的保证。

- [本轮检查程序](../../experiments/major-revision-2026-10-03/check_delivery.py)检查四PDF与编译日志、生成逐页联系表和可复现源码ZIP；全部页面联系表已目视核对。
- `data/check-manuscripts.py` 四根引用/图片检查及双语公式、证明、表内数字检查通过；两处已人工核对的公式条件文字允许忠实翻译，其余公式结构仍严格匹配。
- `data/check-current-measurements.py`、新网络及提案覆盖分析从记录重算通过。原 `721800c` 与新 `9879f64` 证据独立归属。
- `pdf-checks.json`、`source-checks.json`、`bilingual-checks.json`、`SHA256.json` 是本轮结果。四PDF及 `Next-review-2026-10-03-LaTeX.zip` 为最新本地交付。
- 原Overleaf英文补充预览曾因缺algorithmic报错；已在本地两种补充根文件修正并成功编译。旧 `compile-supplement.txt` 保留错误轨迹；以 `compile-local-*.log` 为最终日志。
- Claude起草/精简英文和主要中文章节；GPT复核数学、状态与测量口径并翻译安全和新增补充章节；Codex核对源码与记录、校对并编译。双方不被描述为独立执行实验。
- 投稿前仍需用户提供真实单位和通讯信息，当前University占位按既定要求保留。官方格式与AI披露核对见 [submission-check.md](../../experiments/major-revision-2026-10-03/submission-check.md)。

在线同步没有改动论文正文、图表、代码或实验数据；冻结的本地PDF与源码包保持原摘要。在线编译产物、同步前后源码备份和检查记录另行保存。

---

## 上一轮局部批准回收修订（历史记录，非本轮交付状态）

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
