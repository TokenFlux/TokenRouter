# HTTP 接口

本文描述 TokenRouter 的路由族、认证方式、共同中间件、响应格式和各路由由哪个模块负责。新增或移动接口时，用本文确定接口应该放在哪里。本文不逐个列出所有 endpoint；上游协议规范和领域状态机见各自的文档。

## 章节导航

- [全局入口](#全局入口)：所有请求共同经过的处理顺序。
- [路由族](#路由族)：URL 前缀、认证方式和负责的模块。
- [支付管理恢复](#payment_admin_recovery)：订单强制过期和退款查单。
- [身份登录接口](#身份登录接口)：修改 OAuth start、人机验证和 One Tap 时读取。
- [创作台接口](#创作台接口)：修改工作区、任务和输出交付时读取。
- [提供商管理接口](#提供商管理接口)：修改批量管理、测试、诊断和导入时读取。
- [备份与维护接口](#备份与维护接口)：修改备份和系统操作时读取。
- [API Key 结算策略接口](#api-key-结算策略接口)：配置资金来源、查询订阅、收窄分组。
- [API Key 凭据轮换接口](#api_key_rotation)：原地替换用户 Key 的凭据。
- [分组客户端协议](#分组客户端协议)：客户端可用协议和上游平台各自独立配置。
- [价格管理与分组策略](#价格管理与分组策略)：修改不区分平台的价卡、模型规则和管理字段时读取。
- [认证方式](#认证方式)：JWT、管理密钥、API Key 和签名票据的区别。
- [外部支付管理集成](#外部支付管理集成)：服务间充值和嵌入页的对接方式。
- [API Key 上游用量查询](#api-key-上游用量查询)：修改管理员手动用量查询时读取。
- [公告接口](#announcement_api)：修改公告匹配、已读和归档时读取。
- [面板命令幂等](#write_idempotency)：修改认领、重放和存储故障策略时读取。
- [响应与错误](#response_errors)：面板 envelope 和各协议的错误格式。
- [请求关联](#请求关联)：request ID 的生成和透传。
- [已移除的接口](#已移除的接口)：已经下线、访问返回 404 或 400 的路径和字段。
- [新增接口检查清单](#新增接口检查清单)：新增接口时逐项核对。
- [产品名称兼容](#product_name_compatibility)：修改新旧导入标识、请求头和浏览器状态时读取。

新增创作台路由时，先读[创作台](../domains/creative_studio.md)，核对生命周期、幂等和留存规则。

## 全局入口

`SetupRouter` 在一个 Gin engine 上安装 app 提供的共同中间件和注册函数。common 路由由 server 注册；app 按顺序挂载 auth、user、admin、gateway、payment 和 page 路由，各模块的注册函数在各自的 HTTP 适配层里。综合设置的 GET 和 PUT 属于 `settings/httpapi`，预聚合有自己的处理器，创作模型候选和 worker 状态属于 `creative/httpapi`。输出格式的映射由所属的 HTTP 模块提供。主要的全局顺序：

```text
RequestLogger
  -> security client IP / session binding context
  -> access logger
  -> CORS
  -> security headers / CSP
  -> optional Server-Timing
  -> embedded frontend and API routes
```

`X-Request-ID` 由全局入口生成，网关复用这个 ID。`X-TokenRouter-Request-ID` 和兼容头返回同一个值，响应提交时固定为本地 ID。调用方传入的 ID 和供应商 ID 保存为可查询的外部别名。合法的 `X-Client-Request-ID` 继续回显，缺失时使用本地 ID。完整的生成、计费关联、留存和查询规则见[请求 ID 与请求查询](../operations/request_lookup.md)。

请求体大小限制和错误采集按路由族分别设置。网关在读取 JSON 或 multipart 之前，依次应用通用或文本的 body limit、client request ID、Ops error logger、endpoint 归一化和 API Key 认证。面板接口使用全局限流、重查询限流和审计；高风险的公开认证接口使用单独的 Redis 限流，依赖故障时拒绝请求（fail-close）。

## 路由族

| 路由族 | 认证 | 负责的模块和用途 |
| --- | --- | --- |
| `/health`、`/setup/status` | 无 | `server/router.go`；进程健康检查和正常模式下的 setup 状态 |
| `/api/event_logging/batch` | 无 | 兼容 Claude Code 遥测的空接收端，固定返回成功 |
| `/api/v1/auth/*` | 大多公开，账户管理的子流程按路由加 JWT 或短期状态 | `app/http_routes.go`；注册、登录、刷新、找回密码、OAuth、Passkey 登录和身份补全 |
| `/api/v1/user/*`、`/keys`、`/team`、`/groups`、`/subscriptions`、`/redeem` 等 | 用户 JWT | `app/http_routes.go`；用户面板的资源、团队、Key、用量和权益查询 |
| `/api/v1/admin/*` | 管理员 JWT 或受限的管理密钥；部分操作还需要 step-up | `app/http_routes_admin.go`；用户、分组、提供商、价格配置、设置、运维、备份、支付和安全管理 |
| `/api/v1/payment/*` | 用户 JWT | `app/http_routes.go`；读取配置和套餐、下单、查单、取消、invoice 和申请退款 |
| `/api/v1/payment/public/*` | 签名的 resume token，或旧订单的验证约束 | 恢复支付结果；这组接口只服务已知订单，匿名列举订单是不允许的 |
| `/api/v1/payment/webhook/*` | 提供商验签 | EasyPay、Alipay、WeChat Pay、Stripe、Airwallex 的通知 |
| `/v1/*` 和兼容的裸路径别名 | TokenRouter API Key | Anthropic 和 OpenAI 兼容的消息、Responses、Chat、图片、视频、模型、用量和批任务 |
| `/v1beta/*` | TokenRouter API Key | Gemini 原生的模型 URL、生成、流式生成和 token 统计 |
| `/antigravity/*` | TokenRouter API Key，加强制平台 | Antigravity 专用的 Claude 和 Gemini 入口，以及管理用的自省接口 |
| `/backend-api/codex/*` | TokenRouter API Key | Codex Responses、Realtime 和 sideband 的兼容入口 |
| `/api/v1/pages/*` 等页面路由 | 按页面类型使用用户或管理员 JWT | 服务端生成或读取的 pricing、账单和管理页面数据 |

### 各模块的 HTTP 入口

- 提供商：管理端展示值和脱敏映射在 `provider/httpapi/dto`，代理展示值在 `egress/httpapi/dto`。敏感字段、省略和空集合的处理、代理管理员字段的范围都按既定格式输出；非敏感的嵌套 map、slice 和时间指针各用独立副本，修改展示结果不会影响提供商配置。提供商备份、即时和计划测试、API Key 上游用量查询使用 `provider/httpapi`；提供商的增删改查、列表、复制和恢复、批量管理、凭据字段更新、刷新和重新授权、隐私、调度开关、额度重置和健康恢复绑定 `provider/httpapi.ManagementHandler`；模型目录、实时模型同步、tier 和详细统计也走这里。高级调度诊断绑定 `scheduler/httpapi.DiagnosticsHandler`。
- 设置：综合设置由 `settings/httpapi` 组合各领域的端点；SMTP、预聚合和创作状态分别调用各自的处理器。备份导出需要 step-up，导入使用管理员的幂等 helper。
- 订阅、兑换和套餐：用户端和管理端的 handler 和 DTO 在 `billing/httpapi`。URL、认证和幂等中间件的顺序、reason、CSV、分页排序都按既定格式。用户兑换历史 `GET /api/v1/redeem/history` 按 `page`、`page_size` 返回标准分页结构，按使用时间倒序，普通用户看到的数据不含 `notes`。管理员看到的套餐保持 Ent 的字段省略和 `edges` 格式，公开套餐使用单独整理过的数据。
- 身份、团队和 Key：用户资料、会话、七类身份、强认证和用户管理在 `identity/httpapi`，团队在 `team/httpapi`，Key 的生命周期和凭据在 `apikey/httpapi`。app 组装同一组身份处理器；微信支付 OAuth 在 `payment/httpapi` 里单独接入。HTTP 适配层保持历史的 DTO 格式和凭据差异，安全的 `Principal` 表示身份，Key 的 `AccessSnapshot` 表示付款和成员上下文。

`GET /api/v1/admin/usage/stats` 的可选参数 `endpoint_source` 接受 `inbound`、`upstream`、`path`，选择附带的端点统计；省略、空值或 `all` 返回全部端点维度，其他值返回 400。摘要字段照常返回，缓存按端点来源分别保存。管理页面先加载摘要和入站端点，切换图表后才读取上游端点或路径；筛选变化和页面退出会取消过期的端点请求。
- 网关观测：网关 HTTP 请求的 Ops 观测键、流错误快照和传输标记由 `gateway/httpapi` 管理；每个 WS turn 单独保存第一个错误，以及当次的提供商、模型和规则匹配快照。采集队列和持久化由 Ops 负责。
- 用量、审计和 Ops：用量和 Dashboard 的用户端、管理端入口在 `usage/httpapi`；`/v1/usage` 和 Antigravity 用量自省的公开 handler 由 app 直接构造，区分 quota_limited 和 unrestricted、日期范围、余额和指定订阅，统计按尽力而为计算。审计入口在 `audit/httpapi`，已清空的 TOTP 和管理员 API Key 的拒绝规则继续有效。Ops 的管理和实时入口在 `ops/httpapi`。留痕的保证见[清理与留存](../operations/observability_and_data_lifecycle.md#data_cleanup)。
- 通知、搜索、风控和站点：通知模板、SMTP 测试和公开退订绑定 `notification/httpapi`；搜索配置、管理测试和额度重置绑定 `search/httpapi`；风险配置、日志、媒体、Cyber 和解封绑定 `moderation/httpapi`；公开设置和页面由 `site/httpapi` 提供。
- 网关：HTTP、SSE、模型和计数入口直接绑定 app 构造的 `gateway/httpapi` 对象；Responses WebSocket 和 Live 有各自的 Handler。Qoder Chat、Messages 和 Responses、Messages 计数、OpenAI 和 Grok 计数、Responses 输入 token 预检，分别接入各自的用例。每个入口按自己的协议处理重试、部分用量、取消、认证顺序、裸路径别名和 Responses 子路径白名单。普通 Key 的协议门禁不提前读取 body，复合 Key 按既定时机读取模型并放回报文。实际的提供商循环在 `gateway/text`、媒体或会话用例里；每次 attempt 的模型和完成输入相互独立。

路由路径、中间件顺序、JSON 和 CSV、分页、ETag 和 304、WebSocket 子协议，修改时都要保持兼容。

客户端错误由 `gateway/httpapi` 写出，错误规则和它的管理在 `gateway/errorpolicy`。错误规则只改变返回给客户端的内容和是否跳过监控，提供商健康、重试和扣费资格都不受影响。停止时，请求和平台尝试共用 app 的进入屏障；在途请求的尾部完成后，才停止完成队列；超时时报告哪些阶段没完成、由谁持有。

网站展示请求使用 `Accept-Language`，语言和回退规则见[用户侧国际化](user_localization.md)。`GET /api/v1/settings/legal/:id` 按语言返回协议，账户资料的 `preferred_locale` 用于保存用户选择。

<a id="site_pages"></a>
### 站点页面

Markdown 正文需要 JWT，并且菜单可见；管理员页面只对管理员开放；页面列表需要管理员权限。图片不需要 JWT，公开接口接受普通可见页面的 PNG、JPEG、GIF、WebP、SVG、AVIF、ICO 和 BMP 文件。请求路径和符号链接解析后的目标都检查扩展名，Markdown 正文通过带 JWT 的正文接口读取。Markdown 正文和图片的响应格式各自独立。正文里指向页面根目录之外的符号链接返回 404，根目录内的链接可以读取，正文读取上限 1 MiB。文件适配层在同一个打开的句柄上做检查和限量读取。正文按 `pages/<slug>/<locale>.md` 和 `pages/<slug>.md` 选择，`Content-Language` 报告实际语言。语言专属图片目录缺少文件时使用公共目录。静态 SPA 和 `data/public` 由 web 模块负责。

<a id="subscription_self_revoke_api"></a>
### 订阅自助撤销

用户订阅页提供一个有额度条件的自助撤销接口：

- `POST /api/v1/subscriptions/:id/revoke` 需要用户 JWT。服务端只接受当前用户本人的、处于 `active`、并且最高层有限额度已经用完的订阅。成功时在事务里撤销当前记录，让同一套餐的下一份 pending 订阅提前生效，并自动改绑指定了这个订阅的 Key。
- 成功响应的 `data` 是 `{ revoked_subscription_id, replacement_subscription_id, rebound_api_key_count }`；没有接续的订阅时，`replacement_subscription_id` 为 `null`，改绑数量为 `0`。
- 订阅不属于当前用户或不存在，返回 `SUBSCRIPTION_NOT_FOUND`；不是当前 active 的记录，返回 `SUBSCRIPTION_NOT_ACTIVE`（409）；最高层额度还有剩余或套餐不限额，返回 `SUBSCRIPTION_QUOTA_NOT_EXHAUSTED`（409）。撤销不退款，用户端也没有恢复接口。

<a id="payment_admin_recovery"></a>
## 支付管理恢复

支付、推广和 Promo 的路由分别绑定 `payment/httpapi` 和 `promotion/httpapi`。Webhook 按原始的 body、query 和 Header 验签。

管理员可以用以下接口恢复支付订单，它们都经过管理员认证、面板限流和审计中间件：

- `POST /api/v1/admin/payment/orders/{id}/force-expire`：必填 JSON 字段 `reason`（1 到 500 个字符）。只有当前为 `PENDING` 的订单可以被改成 `EXPIRED`，整个过程不调用上游，成功时 `data.message=force_expired`。订单不存在返回 `NOT_FOUND`，状态被并发修改返回 `ORDER_STATUS_CHANGED`（409）。之后迟到的付款仍可以通过正常的 webhook 恢复。
- 退款查单：同时接受 `REFUND_PENDING`，以及带有有效准备记录的 `REFUNDING`。页面上是同一个查单按钮；接口只查询已经发起过的渠道退款，渠道退款只发起那一次。恢复记录不完整、互相矛盾或渠道无法确认时，返回要求人工核实的错误，调用方不能据此认为退款没发生。
- `POST /api/v1/admin/payment/providers/test`：接受 `provider_key`、`config` 和可选的 `instance_id`，目前只支持 `easypay`。带实例 ID 时，服务端按更新规则合并请求里没回传的敏感字段，再用随机订单号做一次只读查单。接口不保存草稿，也不创建订单；成功时只返回 `data.reachable=true`，上游的 body、URL 细节和凭据都不会出现在响应里。

普通取消在无法确认上游支付状态时，返回 `PAYMENT_STATUS_UNAVAILABLE`（503）。某个 provider instance 还有强制过期、尚未恢复的订单时，删除接口返回 `FORCED_EXPIRED_ORDERS`（409）；管理员应当停用并保留这个实例，以便接收迟到的回调。

`GET /api/v1/admin/groups/usage-summary` 返回管理员可见的全局分组汇总，字段是 `today_cost`、`yesterday_cost` 和 `total_cost`。自然日固定使用服务端配置的时区，接口不接受浏览器时区参数，所有管理员在同一个列表里看到的"今日"是同一个时间段。

## 身份登录接口

GitHub、Google、LinuxDo、DingTalk、WeChat 和 OIDC 的 OAuth 登录 start 同时支持 `GET` 和 `POST`。没有启用腾讯天御或阿里云验证码时，`GET` 照常返回 `302` 跳转。只要启用了其中一种，匿名登录就需要用 `POST`：腾讯的票据放在 `tencent_captcha_ticket` 和 `tencent_captcha_randstr`，阿里云的 `captchaVerifyParam` 放在 `turnstile_token` 字段；成功响应的 `data.authorize_url` 由前端负责跳转。

`*/bind/start` 是当前用户的绑定入口，不消费匿名登录用的验证码。Passkey 登录的 `/auth/passkey/login/begin` 使用同样的验证码字段映射，`finish` 只接受 ceremony session 和 WebAuthn credential。

`POST /api/v1/auth/oauth/google/one-tap` 接受浏览器 GIS 返回的 `credential`、本地的 `redirect`，以及可选的 `aff_code` 和 `promo_code`。credential 最大 16 KiB；入口按客户端 IP 用 Redis 限流，每分钟 20 次，Redis 故障时拒绝请求。接口不接收 Client Secret，token 和未验证的 claims 都不写日志。验证通过、已有用户登录成功时，统一 envelope 的 `data` 返回 `status=authenticated`，以及标准的 `access_token`、`refresh_token`、`expires_in`、`token_type`。新用户只返回 `status=registration_required` 和本地的 redirect，通过 HttpOnly 的 pending cookie 继续 `/auth/oauth/callback` 的补全流程。

One Tap 设置或 Google OAuth 配置无效、backend mode、启用了腾讯或阿里云验证码、注册关闭、token 无效或用户状态不可登录时，请求被拒绝。只开启 Turnstile 时，这个入口的校验范围保持不变。

## 创作台接口

创作台（Creative Studio）使用 `/api/v1/creative/*` 路由族，需要用户 JWT（在 `app/http_routes.go` 注册，返回统一 envelope），用于个人的图片生成、编辑和局部重绘任务：

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

除了 `GET /creative/models` 和 `GET /creative/capabilities`，其他创作台路由（创建任务、历史、活动任务、详情、输出内容和 ack）都需要 `X-Creative-Workspace-ID` 请求头（规范化的小写 UUID）。缺失时返回 `400 CREATIVE_WORKSPACE_REQUIRED`，格式不对返回 `400 CREATIVE_WORKSPACE_INVALID`；任务不属于这个工作区时，统一返回 `404 CREATIVE_RUN_NOT_FOUND`。

`GET /creative/capabilities` 返回 `max_prompt_chars`、`max_asset_bytes`、`max_total_input_bytes`、`max_mask_bytes`，以及允许的 PNG、JPEG、WebP MIME 类型。`GET /creative/runs/active` 用不透明的 cursor 分页，返回所有活动状态的任务，不受历史页大小的限制。`POST /creative/runs` 接受 `multipart/form-data`，除了素材字段，还可以提交 `image_size`、`aspect_ratio`、`quality`、`background` 和 `thinking_level`。输出格式由供应商决定，请求里带 `output_format`、`output_compression` 或旧的 `response_mime_type` 会被拒绝；输出 metadata 的 `mime_type` 记录供应商实际返回的 MIME。

所有参数都按 `GET /creative/models` 返回的模型能力校验，每个任务固定生成一张图片。各平台支持的操作：OpenAI 支持 `generate`、`edit`、`inpaint`；Gemini 和 Grok 支持 `generate`、`edit`。Grok 编辑最多 3 张源图，Gemini 不接受单独的 mask。素材只能以上传文件提交，远程 URL 会被拒绝。请求可以带 `Idempotency-Key`：同一用户、同一工作区、同一个键、请求体也相同时，返回原来的任务（`idempotent_replay=true`）；同一用户、同一工作区、同一个键但请求体不同时，返回 `409 CREATIVE_IDEMPOTENCY_CONFLICT`；不同工作区可以用同名的键各自创建任务。

只有结算完成、可以交付的终态任务，才能读取输出内容。ack 先写数据库，再删除服务端的临时输出，删除失败由后台清理补做。临时输出过期或丢失时，输出内容路由返回 410 类错误（`CREATIVE_OUTPUT_EXPIRED` 或 `CREATIVE_RESULT_LOST`），并把任务改为 `result_lost`。服务端只在数据库里保存任务元数据，图片和 prompt 明文只存在 Redis 的临时键里，细节和限制见[创作台](../domains/creative_studio.md)。

部分下载路由使用短期的签名票据，让浏览器可以原生下载大文件；票据只授权一个预先生成的资源，权限远小于用户 JWT。模型列表、用量和已有批任务的管理接口即使跳过了余额检查，仍然会验证 Key 的身份和资源归属。

### 创作台的管理设置

管理员设置接口 `GET|PUT /api/v1/admin/settings` 的 `creative_model_settings` 字段维护创作台全局的生图模型白名单，格式是 `[{"group_id":123,"model":"gpt-image-2","operations":["generate","edit","inpaint"]}]`。省略这个字段时保留现值，传空数组时清空。服务端校验能力值和 `(group_id, model)` 的唯一性，审计里只记录这个字段是否有变化。

保存和执行时，按这个模型的候选提供商和实际的 provider 规范化操作：OpenAI 支持 `generate`、`edit`、`inpaint`，Gemini 和 Grok 支持 `generate`、`edit`；同一个分组里的不同模型可以对应不同的 provider。调用资格以解析出的可执行能力为准，只写进白名单而解析不出能力的配置无法调用。

`creative_worker_count` 也属于这个接口，要求是大于 0 的整数，默认 128，保存后热更新当前实例的创作台 worker 池。`GET /api/v1/admin/settings/creative-model-candidates` 返回候选列表 `{group_id, group_name, platform, model, operations}`，条件是分组 active、启用了图片生成，并且有可以调度的图片模型；结果不按管理员本人的用户权限过滤。候选里的 `platform` 是这个模型实际的 provider，和分组无关。任务提交前会固定提供商、provider、参数和价格快照，已有的任务按这些绑定继续执行。

`GET /api/v1/admin/settings/creative-worker-status` 返回创作台 worker 池的快照 `{running, worker_count, busy_workers}`。运行中时，`worker_count` 是当前活动的 worker 数，`busy_workers` 是正在处理任务的 worker 数，管理端的设置页据此轮询显示使用情况；没有运行时返回 `running=false` 的零值快照，前端改为显示 `creative_worker_count` 的配置值。

## 提供商管理接口

提供商的创建、编辑、批量创建和更新，以及支持选择分组的导入入口，都使用明确的 `group_ids`，可以把提供商关联到任何平台提供商所在的分组。通用备份格式不包含分组关系，导入后的提供商没有分组；其他创建入口没有指定分组时，也不会自动绑定默认分组。Spark 影子没有指定分组时，可以继承母提供商已有的分组关联；母提供商没有分组时，影子同样没有。OAuth-only、隐私和实际的协议能力继续限制使用资格。

提供商的写入、备份和 Codex 导入，以及创建提供商的 OAuth、PAT、SSO 入口，对 `confirm_mixed_channel_risk`、`skip_default_group_bind` 等未知的结构字段返回 400，没有确认旁路。这些接口在各自的 handler 里使用严格的 JSON 绑定，`credentials`、`extra` 这类动态 map 照常接收，内容由领域层校验；其他接口的全局 Gin 行为保持不变。

批量删除提供商使用 `POST /api/v1/admin/providers/batch-delete`，请求体是 `provider_ids`。服务端先去掉非正数和重复的 ID，再以最多 5 路并发执行删除。同一批里同时选了父提供商和它的影子时，只删除根提供商一次，级联的影响映射回每个提供商的结果里。响应按固定顺序返回 `success_ids`、`failed_ids` 和错误明细，某一项失败不会取消其他项。管理端"全选筛选结果"先按同一个筛选快照分页读取轻量的 ID；任何一页缺失或重复时，保留原来的选择；集合完整时才提交。

### 连接测试

管理员测试提供商连接使用 `POST /api/v1/admin/providers/:id/test`，响应是 SSE。请求体可以包含：

- `model_id` 和 `prompt`。
- OpenAI 专用的 `mode`。
- 文字测试的 `protocol`：OpenAI API Key 可选 `responses` 或 `chat_completions`，OAuth 只接受 `responses`；Kimi、Zhipu、DeepSeek 可以在已启用的 `chat_completions`、`anthropic`、`responses` 中选择；其他平台只有一个测试端点，带这个字段会被拒绝。
- `test_type`（`text` 或 `image`）；旧客户端也可以用 `test_mode` 作为别名。

管理端每次都发送 `test_type`：`text` 走文字测试并使用自定义提示词，`image` 走图片测试并使用自定义提示词。OpenAI 的 `compact` 和 `legacy_compact` 是固定载荷的连接测试，忽略自定义提示词，也不会修改提供商的能力开关。

测试结果不会作为能力探测状态返回或保存，但认证错误、限流和额度观测照常记录。只有没带 `test_type` 的旧调用，才按模型名做兼容判断。图片结果通过 SSE 的 `image` 事件返回，文字结果通过 `content` 事件返回；提供商的平台没有对应的图片端点时，返回流式错误事件。

创作台的 JWT 和工作区入口由 `creative/httpapi` 处理，批量图片的 Key 入口由 `batchimage/httpapi` 处理。批量下载流关闭时会释放下载许可，应用关闭时等待完整的 HTTP 调用结束。创作供应商已经成功、但临时输出无法交付时，返回 `result_lost`，资金按已经确认的服务收取；这个状态表示生成发生过，系统也不会自动重新生成。

### 高级调度评分诊断

提供商的高级调度评分诊断只对管理员开放。`GET /api/v1/admin/providers/:id/advanced-scheduler-score` 返回这个提供商所属的高级分组摘要；带 `group_id` 时，返回指定高级分组的完整候选池、硬过滤结果、有效配置、各指标的原值、归一化值和贡献、Top-K 权重、实际活动池的概率，以及平台策略的提示。开启订阅优先、并且存在合格的订阅提供商时，普通提供商标记为延后，不参与本轮概率。开启粘性加权时，previous-response 和 session 只影响 Top-K 权重；关闭时，有效的硬粘性提供商按实际的强制选择显示概率 1。

`POST /api/v1/admin/providers/:id/advanced-scheduler-score/preview` 接受 `group_id`、可选的 `requested_model`、`sticky_provider_id` 和 `previous_response_provider_id`，做无状态的评分模拟。previous-response 按候选提供商和协议的实际能力应用，不支持的提供商返回 `ignored`。分组摘要里没有平台字段；没有关联目标分组的提供商，原因标记为 `group_mismatch`。请求体拒绝其他任何字段，session hash、响应正文和凭据都无法传入。

这两个接口不分配并发槽，也不写粘性；响应里没有凭据、代理认证、session hash 和上游响应内容。诊断复用生产环境里、仅凭请求信息就能判断的硬过滤；endpoint、transport、compact、media 等需要请求上下文的能力，标记为 `not_evaluated`。

路由前缀只表示客户端用的协议。`/v1/messages` 等通用入口先在当前分组里选出满足模型、协议和其他策略的提供商，再按这个提供商的平台调用单次执行器。供应商由选号结果决定，模型族和分组的展示品牌都不影响它；`/antigravity/*` 另外有强制平台条件。

### 同步、导入和用量

CRS 同步和预览路由绑定 `provider/httpapi.CRSHandler`，路径是 `/api/v1/admin/providers/sync/crs` 和 `/preview`，使用管理员中间件和标准的错误 envelope。默认同步代理，传 false 时关闭；"只创建选中项"传空集合时，已存在提供商的更新照常执行。Codex session 导入 `/api/v1/admin/providers/import/codex-session` 绑定 `provider/httpapi.CodexImportHandler`，包含请求校验、逐项结果和幂等 scope。

Ollama Cloud 的设置、状态、会话、自动刷新和主动刷新路由绑定 `provider/httpapi.OllamaUsageHandler`，需要管理员认证，敏感的会话内容不回显。提供商的主动和被动用量、批量用量、今日统计和批量今日统计绑定 `provider/httpapi.OAuthUsageHandler`，带 30 秒的快照缓存、ETag、Vary、304 和 `X-Snapshot-Cache`。

其余的提供商管理路由都使用 ManagementHandler；供应商的 OAuth 交换路由由 `provider/httpapi` 提供，调用对应的提供商授权用例。

## 备份与维护接口

`/api/v1/admin/backups` 和旧的 data management 路由绑定 `backup/httpapi`，`/api/v1/admin/system` 绑定 `ops/httpapi` 的系统维护处理器。这些接口需要 step-up 和管理员身份，恢复时复核密码，校验 ID，并使用幂等 envelope；data management 返回功能已下线的响应。响应由 HTTP 层写出，密码复核只把布尔结果传给备份核心，用户实体留在 HTTP 层。

## API Key 结算策略接口

普通 Key 需要绑定一个可用的分组。历史上没有绑定分组的 Key，字符串仍然保留，调用前需要先绑定；系统没有不经过分组的调度入口。`fallback_when_group_unavailable` 只能回退到管理员配置的回退分组，目标分组要重新检查权限、模型、协议、团队和订阅范围。

`POST /api/v1/keys` 和 `PUT /api/v1/keys/{id}` 接受 `billing_mode`（`auto`、`subscription`、`balance`）和可空的 `preferred_subscription_id`。省略模式或使用 `auto` 时，先用订阅，不足的部分由余额补足；`balance` 会清除指定的订阅；`subscription` 需要指定当前付款主体的一份有效订阅。个人 Key 的付款主体是本人，团队 Key 的付款主体是 Team Owner。

创建和更新 API Key 时，`quota`、`rate_limit_5h`、`rate_limit_1d`、`rate_limit_7d` 要求是有限的、非负的、小于 `1e12` 的 USD 数值，以适配数据库的 `DECIMAL(20,8)`；`0` 表示不限额。创建时省略 `expires_in_days` 表示永不过期，提供时要大于 0；更新时用空的 `expires_at` 清除到期时间，用合法的 RFC3339 时间设置到期点。handler 的早期校验和 service 的最终校验使用同一套规则，内部调用同样要经过校验。

`GET /api/v1/keys/billing-options?scope=personal|team` 返回当前作用域下可以指定的有效订阅摘要，包括 `id`、`plan_id`、`plan_name`、`expires_at`、`groups_restricted` 和 `applicable_groups`。`GET /api/v1/groups/available?scope=personal|team&subscription_id={id}` 带 `subscription_id` 时，返回付款主体已有的分组权限和这个订阅套餐分组的交集；不带时返回普通的可用分组列表。两个接口在团队作用域下都不会返回成员自己的订阅。可见分组的 `models` 和 `model_protocols` 来自有限模型目录，供客户端配置时选择型号，权限由请求时的分组策略校验。`include_models` 默认为 `true`；传 `false` 时保留作用域、套餐和分组权限过滤，跳过模型、协议和模型属性计算。使用记录及仪表盘的筛选项使用轻量查询，Key 配置导出使用完整查询。非法的 `include_models` 值返回 400。

网关的 `GET /v1/usage` 除了 Key 配额、订阅或余额字段，总是返回一个 `billing` 对象，至少包含 `mode`、`source`、`preferred_subscription_id`、`available` 和 `unit`：

- `source=subscription` 时，只返回实际选择的订阅的额度和剩余值；指定的订阅失效时，来源仍是这个订阅，并标记 `available=false`，余额不出现。
- `source=balance` 时，只返回付款主体的余额，订阅额度不加载也不展示。
- `auto` 的 `source` 随当前可用的订阅变化。

Key 自身的配额和滚动限额字段不受这些展示规则影响。

<a id="api_key_rotation"></a>
## API Key 凭据轮换接口

`POST /api/v1/keys/:id/rotate` 使用面板 JWT，以及用户限流和审计中间件，不需要请求体。成功时返回包含新 `key` 的 API Key DTO，ID 不变。非正整数的 ID 返回 `400`；Key 不存在、不属于当前用户或是系统托管的，统一返回 `404`。团队 Key 返回和编辑操作相同的团队上下文错误。读取之后记录被并发修改时，返回 `409 API_KEY_ROTATION_CONFLICT`，调用方需要刷新记录后再试。

前端从更多菜单进入确认框，提交期间禁止重复点击和关闭，成功后更新列表里的凭据，并提供复制入口。配置和用量的保留、缓存失效和并发处理见 [API Key 凭据轮换](../domains/identity_and_tenancy.md#api_key_rotation)。

## 分组客户端协议

分组接口不返回 `platform` 和 `is_default`，协议配置通过 `allowed_protocols`、`protocol_fallbacks`、`responses_image_policy` 返回。`protocol_fallbacks` 是入口到有序目标数组的映射：缺少某个入口表示自动，空数组表示只走原生，非空数组限定目标。提供商仍然有平台，使用 `credentials.upstream_protocols`。管理员只读接口 `GET /api/v1/admin/protocol-capabilities` 提供全部协议、各提供商的原生 profile、单元素的通用分组 profile、可用的入口和转换目标。字段的完整含义见[统一协议能力](protocol_capabilities.md)。

旧的 `allowed_client_protocols`、媒体和 Live 开关、提供商文本路由，只在输入时兼容，新的响应和导出使用统一结构。分组模型映射统一使用 `routing_policy.model_mapping`。门禁拒绝时，在调用上游之前返回对应协议的 403 错误；平台没有实现的入口返回 404。

## 价格管理与分组策略

分组的管理请求和列表筛选不接受上游平台。分组的 `routing_policy.model_mapping` 是 `Record<string,string>`，`allowed_models` 是 `string[]`，外面没有平台这一层。提供商列表仍然可以按提供商自己的平台筛选。

`/api/v1/admin/pricing/configs` 和它的 `/:id` 子路由提供共享价格配置的增删改查。请求只接受价格字段；模型映射、白名单和功能字段通过分组的 `routing_policy` 保存。共享价卡和提供商成本价卡都没有 `platform`，模型规则统一检查是否重叠；价格配置里出现未知字段时返回 400。

默认价的只读查询在 `/api/v1/admin/pricing/defaults` 和 `/model`，`/model` 不需要上游平台参数，默认目录列表可以按模型来源标签筛选。价格配置通过手动添加模型规则维护。价格规则见[管理员默认价格查询](model_catalog_and_marketplace.md#gateway_default_pricing)。模型链字段使用 `group_mapped`；历史用量里的共享价格关联字段是 `pricing_config_id`，取值就是原来的 ID。

管理员手动更新价格目录使用 `POST /api/v1/admin/pricing/defaults/update`，复用服务端已经配置好的目录来源，请求里不接收来源地址或文件路径。普通的 GET 查询和列表刷新不会触发更新。

价格配置的创建、更新和响应包含：`peak_rate_enabled`、`peak_start`、`peak_end`、`peak_rate_multiplier`、`long_context_pricing_enabled`、`free_openai_fast`、`batch_image_discount_multiplier`、`batch_image_hold_multiplier`，以及 `web_search_price_per_call`、`search_price_per_1k`、`audio_realtime_price_per_min`、`audio_tts_price_per_million_chars`、`audio_stt_price_per_hour`。更新时省略某项设置表示保持现值；五项可空单价传 `null` 表示清除覆盖，传 `0` 表示免费。预扣倍率不能低于折扣倍率，高峰窗口只接受同一天内的有效时段。

分组接口没有上述价格字段，也没有 `model_pricing`；`rate_multiplier` 仍然属于分组。控制台把基础倍率放在分组的"基本"页，其余设置统一在价格配置的"计费设置"页编辑。旧的分组价格值不会复制到价格配置里。

## 认证方式

| 凭据 | 放在哪里 | 授权范围 |
| --- | --- | --- |
| JWT access token | 面板的 `Authorization: Bearer`，少数 WebSocket 用子协议 | 检查当前用户状态、token version 和可选的会话绑定；管理员还检查当前角色 |
| Refresh token | `/api/v1/auth/refresh`，以及 logout 约定的 payload 或 cookie | 只用于轮换和撤销，无法直接访问业务资源 |
| 管理 API Key | 管理接口的 `x-api-key` | 代表第一个实际的管理员；开启敏感操作 step-up 后，需要近期 TOTP 的操作无法用它执行 |
| TokenRouter API Key | 网关的 `Authorization: Bearer`、`x-api-key`，Gemini 兼容入口还有 `x-goog-api-key` | Key、用户和团队、分组、IP、额度和订阅、请求资源的归属；通用网关拒绝放在 query 里的 Key |
| OAuth 和 pending 补全状态 | auth callback 和补全接口 | provider state、浏览器会话、一次性完成码和过期时间共同约束 |
| Google GIS ID Token | `/api/v1/auth/oauth/google/one-tap` 的请求体 | 经官方验证器校验后，`sub` 和已验证的邮箱进入 Google 身份和 pending 流程；这个 token 只用于这一个接口 |
| 支付 webhook 签名 | 原始的 body 或 query，加上 provider 的 header | 只授权解释一条已绑定本地订单的通知，金额和 metadata 仍要校验 |
| 下载和 resume 票据 | 指定的公共恢复或下载路由 | 有时限，限定资源和操作，无法升级为一般的会话 |

认证成功只确定了"谁在请求"。资源的 owner、团队成员、管理员 step-up、支付订单的 user ID、批任务的 owner 和分组能力，由对应的 HTTP 适配层和业务用例分别检查。路由挂了认证中间件，对象级的授权检查仍然要做。

## 外部支付管理集成

外部支付服务使用管理 API Key，通过 `x-api-key` 调用管理路由；管理员 JWT 适合交互式的管理端。支付成功后的余额发放，优先使用 `POST /api/v1/admin/redeem-codes/create-and-redeem`，由服务端原子地创建并兑换余额兑换码。调用方要提供稳定的业务 `code` 和 `Idempotency-Key`，同一操作重试时复用这两个值，并按 200、409 和业务错误区分重放、冲突和失败。`GET /api/v1/admin/users/:id` 可以用来做前置查询；`POST /api/v1/admin/users/:id/balance` 只用于明确的人工增减或补偿，同样需要幂等键。

购买页和用户自定义页面的 URL 由前端追加 `user_id`、`token`、`theme`、`lang`、`ui_mode`、`src_host` 和 `src_url`。其中 `token` 是用户的 Bearer 凭据，只能发给部署者信任、并且使用 HTTPS 的页面；接收方不应把它写进访问日志、分析参数，或转发给第三方。完整的请求示例和重试约定见[外部支付管理 API 指南](../guides/payments/admin_integration_api.md)。

## API Key 上游用量查询

管理员的提供商列表提供两个手动查询、只用于展示的接口：

- `POST /api/v1/admin/providers/:id/upstream-usage/query`
- `POST /api/v1/admin/providers/upstream-usage/query/batch`，请求体的 `provider_ids` 最多 100 个正整数。

接口只接受 `type=apikey` 的提供商（Bedrock 除外），使用管理员认证和审计中间件。可选的适配器有 `sub2api`、`new_api` 和 `zivv`（Kimi、Zhipu、DeepSeek 使用按平台固定的适配器），由提供商的配置明确选择。成功结果的顶层包含 `adapter`、`provider`、UTC 时间的 `observed_at`，以及余额、限额和订阅字段；批量接口把每个提供商的成功结果和结构化错误分开返回。错误 reason 使用 `UPSTREAM_USAGE_*` 命名空间，覆盖：提供商无效或已禁用、协议不支持、认证失败、钱包不可用、钱包认证失败、限流、超时、响应格式错误、网络错误和身份变化。

查询只读：提供商、Extra、调度快照和计费记录都不会被写入，API Key 也不会出现在响应或审计 body 里。前端只在点击行内按钮或批量操作时发起请求，成功的结果在按管理员隔离的 `sessionStorage` 里缓存五分钟。缓存规则见 [API Key 上游用量查询](upstream_usage.md)。

<a id="announcement_api"></a>
## 公告接口

公告的用户端和管理端 handler、DTO 在 `site/httpapi`，接入相同的认证、审计和限流。用户入口是 `GET /api/v1/announcements` 和 `POST /api/v1/announcements/:id/read`；管理员的增删改查和已读状态在 `/api/v1/admin/announcements` 下。

site 负责定向条件的校验、余额和有效订阅的匹配、开始和结束时间、已读和到期归档。用户数据由 app 从 identity 适配，有效订阅数据由 app 直接从 billing 适配；HTTP 适配层不直接访问数据库。开始和结束字段的省略、用零值清空、分页排序和 JSON 格式都保持兼容。重复标记已读时，保留第一次的读取时间。管理员查询前先归档过期的公告，归档失败时返回错误。

<a id="write_idempotency"></a>
## 面板命令幂等

app 为所有需要幂等的用户和管理员 HTTP 处理器绑定同一个协调器。`idempotency` 负责认领、请求指纹、重放、冲突、失败退避和响应存储，`idempotency/postgres` 负责持久化记录，`idempotency/httpapi.Executor` 负责接入 HTTP。

命令的身份由业务 scope、操作者范围、HTTP 方法、路由和 `Idempotency-Key` 组成；同一个键、不同的 payload 产生指纹冲突。重放时返回 `X-Idempotency-Replayed: true`；处理中或退避中的错误，按协调器的结果返回 `Retry-After`。存储的响应按 UTF-8 截断并脱敏。TTL 从绑定的实例读取，进程里没有默认的协调器。

用户 helper 和默认的管理员 helper 在存储不可用时拒绝请求。管理员操作如果明确选择了降级模式，可以继续执行，并返回 `X-Idempotency-Degraded: store-unavailable`。没有绑定协调器的零值 Executor 会直接执行回调，这只用于独立的 HTTP 测试夹具；生产环境的装配都会绑定协调器。observe-only 行为由协调器的配置控制。

面板命令的幂等和 billing 的资金事务去重是两层不同的保护。维护操作锁有自己的全局范围、续租和成功或失败的处理。清理 worker 由 app 启动，并在停止时等待，数据库关闭之前清理已经结束。

<a id="response_errors"></a>
## 响应与错误

面板和内部 REST 接口通常使用统一的 envelope：

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

业务错误由 `ApplicationError` 映射成 HTTP status，可以带 `reason` 和字符串 `metadata`。未知错误按 500 处理，详情脱敏后只记在服务端。分页数据使用 `items`、`total`、`page`、`page_size` 和 `pages`；创建和异步接受分别可以返回 201 和 202。

错误实体只在 `pkg/apperror` 定义，HTTP 映射和面板 envelope 在 `server/httpx`。使用方直接使用具名的错误类别，code 数值、字段、`errors.Is/As`、cause 和 metadata 的复制方式都保持兼容；自定义状态码按原值映射。

管理员 `GET /api/v1/admin/usage` 的每条记录可以带 `detailed_timing`。它通过同一个内部请求 ID 关联 `http.access` 日志得到，字段是相对于请求进入 TokenRouter 的毫秒时间点，包括：拿到提供商槽位、上游连接和写入、首字节、第一个 SSE、第一个可见输出和第一次下游 Flush。历史记录或观测日志缺失时，省略这个对象。

网关内置错误固定使用英文。网关错误使用调用方协议的格式：OpenAI 入口用 `error` 对象，Anthropic 用 `type: error` 加嵌套错误，Google 用 HTTP code、message 和 status。认证失败、未分组、复合 Key 和本地能力拒绝，都使用当前协议的 writer；Google 或 Anthropic 客户端收到面板 envelope 会无法解析。

客户端协议被分组禁用时，返回 `403`，并在提供商选择、计费、重试和 fallback 之前记录 `LocalPolicyDenied`。Anthropic 入口返回 `permission_error`，OpenAI 入口返回 `protocol_not_allowed`，Gemini 入口返回 Google 的 `PERMISSION_DENIED`。模型列表的 GET 不经过生成协议的开关。

错误响应隐去上游凭据、代理 URL、原始 service account、数据库错误和未脱敏的请求正文。流式响应开始后，普通 JSON 错误已经写不出去，只能按当前的 SSE 或流协议结束，或者发送协议允许的错误事件。

## 请求关联

本地请求 ID 贯穿 HTTP 响应、日志、使用记录、错误、审核和审计。`usage_logs.billing_key` 保存资金去重值，账本和资金去重表的既有字段继续保存历史动作键。客户端重试会生成新的调用 ID；任务重放通过业务动作键去重。

管理员通过 `/api/v1/admin/requests` 按 `request_id` 查询诊断详情，写入状态接口同样要求管理员身份。调用方与供应商的 ID 可以作为搜索别名。普通用户在使用记录里筛选和复制请求 ID。后台完成任务携带请求快照，在独立超时内写入用量和请求摘要。

协议、兼容响应头和查询权限见[请求 ID 与请求查询](../operations/request_lookup.md)。

## 已移除的接口

以下路径和字段已经下线，访问时按表中的方式返回；它们没有兼容 handler、重定向或弃用提示，调用方需要改用新接口。

| 路径或字段 | 返回 | 替代 |
| --- | --- | --- |
| `GET /api/v1/requests`、`GET /api/v1/requests/:request_id` | `404` | 管理员使用 `/api/v1/admin/requests`，普通用户通过使用记录查看用量 |
| `GET`、`PUT /api/v1/admin/settings/openai-oauth-import-defaults` | `404` | 在提供商创建表单或导入文件中填写配置 |
| `GET /v1/sub2api/billing` | `404` | 无；这个路径不再享有 API Key 非消费请求的豁免 |
| `GET`、`PUT /api/v1/admin/providers/upstream-billing-probe/settings`、`POST .../upstream-billing-probe/batch`、`PUT`、`POST /api/v1/admin/providers/:id/upstream-billing-probe` | `404` | 无 |
| `POST /api/v1/admin/providers/check-mixed-channel` | `404` | 提供商写入时直接校验 |
| `GET /api/v1/user/platform-quotas`、`GET`、`PUT /api/v1/admin/users/:id/platform-quotas`、`POST /api/v1/admin/users/:id/platform-quotas/reset` | `404` | 提供商上游额度、余额、订阅、团队和 Key 限额各自的接口 |
| `/api/v1/admin/channels` | `404` | `/api/v1/admin/pricing/configs` |
| `/api/v1/admin/pricing/defaults/models`（批量补入目录模型） | `404` | 在价格配置里手动添加模型规则 |
| `GET /api/v1/admin/groups/:id/stats`（管理员已认证） | `404` | `/admin/groups/usage-summary` 和 `/capacity-summary` |
| `GET /api/v1/admin/redeem-codes/stats` | 按兑换码 ID 路由处理，返回非法 ID 的 `400` | 列表里的条目和分页总数 |
| 综合设置的 `default_platform_quotas`、`auth_source_default_*_platform_quotas`、`allow_ungrouped_key_scheduling` | `400` / `REMOVED_SETTING_FIELD` | 无 |
| Key 的 `fallback_to_default_group_when_unavailable`（包括 false 和 null） | `400` | `fallback_when_group_unavailable` |
| 分组的 `platform`、`is_default` | `400` | `allowed_protocols` 等协议字段 |
| `messages_dispatch_model_config` | `400` | `routing_policy.model_mapping` |
| 综合设置的 `grok_cross_client_model_map_enabled` | `400` | 分组或提供商手动配置的模型映射 |

## 新增接口检查清单

新增或移动接口时，至少核对：

- 由 common、auth、user、admin、payment、gateway 还是页面路由负责，是否使用已有的前缀和 handler 分派。
- 需要无认证、JWT、管理员、step-up、API Key、provider 签名，还是短期票据；是否还需要对象级的 owner 检查。
- body 和 header 限制、面板限流、审计、Ops 采集、request ID 和 client request ID、Server-Timing 是否适用。
- 返回面板 envelope，还是 OpenAI、Anthropic、Google 的协议格式；流式开始后的错误路径是否有效。
- 后端的路由接口测试、HTTP 和用例测试、前端的 API 模块是否一起更新。
- 有没有引入冲突的动态路由，例如 wildcard 或子路径吞掉了已经移除或专用的固定 endpoint。

<a id="product_name_compatibility"></a>
## 产品名称兼容

提供商和代理的导出使用 `type=tokenrouter-data`、`version=2`。导入同样接受 `type=sub2api-data` 的版本 2 文件；版本 1、缺少版本和带不支持字段的文件仍然被拒绝，旧的账号集合不会因为名称兼容而重新启用。

网关响应同时提供 `X-TokenRouter-Request-ID` 和值相同的 `X-Sub2API-Request-ID`。这两个内部关联头在入口都会从请求里删掉，调用方无法指定内部身份，它们也不会透传给上游。`X-Client-Request-ID` 作为父请求标识的用法不变。

Grok 请求缓存开关使用 `X-TokenRouter-Grok-Client-Tool-Cache`，没有新头时才读取旧的 `X-Sub2API-Grok-Client-Tool-Cache`；新头明确关闭时优先，两种头都只在本地使用。Ops WebSocket 优先协商 `tokenrouter-admin`，兼容 `sub2api-admin`，JWT 继续通过单独的 `jwt.<token>` 项提供，并且不会被回显。

浏览器的语言设置和登录协议确认使用 `tokenrouter_` 开头的存储键；新键没有值时，复制旧键的值，协议仍然按 revision 判断。新键已经有值或撤回记录时，以新键为准；可以重建的缓存使用新名称，旧缓存留在浏览器里，等它自然失效。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关错误响应策略](gateway_error_policy.md)、[身份与租户](../domains/identity_and_tenancy.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[支付与权益](../domains/payments_and_entitlements.md)、[接口目录](index.md)。

## SystemOne

`POST /v1/systemone` 使用网关 API Key 认证，支持普通 Key 和复合 Key。分组开放 `systemone` 后，调度到已启用该协议的 Jev API Key 提供商。请求字段、成功响应和用量异常处理见 [Jev 与 SystemOne](jev_upstream.md)。
