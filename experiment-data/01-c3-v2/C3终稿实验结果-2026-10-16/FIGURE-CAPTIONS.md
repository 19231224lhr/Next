# 图题与正文使用建议

所有图从 `analysis/` 的实际逐轮数据生成。PDF/SVG 为矢量，PNG 为 300 dpi 预览。独立服务运行是重复单位；图中的同一轮多笔付款先汇总，再比较各轮。

## A. 连续付款性能（优先进入正文）

**Continuous-payment completion at the receiver-verified receipt endpoint.** (a) Chains of 100 dependent payments. (b) Chains of 1,000 dependent payments. Each point is one fresh run; horizontal marks and vertical lines show the median and full range across seven runs. Chain time includes preparation of subsequent requests. Public execution and member closure are reported separately.

这张图回答“费用保障加入后，整条连续付款链有多快”。正文应同时交代相对 No C3 的额外开销，以及官方和调优 FastPay 的不同结果。不能以短链优势替代长链结论。

## B. 来源延迟与恢复

**Settlement outcomes under delayed source submission.** Each run contains 20 source–successor pairs. (a) Mean numbers of normally fulfilled and compensated-then-recovered pairs; dots show the normal-fulfillment count in each run. (b) The within-run median closure offset from the workload start, with the median and full range across five runs. The x-axis denotes the requested source-submission delay, not the duration of a missing input on the public ledger.

这张图回答“来源迟到是否导致赔付，以及迟到来源能否随后清偿”。运行间赔付比例的变化保留在点图中；不要把请求延迟当作确定的执行顺序。

## C. 费用保障（优先进入正文）

**Fee accounting across three execution orders.** Each run executes the same source–successor pair with a signed service fee of 2 CAL per payment. (a) Total user CAL fees; users spend no FUEL. (b) Issuer FUEL expenditure, split into fixed committee rewards and timeout-fee redistribution. Accounting is identical across all five runs of each order. CAL and FUEL are shown separately.

这张图直接展示用户成本确定与超时费用归责：正常乱序不增加费用，赔付和偿还增加签发方支出，但不增加委员会报酬。预算初值、释放及最终 Held 放入费用表，避免把存量与累计流量堆在同一柱中。

## D. 拥堵下的优先与保留处理

**Public inclusion under congestion with two reserved-byte settings.** A 32 KiB block limit is used with 60 base/priority payments and four source-recovery submissions. Each point is a run-level median from first actual HTTP submission to verified block observation. Marks and lines show the median and full range across five runs. The reserved-byte shares are 0.01% and 25%; unused reserved space may be reused.

这张图比较基础档、付费优先档和来源补交的实际等待。0.01% 是近零保留配置，不能写成关闭。时延包含区块达成和客户端观察，不能称为纯调度器耗时。

## S. 受控 RTT（补充图）

**Dependent-payment chains under controlled proxy delays.** The configured round-trip delay is applied per proxied link; even the 0 ms control traverses the proxy. Curves show medians and whiskers show full ranges across five fresh 100-payment runs per condition. The endpoint is the last receiver-verified receipt. No bandwidth limit is imposed.

这张图检查通信路径对延迟的敏感性。它不代表真实广域网，也不替代本机无代理对照。FastPay 的收款确认路径与证书形成的一次往返应分开解释。
