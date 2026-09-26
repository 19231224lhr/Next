# wire 4：组合模型、归纳证明与实现对应

> 第二阶段 · 基线 `security-analysis@a80628f`，生产代码仍与 `re@8d1590b` 相同。本文给出统一抽象状态机上的手工条件证明及源码对应，不是 Go 全程序机械化证明。上一轮的四个有限模型只用于反例检查，不作为本轮归纳证明的前提。

[总模型与假设](security-argument-wire4.md) · [有限模型及第一轮测试](security-model-validation-2026-09-26.md) · [实施计划](security-implementation-plan.md)

## 1. 命题范围与证明顺序

本轮证明的核心命题是：**在固定授权、诚实成员不遗忘批准、公共提交顺序一致等条件下，快速认证形成的潜在 CAL 责任始终由剩余真实备付覆盖；消费、赔付和权限恢复保持一致，既不重复消费同一实例，也不把实际损失重新当作可用资本。**

保持总模型的 A1–A6：可信固定配置、认证与摘要安全、成员原子且不回滚、可信公共顺序与连续跟块、检查过的整数运算、受限修订密码学接口。安全结论对任意有限执行前缀成立，不依赖两个 Fact、两个输出或六个事件的模型上界。它不意味着实现支持无限内存、任意成员变更、崩溃恢复或无条件活性。

证明先后关系如下，避免用“有钱必能赔”反过来证明有钱：

1. 定义单一状态、身份和原子事件；先证明每输出至多关闭一次及 Paid≤cap，再证明批准账与 Worker 账一致。
2. 从输出创建／消费规则证明自身成立后 Paid 不再增长，再证明本地残余不低于风险。
3. 用法定集合双计数推出全部有效 QC 的风险上界，包含尚未公开的 QC。
4. 独立证明公共账与真实账户的归纳不变量，最后与第 3 步组合。
5. 独立证明 CAL/FUEL 资产守恒及受限修订的经济不变性。

## 2. 统一状态与接口

### 2.1 固定身份

每个证书事实 c 具有不变记录 `D(c)=(Fact, TxID, issuer, config, Grant, outputs, cap)`。其中 `cap=Σ输出金额`，恰有一个 CAL allocation，Grant 的账户等于 issuer 的 CAL 备付账户。签名子集变化不产生新 Fact；同一事实重复登记不重复计额。

`k=(OutputID, instance)` 是消费身份；实例 0 与合法迟到实例 1 不共用消费权。本文没有把“父 TXCer 的发行者”和“当前付款的签发者”合成一个责任主体。

这些是组合接口的实质内容，不能只从投票模型传递一个“QC=true”。`PrepareDirectVector`、`SummaryFor`、`VerifyDirectSubmission`、`grant` 分别建立金额、事实、组织授权及 Grant 对应。诚实签署者只为通过该验证的 D(c) 签票。

### 2.2 状态变量

| 部分 | 状态与含义 |
| --- | --- |
| 成员 m、Grant g | 所见公共前缀 `h_m`，累计授权 `B_mg`；各 Worker 的 `Available/Reserved`；原 Approval 及累计 `Applied_m,c` |
| 证明用历史 | 已发有效签名集合 `Votes(c)`；`C_g={c: 至少 q 个不同有效签署者}`，按 Fact 去重，包含未公开证书 |
| 公共证书 | `z_c`：其自身交易是否已成立；`p_c`：其自身输出累计实际赔款；已登记覆盖 `R_c`、已解除 `d_c`；未登记时 R、p、d 为 0 |
| 公共输出 | 最终 Creation、各实例 Consumed、Promise、直接 Obligation 的 Absent/Open/Fulfilled/Repaired 状态 |
| 公共预算与资金 | Grant 累计授权 `B_g`、`Usage.Reserved=V_g`、`Usage.Spent=S_g`；真实 CAL 账户 `A_x`；备付集合 Protected |
| 费用与表示 | 每笔费用托管及阶段，真实 FUEL 资产、奖励、销毁；原始正文、授权修订版本、物化版本 |

