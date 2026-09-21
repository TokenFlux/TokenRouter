# 存储装配与传输入口收敛

## 生产路径

本批 repository 生产文件从 76 个降至 26 个。表中路径均相对 `backend/internal`，被删除的旧入口不再保留状态或算法。

| 原 repository 文件 | 唯一实现与最终装配 |
| --- | --- |
| `wire.go` | `app/storage_wire.go`、`module_storage_wire.go`、`upstream_clients_wire.go` 分组绑定原生 provider；不再导入 repository.ProviderSet |
| `setting_repo.go` | `settings/postgres` + app 唯一 `settings.Store`，存取接口绑定同一 Store |
| `idempotency_repo.go` | `idempotency/postgres`；app 从原 Ent 驱动取得同一 SQL 连接池 |
| `audit_log_repo.go` | `audit/postgres`，原 app 直接装配 |
| `error_passthrough_repo.go`、`error_passthrough_cache.go` | `gateway/postgres`、`gateway/rediscache` |
| `content_moderation_repo.go`、`content_moderation_hash_cache.go` | `moderation/postgres`、`moderation/rediscache` |
| `user_subscription_repo.go`、`user_group_rate_repo.go`、`redeem_code_repo.go`、`redeem_cache.go` | `billing/postgres` 与 `billing/rediscache`；app 显式接口绑定 |
| `batch_image_repo.go`、`creative_run_repo.go`、`creative_run_outbox_repo.go` | 各任务模块 PostgreSQL Adapter；托管 Key 投影复用 app 已构造的 KeyStore |
| `group_availability_probe_repo.go` | `routing/postgres` |
| `creative_queue.go`、`creative_transient_store.go`、`batch_image_queue.go`、`batch_image_download_limiter.go` | 所属任务 Redis Adapter；`app/task_storage.go` 按原单位、缺省值投影配置 |
| `claude_oauth_service.go`、`claude_usage_service.go` | `upstream/anthropic`；app 注入原 HTTP 端口 |
| `grok_oauth_client.go` | `upstream/grok` |
| `gemini_drive_client.go`、`geminicli_codeassist_client.go`、`gemini_oauth_client.go` | `upstream/gemini/codeassist`，配置读取时机保留 |
| `openai_oauth_service.go` | `upstream/openai` |
| `github_release_service.go` | `ops/provider`，原入口无消费者 |
| `backup_s3_store.go`、`backup_pg_dumper.go` | `backup/provider`，恢复测试直接构造所属执行器 |
| `auth_cache_invalidation_outbox_repo.go` | `apikey/postgres` |
| `channel_repo.go` | `routing/postgres` |
| `promo_code_repo.go` | `promotion/postgres` |
| `passkey_repo.go`、`user_attribute_repo.go` | `identity/postgres` |
| `tls_fingerprint_profile_repo.go`、`tls_fingerprint_router_repo.go` | `egress/postgres` |
| `user_msg_queue_cache.go`、`concurrency_cache.go` | `scheduler/rediscache` |
| `team_invitation_limiter.go` | `team/rediscache` |
| `scheduler_outbox_repo.go` | `scheduler/postgres`；账号、分组、代理和结算参与者直接调用原同连接 writer |
| `dashboard_aggregation_repo.go`、`dashboard_cache.go`、`usage_cleanup_repo.go` | `usage/postgres`、`usage/rediscache`，原 app 装配已直接使用 |
| `proxy_probe_service.go` | `egress/provider`；配置契约在 app 测试 |
| `migrations_compat.go` | `infra/postgres.ApplyMigrations` + 原 migrations.FS |
| `http_upstream.go` | `gateway/provider/transport`；app 提供明确传输参数，复用 infra HTTP 池、egress 策略及 Grok 原语 |
| `req_client_pool.go` | `app/privacy_client.go` 投影参数，`infra/httpclient` 持有原共享池 |
| `api_key_cache.go` | `apikey/rediscache` |
| `session_limit_cache.go` | 原生产 app 已直接组合 scheduler 会话与 billing 窗口缓存；测试改用所属会话缓存 |

HTTP 传输保留 nil 配置与显式零值的区别、参数读取时点、隔离键、账号并发调整、TLS profile hash、HTTP/2 回退、Grok Header/403 回退、逐跳校验和响应关闭释放。新 provider 不读取完整 config，不依赖旧 service；旧请求标记与原生 upstream 使用同一 context key。

## 测试路径

