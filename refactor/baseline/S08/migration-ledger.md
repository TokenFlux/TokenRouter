# S08 迁移与兼容交接

基线为 `98292d5e50a8f255270910918fa7dab0ce7b929d`。本清单按能力及混合符号划分；[文件索引](file-index.md)、[逐符号账本](ownership-ledger.json.gz)覆盖 400 个当前 Go 文件、5338 个声明和 236 个原文件、4302 个声明。130 个原文件已删除或搬迁；129 个当前文件是测试。计数包含被修改的混合文件内未迁声明，不表示这些声明全部归 S08。

[实际静态引用](consumer-edges.json.gz)记录 normal/unit/integration 的 40219 条合并调用边；[接口实现候选](interface-implementations.json.gz)是 Go 类型检查结果，不能代替运行时装配。实际生产绑定通过账本的 Wire/app 引用定位。方法的接口引用、类型字段引用与函数调用分别保留，测试替身不算生产实例。

## 唯一实现与旧入口

| 能力 / 原入口 | 唯一实现与具体拆分 | 生产消费者与兼容退出 |
| --- | --- | --- |
| `service/usage_log.go`、`pkg/usagestats` | `usage/log.go`、`request_type.go`、`usage_log_types.go`、`views.go`；独立展示值，标量方法只有一份 | 旧 UsageLog 保留递归展示形状，仅字段转换；旧完成 worker、gateway 和任务继续消费兼容入口，S11/S13；类型包装 S15/S16 |
| `service/usage_service.go`、`repository/usage_log_repo*.go` | `usage/service.go`、`repository.go`、`query_readers.go`、`usage/postgres/usage_log_repo*.go` | 原查询接口委托同一 Store；构造器复用原 SQL/Ent。新 HTTP 直接调用新用例。旧 `applyClientModel` 只留 `repository/usage_client_model_legacy.go`，S11 |
| usage 写入结果与批处理 | `usage/usage_log_create_result.go`、`usage/postgres/usage_log_repo_insert.go`；插入/重复/确定未写/结果不明保留，异步及同步兜底一份 | 普通记录写入、新 HTTP；结算命令与 UsageRecordWorkerPool 仍由旧 gateway 完成链拥有，S11。日志失败不触发再次扣款 |
| Dashboard、分析状态及清理 | `usage/dashboard_service.go`、`dashboard_aggregation_service.go`、`usage_cleanup_service.go`、`aggregation_state.go`；SQL 在 usage/postgres | app 同一实例绑定 worker；旧包装仅 Options/锁/展示转换。SaveUsageAnalyticsState 全量保存仅用于已有数据库夹具，不是核心端口或生产写入口，S15 清理测试兼容 |
| 资金去重归档 | `billing/postgres/usage_dedup_retention.go` | app 注入 usage 聚合器回调；原单条 SQL 先归档后删及 10000 批次保留。usage 无资金去重表写权限 |
| 用户排序与最近活动 | `usage/postgres/query/identity.go` | identity/postgres 在原 Ent 主查询内组合排序，最近活动批量读；不做分页后排序。identity 仍拥有用户权限和原事务 |
| Key 最近 IP / 用量合计 | `usage/postgres/query/keys.go`、`usage/postgres/key_totals.go` | apikey/postgres 使用原 executor；旧 repository 只委托；保留空批次直接返回和旧 SQL 形状 |
| 团队用量 | `usage/postgres/query/team.go` | team/postgres 传入原连接，权限仍由 team 决定；不扩大用量领域事务 |
| 账号及窗口统计 | `app/account_usage_statistics.go`、`app/usage_window_stats.go` 对 usage 直接投影；`account/postgres/ops_projection.go` 保留轻量账号负载 SQL | account/routing、scheduler/billing 的旧入口使用显式统计源；`legacybridge/account_usage_statistics.go` 只剩 OAuthUsagePlatformOptions，S09。不迁账号管理或供应商用量解析 |
| 用户/管理员用量及 Dashboard HTTP | `usage/httpapi`、`usage/httpapi/admin`、`usage/httpapi/dto` | Wire 直接新 handler；旧 handler/dto 只做类型/字段兼容，S15/S16。原 JSON、CSV、过滤、排序、成本字段边界保持 |
| 公共 `/v1/usage` | `usage/httpapi/PublicUsageHandler`；`billing/public_view.go` 拥有资金展示值及剩余额度规则 | app 绑定 identity/apikey/billing/usage；`legacybridge/usage_context.go` 仅原请求 context 投影，S11。原 Gateway.Usage 仅委托；最小路由夹具保留旧 handler fallback |
| 查询缓存机制 | `pkg/querycache` 只拥有 TTL/singleflight/独立副本；usage 的 dashboard/usage 数据缓存及 ops/dashboard_snapshot 分别持有 | HTTP 仅拥有 ETag/304/Header；`server/httpx.SnapshotCache` 委托机制。保留各实例作用域、Get/Set 与单飞差异；没有共享成一个缓存 |
| Dashboard Redis / release Redis | `usage/rediscache/dashboard.go`、`ops/rediscache/update_cache.go` | 原 key、prefix、TTL、序列化；旧 repository 构造转接，S15/S16 |
| 预聚合设置 | `settings/preaggregation/settings.go`、`runtime_status.go` | app 单次构造，usage/ops 读取各自投影；原键 JSON/15 秒缓存/通知不变。SettingService 的其他业务解析仍在所属旧用例 |
| audit 记录、脱敏及队列 | `audit/types.go`、`redact.go`、`service.go`、`audit/postgres/store.go` | app 注入现有账号/支付敏感字段清单；旧 audit service/type 只兼容；支付/退款专项审计不转入普通队列，S12 |
| audit HTTP 与中间件 | `audit/httpapi/handler.go`、`middleware.go` | 原顺序/body 恢复/动作捕获；清空拒绝管理员 Key，仍校验原 TOTP。旧 middleware/admin 仅委托，S15/S16 |
| Ops 查询、设置、错误、指标、SLA/趋势/直方图 | `ops` 核心及 `ops/postgres` 唯一 SQL | app 提供 account/identity/scheduler/apikey 只读端口；旧 OpsService 类型兼容，不持有四个无用 Gateway 指针或第二缓存 |
| Ops 系统日志与错误采集 | `ops/system_log_sink.go`、`ops/error_queue.go`；日志值 `pkg/logevent` | app 绑定实际唯一 sink/queue；handler 的旧 free functions 只委托。`handler/ops_error_logger.go` 的 Gin context/供应商解析/重试观察仍归 S09/S11 |
| 采集主机及连接池 | `ops/provider/host.go`；业务统计 SQL `ops/postgres/collector_queries.go` | `OpsMetricsCollector` 仅用接口；技术 logging/timing 不读业务表。测试分拆主机/SQL/原网关断言 |
| 告警、报表、清理、运行聚合 | `ops/ops_*_service.go`；Redis runtime 与 PostgreSQL cleanup/advisory Adapter | `app/legacybridge/ops_notifications.go` 只转接邮件策略，S10；备份、系统操作锁及维护 heartbeat 原所属 S14，不挪规则进 bridge |
| Ops 实时 | `ops/realtime_runtime.go`、`ops/ops_realtime.go` 持有采样/连接计数/空闲停止；`ops/httpapi/ops_ws_handler.go` 拥有握手/Origin/帧 | app 停止实际 OpsService.Realtime；旧全局 WS runtime 删除。按需启动，诊断 payload 每请求独立 |
| 发布查询与版本 | `ops/release_query.go`、`ops/provider/github_release.go` | app 只有一个 ReleaseQuery/cache；旧 UpdateService 保留下载/校验/替换/回滚/操作锁，S14。provider 下载能力暂供旧维护使用，未迁维护编排 |
| 历史入口拒绝清理命令 | `ops/historical_ingress_cleanup.go`、`ops/postgres/historical_ingress_cleanup.go` | cmd 仅 flags/bootstrap/output，dry-run 默认、分类版本及摘要不变；无完整 worker，S14 继续精简命令装配 |

