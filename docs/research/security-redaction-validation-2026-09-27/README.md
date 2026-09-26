# R6 验证记录

对应[门限哈希与共识接口论证](../security-redaction-consensus-wire4.md)。代码基线 `security-analysis@d12e430`，本目录随修复提交保存；实验基线 `re@8d1590ba559eeb23548f2bf598f064f490553330` 未合并、未修改。运行环境 Windows amd64、Go 1.27.1；不是 Mac TPS 实验。

## 修复与证据

生产改动仅为 Comet overlay 的实时分片入口调用 `Part.ValidateOriginal`，要求原始分片 opening=1。密码学公式、门限、投票规则、业务费用和存储配置未改。

| 文件 | 命令／含义 | 结果 |
| --- | --- | --- |
| [red.log](red.log) | 保留新回归，临时把生成源码的入口恢复为旧标签检查，运行 TestConsensusRejectsRelabelledOpening | 按预期失败：旧入口接受重标记适配分片 |
| [green.log](green.log) | finally 恢复修复源码后重跑同一用例 | 通过 |
| [regression.log](regression.log) | `go test -count=1 -v -tags=comet_v3 -run 'TestPublishedAdaptationReuse\|TestConsensusRejects\|TestRealBlockStoreRewriteAndOriginalReplay' ./crypto/chameleon ./internal/redaction` | 通过 |
| [default.log](default.log) | `go test -count=1 ./...` | 通过 |
| [tagged.log](tagged.log) | `go test -count=1 -tags=comet_v3 ./...` | 通过 |
| [vet.log](vet.log) | `go vet -tags=comet_v3 ./...` | 通过；空日志表示无诊断 |
| [comet.log](comet.log) | `go test -count=1 -tags=comet_v3 github.com/cometbft/cometbft/types github.com/cometbft/cometbft/store` | 通过 |
| [consensus.log](consensus.log) | `go test -count=1 -tags=comet_v3 -run 'TestBlockPartMessageValidateBasic\|TestProposalBatch' github.com/cometbft/cometbft/consensus` | 通过；并非上游整个测试集 |
| [race.log](race.log) | `go test -race -count=1 -tags=comet_v3 ./crypto/chameleon ./internal/redaction` | 通过 |

red/green 只修改临时生成的共识入口，不改动跟踪源码，并在恢复后确认 green。它验证回归能识别旧逻辑，不表示重现了完整网络攻击。真实消息和原始追块断言随后补入同一回归文件；最终 tagged/race 日志包含这些补充。

微基准入口为 `go test -tags=comet_v3 -run '^$' -bench '^BenchmarkOriginalPartGate$' -benchmem -count=3 ./internal/redaction`。本轮输出：

| 检查 | 三次 ns/op | B/op／allocs/op |
| --- | --- | --- |
| 旧标签检查 | 1.294 / 1.327 / 1.313 | 0 / 0 |
| 原始 opening 检查 | 8.710 / 8.878 / 9.240 | 0 / 0 |

CPU 为 AMD Ryzen AI 9 HX 370。微基准隔离检查函数，不包含密码验证、网络、持久化或完整共识，不外推为系统吞吐提升／下降比例。源文件指纹见 [source-sha256.txt](source-sha256.txt)，与本目录同次提交的源码为最终可复查版本。
