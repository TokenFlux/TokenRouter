# S08 资源与停止顺序

实际 provider 和 Hook 引用见 [逐符号账本](ownership-ledger.json.gz)，进程行为见 [s08-process-observation-contracts](s08-process-observation-contracts.result.json) 及全量 integration JSON。app 继续使用 HTTP 5 秒、后台合计 30 秒预算；某个 Stop 超时会保留依赖、不把未结束项报告为已 drain。

| 资源 | 唯一构造/拥有者 | 启动 / 停止与依赖 |
| --- | --- | --- |
| usage Store / batchers | app/usage → usage/postgres.Store；旧 repo 包同一 native Store | 构造不启动独立业务 worker；批处理按原首次写入路径建立。UsageRecordWorkerPool(40) 结束后 UsageLogBatchers(48) drain；SQL 最后关闭 |
| UsageCleanupService | app 绑定 native usage 核心，旧 wrapper 不复制状态 | 保留原立即首轮、认领、批次和手动调用；停止新认领并等待在途。先于 DashboardAggregation(30) 停止，已删除收尾可登记修复 |
| DashboardAggregationService | app 唯一聚合器 / usage | StartOrder970，StopOrder30；B04 运行 context 取消在途和重试；workWG 跟踪手动/后台重算，StopContext 受剩余预算约束 |
| 查询缓存 / 预聚合控制器 | usage/ops 各自持有缓存；settings/preaggregation 唯一控制器 | 原 TTL/stale refresh/key 范围保留，返回独立副本；没有全局合并不同查询缓存 |
| AuditLogService | app/observability_foundation → audit | 队列/批次/健康唯一；启动屏障阻止重复 writer；StopOrder75，StopContext 幂等并禁止重开 |
| OpsService | app/ops → ops.OpsService | 同步初始设置/日志投影保持；构造无刷新 goroutine。StartOrder975 / StopOrder25；定时运行设置刷新停止 |
| OpsMetricsCollector / Aggregation / AlertEvaluator / Cleanup / ScheduledReport | app/ops 各唯一实例；窄 SQL/Redis/邮件/主机端口 | StartOrder980 / StopOrder20；保留首轮/周期/Cron/锁/预算。停止后不重开，app manager 约束等待和依赖顺序 |
| OpsIngressRejectAggregator | app/ops 的唯一实例绑定 OpsService | 同上，停止后检查 PendingBatches，非零返回未排空错误；保留原批次重试和容量 |
| Ops realtime | OpsService.Realtime() 惰性唯一 Runtime | 首个连接启动、最后连接空闲停止；HTTP 帧由 Adapter；OpsWSRuntime(17) 关闭连接后才停服务/数据源 |
| OpsErrorLogQueue | app/error_queue 提供并绑定 handler 消费者接口 | 无构造启动，首条记录按原 worker 规则启动；HTTP/生产者结束后 StopOrder76，阻止新入队，排空并报告错误；绑定清理852 |
| OpsSystemLogSink | app/observability_foundation 唯一 sink | StartOrder210 安装到唯一 logging 后端；StopOrder790 先解绑 sink，再受 context 约束停止。2–60 秒退避与健康只有一份 |
| ReleaseQuery / update cache | app/ops 的唯一 ReleaseQuery | 查询按需执行、20 分钟缓存；无第二定时器。下载替换/回滚任务仍由旧维护拥有 |
| PostgreSQL / Redis / logging | app 既有资源拥有者 | HTTPRequests(15) 先于观测停机；队列和在途工作先结束，Redis/Ent/日志资源最后释放；超时不声称关闭成功 |

进程夹具验证 standard/simple 真实 SIGTERM、各观测 Hook 仅一次、HTTP → 生产者 → 队列 → Redis/Ent、UsageCleanup → DashboardAggregation、错误队列 → 系统 sink。它使用隔离 PostgreSQL/Redis、本地价格服务和临时配置。维护命令只使用精简 bootstrap，不构造完整 worker 图。

B05 的重算独立于被取消的清理任务 context，但由聚合运行时管理；进程崩溃或最终预算耗尽后的持久修复不属于本阶段保证。普通日志过载丢弃及拒绝聚合未完成项按原语义分别统计，不把队列空或日志落盘当作资金成功。
