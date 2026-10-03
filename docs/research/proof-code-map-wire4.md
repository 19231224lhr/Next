# 论文证明与当前实现入口对应

核对日期：2026-10-04。生产基线 `f856c7b3d6265f7275a15767bdced52d40dafada`，分支 `fix/partial-approval-reclamation`。论文对象为 `docs/paper/review-2026-10-02/security.tex`；表中的命题使用 LaTeX label，避免排版后编号变化。当前工作树增加客户端计时和测试，未改变成员、网关、委员会的生产支付路径。构建指纹见 [最终版本复测](../experiments/final-evidence-2026-10-04/source-manifest.json)。

这里区分三种支持：数学论证覆盖模型中的任意有限轨迹；生产守卫说明实现对应点；回归测试检查具体正常与拒绝轨迹。测试通过不替代密码学假设或一般性证明。既有测试直接复用，本轮新增一个实际 ABCI 入站边界测试。

## 前提分类

| 前提 | 类型 | 实现对应与边界 |
|---|---|---|
| 固定四成员/四委员、三票、每组至多一个静态 Byzantine | 模型/部署条件 | QC 检查不同成员及阈值；代码不能测出真实被攻陷成员数。|
| 唯一组织身份/配置、固定路由和 grant 绑定 | 配置与运行时守卫 | `protocol.ValidateOrganizations`；`cmd/internal/config` 加载；`rules.PrepareDirectVector`；`member.ApproveDirectBytes`；重复组织/配置由已有配置测试检查。|
| 诚实成员批准记录、输入锁和额度不回滚 | 存储/部署条件 | 同一 Store.Update 写入后才 `SignSpend`；同步模式用于本轮成员实验。内存/NoSync 实验不能继承断电恢复保证。|
| 所有者与成员签名不可伪造，哈希绑定，合法初始化的门限 RSA CH | 密码学假设及初始化条件 | 签名与开口验证函数实现接口；2048-bit RSA、固定可信 dealer 及门限构造另见补充材料。功能测试不证明密码难题。|
| 诚实公共执行副本/经过认证的成功区块结果 | 下层接口和本地信任条件 | `finality.VerifyBlock` 验证下一高度签名头、目标块与结果承诺；`blockfollow.Commit` 原子推进业务和游标。历史消费者信任已执行前缀 V_H。|
| 持续运行的 follower、公平传送/包含、可执行来源、可用签署容量 | 活性条件 | 有界重试、持久 outbox 和公开删除；不产生无条件时限、不授权超时解锁。|

## 命题—入口—守卫—测试

下列路径相对仓库根。每行列出关键入口及共享原子边界，不要求每个引理单独新造测试。

