# 第四轮最终交付

本目录是联合核验后的交付版；上级目录同名PDF属于初评快照，保留用于追溯。

| 文件 | 本地Tectonic | Overleaf |
|---|---:|---:|
| 英文正文 | 18页 | 18页，pdfLaTeX |
| 中文阅读版 | 20页 | 20页，XeLaTeX |
| 英文补充材料 | 27页 | 27页，pdfLaTeX |
| 中文补充材料 | 28页 | 28页，XeLaTeX |

- `Next-fourth-revision-*.pdf` 为本地编译版，`Overleaf-*.pdf` 为在线下载版。
- `Next-fourth-revision-LaTeX.zip` 含59个源码和图表，平铺布局可独立编译四个根文件。
- `overleaf-source-checks.json` 核对全部59文件；文本只正规化CRLF/LF，图表逐字节一致。在线项目另保留四张前序图和一份同版本源码ZIP，不被当前根文件引用。
- `pdf-checks.json` 与 `overleaf-pdf-checks.json` 记录页数、引用和SHA256。四份本地PDF无超出页面文字块；最终源码无Overfull警告。本地缓存algorithmic.sty带非UTF8注释警告和部分Underfull排版提示，未阻断编译；在线中文正文出现接近免费编译时限的提示，实际产物完整20页。
- [Overleaf](https://www.overleaf.com/project/6abeb5a49948f44270284a06) 保持英文main.tex/pdfLaTeX为默认。

## 复现与边界

使用本目录冻结源包或父级最终TeX，不要重跑上级历史文字集成脚本覆盖最后校订。节点基线、实验源码清单、测试和证明入口分别见实验报告与逐项关闭表。所有作者信息占位仍需投稿前确认。
