# S02 生命周期与资源拥有者

完整图构造与回调绑定结束后才调用 Start。必要初始化 I/O 为 bootstrap 的明确失败点；SQL 连接取得后立即登记错误回收，成功返回后由 Ent 单独关闭。Redis、默认 coordinator、观察接口、后台任务端口、日志后端均登记失败清理。

## 顺序与预算

启动按 StartOrder 由小到大；关闭按 StopOrder 由小到大，同组只能放可独立等待的拥有者。HTTP 监听/优雅关闭在 Manager 外由 Serve 统一收尾，独立 5 秒；后台从新的 context 起算 30 秒。HTTPHijackedConnections 为 10，HTTPRequests 为 15；晚于关闭快照的 hijack 也会关闭。所有 handler 尾部结束后才停依赖。

Manager 对重复 Start/Stop/Cleanup 共享结果；构造失败释放已获得的无 Start 资源，部分启动失败也清理失败项。超时包含未完成任务名，不推进共享连接关闭；进程以非零结束。清理本身或日志报告可能阻塞，因此二者都受预算限制。正常 Linux 重启请求只延迟约 600ms 取消主循环，由同一退出链处理。

| 拥有者 | StartOrder | StopOrder | 注册证据 |
| --- | --- | --- | --- |
| `PricingInitialization` | 188 | — | `internal/app/legacy_runtime_boot.go:22` |
| `TLSFingerprintCollectorService` | 995 | 5 | `internal/app/legacy_runtime_core.go:98` |
| `OpenAILiveObservers` | 995 | 5 | `internal/app/legacy_runtime_core.go:140` |
| `OpenAIGatewayService` | 990 | 10 | `internal/app/legacy_runtime_core.go:79` |
| `fmt.Sprintf("BackgroundBarrier%d", phase)` | 1000 - phase | phase | `internal/app/legacy_runtime_core.go:137` |
| `OpsWSRuntime` | 983 | 17 | `internal/app/legacy_providers.go:45` |
| `PricingService` | 980 | 20 | `internal/app/legacy_runtime_boot.go:46` |
| `AuthCacheInvalidationWorker` | 980 | 20 | `internal/app/legacy_runtime_core.go:33` |
| `SchedulerSnapshotService` | 980 | 20 | `internal/app/legacy_runtime_core.go:44` |
| `UsageCleanupService` | 980 | 20 | `internal/app/legacy_runtime_core.go:56` |
| `IdempotencyCleanupService` | 980 | 20 | `internal/app/legacy_runtime_core.go:67` |
| `PaymentOrderExpiryService` | 980 | 20 | `internal/app/legacy_runtime_core.go:86` |
| `BatchImageCleanupService` | 980 | 20 | `internal/app/legacy_runtime_jobs.go:21` |
| `BatchImageWorkerRuntime` | 980 | 20 | `internal/app/legacy_runtime_jobs.go:32` |
| `CreativeWorkerRuntime` | 980 | 20 | `internal/app/legacy_runtime_jobs.go:43` |
| `CNProviderBalanceCheckService` | 980 | 20 | `internal/app/legacy_runtime_jobs.go:55` |
| `TokenRefreshService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:29` |
| `AccountExpiryService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:40` |
| `ProxyExpiryService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:51` |
| `SubscriptionExpiryService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:62` |
| `AnnouncementExpiryService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:73` |
| `ScheduledTestRunnerService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:85` |
| `GroupAvailabilityProbeRunnerService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:96` |
| `ConcurrencyService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:108` |
| `UserMessageQueueService` | 980 | 20 | `internal/app/legacy_runtime_maintenance.go:119` |
| `OpsMetricsCollector` | 980 | 20 | `internal/app/legacy_runtime_ops.go:29` |
| `OpsAggregationService` | 980 | 20 | `internal/app/legacy_runtime_ops.go:40` |
| `OpsAlertEvaluatorService` | 980 | 20 | `internal/app/legacy_runtime_ops.go:51` |
| `OpsCleanupService` | 980 | 20 | `internal/app/legacy_runtime_ops.go:62` |
| `OpsScheduledReportService` | 980 | 20 | `internal/app/legacy_runtime_ops.go:73` |
| `OpsIngressRejectAggregator` | 980 | 20 | `internal/app/legacy_runtime_ops.go:108` |
| `BackupService` | 980 | 20 | `internal/app/legacy_runtime_ops.go:123` |
| `OpsService` | 975 | 25 | `internal/app/legacy_runtime_ops.go:97` |
| `DashboardAggregationService` | 970 | 30 | `internal/app/legacy_runtime_ops.go:135` |
| `UsageRecordWorkerPool` | 960 | 40 | `internal/app/legacy_runtime_queues.go:47` |
| `ContentModerationService` | 955 | 45 | `internal/app/legacy_runtime_queues.go:106` |
| `UsageLogBatchers` | 952 | 48 | `internal/app/legacy_runtime_core.go:129` |
| `BillingCacheService` | 950 | 50 | `internal/app/legacy_runtime_queues.go:36` |
| `UserPlatformQuotaUsageFlusher` | 945 | 55 | `internal/app/legacy_runtime_queues.go:59` |
| `OllamaCloudUsageService` | 940 | 60 | `internal/app/legacy_runtime_queues.go:71` |
| `DeferredService` | 935 | 65 | `internal/app/legacy_runtime_queues.go:94` |
| `LegacyBackgroundTasks` | 932 | 68 | `internal/app/legacy_providers.go:54` |
| `EmailQueueService` | 930 | 70 | `internal/app/legacy_runtime_queues.go:25` |
| `AuditLogService` | 925 | 75 | `internal/app/legacy_runtime_queues.go:82` |
| `OpsErrorLogWorkers` | 924 | 76 | `internal/app/legacy_providers.go:46` |
| `OpsSystemLogSink` | 210 | 790 | `internal/app/legacy_runtime_ops.go:84` |
| `HTTPSettingsInitialization` | 182 | 800 | `internal/app/http_bindings.go:43` |
| `SettingsHTTPNotification` | 0 | 800 | `internal/app/http_bindings.go:86` |
| `LegacySettingsInitialization` | 181 | 800 | `internal/app/legacy_runtime_boot.go:34` |
| `APIKeyService` | 195 | 805 | `internal/app/legacy_runtime_auth.go:27` |
| `OAuthService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:39` |
| `OpenAIOAuthService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:50` |
| `GeminiOAuthService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:61` |
| `AntigravityOAuthService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:72` |
| `QoderOAuthService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:83` |
| `GrokOAuthService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:94` |
| `TLSFingerprintProfileService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:106` |
| `TLSFingerprintRouterService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:117` |
| `ErrorPassthroughService` | 190 | 810 | `internal/app/legacy_runtime_auth.go:129` |
| `RuntimeLocalCaches` | 185 | 815 | `internal/app/legacy_runtime_core.go:117` |
| `TimingWheelService` | 180 | 820 | `internal/app/legacy_runtime_core.go:105` |
| `HTTPIdleConnections` | 160 | 840 | `internal/app/legacy_runtime_core.go:141` |
| `DefaultIdempotencyCoordinator` | -999 | 850 | `internal/app/application.go:42` |
| `IdempotencyObserver` | -1000 | 850 | `internal/app/application.go:47` |
| `LegacyBackgroundBinding` | -998 | 850 | `internal/app/legacy_providers.go:55` |
| `Redis` | -90 | 900 | `internal/app/providers.go:22` |
| `Ent` | -100 | 910 | `internal/app/providers.go:17` |
| `LogBackend` | -2000 | 1000 | `internal/app/application.go:39` |

