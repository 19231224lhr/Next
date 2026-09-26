# 门限变色龙哈希与共识修订接口安全论证

本报告接续[组合安全论证](security-composition-wire4.md)，审查 wire 4 的 R6：门限适配、公共修复授权、历史物化及修改后的 Comet 接口。基线为 `security-analysis@d12e430`，CometBFT 固定为 `v0.38.26`，CIRCL 固定为 `v1.6.5`；生成的 Comet 源码以仓库 `third_party/cometbft/overlay.py` 为准。

**结论：可以给出分层的条件安全论证；不能宣称完整门限密码学归约或整个修改后 Comet 已被形式化验证。** 本轮复现并修复了实时共识分片的原始版本检查缺口。公开适配结果可复用，因此“哈希验证成功”与“本次修改获得公共授权”必须分开。

## 1. 对象、边界与假设

### 1.1 三种对象

| 对象 | 含义 | 权威依据 |
| --- | --- | --- |
| 原始执行块 `L[h]` | 高度 h 首次提交时的完整交易字节 | BFT 提交的 BlockID，包含 PartSetHeader |
| 规范修订 `A[h]` | 公共应用状态中的修订版本和正文 | 新块中成功执行并提交的 RepairInput |
| 物理表示 `P[h]` | BlockStore 当前保存的历史正文 | 已提交 Task 的版本顺序和精确正文检查 |

`L[h]` 不因修订变化；`A[h]` 可领先于 `P[h]`。重放原始交易与后续修复命令产生经济状态，不能拿已修订正文替代原始交易重新执行。分片承诺允许不同表示共享承诺，不意味着这些表示具有相同执行含义。

### 1.2 明示条件

1. 固定四委员、三门限，至多一名拜占庭委员；委员认证密钥、公钥模数、ChainID 和上下文规则正确配置且不在运行中偷偷切换。
2. RSA 密钥由可信 Dealer 按固定实现生成和分发。未实现 DKG，不证明 Dealer 不知道完整私钥；测试夹具中的公开私钥只用于功能测试。
3. 常规签名不可伪造；普通 SHA-256／Merkle 承诺抗碰撞；FDH 在所需上下文中具有抗碰撞性质。门限适配的新目标不可伪造性另列义务，不由这些假设自动推出。
4. 已提交应用状态和保存的原始块可信；不允许攻击者直接改写本地数据库、调用任意内存函数或回滚诚实节点。内存实验不提供掉电恢复保证。
5. 继承的 BFT 顺序／提交规则满足其故障模型；本报告证明适配层所需的数据接口性质，不重新证明全部轮次、锁、网络调度和实现精化。

侧信道、Dealer 泄漏、移动腐化、状态快照同步、任意独立轻客户端验证最新历史，均不在本轮已证范围内。进度还需要足够有效份额可得、公共命令能被纳入区块以及物化工作获得调度。

## 2. 密码学接口

### 2.1 定义与正确性

设 RSA 模数为 N，指数 e=65537，所有运算在单位群 `Z*_N` 中。实现定义：

```text
h = H(pk, context, message) ∈ Z*_N
C = h · r^e mod N
z^e = H(pk, context, old) / H(pk, context, new) mod N
r' = r · z mod N
```

代入即得 `H(new)·(r')^e = H(old)·r^e = C`。这证明适配正确性，前提是合成出的 z 满足指数等式；不证明谁有权适配。

H 使用 SHAKE256、公钥与固定标识域分离，对 context/message 编码长度，拒绝采样至单位群。输入上下文绑定网络、稳定交易核心、输入编号和 KeyID；分片上下文绑定链、KeyID、高度及分片编号。上下文不是一张业务许可证：它限制复用范围，授权还要检查状态和修改内容。

`Signer.Adapt` 先检查旧承诺，再对比值生成门限贡献；`Combine` 检查份额配置、索引唯一性，尝试三元子集并最终验证承诺。最终等式验证可拒绝错误结果；它不提供每份贡献的独立正确性证明，也不是完整的可问责／稳健门限协议。

### 2.2 公开适配不等于新批准

