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

管理员 OAuth/PAT 导入、账号刷新和额度操作由 account 用例执行，HTTP 位于 account/httpapi，组合根绑定同一实例。额度的供应商请求由 upstream/openai 执行，窗口与返回报文使用 protocol/openai 值类型。额度消费完成后的恢复阶段保留八秒独立预算，客户端断开不取消该收尾，应用停止会取消并等待；操作先于底层额度服务停止。

OpenAI 正式支持 `oauth` 与 `apikey`。OAuth 账号保存 access/refresh token、账号/组织上下文和 Codex 能力元数据，后台与请求路径都可触发刷新；API Key 账号保存 key、base URL、工作负载能力、文本协议路由和管理员压缩开关。其它通用导入类型不构成 OpenAI 转发支持，详见[上游账号能力矩阵](upstream_account_matrix.md)。

OAuth 授权会话、刷新结果补全和凭据组装由 `account.OpenAIAuthorization` 持有，app 直接构造同一实例。`account/provider` 将代理、TLS Router/Profile、动态 Codex UA 和隐私查询投影为供应商调用参数，保留原读取时点；不持有第二份会话或刷新状态。token source/refresher 复用原缓存与协调器。共享 OAuth token 与刷新锁的 Redis Adapter 位于 account/rediscache，由 app 构造唯一实例，保留原键和 TTL。

Agent Identity 的任务锁、锁内复查和凭据登记由 account 协调；app 显式构造一个协调器供请求执行、额度查询、用量查询和账号探测共同使用。gateway/provider 的独立身份适配负责请求签名、恢复与脱敏投影，不持有 Gin、完整配置或第二份注册状态；供应商交换与加解密由 upstream/openai 唯一实现。

推理凭据由 `account.OpenAIExecutionCredentials` 统一选择，app 绑定原母账号读取、OpenAI 和 Grok token 源。影子账号在需要凭据时读取母账号，普通账号不增加查询；Agent Identity、setup-token 与缺少 token 源时的存量凭据回退保持独立。setup-token 不进入刷新，OpenAI 与 Messages 对其它平台 setup-token 的拒绝差异继续保留。token 源的运行阻断回调也由 app 在开放请求前绑定。

供应商 OAuth/PAT/隐私交换、规范 Codex 身份、请求指纹、Header 组合及 WS 客户端位于 `upstream/openai`；WS v2 relay 与 Live attestation 是平台内的技术子包。WS 池唯一持有连接、预热、队列和租约状态，构造不启动 worker，入站拥有者在首次使用时显式启用。Live 创建、sideband、DeviceCheck 密文与观察者适配由 gateway/httpapi.OpenAILiveExecutor 组合，app 负责 JWT 密钥投影与关闭登记。

长连接的逐轮模型资格复核由共享选择器拥有；Live 仍只记录零费用用量。WS 的池化、透传、HTTP 桥接及逐轮帧适配由 gateway/httpapi.OpenAIWebSocketExecutor 执行，原循环仍由 gateway/ws 唯一拥有。OpenAIWSConnections 统一管理按需连接池、拨号器及停止屏障；账号授权、用量和探测直接调用同一连接失效端口。

标准 Responses、passthrough、Chat/Messages 转换和 Raw Chat 读取使用原生实现，通过同步 OutputSink 输出。 `gateway/httpapi.OpenAIResponseOutput` 固定绑定响应读取、Header、错误规则、健康观测、超时及诊断；app 注入静态参数，TTFT 设置仍在原读取时点查询。响应结果直接使用上游读取器的值类型，保留“仅有观测的失败”在不同入口上的返回差异。

首输出暂存器拥有当前尝试的内存和临时文件；protocol 唯一提供工具参数、usage、终态重建和图片产出计数。Embeddings、Images 和 Alpha Search 的单次执行负责网络调用和响应资源，账号选择、健康写入及全局重试由入站适配。Alpha Search 在错误处理回卷响应体时仍关闭最初取得的上游 Body。计数查询保持原生完整 JSON 与 Anthropic 兼容响应的区别，不作为推理结算事实。 Embeddings、AlphaSearch、Messages count_tokens 和 Responses input_tokens 由 `gateway/httpapi.OpenAIAuxiliary` 直接接入；请求构造与健康/输出复用原实例，模型投影和计数请求准备归 gateway/provider。计数路由直接组合 RoutePlanner、选择器及受控账号目标。

Responses 主请求由 `gateway/httpapi.OpenAIResponsesExecutor` 执行准备、模型与工具转换、HTTP 交换及协议分派。图片桥接按分组、账号、渠道、全局默认的原顺序求值；渠道仍在需要时读取。HTTP 与 WS使用同一 OpenAIEncryptedLineage 和会话存储，失效密文摘要只在上游明确拒绝后记录，后续请求按原会话键剥离。转入 WS时传递已固化的模型、计费投影、TLS 及请求体，继续使用原连接池和恢复循环；WS 资源已由同一 OpenAIWSConnections 持有，关闭后不能重新创建连接池。

app 分别装配原生文本、Responses、WS、Images 和辅助执行器，复用同一请求构造、输出、凭据及连接拥有者。

图片入口由 `gateway/httpapi.OpenAIImagesExecutor` 组合原生请求和输出能力，app 将同一实例直接绑定到媒体运行时。API Key 与 OAuth 分支保留各自的协议转换、实际产出计数和失败资格。图片请求在原位置脱离客户端取消，读取完成后才交付已观测用量；JSON 心跳不视为真实图片输出。URL 回填复用原传输与逐跳目标校验，不改变返回格式选择和计费元数据。结构化图片工具不可用事件由 account/provider.ImageToolCooldown 写模型级冷却，模型文字兜底不触发该写入。

