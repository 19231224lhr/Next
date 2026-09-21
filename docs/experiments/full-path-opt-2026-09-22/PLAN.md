# 完整快速转账优化执行记录

目标：五小时内按证据推进，力争持续500 TPS，保留输入防双花、验签、预算原子约束；实验无需崩溃恢复。实际发送不足目标必须报告；不增加并发上限掩盖积压。

基线：single-vs-full500-stages-2026-09-22/stage500：90000 /195.560s发送=460.212TPS；89957成功/43成员限流；快P50/P95=80.02/703.16ms，钱包块2355.85ms，成员完成2491.06ms，四委员同状态/outbox0。

工作循环：1.先诊断成员状态写者：逐类回调、bbolt实际提交、运行时等待；2.结合GPT评审最小修改；3.小范围单变量60s500TPS比较；4.有收益再180s持续与不同速率确认；5.记录失败分支，推送通过校验的版本。若数轮无收益，改查实测剩余瓶颈而非堆叠参数。

所有运行串行执行，不在压测期间编译或运行其他测试。每轮新实验目录；旧源码快照baseline-source.tar.gz保全，不执行git reset/clean。

## H1（待诊断）
成员Group写者被跟块大回调或bbolt写入拖住，使前台请求排队。现有234ms P95只是等待跨度，不能直接归因为fsync（NoSync）。新增诊断只在一个签发成员启用，记录回调归属、执行时间和bbolt提交分解；正常对照不启用。

诊断60s：单成员Group总回调5.16s（签发2.823s、跟块2.042s），bbolt提交18.132s，含WriteTime12.645s、其他4.139s；慢提交最大509ms、跟块回调最大67ms。源码证实NoSync不关闭扩容Sync。H1a先单变量NoGrowSync:noSync，正常同步模式保留；扩容/干净重开测试先红后绿，store race通过。A/B各30000/500TPS，关闭详细全局诊断保留1%前台埋点。

## 后续候选（尚未实施）
- H2：DirectStatus仅判断Approval是否存在，改为Get+ErrNotFound而非完整JSON解码；保留Observed/Closed条件和读错误。独立测试HTTP状态成本和500TPS性能。
- H3：诊断慢批write_count约600~1200、page_bytes8~21MB。Mac默认bbolt页为16KB，可能存在随机更新写放大；若H1后实际Write仍主导，可独立比较实验成员4KB页，不能预判一定改善，也不同时切内存Store。
- 不改业务验证，不盲目调大128/256/2048，不把等待移到新队列冒充吞吐改善。

GPT第一轮独立评审确认NoSync不包含grow Sync，建议提交主导时查grow/remap/write，回调主导时才考虑事务内Slice复用（不能跨事务缓存可变额度）。当前先小A/B只改NoGrowSync；已发送实际诊断结果和DirectStatus候选待复审。

H1a短A/B：growA实际483.71TPS/17失败/快P95=487.09ms；growB实际475.23TPS/0失败/快P95271.99ms。仅支持改善尾延迟与本轮失败，不支持吞吐提升，暂保留实验NoSync完整语义，后续持续复验。
H2已实现DirectStatus存在性检查；微基准与状态/错误传播测试，以及member/blockfollow/store race通过。statusB与growB比较，仅此一项业务读路径变化。

GPT第二轮确认两项顺序与最小边界。NoGrowSync也关闭Truncate预分配，不能把全部收益当fsync；只验证当前Mac实验环境，不当通用平台配置结论。DirectStatus减少JSON但仍复制Get字节，且不再附带校验Approval JSON损坏；其他读错误继续传播。4KiB页需新库与真实字节/耗时对照。

H2短对照：statusB 30000全成功，486.07TPS/快P50=43.559ms/P95216.17ms；比growB少重复解码，正式180s仍待验证。H3仅新实验NoSync成员4KiB页（正常store native页不变），页大小/17MiB扩容/clean reopen和store race测试通过。NoGrowSync限定Darwin，避免ext3/ext4不支持；Mac A/B效果不受此条件影响。

页测试首次红轮因关闭后读Info产生测试自身panic，已修正为关闭前读取；同修正测试对16KiB旧代码明确失败（got16384 want4096），4KiB代码通过，不将测试panic当有效红证据。

H3短测：4KiB实际486.30TPS/0失败/快P95=201.00ms，16KiB对应486.07TPS/0失败/216.17ms。吞吐几乎无差异，不能凭一轮小幅尾延迟就定案；开始16KiB/4KiB各90000持续对照，其余均保留H1a/H2。

## H4（待profile）
page4B稳定阶段CPU计数：gateway0约2.09核，签发成员各0.73~1.07核，payctl0.56核。网关runDirect每次worker完成重启分页扫描，两lane各取16条，记录即使cooldown或全安装ACK仍先JSON反序列化整个Outbox；一个扫描页填满4个动作后仍解码剩余记录。优先验证CPU profile是否集中在refill/JSON，候选只是在解码前根据key+已有busy/cooldown/install提示跳过不需处理的记录，并在动作槽满时停扫、保留cursor。仍在真正动作执行时重读终态与验证完整证据。不先加新持久队列/新数据库/全局免验签缓存。