| 论文命题 | 生产入口与关键守卫 | 正/负测试与核对职责 |
|---|---|---|
| `lem:security-original` 原始执行一致 | `third_party/cometbft/overlay.py` 对 live parts、proposal/POL/locked/valid/commit 比较及同步/重放的补丁；`consensus/block_identity.go:matchesBlockID` 比较 hash 和 part-set header；原始开口/高度/修订校验 | `third_party/cometbft/consensus/block_identity_test.go:TestStableHashIdentityCases`；`internal/redaction/comet_test.go:TestConsensusRejectsRelabelledOpening`, `TestConsensusRejectsUncertifiedPartRevision`, `TestRealBlockStoreRewriteAndOriginalReplay`。局部生产接口与保留原始重放测试；不声称覆盖全部异步网络调度。|
| `lem:security-consumption` 认证与唯一消费 | `internal/member/direct.go:ApproveDirectBytes` 先验证配置、输入证据、所有者、金额，再于一个 Update 内写 input/fee-input locks、Intent、Approval 和 debits；返回成功后签名。`internal/rules/direct.go:EvaluateDirectPaymentAt` 共享 `DirectSpendKey`，同 fact Settled 返回无变化 | `TestDirectDurableApprovalAndBackgroundInstall`；`TestDirectVerificationCacheStillChecksLedger`；`TestDirectABCIChildBeforeParentReplay`；`protocol.TestC01QuorumIdentityAndDistinctVotes`。冲突/重投不二次消费；非 QC 伪造和 raw 入站层见下一行。|
| 公开付款授权链（多个定理共用） | `internal/committee/app.go:CheckTx`, `ProcessProposal`, `FinalizeBlock` → `Engine.Check` → `verifyV3` → `DecodeDirectSubmission`, `rules.VerifyDirectSubmission`；最终执行仍检查当前账本。缓存键是完整原始 bytes，非稳定 TxID | 既有 `TestPublicSubmissionRejectsMissingOrUnrelatedOrganizationApproval` 在规则验证层；`TestDirectVerificationCacheReusesOnlyIdenticalBytes` 在 Engine.Check 层。本轮 `TestPublicAuthorizationAtABCIEntry` 将挪用 QC、伪造 QC、伪造所有者签名的 bytes 送入真实 ABCI 检查/提案/最终执行入口，均拒绝、pendingChanges 为空、业务库不变。**0/2 票的编码器拒绝另列，不当成入站验签测试。**|
| `lem:security-superseded` 窄范围局部批准回收 | `member.PrepareBlock` 仅处理成功、Applied 的认证结果；`applyPayment` 同配置 → `consumePublicInput` → `reclaimPartial`。验证共享输入实例/不同 fact 与 TxID、旧批准无已观察/install/outbox/经济进度，仅恢复残留 debits，写 Invalidated，保留防冲突事实 | `TestPartialReclaimPublicConflict`, `TestPartialReclaimMultipleInputs`, `TestPartialReclaimRequiresSuccessfulExecution`, `TestPartialReclaimRejectsContradictionAtomically`, `TestInvalidatedApprovalRejectsLateEffects`。重复回收幂等，错误/失败公共结果不释放。不同输入的 Intent 输家不适用。|
| `lem:security-resubmission` 补交不扩授权 | `member.applyPayment` → `source_recovery.go:recoverExposedSources`；同配置已批准材料+公共 QC 重建事实，重新验证完整付款；已观察/已有 outbox 不重复排队；只写 outbox，不写批准和 debits | `TestExposedSourceReconstructionAcrossOrganizationsAndDepth`；原来源材料、跨组织/多跳暴露及重复公开处理；`TestRepairAcceptedRetriesKeepBytesAndStopOnPublicCompletion` 检查另一公共重试任务的相同 bytes 与结束条件，不能替代来源补交测试。|
| `lem:security-intent` QC 不保证执行 | `member.ApproveDirectBytes` 为本组织本地 Intent；`rules.EvaluateDirectPaymentAt` 全局 Intent 写入成功交易 TxID 后不改绑 | `TestRevisionIntentTwoRoundCompensation` 真实两轮获批/Intent 拒绝/后继消费/赔付，完整 CAL 与 FUEL 账目；`intent_scope_test.go` 检查不同 Subject 不误冲突。|
| `lem:security-decision` 赔付至多一次 | `redaction/decision.go:ExecuteDecision` 已有决定相同即无变化、不同拒绝；否则要求 Open、公开到期、原坐标与确定金额；`rules.EvaluateDirectCompensation` 和决定记录同一 Overlay 提交 | `TestCompensationBeforeAdaptation`；`TestRevisionIntentTwoRoundCompensation`；`TestPaymentRepairMonetaryReplay`。重复决定/相冲突决定、回款前后顺序均不能重复扣款。|
| `lem:security-recovery` 来源回款 | `rules.EvaluateDirectPaymentAt` 在成功来源交易的输出循环识别 Repaired；`source_recovery.go:recoverOutput` 累计 Recovered≤Paid、原 issuer 入账、Spent 减少、状态转 Recovered；该输出不再创建用户 UTXO | `TestLateSourceRepaysReserveWithoutSecondUserOutput`；`TestSourceRecoveryAllFourHopOrders`；`TestSourceRecoverySplitMergeOrders`；`TestSecurityPartialOutputRepairAndLateSource`。检查迟到来源不增发且多输出/顺序一致。|
| `lem:security-witness` 剩余额度见证 | `member/blocks.go:finishLocal` 只为原 Approval.Debits 的位置、资源、Worker 恢复差额；CAL 仅在源支付 Settled 后按 Cap−(Paid−Recovered) 恢复；`Applied` 累计单调、不超过 Cap；支付/游标同一事务 | `TestOnlyOriginalDebitGetsAuthenticatedCredit`；`TestBatchCreditIsAtomicAndRetryDoesNotRefundTwice`；`TestLocalCreditRetainsLegacyCumulativeAcrossRestart`；以上窄回收测试。赔付本身不释放未执行来源的 CAL 签署占用。|
| `thm:security-coverage` 包括隐藏证书的本金覆盖 | `rules.PrepareDirectVector` 与 `VerifyDirectSubmission` 绑定唯一 CAL 分配、issuer、配置、grant；`ApproveDirectBytes` 检查每个 Worker 的 Available；`protocol.GrantShare` 及初始化/补资按累计总额计算份额；诚实签署者残留容量是证明见证 | `TestRevisionCapacityFragmentationAndFunding`；`TestRevisionSplitInputNotUnlockedByFunding`；`TestReserveMemberKeepsDebitsAndCumulativeRounding`。隐藏 QC 集合依数学论证，测试不枚举所有隐藏组合。|
| 动态补资不重复抵押 | `rules/reserve.go:EvaluateReserveIncrease` 隔离 protected reserve，检查外部来源、余额、Previous 与单一 grant；`ApplyReserveIncrease` 保留既有 debits，使用累计份额差额 | `TestReserveIncreaseRejectsProtectedSource`, `TestReserveFundingRejectsProtectedAccounts`, `TestReserveSharedExternalFunding`, `TestReserveIncreaseConservesFundsAndReplays`。自转账与挪用别组织已授权备付被拒绝。|
| `thm:security-conservation` CAL 守恒 | `rules/direct.go` 原子消费/输出/缺口；`EvaluateDirectCompensation` 缺口转备付扣款；`recoverOutput` 用户输出被回款取代；FUEL 独立账目 | `TestSecurityMonetaryProjectionAcrossInterleavings`, `TestSecurityPartialOutputRepairAndLateSource`, `TestPublicPaymentLifecycleConservationAndReplay`；本轮 600 连续及 21,048 混合付款逐副本总额审计。|
| `thm:security-neutrality` 来源闭合后的终态 CAL | 上述状态机、唯一消费、全部来源成功的条件；不包含永久 Intent 输家与 FUEL 费用的时延中性 | 多顺序与 DAG 规则测试、`TestPaymentRepairMonetaryReplay`。两轮 Intent 损失测试是适用条件的反例，不能拿来宣布无条件中性。|
| `lem:security-repair` 表示修改授权 | `redaction.InputTarget` 要求成功决定；`batchBody` 从当前 Canonical 重建准确 funding slots、非目标字节不由请求替换，检查 Base/Previous、开口和固定长度；`ExecuteBatch` 验证 Next 摘要与原完整 BlockID，原子写 Revision/Task/队列 | `TestAtomicRepairBatchTwoInputs`, `TestRepairBatchAcrossParts`, `TestHistoricalExportConsumer`。已入块但执行失败的命令没有 Task 授权；原 commit 只证明身份相容。|
| `lem:security-representation` 表示无经济效果 | `ExecuteBatch` 写集只有修订、头命令、task、batch 引用、队列及 pending-index；不写账户、消费、额度和费用。`DecodePublicExecution` 与 follower 不重复应用本金 | `TestCompensationBeforeAdaptation`, `TestPaymentRepairMonetaryReplay`；三轮 P 暂停适配时先赔付并回款、恢复后无新经济效果。|
| `lem:security-installation` 身份/安装独立 | `Canonical` 读公开逻辑修订；`PrepareMaterialization` 从精确 Task 导出；安装按逻辑/物理版本处理，禁止回滚；修订资金槽不改 owner auth、TxID/OutputID、后继引用 | `TestMaterializationRevisionOrderAndCursorReplay`, `TestMaterializationInterleavedQueue`, `TestRealBlockStoreRewriteAndOriginalReplay`, `TestHistoricalReaderEquivalence`；body 相同不意味着 command history/AppHash 相同。|
| 本地历史读取契约 | `Canonical`、精确 Revision/Task、已执行 V_H、原 BlockStore commit；验证完整 hash+part-set，再读当前 obligation | `TestHistoricalExportConsumer/same_consumer_views`：A 索引/M 普通缓存/B 授权正文经济答案一致；H=4/5 的 Repaired→Recovered；旧 commit 验证原/授权 body；未适配 parts 保持 header 却不保持完整 ID。M 无性能主张，非远端认证接口。|
| `thm:security-continuation` 组合安全 | FreshRecv 历史条件，以上认证/消费、覆盖、赔付与表示性质的归纳组合；钱包身份及 owner 授权约束 | 最终版六轮 100 跳、297 个提前后继；组合规则/历史重放测试。实验是具体轨迹，FreshRecv 不由 receipt 单独推出。|
| `lem:security-closure` 经济闭合不依赖适配 | `ExecuteDecision` 不访问 adaptation endpoint；到期、备付、消费者 fee escrow、历史位置及公开包含是独立前提 | `TestCompensationBeforeAdaptation`；最终版三轮 P 中 4×8×3 观测均先 Recovered 且表示未提交/物化；稍后恢复表示。无固定完成时限、无无干扰性能推论。|


