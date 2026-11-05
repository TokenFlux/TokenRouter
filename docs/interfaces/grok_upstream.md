# Grok / xAI 上游

TokenRouter 支持 Grok OAuth 订阅提供商和标准的 xAI API Key 提供商，通过 OpenAI 兼容的 Responses、Chat Completions、Messages 和 WebSocket 入口转发请求。Grok 提供商还支持图片生成和编辑、视频的生成、编辑和扩展、视频状态查询、原生搜索和 Voice API。

本文覆盖提供商凭据、聊天和媒体的转发、媒体资格、异步视频的归属、模型目录和运行时变量。xAI 的套餐价格不在本文范围内；上游当前返回的动态模型，也不全都是兼容承诺。

## 章节导航

- [基本信息](#基本信息)：修改路由或默认上游时读取。
- [客户端协议](#客户端协议)：修改 Responses、Chat 或 Messages 的准入时读取。
- [提供商配置](#提供商配置)：修改 OAuth、API Key 的导入和刷新时读取。
- [媒体请求格式](#媒体请求格式)：修改图片和视频的 body 转换时读取。
- [媒体提供商资格](#媒体提供商资格)：修改付费探测和调度隔离时读取。
- [搜索与语音](#搜索与语音)：修改搜索、TTS、STT、自定义 Voice 或 Realtime 时读取。
- [任务归属与结算](#任务归属与结算)：修改视频查询、下载或用量记录时读取。
- [凭据与健康](#凭据与健康)：修改凭据失效、额度观测和错误恢复时读取。
- [客户端配置](#客户端配置)：核对生成给客户端的 base URL。
- [模型与执行规则](#模型与执行规则)：修改候选、推理档位和默认请求型号时读取。
- [进程配置](#进程配置)：修改 OAuth 或上游运行参数时读取。

## 基本信息

- 平台名：`grok`
- 提供商类型：OAuth 订阅提供商、API Key 提供商
- 主要网关入口：`/v1/responses`、`/responses`、Chat Completions、Messages、Responses WebSocket、`/v1/web_search`、`/v1/x_search`、`/v1/tts`、`/v1/stt`、`/v1/custom-voices` 和 `/v1/realtime`；Voice 和搜索也有不带 `/v1` 的同名入口
- API Key 提供商默认的上游地址：`https://api.x.ai/v1`

Grok 的原生协议集合包括 HTTP Responses、Chat、Images、视频和 Voice；Responses WebSocket、Compact、网页和 X 搜索，是分组转换到 Responses 的入口，不会作为原生选项出现在提供商上。统一的配置字段和入口门禁见[统一协议能力](protocol_capabilities.md)。

## 客户端协议

分组可以为 Grok 提供商开放 Anthropic Messages、OpenAI Responses 和 Chat Completions，开放规则见[统一协议能力](protocol_capabilities.md#group_protocol_routes)。文本协议被禁用时，在提供商选择、计费、重试和 fallback 之前，返回对应协议的 `403`。

原生 Responses 在同一提供商内，保留一次密文和 Compact 的解码恢复；Chat 转 Responses 和图片辅助请求只交换一次。HTTP 提交、关闭重试窗口、语义输出和 TTFT 是四个独立的观测。执行结果可以和错误同时存在，Grok 的失败入口按自己的规则判断能否结算；只看到响应 ID、前导事件或部分输出，不足以执行"成功之后"才做的操作。

Grok 的 `event: ping` 按通用规则在上游一侧丢弃，下游连接由统一组件维护，详见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。JSON type 声明实际业务事件时保留该数据；未知字段、缓冲上限和不完整帧使用通用过滤器的保守放行规则。

Responses WebSocket 是 Grok 和 OpenAI 的原生传输能力，兼容 Responses 的开关不会把它扩展到其他平台。图片和视频由独立的媒体资格和分组策略控制，与文本协议集合无关。

<a id="grok_account_contract"></a>
## 提供商配置

供应商的 OAuth 和 SSO 交换、模型和额度的解析在 `upstream/grok`；提供商的授权、令牌读取和刷新，分别由 `provider.GrokAuthorization`、`GrokTokenSource` 和 `GrokTokenRefresher` 执行。提供商通过 `protocol/grok` 的报文和注入的接口调用供应商，不持有具体的客户端。app 直接构造一个授权实例，`provider/provider` 提供代理读取和密码授权开关，`provider/rediscache` 实现 Redis 会话存储。

管理员的 Grok 授权和 SSO 导入 HTTP 由 `provider/httpapi` 接入；导入队列、配额探测和模型观测共用提供商运行时。Responses、Chat 桥接、Composer 图片辅助请求、媒体和 Voice 由 `gateway/httpapi.GrokExecutor` 接入，响应解析复用 upstream 和共享的 `OpenAIResponseOutput`。app 绑定凭据、健康、HTTP 池、TLS 和同一个 WS 拨号器；Realtime 连接和视频内容流分别通过各自的接口释放。Chat 桥接不适用时，回到原生的 Chat 分支；提供商切换、入站编排和资金完成由请求的负责方处理。

管理员可以在控制台选择 OAuth 或 API Key 创建提供商。OAuth 提供商可以通过浏览器授权、refresh token 或 SSO cookie 创建和重新授权；把提供商绑定到分组后，用户就可以生成这个分组的 API Key。OAuth 的 state 和 PKCE 会话优先保存在 Redis，并用一次性消费标记防止并发请求重复兑换；Redis 写入失败时，才使用进程内的短期存储。SSO cookie、邮箱密码等临时输入，只用于兑换 Build OAuth token，不会写进提供商凭据、响应或日志。

邮箱密码授权由进程配置 `provider.grok.password_auth_enabled` 控制，默认关闭，管理端不显示入口。即使手动开启，服务也只把密码临时换成 SSO，再换成 OAuth token。重新授权成功后，清除 Grok 软性消费上限的重新授权标记，并按凭据快照和 CAS 规则更新提供商，旧请求无法覆盖新的 token。

`provider/provider` 统一处理文本和媒体的端点：手动配置的自定义地址保持不变；OAuth 的官方 CLI 主机，在媒体请求里改为官方的媒体 API。实际请求照常执行 URL 信任检查；这个选择不影响官方的授权和刷新端点。

提供商没有保存手动的 base URL 时，`grok_default_base_url_mode` 决定文本请求使用 CLI 代理、公共 API 还是区域 API；提供商手动配置的端点优先。`grok_default_text_model` 只用于允许省略模型的请求。网关不会把 `grok`、`grok-latest`、媒体简称，或者 Claude、GPT 的请求自动改成默认型号，需要改写时配置手动的模型映射。`grok_cross_client_model_map_enabled` 已经移除，管理设置提交这个字段时返回 400。

其他通用的提供商类型，即使兼容导入层可以保存，Grok 也没有为它们实现正式的凭据和转发；`cosy` 只属于 Qoder。完整的分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。

Grok 复用 OpenAI 兼容的能力适配层，使用哪种调度器由分组配置决定。最终目标分组的 `scheduler_type=advanced` 时，Grok 先通过模型、媒体、提供商状态、配额和 transport 的硬过滤，再使用通用的 Top-K 评分；`basic` 使用基础调度器的选择顺序。Grok 的请求和令牌额度快照、媒体付费资格和 HTTP bridge 是平台专属的资格，通用评分缺少这些可选信号时，提供商照常参与。

OAuth 访问令牌 JWT 里的数字或字符串 `tier`，是判断提供商档位的首选信号：刷新后新的 JWT 带有这个声明时覆盖旧凭据，没有时保留已有的值。账单月限额可以进一步确认已知的付费档位。供应商含糊的 `SuperGrokPro`，只有在 24 小时内观察到 `grok-4.5` Responses 的 Heavy 请求和令牌窗口时，才显示为 Heavy；其他模型的新快照会沿用这个结果，但不会刷新它；过期的，或者来自其他模型的窗口，不能把档位升级。明确的 Free、SuperGrok Lite、SuperGrok Plus 和 Heavy 信号不走这套推断。管理端的提供商徽章和用量条使用同一个规范化的档位，额度快照变化后会刷新。

## 媒体请求格式

图片请求按最终模型路线的输出模态校验，未知型号需要先配置 image 输出能力。Composer 的可调推理控制来自统一属性解析后的 `reasoning_options`，管理员覆盖会影响实际请求；空集合清理控制参数，未知集合保留客户端明确值。图片转文本适配和 logprobs、stop 等报文限制分别由平台执行规则处理。

媒体和 Voice 的 HTTP 由 app 直接绑定 `gateway/httpapi/mediaentry`；生成循环、视频的归属和认领只在 `gateway/media` 里实现，计量输入在提交完成任务之前固定下来。gateway/provider 只组合现有的 Grok Codec 和价格规范化接口，媒体资格复用提供商的规则，并在缺少观测时在原位置执行探测。

JSON 格式的图片编辑和视频生成请求，可以在 `image`、`images`、`reference_images` 和 `mask` 对象里提供参考图片。直接兼容 xAI 的请求使用 `url` 字段；旧的 `image_url` 字段仍然可用，TokenRouter 在转发前把它规范化为 `url`。两者同时存在时，保留非空的 `url`；`url` 为空白时，使用 `image_url`。multipart 图片编辑里上传的文件，也会转成 `url` 形式的 data URL。

创作台的 Grok 图片 `edit` 使用 xAI 官方的 JSON 接口 `POST /v1/images/edits`，不使用 OpenAI 风格的 multipart：单张图片的请求用 `image: {"type":"image_url","url":"data:image/png;base64,..."}`，多张图片用 `images` 数组，最多 3 张源图；请求保留 `model`、`prompt`、`resolution`、`aspect_ratio`，并设置 `response_format: "b64_json"`，响应从 `data[].b64_json` 解析成创作台的输出。`generate` 使用 `/v1/images/generations`。

## 媒体提供商资格

新的 Grok 图片或视频生成请求，会做媒体专用的提供商资格检查：

- API Key 提供商始终可用。
- OAuth 提供商需要 xAI 计费探测给出明确的付费资格证据。Free、禁止访问、没有观测、观测格式错误或结论不明确的 OAuth 提供商，都不承接新的媒体生成请求。
- 还没有观测的 OAuth 提供商，在第一次转发媒体请求之前执行探测；导入提供商时也会主动先做一次计费探测。
- 聊天请求和已有视频任务的状态查询不受这项检查影响。
- 分组里没有合格的提供商时，媒体端点返回 HTTP `503`，错误类型是 `grok_media_no_eligible_provider`。

管理员可以通过提供商创建或更新 API 的 `extra.grok_media_eligible` 覆盖自动判断：`false` 表示排除，`true` 表示强制允许；更新时传 `null` 删除覆盖、恢复按探测结果判断，省略这个字段时保留现有的覆盖。只出现每周用量周期，不能证明是付费档位。图片接口返回成功时，需要至少有一张实际的图片；空的 HTTP `200` 响应会触发提供商 failover，不会作为成功的生成结果计数或返回。

Grok 兼容提供商对所选端点返回 HTTP `405`，表示这个提供商不支持当前端点。还没向客户端输出内容时，请求换到其他提供商；非池模式的提供商同时临时排除 30 分钟，粘性会话因此不会反复命中它。公共池提供商仍然跳过默认的提供商冷却，`405` 也不会被记成模型级冷却。

## 搜索与语音

`POST /v1/web_search` 和 `POST /v1/x_search` 只允许 Grok 提供商处理，接收查询或 `input`，结果最多 20 条。两者在选择提供商之前先经过内容审计，然后复用常规的 Grok 提供商资格、并发等待，提供商最多尝试四次。`web_search` 使用原生 Responses 的 `web_search` 工具；`x_search` 强制使用 `x_search`，并接受 `allowed_x_handles`、`excluded_x_handles`、`from_date`、`to_date`，以及图片和视频理解的开关。

两者只返回实际工具来源 URL 对应的结果，计费模型分别记为 `grok-web-search` 和 `grok-x-search`。每次调用用一个独立的服务端 request ID 作为结算的幂等键；相同的查询、IP 或 User-Agent 不会被合并成一次搜索。Responses 和 Chat 路径保留原生的 `x_search` 工具字段，并从上游的 usage 或工具事件恢复 `SearchCount`，作为 token 费用之外的附加费。

Voice 的 HTTP 入口包括 TTS、STT，以及自定义 Voice 的创建、读取、修改、删除和音频下载；`GET /v1/realtime` 代理 xAI 的 Voice WebSocket。这些入口只允许 Grok 提供商处理，整个会话期间持有并发槽。TTS 按字符数、STT 按音频时长生成 `AudioUsage`。Realtime 要先在任一个中继方向观察到带非空音频数据的事件，才按连接的会话时长生成用量；握手失败、纯文本或只有转录事件的会话都不收费。正常或常见的断开，照样结算之前已经确认的音频会话。Voice 和搜索的价格来自价格配置，`NULL` 时使用代码里的默认价，`0` 表示免费；它们使用基础的分组倍率，和文本的 token 价格分开计算。

## 任务归属与结算

新视频请求成功后，从上游响应的 `request_id`、`id` 或 `task_id`（包括 `data.*`、`video.*` 的嵌套形式）里提取任务标识，`request_id` 和 `id` 的优先级保持不变。服务按"规范化的任务标识 + `user_id` + `api_key_id`"，保存所选的分组和提供商绑定。之后的状态查询和 content 下载，回到创建任务的那个提供商，不再重新调度。复合 Key 的映射后来被删除时，服务仍然可以从持久化或缓存的绑定，构造一个只用于查询旧任务的最小 Grok 分组视图。查询已有任务不要求提供商仍然具备"新媒体生成"的资格，但 Key、用户和任务归属照常校验。

视频 content 先确认任务状态，再用服务端的上游凭据代理下载，并安全地透传 Range 和内容相关的头；上游 URL 和 bearer token 不会返回给客户端。异步视频创建成功时，只保存模型、计费模型、分辨率、时长和创建时间的快照，不立即扣费；状态查询或 content 下载第一次观察到官方的 `status=done`、并且有 `video.url` 时，才尝试结算。模型和时长优先取完成响应里的值，分辨率取创建时的快照；时长缺失时先取创建快照，再用 8 秒。分辨率只在空值时默认 480p，自定义标签和完整的正数秒数会保留到 Usage Log 与计费，计费层不限制为 15 秒。模型缺失时使用官方默认的型号族。并发的查询和下载请求通过 Redis `SET NX` 领取一次性的结算权；持久化结算失败时释放领取，留给之后的轮询重试，并用任务 ID 派生的稳定 request ID 防止重复扣费。普通的查询和下载不会产生第二笔费用。

视频任务摘要按每次状态查询的上游状态更新，`done`、`failed`、`expired` 和取消状态都会结束摘要。该更新先于计费认领，零费用失败与已计费任务的再次查询同样会记录终态。请求记录规则见[请求 ID 与请求查询](../operations/request_lookup.md#request_storage)。

模型重定向、分组映射和响应模型恢复遵守共同的模型链；媒体专用的路由模型只用于能力选择，用户账单里的 requested 和 upstream model 不受它影响。视频单价按共享价格配置的价卡解析：分辨率档位优先，其次为显式默认价，缺价保持不可用。没有有效价卡时查询目录里同完整型号和同分辨率的每秒价。`video` 价卡按输出数量与时长相乘，按次价卡按输出数量计算；媒体使用普通的分组、用户和订阅倍率。`2K` 等标签在共享媒体解析中同时保留图片几何解释和视频计费解释，由对应入口使用。新视频任务提交前校验标签最多 50 个字符且不含控制字符。

## 凭据与健康

OAuth 凭据失效、提供商资格变化和上游限流，使用带凭据快照的分类和 CAS 更新，旧请求无法把刚刷新过的提供商再次封禁。凭据获取的分类结果和 reason 常量在 `gateway/forward`，分类只读取错误链和是否配置了代理；持久化用的比较快照是私有字段，不参与 JSON。提供商写入和请求重试由各自的用例执行。内容策略的 403、凭据的 401 和 403、付费资格拒绝、可以切换的上游错误，各自处理；只有可以切换、并且响应还没开始的错误，才进入下一个提供商。

实际的 Grok 非流式 Chat 响应，需要至少有一个为正的聚合 token 桶：输入、输出、缓存写入或缓存读取。缺失、全为零，或者只有图片和文本明细的成功响应，会在提交 HTTP 200 之前返回稳定的 `grok_missing_usage` 故障转移错误。判断时同时看 Grok 平台的提供商、最终的计费模型、映射后的上游模型和响应模型：通用的 OpenAI 兼容提供商绕不过这项检查，客户端用 Grok 命名、但映射到非 Grok 上游的别名也不会被误拒。

请求凭据统一由 `gateway/provider.RequestCredentials` 取得，文本、媒体、Voice 和 WS 共用一个实例。十五秒的换号预算由请求自己的 `requeststate.CredentialBudget` 持有，HTTP 适配层负责把失败的分类关联到 Ops。条件写入、重新读取确认和缓存清理由 `provider.GrokCredentialRecovery` 执行，并共用应用的运行时阻断状态；写入预算五秒，提交确认 250 毫秒，缓存清理 500 毫秒。Grok 转发和共享的 Responses 输出使用同一个凭据恢复实例。

额度观测和出错后的提供商状态，统一由 `provider/provider.GrokHealth` 处理。app 把同一个提供商存储、运行时阻断、模型冷却和快照节流器注入文本、媒体和 WS 的使用方，Grok 和 OpenAI 的普通快照共用同一个写入间隔。429、窗口用完和成功恢复，绕过普通的节流；持久化使用"只延长限流"和"比较已观察到的版本后再恢复"两种方式。团队冷却的模型由当前尝试明确传入，请求内容被拒绝时，在写入之前就结束处理。

## 客户端配置

用户可以在 API Key 页面通过"使用密钥"，生成 Grok Build CLI、Codex CLI 或 OpenCode 的配置。已有的 `config.toml` 先备份，再合并新的模型配置。Codex 的配置用环境变量保存 TokenRouter 的 Key，设置 `requires_openai_auth=false`，并以 HTTP/SSE 的 Responses 模式关闭 WebSocket；用户不需要再登录 ChatGPT，密钥也不要写进代码仓库。

Grok Build CLI 的模型配置指向 TokenRouter 对外的地址（以 `/v1` 结尾），不要直接使用 `api.x.ai` 或内部的 OAuth 代理地址。OAuth 流量默认转发到 Grok CLI 的订阅代理。

## 模型与执行规则

模型名称和候选来自统一目录。白名单为空时允许任意 ID，未知型号由上游处理。Chat 到 Responses 的适配资格、推理档位、媒体请求默认值和套餐额度观测由 Grok 执行规则决定。

文本和媒体入口使用手动映射后的完整模型 ID。推理属性由[统一解析器](model_catalog_and_marketplace.md#model_attributes)读取 xAI 服务的目录条目并叠加管理员覆盖。Grok 4.5/4.6/4.7 的 xhigh 和 4.3 的 none 分别来自各自条目。Build 0.1 与 4.20 reasoning 会推理但没有可调档位，模型列表也展示空集合。Composer 的参数清理和 4.20 系列的 logprobs 限制属于平台执行规则。 Messages 和 WebSocket 在最终模型确定后应用同一属性结果，再生成缓存身份。已知没有可调档位的型号会移除 effort；目录缺项的型号保留客户端传值。

`grok-latest`、`grok-build-latest` 与未登记的供应商别名按完整 ID 处理。缺少目录记录时保留明确的请求参数，准入继续检查白名单和协议资格。健康状态、缓存和价格使用各自已经确定的完整模型身份。

未知的 Grok 文本型号没有手动定价时保持缺价，不会借用 `grok-4.6` 或其他型号的价格。图片和视频按完整的型号、尺寸和分辨率查询独立的目录报价，Voice 和搜索读取手动配置的操作价格。

## 进程配置

Grok 覆盖值位于 `provider.grok`：OAuth 字段在 `oauth.authorize_url`、`token_url`、`client_id`、`scope`、`redirect_uri`，API 地址使用 `base_url`，客户端版本使用 `cli_version`。`allow_unsafe_url_overrides` 和 `unsafe_allow_concurrency_above_one` 是独立的显式放宽开关；密码登录验证码密钥使用 `yescaptcha_client_key`。

这些值在 app 构造依赖前发布为进程配置，不再读取 `XAI_*` 或 `YESCAPTCHA_*` 隐式覆盖。v1 升级会导入旧值。CLI 覆盖仍要求不带 `v`、不含 build metadata 的规范 SemVer；代理传输继续执行其最低版本检查。

`scheduling.grok_free_quota` 控制 Free OAuth 提供商的本地滚动窗口软门禁：默认 24 小时、500000 token、95% 时停调、统计缓存 60 秒。只有明确标记为 Free 的提供商参与；未知或付费的档位，以及数据库或统计查询失败时，都放行（fail-open）。管理端主动的额度查询和导入时的探测，不经过这个软门禁。门禁判断和缓存由 `provider.FreeQuotaGate` 负责，app 传入配置，绑定 usage 批量统计和后台任务屏障；两条普通选择链各自共用自己的缓存，高级调度器按实例使用独立的缓存，第一次缓存未命中时照常放行，并在后台刷新。

自定义的 base URL，以及媒体和 billing 的子路径，都要通过同一个 URL allowlist 和 SSRF 校验。配置中的 client secret、token 和上游 URL，不会进入前端配置或错误响应。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)、[接口目录](index.md)。
