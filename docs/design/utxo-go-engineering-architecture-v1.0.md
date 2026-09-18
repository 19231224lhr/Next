# UTXO 快速转账系统 Go 工程架构规范

> **2026-09-19 工程增量：** [v1.2 直接担保与输入替换方案](./utxo-direct-liability-amendment-v1.2.md) 第 2–8 节规定新对象、身份、预算、责任、迟到及历史改写；第 9–10 节规定包级改动和验收。沿用下述 Go 分层与 bbolt，不新增根责任服务。涉及旧 TXCer 内嵌父交易、RootRecord、DEFERRED、零 CAL 转手和不可变交易字节的定义，以 v1.2 为准。现代码仍是 v1.1；真实改写需限定的 Comet 适配和密码实现验证，不能声称已有依赖直接支持。

版本：工程规范 v1.1 · 一轮取证与后台 INSTALL  
协议基线：`utxo-fast-payment-system-design-final.md`，协议 v1.1  
日期：2026-09-18  
交付性质：用于后续代码实现的架构、数据和接口规范；本文件不是已经实现、编译或压测通过的软件。

阅读入口：协议对象见第 4–5 节；Go 分层与执行见第 2、6–9 节；数据库见第 8 节；接口事件见第 10 节；生成器见第 12 节；测试、测量及落地顺序见第 13–15 节。

## 1. 实现边界与规范效力

本规范把协议对象、纯规则、持久状态、运行时调度和外部接口连成一个可实施的 Go 工程。所有实现遵守协议终稿的资金守恒、唯一消费、原成员核销和一轮持久批准、完整 SpendQC 与后台安装规则。工程优化不能改变这些业务事实。

首版先跑通一个四成员组织，再以至少两个独立四成员组织、一个四成员委员会、可替换网关及组织/散户钱包验收完整闭环。先验证正确性，再测稳定 TPS 与延迟。四个成员使用不同身份、进程和数据目录；在同机运行只用于功能实验，不代表独立故障域。

### 1.1 本次实现范围

| 范围 | 工程要求 |
|---|---|
| 完整设计 | 两资产、工作额度、政策限额、根义务、一轮 SpendQC 证书、公共结算、费用关闭及累计核销都有确定的对象、状态和处理入口 |
| 优先实现 | 同组织/跨组织快转、散户直接付款、拆分合并、预存 FUEL 代付、低水位真实补资、后台完成反馈与核销、证明与测量；闭环后接入轻量分段归档 |
| 后续接入但模型现在固定 | 合法 TXCer 的担保履行、原输入结算与真实回补；按根责任和累计核销规则实现 |
| 本阶段不做 | 候选取消/授权退役释放、在线版本重整、损坏数据库修复、旧备份回滚恢复、跨机 Worker 迁移、在线成员变更、安全记录裁剪、在线数据库压缩、外链适配 |
| 不能省略 | 同库原子提交、同步持久化、落盘后签名、原身份幂等重传、正常同库重启、提交结果不明时停签 |

“暂不做恢复”指不建设复杂恢复子系统，不关闭数据库与共识引擎自身的正常持久保障。持续分票、隐藏证书、状态损坏或无法证明完整性的旧备份可能使受影响身份停签并保留数据；不启用旧版遗漏作废和退役返额，不把重新启动当成清空义务。未实现的策略返回明确不支持，不能用空函数或模拟成功开启。

本规范中的“必须”属于一致性要求，“默认”是首版实现选择，“测后可选”须先给出数据和同语义测试。完整协议的功能覆盖与首轮原型的启用范围分别记录，不能因为定义了结构体就宣称对应协议已经实现。

### 1.2 交付与后续落地

本次产物是这份规范与实际讨论记录。文中的 Go 类型、目录、命令和数据库键描述的是后续工程应实现的契约，当前目录中没有这些运行程序。架构参考生成器作为有界的开发工具设计，负责显式结构与映射的生成；不让自然语言生成过程决定资金规则，也不声称生成器能够保证整个系统正确。

## 2. 进程、包与依赖方向

采用一个 Go module、按职责分包、多个独立节点进程。核心状态规则不拆成 RPC 微服务。网关、证据收集与中继合并在一个进程内；组织成员与委员会成员必须保持独立身份和数据库。

```text
cmd/
  member/       组织成员：预检、准备、安装、额度与核销
  committee/    委员会应用与 CometBFT 节点装配
  gateway/      接入、收集器、可靠中继
  payctl/       钱包和本地开发操作
  bench/        工作负载与端到端测量
  archgen/      后续按显式规范生成结构与文档的开发工具
protocol/       对外共享类型、规范编码、身份、签名与证书校验
finality/       最终事实证明校验；隔离 Comet 头与 Merkle 依赖
internal/
  state/        持久记录、规范键、ReadView、Change、Overlay
  rules/        无网络/时钟/数据库依赖的确定性状态转移
  member/       组织运行时、Worker、提交调度、签署与 outbox
  committee/    公共执行、区块工作区、ABCI 适配与最终证明
  gateway/      收集器、中继、查询与同请求合并
  wallet/       组织/散户收款描述、输入选择、请求保存与付款状态
  peers/        首次组织通信、配置验真与节点信息缓存
  store/        业务数据库适配；不复制业务规则
  transport/    HTTP 消息封装、大小限制、错误映射、事件游标
  telemetry/    指标、结构化日志、基准采样
  testkit/      内存状态、确定种子、故障注入与本地多节点夹具
spec/           显式对象、键、接口和规则映射定义
testdata/       独立黄金向量、场景输入与期望事实
docs/           生成的对象目录、键目录、接口和规则追踪矩阵
```

`protocol` 只依赖 Go 标准库；`state` 依赖 `protocol`；`rules` 依赖前两者；`store` 实现 `state` 所需持久访问；各运行时调用规则并提交结果；`transport` 调用运行时。装配、配置读取和进程生命周期留在 `cmd`。遥测不能反向决定规则输出。

不设置通用 repository/service/factory 三层，也不为每张逻辑表建一个接口。只在确实存在两种用途的边界定义小接口，例如内存模型与实际数据库共同使用的状态视图、节点和测试夹具共同使用的发送端。现阶段不引入 Redis、Kafka、ORM、依赖注入框架或独立规则引擎服务。

`protocol` 内按 `amount.go、identity.go、transaction.go、certificate.go、receipt.go、organization.go、codec.go` 分文件；`rules` 按 `prepare.go、install.go、direct_transfer.go、funding.go、settle.go、fees.go、credit.go` 分文件。文件随职责增长再拆，不预先生成大量空目录。

## 3. 技术栈与版本基线

工程语言固定为 Go。哈希使用标准库 SHA-256，普通身份签名使用 Ed25519；生产密钥、组织成员密钥与委员会身份按用途隔离。签名输入具有网络、角色、阶段、配置和事实身份的绑定。

建议初始化版本为 Go 1.27.1、CometBFT v0.38.26，业务库默认 bbolt v1.5.0。这些版本存在于核对日的官方发布记录，尚未在本项目完成组合编译或兼容测试；建库第一步锁定 `go.mod/go.sum`、运行兼容测试和漏洞扫描，不能使用浮动 latest。协议终稿的 ABCI 0.38 语义因此可以直接作为适配基线。[S1][S2][S3]

Go 标准库承担 HTTP、TLS、JSON 运维封装、日志、哈希、签名、测试与性能剖析。二进制支付消息使用本规范的规范编码；JSON 只用于配置、查询投影和基准报告。遥测先提供固定低基数计数器、耗时分布与受限 pprof 入口，不把监控服务加入支付依赖。

## 4. 协议对象与规范编码

### 4.1 四类对象不能混用

| 类别 | 示例 | 使用原则 |
|---|---|---|
| 共同认证内容 | TxBody、AdmissionVector、CertifiedEffects | 规范编码、确定性计算；所有成员认证同一个值 |
| 证据封装 | OwnerAuth、SpendQC、FinalStateProof | 可以补齐、重传、换有效签名子集；不能因此换业务身份 |
| 持久记录 | ApprovalRecord、OutputSpend、AppliedCredit | 保存节点自己的责任、动作和状态；不能直接作为公共证明 |
| 传输与观察 | HTTP DTO、Outcome、Event | 包含队列和本地观察信息；不直接授权付款或返还权限 |

网络、本次认证组织、原担保组织、输出消费路由、成员配置、规则与授权版本均有独立字段。配置摘要固定四个成员身份及阈值，不能只用一个可被重定义的组织名称。

### 4.2 基础类型

以下是类型约束示例，不是完整可编译源文件。ID 使用不同 Go 命名类型，避免把 RootID 误传成 TxID；金额与资源同样分离。

```go
type Hash [32]byte
type TxID Hash
type OutputID Hash
type RootID Hash
type SourceID Hash
type SpendFactID Hash
type FeeID Hash
type CAL uint64
type FUEL uint64
type ExecUnits uint64
type RetainedBytes uint64
type PolicyUnits uint64
type Height uint64
type Revision uint64
type MemberIndex uint16
type Nonce [16]byte
```

所有相加、相减及乘除使用受检运算；错误返回明确枚举，不使用饱和运算掩盖超额。价格乘工作量的中间值也检查溢出。授权比例采用整数 `floor((f+1)*coverage/(2*f+1))`，逐 Grant 向下取整后累计并按 Grant 事实防重，不将累计充值重新合并取整。实现用安全的宽乘除或商余分解；不能先执行可能溢出的 uint64 乘法。不同资产不能通过统一的裸 `uint64 balance` 隐式互换。JSON 中金额与累计计数输出十进制字符串。

### 4.3 唯一字节表达

首版采用显式字段顺序的二进制编码，固定整数大端，枚举 u8/u16、计数和长度 u32、金额和版本 u64、ID 固定 32 字节、Ed25519 公钥 32 字节和签名 64 字节。可选值先编码 0/1 标记；变长字节和列表先编码长度，再编码内容。协议 schema 明确每个字段的实际宽度、上限和顺序，禁止依赖 Go 内存布局。

每种消息有独立类型标签和 WireVersion。业务集合按规定键排序且去重；输出列表保留用户指定顺序，因为 OutputIndex 决定 OutputID。输入在签名前按 OutputID 排序，费用输入与本金输入合并检查重复。零值、空列表与缺失值的合法性按消息明确。未知版本、非法枚举、非规范排序、重复键、越界长度、尾随字节均拒绝；分配内存前检查长度。

TxID 计算使用包含版本和网络的 CanonicalTxBody；签名摘要另加用途域。JSON、map 遍历、Protobuf 的 deterministic 模式都不能作为业务身份的规范编码依据；Protobuf 官方也明确 deterministic 不等于 canonical。[S4]

### 4.4 身份及无环引用

| 身份 | 定义或绑定 |
|---|---|
| SourceID | 原始真实 UTXO 的网络、资产及 Outpoint；不含本次 nonce 和证据封装 |
| IntentID | H("INTENT", NetworkID, SubjectID, UserNonce)；钱包持久业务意图 |
| RootID | H("ROOT", NetworkID, GuarantorOrgID, SubjectID, RootNonce) |
| TxID | H(CanonicalTxBody) |
| OutputID | H("OUTPUT", NetworkID, TxID, OutputIndex) |
| EffectsHash | H(CanonicalCertifiedEffects) |
| SpendFactID | H("SPEND", TxID, AdmissionVector, EffectsHash, RuleIDs) |
| FeeID | H("FEE", TxID, FeeTermsHash) |
| ActionID | H(NetworkID, BusinessID, ActionKind, ActionIndex)；不含重试次数 |
| CommandID | 规范公共命令内容摘要；不同批封装仍共享 ActionID 防重 |

