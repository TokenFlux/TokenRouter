# 网关策略控制

本文说明分组、价格配置、提供商和全局运行设置共同决定的网关策略：每项策略的归属层级、生效优先级及失败处理。平台协议转换、提供商调度算法和具体 HTTP 字段见对应文档。

## 章节导航

- [策略层级](#gateway_policy_layers)：配置在 API Key、分组、价格配置、提供商和全局之间的归属。
- [分组模型与功能策略](#group_routing_policy)：`routing_policy`、模型白名单、图片策略与历史字段兼容。
- [协议与能力准入](#协议与能力准入)：客户端入口协议、单步转换路线与媒体/作业准入。
- [模型路由](#模型路由)：模型解析链、跨分组 fallback 与分组调度器覆盖。
- [认证与隐私](#认证与隐私)：OAuth-only、隐私确认、提供商白名单与会话隔离。
- [推理与 Header](#推理与-header)：reasoning effort、Fast 档位判定与出站 Header 安全。
- [重试与降级](#重试与降级)：同组重试、跨组 fallback 与错误响应规则的边界。

<a id="gateway_policy_layers"></a>
## 策略层级

| 层级 | 管理的策略 | 职责范围之外 |
| --- | --- | --- |
| API Key | 分组选择、复合前缀、Key 级模型重定向、Key 限额、Fast 偏好、强制 Claude 1 小时缓存 | 上游提供商凭据和平台 |
| Group | 组内提供商关联、基础或高级调度器选择、高级调度稀疏覆盖、客户端协议与媒体准入、fallback、OAuth 与 privacy、推理上限、模型可见性、Fast/Standard 计费和 RPM | 上游 token |
| PricingConfig | 可被多分组复用的价卡、价格引用、计费设置、计费模型来源和提供商成本配置 | 模型权限、请求模型改写和功能开关 |
| Provider | 凭据、代理、模型映射与白名单、重试状态码、临时不可调度、Header override 和 capability | 超出分组对用户公开的能力范围 |
| Setting/config | 高级调度评分参数、跨分组运行策略、兼容开关、默认 Header 与 UA、缓存和安全策略 | 分组调度器选择及各提供商运行状态 |

层级归属约束：

- 每个分组最多关联一个价格配置，并可单独关联一份模型属性配置（仅影响模型展示和客户端配置导出，属展示配置）。
- 可请求模型和请求参数由请求策略决定，提供商资格由分组和提供商策略决定，价格配置只参与计费。
- 字段归属决定缓存失效范围、管理权限和审计来源；不要为表单便利将同一字段复制成多份互相覆盖的配置。

<a id="group_routing_policy"></a>
## 分组模型与功能策略

`groups.routing_policy` 保存模型映射、模型白名单、白名单检查阶段、网页搜索模拟和 Bedrock CC 兼容设置。

生效与历史字段兼容：

- 开关语义：管理表单无策略总开关，保存后直接生效；模型白名单保留独立开关。存储中的 `enabled` 字段仅兼容历史停用草稿：保存前不参与请求处理，重新编辑保存时写入 `true`，只打开弹窗不改服务端值；未配置策略的存量分组不受限。
- 透传保留字段：`features` 文本、`supported_model_scopes`（支持的模型系列）和 `mcp_xml_inject`（MCP XML 协议注入）在管理表单不展示也不提交，运行时均不读取（路由不按模型系列过滤候选；Antigravity Claude 请求转换在工具名以 `mcp__` 开头时默认开启注入，不读分组开关）。编辑其他策略时，存储中的既有值原样保留。

模型映射、白名单与媒体策略：

- 模型映射：`model_mapping` 是跨平台模型名映射表，精确匹配优先，末尾通配符其次，每阶段只改写一次。
- 模型白名单：`allowed_models` 为模型列表，`restriction_model_source` 可选 `requested`、`group_mapped` 或 `upstream`（选 `upstream` 时逐提供商校验）。开启限制且白名单为空时拒绝所有模型。模型目录共用该策略，修改、停用或删除价格配置不影响模型权限和请求改写。
- 图片与网页搜索：Codex 图片行为在分组「协议与访问」配置，优先级依次为分组协议设置、提供商设置、全局默认值（忽略 `routing_policy` 中旧桥接默认值）。网页搜索按全局门禁、提供商覆盖顺序判断。
- 快照读取：策略经分组管理保存，普通 Key 和复合 Key 快照分别深拷贝。请求优先读取匹配分组的可信认证快照，回退与管理查询读取目标分组策略；读取或解码失败时，模型白名单按原样生效。

## 协议与能力准入

分组没有平台字段，协议能力按入口和转换目标控制：

- 入口与转换：`allowed_protocols` 控制客户端入口，`protocol_fallbacks` 按入口存有序转换目标（未配置自动匹配，空数组仅原生协议，非空限定目标与顺序）。提供商 `credentials.upstream_protocols` 只勾选原生协议；完整的 24 项协议目录和默认值见[统一协议能力](../interfaces/protocol_capabilities.md)。
- 硬资格规则：候选先匹配原生协议，既无原生协议又无允许的单步转换路线时不参与评分；模型、配额、媒体和会话约束照常生效。换号时重新解析，请求失败后不临时换用其他协议。转换目标无需同时作为客户端入口开放。
- 媒体与已有作业：Images 生成和编辑单独控制。Responses 图片工具使用 `responses_image_policy`，支持继承、开启桥接、关闭桥接、屏蔽四种状态（分组优先，继承时沿用提供商和全局设置）。已有视频和批量作业资源按任务归属授权，新建入口开关不影响它们。

## 模型路由

模型名按以下顺序逐层解析：

1. 复合 Key 前缀解析。
2. Key 级重定向（精确匹配或最长末尾通配符）。
3. 分组路由（`routing_policy.model_mapping`，精确名称或末尾通配符；Messages 与其他文本入口共用，无协议专用覆盖层）。
4. 提供商映射与白名单。

请求模型、上游模型和计费模型分别记录，响应模型名按客户端协议恢复。分组管映射、白名单与功能，价格配置管价格，提供商管供应商与站点差异；可见模型仅包含当前可请求结果，未知或有歧义价格显示为未定价。

分组选择与普通 Key：

- 客户端限制的逐组读取和回退由 `routing.ResolveClientGroup` 执行。通用入口返回读取原错，OpenAI 快照入口可保留未解析分组；客户端识别在读取当前分组后按固定短路顺序执行。强制平台旁路及最终分组资金和权限复查由各入口负责。
- 普通 Key 创建和调用必须选定分组，系统不自动创建或查找默认分组，未关联分组的提供商不进入候选池。Key 的 `fallback_when_group_unavailable` 只允许回退到管理员配置的不可用回退分组。

跨分组 fallback：

| 类型 | 触发与解析位置 | 共同约束 |
| --- | --- | --- |
| 客户端限制 fallback | 在入口完成（Messages 与计数入口先识别 Claude Code，其他协议在通用协议门禁解析回退链）；每跳重检权限、协议和资金，成功后同步替换有效 Key、资金来源和请求分组，初始回退不额外累计 RPM | 目标分组重新检查 Key、模型、协议、权限、计费和 `scheduler_type`；强制平台入口保留平台条件，提供商列表按目标分组重算；出现循环、目标失效或策略不匹配时终止 |
| invalid-request fallback | 按分组显式配置的无效请求回退链执行 | 同上 |
| unavailable fallback | 按分组显式配置的不可用回退链执行 | 同上 |

选择器只校验已授权的当前分组，回退链由入口处理。

分组调度器与稀疏覆盖：

- `scheduler_type` 默认为 `basic`；设为 `advanced` 时在硬过滤后启用通用高级评分。Top-K、评分权重、粘性加权和订阅优先是网关通用设置（无全局启用开关），不会将 `basic` 分组切为 `advanced`；OpenAI 和 Grok 专有能力仅作用于具备这些能力的提供商。
- 高级分组可在 `advanced_scheduler_overrides` 中稀疏覆盖，缺失字段依次继承数据库通用设置和 `scheduling.advanced` 默认值。可覆盖项：Top-K、评分权重、粘性与订阅开关、错误率和 TTFT 的 EWMA alpha（`(0, 1]`）、sticky escape 开关及阈值（TTFT 为正，错误率在 `[0, 1]`）。
- 已出现字段（含 `false` 和 `0`）以分组值为准，空对象表示全部恢复继承。`basic` 分组即使留有该对象也不会读取，切为 `advanced` 后重新生效。

## 认证与隐私

- OAuth 与隐私门禁：`require_oauth_only` 排除 API Key 等非 OAuth 提供商；`require_privacy_set` 要求上游隐私状态已确认。OpenAI 与 Antigravity 隐私检查和设置可在创建、刷新或维护流程触发，热路径读取已验证状态，刷新成败以刷新结果为准。
- 客户端判定与传输选择：OpenAI 客户端访问判定在 HTTP、Live 和自动探针共用同一套规则（按需读取 Header，TLS 路由只传匹配结果）；WS 传输选择共用唯一 egress 决策，并在调用时读取配置和动态客户端放行设置。
- 提供商模型白名单：缺失或为空时允许任意模型 ID（含统一目录未收录型号）；非空时在提供商映射后校验最终模型，支持末尾通配符和 `*`。Spark、OAuth、站点及端点资格分别检查，OAuth 和 API Key 透传模式同样执行这些规则。
- 会话隔离与防伪造：不同提供商、团队和用户上下文彼此隔离不复用。OAuth passthrough、Claude Code-only 和客户端白名单须与提供商类型联合校验，客户端伪造 User-Agent 无法获得额外权限。

## 推理与 Header

- Reasoning effort：分组可设置最大 reasoning effort 和 effort 映射，由平台适配器转为 Anthropic thinking、OpenAI reasoning 或 Qoder、Gemini 原生字段。手动关闭、平台不支持和管理员上限三者优先级固定；无效字符串保持默认值或被拒绝，不会静默提高推理强度。
- 档位快照：HTTP 在策略改写点记录客户端档位并固化请求策略快照；WS 共用报文规则和用量解码器，每轮区分客户端原始档位与最终发给上游的档位。桥接补出的默认档位不算客户端请求；任何阶段不从模型名后缀推断档位；提供商组合映射消费策略后的有效档位，见[提供商组合映射](routing_and_billing.md#provider_effort_model_mapping)；异步完成只读取固化值。
- Fast / Ultra Fast 判定：系统、分组和 Key 三层规则统一由 `gateway/tierpolicy.Resolve` 执行，HTTP 报文和 WS 帧共用结果。系统层短路或分组强制关闭即结束（不执行后续 Key 开启时的查价）；Key 强制开启产生的新档位须再经过一次系统策略。协议拒绝、HTTP/SSE 错误和 WS 错误帧由各协议适配层输出。
- Header 策略：由平台默认值、全局设置和允许的提供商 override 组合，认证头、hop-by-hop 头、Host 和长度等受保护头不可随意覆盖。
- 平台兼容与缓存：Anthropic beta/cache、OpenAI UA/客户端元数据、Claude Code 模拟、dateline 和 metadata 兼容在平台适配层处理并经出站安全校验。Key 级 `force_claude_1h_cache` 与全局 OAuth 1 小时缓存注入差异见 [Claude 缓存 TTL 策略](../interfaces/anthropic_upstream.md#claude_cache_ttl_policy)。

## 重试与降级

- 池模式默认同提供商重试状态码为 `401`、`403`、`429`、`502`、`503`。提供商显式配置的列表会替换默认列表，显式空数组关闭按状态码匹配。具体是否重试仍取决于入口的错误分类、输出状态和重试预算。
- 换号取决于平台错误分类、临时不可调度规则、响应是否已开始、attempt 上限和上下文截止时间。
- 切换限制：同分组内不同平台的提供商可竞争同一模型，但 `previous_response_id`、WS/Live 状态、thinking 签名及已开始的流会限制可切换范围。
- 阶段边界：重试发生在同分组内 attempt 之间，fallback 是显式跨分组策略，错误响应规则仅改变最终返回内容。修改这些阶段时须确保不重复扣费、不重复写流，且不把本地策略拒绝记为提供商故障。

相关文档：[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)、[路由与结算](routing_and_billing.md)。