## 补充入口与读侧核对

- **接收入口**：`internal/wallet/direct.go:ReceiveDirect` 按注册配置验证 network、收款 owner 与 `OutputCertificate.VerifyOutput`（QC 和输出绑定），随后在同一 Update 保存 DirectCoin；同 OutputID 不同输出拒绝。接收事件由 `TestE7ReceiptOnlyAfterSuccessfulReceive` 和 `TestE7ReceiverVerifiesAndQueuesBeforeACK` 约束。它不是仅验一个组织标签。
- **INSTALL 入口**：`internal/member/direct.go:InstallDirectClassified` 先 `VerifyDirectPayment`，再于事务内拒绝 Invalidated、对 Observed 返回空修改；检查并写入普通和 FUEL 输入 Consumed、完整 Install 与 outbox，不再扣一份签署额度。既有 `TestDirectDurableApprovalAndBackgroundInstall` 与 `TestInvalidatedApprovalRejectsLateEffects` 覆盖正常及迟到路径。不同协议版本的旧 INSTALL 测试不能代替 wire4 核对。
- **公共命令分派**：`committee.Engine.Check/ExecuteAt` 在 direct 模式只识别补资、时钟、赔付/单项修复/合批修复及完整 DirectSubmission；legacy `Execute` 明确拒绝 direct 模式。`TestDirectModeRejectsAlternatePaymentEnvelopes` 检查裸付款及 legacy 报文的预检/执行拒绝和合法封装成功。修复类进入 `EnableRepair` 装配的共享验证与执行路径。
- **创世与受保护 CAL**：`committee.NewEngine` 按同一 AccountKey 累计所有 CAL/FUEL grant 的 Amount，累加溢出或合计超过账户余额即拒绝；受保护 CAL 集合来自所有组织与 CAL grant 账户。正常支付消耗 UTXO，FUEL 收費使用独立资产；本金账户扣减由决定/赔付路径，補資不得使用集合内来源。公共分派无任意 CAL 提款命令。这是覆盖论证的部署与入口边界，不把仅补资测试当作全命令枚举。
- **表示的后续读行为**：`EvaluateDirectPaymentAt` 读取当前账本与固定付款，不依赖历史 Canonical。`ExecuteDecision` 对已有同决定先幂等返回；尚无决定时 `targetSlot` 检查 Open 义务、原坐标/TxID/消费者事实/金额，且该目标仍为 OriginalFunding 与原 OutputID。其他已决定槽可修订，但不改变此槽及固定字段。因此同高度/时间下表示步骤不改变后续经济执行。固定目标集的字节/开口收敛仍要求参数使有效开口唯一。

