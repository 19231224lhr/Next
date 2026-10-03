# 局部批准额度回收与论文窄范围修订计划

> 执行方式：使用 executing-plans 在当前会话逐项实施；不启动子代理。实施结果见文末记录和实验报告；以下保留计划结构，并标明实际验证范围。

**Goal:** 对已由可信公共冲突消费排除的局部批准，安全、幂等地回收成员额度，并使论文规则、证明和验证证据一致。

**Architecture:** 沿用成员跟块事务及 `LocalProgress.Applied`。在成功付款覆盖输入消费记录前找到旧 Candidate，将其标记为失效并归还剩余预留；不可变 Approval 和拒签证据继续保留。公共结算、TXCer、委员会共识与历史修复流程不变。

**Tech Stack:** 仓库现有 Go、state.Overlay、store.Store/bbolt、blockfollow.VerifiedBlock；不增加依赖。

## 全局约束

- 基线：论文归档 `c3b20ac`；现有主实验构建 `721800c`。旧实验不能改标为新代码结果。
- 代码补丁已提交为 `bf71c4d`；定点回归与两组 Mac 短回归已完成。
- 只处理同一组织配置、同一输入实例、不同 TxID 的局部批准；跨配置不外推。
- 超时、HTTP 拒绝、INSTALL、停止补投均不能触发额度回收。
- 不释放“独立输入、相同 Intent”的证书责任，不修改公共余额，不恢复已支出的手续费或赔付本金。
- 不清除旧 Approval、其余输入 Candidate 或本地 Intent 绑定，不引入取消协议。
- 只做定点正确性验证和短回归，不重做实验矩阵。

## 代码核查与修复依据

`internal/member/blocks.go:applyPayment` 当前恢复的是成功付款自身的 Approval。随后普通输入及手续费输入被写为 `Spend{Consumed: fact}`，旧 Candidate 被覆盖，未回收其局部预留。

`internal/member/direct.go:ApproveDirectBytes` 已有 Approval 时直接走幂等返回并重新签名。因此仅增加 Available 不完整；失效状态必须在该快捷返回前检查。

`InstallDirectClassified` 会写 Consumed，但该事件不是公共成功执行；不能把任意 Consumed 当作回收证据。跟块 `Commit` 在同一个 Overlay 事务中写业务状态和区块游标，适合作为回收原子边界。

`internal/rules/direct.go:VerifyDirectSubmission` 验证当前组织 Authorization 的 SpendQC。准确前提是“成功公共付款具有同配置法定人数授权”，不是“委员会接收了新 TXCer 正文”。

`internal/member/outcome.go:Outcome` 已通过 `AppliedDebits` 计算残余。优先复用该路径，不另写一套审计金额公式。

## 选定机制

输入事件必须来自通过认证和顺序检查的区块，且执行结果 `Code == 0 && Applied`，为本组织 DirectSubmission。

1. 在覆盖每个普通/手续费输入的 Spend 前读取旧 Candidate。
2. 按确定的输入顺序处理不同于当前 fact 的旧 Candidate；同一 Overlay 中的 Invalidated 标记去重，不增加集合与排序。
3. 读取原 Approval，确认其确实包含所涉输入实例、配置相同、TxID 不同；不能仅信任一个裸 Candidate 指针。
4. 已失效则幂等跳过；尚未失效时要求未 Observed/Settled、无 Pending/Paid/Recovered 和实际费用活动，且不存在该候选自己的完整 Install/outbox 证据。矛盾状态返回错误并回滚整个事务，不做猜测性退款。赢家的 Install 不属于此禁止项。
5. 新增 `Invalidated`、`SupersededBy` 和独立 `InvalidatedHeight`；不复用结算 Height。为每笔原 Debit 使用其原 Grant/Worker，计算 `delta = Cap - Applied`。执行 `Reserved -= delta; Available += delta; Applied = Cap`，保留现有溢出和余额检查。
6. 失效标记、全部 Slice 差额、公共消费记录和块游标一次提交。不得把失效写成 Settled、Observed 或 Fee.Closed。
7. 重复批准先检查失效；恢复来源、`applyRepairEffect`、正常 `finishLocal` 与异常公开执行不得把失效批准重新投入工作。已存在合法 QC 却同时失效应作为不变量错误处理。采用集中状态守卫，避免多个入口各自误解释状态。

这里回收的是没有形成可兑现责任的资源预留。用户付费模式没有组织 FUEL/Policy Debit 时不凭空新增退款；组织代付模式只归还旧局部批准自己的预留。Bytes 是逻辑工作额度，旧证据仍保留，不能宣称释放了磁盘空间。

