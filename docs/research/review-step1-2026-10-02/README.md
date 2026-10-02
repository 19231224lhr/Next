# 第①项完成：定点代码审查与读者契约

2026-10-02 · 基线 `6e7c0b8b860d7650b31abad27d23b34fbdb70639`，分支 `re`。

本轮对应联合评审计划的第①项：核对具名的授权、资金、恢复与历史读取路径，补回归和实现对应。没有修改支付协议、热路径、共识参数或存储模式，没有开始长链、混合负载或读者性能实验。

## 结论与改动

在本页列出的路径内，没有复现绕过组织认证消费、补交扩大授权、赔付重复扣款或物化重复恢复额度。确认了一个需要明确写入模型的**跨组织 Intent 冲突边界**，并补上正反回归。此结论是定点审查结果，不替代整体实现精化证明。

交付包括：

1. [历史读者契约](../../design/historical-reader-contract.md)：读者、同一快照、授权来源、物化隔离、身份映射。
2. `internal/member/intent_scope_test.go`：两个组织、真实成员批准生成三票，独立本金和独立费用来源；不同用户的相同 nonce 可执行，同一用户跨组织复用 nonce 在两种执行顺序下均只能成功一笔；冒充其他 Subject 被拒绝；失败重试不写公共状态，原成员 CAL 占用和输入锁保留。
3. `internal/redaction/decision_test.go` 和 `payment_test.go`：补齐赔付、回款、逻辑表示与物化分别读取的断言。
4. 更正 Comet 适配 README 中旧的“修复命令负责扣款”描述，注明 `Canonical` 的信任前提。
5. `internal/committee/direct_entry_test.go`：活跃 wire4 拒绝裸付款及旧格式封装，旧执行分派不能绕过模式约束；完整组织授权提交仍可执行。

## 1. Intent 的实际边界

当前 `IntentID = Digest("INTENT", Network, Subject, Nonce)`。`TxBody.validateVersion` 检查其构造，`FastTx.VerifyAuth` 要求 Subject 授权。它是同一网络、同一付款人的全局操作身份，**不是所有用户之间共享的 nonce 空间**。

两个组织各自保存本地 `KeyIntent`，故同一付款人可以使用相同 nonce 为两笔不同、独立输入的付款分别取得三票。公共 `EvaluateDirectPaymentAt` 在该 Intent 已对应另一个 TxID 时返回 `ErrConflict`。公共 Intent 在成功执行的同一原子转换中写入；当前生产写入口没有删除或重新绑定它的分支。因此同字节重投不能消除此冲突。

这不违反同一输入不被重复认证：该例两笔输入不同、各自路由正确，也不允许冒充其他付款人。但它说明完整 QC 不等于无条件公共执行保证。恶意所有者也能触发；不能通过把“所有用户诚实分配 nonce”加入安全假设来忽略它。

若失败来源的后继已执行并登记缺口，按现行规则可以由发行组织赔付，而来源不能因单纯补交自动偿还；原签发容量仍须扣留。来源输入亦仍受本组织原批准锁限制，不能据此声称用户自由取回并再次花费该本金。该组合经济后果是依据已验证状态规则的推导；本轮 nonce 回归直接覆盖双 QC、执行冲突、原子拒绝、锁与额度，未新增一轮网络赔付实验。

**处理决定：保留已签协议语义。** 诚实钱包应在同一 Network＋Subject 下跨组织、跨实例使用唯一的新付款 nonce；重试复用同一付款与原授权。来源活性陈述补上“不存在阻止其执行的全局 Intent 冲突”。不得改 nonce 后复用旧证书，也不得因共享 Intent 合并两个 Fact 的担保责任。本轮不把更改 Intent 域作为顺手修补。

现有实验构造器 `benchDirect`、`runChain`、`budgetRequest` 从实际输入身份派生 nonce；这不是一个已实现通用多设备钱包分配服务的声明。

## 2. 来源失败与额度占用的分类