OAuth 补全账号元数据时，ID token 中的个人 `chatgpt_plan_type` 是个人套餐的权威来源。`accounts/check` 可能按 access token 的 `poid` 命中另一个 workspace；仅当该记录的账号 ID 与个人 `chatgpt_account_id` 一致时，才能把它的 `entitlement.expires_at` 与个人套餐组合。账号不一致时，到期时间必须改从个人 `/backend-api/subscriptions` 的 `active_until` 获取；若套餐本身来自 `accounts/check`，套餐和到期时间仍保持来自同一条记录。

OAuth 账号可受 Codex CLI-only、允许客户端、agent identity、privacy status 和 OAuth passthrough 策略限制。OAuth 出站的 `originator` 必须与最终 User-Agent 首段配对；客户端未提供可识别官方身份或身份修复失败时统一回退 `codex-tui`，PAT、模型/额度探测、Alpha Search、HTTP 与 WebSocket 走同一默认身份。

客户端或 TLS 路由显式提供且可配对的官方身份继续保留，历史 `codex_cli_rs` 仍只作为兼容识别值。API Key 账号不应借用 OAuth-only 的内部端点或身份元数据。Header override、代理、base URL 和 TLS 配置属于出站安全边界，不能覆盖受保护认证头或绕过目标校验。

OpenAI OAuth 账号的 `extra.codex_fingerprint_mode` 控制 Codex Responses 的设备指纹收敛，未配置、空值或无效值都默认 `off`，只有 `device`、`session`、`full` 是显式 opt-in：`device` 只统一 installation ID，`session` 进一步统一 session ID 并按客户端原始 session 稳定派生 thread ID，`full` 再把所有客户端收敛到同一 thread。

session/full 的 turn ID 每个请求重新生成，但同一次请求的 HTTP 头、`client_metadata` 和内嵌 turn metadata 必须共用同一组 ID；HTTP 内部重试也不得重新派生。普通转换与 OAuth passthrough 都遵守该配置，透传大 body 只局部改写 `client_metadata`，不做整包解码；旧版 `/responses/compact` 保持既有协议且不应用额外收敛。

管理员配置的真实 OpenAI device ID 优先于账号 ID 派生值。Spark 影子账号继承父账号模式、device ID 和稳定种子，不允许以影子 ID 分裂同一 OAuth 凭据的上游设备身份。

Codex 身份命名空间与指纹配置组合由 account/provider 负责，签名、ID 派生和 wire 改写继续复用 upstream/openai。gateway/httpapi 分别发布本 attempt 的身份来源和指纹 IDs，沿用 Gin 的同步读写与原覆盖时点；nil IDs 会覆盖旧值。身份来源保留当前记录引用，命名空间在实际 Header/metadata 生成时读取；影子继承与跨账号旧指纹拒绝仍生效。

OpenAI 兼容请求的显式粘性会话头按 `session-id`、`session_id`、`conversation_id`、OpenCode 会话头和 CodeBuddy 会话头依次读取；其中 `session-id` 是 Codex 客户端使用的连字符形式，优先于旧下划线形式。WebSocket 会话日志采用相同优先级，缺少显式会话头时才回退到 `prompt_cache_key`，避免重连时因头名差异漂移到其它账号。

会话 Header 与哈希请求绑定由 gateway/httpapi 提供，内容种子和摘要格式由 gateway/session 拥有。Messages→OpenAI 的摘要绑定使用 app 构造的唯一 `AnthropicPromptCache`，保留账号/Key 命名空间、最长有效前缀、原 TTL 和替换后删除旧链的顺序；没有新增后台清理或持久化。执行适配只传递标识与摘要值。

<a id="openai_protocol_dispatch"></a>
## 协议与传输

兼容文本的 Messages、Chat、Raw Chat、原生 Anthropic 与 passthrough 已由 `gateway/httpapi.OpenAITextExecutor` 接入；请求构造、Header、TLS 与客户端策略使用同一 `OpenAIRequests`。Responses、WS 和 Live分别由原生执行器负责，app 固定绑定共享请求、输出、凭据与连接资源；平台执行、重试边界及完成资格保持原约定。

Responses、Chat、Messages 的入站 HTTP 与单次尝试运行时由 app 直接装配；`gateway/httpapi/openaiattempt` 复用同一选择、反馈、完成及槽位能力。重试循环仍由 `gateway/text` 唯一拥有，跨模式切换从原始报文派生 reasoning 清理结果，不污染后续请求。WS 入站、每轮账号目标与完成 hooks 由原生 wsentry 绑定，Forward/WS 结果投影归 gateway/provider；保留终态、恢复报文、响应 turn-state 和每轮计费时刻。

媒体和辅助入口由原生 mediaentry 直接绑定，沿用同一失败输出、槽位与完成快照；图片 mandatory 与搜索/音频提交策略保持各自原语义。

`protocol/openai` 拥有 Responses/Chat 报文、自定义编解码、服务层级值与宽容 JSON 字节修复；`protocol/bridge` 拥有跨协议转换和每条流的状态。调用方显式提供时刻和随机源。BOM、控制字节、原文与大小限制保持原行为；纯 `BodyLimitError` 在 `gateway/httpapi` 边界转为 `http.MaxBytesError`，请求读取和解压仍由 `server/httpx` 执行，原先未使用宽容修复的入口不会自动启用。

