# 完整大修：证据与执行记录

实施计划见 [A–G](../../superpowers/plans/2026-10-03-complete-paper-major-revision.md)。本轮定向边界、组合重放与12轮网络实验完成；中英文正文、补充材料及本地源码包已编译核对。Overleaf最后同步已完成，56个源码/图表核对一致、四根在线编译通过，见[交付核查](../../paper/review-2026-10-02/build-verification.md)。本目录保留成功、预期拒绝和夹具调试记录。

## A：版本与影响

`721800c → 841e5ac → 0d90417 → 35b4d5f → c3b20ac → bf71c4d → 9879f64` 的祖先关系已由 Git 核实。`9879f64` 为本轮起点，当前隔离分支为 `fix/partial-approval-reclamation`。721800c 之后生产路径差异在成员批准/跟块/来源恢复；payctl 增加审计和测量，历史读取文件为测试。`third_party`、go.mod/go.sum 没有提交差异。磁盘 overlay 和实际新二进制仍在新实验生成时分别取指纹。

| 主张/问题 | 规则与代码 | 可复用证据 | 本轮缺口/影响 |
|---|---|---|---|
| 当前组织认证及所有者授权 | rules.VerifyDirectSubmission、protocol.FastTx.VerifyAuth、committee.Engine.ExecuteAt | direct_submission_test、direct_authorization_test | B0 所有入口/错误组织/费用授权负向检查 |
| 不同组织同 Subject/Nonce 冲突 | member.ApproveDirectBytes、rules.EvaluateDirectPaymentAt | member/intent_scope_test.go | B1 两轮后继消费与公开赔付，完整经济账目 |
| 局部批准回收 | member/partial_reclaim.go、blocks.go | partial-reclaim-2026-10-03 全部定向日志、Mac短回归 | B2 区分无QC碎片化和同输入分裂；不重复声称所有停顿已解决 |
| 资金覆盖/费用 | rules/direct.go、direct_fee.go、reserve.go | security_projection_test、reserve_test、owner_fee_test | 不重叠累计成员本金，核对净损失与历史Paid |
| 公开赔付与表示分离 | redaction/decision.go、batch.go、repair.go | compensation-before-adaptation、payment monetary replay、历史读取实验 | C 新构建组合顺序和保留状态追赶 |
| 连续支付主测量 | payctl chain、论文VI-A | 721800c 冻结数据，841e5ac归档 | D2 原始时间戳/轮询解释；如生产变化影响则重测对应比较 |
| 混合负载 | payctl e5-load、修复服务 | 0d90417归档的721800c数据 | 不更换数据版本；新故障验证单列 |
| 历史读取成本 | redaction/reader_cost_test.go | 35b4d5f归档 | E 可执行消费者接口，不能以微小读取差异证明必需性 |

## 已执行

- Mac SSH 可达，实验开始前没有 `/Users/richz/lab/man` 下运行的实验进程。
- 本地目标基线测试通过：[普通构建](baseline-targeted.log)、[comet_v3构建](baseline-comet.log)。没有据此声称全项目/竞态检查已完成。
- 图谱已索引。B0 静态检查：wire4的ExecuteAt分派只接纳DirectSubmission、补资、时钟及修复命令；普通DirectTransfer不进入该付款分支。旧DirectTransfer规则仅接受CommitteeRoute。FastTx所有者校验要求输入OrgRoute与Certifier一致；当前QC验证绑定配置和付款摘要；组织付费通过ResourcePolicy的Subject授权。

## 本轮结果与证据

### B：真实批准、公共执行和经济边界

主证据为 [boundaries-reviewed.log](boundaries-reviewed.log)、[逐状态账目](boundary-summary.json) 和 `internal/redaction/revision_boundaries_test.go`。早期 `boundaries-*.log` 是夹具/断言调整记录，不是最终结果。夹具使用真实成员签名、Engine/ABCI、同步提交及带下一高度认证的公共块；没有直接改业务状态伪造 QC 或释放额度。

