# 授权合批修复：审查缺陷修正与回归

工作分支：`feat/authorized-repair-batching`。修正基线：`3bb9ceb8838839d3ded9b4e44a08648d577f4d94`；代码修复提交：`f82f8cd`。[历史审查](../../design/authorized-repair-code-review-2026-09-29.md)中的 R1、R2 均已修复，尚未合入 `re`。

## 1. 修复内容

| 问题 | 最终实现 | 验证 |
| --- | --- | --- |
| 第四个空、重复编号或错误份额使三个有效响应无法完成修复 | 固定委员端点绑定份额编号；三份候选分别验证，坏候选不否决其他候选；密码学验证通过后立即返回 | 空行、短行、重复编号、错误数值先到均不阻断三个诚实响应；仅两个诚实响应拒绝 |
| 已有三个有效响应仍等待第四个请求超时 | 收集器使用子 context，完成有效门限后取消其他请求 | HTTP 失联测试在 1 秒整体 deadline 内成功，未等待 2 秒请求超时 |
| 已知资金不足的批仍发起昂贵取证 | RPC 前按规范顺序在临时 Overlay 累计试算，选择可支付项目；最终实际集合再次试算；各份额服务独立试算 | 40+60 CAL 缺口对 99 CAL 余额时，只选择可支付前项；完整 100 CAL 批在 BuildBatch 和份额服务处均拒绝，真实余额仍为 99 |
| 跨分片失败候选可能污染重试 | 每个三行候选使用独立临时 openings，必须通过所有目标分片才返回 | 坏候选第一分片有效、第二分片无效时，不发布部分结果；改用三个诚实行后成功 |

以上保留固定 3-of-4 门限、原赔付规则、公共最新状态复核及原子提交。预执行不扣真实资金，也不构成后续执行保证；父来源到达、资金变化或 Base 变化仍可使候选需要重建。未修改普通付款取证、费用、签名及共识参数。

代码入口：`cmd/committee/repair_collect.go`、`repair_batch_runtime.go`；`internal/redaction/preflight.go`、`batch.go`、`repair.go`；`crypto/chameleon/chameleon.go`、`keys.go`。使用已有 Overlay 和赔付函数，未增加服务、数据库或密码学协议。

## 2. 回归证据

先复现原缺陷，再加入正式回归；最终候选资金复核也先观察到新断言失败，再补实现并通过。[定向测试原始输出](fix-final-results/fix-regressions.txt)覆盖真实门限 RSA 与实际 HTTP 收集器。

新增跨分片 fixture 把两个来源槽放在不同的真实 65,536 字节区块分片，继续核对逐项/合批经济投影、完整 BlockID、后继付款和逐高度原始重放。原有“取证后余额变化导致公共执行原子拒绝”测试保留，防止用预检替代最终检查。

最终源码执行并通过：

```sh
go test -count=1 ./...
go test -count=1 -race -tags=comet_v3 ./...
go vet -tags=comet_v3 ./...
go build -tags=comet_v3 ./...
go test github.com/cometbft/cometbft/consensus -run 'TestStableHashIdentity|TestState|TestProposalBatch' -count=1
go test github.com/cometbft/cometbft/store -run 'Test.*(Revis|Redact|Original|Install)' -count=1
```

Mac 另执行 `go test -count=1 -race -tags=comet_v3 ./cmd/committee ./crypto/chameleon ./internal/redaction` 后构建真实节点，输出见[运行日志](fix-final-results/smoke-fix-final-run.log)。历史 `review-reproduce.py` 仅用于干净的被审查版本，不作为修复后回归；当前应运行上述正式测试。

## 3. Mac Studio 最终闭环

M4 Max、16 核、64 GB，14 个同机节点：四委员、两组织各四成员与一个网关。新创世，使用既有磁盘存储实验配置，Flush/Gossip 各 10 ms，完整验签及真实门限修复保留。正常压测钱包启用 `wallet-no-sync`。沿用组织代付 demo fixture，不将其作为用户自付费用的新实验。

先并发运行四个扣住父来源的两跳案例，触发到期赔付，再处理迟到父；随后输入 100 TPS、1,000 笔普通快速付款，停止节点后离线审计。[驱动脚本](smoke-fix-final.py)须在独立源码副本、空实验目录中运行。

| 指标 | 最新结果 |
| --- | ---: |
| 实际公共修复批 | 高度 2、Base 0，四项，1,568 字节 |
| 四委员批身份 | 同一个 BatchID |
| 四笔缺口修复、迟到父处理 | 全部成功 |
| 正常快速付款 | 1,000/1,000，失败 0、漏发 0、最终未完成 0 |
| 付款钱包发送至快速到账 P50 / P95 | 1.048 / 26.531 ms |
| 正常负载输入及全部收尾 | 11.120 秒 |
| 计划发送滞后 P95 | 0.480 ms |
| 四委员付款 / 已关闭付款 | 各 1,008 / 1,008 |
| 缺口余额、所有节点待投递记录 | 全部 0 |
| 四委员 StateHash | 完全一致 |
| 每委员历史修订高度数 | 1 |

证据：[批记录](fix-final-results/batches.txt)、[压测汇总](fix-final-results/bench-summary.json)、[逐笔压缩记录](fix-final-results/bench-v4-100.json.gz)、[审计](fix-final-results/audit.json)、[二进制摘要](fix-final-results/binary-sha256.txt)。四个 demo 原始结果为同目录 `direct-0.json` 至 `direct-3.json`。本次测试节点已由驱动停止。

## 4. 结论与边界

R1 的单异常响应阻塞和 R2 的已知无效经济取证已修复，跨分片回归与真实支付闭环通过。异常响应注入在实际 HTTP 收集器及密码学回归层完成；14 节点网络轮验证的是正常委员下的到期赔付集成，不能把二者描述成完整网络拜占庭故障实验。

侧边栏 GPT 的复核促成了两处补充：最终实际集合重新试算，以及坏候选在第二分片失败时的结果隔离回归。结论以代码和上述测试为依据。进度仍要求三个诚实委员能就同一合法目标完成响应、资金足够且公共执行持续推进；不保证任意竞争下的硬完成期限。

提交前已向[同一 GPT 对话](https://chatgpt.com/c/6aa8b2d7-b6f8-83ec-8e23-ea9f3b634e90)反馈最终改动与测试。它认可 R1/R2 可按本轮范围结束，并明确未自行运行最新源码；其复核保留“有效门限下的可用性、预检不等于资金预留、局部故障注入与网络闭环分开表述”三条边界。以上边界已纳入报告，不以模型认可代替实测。

本轮是正确性和可用性修复，没有成对吞吐消融；不能由上述延迟宣称性能提高或完全无影响，也不把有限回归等同于全实现安全证明。原 E1–E8 与前期成本实验成绩未改写。
