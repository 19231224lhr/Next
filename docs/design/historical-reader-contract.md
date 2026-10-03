# 历史资金表示的读者契约

2026-10-02 · 适用当前 wire4 / decision-first 规则。本文说明已有内部函数的组合使用方式，**不是新增 RPC 或外部无状态证明格式**。代码核对与回归见[第①项审查](../research/review-step1-2026-10-02/README.md)。

## 1. 读者、输入与信任来源

读者是一个已经验证并执行公共前缀的诚实副本。网络、创世、组织配置、规则及门限公钥均已确定。记当前读事务中的已执行高度为 H，应用状态为 V_H；“收到高度 H 的提交证明”不等于“已执行到 H”。

查询输入为原始区块坐标 `(h, j, k)`：区块高度、原始完整交易列表中的索引、付款的输入索引。要求 `1 ≤ h ≤ H`，交易存在且在原始执行中成功，输入槽合法。不能把 j 改成过滤掉维护命令或失败交易后的付款序号。

所有应用记录在**同一个固定读事务**内读取，包括高度、修订、成功决定及义务状态。当前能力可以服务于持有当前快照的副本；不据此声称数据库提供任意历史 H 的查询 API。重放到较早前缀的副本同样遵守这条契约。

## 2. 分开读取三类信息

| 信息 | 现有实现 | 正确解释 |
|---|---|---|
| 逻辑区块与修订号 | `redaction.Canonical(V_H, BlockStore, h)`；`RevisionKey(h)` | 截至 H 的已授权表示，不是后台安装进度 |
| 该槽的赔付决定 | 由原始输入 OutputID 查 `rules.DirectRepairKey`；块级枚举可用 `DecisionIndexKey` | 保存成功决定、决定高度、不可变义务快照、实际 Debit 身份 |
| 当前经济状态 | `rules.DirectObligationKey(OutputID)`；必要时账户与 Coverage | **截至 H** 是否 Open、Fulfilled、Repaired 或 Recovered |

`DecisionPendingKey` 是待表示工作索引，表示完成后会删除，不能用它判断赔付是否曾经发生。成功决定索引和 `DirectRepairTodo` 保存历史事实。

这三个读结果不能互相替代：

- `OriginalFunding`：该槽尚未以修订表示垫付；可能已经赔付，甚至已经回款。
- `ReserveFunding`：该历史输入获得过已授权的备付垫付；不表示当前仍存在净损失。
- `Repaired`：已赔付、尚未回收；`Recovered`：迟到来源已经偿还。两者都不重新增加后继收款人的余额。

可供论文描述的逻辑返回值是 `(H, h, j, k, Funding, revision, decision-or-absent, obligation-as-of-H)`。现有函数分别提供这些材料；这里没有把该逻辑元组冒充已经部署的一体化网络接口。

## 3. 授权从哪里来

原始表示来自已验证的原始执行历史。修订表示来自该前缀内**成功执行**的 `CompensationDecision` 和精确绑定正文的 `RepairInput` / `RepairBatch`：

1. 决定执行检查 Open 义务、到期、网络、坐标、原授权及金额，原子写入实际扣款和决定。
2. 表示执行检查决定、当前逻辑版本、唯一目标资金引用、输入开口、精确新正文、原完整 BlockID；只写修订和任务。
3. 后台安装从已提交 Task 取精确正文，再次核对正文与身份后写 BlockStore。安装没有经济效果。

`Canonical` 信任已经执行这些检查的本地状态。它不是一个对不可信服务器返回块进行完整认证的验证器。交易包含证明、相同 BlockID 或变色龙哈希等式，单独都不足以证明“这个表示是截至 H 已获授权的版本”。未经成功执行的命令，即使被收入区块，也不产生表示授权。

若要未来支持不可信远端读者，应另定义与认证状态根或原始执行证明相连的授权证据；本轮不新增该能力，也不把它写入论文现有实现。

## 4. 逻辑版本与物理版本

