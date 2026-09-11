# 统一协议能力

本页拥有账号原生集合、分组客户端准入和单步转换配置契约。平台认证、载荷转换、模型与媒体资格由各平台专题维护。

<a id="protocol_catalog"></a>
## 唯一能力目录

后端 `routing/capability.ProtocolCatalog` 唯一维护 24 项纯能力目录，旧 domain 入口只作委托。管理员 `GET /api/v1/admin/protocol-capabilities` 返回 `protocols`、`accounts`、`groups` 和 `auxiliary_operations`；账号 profile 按平台、类型、认证方式提供原生选项，分组 profile 提供可用入口、默认集合、转换目标及默认映射。目录不包含凭据，前端共享只读结果，不维护平台白名单。

账号与分组共用 `protocol.ProtocolID`，平台与账号类型常量由 `routing/capability` 定义，domain 保留兼容别名。HTTP 方法、路径、别名和 WebSocket 标记在 `gateway/httpapi` 声明，实际路由门禁读取同一声明；app 将展示地址投影注入 `routing/httpapi` 的目录 handler，纯目录不依赖 HTTP Adapter。路由层只规范化别名前缀；文本入口沿用各自的原生错误格式与动作校验，Compact 在 Responses 子路径校验后检查。内部路由元数据不进入管理员 API 响应，协议 ID、JSON 字段和持久化格式保持稳定。

前端表单共用目录的加载、错误和重试状态。创建分组的默认准入集合与转换映射在目录就绪后一起初始化；编辑回显和管理员显式清空的集合不会因加载完成而恢复默认值。目录不可用时禁止提交分组，任一协议选择器重试成功后恢复共享状态。目录测试在普通运行中同时核对前端 JSON 夹具与 `ProtocolID` 联合类型；表单测试按需准备目录，不依赖全局预热。

| ID | 界面名称 | 主要入口 | 分组可用平台 |
| --- | --- | --- | --- |
| `anthropic_messages` | Anthropic Messages | `POST /v1/messages` | 现有九个平台 |
| `openai_responses` | OpenAI Responses | `POST /v1/responses` | 现有九个平台 |
| `openai_chat_completions` | Chat Completions | `POST /v1/chat/completions` | 现有九个平台 |
| `gemini_generate_content` | Gemini GenerateContent | `POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent` | Gemini、Antigravity |
| `openai_embeddings` | Embeddings | `POST /v1/embeddings` | OpenAI |
| `openai_images_generations` | Images 图片生成 | `POST /v1/images/generations` | OpenAI、Grok |
| `openai_images_edits` | Images 图片编辑 | `POST /v1/images/edits` | OpenAI、Grok |
| `image_batches` | 批量图片作业 | `POST /v1/images/batches` | Gemini，含 Vertex Service Account |
| `grok_videos_generations` | 视频生成 | `POST /v1/videos/generations` | Grok |
| `grok_videos_edits` | 视频编辑 | `POST /v1/videos/edits` | Grok |
| `grok_videos_extensions` | 视频扩展 | `POST /v1/videos/extensions` | Grok |
| `grok_tts` | 语音合成 TTS | `POST /v1/tts` | Grok |
| `grok_stt` | 语音识别 STT | `POST /v1/stt` | Grok |
| `grok_custom_voices` | 自定义声音 | `/v1/custom-voices` 及其子资源 | Grok |
| `grok_voice_realtime` | 实时语音 | `GET /v1/realtime`，WebSocket | Grok |
| `openai_responses_websocket` | Responses WebSocket | `GET /v1/responses`，WebSocket | OpenAI、Grok |
| `openai_live` | OpenAI Live | `POST /v1/live` | OpenAI |
| `openai_responses_compact` | Responses Compact | `POST /v1/responses/compact` | OpenAI；Grok 使用现有兼容实现 |
| `openai_alpha_search` | Alpha Search | `POST /v1/alpha/search` | OpenAI |
| `grok_web_search` | 网页搜索 | `POST /v1/web_search` | Grok |
| `grok_x_search` | X 搜索 | `POST /v1/x_search` | Grok |

共 21 个客户端控制项。

图片生成与图片编辑、视频生成与编辑与扩展分别控制。HTTP/SSE 共用所属协议项；Responses WebSocket 因其传输和会话要求独立控制。

已有无 `/v1` 前缀别名、`/backend-api/codex/*` 别名和 `/antigravity/*` 强制平台入口映射到相同协议 ID，不重复增加控制项。`POST /v1/videos` 归入视频生成。

### 上游专用协议

这些条目进入账号原生支持集合，不新增公共客户端 URL。

| ID | 界面名称 | 实际上游接口 | 适用账号 |
| --- | --- | --- | --- |
| `qoder_chat` | Qoder 原生对话 | Qoder COSY `agent_chat_generation` SSE 接口 | Qoder Cosy |
| `gemini_batch_generate_content` | Gemini Batch GenerateContent | `/v1beta/models/{model}:batchGenerateContent` | Gemini API Key |
| `vertex_batch_prediction` | Vertex Batch Prediction | `/v1/projects/{project}/locations/{location}/batchPredictionJobs` | Gemini Vertex Service Account |

共 3 个上游专用项，整个目录共 24 项。

批量图片属于持久作业入口：分组控制 `image_batches`，执行器检查账号启用的 Gemini Batch 或 Vertex Batch 协议，并沿用现有 provider 选择和任务绑定。这里不提供文本式的任意转换目标下拉框。

Antigravity 的 Google 内部封装归入 `gemini_generate_content` 的平台适配变体，不再增加一个同义协议复选框。Bedrock、Vertex Anthropic 等认证及端点变体同样归入对应协议族。