一次批准事务已经提交、但响应签名尚未返回时，可以与跟块交错。该迟到单票不构成第二份法定授权；测试应确认其后旧请求被拒绝。不要为了保证“网络上绝无迟到单票”扩大锁范围或阻塞签名路径。

## Task 1：先复现，再补最小状态转换

**Files:**
- 新增 `internal/member/partial_reclaim_test.go`。
- 修改 `internal/member/blocks.go`、`internal/member/direct.go`。
- 必要时新增 `internal/member/partial_reclaim.go`，只承载上述回收函数，不拆其他模块。

- [x] 构造真实四成员测试：M0 只批准 T；其余三成员为共享输入的 T′ 形成合法授权并通过真实执行器执行；M0 跟块。补丁前应准确失败在旧预留未回收，而不是测试构造或认证失败。
- [x] 新增失效状态和独立回收函数，复用 `localApplied` 的单调计数；两个输入循环均处理。
- [x] 拒签检查放在已有 Approval 快捷返回前。原 Approval 字节必须保持一致。
- [x] 用 T′ 的新输出生成后续付款，让恢复的 M0 加另两个成员形成三签，第四成员不参与。这样验证恢复的是容错容量，不只是计数器。
- [x] 执行 `go test ./internal/member -run 'PartialReclaim' -count=1`，保存失败基线与通过结果。

## Task 2：入口、幂等与负例

**Files:**
- 修改 `internal/member/source_recovery.go`、`internal/member/outcome.go`（只在需要显式展示 Invalidated 时增加字段）。
- 扩展 `internal/member/partial_reclaim_test.go`、`internal/member/intent_scope_test.go`、`internal/member/local_credit_test.go`。
- 审核 `cmd/payctl/direct_budget_audit.go` 的状态分类；金额仍通过现有共享读取，不做全盘审计重写。

- [x] 覆盖仅手续费输入冲突、OwnerFinalUTXO 与组织代付两种模式，按实际 Debit 集合验证。
- [x] 覆盖同一候选在多个输入命中、同块多次命中、重复块、关库重开，均只能恢复一次。
- [x] 覆盖 INSTALL 先到、批准先到随后跟块、失效后旧请求重试；不得重新签发或创建来源恢复任务。
- [x] 失效记录随后收到按 ParentFact 关联的修复效果时，不得增加 Paid 或继续正常核销；矛盾事件必须回滚，保持原 Slice 不变。
- [x] 保留并扩展不同输入同 Intent 的负例：两张合法证书可以并存，失败来源的责任及额度不得被本规则回收。
- [x] 负例包括跨配置、相同 TxID、失败公共结果、仅 INSTALL/超时，以及已存在赔付或执行状态的矛盾记录；不得归还额度。
- [x] 升级 `DirectStoreSchema`，阻止旧程序读取新增终态后沿旧幂等路径重签；实验使用新建目录，保留原实验数据，不做复杂迁移。
- [x] 执行 `go test ./internal/member ./internal/rules ./internal/blockfollow ./internal/gateway -count=1`；对成员相关测试运行 `go test -race ./internal/member -count=1`。

## Task 3：证明与论文同步

**Files:** `docs/paper/review-2026-10-02/` 下的 `protocol.tex`、`security.tex`、`supplement-security.tex`、`evaluation.tex` 及对应 `zh-*.tex`；涉及读者和版本措辞时同步 `introduction.tex`、`supplement-tables.tex`、`supplement-methods.tex`、`supplement-archived.tex`。

- [x] 补充引理：在固定四成员、最多一恶意、诚实成员不遗忘签署锁的条件下，两份三签授权不能消费同一输入实例。成功冲突付款的授权因此排除了旧批准取得授权的可能。
- [x] 资金覆盖中保留历史批准集合、令失效批准残余为零，并说明有效证书永不被该规则排除；新转换不改变任何公共经济状态。
- [x] 单列失效回收、正常结算和赔付三种状态，说明永久拒签及原子幂等。
- [x] 披露独立输入同 Intent 的重复预算消耗、有限授权/备付边界；不把补丁写成全面抗 griefing，也不声称未执行局部批准均可回收。
- [x] 保留 C3 为授权历史表示、完整身份保持和经济惰性机制；读取差异归于实测布局，不归因为变色龙哈希的固有加速。
- [x] 修正既有复审发现的读者范围、构建表、84,192 笔为 12 轮合计、C/P 并发上限叙述等材料一致性问题，不增加实验主张。
- [ ] Claude/GPT 实现双复核：GPT 已完成两轮实际代码、测试、双语稿复核。Claude 在计划阶段参与，但实施阶段订阅已过期且次数为 0，未取得新回复；这项外部复核没有冒充完成。

