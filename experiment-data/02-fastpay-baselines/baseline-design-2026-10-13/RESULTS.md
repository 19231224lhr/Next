# Next 与官方 FastPay：单机复现及统一计时

本轮完成了官方 FastPay 的本地复现和 Next 的同主机对照。五轮主实验加三轮固定负载实验共取得 **2,650 笔核验成功的收款**；Next 的后台公开执行与成员观察均完成。当前结果支持一个具体结论：**Next 的独立付款中位延迟较低，FastPay 的新客户端首笔收款更快，100 笔依赖付款的总时间中位数接近。**

**配置补核（2026-10-13）：** 本轮使用默认普通 Direct，未启用成员/网关的 `--same-org-continuation` 和网关的 `--compact-direct-approvals`；服务启动只传入配置文件。数据仍有效，但它不代表已有可选同组优化全部启用后的成绩。准备成本与后续对照建议见 [设计与实测差距讨论](DESIGN-SPEED-DISCUSSION.md)。

## 主对照结果

以下是五轮各自 p50/p95 的中位数，单位 ms。每轮重新初始化状态，两系统交替先后运行。首笔每轮只有一个观测，故不据此报告 p95。

| 场景 | Next p50 | FastPay p50 | Next p95 | FastPay p95 |
|---|---:|---:|---:|---:|
| 新客户端首笔收款 | 10.21 | 7.01 | — | — |
| 10 笔依赖付款：单笔 | 4.75 | 6.35 | 10.71 | 7.11 |
| 100 笔依赖付款：单笔 | 5.12 | 6.43 | 9.05 | 6.78 |
| 100 笔独立付款：单笔 | 3.95 | 6.33 | 5.65 | 6.80 |

独立付款的 p50 从 FastPay 的 **6.33 ms** 降至 Next 的 **3.95 ms**，降低 **37.6%**；p95 从 6.80 ms 降至 5.65 ms。依赖付款按收款凭证构造下一笔，单笔中位延迟也更低，但长链 p95 较高。

| 从首笔发送至末笔收款的总时间 | Next (ms) | FastPay (ms) |
|---|---:|---:|
| 10 笔依赖付款 | 63.07 | 63.69 |
| 100 笔依赖付款 | 648.05 | 646.93 |

100 笔长链总时间中位数分别为 648.05 ms 和 646.93 ms，差约 0.17%。各轮总时间范围为 Next 589.62–675.35 ms、FastPay 626.46–697.47 ms；这里描述观测值接近，没有进行统计等价性检验。

总时间与单笔 p50 的差别有直接记录可以解释：长链中后续 99 笔请求的构造时间累计中位数，Next 约 **95.56 ms**，FastPay 约 **4.98 ms**；Next 的长链尾部耗时也更高。首笔构造发生在第一次发送之前，处于长链计时之外；后续构造开销计入总时间，见 [构造时间记录](chain-construction.jsonl)。

## 相同固定发送负载

每个负载做三轮、每轮 30 笔独立付款；下表为各轮统计量的中位数。两系统都实际达到了设定速率，最大单轮偏差 **0.34%**。

| 目标 (笔/s) | Next 实际发送 (笔/s) | FastPay 实际发送 (笔/s) | Next p50 / p95 (ms) | FastPay p50 / p95 (ms) |
|---|---:|---:|---:|---:|
| 10 | 10.007 | 9.995 | 3.41 / 5.76 | 7.99 / 8.98 |
| 25 | 25.040 | 24.998 | 3.02 / 5.07 | 8.16 / 8.74 |
| 50 | 50.159 | 49.967 | 3.26 / 4.75 | 7.54 / 8.27 |

这些是低负载下的收款延迟比较。未限速的独立付款采用收款后再发下一笔的串行客户端；它的有限窗口收款速率约为 Next 236 笔/s、FastPay 156 笔/s。这些数值对应当前串行负载，不作为系统最大吞吐量。

## 统一口径与运行条件

- **收款终点：** 收款钱包核验材料并接受可供后续付款使用的资金。Next 在 `ReceiveDirect` 接受认证输出后停止计时；FastPay 完成官方 `receive_from_fastpay` 的证书验证和 quorum 同步后停止计时。FastPay 每次运行还额外执行一次不计时的末笔收款人转出，检查实际续付和本金守恒。
- **计时起点：** Next 在付款人向 gateway 发出 HTTP POST 前开始，FastPay 在第一次 authority RPC 调用开始。均使用原生单调时钟，服务器、CLI 启动和初始密钥生成处于计时之外。请求构造和签名前置开销单列；发生在第一次 RPC 之后的恢复操作仍被计入。长链总时间包含后续付款的构造与签名。
- **负载：** 每笔 100 个原生价值单位。依赖链使用三个钱包，只有第一个钱包预置本金，之后每笔花掉上一笔实际收到的资金；Next 同时检查前一输出 ID 与后一输入相等。独立付款使用付款/收款两个钱包。Next 的费用资源与担保备付按原协议准备。
- **主机：** Windows 上的 Ubuntu 24.04 WSL2，AMD Ryzen AI 9 HX 370，WSL 可见约 15.2 GiB 内存。每套系统独占运行，共享四个逻辑 CPU 的总预算（affinity 0–3；Next `GOMAXPROCS=4`）。没有测量峰值 RSS 或单独的 CPU 利用率，因此这里比较的是收款性能。
- **真实服务：** FastPay 为四个 authority、一个 shard、三票 quorum；Next 为四个担保成员、四个真实委员会服务和一个 gateway，共九个服务器进程。Next 使用常规同组织付款与正常后台结算；新内部资金池的结果没有混入本次对照。
- **存储与通信：** FastPay authority 与钱包为内存状态，使用原生 TCP。Next 的 member/gateway/committee 应用存储及 Comet block/state/evidence 使用内存，钱包使用不等待同步的 Bolt；部分 Comet 文件仍可能写盘，通信使用原生 HTTP。两系统运行目录均位于 Linux ext4。这些实现差异保留在 JSON 中。

