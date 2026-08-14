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

`X-Request-ID` 是服务端请求关联 ID：长度和字符合法时沿用客户端值，否则生成 UUID，并写回响应和 request context。网关路由另外安装 `ClientRequestID`，以 `X-Client-Request-ID` 关联一次客户端业务请求、Ops 记录和后台结算；两者用途不同，不能互相覆盖。

入口体积限制和错误采集按路由族叠加。网关在读取 JSON/multipart 之前应用通用或文本 body limit、client request ID、Ops error logger、endpoint 归一化和 API Key auth。面板接口使用全局/重查询限流和审计；高风险公开认证接口使用独立 Redis 限流并在依赖故障时 fail-close。

## 路由族

| 路由族 | 认证 | 主要所有者与用途 |
| --- | --- | --- |
| `/health`、`/setup/status` | 无 | `routes/common.go`；进程健康与正常模式 setup 状态 |
| `/api/event_logging/batch` | 无 | Claude Code 遥测兼容空接收，固定返回成功 |
| `/api/v1/auth/*` | 大多公开，账户管理子流程按路由加 JWT/短期状态 | `routes/auth.go`；注册、登录、刷新、密码恢复、OAuth、Passkey 登录和身份完成 |
| `/api/v1/user/*`、`/keys`、`/team`、`/groups`、`/subscriptions`、`/redeem` 等 | 用户 JWT | `routes/user.go`；用户面板资源、团队、Key、用量、数据共享和权益自省 |
| `/api/v1/admin/*` | 管理员 JWT 或受限管理密钥；部分操作另需 step-up | `routes/admin.go`；用户、分组、账号、渠道、设置、运维、备份、支付和安全管理 |
| `/api/v1/payment/*` | 用户 JWT | `routes/payment.go`；配置/套餐读取、下单、查单、取消、invoice 和退款申请 |
| `/api/v1/payment/public/*` | 签名 resume token 或遗留订单验证约束 | 支付结果恢复；不得扩展为匿名订单枚举接口 |
| `/api/v1/payment/webhook/*` | 提供商验签 | EasyPay、Alipay、WeChat Pay、Stripe、Airwallex 通知 |
| `/v1/*` 和兼容裸别名 | TokenRouter API Key | Anthropic/OpenAI 兼容消息、Responses、Chat、图片、视频、模型、用量与批任务 |
| `/v1beta/*` | TokenRouter API Key | Gemini 原生模型 URL、生成、流式生成和 token 统计 |
| `/antigravity/*` | TokenRouter API Key + 强制平台 | Antigravity 专用 Claude/Gemini 入口与管理型自省 |
| `/backend-api/codex/*` | TokenRouter API Key | Codex Responses、Realtime 与 sideband 兼容入口 |
| `/api/v1/pages/*` 等 page routes | 按页面类型为用户或管理员 JWT | 服务端生成/读取的 pricing、账单或管理页面数据 |

OAuth 登录 start 对 GitHub、Google、LinuxDo、DingTalk、WeChat 和 OIDC 同时保留 `GET` 与 `POST`。未启用腾讯天御或阿里云验证码时，`GET` 继续以 `302` 跳转保持兼容；任一动作验证码启用后，匿名登录必须用 `POST`，腾讯票据使用 `tencent_captcha_ticket` 与 `tencent_captcha_randstr`，阿里云的 `captchaVerifyParam` 复用 `turnstile_token` 字段，成功响应的 `data.authorize_url` 由前端再导航。`*/bind/start` 是当前用户绑定入口，不消费匿名登录验证码。Passkey 登录的 `/auth/passkey/login/begin` 使用相同的提供方字段映射，`finish` 只接受 ceremony session 和 WebAuthn credential。

`POST /api/v1/auth/oauth/google/one-tap` 接受浏览器 GIS 返回的 `credential`、本地 `redirect` 及可选 `aff_code`/`promo_code`。credential 上限为 16 KiB，入口按客户端 IP 使用 Redis `20 次/分钟` fail-close 限流；不接收 Client Secret，也不能记录 token 或未验证 claims。验证和已有用户登录成功时，统一 envelope 的 `data` 返回 `status=authenticated` 与标准 `access_token`、`refresh_token`、`expires_in`、`token_type`；新用户只返回 `status=registration_required` 与本地 redirect，并通过 HttpOnly pending cookies 继续 `/auth/oauth/callback` 的既有补全状态机。One Tap 设置或 Google OAuth 配置无效、backend mode、腾讯/阿里云动作验证码启用、注册关闭、token 无效或用户状态不可登录时拒绝。Turnstile 单独开启时不新增该入口的校验范围。

