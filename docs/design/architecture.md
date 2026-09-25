# Next 工程架构与协议实现映射

> **wire 4 · 代码审读基线 `1a2eed0a9df303585c086a76851670b6fe6ed8cc` · 2026-09-25**
>
> [系统设计](system.md)定义业务状态与转移；本文说明它们落在哪些进程、函数、存储和并发边界。本轮只重构文档，不增加新协议、不修改运行代码。旧名称 `v3.go`、构建标记 `comet_v3` 仍可能承载 wire 4，不可按文件名猜测协议版本。

## 1. 工程分层与运行拓扑

项目使用 Go，模块为 `utxo`，版本由 [go.mod](../../go.mod) 固定。CometBFT v0.38.26 通过仓库内受限适配接入；[overlay.py](../../third_party/cometbft/overlay.py) 生成被忽略的 `.scratch/comet-src`，补丁源必须保留，上游生成副本可以重建。

| 层次 | 代码入口 | 职责 |
| --- | --- | --- |
| 对象与密码学 | [protocol](../../protocol/)、[crypto/chameleon](../../crypto/chameleon/) | 规范编码、身份、授权、组织证据、输入与分片承诺 |
| 确定性规则 | [internal/rules](../../internal/rules/) | 输入与资源准入、公共付款、费用、直接责任、补资 |
| 原子状态 | [internal/state](../../internal/state/)、[internal/store](../../internal/store/) | 状态键与记录、Overlay、内存／bbolt／批量更新 |
| 前台角色 | [wallet](../../internal/wallet/)、[gateway](../../internal/gateway/)、[member](../../internal/member/) | 钱包构造接收、三票收集、成员批准与预算 |
| 公共执行 | [committee](../../internal/committee/)、[redaction](../../internal/redaction/) | ABCI、静态缓存、按块执行、修订授权和物化 |
| 公开事实应用 | [finality](../../finality/)、[blockfollow](../../internal/blockfollow/) | 块与结果认证、本地有序应用及游标 |
| 传输与装配 | [transport](../../internal/transport/)、[cmd](../../cmd/) | HTTP 复用、节点生命周期、实验与审计 |
| 共识适配 | [third_party/cometbft](../../third_party/cometbft/) | 稳定承诺、历史修订、原始版本重放及性能补丁 |

双组织实验拓扑为四委员、八成员、两网关，共十四服务进程；单组织性能轮为九个服务。钱包通常由实验驱动承载，E7 另有独立收款路径。进程拓扑、钱包位置和存储配置必须随实验报告注明。

业务层通过状态接口工作，网络和后台调度不替代规则裁决。`tools/lnbench` 是独立 Lightning 对照模块，不属于支付节点，但属于复现实验材料。

## 2. 从钱包到三票交付

| 步骤 | 当前代码 | 必须保持的语义 |
| --- | --- | --- |
| 保存发送请求 | [wallet/direct.go](../../internal/wallet/direct.go) · `SaveDirectRequest` | 原请求、Intent 与本地 CAL／FUEL 消费标记原子保存，重试复用原 TxID |
| 网关接入 | [cmd/gateway/direct.go](../../cmd/gateway/direct.go) · `newDirectPaymentHandler` | 有界接入；构造证书后响应，响应后早发与保存可重叠 |
| 四成员分发 | [gateway/direct.go](../../internal/gateway/direct.go) · `CollectDirect` | 规范请求编码复用，前三份匹配有效票结束收集；不把任意三响应当 QC |
| 成员批准 | [member/direct.go](../../internal/member/direct.go) · `ApproveDirectBytes` | 从原始字节验证，同一事务占用输入、Worker 权限并保存批准，成功后签票 |
| 资源计算 | [rules/direct.go](../../internal/rules/direct.go) · `PrepareDirectVector` | 全输出 CAL、费用来源、工作上界和 Grant 绑定 |
| 收款钱包 | [wallet/direct.go](../../internal/wallet/direct.go) · `ReceiveDirect` | 验证可信配置、输出正文绑定和 QC，原子接收；重复与迟到实例处理不增余额 |