Votes 是逻辑历史变量；证明不要求任何在线节点枚举所有隐藏 QC。C 只增不减，已经结束的证书也保留其中，其权重可以降为 0 或实际 Paid。各有限前缀中对象和求和项均有限。

### 2.3 可执行事件与不干扰约束

| 事件 | 守卫及状态效果 | 跨模块约束 |
| --- | --- | --- |
| Approve | 验证 D、授权、输入及所选 Worker 额度；同事务写 Candidate、原 Approval、扣占 | 不动公共资产／授权；失败不留下部分占用 |
| Sign | 诚实成员先有成功批准；拜占庭成员可任意签 | 不额外扣占；重复签名不增加预算 |
| Register(c) | 已验证 QC，第一次缺失输入使用才登记整 cap 和全部 Promise | 是 Execute 的原子子步骤；公共登记不创造新 QC 风险 |
| Execute(T) | 认证、唯一消费、费用足额；原子处理输入和输出 | 缺失来源同时产生 Gap 和义务；自身输出逐一创建／关闭 Promise，完成后置 `z=true` |
| Repair(o) | 唯一 Open 到期、合法经济修订授权，相关资金和费用条件满足 | 同事务更新 p、S、A、R、Gap、费用和待办；失败全不生效 |
| TopUp(g,a) | 固定合法 Grant、正确 Previous、防重；真实外部来源不属于 Protected，余额足够且不同于目标 | 源减 a、备付加 a、B 加 a；不改变旧 Approved/Reserved/Paid |
| Follow(m) | 经验证公共成功结果、连续前缀，按块内次序应用 | 只根据原 Approval 释放；游标与本地账同事务推进 |
| Install／重传／Materialize | 按现有证据与已提交授权传播或更改物理表示 | 不解除原批准、不改变公共经济状态；同一已完成身份不再收费 |

公开 Execute/Repair 的内部更新只有整体成功才可见；不能在调用 `completeOutput` 后、扣款前把半笔结果当作可达公共状态。拒绝和幂等重放均是无经济变化步骤。Anchor 只确定期限，不动金额或批准。

## 3. 局部账与生命周期接口引理

### 引理 0：输出会计先于预算证明

每个输出的 Promise 仅能从 Open 到 Fulfilled 或 Repaired，后续重复关闭无变化；赔付只对唯一 Open 义务发生。金额来自不变 D(c)，所以 p 是互不重复的已赔输出金额之和，独立得到 `0≤p_c≤Σoutputs=cap_c`。已登记覆盖满足 `cap=p+d+R`：登记初始 `(p,d,R)=(0,0,cap)`，每次关闭将 R 的相同金额转给 p 或 d。未登记的 p、d、R 都为零，不要求此时三者之和等于 cap。这个会计引理不使用 Q≤B 或真实备付足够，后文可据此比较本地 cap 与公共 p。

### 引理 A：批准残余等于 Worker 占用

对诚实成员所有使用 g 的成功批准，定义 `r_m,c=cap_c−Applied_m,c`，包含未签出或未形成 QC 的批准。设 `s(B)=floor(2B/3)`，则：

$$\sum_c r_{m,c}=\sum_j Reserved_{m,g,j},\qquad
\sum_j(Available_{m,g,j}+Reserved_{m,g,j})=s(B_{m,g}),\qquad Available_{m,g,j}\ge0.$$

**归纳证明。** 创世没有批准，将 s 排他分配给 Worker。新批准在同一 Worker 以等量 a 从 Available 移到 Reserved，并新记 cap=a、Applied=0；重复批准不再次扣占。释放差额 δ 同时增加 Applied 和 Available、减少原 Worker Reserved。补资只将 `s(B+a)−s(B)` 分摊到 Available，各份额之和恰为该差值，旧 Reserved 不变。其他事件不改变这些量。失败不提交。故等式逐步保持，且每成员总残余不超过 s。无需假设负载均匀或自动调配。

