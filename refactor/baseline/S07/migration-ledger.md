# S07 迁移与交接账本

本账本以已提交 S06 `1da54c8091e7f56f5cb9d8621bea5918f87471a6` 为基线。计划正文最初写的 `d0ce550 + S06 工作树` 已按用户“先提交”要求转换为该提交，未回退 S06。实施阶段只落实 B01—B05、迁移和约定验证。

逐文件、符号行号、构建条件、import、测试名与源码摘要见 [current-symbols.json.gz](current-symbols.json.gz)。[build-selections.json.gz](build-selections.json.gz) 保留普通/unit/integration/wireinject/embed/e2e 与 Darwin/Linux 的实际 go list 结果；[consumer-refs-normal.json.gz](consumer-refs-normal.json.gz) 及相应 unit/integration 文件按 Go 类型信息记录真实静态引用和接口结构实现候选。结构实现候选不等同于生产装配，生产实例以下表及 Wire 为准。

所有路径均相对于 `backend/internal/`。

| 旧入口或混合职责 | 唯一实现/生产装配 | 保留调用者与退出 |
| --- | --- | --- |
| service/scheduler_events.go、scheduler_outbox.go；repository/scheduler_outbox_repo.go | scheduler/events.go、outbox.go、postgres/outbox.go。account/routing/egress/billing 的 app writer 使用原 SQL 连接 | 旧 repository/service 事件签名仅别名/委托；旧仓储消费者 S08—S14 逐批改绑，S15/S16 清理 |
| service/scheduler_snapshot_service.go | scheduler/snapshot.go、snapshot_contracts.go；app/scheduler.go 从 account/postgres、routing/postgres 读取 | service facade 仅旧 Group/Account 投影；网关 S09/S11，最终兼容 S15/S16 |
| repository/scheduler_cache.go | scheduler/rediscache/snapshot.go、snapshot_codec.go、bucket_lock.go | repository facade 只改形状；原完整/轻量 JSON 的 LegacySchedulerCodec 唯一保留 service/scheduler_snapshot_codec.go，不能改成 account.Record 的管理 JSON；S11/S15/S16 清理 |
| repository/account_snapshot_publisher.go 及 app/legacybridge/account_snapshots.go | scheduler/SnapshotPublisher；app account/routing/billing/egress 共享快照和同连接 outbox writer | 相同事务与提交后尽力发布保持，未额外重复入队 |
| service/advanced_scheduler_core.go、openai_account_scheduler.go 中评分/反馈/Top-K | scheduler/scoring.go、basic_selection.go、sticky_escape.go、policy；app/scheduler_state.go 唯一 RuntimeStats | 旧类型转换与平台资格回调留 S09/S11；白盒私有函数只在对应标签 `_test.go` 存在 |
| service/gateway_scheduling.go 基础/高级/单平台/混合选择 | scheduler/GenericSelector，generic_selection*.go | service/scheduler_generic_legacy.go 仅每次调用的投影关联和平台端口；旧执行账号不能进入评分/诊断；S09/S11 |
| service/openai_account_scheduler.go、openai_gateway_scheduling.go 的硬绑定、订阅池、选择与 fresh/DB 复核 | scheduler/PlatformSelector，platform_selection*.go、platform_basic_selection.go；Lease.Select | service/scheduler_platform_legacy.go 保留平台资格、Compact/transport/Grok 等旧能力调用；Grok 专属免费配额观测缓存留 S09，非第二份调度状态 |
| service/setting_update.go 及高级设置读取 | scheduler/SettingsRuntime 五秒 TTL/singleflight/解析；app 绑定唯一实例 | service/scheduler_settings_legacy.go 读取旧 Settings 并投影；S10/S11/S16 |
| service/concurrency_service.go、repository/concurrency_cache.go | scheduler/concurrency.go、rediscache/concurrency_cache.go；app provideConcurrency | 旧 service 类型别名；HTTP/Ops/任务消费者 S08/S11/S13，最终别名 S15/S16 |
| handler/gateway_helper.go 等用户/账号等待 | scheduler/AcquireUser、WaitResult、WaitForSlot；request Lease → attempt Lease | HTTP 拥有同步心跳、SSE、Header、Flush、错误码和完成状态；S11 迁 handler 时继续复用 |
| handler/gateway_handler.go 会话 map 与释放；通用账号补全失败 | scheduler/SessionAttempts、SessionBinding、AttemptLease.Finish；Lease 获取即登记 | 网关继续决定重试、成功或可结算部分结果；物理槽释放不能提前把尾部 usage 判为失败；S11 |
| service/user_msg_queue_service.go、handler/user_msg_queue_helper.go | scheduler/message_queue*.go；rediscache/user_msg_queue_cache.go | HTTP 只观察和输出，Redis TIME、ID、TTL、延迟、串行锁不变；旧入口 S11/S16 |
| handler/image_concurrency_limiter.go | scheduler/image_limiter.go 与 Lease 幂等释放 | 保持原独立本地限流器实例，不合并 Redis 账号池；S11 |
| service/openai_live.go、openai_live_types.go 的租约端口 | scheduler/live_lease.go；ConcurrencyService.LiveLeases | 远端会话、Live 观察、结算及普通槽替换仍在平台执行层 S09/S11；独立于 WS 入站租约 |
| service/openai_sticky_compat.go；repository/gateway_cache.go 的 sticky 键 | scheduler/sticky.go、rediscache/sticky.go；app 唯一 StickyStats | session 标识字符串解析、远端 response/reasoning/session 及异步资金数据仍留 S09/S11；只迁调度粘性 |
| service/rpm_cache.go、user_rpm_cache.go 及 BillingCache 的 RPM 准入 | scheduler/rpm_admission.go、account_rpm.go、policy/rpm.go 及 Redis Adapter | billing 资金检查后才调用；simple、故障放行和成功后软计数时点不变；RateLimitService 的供应商错误/健康解析留 S09 |
| SessionLimitCache 混合费用窗口与会话查询 | scheduler/session_limit.go、session_policy.go、rediscache/session_limit.go；billing/window_cost*.go、rediscache/window_cost.go | app 的 legacySessionCache 只组合两个唯一实现；usage 源投影 service/scheduler_window_cost_legacy.go 等待 S08；无金额算法或精度变更 |
| service/advanced_scheduler_diagnostics.go；account/httpapi/management_diagnostics.go | scheduler/DiagnosticService 与 scheduler/httpapi/DiagnosticsHandler，路由直接绑定 | service/scheduler_diagnostics_legacy.go 为每次调用提供平台资格和旧观测投影；S08/S09/S11；旧 AccountHandler 方法仅兼容委托 |
| service/wire.go、repository/wire.go 的相关 provider | app/scheduler_wire.go、scheduler.go、scheduler_state.go、account_diagnostics.go；wire_gen.go 生成 | service.NewRateLimitService 保留原五参数签名，生产使用显式反馈构造；旧入口 S09/S11/S16 |