<a id="account_native_protocols"></a>
## 账号原生集合

旧 Account/Group 入口解析历史字段、缺省值与错误 reason，再将平台、账号类型、认证方式、启用协议和 fallback 映射投影到 `capability.AccountProtocols`。纯判断不读取凭据、配置或请求 Context；每次候选判断重新计算，显式空集合不会被默认补全。

`credentials.upstream_protocols` 是原生协议 ID 数组，显式空数组表示不承接新调用。创建、编辑、复制、导入与批量编辑共享校验，不允许重复、未知或账号认证方式不支持的项；错误为 HTTP 400 / `UPSTREAM_PROTOCOLS_INVALID`。批量更新先验证全部账号，再由同一 SQL 原子写入逐账号协议补丁。

OpenAI API Key 原生提供 Responses、Chat、Embeddings、Images 生成/编辑、Responses WebSocket、Compact、Alpha Search；OAuth 提供 Responses、WebSocket、Compact、Alpha Search、Live，PAT 去掉 Alpha Search/Live，Agent Identity 去掉 Live。OpenAI 不显示 Messages 原生项。Grok 的 HTTP Responses、Chat、Images、视频、Voice 是原生项，WebSocket/搜索/Compact 是转换入口。CN API Key 使用 Messages/Chat，DeepSeek 和 Kimi 另有 Responses。Gemini、Antigravity OAuth 使用 GenerateContent（历史 upstream 类型保留 Messages 直连），Qoder 使用专用 Chat；Gemini API Key 和 Vertex SA 分别使用各自 Batch 协议。

自定义 `api_base_urls` 保留原有分协议键 `chat_completions`、`anthropic`、`responses`。旧 CN 固定协议的 `base_url` 迁入对应地址槽，原 `base_url` 仍保留给模型同步和用量查询。新表单直接编辑地址与原生集合，二者独立。

<a id="group_protocol_routes"></a>
## 分组准入与转换

分组返回 `allowed_protocols`、`protocol_fallbacks` 和 `responses_image_policy`。空准入集合关闭全部新入口，空映射表示仅原生。每个候选账号先尝试已启用的原生协议，否则只使用该源在分组中指定的目标；目标必须启用且有对应平台/认证方式的转换器。目标无需作为客户端入口开放。没有可行路线的候选在评分之前排除，切号重新解析；转换不能绕过模型、额度、媒体资格、WebSocket 模式和会话约束。

支持的转换以目录的 `fallback_targets` 为准，不提供任意多跳或排序。文本可使用现有平台适配；OpenAI OAuth Images 转 Responses，Grok WebSocket 转 HTTP Responses，Grok 搜索/Compact 转 Responses，PAT Alpha Search 转 hosted web search。批量图片由既有 provider 绑定选择 Gemini Batch 或 Vertex Batch，不提供文本式转换下拉。

Responses 图片策略独立于 Images 入口：`inherit` 沿原账号/渠道/全局链解析；`enabled` 在适用条件下注入桥接；`disabled` 不主动注入但允许显式图片工具；`block` 使用既有工具剥离。分组显式覆盖优先，作用于客户端 Responses 和 WebSocket，不作用于 Images 内部生成的 Responses 请求。

关闭生成入口不会改变已有视频或批量作业的读取、取消、下载和删除权限；自定义声音子资源仍归属于声音协议，Live sideband 继续检查原会话权限。辅助操作沿以下归属管理。

| 操作 | 管理方式 |
| --- | --- |
| `/messages/count_tokens` | 归属 Messages，保留平台原有支持／本地估算边界 |
| `/responses/input_tokens` | 归属 Responses，保留原有端点能力限制 |
| Gemini `:countTokens` | 归属 Gemini GenerateContent |
| 原生 V2 压缩、HTTP continuation | Responses 的独立能力设置，不视为新的协议 |
| Responses 图片工具 | 使用下述独立图片策略 |
| `/models`、`/usage` | 本地模型和用量接口，继续按现有认证权限处理 |
| 视频状态和内容查询 | 按已创建任务的归属与供应商绑定处理 |
| 批量图片查询、下载、取消、删除 | 按既有作业生命周期处理 |
| Live Sideband | 归属 Live 会话，不新增协议项 |
| 自定义声音的读取、修改、删除、音频下载 | 归属自定义声音协议及其资源权限 |

## 兼容与切换

历史 `api_protocol`、OpenAI 文本路由和工作负载字段只在旧输入边界转换，新保存和导出使用原生集合。历史 `payg + chat_completions` 缺省值转成 Chat 单项；新 CN 表单默认启用全部原生项。旧 `allowed_client_protocols` 输入只修改文本部分，未表达的非文本入口保留；旧媒体/Live 开关仍接受兼容输入，新响应不再暴露它们。数据库内部保留派生布尔镜像供既有任务/计价消费者使用，不是第二份管理员配置。

兼容转换集中在服务层 `protocol_legacy.go`；账号规范化、分组策略和请求选路分别拥有当前配置规则。分组保存先校验原始协议集合一次，再应用受支持的旧字段补丁并校验转换目标与图片策略；非法集合继续返回 HTTP 400 / `INVALID_ALLOWED_CLIENT_PROTOCOLS`，兼容转换不会掩盖重复或未知协议。分组默认准入与默认转换映射均由 domain 提供。

迁移 273、认证缓存 v40 和调度缓存 `sched:v2:` 一次切换，步骤见[部署与数据库迁移](../operations/deployment_and_migrations.md)。

相关文档：[账号能力矩阵](upstream_account_matrix.md)、[分组策略](../domains/gateway_policy_controls.md)、[调度缓存](../architecture/account_scheduling_and_cache.md)。