Compact 请求白名单、reasoning replay 与 store=false 修复由原生请求 codec 执行，触发条件仍由原入站决定。Responses Header 与 CC 请求发送也通过原生实现，账号身份、代理/TLS 和请求状态以窄端口投影，保持原覆写顺序。UA/originator 字符串识别在 `gateway/clientmeta`，规范 Codex 出站身份与动态 UA resolver 已由原生包唯一持有，请求字段改写时机仍由 gateway/httpapi 决定。

Responses 标准/透传读取、Responses 转 Chat/Messages、Raw Chat 直通及 Chat 转 Responses/Messages 的响应执行已归 upstream/openai；协议算法继续由 protocol/bridge 唯一拥有。缓冲终态、空响应检测与流状态按每次尝试创建，终态 usage 的覆盖顺序和断开返回差异分别保留。执行适配传入账号策略和观察端口；HTTP 只在原输出时点取得 Header，等待心跳不因创建适配器而提前结束。

OpenAI 平台拥有以下正式协议族：

| 协议 | 处理边界 |
| --- | --- |
| Responses HTTP/SSE | 原生 OAuth/API Key 转发；支持允许的 `/responses/*` 子路径 |
| Responses WebSocket | 根据账号 transport capability 选择 WS 或兼容传输；连接建立后遵守流式不可换账号边界 |
| Chat Completions | API Key 默认保留 Chat 协议原生转发；显式强制时才转换到 Responses；每次 attempt 重建协议状态 |
| Anthropic Messages | 转换到 OpenAI 请求并把事件、工具、thinking/usage 恢复为 Anthropic 形状 |
| Embeddings | 仅 OpenAI 分组，账号工作负载能力必须包含 `embeddings` |
| Images | OpenAI 图片生成/编辑；当前网关保留同步生命周期，批量图片由 Gemini/Vertex 专题定义 |
| Realtime/Live/sideband、Alpha Search | 仅 OpenAI 分组，并受分组开关、账号类型和 transport capability 限制 |

<a id="images_url_backfill"></a>
### 图片结果回填

图片 API Key/OAuth 的单次发送与响应释放由原生 ImagesExecutor 执行，gateway 的入站编排决定账号恢复、失败重试与完成处理。图片真实产出、HTTP 提交和失败分别记录；原非流张数回退与部分结果返回保持各入口语义。

OpenAI API Key 账号可通过 `extra.images_url_to_b64_json=true` 启用图片回填，默认关闭。非流式 `/images/generations` 与 `/images/edits` 响应中，只有缺少非空 `b64_json` 且含 URL 的图片项会被补全；已有 Base64、显式 `response_format=url` 和流式请求保持原行为。回填保留原 URL、修订提示词和所有上游元数据，下载失败只跳过该项；用量、图片数量和计费尺寸始终从回填前的上游响应读取。

下载复用账号代理，不携带账号认证或客户端 Cookie；每张最多 20 MiB、60 秒，只接受字节嗅探确认的 PNG/JPEG/WebP/GIF，data URI 也执行内容与大小检查。目标检查见[上游传输安全](../operations/upstream_transport_security.md)。

图片 JSON/multipart 解析和上传边界由 upstream 的通用图片输入处理；OpenAI 原生包拥有 Responses 图片转换、渐进事件、终态去重和尺寸解析。API Key 与 OAuth 仍采用各自的响应读取规则，通过同步 OutputSink 交付。非流 OAuth 图片在读取上游期间继续发送原 JSON 空白心跳，响应写出时才停止心跳；断开后的读取与计费边界沿用各自原规则。

### 创作台 Images 契约

创作台异步执行器的 `generate` 使用 `/v1/images/generations` JSON，`edit`/`inpaint` 使用 `/v1/images/edits` multipart；固定发送 PNG、单张 `n=1`，并按最终模型能力透传尺寸、质量和背景。GPT Image 模型不发送 `response_format`（其响应固定包含 base64），只有 DALL-E 模型保留 `response_format=b64_json`。inpaint 的 mask 必须是与源图同尺寸、4 MiB 以内的 PNG，透明像素表示需要重绘区域。

OpenAI 分组支持 Messages、Responses 和 Chat，新建时默认启用 Responses 与 Chat；三项都可关闭。已有分组迁移时仅在旧 `allow_messages_dispatch` 开启时加入 Messages。该旧字段只作为 Messages 的弃用兼容镜像，专用 `messages_dispatch_model_config` 仍只负责 Claude 到 GPT 模型映射；系列和精确映射都只在目标值非空时生效，全部留空时不执行分组层模型映射。Responses WebSocket 是 OpenAI/Grok 的原生传输能力，不因其它平台启用兼容 Responses 而开放。

<a id="openai_fast_policy"></a>
### Fast 与 Ultra Fast 策略

`service_tier` 的报文字段校验、归一化和类型化错误由 `protocol/openai` 提供；策略求值和拒绝错误由 `gateway/tierpolicy` 承载；认证作用域与模型白名单动作复用 routing 的纯规则。

OpenAI 分组以 `openai_fast_policy` 选择 `follow_request`、`force_priority`、`force_ultrafast` 或 `force_off`。HTTP/Chat/Messages/passthrough 与 WebSocket 共用策略；强制开启可为未携带 tier 的请求注入对应档位，强制关闭移除 Fast 和 Ultra Fast 并阻止 Key 再开启，保留其它合法 tier。

组级强制意图先经过全局规则；全局过滤、阻断、强制 Fast/Ultra Fast 均拥有最终优先级，Key 的 force_off 可移除全局放行的组级加速，force_on 不会把组级 Ultra Fast 降档。全局规则只匹配已有合法 tier，主动作和其它模型动作均支持 `force_ultrafast`。