| 条件 | 代码行为 | 获证后的解释与处理 |
|---|---|---|
| 正文或完整 QC 未交付 | 签名成员保留 Approval；公开后继暴露 QC 后可重建 outbox | 在材料保留、成员可跟块并服务、投递公平且交易仍可执行时补交；不是固定时间保证 |
| 同一 Subject 跨组织复用 nonce | 公共全局 Intent 拒绝后执行的一笔 | 本轮已复现；重新发送相同授权不能消除冲突，不能无条件保证回款 |
| 不足三票的局部批准 | 原子批准已锁输入并占用额度 | 未形成可消费 QC，不允许仅超时释放；有限资本下可能阻塞服务 |
| 额度不足、跟块滞后 | 批准检查 `Slice.Available`；成功公共事实驱动恢复 | 属于接纳或周转能力限制，不应改称公共账本增发或资金泄漏 |
| 非法所有者／组织签名、错误路由、重复输入、无效证书 | 批准与公共验证拒绝；CAL/FUEL 共用消费状态 | 在既定诚实签名、唯一组织配置和状态不遗忘条件下，不能把这些校验失败直接列成“正常合法 QC 必然遇到”的场景 |
| 缺少已最终输入或重复消费 | 公共状态检查拒绝 | 是执行防线；要主张正常获证后可达，必须另展示违反了什么状态／入口假设，不能仅因代码存在 ErrMissing/ErrConflict 就推定漏洞 |
| 委员会不推进／公共时间未推进 | 没有成功状态转换，也没有新的合法到期决定 | 活性依赖委员会进展；本地墙钟到点不授权扣款 |
| 原签名状态丢失、配置歧义、超过容错上界 | 可能破坏证明前提 | 属于模型外条件；内存实验只覆盖无状态遗忘轨迹 |

全额扣留包括 CAL 输出及找零；真实 FUEL 支出不能作为未花手续费恢复。来源公共成功时，迟到赔付回收与输出处理在同一原子转换内完成。赔付本身不会把来源的全部签发额度提前释放。

## 3. 规则—实现—正反测试

表中的文件路径相对仓库根；函数和测试名用于定位，避免依赖漂移行号。

| 规则 | 实现入口 | 正向证据 | 反向／重复证据 |
|---|---|---|---|
| 批准先锁输入及 CAL/FUEL/工作额度，再签名 | `member.ApproveDirectBytes`，`rules.ValidateDirectFee` | `TestDirectDurableApprovalAndBackgroundInstall` | 同测试中的无效签名、冲突批准；`TestBlockFollowerMissingRepairLateAndDuplicate` 中共享 FUEL 输入拒绝后合法请求仍成功 |
| 公共付款仍需组织认证；输入证书恰好对应所用输入 | `committee.ExecuteAt` → `verifyV3` → `VerifyDirectSubmission` | `TestDirectABCIChildBeforeParentReplay` | `TestDirectPaymentRequiresCurrentOrganizationApproval`；`TestPublicSubmissionRejectsUnusedInputCertificates` |
| 活跃 wire4 不混入旧慢速消费通道 | `committee.ExecuteAt` 与仅旧模式使用的 `Execute`；旧 `EvaluateDirectTransfer` 限 CommitteeRoute | **新增** `TestDirectModeRejectsAlternatePaymentEnvelopes` 中合法公共封装成功；旧 `TestDirectPaymentAtomicFeeAndRoute` 覆盖旧路由语义 | 新测试拒绝裸付款、旧格式和旧执行分派；不把旧入口测试称为 wire4 慢速接口已实现 |
| 用户 FUEL 只能花一次，收费／退回守恒 | `ValidateDirectFee`、`EvaluateDirectPaymentAt`、`direct_fee.go` | `TestDirectOwnerFeeAtomicAccounting`、`TestDirectOwnerFeeRefundCanPayNextTransaction` | `TestDirectOwnerFeeRejectsInvalidFundingAtomically`；成员跟块的共享费用输入拒绝 |
| 全局 Intent 与 Subject 绑定 | `TxBody.IntentID`、`FastTx.VerifyAuth`、`EvaluateDirectPaymentAt` | **新增** `TestDirectIntentScopeAcrossOrganizations/different_subjects` | **新增** same_subject 两种先后顺序、Subject 冒充、重投、保留锁与占用 |
| 补交不重新授权或扣额度 | `member.recoverExposedSources` | `TestExposedSourceReconstructionAcrossOrganizationsAndDepth` | 原 Approval、额度不变；后继重复跟块不新增效果；公共正常验证器再次验证重建材料 |
| 未执行的来源保留全额；只恢复本人的实际扣留 | `member.finishLocal`、`applyPayment`、`applyRepairEffect` | `TestBlockFollowerMissingRepairLateAndDuplicate` 的正常来源、迟到签名和回款分支 | `TestSnapshotUnsettledApprovalRetainsFullDebit`；重复块、迟到 INSTALL、表示成功均不重复恢复 |
| CAL 补资不能自转增发或挪用备付 | `EvaluateReserveIncrease`、受保护账户配置 | `TestReserveIncreaseConservesFundsAndReplays` | `TestReserveIncreaseRejectsProtectedSource`、`TestReserveFundingRejectsProtectedAccounts` |
| 赔付是唯一经济决定，按义务至多一次 | `redaction.ExecuteDecision` → `EvaluateDirectCompensation` | `TestCompensationBeforeAdaptation` | 提前、错网络／位置／金额、余额不足、来源先到、重复决定、回款后重复决定 |
| 迟到来源归还实际赔付，不复活用户输出 | `directEval.recoverOutput`、`EvaluateDirectPaymentAt` | `TestPaymentRepairMonetaryReplay`、`TestSecurityMonetaryProjectionAcrossInterleavings` | 来源重复、父先／决定先、钱包迟到证书不复活已回收输出 |
| 表示仅替换授权资金槽 | `InputTarget`、`nextBody`、`batchBody`、`Execute` / `ExecuteBatch` | `TestAtomicRepairBatchTwoInputs`、`TestRepairBatchAcrossParts` | `TestPaymentRepairMonetaryReplay/reject-*`：含有效开口但错误 Debit、所有者签名、组织签名、父证书与非目标字节篡改 |
| 表示与物化无重复经济效果 | `Execute` / `ExecuteBatch`、`PrepareMaterialization` / `Install`；公共 416 投影 | `TestPaymentRepairMonetaryReplay` 逐高度重放 | 同测试物化前后完整账本相等；成员跟块表示结果不恢复额度；重复批次无效果 |
| 支付身份、DataHash 与完整 BlockID 各有对应 | `FastTx.ID`；Comet `redactionTxHash → Txs.Hash → Data.Hash`；分片与完整 ID 检查 | `TestV3TransactionStableIdentity`、`TestRealBlockStoreRewriteAndOriginalReplay` | `TestConsensusRejectsRelabelledOpening`、`TestConsensusRejectsUncertifiedPartRevision`；Comet `TestStableHashIdentity` |
| 读取固定逻辑前缀，不能以物化进度代替 | `Canonical`；完整决定索引与当前义务独立读取 | **新增断言**于 `TestCompensationBeforeAdaptation` 和 `TestPaymentRepairMonetaryReplay` | 旧前缀读取配合已修订 BlockStore；回款后表示不撤销；`TestMaterializationRevisionOrderAndCursorReplay` |
| 验证缓存不放松授权或公共状态检查 | `verifyV3` 的完整字节键与固定政策实例 | `TestDirectVerificationCacheConcurrentAndFrozen` | `TestDirectVerificationCacheReusesOnlyIdenticalBytes`、`TestDirectVerificationCacheStillChecksLedger`、修复前后暖缓存对照 |

