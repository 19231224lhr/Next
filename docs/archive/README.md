# 历史材料与迁移索引

> **历史材料，不是当前规范。** 这些文档保留早期规则、开发计划和决策依据，不用于覆盖当前 wire 4 实现。当前入口是[系统设计](../design/system.md)和[工程架构](../design/architecture.md)。

## 归档原则

旧规范与已完成开发过程统一归档；正式实验报告、失败对照、数据、配置、源码版本和复现工具保留。字段细节集中在 [reference](../reference/README.md)，实验方案与评审记录移到各自实验目录。

[历史工程定位索引](engineering-experiments.md)保留早期性能调查入口，数据和脚本仍在原实验路径。Git 历史可查询整理前版本 `918acd3`，不重写提交历史。

历史材料中的附件引用不保证都已收录：本轮检查发现，早期系统/架构草案，以及 consensus-review、consensus-tps、gateway-dispatch、member-runtime-tuning、stage-profile-100 报告中已有部分本地附件缺失。保留原引用以便追溯，不把未收录附件当作可复核证据。本轮迁移未新增失效链接；正式实验引用仍应以实际在库材料为准。

## 文档去向

| 整理前路径 | 当前位置 |
| --- | --- |
| `docs/design/utxo-fast-payment-system-design-final.md` | [utxo-fast-payment-system-design-final.md](design/utxo-fast-payment-system-design-final.md) |
| `docs/design/utxo-go-engineering-architecture-v1.0.md` | [utxo-go-engineering-architecture-v1.0.md](design/utxo-go-engineering-architecture-v1.0.md) |
| `docs/design/utxo-development-execution-plan-v1.0.md` | [utxo-development-execution-plan-v1.0.md](design/utxo-development-execution-plan-v1.0.md) |
| `docs/design/utxo-direct-liability-amendment-v1.2.md` | [utxo-direct-liability-amendment-v1.2.md](design/utxo-direct-liability-amendment-v1.2.md) |
| `docs/implementation-block-following-v4.md` | [implementation-block-following-v4.md](../reference/implementation-block-following-v4.md) |
| `docs/implementation-public-submission-v4.md` | [implementation-public-submission-v4.md](../reference/implementation-public-submission-v4.md) |
| `docs/implementation-owner-fuel-v4.md` | [implementation-owner-fuel-v4.md](../reference/implementation-owner-fuel-v4.md) |
| `docs/implementation-adaptive-reserve-v4.md` | [implementation-adaptive-reserve-v4.md](../reference/implementation-adaptive-reserve-v4.md) |
| `docs/block-following-plan.md` | [block-following-plan.md](development/block-following-plan.md) |
| `docs/committee-1000tps-review-2026-09-21.md` | [committee-1000tps-review-2026-09-21.md](development/committee-1000tps-review-2026-09-21.md) |
| `docs/committee-consensus-optimization-2026-09-21.md` | [committee-consensus-optimization-2026-09-21.md](development/committee-consensus-optimization-2026-09-21.md) |
| `docs/implementation-v1.2.md` | [implementation-v1.2.md](development/implementation-v1.2.md) |
| `docs/progress.md` | [progress.md](development/progress.md) |
| `docs/plans/2026-09-19-committee-submission.md` | [2026-09-19-committee-submission.md](development/2026-09-19-committee-submission.md) |
| `docs/research/adaptive-reserve-control-proposal-2026-09-23.md` | [adaptive-reserve-control-proposal-2026-09-23.md](../experiments/adaptive-reserve-2026-09-23/adaptive-reserve-control-proposal-2026-09-23.md) |
| `docs/research/adaptive-reserve-control-review-2026-09-23.json` | [adaptive-reserve-control-review-2026-09-23.json](../experiments/adaptive-reserve-2026-09-23/adaptive-reserve-control-review-2026-09-23.json) |
| `docs/research/adaptive-reserve-implementation-plan.md` | [adaptive-reserve-implementation-plan.md](../experiments/adaptive-reserve-2026-09-23/adaptive-reserve-implementation-plan.md) |
| `docs/research/continuous-respending-experiment-design-2026-09-22.md` | [continuous-respending-experiment-design-2026-09-22.md](../experiments/continuous-respending-2026-09-22/continuous-respending-experiment-design-2026-09-22.md) |
| `docs/research/e2-owner-fuel-plan-2026-09-23.md` | [e2-owner-fuel-plan-2026-09-23.md](../experiments/owner-fuel-2026-09-23/e2-owner-fuel-plan-2026-09-23.md) |
| `docs/research/e3-implementation-review-2026-09-23.json` | [e3-implementation-review-2026-09-23.json](../experiments/liability-repair-2026-09-23/e3-implementation-review-2026-09-23.json) |
| `docs/research/e3-liability-repair-experiment-design-2026-09-23.md` | [e3-liability-repair-experiment-design-2026-09-23.md](../experiments/liability-repair-2026-09-23/e3-liability-repair-experiment-design-2026-09-23.md) |
| `docs/research/e3-liability-repair-final-review-2026-09-23.json` | [e3-liability-repair-final-review-2026-09-23.json](../experiments/liability-repair-2026-09-23/e3-liability-repair-final-review-2026-09-23.json) |
| `docs/research/e3-liability-repair-review-2026-09-23.json` | [e3-liability-repair-review-2026-09-23.json](../experiments/liability-repair-2026-09-23/e3-liability-repair-review-2026-09-23.json) |
| `docs/research/e3-network-final-review-2026-09-23.json` | [e3-network-final-review-2026-09-23.json](../experiments/liability-repair-2026-09-23/e3-network-final-review-2026-09-23.json) |
| `docs/research/e3-network-preliminary-review-2026-09-23.json` | [e3-network-preliminary-review-2026-09-23.json](../experiments/liability-repair-2026-09-23/e3-network-preliminary-review-2026-09-23.json) |
| `docs/research/e4-fault-conflict-experiment-design-2026-09-23.md` | [e4-fault-conflict-experiment-design-2026-09-23.md](../experiments/fault-conflict-2026-09-23/e4-fault-conflict-experiment-design-2026-09-23.md) |
| `docs/research/e4-fault-conflict-review-2026-09-23.json` | [e4-fault-conflict-review-2026-09-23.json](../experiments/fault-conflict-2026-09-23/e4-fault-conflict-review-2026-09-23.json) |
| `docs/research/e4-implementation-review-2026-09-24.json` | [e4-implementation-review-2026-09-24.json](../experiments/fault-conflict-2026-09-23/e4-implementation-review-2026-09-24.json) |
| `docs/research/e4-result-review-2026-09-24.md` | [e4-result-review-2026-09-24.md](../experiments/fault-conflict-2026-09-23/e4-result-review-2026-09-24.md) |
| `docs/research/e5-delivery-gate-ablation-design-2026-09-24.md` | [e5-delivery-gate-ablation-design-2026-09-24.md](../experiments/delivery-gate-2026-09-24/e5-delivery-gate-ablation-design-2026-09-24.md) |
| `docs/research/e5-delivery-gate-review-2026-09-24.md` | [e5-delivery-gate-review-2026-09-24.md](../experiments/delivery-gate-2026-09-24/e5-delivery-gate-review-2026-09-24.md) |
| `docs/research/e7-cross-org-network-plan-2026-09-24.md` | [e7-cross-org-network-plan-2026-09-24.md](../experiments/cross-org-network-2026-09-24/e7-cross-org-network-plan-2026-09-24.md) |
| `docs/research/e8-workload-resource-plan-2026-09-24.md` | [e8-workload-resource-plan-2026-09-24.md](../experiments/workload-resource-2026-09-24/e8-workload-resource-plan-2026-09-24.md) |
| `docs/research/e8-workload-resource-review-2026-09-24.md` | [e8-workload-resource-review-2026-09-24.md](../experiments/workload-resource-2026-09-24/e8-workload-resource-review-2026-09-24.md) |
| `docs/research/finite-budget-experiment-design-2026-09-23.md` | [finite-budget-experiment-design-2026-09-23.md](../experiments/finite-budget-2026-09-23/finite-budget-experiment-design-2026-09-23.md) |
| `docs/research/lightning-comparison-plan-2026-09-24.md` | [lightning-comparison-plan-2026-09-24.md](../experiments/lightning-2026-09-24/lightning-comparison-plan-2026-09-24.md) |

## 本次清理边界

确认了重复的本地传输包、探针副本及 Python 缓存，并新增精确的忽略规则；删除操作被工具策略拦截，文件仍留在本地，未绕过限制。业务 Go 文件、测试、Comet 补丁、共识实验驱动、数据库审计探针及 Lightning 工具均保留。未移动实验数据目录，不修改原始 JSON/CSV/压缩数据与源码包内容。Mac 实验工作目录有未提交改动，本次未覆盖或清空。

与[GPT](https://chatgpt.com/c/6aa8b2d7-b6f8-83ec-8e23-ea9f3b634e90)核对后采用这一划分：当前规则、工程实现、实验事实与历史记录分开，避免误把历史设计或实测成功写成已证明的形式规范。