H 的每个复合参数使用规范编码，字符串域也有明确长度。RootNonce 先产生，允许原输入的付款交易绑定 RootID；RootID 不从包含来源交易哈希的 TxID 反向推导。TxBody 不包含自己的 TxID、FeeID、签名或 QC。输出的引用在正文外按输出索引选择，不能让接收者改写输出金额。

manifest 固定推导次序：RootID/IntentID 与外部输入 → TxBody → TxID → OutputID/FeeID/相应 ActionID → AdmissionVector/CertifiedEffects → EffectsHash → SpendFactID → SpendVote/SpendQC/TXCer。CertifiedEffects 不包含自身 SpendFactID、QC 或依赖它们的持久记录字段。动作类别和序号由 FeePlan/规则明确划分，不因重试或批次改变。

新部署固定 ProtocolVersion=2、WireVersion=2，并采用本版 FeeRuleID/WorkRuleID/AccountingRuleID；SpendVote 在 SPEND 签名用途域覆盖 SpendFactID。TxID 已绑定 NetworkID、本次组织/纪元/配置及规则。旧两阶段票与本版不混签或自动转换，未启用旧解析器返回 UNSUPPORTED。文件名保留原路径，不表示内容仍为旧协议。

### 4.5 完整业务对象目录

下表给出必须进入显式 schema 的字段组。`Ref` 引用的事实必须能按哈希取得并验证；不能仅靠节点当前配置推断签名时的规则。

| 对象 | 必需内容与约束 |
|---|---|
| TxBody | WireVersion、NetworkID、ProtocolVersion、IntentID、SubjectID、UserNonce、RuleIDs、TxKind、Inputs、Outputs、FeeTerms；FAST_TRANSFER 另带 CertifierOrgID、CertifierEpoch、OrgConfigHash、DependencyAnchors、AdmissionRefs、WorkLimit 与可选 RootTerms，DIRECT_TRANSFER 的 FeeTerms 使用 DirectFeeTerms 分支且不带组织认证字段；直接父引用与最终锚点固定在正文 |
| FinalInput | OutputID、最终创建事实/证明引用；资产、金额、OwnerLock、SpendOrg 由证明认证；成员或委员会在其消费职责内复核当前状态 |
| CertificateInput | OutputID、创建 TxID/输出索引、父 SpendFactID 与证据引用；发行组织不限，确认本笔 CertifierOrgID 等于该输入 SpendOrg，资产/金额/所有者由认证事实确定 |
| Output | AssetID、Amount、ReceiveDescriptor（OwnerLock、SpendOrg）、收款描述确认引用；零额输出拒绝，序号就是列表位置，SpendOrg 不随最终落账改变 |
| OwnerLock / OwnerAuth | 首版单 Ed25519 所有者；授权记录含主体、用途、KeyID、算法、签名；覆盖 TxID 与用途域，全部输入所有者及所需费用授权齐备。复杂脚本和委托链不默认开启 |
| AdmissionRef | ResourceKind、AccountID 或 PolicyID、AuthorizationVersion、GrantFactRef、RuleID；不同维度可引用不同账户和版本 |
| AdmissionVector | CAL、FUEL、E、B 与按键排序的政策项；每项带所属授权键及本笔上限；由规则计算后全体共同认证，不相信客户端任意填值 |
| FeeTerms（快路径） | FeeSourceKind、付款/代付账户或明确 FeeInputs、PolicyID、资金方授权引用、FeeRuleID、Fmax、各动作及次数上限、资源/服务费上限、ChangeTerms、RefundDestination、受益分配策略；一笔只选一个费用来源 |
| DirectFeeTerms | FeeSourceKind=OWNER_FINAL_UTXO、FeeRuleID、Fmax、确切 FeeInputs、ChangeTerms；不带组织政策/认证/担保服务字段，费用及找零与本金原子执行 |
| RootTerms | RootID、RootNonce、GuarantorOrgID、SourceID、SourceOutpoint、原输入证明引用、SubjectID、Amount、不可变 RootOutputs 条款、原组织账户/授权及真实回款目的 |
| ReceiveDescriptor | NetworkID、WireVersion、OwnerLock、SpendOrg（ORG + OrgID 或 COMMITTEE）、所有者确认签名；描述可预先签署并复用，无逐笔收款确认；钱包默认服务关系不能覆写既有输出 |
| OrgDescriptor | OrgID、Epoch、OrgConfigHash、成员索引/公钥/地址、q、委员会登记事实或创世配置引用；首次获取后按配置身份缓存 |
| WorkLimit | E 上限、B 上限、祖先深度/唯一依赖数/证据字节界、不可分割公共命令大小界、工作政策版本 |
| CertifiedEffects | 输入消费映射、固定输出、根及费用/工作义务摘要、深度和工作计量结果；不含本地 Worker、余额、缓存、日志位置及签名子集 |
| SpendVote / SpendQC | SpendFactID、TxID、完整 AdmissionVector、EffectsHash、规则/配置绑定；成员在完整事实持久批准后签票，QC 含至少 q 个不同有效成员，不能跨配置拼接 |
| InstallAck | SpendFactID、NodeID、本地 INSTALLED 结果及请求关联；仅是经过身份认证传输返回的普通持久安装 ACK，不汇成支付 QC，不授权费用或额度变化 |
| TXCer | TxBody、OwnerAuth、AdmissionVector、CertifiedEffects、SpendQC、OutputReference；可附父材料/证明包，附包不改变业务身份；不包含必需的安装回执 |
| FinalFact | FactKind、FactKey、Revision、NetworkID、RuleIDs、规范 Payload；按事实类型在载荷中明确组织角色，委员会慢交易不伪造 CertifierOrgID；每个版本值不可变 |
| CumulativeCredit | 原 SpendFactID、资源/账户或政策/版本、OriginalCap、累计 Paid/Discharged/Returned/Remaining 或该资源的专用累计字段、阶段和最终证明引用 |
| FinalStateProof | FinalFact、区块事实包含证明、应用承诺字段、已认证公共头和必要信任配置；验证规则见第 9 节 |

`RootOutputs` 在交易条款中用输出索引与内容表达，根记录中保存派生 OutputID，避免正文自引用。费用自付使用已最终的 FUEL UTXO；其输入是需要原子消费的费用输入，不能把未最终 FUEL 当成无覆盖的付款来源。费用预算承诺和实际余额扣款分别由成员、委员会执行。退款生成的资产归属由 FeeTerms 固定。

交易类型只有 `FAST_TRANSFER` 和 `DIRECT_TRANSFER`。输入为 `FinalInput | CertificateInput` 联合类型；DIRECT_TRANSFER 只允许 FinalInput。组织认证字段、AdmissionVector、WorkLimit 和 QC 仅属于快路径。最终与凭证态共享 OutputID，证据更新不会创建第二份输出。

### 4.6 公共命令目录

| 命令 | 主要输入 | 权威结果 |
|---|---|---|
| DirectTransfer | DIRECT_TRANSFER、已最终且 SpendOrg=COMMITTEE 的本金/FUEL 输入、所有者授权及收款描述 | 原子消费、普通 UTXO/找零、实际费用和最终事实；无 TXCer |
| RegisterReady | 本版完整 TXCer、父材料/证明引用；不要求安装 ACK | SpendQC 公共登记、一次费用托管、义务索引；必要材料缺失则按阶段 DEFERRED |
| FundRoot | 完整根 READY、来源证据或原备付条款 | 同一 RootFundingID 下 SOURCE/RESERVE 互斥落实、同组根输出 |
| SettleTransfer | READY、必要依赖和已落实父输出 | 输入终态及相同 OutputID 的最终 UTXO，保持 OwnerLock/SpendOrg |
| ApplySourceReturn | 真实入款身份及证明、RootID、回款条款 | 一次回补原垫付；不是同一笔资金的自由充值 |
| TopUpAndGrant | 真实充值输入、组织/资源/授权版本 | 扣转资金、同版本增量授权事实 |
| RestoreFeeCoverage | 真实补款、原版本已确定且不可再退的净费用支出引用 | 仅补回原占用；入款不能又做新 Grant |
| CompleteWork / CloseFee | 可验证已完成阶段与原费用计划 | 工作核销、阶段分润、退款及 CLOSED |
| ClaimReward / SubmitViolation | 原动作工作证据与受益策略；可证明违规事实 | 奖励账户累计/批量领取；按固定规则处罚独立 FUEL 保证金或可处罚收益 |
| FusedLifecycle | 有界的登记/落实/结算/关闭动作序列 | 已完成前缀保留，未完成阶段 DEFERRED |

命令中的证明、成员票和输入授权由应用独立核验。网关预检不能代替委员会验权。恢复、授权版本退役和成员更换命令仅预留类型及明确 `UNSUPPORTED`，首版没有“强制清锁继续”入口。

工程中的 MemberConfigID 等同 `OrgConfigHash`，不增加第二套配置身份。签名和证书同时绑定 CertifierEpoch；它与各资源 AuthorizationVersion 各有含义，不得合成一个含糊的 epoch。本版不提供候选取消或相同 IntentID 换正文的操作；原候选保留并重传，不因超时另造一次付款。

### 4.7 组织互通、散户与原责任

| 场景 | 实现契约 |
|---|---|
| 甲组织用户付乙组织用户 | 甲认证一次 FAST_TRANSFER，新输出包含乙用户确认的 OwnerLock/SpendOrg=乙；乙用缓存甲配置验签，无交接 QC、兑换或新增跨组织费 |
| 乙使用甲、丙签发的输入 | 分别验证发行者签名；全部输入已指定 SpendOrg=乙时，由乙本地原子消费，不要求发行组织相同 |
| 散户发送 | DIRECT_TRANSFER 直接发委员会，只接受最终且 SpendOrg=COMMITTEE 的输入；不调用组织 PREPARE/INSTALL，不生成 TXCer |
| 组织用户付散户 | 普通 FAST_TRANSFER 输出指定 COMMITTEE；组织后台提交，散户只计最终 UTXO 余额 |
| 散户付组织用户 | 普通 DIRECT_TRANSFER 输出指定收款人服务组织；最终后收款人可据最终输出开始快转 |
| 原担保履行 | RootObligation.GuarantorOrgID 不变；仅由原组织覆盖、真实责任解除/回款及原占用记录驱动核销，后继 CertifierOrg 不接管根 |

收款描述由所有者确认、输出条款由付款签名和认证绑定。现有输出的 SpendOrg 不由当前钱包配置修改；新加入组织者可直接用组织收款描述收新款，已有散户输入依旧经普通 DirectTransfer 付款，包括付给本人组织描述。没有专门的资产进入、退出或兑换 API。

每个输入的消费只交给其 SpendOrg；组织认证输入到公共结算后也不能被裸用户签名绕过，防止快慢双花。元数据缓存、服务绑定和输入处理方是不同用途，不增加按账户串行所有付款的全局 nonce。

首次通信通过 `internal/peers/registry.go` 获取并缓存 OrgDescriptor，以受信创世配置或委员会 OrgRegistered 证明验明公钥与阈值。正文携带 CertifierOrgID/配置引用；父 QC 用父交易签发者的历史配置验签，不使用本次 CertifierOrg 或根 GuarantorOrg 的配置；缓存未命中补取配置，不逐笔做在线组织互认。