## 混合文件按符号保留的边界

- `handler/ops_error_logger.go`：全局队列状态和 worker/flush/sanitize 所有权改为 `ops.ErrorLogQueue`；HTTP 元信息键、供应商错误分类、记录时点保留。当前 `ops_error_queue_legacy.go` 仅消费者接口和 app 绑定。构造 fallback 只服务未装配旧入口，不在生产图建立第二队列。
- `handler/gateway_handler.go`：仅公共 Usage 及其展示 helper 迁移；请求转发、取消、重试、完成任务仍属于 S09/S11。额度 helper 的旧单测入口仅存于 `s08_compat_unit_test.go`。
- `service/update_service.go`：CheckUpdate、版本比较、可回滚版本列表及查询缓存由 ReleaseQuery 实现；真正执行更新/回滚不迁。`backup_service.go` 只改共享维护错误值，备份算法不迁。
- `service/gemini_messages_compat_service.go`：只把查询凭据遮罩委托 `pkg/logredact.SanitizeUpstreamQueries`；供应商报文、流状态及取消保持原执行层。
- `service/notification_email_service.go`：报表占位符读取 Ops 的静态列表；模板、收件人与发送规则仍在通知。`domain_constants.go` 只将已迁 Ops 常量别名化；其他阶段常量不算 S08 迁移。
- `service/setting_service.go`：排行纯限制/规范化委托 usage；动态设置业务校验、敏感处理和原回调保持。
- `repository/ops_write_pressure_integration_test.go`：只迁 Ops 写入测试，scheduler outbox 原测试留原文件。`service/usage_record_submit_task_test.go` 是资金完成任务测试，保留原文件及内容。
- `repository/usage_billing_repo_integration_test.go`：原同步写入 FK 锁断言改用传原 executor 的委托入口，未让异步批处理延迟制造通过；原断言保留。
- 无消费者的旧私有包装删除；仍供单测调用的标量 helper 移入对应 unit/integration 测试文件。删除的 unchecked 类型断言来自构造器返回具体 Store，未削弱行为断言。