### 引理 B：自身成立后，该凭证 Paid 不再增加

若 `z_c=true`，c 的每个输出处于以下情形之一：

1. 未曾作为缺失输入消费：最终实例 0 已创建；后来合法消费使用该创建事实，不建立缺失义务。
2. 已作为缺失输入消费且尚未赔：旧实例 0 保持 Consumed；来源成立原子关闭 Open 并创建最终实例 0，后续不能再消费该实例。
3. 已赔：旧实例 0 保持 Consumed，旧义务终态 Repaired；来源成立创建独立最终实例 1。旧 TXCer 不授权实例 1，后者只能按最终输入消费。

Promise 若存在也在这一 Execute 中逐一关闭；从未公开登记过的输出不需新建 Promise。已关闭义务没有返回 Open 的转移，重复来源不重新执行。Repair 只接受 Open，故成立之后无新的 `p_c` 增量。此结论只依赖身份与公开状态规则，不依赖预算上界，因而可用于后续预算证明而不循环。

### 引理 C：诚实签署者残余覆盖当前风险

定义：

$$w_c=\begin{cases}cap_c,&z_c=false,\\p_c,&z_c=true.\end{cases}$$

对 c 的任一诚实签署者，有 `r_m,c ≥ w_c`。

**证明。** Sign 保证先有 Approval。公共自身交易未成立时，任何合法前缀中都不能观察到成立，CAL Applied=0，r=cap。公共已成立而成员落后时，由引理 0 仍 r=cap≥p。成员已观察自身成立时，连续前缀包含所有此前对自身输出的赔付，得到准确 p；引理 B 排除以后再增 p，所以释放 cap−p 后恰留 r=p。没有原 Approval 的成员不会获得这项释放。成员落后只会多保留占用，不能多恢复额度。成员可见前缀以完整区块提交为边界；块内按序计算只用于证明中间推导，不暴露半块状态给外部请求。

成立后再次返回同 Fact 的旧签票也不改变结论：C 按 Fact 去重，风险已经是 p；批准和输入消费记录不因释放而删除。不同新 Fact 必须重新占用可用额度。部分票残留仍占右侧预算，但不进入有效 QC 风险集合，可能影响进度而不破坏上界。

## 4. 从全部认证到真实余额的一般证明

### 定理 D：固定四成员的动态风险上界

固定每 Grant 所属组织的三名诚实成员集合 H。即使实际四名都诚实，也可任取三名。每个三票集合与 H 至少交叠两人。由引理 A/C 和授权单调增加 `B_mg≤B_g`：

$$2\sum_{c\in C_g}w_c
\le\sum_{m\in H}\sum_{c\in C_g:m\text{签过}c}r_{m,c}
\le\sum_{m\in H}\sum_{c:m\text{批准}c}r_{m,c}
\le3\lfloor2B_g/3\rfloor\le2B_g.$$

于是 `Q_g=Σw_c≤B_g`。该证明不以当前公开 V 为前提，包含隐藏 QC、不同诚实成员集合、成员落后、重复补资和已恢复额度再次使用。p 已包含在 w 内，不能在 Q 外再加一次 p；反过来也不能把已赔证书从 C 删除以抹掉损失。

**参数推广引理。** 对固定 n、至多 f 个永久拜占庭、q 个不同签署者，若 `0≤f<q≤n`，选固定 `n−f` 个诚实成员。每 QC 至少 `q−f` 个来自该集合。若成员份额满足

$$s(B)=\left\lfloor\frac{q-f}{n-f}B\right\rfloor,$$

相同双计数即得 Q≤B。唯一消费还需 `2q>n+f` 以保证两个 quorum 交叠诚实成员；不受恶意成员拒答影响的成证活性另需 `q≤n−f`。实现只使用 (4,1,3)，推广是数学命题，不表示已有动态成员实现。

