# 本轮实验复现入口

所有实验按新创世运行，不在旧数据上升级协议。工作代码为 `d99ec92`，先按[运行指南](../../operations.md)应用 Comet 补丁。脚本保存了本轮 Mac 的绝对路径 `/Users/richz/lab/man/utxo-review-20261002`；在其他 checkout 复现时修改脚本顶部的 `ROOT` / `R`，并保持配置不变。

## 构建

当前存储、吞吐与链控制使用 `bin-review3`。成员单独固定 GOGC 和 GOMAXPROCS，其他进程不加这两个覆盖：

```sh
python3 third_party/cometbft/overlay.py
mkdir -p bin-review3
go build -tags=comet_v3 -o bin-review3/ ./cmd/committee ./cmd/gateway ./cmd/payctl
go build -tags=comet_v3 -o bin-review3/member-real ./cmd/member
```

在 `bin-review3/member` 写入可执行包装器，`member-real` 路径应指向上述实际构建位置：

```sh
#!/bin/sh
exec env GOMAXPROCS=16 GOGC=200 /absolute/checkout/bin-review3/member-real "$@"
```

`chmod +x bin-review3/member` 后，运行所选脚本。脚本要求该轮结果目录和 `.run` 子目录尚不存在，以避免覆盖旧轮；需要复跑时先改用新的实验根目录。

## 选择实验

| 脚本 | 工作内容与依赖 |
| --- | --- |
| `chain-sync-network.py` | 可独立执行的九轮磁盘部署控制：NoSync 快速、同步快速、同步逐跳等待，各三轮。使用预先构建的 `bin-review3`。 |
| `chain-memory-network.py` | 九服务内存配置的快速/等待连续链；配置与当前论文主图对应。 |
| `owner-recovery-network.py` | 九轮恢复矩阵；复用仓库 E2 用户自付驱动，需要 `bin-review2` 为当前规则构建。保留该目录名用于对应原始记录。 |
| `storage-tps-network.py` | 六轮成员提交存储对照及三轮短吞吐；会构建 `bin-review3`。本轮按恢复矩阵完成后启动，脚本的开头明确等待 `review-owner-progress.log` 成功标志。独立复现时先完成矩阵并保存该日志，或显式移除这个实验串行调度等待；业务配置不变。 |
| `paired-network.py` | 14 服务正常/修复背景成对轮，原始构建与费用设置见各轮配置。 |
| `liability-controls.py` | 永久缺失与跨组织恢复两项控制。 |

脚本捕获子进程输出，在 `finally` 中向实验进程发 SIGINT 并等待退出，再审计停机快照。结果为源码旁的 `review-*-results`，既有历史 E1–E8 目录不变。

## 审计与论文数据

每轮首先检查四委员 StateHash 一致、预期付款全部 Closed、Gap=0、Pending=0。永久缺失控制另外要求真实净损失保留；恢复矩阵检查 Paid=Recovered、组织备付及净 Spent/Reserved。

`curate-sync-chains.py` 校验九轮配置只在指定变量上不同，并逐笔确认快速后继发送介于父 Ready 与父公共完成观察之间；它保留公开摘要、逐笔链和二进制摘要，不复制私钥。`analyze-owner-recovery.py`、`reaudit-paired.py` 和 `summarize-network.py` 为本轮分析脚本，读取位置在其顶部标明。

仓库的 `evidence/` 已保存本轮必要结果；完整大日志由 `archive-manifest.json` 索引。要核查本轮结论可直接从这些冻结结果开始，无需重新生成实验账户。