- `auth_cache_invalidation_outbox_repo_test.go` → `apikey/postgres/invalidation_outbox_contract_test.go`，保留 SQL/触发器断言；仅该文件精确允许 migrations。
- `team_invitation_limiter_test.go` → `team/rediscache/invitation_contract_test.go`，保留 unit 标签和所有行为断言。
- `proxy_probe_service_test.go` → `app/proxy_probe_configuration_test.go`，验证实际配置 provider。
- `http_upstream_test.go`、`http_upstream_pool_test.go` → `gateway/provider/transport/http_test.go`、`pool_test.go`，保留代理、TLS、回退与缓存断言；删除已静态确定的构造类型断言。
- `api_key_cache_subscriber_test.go` → `apikey/rediscache/subscriber_test.go`，继续使用 miniredis，不当作真实 Redis 证据。
- `s07_session_cache_integration_test.go` → `scheduler/rediscache/script_flush_integration_test.go`，真实 Redis 验证 B04。
- `s04_compat_integration_test.go`、`team_invitation_limiter_s05_compat_unit_test.go`、`api_key_cache_s05_compat_integration_test.go` 只有别名；删除后原测试直接引用原生符号。
- 其余跨模块资金、存储和任务契约仍在原测试文件中，构造器改绑不改变断言、事务连接或标签。
- service 中 Bedrock 二进制流契约迁到 `upstream/bedrock/stream_contract_test.go`；OpenAI 图片尺寸及 Grok 几何测试直接调用原生实现。删除无消费者的 `QuotaFetcher` 和 `LegacyScheduledRecovery`，不改健康恢复规则。

## 验证

各集合包含父子测试且有重叠，不相加。下列完成结果均无实际失败或跳过。

| 日志 | 通过事件 | 范围 |
| --- | ---: | --- |
| `module-storage-direct-integration-race.json` | 91 | 设置、权益、兑换、并发事务与应用存储 |
| `repository-provider-removal-unit.json` | 450 | 原消费者、app 与任务 Redis |
| `storage-outbox-direct-unit-retry.json` | 433 | outbox、团队限流及原消费者 |
| `outbox-task-native-integration-race.json` | 16 | 调度 outbox、任务资金、维护恢复 |
| `storage-migrations-direct-race.json` | 11 | 原迁移 runner、维护恢复与共享存储 |
| `http-transport-race-retry.json` | 64 | 原 HTTP 传输契约 |
| `transport-storage-unit.json` | 424 | 原消费者、传输和 Key 订阅 |
| `key-session-transport-direct-race.json` | 11 | Key 缓存、真实 Redis 脚本清空及 app 配置读取 |
| `platform-wrapper-tests.json` | 43 | Bedrock 事件帧与图片尺寸/几何 |

初次编译发现 app 外部测试包误用私有 provider、一个遗漏的 outbox 函数值引用、迁移测试遗漏本地 HTTP helper，均为本批迁移回归；已修正并保留初次日志，未计作通过。普通 lint 首次检出迁移 SQL 契约缺少精确许可，补齐后普通/unit/integration 全量 lint 均为 0，日志为 `storage-transport-full-*-lint.log`。平台包装清理之后的 lint 与 wireinject 结果另追加到阶段记录。

本批未开展历史问题审计。service/handler 的应用聚合、旧实体转换和业务 ctxkey 尚未清零，S16 仍为实施中。

## 后续装配清理（2026-09-19）

- S16.2：删除 `repository/team_repo.go`、`affiliate_repo.go`，repository 非测试文件剩 24 个。团队测试直接组合原生 TeamRepository、TeamKeys、MemberUsageStore；推广测试使用原生 AffiliateRepository 与 `billing/postgres.BalanceInTx`。事务仍使用原连接，真实 PostgreSQL race 为 38 条通过事件。
- S16.3/S16.5：删除已清零的健康、Ops、邮件队列、额度 flusher、代理和 HTTP context 旧委托；60 处函数消费者按 Go 类型信息改绑原生所有者，见 `function-direct-consumers.json`。Bedrock、图片和锁断言继续由原生实现承接。
- S16.5：`app/idempotency_runtime.go` 接管默认值与配置投影、唯一协调器发布和清理任务构造；`app/storage_wire.go` 直接绑定 `timingwheel.New`。移除 service 的三项 provider、`idempotency_compat.go` 和 `timing_wheel_service.go`；默认状态仍只有 idempotency 的一份实现。
- S16.6：删除的 `service/wire_test.go::TestProvideTimingWheelService_Success` 仅断言无错误、对象非空和可停止；由 `infra/timingwheel/wheel_test.go::TestNewTimingWheelService_Success` 的同等断言承接，并保留原生 Start 失败回收与回调测试。Deferred 消费者直接构造同一时间轮类型，业务断言不变。
- 幂等 HTTP helper、管理接口和维护锁测试只替换函数所属包，原 scope、TTL、错误、去重与释放断言保留。普通和 unit 定向 race 分别 220、80 条通过事件，无失败或跳过；无匹配包不计行为通过。
- 前一组 `direct-function-contracts-race.json` 为 129 条通过、1 项既有 WS `other_event_type` 跳过；该跳过不计通过。原生维护锁 race 2 条通过。各集合有重叠，不相加，不代替最终全量验收。