公开同一上下文中的 `(old,r)` 和 `(new,r')` 后，任何人都能计算 `z=r'/r`。对于该上下文下任意另一旧 opening s，可构造 `s'=s·z`，从而把另一承诺的同一 old 消息适配成同一 new 消息，不需要新收集三份贡献。多个已公开适配也能按比值相乘、求逆复用。

新增 `TestPublishedAdaptationReuse` 实际验证这一代数性质，并核查换上下文后该构造不再通过。后一测试只是实例检查，不能替代跨上下文不可伪造性的归约。

因此不可采用以下论断：

> 任意新出现的有效 opening 都证明至少三名委员刚刚批准了这个业务动作。

可研究的密码学目标应排除已公开适配关系的代数闭包，要求攻击者对**未获授权的新目标关系**仍不能生成有效适配；还必须明确腐化份额、适配查询、部分贡献查询和可复用结果。当前尚未完成该自适应攻击游戏及归约。本系统的业务安全不能依靠“每个 opening 都是新批准”这一错误命题。

### 2.3 固定依赖审查

[Shoup 的 Practical Threshold Signatures](https://www.iacr.org/archive/eurocrypt2000/1807/18070209-new.pdf) 给出门限 RSA 的构造和假设背景，不能直接充当本项目 FDH 比值适配、公开复用及应用授权组合的证明。

CIRCL v1.6.5 的 [README](https://raw.githubusercontent.com/cloudflare/circl/v1.6.5/tss/rsa/README.md) 写明省略份额验证、并称没有采用 safe primes；但该版本 [GenerateKey 源码](https://raw.githubusercontent.com/cloudflare/circl/v1.6.5/tss/rsa/rsa_threshold.go) 实际调用 `SafePrime` 生成两个素数。本项目 `GenerateDealer` 调用此生成函数；`Deal` 对外部导入密钥并不因此自动保证同样的生成条件。论文应引用固定调用链，不能照搬 README 或假定任意导入模数满足门限证明前提。

实现使用非恒定时间的大整数运算；没有据此证明远程或本地侧信道安全。可信初始化、固定参数和密钥保管属于必要部署条件。

## 3. 公共修订的状态机论证

### 3.1 转移与线性化点

| 转移 | 前置条件和效果 | 经济投影 |
| --- | --- | --- |
| Prepare／贡献收集 | 当前 Open、到期、位置和版本符合；生成候选适配 | 无变化；结果仍可能因父到或版本推进而失效 |
| Execute 修复命令 | 重新检查当前义务、历史位置、Base/Previous、完整正文与适配；产生扣款、关闭、Task、Revision | 以所属块的应用 Commit 为共同生效边界 |
| Materialize | 从已提交视图读取 Task，按版本调用 ReviseBlock | 仅更新物理表示，不再扣款、不产生用户输出 |
| 重复命令／物化 | 已有同一 Task 的相同命令幂等；冲突命令拒绝；迟到物化不得倒退 | 不重复赔付 |

实际规则函数返回的 Transition 不是公共最终性；工作线程也不能读取尚未 Commit 的 Overlay 并据此授权改写。应用已经提交而物化尚未完成是允许状态。

### 3.2 引理 R6-A：修改范围受限

`InputTarget` 从义务定位原消费交易、输入、金额与规范版本。`nextBody` 解码命令中的下一交易，要求目标 Funding 是该输出唯一 `ReserveDebitIdentity`，验证其 opening；随后只把原交易的这个 Funding 改掉，要求重新序列化后与命令提供的交易字节完全相同。

故一个成功转移保持其他输入、输出、声明、认证与授权字节不变。修订后的整块也由规范旧块只替换此交易构造。此推导依赖当前规范编码和已核查写入口，而非“TxID 相同就必然内容相同”。

### 3.3 引理 R6-B：授权序列与幂等

初始版本为 0。假设规范版本为 j，成功 Execute 必须引用其 Base 和 Previous，下一版本为 j+1。串行块执行使两个引用同一旧版本的不同修改不能同时成功。相同 Task 重试只在命令完全相同时返回幂等结果；不同命令不能借同一任务身份覆盖授权。

赔付规则还独立检查义务仍 Open。因此适配贡献已生成但父交易先成立时，候选修复不能凭 opening 绕过当前状态；重复物化也不能再次扣款。赔付与版本授权由同一应用提交保存，避免已经扣款却没有对应规范授权，或未扣款先公开授权的逻辑状态。

### 3.4 引理 R6-C：物化不改变经济结果

Materialize 只接受已提交 Task 中的精确正文。ReviseBlock 检查版本递增、稳定 BlockID/PartSetHeader、分片证明和授权回调；重复或已被后续已授权版本覆盖的任务不使表示倒退。

在可信 BlockStore、版本连续和已提交 Task 的假设下，归纳可得 `P[h]` 是授权修订序列的某一前缀，可能落后于 `A[h]`。每次物化都不写经济状态，故其经济投影为 stutter（无变化步骤）。`Canonical` 从已提交 Revision 读取正文，未修订时读原始块，不根据物化线程进度决定下一条公共命令的结果。

这不是跨应用数据库和 BlockStore 的一次物理事务证明，也不覆盖磁盘损坏／断电恢复；本轮保证的是声明故障模型下的提交顺序和经济／表示分离。

## 4. 修改后共识的数据接口

### 4.1 复现：Revision 标签不能认证原始字节

分片叶承诺为 `SHA256(CH(context, bytes, opening))`。Revision 标签不进入这个承诺，否则修订不能保持相同分片根。原始分片由 `NewRedactablePartSet` 使用确定的 opening=1 构造。

旧实时入口只检查高度和 `Revision==0`。新增回归完成以下轨迹：

1. 构造原始块及分片承诺，用真实测试门限份额生成不同正文的有效适配。
2. 确认新旧 PartSetHeader 相等。
3. 只把修订分片的 Revision 从 1 改为 0，经 protobuf 编解码。
4. 通用 `AddPart` 仍接受证明；修复前实时 `BlockPartMessage.ValidateBasic` 也接受。

红测试报告 `live consensus accepted relabelled adapted opening`。这是**原始执行分片入站检查缺口**的复现，使用简化交易封装；未声称已经复现完整支付攻击或恶意节点完成了全网冲突提交。

### 4.2 最小修复与绑定引理

实时 `BlockPartMessage.ValidateBasic` 调用新增 `Part.ValidateOriginal(height)`。启用修订配置时要求元数据存在、高度一致、Revision=0、固定长度 opening 严格等于 1。未启用修订的普通 Comet 分片兼容原行为。通用 AddPart 仍支持合法修订表示，但它不是实时共识授权接口。

**原始字节绑定引理。** 固定上下文和 opening=1 后，叶承诺退化为 `SHA256(H(context, bytes))`。若两个不同的可接纳分片正文在相同位置匹配同一已提交 PartSetHeader，则必须产生 FDH／外层哈希／Merkle 绑定中的碰撞。排除这些事件后，同一 BlockID 对实时接纳的原始分片字节具有唯一性。分片总数、索引和 Merkle 证明仍由原有 PartSet 检查承担。

这个引理不要求“适配陷门永远不泄漏”：陷门可找到其他 opening，却不能使不同正文在固定 opening=1 时保持 FDH 值。因此修订能力不会被当成实时执行字节的替代授权。

仅依靠 `ProcessProposal` 检查不足：Comet 节点可能在收集足够提交票后进入 `enterCommit/tryFinalizeCommit`，不能假定每个执行节点之前都对该完整提案做过本地投票检查。数据接纳边界须先满足原始字节约束。

### 4.3 接口对应与条件组合

| 路径 | 当前接口要求 | 核查依据 |
| --- | --- | --- |
| 实时外部 BlockPartMessage | 原始高度、标签及 opening；之后验证分片承诺 | 新入口回归和 overlay 生成源码 |
| 本地提案 | MakePartSet 生成 opening=1 的原始分片 | types/redaction.go；不允许后台物化替换本地候选 |
| 共识追块分片 | OriginalBlockPart | overlay 对 consensus/reactor.go 的替换 |
| blocksync 发送块 | LoadOriginalBlock | overlay 对 blocksync/reactor.go 的替换 |
| 启动重放 | LoadOriginalBlock | overlay 对 consensus/replay.go 的替换 |
| 修订规范状态 | 已提交 Revision，未修订则原始块 | redaction.Canonical |
| 历史物化 | 已提交 Task＋精确正文、版本、稳定根 | redaction.Materialize → BlockStore.ReviseBlock |

调用核对补充：外部消息经 `MsgFromProto → ValidateBasic` 入队，WAL 消息反序列化也调用 MsgFromProto；本地 `defaultDecideProposal` 从 MakePartSet 生成分片后入队。实际加入提案集合的是 `handleMsg → addProposalBlockPart → ProposalBlockParts.AddPart`。导出的 `AddProposalBlockPart/SetProposalAndBlock` 可以直接向内部队列投递，当前生产项目没有该调用者；它们不是面向不可信数据的新接入 API。若后续增加这样的调用者，必须在其边界复用 ValidateOriginal，不能把本轮限定调用图的结论推广到任意进程内注入。原始 BlockStore、WAL 均受第 1 节的本地状态信任约束。

在上述路径闭合、原始字节绑定、确定性应用转移及既有 BFT 顺序安全成立的条件下，可把物化步骤从经济轨迹中擦除：剩下的轨迹按同一原始交易序列和 Repair 命令执行。由 R6-A/B/C 得到“受限字段修改、至多一次经济赔付、表示滞后不影响经济状态”的条件组合结论。

[CometBFT v0.38.26 共识规范](https://raw.githubusercontent.com/cometbft/cometbft/v0.38.26/spec/consensus/consensus.md) 是轮次与提交规则的参考。本报告没有用其文档直接证明本地补丁；网络消息、WAL、同步及所有异常路径到该模型的完整精化仍未完成。原始块可用性、状态快照同步、轻客户端如何认证“最新而非某个合法旧修订”也不能从稳定哈希单独推出。

## 5. 源码、测试与结论范围

| 性质 | 核查代码 | 证据 |
| --- | --- | --- |
| 适配等式、份额组合 | crypto/chameleon/chameleon.go | 原有 quorum 测试；新增 TestPublishedAdaptationReuse |
| 固定 Dealer 参数 | crypto/chameleon/keys.go、固定 CIRCL GenerateKey | 源码核对；不是 DKG 或完整密码学证明 |
| 精确字段与版本 | internal/redaction/repair.go | 已有非目标授权／输入证书篡改回归 |
| 赔付、修订与原始重放 | internal/redaction/payment_test.go | TestPaymentRepairMonetaryReplay |
| 真 BlockStore 与原始表示 | internal/redaction/comet_test.go | TestRealBlockStoreRewriteAndOriginalReplay |
| 原始分片入站绑定 | third_party/cometbft/types/redaction.go、overlay.py | 新 TestConsensusRejectsRelabelledOpening 覆盖真实 MsgFromProto；扩充缺失元数据／错误 opening，真实修订后的原始分片追块回归 |

新增实时检查没有新增 RSA 运算、锁、扫描、RPC 或写盘，仅比较固定元数据与 256 字节 opening。Windows amd64 的三次隔离微基准中，旧标签检查为 1.294–1.327 ns/op，新检查为 8.710–9.240 ns/op，均 0 B/op、0 allocs/op。它只度量函数成本，**不是 Mac 端到端 TPS 对照**，不据此改写 E1–E8 性能成绩；合并实验基线前仍按实施计划做实际负载验收。

### 当前可采用的论文表述

> 我们区分门限适配结果、公共经济授权和物理历史表示。公开适配结果不被视作新的委员会授权；修复命令在当前公共状态下验证精确修改范围，并将赔付与规范版本原子提交。历史物化只消费已提交授权，不重复执行经济转移。实时共识固定原始分片 opening，从而在常规哈希绑定假设下保持原始执行字节与提交根的对应。该论证由源码映射及真实门限／存储回归支持；自适应门限适配的完整密码学归约和整个修改后共识实现的机械化验证仍为独立义务。

### 尚未完成

- 门限比值适配在腐化与适配查询下的明确安全游戏、代数闭包边界和完整归约；CIRCL 份额验证／侧信道保证不应被虚构。
- 修改后 Comet 全部实现路径的精化或机器证明；本轮为限定接口的手工条件论证。
- 独立最新历史证明、状态快照同步和崩溃恢复；这些不是正常快速付款新增的必需步骤。

验证记录见 [R6 验证清单](security-redaction-validation-2026-09-27/README.md)。本轮只在安全分析分支修改共识分片入口；`re` 实验基线保持不变。