新字段优先于旧 `force_openai_fast`；旧 true 映射为强制 Fast，false 映射为跟随请求，更新时均省略则保留。其它平台清除策略，公开分组不返回管理策略。迁移 269 保留旧开关行为，新字段经过分组复制、仓储和认证快照传递，当前认证缓存为 v40，版本不匹配时重建快照。

`free_openai_fast` 是同一分组的用户计费策略，不会改变出站 `service_tier`。只有 OpenAI 账号实际按 `priority`/`fast` 计费时才生效；网关使用同一模型映射、渠道价卡、峰值和长上下文时刻重新取得 Standard 价格，将其写入用户侧 `ActualCost` 和统一结算的基础金额，同时保留 Fast `TotalCost` 给 Usage Log、账号统计和账号额度。Standard 定价缺失时沿用零成本缺价记录，不能借此绕过原有定价错误边界；非 OpenAI 账号、普通 tier 和不可信认证快照均不适用。

该字段随 API Key 认证快照传递；当前 v40 的快照与失效规则见[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)。

Messages 兼容模型后缀与 Codex 模型规则的组合由 `gateway/provider` 拥有，纯 effort 资格由 `routing/capability` 判断。模型名中的旧 Codex max 与实际 reasoning 后缀继续区分；显式 output_config.effort 优先于模型后缀，最终 GPT-5.6 模型支持原生 max 时不降为 xhigh。调用时点仍在原请求改写和完成取值位置。

OpenAI 分组的 `max_reasoning_effort` 是显式推理强度上限，`max_reasoning_effort_over_limit` 取 `downgrade`（默认）或 `deny`。网关只对客户端真正发送的 `reasoning.effort`、`reasoning_effort` 和 Messages `output_config.effort` 执行策略，不会因为兼容桥为缺省 Messages 请求生成的默认 `medium` 而改变行为；模型范围映射先于上限比较。

`downgrade` 把超限值改写为上限，`deny` 在 HTTP 上返回 403 `permission_error`，Messages 返回 Anthropic `forbidden_error`，Responses WebSocket 以 policy-violation 关闭。复合 Key 已在鉴权中间件解析到具体 OpenAI 分组，因而使用该分组的策略。该动作和上限随认证快照传递，快照版本为 v40；版本不匹配的旧快照失效并从数据库重建。

<a id="openai_account_configuration"></a>
### 原生协议配置

账号协议和旧文本/工作负载字段的转换由 `account` 唯一实现；wire 的文本协议枚举仍属于 `protocol/openai`。逐次尝试的解析协议由 `requeststate.AttemptRoute` 持有，不写入账号持久记录或共享缓存。