部分下载路由使用短期签名票据，以支持浏览器原生下载大文件；票据只授权一个预生成资源，不能等价为用户 JWT。模型列表、用量和既有批任务管理即使跳过消费余额检查，仍要执行 Key 身份和资源归属验证。

上游声明倍率探测与 Key 账单自省已从路由表完全注销：`GET /v1/sub2api/billing`，`GET|PUT /api/v1/admin/accounts/upstream-billing-probe/settings`，`POST /api/v1/admin/accounts/upstream-billing-probe/batch`，以及 `PUT|POST /api/v1/admin/accounts/:id/upstream-billing-probe` 都返回普通 `404`。这些路径没有兼容 handler、重定向或弃用响应，也不再享有 API Key 非消费请求豁免。

账号批量删除使用 `POST /api/v1/admin/accounts/batch-delete`，请求体为 `account_ids`。服务端先去除非正数和重复 ID，再以最多 5 路并发执行删除；同批选择父账号及其影子账号时只删除根账号一次，并将级联影响映射回逐账号结果。响应返回稳定排序的 `success_ids`、`failed_ids` 和错误明细，单项失败不会取消其它账号。管理端“全选筛选结果”先以同一筛选快照分页读取轻量 ID，任何分页缺失或重复都保留原选择，不得提交部分集合。

账号高级调度评分诊断仅限管理员：`GET /api/v1/admin/accounts/:id/advanced-scheduler-score` 返回该账号所属高级分组摘要；携带 `group_id` 时返回指定高级分组的完整候选池、硬过滤、有效配置、指标原值/归一化值/贡献、Top-K 权重与实际活动池概率及平台策略提示。订阅优先启用且存在合格订阅账号时，普通账号标记为延后且不进入本轮概率；开启粘性加权时 previous-response 和 session 只影响 Top-K 权重，关闭时有效硬粘性账号按实际强制选择显示概率 1。`POST /api/v1/admin/accounts/:id/advanced-scheduler-score/preview` 接受 `group_id`、可选 `requested_model`、`sticky_account_id` 和 `previous_response_account_id`，用于无状态的评分模拟；previous-response 只对 OpenAI 分组有效，其它平台返回 `ignored`。请求体严格拒绝其它字段，尤其不得传入 session hash、响应正文或凭据。两个接口不分配并发槽、不写粘性，并且响应不包含凭据、代理认证、session hash 或上游响应内容。诊断复用请求信息足以判断的生产硬过滤；endpoint、transport、compact、media 等缺少请求上下文的能力以 `not_evaluated` 明示，不伪装成已通过。

路由前缀不独自决定协议处理器。例如 `/v1/messages` 会根据分组平台分派到 Anthropic、OpenAI/Grok 或 Qoder handler；路由层拥有分派，handler/service 不能通过字符串猜测调用方已经具备某个平台能力。

## API Key 结算策略接口

`POST /api/v1/keys` 和 `PUT /api/v1/keys/{id}` 接受 `billing_mode`（`auto`、`subscription`、`balance`）及可空 `preferred_subscription_id`。省略模式或使用 `auto` 保持旧的订阅优先、余额兜底行为；`balance` 会清除指定订阅；`subscription` 必须指定当前付款主体的一份有效订阅。个人 Key 的付款主体是本人，团队 Key 的付款主体是 Team Owner。

创建和更新 API Key 时，`quota`、`rate_limit_5h`、`rate_limit_1d`、`rate_limit_7d` 必须是有限、非负且小于 `1e12` 的 USD 数值，以匹配数据库 `DECIMAL(20,8)`；`0` 仍表示不限额。创建请求省略 `expires_in_days` 表示永不过期，显式提供时必须大于 0；更新请求用空 `expires_at` 清除到期时间，用合法 RFC3339 时间设置明确到期点。handler 的早期校验与 service 的最终校验必须使用同一规则，内部调用不能绕过。