没有合法 TXCer 或真实 UTXO 的入金申请不签发。已最终并排他可消费的真实输入直接结算，不凭空新建担保占用；确有合法原根义务时，仍按原条款执行 FundRoot、原资金回补及累计核销。合法来源可用时选 SOURCE；缺证 DEFERRED，不因缓存缺失或超时进入 RESERVE。凭证落实成 UTXO 沿用 OutputID，原持有人不能再领取一份本金。

## 5. 持久状态与业务不变量

### 5.1 成员本地记录

| 记录 | 主键 | 必需字段 / 更新限制 |
|---|---|---|
| PeerConfig | OrgID/Epoch/OrgConfigHash | 经认证节点公钥/阈值/地址、登记证明；不以对方任意自报密钥覆盖可信配置 |
| VerifiedParent | NetworkID/ParentTxID/SpendFactID/OrgConfigHash/RuleIDs | 本成员已完整核验的不可变父事实、输出描述、证据位置；不缓存未花费状态 |
| OutputCreation | OutputID | 创建事实、所有者、金额、资产、SpendOrg、父 CertifierOrg/配置；只允许幂等补齐 |
| OutputSpend | OutputID | 局部 Prepare 锁、成立的 SpendFactID、消费终态；不会被 Creation 覆盖 |
| ApprovalRecord | SpendFactID | 正文/OwnerAuth、CertifiedEffects、AdmissionVector、必要直接父材料/最终边界、原资源占用及 Worker 归属、签署意图；与输入锁/outbox 原子持久，仅实际批准者建立 |
| InstallRecord | SpendFactID | 完整 SpendQC/正文/授权/直接父材料、EffectsHash、本地 INSTALLED 及传播待办；不自动建立 ApprovalRecord，不覆盖已有公共终态 |
| GrantRecord | 资源/账户或政策/版本 | 累计授予、剩余公共池、已分配 Worker、状态；增量授权防重 |
| WorkerSlice | GrantKey/WorkerID | 独占分配、当前可用、在途占用；同成员调配原子转移、不增发 |
| AppliedCredit | SpendFactID/ResourceKey | 已应用累计值、对应 Revision、返还到原额度的数值；与剩余占用同事务更新 |
| SourceImport | GuarantorOrgID/SourceID | RootID、不可变条款摘要、局部候选/已成立标志；有效关联跨版本保留 |
| WorkRecord | SpendFactID/阶段 | E 完成、B 公共交接授权、本地安全替换/交接完成及实际释放 |
| Action / Outbox | ActionID；本地序号 | 业务阶段、可重试原因、完整证书或原票引用、待发目标、公共保管事实引用；传输 ACK 和公共完成分别记录 |

预算状态不能靠扫描事件流临时重建后继续签名。热缓存只加速读取；重查和更新仍在权威事务里进行。INSTALL 接受合法 QC 时可以覆盖冲突局部输入锁的裁决结果，但被替代局部 PREPARE 的预算没有合法核销就保留。没有复杂恢复时，这种占用可能长期存在，必须反映为可用性限制。

### 5.2 委员会公共记录

| 记录 | 主键 | 核心内容 |
|---|---|---|
| UTXORecord | OutputID | 创建与消费分别记录，OwnerLock/SpendOrg、资产金额、最终状态；与凭证同身份 |
| OrgRegistration | OrgID/Epoch/OrgConfigHash | 固定成员身份、公钥和阈值；生成 OrgRegistered 正向事实 |
| OrganizationAccount | OrgID/AssetID | 自由余额、已授权覆盖、已托管/已支付资金的明确科目 |
| PublicAuthorization | GrantKey | 累计资金授权及 ACTIVE 状态；新增授权事实不能覆盖旧承诺 |
| RootObligation | RootID | GuarantorOrgID（不可变）、SourceID、RootOutputs、OriginalSpendFactID、责任计数、回款目的、资金落实分支 |
| SourceImport / SourceReceipt | GuarantorOrgID/SourceID；真实入款 ID | 前者固定本组织根关联；后者全账本一次消费/分配 |
| FeeEscrow | FeeID | Fmax、各动作实际费用、阶段服务费、Held/Spent/Refunded、退款归属、状态 |
| PolicyUsage | PolicyID/主体/版本 | 累计商户支出；与资金覆盖分别记账 |
| RewardAccount / PenaltyRecord | 受益人/资产；违规事实 ID | 按工作证据一次归属与批量领取，独立运营保证金罚没防重 |
| PublicWork | SpendFactID/阶段 | 有界工作计划、阶段结果、可核销资源计数 |
| LifecycleAction | ActionID | 未开始/完成/合法终止；最后尝试结果与终态分离 |
| FinalFact / FactIndex | FactKey/Revision；高度/叶序号 | 可验证不可变事实与包含证明材料 |
| CommitMeta | NetworkID | 最近已提交高度、区块身份、AppHash、schema/config 摘要 |

物理存储使用各自数据库中的命名 bucket；逻辑表不要求独立数据库或 RPC。键编码由 `state` 统一定义，前缀为 schema 版本和记录类别，定长 ID 后接大端序号；不使用容易歧义的字符串拼接。二级索引只为已确定的查询建立，并与主记录原子更新。

### 5.3 必须直接测试的不变量

1. 同一有效输入不能形成两个不同合法消费事实；创建事实补齐不改变已有消费终态。
2. 每笔批准同时占用完整 CAL/FUEL/E/B/政策向量，三份签名来自同一个完整向量的合格成员集合。
3. 新根必须由 GuarantorOrgID 对应组织认证并占用其覆盖；成员预算有界且 Worker 份额排他；转手 `c_CAL=0` 不读取或写入 CAL 额度热点。
4. CAL/FUEL 每项 `OriginalCap = Paid + Discharged + Remaining`，`Credit = Discharged + Returned`，`Residual = Paid - Returned + Remaining`，且 `Returned <= Paid`；收到更大累计值才应用差额。
5. 只有存在本地原占用的成员恢复对应权限；INSTALL-only 不增加可用额度。退款到外部地址不增加组织覆盖；普通充值不伪装成原债务回款。
6. E、B、商户累计支出分别核销；不能提供一个通用 `ReleaseAll(tx)`。
7. Root 只全额落实一次；来源/备付创建同一组根 Claim，迟到来源仅按原条款回补，真实入款全局防重。
8. FeeClose 检查“除本关闭动作外”的必需动作完成或合法终止，原子结清尾款、退款并 CLOSED；不先要求 Held=0，不等待所有后继付款。
9. 新增准入限流不能阻止已成立承诺的 INSTALL、结算、核销；事件和未认证查询不构成额度授权。
10. GuarantorOrgID 不因跨组织转手变化；输入 SpendOrg 必须匹配本次处理方，凭证与最终 UTXO 同 OutputID，散户不能用未最终凭证抢先消费。
11. 同组织缓存优化开关不改变有效性、金额、费用、责任、输出身份或持久化要求。

## 6. 规则层与运行时契约

使用确定性的“读视图 → 业务转移”模型。纯规则只接收明确输入和当前逻辑状态，不读取网络、墙钟、随机数或节点本地缓存。专用规则按 member/settlement 职责组织在 `rules` 中，不建立可插拔规则平台。

```go
// 契约示意；具体签名按消费者定义，禁止暴露底层数据库事务。
type ReadView interface { Get(key []byte) ([]byte, error) }
type Change struct { Key, Value []byte; Delete bool }
type Transition struct {
    Changes []Change
    Facts []FinalFact
    SignIntents []SignIntent
    Result BusinessResult
}
// Get 返回独立字节副本；NotFound 与存储错误不同。
// EvaluatePrepare / EvaluateInstall / EvaluateCommand / EvaluateCredit
// 均返回 Transition 或明确业务拒绝；没有网络和持久化副作用。
```

`VerifiedTx`/`VerifiedEvidence` 由内部验证器构造且字段不导出，表示特定正文和证据已经完成不可变检查；HTTP 解码器不能伪造该类型。它们不表示输入仍未消费、额度仍足够或政策仍有效。所有可变状态条件必须在最终写事务或区块 overlay 内重查。

规则先生成完整 Transition，再由调用者合并；不能一边验证一边留下部分副作用。每个公共命令的成功原子边界与融合命令的阶段边界明确不同：融合批允许保留已完成阶段，单个阶段失败则该阶段无半笔改变。无状态证明验证缓存命中与否不影响结果。

BusinessResult 明确包含 `CommittedEffects、ActionOutcomes、PrincipalStatus`，不使用一个 Success 布尔值决定是否保留全部变化。例如 REGISTER 和费用托管成功、SETTLE 缺依赖时，前者仍提交，PrincipalStatus 为 DEFERRED；本金使用独立子 overlay。费用授权或来源非法则没有合法扣费效果。规则错误、业务暂缓和磁盘提交失败是不同返回类别。

## 7. 一轮签发与后台安装

1. 组织入口只接受 FAST_TRANSFER；验证所有输入的 SpendOrg 与本组织一致，父发行组织可不同。先限字节和通用速率，验权成功后才占主体未决额度。
2. Worker 按 TxID 合并重复处理，验证用户授权、父完整 SpendQC/最终边界、金额及规则，计算一致的 AdmissionVector 与 CertifiedEffects。无效封装不能污染同正文的合法重传。
3. CommitLoop 在短写事务中重查输入/资源，原子导入必要的已认证父输出、保存父完整材料及转交待办，锁定本笔输入、占用所有资源，保存 ApprovalRecord 和签署意图。候选输出此时不可消费。
4. 同步提交成功后签发 SpendVote。收集器取得 q=3 张匹配票并验证组成的 SpendQC/TXCer 后立即交付；网关保存证书和投递待办移至后台，不作为交付门槛；钱包核验并原子保存证书和补交待办后标记 READY，不等待 INSTALL_ACK。
5. 完整证书同时进入发行成员 INSTALL 队列和委员会登记/结算入口。INSTALL 验证完整 SpendQC 并幂等持久输入终态、输出及材料后返回 InstallAck。委员会独立核验，无 q-ACK 前置；INSTALL-only 不扣首次预算也不取得原占用返还。

Worker 仍是成员内任务单元，每成员一个权威写者，跨输入事务保持原子。Worker 私额和完整资源向量检查沿用原规则。机会式组提交只合并已到达任务，受字节/笔数/时间约束，签名与网络在事务外；不因后台化关闭同步写。

父尚未本地安装时，`ResolveInput` 可返回完整证书认证的输出，CommitLoop 重查消费状态后将父导入与子批准一次提交；单票、候选输出和 ACK 都不是创建凭证。父/子创建消费分键，迟到安装不得恢复 OPEN；跨组织父导入仅保存认证事实和转交任务，不操作其发行者覆盖。合法 T 证书覆盖本地 U 的冲突裁决时，U 原占用仍保留，不新签冲突 SpendVote。

INSTALL 使用既有承诺的专用容量；容量覆盖本地批准、全组织可能成立证书及子交易带入的父证据。新准入限流不能挡住合法安装、证明、核销和必要补证。按原事实有界重试，公共完成早于安装时只补本地材料，不回退已核销状态。

### 7.1 验证复用与可靠传播

`internal/member/verifycache.go` 使用 `NetworkID/ParentTxID/SpendFactID/CertifierOrgID/OrgConfigHash/RuleIDs` 绑定本成员已完整核验的父事实；缓存不提供当前未消费结论。验证复用开关只改变验签/解析次数，不改变业务结果。

同组织收到父完整证书后可复用或顺带安装，跨组织按父发行者配置验签；不等待父组织在线响应。签子票前持久保存使用过的父证书和转交待办，后台可将该父证书提交发行组织或委员会。无完整父证书且缓存无已验事实时，保持缺证，不从单份本地安装 ACK 推断父有效。