H3 rejected: three-minute 16KB/4KB actual send TPS 464.42/462.89, fast P95 287.58/324.05ms. Restore native page size; keep H1/H2. Gateway diagnostic: serialization performs repeated authorization verification; scheduler scan is 6% CPU, not dominant. Existing Memory.Scan walks whole state map, so direct swap is not yet justified.

H4: encode DirectRequest once in collector, share immutable bytes with transport clients through optional byte-send interface; legacy/in-process clients retain typed path. Keep decoding/authorization at member, signature/quorum verification at collector, cancellation and fanout count. Test all four receive identical backing bytes and malformed owner auth never fans out; compare frozen 500TPS before/after. No protocol encoding changes.

H5: member HTTP forwards owned raw body into a single decoding/authentication entry. Typed API still marshals then decodes to own caller data. Retain all initial commitment, input, quota and signer checks. H4 short A/B inconclusive for latency (P95 252/363ms) despite lower encode work; do not claim throughput improvement.

H6: fixed 1024-entry success-only LRU of full ReceiveDescriptor values including Signature. Validate expected network, wire, route before lookup. Ed25519 verification outside cache lock, no singleflight. No dynamic state cached. Workloads must report descriptor reuse; cold/churn microbenchmark included. H5 short result 30000 success, actual 488.43 TPS, fast P95 205.89ms vs H4 362.96ms (single pair, not conclusive sustained capacity).

H6 removed from working source: no stable full-system benefit in this iteration (14/30000 failed, P95 361.7ms). Preserve code snapshots, race tests and cold/warm benchmarks as rejected candidate evidence. H7 wallet sync/NoSync+Darwin NoGrowSync comparison running on frozen H5; report durability difference explicitly, no pacing behavior changes.

H7 short pair: sync/NoSync+NoGrowSync wallet both 30000 successful, fast P50 42.90/4.84ms, P95 302.15/140.52ms, actual478.75/490.76TPS. Lost virtual pacing time2.669/1.135s nearly equals observed send-span excess; underlying stops not yet attributed. Add explicit -wallet-no-sync (default false), keep accounting transactions and wallet verification, annotate report. Reverse A is running.

H8 references: first payload retained in Collected/Install, Outbox references it; legacy/early inline still supported, decode only on actual action, strict Fact/Issuer checks. Gateway local schema5/member6. Race and repeated alternate-QC INSTALL tests passed. Fixture payload2378B, inline outbox3388B -> reference267B (no claim of fewer transactions). 600 target/36000 comparison running.


### H8 reference outbox — rejected after short and sustained comparisons
Payload 3388 -> 267 bytes in fixture, but full600 referenceB actual 578.55 vs585.03, sustained referenceLong551.28 vs555.89, 8 vs0 failures. P95 improved in long sample but no coherent overall benefit. Restored only H8 touched source files from h7-source.tar.gz; candidate source remains archived as .txt. Next: diagnostic-only bbolt native writes, freelist writes, mmap/grow and Update walltime; no parameter change.


### H9 experimental member ordered memory
Native bbolt IO diagnostic: active member646459 WriteAt calls11.629GB,13.623s, max678.65ms. Freelist0.149s,mmap1.8ms,grow9.7ms not main cost. Existing google/btree supports ordered in-memory state with one outer RWMutex, same Group and validation/commit-before-sign order. All changes validated before applying; keys/values cloned. Exclusive .pending marker reserves fresh experiment; clean Close exports bbolt audit in bounded batches then renames; not a resume path. Explicit UTXO_EXPERIMENT_MEMBER_MEMORY=1; other roles unchanged. Store/member/gateway race suites pass. 36000 target600: actual599.46TPS,0fail,fast3.61/93.27ms,61.015s,all audit/outbox pass. Sustained comparison next.


### H10 gateway persistence fallback diagnosis — not a primary target
GatewayWait 4803/90000 saves fell back synchronously. GatewayConn (after documented completed-lab disk cleanup) only524/90000; 900 HTTP samples52 wait>50ms, only1 of those overlaps the previous same-connection fallback. Three samples matched at all;110.64ms of6848.12ms written-to-handler waiting(1.6%). One concrete92.66ms case proves the mechanism exists, not dominant. Gateway memory candidate deferred. GatewayConn actual599.54TPS,0fail,fast4.15/118.68ms, no fast/total permit saturation. Variation not attributable to code; next all-source fresh build without extra diagnostic and higher-rate probe.


## 收敛
全源码fresh500/fresh600/fresh800完成。保留H1/H2/H4/H5/H7/H9；H3/H6/H8撤回、H10不实施。500/600正式轮零失败、审计通过、总配额未触顶；800实际679.66且背压明显，不认定800达标。最后backendProfileRetry只拆解等待，没有足够证据支持新增补丁。完成回归、干净发布树校验、文档和Git推送。
