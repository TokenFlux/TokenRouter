# 版本升级说明

本文记录跨版本升级中需要特别注意的停机窗口、配置迁移、验证项与回退限制。迁移执行机制和通用兼容规则见[部署与数据库迁移](deployment_and_migrations.md)；当前生效的认证缓存版本与调度缓存命名空间以[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)为准，本文历史条目中提到的缓存版本仅代表当时迁移的值。

## 章节导航

- [通用升级与回退规则](#通用升级与回退规则)：所有跨版本升级的共同步骤与停机边界。
- [未发布与近期升级](#未发布与近期升级)：当前待发布变更及迁移 292 到 316 的详细步骤。
- [关键破坏性历史升级](#关键破坏性历史升级)：迁移 282 到 291 中涉及停机或结构大改的专题。
- [更早历史迁移速查表](#更早历史迁移速查表)：迁移 234 到 281 的快速索引与已取代状态。

## 通用升级与回退规则

1. 单实例前后端始终作为同一版本整体切换；升级前停止接收新流量，按关闭流程停止旧后端，确认在途请求与后台任务结束。
2. 停机迁移前必须备份并验证 PostgreSQL（涉及缓存或文件结构变更时一并备份 Redis、配置和 `pages/`、`plugins.dir`）。
3. 由目标版本后端执行前向迁移并完成抽样验证后，再恢复外部流量。
4. 需要撤销数据库变更的回退必须停止新版本并恢复升级前备份（含对应的 Redis、配置与前端资源）；仅回退二进制或删除迁移记录不会撤销表结构变更。

## 未发布与近期升级

<a id="qoder_account_catalog"></a>
### 未发布：Qoder 桌面账号目录

- 变更要点：Qoder 模型改为按账号手动同步；首次成功查询用量后保存带账号身份的额度快照，后续查询失败时可查看上次成功额度。Responses 调用方需关闭响应存储并提交完整历史，工具结果须携带原函数调用。未传推理参数时改用模型默认推理设置，可能影响 Credits 消耗。
- 升级动作：升级后如无身份匹配快照或切换账号，在提供商「模型」页点击「同步上游支持的模型」建立目录（如需空白名单可取消保存白名单草稿，模型属性仍保留），并检查映射是否指向已退役型号。
- 回退限制：旧 COSY/PAT 刷新保留，新桌面授权使用 `device_token`；回退旧版需重新配置旧凭据，切勿直接修改 `refresh_mode`。

### 迁移 316：5m 缓存写入独立字段与畸形用量诊断

- 变更要点：`316_cache_write_5m_and_anomaly.sql` 为四张价格配置表新增可空 `cache_write_5m_price`，并为 `usage_logs` 新增可空 `cache_write_anomaly` JSONB 列。存量 `cache_write_price` 保持统一写入价语义（同时填了 `cache_write_price` 与 `cache_write_1h_price` 的旧价卡行为不变）。
- 验证与回退：前后端整体升级后，核对价卡编辑器的 `统一 / 5m & 1h / 30m & 12h` 模式与使用记录 TTL 标签展示。回退旧版前先将使用了 `cache_write_5m_price` 的条目改回统一价或恢复升级前数据库备份，规则见[缓存写入分档](../domains/routing_and_billing.md#cache_write_pricing)。

### 迁移 315：API Key 强制开启 Ultrafast

- 变更要点：`315_api_key_force_ultrafast.sql` 扩展 Key Fast 策略约束，存量记录保持原值。
- 验证与回退：前后端整体升级后，用声明了 Ultrafast 定价的 OpenAI 模型核对出站档位。旧后端会把 `force_ultrafast` 当作跟随请求，回退前先在管理端改回旧策略。

<a id="config_version_2"></a>
### 迁移 314 与配置文件版本 2、models.dev v2

- models.dev v2 与 GPT 缓存分档：目录在线源、磁盘缓存（`models_dev_catalog_v2.json`，旧缓存保留供回退）与离线快照统一升至 v2，旧官方地址自动映射，自定义镜像需先同步 v2 结构。`314_gpt_cache_write_lifetimes.sql` 添加可空分档字段，历史金额保留，旧后端可继续读写原有列。
- 配置文件自动升 v2：缺少 `config_version` 的旧文件视为 v1，首次启动按旧优先级导入当时环境变量，将敏感值和 `DATABASE_HOST`/`REDIS_HOST` 写为 `${VAR}` 引用，保留注释和原顺序，原文件以 `0600` 权限备份为 `config.yaml.v1.bak`（冲突时加 UTC 时间戳）。
- 只读挂载离线迁移：配置目录只读时启动失败，使用原部署环境变量执行：

```bash
./tokenrouter -migrate-config -config /path/config.yaml -out /writable/config.v2.yaml
```

- 升级核对要点：
  - 保留配置中所有 `${VAR}` 所需的容器变量；未被文件引用的旧应用变量不再覆盖配置，启动时告警；v2 中的未知/旧键会直接阻断启动。
  - 新安装改用代码连接池默认值（DB 最大连接 256/最大空闲 128、Redis 最小空闲 128、compact-model 覆盖为空）；存量升级首次导入当时值。
  - 已生成 v2 的 Apple Container 部署如 `database.host`/`redis.host` 仍为旧 IP，需备份后改为 `${DATABASE_HOST}`/`${REDIS_HOST}` 再重启；若此前升级误将留空 `server.mode` 写成 `debug` 或改变了微信登录模式，对照 v1 备份修正。
  - `ALIPAY_MOBILE_PRECREATE_DEEP_LINK` 与 `GEMINI_CLI_OAUTH_CLIENT_SECRET` 已删除；完整键名见 [config.example.yaml](../../deploy/config.example.yaml)，升级与引用规则见[配置版本](../interfaces/configuration.md#config_versioning)。
- 回退限制：停止新进程，用 `config.yaml.v1.bak` 恢复旧配置并保留旧环境变量后再启动旧二进制（旧版不认识 v2 键和 `${VAR}`）。

### 迁移 311：订阅退款暂停

- 变更要点：`user_subscriptions` 新增可空 `refund_hold_operation_id`，旧订阅为 NULL。
- 验证与回退：整体升级后在测试环境验证退款待确认暂停、成功撤销、失败恢复及排队续订时间；回退前须先处理完仍带暂停标记的退款，保留新增列与退款审计。详见[支付与权益](../domains/payments_and_entitlements.md#退款)。

### 迁移 310：MCP 工具结果脱敏

- 变更要点：手工令牌与 OAuth 应用授权新增 `redact_tool_results`（存量默认关闭）。
- 验证与回退：升级后可按凭据开启并复用连接核对结果；回退支持 MCP 的旧版可保留新增列（工具结果恢复额外脱敏）。详见 [MCP 管理接口](../interfaces/mcp_management.md#tool_execution)。

<a id="gif_frame_billing"></a>
### 迁移 309：GIF 参数与帧计费

- 变更要点：`309_image_gif_frames.sql` 新增创作任务帧数/间隔、输出实际帧数及使用记录动画帧数和图片缓存 token 细分列（默认零值，不回填历史记录）。
- 验证与回退：不支持动画的上游应在提供商设置关闭 GIF 开关（仅流式 API Key 端点自动排除）；回退旧二进制前须排空未完成的 GIF 任务，避免旧 worker 按静态图片结算。

### 迁移 308：用户专属余额充值倍率

- 变更要点：`users` 新增可空 `balance_recharge_multiplier`（约束为有限正数，存量 NULL 沿用全局倍率）。
- 验证与回退：前后端整体更新后再配置专属倍率；旧二进制会忽略专属覆盖并回退全局倍率，回退前需先停止充值入口并核对展示条款。

### 迁移 305 到 307：插件系统、兑换固定天数与归档目录

- 迁移 305 创建 `plugin_indexes`、`plugin_installations`、`plugin_kv`、`plugin_ledger`；部署需保持单后端实例、持久化 `plugins.dir` 并配置固定加密密钥，详见[插件运维](plugin_operations.md)。
- 迁移 306 为 `redeem_codes` 增加可空 `validity_days`（NULL 沿用兑换时套餐时长，插件签发固定天数码不受后续套餐改长影响）；签发固定天数码后切勿用旧二进制处理兑换。
- 迁移 307 为当前/上一包增加 `ignore_host_version`，新包改用归档摘要目录（旧版本目录继续可读，备份保留整个 `plugins.dir/installed`）；回退旧宿主前保留数据库与目录备份，业务 KV 与台账不会因回退删除。

### 迁移 304：系统模型使用记录

- 停机要求：必须先停旧后端再迁移。迁移允许 `usage_logs` 的用户/Key 列为空（旧扫描器无法读取系统记录），新增 `NOT VALID` 归属 CHECK 与系统记录部分索引，并重建小时/日聚合主键（需预留停机窗口）。
- 验证与回退：上线抽检分组探针和手动测试的归属与提供商限额累计（确认用户资金不变）；如需数据库标记约束已验证，升级后可执行 `ALTER TABLE usage_logs VALIDATE CONSTRAINT usage_logs_source_attribution_check`。回退需恢复升级前备份或先切兼容版本。

### 迁移 303：模型配置引用

- 变更要点：`303_model_price_references.sql` 增加 `reference_model`，属性引用存入 rules JSONB。
- 回退限制：前后端需配套升级；旧编辑器保存可能清除引用，回退前需将引用展开为完整配置或移除并备份。

<a id="batch_image_protocol_admission"></a>
### 批量图片配置统一与用户页面移除（无数据库迁移）

- 变更要点：移除 `/batch-image` 与 `/docs/batch-image` 用户页，保留全部 `/v1/images/batches` API 与历史任务。`batch_image.enabled`、`queue_enabled`、`vertex_enabled` 及对应环境变量退役，改由分组 `allowed_protocols` 的 `image_batches` 与提供商原生协议控制新建准入（详见[已退役和改名的键](../interfaces/configuration.md#retired_and_renamed_keys)）。
- 升级注意：此前仅靠全局开关关闭但协议已开放的部署，升级前需在协议配置中关闭该许可；自动选择合并两类后端按优先级升序、ID 升序排列。回退旧版需恢复旧启用开关。

<a id="custom_video_pricing"></a>
### 迁移 299：自定义视频分辨率与长视频计费

- 变更要点：`usage_logs.video_resolution` 扩至 50 字符（DDL 锁等待 3 秒，冲突回滚重试），按完成响应的实际正数秒数结算，不再把未知分辨率回退为 480p 或截断超过 15 秒时长。
- 升级与回退：升级前暂停新视频任务并结算旧任务，为自定义分辨率配置单价或显式默认价；回退旧二进制前先停止自定义分辨率和长视频创建与结算。

<a id="usage_performance"></a>
### 迁移 298：性能指标集中到使用记录

- 停机要求：停旧后端与后台写入后再迁移，不能直接回退旧二进制（已删旧首 token 列与 Ops 错误表副本）。
- 变更要点：`usage_logs` 新增 `timings JSONB`（锁等待 2 秒、语句上限 30 秒，不回填历史），旧 `openai_ttft_mode` 的 `visible`/`semantic` 分别迁至全局 `first_output`/`first_event` 后删除旧键。

### 图片能力显式声明、模型属性运行时与目录候选收窄（无数据库迁移）

<a id="unified_model_attributes"></a>
<a id="finite_model_catalogue"></a>
- 图片能力：不再凭模型名含 `image` 推断资格，私有型号或别名需在属性档案显式声明 `output_modalities` 含 `image`。
- 运行时属性：模型属性档案开始影响采样、推理和媒体参数（缺失字段继续继承目录与平台值）；回退前请先导出配置，防止旧界面保存丢失新增字段。
- 目录候选收窄：对外模型列表仅由已保存白名单、映射、自定义列表及平台可枚举型号生成；仅配空白名单或通配规则的提供商可能返回空目录并在模型广场隐藏，需手动填写具体型号或同步上游。`gateway.models_list_cache_ttl_seconds` 仅保留配置解析兼容。

<a id="request_lookup_performance"></a>
### 迁移 295 到 296：请求查询分阶段与审计索引

- 迁移 295 更新 `request_lookup_ids` 分阶段查本地 ID 与外部别名；迁移 296 并发创建审计表 `(request_id, created_at DESC)` 索引（中断留下的无效索引在下次启动自动清理重建）。
- 如需缩短停机等待，可在旧实例运行期间提前执行 `296_audit_request_id_index_notx.sql` 并确认 `pg_index.indisvalid=true` 后再升级。

<a id="request_id_unification"></a>
### 迁移 293 到 294：请求 ID 与使用记录去重

- 迁移 293 新增请求摘要表、`usage_logs.billing_key` 及去重唯一索引；迁移 294 更新查询函数使父请求可关联升级前子记录费用。
- 升级后确认 `idx_usage_logs_billing_key_api_key` 有效、响应头 ID 与用量 ID 一致且任务重放不重复计费，完整规则见[请求 ID 与请求查询](request_lookup.md)。

### 迁移 292：Responses WS 设置与在线调参

- 停机要求：前后端一起升级，停旧实例后迁移；回退须恢复数据库备份。
- 变更要点：连接模式统一为 `responses_ws_connection_mode`（透传转 `per_session`，其余转 `pooled`）；旧 WS 关闭字段退出运行时，需禁用客户端 WS 的分组应在升级前取消协议许可；旧 HTTP 桥接/强制 HTTP 移除上游 WS 能力，改按 HTTP 能力与分组转换路由。
- 升级前排查旧 HTTP 桥接分组（缺少 `openai_responses` 或 `protocol_fallbacks.openai_responses_websocket` 为 `[]` 时需手工调整）：

```sql
SELECT p.id AS provider_id, g.id AS group_id, g.name AS group_name,
       p.credentials->'upstream_protocols' AS upstream_protocols,
       g.allowed_protocols, g.protocol_fallbacks
FROM providers p
JOIN provider_groups pg ON pg.provider_id = p.id
JOIN groups g ON g.id = pg.group_id
WHERE p.platform = 'openai'
  AND (p.extra->>'openai_oauth_responses_websockets_v2_mode' = 'http_bridge'
    OR p.extra->>'openai_apikey_responses_websockets_v2_mode' = 'http_bridge'
    OR p.extra->'openai_ws_force_http' = 'true'::jsonb);
```

## 关键破坏性历史升级

<a id="openai_oauth_import_defaults_removal"></a>
### 迁移 291：OpenAI OAuth 导入模板下线

`291_remove_openai_oauth_import_defaults.sql` 删除 `openai_oauth_import_defaults` 设置及编辑接口，存量提供商保持现状。前后端同升；回退依赖升级前备份。

<a id="user_localization_migration"></a>
### 迁移 290：用户可见内容多语言

- 停机要求：停全服务并备份数据库、settings 与 `pages/` 文件；前后端同升，旧 `_zh`/`_en` 字段写入会被拒绝；回退须恢复数据库备份。
- 变更要点：新增账户语言偏好与公告/套餐/分组多语言 JSONB，`zh` 转为 `zh-Hans`，无法判断的历史内容置 `source_locale=null`；网关错误保持英文单文本。完整规则见[用户侧国际化](../interfaces/user_localization.md)。

<a id="user_activity_migration"></a>
### 迁移 289：用户活动汇总

- 停机要求：创建 `usage_user_activity`、安装触发器并回填最近使用时间（持用量表写锁，发布前建议在备份库实测耗时）。
- 恢复与回退：当前备份包含该表；恢复早于 289 的备份后重启由迁移重建。回退应用可保留表结构，再次升级前或在回退期间删分区后须在维护事务内执行 `SELECT usage_rebuild_user_activity()`；分区清理使用 `usage_drop_partition_with_activity`。

<a id="antigravity_static_retirement"></a>
### 迁移 286：Antigravity 静态提供商停用

停旧后端备份后升级。迁移将未删除的 Antigravity `apikey`/`upstream` 设为 `status=inactive`、`schedulable=false` 并写调度 outbox，保留 ID、凭据和历史用量。需继续接中转网关时，改建对应协议的 Anthropic 或 Gemini API Key 提供商（Base URL 填完整前缀如 `https://gateway.example/antigravity`）。

<a id="provider_name_migration"></a>
### 迁移 282：提供商名称统一（Account -> Provider）

- 停机要求：等待在途结束并停旧后端，备份 PostgreSQL、Redis 与配置；回退需同时恢复三者与旧前端。
- SQL 迁移：`282_rename_accounts_to_providers.sql` 原地改名表、列、索引与序列（锁等待 10 秒、语句上限 120 秒，冲突全回滚；不回填 `usage_logs` 或重建索引）。管理端 API 切至 `/api/v1/admin/providers`（无旧接口兼容），启动配置自动兼容旧键名。JSON 导出标识仍为 `tokenrouter-data` v2，集合名改为 `providers`（拒绝旧 `accounts` 集合）。
- Redis 键迁移：新实例开放流量前分批迁移并发、会话、限流、停调与窗口键（保留类型与剩余 TTL），中断后按 `migration:provider-names:v1` 续跑；目标键冲突会停止启动并保留双方数据。

## 更早历史迁移速查表

从早于迁移 282 的旧版本跨多代升级时，均须停机、完整备份 PostgreSQL/Redis/配置并前后端整体切换；下表记录 234 到 284 期间的重要迁移与当前有效状态。

<a id="api_key_billing_cache_invalidation"></a>

| 迁移 | 专题 | 停机与关键操作 | 当前状态 |
| --- | --- | --- | --- |
| 284 | 模型属性档案与统一目录 | 整体升级；目录切至 models.dev（后在 v2 再升级），旧缓存独立保留 | 有效 |
| 无 | 文件价格覆盖退役 | `pricing.override_file` 退出构建；升级前需手工把售价迁入价格配置，提供商成本另配成本规则 | 有效 |
| 283 | 产品名称兼容 | 整体升级；详见[产品名称兼容](deployment_and_migrations.md#product_name_compatibility) | 有效 |
| 281 | 分组价格设置归入价格配置 | 停机；删除分组价格列与覆盖价，需在价格配置重新设置（不可直接回退旧二进制） | 有效 |
| 280 | Messages 专用模型覆盖下线 | 前后端同升；删除旧列，规则不自动复制，旧字段请求返回 400 | 有效 |
| 279 | Messages 系列默认映射下线 | 前后端同升；删除 Opus/Sonnet/Haiku 系列目标字段 | 被 280 取代 |
| 276–278 | 分组跨平台与统一价格配置 | 停机；删除分组/价格平台与默认分组，冲突价卡会阻断迁移，无分组 Key 需手工选组；旧策略归档至 `platform_independent_*_archive`；278 为创作台补 `provider` 快照 | 有效 |
| 275 | 移除重复价格和图片设置 | 停机；删除 `apply_pricing_to_provider_stats` 和旧图片桥接开关 | 有效 |
| 274 | 价格配置与分组策略拆分 | 停机；`channels` 改名 `pricing_configs`，非价格策略迁至 `groups.routing_policy`，旧渠道 API 下线 | 有效 |
| 273 | 统一协议能力 | 停机；旧文本路由/工作负载迁至 `credentials.upstream_protocols` 与 `allowed_protocols` | 有效 |
| 272 | 独立媒体定价下线 | 停机；删除分组 11 个独立媒体价格/倍率列，统一改用价格配置价卡或目录默认价 | 有效 |
| 270–271 | OpenAI 能力探测下线 | 整体升级；压缩与协议探测固定为显式开关，清理历史探测字段 | 有效 |
| 269 | 分组 Fast 与 Ultra Fast 策略 | 整体升级；新增四值 `openai_fast_policy`，旧布尔列保留兼容镜像 | 有效 |
| 265 | 数据共享功能下线 | 停机；删除共享表、列、索引与 9 项设置，`/api/v1/data-sharing/*` 返回 404；旧对象存储文件与配置需人工清理 | 有效 |
| 264 | 分组 OpenAI Fast 按 Standard 计费 | 整体升级；新增 `free_openai_fast` | 字段已由 281 移至价格配置 |
| 263 | 分组推理强度超限动作 | 整体升级；新增 `max_reasoning_effort_over_limit`（默认 `downgrade`） | 平台限制已由 277 取消 |
| 262 | 分组 OpenAI Fast 强制策略 | 整体升级；新增 `force_openai_fast` | 被 269 取代 |
| 261 | 缓存写入 1 小时分档 | 整体升级；价卡与区间新增可空 `cache_write_1h_price`（NULL 沿用旧价、0 免费） | 有效（表已由 274 改名） |
| 258 | API Key 结算字段缓存失效 | 整体升级；扩展 auth cache outbox 触发器纳入 `billing_mode` 与 `preferred_subscription_id`（仅存哈希） | 有效 |
| 257 | 创作台持久状态与 outbox | 整体升级；新增 `creative_run_outbox` 与结算恢复字段，托管 Key 建部分唯一索引 | 有效 |
| 248 | 国产供应商用户平台额度 | 整体升级；扩展平台 CHECK 约束 | 被 277 删除 |
| 246 | 分组逐模型定价 | 整体升级；分组新增 `model_pricing` 与长上下文开关 | 被 281 删除 |
| 242–245 | Grok 媒体/搜索/Voice 定价 | 整体升级；245 备份并清理非 Grok 视频配置至 `groups_video_price_backup_245` | 分组媒体价被 272/281 删除 |
| 241 | OpenAI 提供商级长上下文开关下线 | 停机；删除触发器、函数与 `extra.openai_long_context_billing_enabled` | 有效 |
| 238–240 | 通用高级调度器与优先级统一 | 停机；`scheduler_type` + `advanced_scheduler_overrides` 取代旧实验开关；240 删除 `provider_groups.priority`，统一用 `providers.priority` | 有效 |
| 237 | API Key 结算模式与批量图片快照 | 整体升级；新增 `billing_mode`（默认 `auto`）与 `preferred_subscription_id` | 有效 |
| 236 | 上游声明倍率探测下线 | 停机；删除探测字段与设置，`/v1/sub2api/billing` 等路由返回 404 | 有效 |
| 235 | 分组客户端协议 | 一次性切换；新增 `allowed_client_protocols`（库默认空数组 fail-closed） | 字段已由 273 改名 |
| 234 | 自研异步图片任务下线 | 停机；删除自研异步图片路由与 `image_storage_config`，历史 S3/R2 对象需人工盘点清理 | 有效 |

相关文档：[部署与数据库迁移](deployment_and_migrations.md)、[开发、验证与上游同步](development_workflow.md)、[运维目录](index.md)。