`BackgroundBarrier%d` 实际登记于 16、21、26、31、41、46、49、51、56、61、66，等待此前一层派生的后台副作用，再继续关闭下一层；最终 Tasks.Stop 在 68 封闭任务入口。原 context、并发方式、参数求值与失败行为保留。完整 Start/Stop 函数体见 [lifecycle-hooks.json](lifecycle-hooks.json)。

## 显式及嵌套资源

- 时间轮：infra/timingwheel.New 不运行，Start 保持 1 秒/3600 槽；CancelAndWait 和 Shutdown 阻止 recurring 再入队并等待已执行回调。Deferred 在回调停止后串行最后 flush，失败上报且保留原重试数据。配额周期仍限制 16 批，退出会在预算内处理剩余批次；原单批事务和失败回填不变。
- 队列：用量池停止自动扩缩并等待执行；仓储两个按需 batcher 封闭入队并消费剩余批次；billing/email/audit/moderation/运维错误日志按既有失败语义排空。内容审核、配额或 ingress 聚合仍有未写回内容时明确返回失败，不以 Stop 代替成功持久化。
- 嵌套会话：Claude/OpenAI/Gemini/Antigravity/Grok/Qoder 的内存会话清理在所属 OAuth Start 开启，Stop 等待；替换为 Redis session 后不启动被丢弃内存实例。API Key 的两个 ristretto 缓存和 subscriber 同一拥有者关闭。错误透传与 TLS profile/router 的最后一次订阅回调结束后才关闭 Redis。
- 缓存：go-cache 构造不再隐式创建 janitor，共用 timewheel `runtime:local_caches` 定期清理。Dashboard 聚合、配置触发重算与异步刷新都纳入完成等待。Ops reload 产生的已退休 cron 也在最终 Stop 等待。
- 按需资源：WS 池保持原启动时机，关闭封闭创建、获取和预热；活动租约仍按原请求策略收尾，归还时释放。TLS collector 在 Shutdown 后拒绝重开并等待 Serve 返回。Live observer 只取消本地观察，不提前结算远端会话。QPS WS 的三十秒空闲定时器及后台刷新有明确关闭入口。
- 技术资源：各 HTTP 缓存/账号池保持原命名空间，只关闭闲置连接；Redis 和 Ent/SQL 保持唯一实例；日志最后 Sync 并关闭文件。lumberjack 内部维护循环保留原第三方进程生命周期，其公共 API 不能独立停止，本阶段没有复制/修改轮转算法。

[background-ownership.json](background-ownership.json)覆盖本阶段核对的显式 goroutine、ticker、AfterFunc 与后台任务入口，记录实际方法/行号及等待链。[construction-audit.json](construction-audit.json)剩余三项是请求内的流 body/line pump，依靠原 body.Close/取消链结束，并非 Wire 构造启动；按需 WS pool 由调用路径和所属服务拥有。Provider 内未保留 Start 调用。

## 验证

可控阻塞任务验证启动失败逆序清理、延迟回调、重复调用、有界返回、HTTP 尾部与迟到 hijack；新模块/时间轮与旧队列/连接池/订阅做定向 race。真实进程验证 standard/simple、CLI/Web/AUTO_SETUP、监听失败、初始化失败、SIGTERM、版本；Linux 容器调用真实管理员重启及幂等重放，具体退出码与时长见 [linux-process.result.json](linux-process.result.json)。所有记录见 [verification.md](verification.md)。
