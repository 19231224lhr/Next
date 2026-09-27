# 完整安全分析：文献、模型与实现证据

[完整报告](../security-analysis-complete-wire4.md) · [论文第五章](../../paper/security-analysis-chapter5.md) · [前轮组合证据](../security-composition-wire4.md) · [R6 证据](../security-redaction-validation-2026-09-27/README.md)

审查基线为 `security-analysis@a6ebeb2918bfdc191d40daf0fc64fb5f7cffa827`，实验对照分支保持 `re@8d1590ba559eeb23548f2bf598f064f490553330`。本轮在 Windows amd64、Go 1.27.1 上分析和验证；没有重跑或改写 Mac 的 E1–E8 性能数据。

## 1. 本轮解决的论证问题

原来的“任意诚实签署者都保留 r≥w”过强。第三诚实成员可以在尚未批准父交易时跟过其输出的赔付，再补签父交易。跟块只更新确有原 Approval 的 Paid，因此该迟到成员在父成立后按本地 Paid=0 释放。这是实现允许的轨迹。

修订后的证明选取**最早凑齐三份签名的固定见证集合**，其中至少两名诚实成员的批准必早于赔付或父成立。对这些成员成立 r≥w；对所有批准仍检查残余和、Worker 总占用与授权上限。该修订既不要求在线识别见证，也不新增历史扫描或线上检查。

这里检出的是旧证明的量词错误；新增轨迹未发现真实备付不足。模型的通过也不能单独证明任意规模的实现安全。

## 2. 模型范围与结果

| 检查 | 有限范围 | 结果 |
| --- | --- | --- |
| 原分解模型 | 原 16 配置：投票、生命周期、账户与费用 | 268,965 状态、1,227,450 转移，全部完成；原七类错误仍产生反例 |
| 迟到批准扩展 | 一份两输出凭证，三诚实成员及一拜占庭签署者；两名早期诚实批准者，第三人可在任意已观察前缀后补签 | 393,718 状态、1,848,009 转移，搜索完成；见证残余、双计数与预算界未违例 |
| 过强全称断言 | 同上，但把 r≥w 施加给所有后来批准者 | 找到 7 事件反例，见下方 |

两组状态数属于分解的模型运行，不能相加后宣称完成了一个包含全部系统的联合穷尽。模型不执行签名、网络、Comet 或真实磁盘。一般前缀结论来自报告中的条件推导，测试只检查声明范围。

最短反例：

```text
consume-missing(o0)
repair(o0)
source-arrives
follow(m2,event1)
follow(m2,event2)
late-approve(m2,prefix2)
follow(m2,event3)
```

父交易已经公共成立，但 m2 尚未观察到该事件，因此仍可在它自己的合法前缀上新增批准。观察父事件后，它释放本地占用；早期见证成员仍保留赔款。完整搜索输出见 [models.txt](models.txt)，模型源码见 [lifecycle_test.go](../../../tools/securitymodel/lifecycle_test.go)。

## 3. 真实代码回归

| 用例 | 检查什么 | 不代表什么 |
| --- | --- | --- |
| `TestSecurityComposition` | 原 8 场景加 Worker=1/3 的两项迟到补签；真实成员批准、真实 QC 验证、每步权限／账户对应 | 不执行完整网络 BFT 或门限物化 |
| `TestBlockFollowerMissingRepairLateAndDuplicate` | 11 场景，包括新加入的两项经济投影及一项迟到签署；原消费、返还、费用检查仍保留 | 不说明普通块证明认证最新可变正文 |
| `TestBlockProofAuthenticatesProjectionNotMutableHistory` | 相同固定交易与结果下，可变 Funding/opening 不改变普通块证明；结果篡改被拒绝 | 接受经济投影不是接受该正文作为新的合法付款 |
| `TestDirectVerificationCacheReusesOnlyIdenticalBytes` | 原有缓存回归重新执行：所有者、Funding、opening、QC 任一变化不复用原认证 | 缓存不免除执行时的动态状态检查 |
| `TestDirectOwnerFeeRefundAfterParentArrives` | 父未提交、自付 FUEL 的自转产生同所有者的新最终 CAL 输出，并建立直接义务；父后到正常退款 | 是既有规则的条件路径，不是成员服务或网络公平性证明 |

详细输出：[regressions.txt](regressions.txt)。新回归使用项目真实验签与状态转移，但测试签出的块仍是测试工具提供的认证输入；不把它们称为端到端共识证明。

最后补充的自转断言单独验证，见 [self-transfer.txt](self-transfer.txt)；与已有 `TestDirectChildBeforeParentAndImmediateSuccessor` 一并成功执行，追加后的整个规则包也通过 [race 测试](rules-race.txt)。全项目运行记录早于这次仅测试断言的追加；不将此前结果冒充追加后的全项目重跑。

## 4. 验证命令与输出

以下运行均成功退出；`go test` 显式使用 `-count=1`：

