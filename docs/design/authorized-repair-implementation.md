# 授权合并修复实现计划

基线：`re@0f7be69`；工作分支：`feat/authorized-repair-batching`。按[已确认设计](authorized-repair-batching.md)在当前会话顺序执行，不委派子任务。

目标：保留现有逐义务偿付语义，实现同块合批、逐项认证结果、已提交版本直达物化及有界调度。技术栈沿用 Go、当前状态 Overlay 和修改后的 CometBFT；不新增服务、数据库或普通快付往返。

## A. 已提交版本安装

- [x] 在 `internal/redaction/materialize_test.go` 补延迟物化、旧任务重放、完整原始块保留测试，先确认当前版本拒绝跳跃。
- [x] 在 `third_party/cometbft/store/redaction.go` 增加显式授权安装入口，保留顺序 ReviseBlock 语义，统一验证完整块身份、分片和版本。
- [x] 在 `internal/redaction` 复制已提交安装材料；`cmd/committee/repair_runtime.go` 在释放应用视图后物化，逐项推进原队列游标。
- [x] 运行 `go test -tags=comet_v3 ./internal/redaction`，保留严格安装入口并随完整功能统一提交。

## B. 合批协议与经济执行

- [x] 新增 `protocol/repair_batch.go` 及 codec 测试：规范排序、双重去重、大小限制、全字节身份、结果逐项对应。
- [x] 新增 `internal/redaction/batch.go`：按同一规范正文累计修改多个槽，逐项复用当前赔付规则，在同一 Overlay 原子提交。
- [x] 集成 `internal/committee/repair.go` 与命令分发；新旧命令共用义务终态，批重投返回无新增效果。
- [x] 在付款修复 fixture 上覆盖双缺口、共享账户/费用、中途失败、父抢先、闭合后重组拒绝、双入口防重、后继授权及原始重放。

## C. 跟块与运行调度

- [x] 成员和钱包识别批结果，逐项赔付效果与顶层费用输出各处理一次；失败/幂等结果不更新额度。
- [x] 委员会修复 worker 接入有界同块组批、输入适配缓存、最终正文分片取证、有限租期与原字节重试；单项不等凑批。
- [x] 验证正常来源关闭与批修复竞争、队列 A1/B1/A2、不回退及连续游标。

## D. 验证与报告

- [x] 执行普通全项目测试、`go test -race -tags=comet_v3 ./...`、共识身份回归、vet 与 build。
- [x] 运行相同经济工作量下的单项/合批/直达成本对照，至少包含 k=1、同块聚集与分散负对照；只报告实际观测指标。
- [x] 更新设计实现状态、试验结论和复现命令。未验证的吞吐收益不写成事实；保留原实验基线。

结果和准确范围见[实现报告](../experiments/authorized-repair-2026-09-29/README.md)。有界工作循环替代了讨论中的异步构造方案，避免不必要的 generation 管理；完整扩展性能矩阵另列后续，未作为本次实现完成条件。

## E. 审查缺陷修复

- [x] 隔离第四个空、重复编号、错误数值或失联响应；只在三个不同编号份额真正验证成功后提前返回。
- [x] 对输入、单项分片、合批分片共用严格门限；跨分片失败候选不污染后续候选。
- [x] 取证前按规范顺序累计试算，选择可支付项目；最终候选集合再次试算；签署服务独立拒绝无效固定批。
- [x] 保留公共执行的最新状态复核和原子赔付，不把预检变成真实扣款。
- [x] 完成回归、race、vet/build 和 Mac 真实闭环。证据与适用范围见[修复报告](../experiments/authorized-repair-2026-09-29/fix-report.md)。
