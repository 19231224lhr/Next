# Lightning 原生实验工具

独立 Go module，不改变 Next 的依赖与支付逻辑。部署目标是 Mac Studio；仅使用 Bitcoin **regtest**。

## 当前验证状态

已完成官方 ARM64 二进制校验、双节点真实付款预扫与一轮资金依赖续花；Go 工具通过单元测试、`go vet` 与 Darwin ARM64 交叉编译。**正式持续负载重复与路由拓扑仍待完成，当前结果属于预实验。** 见[实验记录](../../docs/experiments/lightning-2026-09-24/README.md)。SHA256 清单经官方 HTTPS 获取，尚未做发布者 PGP 签名验证。

## 部署与烟测

从仓库根目录运行：

```sh
python3 tools/lnbench/download.py
(cd tools/lnbench && go test ./... && go build -o ../../.run/lnbench .)
python3 tools/lnbench/lab.py start pilot
.run/lnbench -config .run/ln-pilot/endpoints.json \
  -count 2 -out docs/experiments/lightning-2026-09-24/pilot/smoke
python3 tools/lnbench/lab.py stop pilot
```

同一时间只启动一个实验环境。脚本使用固定、仅本机监听的端口，拒绝覆盖已有实验目录；状态保存在 `.run/ln-*`，不得提交钱包、种子、macaroon 或节点数据库。`noseedbackup` 仅用于这些可丢弃的 regtest 钱包，不关闭通道持久化或 RPC 认证。

双节点使用一条 1600 万 sat 通道，初始双方约各 800 万 sat；开通方还承担承诺手续费。`--routed` 改为三节点两通道。每轮在开通道、确认与路由可用后计时，测量期间不挖块、不充值。

## 测量口径

- 发票在计时前创建；发送使用常驻 TLS gRPC、普通发票、单路径单分片。
- 同一 Go 单调时钟记录实际 `SendPaymentV2` 调用前、接收方 `SETTLED` 订阅到达、发送方 `SUCCEEDED` 到达。
- `-rate 0 -count 100` 是交替方向的串行付款，不自动代表资金依赖实验。
- `-rate 50 -duration 180s` 是双向合计 50 笔/秒；固定发送窗口，超过窗口的计划任务保留为 `NOT_SENT`，不延长发流伪装目标 TPS。
- 原始数据包含失败和未发样本。超时记为 `UNKNOWN`，不声称资金未转移。结束后按 hash 查询发票独立核对，不用查询时间补造接收事件。
- `summary.json` 的 SuccessTPS 是成功数除以含排空的测量时长，不等同于稳定容量；持续性还须检查 `progress.json`、实际发送速率和未决趋势。
- 同步存储的 LND 与历史 Next 内存版数字不能直接解释为同等保障下的协议速度差。

## 资金依赖链

使用独立环境：`lab.py start chain --capacity 1000000 --push 20000`，随后 `lnbench -chain -amount 950000 -count 100 ...`。

每步检查收款方只有一个通道、没有未决 HTLC，且本地余额小于下一笔金额；后继付款真实成功才构成资金依赖证据。100 笔对应 99 个后继依赖关系。储备、手续费和两侧 HTLC 限额以实际通道快照为准，950000 sat 尚待预跑验证。

下一笔等上一笔发送端成功后再发；余额查询与静止等待计入整链时间，另存 `chain-checks.json`。这不是“SETTLED 瞬间最快续花”，也不是 LN 中逐笔追踪同一 UTXO。

`run.py` 记录各进程 CPU/RSS 样本，并在每组前后等待通道 HTLC 清空。`suite.py` 为每次重复创建 fresh 通道并在结束后关闭节点；正式矩阵和对照边界见 [实验方案](../../docs/research/lightning-comparison-plan-2026-09-24.md)。Mac 长批次应在整个驱动外层使用 `caffeinate -i`，不要只覆盖部署过程。
