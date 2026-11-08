# OpenAI 上游

本文描述 OpenAI 的 OAuth 和 API Key 提供商，以及 Responses、Chat、Messages、Embeddings、Images、Realtime 和 Codex 兼容能力的现状。上游的完整模型列表会变化，本文不列出；OpenAI 格式的入口，也不代表任何平台都能处理。

## 章节导航

- [提供商与凭据](#提供商与凭据)：修改 OAuth、API Key、隐私或客户端限制时读取。
- [协议与传输](#协议与传输)：修改 Responses、WebSocket、Realtime 或兼容转换时读取。
- [远程压缩协议](#远程压缩协议)：区分原生的 `remote_compaction_v2` 和旧版 `/responses/compact` 时读取。
- [Responses 请求细节](#responses-请求细节)：修改路由提示、回合状态、工具改写、Lite 或计数时读取。
- [模型与能力](#模型与能力)：修改模型别名、endpoint capability 或推理参数时读取。
- [额度与调度](#额度与调度)：修改窗口配额、评分、粘性或自动暂停时读取。
- [失败与诊断](#失败与诊断)：修改错误分类、刷新、CAS 状态或 failover 时读取。

## 提供商与凭据

OpenAI 正式支持 `oauth` 和 `apikey` 两种提供商。OAuth 提供商保存 access token 和 refresh token、提供商和组织的上下文，以及 Codex 能力的元数据，后台和请求路径都会触发刷新；API Key 提供商保存 key、base URL、工作负载能力、文本协议路由和管理员的压缩开关。其他通用的导入类型，OpenAI 没有为它们实现转发，详见[上游提供商能力矩阵](upstream_provider_matrix.md)。

### 授权、刷新和凭据选择

管理员的 OAuth 和 PAT 导入、提供商刷新和额度操作，由 provider 的用例执行，HTTP 在 `provider/httpapi`，app 绑定同一个实例。额度查询的供应商请求由 `upstream/openai` 执行，窗口和返回报文使用 `protocol/openai` 的值类型。额度消费完成后的恢复阶段有单独的八秒预算：客户端断开不会取消这一步，应用停止时会取消并等待；它在底层额度服务之前停止。

OAuth 的授权会话、刷新结果的补全和凭据组装，由 `provider.OpenAIAuthorization` 持有，app 直接构造一个实例。`provider/provider` 把代理、TLS Router 和 Profile、动态的 Codex UA 和隐私查询整理成供应商调用的参数，在调用时读取；它不再保存另一份会话或刷新状态。token source 和 refresher 复用已有的缓存和协调器。共享的 OAuth token 和刷新锁的 Redis 适配层在 `provider/rediscache`，由 app 构造一个实例，Redis 的键和 TTL 保持兼容。

Agent Identity 的任务锁、锁内复查和凭据登记由 provider 协调；app 构造一个协调器，供请求执行、额度查询、用量查询和提供商探测共用。gateway/provider 里单独的身份适配负责请求签名、恢复和脱敏，它不持有 Gin、完整配置，也没有自己的注册状态；供应商交换和加解密只在 `upstream/openai` 实现。

推理凭据统一由 `provider.OpenAIExecutionCredentials` 选择，app 绑定母提供商的读取、OpenAI 和 Grok 的 token 来源。影子提供商在需要凭据时读取母提供商，普通提供商不增加查询；Agent Identity、setup-token，以及缺少 token 来源时使用存量凭据的回退，各自独立处理。setup-token 不参与刷新；OpenAI 和 Messages 对其他平台 setup-token 的拒绝方式不同。token 来源的运行阻断回调，由 app 在开放请求之前绑定。

OAuth 补全提供商元数据时，ID token 里个人的 `chatgpt_plan_type` 是个人套餐的权威来源。`accounts/check` 可能按 access token 的 `poid` 命中另一个 workspace：只有这条记录的账户 ID 和个人的 `chatgpt_account_id` 一致时，它的 `entitlement.expires_at` 才能和个人套餐组合。账户不一致时，到期时间改从个人的 `/backend-api/subscriptions` 的 `active_until` 读取；如果套餐本身来自 `accounts/check`，套餐和到期时间都取自同一条记录。

### 客户端限制和 Codex 身份

OAuth 提供商可以受 Codex CLI-only、客户端白名单、agent identity、privacy status 和 OAuth passthrough 策略的限制。OAuth 出站的 `originator` 需要和最终 User-Agent 的第一段配对；客户端没有提供可以识别的官方身份，或者身份修复失败时，统一使用 `codex-tui`。PAT、模型和额度探测、Alpha Search、HTTP 和 WebSocket 都使用同一个默认身份。

客户端或 TLS 路由明确提供、并且能够配对的官方身份，原样保留；历史上的 `codex_cli_rs` 只作为兼容识别的值。API Key 提供商不使用 OAuth 专用的内部端点和身份元数据。Header override、代理、base URL 和 TLS 配置受出站安全规则约束，无法覆盖受保护的认证头，也无法绕过目标校验。

OpenAI OAuth 提供商的 `extra.codex_fingerprint_mode` 控制 Codex Responses 设备指纹的统一程度。没有配置、空值或无效值都按 `off` 处理，只有 `device`、`session`、`full` 是手动开启的选项：

- `device`：只统一 installation ID。
- `session`：再统一 session ID，并按客户端原始的 session 稳定派生 thread ID。
- `full`：再把所有客户端统一到同一个 thread。

session 和 full 模式下，turn ID 每个请求重新生成，但同一个请求的 HTTP 头、`client_metadata` 和内嵌的 turn metadata 使用同一组 ID；HTTP 内部重试也不会重新派生。普通转换和 OAuth passthrough 都遵守这个配置；透传的大 body 只局部改写 `client_metadata`，不做整包解码。旧版 `/responses/compact` 不受这个设置影响，按它自己的协议转发。

管理员配置的 OpenAI device ID，优先于根据提供商 ID 派生的值。Spark 影子提供商继承父提供商的模式、device ID 和稳定种子，同一个 OAuth 凭据的上游设备身份不会因为影子 ID 而分裂。

Codex 身份的命名空间和指纹配置的组合，由 `provider/provider` 负责，签名、ID 派生和报文改写复用 `upstream/openai`。`gateway/httpapi` 分别发布本次 attempt 的身份来源和指纹 ID，使用 Gin 的同步读写；ID 为 nil 时也会覆盖旧值。身份来源保存当前记录的引用，命名空间在实际生成 Header 和 metadata 时读取；影子继承和跨提供商旧指纹的拒绝照常生效。

### 会话头

OpenAI 兼容请求的手动粘性会话头，按以下顺序读取：`session-id`、`session_id`、`conversation_id`、OpenCode 的会话头、CodeBuddy 的会话头。`session-id` 是 Codex 客户端使用的连字符写法，优先于旧的下划线写法。WebSocket 的会话日志使用相同的优先级，没有手动的会话头时，才回退到 `prompt_cache_key`，重连时不会因为头名不同而漂移到其他提供商。

会话头和哈希的请求绑定由 `gateway/httpapi` 提供，内容种子和摘要格式由 `gateway/session` 负责。Messages 转 OpenAI 的摘要绑定，使用 app 构造的 `AnthropicPromptCache`（只有一个），按提供商和 Key 区分命名空间，取最长的有效前缀，按 TTL 过期，替换时先写新链再删除旧链；它没有后台清理，也不持久化。执行适配只传递标识和摘要值。

<a id="openai_protocol_dispatch"></a>
## 协议与传输

OpenAI 平台正式支持以下协议族：

| 协议 | 处理方式 |
| --- | --- |
| Responses HTTP/SSE | OAuth 和 API Key 原生转发；支持允许的 `/responses/*` 子路径 |
| Responses WebSocket | 按提供商的 transport capability 选择 WS 或兼容传输；连接建立后，流式输出开始就不能换提供商 |
| Chat Completions | API Key 默认按 Chat 协议原生转发，明确强制时才转换到 Responses；每次 attempt 重建协议状态 |
| Anthropic Messages | 转换成 OpenAI 请求，再把事件、工具、thinking 和 usage 转回 Anthropic 格式 |
| Embeddings | 只支持 OpenAI 提供商，提供商的工作负载能力需要包含 `embeddings` |
| Images | OpenAI 的图片生成和编辑；网关使用同步的生命周期，批量图片见 Gemini 和 Vertex 的文档 |
| Realtime、Live、sideband、Alpha Search | 只支持 OpenAI 提供商，并受分组开关、提供商类型和 transport capability 限制 |

### 执行器装配

app 分别装配文本、Responses、WS、Images 和辅助执行器，它们复用同一套请求构造、输出、凭据和连接资源。

- 文本：兼容文本的 Messages、Chat、Raw Chat、原生 Anthropic 和 passthrough，由 `gateway/httpapi.OpenAITextExecutor` 接入；请求构造、Header、TLS 和客户端策略使用同一个 `OpenAIRequests`。标准 Responses、passthrough、Chat 和 Messages 的转换，以及 Raw Chat 的读取，使用原生实现，通过同步的 OutputSink 输出。
- 输出：`gateway/httpapi.OpenAIResponseOutput` 绑定响应读取、Header、错误规则、健康观测、超时和诊断；app 注入静态参数，TTFT 设置在调用时查询。响应结果直接使用上游读取器的值类型；"只有观测、没有成功"的失败，在不同入口上的返回方式各不相同。
- 尝试：Responses、Chat、Messages 的入站 HTTP 和单次尝试的运行时由 app 直接装配；`gateway/httpapi/openaiattempt` 复用同一套选择、反馈、完成和槽位能力。重试循环只在 `gateway/text` 里；跨模式切换时，reasoning 的清理结果从原始报文重新派生，后续请求不受影响。WS 的入站、每轮的提供商目标和完成 hooks 由 `gateway/ws/httpapi` 绑定，Forward 和 WS 的结果整理在 gateway/provider；终态、恢复报文、响应的 turn-state 和每轮的计费时刻都保留。
- 媒体和辅助：媒体和辅助入口由 mediaentry 直接绑定，使用同一套失败输出、槽位和完成快照；图片的 mandatory 策略、搜索和音频的提交策略各自保持。Embeddings、AlphaSearch、Messages 的 count_tokens 和 Responses 的 input_tokens，由 `gateway/httpapi.OpenAIAuxiliary` 直接接入；请求构造、健康和输出复用已有实例，模型整理和计数请求的准备在 gateway/provider。计数路由直接组合 RoutePlanner、选择器和受控的提供商目标。这些单次执行负责网络调用和响应资源，提供商选择、健康写入和全局重试由入站适配负责。Alpha Search 在错误处理时回卷了响应体，仍会关闭最初取得的上游 Body。计数查询区分原生的完整 JSON 和 Anthropic 兼容的响应，计数结果不作为推理结算的依据。
- Responses：主请求由 `gateway/httpapi.OpenAIResponsesExecutor` 负责准备、模型和工具转换、HTTP 交换和协议分派。图片桥接依次使用分组的协议设置、提供商的覆盖和全局默认值。分组「协议控制」里的 Responses 图片策略，控制非 Responses Lite 的 Codex 请求是否自动补上 `image_generation` 工具和引导指令；关闭自动注入时，客户端自己声明的生图工具保留，独立的图片接口也不受影响。HTTP 和 WS 使用同一个 OpenAIEncryptedLineage 和会话存储；失效密文的摘要只在上游明确拒绝后才记录，之后的请求按原会话键剥离。WS 资源由 `upstream/openai/ws.OpenAIWSConnections` 持有，关闭后无法重新创建连接池。
- WS 和 Live：供应商的 OAuth、PAT 和隐私交换、规范的 Codex 身份、请求指纹、Header 组合和 WS 客户端在 `upstream/openai`；Responses WS 和 Live attestation 是平台内的技术子包。WS 池持有连接、预热、队列和租约状态，构造时不启动 worker，入站的持有者在第一次使用时启用。Live 的创建、sideband、DeviceCheck 密文和观察者适配由 `gateway/httpapi.OpenAILiveExecutor` 组合，app 负责 JWT 密钥和关闭登记。长连接每轮的模型资格复核由共享的选择器负责；Live 的用量费用为零。WS 的池化、透传、HTTP 桥接和逐轮帧适配由 `gateway/ws/httpapi.OpenAIWebSocketExecutor` 执行，循环只在 `gateway/ws` 里。`OpenAIWSConnections` 统一管理按需的连接池、拨号器和停止屏障；提供商授权、用量和探测直接调用同一个连接失效接口。
- 首输出暂存器持有当前尝试的内存和临时文件；工具参数、usage、终态重建和图片产出计数只在 protocol 里实现。

透传只决定报文和传输的处理方式。手动配置的提供商模型映射照常执行一次，普通请求和透传请求的最终模型范围相同；OAuth 认证的硬能力限制，不会因为透传或全模型通配符而关闭。HTTP 出站只局部替换 `model`，同时保留原请求模型和最终上游模型，用于响应回填、日志和计费。

### 报文和协议转换

`protocol/openai` 负责 Responses 和 Chat 的报文、自定义编解码、服务层级的值，以及宽容 JSON 的字节修复；`protocol/bridge` 负责跨协议转换和每条流的状态。时刻和随机源由调用方传入。纯粹的 `BodyLimitError` 在 `gateway/httpapi` 转成 `http.MaxBytesError`，请求的读取和解压由 `server/httpx` 执行；宽容修复只在声明使用它的入口上生效。

Compact 的请求白名单、reasoning replay 和 store=false 的修复由请求 codec 执行，是否触发由入站决定。Responses 的 Header 和 CC 请求的发送也使用原生实现，提供商身份、代理和 TLS、请求状态通过小接口传入，覆写顺序不变。UA 和 originator 字符串的识别在 `gateway/clientmeta`；规范的 Codex 出站身份和动态 UA resolver 只在原生包里，请求字段何时改写由 `gateway/httpapi` 决定。

Responses 的标准和透传读取、Responses 转 Chat 或 Messages、Raw Chat 直通、Chat 转 Responses 或 Messages 的响应执行，都在 `upstream/openai`；协议算法只在 `protocol/bridge`。缓冲的终态、空响应检测和流状态按每次尝试创建。转换路径合并稀疏用量后，向计费和客户端转换提供同一快照；嵌套终态字段优先，明确的零和缺失字段分别处理，详见[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)。执行适配传入提供商策略和观察接口；HTTP 在输出时才取得可修改的 Header，等待心跳不会因为创建了适配器而提前结束；统一的下游时钟和上游心跳消费见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。

HTTP Responses 的标准流和透传流兼容第三方的生命周期事件：

- `event` 行声明 created、in_progress 或终态，data 却是带 `object=response` 和 ID 的裸 Response 时，补上 `type`、`response` 包装和缺失的事件序号。
- 旧的 `response.done` 按实际的 status 转成 completed、failed 或 incomplete 等终态；没有 status 时，只根据非空的 error 或 incomplete_details 补上失败或未完成状态，没有任何状态证据时，保留原事件。
- event 行和 JSON 的 type 保持同步；前面独立的 error 是否被抑制，在规范化之后重新判断，失败终态不会被吞掉。
- 同一个 Response ID 重复的成功终态只下发一次，后面的用量仍参与解析。
- 未知字段、模型回填和工具内容保留；真正缺少终态的断流不会被补成成功。

这些兼容处理只作用于 HTTP Responses，WebSocket 和其他客户端协议的事件处理不变。

OpenAI 兼容非流式响应的 usage，按 `usage`、`response.usage`、`data.usage`、`data.response.usage` 的顺序解析；前两种原生路径，优先于 Cline 等兼容上游使用的 `data` envelope。同一层的 hosted image usage 从对应的路径读取，不同 envelope 的 token 和图片用量不会混在一起。

`/backend-api/codex` 和不带 `/v1` 的别名，服务特定的客户端，同样经过 TokenRouter Key 鉴权、分组准入、调度和结算。Responses WebSocket 不支持 Qoder；其他平台能否进入 OpenAI 兼容处理器，由路由和平台文档共同决定，URL 本身说明不了。

工具和命名空间的请求改写只在 `protocol/bridge` 执行；`gateway/provider` 根据提供商、传输和 Compact 端点决定是否启用。HTTP 适配层持有 `requeststate.ResponseTools`，分别保存当前尝试的 OpenAI 和 Grok 映射、namespace 和 Codex 名称；WS 的会话更新和当前 turn 的名称分开保存，HTTP bridge 下一轮的声明保持原字节副本。非流式、SSE 和 WS 使用同一条恢复路径，未知字段、工具 ID、大数和恢复顺序都保持不变。Codex 工具修正和统计使用一个原生的修正器，usage 和终态的解析直接调用 `protocol/openai`。

推理历史的读取、请求回填和响应缓存，由 `gateway/session.ReasoningHistory` 使用同一个可选的缓存能力完成：保留客户端的 reasoning item ID，TTL 七天，单独的操作预算两秒，读取失败时放行，写入失败时只记日志。这份数据在缓存过期或丢失后无法恢复。

<a id="images_url_backfill"></a>
### 图片结果回填与模式转换

图片 API Key 和 OAuth 的单次发送和响应释放由 ImagesExecutor 执行，网关的入站编排决定提供商恢复、失败重试和完成处理。图片的实际产出、HTTP 提交和失败分别记录。API Key 的原生图片结果可以双向转换，OAuth 继续使用 Responses 图片转换。

图片入口由 `gateway/httpapi.OpenAIImagesExecutor` 组合请求和输出能力，app 把同一个实例直接绑定到媒体运行时。API Key 和 OAuth 两个分支各有协议转换、实际产出计数和失败资格。图片请求在原位置和客户端的取消脱钩，读取完成后才交付已观测的用量；JSON 心跳不算实际的图片输出。结构化的"图片工具不可用"事件，由 `provider/provider.ImageToolCooldown` 写入模型级冷却；模型改用文字回复时，不触发这项写入。

`stream=true` 的图片请求，在每次实际发送上游 HTTP 请求前启动 SSE 注释心跳，覆盖等待响应头的阶段。API Key 和 OAuth 使用同一个 `gateway.images.stream_keepalive_interval_seconds`，默认 10 秒，设为 `0` 关闭。首个周期到达前可以直接返回原有 HTTP 错误；上游返回 SSE 时，取得响应头后交给 SSE 读取器接续保活。上游返回普通 JSON 或 HTTP 错误时，等待心跳持续到有大小限制的读体结束，再停止并交给输出逻辑。心跳字节和已提交的响应格式跨尝试保留，仅有注释时仍按现有规则决定是否重试。

OpenAI API Key 的生成和编辑分别读取 `credentials.images_generations_mode`、`credentials.images_edits_mode`。`both` 跟随客户端；`non_streaming` 从 JSON 或 multipart 中移除 `stream` 和 `partial_images`；`streaming` 设置 `stream=true`，客户端要求非流式时另设 `partial_images=0`。每次提供商尝试都从原始请求构造出站载荷，上传图片、mask、其他字段和客户端模式独立保留。未配置的旧提供商按 `both` 处理。JSON 顶层重复键先收拢为唯一键，再应用模型和模式改写；关闭流式时全部移除 `stream`、`partial_images`，强制流式时写入唯一的目标值。嵌套字段和大整数保留其 JSON 值。

成功响应按实际 Content-Type 选择读取器。所有原生 JSON 结果共用完整文档、非空 `data[]` 和图片字段校验；HTTP 200 错误体、空结果和损坏结果不会按请求张数补成成功。JSON 转 SSE 先验证完整文档与全部 `data[]`，再逐项输出 `image_generation.completed` 或 `image_edit.completed`，最后发送 `data: [DONE]`。每项包含非空字符串 `b64_json` 或 `url`。原生 API Key 的 URL 项经过安全下载补上 `b64_json`，总 usage 只随最后一张图发送。这项流式格式转换不依赖可选的 `images_url_to_b64_json` 开关；下载等待期间继续发送 SSE 注释心跳。中间预览无法从非流式结果重建。

SSE 转 JSON 收集、去重完成图片，输出 `created`、`data[]` 和 usage。非流式汇总跳过预览图片字段校验和下载，同时继续校验 SSE 帧与 JSON 结构；错误事件、读体失败、帧中断、字节超限和完成图片少于请求张数都会结束尝试。客户端要求 `response_format=url` 而结果只有 Base64 时，沿用 data URL 格式。原生 SSE 的逐帧转发与聚合共用读取器；事件多行 data 会合并，声明 SSE 却返回完整 Images JSON 的兼容上游也会转换成客户端要求的格式。

图片数量、尺寸和 usage 从上游原始结果观察，转换后的内容不再次参与结算。原生 Images 未提供可选的 `output_tokens_details` 时，已报告的输出 token 按图片输出计费；显式细分（包括零）优先。Responses 主控模型的用量不使用这项 Images 推断。HTTP 200 中的 `error` 对象按失败处理。转换和下载失败不会再次提交生图；已经观测到的原生完成图片和用量在失败时保留，包括非流式客户端聚合到一半的情况。客户端断开后，原生流停止下游写入并继续收集上游结果；等待心跳和实际输出仍分别决定 HTTP 提交与重试窗口。诊断记录客户端模式、出站模式、响应格式和是否转换。错误提交规则见[网关错误响应策略](gateway_error_policy.md#处理位置)。

流式客户端在已发送图片或 SSE 心跳后遇到上游错误，会收到一个 `error` 事件，网关同时记录失败状态；错误后不发送 `[DONE]`。尚未提交响应时使用 HTTP JSON 错误。

原生 Images 的 `image_generation.completed` 和 `image_edit.completed` 都按实际图片内容计数，沿用 ID、call ID 和内容摘要的去重规则。部分图片按事件类型判断，提示词、URL 等元数据中的 `partial_image` 字样不参与过滤。部分图片事件与没有图片内容的完成事件不增加完成图片数，完成事件中的尺寸和用量照常保留。

OpenAI API Key 提供商可以通过 `extra.images_url_to_b64_json=true` 开启图片回填，默认关闭。非流式的 `/images/generations` 和 `/images/edits` 响应里，只有缺少非空 `b64_json`、但带有 URL 的图片项才会被补全；已有 Base64 的、明确指定 `response_format=url` 的和流式的请求，保持原样。回填保留原 URL、修订后的提示词和所有上游元数据，某一张下载失败时只跳过它；用量、图片数量和计费尺寸，始终从回填之前的上游响应读取。

下载复用提供商的代理，不带提供商认证和客户端 Cookie；每张最多 20 MiB、60 秒，只接受字节嗅探确认的 PNG、JPEG、WebP、GIF，data URI 同样检查内容和大小。目标检查见[上游传输安全](../operations/upstream_transport_security.md)。URL 回填复用同一个传输和逐跳的目标校验，返回格式的选择和计费元数据保持不变。

图片的 JSON 和 multipart 解析、上传限制，由 upstream 通用的图片输入处理；OpenAI 原生包负责 Responses 图片转换、渐进事件、终态去重和尺寸解析。API Key 和 OAuth 各自按自己的规则读取响应，通过同步的 OutputSink 交付。非流式的 OAuth 图片，在读取上游期间继续发送 JSON 空白心跳，开始写响应时才停止；断开之后的读取和计费，各分支按自己的规则处理。

### 创作台的 Images 请求

创作台的异步执行器读取同一套端点模式能力，流式上游由共享 Images 读取器汇总为最终图片。`generate` 使用 `/v1/images/generations` 的 JSON，`edit` 和 `inpaint` 使用 `/v1/images/edits` 的 multipart；默认发送 PNG、单份 `n=1`，GPT Image 2.6 可选择 GIF，并按最终模型的能力透传尺寸、质量和背景。GPT Image 模型不发送 `response_format`，只有 DALL-E 模型带 `response_format=b64_json`。inpaint 的 mask 需要是和源图同尺寸、4 MiB 以内的 PNG，透明像素表示需要重绘的区域。

兼容上游可能只返回 `data[].url`。创作台优先解析可用的 `b64_json`，没有可用的 Base64 时下载 URL 图片；这条路径一定要拿到图片的字节，不依赖普通 API 的 `images_url_to_b64_json` 开关。下载复用上面的回填下载器和提供商代理，保留签名 URL，不带生图的认证和自定义请求头，并应用相同的公网、重定向、格式、20 MiB 大小和单次 60 秒的限制。生图的响应在下载之前关闭，释放连接。

下载最多尝试三次，间隔一秒，受任务执行 context 的限制；最终失败时返回 `IMAGE_DOWNLOAD_FAILED`。HTTP 成功、但响应里没有可以解析的图片时，返回 `INVALID_IMAGE_RESPONSE`。这两类错误会结束任务，按失败路径释放预占，生图请求不会被重新排队；上游的 HTTP 错误按已有的重试规则处理。下载错误的正文和签名地址不会写进任务的错误消息。

分组可以按协议开放 Messages、Responses 和 Chat，新建时默认开放这三个文本入口，三项都可以关闭。旧字段 `allow_messages_dispatch` 是 Messages 已弃用的兼容镜像；迁移时，只有这个旧字段开启的分组才加入 Messages。Messages 的模型改写，统一使用分组的 `routing_policy.model_mapping` 和提供商的模型规则。Responses WebSocket 是 OpenAI 和 Grok 的原生传输能力，其他平台启用了兼容 Responses，也不会因此开放它。

<a id="openai_fast_policy"></a>
### GPT Image 2.6 与 GIF

`gpt-image-2.6-sunburst` 与 `gpt-image-2.6-flare` 使用内嵌目录补充，并开放 `xhigh`、`max` 质量选项。GIF 使用 `output_format=gif`，`frame_count` 为 2 至 16，缺省 9；`frame_duration_ms` 为 20 至 2000，缺省 200，按 10 毫秒舍入。尺寸为 `auto` 或两边均为 16 倍数的正整数，供应商负责总像素预算校验。

原生 Images JSON 的 `frame_count` 和 `frame_duration_ms` 接受 `null`，与省略字段相同：GIF 请求使用对应默认值，静态图片请求不设置动画参数；显式整数继续按范围校验。

OpenAI OAuth 和 API Key 提供商的 `extra.images_gif_enabled` 缺省开启，显式 false 关闭。调度、粘性复核和实际发送都检查开关与最终模型的 GIF 格式能力；API Key 的对应 Images 端点配置为仅流式时不接收 GIF。创建、编辑、批量编辑和导入导出保留该设置，缓存快照保留显式 false。

Images GIF 请求不接受 `stream=true`、`partial_images`、mask 或 `output_compression`。OAuth 通过 Responses 图片工具发送动画参数，承载请求继续使用生命周期事件流，工具不发送中间图片帧。创作台 OAuth 复用相同的请求构造与身份头。

GIF 的 MIME、尺寸和帧数读取返回字节，下载及本地保存保留原始动画。返回帧数可以与请求不同，单帧或超过请求帧数范围的有效 GIF 也会接受；按图片计费使用实际帧数，token 和按次计费保持各自的计费单位。URL 结果使用现有的安全下载器取得字节，即使未启用普通 URL 回填也需要验证动画。无法下载或容器不完整时结束结果处理，不重新提交生成；已观测用量仍保留。文件数量与计费帧数分别记录，计费规则见[路由与结算](../domains/routing_and_billing.md#group_model_pricing)。

Responses 的 HTTP、SSE 和 WebSocket 结果同样按完整 GIF 容器校验并记录实际帧数，不把请求的 2 至 16 帧限制用于返回结果。GIF 容器不完整或缺少图像帧记录时仍作为结果失败处理。

Images 响应中的 GIF 项按实际字节校正 `data[].size`。当全部输出都是同尺寸 GIF 时，顶层 `size` 同步为该尺寸；尺寸不一致或混有其他格式时移除顶层 `size`，各项保留自己的尺寸信息。成功的 GIF 原生 JSON 和 SSE 汇总响应都使用校正后的条目尺寸计费。仅含静态图片的响应保持原有元数据。

### Fast 与 Ultra Fast 策略

`service_tier` 报文字段的校验、归一化和类型化错误由 `protocol/openai` 提供；策略求值和拒绝错误在 `gateway/tierpolicy`；认证范围和模型白名单动作复用 routing 的纯规则。

分组用 `openai_fast_policy` 选择 `follow_request`、`force_priority`、`force_ultrafast` 或 `force_off`。HTTP、Chat、Messages、passthrough 和 WebSocket 使用同一套策略：强制开启时，可以为没带 tier 的请求注入对应的档位；强制关闭时，移除 Fast 和 Ultra Fast，并阻止 Key 再次开启，其他合法的 tier 保留。

分组的强制意图要先经过全局规则；全局的过滤、阻断、强制 Fast 或 Ultra Fast 都有最终的优先级。Key 的 force_off 可以移除全局放行的分组级加速，force_on 不会把分组级的 Ultra Fast 降档。全局规则只匹配已有的合法 tier，主动作和其他模型动作都支持 `force_ultrafast`。

新字段优先于旧的 `force_openai_fast`：旧值 true 映射为强制 Fast，false 映射为跟随请求，更新时两个字段都省略则保留现值。其他平台的提供商不应用这项策略，公开的分组接口不返回管理用的策略。迁移 269 保留旧开关的行为；新字段随分组复制、仓储和认证快照传递，认证快照版本为 46，版本不一致时重建快照。

`free_openai_fast` 是关联价格配置的用户计费策略，出站的 `service_tier` 不受它影响。只有 OpenAI 提供商实际按 `priority` 或 `fast` 计费时才生效：网关用同一个模型映射、价格配置的价卡、高峰和长上下文的时刻，重新取得 Standard 价格，写进用户侧的 `ActualCost` 和统一结算的基础金额，同时把 Fast 的 `TotalCost` 留给 Usage Log、提供商统计和提供商额度。Standard 价格缺失时，按零成本缺价的流程记录，原有的定价错误照常报告；非 OpenAI 提供商、普通的 tier 和不可信的认证快照都不适用。这个字段随 API Key 认证快照传递，快照和失效规则见[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)。

### 推理档位上限

分组的 `max_reasoning_effort` 是客户端指定的推理强度的上限，`max_reasoning_effort_over_limit` 取 `downgrade`（默认）或 `deny`。网关只对客户端实际发送的 `reasoning.effort`、`reasoning_effort` 和 Messages 的 `output_config.effort` 执行策略；兼容桥为没带档位的 Messages 请求生成的默认 `medium` 不受影响。模型范围映射先于上限比较。

`downgrade` 把超出上限的值改成上限值；`deny` 在 HTTP 上返回 403 `permission_error`，Messages 返回 Anthropic 的 `forbidden_error`，Responses WebSocket 以 policy-violation 关闭。复合 Key 在鉴权中间件里已经解析到具体的分组，所以使用这个分组的策略。这个动作和上限随认证快照传递。

<a id="openai_account_configuration"></a>
### 原生协议配置

提供商协议和旧的文本、工作负载字段之间的转换，只在 `provider` 实现；文本协议的报文枚举在 `protocol/openai`。每次尝试解析出的协议由 `requeststate.AttemptRoute` 持有，不写进提供商的持久记录或共享缓存。

OpenAI 和其他平台一样，统一保存 `credentials.upstream_protocols`。API Key 可以分别启用 Responses、Chat、Embeddings、Images 生成和编辑、Responses WebSocket、Compact 和 Alpha Search；OAuth 的原生选项按 PAT 和 Agent Identity 的认证能力收窄，不显示 Messages、Chat 和 Images 的原生复选框。完整的矩阵见[统一协议能力](protocol_capabilities.md#account_native_protocols)。

分组的 `allowed_protocols` 控制客户端入口，`protocol_fallbacks` 为每个源指定单步的目标。提供商已经启用原协议时直接转发，否则只使用分组配置的目标；运行时由协议集合决定目标，`openai_text_route_mode` 不再参与。OAuth Images、PAT Alpha Search 和 WebSocket HTTP bridge 按各自的适用条件执行。`extra.openai_responses_continuation_supported` 和两个压缩开关各自独立，会话能力不算文本协议。

提供商的创建、更新、复制和批量更新不会自动探测协议。旧的工作负载和路由字段在接收输入时转换并清除，历史上的探测状态直接丢弃。Responses 图片工具使用分组独立的四态策略，优先于提供商和全局设置，独立的 Images 入口不受分组的这项策略影响。

## 远程压缩协议

TokenRouter 同时兼容原生的 Remote Compaction V2 和旧版的 Compact 端点。两者的 compaction 输出含义相同，但请求路径、传输方式、提供商能力设置和模型改写规则都不同。

HTTP 的路径识别、body-signal 提升、会话种子和结果日志由 `gateway/httpapi` 负责；触发项的检测、去重和移到 input 末尾，只在 `protocol/openai` 实现。OpenAITextHandler 在读取和校验的位置执行这些步骤。请求字段视图和按需的完整解码，统一使用 `gateway/requeststate`：重复字段保留第一个，前缀宽容读取，数字精度和错误前缀保持不变。

Responses 历史 Chat 格式的转换、工具 ID 清理、平台 schema 选择和 WS 兼容处理，由 gateway/provider 组合原生协议实现，在线 HTTP 和 WS 使用同一份算法。Compact 的单次恢复由 `gateway/compact.Recovery` 决定，`gateway/httpapi.CompactExecutor` 负责关闭原响应和恢复观测；app 只注入静态的默认模型和日志配置。提供商映射优先于默认模型，失败信号直接使用 `compact.Failure`，没有增加换号循环。

转换不会截断客户端或工具的文本；完整对象和字段补丁在调用时同步。官方、OAuth 和手动 passthrough 对 `none` 的保留规则，以及兼容地址删除占位值的规则，各自独立。

| 方面 | 原生 `remote_compaction_v2` | 旧版 `/responses/compact` |
| --- | --- | --- |
| HTTP 识别 | 裸 `/responses` 请求同时带 `stream=true`，并且 `input` 里有 `compaction_trigger`；`x-codex-beta-features` 不是识别条件，但原生 V2 出站一定包含 `remote_compaction_v2` | 客户端明确请求 `/responses/compact`，或者带 `compaction_trigger`、但不满足原生 V2 条件的裸 `/responses` 请求，被网关提升过来 |
| 上游传输 | 走普通 Responses 的流式链路，上游直接返回带 `compaction` item 的 SSE | 走独立的 Compact 子路径；body-signal 的流式客户端，由网关把 unary 的 JSON 结果合成为 Responses SSE，长时间等待时发送注释心跳 |
| 模型处理 | 按普通 Responses 处理模型，不应用 `compact_model_mapping`，也不会因此追加 `-openai-compact` | 只有这条路径，在常规模型处理之外再应用提供商的 `credentials.compact_model_mapping` |
| 提供商设置 | 由 `extra.openai_native_compaction_v2_mode` 控制 | 由 `extra.openai_compact_mode` 和 Compact 专属的模型映射控制 |

提供商设置页用两个独立的复选框，控制"原生 V2 压缩"和"旧版 Compact 端点"，分别持久化为 `openai_native_compaction_v2_mode` 和 `openai_compact_mode` 的 `force_on` 或 `force_off`。调度只看管理员的开关，不读取探测结果，也不按"未知"或"已探测"分层。迁移 270 把旧的 `auto` 和缺省值，按升级前的实际状态固定为明确的开关：历史上明确不支持的转为关闭，没有结论的保持开启；之后旧客户端提交的 auto 按开启处理。原生 V2 仍要求提供商支持 Responses 上游路由；旧版专属的模型映射只影响 `/responses/compact`，管理界面只在启用旧版端点时才显示它。

管理端的手动连接测试，仍然可以选择 `compact` 或 `legacy_compact`，结果只显示这一次的路径是否成功，不会写入压缩能力状态，也不会覆盖管理员的开关。OpenAI API Key 的普通文字测试有 `protocol=responses|chat_completions`，直接连接所选的上游协议，不受提供商保存的路由模式影响，也不修改它；省略时使用提供商的配置。OAuth 测试固定使用 Codex Responses，压缩测试固定使用对应的 Responses 路径。

## Responses 请求细节

<a id="responses_ws_runtime"></a>
### Responses 长连接与在线参数

客户端能否使用 Responses WebSocket，由分组的 `allowed_protocols` 决定；上游使用 WS 还是 HTTP，由提供商的 `credentials.upstream_protocols` 和分组转换规则共同决定。WS 功能没有全局启停开关，上游 WS 使用 v2。分组撤销 WS 许可后，已有连接在下一轮准入时以 1008 关闭，已放行轮次照常完成。

提供商的 `extra.responses_ws_connection_mode` 在 `pooled` 和 `per_session` 中选择，缺省为 `pooled`。页面分别显示“优先复用已有连接”和“每个会话新建连接”。连接方式与协议许可分别保存。HTTP 转换由分组配置，超大首帧也要通过上游 HTTP 能力和分组转换检查。

管理员在“网关设置 → OpenAI → 长连接与连接池”修改参数。管理员设置 GET/PUT 的 `responses_ws` 保存覆盖对象，GET 和保存响应的 `responses_ws_effective` 返回有效值。字段省略表示不修改，字段为 null 时清除该项覆盖，整个对象为 null 时恢复全部部署默认值。0 和 false 按参数定义保存。管理端覆盖优先于版本化 YAML 和代码默认值；环境值只有在 YAML 中显式引用时才参与加载。

`gateway/ws.Runtime` 保存一份不可变快照。综合设置完成校验和原子持久化后，运行时立即发布配置；后台每五秒回源刷新，读取失败继续使用最后一次有效快照。发布代次阻止较早的回源覆盖新保存值。启动时首次读取失败使用部署默认值。保存成功但运行时应用失败时，接口返回 `SETTINGS_APPLY_FAILED`，metadata 的 `persisted=true` 表示参数已写入数据库。

连接池扩容按需执行，缩容回收空闲连接，已占用连接等会话结束后回收。总连接上限和空闲连接上限分别计算，忙连接占用总容量。回收时的关闭握手在池引用和提供商池的访问锁外执行，其他请求仍可获取连接池或释放排队名额。

每次获取租约都会检查 60 分钟寿命上限。普通请求为过期连接重新拨号，强制续接返回指定连接不可用。正在使用的连接在租约归还后回收。等待者的空闲保留规则作用于仍然有效的连接。

最少和最多空闲连接数均支持 0。最多空闲连接数设为 0 时，归还的有效连接先供兼容等待者领取，没有等待者时关闭。预热每次拨号前和结果入池前检查当前需求、总容量及空闲上限。超出新限制的结果立即关闭，未使用的建连名额归还连接池。

客户端连接数量和消息读取上限作用于新连接，超时和恢复参数按下一轮快照执行，轮间超时在下一次等待时读取。每次获取连接的等待预算按当前轮次快照计算，排队期间的唤醒和参数更新都使用入队时的截止时间。会话记录 TTL 作用于新写入和续期，已有记录自然过期。正在生成的回答使用该轮已经取得的参数。

WS 入口、逐轮执行适配和 HTTP 转换在 `gateway/ws/httpapi`，会话与恢复规则在 `gateway/ws`，连接池、握手和 relay 在 `upstream/openai/ws`。app 创建共享拨号器和参数读取器，并在关闭连接资源前停止设置刷新。公开 HTTP Responses 请求固定走 HTTP/SSE。

<a id="responses_ws_warmup"></a>
### WebSocket 预热和续接

官方的 Codex WebSocket v2 先发送一个 `generate=false` 的预热 `response.create`，再把预热响应的 ID 作为业务请求的 `previous_response_id`。严格的续接比较会忽略每次请求都会变的 `client_metadata`、只用于传输的 `stream_options`，并把 `generate=false` 和之后省略这个字段看作等价；`generate=true`，以及 model、instructions、tools、reasoning、store 等上下文字段，仍然需要一致，无关的请求才不会被错误地串在一起。

OpenAI 和 Grok 的 HTTP 桥接在本地处理 `generate=false`：返回共享同一个响应 ID 的 `response.created` 和 `response.completed`，输出为空、用量为零。预热输入保存在当前连接的重放历史中，下一轮用该 ID 续接时才调用 HTTP 推理接口。OpenAI API Key 和 Grok 的预热还会保存工具声明及名称映射，后续轮次省略 `tools` 时可继承这些工具。预热执行身份、协议和资金准入，完成后释放并发槽位；本地预热结果跳过计费、上游健康反馈和上游额度刷新。写客户端失败时结束该轮。

### 路由提示和 beta 头

OpenAI OAuth 的 HTTP、passthrough、旧版 Compact 和 WebSocket 出站，在完成模型映射和本地的 fast 策略之后，由网关生成 `x-codex-routing-hint`。提示至少包含最终的上游模型；只有有效的 `priority`、`ultrafast` 或 `flex` 才附带 tier，`fast` 先规范化为 `priority`，`default`、未知值和空值都只带模型。

旧版 Compact 规范化时保留 `service_tier`，否则提示里会丢掉已经生效的路由层级。这个头完全由网关控制：所有类型的提供商，都先删除调用方和提供商覆盖里任意大小写的这个头，只有 OpenAI OAuth 路径会重新生成；API Key 路径不会透传伪造的提示。OAuth HTTP 不再自动注入或透传旧版的 `responses=experimental` beta 标记，同一个头里其他独立的 beta 项保留。

`x-codex-beta-features` 是 Codex 会话级的协商头：OAuth 的普通 Responses HTTP 和 WebSocket 握手，在客户端没有声明时补上 `remote_compaction_v2`，客户端给出的非空值保持原样；原生 V2 请求无论哪种提供商，都保证带有这个 feature。上游响应里的 `x-codex-turn-state`，在 HTTP/SSE、SSE 转 JSON 和 passthrough 路径上都明确回传。

### 回合状态的来源

网关按 API Key 和客户端原始的 session，记录最近签发回合状态的提供商。故障转移之后，已知由其他提供商签发的客户端回带值会被剥离，来源未知或来自同一提供商的值继续透传。这张来源表由 `gateway/session.CodexTurnOrigins` 持有（只有一个），HTTP 的暂存、提交和回带过滤由 `CodexTurnStateHeaders` 执行。app 绑定同一个实例和粘性 TTL；每登记 256 次清扫一次过期的来源，过期的记录继续透传。来源表只保存提供商 ID，不保存不透明的回合状态值，也不和 WS 的回合状态缓存合并。

### WebSocket 连接池的亲和

WebSocket 连接池在拨号和普通复用时优先选择路由提示相同的连接。池满或请求已经排队时，可以使用其他兼容连接。明确的 continuation 按连接 ID 查找，路由提示变化后仍可续接。

上游 WebSocket 地址、代理 URL、TLS fingerprint profile、握手的 beta feature 和指纹模式要求的身份头共同决定连接能否复用。指定连接、普通连接和预热拨号使用同一套检查。地址或代理变化后，新请求选择匹配新配置的连接，忙连接继续供持有租约的会话使用。池满时，空闲的不兼容连接可以被回收。预热目标变化后，旧目标的拨号结果会被关闭。路由诊断字段为最终模型、规范化的 tier、传输类型、提供商 ID、提示生成状态和连接选择结果。

普通请求在提供商池中等待，连接归还、建连完成或容量变化时重新选择兼容连接。强制续接的请求等待指定连接。提供商的排队总上限为有效连接上限乘以 `queue_limit_per_conn`，强制续接还受指定连接的排队上限限制。等待新目标连接的请求也计入队列和等待时长。取消、超时和关闭会释放排队名额，反复唤醒计为同一次排队。

等待者退出后，剩余空闲连接由池的后台清理任务回收。等待者取得租约时也使用这条回收路径，多余空闲连接的关闭握手和租约交付分别执行。清理通知合并保存，关闭握手使用底层独立的超时。租约交付前发生取消时，连接先归还并唤醒其他等待者，再通知后台清理。池关闭时先唤醒等待者，再等待清理任务完成。

WS 的报文、模型字段恢复和 usage 解析在 `protocol/openai`；OpenAI 的恢复载荷和供应商错误分类由 `upstream/openai` 提供。传输方式由 egress 根据提供商资格决定；入站的会话 Header、关闭码和读循环的连接适配属于网关 HTTP 层，技术诊断在网关的 provider 里。

### TTFT 的计算

OpenAI HTTP/SSE、Chat/Messages 转换和文本 WebSocket 使用全局 `ttft_mode`。首包取首个非心跳上游事件，首字取首个有效内容，两者均从本次生成请求发起计时。内容分类、TPS 和存储归属见 [HTTP 接口](http_api.md#response_errors)，设置规则见 [配置](configuration.md#runtime_settings)。

HTTP 提交、首输出超时与重试资格仍由响应处理策略判断。统计口径不改变这些状态，也不为非流式响应、图片结果或纯 usage 终态补造文本首字时间。

### Codex 基础指令和 Responses Lite

OAuth passthrough 的 Codex 请求可以省略 `instructions`，网关按请求的模型补上内置的 Codex 基础指令；明确提供的非空字符串保持不变，空白或非字符串的值在本地拒绝。这条规则同时适用于 Responses SSE 和旧版 Compact 请求。

Responses Lite 由 HTTP 的 `X-OpenAI-Internal-Codex-Responses-Lite: true`，或者 WebSocket `client_metadata` 里对应的标记识别，模型名不参与判断。所有向 OpenAI 上游转发这个标记的 HTTP、passthrough、旧版 Compact 和 WebSocket 请求，都强制设置顶层的 `parallel_tool_calls=false`。OAuth 提供商还统一设置 `reasoning.context=all_turns`，并把私有的 namespace 工具声明移到 `input.additional_tools`；API Key 提供商在这之外保持标准的 Responses 请求。不带 Lite 标记的普通 Responses、Grok 和专用的 Images 请求，不应用这些约束。Responses Lite 的报文重建和工具校验只在 `upstream/openai` 实现，执行适配按提供商资格，选择完整转换或只禁用并行工具。

### Anthropic count_tokens

OpenAI OAuth 提供商处理 Anthropic 的 `count_tokens` 时，调用 Responses 的 `input_tokens` 端点。缺少 scope、端点不存在，或者上游代理在 API 之前返回 HTML 格式的 `403` 时，网关改用本地的 token 估算并返回成功。这类端点级的失败不会冷却、临时踢出或标错提供商；其他结构化的鉴权和上游错误，按正常的健康策略处理。

### namespace 和工具 ID

OpenAI OAuth 和 API Key 的普通 Responses 请求保留 `function_call`、`tool_call`、`custom_tool_call`、`mcp_tool_call` 历史项上的 `namespace`。工具可以在顶层 `tools`、历史的 `additional_tools.tools` 或 `tool_search_output.tools` 中声明，也可以由上游会话保存；本轮省略声明不影响已经执行的调用身份。普通消息等非调用项上残留的这个字段会被清理，转义的 JSON 键名使用同一规则。旧版 Compact 的字段白名单、namespace 清理和 OAuth 工具平铺仍由对应端点规则决定。

转换到 Chat 或 function-only Responses 时，协议桥接层先解析有效声明，再生成已知历史调用、`tool_choice` 和响应恢复使用的名称映射。默认 `functions` 命名空间使用工具裸名；其他显式 namespace 子工具使用稳定的平铺名称。工具搜索加载的普通 function 根据实际加载声明保留自己的调用身份。转换后的非流式结果、SSE 生命周期和终态使用声明映射恢复名称、namespace 和调用 ID；声明之间的名称碰撞或无法解析的限定工具选择返回错误。

压缩或工具列表变化后，历史 `function_call` 仍可能带有 namespace，但对应声明已被省略。桥接层为这些调用单独分配本次请求的回放名称，保留调用 ID、参数和结果。名称分配预留已知工具、响应恢复认可的 custom 工具别名及历史裸名，按 namespace/name 排序；平铺名称碰撞时，用完整二元组的摘要区分，并检查长度和名称占用。补出的身份只用于输入回放，工具声明、选择器、响应恢复和后续 WS 可用工具不包含这些身份。身份扫描仅检查直接输入项，工具结果和参数中的嵌套数据不参与名称分配。

WS HTTP bridge 在后续轮次省略 `tools` 时复用已有的声明和映射；显式空值清空后续可用工具，历史身份仍可用于解释已经执行的调用。原生 Responses 保留动态声明项的原位置，工具平铺只在需要协议转换的路径执行。提供商切换清空当前尝试的映射，下一次尝试从原始请求重新准备。

上游明确以 HTTP 400 拒绝 `input[i].namespace` 时，现有字段恢复只删除该索引处的字段，同类型其他调用的 namespace 保留。`Missing namespace` 表示上游需要调用身份，不触发删除。恢复继续受现有六次预算和重复报文检查约束，超过预算返回实际错误。

API Key 的 Responses 回放还会校验输入项 ID 的前缀：message 使用 `msg`，工具调用使用 `fc`，reasoning 使用 `rs`；类型对不上的 ID 直接删除，不会被改写成别的，这样伪造的标识就指不到另一个上游对象。兼容层把只支持 function 的上游返回的 `fc_` 工具调用，还原成客户端的 `custom_tool_call` 或 `tool_search_call` 时，把 ID 类型分别改成 `ctc_` 或 `tsc_`，后缀保留；再次降级到 function 时恢复原来的 `fc_`，输出项没有对应 function ID 的继续删除。

流式恢复用上游的 ID 匹配后续事件，只向客户端发送转换过类型的 ID，历史重放和 SSE 的生命周期都有效。只有当 OAuth 提供商的兼容中转不接受 namespace 时，才启用提供商的 `extra.openai_responses_flatten_namespaces=true`，恢复平铺的名称。每次 failover attempt 都清空上一个提供商登记的平铺映射，响应的还原状态不会串到下一个提供商。

Responses 的工具定义，在进入 OAuth passthrough、Codex transform、Grok 或 API Key 的 Chat 分流之前，统一修正明确为 `null` 的 `parameters.type`，改成 `object`；处理范围包括顶层的 `tools[]` 和多轮历史 `input[].tools[]` 里嵌套的工具。没有 `type` 的合法宽松 Schema 保持原样，补写它会收窄客户端原本允许的类型。

### Responses 降级到 Chat

Responses 请求降级到 Chat Completions 时，工具结果里的 `input_image`、`image_url` 和完整的图片 data URL，不能留在只接受文本的 `tool` message 里。转换器按 `call_id` 从工具结果里取出图片，把原位置替换成稳定的标记，并在这一组工具回复之后追加一条用户的多模态消息；并行调用按工具声明的顺序归属图片，孤立的或没有回答的调用不带媒体。没有可识别图片的工具结果保留原始字节，无关的 JSON 不会被重新编码，提示缓存的前缀因此保持不变。

OpenAI API Key 提供商以 `force_chat_completions` 处理 `/v1/messages` 时，Chat 流里并行的 `tool_calls`，按 `tool_calls[].index` 聚合 ID、名称和全部参数分片，在流结束时，再按 index 的顺序，为每个调用生成连续闭合的 `content_block_start`、`input_json_delta`、`content_block_stop`。参数分片先暂存，最后一次拼接；聚合期间由统一组件按下游空闲时间保活，帧格式见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)；文本和 thinking 照常即时流式输出。

空的工具参数归一化为 `{}`，call ID 保持原样，下一轮的 `tool_result.tool_use_id` 才能配对。Anthropic 的 `tool_choice.disable_parallel_tool_use=true` 映射为 Chat 顶层的 `parallel_tool_calls=false`，字段缺失或为 `false` 时，保持默认的 `true`；`auto`、`any`、`none` 和具名工具的选择含义不变。

Responses 生图有一个明确的协议适配例外：请求的图片模型进入 `image_generation` 工具，文本模型承载 Responses 请求；请求模型、承载模型和图片计费模型分别记录。这个例外不开放普通文本模型的隐式别名。管理员手动配置的 Compact 回退照常生效，目标型号不会被再次纠正。

OpenAI OAuth 的图片生成、编辑、后台图片测试，以及 Responses 顶层图片模型的转换，共用提供商的 `extra.codex_image_responses_model` 选择文本主控模型。非空字符串使用指定型号，缺省、空字符串和 null 使用 `gpt-6-luna`；设置属于当前选中的提供商，影子提供商也使用自己的设置。创建、编辑和批量编辑中的「生图主控模型」可写入或清空该值，复制和导入导出随 Extra 保留。这个配置与自动添加图片工具的策略分别保存，图片工具型号仍经过原有模型映射。客户端已指定文本模型的 Responses 请求保留该型号；API Key 图片模型转换使用默认值 `gpt-5.4-mini`。

主控型号在账号上的可用性由 Codex 校验。上游 HTTP 400 明确拒绝本次主控型号时，图片入口和自动转换图片型号的 Responses HTTP 返回 `image_main_model_unsupported`，说明中同时包含主控和图片型号；该错误直接结束当前请求，不写图片能力冷却，也不自动切换主控型号。后台图片测试输出相同的型号说明。最终展示仍应用管理员的错误规则，包括状态码、文案和监控跳过设置；这些覆盖不改变主控拒绝的重试分类。响应提交前返回 JSON，已建立 SSE 的请求返回对应的流内错误，Images 非流式的空白心跳继续使用 JSON。普通响应中的其他错误继续使用各自的错误和故障转移规则。

## 模型与能力

图片模型识别使用最终路线的输出模态。图片接口与 Responses 的原生图片工具在发送前检查能力：未知返回 `MODEL_CAPABILITY_UNKNOWN`，明确不支持返回 `MODEL_CAPABILITY_UNSUPPORTED`。型号名称中的 image 不参与判断。工具明确型号优先于转换过程中确认的图片型号，未指定型号的原生图片工具使用其既定默认型号，并执行相同校验。HTTP、透传与 WebSocket 使用相同规则；本地拒绝不计作上游故障。

Responses 图片工具的型号在候选提供商阶段逐条校验，使用该候选的最终协议和模型属性。同组其他路线具备能力时继续选择；全部路线不满足时返回能力错误。转发前还会检查最终工具列表，工具被策略移除时不参与图片能力校验。

客户端的模型名依次经过 Key、分组和提供商手动配置的映射。调度、模型列表、健康冷却、管理测试和请求转发使用同一个完整的模型身份；原生能力和传输资格继续限制提供商选择，相似的模型名绕不过这些限制。

Messages、Chat、Responses 和 WebSocket 按最终的完整型号查询[统一模型属性](model_catalog_and_marketplace.md#model_attributes)。推理档位来自目录与管理员覆盖，未知型号保留客户端的明确参数。GPT-5.6 Sol/Terra/Luna 的普通请求支持 max，OAuth Compact 使用平台的 max 到 xhigh 转换。GPT-5.2/5.4 的采样能力带 effort=none 条件，GPT-6 的 Chat 工具能力按具体型号与 effort 区分。Codex OAuth 禁用 temperature、top_p 和输出参数字段，目录中的输出容量仍保留。

供应商限定名按完整服务条目查询，例如 `openai/gpt-5.6-sol`。查询结果不改写出站模型，目录缺项时也不会从任意尾段、日期或版本号生成能力。

GPT-5.6 的内置产品是 `gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`。裸的 `gpt-5.6` 按未知型号处理。请求通过提供商的模型范围和协议资格检查后，上游收到手动映射后的完整型号，计费候选使用选定的计费模型。未知名称是否可用由上游决定。管理员配置的 Key、分组和提供商映射参与请求处理，历史配置和用量记录保持原值。模型目录的查询和能力来源，见[模型目录与市场](model_catalog_and_marketplace.md#model_catalog_metadata_lookup)。

`gpt-6-astra` 的最终上游请求只接受 `low` 到 `max` 的推理档位。网关不为 Astra 硬编码档位的改写；需要兼容遗留的 `minimal` 或 `none` 的分组，在"推理强度映射"里按请求模型配置目标值（例如都映射到 `low`）。`none` 只能用于映射，不能作为最大推理强度，因为它没有可以比较的强度排名；没有配置映射时，普通转发层保持客户端的档位，是否接受由上游或对应的兼容层决定。GPT-5.6 仍然支持 `none`。本地价格目录和代码里的回退，都保留 Astra 的官方标准价；远端价卡还没同步时，也不会按其他 GPT 型号计费。

Usage Log 的 `requested_reasoning_effort` 保存策略改写之前、客户端明确发送的值（包括 none），`reasoning_effort` 记录最终上游请求实际发送的档位。协议的默认值不算客户端的输入，被转换删掉的字段也不会补回来。HTTP 和 WebSocket 都不从模型名生成档位，WS 每轮重新提取，缺省值清空。

最终档位是 `high`、`xhigh` 或 `max` 的请求，在使用首输出超时策略的链路里，都选高 effort 的超时档；协议桥仍然可以按上游的实际能力调整转发的值，例如 Anthropic 兼容转换可以把不支持的 `max` 降成 `xhigh`。

API Key 的普通调度能力只表达 `text_generation` 和 `embeddings` 两种工作负载，`chat_completions` 不再同时代表工作负载和协议。Responses 生图等必须走原生 Responses 的路径，有单独的能力门禁：按 Responses 首选解析、结果却落到 Chat 的提供商，不能处理这类请求。OAuth 和 Codex 提供商还可能有 Realtime、WebSocket、旧版 Compact 端点状态和客户端身份的限制。管理员明确配置的兼容上游里，未知模型可以透传，但没有价格或能力证据时，价格和功能都按未知处理。

Images API 的流式和非流式上游请求，都和客户端的取消信号脱钩，继续执行，由上游的响应超时控制最终的回收。生图耗时长，而且上游可能已经产生了实际的成本；客户端中途断开，不应该取消上游、丢掉已经完成的图片的计费结果。下游写失败不影响图片的产出和结算。

OpenAI HTTP 的准备、同一提供商内的恢复和响应消费，由 `gateway/provider/openaiforward` 接入 upstream 的原语；提供商切换只有 `gateway/text` 的一个循环。入站 WS 的 turn 和恢复由 `gateway/ws` 组织，连接池和帧解析在 upstream。Compact 的恢复资格和状态在 `gateway/compact`，keepalive 和提交后的错误写出在 `gateway/httpapi`；计费和会话缓存都只有一份。

执行器使用 `gateway/execution` 明确的 Request 和 ExecutionResult，以及同步的 OutputSink。候选计划只在实际选择返回时记录，缺失时用 PlanProvided 表示；系统不会为了填充结果额外查询，也不会在执行结束后重新计算。完成数据在入队前固定，WS 保留每个 turn 自己的定价时刻、模型链和部分失败资格。

## 额度与调度

OpenAI 为通用的高级调度器提供平台能力适配，评分核心在 scheduler。只有最终目标分组的 `scheduler_type=advanced` 时，OpenAI 路径才在共同的 active、schedulable、分组、模型、限流和并发硬过滤之后，使用通用的 Top-K 评分；`basic` 使用默认的选择路径。高级分组可以用稀疏的 `advanced_scheduler_overrides` 覆盖全局的 Top-K、评分权重和粘性开关，没设置的字段继承网关的设置。

高级分组还会考虑所需的 transport 和 capability、提供商优先级、负载、排队、错误率、近期延迟、配额余量和粘性上下文。previous response、WebSocket 会话和手动指定的 session 可以约束提供商的复用；只有策略允许时才能迁移到其他提供商。

OpenAI 专属的能力，只在提供商和请求都具备相应条件时，才加入候选或评分：Responses transport、WebSocket、旧版 Compact、previous response、订阅优先和 Codex 额度余量，缺少这些可选信号的普通提供商照常参与。OAuth 的 5 小时、7 天等上游窗口和自动暂停，由 OpenAI 的设置和提供商的运行状态控制，高级调度器的通用化不会把它们扩展到其他平台。

OAuth 提供商的 5 小时、7 天等上游窗口和重置时间，保存在提供商的运行状态里，可以触发临时限流或自动暂停；API Key 的文本协议和压缩资格只由管理员的配置决定。OpenAI 不采集上游站点声明的倍率，也不按它做低倍率优先或高级评分。提供商本地的 `rate_multiplier`，以及价格配置里的上游计费模型来源，用于 TokenRouter 的结算，和用户的余额、订阅、Key 限额无关。

管理 API 的 `GET /admin/openai/providers/:id/quota` 是只读的；提供商列表用 `POST /admin/openai/providers/:id/quota/refresh` 查询上游，并把重置次数写进 `provider.extra.codex_reset_credit_snapshot`。正数的次数，只有同时拿到到期明细时才覆盖快照；前端加载时，过滤已经过期的明细，并把次数修正为仍然有效的卡片数量。这个 extra 键只用于展示缓存，不触发调度 outbox。Spark 影子提供商的查询可以解析母提供商的额度，但快照写在被查询的那一行上；列表只提供查询入口，没有实际的重置按钮。

## 失败与诊断

提供商状态的更新使用凭据快照和 CAS，token 已经刷新之后，较早的请求无法再次封禁这个提供商。401 和 403、429、endpoint 不支持、内容策略、网络错误和上游的 5xx 分别分类；只有可以切换、并且客户端响应还没开始的失败，才进入下一个提供商。OpenAI 上游代理或 CDN 返回的 HTML 格式的 403，只说明当前链路或端点被拦截：请求仍可按已有规则 failover，但不会增加连续 403 的计数，也不会临时停调或永久禁用提供商；结构化的 JSON 和纯文本 403，按提供商级的策略处理。

API Key passthrough 的池模式，把命中 `pool_mode_retry_status_codes` 的 HTTP 错误，先转成尚未提交响应的 failover，在同一提供商的预算用完后才换号；没有配置时默认覆盖 401、403、429，配置成空列表可以关闭这种按状态码的重试。原生 Responses 上游返回的确定性 `400`，在提供商策略、池模式重试和错误透传规则都没有要求改写或故障转移时，按实际的 400 返回，并保留脱敏后的 `message`，以及诊断需要的 `type`、`code`、`param`；瞬时的处理错误和容量类的 400，仍按可重试或通用的网关错误处理。

图片模型被 Codex 的文本端点以 plan-gated 的 `400` 拒绝，属于端点用错了：当前尝试仍然换号，但不写模型冷却，这样同一提供商之后通过 `/v1/images/*` 生图不受影响；专用 Images 端点上的同类拒绝，按提供商实际缺少能力处理并冷却，图片模型的 `404 model_not_found` 也照常冷却。Responses 的 HTTP 和 WebSocket v2 第一次发送时，保留加密的 reasoning 和 compaction；上游明确返回 `invalid_encrypted_content` 时，在同一提供商内最多恢复重试一次，清理绑定在这个提供商上的加密状态，未加密的 compaction 保留。

提供商和模型组合的瞬时失败，按连续的结果累计：第一次失败只记录，第二次短冷却，第三次及以后长冷却。请求之间间隔较长，不代表故障已经恢复；只要没超过状态回收的 TTL，稀疏流量里的失败仍然继续累计；任何一次成功都会立即清零这个组合。TTL 只负责回收长期不用的条目，它不是"短时间内连续失败"的重置条件。

流式错误要保持 SSE 和 WebSocket 协议的完整：Responses 可以产生 `response.failed`，非流式接口返回对应的 OpenAI envelope。入站 WebSocket 的下行写入，不继承入站租约单独的取消信号：旧的 ingress 路径绑定客户端请求的生命周期，再加上写超时；v2 relay 只受写超时限制，退出时通过 Close 或 CloseNow 回收连接。这样租约丢失时，不会在写终态事件期间抢先强制关闭 TCP，客户端可以先收到终态事件，再收到 1013 关闭帧。

上行写入继承控制面的取消，以便快速回收上游连接。HTTP 200 的 SSE 里出现 `rate_limit_exceeded`，视为状态 429，进入故障转移和池模式重试，但不用这个 200 响应里正常的配额快照头，写入默认的提供商冷却。上游容量降载时，通常先发 `error`，再以 `response.failed` 收尾；`server_is_overloaded` 和 `slow_down` 的前置错误帧，在还没有业务输出时继续留在 attempt 缓冲里，触发有上限的同提供商重试和输出前的 failover，按请求级的瞬时故障处理，不冷却当前的提供商。

已经有实际输出，或者重试用完、不能再重放请求时，SSE 和 WS HTTP bridge 只在发给客户端的副本里，把这两个致命错误码改成可以重试的 `server_error`，原始事件仍用于提供商策略和观测。客户端还没收到业务输出时，池模式提供商的其他瞬时流内处理错误，也可以在请求级的预算内重试同一个提供商。所有已记录的心跳按[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)的统一口径从业务输出中扣除：即使已经提交了 200 响应头，只有心跳时仍允许既有策略内的重试，最终失败则追加 `response.failed`。OpenAI Responses 的标准流和透传流，如果只收到前导事件，以及完全不含 output、usage、error 的 `response.completed` 或 `response.done`，在还没向客户端写出业务内容时，按静默拒绝换提供商，不会记成一次 0/0 的成功；终态带有 usage、error、任何输出项，或者之前已经出现过语义输出时，不触发这条规则。

实际输出一旦开始，网关就不再重放请求或换提供商。最终的错误还可以命中[网关错误响应策略](gateway_error_policy.md)，但规则不会把失败结算成成功。排查问题时，同时检查提供商类型、需要的 transport 和 capability、客户端限制、privacy status、模型映射、配额重置时间、代理和 TLS，以及 attempt 记录。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[模型目录与市场](model_catalog_and_marketplace.md)。

模型名称与通用候选来自[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)。提供商空白名单允许任意型号，平台的请求格式、认证和协议规则分别检查；默认测试型号用于探测请求。