收集器在交付后保存已组证结果和 outbox；钱包保存完整 TXCer 和补交待办并运行补交中继，成员保存自身原票及已获完整证书。最小 HTTP 实现先发送带准确 Content-Length 的完整响应并 Flush，再在原处理协程内同步保存，保留既有并发槽至保存结束；不新增无限 goroutine 或无界队列。后台保存失败记录错误，不撤销已经交付的证书。钱包补交直接联系发行成员和委员会，不依赖原网关；钱包离线期间仍可能暂缓推进。持证方都能重传；原票查询可协助组证，但在诚实成员分票、恶意成员隐藏票时不保证可从节点重新拼齐。

安装 ACK 只结束对应传输尝试。委员会最终保管完整证书、必要材料与认证效果后，按工作表产生明确的 WorkCompleted/CustodyAllowed 事实，才能结束主动公共投递并将未送达成员转为按需同步；HTTP 成功、裸 REGISTERED 或 FUEL 托管均不够。该规则只替代已定义的传播/安装同步义务及其 E 阶段，不能提前核销结算/收尾，后续同步计入原计划或有限维护容量。成员释放 B 仍需核对公共依据、自己的安全替代和持久交接，FeeClose 不等待所有安装 ACK。新传播待办去重并计入原 B/维护容量，不能成为另一条无界队列。

READY 的含义是钱包取得可续花 SpendQC，不是已有法定人数保存完整证书。证书传播前所有完整持证者离线时可停滞，保留已签锁/残余；至少一个完整持证者恢复重传及依赖/委员会可用，才有相应推进条件。始终凑不成 QC 的局部候选可能无持证者可补救，其锁和原占用继续保留，资源有界不等于可自动排空。发布与测试明确这些可用性变化。

性能测试可用 `delivery_wait=spend_qc|installed_q` 比较同一版本证书的交付门槛，默认且唯一线上模式为 spend_qc；installed_q 仅限隔离基准，ACK 计数不是协议授权或旧版恢复支持。另保留 `verification_reuse=on|off` 对照，测量父验签数、取证字节、批准写盘和后台安装成本。

## 8. 数据库：bbolt 与最小提交保障

### 8.1 默认选择与实际边界

业务状态默认使用 bbolt，每个组织成员与每个委员会应用各有自己的数据库文件。它提供纯 Go 嵌入式 KV、多读单写和可串行化事务，适合首版降低部署与事务实现成本。[S5] CometBFT 仍使用自身的共识日志、块存储及所选后端，不能把整个节点宣传为“仅一个文件、一个依赖”。

bbolt 的优势是简单，并非已经证明具有本系统最高 TPS。首版先测短事务与组提交；只有它被数据确认是瓶颈后再评估 Pebble 等存储。替换时必须同时解决读—检查—写隔离，不能把原子 WriteBatch 当作可串行化资金事务。业务规则及同一套存储契约测试保持不变。

不引入额外数据库服务器、分布式事务、应用层自制 WAL、定时数据库快照、恢复编排或自动迁移。正常同库重启使用数据库自身保障；损坏或旧备份不能作为同身份继续签署的起点。

### 8.2 写事务模板

成员 CommitLoop 从有界队列取得一笔或已到达的一小批请求，开启一次 `DB.Update`。每笔成员操作针对“已合并 overlay + 当前库”重查条件，在独立子 overlay 内生成 Transition；业务拒绝只记录该请求结果，成功才合并。最终统一写记录、动作与 outbox，提交成功后再发布缓存、签名或响应。公共融合命令按第 6 节逐阶段合并，不把“本金暂缓”当成整个命令回滚条件。

业务拒绝只丢弃该笔子 overlay；磁盘错误使整个写事务失败，该批没有任何对外成功结果。若底层返回结果无法排除持久提交已发生，则停止该实例后续权威写入与认证输出，并核对同库提交点和原事实，不能盲目再扣、回滚额度或签第二个候选。客户端超时也不撤销已经持久的承诺。

禁止 `NoSync`。禁止在事务内网络请求、等待外部锁、签名发送或更新事务外权威余额。首版自行管理组提交，不使用 `DB.Batch` 回调承载外部副作用；官方指出该回调可能被执行多次。[S5] 签署意图已持久后，从相同域和摘要重建 Ed25519 签名可以幂等重发，不必为了可重新计算的签名字节额外增加一轮 fsync。

`DB.View` 生命周期保持短小，取出的 mmap 字节在离开事务前复制；网络发送及证明组装不能持有读事务。事务对象不跨 goroutine 共享。长时间读事务、频繁文件扩展和组提交等待分别计入测量。

### 8.3 目录、打开与退出

每个实例独占 `data/<network>/<role>/<node-id>/`，业务库、Comet 数据和身份配置分别命名；进程启动校验 NetworkID、角色、成员身份、schema 版本与配置摘要。数据目录/签署身份单活，不能复制一份成员密钥并在另一数据目录并行运行。文件锁只能约束同一文件，运维也必须保证身份不被克隆。

正常退出停止新准入，完成正在提交的事务，保留未投递 outbox 后关闭库。重启从已提交记录继续同身份重传和待办；不重新获得一份预算。原 Worker 不存在时，合法返还进入对应公共池；重分配须核对代际并原子移动尚未占用份额。

首版归档已完成正文与证据，但不裁剪安全记录。B 逻辑权限完成交接不等于磁盘字节已经释放，输入消费墓碑、来源索引、防重键及累计检查点持续占空间。配置物理存储高水位与承诺保留空间，到限时停止新准入，继续为既有承诺服务。分段归档降低活动库压力，不提供无限期固定容量保证。

### 8.4 只追加分段归档

由 `internal/store/archive.go` 实现分段读写和定位，成员/委员会现有后台维护队列调用它；不新增独立服务。运行中先按原事务写入业务库，只有已终结且无未完成依赖需要直接读取的不可变正文/证据才能搬走，首版不把签发改成数据库与文件的双写事务。

活动库继续保存未花 UTXO、未决锁/责任、授权/预算、消费/来源/动作防重和累计核销状态。归档保持原规范字节与编码版本，保留历史配置、事实叶、树索引及认证头等证明重建材料；读取统一通过存储适配，缺少归档返回明确不可取，不能解释为“从未消费”或“允许再核销”。安全记录不采用 TTL。

本地新增两类记录，不参与公共逻辑修改哈希：`ArchiveSegment(SegmentID, FormatVersion, HeightRange, Size, Digest, Location)` 和 `ArchiveLocation(ObjectKind, ObjectID, Revision → SegmentID, Offset, Length, Digest)`。SegmentID 使用封存文件内容摘要，文件内保存可校验记录边界及对象身份；达到配置大小即封存，单条记录仍受原大小上限约束。跨节点物理分段不同不能改变逻辑读取、事实历史或 AppHash。

交接按以下顺序执行：

1. 短读事务复制一小批合格对象并关闭事务，使用受限维护资源写临时段，不能持有数据库事务等待文件或网络操作。
2. 可靠写入文件，校验大小和摘要，完成封存文件名及目录的持久保障；平台不能保证这一点时保留库内副本。未封存或校验失败的段不建立可用定位。
3. 在短写事务重查归档资格与对象版本，原子写入段定位并移除库内冗余正文；不删除其逻辑身份、终态或防重信息。读取方依据同一定位状态取到原对象。
4. 中断后按已有定位重试，未被引用的文件不得冒充已交接；原副本或已可靠封存的文件至少有一份可用。迁移封存段时先验证目标副本、持久切换定位，待本地读者结束后才移除原文件。首版用文件目录后端即可，远程对象存储非必需。

组织 B 的释放仍独立核对公共交接许可与本地安全替代，归档不自行创造返还。委员会应用归档不自动裁剪 Comet 块存储/WAL；引擎历史仍计入容量，首版不改变其保留行为。

bbolt 删除正文仅产生库内可复用页，不自动缩小文件。[S5] 应周期归档并复用空闲页；已有大库需要缩小时，在维护窗口停止该实例、复制压缩并校验同一提交点/身份状态后切换，保留原文件至验收，禁止新旧实例同身份并行。首版只要求轻量归档，在线压缩及自动压缩编排不在范围内。容量监测分别记录活动库、可复用空间、归档段、永久索引估算和维护临时空间。

## 9. 委员会执行与最终证明

### 9.1 ABCI 提交边界

`CheckTx` 和并行预检只检查可重复的格式、授权、证据与上限，不据此扣真实余额。`PrepareProposal/ProcessProposal` 对材料和区块界限作确定性检查；本地缓存缺失时能够验证提案携带的实际材料，不能用联网结果决定接受与否。

`FinalizeBlock` 在最近已提交状态上建立块级 overlay，按共识顺序执行命令及其明确阶段，返回确定的 AppHash 和结果，但不永久提交。`Commit` 同步原子持久状态、事实、动作结果、最近高度/区块身份/AppHash 与应用 outbox 后才返回。`Info` 报告最后成功提交点；正常握手与重放遵循 Comet 0.38 的提交语义。[S6]

同一已提交区块的重复处理返回原结果；高度相同而区块身份不同不能覆盖。查询默认读取已提交状态；未提交 overlay 不得产生可供核销的最终证明。Comet 应用与引擎按同一实例生命周期管理，不自行跳过引擎的重放检查。

### 9.2 DAG 后台调度

DAG 是交易依赖的有向无环图：父输出先被建立，子交易才能在公共账本消费。快速路径验证直接父证据，后台对必要未最终祖先拓扑执行；不维护逐输出的根金额贡献谱系。

调度器以 RootID 和 TxID 合并待办；先独立落实必要根，再按依赖推进。多个未落实根合并时，每个根全额且只落实一次，随后执行合并交易。一个根已完成而另一个仍缺材料时，保留前者结果，不能重扣资金。REGISTERED 不是可截断依赖的最终锚点，只有合法 SETTLED 事实可以。

依赖可跨块分段推进，每个不可分割动作在准入前已证明能放入一块。缺少依赖记录 `DEFERRED`，不关闭结算身份；后续补证只执行尚未完成的 ActionID。块末合并同键最终变化，同时保留逐笔授权、逻辑计费和动作历史。规则不因物理批次、缓存命中或本机耗时改变费用。

证明服务、核销投递和后台任务拥有保留资源，不能等新增交易占满 CPU 后才尝试工作。共识、公平调度和数据最终可得是推进前提；四节点中故障超出容忍范围时不承诺继续结算。

在 `internal/member/admission.go` 实现实测安全工作速率上限及高低水位准入调整，复用 E/B 硬约束；已有 INSTALL/结算/核销使用原预订和独立服务份额，不能被新业务限流一并阻断。阶段指标区分待执行工作量、证明字节与待应用核销量，不把交易条数视为等量工作，也不把实际 FUEL 支出视为核销欠账。

`internal/committee/dependencies.go` 以父事实索引和持久游标有界唤醒待办，避免全队列扫描；预检及块内复用已解析的不可变证据和状态对象。outbox 可按原事实/资源/账户或政策/版本/阶段合并尚未送达的累计通知，仅保留可独立验真的最新累计值并共享认证头；原动作、证明与接收方差额校验不省略。批大小有限，低负载不等待凑批，不调整既定计费语义。

### 9.2.1 散户直接执行

`EvaluateDirectTransfer` 仅接受 DIRECT_TRANSFER 和 FinalInput，在区块 overlay 内读取最终 UTXO 并检查 SpendOrg=COMMITTEE、所有者签名、唯一消费、金额及费用上限。一次原子提交本金/FUEL 输入消费、收款/找零输出、确定费用和结果；找零输出采用正文固定的接收条件及索引，金额由确定计费规则推导并校验，重试不改变 OutputID；失败不留下半笔本金或费用扣款。它不创建组织 ApprovalRecord、RootObligation、WorkCredit 或 TXCer。