## 事务与资金写权限

| 操作 | 拥有者与连接 | 保证 / 不扩大范围 |
| --- | --- | --- |
| usage 普通 Create / BestEffort | usage/postgres 原 SQL，批处理/重试/兜底 | 结果不明与失败区分；原取消窗口、容量、冲突语义 |
| 外层 Ent Tx 的 usage 写入 | 原 Ent context → 同一驱动连接，绕过异步队列 | Adapter 不提交外层事务；后续记录失败整体回滚 |
| 手工/后台聚合状态 B01 | usage/postgres 短事务锁定唯一状态行 | 按所有权更新；手工目标/游标条件匹配才推进/清除；聚合 SQL 期间不持锁 |
| 清理分析记录 B05 | 原批量删除独立提交，收尾登记异步重算 | 已删除后取消仍修正统计；不退款、不修改余额/订阅/结算；无持久修复队列 |
| 归档资金去重 | billing/postgres，app 注入 | 原 archive-before-delete SQL 一次执行；usage 不直接拥有该表 |
| 审计清空 B02 | audit/postgres ClearWithTrace 同一 SQL Tx | 表锁 → 计数 → TRUNCATE → 留痕 → commit，失败全回滚，不重启序列 |
| 普通 audit、Ops 清理留痕 | 原尽力异步/原尽力同步入口 | 不变成交易提交条件；支付专项审计及退款原事务独立保留 |
| 用户/团队/Key 查询参与 | 原用户主查询/原 executor | 不增加提交点，不改变权限或分页排序 |

## 后续阶段退出项

| 阶段 | 明确剩余责任 |
| --- | --- |
| S09 | 供应商 usage/error 解析、HTTP context 输入、OAuth 平台选项与取消/重试策略；将窄观测投影接入各 upstream |
| S10 | 通知邮件模板、收件人、限流及 Ops 告警/报表投影桥接；账号/支付敏感字段注册来源按所属模块直接绑定 |
| S11 | gateway 完成 worker、请求 ID 和供应商归一化、资金失败后的记录时点、旧 client model context、公共 usage context、错误队列绑定入口 |
| S12 | 支付/退款专项审计的原闭合事务，不迁为可丢弃记录 |
| S13 | creative/batchimage 完成编排与旧 UsageLog 形状消费；资金操作继续 billing |
| S14 | binary update/rollback、备份、维护锁、setup/CLI 剩余装配和原设置领域解析 |
| S15/S16 | 别名/旧 DTO/构造器/测试 helper 消费者清零后删除；重新核对精确 import 许可，不继承旧文件例外 |

原 S07 outbox 迟提交事件仍可能越过水位，依赖周期全量重建恢复；十秒清理宽限不保证再次轮询。本阶段不改变此边界，也不扩大多实例部署支持。
