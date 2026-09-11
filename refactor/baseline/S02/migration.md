# S02 迁移与交接清单

基线为 main `5d3717f09ea5efcf4ae6e9c708031b51de1c668b`。本阶段修改/新增/移除 307 个 Go 文件；[逐文件清单](migration-files.json)包含职责、符号、测试条件、实际 import、文档锚点和当前 SHA256；[包消费者表](package-consumers.json)提供所有直接 import 文件，包括测试和生成代码。完整所有权继续以 S00 清单及总计划为准，未涉及的旧能力没有被宣称为已迁移。

## 实现边界

app 是唯一生产组合根。cmd/server 只保留 CLI、构建变量与最终退出决定；bootstrap 处理连接、Ent 包装、迁移、密钥、完整校验与 simple 数据。迁移技术 runner 在 infra/postgres，接收 migration FS，旧 repository 不导入 app。旧 Wire provider set 保留业务对象构造，应用侧按 boot/auth/maintenance/ops/queues/jobs/core 分组登记启停。

settings/postgres 是原 settings 表的唯一存取实现；旧 SettingService 保留业务解释与缓存刷新时机。Store 的写方法不自动广播，旧批量业务更新在原刷新点显式通知，单回调仍替换，独立订阅可以注销。版本为原应用版本，未增加数据库 revision。

idempotency 拥有完整认领/指纹/重放/冲突/过期/退避/存储/指标及可等待清理。Options 不接收 config，观察接口由 app 投影；旧默认 coordinator、指标和 alias 不复制状态。系统维护操作锁只改用新契约。

site 完整拥有公告 targeting、可见性、CRUD、已读与归档；路由直接指向 site/httpapi。用户/订阅只读桥接集中在 app/legacybridge，查询顺序与资格规则保留。PostgreSQL Adapter 继续读取原 Ent 事务 context，旧 domain 别名避免 Ent 生成变化。

## 迁移路径

同名旧文件的薄转接见后表；以下是移除旧文件与实际承接路径的映射。[机器可读映射](path-mapping.json)用于诊断路径归一化。

