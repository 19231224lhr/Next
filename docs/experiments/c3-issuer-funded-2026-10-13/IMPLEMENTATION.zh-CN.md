# 新 C3 实现说明

本轮实现把用户报价与组织承担的处理费用分开：付款签名和 TXCer 固定用户报价，组织在签名前准备 CAL 本金保障与 FUEL 处理预算；公共账本按实际发生的阶段使用预算。来源迟到的费用记在来源签发方，不向下游付款人加收。

## 代码入口

| 部分 | 文件 | 实现内容 |
|---|---|---|
| 凭证承诺 | `protocol/c3.go`、`transaction.go`、`v3.go` | 绑定规则版本、报价、实际付费人、签发方授权池、资源上限、预算和固定报酬；所有金额计算检查溢出。旧模式保持原编码。 |
| 费用状态机 | `internal/rules/c3.go` | 按稳定认证事实建立唯一状态；分别记录 Held、Burned、Rewards、Released；输出级事件去重。 |
| 签发与公共结算 | `internal/rules/c3_policy.go`、`c3_ledger.go` 及既有 direct 路径 | 注册并认证授权池；签名前复用额度预留；登记、执行、赔付、偿还与费用变动原子提交。 |
| 状态观测 | `internal/committee/c3.go` | 查询单凭证费用档案和授权池收支；区分池级与全局报酬/销毁。 |
| 客户端与配置 | `cmd/internal/config`、`cmd/payctl`、`cmd/gateway`、`internal/wallet` | 生成、签署、传递、验证费用承诺；接收钱包使用同一受信策略；提供 `init-lab --c3`。 |
| 实验客户端 | `cmd/payctl/c3_fees.go` | 真实成员签名、收款钱包核验、委员会提交、已验证区块观察、费用找零核对及重放检查。 |

具体文件清单以本轮 Git 提交为准。没有改变 C1 的认证轮次；旧历史修复仍在后台执行。

## 四项性质的实现证据

| 性质 | 核心自动化测试 | 检查内容 |
|---|---|---|
| F1 费用覆盖 | `TestC3MembersEngineDurableHiddenReservations`、`TestC3ConcurrentHiddenApprovalsAndAtomicPolicyFailure`、`TestC3FundedPoolSnapshotAndTopup` | 未公开和部分批准保留额度；并发预留与失败回滚；真实补资；成员和账本重启后按已验证区块恢复。 |
| F2 用户净费用确定 | `TestC3OwnerQuoteIndependentOfResolution`、`TestC3PublicLifecycle`、`TestC3FeeInputMustMatchExplicitPayer` | 三种执行顺序收取同一报价；显式付款人与费用输入匹配；所有者和赞助支付路径。 |
| F3 预算充足、事件只收费一次 | `TestC3AllTwoOutputPathsConserveAndRejectCorruption`、`TestC3MultiOutputDifferentQuorumSubsetAndLateRepay` | 多输出路径；不同签名子集仍对应同一档案；赔付后保留偿还预算；重放不重复收费；拒绝损坏状态。 |
| F4 异常不增发报酬 | `TestC3SixUnitPaths`、`TestC3PartialResolutionAndImmutablePlan` | 来源履行和赔付共享完成奖励；额外阶段费进入销毁；拒绝修改已绑定的金额和受益者。 |

`TestC3CrossOrgChargesDirectSourceAndBoundsGrandchild` 还检查跨组织费用归属，以及每张凭证只承担其直接输出责任，不递归承包整条后继链。原 CAL、付款、修复回归测试继续运行。最终执行命令和结果见实验报告与测试日志。

## 保留的协议边界

费用策略目前采用创世配置中冻结的受信注册表，可预置多个接受的规则版本；签发选择新版本不会改变旧凭证的承诺。本轮没有添加链上费率治理或热更新交易。组织收到的服务收入也不会自动变成新的签发额度，补资必须有真实 FUEL 转入。

费用预算不是额外发行的资产。已销毁或支付给委员会的 FUEL 不随 CAL 偿还恢复；只有确定不再使用的余额才能释放。历史修复不重置收费标记。

## 仍需完成的论文证明

自动化测试和真实运行提供实现证据，论文仍需完整写出以下论证：

1. 在既有四成员、至多一名拜占庭成员的假设下，以重叠额度和持久批准为基础，证明所有有效凭证（含未公开凭证）的已支出与未结费用总和不超过真实备付。需覆盖部分批准、补资及配置迁移。
2. 对公共原子状态转换归纳，证明用户净支出等于其成功执行付款的已接受报价之和。
3. 证明每种合法多输出状态均满足预算分解和剩余费用上界，事件标记在重启、赔付、偿还和修复后仍保持唯一。
4. 证明所有处理顺序下的报酬不超过凭证预定计划。该性质排除因异常阶段直接多领报酬，不等价于一般性的博弈均衡证明。

上述证明的详细推导提纲保存在 `proof-obligations.md`；本轮没有把实验成功表述为形式化证明已经完成。