OpenAI 与其它平台统一保存 `credentials.upstream_protocols`。API Key 可以独立启用 Responses、Chat、Embeddings、Images 生成/编辑、Responses WebSocket、Compact 和 Alpha Search；OAuth 原生选项依据 PAT/Agent Identity 认证能力收窄，不显示 Messages/Chat/Images 原生复选框。完整矩阵见[统一协议能力](protocol_capabilities.md#account_native_protocols)。

分组 `allowed_protocols` 控制客户端入口，`protocol_fallbacks` 为每个源指定单步目标。账号已启用原协议时直通，否则仅使用分组目标；不再由 `openai_text_route_mode` 在运行时选择目标。OAuth Images、PAT Alpha Search 和 WebSocket HTTP bridge 继续沿现有适用条件执行。`extra.openai_responses_continuation_supported` 和两个压缩开关保持独立，不把会话能力视为文本协议。

账号创建、更新、复制和批量更新不自动探测协议。旧工作负载/路由字段在输入边界转换并清除，历史探测状态继续丢弃；CN 不再异步镜像写回 OpenAI 路由字段。Responses 图片工具使用独立分组四态策略，优先于账号、渠道与全局，独立 Images 入口不受该分组策略影响。

OpenAI 兼容非流式响应的 usage 按 `usage`、`response.usage`、`data.usage`、`data.response.usage` 的顺序解析；前两条原生路径优先于 Cline 等兼容上游使用的 `data` envelope。同层的 hosted image usage 必须随对应路径读取，不能把不同 envelope 的 token 与图片用量混合。

`/backend-api/codex` 和无 `/v1` 别名服务特定客户端兼容，但仍经过 TokenRouter Key 鉴权、分组准入、调度和结算。Responses WebSocket 不支持 Qoder；其它平台是否可进入 OpenAI 兼容处理器由路由和平台专题共同决定，不能仅凭 URL 推断。

工具和命名空间的请求改写由 `protocol/bridge` 唯一执行；`gateway/provider` 根据账号、传输和 Compact 端点决定是否启用。HTTP Adapter 持有 `requeststate.ResponseTools`，分别保存当前尝试的 OpenAI/Grok 映射、namespace 与 Codex 名称；WS 的会话更新和当前 turn 名称分开，HTTP bridge 的下一轮声明保持原字节副本。非流、SSE 和 WS 使用同一恢复路径，未知字段、工具 ID、大数和原恢复次序保持。Codex 工具修正与统计继续使用原生唯一修正器，usage 与终态解析直接调用 `protocol/openai`。

推理历史的读取、请求回填与响应缓存由 `gateway/session.ReasoningHistory` 使用同一可选缓存能力完成，保留原 reasoning item ID、七天 TTL、两秒独立操作预算及读失败放行、写失败只记日志的规则。没有新增缓存实例或持久恢复保证。

### 远程压缩协议

TokenRouter 同时兼容原生 Remote Compaction V2 和旧版 Compact 端点。两者共享 compaction 输出语义，但请求路径、传输方式、账号能力设置和模型改写边界不同：

HTTP 路径识别、body-signal 提升、会话种子和结果日志由 gateway/httpapi 直接拥有；触发项检测、去重及移到 input 末尾由 protocol/openai 唯一实现。OpenAITextHandler 在原读取和校验位置执行这些步骤。请求字段视图与按需完整解码统一使用 gateway/requeststate，保留首个重复字段、宽容前缀读取、数字精度以及原错误前缀。

Responses 的历史 Chat 形状转换、工具 ID 清理、平台 schema 选择及 WS 兼容处理由 gateway/provider 组合原生协议实现，在线 HTTP 与 WS 消费者使用同一份算法。 Compact 单次恢复由 `gateway/compact.Recovery` 决定，`gateway/httpapi.CompactExecutor` 负责原响应关闭和恢复观测；app 只注入静态默认模型与日志配置。账号映射仍优先于默认模型，失败信号直接使用 `compact.Failure`，没有增加换号循环。

转换不截断客户端或工具文本；完整对象与字段补丁按调用时点同步。官方、OAuth 和显式 passthrough 的 `none` 保留规则，以及兼容地址的占位值删除规则保持各自边界。

| 边界 | 原生 `remote_compaction_v2` | 旧版 `/responses/compact` |
| --- | --- | --- |
| HTTP 识别 | 裸 `/responses` 请求同时携带 `stream=true` 且 `input` 含 `compaction_trigger`；`x-codex-beta-features` 不是识别门槛，但原生 V2 出站必保证包含 `remote_compaction_v2` | 客户端显式请求 `/responses/compact`，或带 `compaction_trigger` 但不满足原生 V2 条件的裸 `/responses` 请求被网关提升 |
| 上游传输 | 保持普通 Responses 流式链路，由上游直接返回包含 `compaction` item 的 SSE | 走独立 Compact 子路径；body-signal 流式客户端由网关把 unary JSON 结果合成为 Responses SSE，并在长时间等待时发送注释心跳 |
| 模型处理 | 沿用普通 Responses 的模型处理，不应用 `compact_model_mapping`，也不会因此追加 `-openai-compact` | 仅此路径在常规模型处理基础上应用账号 `credentials.compact_model_mapping` |
| 账号设置 | `extra.openai_native_compaction_v2_mode` 控制此路径的管理员开关 | `extra.openai_compact_mode` 与 Compact 专属模型映射只控制此路径 |

账号设置页以两个独立复选框控制“原生 V2 压缩”和“旧版 Compact 端点”，持久化为 `openai_native_compaction_v2_mode`、`openai_compact_mode` 的 `force_on` / `force_off`。调度只使用管理员开关，不读取探测结果，也不按未知/已探测结果分层。迁移 270 将旧 `auto`/缺省值按升级前的有效状态冻结为显式开关：历史明确不支持转为关闭，无结论保持原有开启行为；后续旧客户端的 auto 输入按开启兼容。原生 V2 仍要求账号支持 Responses 上游路由，旧版专属模型映射仅影响 `/responses/compact`，且管理界面只在启用旧版端点时显示。

管理端手动连接测试可以继续选择 `compact` 或 `legacy_compact`，但结果只显示本次路径是否成功，不再写入压缩能力状态或覆盖管理员开关。OpenAI API Key 普通文字测试新增 `protocol=responses|chat_completions`，显式直连所选上游协议，不受账号保存的路由模式覆盖，也不修改该模式；省略时沿用账号配置。OAuth 测试固定使用 Codex Responses，压缩测试固定使用对应 Responses 路径。

官方 Codex WebSocket v2 会先发送 `generate=false` 的预热 `response.create`，再以预热响应 ID 作为业务请求的 `previous_response_id`。严格续接比较会忽略逐请求变化的 `client_metadata`、仅用于传输的 `stream_options`，并把 `generate=false` 与后续省略该字段视为等价；`generate=true` 以及 model、instructions、tools、reasoning、store 等上下文字段仍必须保持一致，避免把无关请求错误串接。

OpenAI OAuth 的 HTTP、passthrough、旧版 Compact 与 WebSocket 出站会在模型映射和本地 fast 策略处理完成后，由网关生成 `x-codex-routing-hint`。提示至少包含最终上游模型；只有有效的 `priority`、`ultrafast` 或 `flex` 才附带 tier，`fast` 先规范化为 `priority`，`default`、未知值和空值均保持 model-only。

旧版 Compact 规范化必须保留 `service_tier`，否则提示会丢失已经生效的路由层级。该头由网关独占控制：所有账号类型都会先删除调用方及账号覆盖提供的任意大小写变体，只有 OpenAI OAuth 路径会重新生成；API Key 路径不得透传伪造提示。OAuth HTTP 也不再自动注入或透传旧版 `responses=experimental` beta 标记，但同一头中的其它独立 beta 项仍保留。

`x-codex-beta-features` 是 Codex 的会话级协商头：OAuth 普通 Responses HTTP 与 WebSocket 握手在客户端未声明时补入 `remote_compaction_v2`，客户端给出的非空值保持原样；原生 V2 请求无论账号类型都保证该 feature 存在。上游响应中的 `x-codex-turn-state` 会在 HTTP/SSE、SSE 转 JSON 与 passthrough 路径显式回传。

网关按 API Key 与客户端原始 session 记录最近签发账号；故障转移后，已知由其它账号签发的客户端回带值会被剥离，未知或同账号的值保持透传。 这份来源表由 `gateway/session.CodexTurnOrigins` 唯一持有，HTTP 的暂存、提交与回带过滤由 `CodexTurnStateHeaders` 执行。app 绑定同一实例和原粘性 TTL；每 256 次登记清扫过期来源，过期记录保持透传。来源表只保存账号 ID，不保存不透明回合状态值，也不与 WS 的回合状态缓存合并。

WebSocket 连接池把 routing hint 视为拨号和普通复用的软亲和：优先复用相同提示建立的连接，池满时仍可在硬兼容连接上排队，显式 continuation 也不会仅因提示变化而断链。握手 beta feature 与本 fork 的 TLS fingerprint profile 仍是硬兼容键，任一变化都禁止复用，并会使尚未完成的旧目标预热拨号失效。路由诊断只记录网关推导的最终模型、规范化 tier、传输类型、账号 ID、是否生成提示和 WS 亲和决策，不记录提示头值、token 或凭据。

Responses WebSocket 的 TTFT 只从实际 token delta 计算；若上游没有 delta，则携带完整文本或工具参数的 `response.output_text.done`、`response.function_call_arguments.done` 可作为语义输出兜底。`response.completed`、`response.done` 以及 content part/output item 等结构终态不产生 TTFT，纯终态响应保持未观测状态，避免把总耗时误记为首 token 延迟。

WS 报文、模型字段恢复及 usage 解析由 `protocol/openai` 拥有；OpenAI 恢复载荷与供应商错误分类由 `upstream/openai` 提供。传输选择由 egress 根据账号资格投影裁决，入站会话 Header、关闭码和读循环连接适配属于网关 HTTP 层，技术诊断保留在网关 provider。

Responses HTTP/SSE 同样区分结构进度与可见输出：`response.created`、空 reasoning item 等进度可以提交当前 attempt、解除首输出超时并关闭 pre-output failover 窗口，但不记录 TTFT；非空文本/工具 delta、完整文本或工具参数、图片结果以及终态内实际 output 才开始 TTFT。只携带 usage 的终态必须保持 TTFT 未观测。

OAuth passthrough 的 Codex 请求可以省略 `instructions`，网关会按请求模型补入内置 Codex 基础指令；显式提供的非空字符串保持不变，空白或非字符串值仍在本地拒绝。该规则同时适用于 Responses SSE 与旧版 Compact 请求。

Responses Lite 的报文重建与工具校验由 `upstream/openai` 唯一实现，执行适配负责按账号资格选择完整转换或仅禁用并行工具。

Responses Lite 通道由 HTTP `X-OpenAI-Internal-Codex-Responses-Lite: true` 或 WebSocket `client_metadata` 中的对应标记识别，不根据模型名称推断。任何向 OpenAI 上游转发该标记的 HTTP、passthrough、旧版 Compact 或 WebSocket 请求都必须强制顶层 `parallel_tool_calls=false`。OAuth 账号还会统一设置 `reasoning.context=all_turns`，并把私有 namespace 工具声明迁入 `input.additional_tools`；API Key 账号保留除此之外的标准 Responses 请求语义。未携带 Lite 标记的普通 Responses、Grok 和专用 Images 请求不应用这些约束。

OpenAI OAuth 账号承接 Anthropic `count_tokens` 时会调用 Responses `input_tokens` 端点；缺少 scope、端点不存在，或上游代理在 API 前返回 HTML 格式的 `403` 时，网关改用本地 token 估算并返回成功结果。这类端点级失败不会冷却、临时踢出或标错账号；其它结构化鉴权与上游错误仍进入正常健康策略。

OpenAI OAuth 的普通 Responses 请求默认原样保留 Codex namespace 工具声明，并保留 `function_call`、`tool_call`、`custom_tool_call`、`mcp_tool_call` 历史项上的 `namespace`；普通消息等非调用项上的残留字段仍会清理。旧版 Compact 请求始终摊平 namespace 并移除输入项字段，API Key 出口也按标准 Responses schema 清理。

API Key Responses 回放还会校验输入项 ID 前缀：message 使用 `msg`、工具调用使用 `fc`、reasoning 使用 `rs`；不符合类型约束的 ID 直接删除而不改写，避免伪造标识指向另一上游对象。兼容层把 function-only 上游返回的 `fc_` 工具调用还原为客户端 `custom_tool_call`/`tool_search_call` 时，会分别将 ID 类型改为 `ctc_`/`tsc_` 并保留后缀；再次降级到 function 时恢复原 `fc_`，输出项没有对应的 function ID 则继续删除。

流式恢复用上游 ID 匹配后续事件，只向客户端发送类型转换后的 ID，保证历史重放和 SSE 生命周期都有效。仅当 OAuth 账号的兼容中转不接受 namespace 时，才应启用账号 `extra.openai_responses_flatten_namespaces=true` 恢复平名行为。每次 failover attempt 都会清空上一账号登记的平名映射，避免响应还原状态串到下一账号。

Responses 工具定义在进入 OAuth passthrough、Codex transform、Grok 或 API Key Chat 分流前统一修正显式为 `null` 的 `parameters.type`，将其归一为 `object`；处理范围包括顶层 `tools[]` 和多轮历史 `input[].tools[]` 中的嵌套工具。缺失 `type` 的合法宽松 Schema 保持原样，不能为了兼容而补写并收窄客户端语义。

Responses 请求降级到 Chat Completions 时，工具结果中的 `input_image`、`image_url` 和完整图片 data URL 不能留在只接受文本的 `tool` message。转换器会按 `call_id` 从工具结果中提取图片，把原位置替换为稳定标记，并在对应的一组工具回复后追加用户多模态消息；并行调用按工具声明顺序归属图片，孤儿或未回答调用不携带媒体。没有可识别图片的工具结果必须保留原始字节，避免无关 JSON 重编码改变提示缓存前缀。

OpenAI API Key 账号以 `force_chat_completions` 承接 `/v1/messages` 时，Chat 流中的并行 `tool_calls` 必须按 `tool_calls[].index` 聚合 ID、名称和全部参数分片，在流收尾时再按 index 顺序生成各自连续闭合的 `content_block_start`、`input_json_delta`、`content_block_stop`；参数分片暂存后一次拼接，聚合期间通过 Anthropic `ping` 维持下游活动，文本与 thinking 仍即时流式输出。

空工具参数归一为 `{}`，call ID 保持原样，以便下一轮 `tool_result.tool_use_id` 配对。Anthropic `tool_choice.disable_parallel_tool_use=true` 映射为 Chat 顶层 `parallel_tool_calls=false`，字段缺失或为 `false` 时保持默认 `true`；`auto`、`any`、`none` 和具名工具的选择语义不变。

## 模型与能力

客户端模型先经过 Key、渠道和账号层映射。OpenAI 内置别名、reasoning effort 归一化、旧版 Compact 端点支持、图像/embedding 能力和传输能力会影响候选账号；模型列表只公开当前分组可请求的结果。

GPT-5.6 的内置产品仅为 `gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`。裸 `gpt-5.6` 不作为预设型号或 Sol 别名，OAuth 归一化与用量计费候选也不再自动将它改为 Sol 或旧 GPT；未知名称沿用兼容上游的既有透传边界，不因此保证上游支持。管理员显式 Key、渠道和账号映射仍然有效，历史配置和用量记录不回写。模型目录查询与能力来源见[模型目录与市场](model_catalog_and_marketplace.md#model_catalog_metadata_lookup)。

`gpt-6-astra` 的最终上游请求只接受 `low` 至 `max` 推理档位。网关不为 Astra 硬编码推理档位改写；需要兼容遗留 `minimal` 或 `none` 的分组，应在“推理强度映射”中按请求模型配置目标值（例如分别映射到 `low`）。`none` 仅可用于映射，不能作为最大推理强度，因为它没有可比较的强度排名；未配置映射时，普通转发层不得自行把一个推理档位改成另一个档位，客户端显式值由上游或对应兼容层决定是否接受。GPT-5.6 仍支持 `none`。本地价格目录和代码回退均保留 Astra 的官方标准价，远端价卡尚未同步时也不得退回到其他 GPT 型号计费。

Usage Log 将客户端显式档位和最终上游档位分别记录：`requested_reasoning_effort` 保留策略改写前的值（包括 `none`），`reasoning_effort` 以最终上游请求体为准；分组映射发生时，界面可据此展示改写箭头。协议转换或兼容策略未实际转发的字段不记录最终档位。请求未显式提供 effort 时，才允许从模型名后缀推导，并继续受模型能力门槛约束，避免把第三方模型名中的普通 `-max` 后缀误记为推理档位。

最终为 `high`、`xhigh`、`max` 的请求在使用首输出超时策略的链路中均选择高 effort 档；协议桥仍可按真实上游能力调整实际转发值，例如 Anthropic 兼容转换可把不支持的 `max` 降为 `xhigh`。

API Key 的普通调度能力只表达 `text_generation` 与 `embeddings` 工作负载，不再用 `chat_completions` 同时代表工作负载和协议。Responses 生图等必须使用原生 Responses 的路径仍有独立能力门禁：以 Responses 为首选协议解析后若落到 Chat，该账号不能承接此类请求。OAuth/Codex 账号还可能包含 Realtime、WebSocket、旧版 Compact 端点状态和客户端身份限制。未知模型可以在管理员明确配置的兼容上游中透传，但没有定价或能力证据时不能虚构价格与功能。

Images API 的流式与非流式上游请求都脱离客户端请求取消信号继续执行，并由上游响应超时控制最终回收。生图属于长耗时且上游可能已经产生实际成本的媒体任务；客户端中途断开不能取消上游并丢失已完成图片的计费结果。下游写失败不改变图片产出和结算事实。

OpenAI HTTP 准备、同账号恢复和响应消费由 `gateway/provider/openaiforward` 接入原生 upstream 原语；账号切换仍只有 `gateway/text` 的一套循环。HTTP-to-WS 恢复与入站 turn 编排由 `gateway/ws` 组织，连接池与帧解析继续由 upstream 持有。Compact 恢复资格与状态位于 gateway/compact，keepalive 和提交后的错误写出位于 gateway/httpapi；没有复制另一份计费或会话缓存。

固定执行器使用 gateway/execution 的显式 Request/ExecutionResult 和同步 OutputSink。候选计划仅在实际选择返回时捕获，缺失用 PlanProvided 表达，不为填充结果增加查询或在执行结束后重新计算。完成数据在入队前冻结，WS 继续保留每 turn 原有定价时点、模型链和部分失败资格。

## 额度与调度

OpenAI 为通用高级调度器提供平台能力适配，评分核心由 scheduler 拥有。只有最终目标 Group 的 `scheduler_type=advanced` 时，OpenAI 路径才在共同 active/schedulable、分组、模型、限流和并发硬过滤后使用通用 Top-K 评分；`basic` 保留原有默认选择路径。高级分组可用稀疏 `advanced_scheduler_overrides` 覆盖全局 Top-K、评分权重和粘性开关，未设置字段继续继承网关设置。

高级分组还会考虑所需 transport/capability、账号优先级、负载、排队、错误率、近期延迟、配额余量和粘性上下文。previous response、WebSocket 会话和显式 session 可约束账号复用；只有策略允许时才能迁移。

OpenAI 专属能力只在账号和请求具备对应条件时加入候选或分数：Responses transport、WebSocket、旧版 Compact、previous response、订阅优先和 Codex 额度余量都不会排除缺失这类可选信号的普通账号。OAuth 5 小时、7 天等上游窗口和自动暂停仍由 OpenAI 设置及账号运行状态控制，不随高级调度器通用化而迁移到其它平台。

OAuth 账号的 5 小时、7 天等上游窗口和重置时间保存在账号运行状态中，可触发临时限流或自动暂停；API Key 的文本协议和压缩资格只由管理员配置决定。OpenAI 不再采集上游站点声明倍率，也不按该值进行低倍率优先或高级评分。账户本地 `rate_multiplier` 和渠道上游计费模型来源继续用于 TokenRouter 结算，但都不是用户余额、订阅、Key 限额或用户平台额度。

管理 API 的 `GET /admin/openai/accounts/:id/quota` 保持只读；账号列表使用 `POST /admin/openai/accounts/:id/quota/refresh` 查询上游并把重置次数写入 `account.extra.codex_reset_credit_snapshot`。正数次数只有同时取得到期明细时才覆盖快照，前端水合时过滤已过期明细并把次数收敛到仍有效的卡片数量。该 extra 键只用于展示缓存，不触发调度 outbox；Spark 影子账号的查询可解析母账号额度，但快照仍写在被查询的行上，且列表继续只提供查询入口，不提供真实重置按钮。

## 失败与诊断

账号状态更新使用凭据快照/CAS，避免较早请求在 token 已刷新后再次封禁账号。401/403、429、endpoint 不支持、内容策略、网络错误和上游 5xx 分别分类；只有可切换且客户端响应未开始的失败才进入下一账号。OpenAI 上游代理或 CDN 返回的 HTML 403 只证明当前链路或端点被阻断：请求仍可按既有规则 failover，但不得递增连续 403 计数、临时停调或永久禁用账号；结构化 JSON 与纯文本 403 继续按账号级策略处理。

API Key passthrough 池模式会把 `pool_mode_retry_status_codes` 命中的 HTTP 错误先转换为未提交响应的 failover，在同账号预算耗尽后才换号；未配置时默认覆盖 401、403、429，显式空列表可关闭这类按状态码重试。原生 Responses 上游返回的确定性 `400` 在现有账号策略、池模式重试和错误透传规则均未要求改写或故障转移时，按真实 400 回写，并保留脱敏后的 `message` 与诊断所需 `type`、`code`、`param`；瞬时处理错误和容量类 400 仍保持可重试或通用网关错误语义。

图片模型被 Codex 文本端点以 plan-gated `400` 拒绝时属于端点错配：当前尝试仍切号，但不写模型冷却，避免影响同账号后续通过 `/v1/images/*` 正常生图；专用 Images 端点上的同类拒绝仍按真实账号能力缺失冷却，图片模型的 `404 model_not_found` 也不豁免。Responses HTTP 与 WebSocket v2 首次发送时保留加密 reasoning/compaction；若上游明确返回 `invalid_encrypted_content`，同账号恢复最多重试一次，清理账号绑定的加密状态但保留未加密 compaction。

账号与模型组合的瞬时失败按连续结果累计：首次失败只记录，第二次短冷却，第三次及以后长冷却。请求间隔较长不能把持续故障误当成恢复，只要未超过状态回收 TTL，稀疏流量中的失败仍继续累计；任一成功结果立即清零该组合。TTL 只负责回收长期不再使用的条目，不能兼作短窗口的连续失败重置条件。

流式错误要保持 SSE/WebSocket 协议完整；Responses 可产生 `response.failed`，非流接口返回相应 OpenAI envelope。入站 WebSocket 的下行写不得继承独立的 ingress 租约取消信号：旧 ingress 路径绑定客户端请求生命周期并叠加 write timeout，v2 relay 只受 write timeout 限制，退出路径再通过显式 Close/CloseNow 回收连接；这样租约丢失不会在终态事件写入期间抢先硬关 TCP，客户端可先收到终态事件，再收到 1013 关闭帧。

上行写继续继承控制面取消，以便快速回收上游连接。HTTP 200 SSE 中的 `rate_limit_exceeded` 按语义状态 429 进入故障转移与池模式重试，但不使用该 200 响应的正常配额快照头写入默认账号冷却。上游容量降载通常先发 `error`、再以 `response.failed` 收尾；`server_is_overloaded` / `slow_down` 的前置错误帧在尚无业务输出时继续留在 attempt 缓冲中，触发有界同账号重试和 pre-output failover，并按请求级瞬时故障处理，不冷却当前账号。

已有真实输出或重试耗尽后不能重放请求，SSE 与 WS HTTP bridge 会仅在客户端副本中把这两个致命码改为可重试的 `server_error`，原始事件仍用于账号策略与观测。客户端尚未收到业务输出时，池模式账号的其它瞬态流内处理错误也可在请求级预算内重试同一账号；旧版 Compact 桥接心跳注释不算业务输出，即使已提交 200 响应头，只要没有语义 SSE 载荷，最终失败仍必须追加 `response.failed`。OpenAI Responses 标准流与 passthrough 流若只收到前导事件和完全不含 output、usage、error 的 `response.completed` / `response.done`，会在尚未写出客户端业务内容时按静默拒绝切换账号，而不是记录 0/0 成功；终态含 usage、error、任一输出项，或此前已出现语义输出时均不触发该规则。

一旦真实输出开始，网关不得重放请求或切换账号。最终错误还可命中[网关错误响应策略](gateway_error_policy.md)，但规则不会把失败结算成成功。排障应同时检查账号类型、required transport/capability、客户端限制、privacy status、模型映射、quota reset、代理/TLS 和 attempt 记录。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[模型目录与市场](model_catalog_and_marketplace.md)。