| 场景 | 观测 | 说明 |
|---|---|---|
| 四个独立 100 CAL 请求，各两票 | 四成员每人预留200、可用0；无QC，公共Reserved/Spent均0 | 局部签名占用不是四份真实本金，也不能靠空闲自动解锁 |
| 从未保护来源补资150 | 备付300→450，每成员容量200→300；保留原锁，同请求补齐后完成 | 增加授权可解容量碎片化，不是回滚旧批准 |
| 同输入2/2分裂后补资 | 余额和可用容量增加，冲突锁仍使两个候选无法补齐 | 增资不能解决唯一消费约束引起的停顿 |
| 同输入已有公共成功赢家 | 复用bf71c4d回收测试；一次恢复局部额度，保留原锁、批准、无效标志 | 不推广为所有无QC候选的通用解锁 |
| 两组织不同输入、同Subject/Nonce，两个Intent冲突轮 | 目标A备付300→200→100；三名原签署成员累计Reserved200，可用0；第四成员仍有200 | 可有真实担保损失与成员停服；不违反已承诺责任覆盖 |

Intent 轨迹中，B 先公开执行，A 的合法 QC 所对应付款因已有全局 Intent 而拒绝；B 对消费 A 输出的后继付款提供当前组织认证，委员会登记 A 的直接义务并到期赔付。第一轮后继输出用于第二轮 A 付款，B 每轮使用新的原始输入；末轮后继再次通过 B 成功消费。A 原始 CAL 与费用输入公开未消费，但在原成员处仍锁定，不能将后继成功解释成原输入也能重新花。

用户付费与已授权组织付费各执行一遍。用户付费版两轮保留整笔 FUEL 输入合计20,000；这是输入锁定的面额，不是已烧掉的费用。五笔成功付款合计最大预留5,000 FUEL，最终实际费用480（奖励430、销毁50）、退款4,520、Held=0。普通成功付款94、带赔付后继99。组织付费版实际480记在B组织费用账户；A没有成功公开收费，但本地签名费用容量可能仍被保留。不能由“FUEL已足够”推出任何人可让组织代付。

每个提交前缀分别核对：全部Open义务金额=Gap、证书Paid/Recovered/Remaining/Discharged、grant Reserved/Spent、备付对净已用授权覆盖、CAL总量、FUEL余额/托管/奖励/销毁。重复决定经过真实执行入口和引擎均无新余额、费用或责任效果。

### B0：公开入口与守卫对应

| 入口/性质 | 实际代码守卫 | 测试 |
|---|---|---|
| wire4只接受对应付款入口 | `committee.Engine.ExecuteAt` 分派；旧路径仅CommitteeRoute | `TestRevisionUnauthorizedSponsorAndLegacyEntry` |
| 当前组织三票与摘要绑定 | `rules.VerifyDirectSubmission`，重构认证摘要、配置、父证据映射 | `TestPublicSubmissionRejectsMissingOrUnrelatedOrganizationApproval`直接调用接收端验证，无票与错误QC均ErrAuth；另有0/2票编码拒绝 |
| 所有者及路由绑定 | `FastTx.VerifyAuth`要求输入OrgRoute与Certifier相符 | `TestPublicSubmissionStillRequiresOwnerAuthorization`保留有效组织QC但改所有者授权，公共与INSTALL均拒绝 |
| 输入证据必须一一对应 | `VerifyDirectSubmission`父证据与输入匹配 | `TestPublicSubmissionRejectsUnusedInputCertificates` |
| 费用来源合法 | `ValidateDirectFee`及ResourcePolicy.Subject；费用输入锁 | `TestRevisionUnauthorizedSponsorAndLegacyEntry`、`TestDirectOwnerFeeRejectsInvalidFundingAtomically` |
| 持久批准后才能签名 | `member.ApproveDirectBytes`原子写批准、输入、Intent、额度，再返回签名 | `TestDirectDurableApprovalAndBackgroundInstall`、本轮`approval_boundary_test.go` |