此路径与 SettleTransfer 共用 UTXORecord/OutputSpend，不能借不同命令类型重复消费同一 OutputID。新输出可以是组织收款描述或 COMMITTEE。费用只计算实际委员会工作和适用销毁，按签署费率和确定逻辑工作量计算，实际费不超过 Fmax，不收组织认证及服务费；首版直接交易使用 OWNER_FINAL_UTXO，自付输入须同样为 COMMITTEE 路由，先于快速自付专项实现。

### 9.3 轻量最终事实证明

首版将终稿中的 FinalStateProof 实现为 **FinalFactProof**：只证明已提交公共状态中的正向事实，包括授权、根落实、输出创建/SETTLED、输入消费、CAL/FUEL 累计核销、E 完成、B 交接许可、费用分配/关闭。它不提供任意当前余额、未消费、不存在或任意数据库快照一致性证明。这是需要在创世配置中冻结的工程证明格式，不修改付款和核销的业务条件。

事实由成功状态转移产生，与相应状态同批持久。每块按 `(FactKind, FactKey, Revision)` 的规范键排序；同块同 FactKey 只保留最终 revision 及累计值，中间阶段保存在动作历史。不同业务事实使用不同 FactKey，创建与消费不能覆盖彼此。重传不增加 revision，失败子 overlay 不产生事实。Fact 正文不含自身证明、AppHash 或 Merkle 路径。

对有权威变化的高度 h 定义：

```text
WriteSetHash_h = H(规范排序的最终公共逻辑修改)
FactRoot_h    = Merkle(本块规范事实叶)
AppHash_h     = H(RootScheme, NetworkID, h, PreviousAppHash,
                  WriteSetHash_h, FactRoot_h)
```

逻辑修改排除物理页、节点本地 outbox、缓存、观测指标和本地提交高度；事实历史记录也是公共逻辑状态。无权威变化且无新事实的空块沿用原 AppHash，仅更新本地提交元数据，避免为了认证上一空块又不断制造新的状态根。创世根和空叶根在 RootScheme 明确固定，不能由不同库的默认空值决定。

采用 CometBFT 原生按需出块：`CreateEmptyBlocks=false`、`CreateEmptyBlocksInterval=0`，由交易可用通知和 `needProofBlock` 推进。持久依赖队列的实际出队和游标变化纳入上述逻辑修改，使有界 Drain 在必要区块中分批收尾；队列无工作时不制造伪变化。中继投递与取证由本地时间驱动，不依赖空闲高度持续增长。无需新增周期唤醒命令；共识超时和客户端轮询分别调优。

采用锁定版本的 Comet Merkle 算法构树和验路径，[S7] 不自行发明另一套树算法。`finality` 包隔离该依赖；`protocol` 保存规范类型，不导入 Comet。最初用普通单叶证明与共享头即可，不预先实现复杂多证明压缩。保存每块叶值与位置/内部树索引，按需组装路径；不必永久重复保存所有叶的完整路径。

证明包包括 Fact、Total/Index/路径、RootScheme、实际变化高度、PreviousAppHash、WriteSetHash、FactRoot，以及 h+1 认证头和 commit。验证器从受信 NetworkID/ChainID 与固定委员会配置出发，验证委员会 commit、头高度和 AppHash，再验证应用承诺及事实路径、事实类型/键/版本/上限。不能相信证明包自带的一组陌生验证者。应用保持四个等权委员会成员，不启用动态验证者集更新。

本地 Commit、h 块事件或裸头都不够；证明必须取得认证 h+1 头。配置空闲时仍能产生必要证明块，并做实际端到端测试。证明未到时 Outcome 标注 `proofPending`，不假称用户已验证最终性。

历史证明的有效范围：Grant 证明曾经授予，使用仍需本地版本屏障；输出创建证明不说明现在未花；累计核销在原事实/原资源/原版本内只应用更大累计值。退役、候选取消将来必须有显式正向事实，不能用“查不到”释放锁；首版不启用这些恢复动作。该增量承诺不能验证任意导入快照，禁用应用 state-sync 快照导入。

每类事实的消费端约束必须直接写入校验函数：

| 事实 | 必须认证的载荷 | 允许的本地变化 |
|---|---|---|
| OrgRegistered | OrgID、纪元、成员公钥/索引、q、OrgConfigHash | 验真后保存历史节点配置；不自动更换既有输入路由 |
| GrantFact / PolicyGrantFact | 资源、账户/主体、版本、配置、唯一 Grant 身份、增量/累计授权 | 原 Grant 防重后增加合法配额，不越过退役屏障 |
| OutputSettled | OutputID、创建身份/索引、资产金额、OwnerLock、SpendOrg、最终资金落实与该输出的结算语义 | 补齐最终创建事实、作为该输出祖先截断边界；不覆盖消费墓碑 |
| OutputConsumed | OutputID、唯一消费 TxID 及最终效果 | 单调记录消费，不再生成第二次价值 |
| RootFunded | RootID、条款、RootFundingID、来源/备付分支、固定根输出 | 同步根落实；不能自行认定垫付已经收回 |
| CumulativeCredit | 原批准事实、资源授权键、原 cap、累计计数和阶段 | 找到本地原占用后应用已认证差额 |
| WorkCompleted / CustodyAllowed | 原 SpendFactID、阶段/字节、累计完成量；若为公共保管替代，绑定原工作表规定的替代类型、认证效果及已提交材料清单/保管事实 | 只核销实际完成或按规则已合法替代的 E 阶段；B 还需本机安全替换与持久交接；不以单独托管费用或 REGISTERED 代替 |
| FeeClosed / RewardVested | FeeID、ActionID、确定费用/归属/退款与终态 | 同步费用/收益事实，不直接释放 CAL 或所有工作额度 |

历史事实成立、当前状态允许本次操作、必要正文实际可得分别检查。单个 `verified=true` 不能代替三者；验真后的状态变化仍进入 CommitLoop。

### 9.4 必须一起保存的提交结果

Commit 除业务数据外保存足以重现 `FinalizeBlock` 的确定性响应，包括交易结果、事件及参数更新结果（首版动态成员更新为空）、父提交点与区块身份。正常重放不得重新收费、增加累计 revision 或生成第二组业务事实。以上是正常提交契约，不是新增备份恢复系统。

## 10. 接口、事件与钱包契约

### 10.1 传输规范

首版采用标准库 HTTP 长连接，跨主机启用 TLS；内部节点端点使用节点身份认证，外部用户请求仍独立验证业务签名。热点正文使用 `application/vnd.utxofast.v2` 二进制消息，控制/查询投影用 JSON。协议签名和身份使用第 4 节字节，不使用 HTTP JSON 原文。压缩、超时、连接数和最大解压字节均有上限。

下面路径保留 `/v1` HTTP 路由前缀，消息 WireVersion=2；路由版本与证书版本分别校验，语义以本版协议为准。接口请求有 RequestID 用于观察，但 RequestID 不参与资金防重。

| 接口 | 请求 / 响应 | 成功含义 |
|---|---|---|
| POST /v1/transactions | SignedTx + 有界 EvidenceBundle → SpendVote 或当前 Outcome | 成员返回票表示本地已持久批准；网关 202 仅接入 |
| POST /v1/certificates | 本版 TXCer/必要父材料 → InstallAck | 成员本地已持久安装；不决定 READY，不授权核销；不作为委员会接纳前提 |
| GET /v1/txcers/{txid} | → TXCer 或带缺失阶段的 Outcome | 有完整合法 TXCer 才可由钱包验收 |
| POST /v1/direct-transactions | SignedDirectTx → TxID/Outcome | 委员会接入慢交易，不返回 TXCer，钱包等待最终证明 |
| GET /v1/organizations/{orgid}/configs/{hash} | → OrgDescriptor + 登记证明/创世引用 | 首次通信建立可信缓存；接口返回本身不赋予信任 |
| POST /v1/commands | Command/有界批/证据 → CommandID、接入结果 | 仅进入公共处理，不等于 SETTLED |
| GET /v1/outcomes/{id} | → 已完成阶段、尝试结果、FactRef、proofPending | 明确区分本地观察与有证据终态 |
| GET /v1/receipts/{fact} | 可指定 revision 或 latest → 累计事实及 FinalFactProof | latest 是服务查询选择，不是“绝对最新”密码学证明 |
| GET /v1/evidence/{hash} | → 有界规范证据对象 | 接收者按哈希和规则验证，服务端不赋予权威 |
| GET /v1/events?after=cursor | 有界长轮询批或 SSE → Event[] / nextCursor | 至少一次通知；可断线补拉，不直接核销 |
| GET /healthz、/readyz | → 存活、同步/预算/存储/承诺队列服务状态 | 不把“进程存活”解释为可签署 |

出站 Collector 的提交和返回封装不能改变 TxID。公共提交响应与最终证明分离，避免让 HTTP 长连接一直等待共识。网关可替换；钱包可以查询多个成员并合并相同事实的有效签名，不把网关数据库当成付款真相。

### 10.2 返回与错误

Outcome 分字段表示 `FastPhase`（COLLECTING/READY）、`LocalApproval`（NONE/APPROVED_LOCAL）、`LocalInstall`（NOT_INSTALLED/INSTALLED）、`PublicPhase`、`FeePhase`、`LastAttempt`、证据引用及本地观测高度。READY 必须有完整 SpendQC，独立于本地安装及公共登记；SETTLED 不能只凭登记或某个根落实推断。

同时返回 `BusinessStatus` 与 `ProofStatus`：本地公共执行已提交而证明待取时为 `APPLIED / PENDING`；客户端取得并验证证明才报告 `VERIFIED`。202 的接入队列是否持久必须由接口能力声明，默认不保证，钱包保留请求负责重试。HTTP 断连只取消响应等待，不撤销已提交业务。

| 代码 | 含义 | 重试规则 |
|---|---|---|
| WRONG_SPEND_ORG / FINAL_INPUT_REQUIRED | 输入路由不匹配或散户引用未最终输入 | 不换身份强行重试；返回输入规定的处理路径 |
| INVALID_ENCODING / INVALID_AUTH / INVALID_RULE | 本次正文或认证封装非法 | 修复封装可保留正文身份；换正文遵守 IntentID 规则 |
| LOCAL_CONFLICT | 本地局部锁冲突，未声称公共终局 | 查询原事实；合法 QC 的裁决由 INSTALL 规则处理 |
| FINAL_CONFLICT | 已验证终局消费冲突 | 返回可验证事实，不再把相同效果作为新付款尝试 |
| DEFERRED | 缺依赖/证明，业务未终止 | 同业务身份补证或等待事件，已完成动作不重做 |
| LIMITED | 配额、未决量、队列或存储限制 | 明确限制维度与建议重试时间；不是释放旧占用的许可 |
| UNSUPPORTED | 未启用功能、算法或规则版本 | 不以空实现返回成功 |
| COMMIT_UNCERTAIN / UNAVAILABLE | 持久结果不明或节点暂不可服务 | 查询同身份，节点暂停新增认证；不另造付款 |

HTTP 400/401、409、413、429、503 只是外层映射，业务码及其范围必须完整返回。某成员拒绝不等于组织终局失败，网络错误也不等于交易未发生。对已成立证书不能以新准入额度不足拒绝 INSTALL。

### 10.3 Outbox 与事件