| 旧文件 | 承接文件 |
| --- | --- |
| `cmd/server/wire.go` | `internal/app/wire.go` |
| `cmd/server/wire_gen.go` | `internal/app/wire_gen.go` |
| `cmd/server/wire_gen_test.go` | `internal/app/process_integration_test.go` |
| `internal/config/wire.go` | `internal/app/wire.go` |
| `internal/handler/admin/announcement_handler.go` | `internal/site/httpapi/admin.go` |
| `internal/handler/admin/announcement_handler_sort_test.go` | `internal/site/httpapi/admin_sort_test.go` |
| `internal/handler/announcement_handler.go` | `internal/site/httpapi/user.go` |
| `internal/handler/dto/announcement.go` | `internal/site/httpapi/dto.go` |
| `internal/pkg/logger/config_adapter.go` | `internal/app/logging.go` |
| `internal/pkg/sysutil/restart.go` | `internal/app/lifecycle/restart.go` |
| `internal/repository/aes_encryptor.go` | `internal/app/bootstrap/aes_encryptor.go` |
| `internal/repository/aes_encryptor_test.go` | `internal/app/bootstrap/aes_encryptor_test.go` |
| `internal/repository/announcement_read_repo.go` | `internal/site/postgres/announcement_read_repo.go` |
| `internal/repository/announcement_repo.go` | `internal/site/postgres/announcement_repo.go` |
| `internal/repository/announcement_repo_expiry_test.go` | `internal/site/postgres/announcement_repo_expiry_test.go` |
| `internal/repository/announcement_repo_sort_test.go` | `internal/site/postgres/announcement_repo_sort_test.go` |
| `internal/repository/db_pool.go` | `internal/app/bootstrap/db_pool.go` |
| `internal/repository/db_pool_test.go` | `internal/app/bootstrap/db_pool_test.go` |
| `internal/repository/ent.go` | `internal/app/bootstrap/database.go` |
| `internal/repository/ent_init_retry_test.go` | `internal/infra/postgres/initialize_retry_test.go` |
| `internal/repository/leader_lock_cache_test.go` | `internal/infra/redis/leader_lock_test.go` |
| `internal/repository/migrations_runner.go` | `internal/infra/postgres/migrations_runner.go` |
| `internal/repository/migrations_runner_checksum_test.go` | `internal/infra/postgres/migrations_runner_checksum_test.go` |
| `internal/repository/migrations_runner_extra_test.go` | `internal/infra/postgres/migrations_runner_extra_test.go` |
| `internal/repository/migrations_runner_notx_test.go` | `internal/infra/postgres/migrations_runner_notx_test.go` |
| `internal/repository/redis.go` | `internal/app/bootstrap/redis.go` |
| `internal/repository/redis_test.go` | `internal/app/bootstrap/redis_test.go` |
| `internal/repository/security_secret_bootstrap.go` | `internal/app/bootstrap/security_secret_bootstrap.go` |
| `internal/repository/security_secret_bootstrap_test.go` | `internal/app/bootstrap/security_secret_bootstrap_test.go` |
| `internal/repository/simple_mode_admin_concurrency.go` | `internal/app/bootstrap/simple_mode_admin_concurrency.go` |
| `internal/repository/simple_mode_default_groups.go` | `internal/app/bootstrap/simple_mode_default_groups.go` |
| `internal/repository/simple_mode_default_groups_integration_test.go` | `internal/app/bootstrap/simple_mode_integration_test.go` |
| `internal/server/frontend_server_embed.go` | `internal/app/http_bindings.go` |
| `internal/server/frontend_server_noembed.go` | `internal/app/http_bindings.go` |
| `internal/service/announcement_expiry_service.go` | `internal/site/expiry.go` |
| `internal/service/announcement_expiry_service_test.go` | `internal/site/announcement_expiry_service_test.go` |
| `internal/service/announcement_service.go` | `internal/site/service.go` |
| `internal/service/announcement_service_test.go` | `internal/site/announcement_service_test.go` |
| `internal/service/announcement_targeting_test.go` | `internal/site/announcement_targeting_test.go` |
| `internal/service/idempotency.go` | `internal/idempotency/idempotency.go` |
| `internal/service/idempotency_cleanup_service.go` | `internal/idempotency/idempotency_cleanup_service.go` |
| `internal/service/idempotency_cleanup_service_test.go` | `internal/idempotency/idempotency_cleanup_service_test.go` |
| `internal/service/idempotency_observability.go` | `internal/idempotency/idempotency_observability.go` |
| `internal/service/idempotency_test.go` | `internal/idempotency/idempotency_test.go` |
| `internal/service/timing_wheel_service_test.go` | `internal/infra/timingwheel/wheel_test.go` |

## 过渡入口与退出阶段

下表与 [剩余消费者详情](remaining-consumers.json)一起使用；按包列出的消费者表包含旧入口所在包的所有直接导入者，不能仅凭 import 推断单个符号已经清零。