## 4. 对论文的具体修改清单

本轮固定语义与证据，未重编排论文 PDF。下一次正文修订直接采用以下要点：

- 模型／活性：写明同一 Subject 的全局 Intent 规则，区分可恢复的材料缺失与无法靠补交解决的执行冲突；用户任意行为仍在安全模型内。
- 资金论证：未执行来源保留全额扣留，赔付后有真实回款才减少净支出；部分批准的服务成本不等于会计不守恒。
- 第三项贡献：经济闭合由独立决定完成；历史表示记录已发生垫付，并由公共成功命令授权。
- 读者段：采用[契约](../../design/historical-reader-contract.md)，限定已执行前缀下的诚实副本；不将内部读取写成外部无状态验证，不把同哈希当作授权。
- 身份段：区分支付 TxID、共识 Tx.Hash、DataHash、完整 BlockID，说明原始分片规则与原始命令重放。
- 实验证据：本页是功能回归与代码审查，不产生新 TPS、长链延迟或读取成本数字。后续代表性实验仍需在同一冻结版本运行。

## 5. 复核与验证

与侧边栏 ChatGPT 讨论了 nonce 作用域和固定前缀读者契约。它赞同保留现有协议语义，并要求区分成功决定索引与待表示索引、补全两种提交顺序和物化滞后／超前断言。本轮已落实；对方意见是审阅反馈，不当作独立源码验证或安全证明。

验证环境为 Windows amd64、Go 1.27.1、CometBFT v0.38.26 项目 overlay。原始命令退出码见 [verification.json](verification.json)，全项目、定向与 Comet 日志保存于本目录。测试中的有限轨迹用于核查这些具体实现路径；本轮停止于审查范围，没有扩大实验矩阵。

本轮最终验证均通过：

```text
go test -count=1 -tags=comet_v3 ./...
go vet -tags=comet_v3 ./...
go test -count=1 github.com/cometbft/cometbft/consensus -run 'TestStableHashIdentity|TestState|TestProposalBatch'
go test -count=1 -race -tags=comet_v3 ./internal/member ./internal/committee ./internal/redaction
```

竞态结果见 [race-verification.json](race-verification.json)；联合评审原文见 [gpt-review.txt](gpt-review.txt)。这些是本地功能验证，没有使用先前性能结果来声称新快照已完成性能验收。
