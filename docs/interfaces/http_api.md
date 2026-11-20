# HTTP 接口

本文说明 TokenRouter 的路由族、中间件顺序、认证方式、核心业务接口约定、响应格式与模块归属。新增或调整接口时，据此确定落点与兼容性边界。本文不逐一列举所有端点；上游协议规范和领域状态机见各自文档。

新增创作台路由前，先读[创作台](../domains/creative_studio.md)，核对生命周期、幂等和留存规则。

## 章节导航

- [全局入口与中间件](#全局入口与中间件)：统一处理链、请求 ID 入口、限流与优雅停机。
- [路由族与模块归属](#路由族与模块归属)：URL 前缀、认证要求、模块边界和通用兼容约定。
- [认证与安全机制](#认证与安全机制)：各类凭据的授权范围与外部支付管理集成。
- [关键业务接口约定](#关键业务接口约定)：
  - [身份登录与 One Tap](#身份登录与-one-tap)、[API Key 结算策略](#api-key-结算策略)、[API Key 凭据轮换](#api_key_rotation)。
  - [分组协议、价格与路由策略](#分组协议价格与路由策略)、[提供商管理、测试与调度诊断](#提供商管理测试与调度诊断)、[创作台接口与管理设置](#创作台接口与管理设置)。
  - [支付管理恢复](#payment_admin_recovery)、[用户专属充值倍率与下单校验](#personal_recharge_multiplier_api)、[订阅自助撤销](#subscription_self_revoke_api)、[使用记录后台导出与统计筛选](#usage_export_tasks_api)。
  - [公告接口](#announcement_api)、[社交分享封面](#social_share_image)、[站点页面](#site_pages)、[备份与系统维护](#备份与系统维护)、[SystemOne 与 MCP OAuth 入口](#systemone-与-mcp-oauth-入口)。
- [面板命令幂等](#write_idempotency)：幂等键构成、重放、冲突和存储故障策略。
- [响应、错误与性能计时](#response_errors)：面板 envelope、协议错误格式与 `usage_logs.timings` 指标口径。
- [请求关联](#请求关联)：本地请求 ID 透传、资金去重与诊断查询。
- [已移除的接口](#已移除的接口)：已下线且返回 404 或 400 的路径与字段。
- [产品名称兼容](#product_name_compatibility)：新旧导入标识、请求头、WebSocket 子协议与浏览器存储键。
- [新增接口检查清单](#新增接口检查清单)：新增或迁移接口时的核对项。

## 全局入口与中间件

主 HTTP 服务在单个 Gin 引擎上安装公共中间件，并按 `common -> auth -> user -> admin -> plugin -> gateway -> payment -> page` 顺序挂载各模块路由。全局中间件执行顺序如下：

```text
RequestLogger
  -> security client IP / session binding context
  -> access logger
  -> CORS
  -> security headers / CSP
  -> optional Server-Timing
  -> embedded frontend and API routes
```

入口层面的公共规则：

- 本地请求 ID：`X-Request-ID` 由全局入口生成并由网关复用。`X-TokenRouter-Request-ID` 与兼容头返回同一本地 ID，响应提交时固定；调用方传入 ID 和供应商 ID 保存为可查询的外部别名。
- 客户端请求 ID：合法的 `X-Client-Request-ID` 原样回显，缺失时回退为本地 ID。完整生成、计费关联与留存规则见[请求 ID 与请求查询](../operations/request_lookup.md)。
- 网关前置保护：读取 JSON 或 multipart 之前，依次应用通用或文本 body limit、client request ID、Ops error logger、endpoint 归一化和 API Key 认证。
- 限流与审计：面板接口应用全局限流、重查询限流和审计；高风险公开认证接口使用独立 Redis 限流，Redis 故障时拒绝请求（fail-close）。
- 优雅停机：请求与上游平台尝试共用进入屏障；在途请求尾部全部完成后才停止完成队列，超时时报告未完成阶段及其持有者。

## 路由族与模块归属

| 路由族 | 认证方式 | 负责模块与用途 |
| --- | --- | --- |
| `/health`、`/setup/status` | 无 | `server`：进程健康检查和正常模式下的 setup 状态 |
| `/api/event_logging/batch` | 无 | 兼容 Claude Code 遥测的空接收端，固定返回成功 |
| `/api/v1/auth/*` | 大多公开；账户管理子流程按路由要求 JWT 或短期状态 | `identity`：注册、登录、刷新、找回密码、OAuth、Passkey 登录和身份补全 |
| `/api/v1/user/*`、`/keys`、`/team`、`/groups`、`/subscriptions`、`/redeem` 等 | 用户 JWT | `identity`、`team`、`apikey`、`billing`、`usage`：用户面板资源、团队、Key、用量和权益 |
| `/api/v1/marketplace/models`、`/api/v1/marketplace/stats` | 无 | `routing`：公开模型分组和首页汇总 |
| `/api/v1/marketplace/models/available` | 用户 JWT | `routing`：公开分组与当前用户获授权的专属分组，返回私有、不缓存响应 |
| `/api/v1/admin/plugins*`、`/api/v1/admin/plugin-catalog*`、`/api/v1/admin/plugin-indexes*` | 管理员（执行代码与来源变更另需 step-up） | `plugin`：安装、更新回滚、市场与索引来源，见[安装管理](plugin_contracts.md#administration) |
| `/api/v1/plugins/*` | UI 描述需用户 JWT；静态资源公开；API 按 manifest 决定 | `plugin`：插件页面描述、版本化静态资源和非流式 HTTP 代理，见[插件契约](plugin_contracts.md#http_contributions) |
| `/api/v1/admin/*` | 管理员 JWT 或受限管理密钥（部分操作另需 step-up） | 各领域管理入口：用户、分组、提供商、价格配置、设置、运维、备份、支付和安全管理 |
| `/mcp` | 专用 MCP Bearer 令牌或 OAuth access token | MCP 管理入口：四个通用管理工具，按操作授权，MCP JSON-RPC 协议，见 [MCP 管理接口](mcp_management.md) |
| `/api/v1/admin/mcp/oauth/*` | 管理员 JWT | MCP OAuth 浏览器授权与应用管理，见 [MCP OAuth 应用授权](mcp_management.md#oauth_authorization) |
| `/api/v1/payment/*` | 用户 JWT | `payment`：配置和套餐查询、下单、查单、取消、invoice 和申请退款 |
| `/api/v1/payment/public/*` | 签名 resume token 或旧订单验证约束 | `payment`：恢复已知订单的支付结果，禁止匿名列举订单 |
| `/api/v1/payment/webhook/*` | 提供商验签（按原始 body、query 和 Header） | `payment`：EasyPay、Alipay、WeChat Pay、Stripe、Airwallex、EPUSDT 异步回调 |
| `/v1/*` 及兼容裸路径别名 | TokenRouter API Key | `gateway`：Anthropic、OpenAI 兼容的消息、Responses、Chat、图片、视频、模型、用量、批任务及 `/v1/systemone` |
| `/v1beta/*` | TokenRouter API Key | `gateway`：Gemini 原生模型 URL、生成、流式生成和 token 统计 |
| `/antigravity/*` | TokenRouter API Key（强制平台） | `gateway`：Antigravity 专用 Claude 和 Gemini 入口及管理自省接口 |
| `/backend-api/codex/*` | TokenRouter API Key | `gateway`：Codex Responses、Realtime 和 sideband 兼容入口 |
| `/api/v1/pages/*` 等页面路由 | 按页面类型使用用户或管理员 JWT（公开图片除外） | `site`：服务端生成或读取的站点页面、pricing、账单和管理页面数据 |

### 模块边界与通用约定

变更接口时按以下模块边界划分职责，并保持路径、中间件顺序、JSON/CSV 格式、分页、ETag/304 和 WebSocket 子协议兼容：

| 领域 | 负责模块 | 关键契约 |
| --- | --- | --- |
| 提供商与代理 | `provider`、`egress`、`scheduler` | 敏感字段按既定脱敏规则输出；非敏感嵌套 map、slice 和时间指针返回独立副本，修改展示值不影响配置。高级调度诊断归 `scheduler`。 |
| 系统设置 | `settings` | 综合设置组合各领域端点；SMTP 测试归 `notification`，创作候选与 worker 状态归 `creative`，备份导出需 step-up、导入走管理员幂等。 |
| 订阅、兑换与套餐 | `billing` | `GET /api/v1/redeem/history` 按 `page`/`page_size` 分页、按使用时间倒序；管理员调账和插件余额调整返回可见 `notes`（插件记录含插件 ID 和原因），其余兑换码隐藏内部备注。管理员套餐保留 Ent 字段省略和 `edges` 格式，公开套餐使用单独整理结构。 |
| 身份、团队与 Key | `identity`、`team`、`apikey` | 用户资料、会话、七类身份、强认证归 `identity`，团队归 `team`，Key 归 `apikey`；微信支付 OAuth 单独归 `payment`。安全主体以 `Principal` 表示，Key 付款与成员上下文以 `AccessSnapshot` 表示。 |
| 支付与推广 | `payment`、`promotion` | 支付路由归 `payment`，推广和 Promo 归 `promotion`。Webhook 均基于原始 body、query 和 Header 验签。 |
| 用量、审计与运维 | `usage`、`audit`、`ops` | `/v1/usage` 和 Antigravity 用量自省区分 `quota_limited` 与 `unrestricted`、日期范围、余额和指定订阅，统计尽力而为。审计维持已清空 TOTP 和管理员 API Key 拒绝规则。留痕规则见[清理与留存](../operations/observability_and_data_lifecycle.md#data_cleanup)。 |
| 通知、搜索、风控与站点 | `notification`、`search`、`moderation`、`site`、`web` | 通知模板、SMTP 测试、公开退订归 `notification`；搜索配置、测试、额度重置归 `search`；风控配置、日志、媒体、Cyber 和解封归 `moderation`；公开设置、公告和站点页面归 `site`；静态 SPA 与 `data/public` 归 `web`。 |
| 网关与批量任务 | `gateway`、`gateway/errorpolicy`、`creative`、`batchimage` | 路由前缀只标识客户端协议，通用入口先按分组选出满足模型、协议和策略的提供商再调用平台执行器，模型族和分组展示品牌不影响供应商选择（`/antigravity/*` 额外强制平台）。普通 Key 协议门禁不提前读 body，复合 Key 按既定时机读模型并放回报文。WS 每轮单独记录首个错误及提供商、模型、规则匹配快照，采集与持久化归 `ops`。批量图片下载流关闭即释放下载许可，应用关闭等待完整 HTTP 调用结束。 |

跨路由的通用约定：

- 国际化：网站展示请求使用 `Accept-Language`，语言与回退规则见[用户侧国际化](user_localization.md)。`GET /api/v1/settings/legal/:id` 按语言返回协议，账户资料的 `preferred_locale` 保存用户选择。
- 短期下载票据：部分大文件下载通过短期签名票据支持浏览器原生下载，票据仅授权单个预生成资源，权限远小于用户 JWT。
- 非消费接口鉴权：模型列表、用量和已有批任务管理接口即使跳过余额检查，仍须校验 Key 身份与资源归属。
- 错误规则边界：网关错误规则归 `gateway/errorpolicy`，只影响客户端返回内容和是否跳过监控，不影响提供商健康、重试和扣费资格。

## 认证与安全机制

| 凭据 | 传递位置 | 授权范围 |
| --- | --- | --- |
| JWT access token | 面板 `Authorization: Bearer`，少数 WebSocket 用子协议 | 校验用户状态、token version 及可选会话绑定；管理员路由另校验角色 |
| Refresh token | `/api/v1/auth/refresh` 及 logout 约定 payload 或 cookie | 仅用于令牌轮换和撤销，不可直接访问业务资源 |
| 管理 API Key | 管理接口 `x-api-key` | 代表首个实际管理员；开启敏感操作 step-up 后，无法执行需要近期 TOTP 的操作 |
| TokenRouter API Key | 网关 `Authorization: Bearer`、`x-api-key`，Gemini 入口另支持 `x-goog-api-key` | 校验 Key、用户与团队、分组、IP、额度、订阅和资源归属；通用网关拒绝 query 参数传 Key |
| OAuth 与 pending 补全状态 | auth callback 与补全接口 | 由 provider state、浏览器会话、一次性完成码和过期时间共同约束 |
| Google GIS ID Token | `/api/v1/auth/oauth/google/one-tap` 请求体 | 经官方验证器校验后，提取 `sub` 和已验证邮箱进入 Google 身份或 pending 流程，仅限该接口使用 |
| 支付 webhook 签名 | 原始 body 或 query，加 provider header | 仅授权解释已绑定本地订单的通知，仍须校验金额和 metadata |
| 下载与 resume 票据 | 指定公共恢复或下载路由 | 有时限且限定单个资源和操作，无法升级为普通会话 |

认证中间件只确认请求者身份。资源归属、团队成员权限、管理员 step-up、支付订单 user ID、批任务 owner 和分组能力，仍由对应 HTTP 适配层和业务用例分别检查。

### 外部支付管理集成

外部支付服务通过管理 API Key（`x-api-key`）调用管理接口；管理员 JWT 用于交互式后台。

- 余额发放：优先调用 `POST /api/v1/admin/redeem-codes/create-and-redeem`，原子创建并兑换余额兑换码。调用方须提供稳定的业务 `code` 和 `Idempotency-Key`，重试复用这两个值，并按 200、409 和业务错误区分重放、冲突和失败。
- 查询与手工调账：前置查询使用 `GET /api/v1/admin/users/:id`；`POST /api/v1/admin/users/:id/balance` 仅用于明确的人工增减或补偿，同样要求幂等键。
- 嵌入页透传参数：购买页和用户自定义页面 URL 由前端追加 `user_id`、`token`、`theme`、`lang`、`ui_mode`、`src_host` 和 `src_url`。
- 凭据安全：`token` 是用户 Bearer 凭据，仅可发往部署者信任的 HTTPS 页面，接收方不得写入访问日志、分析参数或转发第三方。请求示例见[外部支付管理 API 指南](../guides/payments/admin_integration_api.md)。

## 关键业务接口约定

### 身份登录与 One Tap

- OAuth 登录 start：GitHub、Google、LinuxDo、DingTalk、WeChat 和 OIDC 同时支持 `GET` 和 `POST`。未启用腾讯天御或阿里云验证码时，`GET` 返回 `302` 跳转；启用任一种后，匿名登录须改用 `POST`，前端按响应 `data.authorize_url` 跳转。
- 验证码字段映射：腾讯票据传 `tencent_captcha_ticket` 和 `tencent_captcha_randstr`，阿里云 `captchaVerifyParam` 放入 `turnstile_token`。Passkey `/auth/passkey/login/begin` 沿用同一映射，`finish` 只接受 ceremony session 和 WebAuthn credential。
- 账号绑定：`*/bind/start` 面向已登录用户，不消费匿名登录验证码。
- Google One Tap 请求：`POST /api/v1/auth/oauth/google/one-tap` 接受浏览器 GIS 的 `credential`（上限 16 KiB）、本地 `redirect` 及可选 `aff_code`、`promo_code`。按客户端 IP 用 Redis 限流每分钟 20 次（Redis 故障时 fail-close）；不接收 Client Secret，token 和未验证 claims 均不写日志。
- Google One Tap 响应：已有用户返回 `status=authenticated` 及 `access_token`、`refresh_token`、`expires_in`、`token_type`。新用户只返回 `status=registration_required` 和本地 `redirect`，经 HttpOnly pending cookie 继续 `/auth/oauth/callback` 补全。
- One Tap 拒绝条件：One Tap 设置或 Google OAuth 配置无效、backend mode、启用腾讯或阿里云验证码、注册关闭、token 无效或用户不可登录时拒绝请求；仅开启 Turnstile 时校验范围不变。

### API Key 结算策略

普通 Key 必须绑定可用分组，系统无不经分组的调度入口；历史未绑定分组的 Key 保留字符串，调用前须先绑定。`fallback_when_group_unavailable` 仅可回退至管理员配置的回退分组，目标分组重新检查权限、模型、协议、团队和订阅范围。

`POST /api/v1/keys` 与 `PUT /api/v1/keys/{id}` 校验规则（handler 与 service 共用，内部调用同样受限）：

| 字段 | 规则 |
| --- | --- |
| `billing_mode`、`preferred_subscription_id` | `billing_mode` 支持 `auto`、`subscription`、`balance`。省略或 `auto` 时先扣订阅、不足由余额补足；`balance` 清除指定订阅；`subscription` 须指定当前付款主体（个人为本人，团队为 Team Owner）的有效订阅。 |
| `fast_mode_policy` | 取值 `follow_request`、`force_on`、`force_ultrafast`、`force_off`。创建省略或空串默认 `follow_request`；更新省略保留原值，传空串恢复 `follow_request`。详见 [Fast 与 Ultra Fast 策略](openai_upstream.md#openai_fast_policy)。 |
| `quota`、`rate_limit_5h`、`rate_limit_1d`、`rate_limit_7d` | 有限、非负、小于 `1e12` 的 USD 数值（适配 `DECIMAL(20,8)`）；`0` 表示不限额。 |
| `expires_in_days`、`expires_at` | 创建省略 `expires_in_days` 永不过期，提供须大于 0；更新传空 `expires_at` 清除到期时间，合法 RFC3339 设置到期点。 |

辅助查询与网关用量接口：

- 订阅选项：`GET /api/v1/keys/billing-options?scope=personal|team` 返回当前作用域有效订阅摘要（`id`、`plan_id`、`plan_name`、`expires_at`、`groups_restricted`、`applicable_groups`），团队作用域不返回成员个人订阅。
- 可用分组：`GET /api/v1/groups/available?scope=personal|team&subscription_id={id}` 带 `subscription_id` 时返回付款主体分组权限与套餐分组交集，不带返回常规可用分组（团队作用域同样不返回成员订阅）。
- 模型目录裁减：`available` 响应中的 `models` 和 `model_protocols` 来自有限模型目录供客户端选型，实际权限由请求时分组策略校验。`include_models` 默认 `true`，传 `false` 保留过滤但跳过模型、协议和属性计算（筛选页用轻量查询，Key 导出用完整查询）；非法值返回 400。
- 网关用量展示：`GET /v1/usage` 始终返回 `billing` 对象（至少含 `mode`、`source`、`preferred_subscription_id`、`available`、`unit`），Key 自身配额与滚动限额不受影响：
  - `source=subscription`：只返回所选订阅额度与剩余；指定订阅失效时仍归该订阅、`available=false`，不展示余额。
  - `source=balance`：只返回付款主体余额，不加载订阅额度。
  - `auto`：`source` 随当前可用订阅变化。

<a id="api_key_rotation"></a>
### API Key 凭据轮换

`POST /api/v1/keys/:id/rotate` 使用用户 JWT、用户限流和审计中间件，无请求体；成功返回含新 `key` 的 API Key DTO，ID 不变。

- 错误处理：非正整数 ID 返回 `400`；Key 不存在、不属于当前用户或系统托管统一返回 `404`；团队 Key 返回与编辑操作一致的团队上下文错误；读取后被并发修改返回 `409 API_KEY_ROTATION_CONFLICT`，调用方须刷新后重试。
- 前端交互：从更多菜单进入确认框，提交期间禁止重复点击和关闭，成功后更新列表凭据并提供复制入口。配置与用量保留、缓存失效和并发规则见 [API Key 凭据轮换](../domains/identity_and_tenancy.md#api_key_rotation)。

### 分组协议、价格与路由策略

- 分组协议字段：分组接口不返回 `platform` 和 `is_default`，管理请求和列表筛选不接受上游平台（提供商列表仍可按平台筛选）。协议使用 `allowed_protocols`、`protocol_fallbacks`（缺键=自动、空数组=仅原生、非空=限定有序目标）和 `responses_image_policy`；提供商使用 `credentials.upstream_protocols`。
- 兼容与能力查询：旧字段（`allowed_client_protocols`、媒体和 Live 开关、提供商文本路由）仅输入兼容，响应和导出统一用新结构。`GET /api/v1/admin/protocol-capabilities`（只读）返回全部协议、提供商原生 profile、单元素通用分组 profile、可用入口与转换目标，详见[统一协议能力](protocol_capabilities.md)。
- 路由策略与门禁：分组模型映射统一使用 `routing_policy.model_mapping`（`Record<string,string>`），白名单使用 `allowed_models`（`string[]`），均无平台层级。门禁拒绝在调用上游前返回对应协议 403；平台未实现入口返回 404。
- 共享价格配置：`/api/v1/admin/pricing/configs` 及 `/:id` 维护共享价卡。共享价卡和提供商成本价卡均无 `platform`，模型规则统一查重，未知字段返回 400。
- `reference_model` 校验：价格条目和属性规则可传 `reference_model` 引用同配置内另一具体模型的生效价格，省略或空串走字段覆盖模式。非空引用与非空覆盖字段并存、目标含通配符或目标命中本档案任一映射来源规则时，返回 `400 INVALID_MODEL_REFERENCE`。价格引用接受默认 `billing_mode=token` 占位，实际计费模式以目标为准；提供商成本条目传非空引用返回 400。属性预览与保存共用校验，预览用 `reference_model` 报告目标，实际路线仍在 `target`。
- 默认目录查询：只读接口 `/api/v1/admin/pricing/defaults` 和 `/model` 无需平台参数，默认列表可按来源标签筛选（见[管理员默认价格查询](model_catalog_and_marketplace.md#gateway_default_pricing)）。模型链字段用 `group_mapped`，历史用量关联字段用 `pricing_config_id`。
- 手动更新目录：`POST /api/v1/admin/pricing/defaults/update` 复用服务端已配置目录来源，不接受来源地址或文件路径；普通 GET 不触发更新。
- 计费设置字段：价格配置支持 `peak_rate_enabled`、`peak_start`、`peak_end`、`peak_rate_multiplier`、`long_context_pricing_enabled`、`free_openai_fast`、`batch_image_discount_multiplier`、`batch_image_hold_multiplier`，以及 5 项可空单价（`web_search_price_per_call`、`search_price_per_1k`、`audio_realtime_price_per_min`、`audio_tts_price_per_million_chars`、`audio_stt_price_per_hour`）。
- 计费设置约束：更新省略保留现值，5 项单价传 `null` 清除覆盖、传 `0` 表免费；预扣倍率不得低于折扣倍率，高峰窗口仅限同一天内有效时段。分组不含上述字段与 `model_pricing`，仅保留 `rate_multiplier`（控制台置于分组"基本"页，其余位于价格配置"计费设置"页；旧分组价格不自动复制）。
- 分组用量汇总：`GET /api/v1/admin/groups/usage-summary` 返回 `today_cost`、`yesterday_cost`、`total_cost`；自然日固定采用服务端配置时区，不接受浏览器时区参数。

### 提供商管理、测试与调度诊断

- 分组关联：创建、编辑、批量创建/更新和可选分组导入均使用显式 `group_ids`，可关联任意分组。通用备份不含分组关系，未指定分组不自动绑定默认分组；Spark 影子未指定分组时可继承母提供商已有分组（母提供商无分组则影子同样无分组）。OAuth-only、隐私和实际协议能力继续约束资格。
- 严格 JSON 绑定：提供商写入、备份、Codex 导入及 OAuth/PAT/SSO 创建入口对 `confirm_mixed_channel_risk`、`skip_default_group_bind` 等未知结构字段返回 400 且无确认旁路；`credentials`、`extra` 等动态 map 照常接收并由领域层校验。
- 批量删除：`POST /api/v1/admin/providers/batch-delete` 接收 `provider_ids`，去除非正数和重复 ID 后最多 5 路并发删除。同批含父提供商及影子时仅删根提供商一次，级联影响映射回各项；响应按固定顺序返回 `success_ids`、`failed_ids` 与错误明细，单项失败不取消其余项。前端"全选筛选结果"按同一筛选快照分页拉取轻量 ID，缺页或重复则保留原选择，完整后才提交。
- 连接测试（`POST /api/v1/admin/providers/:id/test`，SSE）：
  - 支持参数：`model_id`、`prompt`、OpenAI 专用 `mode`、`test_type`（`text`/`image`，旧别名 `test_mode`）及文字测试 `protocol`（OpenAI API Key 可选 `responses`/`chat_completions`，OAuth 仅 `responses`；Kimi、Zhipu、DeepSeek 可在已启用的 `chat_completions`/`anthropic`/`responses` 中选；其他平台带此字段被拒）。
  - 提示词与载荷：管理端均发送 `test_type`（未传时旧调用按模型名兼容判断），`text` 和 `image` 使用自定义提示词；OpenAI `compact`/`legacy_compact` 为固定载荷测试，忽略提示词且不改能力开关。
  - 结果与事件：测试结果不作为能力探测状态返回或保存，但认证错误、限流和额度观测照常记录。图片走 `image` 事件，文字走 `content` 事件，平台无对应图片端点时返回流式错误事件。
- 高级调度评分诊断（仅管理员，不占并发槽、不写粘性，不返回凭据、代理认证、session hash 和上游响应）：
  - `GET /api/v1/admin/providers/:id/advanced-scheduler-score`：返回所属高级分组摘要；带 `group_id` 返回完整候选池、硬过滤结果、有效配置、指标原值/归一化值/贡献、Top-K 权重、活动池概率与平台策略提示。
  - 诊断权重口径：开启订阅优先且有合格订阅提供商时，普通提供商标记延后且不参与概率。开启粘性加权时 previous-response 和 session 仅影响 Top-K 权重，关闭时有效硬粘性显示概率 1。
  - `POST /api/v1/admin/providers/:id/advanced-scheduler-score/preview`：接受 `group_id` 及可选 `requested_model`、`sticky_provider_id`、`previous_response_provider_id` 做无状态模拟，拒绝其他字段。不支持 previous-response 的提供商返回 `ignored`；分组摘要无平台字段，未关联目标分组标记 `group_mismatch`。
  - 硬过滤范围：复用仅凭请求信息可判断的硬过滤；endpoint、transport、compact、media 等依赖请求上下文的能力标记 `not_evaluated`。
- 同步、导入与用量：
  - CRS 同步与预览位于 `/api/v1/admin/providers/sync/crs` 和 `/preview`，默认同步代理（传 `false` 关闭）；"只创建选中项"传空集时已存在提供商照常更新。
  - Codex session 导入位于 `/api/v1/admin/providers/import/codex-session`，含请求校验、逐项结果和幂等 scope。
  - Ollama Cloud 的设置、状态、会话和刷新路由需管理员认证，敏感会话内容不回显。
  - 提供商主动/被动用量、批量用量、今日统计和批量今日统计带 30 秒快照缓存、`ETag`、`Vary`、`304` 和 `X-Snapshot-Cache`。
- API Key 上游用量手动查询：
  - 入口为 `POST /api/v1/admin/providers/:id/upstream-usage/query` 和 `POST /api/v1/admin/providers/upstream-usage/query/batch`（`provider_ids` 最多 100 个正整数），仅接受 `type=apikey`（Bedrock 除外），经管理员认证与审计中间件。
  - 适配器可选 `sub2api`、`new_api`、`zivv`、`derouter`（Kimi、Zhipu、DeepSeek 按平台固定），由提供商配置显式指定。成功结果顶层含 `adapter`、`provider`、UTC `observed_at` 及余额、限额、订阅字段；批量接口分开返回成功与结构化错误。
  - 错误 reason 属 `UPSTREAM_USAGE_*` 命名空间，覆盖提供商无效或禁用、协议不支持、认证失败、钱包不可用、钱包认证失败、限流、超时、响应格式错误、网络错误和身份变化。
  - 查询完全只读，不写入提供商、Extra、调度快照或计费记录，响应和审计 body 不含 API Key。前端仅点击触发，成功结果按管理员隔离缓存在 `sessionStorage` 5 分钟，规则见 [API Key 上游用量查询](upstream_usage.md)。

### 创作台接口与管理设置

创作台（Creative Studio）路由族 `/api/v1/creative/*` 需用户 JWT 并返回统一 envelope，用于个人图片生成、编辑和局部重绘：

```text
GET  /api/v1/creative/models
GET  /api/v1/creative/capabilities
POST /api/v1/creative/runs
GET  /api/v1/creative/runs
GET  /api/v1/creative/runs/active?limit=100&cursor=<opaque>
GET  /api/v1/creative/runs/{id}
GET  /api/v1/creative/runs/{id}/outputs/{index}/content
POST /api/v1/creative/runs/{id}/outputs/{index}/ack
```

用户端执行约束：

- 工作区隔离：除 `/creative/models` 和 `/creative/capabilities` 外，均须传 `X-Creative-Workspace-ID`（规范化小写 UUID）。缺失返回 `400 CREATIVE_WORKSPACE_REQUIRED`，格式错误返回 `400 CREATIVE_WORKSPACE_INVALID`，归属不符统一返回 `404 CREATIVE_RUN_NOT_FOUND`。
- 能力与活动列表：`GET /creative/capabilities` 返回 `max_prompt_chars`、`max_asset_bytes`、`max_total_input_bytes`、`max_mask_bytes` 及允许的 PNG、JPEG、WebP MIME。`GET /creative/runs/active` 用不透明 cursor 分页返回全部活动任务，不受历史页大小限制。
- 表单字段与输出格式：`POST /creative/runs` 接受 `multipart/form-data`，支持素材及 `image_size`、`aspect_ratio`、`quality`、`background`、`thinking_level`。输出格式由供应商决定，传 `output_format`、`output_compression` 或旧 `response_mime_type` 会被拒绝；输出 metadata 的 `mime_type` 记录实际 MIME。
- 平台能力校验：参数按 `/creative/models` 校验，每任务固定生成一张图。OpenAI 支持 `generate`、`edit`、`inpaint`；Gemini 和 Grok 支持 `generate`、`edit`（Grok 编辑最多 3 张源图，Gemini 不接受单独 mask）。素材仅接受上传文件，拒绝远程 URL。
- 幂等键：提交可带 `Idempotency-Key`。同用户、同工作区、同键且请求体相同返回原任务（`idempotent_replay=true`）；请求体不同返回 `409 CREATIVE_IDEMPOTENCY_CONFLICT`；不同工作区可用同名键各自创建。
- 输出交付与 ack：仅结算完成且可交付的终态任务可读取输出。ack 先写库再删服务端临时输出，删除失败由后台清理补做。
- 临时输出失效：过期或丢失时输出路由返回 410 类错误（`CREATIVE_OUTPUT_EXPIRED` 或 `CREATIVE_RESULT_LOST`）并将任务置为 `result_lost`；供应商已成功但临时输出不可交付时仍按确认服务计费，不自动重生成。数据库仅存元数据，图片和 prompt 明文仅存 Redis 临时键，详见[创作台](../domains/creative_studio.md)。

管理设置与运行状态：

- 模型白名单：`GET|PUT /api/v1/admin/settings` 的 `creative_model_settings` 格式为 `[{"group_id":123,"model":"gpt-image-2","operations":["generate","edit","inpaint"]}]`，省略保留现值，空数组清空。服务端校验能力值和 `(group_id, model)` 唯一性，审计只记是否变化。
- 能力规范化：保存和执行按候选提供商的实际 provider 规范化操作，同分组不同模型可对应不同 provider；解析不出能力的白名单条目不可调用。
- 候选与并发：`GET /api/v1/admin/settings/creative-model-candidates` 返回分组 active、启用图片生成且有可调度图片模型的 `{group_id, group_name, platform, model, operations}`，不按管理员权限过滤，`platform` 为实际 provider。任务提交前固定提供商、provider、参数和价格快照。`creative_worker_count` 为大于 0 整数（默认 128），保存后热更新本实例 worker 池。
- 池状态查询：`GET /api/v1/admin/settings/creative-worker-status` 返回 `{running, worker_count, busy_workers}`；未运行时返回 `running=false` 的零值快照，前端改显 `creative_worker_count` 配置值。

<a id="payment_admin_recovery"></a>
### 支付管理恢复

`GET/POST /api/v1/payment/webhook/epusdt` 接收 EPay（易支付）MD5 签名的 query 或 form，成功返回 HTTP 200 和纯文本 `success`，通过商户验签、收银台付款查询和订单绑定授权。

下单返回 `503 PAYMENT_CREATE_UNCONFIRMED` 时，`metadata` 提供 `order_id`、`out_trade_no`、`amount`、`pay_amount`、`currency` 和 `expires_at` 供购买页继续查单，规则见 [EPUSDT 加密货币支付（EPay 协议）](../domains/payments_and_entitlements.md#epusdt_payments)。

以下管理员支付接口均经过管理员认证、面板限流和审计中间件：

| 接口 | 行为与约束 |
| --- | --- |
| `POST /api/v1/admin/payment/orders/{id}/refund`<br>`POST /api/v1/admin/payment/orders/{id}/refund/query` | 响应含 `status`（`succeeded`、`pending`、`failed`、`requires_force`），同时保留 `success`、`warning`、`require_force`。订阅金额按订单价格币种校验，部分退款返回 `SUBSCRIPTION_REFUND_FULL_ONLY`；`deduct_balance` 表示是否暂停并撤销原订阅。查单同时接受 `REFUND_PENDING` 和带有效准备记录的 `REFUNDING`，只查已发起退款、不重复发起渠道退款；请求体可省略，收到 `REFUND_REFERENCE_REQUIRED` 后可传 `{"refund_id":"re_…"}` 由 Stripe 核验所属支付、金额、币种和创建时间（仅限待恢复且缺渠道参考的 Stripe 退款，已存退款 ID 禁止替换）。恢复记录不完整、矛盾或渠道无法确认时返回要求人工核实的错误。 |
| `POST /api/v1/admin/payment/orders/{id}/force-expire` | 必填 `reason`（1 到 500 字符）。仅 `PENDING` 订单可改为 `EXPIRED`，不调用上游，成功返回 `data.message=force_expired`。不存在返回 `NOT_FOUND`，并发变更返回 `409 ORDER_STATUS_CHANGED`。迟到付款仍可通过正常 webhook 恢复。 |
| `POST /api/v1/admin/payment/providers/test` | 接受 `provider_key`、`config` 和可选 `instance_id`（目前仅支持 `easypay`）。带 `instance_id` 时按更新规则合并未回传的敏感字段，再用随机订单号做只读查单。不保存草稿、不建单，成功仅返回 `data.reachable=true`，不暴露上游 body、URL 细节或凭据。 |
| `POST /api/v1/admin/payment/plans`<br>`PUT /api/v1/admin/payment/plans/{id}` | 请求中的 `group_ids` 必须全指向现存分组，否则返回 `404 GROUP_NOT_FOUND` 且不写入；更新省略 `group_ids` 时保留原分组且不查分组。 |

其他支付保护约束：普通取消无法确认上游支付状态时返回 `503 PAYMENT_STATUS_UNAVAILABLE`。当某 provider instance 仍存在强制过期且尚未恢复的订单时，删除返回 `409 FORCED_EXPIRED_ORDERS`，管理员应停用并保留该实例以接收迟到回调。

<a id="personal_recharge_multiplier_api"></a>
### 用户专属充值倍率与下单校验

- 管理员读写：用户列表、详情和更新响应含可空数字 `balance_recharge_multiplier`（普通用户资料更新无此可写字段）。`PUT /api/v1/admin/users/:id` 省略保留原值，传 `null` 清除覆盖，传有限正数设置覆盖，其余返回 `INVALID_BALANCE_RECHARGE_MULTIPLIER`；MCP `users.update` 语义相同。
- 列表排序：`GET /api/v1/admin/users` 支持 `sort_by=balance_recharge_multiplier` 和 `sort_order=asc|desc`，同值按同方向用户 ID 排序，NULL 始终排在末尾。
- 预览与下单：`GET /api/v1/payment/checkout-info` 需用户身份，返回 `balance_recharge_multiplier`（实际倍率）、`default_balance_recharge_multiplier`（全局倍率）和 `user_balance_recharge_multiplier`（可空专属倍率）。下单不接收用户指定倍率，由服务端读取当前条款，规则见[用户专属充值倍率](../domains/payments_and_entitlements.md#personal_recharge_multiplier)。
- 下单前置校验：在订单保存和渠道支付创建前返回 HTTP 400。`order_type` 省略或空串表示 `balance`，其余只接受 `subscription`，未知值返回 `INVALID_CHECKOUT_ORDER_TYPE`；余额到账金额舍入后为零、非有限值或达到 1 万亿时返回 `INVALID_CREDITED_AMOUNT`。

<a id="subscription_self_revoke_api"></a>
### 订阅自助撤销

`POST /api/v1/subscriptions/:id/revoke` 需要用户 JWT，提供带额度条件的自助撤销：

- 准入与事务：仅接受当前用户本人、处于 `active` 且最高层有限额度已用完的订阅。成功时在事务内撤销当前记录，让同套餐下一份 pending 订阅提前生效，并自动改绑指向原订阅的 Key。
- 响应结构：`data` 为 `{ revoked_subscription_id, replacement_subscription_id, rebound_api_key_count }`；无接续订阅时 `replacement_subscription_id` 为 `null`，改绑数为 `0`。
- 错误与边界：订阅不存在或不属于当前用户返回 `SUBSCRIPTION_NOT_FOUND`；非 `active` 返回 `409 SUBSCRIPTION_NOT_ACTIVE`；最高层额度仍有余或不限额返回 `409 SUBSCRIPTION_QUOTA_NOT_EXHAUSTED`。撤销不退款，用户端无恢复接口。

<a id="usage_export_tasks_api"></a>
### 使用记录后台导出与统计筛选

用户和管理员的使用记录导出统一通过后台任务生成带 UTF-8 BOM 的 `.csv` 文件：

- 路由与中间件：用户入口为 `/api/v1/usage/export-tasks`（`GET` 列表、`POST` 创建、`POST /:id/cancel` 取消、`POST /:id/download-ticket` 换票、`GET /:id/download` 下载），创建、轮询和换票不占重查询限流配额。管理员入口位于 `/api/v1/admin/usage/export-tasks` 对应子路径，经过管理员认证、面板限流和审计中间件。
- 列表观测：除分页记录外，返回作用域内全量活动任务数 `active_count` 和 `active_ids`，支持 `watch_ids` 查询跨分页任务的终态变化（`watched_items`）。
- 创建参数与幂等：提交左闭右开 `[start_time, end_time)`（支持带偏移 RFC3339 或按 `timezone` 解析的秒级本地时间 `YYYY-MM-DDTHH:mm:ss`）、必填 IANA `timezone` 和筛选条件，接入面板命令幂等执行器（`Idempotency-Key`）。
- 配额校验：活动任务（`pending` 与 `running`）在入库事务内校验配额，超限返回 `429 USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED`，功能关闭返回 `503 USAGE_EXPORT_DISABLED`。
- 浏览器与 API 下载：浏览器先调用 `POST .../export-tasks/:id/download-ticket` 获取 60 秒有效期的 HMAC-SHA256 签名票据（绑定任务 ID、调用作用域、请求者用户 ID 和过期时间戳），再经公共路由 `GET /api/v1/usage/export-downloads/:ticket` 原生流式下载。API 和 MCP 客户端也可直接持 Bearer 凭据请求 `GET .../export-tasks/:id/download`。
- 产物状态错误：文件超保留期或本地产物缺失返回 `410 USAGE_EXPORT_EXPIRED`，任务未完成返回 `409 USAGE_EXPORT_NOT_READY`。

管理员用量统计 `GET /api/v1/admin/usage/stats` 的可选参数 `endpoint_source` 接受 `inbound`、`upstream`、`path`；省略、空值或 `all` 返回全部端点维度，其他值返回 400。摘要字段照常返回，缓存按端点来源分别保存；管理页先加载摘要和入站端点，切换图表后再拉取上游端点或路径，筛选变化和离开页面时取消过期请求。

<a id="announcement_api"></a>
### 公告接口

公告归属 `site` 模块，用户端入口为 `GET /api/v1/announcements` 和 `POST /api/v1/announcements/:id/read`，管理员增删改查和已读状态位于 `/api/v1/admin/announcements`，接入统一的认证、审计和限流中间件。

- 定向与归档：`site` 负责定向条件校验、余额和有效订阅匹配、起止时间、已读及到期归档；管理员查询前先归档过期公告，归档失败直接返回错误。
- 兼容约定：起止时间省略保留原值、传零值清空，分页排序和 JSON 格式保持兼容；重复标记已读保留首次读取时间。

<a id="social_share_image"></a>
### 社交分享封面

`GET/HEAD /api/v1/settings/share-image/:hash` 无须登录，返回当前已保存封面的图片字节，继承公开设置接口限流。路径中的 SHA-256 摘要必须与当前图片一致；图片已替换、已移除或摘要错误返回 404，存储故障返回错误响应。

成功响应带实际图片 `Content-Type`、`X-Content-Type-Options: nosniff`、摘要 `ETag` 和 `Cache-Control: public, max-age=3600, must-revalidate`，支持条件请求与 `HEAD`，错误响应使用 `no-store`。更换图片会生成新地址；移除图片后源站停止提供旧内容，但客户端和平台此前缓存的图片可能仍会显示。

<a id="site_pages"></a>
### 站点页面

- 访问控制：Markdown 正文需用户 JWT 且菜单可见；管理员页面仅对管理员开放；页面列表需管理员权限。图片接口公开无需 JWT，仅接受普通可见页面的 PNG、JPEG、GIF、WebP、SVG、AVIF、ICO 和 BMP 文件，Markdown 正文与图片响应格式相互独立。
- 安全与语言回退：图片请求路径和符号链接解析目标均校验扩展名；正文指向页面根目录外的符号链接返回 404，根目录内允许读取，正文读取上限 1 MiB，校验与限量读取在同一打开的文件句柄上完成。正文按 `pages/<slug>/<locale>.md`、`pages/<slug>.md` 顺序选择并通过 `Content-Language` 报告实际语言；语言专属图片目录缺文件时回退公共目录。

### 备份与系统维护

`/api/v1/admin/backups`（`backup`）和 `/api/v1/admin/system`（`ops`）需管理员身份和 step-up；恢复操作复核密码、校验 ID 并使用幂等 envelope。旧 data management 路由保留为已下线错误响应。

### SystemOne 与 MCP OAuth 入口

- SystemOne：`POST /v1/systemone` 使用网关 API Key 认证，支持普通 Key 和复合 Key。分组开放 `systemone` 后调度至已启用该协议的 Jev API Key 提供商，请求字段、成功响应和用量异常处理见 [Jev 与 SystemOne](jev_upstream.md)。
- MCP OAuth：`/mcp` 同时接受专用 Token 和 OAuth access token。OAuth 公开发现与协议端点使用标准响应，浏览器授权与应用管理位于 `/api/v1/admin/mcp/oauth`（需管理员 JWT），详见 [MCP OAuth 应用授权](mcp_management.md#oauth_authorization)。

<a id="write_idempotency"></a>
## 面板命令幂等

需要幂等保护的用户和管理员写接口共用同一幂等协调机制（由 `idempotency` 负责认领、指纹、重放、冲突、退避和响应存储）。

- 键构成与响应头：命令身份由业务 scope、操作者范围、HTTP 方法、路由和 `Idempotency-Key` 组成；同键不同 payload 触发指纹冲突。重放响应带 `X-Idempotency-Replayed: true`；处理中或退避中返回 `Retry-After`。存储响应按 UTF-8 截断并脱敏；TTL 和 observe-only 行为均由协调器配置控制。
- 存储故障策略：用户侧和默认管理员接口在存储不可用时拒绝请求（fail-close）；显式启用降级模式的管理员操作可继续执行并返回 `X-Idempotency-Degraded: store-unavailable`。
- 分层与停机：面板命令幂等与 billing 资金事务去重是两层独立保护；维护操作锁拥有独立全局范围与续租机制。应用停机时等待幂等清理 worker 退出后才关闭数据库。

<a id="response_errors"></a>
## 响应、错误与性能计时

### 面板 Envelope 与业务错误

面板和内部 REST 接口统一使用 envelope：

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

- 列表分页使用 `items`、`total`、`page`、`page_size` 和 `pages`；创建和异步接受可返回 201 和 202。
- 插件 API 代理的有效响应透传插件原始 body 和状态码；代理自身的鉴权、路由和传输错误仍用面板错误格式。插件 UI 描述和管理接口沿用普通 envelope。
- 业务错误定义在 `pkg/apperror`（`ApplicationError`），由 `server/httpx` 映射 HTTP 状态码，可携带 `reason` 和字符串 `metadata`，自定义状态码按原值映射。未知错误按 500 返回，脱敏详情仅记服务端。

### 网关协议错误

网关内置错误固定使用英文，并按调用方协议格式返回（认证失败、未分组、复合 Key 和本地能力拒绝同样使用当前协议 writer）：

| 协议入口 | 错误结构 | 分组禁用协议（403）错误标识 |
| --- | --- | --- |
| OpenAI | `error` 对象 | `protocol_not_allowed` |
| Anthropic | `type: error` 加嵌套错误 | `permission_error` |
| Gemini / Google | HTTP code、message、status | `PERMISSION_DENIED` |

- 分组禁用客户端协议时，在提供商选择、计费、重试和 fallback 之前返回 403 并记录 `LocalPolicyDenied`。模型列表 GET 不受生成协议开关限制。
- 错误响应隐去上游凭据、代理 URL、原始 service account、数据库错误和未脱敏请求正文。流式响应一旦开始，无法再写普通 JSON 错误，只能按流协议结束或发送协议允许的错误事件。

### 使用记录性能观测与计时

性能观测唯一保存在 `usage_logs.timings`。普通用量接口返回由该行派生的 `ttft_ms`、`tps` 和 `ttft_mode`，管理员接口另返回 `detailed_timing`。请求详情按请求 ID 批量关联 usage 并分别展示，不回写请求摘要，也不从 Ops 日志补值；历史无快照记录显示未记录。

`timings` 保存当次模式、上游流式与完成状态、usage 完整性及 `values` 中的毫秒观测，所有事件时刻均以所属逻辑请求入口为参考：

| 类别 | 字段 | 口径说明 |
| --- | --- | --- |
| 上游单次尝试 | `upstream_started_ms`、`upstream_first_event_ms`、`upstream_first_output_ms`、`upstream_last_output_ms`、`upstream_finished_ms` | 归属产生该条 usage 的同一次上游尝试。首包和首字分别减去本次 `upstream_started_ms`，排除排队与先前失败尝试。`duration_ms` 为该次尝试耗时，请求摘要总耗时覆盖整个逻辑请求。 |
| 首次准入分解 | `request_body_read_ms`、`preprocess_ms`、`user_slot_wait_ms`、`provider_slot_wait_ms`、`retry_provider_slot_wait_ms`、`provider_slot_acquired_ms` | 分解首次准入前耗时：原始 body 读取才计入 `request_body_read_ms`，容量检查执行时间归 `preprocess_ms`，等待只统计检查间排队，`retry_provider_slot_wait_ms` 单列后续重试等待。`provider_slot_acquired_ms` 是首次取得槽位的时刻，不可与连接、发送、响应和输出事件作时长相加。 |
| 准入冻结标记 | `timing_admitted` | 启用 HTTP 文本计时的请求（含原生 Gemini 和 Antigravity 专用入口）在取得槽位后、粘性绑定和上游执行前记录准入，首次置为 `1` 并冻结四段耗时与槽位时刻，重试不移动；始终未取得槽位保持 `0` 且无槽位时刻。 |
| 可选细分子阶段与载荷体积 | `request_raw_bytes`、`request_decoded_bytes`、`request_compressed`、`upstream_request_bytes`；`request_body_decode_ms`、`ingress_validate_ms`、`bootstrap_ms`、`moderation_extract_ms`、`moderation_external_ms`、`session_ms`、`scheduler_select_ms`、`provider_prepare_ms` | 按实际路径附带于 `detailed_timing` 和失败阶段快照。`request_decoded_bytes` 为解压与宽容 JSON 规范化后字节，`request_compressed` 为 `0`/`1`，`upstream_request_bytes` 为当次上游实际发送字节。准入前子阶段属 `preprocess_ms` 细分，`provider_prepare_ms` 为取得槽位后至上游交换开始前的报文准备耗时，均不改变前述四段准入耗时求和关系。 |
| 下游交付时刻 | `first_visible_output_ms`、`first_downstream_flush_ms` | 分别表示成功写入最终下游 writer 和包含上游事件的首次 HTTP Flush（可由元数据触发）；二者不证明客户端已收到或渲染，本地错误与排队心跳不建立。 |

内容判定与 TPS 计算规则：

- 首事件与有效内容：首事件允许正常元数据，排除心跳和空保活 delta。有效内容包括正文、明文思考、工具名称及实际参数，排除单独角色、ID、空占位、签名和密文；重复的完整终态不延长输出区间。
- TPS 公式与条件：`输出 token × 1000 /（最后有效内容时刻 − 上游调用开始时刻）`，服务端统一保留一位小数。仅当上游为流式且正常完成、usage 完整、输出 token 为正、起止观测齐全且区间为正时生成（单块输出亦可计算）。
- TPS 统计边界：区间包含本次调用的连接、上游等待、预填充、思考与内容传输，排除客户端上传、准入排队和先前失败尝试；末次有效内容之后的 usage、重复完成快照和连接关闭不延长区间。TPS 受网络与背压影响，不代表模型内部解码速度，不受 `ttft_mode` 影响；完整历史记录按同一公式计算，缺失观测显示未记录。
- Chat 与 Responses 完成判定：Chat 流的非空 `finish_reason` 或最终输出 usage 即可确认完成，不要求额外 `[DONE]`（缺最终计数仍不生成 TPS）。Responses 分别记录工具名与输入参数是否到达，参数首次出现在 `.done`、完整 item 或完成快照中计入有效输出，后续重复不延长区间。
- 协议与缺失口径：HTTP 文本流、协议转换和文本 WebSocket 共用内容判定（WS 每轮及重试独立归属）。真正非流式上游无首字、首事件或 TPS；上游流式、下游缓冲按各自模式展示；未知字段保持缺失，实测零保留为零，不适用字段不补造。

## 请求关联

本地请求 ID 贯穿 HTTP 响应、日志、使用记录、错误、审核和审计。`usage_logs.billing_key` 保存资金去重值，账本和资金去重表既有字段继续保存历史动作键；客户端重试生成新调用 ID，任务重放按业务动作键去重。

管理员通过 `/api/v1/admin/requests` 按 `request_id` 查询诊断详情，写入状态接口同样需要管理员身份，调用方和供应商 ID 可作搜索别名；普通用户在使用记录中筛选和复制请求 ID。后台完成任务携带请求快照，在独立超时内写入用量和请求摘要。协议、兼容头和权限细节见[请求 ID 与请求查询](../operations/request_lookup.md)。

## 已移除的接口

以下路径和字段已下线，无兼容 handler、重定向或弃用提示，调用方须改用新接口：

| 路径或字段 | 返回 | 替代方案 |
| --- | --- | --- |
| `GET /api/v1/requests`、`GET /api/v1/requests/:request_id` | `404` | 管理员使用 `/api/v1/admin/requests`，普通用户通过用量记录查看 |
| `GET`、`PUT /api/v1/admin/settings/openai-oauth-import-defaults` | `404` | 在提供商创建表单或导入文件中填写配置 |
| `GET /v1/sub2api/billing` | `404` | 无；不再享有 API Key 非消费请求豁免 |
| `GET`、`PUT /api/v1/admin/providers/upstream-billing-probe/settings`、`POST .../upstream-billing-probe/batch`、`PUT`、`POST /api/v1/admin/providers/:id/upstream-billing-probe` | `404` | 无 |
| `POST /api/v1/admin/providers/:id/refresh-tier`、`POST /api/v1/admin/providers/batch-refresh-tier` | `404` | 个人与 Google One 账号改用 Antigravity 平台自动同步订阅档位；GCP Code Assist 在刷新与授权时自动同步 tier |
| `POST /api/v1/admin/providers/check-mixed-channel` | `404` | 提供商写入时直接校验 |
| `GET /api/v1/user/platform-quotas`、`GET`、`PUT /api/v1/admin/users/:id/platform-quotas`、`POST /api/v1/admin/users/:id/platform-quotas/reset` | `404` | 提供商上游额度、余额、订阅、团队和 Key 限额各自接口 |
| `/api/v1/admin/channels` | `404` | `/api/v1/admin/pricing/configs` |
| `/api/v1/admin/pricing/defaults/models`（批量补入目录模型） | `404` | 在价格配置中手动添加模型规则 |
| `GET /api/v1/admin/groups/:id/stats`（管理员已认证） | `404` | `/admin/groups/usage-summary` 和 `/capacity-summary` |
| `GET /api/v1/admin/redeem-codes/stats` | 按兑换码 ID 路由处理，返回非法 ID 的 `400` | 列表条目和分页总数 |
| 综合设置 `default_platform_quotas`、`auth_source_default_*_platform_quotas`、`allow_ungrouped_key_scheduling` | `400` / `REMOVED_SETTING_FIELD` | 无 |
| Key 的 `fallback_to_default_group_when_unavailable`（含 `false` 和 `null`） | `400` | `fallback_when_group_unavailable` |
| 分组的 `platform`、`is_default` | `400` | `allowed_protocols` 等协议字段 |
| `messages_dispatch_model_config` | `400` | `routing_policy.model_mapping` |
| 综合设置 `grok_cross_client_model_map_enabled` | `400` | 分组或提供商手动配置的模型映射 |

<a id="product_name_compatibility"></a>
## 产品名称兼容

- 数据导入导出：提供商和代理导出使用 `type=tokenrouter-data`、`version=2`；导入兼容 `type=sub2api-data` 的 v2 文件，拒绝 v1、缺版本或含不支持字段的文件，不因此重新启用旧账号集合。
- 关联请求头：网关响应同时输出同值的 `X-TokenRouter-Request-ID` 和 `X-Sub2API-Request-ID`；入口会剥离这两个内部头，调用方无法指定内部身份，也不会透传给上游。`X-Client-Request-ID` 用法不变。
- Grok 缓存头：Grok 请求缓存开关优先读取 `X-TokenRouter-Grok-Client-Tool-Cache`（显式关闭优先），缺失时兼容 `X-Sub2API-Grok-Client-Tool-Cache`，二者仅本地生效。
- Ops WebSocket：优先协商 `tokenrouter-admin`，兼容 `sub2api-admin`；JWT 继续通过独立 `jwt.<token>` 项提供且不回显。
- 浏览器存储键：语言设置和登录协议确认使用 `tokenrouter_` 前缀。新键无值时复制旧键值（协议仍按 revision 判定）；新键已有值或撤回记录时以新键为准。可重建缓存直接使用新名，旧缓存自然失效。

## 新增接口检查清单

1. 明确归属路由族（common、auth、user、admin、payment、gateway 或页面路由），复用既有前缀和模块入口。
2. 核对认证方式（无认证、JWT、管理员、step-up、API Key、provider 签名或短期票据）及对象级 owner 校验。
3. 核对 body 与 header 限制、面板限流、审计、Ops 采集、request ID 与 client request ID、Server-Timing 是否适用。
4. 确认响应格式为面板 envelope 还是 OpenAI、Anthropic、Google 协议格式，并验证流式开始后的错误路径。
5. 同步更新后端路由接口测试、HTTP 与用例测试，以及前端 API 模块。
6. 检查是否存在动态路由冲突，避免 wildcard 或子路径吞掉已移除或专用固定端点。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关错误响应策略](gateway_error_policy.md)、[身份与租户](../domains/identity_and_tenancy.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[支付与权益](../domains/payments_and_entitlements.md)、[接口目录](index.md)。