一般性排除容量不足的条件是逐成员/Worker `C ≥ L + N·a_max`；N必须涵盖局部批准、已获证未释放及尚未跟块释放，不只是压测器当前在途数。这是受控负载条件，不是开放collector的协议限流保证。固定无补资grant下，净未偿还损失受覆盖上界约束；历史累计赔付不受同一常数界限约束。

### C：组合、持久化和历史重放

[composition.log](composition.log) 和最终边界日志覆盖“赔付→表示→回款”“赔付→回款→表示”，实际三份门限适配、完整BlockID/parts、旧前缀、预先签好的后继不变及至多一次经济效果。副本在高度2关闭并重新打开真实Bolt，从已经保存修订表示的BlockStore取保留原始材料追赶；逐高度应用根与全部账本行一致。这是**本地保留状态重启/原始历史读取重放**，没有另行测试远端下载协议。

[approval-boundary.log](approval-boundary.log) 在真实Bolt.Update提交成功但返回签名之前用测试屏障中断，重开库后原输入冲突仍被拒绝、Reserved仍100，同一事实重试不重复预留。另复用`TestCursorAndApplicationCommitTogether`、`TestBatchCreditIsAtomicAndRetryDoesNotRefundTwice`、`TestLocalCreditRetainsLegacyCumulativeAcrossRestart`及PartialReclaim重启/拒绝测试，分别检查cursor与业务原子性、恢复至多一次及累积记录保留。

### D：网络与计时口径

新网络矩阵见[完整报告](network-report.md)、[逐轮CSV](network-runs.csv)、[分析JSON](network-summary.json)。12轮全部闭合，但委员暂停显著增加公共等待；不是“故障毫无代价”。离线区块验签核对见[提案覆盖](consensus-fault-coverage.json)：每轮五次故障节点round-0机会，均由round-1提交推进。

旧 `721800c` 600跳时间戳按[measurements.py](measurements.py)重算：[waiting-analysis.json](waiting-analysis.json)、[逐跳CSV](waiting-hops.csv)。等待模式每轮99跳的最早委员commit到压测器观察区间合计29.35–29.68 s；包含下一高度认证、获取验证、记录/观察及副本差异，不能全称为轮询耗时。14.15倍是所测端到端模式之比，不是纯共识加速比。

旧混合负载84,000笔普通交易逐笔重算`FastUnixNS−ScheduledUnixNS`，不相加两个P95：[脚本](analyze_archived.py)、[CSV](mixed-scheduled-receipt.csv)。N/R/C/P的计划时刻至到账P95（三轮统计量中位数）分别304.253/140.007/475.236/666.915 ms；相应实际发送至到账P95为152.766/139.894/136.491/144.205 ms。两种口径回答不同问题。第三轮原始wall-clock出现少量提前记录（最小−13.104 ms），CSV保留数量与原值，没有夹成零；不由这些时间戳推断微秒级调度精度，也不声称已区分计时器唤醒与时钟校正。

### E：可执行历史消费者和密码参数

[history-export.json](history-export.json) 与[测试](../../../internal/redaction/history_export_test.go)提供本地原坐标字节导出，返回原始/授权body、完整BlockID、Revision、Task、资金字段及经济状态，检查精确Task和原承诺。回款后资金表示不变，经济状态变为Recovered。它证明一个可执行接口，不证明已存在外部生产用户。

Next采用2048-bit RSA CH、e=65537、SHAKE256拒绝采样到单位群。CIRCL v1.6.5的GenerateKey生成不同安全素数并求e逆元，可信dealer分发3-of-4份额；Next公开构造器只检查N位数/奇数和e，不证明模数结构。适配是公开hash比值的原群门限RSA，最终验证等式。决策、精确Task及完整身份守卫提供修改授权，不把公开可组合比值当作每次修改的独立授权证明。论文补充材料列出编码和假设。

## 验证与版本归属

[全项目测试](final-tests.log)与[go vet](final-vet.log)均通过，空vet日志表示exit 0；新增离线历史导出额外逐轮验签通过。未将测试夹具修改说成协议修复，未将旧性能改标成9879f64。生产路径无新改动，payctl只增加选择测试输入的参数；正文和补充采用对应版本分别报告。