`Canonical` 首先读取 V_H 内的 `Revision.Body`，无修订时才调用 `LoadOriginalBlock`。它不以 `LoadBlock` 当前物理正文代替逻辑答案。

因此，无论物理安装落后，还是用于较早前缀重放的 BlockStore 已安装更晚版本，答案均由 V_H 决定。存在修订但正文无法解码时返回错误，不能默默退回原始表示；原始历史不可得时返回不可用，不能推断“没有赔付”。当前契约假定相关历史未被裁剪。

应用状态缺少成功决定，在完整、已验证、未裁剪的 V_H 中可以解释为“截至 H 没有该决定”；缺少历史材料、读取错误、非法坐标必须单独报告。物理安装进度若另行展示，应注明它是观察时刻的本地状态，不能混作 H 下的经济结论。

## 5. 身份映射

| 层次 | 计算与检查 | 不应混淆的结论 |
|---|---|---|
| 支付 `TxID` | `protocol.FastTx.ID` 固定授权与承诺；所有者签名绑定该 ID | 不等于共识 `Tx.Hash()` |
| 输出身份 | `OutputIdentity(Network, TxID, index)` | 修复不能改变输出及后继引用 |
| 共识交易哈希 | 合法 `UTXO4CH` 封装对 `header + fixed` 做 SHA-256；畸形长度回退哈希全字节 | 哈希函数不承担付款合法性校验 |
| `DataHash` | `Txs.Hash` 对共识交易哈希构造 Merkle 根，`Data.Hash` 使用该根 | 单个包含证明不赋予修订权限 |
| 分片承诺 | 绑定 chain、key、height、part index 的 CH 承诺 | 适配相容性与公共授权是两项检查 |
| 完整 `BlockID` | 区块哈希和原 `PartSetHeader` 都保持；表示执行与安装均核对 | 不能只比较头部哈希 |
| 原始共识入口 | `ValidateOriginal` 要求正确高度、revision=0、opening=1；应用正常验证原付款 | 修订分片不能冒充新的原始执行分片 |

重放使用保留的原始命令，顺序重新执行赔付决定、来源回款与表示命令。修订后的付款正文不作为新付款再执行。

## 6. 可检验职责

- `TestCompensationBeforeAdaptation`：决定已赔付但逻辑表示仍为原版；回款不删除决定。
- `TestPaymentRepairMonetaryReplay`：逻辑修订先于物化可读；旧执行前缀不读取后来的物理修订；回款后仍保留垫付表示；全部原始命令逐高度重放一致。
- `TestMaterializationRevisionOrderAndCursorReplay`：落后任务不能倒退当前物理版本。
- `TestRealBlockStoreRewriteAndOriginalReplay`：真实重写保持完整 BlockID、提交证据和交易包含关系，并保留原始重放字节。

后续读者微基准应比较同一 V_H 下的优化决定索引与规范表示，使用相同授权前提。这里固定了评价对象，尚未产生新的读取性能数据。
# 2026-10-03：可执行导出示例与状态对齐

本轮新增 `internal/redaction/history_export_test.go::TestHistoricalExportConsumer`。可设 `UTXO_HISTORY_EXPORT` 输出 JSON，包含读取前缀、原始 `(height, transaction, input)` 坐标、完整 BlockID、Revision/Task、原始与授权 block body、历史资金表示及当前义务。实际断言精确 Task 字节、部件承诺、所有者授权、QC、输入 CH 和迟到回款前后差异，见 `docs/experiments/major-revision-2026-10-03/history-export.json`。这是已验证且保留历史的本地副本上的示例消费者，不是新远程轻客户端协议。

公开付款 `Settled` 表示成功执行；其中正常新建输出最终可用，已赔付输出改为偿还储备而不重建。义务 `Repaired` 表示已赔付，`Recovered` 表示后来已偿还。逻辑 `ReserveFunding` 仍表示当时的历史垫付，回款不会把它改回；物理安装进度不决定同一公共前缀的逻辑读取结果。永久决定及义务记录在两条读取路径中仍然必要。