## 生命周期与资源拥有者

| 资源 | 唯一生产持有者 | 构造、启动与结束 |
| --- | --- | --- |
| 快照重建/outbox/排队 I/O | scheduler.SnapshotService + WorkerRuntime | 构造不启动；初始重建异步/outbox 即刻首轮；Stop 取消并等待，重复 Start/Stop 屏障，停止后不能重开 |
| 用户、账号、Key 统计槽，等待、负载读取、WS 入站租约 | scheduler.ConcurrencyService | app 在原位置进行旧进程槽初始化清理；周期由生命周期启动；取消等待、封闭认领，实际槽位和续租结束后才报告成功 |
| 串行锁、RPM 延迟、清理任务 | scheduler.UserMessageQueueService | 构造不启动；原周期 Start；Stop 取消等待，归还已确认锁，并等在途 |
| 每次请求/尝试 | Lease / AttemptLease / SessionAttempts | 获取即登记；补全/复核失败回滚；重复释放幂等；成功或部分结果按原规则保留会话 |
| 反馈/动态参数/粘性统计 | app.schedulerSharedState | 不启动后台；同一实例供生产和诊断；旧 getter 只指向该实例，不复制锁/缓存 |
| SQL/Redis | app 原资源持有者 | HTTPRequests 完成后停止调度，再关 Redis/Ent；任何未完成任务按剩余预算报告，不能宣称 drain |

Qoder 已进入上游的流式请求采用完成释放，断开只停止下游写入，原预算内收集尾部 usage；非流与等待保持取消释放。上游重试循环仍由旧网关拥有，未增加第二套 failover。

## 固定修复与保留限制

- B01：启动/停止屏障与运行 context，定向 race 及真实进程启停验证。
- B02：Allowed 与 Ownership 分离；增加失败放行但不递减不属于自己的等待计数。
- B03：补全失败即时、幂等归还已取得账号槽。
- B04：批量会话 Lua 冷启动可执行，SCRIPT FLUSH 后不漏报。
- B05：原 key/string/TTL 上增加 owner token，比较后删除，旧持有者不能删除继任锁。
- 原负载集成测试恢复 Skip，原断言完整保留。
- 低 ID outbox 迟提交保持周期全量重建恢复；十秒清理宽限不能重新轮询旧 ID，不声明严格逐事件消费。
- 本阶段没有扩展历史故障审计。旧 Compact SSE 并行测试 Gin.SetMode race 仅登记，详见 incidental-gin-race.json；不改变无关测试或扩大忽略。
- 本次迁移导致的已取消无限制槽行为回归、Responses 生图意图上下文被覆盖，均作为本次回归修正；不列为新发现的历史业务修复。

## 文档与不变边界

稳定锚点包括 account_scheduling_and_cache 的 `advanced_scheduler_selection`、`scheduler_snapshot_consistency`、`session_lifecycle`，gateway_request_lifecycle 的 `gateway_pipeline`、`account_selection_and_failover`，system_architecture 的 `dependency_layers` 以及 development_workflow 的 `backend_dependency_rules`。资金、配置、HTTP 文档同步当前实现；不提前描述 S08—S16 为已迁移。

SQL migration、Ent、S00—S06 冻结资料及其他任务内容独立校验。只生成 Wire，缓存仍使用 sched:v2 与认证 v40，金额、资金原子范围、HTTP 及供应商取消策略保持。撤销 S07 时不撤销 S06；撤销 B01—B05 会恢复对应风险，无数据格式降级。