### 引理 E：公共覆盖、已支出与实际账户同源

按归属 Grant 聚合，有：

$$S_g=\sum_{c\in C_g}p_c,\qquad V_g=\sum_{c\in C_g}R_c,\qquad
0\le R_c\le w_c-p_c.$$

**证明。** 初始量为零。第一次 Register 只针对已有 QC：其自身尚未成立，p=0，整 cap 同时进入 R 与 V。其余输出复用相同登记。正常解除金额 a 同时减少 R、V 并增加 d；赔付金额 a 同时减少 R、V 并增加 p、S。已成立证书的所有 Promise 已解除，R=0，w=p；未成立时 p+R≤cap。费用路径跳过 CAL Usage，不改本引理。已公开登记的证书是 C 的子集，故未登记项按零记账不漏任何已发生支出。

所有 CAL Grant 归属账户固定。令 `G_x={g: backing(g)=x}`。在只开放本文列出入口的闭合集合下：

$$\sum_{g\in G_x}(B_g-S_g)\le A_x.$$

创世按实际账户合并 Grant 后检查；TopUp 同时增加目标 B 与同账户 A，外部来源与所有 Protected 账户隔离；Repair 同时增加对应 S 并减少同账户 A；其他业务不从该 CAL 账户扣款。真实账户可能服务多个 Grant，必须先按账户聚合，不能为各 Grant 单独重复引用整份余额。

源码中的扣款用 `ob.Issuer`，Usage 用证书 CAL allocation；两者相等来自不可变 D(c) 的 `CAL.Key.Account=Issuer`、有效 QC 和 Grant 匹配。不能仅因为两个函数各自金额正确就默认它们扣的是同一账户。

### 组合定理 F：直接缺口与潜在责任有真实覆盖

定义 `E_g=Σ(c∈C_g)(w_c−p_c)=Q_g−S_g`。令 D_x 为发行责任对应账户 x 的 Open 义务总金额。每个 Open 义务关联唯一仍 Open 的输出 Promise；未消费 Promise 可有 R 而无实际缺口。由 D/E：

$$\boxed{D_x\le\sum_{g\in G_x}V_g
\le\sum_{g\in G_x}E_g
\le\sum_{g\in G_x}(B_g-S_g)\le A_x.}$$

这是同一执行前缀中的库存覆盖链。既覆盖已执行的缺口，也保守覆盖尚未公开／使用的有效认证；不把各成员重叠额度相加为真实本金。一个合法 Open 金额 a≤D_x≤A_x，故在此前闭合假设下，其单次赔付不会仅因 CAL 账户余额不足而失败；FUEL、到期、修订材料与公平执行仍是独立前提。

首次登记新的隐藏 QC 也不会引起理论超额：登记前 E 已包含它的风险，其他已登记 V 与其新增 cap 的和受相同 E 上界约束。代码继续保留 `Reserved+Spent≤Grant` 检查；定理不要求移除该检查。

新增提现、授权缩减、迁移、旧账兼容写入、退款到其他资产等入口都必须另增归纳分支。本结论没有自动覆盖这些未建模操作。

## 5. 唯一消费、资产与表示的组合

**唯一消费。** 同一固定路由的两个冲突 q 票集合交叠诚实成员；该成员不能绕过运行期间保留的 Candidate/Consumed 为两者签票。公共 Execute 再按同实例唯一 Consumed 串行裁决。释放额度不删除消费锁，父到不复活已消费实例。额外实例 1 使用独立身份和真实迟到来源，不能用旧凭证消费。

**CAL。** U 为最终且未消费实例总额，A 为全部真实 CAL 账户（包含外部来源），D 为 Open 实际缺口。合法初始量为 C₀。一次 Execute 消耗最终输入 F 和缺失证书输入 M，输出总额 O=F+M；其中 H 是本次来源到达正常关闭、已被后继消费的输出金额。其增量为：

