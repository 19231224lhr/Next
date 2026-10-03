# 服务保证与风险：定向修订

本轮于 2026-10-03 开始，2026-10-04 收尾。目标是把已实现的服务语义集中讲清楚，不改 Intent 或支付协议。源码核对基于当前 `fix/partial-approval-reclamation`；本轮没有 Go 生产代码变更。

## 交付

- [英文正文](Next-service-contract-English.pdf)（18 页），[中文阅读版](Next-service-contract-Chinese.pdf)（20 页）。正文 III-E 与表 II 为新增服务契约。
- [英文补充材料](Next-service-contract-supplement-English.pdf)（21 页），[中文补充材料](Next-service-contract-supplement-Chinese.pdf)（21 页）。S3 与表 S4 补充逐轮风险台账及固定签署模式的容量上界。
- [完整 LaTeX 包](Next-service-contract-LaTeX.zip)。父目录的 TeX 是本轮工作稿；旧 `pdf/`、旧源码包及 `overleaf-sync-2026-10-03/` 保持上一冻结版，不覆盖历史产物。
- [面向实现的服务契约](../../../design/payment-service-contract.md)，列出具体守卫和函数对应。

## 本轮修改

1. 区分钱包认证到账、后继公开消费、来源履行或赔付三个事件。TXCer 允许请求后继；后继有自己的授权、输入、费用和容量检查。持有证书不等于已经登记公共赔付义务。
2. 指明每名成员在批准事务中检查各资源的 Worker 可用额度，失败不提交该次变更、不签名。已有其他局部批准仍可能占用；无法取得法定人数时不产生新证书。公共备付余额不等于可用签发额度。
3. 明确 CAL 责任发行方与 FUEL 费用支付方可以不同；没有把费用赞助政策或网关并发限额写成 CAL 全局准入。
4. 保留固定 grant 下每个前缀的净未偿赔付界 `S_g <= Q_g <= B_g`。新增台账呈现两轮后备付仍剩 100、三名来源签署者却各保留 200 的停止状态。解析式仅适用于固定三签者、单 Worker、无其他占用、无释放、无补资的模式。
5. 同步摘要、引言、结论的服务范围与中英文正文。没有新增“认证必然可执行”引理，没有把本轮表述修订说成消除 Intent 风险。

## 代码与测试核对

通过知识图谱定位并阅读 `ApproveDirectBytes`、`CollectDirect`、`ReceiveDirect`、`EvaluateDirectPaymentAt` 和 `TestRevisionIntentTwoRoundCompensation`。关键证据：成员成功提交后才签名；钱包核验输出 QC；公共义务、缺口与后继输出在同一转换内记录；赔付后到达的来源偿还备付、不重复创建用户输出。

定点回归：

```powershell
go test ./internal/member -run '^TestDirectIntentScopeAcrossOrganizations$' -count=1
go test -tags comet_v3 ./internal/redaction -run '^TestRevisionIntentTwoRoundCompensation$' -count=1 -v
```

两种费用模式的两轮轨迹均通过，见 [本轮日志](intent-regression.log)。这不是新吞吐测试，也不是重新运行完整实验矩阵。[逐行核对后的台账](ledger-checked.json) 来自已有冻结 `boundary-summary.json`；本轮真实测试再次执行相同断言。拒绝、永久锁定、后继继续、重复赔付无效果均由原测试核对。

## Claude 协作与采纳

使用已有 Pro 对话、Opus 5.5 High；进入时当前会话已用 24%、周额度 19%。[对话](https://claudez.yuehanxai.com/chat/89c58d30-5daa-46ec-b8b6-36fd428dd3a3)。提示、草稿与二次校读记录均在本目录。

采纳其服务分阶段、风险承担方、逐轮台账的组织方式。对草稿修正：幂等重试依据已有批准重新签同一事实，并非读取保存的签名；额度检查覆盖所有适用资源；净未偿赔付不同于终身赔付毛额；条件进展不改写成全局强制准入；没有消费的证书不会因来源未执行就自动产生公共赔付。双方在这些局部修订上无未解决分歧，未要求 GPT 再作同一轮复核。

## 检查范围

四个根文件使用现有 Tectonic 0.17.0 缓存编译成功。`data/check-manuscripts.py` 通过，双语标签、引用、公式、证明数量和表格数字匹配。[PDF 检查](pdf-checks.json) 未发现未定义引用、缺字或越界框；存在 Underfull 排版提示，已查看新增服务契约与风险台账页面。没有通过缩小字体或负间距挤页。

本项完成不代表其他评审项全部关闭；本轮没有扩展 C3、性能重测或协议改造范围。

## Overleaf 同步完成

项目 `6abeb5a49948f44270284a06` 已同步。本轮 12 份修改源码从 Overleaf 下载后逐一比对，忽略换行编码后与本地完全一致。四个根文档均完成在线编译，最终 Errors 与 Warnings 计数为 0；排版日志仍有正常的 Underfull 提示。已恢复 `main.tex` / pdfLaTeX 作为默认英文预览。

- [在线编译英文正文](Next-service-contract-overleaf-English.pdf)，[在线编译中文正文](Next-service-contract-overleaf-Chinese.pdf)。
- [在线编译英文补充](Next-service-contract-overleaf-supplement-English.pdf)，[在线编译中文补充](Next-service-contract-overleaf-supplement-Chinese.pdf)。
- [下载的在线源码](Overleaf-service-contract-source.zip)，[逐文件与 PDF 检查](overleaf-checks.json)。

正文分别为 18 / 20 页，补充材料分别为 21 / 21 页。此前冻结版没有覆盖。本轮尚未提交或推送 Git。
