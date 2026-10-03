# 第四轮修订：最终构建证据与联合核验

本轮实质修订与复审已经收敛。生产付款协议保持 `f856c7b` 基线；新增客户端计时和定向测试，未改被测节点的运行逻辑。Overleaf四根已编译并下载，59个源码/图表经换行正规化后全部一致，详见 final/ 记录。

## 交付入口

- [英文正文](final/Next-fourth-revision-English.pdf) · [中文阅读版](final/Next-fourth-revision-Chinese.pdf)
- [英文补充材料](final/Next-fourth-revision-supplement-English.pdf) · [中文补充材料](final/Next-fourth-revision-supplement-Chinese.pdf)
- [可直接上传的 LaTeX 源码包](final/Next-fourth-revision-LaTeX.zip) · [源码指纹](final/source-sha256.json) · [本地 PDF 检查](final/pdf-checks.json)
- [逐项意见处理](revision-closure.md) · [最终独立核验](independent-review/gpt-final-closure.txt)
- [完整实验报告](../../../experiments/final-evidence-2026-10-04/README.md) · [证明—代码对应](../../../research/proof-code-map-wire4.md)

英文正文18页，中文阅读版20页；中英文补充材料27/28页。保持 IEEEtran 字号和页边距，通过内容精简控制英文正文篇幅。单位 University 仍为用户要求的占位，正式投稿需填写真实信息。

## 本轮证据

六轮100跳连续支付共600笔，三轮代表性混合P共21,048笔；追加一轮自动来源恢复R共2,006笔，全部闭合。100跳认证到账中位数4.707s，逐跳等待对照64.479s；该13.70倍比较包含完整客户端等待与重试。297个fast后继均早于父交易最早应用提交发出，实际缺输入义务为2/4/1。

P中2,400 CAL赔付全额回收，96次副本/目标检查确认经济回收先于表示适配。R中三个扣留来源自动补交并Fulfilled，无赔付。新增一个拜占庭签署者与轮换诚实签署者回归，达到300 CAL备付损失界；第四笔被诚实成员拒绝，重复赔付无新增扣款。

三轮P都触及驱动未完成上限，发送滞后P95最高780.727ms，结果完整保留。已细分917次快速限额事件，避免把发送前持有名额误称为成员慢请求。

## 两位审阅者最终意见

独立初评均为大修。随后发送相同的真实实现、原始证据、版本桥，并互换不同意见：

- **GPT：倾向小修后接收。** 最后确认装配与期限入口疑问关闭，没有必须改动生产逻辑的问题，只剩检查表三处表述修正，现已逐条修改。
- **Claude：没有剩余结构性反对点。** 撤回新AO-CR游戏、弱化消费者类和非CAL已确认缺口的判断，并提供中英文字；他没有给出最终录用档位，不能写成“两位均判小修”。

Claude最后追加请求发送失败，界面显示Pro会话93%、周16%；这不是已确认100%用尽。按照用户最新要求，GPT完成最终装配与文字核验。默认Claude Sonnet5.5 High，不可用时使用GPT。

审阅者进行了材料审查，没有独立重建或复跑。测试、编译与源码核对由Codex执行；这些结论不等于期刊录用或整套实现机械化安全证明。初评PDF和完整对话保留，final/才是本次交付版。