```sh
go test -count=1 -v ./tools/securitymodel
go test -count=1 -tags=comet_v3 ./...
go test -count=1 ./...
go vet -tags=comet_v3 ./...
go test -count=1 -race -tags=comet_v3 ./internal/member ./finality ./tools/securitymodel
go test -count=1 -v -tags=comet_v3 ./finality ./internal/member ./internal/committee \
  -run 'TestBlockProofAuthenticatesProjectionNotMutableHistory|TestBlockFollowerMissingRepairLateAndDuplicate|TestSecurityComposition|TestDirectVerificationCacheReusesOnlyIdenticalBytes'
```

[Comet 配置全项目测试](go-test-comet.txt) · [默认配置全项目测试](go-test-default.txt) · [受影响包 race](go-race.txt)。`go vet` 成功且无诊断输出；退出结果与环境记录见 [verification.json](verification.json)。

另对共享协议解码器运行一次 10 分钟 fuzz：

```sh
go test -run '^$' -fuzz '^FuzzDecoder$' -fuzztime=10m -parallel=2 ./protocol
```

共 51,364,027 次执行，完成且无失败，见 [decoder-fuzz.txt](decoder-fuzz.txt)。这只检验该 fuzz 目标的输入健壮性，不是密码学、协议安全或整个解码面穷尽。

相对本轮基线，运行代码仅修改钱包 `ReceiveDirect` 的耐久性说明注释；其余 Go 修改均为测试／独立模型。本轮未增加签发、验签、数据库或共识的运行工作。整个安全分支仍包含前一 R6 阶段的原始分片接纳修复，不能把整个分支说成仅文档改动；合并 `re` 前的 R6 性能验收仍独立保留。

## 5. 阅读与交叉审阅

[references.json](references.json) 记录固定版本、核查章节、PDF 指纹及 DOI 元数据；不将论文 PDF 或大段原文提交仓库。[section-lengths.json](section-lengths.json) 记录粗略英文词元统计，只有写作篇幅参考用途，没有统一的论文安全章节字数标准。

阅读包括 FastPay、Snappy、Sui Lutris、Stingray、Zef 和两篇 2025 年 Lightning 形式化研究，另核对 Shoup 的门限参数与修订区块链背景。不同文献的用户权利、活性、恢复、密码学模型不直接继承到 Next。

本轮与侧边栏 GPT 完成九轮实质讨论，用于寻找遗漏和反驳，不作为独立安全证据。实际采取的关键修正包括：区分认证接收与未消费权利；先证明输出至多关闭和 Paid 冻结，避免资金推导循环；用早期见证集合修正迟到签署量词；区分公共经济授权、适配等式和最新历史认证；把进度前提与全程序活性分开。每项采用的建议均回到代码、原文或测试核对。

最终文稿经过一次确定性写作检查，见 [style-review.json](style-review.json)。正文的七个编号小节和参考文献触发扁平标题提示；这是有意保留的论文结构，不是格式错误。报告无其他提示。引用格式及篇幅仍须依最终投稿模板统一。

完整实现精化、门限适配查询归约、修改后 Comet 全路径证明、理想支付功能的可兑现性精化仍未完成。论文正文相应使用条件论证，不能将本轮完成表述为系统已获全面机械形式化验证。

## 6. 共识身份组件探针

