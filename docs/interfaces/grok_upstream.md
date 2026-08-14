# Grok / xAI 上游

TokenRouter 支持 Grok OAuth 订阅账号和标准 xAI API Key 账号，并通过 OpenAI 兼容的 Responses、Chat Completions、Messages 和 WebSocket 入口转发请求。Grok 分组还支持图片生成/编辑、视频生成/编辑/扩展、视频状态查询、原生搜索和 Voice API。

本文覆盖账号凭据、聊天/媒体转发、媒体资格、异步视频归属、模型目录和运行时变量，不定义 xAI 套餐价格，也不把上游当前返回的所有动态模型固化为兼容承诺。

## 章节导航

- [基本信息](#基本信息)：修改路由或默认上游时读取。
- [账号配置](#账号配置)：修改 OAuth/API Key 导入与刷新时读取。
- [客户端协议](#客户端协议)：修改 Responses、Chat 或 Messages 准入时读取。
- [媒体请求格式](#媒体请求格式)：修改图片/视频 body 转换时读取。
- [媒体账号资格](#媒体账号资格)：修改付费探测和调度隔离时读取。
- [搜索与语音](#搜索与语音)：修改搜索、TTS、STT、自定义 Voice 或 Realtime 时读取。
- [任务归属与结算](#任务归属与结算)：修改视频查询、下载或用量记录时读取。
- [客户端配置](#客户端配置)：核对生成给客户端的 base URL。
- [默认模型目录](#默认模型目录)：修改内置模型与别名时读取。
- [环境变量](#环境变量)：修改 OAuth 或上游运行参数时读取。

## 基本信息

- 平台名：`grok`
- 账号类型：OAuth 订阅账号、API Key 账号
- 主要网关入口：`/v1/responses`、`/responses`、Chat Completions、Messages、Responses WebSocket、`/v1/web_search`、`/v1/x_search`、`/v1/tts`、`/v1/stt`、`/v1/custom-voices` 和 `/v1/realtime`；Voice/搜索也提供不带 `/v1` 的同名入口
- API Key 账号默认上游地址：`https://api.x.ai/v1`

## 客户端协议

Grok 分组支持 Anthropic Messages、OpenAI Responses 和 Chat Completions，新建时默认启用 Responses 与 Chat；三项都可关闭，迁移前已有分组启用三项。文本协议禁用时会在账号选择、计费、重试和 fallback 前返回协议原生 `403`。

Grok Responses 上游可能注入严格客户端不认识的 `event: ping` 帧。流式转发会把 data 未声明冲突事件类型的 ping 改写为 `: ping` SSE 注释，既保留连接活性，也避免中断 Grok CLI 或 Codex CLI；普通事件、未知字段帧和终止用量事件保持原样进入公共流处理链路。

Responses WebSocket 是 Grok/OpenAI 的原生传输能力，不由兼容 Responses 开关扩展到其它平台。图片和视频继续使用独立媒体资格与分组策略，不受文本协议集合直接控制。

<a id="grok_account_contract"></a>
## 账号配置

管理员可在控制台选择 OAuth 或 API Key 创建账号。OAuth 账号可通过浏览器授权、refresh token 或 SSO cookie 创建和重新授权；创建 Grok 分组并绑定账号后，用户即可生成分组 API Key。OAuth state 和 PKCE 会话优先保存在 Redis，并通过一次性消费标记阻止多实例重复兑换；Redis 写入失败时才使用进程内短期回退。SSO cookie、邮箱密码等临时输入只能用于兑换 Build OAuth token，不能写入账号凭据、响应或日志。

邮箱密码授权由进程配置 `gateway.grok.password_auth_enabled` 控制，默认关闭且管理端不展示入口。即使显式开启，服务也只接受密码到 SSO、再到 OAuth token 的临时转换。成功重新授权会清除 Grok 的软性消费上限重新授权标记，并以凭据快照/CAS 规则更新账号，避免旧请求覆盖新 token。

账号未保存显式 base URL 时，数据库运行时设置 `grok_default_base_url_mode` 决定文本请求使用 CLI 代理、公共 API 或三个区域 API；账号显式端点始终优先。`grok_default_text_model` 作为需要默认文本模型的请求及 Claude Messages 映射的目标；`grok_cross_client_model_map_enabled` 开启后，Grok 分组的 Anthropic Messages 派发会将 Claude 模型 ID 映射到该目标，不影响 Responses 或 Chat Completions 中的其他模型。三项设置热更新运行时映射快照，不能把媒体模型继承为文本价格或文本默认模型。

其它通用账号类型即使可由兼容导入层保存，也没有 Grok 正式凭据和转发契约；`cosy` 明确只属于 Qoder。完整分类见[上游账号能力矩阵](upstream_account_matrix.md)。

Grok 使用 OpenAI 兼容能力适配层，但高级调度器由分组而不是平台全局开关决定。最终目标 Group 的 `scheduler_type=advanced` 时，Grok 在既有模型、媒体、账号状态、配额和 transport 硬过滤后复用通用 Top-K 评分；`basic` 保持原有选择顺序。Grok 的请求/令牌额度快照、媒体付费资格和 HTTP bridge 仍是平台专属资格，不能因通用评分缺少这些可选信号而被错误排除。

OAuth 访问令牌 JWT 的数字或字符串 `tier` 是账号档位的首选信号；刷新后新 JWT 有声明时覆盖旧凭据，没有声明时才保留已有值。账单月限额可进一步确认已知付费档位。供应商含糊的 `SuperGrokPro` 只有在 24 小时内观察到 `grok-4.5` Responses 的 Heavy 请求/令牌窗口时才显示为 Heavy，其他模型的新快照会延续但不会刷新该信号；过期或来自其它模型的窗口不能升级档位。明确的 Free、SuperGrok Lite、SuperGrok Plus 和 Heavy 信号不走这条推断。管理端账号徽章和用量条都使用同一规范档位，并在额度快照变化后刷新。

## 媒体请求格式

JSON 图片编辑和视频生成请求可在 `image`、`images`、`reference_images` 与 `mask` 对象中提供参考图片。与 xAI 直接兼容的请求应使用 `url` 字段；历史 `image_url` 字段仍可使用，TokenRouter 会在转发前把它规范化为 `url`。如果两者同时存在，则保留非空的 `url`；空白 `url` 会回退使用 `image_url`。multipart 图片编辑中的上传文件也会转换为 `url` 形式的 data URL。

## 媒体账号资格

新的 Grok 图片或视频生成请求会执行媒体专用的账号资格检查。API Key 账号保持可用；OAuth 账号必须由 xAI 计费探测提供明确的付费资格证据。Free、禁止访问、缺少观测、观测格式错误或结论不明确的 OAuth 账号都不会承接新的媒体生成请求。尚无观测的 OAuth 账号会在第一次转发媒体请求前执行探测，导入账号时也会主动先执行计费探测。聊天请求和已有视频任务的状态查询不受这项隔离影响。当分组中没有合格账号时，媒体端点返回 HTTP `503`，错误类型为 `grok_media_no_eligible_account`。

管理员可通过账号创建或更新 API 的 `extra.grok_media_eligible` 覆盖自动判定：`false` 表示排除，`true` 表示强制允许；更新时传 `null` 可删除覆盖并恢复基于探测结果的自动判定，省略该字段则保留现有覆盖。仅出现每周用量周期不能作为付费层级证据。图片接口返回成功时必须包含至少一个实际图片输出；空的 HTTP `200` 响应会触发账号 failover，不会作为成功生成结果计数或返回。

Grok 兼容账号对所选端点返回 HTTP `405` 时，表示该账号不支持当前端点；在尚未向客户端输出内容时请求会切换到其它账号，非池模式账号同时临时排除 30 分钟，避免粘性会话反复命中。公共池账号继续跳过默认账号冷却，`405` 也不会被误记为模型级冷却。

## 搜索与语音

`POST /v1/web_search` 和 `POST /v1/x_search` 只允许 Grok 分组，接收查询或 `input` 和最多 20 条结果。两者在选择账号前进入共同内容审计，随后复用常规 Grok 账号资格、并发等待和最多四次账号尝试。`web_search` 使用原生 Responses `web_search` 工具；`x_search` 强制使用 `x_search`，并接受 `allowed_x_handles`、`excluded_x_handles`、`from_date`、`to_date` 及图片/视频理解开关。两者只返回实际工具来源 URL 对应的结果，分别以 `grok-web-search` 和 `grok-x-search` 记录计费模型。每次调用使用独立服务端 request ID 作为结算幂等键；不得用查询、IP 或 User-Agent 哈希合并相同搜索。Responses/Chat 路径会保留原生 `x_search` 工具字段，并从上游 usage 或工具事件恢复 `SearchCount` 作为 token 费用之外的附加费。

Voice HTTP 入口包括 TTS、STT 和自定义 Voice 的创建、读取、修改、删除与音频下载；`GET /v1/realtime` 代理 xAI Voice WebSocket。所有入口只允许 Grok 分组，并在整个会话持有并发槽。TTS 按字符、STT 按音频时长生成 `AudioUsage`；Realtime 必须先在任一中继方向观察到包含非空音频负载的事件，才按连接会话时长生成用量，握手失败、纯文本或只有转录事件的会话不结算。正常或常见断开仍要结算此前已确认的音频会话。Voice 和搜索分别使用分组显式价格，`NULL` 回退代码默认价，显式 `0` 表示免费，且都使用基础分组倍率，不混入文本 token 价格。

## 任务归属与结算

新视频请求成功后会从上游响应的 `request_id`、`id` 或 `task_id`（包括 `data.*`、`video.*` 嵌套形态）提取任务标识，且保留既有 `request_id` / `id` 优先级；服务按规范化后的任务标识 + `user_id + api_key_id` 保存所选分组和账号绑定。后续状态与 content 下载必须回到创建任务的账号，不能重新随机调度；复合 Key 的映射后来被删除时，服务仍可从持久/缓存绑定构造只用于旧任务查询的最小 Grok 分组视图。查询已有任务不要求账号仍具备“新媒体生成”资格，但仍校验 Key、用户和任务归属。

视频 content 先确认任务状态，再使用服务端上游凭据代理下载，并安全透传 Range/内容头；上游 URL 和 bearer token 不返回客户端。异步视频创建成功时只保存模型、计费模型、分辨率、时长和创建时间快照，不立即扣费；状态查询或 content 下载首次观察到官方 `status=done` 且存在 `video.url` 时才尝试结算。模型和时长优先采用完成响应，分辨率采用创建快照，缺失时分别使用官方默认族、8 秒和 480p。多实例通过 Redis `SET NX` 领取一次性结算权，持久结算失败会释放领取供后续轮询重试，并以任务 ID 派生稳定 request ID 防止重复扣费。普通查询/下载不生成第二笔费用。

模型重定向、渠道映射和响应模型恢复遵守共同模型链，媒体专用路由模型只用于能力选择，不能覆盖用户账单中的 requested/upstream model。视频单价优先使用 `video_model_prices` 的模型族和分辨率覆盖，其次使用旧 `video_price_*` 列，最后使用内置每秒默认价。

OAuth 凭据失效、账号资格变化和上游限流使用带凭据快照的分类与 CAS 更新，避免旧请求把刚刷新的账号再次封禁。内容策略 403 与凭据 401/403、付费资格拒绝和可切换上游错误要分别处理；只有可切换且响应未开始的错误进入下一账号。实际 Grok 非流 Chat 响应必须包含至少一个为正的聚合输入、输出、缓存写入或缓存读取 token 桶；缺失、全零或只有图片/文本明细的成功响应会在 HTTP 200 提交前返回稳定的 `grok_missing_usage` 故障转移错误。识别同时依据 Grok 平台账号、最终计费模型、映射后上游模型和响应模型，通用 OpenAI 兼容账号不能绕过，客户端 Grok 命名别名映射到非 Grok 上游时也不会误拒。

## 客户端配置

用户可在 API Key 页面通过“使用密钥”生成 Grok Build CLI、Codex CLI 或 OpenCode 配置。现有 `config.toml` 应先备份，再合并新模型配置。Codex 配置使用环境变量保存 TokenRouter Key，显式设置 `requires_openai_auth=false`，并以 HTTP/SSE Responses 模式关闭 WebSocket；不能要求用户再登录 ChatGPT，也不能把密钥写进仓库。

Grok Build CLI 的模型配置必须指向 TokenRouter 对外地址（以 `/v1` 结尾），不能直接使用 `api.x.ai` 或内部 OAuth 代理地址。OAuth 流量默认转发到 Grok CLI 订阅代理。

## 默认模型目录

- `grok-4.6`
- `grok-4.5`
- `grok-4.3`
- `grok-build-0.1`
- `grok-composer-2.5-fast`
- `grok-4.20-0309-reasoning`
- `grok-4.20-0309-non-reasoning`
- `grok-4.20-multi-agent-0309`
- `grok-imagine`
- `grok-imagine-image`
- `grok-imagine-image-quality`
- `grok-imagine-edit`
- `grok-imagine-video`
- `grok-imagine-video-1.5`

`grok`、`grok-latest` 和 `grok-4.5-latest` 归一化为 `grok-4.5`，`grok-4.6-latest` 归一化为 `grok-4.6`，其它内置别名由 `internal/pkg/xai/models.go` 维护。模型列表默认展示当前目录，并结合账号模型映射/范围和 API Key 别名；未知模型保持透传，以支持管理员配置的 xAI 兼容上游。未知 Grok 文本族在没有显式定价时按 `grok-4.5` 回退，图片、视频、Voice 和搜索等非文本族不会误用该价格。

## 环境变量

- `XAI_OAUTH_CLIENT_ID`
- `XAI_OAUTH_SCOPE`
- `XAI_OAUTH_REDIRECT_URI`
- `XAI_OAUTH_AUTHORIZE_URL`
- `XAI_OAUTH_TOKEN_URL`
- `XAI_BASE_URL`
- `XAI_GROK_CLI_VERSION`：覆盖 Grok CLI 客户端版本；内置版本与最低允许版本均为 `0.2.114`，覆盖值必须是规范 SemVer 且不得低于该版本

进程配置 `gateway.grok` 还包含 Free OAuth 账号的本地滚动窗口软门禁：默认 24 小时、500000 token、95% 停调阈值和 60 秒统计缓存。只有明确标记为 Free 的账号参与；未知或付费层级以及数据库/统计失败均 fail-open。管理端主动额度查询和导入探测不经过该软门禁。

自定义 base URL 和媒体/billing 子路径都必须通过同一 URL allowlist/SSRF 校验。环境变量中的 client secret、token 和上游 URL 不得进入前端配置或错误响应。

相关文档：[上游账号能力矩阵](upstream_account_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口边界](http_api.md)、[接口目录](index.md)。