`protocol/v3.go` 中的 `FastTx` 把不可变 core 与可替换 Funding 分开；`SummaryFor` 将交易、输出、资源和 Grant 绑定成签署事实。业务层不能信任调用方预解析后的可变切片。

当前 Worker 由交易哈希首字节取模选择，成员份额均分给 Worker；没有旧方案中的自动调配公共池。所有 Worker 的状态仍经同一成员 Store 原子更新，不应把“预算分区”画成无共享状态的独立资金节点。

同组织复用已验证配置与静态材料；跨组织同样校验原发行组织证据。支付路径没有省略所有者签名、输入冲突或三票检查的“可信组织内转账”特例。

## 3. 后台保存、INSTALL 与公共投递

### 3.1 三种对象各司其职

`DirectRequest` 供成员取证；`DirectPayment`（405）保存本次完整 TXCer 并向成员 INSTALL；`DirectSubmission`（406）提交委员会，保留组织消费授权、准入和直接输入证书，不再附本次新输出证书对象。

新 TXCer 的交付与公共提交共用同一批准事实。分离封装不意味着取消组织认证，也不意味着任何带用户签名的快速请求都能直接通过公共规则。

### 3.2 网关路径与成员路径尚不完全相同

| 行为 | 网关直接中继 | 成员备用中继 |
| --- | --- | --- |
| 入口 | [direct_relay.go](../../internal/gateway/direct_relay.go) · `runDirect` | [relay.go](../../internal/gateway/relay.go) 的成员分支 |
| 首次来源 | 有界早发内存项／已保存 outbox | 首次 INSTALL 保存的 outbox |
| 并发 | Submit 8 个；INSTALL 4 个**目标 RPC** | 分页 16 条，每组 4 条并发 |
| Submit／INSTALL | 独立配额，不等待 INSTALL 完成才能 Submit | 不再扇出 INSTALL |
| 重试节流 | 有界内存冷却表，2 秒；不为每次节流写数据库 | 原有 `NextSubmitUnixNS` 更新仍保存 |
| 唤醒／兜底 | 早发、保存通知、完成补位，100 ms 公平分页兜底 | 100 ms 扫描 |
| 首次备用期限 | 正常立即首投 | 首次安装时间 + 2 秒 + 成员索引 × 250 ms |

上述数字是当前工程参数，不进入安全定理。不能把网关已经完成的调度优化写成全系统所有中继都采用同一实现。

[direct_inbox.go](../../internal/gateway/direct_inbox.go) 提供有界早发材料；待办保存与网络动作可并行，队列满时保留保存后扫描途径。网关按事实／动作／目标防止同动作重叠，完成信号不能丢失；新提示与旧分页轮流推进。

可信公共成功结果优先于迟到更新。执行前重读状态，INSTALL 和保存事务内检查 Observed；已删 outbox 不由旧副本恢复。HTTP 202 不能替代公共终态，也不能无限延后成员备用期限。

快速响应已经发出之后，后台保存失败不会把已交付凭证撤回。因此安全依赖批准锁仍有效，活性另依赖完整材料可得和诚实重投；这正是提前交付与等待复制回执的取舍。

## 4. 委员会：静态预检与最新状态裁决分离

### 4.1 调用路径

[committee/engine.go](../../internal/committee/engine.go) 接入确定性规则，[committee/app.go](../../internal/committee/app.go) 承担 ABCI 生命周期：

`CheckTx／PrepareProposal／ProcessProposal → FinalizeBlock → Commit`

静态检查可复用已验证的完整报文；提案选择受实际块字节等配置限制，不以祖先已到达为排序前提。按块执行由单写者顺序处理，交易 Overlay 成功后合并到块状态，块末统一生成状态修改并提交。

`CheckTx` 接受不等于已消费输入，也不承诺以后执行成功。`FinalizeBlock` 对当前余额、输入、Grant 和责任状态继续检查；业务拒绝或幂等空执行不能被跟块方当成新增成功付款。

