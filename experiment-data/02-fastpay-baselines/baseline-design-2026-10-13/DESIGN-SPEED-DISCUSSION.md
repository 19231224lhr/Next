# 为什么单机结果没有大幅领先 FastPay

2026-10-13，与侧边栏 GPT 讨论三轮，并重新核对原始样本、代码启动路径、此前同组实验和 FastPay 原论文。这里记录对既有实验的解释，不修改采样或付款协议。

## 已确认的三件事

**比较对象本身已经很快。** FastPay 同样不等待普通区块链的逐笔共识，其论文第 4.1 节说明形成付款证书仅需一次 quorum 往返，并区分证书确认与随后更新余额的 settlement。因此，Next 相对逐笔等公开执行的旧内部加速比例，不能变成对 FastPay 的预期领先比例。[原论文](https://sonnino.com/papers/fastpay.pdf)

我们当前测量的官方 FastPay 客户端，在完整收款路径中还做账户查询、收票、收款侧查询和按需证书确认；Next 钱包接受认证输出时不等待委员会执行。这是两个实现的可续付路径差别，不能将 FastPay SDK 的四个顺序阶段写成其协议形成证书的最低轮数。同机通信成本低，少一些顺序通信未必带来数量级加速；较大 RTT 下的收益仍需测量。

**Next 的收款阶段确实更快，准备下一笔的成本却更高。** 按同一轮逐项对照，五轮 100 笔链中，Next 从网络发送到钱包接受的累计时间均比 FastPay 短 84.86–123.38 ms；后续 99 笔请求准备又多花了 71.95–101.90 ms。这个相抵来自每轮实际累计值，不是用单笔 p50 乘以 100。后续准备累计的跨轮中位数分别为 95.56 ms 与 4.98 ms。

`runChain` 的准备阶段包括来源读取/整理、`NewFastTx` 的资金承诺、所有者签名、`SaveDirectRequest` 的消费锁与请求保存，以及外层编码。保存函数内部也进行编码。可以确认这些操作存在，但还没有本轮子阶段计时，不能把差距全部归因于 CH、数据库、验签或后台委员会。

**本轮没有启用全部可选同组优化。** 网关的 `--same-org-continuation`、`--compact-direct-approvals` 和成员的 `--same-org-continuation` 均默认 false。`payctl runLab` 实际只传 `-config`；本轮适配脚本也未添加其他服务参数。因此，原成绩代表当前代码的默认普通 Direct 配置，通用代码优化仍在，三项可选开关未启用，Pool 未参与。这是配置覆盖的遗漏，不能把默认成绩称作启用已有优化后的最佳成绩。

此前 [308/309 同组对照](../same-org-design-2026-10-12/REPORT.zh-CN.md) 已发现：请求/响应明显缩小，受控低带宽下收益更清楚，本机低传输成本下没有稳定的延迟收益。因此开关需要补测，但现有证据不支持直接把长链打平归咎于开关关闭，也不支持预测打开后必然大幅领先。

## Codex 与 GPT 收敛后的下一步

1. 先补同语义三组对照：默认 Direct、三个开关实际启用的 Direct、当前合法 FastPay 客户端。先测 100 笔独立付款和 100 笔依赖链，保存真实成员/网关启动命令与生效记录；原样本保留。
2. 对 Next 请求准备阶段逐项计时，区分必要工作、重复编码、存储排队及其他处理。保持原成功标准和验证逻辑。
3. 再做少量相同通信边条件的受控 RTT 对照，核对实际 RPC 阶段，验证续付路径少同步的收益。它是受控网络实验，随后才能由真实多机部署补充。

Pool 是另一种入池、组内转移、退出的付款模式，资金复用与当前成功/恢复条件应单列；不直接替换普通 Direct 的成绩，也不预判一定更快。

判断：用户关于设计优势可能被实现开销掩盖的直觉有依据。现有数据已经显示收款阶段的优势，以及额外请求准备对总时间的抵消；可选优化未开启也已确认。可以继续争取更好的实现表现，尚不能预设对同属快速证书体系的 FastPay 会取得数量级领先。

## 核对入口

- [主实验原始统计](validated-results/summary.json) 与 [逐轮构造记录](chain-construction.jsonl)
- 启动逻辑：`cmd/payctl/lab.go` 的 `runLab`
- 开关默认值：`cmd/member/main.go`、`cmd/gateway/main.go`
- 准备操作：`cmd/payctl/direct_chain.go` 的 `runChain`、`protocol/v3.go` 的 `NewFastTx`、`internal/wallet/direct.go` 的 `SaveDirectRequest`
- FastPay 接收路径：官方 `fastpay_core/src/client.rs` 的 `receive_from_fastpay` / `communicate_transfers`
- [侧边栏 GPT 讨论](https://chatgpt.com/g/g-p-6a97957ba6e88191b1e922255a89ceeb-utxodan-bao-zu-zhi-mo-xing-kuai-su-zhuan-zhang-akan/c/6acbc870-be1c-83ec-b253-9f1277f79ac5)
