# 统一协议能力

本文定义提供商的原生协议集合、分组的客户端准入和单步转换配置。平台认证、载荷转换、模型和媒体资格见各平台的文档。

<a id="protocol_catalog"></a>
## 能力目录

25 项能力目录只在后端的 `routing/capability.ProtocolCatalog` 维护。管理员接口 `GET /api/v1/admin/protocol-capabilities` 返回 `protocols`、`providers`、`groups` 和 `auxiliary_operations`：

- 提供商 profile 按平台、类型和认证方式列出原生选项。
- `groups` 是数组，只有一个不带 `platform` 的通用 profile，列出所有分组可用的入口、默认集合、转换目标和默认映射。`defaults` 只包含 Messages、Responses、Chat 三个文本入口，`default_fallbacks` 为 `{}`。
- 目录里没有凭据。前端共用这份只读结果，不维护自己的平台白名单。

Responses WebSocket 的客户端许可在分组协议控制中设置，提供商协议集合声明上游能力。连接方式和在线调参见 [Responses 长连接与在线参数](openai_upstream.md#responses_ws_runtime)。

提供商和分组共用 `protocol.ProtocolID`，平台和提供商类型的常量在 `routing/capability` 定义。HTTP 方法、路径、别名和 WebSocket 标记在 `gateway/httpapi` 声明，实际的路由门禁读取同一份声明；app 把展示用的地址注入 `routing/httpapi` 的目录 handler，纯目录代码不依赖 HTTP 适配层。路由层只规范化别名前缀；各文本入口按自己的原生错误格式和动作校验，Compact 在 Responses 子路径校验之后再检查。内部的路由元数据不出现在管理员 API 的响应里，协议 ID、JSON 字段和持久化格式保持稳定。

前端表单共用目录的加载、错误和重试状态。创建分组时，默认的准入集合和转换映射在目录加载完成后一起初始化；编辑时回显的值，以及管理员明确清空的集合，在目录加载完成后保持原样。目录不可用时禁止提交分组，任何一个协议选择器重试成功后，共享状态都会恢复。目录测试在普通运行中同时核对前端的 JSON 夹具和 `ProtocolID` 联合类型；表单测试按需准备目录，不依赖全局预热。

| ID | 界面名称 | 主要入口 | 候选提供商的适配器范围 |
| --- | --- | --- | --- |
| `anthropic_messages` | Anthropic Messages | `POST /v1/messages` | 现有的九个平台 |
| `openai_responses` | OpenAI Responses | `POST /v1/responses` | 现有的九个平台 |
| `openai_chat_completions` | Chat Completions | `POST /v1/chat/completions` | 现有的九个平台 |
| `gemini_generate_content` | Gemini GenerateContent | `POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent` | Gemini、Antigravity |
| `systemone` | SystemOne | `POST /v1/systemone` | Jev |
| `openai_embeddings` | Embeddings | `POST /v1/embeddings` | OpenAI |
| `openai_images_generations` | Images 图片生成 | `POST /v1/images/generations` | OpenAI、Grok |
| `openai_images_edits` | Images 图片编辑 | `POST /v1/images/edits` | OpenAI、Grok |
| `image_batches` | 批量图片作业 | `POST /v1/images/batches` | Gemini，包括 Vertex Service Account |
| `grok_videos_generations` | 视频生成 | `POST /v1/videos/generations` | Grok |
| `grok_videos_edits` | 视频编辑 | `POST /v1/videos/edits` | Grok |
| `grok_videos_extensions` | 视频扩展 | `POST /v1/videos/extensions` | Grok |
| `grok_tts` | 语音合成 TTS | `POST /v1/tts` | Grok |
| `grok_stt` | 语音识别 STT | `POST /v1/stt` | Grok |
| `grok_custom_voices` | 自定义声音 | `/v1/custom-voices` 和它的子资源 | Grok |
| `grok_voice_realtime` | 实时语音 | `GET /v1/realtime`，WebSocket | Grok |
| `openai_responses_websocket` | Responses WebSocket | `GET /v1/responses`，WebSocket | OpenAI、Grok |
| `openai_live` | OpenAI Live | `POST /v1/live` | OpenAI |
| `openai_responses_compact` | Responses Compact | `POST /v1/responses/compact` | OpenAI；Grok 使用现有的兼容实现 |
| `openai_alpha_search` | Alpha Search | `POST /v1/alpha/search` | OpenAI |
| `grok_web_search` | 网页搜索 | `POST /v1/web_search` | Grok |
| `grok_x_search` | X 搜索 | `POST /v1/x_search` | Grok |

平台一列表示实际候选提供商和转换器支持的范围；所有分组都可以配置这些入口。开放一个入口，并不会自动产生可用的提供商。

这 22 个客户端控制项里，图片生成和图片编辑分开控制，视频的生成、编辑和扩展也分开控制。HTTP 和 SSE 共用所属协议的控制项；Responses WebSocket 有自己的传输和会话要求，所以单独控制。

已有的无 `/v1` 前缀别名、`/backend-api/codex/*` 别名和 `/antigravity/*` 强制平台入口，都映射到相同的协议 ID，不增加控制项。`POST /v1/videos` 归入视频生成。

### 上游专用协议

这几项只出现在提供商的原生支持集合里，没有对应的公共客户端 URL。

| ID | 界面名称 | 实际的上游接口 | 适用的提供商 |
| --- | --- | --- | --- |
| `qoder_chat` | Qoder 原生对话 | Qoder COSY 的 `agent_chat_generation` SSE 接口 | Qoder Cosy |
| `gemini_batch_generate_content` | Gemini Batch GenerateContent | `/v1beta/models/{model}:batchGenerateContent` | Gemini API Key |
| `vertex_batch_prediction` | Vertex Batch Prediction | `/v1/projects/{project}/locations/{location}/batchPredictionJobs` | Gemini Vertex Service Account |

这 3 个上游专用项加上 22 个客户端控制项，就是 25 项目录。

批量图片是持久作业的入口：分组控制 `image_batches`，执行器检查提供商启用的是 Gemini Batch 还是 Vertex Batch 协议，并使用现有的 provider 选择和任务绑定。批量图片没有文本那样可以任选的转换目标。

Antigravity 对 Google 内部接口的封装，算作 `gemini_generate_content` 的一种平台适配，没有单独的协议复选框。Bedrock、Vertex Anthropic 等认证和端点的变体，同样归入对应的协议族。

<a id="account_native_protocols"></a>
## 提供商原生集合

提供商历史字段、缺省值和错误 reason 的解析和保存校验在 `provider`，分组配置在 `routing`。提供商把自己的平台、类型、认证方式和启用的协议整理成 `capability.ProviderProtocols`；分组单独提供客户端准入和转换策略。实际选择候选时，提供商配置被整理成不含凭据的 `provider.ProviderSnapshot`，由 `routing.RoutePlan.ResolveCandidate` 用复制过的分组协议策略判断。

这些判断是纯计算，不读取凭据、配置或请求 Context；每次判断候选都重新计算，明确设为空的集合不会被默认值补上。RoutePlan 负责候选协议这一部分，模型改写、最终的权限和资金检查、重试的时机，由调用链按既定顺序执行。

`credentials.upstream_protocols` 是原生协议 ID 的数组，空数组表示这个提供商不再承接新的调用。创建、编辑、复制、导入和批量编辑使用同一套校验：不允许重复、未知，或者提供商认证方式不支持的项，出错时返回 HTTP 400 / `UPSTREAM_PROTOCOLS_INVALID`。批量更新先验证全部提供商，再用同一条 SQL 原子地写入每个提供商的协议补丁。显式协议集合在旧 WS 桥接配置转换之前完成类型、重复项和选项校验。连接方式的完整保存、批量更新和 Extra 按键更新共用枚举校验，非法值在写入前返回错误。

各平台的原生协议：

- Jev API Key：SystemOne，支持同步决策请求。
- OpenAI API Key：Responses、Chat、Embeddings、Images 生成和编辑、Responses WebSocket、Compact、Alpha Search。
- OpenAI OAuth：Responses、WebSocket、Compact、Alpha Search、Live。PAT 没有 Alpha Search 和 Live，Agent Identity 没有 Live。OpenAI 没有 Messages 原生项。
- Grok：HTTP Responses、Chat、Images、视频和 Voice 是原生项；WebSocket、搜索和 Compact 是转换入口。
- CN API Key：Messages 和 Chat；DeepSeek 和 Kimi 另外有 Responses。
- Gemini 和 Antigravity OAuth：GenerateContent。Antigravity 的 API Key 和历史 `upstream` 类型没有可用协议。
- Qoder：专用的 Chat。
- Gemini API Key 和 Vertex SA：各自的 Batch 协议。

自定义的 `api_base_urls` 按协议分键：`chat_completions`、`anthropic`、`responses`。旧的 CN 固定协议的 `base_url` 迁到对应的地址槽里，原来的 `base_url` 继续用于模型同步和用量查询。新表单直接编辑地址和原生集合，两者互相独立。

<a id="group_protocol_routes"></a>
## 分组准入与转换

分组返回 `allowed_protocols`、`protocol_fallbacks` 和 `responses_image_policy`。准入集合为空时，关闭所有新入口。`protocol_fallbacks` 的值是有序的协议数组：某个入口没有配置时，自动匹配目录里的转换路线；入口的值为 `[]` 时，只允许原生协议；非空数组限定允许的目标和尝试顺序。

每个候选提供商先尝试已启用的原生协议，再选择第一个能执行的、允许的转换目标；这个目标要在提供商上启用，并且存在对应平台和认证方式的单步转换器。转换目标不需要同时作为客户端入口开放。找不到可行路线的候选在评分之前就被排除，换号时重新解析；转换后，模型、额度、媒体资格、WebSocket 模式和会话的约束照常生效。

支持哪些转换、自动模式下的顺序，以目录里的 `fallback_targets` 为准。管理员可以缩小目标集合、调整顺序或者关闭转换，但只支持一步转换。文本使用现有的平台适配；OpenAI OAuth 的 Images 转成 Responses，Grok 的 WebSocket 转成 HTTP Responses，Grok 的搜索和 Compact 转成 Responses，PAT 的 Alpha Search 转成 hosted web search。批量图片由已有的 provider 绑定选择 Gemini Batch 或 Vertex Batch。

同一分组的各个候选可以解析出不同的目标协议。例如客户端发来 Chat 请求，OpenAI 提供商可以原生处理，Anthropic 提供商可以转成 Messages，Gemini 提供商可以转成 GenerateContent。选中提供商后，用解析出的协议路线实际执行；换号后重新验证模型和路线。`previous_response_id`、WebSocket 和 Live 会话、签名，以及已经开始的流，都会限制跨提供商的切换。

Responses 图片策略和 Images 入口互相独立：`inherit` 沿用提供商和全局的设置；`enabled` 在条件满足时注入桥接；`disabled` 不主动注入，但允许客户端自己带图片工具；`block` 使用现有的工具剥离。分组的设置优先，作用于客户端的 Responses 和 WebSocket；Images 内部生成的 Responses 请求不受影响。

关闭生成入口后，已有视频和批量作业的读取、取消、下载和删除权限保持不变；自定义声音的子资源归属于声音协议；Live sideband 检查原会话的权限。辅助操作的归属：

| 操作 | 管理方式 |
| --- | --- |
| `/messages/count_tokens` | 归属 Messages，按平台原有的支持情况或本地估算 |
| `/responses/input_tokens` | 归属 Responses，按原有的端点能力限制 |
| Gemini `:countTokens` | 归属 Gemini GenerateContent |
| 原生 V2 压缩、HTTP continuation | Responses 的独立能力设置，不算新的协议 |
| Responses 图片工具 | 使用上文的 Responses 图片策略 |
| `/models`、`/usage` | 本地的模型和用量接口，按现有的认证权限处理 |
| 视频状态和内容查询 | 按已创建任务的归属和供应商绑定处理 |
| 批量图片的查询、下载、取消、删除 | 按作业生命周期处理 |
| Live Sideband | 归属 Live 会话，不增加协议项 |
| 自定义声音的读取、修改、删除、音频下载 | 归属自定义声音协议和它的资源权限 |

## 上游响应模型观测

管理员的用量记录按上游实际的响应格式采集模型名。Responses 的 JSON `model` 和流式的 `response.model`、Chat 的顶层 `model`、Messages 的 `model` 和 `message_start.message.model`、Gemini 的 `modelVersion`，都在响应改写之前读取。这覆盖了八个适用平台的原生、透传和转换分支；Bedrock 在 EventStream 解码之后读取，Vertex 复用对应的原生协议，Antigravity 在内部响应解包之后读取。

Responses WebSocket 的连接池、透传、HTTP 桥接和 Grok 兼容路径，每轮单独保存观测结果。原生 V2 压缩随 Responses 一起处理；旧版 Compact 只在实际响应里有模型声明时才采集，标准的 `response.compaction` 没有模型字段时留空。

SystemOne 在响应模型恢复之前采集顶层 `model`，缺失或非法用量的响应仍可以交付有效答案，处理规则见 [Jev 用量与价格](jev_upstream.md#systemone_usage)。

Qoder 当前的原始响应解析还没有确认模型声明的位置，转换后生成的客户端模型名不算观测结果。Live 和 Realtime、Embeddings、独立搜索、媒体、批量任务和计数接口不在采集范围内。模型声明只代表直接上游返回的身份，中转背后实际运行的模型无法据此确定。

## 旧字段输入与升级切换

历史的 `api_protocol`、OpenAI 文本路由和工作负载字段，只在接收旧输入时转换，新的保存和导出使用原生集合。历史缺省的 `payg + chat_completions` 转成只有 Chat 一项；新的 CN 表单默认启用全部原生项。旧的 `allowed_client_protocols` 输入只修改文本部分，它没有表达的非文本入口保持原样；旧的媒体和 Live 开关仍可作为兼容输入，新的响应不再返回它们。数据库内部保留派生的布尔镜像，供现有的任务和计价代码使用，管理员看到的配置只有原生集合这一份。

提供商的兼容转换在 `provider/protocol_legacy.go`，分组的兼容配置在 `routing/group_protocols.go`；提供商的规范化、分组策略和请求选路各自维护自己的规则。保存分组时，先校验一次原始的协议集合，再应用受支持的旧字段补丁，并校验转换目标和图片策略；集合非法时返回 HTTP 400 / `INVALID_ALLOWED_CLIENT_PROTOCOLS`，兼容转换掩盖不了重复或未知的协议。分组的默认准入和默认转换映射由 `routing/capability` 提供。

管理端在同一个版本里切换到无平台的分组和数组形式的转换目标，旧的分组 `platform`、`is_default`、按平台嵌套的模型策略和标量形式的转换目标，都明确返回 400。存量的入口许可和转换限制由迁移保留，新建时的默认值不会扩大已有分组的开放范围；数据库、认证缓存和调度快照需要一起切换，步骤见[部署与数据库迁移](../operations/deployment_and_migrations.md)。

相关文档：[提供商能力矩阵](upstream_provider_matrix.md)、[分组策略](../domains/gateway_policy_controls.md)、[调度缓存](../architecture/provider_scheduling_and_cache.md)。
