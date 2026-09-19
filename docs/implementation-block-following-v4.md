# 按块处理实现规范与验证记录

> **数据库修订（2026-09-20）：** 跟块先在事务外 `PrepareBlock` 解码认证，再在同一写事务中读取最新状态、顺序应用并推进游标。direct 成员的批准证据保持不变，累计核销量存于 `LocalProgress.Applied`，状态查询和离线审计统一读取该记录。成员本地数据库 schema 为 **5**，委员会、网关及钱包仍为 **4**，线协议仍为 wire 4。旧成员 schema 4 拒绝直接打开，不提供自动迁移；不得手改 schema 或以旧二进制打开新库。Comet 搜索索引关闭，按高度的区块、执行结果和提交认证完整保留。实现与对照结果见[数据库优化实验](experiments/database-optimization-2026-09-20/README.md)。

> **当前公共提交修订：** [公共提交与新输出 TXCer 分离](implementation-public-submission-v4.md) 将内部 INSTALL 与公共提交明确拆开，并取消正常新输出的预先担保登记。组织批准仍须验证。

2026-09-19；当前分支 `implementation/block-following-v4`。这是 v1.2 直接担保方案的工程修订，使用独立创世的线格式／应用版本 4。原 `039374d` / wire v3 保留为实验基线，不在线迁移旧数据库。

## 正常路径

付款钱包发送 → 成员验证并同步保存输入锁和额度占用 → 三票形成 TXCer → 收款钱包验签、保存，可续花。

后台 INSTALL 继续保存、传播完整交易。中继提交精简公共交易；委员会执行并出块。钱包、成员、网关各运行一个按高度读取的 BlockFollower，每块验证一次，在一次本地事务内应用相关状态并推进游标。没有逐笔输出证明或成员核销证明查询。

输入字段 `InputCertificates` 只携带本交易实际使用的 TXCer，不携带父交易正文或祖先历史。公共编码省去本次新输出证书的重复正文；保留原有三票及预算向量，用于授权本笔输入消费及组织 FUEL 代付，并从交易重建签署摘要。不增加签名往返。

委员会保留消费裁决、公共输出、组织真实账户、直接责任、费用和赔付；删除正常结算的 `CreditReceipt`、专用事实树、逐事实索引、专用核销查询和重复 CAL 核销镜像。剩余公共覆盖余额属于账本资金约束，不是给成员发送的通知。

## 数据与模块

| 模块 | 当前职责 |
|---|---|
| `protocol/direct_payment.go` | 固定交易、必要组织授权、直接输入证书及允许改写的资金引用 |
| `protocol/execution.go` | 普通执行结果：是否新执行、当时缺失的输入索引、迟到输出索引；没有成员额度或收款通知 |
| `internal/committee/app.go` | 执行结果放入正常 ABCI Data；应用承诺绑定状态变化；新版本不生成私有证明历史 |
| `finality/block.go` | 验证固定委员会提交签名、块连接、交易根和整个执行结果根 |
| `internal/blockfollow` | 顺序读取、原子游标、重复块幂等；断线从本地高度补齐 |
| `internal/member/blocks.go` | 本地原批准记录、直接待办和费用计划驱动额度恢复及输出导入 |
| `internal/wallet/blocks.go` | 按成功输出及实例更新币记录，核对内容并移除 TXCer |
| `internal/gateway/blocks.go` | 跟块停止已成功的公共补交，清理 INSTALL 重试缓存 |
| `cmd/payctl` | 钱包发送起计时、跟块观察、成员实际处理、停止后审计 |

成员只给自己确实签署并扣过的额度返还：正常已履行 CAL 可释放，实际赔付 CAL 保持净支出；FUEL／代付策略额度扣除真实费用后返还，工作额度在必要收尾完成后释放。输入尚缺时只保存直接等待索引；前置输出到达或 RepairInput 成功后结束该项等待，不遍历祖先链。

区块读取接口使用 `/block?height=H`、`/block_results?height=H`、`/commit?height=H+1`，返回 Comet 普通结果结构。它们不接受钱包或成员身份，不制作单笔证明。`/block` 提供原始执行版本；真实历史修订仍存于 BlockStore，通过后续不可变 RepairInput 处理。不能把改写后的旧输入当成早已赔付。

H 的执行结果由 H+1 的 `LastResultsHash` 认证，因此仍需要必要的后续块。包含交易不等于执行成功：失败结果不更新币或返额度；幂等重复命令的空结果也不重复应用。哈希规则由交易版本标记决定，钱包无需持有或配置修复密钥就能验证交易根。