$$\Delta U=O-H-F=M-H,\quad\Delta D=M-H,\quad\Delta A=0.$$

Repair 的 `ΔA=ΔD=−a, ΔU=0`，TopUp 在账户间守恒，其余非经济步骤无变化。因此逐步保持 `U+A−D=C₀`。输出多个、来源与子交易任意合法顺序、先赔后到及后继本身又有缺失输入，都由同一增量覆盖，无需追溯完整祖先链。结合定理 F，D 是有资金覆盖的负债，不是为无备付增发补一个会计符号。

**FUEL。** 每个托管保持 `Maximum=Held+Rewards+Burned+Refunded`。用户自付消费最终输入总额 F，产生找零 F−Maximum 和 Held=Maximum；代付等额扣真实 FUEL 账户。每个阶段把 Held 等额转为实际奖励、销毁或退款；每项直接义务至多一次 RepairCost，原始预留覆盖证书输入数量的上界。退款输出身份由原付款及保留索引决定。因而 `最终未消费FUEL+真实FUEL账户+ΣHeld+奖励账户+销毁计数` 不变，累计 Refunded/Rewards 不重复计为资产。这里证明守恒，不把 CAL 的 `B−Spent≤账户余额` 直接套到尚有 Held 的 FUEL 账户。

**历史表示。** Repair 的经济提交与版本授权是原子步骤；Materialize 仅按已提交版本改变指定 Funding/opening，在经济投影下是无变化步骤。`nextBody` 从旧规范交易只替换允许字段，并要求完整编码等于命令正文；其他 Claims、输出及授权不变。可得“授权修订不再转一次钱、不改变原输出权利”的条件结论。门限适配安全、修改后 BFT 的排序和独立历史验证仍依赖 A4/A6，不能由上述资产证明反推。

## 6. 源码精化对应与线性化点

令 π 将具体状态投影到第 2 节变量。对一段实现步骤，要求它映射为一个合法抽象事件、顺序合法事件列表或不变步骤；底层失败不得发布半笔经济事件。这是本轮源码核对的判据，尚未机械验证所有可能 Go 执行。