## 原子边界与测试层级

1. **成员批准**：验证可在事务前完成；输入/Intent/额度/Approval 的修改在同一个 `Store.Update`，成功返回后才签名。已有真实 Bolt 中断/重开测试归档于 `major-revision-2026-10-03/approval-boundary.log`。
2. **公共执行**：`App.FinalizeBlock` 先 Check，再在 Overlay 执行业务，失败交易不应用修改；`App.Commit` 原子提交变更和响应/高度。新 ABCI 负向测试直接调用接口，不是 HTTP/TCP 网络故障试验。
3. **成员/钱包跟块**：`finality.VerifyBlock` 成功后 PrepareBlock；`blockfollow.Commit` 使业务修改与 cursor 同步。当前本地计时的 committed 回调在事务成功返回后，NoSync 钱包并不因此获得耐久刷盘保证。
4. **表示安装**：公开命令先形成 Task；物理块库按 Task 安装。物理安装不重新执行经济动作；本地保留原始材料用于重放。

## 本轮验证与未扩大范围

- [八包定向回归](../experiments/final-evidence-2026-10-04/targeted-regression.log)：committee/member/rules/redaction/finality/protocol/config/payctl 均通过。
- 新增测试是 `internal/committee/submission_boundary_test.go`；初次测试发现构造无关 QC 会被编码器提前拒绝，因此改为等宽替换已编码合法报文中的 QC 字段，真正测试接收路径。生产代码不变。
- Comet 状态机接口、局部持久重启、网络补证、门限原语测试继续各自引用原归档版本；没有将本轮复测说成新的远程历史下载、任意 Byzantine 调度或密码学机械证明。
- 普遍活性、恶意真实数量、可信 dealer、不可伪造假设与无回滚部署前提不由这一映射表“测试为真”。论文中的条件保留在对应结论附近。

## 本轮最终核验补充

- `internal/committee/repair.go:EnableRepair` 装配的 Check 负责解码/网络，Execute 分别调用三种真实执行器；语义授权在 redaction 执行路径，不从入口静态检查推出可执行性。
- `internal/rules/direct_deadline.go:AnchorDirectDeadlines` 仅为达到锚定高度、仍Open且Deadline为零的义务设期限，处理后删除锚定项；实际文件位置以本轮 `final-small-checks.txt` 为准。源码/函数证据保留在论文第四轮评审包。
- `TestRotatingQuorumsReachFundedLossBound` 证明300 CAL上界在一个拜占庭签署者轮换诚实对子时可达，重复赔付无效果，后续无法得到诚实批准；不是一般经济激励证明。
- 成员滞后可产生单个局部冲突批准，交集论证排除的是冲突QC。最终输入与用户费用输入需要认证创建记录，证书输入使用匹配证书；锁才是在签名前新增的状态。
- 原始block parts的revision0/opening1、CH兼容性、已执行决策/Task的修订授权是不同性质。不使用本轮评审中被撤回的AO-CR游戏。