| 入口 | 剩余职责 | 退出阶段 | 处理约束 |
| --- | --- | --- | --- |
| `internal/service/setting.go` | settings 值类型/端口别名 | S10/S15 | 旧 SettingService 的业务解释、敏感值、页面聚合与领域缓存继续保留，存取进入唯一 Store |
| `internal/service/setting_service.go` | 业务设置服务 | S04—S14/S16 | 各领域逐批收回配置解释；通用存取、版本、通知已唯一实现，不整体搬迁旧大服务 |
| `internal/repository/setting_repo.go` | settings PostgreSQL 构造转接 | S15/S16 | 旧 provider set 仍构造新 Store；消费者清零后移除 |
| `internal/service/idempotency_compat.go` | 幂等类型/默认状态/构造转接 | S14/S15/S16 | 旧用户、管理员 helper 和系统维护命令仍经此唯一委托 |
| `internal/repository/idempotency_repo.go` | 幂等 SQL 构造转接 | S14/S15/S16 | 生产与维护测试共用新 PostgreSQL Adapter |
| `internal/service/announcement.go` | 公告服务及请求类型别名 | S15/S16 | 旧聚合类型的兼容入口；路由 handler 已直接接入 site/httpapi |
| `internal/domain/announcement.go` | Ent schema/生成代码公告类型别名 | S15/S16 | 保留生成代码来源类型身份；本阶段不生成 Ent |
| `internal/app/legacybridge/announcement.go` | 用户/订阅只读投影 | S04/S05 | billing 与 identity 能直接提供窄读接口后删除相应桥接 |
| `internal/service/timing_wheel_service.go` | 时间轮构造/类型兼容 | S06/S16 | 调度技术唯一位于 infra/timingwheel，旧任务 key 与周期留在所属业务 |
| `internal/service/deferred_service.go` | 账号 last-used 延迟写回 | S06 | 本阶段仅处理停止、串行最后 flush 及失败报告 |
| `internal/repository/leader_lock_cache.go` | Redis leader lock 旧端口构造 | S08/S14/S16 | 技术算法已移 infra/redis，锁名、TTL、owner 和故障回退由旧调用者决定 |
| `internal/service/ops_advisory_lock.go` | 数据库 advisory lock 旧入口 | S08/S16 | 只委托 infra/postgres；策略与竞争语义留使用方 |
| `internal/repository/migrations_compat.go` | 旧集成测试迁移入口 | S16 | 只委托 infra 的嵌入迁移调用，不反向依赖 app |
| `internal/app/bootstrap/simple_mode_default_groups.go` | simple 默认数据编排 | S14 | 仍投影旧 group/domain，精确文件许可随维护用例迁移退出 |
| `internal/setup/setup.go` | 精简 bootstrap 迁移入口 | S14 | 管理员、配置文件、安装锁流程保持；不构造完整 worker |
| `internal/service/system_operation_lock_service.go` | 系统维护操作锁策略 | S14 | 新幂等契约承载认领/续租；全局 scope 与释放规则未变 |
| `internal/service/background_tasks.go` | 旧后台任务完成端口 | S04—S14/S16 | 逐个拥有者迁移后删除；参数求值、context 和并发策略保持 |
| `internal/app/legacy_providers.go` | 旧图与运行时端口装配 | S04—S14/S16 | 逐模块替换 provider set；app 只调用与投影 |
| `internal/app/http_bindings.go` | 公开设置/CSP/websearch/限流绑定 | S10/S15 | 业务设置解释仍属旧服务；server 只接收 RouterRuntime |
| `internal/handler/ops_error_logger.go` | 按需运维错误日志队列 | S08/S15 | 队列实现仍在 HTTP 旧层，本阶段接入共享预算下的实际 drain |
| `internal/handler/admin/ops_ws_handler.go` | 按需 QPS 刷新与空闲定时器 | S08/S15 | 启动时机不变；应用关闭时封闭重启与等待完成 |

S03 接收现有 protocol 白名单与未改的分组协议约束失败。S06 接收 Deferred 账号业务及 OpenAI HTTP/2 策略；S09 接收 Grok CLI/403 平台策略；S10 接收剩余页面/内容及设置业务解释。S08 接收运维运行资源业务拆分。S14 完成 setup/维护命令精简。S15/S16 清理 alias、旧 provider 和按文件依赖许可。

## Wire、文档与回退

入口仍为 `GOTOOLCHAIN=go1.27.0 go generate ./cmd/server`，仅生成 app/wire_gen.go；[二次生成摘要](wire-reproducibility.json)必须相同。Ent 生成物与 SQL 原始 checksum 用 [checks.json](checks.json)核对。

现有 Project Doc 更新到实际共存结构：architecture/system_architecture 的 dependency_layers、startup_and_shutdown；interfaces/configuration 的 runtime_settings；interfaces/http_api 的 announcement_api、write_idempotency；operations/deployment_and_migrations、development_workflow、ops_monitoring_and_alerting；domains/platform_quotas 和 content_moderation。代码锚点在逐文件清单中登记。

回退只撤销此清单对应的代码、规则、Wire 和文档，恢复旧调用链。数据库/缓存格式未变；不回退其他任务文件，不提交 SYNC.md，索引保持原状。