事件至少包含 `NodeID、DBGeneration、Sequence、EventKind、BusinessID、FactKey/Revision、ActionID、EvidenceRef、ObservedHeight`。游标只在同一节点和同一数据库代际有效，不构造虚假的全网序号。更换节点后从事实查询和新的游标衔接；返回 `CURSOR_MISMATCH` 或 `CURSOR_EXPIRED` 时提供重查起点。

业务状态与 outbox 原子提交。投递成功标记失败或重复投递均安全，消费者以 ActionID 或 FactKey/Revision 防重。不同资源核销不能仅按 TxID 去重。消息丢失靠游标补拉和事实查询补足；没有 exactly-once 网络假设。事件签名也不能替代最终事实证明。

DBGeneration 在首次建库时保存，同库正常重启保持不变。先验证认证事实，再推进已应用 revision，不能让伪造的大 revision 屏蔽真实证明。内部可靠 outbox 与外部观察事件分开管理，任意订阅者不确认不能无限阻止任务清理。事件流规定每页字节/条数和保留范围，过期订阅通过钱包未决意图与已知事实补查；清理观察事件不删除安全事实或仍有保管义务的数据。

### 10.4 钱包

钱包本地创建密钥后生成收款描述：组织用户选择服务组织，散户使用 COMMITTEE；模式由描述确定，不设置资产进入/退出按钮。来自不同发行组织的输出可加入同一输入池，只按本次实际 SpendOrg、金额和依赖成本选择。

付款前保存 IntentID、正文和签名，超时重传同一正文。组织钱包验证本次完整 QC、金额、所有者、SpendOrg、网络/配置/规则及输出索引，原子保存 TXCer 及向发行成员/委员会补交的 outbox 后显示“可继续支付”，无需安装 ACK；最终后用相同 OutputID 更新状态。散户钱包只保存和使用最终 UTXO，收到组织的待结算付款时显示“待最终确认”，不把 TXCer 计入可用余额。

界面仅需“处理中、可继续支付、已最终、需要等待/同步”等状态。散户交易没有组织批准/READY 阶段。事件增加 `PeerConfigCached、TxCerReady、CertificateInstalled、DirectTxFinalized、OutputSettled`；CertificateInstalled 仅指本地持久安装，TxCerReady 需附完整 SpendQC，公共状态仍以最终证明为准。付款或找零重复到达不增余额，输出在途按原身份查询，不因换网关改写 SpendOrg。

## 11. 费用、奖励与资源周转的实现分工

本节生命周期托管与组织核销用于快速交易；散户直接交易的实际计费及原子提交按 §9.2.1。

`FeePlan` 固定 Fmax 的组成、动作和次数上限、费率版本、尾款条件及受益策略。首次合法 SpendQC 登记一次性建立 FeeEscrow，不要求安装回执，保持 `Fmax = PaidRewards + Burned + Refunded + Held`。每个动作只有一份收费/奖励事实；缓存命中不减费，重复网络处理不加费。

FeePlan 是 FeeTerms 中的有界生命周期动作计划，不能另建一份可在签署后修改的计划。根导入支付自己的落实与有限收尾，后继交易支付自己的认证和结算，不把祖先计划当作无限 Gas 账户。

| 费用分支 | 成员 PREPARE | 委员会首次登记 | 后续核销 |
|---|---|---|---|
| 组织/商户预存代付 | 占用 Fmax 的对应 FUEL 覆盖，并独立占商户政策与 E/B | 从原授权覆盖实际转入 FeeEscrow，FeeID 防重 | 只给原签署者按合法责任解除/真实回补恢复；实际净支出保留 |
| 用户最终 FUEL 自付 | 对 FeeTerms 指定的已最终 FUEL 输入验权并与本金输入原子锁定，组织 FUEL 覆盖占用为零；E/B 等仍检查 | 一次消费费用输入并建立托管；找零和退款遵守签署条款 | 不虚构组织 FUEL 原占用或返还；其他实际占用仍按自己的规则核销 |

费用输入与支付输入有独立用途标记，跨两集合检查重复。资金转移阶段不能再消费已在登记阶段使用的费用输入。公共费用变化、托管和支付输出分别保持守恒；接口不能仅返回“已扣费”而省略具体消费事实。已转入托管但仍可能退还的 Fmax 不可当成已最终净支出申请 RestoreFeeCoverage。

费用来源枚举为 `ORG_RESERVE` 与 `OWNER_FINAL_UTXO`，失败不自动切换。FAST_TRANSFER 首阶段启用预存 ORG_RESERVE；其 OWNER_FINAL_UTXO 分支按开发计划的费用模块通过专项验收后启用，费用输入必须已最终且 SpendOrg 与本次组织相符。DIRECT_TRANSFER 随公共结算实现最小 OWNER_FINAL_UTXO，按 §9.2.1 与本金原子执行，不依赖快速自付专项，不创建虚构组织占用。

同组织、跨组织使用相同费用规则；不以跨组织为由增加额外转接费，不按本机缓存命中变更应收费用。实际资源收益通过成本及性能报告衡量；若将来调整报价，使用公开版本化费率。

组织真实账户与已落实用户 UTXO 资金分开。保持终稿 `A = F + ΣR_active + ΣK_retired`、`R_v = G_v - P_v` 的覆盖科目约束；成员的总额度由累计合法 Grant 决定，不根据委员会当前可见义务数量自由提款。首版不实现受控退役，就不提供减少既有覆盖后继续使用原授权的接口。

四成员奖励份额预先有界，不能因收集器只采用三份票就把第四份报酬转给前三者。工作证据验证与领取期限是确定规则，奖励先累计账户再批量领取。中继收款人由原服务策略和 FeeActionID 确定；实际推进才取得对应报酬。

ClaimReward 绑定受益人授权、领取金额、领取 ActionID、目标输出及费用上限；同事务更新可领/已领累计数和生成输出。领取费由预先授权的奖励内扣或明确费用来源支付，重传不能再次领取。FeeActionID 是费用范围内的稳定 ActionID，不是按网络包新建的身份。

组织服务费阶段对应 READY 登记、公共支付完成和本笔必需收尾。20/40/40 仅作为显式可配置的实验政策，固定到签署费率版本，禁止对旧 READY 追溯改价。赔付完成可以构成履约，追偿成功不是尾款条件。罚没只执行可证明违规的固定规则，使用独立 FUEL 保证金和可处罚收益；不从受保护 CAL 覆盖扣款，不按自报延迟自动罚款。

首版自动处罚关闭并返回 UNSUPPORTED。局部 PREPARE 后安装另一笔合法冲突 SpendQC 是允许的收敛，安装 ACK 不是另一张消费签票；不能将两种消息混为双签。后台安装与父证书转交的有界成本计入原 FeePlan/E/B，不按 ACK 新设费用阶段或奖励。CertifiedEffects 认证确定权利/义务，不声明公共扣款或根资金已完成。公共保管/结算/收尾事实驱动原工作核销，ACK 不产生 E/B 信用；FeeClose 不等待全部成员安装或全部本地 B 释放。

核销处理分为：证明验真 → 按原事实及资源找到本地占用 → 核对原 cap/授权/阶段 → 应用累计差额 → 同事务更新残余、AppliedCredit 和 Worker/公共池。若该事实公共处置已被观察，不能随后新建一次首次占用来领取返还。B 还必须满足本地安全交接；只拿到交接证明不足以提前删除必须保存的数据。

例：费用上限 100，实际奖励 84、销毁 10、退款 6 到原活跃覆盖，则仅恢复 6；随后真实补回原净支出 40，再恢复 40。若退款 6 到外部钱包，不恢复组织覆盖。RestoreFeeCoverage 不重置商户累计限额。

### 11.1 低水位真实补资

`internal/wallet/funding.go` 在已有资金方进程内运行授权补资任务，复用普通真实转账、TopUpAndGrant 和 RestoreFeeCoverage，不增加支付前台调用。配置资金来源/目标、资产、低水位、目标余额、单次及累计授权上限；按净消耗速率、到账与授权可用延迟和突发量确定缓冲。只能使用可自由支付的真实来源，不能挪用保护用户的覆盖。

同一资金目标首版只允许一个未完成补款请求。`FundingRequest` 持久保存 RequestID、来源/目标/资产、金额、分类、原支出引用（如适用）、原交易身份与提交状态，借用钱包意图和 outbox 幂等提交；超时先查原结果，不创建第二笔付款。在途金额只抑制重复申请，到账最终事实及相应授权/核销应用完成后才增加可用额度。新增 Grant 与补回旧净支出互斥，商户累计政策上限不因充值重置；重复/重启和多触发者共用同一持久请求，首版资金任务保持单实例。

实验支持大额有限创世资金及授权正常补款，禁止无限金额、清占用和免验费用。报告充分供资与受限供资两类结果，记录真实外部补款和净消耗。正式运营区分 FUEL 预付/服务收入与 CAL 资本/回款；无资金或授权不足时停止新增补款/相应准入，不放弃原承诺。供应与销毁按目标服务周期单列测算，低水位任务不增发 FUEL，也不保证服务长期盈利。

## 12. 架构参考生成器规范

### 12.1 定位与输入

`archgen` 是后续开发工具，可在首个支付闭环之后实现。它从经人工确认的显式 schema 生成结构参考；本次只规定契约，没有交付已可运行的生成器。

输入使用严格 JSON：`objects.json` 定义字段类型/顺序/界限/可选性，`keys.json` 定义逻辑记录与键，`api.json` 定义消息和错误码，`rules.json` 映射协议条款、负责模块和独立测试 ID。`manifest.json` 固定 ProtocolVersion、SchemaVersion、RootScheme、各文件摘要。使用标准库严格解码，拒绝未知字段、重复身份和引用缺失。

### 12.2 允许与禁止生成的内容

首版只生成字段/键/API/规则追踪目录、Go 结构类型、枚举和键命名空间常量。规范 codec 与身份派生先手写并通过独立黄金向量，schema 稳定后才能单独评估编码生成。资金转移、准入计算、费用分配、证明信任判断和取消规则保持手写、可测试，不由模板臆测。

生成器仅写声明的 `zz_generated_*` 文件和生成文档，文件头带 schema 版本/摘要。不得覆盖手写规则、修改数据库或自动迁移线上状态。不把未实现状态转移生成成 `return nil`；需要占位时返回明确 `ErrNotImplemented`，测试也不能默认跳过并宣称通过。

预期命令为 `go run ./cmd/archgen --check`（检查生成物是否一致）与 `--out <目录>`（输出结构参考）；这是未来接口，不是目前可执行命令。生成顺序稳定，运行两次字节一致；路径规范化并限制在指定输出目录。生成后做 gofmt/语法检查，不能把编译成功当成协议安全证明。

### 12.3 验证等级

独立黄金向量由协议示例及人工审核的第二种实现产生，至少记录规范字节、TxID、各签名摘要、非法输入和预期拒绝原因。不得用同一个生成器同时生成编码和“正确期望”然后互相验证。生成器证明结构一致，独立状态测试检查业务语义，多节点实验检查真实行为，三者分别报告。

追踪矩阵分别标记测试“已声明、已实现、已运行及结果”，空测试、跳过测试和占位错误都不算通过。生成器不进入节点启动路径，正常构建使用已纳入版本管理的生成物，不依赖在线模型服务。

## 13. 校验与测试基线

### 13.1 分层测试