`GET /api/v1/keys/billing-options?scope=personal|team` 返回当前作用域可指定的有效订阅摘要，包括 `id`、`plan_id`、`plan_name`、`expires_at`、`groups_restricted` 和 `applicable_groups`。`GET /api/v1/groups/available?scope=personal|team&subscription_id={id}` 在带 `subscription_id` 时返回付款主体原有分组权限与该订阅套餐分组的交集；不带该参数时保持历史的可用分组结果。两个接口都不把成员自己的订阅泄露到团队作用域。

网关 `GET /v1/usage` 在原有 Key 配额、订阅或余额字段之外始终返回 `billing` 对象，至少包含 `mode`、`source`、`preferred_subscription_id`、`available` 和 `unit`。`source=subscription` 时只返回实际选择的订阅额度/剩余值；指定订阅失效时仍使用该来源并标记 `available=false`，不返回余额。`source=balance` 时只返回付款主体余额，不加载或展示订阅额度。`auto` 的 `source` 随当前可用订阅动态变化；Key 自身的配额和滚动限额字段不受该展示规则影响。

## 分组客户端协议

Group 的 `platform` 表示上游平台，客户端文本协议由 `allowed_client_protocols` 独立准入。管理接口和公开 Group DTO 返回完整有效集合，并固定按以下顺序排列：

```text
anthropic_messages
openai_responses
openai_chat_completions
gemini_generate_content
```

创建分组时省略字段会使用平台默认协议；更新时省略字段保持原集合。若同一次更新切换了上游平台，服务端只保留两平台都支持的协议，不自动启用新平台默认值。显式输入会拒绝未知值、重复值和平台不支持的值并返回 `400`；默认协议不是必选项，所有平台都接受显式空数组。

`allow_messages_dispatch` 是弃用兼容字段，响应值由新集合是否包含 `anthropic_messages` 派生。只有 OpenAI 分组在新字段缺省时继续接受旧字段输入；两者同时提交时以 `allowed_client_protocols` 为准。`messages_dispatch_model_config` 仅保存 OpenAI Messages 到 GPT 的模型映射，不参与协议准入；每个映射项只在目标值非空时生效，全部留空时不执行分组层模型映射。

管理 Group 创建、更新和返回体额外包含 `scheduler_type`（`basic` 或 `advanced`）及 `advanced_scheduler_overrides`。后者是高级分组的稀疏参数对象：未出现字段继承网关通用设置，显式 `false`/`0` 是覆盖，更新传空对象会清除全部覆盖；省略该对象则保持现值。管理接口还接受 `long_context_pricing_enabled` 和 `model_pricing`：创建时省略长上下文开关默认开启，显式 `false` 才关闭；更新时省略两者都保持原值，`model_pricing: []` 清空分组价卡。公开 Group DTO 返回有效价格开关和价卡供模型市场投影，但不包含调度器管理配置。

准入使用认证后最终选中的分组。普通 Key 在读取正文和调度前检查；复合 Key 需要先读取并恢复正文以解析目标分组，再按该最终分组检查。文本协议开关不扩展 Live、WebSocket、Embedding、图片或视频能力，也不会绕过账号 endpoint capability 等更窄限制。

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

网关错误必须保持调用协议形状：OpenAI 入口使用 `error` 对象，Anthropic 使用 `type: error` 与嵌套错误，Google 使用 HTTP code/message/status。认证、未分组、复合 Key 和本地能力拒绝都选择当前协议 writer；不能为了复用面板 helper 把一个 Google/Anthropic 客户端错误改成面板 envelope。

客户端协议被分组禁用时返回 `403`，并在账号选择、计费、重试和 fallback 前记录 `LocalPolicyDenied`。Anthropic 入口使用 `permission_error`，OpenAI 入口使用 `protocol_not_allowed`，Gemini 入口使用 Google `PERMISSION_DENIED`。模型列表 GET 不经过生成协议开关。

错误响应不得包含上游凭据、代理 URL、原始 service account、数据库错误或未经脱敏的请求正文。流式响应开始后不能再写普通 JSON 错误；只能按当前 SSE/流协议结束或发送允许的错误事件。

## 请求关联

- `X-Request-ID` 用于一次 HTTP 调用的日志和审计关联，最长持久化长度受限。
- `X-Client-Request-ID` 用于网关业务链路和结算幂等来源；缺失时由入口生成并写回。
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
