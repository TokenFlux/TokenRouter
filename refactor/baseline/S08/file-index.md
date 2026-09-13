# S08 文件与消费者索引

逐符号归属、真实静态调用、构建集合、接口候选实现和 Wire 引用见 [完整账本](ownership-ledger.json.gz)、[调用边](consumer-edges.json.gz)、[接口实现](interface-implementations.json.gz)。测试名及实际事件记录在符号账本和 [测试事件索引](test-contract-events.json.gz)。

| 文件 | 所有者 / 角色 | 符号数 | 后续阶段 |
| --- | --- | ---: | --- |
| [backend/cmd/cleanup-ingress-reject-logs/main.go](../../../backend/cmd/cleanup-ingress-reject-logs/main.go) | app/CLI / 精简命令装配与原参数输出 | 1 | S14 |
| [backend/internal/account/ops_projection.go](../../../backend/internal/account/ops_projection.go) | account / 原业务/原事务；用量 SQL 参与和观测只读投影改绑 | 1 | 已迁模块；兼容 S15/S16 |
| [backend/internal/account/postgres/ops_projection.go](../../../backend/internal/account/postgres/ops_projection.go) | account / 原业务/原事务；用量 SQL 参与和观测只读投影改绑 | 1 | 已迁模块；兼容 S15/S16 |
| [backend/internal/apikey/postgres/key_store.go](../../../backend/internal/apikey/postgres/key_store.go) | apikey / 原业务/原事务；用量 SQL 参与和观测只读投影改绑 | 50 | 已迁模块；兼容 S15/S16 |
| [backend/internal/app/account_oauth_usage.go](../../../backend/internal/app/account_oauth_usage.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 2 | S08 |
| [backend/internal/app/account_usage_statistics.go](../../../backend/internal/app/account_usage_statistics.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 8 | S08 |
| [backend/internal/app/error_queue.go](../../../backend/internal/app/error_queue.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 2 | S08 |
| [backend/internal/app/legacy_providers.go](../../../backend/internal/app/legacy_providers.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 8 | S08 |
| [backend/internal/app/legacy_runtime_ops.go](../../../backend/internal/app/legacy_runtime_ops.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 2 | S08 |
| [backend/internal/app/legacy_runtime_queues.go](../../../backend/internal/app/legacy_runtime_queues.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 2 | S08 |
| [backend/internal/app/legacybridge/account_usage_statistics.go](../../../backend/internal/app/legacybridge/account_usage_statistics.go) | account/upstream 过渡投影 / 仅剩 OAuth 平台参数；统计已由 app 直绑 usage | 1 | S09 |
| [backend/internal/app/legacybridge/ops_notifications.go](../../../backend/internal/app/legacybridge/ops_notifications.go) | notification 过渡投影 / 邮件策略调用与值投影；无缓存/规则 | 1 | S10 |
| [backend/internal/app/legacybridge/usage_context.go](../../../backend/internal/app/legacybridge/usage_context.go) | gateway 过渡投影 / 读取旧 HTTP context 的公开展示输入 | 1 | S11 |
| [backend/internal/app/observability_foundation.go](../../../backend/internal/app/observability_foundation.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 5 | S08 |
| [backend/internal/app/ops.go](../../../backend/internal/app/ops.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 12 | S08 |
| [backend/internal/app/ops_projections.go](../../../backend/internal/app/ops_projections.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 9 | S08 |
| [backend/internal/app/ops_wire.go](../../../backend/internal/app/ops_wire.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 1 | S08 |
| [backend/internal/app/process_integration_test.go](../../../backend/internal/app/process_integration_test.go) | app / 测试/夹具 | 10 | S08 |
| [backend/internal/app/public_usage.go](../../../backend/internal/app/public_usage.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 1 | S08 |
| [backend/internal/app/routing_model_list.go](../../../backend/internal/app/routing_model_list.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 2 | S08 |
| [backend/internal/app/usage.go](../../../backend/internal/app/usage.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 12 | S08 |
| [backend/internal/app/usage_http.go](../../../backend/internal/app/usage_http.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 5 | S08 |
| [backend/internal/app/usage_window_stats.go](../../../backend/internal/app/usage_window_stats.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 3 | S08 |
| [backend/internal/app/wire.go](../../../backend/internal/app/wire.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 1 | S08 |
| [backend/internal/app/wire_gen.go](../../../backend/internal/app/wire_gen.go) | app / 唯一构造/Options/跨模块投影/生命周期/Wire | 1 | S08 |
| [backend/internal/audit/httpapi/handler.go](../../../backend/internal/audit/httpapi/handler.go) | audit / HTTP/DTO/ETag/帧 Adapter | 7 | S08 |
| [backend/internal/audit/httpapi/middleware.go](../../../backend/internal/audit/httpapi/middleware.go) | audit / HTTP/DTO/ETag/帧 Adapter | 18 | S08 |
| [backend/internal/audit/httpapi/middleware_test.go](../../../backend/internal/audit/httpapi/middleware_test.go) | audit / 测试/夹具 | 22 | S08 |
| [backend/internal/audit/postgres/store.go](../../../backend/internal/audit/postgres/store.go) | audit / 同连接 SQL/Ent Adapter | 15 | S08 |
| [backend/internal/audit/redact.go](../../../backend/internal/audit/redact.go) | audit / 核心值/用例/只读端口 | 11 | S08 |
| [backend/internal/audit/s08_lifecycle_test.go](../../../backend/internal/audit/s08_lifecycle_test.go) | audit / 测试/夹具 | 3 | S08 |
| [backend/internal/audit/service.go](../../../backend/internal/audit/service.go) | audit / 核心值/用例/只读端口 | 19 | S08 |
| [backend/internal/audit/types.go](../../../backend/internal/audit/types.go) | audit / 核心值/用例/只读端口 | 17 | S08 |
| [backend/internal/billing/postgres/usage_dedup_retention.go](../../../backend/internal/billing/postgres/usage_dedup_retention.go) | billing / 权益展示/资金去重归档；不由 usage 写资金表 | 2 | S08 |
| [backend/internal/billing/public_view.go](../../../backend/internal/billing/public_view.go) | billing / 权益展示/资金去重归档；不由 usage 写资金表 | 2 | S08 |
| [backend/internal/handler/admin/account_handler.go](../../../backend/internal/handler/admin/account_handler.go) | account HTTP 过渡 / 原账号 HTTP 与新 usage 统计投影 | 78 | S09/S15/S16 |
| [backend/internal/handler/admin/admin_helpers_test.go](../../../backend/internal/handler/admin/admin_helpers_test.go) | HTTP 兼容 / 测试/夹具 | 3 | S15/S16 |
| [backend/internal/handler/admin/audit_log_handler.go](../../../backend/internal/handler/admin/audit_log_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 2 | S15/S16 |
| [backend/internal/handler/admin/dashboard_handler.go](../../../backend/internal/handler/admin/dashboard_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 2 | S15/S16 |
| [backend/internal/handler/admin/ops_alerts_handler.go](../../../backend/internal/handler/admin/ops_alerts_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 3 | S15/S16 |
| [backend/internal/handler/admin/ops_dashboard_handler.go](../../../backend/internal/handler/admin/ops_dashboard_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 1 | S15/S16 |
| [backend/internal/handler/admin/ops_handler.go](../../../backend/internal/handler/admin/ops_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 2 | S15/S16 |
| [backend/internal/handler/admin/ops_ws_handler.go](../../../backend/internal/handler/admin/ops_ws_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 3 | S15/S16 |
| [backend/internal/handler/admin/s08_compat_unit_test.go](../../../backend/internal/handler/admin/s08_compat_unit_test.go) | HTTP 兼容 / 测试/夹具 | 1 | S15/S16 |
| [backend/internal/handler/admin/usage_handler.go](../../../backend/internal/handler/admin/usage_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 5 | S15/S16 |
| [backend/internal/handler/dto/mappers.go](../../../backend/internal/handler/dto/mappers.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 31 | S15/S16 |
| [backend/internal/handler/dto/types.go](../../../backend/internal/handler/dto/types.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 33 | S15/S16 |
| [backend/internal/handler/dto/usage_compat_test.go](../../../backend/internal/handler/dto/usage_compat_test.go) | HTTP 兼容 / 测试/夹具 | 1 | S15/S16 |
| [backend/internal/handler/gateway_handler.go](../../../backend/internal/handler/gateway_handler.go) | gateway HTTP / 原执行/重试/资金完成；公共 usage 已委托新 handler | 58 | S09/S11 |
| [backend/internal/handler/handler.go](../../../backend/internal/handler/handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 3 | S15/S16 |
| [backend/internal/handler/ops_error_logger.go](../../../backend/internal/handler/ops_error_logger.go) | gateway HTTP 观察输入 / HTTP context 捕获/供应商错误分类/重试观察 | 121 | S09/S11 |
| [backend/internal/handler/ops_error_logger_test.go](../../../backend/internal/handler/ops_error_logger_test.go) | HTTP 兼容 / 测试/夹具 | 68 | S15/S16 |
| [backend/internal/handler/ops_error_queue_legacy.go](../../../backend/internal/handler/ops_error_queue_legacy.go) | gateway 兼容绑定 / 唯一 app 队列注入；无第二份写入算法 | 15 | S11/S16 |
| [backend/internal/handler/ops_queue_capture_fixture_test.go](../../../backend/internal/handler/ops_queue_capture_fixture_test.go) | HTTP 兼容 / 测试/夹具 | 8 | S15/S16 |
| [backend/internal/handler/public_usage_legacy.go](../../../backend/internal/handler/public_usage_legacy.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 1 | S15/S16 |
| [backend/internal/handler/s08_compat_unit_test.go](../../../backend/internal/handler/s08_compat_unit_test.go) | HTTP 兼容 / 测试/夹具 | 1 | S15/S16 |
| [backend/internal/handler/usage_handler.go](../../../backend/internal/handler/usage_handler.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 3 | S15/S16 |
| [backend/internal/handler/wire.go](../../../backend/internal/handler/wire.go) | HTTP 兼容 / 别名/DTO 字段投影/新模块入口委托；其他原字段不迁 | 9 | S15/S16 |
| [backend/internal/identity/postgres/user_repo.go](../../../backend/internal/identity/postgres/user_repo.go) | identity / 原业务/原事务；用量 SQL 参与和观测只读投影改绑 | 86 | 已迁模块；兼容 S15/S16 |
| [backend/internal/infra/postgres/values.go](../../../backend/internal/infra/postgres/values.go) | infra / 技术日志类型/SQL 值工具；不读业务表 | 4 | S08 |
| [backend/internal/infra/telemetry/logging/logger.go](../../../backend/internal/infra/telemetry/logging/logger.go) | infra / 技术日志类型/SQL 值工具；不读业务表 | 53 | S08 |
| [backend/internal/ops/auth_health.go](../../../backend/internal/ops/auth_health.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/cleanup_defaults.go](../../../backend/internal/ops/cleanup_defaults.go) | ops / 核心值/用例/只读端口 | 6 | S08 |
| [backend/internal/ops/cleanup_policy.go](../../../backend/internal/ops/cleanup_policy.go) | ops / 核心值/用例/只读端口 | 8 | S08 |
| [backend/internal/ops/cleanup_policy_test.go](../../../backend/internal/ops/cleanup_policy_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/cleanup_ports.go](../../../backend/internal/ops/cleanup_ports.go) | ops / 核心值/用例/只读端口 | 4 | S08 |
| [backend/internal/ops/collector_ports.go](../../../backend/internal/ops/collector_ports.go) | ops / 核心值/用例/只读端口 | 5 | S08 |
| [backend/internal/ops/dashboard_snapshot.go](../../../backend/internal/ops/dashboard_snapshot.go) | ops / 核心值/用例/只读端口 | 3 | S08 |
| [backend/internal/ops/error_queue.go](../../../backend/internal/ops/error_queue.go) | ops / 核心值/用例/只读端口 | 41 | S08 |
| [backend/internal/ops/error_queue_test.go](../../../backend/internal/ops/error_queue_test.go) | ops / 测试/夹具 | 11 | S08 |
| [backend/internal/ops/historical_ingress_cleanup.go](../../../backend/internal/ops/historical_ingress_cleanup.go) | ops / 核心值/用例/只读端口 | 6 | S08 |
| [backend/internal/ops/historical_ingress_cleanup_test.go](../../../backend/internal/ops/historical_ingress_cleanup_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/httpapi/helper_contract_test.go](../../../backend/internal/ops/httpapi/helper_contract_test.go) | ops / 测试/夹具 | 10 | S08 |
| [backend/internal/ops/httpapi/ops_alerts_handler.go](../../../backend/internal/ops/httpapi/ops_alerts_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 20 | S08 |
| [backend/internal/ops/httpapi/ops_auth_cache_health_handler.go](../../../backend/internal/ops/httpapi/ops_auth_cache_health_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 1 | S08 |
| [backend/internal/ops/httpapi/ops_dashboard_handler.go](../../../backend/internal/ops/httpapi/ops_dashboard_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 14 | S08 |
| [backend/internal/ops/httpapi/ops_handler.go](../../../backend/internal/ops/httpapi/ops_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 25 | S08 |
| [backend/internal/ops/httpapi/ops_ingress_reject_handler.go](../../../backend/internal/ops/httpapi/ops_ingress_reject_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 11 | S08 |
| [backend/internal/ops/httpapi/ops_ingress_reject_handler_test.go](../../../backend/internal/ops/httpapi/ops_ingress_reject_handler_test.go) | ops / 测试/夹具 | 3 | S08 |
| [backend/internal/ops/httpapi/ops_realtime_handler.go](../../../backend/internal/ops/httpapi/ops_realtime_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 8 | S08 |
| [backend/internal/ops/httpapi/ops_realtime_handler_test.go](../../../backend/internal/ops/httpapi/ops_realtime_handler_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/httpapi/ops_runtime_logging_handler_test.go](../../../backend/internal/ops/httpapi/ops_runtime_logging_handler_test.go) | ops / 测试/夹具 | 14 | S08 |
| [backend/internal/ops/httpapi/ops_settings_handler.go](../../../backend/internal/ops/httpapi/ops_settings_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 11 | S08 |
| [backend/internal/ops/httpapi/ops_snapshot_v2_handler.go](../../../backend/internal/ops/httpapi/ops_snapshot_v2_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 1 | S08 |
| [backend/internal/ops/httpapi/ops_system_log_handler.go](../../../backend/internal/ops/httpapi/ops_system_log_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 4 | S08 |
| [backend/internal/ops/httpapi/ops_system_log_handler_test.go](../../../backend/internal/ops/httpapi/ops_system_log_handler_test.go) | ops / 测试/夹具 | 24 | S08 |
| [backend/internal/ops/httpapi/ops_ws_handler.go](../../../backend/internal/ops/httpapi/ops_ws_handler.go) | ops / HTTP/DTO/ETag/帧 Adapter | 35 | S08 |
| [backend/internal/ops/ignored_status.go](../../../backend/internal/ops/ignored_status.go) | ops / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/ops/ops_account_availability.go](../../../backend/internal/ops/ops_account_availability.go) | ops / 核心值/用例/只读端口 | 3 | S08 |
| [backend/internal/ops/ops_aggregation_service.go](../../../backend/internal/ops/ops_aggregation_service.go) | ops / 核心值/用例/只读端口 | 36 | S08 |
| [backend/internal/ops/ops_alert_evaluator_service.go](../../../backend/internal/ops/ops_alert_evaluator_service.go) | ops / 核心值/用例/只读端口 | 55 | S08 |
| [backend/internal/ops/ops_alert_evaluator_service_test.go](../../../backend/internal/ops/ops_alert_evaluator_service_test.go) | ops / 测试/夹具 | 6 | S08 |
| [backend/internal/ops/ops_alert_models.go](../../../backend/internal/ops/ops_alert_models.go) | ops / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/ops/ops_alerts.go](../../../backend/internal/ops/ops_alerts.go) | ops / 核心值/用例/只读端口 | 13 | S08 |
| [backend/internal/ops/ops_cleanup_overlay_test.go](../../../backend/internal/ops/ops_cleanup_overlay_test.go) | ops / 测试/夹具 | 16 | S08 |
| [backend/internal/ops/ops_cleanup_service.go](../../../backend/internal/ops/ops_cleanup_service.go) | ops / 核心值/用例/只读端口 | 19 | S08 |
| [backend/internal/ops/ops_concurrency.go](../../../backend/internal/ops/ops_concurrency.go) | ops / 核心值/用例/只读端口 | 9 | S08 |
| [backend/internal/ops/ops_concurrency_test.go](../../../backend/internal/ops/ops_concurrency_test.go) | ops / 测试/夹具 | 7 | S08 |
| [backend/internal/ops/ops_dashboard.go](../../../backend/internal/ops/ops_dashboard.go) | ops / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/ops/ops_dashboard_models.go](../../../backend/internal/ops/ops_dashboard_models.go) | ops / 核心值/用例/只读端口 | 6 | S08 |
| [backend/internal/ops/ops_errors.go](../../../backend/internal/ops/ops_errors.go) | ops / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/ops/ops_health_score.go](../../../backend/internal/ops/ops_health_score.go) | ops / 核心值/用例/只读端口 | 8 | S08 |
| [backend/internal/ops/ops_health_score_test.go](../../../backend/internal/ops/ops_health_score_test.go) | ops / 测试/夹具 | 7 | S08 |
| [backend/internal/ops/ops_histograms.go](../../../backend/internal/ops/ops_histograms.go) | ops / 核心值/用例/只读端口 | 6 | S08 |
| [backend/internal/ops/ops_histograms_test.go](../../../backend/internal/ops/ops_histograms_test.go) | ops / 测试/夹具 | 5 | S08 |
| [backend/internal/ops/ops_ingress_reject.go](../../../backend/internal/ops/ops_ingress_reject.go) | ops / 核心值/用例/只读端口 | 39 | S08 |
| [backend/internal/ops/ops_ingress_reject_test.go](../../../backend/internal/ops/ops_ingress_reject_test.go) | ops / 测试/夹具 | 6 | S08 |
| [backend/internal/ops/ops_log_runtime.go](../../../backend/internal/ops/ops_log_runtime.go) | ops / 核心值/用例/只读端口 | 13 | S08 |
| [backend/internal/ops/ops_metrics_collector.go](../../../backend/internal/ops/ops_metrics_collector.go) | ops / 核心值/用例/只读端口 | 21 | S08 |
| [backend/internal/ops/ops_metrics_collector_projection_test.go](../../../backend/internal/ops/ops_metrics_collector_projection_test.go) | ops / 测试/夹具 | 9 | S08 |
| [backend/internal/ops/ops_models.go](../../../backend/internal/ops/ops_models.go) | ops / 核心值/用例/只读端口 | 9 | S08 |
| [backend/internal/ops/ops_models_test.go](../../../backend/internal/ops/ops_models_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/ops_port.go](../../../backend/internal/ops/ops_port.go) | ops / 核心值/用例/只读端口 | 12 | S08 |
| [backend/internal/ops/ops_query_mode.go](../../../backend/internal/ops/ops_query_mode.go) | ops / 核心值/用例/只读端口 | 10 | S08 |
| [backend/internal/ops/ops_query_mode_test.go](../../../backend/internal/ops/ops_query_mode_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/ops_queue_sanitize_test.go](../../../backend/internal/ops/ops_queue_sanitize_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/ops_realtime.go](../../../backend/internal/ops/ops_realtime.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_realtime_models.go](../../../backend/internal/ops/ops_realtime_models.go) | ops / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/ops/ops_realtime_traffic.go](../../../backend/internal/ops/ops_realtime_traffic.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_realtime_traffic_models.go](../../../backend/internal/ops/ops_realtime_traffic_models.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_request_details.go](../../../backend/internal/ops/ops_request_details.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_request_timings.go](../../../backend/internal/ops/ops_request_timings.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_runtime_snapshot_test.go](../../../backend/internal/ops/ops_runtime_snapshot_test.go) | ops / 测试/夹具 | 12 | S08 |
| [backend/internal/ops/ops_scheduled_report_service.go](../../../backend/internal/ops/ops_scheduled_report_service.go) | ops / 核心值/用例/只读端口 | 49 | S08 |
| [backend/internal/ops/ops_service.go](../../../backend/internal/ops/ops_service.go) | ops / 核心值/用例/只读端口 | 61 | S08 |
| [backend/internal/ops/ops_service_batch_test.go](../../../backend/internal/ops/ops_service_batch_test.go) | ops / 测试/夹具 | 5 | S08 |
| [backend/internal/ops/ops_service_redaction_test.go](../../../backend/internal/ops/ops_service_redaction_test.go) | ops / 测试/夹具 | 3 | S08 |
| [backend/internal/ops/ops_service_user_error_test.go](../../../backend/internal/ops/ops_service_user_error_test.go) | ops / 测试/夹具 | 7 | S08 |
| [backend/internal/ops/ops_settings.go](../../../backend/internal/ops/ops_settings.go) | ops / 核心值/用例/只读端口 | 43 | S08 |
| [backend/internal/ops/ops_settings_advanced_test.go](../../../backend/internal/ops/ops_settings_advanced_test.go) | ops / 测试/夹具 | 5 | S08 |
| [backend/internal/ops/ops_settings_models.go](../../../backend/internal/ops/ops_settings_models.go) | ops / 核心值/用例/只读端口 | 13 | S08 |
| [backend/internal/ops/ops_system_log_service.go](../../../backend/internal/ops/ops_system_log_service.go) | ops / 核心值/用例/只读端口 | 5 | S08 |
| [backend/internal/ops/ops_system_log_service_test.go](../../../backend/internal/ops/ops_system_log_service_test.go) | ops / 测试/夹具 | 12 | S08 |
| [backend/internal/ops/ops_system_log_sink_backoff_test.go](../../../backend/internal/ops/ops_system_log_sink_backoff_test.go) | ops / 测试/夹具 | 9 | S08 |
| [backend/internal/ops/ops_system_log_sink_test.go](../../../backend/internal/ops/ops_system_log_sink_test.go) | ops / 测试/夹具 | 10 | S08 |
| [backend/internal/ops/ops_token_stats.go](../../../backend/internal/ops/ops_token_stats.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_token_stats_models.go](../../../backend/internal/ops/ops_token_stats_models.go) | ops / 核心值/用例/只读端口 | 4 | S08 |
| [backend/internal/ops/ops_token_stats_test.go](../../../backend/internal/ops/ops_token_stats_test.go) | ops / 测试/夹具 | 6 | S08 |
| [backend/internal/ops/ops_trend_models.go](../../../backend/internal/ops/ops_trend_models.go) | ops / 核心值/用例/只读端口 | 8 | S08 |
| [backend/internal/ops/ops_trends.go](../../../backend/internal/ops/ops_trends.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_user_error.go](../../../backend/internal/ops/ops_user_error.go) | ops / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/ops/ops_user_error_test.go](../../../backend/internal/ops/ops_user_error_test.go) | ops / 测试/夹具 | 5 | S08 |
| [backend/internal/ops/ops_window_stats.go](../../../backend/internal/ops/ops_window_stats.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/ops/ops_ws_lifecycle_test.go](../../../backend/internal/ops/ops_ws_lifecycle_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/options.go](../../../backend/internal/ops/options.go) | ops / 核心值/用例/只读端口 | 30 | S08 |
| [backend/internal/ops/postgres/advisory.go](../../../backend/internal/ops/postgres/advisory.go) | ops / 同连接 SQL/Ent Adapter | 3 | S08 |
| [backend/internal/ops/postgres/cleanup.go](../../../backend/internal/ops/postgres/cleanup.go) | ops / 同连接 SQL/Ent Adapter | 12 | S08 |
| [backend/internal/ops/postgres/collector_queries.go](../../../backend/internal/ops/postgres/collector_queries.go) | ops / 同连接 SQL/Ent Adapter | 11 | S08 |
| [backend/internal/ops/postgres/collector_queries_test.go](../../../backend/internal/ops/postgres/collector_queries_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/historical_ingress_cleanup.go](../../../backend/internal/ops/postgres/historical_ingress_cleanup.go) | ops / 同连接 SQL/Ent Adapter | 4 | S08 |
| [backend/internal/ops/postgres/historical_ingress_cleanup_integration_test.go](../../../backend/internal/ops/postgres/historical_ingress_cleanup_integration_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/integration_harness_test.go](../../../backend/internal/ops/postgres/integration_harness_test.go) | ops / 测试/夹具 | 28 | S08 |
| [backend/internal/ops/postgres/ops_cleanup_service_test.go](../../../backend/internal/ops/postgres/ops_cleanup_service_test.go) | ops / 测试/夹具 | 6 | S08 |
| [backend/internal/ops/postgres/ops_error_where_test.go](../../../backend/internal/ops/postgres/ops_error_where_test.go) | ops / 测试/夹具 | 4 | S08 |
| [backend/internal/ops/postgres/ops_ingress_reject_repo.go](../../../backend/internal/ops/postgres/ops_ingress_reject_repo.go) | ops / 同连接 SQL/Ent Adapter | 3 | S08 |
| [backend/internal/ops/postgres/ops_ingress_reject_repo_test.go](../../../backend/internal/ops/postgres/ops_ingress_reject_repo_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo.go](../../../backend/internal/ops/postgres/ops_repo.go) | ops / 同连接 SQL/Ent Adapter | 25 | S08 |
| [backend/internal/ops/postgres/ops_repo_alerts.go](../../../backend/internal/ops/postgres/ops_repo_alerts.go) | ops / 同连接 SQL/Ent Adapter | 17 | S08 |
| [backend/internal/ops/postgres/ops_repo_args_test.go](../../../backend/internal/ops/postgres/ops_repo_args_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/postgres/ops_repo_dashboard.go](../../../backend/internal/ops/postgres/ops_repo_dashboard.go) | ops / 同连接 SQL/Ent Adapter | 31 | S08 |
| [backend/internal/ops/postgres/ops_repo_dashboard_timeout_test.go](../../../backend/internal/ops/postgres/ops_repo_dashboard_timeout_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_dashboard_ttft_test.go](../../../backend/internal/ops/postgres/ops_repo_dashboard_ttft_test.go) | ops / 测试/夹具 | 3 | S08 |
| [backend/internal/ops/postgres/ops_repo_error_where_test.go](../../../backend/internal/ops/postgres/ops_repo_error_where_test.go) | ops / 测试/夹具 | 6 | S08 |
| [backend/internal/ops/postgres/ops_repo_get_error_log_by_id_integration_test.go](../../../backend/internal/ops/postgres/ops_repo_get_error_log_by_id_integration_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_histograms.go](../../../backend/internal/ops/postgres/ops_repo_histograms.go) | ops / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_latency_histogram_buckets.go](../../../backend/internal/ops/postgres/ops_repo_latency_histogram_buckets.go) | ops / 同连接 SQL/Ent Adapter | 2 | S08 |
| [backend/internal/ops/postgres/ops_repo_latency_histogram_buckets_test.go](../../../backend/internal/ops/postgres/ops_repo_latency_histogram_buckets_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/postgres/ops_repo_metrics.go](../../../backend/internal/ops/postgres/ops_repo_metrics.go) | ops / 同连接 SQL/Ent Adapter | 9 | S08 |
| [backend/internal/ops/postgres/ops_repo_metrics_test.go](../../../backend/internal/ops/postgres/ops_repo_metrics_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/postgres/ops_repo_preagg.go](../../../backend/internal/ops/postgres/ops_repo_preagg.go) | ops / 同连接 SQL/Ent Adapter | 4 | S08 |
| [backend/internal/ops/postgres/ops_repo_preagg_coverage_test.go](../../../backend/internal/ops/postgres/ops_repo_preagg_coverage_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_realtime_traffic.go](../../../backend/internal/ops/postgres/ops_repo_realtime_traffic.go) | ops / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_replay_cleanup_test.go](../../../backend/internal/ops/postgres/ops_repo_replay_cleanup_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_request_details.go](../../../backend/internal/ops/postgres/ops_repo_request_details.go) | ops / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_request_details_test.go](../../../backend/internal/ops/postgres/ops_repo_request_details_test.go) | ops / 测试/夹具 | 2 | S08 |
| [backend/internal/ops/postgres/ops_repo_request_timings.go](../../../backend/internal/ops/postgres/ops_repo_request_timings.go) | ops / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_request_timings_test.go](../../../backend/internal/ops/postgres/ops_repo_request_timings_test.go) | ops / 测试/夹具 | 3 | S08 |
| [backend/internal/ops/postgres/ops_repo_system_logs_test.go](../../../backend/internal/ops/postgres/ops_repo_system_logs_test.go) | ops / 测试/夹具 | 4 | S08 |
| [backend/internal/ops/postgres/ops_repo_token_stats.go](../../../backend/internal/ops/postgres/ops_repo_token_stats.go) | ops / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/ops/postgres/ops_repo_token_stats_test.go](../../../backend/internal/ops/postgres/ops_repo_token_stats_test.go) | ops / 测试/夹具 | 3 | S08 |
| [backend/internal/ops/postgres/ops_repo_trends.go](../../../backend/internal/ops/postgres/ops_repo_trends.go) | ops / 同连接 SQL/Ent Adapter | 11 | S08 |
| [backend/internal/ops/postgres/ops_repo_window_stats.go](../../../backend/internal/ops/postgres/ops_repo_window_stats.go) | ops / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/ops/postgres/ops_sla_sql.go](../../../backend/internal/ops/postgres/ops_sla_sql.go) | ops / 同连接 SQL/Ent Adapter | 5 | S08 |
| [backend/internal/ops/postgres/ops_write_pressure_integration_test.go](../../../backend/internal/ops/postgres/ops_write_pressure_integration_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/sqlmock_test.go](../../../backend/internal/ops/postgres/sqlmock_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/postgres/support.go](../../../backend/internal/ops/postgres/support.go) | ops / 同连接 SQL/Ent Adapter | 2 | S08 |
| [backend/internal/ops/provider/github_release.go](../../../backend/internal/ops/provider/github_release.go) | ops / 技术/外部查询 Adapter | 17 | S08 |
| [backend/internal/ops/provider/github_release_test.go](../../../backend/internal/ops/provider/github_release_test.go) | ops / 测试/夹具 | 30 | S08 |
| [backend/internal/ops/provider/host.go](../../../backend/internal/ops/provider/host.go) | ops / 技术/外部查询 Adapter | 16 | S08 |
| [backend/internal/ops/provider/local_http_fixture_test.go](../../../backend/internal/ops/provider/local_http_fixture_test.go) | ops / 测试/夹具 | 5 | S08 |
| [backend/internal/ops/provider/logging.go](../../../backend/internal/ops/provider/logging.go) | ops / 技术/外部查询 Adapter | 4 | S08 |
| [backend/internal/ops/provider/logging_test.go](../../../backend/internal/ops/provider/logging_test.go) | ops / 测试/夹具 | 29 | S08 |
| [backend/internal/ops/provider/ops_metrics_collector_memory_test.go](../../../backend/internal/ops/provider/ops_metrics_collector_memory_test.go) | ops / 测试/夹具 | 7 | S08 |
| [backend/internal/ops/pure_context_error.go](../../../backend/internal/ops/pure_context_error.go) | ops / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/ops/pure_ops_upstream_context.go](../../../backend/internal/ops/pure_ops_upstream_context.go) | ops / 核心值/用例/只读端口 | 5 | S08 |
| [backend/internal/ops/query_fixture_test.go](../../../backend/internal/ops/query_fixture_test.go) | ops / 测试/夹具 | 6 | S08 |
| [backend/internal/ops/realtime_runtime.go](../../../backend/internal/ops/realtime_runtime.go) | ops / 核心值/用例/只读端口 | 19 | S08 |
| [backend/internal/ops/rediscache/integration_harness_test.go](../../../backend/internal/ops/rediscache/integration_harness_test.go) | ops / 测试/夹具 | 16 | S08 |
| [backend/internal/ops/rediscache/runtime.go](../../../backend/internal/ops/rediscache/runtime.go) | ops / Redis Adapter | 7 | S08 |
| [backend/internal/ops/rediscache/update_cache.go](../../../backend/internal/ops/rediscache/update_cache.go) | ops / Redis Adapter | 5 | S08 |
| [backend/internal/ops/rediscache/update_cache_integration_test.go](../../../backend/internal/ops/rediscache/update_cache_integration_test.go) | ops / 测试/夹具 | 8 | S08 |
| [backend/internal/ops/release_query.go](../../../backend/internal/ops/release_query.go) | ops / 核心值/用例/只读端口 | 26 | S08 |
| [backend/internal/ops/release_query_test.go](../../../backend/internal/ops/release_query_test.go) | ops / 测试/夹具 | 14 | S08 |
| [backend/internal/ops/report_placeholders.go](../../../backend/internal/ops/report_placeholders.go) | ops / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/ops/repository_fixture_test.go](../../../backend/internal/ops/repository_fixture_test.go) | ops / 测试/夹具 | 42 | S08 |
| [backend/internal/ops/request_details_types.go](../../../backend/internal/ops/request_details_types.go) | ops / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/ops/runtime_ports.go](../../../backend/internal/ops/runtime_ports.go) | ops / 核心值/用例/只读端口 | 9 | S08 |
| [backend/internal/ops/s08_lifecycle_test.go](../../../backend/internal/ops/s08_lifecycle_test.go) | ops / 测试/夹具 | 1 | S08 |
| [backend/internal/ops/scalar_helpers.go](../../../backend/internal/ops/scalar_helpers.go) | ops / 核心值/用例/只读端口 | 10 | S08 |
| [backend/internal/ops/setting_keys.go](../../../backend/internal/ops/setting_keys.go) | ops / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/ops/settings_fixture_test.go](../../../backend/internal/ops/settings_fixture_test.go) | ops / 测试/夹具 | 10 | S08 |
| [backend/internal/ops/system_log_sink.go](../../../backend/internal/ops/system_log_sink.go) | ops / 核心值/用例/只读端口 | 21 | S08 |
| [backend/internal/ops/upstream_event.go](../../../backend/internal/ops/upstream_event.go) | ops / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/pkg/logevent/event.go](../../../backend/internal/pkg/logevent/event.go) | pkg/logevent / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/pkg/logredact/upstream.go](../../../backend/internal/pkg/logredact/upstream.go) | pkg/logredact / 纯脱敏字节/字符串规则 | 2 | S08 |
| [backend/internal/pkg/querycache/cache.go](../../../backend/internal/pkg/querycache/cache.go) | pkg/querycache / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/pkg/querycache/cache_test.go](../../../backend/internal/pkg/querycache/cache_test.go) | pkg/querycache / 测试/夹具 | 2 | S08 |
| [backend/internal/pkg/querycache/value.go](../../../backend/internal/pkg/querycache/value.go) | pkg/querycache / 核心值/用例/只读端口 | 2 | S08 |
| [backend/internal/pkg/usagestats/account_stats.go](../../../backend/internal/pkg/usagestats/account_stats.go) | usage 旧别名 / 唯一 usage 值类型兼容入口 | 1 | S15/S16 |
| [backend/internal/pkg/usagestats/usage_log_types.go](../../../backend/internal/pkg/usagestats/usage_log_types.go) | usage 旧别名 / 唯一 usage 值类型兼容入口 | 30 | S15/S16 |
| [backend/internal/repository/account_repo.go](../../../backend/internal/repository/account_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 76 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/api_key_repo.go](../../../backend/internal/repository/api_key_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 39 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/apikey_usage_read.go](../../../backend/internal/repository/apikey_usage_read.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/audit_log_repo.go](../../../backend/internal/repository/audit_log_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/dashboard_aggregation_repo.go](../../../backend/internal/repository/dashboard_aggregation_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/dashboard_cache.go](../../../backend/internal/repository/dashboard_cache.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/error_translate.go](../../../backend/internal/repository/error_translate.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 3 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/github_release_service.go](../../../backend/internal/repository/github_release_service.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/ops_repo.go](../../../backend/internal/repository/ops_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/ops_write_pressure_integration_test.go](../../../backend/internal/repository/ops_write_pressure_integration_test.go) | 旧存储兼容 / 测试/夹具 | 5 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/s08_compat_integration_test.go](../../../backend/internal/repository/s08_compat_integration_test.go) | 旧存储兼容 / 测试/夹具 | 6 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/sqlmock_fixture_test.go](../../../backend/internal/repository/sqlmock_fixture_test.go) | 旧存储兼容 / 测试/夹具 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/update_cache.go](../../../backend/internal/repository/update_cache.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/usage_billing_repo_integration_test.go](../../../backend/internal/repository/usage_billing_repo_integration_test.go) | 旧存储兼容 / 测试/夹具 | 34 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/usage_cleanup_repo.go](../../../backend/internal/repository/usage_cleanup_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/usage_client_model_legacy.go](../../../backend/internal/repository/usage_client_model_legacy.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 1 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/usage_log_repo.go](../../../backend/internal/repository/usage_log_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 38 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/user_subscription_repo.go](../../../backend/internal/repository/user_subscription_repo.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 2 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/repository/wire.go](../../../backend/internal/repository/wire.go) | 旧存储兼容 / 构造/形状委托；未迁资金、平台、事务调用保持 | 5 | S09/S11/S12/S13/S14/S16，按能力账本 |
| [backend/internal/server/httpx/snapshot_cache.go](../../../backend/internal/server/httpx/snapshot_cache.go) | server / 原路由/HTTP 元信息及窄委托 | 10 | S11/S15/S16 |
| [backend/internal/server/httpx/snapshot_cache_test.go](../../../backend/internal/server/httpx/snapshot_cache_test.go) | server / 测试/夹具 | 13 | S11/S15/S16 |
| [backend/internal/server/middleware/api_key_billing.go](../../../backend/internal/server/middleware/api_key_billing.go) | server / 原路由/HTTP 元信息及窄委托 | 6 | S11/S15/S16 |
| [backend/internal/server/middleware/audit_log.go](../../../backend/internal/server/middleware/audit_log.go) | server / 原路由/HTTP 元信息及窄委托 | 8 | S11/S15/S16 |
| [backend/internal/server/routes/gateway.go](../../../backend/internal/server/routes/gateway.go) | server / 原路由/HTTP 元信息及窄委托 | 13 | S11/S15/S16 |
| [backend/internal/service/audit_log.go](../../../backend/internal/service/audit_log.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 26 | S15/S16 |
| [backend/internal/service/audit_log_service.go](../../../backend/internal/service/audit_log_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 2 | S15/S16 |
| [backend/internal/service/auth_cache_invalidation_outbox.go](../../../backend/internal/service/auth_cache_invalidation_outbox.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 8 | S15/S16 |
| [backend/internal/service/backup_service.go](../../../backend/internal/service/backup_service.go) | backup / 原备份规则/状态；仅维护错误别名共享 | 119 | S14 |
| [backend/internal/service/dashboard_aggregation_service.go](../../../backend/internal/service/dashboard_aggregation_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/dashboard_service.go](../../../backend/internal/service/dashboard_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/domain_constants.go](../../../backend/internal/service/domain_constants.go) | 旧契约兼容 / 已迁常量委托所属模块；其他常量原属未迁模块 | 361 | S09—S16，按现有常量消费者 |
| [backend/internal/service/gateway_service.go](../../../backend/internal/service/gateway_service.go) | gateway / 旧执行编排；窗口统计端口由 app 注入 usage | 136 | S09/S11 |
| [backend/internal/service/gemini_messages_compat_service.go](../../../backend/internal/service/gemini_messages_compat_service.go) | upstream/gateway / 原 Gemini 解析/流/重试；脱敏工具委托 | 93 | S09/S11 |
| [backend/internal/service/leader_lock.go](../../../backend/internal/service/leader_lock.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 3 | S15/S16 |
| [backend/internal/service/notification_email_service.go](../../../backend/internal/service/notification_email_service.go) | notification / 邮件模板/收件人/限流；共享报表占位符 | 98 | S10 |
| [backend/internal/service/ops_account_availability.go](../../../backend/internal/service/ops_account_availability.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 1 | S15/S16 |
| [backend/internal/service/ops_advisory_lock.go](../../../backend/internal/service/ops_advisory_lock.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 3 | S15/S16 |
| [backend/internal/service/ops_aggregation_service.go](../../../backend/internal/service/ops_aggregation_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 2 | S15/S16 |
| [backend/internal/service/ops_alert_evaluator_service.go](../../../backend/internal/service/ops_alert_evaluator_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 2 | S15/S16 |
| [backend/internal/service/ops_alert_models.go](../../../backend/internal/service/ops_alert_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 7 | S15/S16 |
| [backend/internal/service/ops_cleanup_service.go](../../../backend/internal/service/ops_cleanup_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 2 | S15/S16 |
| [backend/internal/service/ops_dashboard_models.go](../../../backend/internal/service/ops_dashboard_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/ops_histograms.go](../../../backend/internal/service/ops_histograms.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 2 | S15/S16 |
| [backend/internal/service/ops_ingress_reject.go](../../../backend/internal/service/ops_ingress_reject.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 7 | S15/S16 |
| [backend/internal/service/ops_legacy.go](../../../backend/internal/service/ops_legacy.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 9 | S15/S16 |
| [backend/internal/service/ops_log_runtime_test.go](../../../backend/internal/service/ops_log_runtime_test.go) | 旧服务兼容 / 测试/夹具 | 9 | S15/S16 |
| [backend/internal/service/ops_metrics_collector.go](../../../backend/internal/service/ops_metrics_collector.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 8 | S15/S16 |
| [backend/internal/service/ops_metrics_collector_test.go](../../../backend/internal/service/ops_metrics_collector_test.go) | 旧服务兼容 / 测试/夹具 | 1 | S15/S16 |
| [backend/internal/service/ops_models.go](../../../backend/internal/service/ops_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 7 | S15/S16 |
| [backend/internal/service/ops_port.go](../../../backend/internal/service/ops_port.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 12 | S15/S16 |
| [backend/internal/service/ops_query_mode.go](../../../backend/internal/service/ops_query_mode.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 4 | S15/S16 |
| [backend/internal/service/ops_realtime_models.go](../../../backend/internal/service/ops_realtime_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 7 | S15/S16 |
| [backend/internal/service/ops_realtime_traffic_models.go](../../../backend/internal/service/ops_realtime_traffic_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 1 | S15/S16 |
| [backend/internal/service/ops_request_details.go](../../../backend/internal/service/ops_request_details.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/ops_runtime_legacy.go](../../../backend/internal/service/ops_runtime_legacy.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 3 | S15/S16 |
| [backend/internal/service/ops_scheduled_report_service.go](../../../backend/internal/service/ops_scheduled_report_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 7 | S15/S16 |
| [backend/internal/service/ops_scheduled_report_service_test.go](../../../backend/internal/service/ops_scheduled_report_service_test.go) | 旧服务兼容 / 测试/夹具 | 4 | S15/S16 |
| [backend/internal/service/ops_service.go](../../../backend/internal/service/ops_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 8 | S15/S16 |
| [backend/internal/service/ops_settings.go](../../../backend/internal/service/ops_settings.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/ops_settings_advanced_test.go](../../../backend/internal/service/ops_settings_advanced_test.go) | 旧服务兼容 / 测试/夹具 | 3 | S15/S16 |
| [backend/internal/service/ops_settings_models.go](../../../backend/internal/service/ops_settings_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 13 | S15/S16 |
| [backend/internal/service/ops_system_log_sink.go](../../../backend/internal/service/ops_system_log_sink.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 3 | S15/S16 |
| [backend/internal/service/ops_token_stats_models.go](../../../backend/internal/service/ops_token_stats_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 3 | S15/S16 |
| [backend/internal/service/ops_trend_models.go](../../../backend/internal/service/ops_trend_models.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 8 | S15/S16 |
| [backend/internal/service/ops_upstream_context.go](../../../backend/internal/service/ops_upstream_context.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 54 | S15/S16 |
| [backend/internal/service/ops_user_error.go](../../../backend/internal/service/ops_user_error.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 7 | S15/S16 |
| [backend/internal/service/pre_aggregation_settings.go](../../../backend/internal/service/pre_aggregation_settings.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 9 | S15/S16 |
| [backend/internal/service/s08_compat_unit_test.go](../../../backend/internal/service/s08_compat_unit_test.go) | 旧服务兼容 / 测试/夹具 | 4 | S15/S16 |
| [backend/internal/service/scheduler_window_cost_legacy.go](../../../backend/internal/service/scheduler_window_cost_legacy.go) | gateway / 旧执行编排；窗口统计端口由 app 注入 usage | 8 | S09/S11 |
| [backend/internal/service/setting_service.go](../../../backend/internal/service/setting_service.go) | settings 旧领域解析 / 原业务设置校验/通知；排行值规则委托 usage | 95 | S10/S14/S16 |
| [backend/internal/service/update_service.go](../../../backend/internal/service/update_service.go) | maintenance / 二进制替换/回滚/操作锁；发布查询委托 ops | 27 | S14 |
| [backend/internal/service/update_service_test.go](../../../backend/internal/service/update_service_test.go) | 旧服务兼容 / 测试/夹具 | 12 | S15/S16 |
| [backend/internal/service/usage_analytics_aggregation.go](../../../backend/internal/service/usage_analytics_aggregation.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 3 | S15/S16 |
| [backend/internal/service/usage_cleanup.go](../../../backend/internal/service/usage_cleanup.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 8 | S15/S16 |
| [backend/internal/service/usage_cleanup_service.go](../../../backend/internal/service/usage_cleanup_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 4 | S15/S16 |
| [backend/internal/service/usage_log.go](../../../backend/internal/service/usage_log.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 18 | S15/S16 |
| [backend/internal/service/usage_log_create_result.go](../../../backend/internal/service/usage_log_create_result.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/usage_options_legacy.go](../../../backend/internal/service/usage_options_legacy.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 1 | S15/S16 |
| [backend/internal/service/usage_service.go](../../../backend/internal/service/usage_service.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 23 | S15/S16 |
| [backend/internal/service/usage_view_legacy.go](../../../backend/internal/service/usage_view_legacy.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 6 | S15/S16 |
| [backend/internal/service/wire.go](../../../backend/internal/service/wire.go) | 旧服务兼容 / 别名/构造 Options/字段投影；唯一新模块规则与状态 | 58 | S15/S16 |
| [backend/internal/settings/preaggregation/runtime_status.go](../../../backend/internal/settings/preaggregation/runtime_status.go) | settings/preaggregation / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/settings/preaggregation/settings.go](../../../backend/internal/settings/preaggregation/settings.go) | settings/preaggregation / 核心值/用例/只读端口 | 27 | S08 |
| [backend/internal/team/postgres/team.go](../../../backend/internal/team/postgres/team.go) | team / 原业务/原事务；用量 SQL 参与和观测只读投影改绑 | 48 | 已迁模块；兼容 S15/S16 |
| [backend/internal/usage/account_stats.go](../../../backend/internal/usage/account_stats.go) | usage / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/usage/aggregation_state.go](../../../backend/internal/usage/aggregation_state.go) | usage / 核心值/用例/只读端口 | 13 | S08 |
| [backend/internal/usage/dashboard_aggregation_service.go](../../../backend/internal/usage/dashboard_aggregation_service.go) | usage / 核心值/用例/只读端口 | 51 | S08 |
| [backend/internal/usage/dashboard_aggregation_service_test.go](../../../backend/internal/usage/dashboard_aggregation_service_test.go) | usage / 测试/夹具 | 40 | S08 |
| [backend/internal/usage/dashboard_cached_reports.go](../../../backend/internal/usage/dashboard_cached_reports.go) | usage / 核心值/用例/只读端口 | 4 | S08 |
| [backend/internal/usage/dashboard_query_cache.go](../../../backend/internal/usage/dashboard_query_cache.go) | usage / 核心值/用例/只读端口 | 12 | S08 |
| [backend/internal/usage/dashboard_service.go](../../../backend/internal/usage/dashboard_service.go) | usage / 核心值/用例/只读端口 | 45 | S08 |
| [backend/internal/usage/dashboard_service_test.go](../../../backend/internal/usage/dashboard_service_test.go) | usage / 测试/夹具 | 26 | S08 |
| [backend/internal/usage/httpapi/admin/dashboard_handler.go](../../../backend/internal/usage/httpapi/admin/dashboard_handler.go) | usage / HTTP/DTO/ETag/帧 Adapter | 18 | S08 |
| [backend/internal/usage/httpapi/admin/dashboard_handler_cache_test.go](../../../backend/internal/usage/httpapi/admin/dashboard_handler_cache_test.go) | usage / 测试/夹具 | 7 | S08 |
| [backend/internal/usage/httpapi/admin/dashboard_handler_request_type_test.go](../../../backend/internal/usage/httpapi/admin/dashboard_handler_request_type_test.go) | usage / 测试/夹具 | 14 | S08 |
| [backend/internal/usage/httpapi/admin/dashboard_handler_user_breakdown_test.go](../../../backend/internal/usage/httpapi/admin/dashboard_handler_user_breakdown_test.go) | usage / 测试/夹具 | 17 | S08 |
| [backend/internal/usage/httpapi/admin/dashboard_query_cache.go](../../../backend/internal/usage/httpapi/admin/dashboard_query_cache.go) | usage / HTTP/DTO/ETag/帧 Adapter | 6 | S08 |
| [backend/internal/usage/httpapi/admin/dashboard_snapshot_v2_handler.go](../../../backend/internal/usage/httpapi/admin/dashboard_snapshot_v2_handler.go) | usage / HTTP/DTO/ETag/帧 Adapter | 8 | S08 |
| [backend/internal/usage/httpapi/admin/legacy_fixture_test.go](../../../backend/internal/usage/httpapi/admin/legacy_fixture_test.go) | usage / 测试/夹具 | 3 | S08 |
| [backend/internal/usage/httpapi/admin/snapshot_cache.go](../../../backend/internal/usage/httpapi/admin/snapshot_cache.go) | usage / HTTP/DTO/ETag/帧 Adapter | 5 | S08 |
| [backend/internal/usage/httpapi/admin/time_range_test.go](../../../backend/internal/usage/httpapi/admin/time_range_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/httpapi/admin/usage_cleanup_handler_test.go](../../../backend/internal/usage/httpapi/admin/usage_cleanup_handler_test.go) | usage / 测试/夹具 | 29 | S08 |
| [backend/internal/usage/httpapi/admin/usage_handler.go](../../../backend/internal/usage/httpapi/admin/usage_handler.go) | usage / HTTP/DTO/ETag/帧 Adapter | 12 | S08 |
| [backend/internal/usage/httpapi/admin/usage_handler_request_type_test.go](../../../backend/internal/usage/httpapi/admin/usage_handler_request_type_test.go) | usage / 测试/夹具 | 20 | S08 |
| [backend/internal/usage/httpapi/admin/usage_handler_search_users_test.go](../../../backend/internal/usage/httpapi/admin/usage_handler_search_users_test.go) | usage / 测试/夹具 | 3 | S08 |
| [backend/internal/usage/httpapi/admin/usage_handler_sort_test.go](../../../backend/internal/usage/httpapi/admin/usage_handler_sort_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/httpapi/admin/usage_query_cache.go](../../../backend/internal/usage/httpapi/admin/usage_query_cache.go) | usage / HTTP/DTO/ETag/帧 Adapter | 1 | S08 |
| [backend/internal/usage/httpapi/dto/mappers.go](../../../backend/internal/usage/httpapi/dto/mappers.go) | usage / HTTP/DTO/ETag/帧 Adapter | 7 | S08 |
| [backend/internal/usage/httpapi/dto/types.go](../../../backend/internal/usage/httpapi/dto/types.go) | usage / HTTP/DTO/ETag/帧 Adapter | 10 | S08 |
| [backend/internal/usage/httpapi/dto/views.go](../../../backend/internal/usage/httpapi/dto/views.go) | usage / HTTP/DTO/ETag/帧 Adapter | 4 | S08 |
| [backend/internal/usage/httpapi/legacy_fixture_test.go](../../../backend/internal/usage/httpapi/legacy_fixture_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/httpapi/ports/readers.go](../../../backend/internal/usage/httpapi/ports/readers.go) | usage / HTTP/DTO/ETag/帧 Adapter | 14 | S08 |
| [backend/internal/usage/httpapi/public_handler.go](../../../backend/internal/usage/httpapi/public_handler.go) | usage / HTTP/DTO/ETag/帧 Adapter | 19 | S08 |
| [backend/internal/usage/httpapi/usage_handler.go](../../../backend/internal/usage/httpapi/usage_handler.go) | usage / HTTP/DTO/ETag/帧 Adapter | 37 | S08 |
| [backend/internal/usage/httpapi/usage_handler_daily_test.go](../../../backend/internal/usage/httpapi/usage_handler_daily_test.go) | usage / 测试/夹具 | 10 | S08 |
| [backend/internal/usage/httpapi/usage_handler_request_type_test.go](../../../backend/internal/usage/httpapi/usage_handler_request_type_test.go) | usage / 测试/夹具 | 24 | S08 |
| [backend/internal/usage/httpapi/usage_handler_sort_test.go](../../../backend/internal/usage/httpapi/usage_handler_sort_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/httpapi/usage_ranking_settings_test.go](../../../backend/internal/usage/httpapi/usage_ranking_settings_test.go) | usage / 测试/夹具 | 7 | S08 |
| [backend/internal/usage/legacy_fixtures_test.go](../../../backend/internal/usage/legacy_fixtures_test.go) | usage / 测试/夹具 | 9 | S08 |
| [backend/internal/usage/log.go](../../../backend/internal/usage/log.go) | usage / 核心值/用例/只读端口 | 4 | S08 |
| [backend/internal/usage/options.go](../../../backend/internal/usage/options.go) | usage / 核心值/用例/只读端口 | 13 | S08 |
| [backend/internal/usage/postgres/aggregation_fixture_test.go](../../../backend/internal/usage/postgres/aggregation_fixture_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/postgres/dashboard_aggregation_repo.go](../../../backend/internal/usage/postgres/dashboard_aggregation_repo.go) | usage / 同连接 SQL/Ent Adapter | 24 | S08 |
| [backend/internal/usage/postgres/fixtures_integration_test.go](../../../backend/internal/usage/postgres/fixtures_integration_test.go) | usage / 测试/夹具 | 4 | S08 |
| [backend/internal/usage/postgres/integration_harness_test.go](../../../backend/internal/usage/postgres/integration_harness_test.go) | usage / 测试/夹具 | 29 | S08 |
| [backend/internal/usage/postgres/key_totals.go](../../../backend/internal/usage/postgres/key_totals.go) | usage / 同连接 SQL/Ent Adapter | 1 | S08 |
| [backend/internal/usage/postgres/query/identity.go](../../../backend/internal/usage/postgres/query/identity.go) | usage / 同连接 SQL/Ent Adapter | 2 | S08 |
| [backend/internal/usage/postgres/query/keys.go](../../../backend/internal/usage/postgres/query/keys.go) | usage / 同连接 SQL/Ent Adapter | 2 | S08 |
| [backend/internal/usage/postgres/query/team.go](../../../backend/internal/usage/postgres/query/team.go) | usage / 同连接 SQL/Ent Adapter | 5 | S08 |
| [backend/internal/usage/postgres/s08_fixed_integration_test.go](../../../backend/internal/usage/postgres/s08_fixed_integration_test.go) | usage / 测试/夹具 | 11 | S08 |
| [backend/internal/usage/postgres/s08_query_shape_integration_test.go](../../../backend/internal/usage/postgres/s08_query_shape_integration_test.go) | usage / 测试/夹具 | 4 | S08 |
| [backend/internal/usage/postgres/sqlmock_test.go](../../../backend/internal/usage/postgres/sqlmock_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/postgres/support.go](../../../backend/internal/usage/postgres/support.go) | usage / 同连接 SQL/Ent Adapter | 14 | S08 |
| [backend/internal/usage/postgres/usage_analytics_aggregation_repo.go](../../../backend/internal/usage/postgres/usage_analytics_aggregation_repo.go) | usage / 同连接 SQL/Ent Adapter | 15 | S08 |
| [backend/internal/usage/postgres/usage_analytics_aggregation_repo_test.go](../../../backend/internal/usage/postgres/usage_analytics_aggregation_repo_test.go) | usage / 测试/夹具 | 10 | S08 |
| [backend/internal/usage/postgres/usage_cleanup_repo.go](../../../backend/internal/usage/postgres/usage_cleanup_repo.go) | usage / 同连接 SQL/Ent Adapter | 22 | S08 |
| [backend/internal/usage/postgres/usage_cleanup_repo_ent_test.go](../../../backend/internal/usage/postgres/usage_cleanup_repo_ent_test.go) | usage / 测试/夹具 | 11 | S08 |
| [backend/internal/usage/postgres/usage_cleanup_repo_test.go](../../../backend/internal/usage/postgres/usage_cleanup_repo_test.go) | usage / 测试/夹具 | 26 | S08 |
| [backend/internal/usage/postgres/usage_log_deadlock_retry_test.go](../../../backend/internal/usage/postgres/usage_log_deadlock_retry_test.go) | usage / 测试/夹具 | 6 | S08 |
| [backend/internal/usage/postgres/usage_log_repo.go](../../../backend/internal/usage/postgres/usage_log_repo.go) | usage / 同连接 SQL/Ent Adapter | 38 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_analytics.go](../../../backend/internal/usage/postgres/usage_log_repo_analytics.go) | usage / 同连接 SQL/Ent Adapter | 12 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_analytics_queries.go](../../../backend/internal/usage/postgres/usage_log_repo_analytics_queries.go) | usage / 同连接 SQL/Ent Adapter | 15 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_analytics_test.go](../../../backend/internal/usage/postgres/usage_log_repo_analytics_test.go) | usage / 测试/夹具 | 17 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_breakdown_test.go](../../../backend/internal/usage/postgres/usage_log_repo_breakdown_test.go) | usage / 测试/夹具 | 6 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_dashboard.go](../../../backend/internal/usage/postgres/usage_log_repo_dashboard.go) | usage / 同连接 SQL/Ent Adapter | 15 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_dashboard_test.go](../../../backend/internal/usage/postgres/usage_log_repo_dashboard_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_deleted_user_integration_test.go](../../../backend/internal/usage/postgres/usage_log_repo_deleted_user_integration_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_insert.go](../../../backend/internal/usage/postgres/usage_log_repo_insert.go) | usage / 同连接 SQL/Ent Adapter | 40 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_insert_shape_unit_test.go](../../../backend/internal/usage/postgres/usage_log_repo_insert_shape_unit_test.go) | usage / 测试/夹具 | 6 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_integration_test.go](../../../backend/internal/usage/postgres/usage_log_repo_integration_test.go) | usage / 测试/夹具 | 60 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_query.go](../../../backend/internal/usage/postgres/usage_log_repo_query.go) | usage / 同连接 SQL/Ent Adapter | 36 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_request_type_test.go](../../../backend/internal/usage/postgres/usage_log_repo_request_type_test.go) | usage / 测试/夹具 | 35 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_sort_integration_test.go](../../../backend/internal/usage/postgres/usage_log_repo_sort_integration_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_stats.go](../../../backend/internal/usage/postgres/usage_log_repo_stats.go) | usage / 同连接 SQL/Ent Adapter | 31 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_stats_integration_test.go](../../../backend/internal/usage/postgres/usage_log_repo_stats_integration_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_team_scope_test.go](../../../backend/internal/usage/postgres/usage_log_repo_team_scope_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_trend.go](../../../backend/internal/usage/postgres/usage_log_repo_trend.go) | usage / 同连接 SQL/Ent Adapter | 29 | S08 |
| [backend/internal/usage/postgres/usage_log_repo_unit_test.go](../../../backend/internal/usage/postgres/usage_log_repo_unit_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/postgres/usage_log_session_id_integration_test.go](../../../backend/internal/usage/postgres/usage_log_session_id_integration_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/postgres/usage_log_session_id_unit_test.go](../../../backend/internal/usage/postgres/usage_log_session_id_unit_test.go) | usage / 测试/夹具 | 5 | S08 |
| [backend/internal/usage/postgres/usage_ranking_query_test.go](../../../backend/internal/usage/postgres/usage_ranking_query_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/query_cache_key_test.go](../../../backend/internal/usage/query_cache_key_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/query_readers.go](../../../backend/internal/usage/query_readers.go) | usage / 核心值/用例/只读端口 | 7 | S08 |
| [backend/internal/usage/ranking.go](../../../backend/internal/usage/ranking.go) | usage / 核心值/用例/只读端口 | 13 | S08 |
| [backend/internal/usage/rediscache/dashboard.go](../../../backend/internal/usage/rediscache/dashboard.go) | usage / Redis Adapter | 7 | S08 |
| [backend/internal/usage/rediscache/dashboard_integration_test.go](../../../backend/internal/usage/rediscache/dashboard_integration_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/rediscache/dashboard_test.go](../../../backend/internal/usage/rediscache/dashboard_test.go) | usage / 测试/夹具 | 1 | S08 |
| [backend/internal/usage/repository.go](../../../backend/internal/usage/repository.go) | usage / 核心值/用例/只读端口 | 1 | S08 |
| [backend/internal/usage/request_type.go](../../../backend/internal/usage/request_type.go) | usage / 核心值/用例/只读端口 | 16 | S08 |
| [backend/internal/usage/s08_fixed_regression_test.go](../../../backend/internal/usage/s08_fixed_regression_test.go) | usage / 测试/夹具 | 15 | S08 |
| [backend/internal/usage/service.go](../../../backend/internal/usage/service.go) | usage / 核心值/用例/只读端口 | 31 | S08 |
| [backend/internal/usage/usage_analytics_aggregation.go](../../../backend/internal/usage/usage_analytics_aggregation.go) | usage / 核心值/用例/只读端口 | 3 | S08 |
| [backend/internal/usage/usage_cleanup.go](../../../backend/internal/usage/usage_cleanup.go) | usage / 核心值/用例/只读端口 | 8 | S08 |
| [backend/internal/usage/usage_cleanup_service.go](../../../backend/internal/usage/usage_cleanup_service.go) | usage / 核心值/用例/只读端口 | 21 | S08 |
| [backend/internal/usage/usage_cleanup_service_test.go](../../../backend/internal/usage/usage_cleanup_service_test.go) | usage / 测试/夹具 | 61 | S08 |
| [backend/internal/usage/usage_log_create_result.go](../../../backend/internal/usage/usage_log_create_result.go) | usage / 核心值/用例/只读端口 | 12 | S08 |
| [backend/internal/usage/usage_log_types.go](../../../backend/internal/usage/usage_log_types.go) | usage / 核心值/用例/只读端口 | 30 | S08 |
| [backend/internal/usage/usage_log_types_test.go](../../../backend/internal/usage/usage_log_types_test.go) | usage / 测试/夹具 | 2 | S08 |
| [backend/internal/usage/usage_query_cache.go](../../../backend/internal/usage/usage_query_cache.go) | usage / 核心值/用例/只读端口 | 3 | S08 |
| [backend/internal/usage/views.go](../../../backend/internal/usage/views.go) | usage / 核心值/用例/只读端口 | 5 | S08 |