这是一组固定硬件预算和收款口径下的原生实现对照，能补充外部基线证据。上述数字属于本机配置；多机实验将进一步测量网络延迟和分布式部署下的表现。

## 固定代码与客户端路径

Next 基准为 `f6d038748293509875e0e1fe47069b5b9707485b`。唯一 Go 代码修改是实验客户端过滤付款人之外的 `ResourcePolicy` 授权，修复同组织独立付款误带收款人授权的问题，见 [代码补丁](next-code.patch)。CometBFT 为 v0.38.26 加仓库中的既有适配；已在全新目录重建，并验证 454 个生产源码/模块文件与实验依赖一致，见 [重建检查](comet-reproduction.json)。

FastPay 使用 [官方仓库](https://github.com/novifinancial/fastpay) 的 `db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2`。协议源码保持官方版本；增加的 [原生测量客户端](fastpay_client.rs) 调用官方库。上游指定的 Rust 1.54 未用于本轮构建：这里采用 Rust 1.99.0、锁定的 [Cargo.lock](FastPay-Cargo.lock) 和 `--cap-lints=allow`，以处理旧项目的 `deny(warnings)` 与新编译器警告。使用 release 优化构建；debug 符号保留。

主对照使用 **`receiver-confirmed`** 客户端：先调用官方 `get_spendable_amount` 并检查资金，再获得正常付款证书，随后完整执行官方收款核验和 quorum 同步。串行付款纪律保留，省去付款端的一次冗余同步阶段。这一组合经过独立源码审查；没有减掉收款验证或借用收款人的预置本金。计时器在资金检查之前重置，恢复 RPC 若发生也计入。

原始 `normal` 客户端（安全发送 API 再调用完整收款 API）的五轮数据另存于 [default-client-results](default-client-results/summary.json)，采用它时独立付款 p50 为 Next 4.12 ms、FastPay 7.90 ms。主结论使用前述更快的合法 FastPay 路径，两个模式的数据分别保存。

## 启动问题与验证

首次 `receiver-confirmed` 对照在第四轮 FastPay 启动阶段中止。原脚本把 `Listening` 日志当成就绪信号；相同配置重放十次时，一次捕获了 `Connection refused (os error 111)`。现改为确认四个 TCP 端口实际接受连接后才启动测量客户端。修正后的五轮主对照和三轮固定负载均完成；旧尝试与重放记录单独保留，不与主样本合并，见 [失败尝试记录](failed-readiness-results/records.json)、[重放日志](replay-failed-config.log) 和 [捕获的错误](readiness-failure.stderr)。该问题属于实验启动检查。

最终检查包括：12 项 Python 测量/清理测试、Next `go test -count=1 -tags=comet_v3 ./cmd/payctl`、FastPay 官方 45 项核心测试和 2 项网络测试、1 项原生计时测试。固定工具链构建脚本已实际执行，重建的 FastPay 二进制哈希与测量时一致。独立审查重新计算了主实验全部统计量，见 [审查记录](final-review.md)。

## 重跑与多机衔接

在项目根目录用 Python 执行 `third_party/cometbft/overlay.py` 生成依赖，再用 Go 1.27.1 为 Linux/amd64 编译 `committee`、`member`、`gateway`、`payctl`，带 `comet_v3` tag，放入 `.scratch/baseline-bin`。Windows 可设置 `GOOS=linux`、`GOARCH=amd64` 后交叉编译。

Linux/WSL 中运行：

```bash
task_repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
task_dir="$task_repo/docs/experiments/baseline-design-2026-10-13"
bash "$task_dir/build_fastpay.sh"
python3 "$task_dir/compare.py" \
  --next-bin "$task_repo/.scratch/baseline-bin" \
  --fastpay-bin "$HOME/.local/share/next-baseline-tools/fastpay-target/release" \
  --out "$HOME/next-fastpay-reproduction-fresh" \
  --rounds 5 --count 100 --lengths 1 10 100 --client-mode receiver-confirmed
python3 "$task_dir/compare.py" \
  --next-bin "$task_repo/.scratch/baseline-bin" \
  --fastpay-bin "$HOME/.local/share/next-baseline-tools/fastpay-target/release" \
  --out "$HOME/next-fastpay-load-reproduction-fresh" \
  --rounds 3 --count 30 --rates-only --rates 10 25 50 --client-mode receiver-confirmed
```

每次 `--out` 使用全新 Linux ext4 目录。公开采样与服务日志保存在 [主对照](validated-results/summary.json) 和 [固定负载](load-results/summary.json) 目录；生成的私钥与数据库保留在 WSL 本地运行目录，没有复制进公开实验材料。版本、二进制、源码和数据指纹见 [manifest.json](manifest.json)，最终检查见 [verification.json](verification.json)。

多机阶段沿用相同负载、收款终点和 JSON 汇总格式，把两系统的四名投票节点部署到相同的四台机器，匹配客户端位置与网络条件。Next 的委员会和 gateway 继续实际运行并计入资源。地址配置与远程服务启动是部署层需要接入的部分；若把付款和收款钱包也拆到不同机器，两系统都应计入证书交付和收款确认的通信时间。本轮完成的是单机对照，多机部署尚未执行。
