# OpenAI 上游

本文记录 OpenAI 平台（OAuth 与 API Key）的凭据规则、协议转发、图片与 GIF 处理、Fast 与推理档位策略、远程压缩、WebSocket 长连接及错误处理。各平台完整支持矩阵见[上游提供商能力矩阵](upstream_provider_matrix.md)，动态模型列表由[模型目录与市场](model_catalog_and_marketplace.md#model_catalog_resolution)解析。

## 章节导航

- [提供商与凭据](#提供商与凭据)：OAuth/API Key 区别、凭据边界、Codex 指纹与会话头。
- [协议与传输](#openai_protocol_dispatch)：支持的协议族、原生协议配置与报文转换规则。
- [图片与 GIF](#images_url_backfill)：Images 流式/非流式转换、URL 回填、创作台适配与 GIF 计费。
- [Fast 与推理档位](#openai_fast_policy)：Fast/Ultra Fast 优先级、免费 Fast 计费与推理强度上限。
- [远程压缩协议](#远程压缩协议)：原生 `remote_compaction_v2` 与旧版 `/responses/compact` 对比。
- [Responses 请求细节](#responses-请求细节)：WS 长连接与调参、预热续接、路由提示、回合状态、Lite、namespace 与 Chat 降级。
- [模型与能力](#模型与能力)：完整型号规则、工具能力校验与图片模型检查。
- [额度与调度](#额度与调度)：高级调度评分信号、上游窗口与额度刷新。
- [失败与诊断](#失败与诊断)：错误分类、池模式重试、瞬时冷却与流内容错。

## 提供商与凭据

OpenAI 正式支持 `oauth` 和 `apikey` 两类提供商：

| 提供商类型 | 凭据与关键配置 | 适用说明 |
| --- | --- | --- |
| `oauth` | access/refresh token、组织上下文、Codex 元数据、设备指纹模式 | 后台与请求路径均触发刷新；受客户端白名单、privacy、PAT 与 Agent Identity 约束 |
| `apikey` | API Key、base URL、原生协议集合、图片模式、压缩开关 | 不使用 OAuth 专用端点与 Codex 身份头；受出站代理、TLS 与目标校验约束 |

### 凭据与边界约定

- 令牌与刷新：OAuth 授权会话、刷新锁和共享令牌缓存由 `provider` 统一管理；每次上游调用实时读取最新的代理、TLS 与 Codex UA，不在调用层缓存副本。
- Agent Identity：任务锁、锁内复查与凭据登记由 `provider` 协调，请求签名与脱敏在网关适配层完成，上游交换与加解密归 `upstream/openai`。
- 影子提供商与 setup-token：Spark 影子提供商在执行时读取母提供商凭据，普通提供商不触发额外查询；`setup-token` 不参与自动刷新。
- 额度恢复预算：请求完成后的额度恢复有独立 8 秒超时，不受客户端断连影响，进程退出时取消并等待结束。
- OAuth 套餐归属：ID token 中的个人 `chatgpt_plan_type` 是个人套餐的权威来源。`accounts/check` 仅在账户 ID 与 `chatgpt_account_id` 一致时合并其 `entitlement.expires_at`；不一致时改读 `/backend-api/subscriptions` 的 `active_until`。若套餐本身取自 `accounts/check`，到期时间取自同一条记录。

### 客户端身份与 Codex 设备指纹

- 默认身份：OAuth 出站 `originator` 须与最终 User-Agent 首段配对。客户端未提供可识别的官方身份或修复失败时，统一回退为 `codex-tui`（PAT、探测、Alpha Search、HTTP 与 WS 一致）。历史 `codex_cli_rs` 仅作兼容识别。
- 安全边界：Header override、代理、base URL 和 TLS 配置受[上游传输安全](../operations/upstream_transport_security.md)约束，无法覆盖受保护认证头或绕过目标校验。
- 指纹模式：OAuth 提供商的 `extra.codex_fingerprint_mode` 缺省或无效时按 `off` 处理，手动选项如下：

| 模式 | 统一范围 |
| --- | --- |
| `device` | 仅统一 installation ID |
| `session` | 统一 installation ID 与 session ID，按客户端原始 session 稳定派生 thread ID |
| `full` | 将所有客户端统一到同一 thread |

`session` 和 `full` 模式下，每个请求重新生成 turn ID，同一次请求的 HTTP 头、`client_metadata` 与内嵌 turn metadata 共用该 ID，内部重试不重新派生。透传大请求体仅局部改写 `client_metadata`；旧版 `/responses/compact` 不受此配置影响。管理员手动配置的 device ID 优先于派生值；Spark 影子提供商继承母提供商的模式、device ID 与种子。

### 会话头优先级

手动粘性会话头按以下顺序读取，均缺失时回退到 `prompt_cache_key`（WS 会话日志沿用相同顺序）：

1. `session-id`（Codex 客户端连字符写法）
2. `session_id`
3. `conversation_id`
4. OpenCode 会话头
5. CodeBuddy 会话头

Messages 转 OpenAI 的提示缓存摘要按提供商与 API Key 隔离命名空间，取最长有效前缀并按 TTL 过期，替换时先写新链再删旧链（纯内存、无后台清理）。

<a id="openai_protocol_dispatch"></a>
## 协议与传输

| 协议族 | 处理方式 |
| --- | --- |
| Responses HTTP/SSE | OAuth 与 API Key 原生转发，支持允许的 `/responses/*` 子路径 |
| Responses WebSocket | 按提供商原生协议与分组转换规则决定走 WS 或 HTTP 桥接；流开始后不换提供商 |
| Chat Completions | API Key 默认原生转发，分组策略强制时转 Responses；每次尝试重建协议状态 |
| Anthropic Messages | 转换为 OpenAI 请求，再将事件、工具、thinking 与 usage 转回 Anthropic 格式 |
| Embeddings | 仅限启用了 `embeddings` 能力的 OpenAI 提供商 |
| Images | 同步生成与编辑生命周期；批量图片归 Gemini/Vertex，见[批量图片作业](../domains/batch_image_jobs.md) |
| Realtime、Live、sideband、Alpha Search | 仅限 OpenAI 提供商，受分组开关、提供商类型与传输能力约束（Live 计费为零） |

路由与透传要点：

- 透传仅控制报文与传输方式，手动配置的提供商模型映射仍执行一次；HTTP 出站只局部替换 `model`，保留原始请求模型与最终上游模型用于响应回填、日志和计费。
- OAuth 硬能力限制不会因透传或 `*` 白名单关闭。
- `/backend-api/codex` 和裸路径别名同样经过 API Key 鉴权、分组准入、调度和结算。Responses WebSocket 不支持 Qoder。

<a id="openai_account_configuration"></a>
### 原生协议配置与工具约束

- 提供商统一使用 `credentials.upstream_protocols` 声明原生能力，完整矩阵见[统一协议能力](protocol_capabilities.md#account_native_protocols)。API Key 可分别勾选 Responses、Chat、Embeddings、Images 生成/编辑、Responses WS、Compact 和 Alpha Search；OAuth 随 PAT/Agent Identity 收窄，不开放 Messages、Chat 和原生 Images。
- 分组通过 `allowed_protocols` 控制客户端入口（新建分组默认开放 Messages、Responses、Chat），通过 `protocol_fallbacks` 指定单步转换目标；提供商具备原协议时直接转发。
- `extra.openai_responses_continuation_supported` 与压缩开关独立管理，不计入文本协议集合。创建、更新、复制和批量编辑不会自动探测协议。
- Chat 函数工具限制：Chat 请求携带普通 `function` 或旧式 `functions` 工具时，模型映射后跳过 `gpt-6.2-astra` 和 `gpt-6.2-sol` 的原厂 Chat 路线（同名中转支持时仍可选中；纯文本或仅含 `custom` 工具仍可走原厂 Chat）。跳过原厂不会隐式触发对该候选的协议转换；因工具不兼容耗尽候选时返回 `400 invalid_request_error`，提示改用 `/v1/responses`。

### 报文、流生命周期与 usage 兼容

协议编解码与跨协议转换归 `protocol/openai` 和 `protocol/bridge`，平台单次执行与响应读取归 `upstream/openai`，全局故障转移循环统一在 `gateway/text`（WS 在 `gateway/ws`）。跨协议转换合并稀疏用量时优先采用嵌套终态字段，区分显式零与缺失字段，详见[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)。

HTTP Responses（标准流与透传流）额外兼容以下第三方事件形态（仅限 HTTP，不影响 WS 与其他协议）：

- `event` 行声明 created、in_progress 或终态，但 `data` 是带 `object=response` 和 ID 的裸 Response 时，补齐 `type`、`response` 包装与事件序号。
- 旧版 `response.done` 按实际 `status` 转为 completed、failed 或 incomplete；缺少 `status` 时仅凭非空 `error` 或 `incomplete_details` 补齐，无证据则保留原事件。
- 同步 `event` 行与 JSON `type`；规范化后再判断前置独立 `error` 是否抑制，不吞掉失败终态。
- 同一 Response ID 的重复成功终态仅下发一次，后续用量仍参与解析；缺少终态的断流不会补成成功。

非流式 OpenAI 兼容响应的 usage 按 `usage` > `response.usage` > `data.usage` > `data.response.usage` 顺序解析，同层读取 hosted image usage，避免混用不同 envelope。

推理历史缓存（`ReasoningHistory`）保留客户端 reasoning item ID，TTL 7 天、单次操作预算 2 秒；读取失败放行，写入失败仅记日志，缓存过期或丢失后不可恢复。

<a id="images_url_backfill"></a>
## 图片与 GIF

### 模式转换、心跳与容错

OpenAI API Key 的图片生成与编辑分别由 `credentials.images_generations_mode` 和 `credentials.images_edits_mode` 控制（缺省按 `both`）：

| 模式 | 行为 |
| --- | --- |
| `both` | 跟随客户端请求 |
| `non_streaming` | 从 JSON/multipart 移除 `stream` 和 `partial_images` |
| `streaming` | 强制 `stream=true`；客户端要求非流式时追加 `partial_images=0` |

图片处理的核心不变量：

- 每次尝试从原始请求重建载荷；JSON 顶层重复键先收拢去重再改写，嵌套字段与大整数保留原值。
- 客户端断开脱钩：Images 流式与非流式请求均与客户端取消信号脱钩，由上游响应超时回收，避免中途断开丢弃已产生成本的结算。
- 保活心跳：`stream=true` 在每次上游发送前启动 SSE 注释心跳（`gateway.images.stream_keepalive_interval_seconds`，默认 10 秒，`0` 关闭）；非流式 OAuth 图片在等待上游期间发送 JSON 空白心跳（不计为实际图片产出）。
- 双向格式转换：JSON 转 SSE 会先校验完整文档与非空 `data[]`，URL 项自动安全下载补齐 `b64_json`（不依赖 `images_url_to_b64_json`），总 usage 随最后一张图发出后发送 `data: [DONE]`；SSE 转 JSON 汇总去重完成图片，客户端要 `url` 而仅得 Base64 时返回 data URL。
- 计数与错误处理：HTTP 200 内含 `error` 对象、空结果或完成张数少于请求张数均按失败处理。`image_generation.completed` / `image_edit.completed` 按真实图片内容计数并去重；部分图片事件不计入完成数。未提供 `output_tokens_details` 时输出 token 计为图片输出（显式细分含零值优先）。流式客户端在已输出内容或心跳后遇错时收到 `error` 事件且不发 `[DONE]`；已观测的完成图片与用量在失败时保留，不重新提交生图。

### URL 回填与安全下载

API Key 提供商设置 `extra.images_url_to_b64_json=true`（默认关闭）后，非流式 `/images/generations` 和 `/images/edits` 响应中缺少 `b64_json` 但含 URL 的项会自动下载回填：

- 已有 `b64_json`、显式要求 `response_format=url` 或流式请求保持原样；单张下载失败跳过该项，保留原 URL 与上游元数据。
- 计费张数、尺寸与 usage 始终以回填前的上游响应为准。
- 安全下载复用提供商代理，不附带认证头与客户端 Cookie；单张上限 20 MiB、超时 60 秒，须通过字节嗅探确认为 PNG/JPEG/WebP/GIF（data URI 同样校验），并执行逐跳公网与重定向安全检查。

### 创作台与 OAuth 主控模型

- 创作台请求：`generate` 走 `/v1/images/generations`（JSON），`edit` / `inpaint` 走 `/v1/images/edits`（multipart，inpaint mask 须为同尺寸且不超过 4 MiB 的 PNG）。默认 PNG、`n=1`（GPT Image 2.6 支持 GIF）；仅 DALL-E 附带 `response_format=b64_json`。
- 创作台图片下载：缺少 `b64_json` 时强制走安全下载器获取字节（不依赖 `images_url_to_b64_json`），最多重试 3 次、间隔 1 秒。失败返回 `IMAGE_DOWNLOAD_FAILED`，无有效图片返回 `INVALID_IMAGE_RESPONSE`，均结束任务并释放预占，不重排生图，错误消息不泄露签名 URL。
- OAuth 生图主控模型：OpenAI OAuth 生图、编辑、后台图片测试及 Responses 顶层图片模型转换，使用 `extra.codex_image_responses_model` 作为文本承载模型（缺省或空值用 `gpt-6-luna`；API Key 转换默认 `gpt-5.4-mini`）。上游 HTTP 400 明确拒绝主控模型时，返回 `image_main_model_unsupported` 并直接结束请求，不写图片冷却也不自动切换主控模型。

### GPT Image 2.6 与 GIF

- 模型与参数：`gpt-image-2.6-sunburst` 与 `gpt-image-2.6-flare` 开放 `xhigh` / `max` 质量档。GIF（`output_format=gif`）接受 `frame_count`（2 到 16，默认 9）和 `frame_duration_ms`（20 到 2000，默认 200，按 10ms 舍入）；显式 `null` 等价于省略。尺寸为 `auto` 或两边均为 16 倍数的正整数。
- 资格开关：`extra.images_gif_enabled` 默认开启，显式 `false` 关闭；配置为仅流式的 API Key Images 端点不接收 GIF。GIF 请求不接受 `stream=true`、`partial_images`、mask 或 `output_compression`。OAuth 经 Responses 图片工具发送动画参数，不产出中间预览帧。
- 校验与计费：HTTP、SSE 和 WS 均按完整 GIF 容器字节解析真实 MIME、尺寸与帧数（允许返回帧数超出 2 到 16），容器损坏或无帧时按结果失败处理且不重提生图。所有项同尺寸时同步顶层 `size`，不一致时移除顶层 `size`。图片按帧计费与文件/帧记录规则见[共享模型价卡与计费设置](../domains/routing_and_billing.md#group_model_pricing)。

<a id="openai_fast_policy"></a>
## Fast 与推理档位

### Fast 与 Ultra Fast 策略

分组通过 `openai_fast_policy` 配置 `follow_request`、`force_priority`、`force_ultrafast` 或 `force_off`（兼容旧布尔字段 `force_openai_fast`：新字段优先，省略两者保留现值）。HTTP、Chat、Messages、passthrough 和 WS 共享同一判定顺序：

1. 全局规则优先：先对请求中已有的合法 `service_tier` 执行全局过滤、阻断或强制（支持 `force_ultrafast`）。
2. 分组策略：全局放行后，分组 `force_off` 移除 Fast/Ultra Fast 并阻止 Key 重开；`force_priority` / `force_ultrafast` 可为未带 tier 的请求注入档位（再受全局规则约束）。
3. API Key 覆盖：Key 的 `force_off` 可关闭分组级加速；`force_on` 保留分组强制的 Ultra Fast；`force_ultrafast` 仅在 OpenAI 上游且模型定价声明了 Ultrafast 时将 Fast 升为 Ultrafast，改写后再次经过全局规则。

其他平台忽略此策略，公开分组接口不暴露该管理字段。认证快照缓存与失效见[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)。

免费 Fast（`free_openai_fast`）属于价格配置的用户计费策略，不改变出站 `service_tier`。当 OpenAI 提供商实际按 `priority` 或 `fast` 计费时，用户侧 `ActualCost` 与统一结算按同一模型、价卡、高峰和长上下文规则重算为 Standard 价格，Usage Log 的 `total_cost` 与提供商统计/额度仍记 Fast 成本（缺少 Standard 价格时走缺价零成本流程）。

### 推理档位上限

分组可配置 `max_reasoning_effort` 与超限动作 `max_reasoning_effort_over_limit`（默认 `downgrade`，可选 `deny`）：

- 仅作用于客户端显式传入的 `reasoning.effort`、`reasoning_effort` 或 Messages `output_config.effort`；兼容桥补齐的默认 `medium` 不受影响。
- 先执行分组模型范围映射，再按 `minimal < low < medium < high < xhigh < max` 比较（`none` 仅可用于映射，不参与上限排序）。
- `downgrade` 将超限值压到上限；`deny` 在 HTTP 返回 403 `permission_error`，Messages 返回 `forbidden_error`，Responses WS 以 policy-violation 关闭。

## 远程压缩协议

TokenRouter 同时支持原生 Remote Compaction V2 与旧版 `/responses/compact`，两者语义相同但链路独立：

| 维度 | 原生 `remote_compaction_v2` | 旧版 `/responses/compact` |
| --- | --- | --- |
| 触发识别 | 裸 `/responses` 且 `stream=true` 且 `input` 含 `compaction_trigger`（出站必带 `remote_compaction_v2` beta 头） | 显式请求 `/responses/compact`，或带 `compaction_trigger` 但不满足原生 V2 条件的 `/responses` 请求 |
| 上游传输 | 走普通 Responses 流式链路，上游直接返回 `compaction` SSE | 走独立 Compact 子路径；流式客户端由网关把 JSON 合成为 Responses SSE 并发送等待心跳 |
| 模型处理 | 走常规模型映射，不应用 `compact_model_mapping`，不加 `-openai-compact` | 额外应用提供商 `credentials.compact_model_mapping`（优先于默认模型） |
| 提供商开关 | `extra.openai_native_compaction_v2_mode`（`force_on` / `force_off`） | `extra.openai_compact_mode`（`force_on` / `force_off`） |

调度仅看管理员显式开关，不读取历史探测状态；原生 V2 仍要求提供商启用 Responses 上游路由。Compact 单次恢复在当前提供商内执行，不额外嵌套换号循环。管理端手动测试（`compact` / `legacy_compact` 或指定 `protocol=responses|chat_completions`）只返回本次结果，不修改提供商配置。

## Responses 请求细节

<a id="responses_ws_runtime"></a>
### Responses 长连接与在线参数

- 准入与传输：客户端能否使用 WS 由分组 `allowed_protocols` 决定，上游走 WS v2 还是 HTTP 桥接由提供商 `upstream_protocols` 与分组转换规则决定（无全局 WS 开关）。公开 HTTP Responses 固定走 HTTP/SSE。分组撤销 WS 许可后，已有连接在下一轮准入时以 `1008` 关闭，已放行轮次正常完成。
- 连接模式：提供商 `extra.responses_ws_connection_mode` 可选 `pooled`（默认，优先复用）或 `per_session`（每会话新建）。超大首帧走 HTTP 转换前同样校验上游 HTTP 能力与分组许可。
- 在线调参：管理员在「网关设置 → OpenAI → 长连接与连接池」修改 `responses_ws` 覆盖（字段省略保持现值、`null` 清除该项、整对象 `null` 恢复默认值），优先级为「管理端覆盖 > YAML > 代码默认」。运行时保存不可变快照并每 5 秒后台刷新（代次防覆盖）；持久化成功但运行时应用失败返回 `SETTINGS_APPLY_FAILED`（`persisted=true`）。
- 连接池生命周期：扩容按需执行，缩容仅回收空闲连接（忙连接等会话结束回收）；关闭握手在锁外执行。每次租约检查 60 分钟寿命上限，普通请求重拨，强制续接返回指定连接不可用。最小/最大空闲数均可为 0（上限为 0 时归还连接优先给等待者，无等待者则关闭）。
- 参数生效时机：连接数与消息大小上限作用于新连接；超时、恢复与排队截止时间按每轮进入时的快照固定；会话 TTL 仅影响新写入与续期；正在生成的轮次不受中途调参影响。

<a id="responses_ws_warmup"></a>
### WebSocket 预热与续接

- 续接匹配：Codex WS v2 首轮发送 `generate=false` 预热帧，后续携带其 `previous_response_id`。严格续接比较忽略 `client_metadata` 和 `stream_options`，将 `generate=false` 与后续省略视为等价；`generate=true` 及 model、instructions、tools、reasoning、store 须一致。
- HTTP 桥接本地预热：OpenAI 与 Grok 的 HTTP 桥接在本地完成 `generate=false`（返回同 ID 的 `response.created` + `response.completed`，空输出、零用量），把输入与工具声明/映射存入当前连接重放历史，下一轮续接时再调上游 HTTP。预热仍做身份、协议和资金准入，完成后立即释放并发槽，不触发计费、健康反馈和额度刷新。

### 路由提示、beta 头与回合状态

- `x-codex-routing-hint`：完全由网关生成，所有提供商先剥离入站与覆盖中的该头，仅 OpenAI OAuth 在模型映射与 Fast 策略后写入最终上游模型及有效 tier（`fast` 归一为 `priority`；`default`/未知/空值只带模型）。旧版 Compact 保留 `service_tier` 以防丢失层级。OAuth HTTP 不自动注入或透传旧版 `responses=experimental`。
- `x-codex-beta-features`：OAuth 普通 Responses HTTP 与 WS 握手在客户端未声明时补 `remote_compaction_v2`；原生 V2 请求对所有提供商确保包含该项。上游响应中的 `x-codex-turn-state` 在 HTTP/SSE、SSE 转 JSON 与 passthrough 路径透传回客户端。
- 回合状态防串号：网关按 API Key 与客户端原始 session 记录最近签发 `x-codex-turn-state` 的提供商 ID（不存不透明状态原文，与 WS 缓存分离）。故障转移后，来自其他提供商的回带值会被剥离，同提供商或未知/过期来源继续透传。

### WebSocket 连接池亲和与排队

- 复用键：上游 WS URL、代理 URL、TLS profile、握手 beta feature 和指纹身份头共同决定连接兼容性；普通复用与预热优先匹配相同 `x-codex-routing-hint`，池满或排队时可复用其他兼容连接；显式续接按连接 ID 查找，路由提示变化仍可续接。
- 排队上限：提供商总排队上限为「有效连接上限 × `queue_limit_per_conn`」，强制续接另受单连接排队上限约束。取消、超时或池关闭会释放名额并唤醒等待者，多余空闲连接由后台任务在锁外关闭。

### TTFT、Codex 基础指令与 Responses Lite

- TTFT 统计：HTTP/SSE、Chat/Messages 转换和文本 WS 使用全局 `ttft_mode`（首包取首个非心跳事件，首字取首个有效内容），不影响 HTTP 提交、首输出超时与重试判定，也不为非流式、图片或纯 usage 终态补造时间。
- 默认 `instructions`：OpenAI OAuth 按最终上游模型（含提供商映射与 Compact 覆盖）补齐默认指令。`gpt-6` / `gpt-6-astra*` 用 Astra 提示词，`gpt-6.1-sol` / `gpt-6.1-terra` 用各自提示词（识别推理档位、`-openai-compact` 与供应商前缀），其余按 Codex / GPT-5.1 / GPT-5.2 匹配并回退 GPT-5.5。OAuth passthrough 允许省略 `instructions`，非空字符串原样保留，空白或非字符串在本地拒绝。
- Responses Lite：仅由 HTTP 头 `X-OpenAI-Internal-Codex-Responses-Lite: true` 或 WS `client_metadata` 标记识别（不看模型名）。所有带 Lite 标记的 OpenAI 请求强制 `parallel_tool_calls=false`；OAuth 额外设置 `reasoning.context=all_turns` 并将私有 namespace 工具移入 `input.additional_tools`。OAuth Lite 若已在 `input` 的 developer 消息提供非空指令，允许省略顶层 `instructions`。
- Anthropic `count_tokens`：OAuth 提供商调用 Responses `input_tokens` 端点；遇缺少 scope、端点不存在或代理返回 HTML 403 时回退本地 token 估算并返回成功，不冷却或标错提供商。

### namespace、工具 ID 与 Chat 降级

- 原生 namespace 保留：普通 Responses 保留 `function_call`、`tool_call`、`custom_tool_call`、`mcp_tool_call` 历史项的 `namespace`，清理非调用项上的残留值。上游 HTTP 400 拒绝特定 `input[i].namespace` 时仅删该索引（`Missing namespace` 不删），受 6 次恢复预算与重复报文检查约束。
- 转换与平铺：转 Chat 或 function-only Responses 时，默认 `functions` 命名空间保留裸名，其他显式 namespace 生成稳定平铺名并在响应阶段还原；压缩后声明缺失的历史带 namespace 调用仅分配当次输入回放名（冲突时加摘要后缀），不进入工具声明与后续 WS 工具集。WS 桥接在后续轮次省略 `tools` 时复用前声明，显式空值清空；每次故障转移清空上次尝试的映射。OAuth 兼容中转不接受 namespace 时可开启 `extra.openai_responses_flatten_namespaces=true`。
- 工具 ID 校验与转换：API Key Responses 校验输入项 ID 前缀（message=`msg`、工具=`fc`、reasoning=`rs`），类型不匹配直接删除。兼容层将 function-only 上游的 `fc_` 还原为 `custom_tool_call`（`ctc_`）或 `tool_search_call`（`tsc_`），再次降级时还原为 `fc_`。进入转发前统一将显式 `parameters.type: null` 修正为 `object`（省略 `type` 的宽松 schema 保持原样）。
- Responses 降级到 Chat：工具结果中的图片（`input_image` / `image_url` / data URL）按 `call_id` 抽出，原位置替换为稳定标记并在该组工具回复后追加用户多模态消息，无图片结果保持原始字节以保护提示缓存前缀。
- Messages 强制走 Chat：并行 `tool_calls` 按 `index` 聚合，流结束时按序发出完整 `content_block_start` / `input_json_delta` / `content_block_stop`（聚合期间发送下游空闲保活，文本与 thinking 即时流式输出）。空参数归一为 `{}`，`disable_parallel_tool_use=true` 映射为 `parallel_tool_calls=false`。

## 模型与能力

- 图片能力门禁：图片入口与 Responses 图片工具按最终路线的输出模态检查能力（不看模型名是否含 `image`），未知返回 `MODEL_CAPABILITY_UNKNOWN`，不支持返回 `MODEL_CAPABILITY_UNSUPPORTED`，均不计作上游故障。Responses 图片工具在选号阶段逐候选校验，转发前对最终工具列表复核。
- 完整型号身份：客户端模型经 Key、分组、提供商映射后作为统一完整 ID 查询[统一模型属性](model_catalog_and_marketplace.md#model_attributes)，不剥离日期、后缀或供应商前缀猜能力或价格。
- GPT-5.6 与 GPT-6 差异：
  - 内置型号为 `gpt-5.6-sol` / `gpt-5.6-terra` / `gpt-5.6-luna`，裸 `gpt-5.6` 按未知型号处理。
  - GPT-5.6 Sol/Terra/Luna 普通请求支持 `max`，OAuth Compact 降为 `xhigh`；GPT-5.2/5.4 采样能力受 `effort=none` 条件约束；Codex OAuth 禁用 `temperature`、`top_p` 等输出采样字段。
  - `gpt-6-astra` 仅接受 `low` 到 `max`，网关不硬编码改写，需兼容 `minimal`/`none` 的分组通过「推理强度映射」配置。
- 推理日志与超时：Usage Log 分别记录策略前客户端显式发送的 `requested_reasoning_effort` 与最终出站的 `reasoning_effort`。最终档位为 `high` / `xhigh` / `max` 时选用高 effort 首输出超时档。
- 工作负载分离：API Key 工作负载仅区分 `text_generation` 与 `embeddings`；必须走原生 Responses 的路径（如 Responses 生图）拒绝回落到 Chat 的候选。

## 额度与调度

- 高级调度接入：目标分组 `scheduler_type=advanced` 时，在硬过滤之后使用通用 Top-K 评分，支持分组 `advanced_scheduler_overrides` 稀疏覆盖。OpenAI 专属信号（Responses transport、WS、旧版 Compact、previous response、订阅优先、Codex 额度余量）仅在提供商和请求均具备条件时参与。
- 上游窗口与倍率：OAuth 5 小时/7 天窗口与自动暂停保存在提供商运行状态中；OpenAI 不采集上游声明倍率，提供商本地 `rate_multiplier` 仅用于内部成本统计与额度累计。
- 额度刷新接口：`GET /admin/openai/providers/:id/quota` 为只读；`POST /admin/openai/providers/:id/quota/refresh` 刷新上游并写入展示快照 `extra.codex_reset_credit_snapshot`（正数次数须含到期明细才覆盖，前端自动过滤过期项；不触发调度 outbox）。Spark 影子提供商查询母提供商额度，但快照写在自身记录上。

## 失败与诊断

- CAS 状态保护：提供商健康更新携带凭据快照与 CAS，令牌刷新后旧请求无法封禁提供商。
- 分类与 HTML 403 例外：401/403、429、endpoint 不支持、内容策略、网络错误与 5xx 分别处理。代理或 CDN 返回的 HTML 403 允许 failover，但不累增连续 403 计数、不临时停调或禁用提供商；结构化 JSON/纯文本 403 走正常提供商策略。
- 池模式重试与确定性 400：API Key passthrough 池模式将命中 `pool_mode_retry_status_codes`（默认 401/403/429，空列表关闭）的错误先做未提交前同提供商重试，用完预算再换号。原生 Responses 的确定性 400 在未命中重试/改写规则时直接透传脱敏后的 `message` 与 `type`/`code`/`param`。
- 端点错用与加密密文恢复：图片模型在 Codex 文本端点收到 plan-gated 400 时只换号、不写模型冷却（专用 Images 端点同类拒绝与 `404 model_not_found` 照常冷却）。Responses 遇 `invalid_encrypted_content` 时在同提供商内最多清理绑定密文并重试 1 次，保留未加密 compaction。
- 瞬时失败累计：按提供商+模型组合累计（第 1 次仅计数、第 2 次短冷却、第 3 次及以上长冷却），任意成功立即清零；TTL 仅回收长期不活跃条目，不作为短时间重置条件。
- 流内错误与静默拒绝：
  - HTTP 200 SSE 含 `rate_limit_exceeded` 视作 429 触发 failover 与池模式重试，不用正常配额头写默认冷却。
  - `server_is_overloaded` 与 `slow_down` 在未输出业务内容前留在缓冲并触发同提供商重试或换号，不冷却提供商；已输出或重试耗尽后，发给客户端的副本改写为可重试的 `server_error`（原始事件保留给观测）。
  - 仅含心跳字节的流仍允许未提交前重试，最终失败追加 `response.failed`（详见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)）。
  - Responses 流若只收到前导事件和不含 output/usage/error 的空终态且未写出业务内容，按静默拒绝换号，不记 0/0 成功。
- WS 退出顺序：入站下行写入不继承租约取消信号（仅受写超时限制），确保租约丢失时客户端先收到终态事件再收到 `1013` 关闭帧；上行写入继承控制面取消以快速回收上游连接。
- 排查入口：响应开始输出后不再重放或换号，最终错误受[网关错误响应策略](gateway_error_policy.md)约束。排查时按顺序核对提供商类型、协议与能力集合、客户端限制、privacy、模型映射、配额窗口、代理/TLS 及 attempt 记录。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[模型目录与市场](model_catalog_and_marketplace.md)。