| 代码点 | π 对应及线性化边界 | 核对结论／证据 |
| --- | --- | --- |
| [PrepareDirectVector / VerifyDirectSubmission](../../internal/rules/direct.go) | 输出和 cap、唯一 CAL kind、issuer 账户、Fact/QC、Grant 引用 | 直接建立投票与公共执行的相同 D(c)；静态校验不更新资产 |
| [ApproveDirectBytes](../../internal/member/direct.go) | Store 成功更新 Candidate、Approval、Debits 后返回签票 | 已批准按 Fact 幂等；跟块已 Observed 且无原批准则拒绝新批准 |
| [Memory.Update](../../internal/store/store.go)、[Bolt.Update](../../internal/store/bolt.go)、[Group.run](../../internal/store/group.go) | 串行写事务成功点对应批准或 Follow；Group 只合并成功子修改，底层成功后应答 | Memory 全键检查后修改，Bolt 事务错误回滚／不确定错误停止后续写；不涵盖掉电恢复与底层库正确性的机械证明 |
| [register / completeOutput / endObligation](../../internal/rules/direct.go) | Coverage/Promise、Usage、义务、Gap 的同一公共 Overlay | 第一轮真实轨迹现已额外逐步核对 `ΣPaid=Spent`、`ΣRemaining=Reserved` 及共享账户覆盖 |
| [EvaluateDirectPaymentAt](../../internal/rules/direct.go) | 缺失输入与输出效果原子；成立时逐输出关闭；错误不返回可提交 Changes | 六种三跳到达顺序、赔付、补资与部分输出测试支持分支；不以预检成功作为公开执行 |
| [EvaluateDirectCompensation](../../internal/rules/direct.go) | 只在有效修订执行路径中消费 Open，扣 issuer、更新同 Grant Spent 和待办 | 函数本身不是外部 RPC；[redaction.Execute](../../internal/redaction/repair.go) 验证授权修订并合入整体变更 |
| [ExecuteAt / Execute](../../internal/committee/direct.go)、[Engine](../../internal/committee/engine.go) | Direct 配置公开接纳付款、补资、修订、ClockTick；旧 Execute 在 Direct 模式拒绝 | 旧结算规则的账户写入不自动进入此闭合入口集合；不是将全仓库全部入口默认都已证明 |
| [FinalizeBlock / Commit](../../internal/committee/app.go) | Finalize 在临时 Overlay 按顺序构造；Commit 一次 Store.Update 发布变更与提交元数据 | Finalize 成功仍是待提交状态；Commit 失败停机；A4 的网络一致性仍为接口条件 |
| [ApplyReserveIncrease](../../internal/rules/reserve.go) | 公共真实转资；本地观察后按新旧 share 差增加 Available | Previous／身份防重、源目标与 Protected 隔离；不清理既有占用 |
| [PrepareBlock / applyRepair / finishLocal](../../internal/member/blocks.go) | 修复用输入 Evidence 定位原发行 Fact；自身 Settled 才返 CAL cap−Paid | 分开父发行责任和子付款费用；仅原 Approval 可返还 |
| [blockfollow.Commit](../../internal/blockfollow/follow.go) | 认证成功块、连续高度／前块哈希；变更及 cursor 同事务 | 拒绝跳前缀，重复当前块无变化；块内操作按序生成并应用 |
| [feeOutput / ValidateDirectFee](../../internal/rules/direct_fee.go)、[accounting](../../internal/rules/accounting.go) | 费用身份、最终 FUEL 输入及托管阶段 | 真实账投影计找零、退款、Held、奖励、销毁；不重复计累计字段 |
| [nextBody / Materialize](../../internal/redaction/repair.go) | 经济授权先行，限定表示变更，物化为经济不变步骤 | 既有修订与重放测试；本轮不将其扩张为密码学或共识全证明 |

已沿 `changeAmount` 入边核对 CAL 扣款范围，并核查 TopUp 和创世直接写账户的分支。此清单是当前 Direct 配置的人工闭合性审查，不是静态分析器对全部间接写入的完备性证书。

具体写入者按状态归纳如下；Store/Overlay 是提交载体，不另外授予业务写权限。

| 状态 | 当前 Direct 配置中的业务写入者 |
| --- | --- |
| 成员 Grant、Slice、Applied | `bootstrap` 初始化；`ApproveDirectBytes` 扣占；`finishLocal` 累计返还；`ApplyReserveIncrease` 增加新旧份额差。旧 `ApproveContext` 与 `prepareProof` 在 Direct 模式入口拒绝，不能沿旧 `EvaluatePrepare/applyProof` 返还 |
| 公共 Grant、CAL 账户 | `NewEngine` 初始化；`EvaluateReserveIncrease` 原子转入及增加授权；`EvaluateDirectCompensation` 扣 CAL。其他 Direct 费用账户更新是 FUEL；旧委员会 `Execute` 入口拒绝 |
| CAL Usage、Coverage、Promise | `register` 初始登记；`completeOutput` 关闭输出并经 `updateUsage` 更新 Reserved/Spent。费用处理只更新非 CAL Usage |
| Obligation、Gap | `EvaluateDirectPaymentAt` 打开；`endObligation` 正常／赔付关闭；期限锚定仅补充到期信息 |
| Repair Task／历史表示 | `redaction.Execute` 在经济修复一起成功时写入授权 Task；`Materialize` 修改 BlockStore 表示；`Observe` 及运行时查询读取 Task |

`InstallDirectClassified` 保存完整材料并标记输入 Consumed，不删除旧 Candidate 或 Approval，也不返还 Slice；不能把完整凭证传播误算成释放事件。任意新增写入口都需要重审本表。

