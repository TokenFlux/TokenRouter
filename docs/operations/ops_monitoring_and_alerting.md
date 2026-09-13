# 运维监控与告警

本文描述 Ops 信号采集、实时视图、告警评估、邮件报告、健康诊断与发布查询。它是[可观测性与数据生命周期](observability_and_data_lifecycle.md)的详细专题，不拥有 Usage 结算、预聚合实现或备份内容策略。

## 章节导航

- [信号流水线](#信号流水线)：修改指标、错误或系统日志采集时读取。
- [实时与历史查询](#实时与历史查询)：修改 dashboard、WebSocket 或查询模式时读取。
- [告警评估](#告警评估)：修改规则、持续时间、静默或通知时读取。
- [计划报告](#计划报告)：修改日报、周报或健康摘要时读取。
- [健康与失效语义](#健康与失效语义)：排查空面板、漏报或后台任务故障时读取。
- [发布查询与维护命令](#ops_release_and_maintenance)：检查版本、回退候选或历史入口拒绝清理时读取。

<a id="ops_signal_pipeline"></a>
## 信号流水线

`ops` 拥有观测规则和运行实例；`ops/postgres` 执行业务表查询，`ops/rediscache` 提供原键空间的锁与缓存，`ops/provider` 读取主机/cgroup/连接池及发布信息，`ops/httpapi` 保留管理与实时协议。采样核心只读取账号、身份、并发和认证健康投影。

Ops 面同时接收请求错误、独立上游 attempt 错误、入口准入拒绝、系统日志、并发、账号可用性、实时流量和系统指标。每类信号有独立 repository/队列，不能用一个表的计数代替另一类：最终客户端失败可能包含多个上游错误，一次本地拒绝也可能没有任何上游 attempt。

`OpsMetricsCollector` 在 Ops 和 monitoring 开关启用时周期采集数据库、Redis、主机/容器、账号负载和运行时指标。多实例通过 Redis leader lock，必要路径可用 PostgreSQL advisory lock，确保一个周期只持久化一次；运行结果写 job heartbeat、耗时和错误。

系统日志 sink 与 request/error capture 使用相互独立的有界队列。Audit 与系统日志 sink 的重复 Start 由各自屏障合并，Stop 之后不能重开；系统日志退避和健康计数始终只有一份。拥塞时按各自策略丢弃或降级，并累计 dropped/health 计数；它们不得反压网关核心转发。系统日志落库失败会执行 2 秒起、60 秒封顶的指数退避，退避窗口内的批次计入 dropped 而不访问数据库，成功后立即清除失败状态。敏感字段在进入存储前清理，request ID、平台、Group、账号和 endpoint 用于关联。

## 实时与历史查询

管理员 Ops API 提供 concurrency、user concurrency、account availability、realtime traffic、错误/上游错误/请求详情、入口拒绝、系统日志和 dashboard snapshot/trend/histogram/token stats。概览、错误列表与请求明细弹窗必须共享当前时间范围；自定义范围使用同一组 `start_time` / `end_time` 半开区间，请求明细的窗口标签展示对应起止日期时间，已选自定义模式时再次修改边界也应刷新数据。任一边界缺失时统一回退到 `1h`，不能把字面量 `custom` 传给后端。QPS WebSocket 用于短窗口实时展示，仍需管理员鉴权，不能视为长期审计源。Ops 持有按需采样、连接计数和三十秒空闲停止；HTTP Adapter 保留 Origin/可信代理判定、管理员认证、帧、关闭码及写超时，停止等待不能由连接断开假定完成。

历史 dashboard 查询可按配置使用原始表或预聚合，并在覆盖不足时回退。聚合、水位和回填由[使用记录与运维预聚合](pre_aggregation.md)拥有。页面空数据需区分 monitoring 关闭、过滤条件、采集丢弃、聚合覆盖、查询超时和确实无流量。

## 告警评估

告警规则包含 enabled、metric type、operator、threshold、window、sustained minutes、scope/filter 和通知动作。评估器按运行设置周期执行，使用 leader lock 避免多实例重复事件；连续 breach 达到持续要求后创建或更新 active event，恢复后关闭/标记 resolved。

静默记录只抑制匹配范围和时间内的通知/事件动作，不删除原始指标。管理员可以确认、解决事件和配置邮件通知。邮件有全局/运行时限流，发送失败进入 heartbeat/日志，不能把事件标成已经送达。

最终错误透传规则的 `skip_monitoring` 只跳过指定客户端错误的常规监控记录，不能跳过系统健康、访问、计费或安全审计。详见[网关错误响应策略](../interfaces/gateway_error_policy.md)。

## 计划报告

计划报告支持日报、周报、错误摘要和账号健康。Cron 使用分钟级五字段表达式，时区来自应用配置；每分钟调度器只执行已到期报告，并用分布式锁和 last-run key 防止重复发送。

报告收件人、启用项、错误最小计数和账号错误率阈值来自数据库运行设置。生成失败或邮件失败写任务 heartbeat；报告是观测摘要，不应作为扣费、SLA 赔付或账号自动恢复的唯一事实。

Ops 的构造与启动分离，app 在完整绑定后启动采样、聚合、告警、报告和清理。停止时等待当前采样/聚合及 cron 作业结束；清理器还等待此前 Reload 留下的在途 cron。单次局部超时不再被当成停止完成，最终退出受应用的总清理预算约束，详见[启动与关闭](../architecture/system_architecture.md#startup_and_shutdown)。

## 健康与失效语义

- Ops hard switch 关闭采集/查询和后台评估，但不能关闭网关转发、认证或计费。
- 观察 collector/evaluator/report/aggregation/cleanup 各自 heartbeat、leader lock、最近耗时和错误，不能只检查进程存活。
- 规则与运行设置通过数据库持久化和运行时快照传播；更新后检查多实例版本，不以单个管理 API 成功代表全部实例已刷新。
- Retention/cleanup 只删除观测数据；告警、报告或面板缺历史不影响资金账本，但会降低诊断完整性。

相关文档：[可观测性与数据生命周期](observability_and_data_lifecycle.md)、[使用记录与运维预聚合](pre_aggregation.md)、[账号维护](account_maintenance.md)。

<a id="ops_release_and_maintenance"></a>
## 发布查询与维护命令

发布查询由 ops 的 ReleaseQuery 和 GitHub provider 提供，继续使用原版本比较、回退候选过滤及二十分钟缓存。代理初始化失败、GitHub Token 的受信任范围、重定向与超时策略保持；下载校验、替换二进制、执行回滚、系统操作锁和重启仍由旧维护用例编排。

`cleanup-ingress-reject-logs` 使用精简 bootstrap 装配 Ops 的分类与清理能力，不启动完整 worker。默认 dry-run，沿用 `--before`、`--batch-size`、`--execute`、原输出及 `ingress-reject-v1` 分类版本；它只清理匹配的分析事件。