| 层次 | 输入与方法 | 通过标准 |
|---|---|---|
| 编码/身份 | 独立 golden vectors、畸形输入、字段变造、fuzz | 相同逻辑内容唯一字节；非法表达拒绝；必要字段全部受认证 |
| 纯规则 | 小状态场景、金额边界、操作序列、独立慢参考模型 | 守恒、权限上限、终态单调、幂等；参考模型不复用同一资金计算函数 |
| 存储契约 | 内存实现和 bbolt、子 overlay、读写隔离、错误注入 | 失败不留半笔；事务成功之前无成功回执或签名；同库重启一致 |
| 成员协议 | 四个独立成员、真实签名、乱序/重复/超时/一个失联 | q 证据可形成并验证；冲突消费不能同时成立；所有原占用正确 |
| 公共执行 | 相同前态与命令，不同缓存/到达顺序/机器 | 规范顺序固定时状态、AppHash、结果、费用完全一致 |
| 端到端 | 至少两组织各四成员 + 四委员会成员 + 网关 + 组织/散户钱包 | 跨组织链前续花、散户最终收付，后台完成，证明可验，原权限按规则周转 |
| 负载/可用性 | 开环业务、额度补回、稳态/突发/一故障 | 吞吐、长尾、队列、资金/存储占用一起报告，不只报峰值 |

最小集成测试使用真实 bbolt 同步写、实际 Ed25519 和实际 Comet 提交。内存模型可以做快速性质检查，不能取代持久认证的端到端测试。

### 13.2 必须具名的回归用例

| TestID | 场景及必须观察的结果 |
|---|---|
| C01 | 不同 SpendQC 有效子集导出同一 SpendFactID，重复签名成员不增加票数；EffectsHash/完整向量变造失败 |
| C02 | 不同网络、CertifierEpoch、配置、用途签名不可互换；输出改一位校验失败 |
| C03 | uint64 上限、负向减法、乘法中间溢出、各 Grant 向下取整均正确 |
| M01 | 同输入两个候选并发，最多一个合法消费；组织内、跨组织付款及向散户付款都竞争同一输入键 |
| M02 | 子已消费父输出后父 INSTALL 迟到，只补创建事实，不复活余额 |
| M03 | PREPARE 任一资源不足则全部不占；分维度各有三成员但共同可签不足三时不 READY |
| M04 | 合法 QC 与本地局部锁冲突，按 QC 裁决输入；旧候选额度无终局证明不释放 |
| M05 | INSTALL-only 成员保存证据且不恢复从未占用额度；新准入限流不拒绝合法 INSTALL |
| M06 | 同 TxID 无效认证封装先到，不阻断后来合法授权；同 IntentID 改正文无作废证据拒绝 |
| M07 | 同一批第一笔成功、第二笔业务失败，只提交第一笔；整批磁盘失败均无对外成功 |
| M08 | 仅有入金申请、伪造输入或已消费输入均不能签发；合法 TXCer 可在公共结算前续花且不重复占用同一根本金 |
| X01 | 甲→乙→甲组织用户在最终结算前连续付款；每笔只有付款方组织一轮签票，安装延迟期间收款方验完整证书后仍可续花，无交接 QC |
| X02 | 首次取得受信配置后缓存，离线验签；错组织、公钥替换、重复成员票或错误历史配置被拒绝 |
| X03 | 甲/丙签给乙用户的输入由乙合并，原根责任分别保留；后继组织无重复 CAL 占用，最终 CAL 守恒 |
| X04 | 同输出送往两个组织只能由指定 SpendOrg 处理；篡改收款描述/路由、裸签绕过组织、凭证与最终态双花均拒绝 |
| S01 | 散户→散户/组织用户只用最终 UTXO、直接委员会、自付实际费用，无 TXCer 或组织预算；重复提交仅执行一次 |
| S02 | 组织→散户：最终前不可用，最终后可直接付款；交易与费用失败无部分扣款 |
| S03 | 修改钱包服务设置不改变已有输出；已有散户输入经普通付款产生组织输出后才能快转，组织输出路由不能被直付绕过 |
| L01 | 验证复用开/关、冷热缓存、正常重启的输出/费用/责任和接受结果一致 |
| L02 | 本地仅有 SpendVote、候选输出或安装 ACK 时仍须验完整父 SpendQC；未验证记录不可写入 VerifiedParent |
| L03 | 缓存有效父事实后输入已消费仍拒绝再次花费；无效封装不污染合法重传 |
| L04 | 同组织/跨组织 × 冷/热缓存对照，记录真实验签数、取证字节、同步提交数与 READY p50/p99/TPS，不跳过本笔授权 |
| B01 | Credit 重复、乱序、迟到只应用累计差额；公共处置先到后不得新建占用再领返还 |
| B02 | CAL/FUEL/E/B/政策项互不混淆；B 缺本地安全交接不能恢复；商户上限不会被补 FUEL 重置 |
| B03 | Worker 调配、批准、核销交错；份额无双用，退出 Worker 的返还正确进入公共池 |
| R01 | 同 SourceID 换 nonce/版本不能在原担保组织另立有效根；真实入款跨根全局一次分配 |
| R02 | 原输入先结算、备付先履行、原交易资金迟到回补；根只落实一次，同组输出，无双记回款/新授权 |
| R03 | 不同原担保组织两根拆分再合并、多层 DAG、祖先跨块推进；逐原组织全额一次落实，责任不转移、价值守恒 |
| R04 | 已最终且路由正确的真实资金直接支撑，CAL 占用为零、不建空根；缺证 DEFERRED，不自动垫付，已有根不自动解除 |
| F01 | FeeID 一次托管，动作重传不重付；第四成员奖励不因 QC 子集被转走 |
| F02 | FeeClose 不等待自己或后继、不以 Held=0 为前提，合法终止不支付未挣得利润 |
| F03 | 100/84/10/6/40 示例成立；外部退款不恢复组织覆盖，补充覆盖不重置商户累计支出 |
| F04 | 自付/代付互斥且不自动替换；自付输入已最终、消费路由正确、只消费一次，找零/退款金额和身份稳定；组织不虚构 FUEL 占用 |
| F05 | 本金 DEFERRED 保留合法登记/托管前缀；补证后只完成剩余动作，授权非法从未扣费 |
| P01 | FinalizeBlock 后未 Commit 不发布最终状态；Commit 后 ACK 丢失不重复执行 |
| P02 | 同库正常重启保留输入锁、原占用、完整 QC 和待投递事实，不新获授权 |
| P05 | PREPARE、INSTALL、核销和公共 Commit 分别在提交后丢响应再重启；原身份、预算、revision、outbox 与确定响应一致 |
| P03 | h 的事实通过 h+1 认证头验真；裸事件/错误高度/陌生委员会/变造路径拒绝 |
| P04 | 同块事实归并、创建消费分键、空块根稳定；不同缓存和节点得到相同承诺 |
| A01 | 收集器失联后由完整持证钱包/成员补交；有足够可取票时可重新组证，只有不齐局部票时允许等待且不解锁 |
| A02 | 一个组织成员失联或拜占庭无效回包，三诚实成员满足资源条件时可推进；超过阈值如实停顿 |
| A03 | 新准入压满时，既有 INSTALL/根落实/证明/核销仍有进展；超大证据在分配前拒绝 |
| O01 | 稳态下工作量、证明及核销待办不持续增长；超载限流、降载消退，旧义务推进；合并通知与逐项通知最终结果一致 |
| O02 | 多周期低水位补款，重复触发/丢响应/同库重启只转款一次；未最终到账不提前授权，同笔款不双记 Grant/Returned，商户上限不重置 |
| O03 | 归档前后旧 TXCer 验真、查询、最终证明、消费拒绝及累计核销一致；未完成依赖保留，节点分段不同不改变 AppHash |
| O04 | 归档写文件、封存、定位提交及迁移各边界中断后重试，无唯一材料丢失；坏段不切换定位，不因缺材料再次付款；报告库/段/临时空间与并发归档时的延迟 |
| Q01 | 阻断所有安装 ACK，钱包取得完整 SpendQC 后可同/跨组织续花；单票/候选输出不被接受，少于三票不可 READY |
| Q02 | 两诚实签 T、一诚实签 U、恶意票只进入离线钱包的 T 证书：无法重建时保留锁及额度，持证者补交后 T 推进，U 不自动返额；另测始终无 QC 的候选，允许长期残余而非虚构排空 |
| Q03 | 子批准事务保存完整父证书/转交待办，子先消费、父后安装、公共核销先于安装均不复活输入或覆盖新状态；外组织导入无原预算资格 |
| Q04 | 委员会无安装 ACK 也接纳合法 SpendQC；缺必要材料 DEFERRED，ACK/费用托管/裸登记不触发 E/B；公共保管替代仅释放指定 E，B 仍需本地交接，FeeClose 不等所有成员安装或本地 B 全部释放 |
| Q05 | 协议/消息版本或签名用途错配拒绝，旧票不能改装；隐藏证书不能被遗漏作废，取消/退役释放入口 UNSUPPORTED |
| Q06 | 批准、网关响应发出但尚未保存、钱包证书/outbox、后台安装各边界中断后按原身份重传；验证网关慢盘不阻塞响应、网关丢失副本后持证钱包重启补交；分别报告前台延迟、证书保管延迟及未决锁/残余额度 |

R 系列和未启用处罚策略明确标记“功能尚未启用、测试待实现”，不能从首阶段测试报告中静默省略后宣称全协议通过。损坏库修复、旧备份恢复、动态成员切换不列为本阶段通过项。

### 13.3 开发检查入口

未来仓库提供 `go test ./...`、适用平台的 `go test -race ./...`、协议包 fuzz、关键纯函数和事务路径的 `go test -bench . -benchmem`。另提供 `bench` 的场景配置和机器可读报告。race 模式用于找竞态，不能把其吞吐作为正常运行成绩。

每次改资金规则运行对应性质/场景测试；改 codec/身份运行全部 golden；改存储或提交阶段运行真实数据库与正常重启测试；改共识适配运行确定性重放和最终证明测试。测试命令、种子、失败操作序列和构建摘要必须可复现。

## 14. 性能与可用性测量接口

### 14.1 业务计数与时间点

`bench` 记录每个业务意图的提出、首次提交、首个批准、完整 SpendQC、收集器持久组证、钱包持久 READY、各成员本地 INSTALLED、公共证书保管、公共登记、SETTLED、最终证明验证、累计核销实际应用与 FeeClosed。端到端时延以同一测试客户端的单调时钟计量；跨节点时间仅作分段观察，不用未校准墙钟相减作精确时延。

指标至少包括：

| 维度 | 测量项 |
|---|---|
| 业务速率 | offered、admitted、rejected、wallet_ready、settled、proof_verified、credit_applied、fee_closed；失败原因分布 |
| 延迟 | admission→钱包持久 READY、READY→SETTLED、SETTLED→证明、证明→额度应用；p50/p95/p99 与超时数量 |
| 本地瓶颈 | 验签 CPU、队列等待、写者等待/事务持有/sync 时间、单笔写字节、缓存和重复解析次数 |
| 后台稳定性 | 每类队列长度/最老年龄/增长斜率、待执行工作量、待传播/待安装证书、证明字节、待应用核销量、outbox 滞留、局部票锁及残余占用、恢复权限速率 |
| 资源与资金 | CAL/FUEL 净消耗/残余、最低可用余量、实际补款金额/次数/到账及授权可用耗时、E/B 占用、政策限额、共同可签成员数 |
| 历史与容量 | 活动库/可复用页、归档段/积压、永久索引估算、Comet 数据、维护临时空间、磁盘增长与承诺保留空间 |
| 内部验证复用 | 父签名检查数、缓存命中率、取证次数/字节、同步提交数；按同/跨组织及冷/热缓存分组 |
| 散户路径 | 提交到最终证明延迟、成功/拒绝数、最终 TPS、实际 FUEL；不计入 READY TPS |
| 服务状态 | 正常/限流/停止新准入/提交不明停签/共识不可用；持续时长和恢复到服务的时间 |

