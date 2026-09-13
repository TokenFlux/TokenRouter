# HTTP 接口边界

本文描述 TokenRouter 的稳定路由族、认证方式、共同中间件、响应形状和路由所有权。它用于新增或移动接口时选择正确边界，不逐项复制全部 endpoint，也不替代各上游协议规范或领域状态机。

## 章节导航

- [全局入口](#全局入口)：理解所有请求共享的处理顺序。
- [路由族](#路由族)：选择 URL、认证和拥有者。
- [API Key 结算策略接口](#api-key-结算策略接口)：配置资金来源、查询订阅和收窄分组。
- [分组客户端协议](#分组客户端协议)：理解上游平台与客户端准入的独立契约。
- [认证方式](#认证方式)：区分 JWT、管理密钥、API Key 和签名票据。
- [外部支付管理集成](#外部支付管理集成)：服务间充值和嵌入页对接边界。
- [响应与错误](#响应与错误)：保持面板与协议兼容形状。
- [请求关联](#请求关联)：正确透传 request ID。
- [变更规则](#变更规则)：新增接口时检查。

新增创作台路由时读取[创作台](../domains/creative_studio.md)领域文档核对生命周期、幂等与留存边界。

## 全局入口

`SetupRouter` 在一个 Gin engine 上安装共同中间件并依次注册 common、auth、user、admin、gateway、payment 和 page routes。主要全局顺序为：

```text
RequestLogger
  -> security client IP / session binding context
  -> access logger
  -> CORS
  -> security headers / CSP
  -> optional Server-Timing
  -> embedded frontend and API routes
```

`X-Request-ID` 是服务端请求关联 ID：长度和字符合法时沿用客户端值，否则生成 UUID，并写回响应和 request context。网关路由另外安装 `ClientRequestID`，始终为本服务生成内部请求 ID；合法的 `X-Client-Request-ID` 只作为调用方关联 ID 保存和回显，不参与权限或结算幂等。缺失或不安全时，响应中的 `X-Client-Request-ID` 回退为内部 ID；内部 ID 另通过 `X-Sub2API-Request-ID` 返回。服务生成的关联 ID 不主动加入上游请求，避免把网关内部头发送给供应商。

入口体积限制和错误采集按路由族叠加。网关在读取 JSON/multipart 之前应用通用或文本 body limit、client request ID、Ops error logger、endpoint 归一化和 API Key auth。面板接口使用全局/重查询限流和审计；高风险公开认证接口使用独立 Redis 限流并在依赖故障时 fail-close。

## 路由族

账号管理展示值与脱敏映射位于 `account/httpapi/dto`，代理展示值位于 `egress/httpapi/dto`；旧 DTO 入口只作投影和委托。敏感字段、省略/空集合及代理管理员字段边界保持原契约，非敏感的嵌套 map、slice 和时间指针使用独立副本，不能通过修改展示结果污染账号配置。账号备份、即时/计划测试和 API Key 上游用量查询已直接使用 account/httpapi；账号 CRUD、列表、复制/恢复、批量管理、凭据字段更新、刷新/重授权、隐私、调度开关、额度重置及健康恢复已直接绑定 `account/httpapi.ManagementHandler`；模型目录、实时模型同步、tier 和详细统计也使用新入口；高级调度诊断直接绑定 `scheduler/httpapi.DiagnosticsHandler`，旧 AccountHandler 不再构造于生产依赖图。备份导出继续要求原 step-up，导入继续使用同一管理员幂等 helper。

订阅、兑换、平台额度和套餐的用户/管理员 handler 与 DTO 位于 `billing/httpapi`，原路由汇总直接绑定这些实例。URL、认证/幂等中间件顺序、reason、CSV 和分页排序保持原契约；额度 HTTP 不直接读取仓储，用户存在性由用例的只读端口处理。管理员套餐保留原 Ent 的字段省略及 `edges` 形状，公开套餐使用独立投影。

用户资料、会话、七类身份、强认证与用户管理 HTTP 位于 `identity/httpapi`，团队位于 `team/httpapi`，Key 生命周期和凭据入口位于 `apikey/httpapi`。app 组合同一组身份处理器供原路由调用；微信支付 OAuth 单独接入原路径。HTTP 适配保留历史 DTO 形状与凭据差异，安全 `Principal` 和 Key 的 `AccessSnapshot` 分别表达身份与付款/成员上下文；旧 context 读取入口只作兼容投影。

| 路由族 | 认证 | 主要所有者与用途 |
| --- | --- | --- |
| `/health`、`/setup/status` | 无 | `routes/common.go`；进程健康与正常模式 setup 状态 |
| `/api/event_logging/batch` | 无 | Claude Code 遥测兼容空接收，固定返回成功 |
| `/api/v1/auth/*` | 大多公开，账户管理子流程按路由加 JWT/短期状态 | `routes/auth.go`；注册、登录、刷新、密码恢复、OAuth、Passkey 登录和身份完成 |
| `/api/v1/user/*`、`/keys`、`/team`、`/groups`、`/subscriptions`、`/redeem` 等 | 用户 JWT | `routes/user.go`；用户面板资源、团队、Key、用量和权益自省 |
| `/api/v1/admin/*` | 管理员 JWT 或受限管理密钥；部分操作另需 step-up | `routes/admin.go`；用户、分组、账号、渠道、设置、运维、备份、支付和安全管理 |
| `/api/v1/payment/*` | 用户 JWT | `routes/payment.go`；配置/套餐读取、下单、查单、取消、invoice 和退款申请 |
| `/api/v1/payment/public/*` | 签名 resume token 或遗留订单验证约束 | 支付结果恢复；不得扩展为匿名订单枚举接口 |
| `/api/v1/payment/webhook/*` | 提供商验签 | EasyPay、Alipay、WeChat Pay、Stripe、Airwallex 通知 |
| `/v1/*` 和兼容裸别名 | TokenRouter API Key | Anthropic/OpenAI 兼容消息、Responses、Chat、图片、视频、模型、用量与批任务 |
| `/v1beta/*` | TokenRouter API Key | Gemini 原生模型 URL、生成、流式生成和 token 统计 |
| `/antigravity/*` | TokenRouter API Key + 强制平台 | Antigravity 专用 Claude/Gemini 入口与管理型自省 |
| `/backend-api/codex/*` | TokenRouter API Key | Codex Responses、Realtime 与 sideband 兼容入口 |
| `/api/v1/pages/*` 等 page routes | 按页面类型为用户或管理员 JWT | 服务端生成/读取的 pricing、账单或管理页面数据 |

<a id="subscription_self_revoke_api"></a>
用户订阅页面提供一个受额度条件约束的自助撤销接口：

- `POST /api/v1/subscriptions/:id/revoke` 需要用户 JWT。服务端只接受当前用户本人、当前 `active` 且最高层有限额度已耗尽的订阅；成功时在事务中撤销当前记录、提前接续同套餐的下一份 pending 记录，并自动改绑显式订阅 Key。
- 成功响应的 `data` 为 `{ revoked_subscription_id, replacement_subscription_id, rebound_api_key_count }`；没有接续记录时 `replacement_subscription_id` 为 `null`，Key 改绑数量为 `0`。
- 越权或不存在的订阅返回 `SUBSCRIPTION_NOT_FOUND`；记录不是当前 active 返回 `SUBSCRIPTION_NOT_ACTIVE`（409）；最高层额度仍可用或套餐无限返回 `SUBSCRIPTION_QUOTA_NOT_EXHAUSTED`（409）。撤销不触发退款，也不提供用户侧恢复接口。

<a id="payment_admin_recovery"></a>
## 支付管理恢复

管理员支付订单提供两条恢复接口，均受管理员认证、面板限流和审计中间件保护：

- `POST /api/v1/admin/payment/orders/{id}/force-expire` 接受必填 JSON 字段 `reason`（1 至 500 个字符）。仅当前为 `PENDING` 的订单可被无上游调用地写为 `EXPIRED`，成功响应 `data.message=force_expired`；订单不存在返回 `NOT_FOUND`，状态竞争返回 `ORDER_STATUS_CHANGED`（409）。此操作的迟到付款仍通过正常 webhook 恢复。
- `POST /api/v1/admin/payment/providers/test` 接受 `provider_key`、`config` 和可选 `instance_id`。当前仅支持 `easypay`；带实例 ID 时服务端按更新规则合并未回传的敏感字段，再以随机订单号执行只读查单。接口不保存草稿、不创建订单，成功只返回 `data.reachable=true`，不会返回上游 body、URL 细节或凭据。

普通取消在无法确认上游支付状态时返回 `PAYMENT_STATUS_UNAVAILABLE`（503），而不是泛化 500。若一个 provider instance 仍拥有强制过期且未恢复的订单，删除接口返回 `FORCED_EXPIRED_ORDERS`（409）；管理员应停用并保留该实例以接收迟到回调。

`GET /api/v1/admin/groups/usage-summary` 仅返回管理员可见的全局分组汇总，字段为 `today_cost`、`yesterday_cost` 和 `total_cost`。自然日固定使用服务端配置时区，不接受浏览器时区参数，避免不同管理员在同一列表看到不同的“今日”边界。

OAuth 登录 start 对 GitHub、Google、LinuxDo、DingTalk、WeChat 和 OIDC 同时保留 `GET` 与 `POST`。未启用腾讯天御或阿里云验证码时，`GET` 继续以 `302` 跳转保持兼容；任一动作验证码启用后，匿名登录必须用 `POST`，腾讯票据使用 `tencent_captcha_ticket` 与 `tencent_captcha_randstr`，阿里云的 `captchaVerifyParam` 复用 `turnstile_token` 字段，成功响应的 `data.authorize_url` 由前端再导航。`*/bind/start` 是当前用户绑定入口，不消费匿名登录验证码。Passkey 登录的 `/auth/passkey/login/begin` 使用相同的提供方字段映射，`finish` 只接受 ceremony session 和 WebAuthn credential。

`POST /api/v1/auth/oauth/google/one-tap` 接受浏览器 GIS 返回的 `credential`、本地 `redirect` 及可选 `aff_code`/`promo_code`。credential 上限为 16 KiB，入口按客户端 IP 使用 Redis `20 次/分钟` fail-close 限流；不接收 Client Secret，也不能记录 token 或未验证 claims。验证和已有用户登录成功时，统一 envelope 的 `data` 返回 `status=authenticated` 与标准 `access_token`、`refresh_token`、`expires_in`、`token_type`；新用户只返回 `status=registration_required` 与本地 redirect，并通过 HttpOnly pending cookies 继续 `/auth/oauth/callback` 的既有补全状态机。One Tap 设置或 Google OAuth 配置无效、backend mode、腾讯/阿里云动作验证码启用、注册关闭、token 无效或用户状态不可登录时拒绝。Turnstile 单独开启时不新增该入口的校验范围。

创作台（Creative Studio）是 `/api/v1/creative/*` 用户 JWT 路由族（`routes/user.go`，统一 envelope），供个人图片生成/编辑/局部重绘任务使用：

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

除 `GET /creative/models` 与 `GET /creative/capabilities` 外，创作台任务创建、历史、活动、详情、输出 content 和 ack 路由都要求 `X-Creative-Workspace-ID` 请求头（规范化小写 UUID）。缺失返回 `400 CREATIVE_WORKSPACE_REQUIRED`，非法值返回 `400 CREATIVE_WORKSPACE_INVALID`；工作区不匹配的任务统一返回 `404 CREATIVE_RUN_NOT_FOUND`。`GET /creative/capabilities` 返回 `max_prompt_chars`、`max_asset_bytes`、`max_total_input_bytes`、`max_mask_bytes` 和允许的 PNG/JPEG/WebP MIME。`GET /creative/runs/active` 以不透明 cursor 分页返回全部活动状态，不受历史页大小限制。`POST /creative/runs` 接受 `multipart/form-data`，除素材字段外可提交 `image_size`、`aspect_ratio`、`quality`、`background` 与 `thinking_level`；客户端不能指定输出格式，不接受 `output_format`、`output_compression` 或旧的 `response_mime_type` 字段，输出 metadata 的 `mime_type` 保留供应商实际返回的 MIME。所有值按 `GET /creative/models` 返回的模型级能力校验，每次任务固定生成一张图片。平台操作交集为：OpenAI `generate`/`edit`/`inpaint`，Gemini/Grok `generate`/`edit`；Grok 编辑最多 3 张源图，Gemini 不接受独立 mask。接口只接受上传文件、不接受远程 URL，并受 `Idempotency-Key` 头约束：同一用户+工作区同键同体重放返回原任务（`idempotent_replay=true`），同一用户+工作区同键不同体返回 `409 CREATIVE_IDEMPOTENCY_CONFLICT`，不同工作区可使用同名键创建独立任务。输出内容只有在结算完成的可交付终态可读取；ack 先写数据库再删除服务端临时输出，删除失败由后台清理补偿。输出内容路由在临时输出过期或丢失时返回 410 语义（`CREATIVE_OUTPUT_EXPIRED`/`CREATIVE_RESULT_LOST`）并把任务降级为 `result_lost`；服务端只保存任务元数据，图片与 prompt 明文只存于 Redis 临时键，细节与限制见[创作台](../domains/creative_studio.md)。

部分下载路由使用短期签名票据，以支持浏览器原生下载大文件；票据只授权一个预生成资源，不能等价为用户 JWT。模型列表、用量和既有批任务管理即使跳过消费余额检查，仍要执行 Key 身份和资源归属验证。

上游声明倍率探测与 Key 账单自省已从路由表完全注销：`GET /v1/sub2api/billing`，`GET|PUT /api/v1/admin/accounts/upstream-billing-probe/settings`，`POST /api/v1/admin/accounts/upstream-billing-probe/batch`，以及 `PUT|POST /api/v1/admin/accounts/:id/upstream-billing-probe` 都返回普通 `404`。这些路径没有兼容 handler、重定向或弃用响应，也不再享有 API Key 非消费请求豁免。

账号批量删除使用 `POST /api/v1/admin/accounts/batch-delete`，请求体为 `account_ids`。服务端先去除非正数和重复 ID，再以最多 5 路并发执行删除；同批选择父账号及其影子账号时只删除根账号一次，并将级联影响映射回逐账号结果。响应返回稳定排序的 `success_ids`、`failed_ids` 和错误明细，单项失败不会取消其它账号。管理端“全选筛选结果”先以同一筛选快照分页读取轻量 ID，任何分页缺失或重复都保留原选择，不得提交部分集合。

管理员账号连接测试使用 `POST /api/v1/admin/accounts/:id/test`，响应为 SSE。请求体可包含 `model_id`、`prompt`、OpenAI 专用的 `mode`、API Key 文字测试的 `protocol=responses|chat_completions`，以及 `test_type`（`text` 或 `image`）；历史客户端也可用 `test_mode` 作为类型字段别名。管理端必须显式发送 `test_type`：普通 `text` 始终走文字测试路径并使用自定义提示词，`image` 始终走图片测试路径并使用自定义提示词；OpenAI 的 `compact` 与 `legacy_compact` 是固定载荷的连接测试，不使用自定义提示词且不会改写账号能力开关。成功与失败均不返回或持久化能力探测状态，但测试仍按现有流程记录认证错误、限流和额度观测。服务端只对未携带该字段的旧调用保留按模型名兼容判断。图片测试结果以 SSE `image` 事件返回，文字结果以 `content` 事件返回；不具备对应平台图片端点的账号返回流式错误事件。

管理员设置接口 `GET|PUT /api/v1/admin/settings` 的 `creative_model_settings` 字段用于维护创作台全局生图模型白名单，结构为 `[{"group_id":123,"model":"gpt-image-2","operations":["generate","edit","inpaint"]}]`。省略字段保留现值，显式空数组清空；服务端校验能力值和 `(group_id, model)` 唯一性，并在审计中只记录字段是否发生变化。保存时按实际分组平台规范化：Gemini 移除 `inpaint`，移除后无能力的条目删除；无法解析的平台暂时保留，但运行时仍不放行。`creative_worker_count` 同属该接口，要求为大于 0 的整数，默认 128，保存后热更新当前实例的创作台 worker 池。`GET /api/v1/admin/settings/creative-model-candidates` 返回当前 active、启用图片生成且存在可调度图片模型的 `{group_id, group_name, platform, model, operations}` 候选，不按管理员用户权限过滤；OpenAI 返回三项能力，Gemini/Grok 返回 `generate`/`edit`。`GET /api/v1/admin/settings/creative-worker-status` 返回创作台任务 worker 池快照 `{running, worker_count, busy_workers}`：运行中 `worker_count` 为当前活动 worker 数、`busy_workers` 为正在处理任务的 worker 数，管理端设置页据此轮询展示当前使用情况；未运行时返回 `running=false` 的零值快照，由前端回退到 `creative_worker_count` 配置值。

账号高级调度评分诊断仅限管理员：`GET /api/v1/admin/accounts/:id/advanced-scheduler-score` 返回该账号所属高级分组摘要；携带 `group_id` 时返回指定高级分组的完整候选池、硬过滤、有效配置、指标原值/归一化值/贡献、Top-K 权重与实际活动池概率及平台策略提示。订阅优先启用且存在合格订阅账号时，普通账号标记为延后且不进入本轮概率；开启粘性加权时 previous-response 和 session 只影响 Top-K 权重，关闭时有效硬粘性账号按实际强制选择显示概率 1。`POST /api/v1/admin/accounts/:id/advanced-scheduler-score/preview` 接受 `group_id`、可选 `requested_model`、`sticky_account_id` 和 `previous_response_account_id`，用于无状态的评分模拟；previous-response 只对 OpenAI 分组有效，其它平台返回 `ignored`。请求体严格拒绝其它字段，尤其不得传入 session hash、响应正文或凭据。两个接口不分配并发槽、不写粘性，并且响应不包含凭据、代理认证、session hash 或上游响应内容。诊断复用请求信息足以判断的生产硬过滤；endpoint、transport、compact、media 等缺少请求上下文的能力以 `not_evaluated` 明示，不伪装成已通过。

路由前缀不独自决定协议处理器。例如 `/v1/messages` 会根据分组平台分派到 Anthropic、OpenAI/Grok 或 Qoder handler；路由层拥有分派，handler/service 不能通过字符串猜测调用方已经具备某个平台能力。

## API Key 结算策略接口

`POST /api/v1/keys` 和 `PUT /api/v1/keys/{id}` 接受 `billing_mode`（`auto`、`subscription`、`balance`）及可空 `preferred_subscription_id`。省略模式或使用 `auto` 保持旧的订阅优先、余额兜底行为；`balance` 会清除指定订阅；`subscription` 必须指定当前付款主体的一份有效订阅。个人 Key 的付款主体是本人，团队 Key 的付款主体是 Team Owner。

创建和更新 API Key 时，`quota`、`rate_limit_5h`、`rate_limit_1d`、`rate_limit_7d` 必须是有限、非负且小于 `1e12` 的 USD 数值，以匹配数据库 `DECIMAL(20,8)`；`0` 仍表示不限额。创建请求省略 `expires_in_days` 表示永不过期，显式提供时必须大于 0；更新请求用空 `expires_at` 清除到期时间，用合法 RFC3339 时间设置明确到期点。handler 的早期校验与 service 的最终校验必须使用同一规则，内部调用不能绕过。

`GET /api/v1/keys/billing-options?scope=personal|team` 返回当前作用域可指定的有效订阅摘要，包括 `id`、`plan_id`、`plan_name`、`expires_at`、`groups_restricted` 和 `applicable_groups`。`GET /api/v1/groups/available?scope=personal|team&subscription_id={id}` 在带 `subscription_id` 时返回付款主体原有分组权限与该订阅套餐分组的交集；不带该参数时保持历史的可用分组结果。两个接口都不把成员自己的订阅泄露到团队作用域。

网关 `GET /v1/usage` 在原有 Key 配额、订阅或余额字段之外始终返回 `billing` 对象，至少包含 `mode`、`source`、`preferred_subscription_id`、`available` 和 `unit`。`source=subscription` 时只返回实际选择的订阅额度/剩余值；指定订阅失效时仍使用该来源并标记 `available=false`，不返回余额。`source=balance` 时只返回付款主体余额，不加载或展示订阅额度。`auto` 的 `source` 随当前可用订阅动态变化；Key 自身的配额和滚动限额字段不受该展示规则影响。

## 分组客户端协议

Group 使用 `allowed_protocols`、`protocol_fallbacks`、`responses_image_policy` 返回统一配置；账号使用 `credentials.upstream_protocols`。管理员只读 `GET /api/v1/admin/protocol-capabilities` 提供全部协议、原生账号 profile、分组可用入口及转换目标。完整字段语义见[统一协议能力](protocol_capabilities.md)。

旧 `allowed_client_protocols`、媒体/Live 开关和账号文本路由只作为输入兼容，新响应与导出使用统一结构。`messages_dispatch_model_config` 仍只处理模型映射，不参与协议准入。门禁拒绝在上游调用前返回对应协议的 403 错误；平台没有实现的入口保留 404 边界。

## 认证方式

| 凭据 | 接收位置 | 权限边界 |
| --- | --- | --- |
| JWT access token | 面板 `Authorization: Bearer`，少量 WebSocket 子协议 | 当前用户状态、token version、可选 session binding；管理员还校验当前 role |
| Refresh token | `/api/v1/auth/refresh` 与 logout payload/cookie 约定 | 只用于轮换/撤销，不可直接访问业务资源 |
| 管理 API Key | 管理接口 `x-api-key` | 绑定首个真实管理员；启用敏感 step-up 后不能执行需近期 TOTP 的操作 |
| TokenRouter API Key | 网关 `Authorization: Bearer`、`x-api-key`，Gemini 兼容 `x-goog-api-key` | Key、用户/团队、分组、IP、额度/订阅和请求资源归属；通用网关不接受 query Key |
| OAuth/pending completion 状态 | auth callback 和完成接口 | provider state、浏览器会话、一次性完成码与过期时间共同约束 |
| Google GIS ID Token | `/api/v1/auth/oauth/google/one-tap` 请求体 | 仅经官方验证器校验后的 `sub` 和 verified email 可进入现有 Google 身份与 pending 流程；不能作为其它接口的 Bearer 凭据 |
| 支付 webhook 签名 | 原始 body/query + provider headers | 只授权解释一条已绑定本地订单的通知，仍需校验金额和 metadata |
| 下载/resume ticket | 指定公共恢复或下载路由 | 有时限、限定资源和操作，不能升级为一般会话 |

认证成功只建立主体。资源 owner、团队成员、管理员 step-up、支付订单 user ID、批任务 owner 和分组能力仍由相应 handler/service 检查。不得因为路由已挂认证中间件就省略对象级授权。

## 外部支付管理集成

外部支付服务应使用管理 API Key 通过 `x-api-key` 调用管理路由，管理员 JWT 只适合交互式管理端。支付成功后的余额发放优先使用 `POST /api/v1/admin/redeem-codes/create-and-redeem`，由服务端原子创建并兑换余额兑换码；调用方必须提供稳定的业务 `code` 和 `Idempotency-Key`，同一操作重试时复用二者，并按 200、409 和业务错误区分重放、冲突与失败。`GET /api/v1/admin/users/:id` 可用于前置查询，`POST /api/v1/admin/users/:id/balance` 只用于明确的人工增减或补偿，同样需要幂等键。

购买页和用户自定义页面由前端追加 `user_id`、`token`、`theme`、`lang`、`ui_mode`、`src_host` 和 `src_url`。其中 `token` 是用户 Bearer 凭据，只能发送到部署者信任且使用 HTTPS 的页面来源；接收方不得写入访问日志、分析参数或转发给第三方。完整请求示例和重试约定见 [外部支付管理 API 指南](../guides/payments/admin_integration_api.md)。

## API Key 上游用量查询

管理员账号列表提供两个手动、展示型接口：

- `POST /api/v1/admin/accounts/:id/upstream-usage/query`
- `POST /api/v1/admin/accounts/upstream-usage/query/batch`，请求体 `account_ids` 最多 100 个正整数。

接口只接受 `type=apikey`（Bedrock 除外），使用管理员认证和既有审计中间件；内置适配器为 `sub2api`、`new_api` 和 `zivv`，由账号配置严格选择。成功结果在顶层包含 `adapter`、`provider`、UTC `observed_at` 以及余额/限额/订阅字段；批量接口将每个账号的成功结果和结构化错误分开返回。错误 reason 使用 `UPSTREAM_USAGE_*` 命名空间，覆盖账号无效/禁用、协议不支持、认证失败、钱包不可用/钱包认证失败、限流、超时、响应格式、网络和身份变更。

查询不会写账号、Extra、调度快照或计费记录，也不会把 API Key 放入响应或审计 body。前端只在行内按钮或批量操作触发请求，成功结果在管理员隔离的 `sessionStorage` 中缓存五分钟。

<a id="announcement_api"></a>
## 公告接口

公告的用户与管理员 handler/DTO 位于 `site/httpapi`，由原路由族接入相同的认证、审计和限流。用户入口保持 `GET /api/v1/announcements` 与 `POST /api/v1/announcements/:id/read`；管理员 CRUD 和 read-status 保持 `/api/v1/admin/announcements` 路径。

site 拥有 targeting 校验、余额/有效订阅匹配、开始结束边界、已读与到期归档。用户投影由旧身份桥接提供，有效订阅投影由 app 直接适配 billing；HTTP Adapter 不直接访问数据库。开始/结束字段的省略、零值清空、分页排序和 JSON 形状保持原契约。重复已读保留第一次读取时间；管理员查询仍先归档过期公告，归档失败不能伪装成成功列表。

<a id="write_idempotency"></a>
## 面板命令幂等

`idempotency` 拥有命令认领、指纹、重放、冲突、退避、响应存储、指标和到期清理，PostgreSQL Adapter 负责原表读写。用户/管理员 HTTP helper 的唯一实现位于 `idempotency/httpapi`，旧 handler 入口委托它。helper 保留既有 scope、observe-only、缺省 coordinator、存储故障策略、`Retry-After` 与 `X-Idempotency-Replayed`。响应截断仍保持 UTF-8 和脱敏，默认 coordinator 与指标只有一份状态。

维护操作锁继续拥有自己的全局作用域、续租与成功/失败语义；它只复用新存取契约，不与资金去重合并。清理 worker 由 app 启动并等待停止，不能在数据库关闭后继续删除记录。

<a id="response_errors"></a>
## 响应与错误

面板和内部 REST 接口通常使用统一 envelope：

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

业务错误由 `ApplicationError` 映射为 HTTP status，并可返回 `reason` 和字符串 `metadata`。未知错误按 500 处理并只在服务端记录脱敏详情。分页数据使用 `items`、`total`、`page`、`page_size` 和 `pages`；创建与异步接受分别可以返回 201/202。

唯一错误实体位于 `pkg/apperror`，HTTP 映射和面板 envelope 位于 `server/httpx`。旧 `pkg/errors` 和 `pkg/response` 保留兼容入口；错误类型别名维持 `errors.Is/As`、cause 和 metadata 复制语义。新核心使用具名类别；兼容期仍保留旧 code 数值和字段，自定义状态码也不能因提取而归并成另一类错误。

管理员 `GET /api/v1/admin/usage` 的每条记录可包含 `detailed_timing`。该对象由同一内部请求 ID 关联 `http.access` 日志得到，字段是相对于 Sub2API 入口的毫秒时间点，包括账号槽位、上游连接/写入、首字节、首个 SSE、首个可见输出和首次下游 Flush；历史记录或观测日志缺失时省略该对象。

网关错误必须保持调用协议形状：OpenAI 入口使用 `error` 对象，Anthropic 使用 `type: error` 与嵌套错误，Google 使用 HTTP code/message/status。认证、未分组、复合 Key 和本地能力拒绝都选择当前协议 writer；不能为了复用面板 helper 把一个 Google/Anthropic 客户端错误改成面板 envelope。

客户端协议被分组禁用时返回 `403`，并在账号选择、计费、重试和 fallback 前记录 `LocalPolicyDenied`。Anthropic 入口使用 `permission_error`，OpenAI 入口使用 `protocol_not_allowed`，Gemini 入口使用 Google `PERMISSION_DENIED`。模型列表 GET 不经过生成协议开关。

错误响应不得包含上游凭据、代理 URL、原始 service account、数据库错误或未经脱敏的请求正文。流式响应开始后不能再写普通 JSON 错误；只能按当前 SSE/流协议结束或发送允许的错误事件。

## 请求关联

- `X-Request-ID` 用于一次 HTTP 调用的日志和审计关联，最长持久化长度受限。
- `X-Client-Request-ID` 是调用方提供的跨服务关联 ID；服务会限制为安全的 ASCII 标识并保留在日志链路中，但不作为内部结算幂等 ID。缺失或不安全时，响应中的该头回退为服务生成的内部 ID。
- `X-Sub2API-Request-ID` 是服务生成的内部请求 ID，用于本服务日志、结算幂等和下游诊断；它只写入响应，不加入上游请求。
- 上游 request ID 属于供应商观测字段，需单独保存，不能替换本地 ID。
- 后台 worker 从请求派生所需 metadata 后使用受超时约束的新 Context；不得继续持有已取消请求的 body 或 Gin context。

当客户端允许重试时，应保留同一业务 request ID，但服务端必须结合 API Key 和请求指纹识别冲突。只根据请求 ID 字符串判断“重复”会把不同用户或不同 payload 混在一起。

## 变更规则

新增或移动接口时至少核对：

- 由 common、auth、user、admin、payment、gateway 还是 page route 拥有，是否使用既有前缀和 handler 分派。
- 需要无认证、JWT、管理员、step-up、API Key、provider 签名还是短期票据；是否还需要对象级 owner 检查。
- body/header 限制、面板限流、审计、Ops 采集、request/client request ID 和 Server-Timing 是否适用。
- 返回面板 envelope 还是 OpenAI/Anthropic/Google 协议形状，流式开始后的错误路径是否有效。
- 后端 route contract tests、handler/service 测试和前端 API 模块是否同时更新。
- 是否无意新增冲突的动态路由；例如 wildcard/subpath 不得吞掉已明确移除或专用的固定 endpoint。

相关文档：[上游账号能力矩阵](upstream_account_matrix.md)、[网关错误响应策略](gateway_error_policy.md)、[身份与租户](../domains/identity_and_tenancy.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[支付与权益](../domains/payments_and_entitlements.md)、[接口目录](index.md)。


S06 的 CRS 同步和预览路由已直接绑定 `account/httpapi.CRSHandler`；保留 `/api/v1/admin/accounts/sync/crs` 与 `/preview`、管理员中间件及原错误 envelope。默认同步代理，显式 false 关闭；已存在账号更新不受“只创建选中项”的空集合语义影响。Codex session 的 `/api/v1/admin/accounts/import/codex-session` 同样直接绑定 `account/httpapi.CodexImportHandler`，保留原请求校验、逐项结果和幂等 scope。Ollama Cloud 的设置、状态、会话、自动刷新和主动刷新路由直接绑定 `account/httpapi.OllamaUsageHandler`，保留原管理员鉴权、请求字段、状态码和敏感会话不回显语义。账号主动/被动用量、批量用量、今日统计及批量今日统计直接绑定 `account/httpapi.OAuthUsageHandler`；原 30 秒快照缓存、ETag、Vary、304 和 `X-Snapshot-Cache` 保持同一实现。账号管理其余路由均直接使用 ManagementHandler；供应商 OAuth 交换路由仍使用原平台 handler，留待 S09。
