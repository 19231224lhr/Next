# Next 实验数据

整理日期：2026-10-19。这里集中保存本次发布的实验报告、逐轮数据、图表、运行证据、冻结版本信息及复现材料。各批数据的数值和统计口径保持原样，本次归档没有重新运行实验。

## 从哪里开始

- [C3 v2 结果报告](01-c3-v2/C3终稿实验结果-2026-10-16/RESULTS.zh-CN.md)
- [C3 v2 逐轮 CSV](01-c3-v2/C3终稿实验结果-2026-10-16/analysis/runs.csv)、[配对比较](01-c3-v2/C3终稿实验结果-2026-10-16/analysis/paired.csv)、[统计汇总](01-c3-v2/C3终稿实验结果-2026-10-16/analysis/summary.csv)
- [C3 v2 图表](01-c3-v2/C3终稿实验结果-2026-10-16/figures/)、[测量口径](01-c3-v2/C3终稿实验结果-2026-10-16/MEASUREMENT-NOTES.md)、[复现说明](01-c3-v2/C3终稿实验结果-2026-10-16/REPRODUCE.md)
- [历次结果总报告](00-overview/Next实验结果总报告-2026-10-16.md)（原文保留当时的本地检索路径；在线材料按下表定位）
- [全部归档与分片位置](ARCHIVE-INDEX.md)

## 分类目录

| 目录 | 内容 |
| --- | --- |
| [00-overview](00-overview/) | 总报告、文件来源对应表与发布剔除记录 |
| [01-c3-v2](01-c3-v2/) | 2026-10-16 终稿费用、连续链、FastPay 对照、混合负载、延迟、故障与拥堵实验；含冻结源码包 |
| [02-fastpay-baselines](02-fastpay-baselines/) | FastPay 单机复现、统一计时适配与运行时敏感性 |
| [03-wallet-preparation](03-wallet-preparation/) | 请求准备耗时剖析、钱包缓存与本地签名优化 |
| [04-same-organization](04-same-organization/) | 同组织付款、证书交付、资金池原型及对照数据 |
| [05-c3-v1-and-speed](05-c3-v1-and-speed/) | C3 v1 的费用实验及逐阶段性能优化记录 |
| [06-c1-c2-and-transition](06-c1-c2-and-transition/) | 连续付款等待对照、有限担保额度、评审补证和去 Intent 过渡实验 |
| [07-history-verification](07-history-verification/) | 历史授权、离线核验和读取成本实验 |

每个目录保留实验名称和日期。较早的已提交实验仍可在 [docs/experiments](../docs/experiments/) 查询。

## 原始数据与大文件

小型报告、CSV、配置和图表可直接打开。大量日志、逐笔报告和源码快照保存在各实验的 ZIP/TGZ 中；部分子目录打包为 `archives/artifacts.zip`，压缩包内保留原相对目录和逐文件清单。

超过 45 MiB 的归档以同名 `.parts/` 目录分片存放，所有分片均实际提交到 Git，无需 Git LFS。克隆仓库或下载仓库 ZIP 后，在仓库根目录执行：

```bash
python experiment-data/restore_archives.py --verify-only
python experiment-data/restore_archives.py
```

第一条命令验证文件与分片的 SHA-256；第二条还原完整归档并再次验证。恢复脚本只使用 Python 标准库，不会覆盖内容不同的已有文件。还原后按原实验说明解压、构建和运行。

`ARCHIVES.json` 记录分片顺序、归档大小及校验值；`FILE-MANIFEST.json` 记录本次发布的所有材料（清单自身除外）。[SOURCE-MAP.json](00-overview/SOURCE-MAP.json) 记录原始位置与发布位置。实测源码以各实验的冻结清单为准，当前 `main` 代码提交不替代历史构建标识。

## 发布范围

保留正常、失败与诊断运行的实验记录；实验运行私钥、数据库、可执行文件、缓存和 AI 对话材料未收入发布目录。[EXCLUDED.json](00-overview/EXCLUDED.json) 记录剔除原因。早期夹带运行私钥的证据包以 `.public.zip` 发布，包内附有新的公开导出清单；原始包仍在本地保留。冻结源码包中的既有公开测试密钥素材按原样保存，不是实验运行身份。
