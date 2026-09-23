# E3：直接责任、赔付与历史输入修订

**状态：核心驱动与本地验证已完成，Mac 网络实验尚未运行。** 本目录没有正式实验结果，不作为 E3 已成功的论文证据。工作分支为 `e3-liability-repair`，从 `re` 的 `ef24af5` 开始。

依据[实验设计](../../research/e3-liability-repair-experiment-design-2026-09-23.md)与[本轮 GPT 评审](../../research/e3-implementation-review-2026-09-23.json)。当前阻塞是 Windows Tailscale 一直处于 `NoState`，SSH 在握手阶段断开，不能确认或操作 Mac 实验进程。

## 已实现内容

| 内容 | 当前证据 |
|---|---|
| 固定异常集合 | 按计划索引，每 100 单位固定选 1 或 5 个；相同种子保证嵌套；运行前保存 `repair-selection.json` |
| 真实异常触发 | 异常父持续不投递，直到已提交赔付且四委员的历史输入实际改写；超时不擅自放父 |
| 独立终点 | 钱包 READY、钱包观察公共成功、成员跟块完成、各委员观察到修复提交／历史改写分别记录 |
| 改写观测 | `/v3/repairs/{output}/status` 读取已提交 Task 和真实 BlockStore，核对目标 Funding 槽、原交易身份、BlockHash/DataHash 与字节变化 |
| 异常子阶段 | 在原有 `UTXO_SETTLEMENT_TRACE=1` 下记录输入份额、分片份额、提交和物化的耗时、错误、命令大小 |
| 资金审计 | 复用原停机审计；新增分析核对 CAL 净损失、用户 FUEL、退款、报酬、销毁、漏发及未完成 |
| 确定性规则验证 | 两组织孙交易先到、前置在赔付前／后到达、同块两种顺序、重复赔付、真实旧块改写、原历史重放 |

观测接口不是轻客户端证明。等待四份物化状态只是本实验控制迟到父的释放条件，没有把共识改成四票。已提交但未物化会明确返回两种不同状态。后续其他输入再修订时，按本项输入槽检查，不要求整个交易仍等于较早修订的正文。

时间字段是本机首次观察时间，包含 250 ms 轮询、RPC 与调度等待；不能当作远端精确 Commit 时间。协议到期仍依赖共识锚定的 Deadline。本地时间仅用于减少到期前的无效查询。修复日志里的成功物化调用也可能是幂等调用，统计时应按 RepairID 去重。

## 本地验证（Windows，2026-09-23）

下列检查通过：

```powershell
go test -count=1 -tags=comet_v3 ./cmd/... ./internal/... ./protocol/... ./crypto/... ./finality/...
go test -count=1 -race -tags=comet_v3 ./cmd/payctl ./internal/redaction ./internal/rules
go vet -tags=comet_v3 ./cmd/... ./internal/... ./protocol/... ./crypto/... ./finality/...
python docs/experiments/liability-repair-2026-09-23/test_analyze.py
```

原 `TestProgressBatchIndividualCancellation` 在 Windows 上因模拟 HTTP 立即返回、计时值为 0 而失败。仅给测试替身加入 1 ms 耗时后，连续 10 次通过；没有修改运行时业务路径。

分析器的三个合成夹具仅验证统计逻辑，不是实验数据。两组织测试是确定性账本测试，不代替多进程网络实验；真实改写与原历史重放由已有磁盘夹具继续核对。

## Mac 复现入口

连接恢复后，先同步分支、检查无旧实验占用，再在 `/Users/richz/lab/man/utxo-fastpay-v12` 执行：

```bash
python3 docs/experiments/liability-repair-2026-09-23/reproduce.py --case smoke
python3 docs/experiments/liability-repair-2026-09-23/reproduce.py --case calibration --skip-build
python3 docs/experiments/liability-repair-2026-09-23/reproduce.py --case matrix --skip-build
```

每轮拒绝覆盖旧目录，必要时更换 `--prefix`。主矩阵启动前会检查相同二进制和预算配置的两组校准是否成功。

| 参数 | 当前预跑候选，尚未经 Mac 校准 |
|---|---|
| 规模 | 1 组织、4 成员、1 网关、4 委员 |
| 主矩阵 | 20 两跳单位/秒，300 秒输入、90 秒有界收尾 |
| 异常比例 | 0%、1%、5%，三种种子轮换运行顺序 |
| CAL Grant／账户 | 三档统一 60,000；不自动补资 |
| 用户 FUEL | 独立最终 FUEL 输入，不由组织代付 |
| 在途上限 | 128 单位；满时如实记录未启动 |
| 存储 | 委员会磁盘 BlockStore＋应用 bbolt；组织内存模式 |
| 正常父／异常父 | 正常 READY 后约 1 秒；异常等待真实修复完成 |

`smoke` 当前覆盖正常父与赔付后迟到父各三次。`calibration` 为 0%／5% 各 60 秒。`matrix` 为三个种子下的九轮主矩阵。失败时保留日志、原始报告和错误，不能提前放父、加钱或删样本来制造成功。

## 尚待完成

- Mac 上的网络控制、校准和正式九轮；所有性能与开销数字仍为空。
- 补齐“两组织三跳、孙先到”以及“父永久不到、后继继续支付”的多进程小序列，再各运行三次；当前只有对应规则层／已有单元测试证据。
- 汇总修复子阶段、修复队列、CPU 和实际修订字节开销；整理时间线图、对照图和完整财务审计表。
- 根据实际结果与 GPT 讨论能支持的论文结论；不能提前把本轮核心代码完成等同于完整 E3 成功。

完整闭环后才适用 `CAL净损失=100×实际赔付项数` 和 `用户费用=188×完成两跳单位数+5×实际赔付项数` 的对照；存在部分完成时以实际逐阶段账务为准。分层定额异常注入也不能称为独立随机故障或任意拜占庭故障测试。
