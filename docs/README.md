# 文档导航

日常阅读按“系统设计 → 工程架构 → 运行验证 → 实验依据”进行。研究重点是便捷快速付款和真实续花。两份设计文档已从 wire 4 代码重新核对，明确了对象、状态、原子转移和证明边界，供下一步冻结形式模型使用。

| 文档 | 用途与状态 |
| --- | --- |
| [联合评审第①项：代码审查与执行边界](research/review-step1-2026-10-02/README.md) | 资金消费、来源补交、额度恢复、赔付防重及身份映射；规则—函数—正反测试对应 |
| [历史资金表示的读者契约](design/historical-reader-contract.md) | 固定已执行前缀、内部 Canonical 视图、决定与义务独立读取；逻辑／物理版本隔离 |
| [当前 C3 安全审查与证明更新](research/c3-security-review-2026-10-02/README.md) | `7a2517e` 来源补交、独立赔付决定、零经济效果表示及新增回归；当前入口 |
| [论文同步修改清单](research/c3-security-review-2026-10-02/paper-change-list.md) | 英／中文正文、算法、证明和实验的逐文件更新计划；PDF 尚未完成同步 |
| [系统设计与证明建模基线](design/system.md) | 身份与状态、完整付款及赔付流程、资金与权限公式、假设和待证明性质 |
| [授权历史修复增强设计](design/authorized-repair-batching.md) | 已实现的合批／直达机制背景；本分支新增经济与表示分离见[当前设计](design/evidence-driven-source-repair.md) |
| [工程架构与实现映射](design/architecture.md) | 关键函数、并发和存储边界、共识适配、现有回归测试与模型对应关系 |
| [完整安全性分析报告](research/security-analysis-complete-wire4.md) | 文献写法、系统与攻击者模型、潜在责任到真实资金的证明、源码审查、实验对应及剩余义务 |
| [论文第五章：安全性分析](paper/security-analysis-chapter5.md) | 可供论文采用的中文正文：模型、引理、定理、短证明、条件终结性与限制；附参考文献 |
| [本轮分析与回归证据](research/security-analysis-validation-2026-09-27/README.md) | 迟到批准反例、见证集合模型、经济投影回归、文献版本及核查记录 |
| [系统模型与条件安全论证](research/security-argument-wire4.md) | 论文模型、认证与唯一消费、动态预算到真实备付的推导、CAL/FUEL 记账及 E1–E8 证据映射；明确尚未完成的精化与密码学义务 |
| [组合安全论证与实现对应](research/security-composition-wire4.md) | 统一状态、一般参数条件归纳、关键写入口与提交边界；原 8 场景及本轮 2 场景扩展，区分手工推导和机械验证 |
| [门限哈希与共识接口论证](research/security-redaction-consensus-wire4.md) | 公开适配、公共授权与历史表示分层；原始分片绑定修复、回归和未完成的密码学义务 |
| [第一轮有限模型与实现验证](research/security-model-validation-2026-09-26.md) | 16 个有限配置、七类错误反例及账务投影原始日志 |
| [安全分析与最小修正计划](research/security-implementation-plan.md) | 已并入 `re` 的安全分析工作计划：模型、代码回归、修订接口审查及性能验收顺序 |
| [安全分析与证明准备](research/security-analysis-2026-09-26.md) | 条件安全论证、动态预算草图、已复现的补资配置缺陷及活性边界；尚非完整证明 |
| [运行与验证](operations.md) | 构建、初始化、单笔运行、配置和检查 |
| [E1–E8 实验索引](experiments/README.md) | 正式结果、复现材料和适用范围；原始数据保留 |
| [安全论证调研](research/fast-payment-security-proof-survey-2026-09-25.md) | 相关论文、证明对象和建议路线；没有宣称证明完成 |
| [字段与实现参考](reference/README.md) | 按块处理、公共封装、用户自付 FUEL 和 CAL 补资细节 |
| [历史材料](archive/README.md) | 已被修订的规范、开发过程与性能定位；不作当前规范 |

## 文档职责

系统设计负责说明现行语义；架构负责说明代码如何承担这些职责；实验报告负责数字和证据。版本细节留在 reference，已经完成的实验方案及讨论随对应实验保存，不再占用 research 主入口。

旧文档中“终稿”“v1.0”等名称不表示其内容仍覆盖当前实现。archive 明确保存历史语义，当前 wire 4 已取消的根责任、逐笔委员会回执等不能因旧文存在而重新混入模型。存在冲突时先核对代码和版本，不靠日期自动认定某个未实现计划已经完成。

## 下一阶段

当前分支以 [C3 安全审查](research/c3-security-review-2026-10-02/README.md)和[来源回收论证](research/reviewer-revision-2026-10-02/security-recovery.md)衔接旧阶段报告；后者保留版本与原始证据。按[论文修改清单](research/c3-security-review-2026-10-02/paper-change-list.md)完成英／中文同步、编译和复核后再合并 `re`。历史安全修正已并入 `re`，不代表当前 C3 补强也已合并；测试通过不等于全实现机械证明。