## 7. 本轮可执行证据

新增 [TestSecurityComposition](../../internal/member/security_composition_test.go)，8 个配置：Worker=1/3，第三诚实成员未批准／额外批准但不进入 QC，来源正常／部分赔付后迟到。QC 的两票来自真实 `Member.ApproveDirect`；第四成员用测试配置中的对应私钥调用 `SignSpend` 模拟拜占庭自由签票，整个证书实际通过生产 `OutputCertificate.Verify`，三名诚实成员各有独立 Memory 状态。

轨迹包含隐藏完整 QC、子先执行、40/60 分拆输出只赔 40、外部真实补资 1 再 2、父迟到、成员不同连续前缀、重复跟块、拒绝跳过前缀，以及释放后重新批准下一笔独立付款。逐阶段检查原批准残余等于 Worker Reserved、残余≥当前证书风险、累计份额、公开 Reserved/Spent 和真实备付。未批准成员不获得返还，额外批准成员按自身占用处理。

它是**跨层实现轨迹**：区块由现有测试工具签出，赔付经济规则直接调用；用于 follower 的 Repair 命令不构造完整门限物化。因此不代表本测试执行了真实网络共识、修订密码学或磁盘历史修订。后两者使用已有独立测试，A4/A6 仍不由测试消除。

[账务投影测试](../../internal/rules/security_projection_test.go) 同时增强为逐 Grant 检查 `ΣCoverage.Paid=Usage.Spent`、`ΣCoverage.Remaining=Usage.Reserved`、`Original=Paid+Discharged+Remaining`，按真实账户聚合 `Σ(B−Spent)≤余额`。它沿用上一轮 18 个三跳顺序与部分输出场景。

2026-09-26 在 Windows、Go 1.27.1 完成以下检查，均通过，测试均使用 `-count=1`：

```sh
go test -count=1 -v -tags=comet_v3 ./internal/member ./internal/rules -run TestSecurity
go test -count=1 -tags=comet_v3 ./...
go test -count=1 ./...
go test -race -count=1 -tags=comet_v3 ./internal/member ./internal/store ./internal/blockfollow
go vet -tags=comet_v3 ./...
go build -tags=comet_v3 ./...
```

证据：[组合及账务详细日志](security-model-validation-2026-09-26/composition.txt)、[Comet 构建配置全项目日志](security-model-validation-2026-09-26/composition-go-test.txt)、[默认配置全项目日志](security-model-validation-2026-09-26/composition-go-test-default.txt)、[环境、命令及文件指纹](security-model-validation-2026-09-26/composition-verification.txt)。新改 Go 文件均为 `_test.go`，生产代码相对 `re@8d1590b` 未改；没有以新增 TPS 实验替代安全检查。

## 8. 完成状态与论文用语

| 层次 | 本轮状态 |
| --- | --- |
| 统一抽象状态／事件与分解接口 | 已明确，使用相同 Fact、cap、批准、Paid、Grant 和账户 |
| 固定配置、任意有限前缀的条件归纳 | 已给出手工推导及参数推广；需数学审阅，不是证明助手产物 |
| 当前 Direct 主路径代码对应 | 已逐函数核查线性化点并补跨层回归；不是全 Go 精化证明 |
| 一般活性、恢复、动态成员、资源耗尽 | 不在本轮安全定理范围内 |
| 门限变色龙与修改后 Comet | 后续 [R6 论证](security-redaction-consensus-wire4.md) 已分离适配、公共授权和原始字节绑定；完整密码学归约和全栈精化仍保留 A4/A6 |

建议论文表述：我们在统一的条件状态机中证明认证风险、直接责任与真实备付的组合不变量，并以实现对应及跨层回归补强关键接口。不要写成“代码全部形式化验证”“所有攻击下资金绝对安全”或“运行任意久均无积压”。本轮改动限于测试与文档，不改变在线签发和结算路径。
