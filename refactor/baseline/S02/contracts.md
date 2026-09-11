# S02 契约矩阵

所有普通、unit、integration 命令都指定 Go 1.27.0；逐次命令、退出码、事件数量及日志由 result.json 保存。下面列出测试真实执行的关键入口；[contract-events.json](contract-events.json)包含实际 pass 事件、包、构建参数和日志，未执行/跳过不计通过。

| 契约 | 证据入口 | 覆盖与边界 |
| --- | --- | --- |
| 构造、启动、重复启停、部分失败 | lifecycle 的 `TestManagerOrdersStartAndDrain`、`TestManagerRollsBackPartialStartAndConstruction`、`TestManagerStopWaitsForPartialStartup` | 构造不运行 worker；失败项也回收，消费者先于数据库完成 |
| 有界等待与任务完成 | `TestManagerTimeoutKeepsDependenciesAndSharesResult`、`TestManagerReportingIsBounded`、`TestTasksStopDrainsChildrenAndRejectsLaterWork`、`TestTasksBarrierKeepsLaterConsumerSideEffects` | 共享一次停止结果；超时返回未完成名字，不继续关闭依赖；允许在途任务派生子任务 |
| HTTP、重启、进程入口 | `TestRequestsWaitIncludesHandlerTail`、`TestRequestsCloseLateHijack`、`TestRestarterPlatformDelayAndClose`、`TestS02ProcessModes`、Linux 容器脚本 | standard/simple、CLI/Web/AUTO_SETUP、SIGTERM、监听占用、构建变量、失败连接回收；真实 Linux 管理员重启、重放 Header 与退出码 |
| 时间轮及最终写回 | `TestWheelCancelWaitsAndPreventsRearm`、`TestWheelShutdownTimeoutReportsIncompleteCallback`、`TestDeferredStopSerializesFinalFlush`、`TestFlusherShutdownDrainsBeyondTickLimit` | 阻止 recurring 再入队；最终 flush 不重入；失败保留待写数据并报告，原金额/事务算法不变 |
| 队列、扩缩、订阅 | `TestUsageRecordWorkerPool_AutoScaleUpAndDown`、`TestCreativeWorkerRuntimeStartStopAndScale`、`TestOpsErrorLogShutdownDrainsCapturedQueue`、`TestSubscriptionsWaitForInFlightCallback` | 按需日志队列正常 drain；动态 worker 退休等待；真实 Redis 最后回调结束后才允许关闭客户端 |
| 按需连接/观察 | `TestOpenAIWSConnPoolShutdownSealsLazyCreation`、`TestStopLiveObserversPreservesRemoteCall`、`TestTLSFingerprintCollectorShutdownSealsStart`、`TestOpsWSShutdownCancelsIdleTimer` | 关闭后不重开，不提前结束远端 Live 会话；空闲 WS 连接释放，在途租约保持原取消策略；30 秒空闲定时器不拖延退出 |
| settings 存取和通知 | `TestStorePreservesWriteAndExplicitNotificationBoundary`、`TestStoreUnsubscribeDuringNotification`、`TestStoreVersionAndSubscriptionConcurrency`、`TestS02StorageContracts/settings-batch-atomicity` | PostgreSQL 触发器制造真实写失败，批量原子回滚；写本身不隐式通知；旧刷新/替换回调语义和版本字段保留 |
| 设置业务和网页 | 旧 SettingService/UpdateSettings/PublicSettings 测试与 `TestFrontendServer_InjectSettings`、`TestFrontendServer_InvalidateCache` | 省略、清空、敏感值、缓存刷新及公开 JSON/CSP；真实前端产物下 embed 注入已执行。websearch 的旧代理/不直连测试继续保留 |
| 完整幂等 | `TestIdempotencyCoordinator_ConcurrentSameKeySingleSideEffect`、`TestIdempotencyCoordinator_SameKeyDifferentPayloadConflict`、`TestIdempotencyCoordinator_BackoffAfterRetryableFailure`、`TestIdempotencyCoordinator_TruncatedStoredResponseRemainsUTF8` | 核心保留 scope/hash、observe-only、TTL、错误重放、UTF-8/脱敏、存储故障与指标；旧默认入口共用状态 |
| 幂等存储与维护 | `TestS02StorageContracts/idempotency-concurrent-claim-replay-cleanup`、`TestCleanupStopWaitsForFirstRound`、SystemOperationLock 测试及 Linux 管理员重启重放 | 24 个真实 PostgreSQL 并发调用只产生一次副作用；清理保持首轮、60 秒/500 默认与等待停止；维护锁不改变续租作用域 |
| 公告 | site 的原 targeting/service/expiry/sort 测试、`TestS02StorageContracts` 公告子例及全仓 HTTP 契约 | targeting 组合、金额比较、可见性、时间边界、分页排序、清空时间；真实外层事务回滚，重复已读保留首个时间，读前/周期归档保留 |
| 数据库引导 | `TestS02MigrationsLockReplayAndRollback`、InitializeDatabaseWithRetry、EnsureBootstrapSecrets、simple 默认数据测试 | 真实迁移专用锁、并发重放、checksum 拒绝、事务回滚与非事务并发索引；暂时错误重试、永久错误立即失败与取消 |
| 依赖门禁 | [dependency-fixtures.json](dependency-fixtures.json) | 普通/unit/integration/wireinject/embed、Darwin/Linux 的合法依赖和预期违规；准确旧文件许可、同目录新文件、迁出文件、核心反向依赖、非法子包、正常 Adapter；夹具已删除 |
| goroutine 完成 | lifecycle、infra/旧 runtime/handler 的定向 race 与 [lifecycle-goleak-fixture.result.json](lifecycle-goleak-fixture.result.json) | 原文件摘要相同的隔离副本启用 goleak，19 个生命周期事件通过；不新增仓库依赖或全仓 race |

真实供应商 E2E 沿用既有缺少地址/密钥和缺失脚本的限制，归 S16；外部 TLS capture 环境限制也沿用 S01。Linux amd64 二进制完成编译；进程级 Linux 重启使用本机 Docker 的原生 arm64 镜像。最初 amd64 镜像获取超时、容器重开后动态端口改变造成的探测失败均作为验证工具修正保留，不归为产品失败。