### 4.2 验证缓存边界

[committee/direct.go](../../internal/committee/direct.go) 的 `verifyV3` 缓存键绑定完整原始报文摘要及引擎配置上下文。`VerifyDirectSubmission` 冻结切片并验证初始交易、组织 QC 与直接输入绑定。

不能只用 TxID 缓存：Funding 和 Auth 改变时稳定 TxID 可能不变。也不能缓存动态裁决：输入是否消费、Grant 剩余额度、真实余额和义务终态每次读取当前状态。

当前成员拒绝多余且未使用的直接输入证书；委员会静态验证会检查所附证书及所需绑定，但没有完全同样的“所有附带证书必须使用”检查。模型应明确合法消息构造与接纳条件，不笼统宣称两端验证逐项完全一致。

### 4.3 确定性业务核心

| 函数／文件 | 对应系统语义 |
| --- | --- |
| `EvaluateDirectPaymentAt` · [rules/direct.go](../../internal/rules/direct.go) | 按最新状态消费输入、费用托管、按需登记覆盖／缺口、建立最终输出 |
| `register / completeOutput / endObligation` · 同文件 | 分别维护整证书覆盖、逐输出承诺及实际缺口；不能合并成一种状态 |
| `ValidateDirectFee` · [rules/direct_fee.go](../../internal/rules/direct_fee.go) | 用户最终 FUEL 的来源、授权、路由与金额校验 |
| `AnchorDirectDeadlines` · [direct_deadline.go](../../internal/rules/direct_deadline.go) | H+1 一致区块时间锚定 |
| `EvaluateDirectCompensation` · [rules/direct.go](../../internal/rules/direct.go) | 原发行账户真实 CAL 扣款、关闭缺口及费用推进 |
| `EvaluateReserveIncrease / ApplyReserveIncrease` · [reserve.go](../../internal/rules/reserve.go) | 转资增加公共 Grant；成员按累计差额增加本地份额 |

当前应用哈希 `APP_V4` 按前一应用哈希及规范排序的修改集推进，不是完整状态树逐键证明服务。公开执行结果认证与数据库全状态成员证明不能混称。

## 5. 存储状态与原子提交

| 保存内容 | 写入者与时点 | 原子边界 |
| --- | --- | --- |
| 钱包请求、消费标记、接收币 | 钱包发送／接收／跟块 | 请求与输入标记一起；接收验证后一次应用 |
| Candidate、Approval、Slice、Intent | 成员批准 | 同一事务检查最新状态并扣占；提交后签票 |
| 完整材料、outbox、Observed | 网关与成员后台／跟块 | 终态胜过迟到保存；备用期限只在首次安装确定 |
| Creation、Spend、Coverage、Promise、Obligation、Payment、Usage | 委员会规则 | 单笔 Overlay 隔离拒绝，按块归并提交 |
| 费用托管、账户、最终费用输出 | 委员会 | 与触发业务效果相同事务；退款身份属于原付款 |
| 本地进度及跟块 Cursor | 钱包／成员／网关 | 状态变化和连续游标一起提交 |
| Revision、Repair Task | 新块中的修复命令 | 扣款、关闭责任和修订授权一起提交 |
| 原始块、当前修订块、物化进度 | BlockStore／物化器 | 独立存储事务，依据已提交授权推进，不再执行经济效果 |

Store 后端可选内存或 bbolt，`Group` 合并已经排队的更新：每个业务回调看到前面成功回调的 Overlay；失败项不留下部分修改；底层批次提交后才返回成功。它减少物理提交次数，**不是把相互冲突的余额并行写入**。

回调只使用传入 ReadView，不在事务里重新进入同一 Store、调用网络或等待其他任务。停止接入后等待后台保存、relay、follower 退出，再关闭共享 Store，避免关闭期间丢任务或持锁等待。

内存模式仍有状态和原子事务；NoSync 仍写数据库，只是不等待同步耐久落盘。应用 DB、Comet BlockStore、WAL、FilePV 各有配置，不能把应用内存模式写成整个系统完全无磁盘操作。停机审计快照不等于可恢复数据库。

