# C1、C2 与终稿 C3 的证据对应

本文整理 2026-10-16 冻结实现与实际结果，供实验报告及论文引用。终稿采用 C3 v2：用户支付已签名的 CAL 报价，签发方预留 FUEL；正常乱序只结算固定报酬，comp、repay 进入排除付出组织的回流池。实现说明见 [implementation.md](implementation.md)，两项已修复的独立审查问题见 [review.md](review.md)。

## 已完成的证据

**代码测试。** [final-tagged-tests.log](final-tagged-tests.log) 记录冻结测试运行各包通过；具体测试名称及断言可在下文所列源码中核对。成员对象、账本引擎、Bolt 重启及验证区块的组合测试属于进程内集成测试，即使使用真实执行函数，也不计作独立服务实验。测试文件中的旧 `TestC3…` 用例用于兼容回归；终稿费用结论主要依据 `TestFinalC3…`。

**真实服务。** WSL 原始目录为 `/home/qyinf/next-c3-final-20261016/formal-orders/`，每次运行保存 `result.json`、`manifest.json`、`reports/c3-fees.json` 和各服务日志。已核对 15 个 `result.json` 的退出码为 0、错误为空，以及对应报告的 `FUELConserved=true`。三种顺序各完成 5 次，结果如下；表中数值是一对来源/后继凭证的合计，并非每张凭证的费用。

| 真实服务路径 | 完成次数 | FUEL 累计支出 | 委员会总报酬 | 超时费用累计回流 | 结束时 Held |
|---|---:|---:|---:|---:|---:|
| source-first：来源先执行 | 5/5 | 8 | 8 | 0 | 0 |
| child-first：后继先执行、来源随后履行 | 5/5 | 8 | 8 | 0 | 0 |
| compensated：赔付后来源迟到偿还 | 5/5 | 10 | 8 | 2 | 0 |

赔付路径中，来源签发方支出 6，后继签发方支出 4；来源承担的 2 单位超时费用划给另一组织。全网奖励账户应只统计一次，不能把两个池快照各自展示的全网 `Rewards=8` 再相加；累计 `PoolReflow=2` 也不能与已经划出的信用重复计入存量资产。报告中的 `EpochSettlements`、`FinalPools` 和各阶段快照用于核对这一点。

代表记录为 `formal-orders/002-r1-final-compensated/reports/c3-fees.json`：它依次保存 `child-executed`、`compensated-before-source`、`closed` 和 `history-repair-observed`，并记录四个委员会节点的修复提交/物化观察。历史修复因此已有真实服务证据。该记录的重复提交结果是 `HTTP 503 / SUBMISSION_FAILED`、`Outcome=http-rejected`；提交前后费用快照相同，只能支持“拒绝且未新增入账”，不能称为已执行重复命令的幂等证明。

## 性质与证据映射

下表把“测试覆盖的具体状态”“服务中实际发生的过程”和“需要证明的一般结论”分开。C1/C2 的既有测试说明其核心机制仍在回归范围内，终稿专用测试则核对加入 CAL 服务费和 FUEL 预留后的组合行为。