## Task 4：短回归与归档

- [x] 执行 `go test ./...` 与 `go vet ./...`，完成仓库要求检查；如某项环境不支持，记录原因，不能写成通过。
- [x] 沿现有驱动运行一次 100 TPS × 10 s 普通快速转账与一条 100 跳连续支付，仅验证回归；记录确切构建、配置、完成数、错误和审计结果。
- [x] 回收攻击轨迹的定点测试才是新规则证据。正常负载未触发该分支，不能证明其攻击恢复能力；短回归也不能证明旧性能数字在新构建上不变。
- [x] 编译中英文正文与补充材料，检查引用、表号和版本；同步最新 PDF/LaTeX 后再归档提交。
- [x] 报告逐项标明已实现、已测试、理论前提及留存边界；不自动合并未审完补丁。

## 暂不纳入

永久 Intent 冲突的 outbox 终止单独立项，不加入本补丁。它需要处理赢家先上链、失败任务后创建，以及网关 Early 内存任务等顺序；简单反向索引不能覆盖所有顺序。当前不改重试策略，不把一次 HTTP 拒绝作为可信终态。论文如实区分可最终执行实验中的排空与永久冲突时的持续重试。

不增加 A′ 读取实验，不改委员会消息协议，不修改担保本金模型，不取消输入锁，不增加统一全网 Intent 副本，不重测整个 E1—E8。

## 完成判据

同输入冲突正例恢复原 Worker 的预留并恢复三签能力；不同输入同 Intent 负例维持责任；重复、迟到、重启不产生二次恢复或重签；论文与代码一致，旧实验版本明确。达到这些条件即可结束本轮窄范围修订。

## 2026-10-03 联合讨论裁定

Claude 与 GPT 均收到同一实际代码包，先独立回复，再交换分歧。Claude 完成两轮补充核对；GPT 完成一次交叉裁定。

| 问题 | 最终选择与理由 |
|---|---|
| 是否需要补丁 | 需要。已读跟块路径缺少旧 Candidate 对应批准的额度回收；只在论文披露不够。实际回归复现留给 Task 1。 |
| 失效数据放哪里 | 采用 Claude 的 LocalProgress 方向，吸收 GPT 的独立 InvalidatedHeight；不增加通用 FactTerminal 框架。 |
| 是否扩展 outbox | GPT 初版建议同轮实现，经讨论明确接受独立后续项；Claude 同意披露当前边界。本轮不改变 outbox。 |
| 回收检查 | 吸收 GPT 的原 Approval 输入核对、完整证据/经济进展矛盾检查，以及 Claude 的修复效果入口拒绝失效状态。 |
| 额外验签 | 不增加。SpendQC 不能替代可信公共执行事实；继续使用已认证跟块边界。 |
| 实验规模 | 双方均不要求重跑主实验矩阵。选择定点测试加普通负载、连续支付短回归。 |

原始记录见 [本轮复审目录](../../paper/review-2026-10-02/rereview-2026-10-03/README.md)。两位的支持是对计划及其前提的评议，不能写成已修复、测试通过或期刊录用结论。

## 执行结项记录

- 分支 `fix/partial-approval-reclamation`，代码 `bf71c4d`，尚未合入 `re`。
- 真实四成员、三票成功公共冲突复现及修复，恢复后续三签能力；所有定点正反例通过。两组 Mac 短回归完成，详细配置、测量及代码指纹见 [报告](../../experiments/partial-reclaim-2026-10-03/README.md)。
- 用例组织与计划略有调整：新增独立 internal 状态守卫测试，未修改 local_credit_test.go；同一 Overlay 标记完成多输入去重。没有为迟到单票增加生产锁，也没有增加专用暂停签名钩子；批准与跟块的原子边界经代码核查，race 测试另行通过，不将其称为穷尽调度验证。超时不释放由事件入口核查，未增加计时等待测试；失败公共执行、INSTALL 本身不回收均有定点验证。
- Windows protocol 测试进程被 OS 拒绝启动，Mac 完整应用、race、vet、Comet 定向回归和安全模型测试补齐；两种平台结果分别披露。
- 新引理与原资金覆盖证明衔接，失效来源恢复按会计矛盾停止 Follower。永久 Intent 冲突 outbox 终止仍按双方计划裁定单列后续，不扩大修复。
- 历史主实验保持 `721800c`，新短回归为 `bf71c4d`，不宣称性能不变或全实现机械化证明。
