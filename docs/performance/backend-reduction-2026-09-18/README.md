# 后台减负实现与实验（2026-09-18）

已实现同笔核销合并、进程内证明验证去重、INSTALL 传播提示、入队防重，以及最多四笔后台并行。没有改变协议、委员会参数、签名检查或同步持久化要求。

## 配对结果

Mac Studio M4 Max，14 个独立进程，两个四成员组织、四委员、两个网关。每轮独立创世、随机实验身份；8 条跨组织链，每链固定 16 笔，共 128 笔。所有版本使用相同的新版测量逻辑，关闭诊断埋点，发送刷新与 gossip 均为 10 ms。基线代码为 a8e2bbf930f5cda40b28fa3679c42ae44394f3ac。

| 版本 | 两轮全程（秒） | 平均全程（秒） | 两轮钱包 READY 中位数（ms） |
|---|---|---|---|
| 原串行后台 | 23.096 / 23.690 | 23.393 | 193.7 / 206.8 |
| 减负，仍串行 | 12.597 / 12.072 | 12.335 | 207.7 / 205.4 |
| 减负，两路并行 | 11.771 / 12.198 | 11.985 | 238.8 / 217.2 |
| 减负，四路并行 | 10.274 / 10.416 | 10.345 | 252.3 / 273.8 |

“全程”从负载生成开始，到全部交易的公共证明取得验证及发行组织成员核销均被观察到为止，包括交易准备、发送、轮询和观察器排队。它不是单笔结算延迟，也不是所有数据库 outbox 精确归零的时刻。停机后的独立审计确认本次九轮实验的全部 outbox 均为零。

只减负平均缩短 47.3%；加四路并行相对原版缩短 55.8%，相对减负串行再缩短 16.1%。这组结果支持先减少每笔工作量，再重叠等待。它不能单独归因于某一个微优化；物理 fsync 次数和各类请求次数未在此次关闭埋点的实验中逐项统计。

四路并行的前台 READY 比串行减负有所增加，说明后台并发有资源争用成本；目前默认四路，优先处理后台排空问题。两路的后台收益仅约 2.8%，并没有同时取得最佳前台与后台结果。不能把四路并行表述成所有指标都改善。

## 实际改动

- Member.ApplyReceipts 在成员自身 Trust 下验证一组证明，使用同一个 Overlay 更新原占用的 FUEL、Policy、Execution、Bytes，保存已验证的 Custody 原始字节，并按原收尾条件处理 outbox。正常四项齐全路径由四次核销 Update 加一次出队，合成一次逻辑 Update；所有修改仍同步提交。事务内继续检查原 Approval、额度和累计差额，实际 94 FUEL 成本不会恢复成可用额度。
- 单证明 HTTP 入口仍完整验真，复用同一个私有账务应用函数。成员 relay 不再先验一遍，再让 ApplyProof 验第二遍；网关、钱包仍按自身信任配置验证。没有增加可绕过验证的 HTTP 接口。
- 已取得的有效部分证明可以一起提交；缺项继续保留待办。无效证明拒绝批次，不能导致补投永久停止；普通重试复用原封装，长期无进展补投仍保留。
- INSTALL 的远端成功提示只存在内存，不新增逐 ACK 写盘；未成功目标间隔重试，重启允许幂等重发。提示不返额、不授权出队。成员首次安装保留已有投递进度，写事务内重查已安装状态；再次引用父凭证也不覆盖原重试状态或重开已具备公共保管替代的待办。
- 后台沿用原有扫描，每组最多四个不同条目，等本组收尾再前进；同一条目不会与下一轮自身重叠，取消时等待本组退出。没有新增调度框架或依赖。
- bench 的证明观察与核销观察拆成独立有界工作池。CSV 增加 proof_queue_us、credit_queue_us，表示钱包 READY 至该观察任务开始的等待；Proof/Closed 总时延仍包含对应等待及轮询，不能冒充精确提交时刻。新增 transactions-per-lane 固定负载参数。

本轮仍查询原四项证明；按持有者角色只查询必要证据、同一已提交区块证明复用、前后台持久化竞争调度均未混入本补丁。

## 持续输入与账务检查

额外运行 30 秒、8 条闭环链，完成 454 笔，零失败；生成结束 30.405 秒，全程 33.719 秒，停止生成后约 3.314 秒完成观察排空。READY 中位数 228.1 ms。前 30 秒观察到 READY 446 笔、公共证明 388 笔、核销完成 385 笔。

每条链仍在每 16 笔后等待最终锚定，观察队列也有界；这不是固定速率开环压测，不能证明最大 TPS 或长期任意负载不积压。当前结果只证明在这组有限闭环负载下能够继续周转并在停流后排空。

九轮均通过 payctl audit：CAL/FUEL 守恒、费用托管及原占用核对。四委员的账务数量一致；固定负载每轮均有 128 笔付款、128 笔结清，每笔实收 94 FUEL、退款 6 FUEL。原始记录在 raw/，逐轮概览在 summary.json。

## 验证与复现

通过 go test -race ./...、go vet ./...、四节点真实 CometBFT 集成测试，以及全部五个命令构建。新增回归覆盖共享 Overlay 不覆盖四项返额、缺项不出队、无效批次不部分提交、响应丢失重试不重复返额、并发/首次 INSTALL 保留进度、重复父引用不重置待办、INSTALL ACK 不替代结算、工作并行有界及取消等待。保留的旧测试同时检查非原占用者不获额度、伪造证明不停掉重试和同封装补投。

在 Mac 仓库根目录执行（实验名称需新建，24000 系列端口空闲）：

```sh
mkdir -p .scratch
cp docs/performance/backend-reduction-2026-09-18/audit.go.txt .scratch/audit-fresh.go
python3 docs/performance/backend-reduction-2026-09-18/build.py baseline
python3 docs/performance/backend-reduction-2026-09-18/build.py reduced
python3 docs/performance/backend-reduction-2026-09-18/build.py parallel
python3 docs/performance/backend-reduction-2026-09-18/run.py compare-base baseline
python3 docs/performance/backend-reduction-2026-09-18/run.py compare-reduced reduced
python3 docs/performance/backend-reduction-2026-09-18/run.py compare-parallel parallel
python3 docs/performance/backend-reduction-2026-09-18/run.py compare-continuous parallel 0
```

build.py 用 Go overlay 构建基线和对照，不改工作区源文件。reduced 仅将四路 Run 换回原串行 Run，其他减负一致；dual 为两路对照。原实验运行顺序：baseline、reduced、reduced、baseline、parallel、parallel、dual、dual、continuous。重复次数有限，报告保留每次原始数据，不声称统计显著性。