`GET /v4/progress/{spend}` 是成员本机的实验观察接口，不是委员会回执，也不参与业务授权。后台没有按交易轮询它；只有实验程序用它确认四个成员确实完成本地应用。

## 构建与复现

```sh
export PATH=/usr/local/go/bin:$PATH
python3 third_party/cometbft/overlay.py
go test -race -tags=comet_v3 ./...
go vet -tags=comet_v3 ./...
go build -tags=comet_v3 -o bin/ ./cmd/committee ./cmd/member ./cmd/gateway ./cmd/payctl
bin/payctl init-lab -v4 -dir experiments/block-v4 -port 25000 -outputs 1024
UTXO_EXPERIMENT_FLUSH=10ms UTXO_EXPERIMENT_GOSSIP=10ms bin/payctl lab-run -dir "$PWD/experiments/block-v4" -bin "$PWD/bin"
# 另一个终端；每次选择未使用的初始输入。
bin/payctl bench-v4 -dir experiments/block-v4 -start 1 -count 1 -concurrency 1
bin/payctl bench-v4 -dir experiments/block-v4 -start 100 -count 128 -concurrency 64
bin/payctl demo-v4 -dir experiments/block-v4 -input 3 -withhold-parent
# 停止该实验网后：
bin/payctl audit -dir experiments/block-v4
```

`comet_v3` 是沿用的编译开关名称，启用受限 Comet 补丁；当前运行协议为 wire 4。已有 `/v3/transactions` 等传输入口名称保留，解码器只接受当前网络对应格式。

## 实验记录

Mac Studio M4 Max，14 个独立进程，两个组织各四成员、两个网关、四委员会。新网 `.run/block-v4-a`；10/10 ms 传播配置，关闭埋点，保留所有验签和同步持久化。

| 运行 | 结果 |
|---|---|
| 单笔，初始输入 1 | 快速到账 42.928 ms；钱包跟块观察 518.519 ms；观察到全部成员本地应用 520.785 ms |
| 128 笔，并发 64，输入 100–227 | 0 失败；快速到账 p50 171.514 ms，p95 228.376 ms；跟块观察 p50 3034.306 ms；成员应用观察 p50 3056.523 ms；整轮 7.383 s，短时闭环 17.337 笔/s |
| 连续跨组织父子交易，输入 2 | 父快速到账 58.913 ms；子快速到账 46.229 ms；子跟块观察 796.343 ms |
| 故意不投递前置交易，输入 3 | 子快速到账 68.920 ms；子跟块观察 719.378 ms；约 30.621 s 观察到赔付；迟到前置交易创建实例 1 |
| 含历史改写的实验网重启后，16 笔，并发 8，输入 400–415 | 0 失败；新钱包从首块补齐后正常处理；整轮 2.345 s；快速到账 p50 104.384 ms |

时间均从对应付款钱包发出 HTTP 请求开始，快速到账终点为收款钱包验签并持久保存。跟块和成员完成时间包含读取、验证和实验轮询开销，不能称作委员会纯执行耗时。128 笔的 7.383 秒是整轮时间，不是每笔延迟。

首次接入实验暴露了读块端沿用旧交易哈希的问题，已修复并新增无需配置修复密钥的哈希测试。该次输入 0 的钱包观察失败、交易实际已结算，保留原始失败报告，不纳入以上成功运行的性能统计。

重启后另一次输入 300–315 的实验在网关监听前启动，16 次连接均被拒绝、没有提交交易；该报告也保留并排除。待服务就绪后的运行使用输入 400–415。

验证包括全包 race 测试、go vet、执行结果篡改与法定人数不足、原子游标回滚、重复／跳高度、直接责任到达／赔付／迟到、真实历史改写及应用重放。停止审计检查资金总量、原扣额度、待办排空、四委员会应用状态一致以及旧证明记录不存在。

最终停止审计：150 笔付款全部费用收尾完成、所有成员及网关待办为 0、资金缺口为 0，四个委员会应用状态相同，真实历史修订 1 次。CAL 实际赔付与 FUEL 实际费用保留为净支出。实验网已停止，数据保留；原始报告位于 [实验目录](experiments/block-following-v4/)。

旧版短闭环约 13 笔/s，与本版 17.337 笔/s 的测量完成门槛有所变化，不能据此给出严格加速比。此次确认的是整条逐笔证明工作链被移除，正常与赔付闭环仍成立；持续高 TPS、长期存储边界和最新修订轻客户端验证仍需专项实验。