业务身份放日志与抽样 trace，不作为无限基数指标标签。提供本机受限 `/metrics`、可选 JSON 基准导出、受限 `/debug/pprof/`。遥测丢失不得阻断资金提交，监控端点默认不对公网开放。

### 14.2 工作负载和实验对照

至少跑组织内部和跨组织冷/热缓存、散户直付、独立 UTXO 支付、多输入拆分合并、同资金串行续花、TXCer 担保根履行及多根合并、费用不断补回、热点主体、一节点失联、短时突发。独立 UTXO 的吞吐不能代表同一条连续资金链的延迟。

开环调度按计划产生请求，显式报告产生速率、客户端堆积、并发上限和未发出的计划请求，避免只在上一笔结束后发下一笔造成漏计排队时间。分别报告稳态窗口和停发后的排空时间；不能用排空时间之外的结算量冒充同窗口持续吞吐。

建议基准流程为预热、逐级增加 offered load、至少一个足以观察核销和补资多个周期的稳定窗口、停止新业务后排空。窗口长度由资源周转时间确定并记录；短突发另报。初始资金、真实补资、净支出、销毁、数据库大小和后台队列必须一起报告，不能依靠巨额预存额度掩盖不核销。

必要对照包括：同一 SpendQC 一轮交付/额外等待 q 安装 ACK 的隔离基线，本地验证复用开/关及同/跨组织冷/热缓存、单笔提交与机会式组提交、Worker 私额与同单写者下共享计数器、纯转手跳过 CAL 与原路径、证明/核销批量与逐项。每次只改变明确因素。内存模型、单机八进程、跨故障域部署成绩分别列出。

稳定 TPS 是钱包 READY 与后台 SETTLED/证明/核销共同可持续的业务速率，前提是队列不持续增长、资金补给如实计入且故障率受控。当前没有测得 TPS 或延迟数值；任何目标数值均需指定硬件、网络、交易形状和资金条件后再定。

## 15. 配置、实施顺序与验收关口

### 15.1 配置分层

| 类别 | 内容 | 变更规则 |
|---|---|---|
| 创世/协议配置 | NetworkID/ChainID、固定成员及阈值、RootScheme、资产精度、协议规则摘要 | 一致且持久绑定；不能在线改名冒充同一网络 |
| 签署规则配置 | 费率/动作计划、准入策略、输入输出/深度/证据/工作界限、授权引用 | 绑定 RuleIDs 与原条款；新值不追溯否定 READY |
| 本地运行配置 | Worker 数、提交批界、连接数、队列服务份额、安全工作速率与高低水位、缓存、磁盘水位、归档段大小/目录及维护配额 | 不改变业务有效性；缩容不得放弃已承诺数据和资源 |
| 补资配置 | 资金方授权、来源/目标/资产、低水位/目标余额、单次及累计上限 | 只驱动真实授权交易；未到账不使用，不重置商户政策 |
| 基准配置 | 种子、交易分布、并发/到达率、运行时长、资金补给、故障注入 | 完整输出到报告，禁止隐含默认改变对照条件 |

首轮功能实验可以从输入 16、输出 32、规范正文 64 KiB、解压后单个证据包 1 MiB、单个不可分割公共执行封装 2 MiB、区块应用负载 4 MiB、依赖深度 64、唯一未最终祖先 256 开始。这些是待压测的配置种子，不是已验证性能结论；每类动作工作量及价格上限由显式费率/工作表固定，缺少该表不能启动准入。启动校验最小合法执行封装能放入区块，Comet 区块上限还需容纳外层开销，收费计划能覆盖准入允许的完整工作，成员配置和各资源授权相容。不能单独放宽深度而不重新核定费用、E/B 与证据上界。

### 15.2 落地里程碑

| 阶段 | 交付 | 验收关口 |
|---|---|---|
| 0：冻结工程契约 | 显式 schema、身份黄金向量、网络/配置、锁定依赖 | 编码和引用无环，版本组合编译与漏洞检查完成 |
| 1：本地规则和存储 | 内存参考、bbolt、PREPARE/INSTALL/核销、输入状态 | 原子性、占用、重复及乱序用例通过 |
| 2：四成员快路径 | 网关、四成员、钱包、TXCer | 实际同步持久的链前续花，单成员失联场景可测 |
| 3：公共闭环 | 散户 DirectTransfer、组织收款路由、Comet 执行、FinalFactProof、费用/工作闭环 | 后台结算、h+1 证明、权限恢复和普通重启通过 |
| 4：完整担保功能 | 既有 TXCer 的根落实、原交易资金回补、本金核销和奖励领取 | R/F 系列及真实输入验证通过后才启用对应功能 |
| 5：持续运行与参考工具 | 低水位补资、轻量分段归档、稳态压测、瓶颈对照、archgen --check | O01–O04 通过，持续队列/资金/存储测量完整，生成输出可复现 |

阶段 2 不能宣称完整支付系统已实现，阶段 3 不能宣称 TXCer 担保履行已验证。业务实现从最小闭环推进，但对象和模块边界现在统一，避免后续把根责任和费用核销硬塞进无关模块。

### 15.3 规则追踪矩阵

| 协议终稿条款 | 负责模块 | 关键记录 / 接口 | 回归用例 |
|---|---|---|---|
| §4 根支撑、组织互通、散户付款与唯一消费 | rules/funding、rules/settle | RootObligation、SourceImport、OutputRecord | M01、M08、R01–R04 |
| §4.4–4.6 互通与散户 | peers、rules/direct_transfer、wallet | OrgDescriptor、ReceiveDescriptor、SpendOrg、UTXORecord | X01–X04、S01–S03 |
| §6.1 内部验证复用 | member/verifycache、rules/prepare/install | VerifiedParent、验证指标 | L01–L04 |
| §5 身份、认证对象和分离状态 | protocol、wallet、transport | TxBody、IntentID、QC、Outcome | C01–C03、M06 |
| §6 一轮取证与后台安装 | member、rules/prepare/install、wallet、store | SpendQC、ApprovalRecord、InstallRecord、Outbox | M01–M07、P02、Q01–Q06 |
| §7 CAL/FUEL/工作/政策权限 | rules/credit、member | Grant、WorkerSlice、AppliedCredit、WorkRecord | B01–B03、F03 |
| §8 公共执行与最终证明 | committee、finality | CommitMeta、LifecycleAction、FinalFactProof | P01–P04、R03 |
| §9 费用、分润、真实回补 | rules/fees、committee | FeeEscrow、RewardAccount、RestoreFeeCoverage | F01–F03 |
| §11 故障与服务状态 | member、gateway、wallet | 原身份重传、同库重启、服务状态 | A01–A03、P02 |
| §12 性能和物理容量 | telemetry、bench、运行时调度 | 有界队列、磁盘水位、测量报告 | A03、稳态负载 |
| §7.4/9.1/12.2 持续运行 | member/admission、wallet/funding、store/archive | 完成反馈、FundingRequest、ArchiveSegment/ArchiveLocation | O01–O04；开发计划 §2.4/2.6，按 §4–5 集成验收 |
| §14 原型验收 | testkit、bench | 场景、黄金向量、真实节点夹具 | 全部已启用功能对应测试 |

任何规则变更同时更新条款映射、schema/规则版本和对应测试。接口或存储模式变更不能悄悄改变既有业务身份；不支持的旧格式明确拒绝，不自动把旧数据按新规则解释。

## 16. 本轮确定的取舍与完成状态

确定采用 Go 单仓库多进程、共享协议类型、两套运行时状态机、一轮 SpendQC 与后台 INSTALL、bbolt 本地事务、并行预检与短单写提交、Comet 公共排序、正向最终事实证明、有界 outbox 和显式参考生成。首版不引入独立消息中间件、分布式数据库、跨机 Worker 协议、全状态树或复杂恢复平台。

创新主要来自协议与工程路径的结合：一次根支撑后的可续花权利、完整向量认证、按原事实增量恢复权限，以及复用已认证履约事实驱动费用和工作周转。单写数据库、代码生成和 Merkle 证明本身不作为新发明宣称。

本文件已经形成实现参考，但没有生成可运行仓库、没有完成组合编译、联合安全实验或 TPS 压测。旁路模型讨论是设计评审，不构成形式化证明。本次组织互通、散户接口和内部验证复用经过三轮[ChatGPT 讨论](https://chatgpt.com/c/6aa8b2d7-b6f8-83ec-8e23-ea9f3b634e90)，新增 X/S/L 场景已映射到开发计划。后续编码以协议终稿及本规范为约束，以独立测试与实际测量决定性能优化是否保留。

2026-09-18 在同一对话补充一轮持续运行评审，落实后台完成反馈、真实低水位补款与轻量分段归档，新增 O01–O04 场景。安全记录继续保留，有限 FUEL 供应和历史总量增长仍计入部署边界；这些新增项目均待实现和测量。

本次后台 INSTALL 经同一 GPT 对话两轮评审后采用；默认支付证书改为 SpendQC，INSTALL 和委员会登记并行；Q01–Q06 对应隐藏证书、无 ACK 续花、乱序安装及版本隔离。新可用性前提和未决占用限制见协议 §6.2/11，首版不实现候选取消或授权退役释放。

## 17. 依据与外部资料

协议依据：[UTXO 快速转账系统 v1.1 终稿](./utxo-fast-payment-system-design-final.md)。原始组织、委员会 v1.1 和费用 v0.1 文档按终稿的版本关系理解，已移入项目同级的历史归档目录。工程讨论记录见 [四轮工程评审记录](../Next-docs-archive-2026-09-17/utxo-go-engineering-review-notes.md)。后续开发流程见 [开发执行计划](./utxo-development-execution-plan-v1.0.md)。

以下资料于 2026-09-17 核对；用于核实库的能力与接口，不代表本项目已完成依赖组合验证。

- [S1：Go 官方发布历史](https://go.dev/doc/devel/release)。
- [S2：CometBFT 官方发布记录](https://github.com/cometbft/cometbft/releases)。
- [S3：bbolt 官方发布记录](https://github.com/etcd-io/bbolt/releases)。
- [S4：Protobuf Serialization Is Not Canonical](https://protobuf.dev/programming-guides/serialization-not-canonical/)。
- [S5：bbolt 官方 README 与事务约束](https://github.com/etcd-io/bbolt/blob/main/README.md)。
- [S6：CometBFT v0.38.26 ABCI 应用要求](https://github.com/cometbft/cometbft/blob/v0.38.26/spec/abci/abci%2B%2B_app_requirements.md)。
- [S7：CometBFT v0.38.26 Merkle proof 实现](https://github.com/cometbft/cometbft/blob/v0.38.26/crypto/merkle/proof.go)。

[S1]: https://go.dev/doc/devel/release
[S2]: https://github.com/cometbft/cometbft/releases
[S3]: https://github.com/etcd-io/bbolt/releases
[S4]: https://protobuf.dev/programming-guides/serialization-not-canonical/
[S5]: https://github.com/etcd-io/bbolt/blob/main/README.md
[S6]: https://github.com/cometbft/cometbft/blob/v0.38.26/spec/abci/abci%2B%2B_app_requirements.md
[S7]: https://github.com/cometbft/cometbft/blob/v0.38.26/crypto/merkle/proof.go

