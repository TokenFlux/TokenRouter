# 统一协议能力

本文定义提供商的原生协议集合、分组的客户端准入和单步转换配置。平台认证、载荷转换、模型和媒体资格见各平台文档。

<a id="protocol_catalog"></a>
## 能力目录

25 项能力目录只在后端 `routing/capability.ProtocolCatalog` 维护。管理员接口 `GET /api/v1/admin/protocol-capabilities` 返回 `protocols`、`providers`、`groups` 和 `auxiliary_operations`：

- `providers`：按平台、类型和认证方式列出原生协议选项。
- `groups`：单元素数组，包含一个不带 `platform` 的通用 profile，列出所有分组可用入口、默认集合、转换目标和默认映射。`defaults` 只含 Messages、Responses、Chat 三个文本入口，`default_fallbacks` 为 `{}`。
- `probe_protocols`：来自提供商测试能力，包含 Messages、Responses、Chat Completions、Gemini GenerateContent、SystemOne 和图片生成。探针协议下拉取它与分组当前 `allowed_protocols` 的交集，并提供「自动」选项；Compact 等专项测试不在此集合中。
- 只读共享：目录不含凭据，前端共用该只读结果，不维护独立平台白名单。

Responses WebSocket 的客户端许可在分组协议控制中设置，提供商协议集合声明上游能力；连接方式和在线调参见 [Responses 长连接与在线参数](openai_upstream.md#responses_ws_runtime)。

- 模块边界：提供商和分组共用 `protocol.ProtocolID`，平台与提供商类型常量定义在 `routing/capability`。HTTP 方法、路径、别名和 WebSocket 标记声明在 `gateway/httpapi`，路由门禁与目录展示共用同一份声明，目录核心不依赖 HTTP 适配层。
- 路由校验与稳定性：路由层只规范化别名前缀，各文本入口按原生错误格式和动作校验，Compact 在 Responses 子路径校验后检查。内部路由元数据不出现在管理员 API 响应中，协议 ID、JSON 字段和持久化格式保持稳定。
- 前端加载状态：表单共用目录的加载、错误和重试状态。创建分组时，默认准入集合和转换映射在目录加载完成后一起初始化；编辑时回显值和管理员明确清空的集合在加载完成后保持原样。目录不可用时禁止提交分组，任一协议选择器重试成功后共享状态恢复。目录测试在普通运行中核对前端 JSON 夹具与 `ProtocolID` 联合类型；表单测试按需准备目录，不依赖全局预热。

### 客户端协议控制项（22 项）

| ID | 界面名称 | 主要入口 | 候选提供商的适配器范围 |
| --- | --- | --- | --- |
| `anthropic_messages` | Anthropic Messages | `POST /v1/messages` | 现有的十个文本平台 |
| `openai_responses` | OpenAI Responses | `POST /v1/responses` | 现有的十个文本平台 |
| `openai_chat_completions` | Chat Completions | `POST /v1/chat/completions` | 现有的十个文本平台 |
| `gemini_generate_content` | Gemini GenerateContent | `POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent` | Gemini、Antigravity、OpenCode |
| `systemone` | SystemOne | `POST /v1/systemone` | Jev、OpenCode |
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

- 适配器范围列表示候选提供商与转换器的实际支持范围，所有分组均可配置这些入口；开放入口不会自动产生可用提供商。
- 图片生成与编辑分开控制，视频生成、编辑和扩展分开控制；HTTP 与 SSE 共用所属协议控制项，Responses WebSocket 因独立传输和会话要求单独控制。
- 无 `/v1` 前缀别名、`/backend-api/codex/*` 别名和 `/antigravity/*` 强制平台入口映射到相同协议 ID，不增加控制项；`POST /v1/videos` 归入视频生成。

### 上游专用协议（3 项）

以下三项仅出现在提供商原生支持集合中，无公共客户端 URL，与上述 22 项合计组成 25 项能力目录：

| ID | 界面名称 | 实际的上游接口 | 适用的提供商 |
| --- | --- | --- | --- |
| `qoder_chat` | Qoder 原生对话 | Qoder COSY 的 `agent_chat_generation` SSE 接口 | Qoder Cosy |
| `gemini_batch_generate_content` | Gemini Batch GenerateContent | `/v1beta/models/{model}:batchGenerateContent` | Gemini API Key |
| `vertex_batch_prediction` | Vertex Batch Prediction | `/v1/projects/{project}/locations/{location}/batchPredictionJobs` | Gemini Vertex Service Account |

- 批量图片作业：分组控制 `image_batches`，执行器检查提供商启用 Gemini Batch 还是 Vertex Batch 并沿用现有 provider 选择和任务绑定，无文本类可选转换目标。
- 批量候选选择：目录与提交使用同一候选资格，Vertex 新建作业还须配置受管 GCS 存储桶。自动选择按提供商全局优先级数值升序、ID 升序处理 Gemini API 和 Vertex 候选；批量 API、worker 和清理不需要额外启用开关。
- 平台变体归属：Antigravity 对 Google 内部接口的封装归入 `gemini_generate_content` 平台适配，无单独复选框；Bedrock、Vertex Anthropic 等认证与端点变体同理归入对应协议族。

<a id="account_native_protocols"></a>
## 提供商原生集合

提供商历史字段、缺省值和错误 reason 解析与保存校验归 `provider`，分组配置归 `routing`。

提供商整理出 `capability.ProviderProtocols`（平台、类型、认证方式、启用协议）；选路时使用不含凭据的 `provider.ProviderSnapshot`，由 `routing.RoutePlan.ResolveCandidate` 结合分组协议策略执行纯计算。

- 纯计算约束：候选解析不读取凭据、配置或请求 Context，每次判断重新计算，显式空集合不被默认值补齐。Key 和分组映射先于此步骤，权限、资金检查和重试按既定顺序执行。
- 模型与协议解析：`RoutePlan` 同时解析提供商一跳模型映射和协议并保存于本次 attempt。按模型声明协议的提供商（目前是 OpenCode）在快照中携带按模型协议表，先按最终模型收窄可用协议再应用转换规则；其他提供商所有模型共用已启用协议。
- `credentials.upstream_protocols` 校验：原生协议 ID 数组，空数组表示不再承接新调用。创建、编辑、复制、导入和批量编辑共用校验，拒绝重复、未知或认证方式不支持的项，失败返回 HTTP 400 / `UPSTREAM_PROTOCOLS_INVALID`。
- 批量原子更新与连接方式：批量更新先验证全部提供商，再经单条 SQL 原子写补丁。显式协议集合在旧 WS 桥接配置转换前完成类型、重复项和选项校验；连接方式的完整保存、批量更新和 Extra 按键更新共用枚举校验，非法值写入前报错。
- 自定义 `api_base_urls`：按协议分键 `chat_completions`、`anthropic`、`responses`，OpenCode 另有 `gemini`、`systemone`。Kimi、Zhipu、DeepSeek 旧配置中的固定协议 `base_url` 迁至对应地址槽，原 `base_url` 继续用于模型同步和用量查询；新表单直接编辑地址和原生集合，互不依赖。

OpenAI API Key 图片流模式：

- `credentials.images_generations_mode` 与 `credentials.images_edits_mode` 分别声明生成和编辑端点接受的模式：`non_streaming`、`streaming`、`both`（默认 `both`）。模式配置不开启协议，关闭协议时已保存模式保留。
- 创建、编辑、批量更新、复制和导入共用校验，非法枚举或不适用提供商类型返回 `IMAGE_STREAM_MODE_INVALID`。两个字段按独立顶层键合并，批量修改生成模式不覆盖编辑模式。
- 客户端 `stream` 决定返回 JSON 或 SSE：选中提供商原生支持则直用，否则网关自动双向转换（属 Images 协议内部处理，不受分组 `protocol_fallbacks` 控制）。
- 平台差异：OpenAI OAuth 继续使用 Responses 图片工具适配，Grok 使用自有媒体执行器，见 [图片结果回填与模式转换](openai_upstream.md#images_url_backfill)。

各平台原生协议如下：

| 平台与认证类型 | 原生协议 |
| --- | --- |
| OpenCode API Key | 按模型声明的 Messages、Responses、Chat、GenerateContent 或 SystemOne（见 [OpenCode 上游](opencode_upstream.md#opencode_routing)） |
| Jev API Key | SystemOne（同步文本、图片决策及 Box 定位；图片和 Box 需显式实验启用头） |
| OpenAI API Key | Responses、Chat、Embeddings、Images 生成与编辑、Responses WebSocket、Compact、Alpha Search（无 Messages 原生项） |
| OpenAI OAuth | Responses、WebSocket、Compact、Alpha Search、Live（PAT 无 Alpha Search 和 Live；Agent Identity 无 Live；无 Messages 原生项） |
| Grok | HTTP Responses、Chat、Images、视频和 Voice（WebSocket、搜索和 Compact 为转换入口） |
| Kimi、Zhipu、DeepSeek API Key | Messages 和 Chat；DeepSeek 和 Kimi 另有 Responses |
| Gemini、Antigravity OAuth | GenerateContent（Antigravity API Key 和历史 `upstream` 类型无可用协议） |
| Qoder | 专用 Chat（`qoder_chat`） |
| Gemini API Key / Vertex SA | 各自的 Batch 协议（`gemini_batch_generate_content` / `vertex_batch_prediction`） |

<a id="group_protocol_routes"></a>
## 分组准入与转换

分组返回 `allowed_protocols`、`protocol_fallbacks` 和 `responses_image_policy`。`allowed_protocols` 为空时关闭所有新入口。`protocol_fallbacks` 按入口保存有序目标数组：省略入口时自动匹配目录转换路线，空数组 `[]` 只允许原生协议，非空数组限定允许目标和尝试顺序。

候选路由按以下顺序解析：

1. 优先尝试候选提供商已启用的原生协议。
2. 否则选择第一个可执行且允许的单步转换目标（须在提供商启用，且存在对应平台和认证方式的单步转换器；转换目标无需同时开放为客户端入口）。
3. 找不到可行路线的候选在评分前排除，换号时重新解析；转换后模型、额度、媒体资格、WebSocket 模式和会话约束照常生效。

- 单步文本转换：支持转换及自动顺序以目录 `fallback_targets` 为准，管理员可缩小集合、调序或关闭，但仅支持一步转换。同分组不同候选可解析出不同目标协议（如 Chat 请求在 OpenAI 原生处理、Anthropic 转 Messages、Gemini 转 GenerateContent）。
- 专项转换路线：OpenAI OAuth Images 转 Responses、Grok WebSocket 转 HTTP Responses、Grok 搜索和 Compact 转 Responses、PAT Alpha Search 转 hosted web search；批量图片由已有 provider 绑定选 Gemini Batch 或 Vertex Batch。
- 跨提供商切换约束：选中后按解析路线执行，换号后重验模型和路线。`previous_response_id`、WebSocket 和 Live 会话、签名及已开始的流会限制跨提供商切换。

`responses_image_policy` 与 Images 入口互相独立，分组设置优先，作用于客户端 Responses 和 WebSocket，不影响 Images 内部生成的 Responses 请求：

| 策略值 | 行为 |
| --- | --- |
| `inherit` | 沿用提供商和全局设置 |
| `enabled` | 条件满足时注入桥接 |
| `disabled` | 不主动注入，允许客户端自带图片工具 |
| `block` | 使用现有工具剥离 |

关闭生成入口后，已有视频和批量作业的读取、取消、下载和删除权限保持不变；自定义声音子资源归属声音协议；Live sideband 检查原会话权限。辅助操作归属如下：

| 操作 | 管理方式 |
| --- | --- |
| `/messages/count_tokens` | 归属 Messages，按平台原有支持情况或本地估算 |
| `/responses/input_tokens` | 归属 Responses，按原有端点能力限制 |
| Gemini `:countTokens` | 归属 Gemini GenerateContent |
| 原生 V2 压缩、HTTP continuation | Responses 独立能力设置，不算新协议 |
| Responses 图片工具 | 使用上文 `responses_image_policy` |
| `/models`、`/usage` | 本地模型和用量接口，按现有认证权限处理 |
| 视频状态和内容查询 | 按已创建任务归属和供应商绑定处理 |
| 批量图片查询、下载、取消、删除 | 按作业生命周期处理 |
| Live Sideband | 归属 Live 会话，不增加协议项 |
| 自定义声音读取、修改、删除、音频下载 | 归属自定义声音协议及其资源权限 |

<a id="client_usage_cache"></a>
## 客户端用量与缓存

文本响应的客户端用量与后台结算使用同一次上游观察。每次上游尝试独立持有快照。

- 输入桶口径：Messages 的 `input_tokens` 仅含普通输入，缓存读、写分放 `cache_read_input_tokens` 和 `cache_creation_input_tokens`，而 Chat `prompt_tokens` 与 Responses `input_tokens` 包含这三个输入桶。转换时只相加一次，缓存写入聚合数与 TTL 分档不重复计数。强制覆盖缓存 TTL 时以正数 `cache_creation_input_tokens` 为权威总量，缺失正数总量时才用非负分档之和。
- OpenAI Responses 提示缓存：原生路由保留 `prompt_cache_options` 和内容块上的 `prompt_cache_breakpoint`，显式缓存 TTL 被上游拒绝时兼容重试保留该语义。
- Responses 缓存写入观测与结算：读取 `input_tokens_details.cache_write_tokens_details` 的 `30m` 和 `12h` 原始计数交给[缓存写入分档](../domains/routing_and_billing.md#cache_write_pricing)统一归档计费。外层写入总数为 0 时由正数分档之和回填有效总数，合成客户端响应报文时仅在双键非负且相加等于外层总数时输出明细。流式读取器合并稀疏用量并保留已观测分档，终态明确报告完整零用量时按既有规则清空或保留整份快照。
- Gemini 与 OpenAI 兼容别名：Gemini 的 `cachedContentTokenCount` 映射到目标协议缓存读取字段（流式与非流式一致）。OpenAI 兼容响应支持标准嵌套明细和顶层别名（含 DeepSeek `prompt_cache_hit_tokens`、`prompt_cache_miss_tokens`）；标准嵌套优先，显式零不被替换，未报告数不估算。
- 转换读取器合并规则：按字段合并不完整用量帧，再将同一份快照交给客户端转换和计费。仅有输出数的尾帧保留先前输入与缓存数；OpenAI 完整显式零用量可清空旧快照；Messages 保留累计字段与 TTL 分档更新规则。Responses 转换中，终态内嵌 `response.usage` 已报告字段优先于同帧顶层 `usage`，未报告字段从本次请求已有观察补齐。
- 图片桶与容错：OpenAI 转换计费快照保留图片输入、输出桶，文本客户端类型未声明也不丢弃。图片明细缺失沿用已有观察，显式零更新对应桶；hosted image 明细仅补同一 envelope 中 usage 的零图片计数。Chat 帧因字段类型不兼容无法转换时，原始用量解析器可读计数仍参与累计；无法解析为有效 JSON 对象的 usage 不更新快照。
- 输出与透传策略：Messages 缓存写入分档保留在标准 `cache_creation` 明细中；转 Chat 或 Responses 时保留写入总数，不增加私有 TTL 字段。Chat usage 输出遵循所属适配器原有策略，关闭可选 usage 不代表缓存未命中。原生透传保留上游字段；SSE 汇总为 JSON 时，仅在已有观察能补齐终态用量时修补计数，保留其他字段与原始数值表示。

## 上游响应模型观测

管理员用量记录在响应改写之前按上游原始响应格式采集模型名，覆盖 8 个适用平台的原生、透传和转换分支：

| 协议或平台 | 采集字段与时机 |
| --- | --- |
| Responses | JSON `model`、流式 `response.model`；原生 V2 压缩随 Responses 处理；旧版 Compact 仅在响应含模型声明时采集，标准 `response.compaction` 无模型字段留空 |
| Responses WebSocket | 连接池、透传、HTTP 桥接和 Grok 兼容路径每轮单独保存观测 |
| Chat | 顶层 `model` |
| Messages | `model`、`message_start.message.model` |
| Gemini / Bedrock / Vertex / Antigravity | Gemini `modelVersion`；Bedrock 在 EventStream 解码后读取；Vertex 复用对应原生协议；Antigravity 在内部响应解包后读取 |
| SystemOne | 响应模型恢复之前采集顶层 `model`，缺失或非法用量仍可交付答案（见 [Jev 用量与价格](jev_upstream.md#systemone_usage)） |

Qoder 原始响应解析尚未确认模型声明位置，转换后生成的客户端模型名不算观测结果。Live/Realtime、Embeddings、独立搜索、媒体、批量任务和计数接口不在采集范围。模型声明只反映直接上游返回的身份，无法据此确定中转背后实际运行的模型。

## 旧字段输入与升级切换

- 提供商兼容（`provider/protocol_legacy.go`）：历史 `api_protocol`、OpenAI 文本路由和工作负载字段只在接收旧输入时转换，新的保存和导出使用原生集合。历史缺省 `payg + chat_completions` 转为仅 Chat 一项；Kimi、Zhipu、DeepSeek 新表单默认启用全部原生项。
- 分组兼容输入（`routing/group_protocols.go`）：旧 `allowed_client_protocols` 只修改文本部分，非文本入口保持原样；旧媒体和 Live 开关保留为兼容输入，新响应不返回。数据库内部保留派生布尔镜像供现有任务和计价使用，管理员界面仅展示原生集合。
- 分组保存校验：保存时先校验原始协议集合（非法返回 HTTP 400 / `INVALID_ALLOWED_CLIENT_PROTOCOLS`，兼容转换不掩盖重复或未知协议），再应用受支持的旧字段补丁并校验转换目标和图片策略；默认准入与转换映射由 `routing/capability` 提供。
- 废弃字段与同步切换：旧分组 `platform`、`is_default`、按平台嵌套模型策略和标量转换目标明确返回 400。存量入口许可和转换限制由迁移保留，新建默认值不扩大已有分组开放范围；数据库、认证缓存和调度快照需一并切换，步骤见[部署与数据库迁移](../operations/deployment_and_migrations.md)。

相关文档：[提供商能力矩阵](upstream_provider_matrix.md)、[分组策略](../domains/gateway_policy_controls.md)、[调度缓存](../architecture/provider_scheduling_and_cache.md)。
