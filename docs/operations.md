# 运行与验证指南

本文集中保存当前原型的构建、运行、存储选项与验证命令。具体实验复现应以[实验报告](experiments/README.md)固定的版本和配置为准。

## 快速开始

需要 **Go 1.27.1** 与 **Python 3**。以下命令适用于 macOS / Linux shell，在仓库根目录执行。

### 1. 构建程序

```sh
python3 third_party/cometbft/overlay.py
mkdir -p bin experiments
go build -tags=comet_v3 -o bin/ ./cmd/committee ./cmd/member ./cmd/gateway ./cmd/payctl
```

`comet_v3` 是沿用的编译开关名称，用于启用受限 CometBFT 补丁；当前运行协议仍为 **wire 4**。

### 2. 初始化并启动实验网

```sh
bin/payctl init-lab -v4 -dir experiments/local-v4 -port 25000 -outputs 1024

UTXO_EXPERIMENT_FLUSH=10ms \
UTXO_EXPERIMENT_GOSSIP=10ms \
bin/payctl lab-run -dir "$PWD/experiments/local-v4" -bin "$PWD/bin"
```

初始化拒绝覆盖已有实验目录。默认实验网包含两个组织、八个成员、两个网关和四个委员，共 14 个服务进程；上面的单组织性能实验使用九个服务，部署条件不同。此快速开始沿用默认存储配置，不等同于性能报告中的内存模式。

### 3. 在另一终端发送一笔付款

```sh
bin/payctl bench-v4 -dir experiments/local-v4 -start 1 -count 1 -concurrency 1
```

每次独立测试应选用未消费的初始输入。需要阶段诊断、连续续花或 TPS 复现时，使用对应[实验报告](experiments/README.md)中的参数和脚本。

### 4. 停止节点并审计

在启动实验网的终端按 `Ctrl+C`，等待所有子进程完成停机和审计快照导出，再执行：

```sh
bin/payctl audit -dir experiments/local-v4
```

审计核对 CAL/FUEL、费用、成员原始占用及待办状态。内存模式导出的数据库仅供审计，不用于恢复运行；下一轮使用新的实验目录。

## 运行配置与诊断

仅按需要启用以下选项。同条件对照应保持其他参数不变，完整组合以具体实验报告为准。

| 配置 | 用途 |
| :--- | :--- |
| `UTXO_EXPERIMENT_MEMBER_MEMORY=1` | 成员使用内存状态，保留原子更新与签票顺序 |
| `UTXO_EXPERIMENT_GATEWAY_MEMORY=1` | 网关使用内存状态，保留已完成状态检查与有界后台任务 |
| `UTXO_EXPERIMENT_COMMITTEE_MEMORY=1` | 委员会应用使用内存状态；需搭配 Comet 内存存储 |
| `UTXO_EXPERIMENT_MEM_BLOCKSTORE=1` | Comet 区块及相关状态使用实验 MemDB |
| `bench-v4 -wallet-no-sync` | 钱包不等待同步刷盘，仍执行验证与原子接收 |
| `UTXO_EXPERIMENT_COMMIT=250ms` | 显式调整高度推进时序；默认 500 ms，与执行过程重叠 |
| `UTXO_EXPERIMENT_FLUSH=10ms` | 调整发送刷新间隔 |
| `UTXO_EXPERIMENT_GOSSIP=10ms` | 调整交易传播间隔 |

成员默认 bbolt 模式已启用 NoSync，启动日志会显示 `storage_no_sync=true`。网关、钱包及委员会应用默认同步写入，除非显式选择相应实验选项。内存模式正常停机导出审计快照，拒绝将快照作为可恢复数据库重新打开。

### 压测与诊断

- **发送和观察分别限流：** `bench-v4 -concurrency 256 -max-pending 2048` 在钱包快速接收后释放发送名额；后台继续观察，总未完成任务受独立上限约束。报告保留发送滞后、未完成数量及最老等待时间。
- **单组织负载：** `bench-v4 -same-org` 在同一组织服务的两个钱包之间付款；成员进度默认批量查询，仍按各成员自己的逐笔状态判断完成。
- **缓存边界：** 已验证的完整收款描述符使用 1024 项有界缓存。逐笔付款授权、法定票数、输入消费与额度检查仍保留；热地址收益不能直接推广到大量新地址。
- **阶段诊断：** `UTXO_SETTLEMENT_TRACE=1` 启用委员会阶段记录。诊断会增加开销，应与正式性能轮分开；应用 Commit、钱包跟块观察和成员完成观察是不同时间点。

## 测试与构建验证

先应用 CometBFT 补丁，再执行测试和静态检查：

```sh
python3 third_party/cometbft/overlay.py

go test ./cmd/... ./crypto/... ./finality/... ./internal/... ./protocol/...
go test -tags=comet_v3 ./cmd/... ./crypto/... ./finality/... ./internal/... ./protocol/...
go vet -tags=comet_v3 ./cmd/... ./crypto/... ./finality/... ./internal/... ./protocol/...
go test -race -tags=comet_v3 ./cmd/payctl ./protocol ./internal/member ./internal/gateway ./internal/committee ./internal/store
```

这些命令明确限定源码目录，避免旧实验源码快照被当作业务包编译。实验私钥、数据库和构建产物不提交 Git；报告、公开配置、审计结果与复现脚本按实验目录保存。