## 6. 跟块、权限与钱包同步

[finality/block.go](../../finality/block.go) 的 `VerifyBlock` 验证 H 区块与 H+1 签名头的网络、高度、验证者集合、LastBlockID、交易数据哈希和 LastResultsHash。普通 `/block`、`/block_results`、`/commit` 提供材料；必要后继结果块仍需产生。

[blockfollow/follow.go](../../internal/blockfollow/follow.go) 的 `Commit` 先准备可应用内容，再在 Store 事务内核对连续 Cursor、应用变化并推进游标。重复同块不重复应用；准备阶段不能以旧快照绕过事务内进度校验。

[member/blocks.go](../../internal/member/blocks.go) 将公共结果映射为：

- `applyPayment`：自己批准的付款记 Settled、MissingInputs 与费用阶段；标记公共输入消费，关闭先前等待当前输出的记录。
- `applyRepair`：在原发行批准中累计实际 Paid，并处理消费交易的 Pending 与修复费用。
- `finishLocal`：CAL 在自身 Settled 后恢复 `cap−Paid`；其余资源等待 Fee.Closed；仅恢复相对 Applied 的新增差额。

这三个函数是形式模型连接公共账本与本地重叠授权的关键。成员无需事先收到 INSTALL 才能处理自己已有批准的公共结果；未实际批准的成员也不能借跟块凭空恢复额度。

钱包按成功结果收集本金及费用输出，将已有币标记最终并保留消费标记；赔付没有新增用户收款。迟到实例 1 与正常实例 0 分开，晚到 TXCer 不能覆盖已确认的迟到实例。

## 7. 真实历史修订与共识适配

### 7.1 责任到修订的调用链

[redaction/repair.go](../../internal/redaction/repair.go) 的核心链为：

`InputTarget → InputShare／ReplaceInput → nextBody／PartShares → Execute → Materialize`

| 边界 | 核验内容 |
| --- | --- |
| `InputTarget` | 从公共义务定位消费交易／输入，检查 Open、到期、金额及当前正文版本 |
| 输入门限适配 | 三份有效贡献构造新 Funding opening，引用唯一 ReserveDebitIdentity |
| `nextBody` | 重新计算期望目标；精确比较除指定 Funding 外的全部交易内容 |
| 分片适配 | 约束受影响分片的承诺、位置和版本，不能仅凭块哈希相同放行 |
| `Execute` | 新块原子应用赔付与授权，保存修订正文和物化待办 |
| `Materialize` | 按已提交授权改写真实 BlockStore，旧版本或重复任务不二次扣款 |

输入承诺与分片承诺分别约束交易和区块传播表示，三份修订材料也不替代公共 BFT 对修复命令的确认。密码学模块使用 RSA 门限适配及上下文绑定；不提供 DKG 或 Go 大整数恒时保证。

### 7.2 Comet 的边界

[types/redaction.go](../../third_party/cometbft/types/redaction.go) 处理稳定交易／分片承诺；[store/redaction.go](../../third_party/cometbft/store/redaction.go) 的 `ReviseBlock` 需要应用授权回调、版本与正文摘要匹配，并验证 BlockID／PartSetHeader 不变。

首次改写保留原始块，`LoadOriginalBlock` 等接口供初始重放和同步。普通业务跟块读取原始执行版本，修订影响通过新块事件解释。最新物理正文、原始执行历史和经济状态是三个相关但不同的视图。

原有 BFT 投票轮次及法定人数保留，数据承诺和历史表示已经适配，因此不能与未修改 Comet 网络直接互通，也不能只引用原共识证明就宣布修订协议安全。

## 8. 实验扩展与协议模型分离

