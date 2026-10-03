# Overleaf 最后同步核查

同步项目：[Next - Review Revision 2026-10-02](https://www.overleaf.com/project/6abeb5a49948f44270284a06)。源码对应冻结提交 `e99575aa54bc92bed828d3592c488ad4390ac408`，未修改实验或正文内容。

- 同步前先下载源码备份，再上传40个TeX/BibTeX/类文件，包含两份新增中文章节。
- 下载同步后源码，核对40个文本文件与16个图表PDF；56项全部一致。文本规范化BOM、换行及文件末尾空白，图表逐字节一致，见[核对清单](../overleaf-checks.json)。图表在Overleaf根目录，本地在figures目录，正文graphicspath支持两种位置。
- 使用TeX Live 2026、Normal模式，逐根打开后编译并下载PDF。各根均为0错误、0警告，无Overfull，仍有Underfull排版信息。

| 在线产物 | 引擎 | 页数 | Underfull信息数 |
| --- | --- | ---: | ---: |
| [英文正文](Next-overleaf-English.pdf) | pdfLaTeX + BibTeX | 18 | 18 |
| [中文正文](Next-overleaf-Chinese.pdf) | XeLaTeX + BibTeX | 19 | 10 |
| [英文补充材料](Next-overleaf-supplement-English.pdf) | pdfLaTeX + BibTeX | 20 | 6 |
| [中文补充材料](Next-overleaf-supplement-Chinese.pdf) | XeLaTeX + BibTeX | 21 | 3 |

PDF页数、标题、空白页及替换字符检查见[pdf-checks.json](pdf-checks.json)。四份逐页联系表已经目视核查。编译界面记录为compile-*-ui.txt，同步前后源码分别为source-before-sync.zip和source-after-sync.zip。

最终项目默认根文件为main.tex、引擎为pdfLaTeX，英文预览已恢复。[界面截图](overleaf-final.jpg)。本目录在线产物与此前Tectonic本地产物分开归档，原SHA256.json和论文归档标签保持不变。