| 性质 | 具体代码测试 | 已完成真实服务证据 | 仍需形式证明的结论 |
|---|---|---|---|
| **C1：凭认证输出继续付款** | `internal/rules/direct_test.go` 的 `TestDirectChildBeforeParentAndImmediateSuccessor` 在来源未执行时构造后继并继续执行下一跳；`internal/member/direct_test.go` 的 `TestDirectDurableApprovalAndBackgroundInstall` 检查持久批准与本金额度；`internal/committee/c3_final_test.go` 的 `TestFinalC3MembersLedgerAndReservedSource` 使用三个成员实际批准隐藏来源与后继。 | child-first 与 compensated 运行均取得后继认证并在来源公开执行前使用来源输出。它们展示具体可行路径，不直接给出任意链长或网络延迟下的时间界。 | 门限交集、本金重叠额度及输入唯一性共同保证所有有效认证责任有覆盖；服务费不是可续付输出，不增加本金签名责任。认证可继续性与共识活性条件需分别陈述。 |
| **C2：后继可先公开执行，直接义务最终清偿** | `TestDirectChildBeforeParentAndImmediateSuccessor`、`TestDirectRepairThenLateParent`；`internal/rules/source_recovery_test.go` 的 `TestLateSourceRepaysReserveWithoutSecondUserOutput`、`TestSourceRecoveryAllFourHopOrders`；`internal/rules/c3_final_test.go` 的 `TestFinalC3OrderAndTimeout`。 | child-first 的缺口随后由来源履行；compensated 记录赔付、迟到偿还、义务关闭和历史修复观察。 | 每个输出至多一个直接义务、至多一次赔付/偿还；CAL 守恒及来源延迟中立。终稿需将 CAL 经营账户收入纳入守恒量和执行顺序比较对象，历史修复不重置经济清偿。 |
| **F1：包含隐藏批准的 FUEL 覆盖** | `TestFinalC3MembersLedgerAndReservedSource` 检查隐藏预算耗尽后拒签、成员/账本重启后仍拒签、未用预算释放后恢复；`TestFinalC3ConcurrentHiddenApprovals` 检查并发批准；`TestFinalC3MemberCreditOnlyAddsObservedDelta` 与 `TestFinalC3EpochCreditBeforeFirstTopupReplay` 检查公开回流信用及补资重放。 | 15 次运行中签发、公共托管及清偿正常完成，赔付运行观察到回流进入另一组织池。当前这些顺序实验没有专门施加多成员并发隐藏凭证集中出现的服务压力。 | 对全部批准（含未组装和扣留凭证）证明 `Λ+M≤floor(2Φ/3)` 的重叠额度上界，且仅已核实注资提高额度、仅已确定未用的预算恢复额度；持久化/崩溃不破坏该不变量。 |
| **F2：用户成本确定，收款全额到账** | `protocol/c3_final_test.go` 的 `TestFinalC3PlanAndWire`；`TestFinalC3CALFeeIsAtomicAndSigned` 检查报价签名、执行收费和重复执行；`TestFinalC3FailureLeavesNoEscrowOrFee` 检查失败转换回滚；`TestFinalC3OrderAndTimeout` 比较顺序与超时。 | 正常和赔付两类路径均使用终稿费用账目；来源的额外 FUEL 支出没有变成后继签发方费用。CAL 收入和输出应引用报告中实际报价及阶段快照，不能把 8/10 FUEL 当作用户成本。 | 对同一付款集合、同一报价且都成功执行的运行，证明用户净成本恒为其 CAL 报价之和；输入等于输出加服务费，费用只随成功执行原子转入经营账户，不从指定收款额扣取。 |
| **F3：剩余预算充足、事件只计一次** | `TestFinalC3MultiOutputQuorumDedupAndLateSource` 覆盖多输出、替代签名集合、部分赔付后迟到来源；`TestFinalC3PriorityHeldThroughCompensation` 保留迟到执行的优先报酬；`TestFinalC3FailureLeavesNoEscrowOrFee` 检查原子性；上述成员/账本重启测试及 `TestFinalC3EpochCreditBeforeFirstTopupReplay` 覆盖重复区块与重启信用。 | compensated 各阶段保存未完成时预算和最终 Held=0，且历史修复已观察。HTTP 503 重提没有进入重复执行，执行层幂等在此仍由代码测试支持。 | 对所有合法转换归纳证明 `M=D+H+U`、`H≥MaxFutureCost`；认证事实与输出阶段键稳定，签名集合或历史表示改变不能产生新费用权益，支出与释放不重叠。 |
| **F4：委员会报酬上限与受益者绑定** | `TestFinalC3PlanAndWire`、`TestFinalC3PriorityHeldThroughCompensation`；`TestFinalC3WeightedRewardsConserveAtUint64Limit` 检查加权整数分配及溢出边界；`TestFinalC3MembersLedgerAndReservedSource` 核对纳入者/共享奖励总额。 | 三条路径委员会总报酬均为 8；赔付没有使它增长到 10。个别验证者收入可因实际出块者不同而变化，不能据此要求各验证者逐人收入顺序不变。 | 固定计划下，逐证书证明 `PaidReward≤rFixed+π`；实际纳入者绑定到可信区块元数据，委员会权重绑定到可信配置，各阶段不得重复支付。 |
| **F5：正常乱序中立、超时直接归责、报酬有界** | `TestFinalC3OrderAndTimeout`；`TestFinalC3EpochPayerExclusionAndDust`、`TestFinalC3SolePayerRetainsFees`；结合 F3/F4 的多输出和报酬测试。 | source-first 与 child-first 均支出 8、报酬 8、回流 0；compensated 的额外 2 由来源承担并分给另一组织，委员会仍得 8。 | F5a 对完整正常执行逐签发方比较支出并证明总报酬相同；F5b 证明回流始终保留付出方标记且排除该方；F5c 由 F4 导出。保证来源不会被他人延迟到超时，还需额外的有界纳入和补交时延假设。 |
| **F6：FUEL 总量守恒** | `TestFinalC3EpochPayerExclusionAndDust` 检查取整余数、跨纪元留存、重复边界；`TestFinalC3SolePayerRetainsFees`；`TestFinalC3EpochWeightsIgnoreLastBlockTopup`；`TestFinalC3WeightedRewardsConserveAtUint64Limit`；`TestFinalC3EpochCreditBeforeFirstTopupReplay` 检查信用与补资相接、重复/重启不增资。 | 15/15 报告 `FUELConserved=true`；正常无回流，赔付费用通过真实纪元结算进入另一池，结束时无剩余托管。两组织实例没有验证多接收者产生的小数余数。 | 对注资、托管、奖励、超时入池、释放、纪元划转逐项证明全网存量恒等于创世总量；累计支出/信用不能重复算资产；整数余数及原付出方标记跨纪元保持，权重使用明确的区块边界采样语义。 |

