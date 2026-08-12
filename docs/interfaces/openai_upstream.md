# OpenAI 上游

本文描述 OpenAI OAuth/API Key 账号，以及 Responses、Chat、Messages、Embeddings、Images、Realtime 和 Codex 兼容能力的当前契约。它不枚举会随上游变化的完整模型列表，也不把所有 OpenAI 形状的入口都解释为任意平台可用。

## 章节导航

- [账号与凭据](#账号与凭据)：修改 OAuth、API Key、隐私或客户端限制时读取。
- [协议与传输](#协议与传输)：修改 Responses、WebSocket、Realtime 或兼容转换时读取。
- [远程压缩协议](#远程压缩协议)：区分原生 `remote_compaction_v2` 与旧版 `/responses/compact` 时读取。
- [模型与能力](#模型与能力)：修改模型别名、endpoint capability 或推理参数时读取。
- [额度与调度](#额度与调度)：修改窗口配额、评分、粘性或自动暂停时读取。
- [失败与诊断](#失败与诊断)：修改错误分类、刷新、CAS 状态或 failover 时读取。

## 账号与凭据

OpenAI 正式支持 `oauth` 与 `apikey`。OAuth 账号保存 access/refresh token、账号/组织上下文和 Codex 能力元数据，后台与请求路径都可触发刷新；API Key 账号保存 key、base URL 和可探测的 endpoint capability。其它通用导入类型不构成 OpenAI 转发支持，详见[上游账号能力矩阵](upstream_account_matrix.md)。

OAuth 账号可受 Codex CLI-only、允许客户端、agent identity、privacy status 和 OAuth passthrough 策略限制。OAuth 出站的 `originator` 必须与最终 User-Agent 首段配对；客户端未提供可识别官方身份或身份修复失败时统一回退 `codex-tui`，PAT、模型/额度探测、Alpha Search、HTTP 与 WebSocket 走同一默认身份。客户端或 TLS 路由显式提供且可配对的官方身份继续保留，历史 `codex_cli_rs` 仍只作为兼容识别值。API Key 账号不应借用 OAuth-only 的内部端点或身份元数据。Header override、代理、base URL 和 TLS 配置属于出站安全边界，不能覆盖受保护认证头或绕过目标校验。

<a id="openai_protocol_dispatch"></a>
## 协议与传输

OpenAI 平台拥有以下正式协议族：

| 协议 | 处理边界 |
| --- | --- |
| Responses HTTP/SSE | 原生 OAuth/API Key 转发；支持允许的 `/responses/*` 子路径 |
| Responses WebSocket | 根据账号 transport capability 选择 WS 或兼容传输；连接建立后遵守流式不可换账号边界 |
| Chat Completions | 可原生转发或转换到 Responses；每次 attempt 重建协议状态 |
| Anthropic Messages | 转换到 OpenAI 请求并把事件、工具、thinking/usage 恢复为 Anthropic 形状 |
| Embeddings | 仅 OpenAI 分组，账号必须声明或探测到相应 endpoint capability |
| Images | OpenAI 图片生成/编辑；当前网关保留同步生命周期，批量图片由 Gemini/Vertex 专题定义 |
| Realtime/Live/sideband、Alpha Search | 仅 OpenAI 分组，并受分组开关、账号类型和 transport capability 限制 |

OpenAI 分组支持 Messages、Responses 和 Chat，新建时默认启用 Responses 与 Chat；三项都可关闭。已有分组迁移时仅在旧 `allow_messages_dispatch` 开启时加入 Messages。该旧字段只作为 Messages 的弃用兼容镜像，专用 `messages_dispatch_model_config` 仍只负责 Claude 到 GPT 模型映射。Responses WebSocket 是 OpenAI/Grok 的原生传输能力，不因其它平台启用兼容 Responses 而开放。

`/backend-api/codex` 和无 `/v1` 别名服务特定客户端兼容，但仍经过 TokenRouter Key 鉴权、分组准入、调度和结算。Responses WebSocket 不支持 Qoder；其它平台是否可进入 OpenAI 兼容处理器由路由和平台专题共同决定，不能仅凭 URL 推断。

### 远程压缩协议

TokenRouter 同时兼容原生 Remote Compaction V2 和旧版 Compact 端点。两者共享 compaction 输出语义，但请求路径、传输方式、账号能力设置和模型改写边界不同：

| 边界 | 原生 `remote_compaction_v2` | 旧版 `/responses/compact` |
| --- | --- | --- |
| HTTP 识别 | 裸 `/responses` 请求同时携带 `stream=true`、`x-codex-beta-features: remote_compaction_v2`，且 `input` 含 `compaction_trigger` | 客户端显式请求 `/responses/compact`，或带 `compaction_trigger` 但不满足原生 V2 条件的裸 `/responses` 请求被网关提升 |
| 上游传输 | 保持普通 Responses 流式链路，由上游直接返回包含 `compaction` item 的 SSE | 走独立 Compact 子路径；body-signal 流式客户端由网关把 unary JSON 结果合成为 Responses SSE，并在长时间等待时发送注释心跳 |
| 模型处理 | 沿用普通 Responses 的模型处理，不应用 `compact_model_mapping`，也不会因此追加 `-openai-compact` | 仅此路径在常规模型处理基础上应用账号 `credentials.compact_model_mapping` |
| 账号设置 | 不读取 `openai_compact_mode` 和旧端点探测结果来切换协议或筛选账号 | `extra.openai_compact_mode`、`openai_compact_supported`、能力探测和 Compact 专属模型映射都只控制此路径 |

账号设置页中的“旧版 Compact 端点”是能力覆盖而不是协议开关：`force_on` 只把账号视为可承接旧端点并提高其 Compact 支持等级，`force_off` 只将账号排除在旧端点调度之外；两者都不会把普通 Responses 或原生 V2 改写成 `/responses/compact`，也不会启用或禁用原生 V2。旧端点的 OpenAI OAuth GPT-5.6 请求还会把 `reasoning.effort=max` 降为 `xhigh`，原生 V2 则保留常规 Responses 推理强度语义。

官方 Codex WebSocket v2 会先发送 `generate=false` 的预热 `response.create`，再以预热响应 ID 作为业务请求的 `previous_response_id`。严格续接比较会忽略逐请求变化的 `client_metadata`、仅用于传输的 `stream_options`，并把 `generate=false` 与后续省略该字段视为等价；`generate=true` 以及 model、instructions、tools、reasoning、store 等上下文字段仍必须保持一致，避免把无关请求错误串接。

OpenAI OAuth 的 HTTP、passthrough、旧版 Compact 与 WebSocket 出站会在模型映射和本地 fast 策略处理完成后，由网关生成 `x-codex-routing-hint`。提示至少包含最终上游模型；只有有效的 `priority` 或 `flex` 才附带 tier，`fast` 先规范化为 `priority`，`default`、未知值和空值均保持 model-only。旧版 Compact 规范化必须保留 `service_tier`，否则提示会丢失已经生效的路由层级。该头由网关独占控制：所有账号类型都会先删除调用方及账号覆盖提供的任意大小写变体，只有 OpenAI OAuth 路径会重新生成；API Key 路径不得透传伪造提示。OAuth HTTP 也不再自动注入或透传旧版 `responses=experimental` beta 标记，但同一头中的其它独立 beta 项仍保留。

WebSocket 连接池把 routing hint 视为拨号和普通复用的软亲和：优先复用相同提示建立的连接，池满时仍可在硬兼容连接上排队，显式 continuation 也不会仅因提示变化而断链。握手 beta feature 与本 fork 的 TLS fingerprint profile 仍是硬兼容键，任一变化都禁止复用，并会使尚未完成的旧目标预热拨号失效。路由诊断只记录网关推导的最终模型、规范化 tier、传输类型、账号 ID、是否生成提示和 WS 亲和决策，不记录提示头值、token 或凭据。

OAuth passthrough 的 Codex 请求可以省略 `instructions`，网关会按请求模型补入内置 Codex 基础指令；显式提供的非空字符串保持不变，空白或非字符串值仍在本地拒绝。该规则同时适用于 Responses SSE 与旧版 Compact 请求。

Responses Lite 通道由 HTTP `X-OpenAI-Internal-Codex-Responses-Lite: true` 或 WebSocket `client_metadata` 中的对应标记识别，不根据模型名称推断。任何向 OpenAI 上游转发该标记的 HTTP、passthrough、旧版 Compact 或 WebSocket 请求都必须强制顶层 `parallel_tool_calls=false`。OAuth 账号还会统一设置 `reasoning.context=all_turns`，并把私有 namespace 工具声明迁入 `input.additional_tools`；API Key 账号保留除此之外的标准 Responses 请求语义。未携带 Lite 标记的普通 Responses、Grok 和专用 Images 请求不应用这些约束。

OpenAI OAuth 账号承接 Anthropic `count_tokens` 时会调用 Responses `input_tokens` 端点；缺少 scope、端点不存在，或上游代理在 API 前返回 HTML 格式的 `403` 时，网关改用本地 token 估算并返回成功结果。这类端点级失败不会冷却、临时踢出或标错账号；其它结构化鉴权与上游错误仍进入正常健康策略。

OpenAI OAuth 的普通 Responses 请求默认原样保留 Codex namespace 工具声明，并保留 `function_call`、`tool_call`、`custom_tool_call`、`mcp_tool_call` 历史项上的 `namespace`；普通消息等非调用项上的残留字段仍会清理。旧版 Compact 请求始终摊平 namespace 并移除输入项字段，API Key 出口也按标准 Responses schema 清理。仅当 OAuth 账号的兼容中转不接受 namespace 时，才应启用账号 `extra.openai_responses_flatten_namespaces=true` 恢复平名行为。每次 failover attempt 都会清空上一账号登记的平名映射，避免响应还原状态串到下一账号。

Responses 工具定义在进入 OAuth passthrough、Codex transform、Grok 或 API Key Chat 分流前统一修正显式为 `null` 的 `parameters.type`，将其归一为 `object`；处理范围包括顶层 `tools[]` 和多轮历史 `input[].tools[]` 中的嵌套工具。缺失 `type` 的合法宽松 Schema 保持原样，不能为了兼容而补写并收窄客户端语义。

Responses 请求降级到 Chat Completions 时，工具结果中的 `input_image`、`image_url` 和完整图片 data URL 不能留在只接受文本的 `tool` message。转换器会按 `call_id` 从工具结果中提取图片，把原位置替换为稳定标记，并在对应的一组工具回复后追加用户多模态消息；并行调用按工具声明顺序归属图片，孤儿或未回答调用不携带媒体。没有可识别图片的工具结果必须保留原始字节，避免无关 JSON 重编码改变提示缓存前缀。

OpenAI API Key 账号以 `force_chat_completions` 承接 `/v1/messages` 时，Chat 流中的并行 `tool_calls` 必须按 `tool_calls[].index` 聚合 ID、名称和全部参数分片，在流收尾时再按 index 顺序生成各自连续闭合的 `content_block_start`、`input_json_delta`、`content_block_stop`；参数分片暂存后一次拼接，聚合期间通过 Anthropic `ping` 维持下游活动，文本与 thinking 仍即时流式输出。空工具参数归一为 `{}`，call ID 保持原样，以便下一轮 `tool_result.tool_use_id` 配对。Anthropic `tool_choice.disable_parallel_tool_use=true` 映射为 Chat 顶层 `parallel_tool_calls=false`，字段缺失或为 `false` 时保持默认 `true`；`auto`、`any`、`none` 和具名工具的选择语义不变。

## 模型与能力

客户端模型先经过 Key、渠道和账号层映射。OpenAI 内置别名、reasoning effort 归一化、旧版 Compact 端点支持、图像/embedding 能力和传输能力会影响候选账号；模型列表只公开当前分组可请求的结果。

API Key endpoint capability 可通过探测或配置表达 `responses`、`chat_completions`、`embeddings` 等能力。OAuth/Codex 账号还可能包含 Realtime、WebSocket、旧版 Compact 端点状态和客户端身份限制。未知模型可以在管理员明确配置的兼容上游中透传，但没有定价或能力证据时不能虚构价格与功能。

## 额度与调度

OpenAI 是通用高级调度器的能力适配者之一，而不是该调度器的全局所有者。只有最终目标 Group 的 `scheduler_type=advanced` 时，OpenAI 路径才在共同 active/schedulable、分组、模型、限流和并发硬过滤后使用通用 Top-K 评分；`basic` 保留原有默认选择路径。高级分组可用稀疏 `advanced_scheduler_overrides` 覆盖全局 Top-K、评分权重和粘性开关，未设置字段继续继承网关设置。高级分组还会考虑所需 transport/capability、账号优先级、负载、排队、错误率、近期延迟、配额余量和粘性上下文。previous response、WebSocket 会话和显式 session 可约束账号复用；只有策略允许时才能迁移。

OpenAI 专属能力只在账号和请求具备对应条件时加入候选或分数：Responses transport、WebSocket、旧版 Compact、previous response、订阅优先和 Codex 额度余量都不会排除缺失这类可选信号的普通账号。OAuth 5 小时、7 天等上游窗口和自动暂停仍由 OpenAI 设置及账号运行状态控制，不随高级调度器通用化而迁移到其它平台。

OAuth 账号的 5 小时、7 天等上游窗口和重置时间保存在账号运行状态中，可触发临时限流或自动暂停；API Key endpoint capability 仍可独立探测。OpenAI 不再采集上游站点声明倍率，也不按该值进行低倍率优先或高级评分。账户本地 `rate_multiplier` 和渠道上游计费模型来源继续用于 TokenRouter 结算，但都不是用户余额、订阅、Key 限额或用户平台额度。

管理 API 的 `GET /admin/openai/accounts/:id/quota` 保持只读；账号列表使用 `POST /admin/openai/accounts/:id/quota/refresh` 查询上游并把重置次数写入 `account.extra.codex_reset_credit_snapshot`。正数次数只有同时取得到期明细时才覆盖快照，前端水合时过滤已过期明细并把次数收敛到仍有效的卡片数量。该 extra 键只用于展示缓存，不触发调度 outbox；Spark 影子账号的查询可解析母账号额度，但快照仍写在被查询的行上，且列表继续只提供查询入口，不提供真实重置按钮。

## 失败与诊断

账号状态更新使用凭据快照/CAS，避免较早请求在 token 已刷新后再次封禁账号。401/403、429、endpoint 不支持、内容策略、网络错误和上游 5xx 分别分类；只有可切换且客户端响应未开始的失败才进入下一账号。图片模型被 Codex 文本端点以 plan-gated `400` 拒绝时属于端点错配：当前尝试仍切号，但不写模型冷却，避免影响同账号后续通过 `/v1/images/*` 正常生图；专用 Images 端点上的同类拒绝仍按真实账号能力缺失冷却，图片模型的 `404 model_not_found` 也不豁免。Responses HTTP 与 WebSocket v2 首次发送时保留加密 reasoning/compaction；若上游明确返回 `invalid_encrypted_content`，同账号恢复最多重试一次，清理账号绑定的加密状态但保留未加密 compaction。

账号与模型组合的瞬时失败按连续结果累计：首次失败只记录，第二次短冷却，第三次及以后长冷却。请求间隔较长不能把持续故障误当成恢复，只要未超过状态回收 TTL，稀疏流量中的失败仍继续累计；任一成功结果立即清零该组合。TTL 只负责回收长期不再使用的条目，不能兼作短窗口的连续失败重置条件。

流式错误要保持 SSE/WebSocket 协议完整；Responses 可产生 `response.failed`，非流接口返回相应 OpenAI envelope。入站 WebSocket 的下行写不得继承独立的 ingress 租约取消信号：旧 ingress 路径绑定客户端请求生命周期并叠加 write timeout，v2 relay 只受 write timeout 限制，退出路径再通过显式 Close/CloseNow 回收连接；这样租约丢失不会在终态事件写入期间抢先硬关 TCP，客户端可先收到终态事件，再收到 1013 关闭帧。上行写继续继承控制面取消，以便快速回收上游连接。HTTP 200 SSE 中的 `rate_limit_exceeded` 按语义状态 429 进入故障转移与池模式重试，但不使用该 200 响应的正常配额快照头写入默认账号冷却。上游容量降载通常先发 `error`、再以 `response.failed` 收尾；`server_is_overloaded` / `slow_down` 的前置错误帧在尚无业务输出时继续留在 attempt 缓冲中，触发有界同账号重试和 pre-output failover，并按请求级瞬时故障处理，不冷却当前账号。已有真实输出或重试耗尽后不能重放请求，SSE 与 WS HTTP bridge 会仅在客户端副本中把这两个致命码改为可重试的 `server_error`，原始事件仍用于账号策略与观测。客户端尚未收到业务输出时，池模式账号的其它瞬态流内处理错误也可在请求级预算内重试同一账号；旧版 Compact 桥接心跳注释不算业务输出。一旦真实输出开始，网关不得重放请求或切换账号。最终错误还可命中[网关错误响应策略](gateway_error_policy.md)，但规则不会把失败结算成成功。排障应同时检查账号类型、required transport/capability、客户端限制、privacy status、模型映射、quota reset、代理/TLS 和 attempt 记录。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[模型目录与市场](model_catalog_and_marketplace.md)。
