# 字段与实现参考

这些文档保留现行字段和工程修订细节。首次阅读请先看[系统设计](../design/system.md)与[架构](../design/architecture.md)，无需逐份按日期拼接规则。

| 文档 | 保留原因 |
| --- | --- |
| [公共提交与输出凭证分离](implementation-public-submission-v4.md) | 405/406 对象、组织消费授权、静态验证缓存与公共登记边界 |
| [用户自付 FUEL](implementation-owner-fuel-v4.md) | 费用输入、找零、退款身份、tag 411 和资源口径 |
| [按块处理](implementation-block-following-v4.md) | 结果认证、原子跟块、累计核销与普通区块读取 |
| [CAL 增量授权](implementation-adaptive-reserve-v4.md) | tag 420、真实补资、成员容量增量及 E2 控制器 |

文档中的旧分支名、当时同步存储默认值及历史性能数字反映修订发生时的环境；当前配置以[运行指南](../operations.md)和具体实验为准。原文留存以避免丢失字段级细节，不意味着其中所有旧验收计划已实现。