[测试源码](comet-identity-probe.go.txt) · [运行输出](comet-identity-probe.txt) · [边界解释](../security-redaction-consensus-wire4.md#44-完整候选身份反例与修复)

在当前 overlay 生成的 `.scratch/comet-src/consensus` 中，将保存的源码复制为 `next_identity_probe_test.go` 后，从项目根运行：

```sh
go test -mod=readonly -count=1 -v github.com/cometbft/cometbft/consensus \
  -run '^TestNextProjectedHashDifferentPartHeaderCommitBoundary$'
```

这是通过断言确认现有拒绝边界的测试，预期结果是捕获特定 panic，而非正常完成区块。它使用仓库公开 RSA 测试夹具，不读取部署密钥。测试直接注入本地候选与提交票不匹配的状态；没有构造合法 Next 支付的网络攻击。保存为 `.go.txt` 是为了保留复现方法，不把第三方测试包探针混入项目日常构建，也未修改生产 Comet 状态机。

## 7. 本轮评审收敛记录

| 讨论主题 | 独立核查和采用的改动 |
| --- | --- |
| 接收与权利 | `AuthRecv` 认证对象；`FreshRecv` 是此前未授权消费的语义条件，不是本地第一次保存即可判定 |
| 风险覆盖的量词 | 真实成员回归及独立模型复现迟到签署反例，改用最早三份签名的固定见证集合 |
| 证明的依赖次序 | 先建立唯一关闭、Paid 冻结和认证先行，再证明预算界，最后连接真实账户，避免循环 |
| 财务与费用 | 按同一真实账户聚合全部 Grant；用户自付与可选代付分别核算；实际赔款不能通过核销复活 |
| 修订与密码学 | 把公共 Repair 授权、适配等式、原始分片绑定和最新历史认证分开；不直接套用不匹配参数的门限定理 |
| 可用的后继权利 | 增加条件自转为新最终输出的构造，并核查已有用户自付规则；不新增兑现业务或无限可用性承诺 |
| 共识实现范围 | 核查入口与重放调用图，保留完整 BlockID 的状态机义务；组件注入结果不当作网络漏洞结论 |

评审意见用于提出可检验问题，最终判断以代码、论文原文、实际测试及写明的假设为依据。

## 8. 已提交终稿的三轮辩证复审

在 `d097358` 终稿基础上，再向原侧边栏会话发送第五章、完整报告和本证据索引的全文，完成三轮实质复审。本节记录新增结论，不把 GPT 的意见算作独立证明或独立运行测试。第一轮要求按模型内反例、实现连接、措辞及范围扩展分类；第二轮用实际函数与新推导回应质疑；第三轮核对配置边界及最终主张。

| 质疑 | 核实与处理 |
| --- | --- |
| 双计数公式中间项漏写 Grant 范围 | 采纳。同一成员可以在多个 Grant 下分别合法占用，全部批准之和不能用单 Grant 份额约束。正文、报告及组合附录统一以 `A_m,g` 限定同 Grant 的全部批准；诚实集合属于该 Grant 的组织。修正证明记号，不改变运行代码。 |
| 身份、历史集合是否足够明确 | 补出完整 `(Config, ResourceKey, GrantID)` 与唯一状态键的对应；`C_g` 是曾经成证的 Fact 历史集合，不随票子集、材料或终结删除。不同 QC 表示不改变经济去重，完整字节缓存与修复检查仍保留原口径。 |
| Follow 表与迟到补签轨迹冲突 | 采纳。`applyPayment/applyRepair` 可以在没有相关 Approval 时跟块；只有更新原占用与返还需要原 Approval/Debit。另核对 `InstallDirectClassified`，补回它记录本地 Consumed 的效果。 |
| “有覆盖”是否只是把成功当作前提 | 新增 CAL 数值能力推论。按执行前状态及不同实际父 Fact 分组，证明 `V_g + S_g + Σ新登记cap ≤ Q_g ≤ B_g`，推出任意登记顺序不会单因 CAL 总额失败；Open 义务也有本金余额覆盖。多父、多输出、部分赔付、迟到和共享账户分别核对。 |
| 是否仍要新增 QC 规范化机制 | 不采纳为运行改动。Fact 不依赖 Votes，现有经济身份已足够；新见证不能随报文票集合更换，字节认证缓存仍不得改成仅按 Fact。 |
| 配置唯一性是否已由代码保证 | 尚未完整保证。`NewEngine` 检查重复 Config Hash，`DirectSettings.Policy` 按该哈希建表，不能排除同 Org 的不同配置。正文显式限定各诚实方共享单一活动配置／锁域，不把所有可初始化配置均称安全；报告列出最小启动检查作为后续实现补强。未将其冒充已复现的网络攻击。 |
| 修订与共识是否只差机械化 | 不是。完整 BlockID 与公共执行的实现对应仍是实质待证项；门限控制、最新历史认证按各自实际主张分别列明，不自动推翻条件经济推导。 |
| “经济效果相同”是否量化过宽 | 补充相同观察者本地状态、配置和游标；不要求不同成员得到相同返还。Repair 会改变备付、缺口及费用，所保持的是已认证字段与不重复的子付款入账。 |

复核了 `PrepareDirectVector`、`OutputSummary.Validate/SummaryFor/Fact`、`VerifyDirectSubmission`、`grant/register/completeOutput/updateUsage`、`EvaluateDirectPaymentAt/EvaluateDirectCompensation`、成员跟块及 INSTALL、初始化和修订执行。补款、公开 Spent 与 Fact/Grant 的对应已在报告 §5.3 展开，不再只用“精化”一词概括。

本轮重新运行的相关回归：

```sh
go test -count=1 ./internal/member ./internal/rules -run 'TestSecurityComposition|TestBlockFollowerMissingRepairLateAndDuplicate|TestDirectOwnerFeeRefundAfterParentArrives'
```

两个包均通过（成员包 1.079 s，规则包 0.551 s）。它只用于复核本轮引用的实际语义，不是新的全项目、网络 BFT 或性能测试。本轮变更仅为文档；`re` 和 E1–E8 测量保持原样。

最终主张限定为：对满足明确配置及公共执行接口的 wire 4，给出隐藏 QC 风险、真实备付、守恒及条件后继使用的手工安全论证，并附代码对应与回归证据。未在本轮找到修正后条件推导的新反例，不等于排除了所有实现反例，也不等于整套实现已经完成安全证明。

## 9. 后续代码加固与联合证明

上一节的配置和完整共识身份问题已继续落实，见[加固报告与回归说明](hardening.md)。共同启动校验拒绝同组织冲突配置；共识按完整 BlockID 处理 Proposal、POL、锁定和提交，并在新回归中按真实提交票取回、存储和执行正确正文一次。另用任意适配输出模型论证业务授权不依赖“有效 opening 必然是一次新的门限批准”。

第 6 节的 panic 探针保留为 `37edf2a` 的历史证据，不应再复制到当前 fork 并期待相同 panic。当前正式回归由 overlay 复制，直接运行 `TestStableHashIdentity`。第 1–8 节的“未修改共识生产代码”“配置未完整保证”等是各轮当时状态；当前结论与验证以第 9 节及加固报告为准。