| 配置／工具 | 放在实现或实验层的原因 |
| --- | --- |
| 内存 Store、NoSync、Comet MemDB | 改变耐久性和性能配置，不改变输入和账务检查；需限制故障模型 |
| 静态缓存、编码复用、Group | 减少重复工作；要保持同样的验证结果和原子可见性 |
| 早发队列、并发配额、重试定时 | 改变调度；不能提前改变业务终态 |
| 等待 INSTALL 的交付门槛开关 | E5 消融基线；正常模式仍为后台 INSTALL |
| 序列化取证 gate | 默认关闭的 E2 实验选项；不把正常协议描述为逐笔等待全局串行 |
| 动态补资控制器 | `internal/reservecontrol` 的水位策略与 `cmd/payctl` 观测；真实管理命令才改变资金 |
| 负载器与观察器 | 测发送、快速接收、公共观察及成员完成；观测查询不属于私有委员会通知 |

不将这些策略和常量直接写成证明公理。抽象模型保留其必要前提，例如有限资源、材料可得和公平推进；实现到模型的对应关系另行验证。

## 9. 可追溯的测试与证明准备

以下是已有回归测试的机制映射，测试通过只支持覆盖场景，不替代一般安全证明。

| 机制 | 测试入口／代表用例 |
| --- | --- |
| 来源缺失仍执行，普通付款不登记新担保，赔付后迟到 | [rules/direct_test.go](../../internal/rules/direct_test.go)：`TestDirectChildBeforeParentAndImmediateSuccessor`、`TestDirectFinalPaymentDoesNotRegisterNewGuarantees`、`TestDirectRepairThenLateParent` |
| 成员拒绝与累计跟块 | [member/blocks_test.go](../../internal/member/blocks_test.go)：`TestInvalidPaymentRejectedBeforeMemberWrite`、`TestBlockFollowerMissingRepairLateAndDuplicate` |
| 早发与保存解耦，完成不复活 | [gateway](../../internal/gateway/)：`TestDirectInboxSubmitsAndInstallsWhilePersistenceBlocked`、`TestDirectInboxCompletedPaymentDoesNotRevive` |
| 公共投递不等 INSTALL、不带新证书，备用仍推进 | [gateway](../../internal/gateway/)：`TestDirectRelaySubmitDoesNotWaitForInstall`、`TestDirectRelayDoesNotSubmitNewOutputCertificate`、`TestMemberRelayEventuallySubmitsWithoutGateway` |
| 缓存不放过修改报文或跳过账本检查 | [committee/direct_cache_test.go](../../internal/committee/direct_cache_test.go)：`TestDirectVerificationCacheReusesOnlyIdenticalBytes`、`TestDirectVerificationCacheStillChecksLedger` |
| 真 BlockStore 修改、原始重放、未授权版本拒绝 | [redaction](../../internal/redaction/)：`TestRealBlockStoreRewriteAndOriginalReplay`、`TestConsensusRejectsUncertifiedPartRevision`、`TestPaymentRepairMonetaryReplay` |
| 补资水位与封顶 | [reservecontrol](../../internal/reservecontrol/)：`TestWatermarksAndCap` |

后续证明应先固定：[系统设计](system.md)中的初始资金和配置、SpendKey／Fact 等身份、原子事件、明确不遗忘假设及条件活性。随后把现有实现映射到模型，重点检查以下四条对应关系：

1. 本地批准事务与“签票之前已占用”的关系。
2. 公共 Overlay／Commit 与消费、缺口、费用和输出同时生效的关系。
3. 原批准、累计 Applied、公开结果与本地权限释放的关系。
4. 新块经济授权、旧块允许表示与原始重放一致性的关系。

本轮执行了带 `comet_v3` 标记的 protocol、rules、member、gateway、wallet、committee、blockfollow、redaction、finality、reservecontrol 现有测试，均通过（Go 使用已有测试缓存）；本轮未新增性能实验。

构建与运行见[运行指南](../operations.md)，精确字段见[实现参考](../reference/README.md)，论文证据见[E1–E8](../experiments/README.md)。业务代码、回归测试、Comet 补丁、实验驱动及原始结果都属于可复现材料；生成的二进制、数据库和上游展开副本不属于新增源代码。
