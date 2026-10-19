# Next 终稿 C3 实验交付

先看 [结果报告](RESULTS.zh-CN.md)，再看 [实验完成表](COMPLETION.zh-CN.md) 和 [安全证据](EVIDENCE.zh-CN.md)。

- `analysis/`：逐轮 CSV、配对对照、费用状态与汇总。
- `figures/`：PDF/SVG 矢量图、300 dpi PNG，以及绘图检查记录；图题见 `FIGURE-CAPTIONS.md`。
- `raw-experimental-data.zip`：真实服务报告、运行配置、日志和失败记录，含逐文件 SHA-256 清单。排除了实验身份私钥与数据库；原始完整运行仍保存在 WSL。
- `measured-source-snapshots.zip`：修改前 baseline 与终稿 final 源码，以及各自的本地 CometBFT 依赖。包含已有公开测试用密钥素材，不包含实验钱包私钥。
- `source-delta.zip` / `.patch`：两份实际测量源码之间的修改，方便审查。
- `reproduction-scripts.zip`：保留仓库相对目录的实验、统计、绘图和 FastPay 构建脚本。绘图脚本的 `--audit-dir` 可指向包内 `figure-audit-tools/`。
- `provenance/`：构建与源码哈希、测试输出、环境、审查和 GPT 讨论记录。

复现方法见 `REPRODUCE.md`。先分别解压源码并构建，再按脚本指定新的运行目录；不要覆盖本轮原始数据。FastPay 官方提交、客户端适配器及单 worker 变体分别保留，不混为一个基线。

代码尚未自动提交或推送。实验验证不代替 F1–F6 的一般性数学证明。