## 后续实验补充的证据

**来源延迟。** `formal-delays` 已完成 0、500、1500、4000 ms 各 5 轮，每轮 20 对付款，共 400 对，全部最终闭合且 FUEL 守恒。此实验保留认证后暂不提交的来源，并保存成员预留与公共托管的定期快照。4 秒组按实际结果记录正常履行或赔付后偿还，未把请求延迟直接当作确定的公共执行顺序。

**拥堵调度。** `formal-congestion` 已完成 0.01% 与 25% 保留比例各 5 轮。每轮 60 笔基础/优先付款、4 笔建立缺源状态的后继及 4 笔来源补交全部闭合，费用守恒。逐请求提交时间、实际纳入高度、付费档及费用记录见原始报告。客户端 outstanding 曲线已另存，不称为节点内存池长度。

**持续与连续负载。** 终稿的 100/1000 笔依赖链各 7 轮均通过收据、输入关系、本金和最终闭合校验；固定混合负载 5 轮中所有正式实际发送付款都完成闭合。独立付款、RTT、有限额度和暂停的具体轮次及失败状态见动态结果表，不以本清单替代原始数据。

**仍只有集成测试的部署边界。** 三组织小额余数跨纪元回流、回流后首次补资及成员重启，尚未单独组合成真实多进程场景。`TestFinalC3EpochCreditBeforeFirstTopupReplay` 等回归覆盖了信用与补资的衔接；这不等于该组合已经完成部署实验。

这些服务结果支持具体执行路径的正确性。论文中任意执行序列、任意合法并发和所有故障恢复路径的结论，仍由相应不变量证明承担。
