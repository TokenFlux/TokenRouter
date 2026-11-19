# 网关请求生命周期

本文描述 AI 网关请求从 Gin 路由进入，经过认证、模型名称处理、提供商调度、上游转发、故障转移，到用量结算的共同阶段。修改跨协议热路径时，用本文核对阶段顺序和失败语义；具体端点、供应商字段和模型能力见接口分类的平台文档。

## 章节导航

- [入口族与处理器](#入口族与处理器)：请求由哪个协议分支处理。
- [共同处理管线](#gateway_pipeline)：阶段顺序、计时观测边界与模块不变量。
- [认证与准入](#apikey_authentication)：API Key 认证、协议门禁、WS 复核与分组回退。
- [模型名称链](#模型名称链)：模型映射、范围过滤、协议回退与响应模型恢复。
- [提供商选择与故障转移](#account_selection_and_failover)：调度输入、辅助入口、会话哈希与重试退避。
- [转发与流式输出](#protocol_conversion_boundary)：平台转发、流式不可逆点与错误分类。
- [SSE 保活](#sse_keepalive)：下游空闲心跳、上游心跳消费与字节计数。
- [用量与结算](#用量与结算)：中断保留、完成器与幂等扣费步骤。
- [审核与搜索协作](#moderation_search_boundaries)：审核裁决、搜索额度与工具模拟边界。
- [SystemOne 决策请求](#systemone-决策请求)：SystemOne 同步尝试与冷却规则。
- [新增入口检查清单](#新增入口检查清单)：新增入口或平台时逐项核对。
- [Qoder Chat 请求编排](#qoder_gateway_execution)：Qoder Chat 准入、刷新重试与排空顺序。
- [平台执行与资源归属](#upstream_attempt_ownership)：单次执行、流输出、图片聚合与关闭顺序。

## 入口族与处理器

`RegisterGatewayRoutes` 在面板 `/api/v1` 之外注册客户端协议入口：

| 入口族 | 主要用途 | 处理器分派 |
| --- | --- | --- |
| `/v1`，以及裸 `/models`、`/responses` 等兼容别名 | Anthropic Messages、OpenAI Responses/Chat/Embeddings、图片、视频、模型列表和用量 | 进入统一文本、媒体和模型入口，选号后按实际提供商执行 |
| `/v1beta` | Gemini 原生模型、生成、流式生成和 token 统计 | 按 Google 方式认证后进入 `GeminiNativeHandler` 或 `ModelsHandler` |
| `/antigravity/v1`、`/antigravity/v1beta` | 限定 Antigravity 平台的 Claude 和 Gemini 专用入口 | 在上下文写入强制平台，再走通用 handler 和调度 |
| `/backend-api/codex` | Codex/ChatGPT 风格的 Responses、Realtime 和 sideband | `OpenAITextHandler`、`ResponsesWSHandler` 和 `LiveHandler`，部分路径有专用认证与路由限制 |
| 批量图片管理 | 提交、查询、下载、取消和清理任务 | 专用 handler 和 service；查询入口按任务归属认证并沿用任务既有提供商 |

- `/v1/messages`、`/v1/responses` 和 `/v1/chat/completions` 共用文本提供商循环；HTTP 路由层不看分组平台，选号后按实际提供商所属平台执行单次调用。媒体、WS 和 Live 各自管理资源生命周期。
- `/antigravity/*` 附加的强制平台过滤在分组回退后仍然生效。
- `/v1/sub2api/billing` 返回普通 `404`，请求在路由层结束，不进入 API Key 的非消费请求分支。

<a id="gateway_pipeline"></a>
## 共同处理管线

网关路由在请求体限制和鉴权之前固定英文响应语言，内置错误返回英文；网站语言由面板路由和 HTML 请求处理。

```text
请求体/连接限制、request ID、Ops 采集
                 |
             API Key 认证
                 |
       复合 Key 选组 -> Key 级模型重定向
                 |
       用户/团队/分组/IP/权益准入
                 |
    RequireGroup + 客户端协议准入门禁
                 |
             协议 handler 分派
                 |
  解析与归一化 -> 内容策略 -> 用户并发槽
                 |
          等待后的权益二次检查
                 |
  会话/分组策略/能力解析 -> 提供商选择 -> 提供商并发槽
                 |
       请求转换、凭据/代理和上游转发
                 |
      可重试错误 -> 受限故障转移循环
                 |
  成功响应/流 -> 用量解析 -> 幂等结算 -> 记录
                 |
      响应模型元数据恢复与 Ops 完成采集
```

### 计时与响应观测边界

- 计时与保活层级：HTTP 文本请求在鉴权前安装请求级计时（覆盖复合 Key 预读正文），其后紧接统一 SSE writer，位于 Ops 输出采集和响应模型改写内侧，串行化所有正文写入与心跳，见 [SSE 保活](#sse_keepalive)。
- 排队耗时拆分：首次提供商准入前的正文读取、前置处理和两类槽位排队分别计时，首次准入后的提供商排队独立记录；事件时间线按入口起算。
- 输出观测边界：流式响应经共享 HTTP 观察边界记录上游事件、有效内容交付和首次 Flush，计时口径与计费、重试输出标志分离。
- 上游与本地 writer 分离：上游输出经 `NewResponseSink` 绑定计时来源，最终 HTTP writer 写入成功后记录；网关本地错误直接走普通 writer，不继承上游来源，见 [HTTP 接口](../interfaces/http_api.md#response_errors)。

### 阶段顺序不变量

1. 复合 Key 先按完整 `前缀/模型` 选组，Key 级模型重定向处理去掉前缀后的模型。
2. 客户端协议准入基于普通 Key 绑定分组或复合 Key 最终选中分组；被拒请求在协议 handler、选号和计费之前终止。
3. 网关入口与协议解析器在选组、前置审核、模型改写及转发之前校验 JSON 请求对象和 Multipart 标量控制字段，直接拒绝任意深度的解码重名键与协议控制字段的大小写变体碰撞，合法请求的原始字节保持原样。
4. 等待用户并发槽期间余额、订阅或额度可能变化，拿到用户槽后须经 BillingCache 二次检查权益。
5. 模型权限、分组白名单和提供商资格按逐层解析后的对应层模型名判断，客户端别名与最终路由模型区分处理。
6. 统一文本入口与 Gemini 生成动作在选定提供商后检查计费模型价格（每次换号重检），Responses WS 每轮执行同检；缺价返回本地错误，手动零价放行，见[缺价处理](../domains/routing_and_billing.md#missing_model_pricing)。
7. 上游成功后才递增 RPM 软计数并安排用量结算；本地拦截、内容拒绝和上游失败分记各自审计与运维记录。
8. HTTP 200 内的失败事件按实际状态执行提供商策略；HTTP 转换或 WS 桥已执行的健康策略经 `AppliedOpenAIResponseEffects` 传给输出端复用，未传入时由输出端执行终态策略；HTTP 提交、重试窗口与实际输出分开记录。
9. 响应模型别名恢复仅修改协议元数据，正文原样保留；工具恢复状态按请求与 turn 持有（WS 会话更新时，在途 turn 保持自身状态）。

### 模块边界与运行不变量

| 模块 | 职责边界 |
| --- | --- |
| `gateway/httpapi`、`gateway/ws`、`gateway/live` | HTTP/WS/Live 协议适配、同步写入与 Flush、连接与 turn 生命周期 |
| `gateway/text`、`gateway/forward` | 文本提供商切换循环、计数预检、通用请求准备与转换策略 |
| `gateway/requeststate`、`gateway/modeltrace` | 报文副本、规范化结果、执行提示、路由状态与响应模型恢复链 |
| `gateway/provider`、`upstream`、`protocol/bridge` | 平台凭据与出站策略、上游单次交换、纯协议转换算法 |
| `gateway/completion`、`gateway/searchtools`、`gateway/moderationflow` | 幂等完成结算、工具模拟与审核完成编排 |
| `gateway/session`、`errorpolicy`、`scheduler` | 会话摘要与归属、不可变错误策略快照、无凭据候选调度 |

运行时关键不变量：

- 单一故障转移循环：跨协议文本共用唯一提供商切换循环；平台单次交换、单步协议转换和 Compact 错误恢复在当前提供商内完成，不重入外层切换循环。
- 尝试级状态隔离：每次 attempt 创建独立状态（如图片意图提示在分组改写后重判，不跨尝试残留）。分组在读写时均复制，后续 attempt 绑定新分组而不改写先前快照；telemetry 只保存关联观测信息。
- Messages 身份解析时机：选号前解析 Claude Code、版本和 Thinking 身份，选号后再执行平台专有策略、warmup 拦截和串行队列。
- 共享资源与释放：用户槽绑定等待前请求 context（取消即释放），图片本地限流保留独立等待、拒绝和作用域；Responses 失效密文在 HTTP 与 WS 间共享会话存储和 TTL，HTTP 转 WS 时沿用已解析的模型映射与请求变换。
- WS 连接池与停机屏障：WS 连接池首次使用时懒启动，关闭屏障保证退出后无法重建；Grok、Live 与 WS 共用拨号器，Agent Identity 凭据失效同步作用于该池；WS 每轮读取在线参数快照（管理端可覆盖进程默认值）。

<a id="apikey_authentication"></a>
## 认证与准入

凭据提取与认证错误展示归 `apikey/httpapi`，Key、用户、团队和 IP 校验归 `apikey.Authenticate`（`AccessSnapshot` 区分 owner、payer、actor 和 team）。网关认证入口在此之上组合复合 Key 选组、模型改写与资金准入。

认证缓存版本号、深拷贝隔离与失效机制见[认证快照](provider_scheduling_and_cache.md#认证快照)。

- 策略绑定与失败隔离：最终选组授权后绑定 `AccessSnapshot`、分组手动 Fast 策略与 Key 的 `force_claude_1h_cache`（跨协议回退后仍可读取），付款用户取本次请求付款主体。认证失败时，供 Ops 使用的已加载 Key 信息与认证成功快照分开存放，能加载出 Key 不代表授权通过。

通用 API Key 认证步骤：

1. 入口防滥用与大 Header 限制：通用网关拒收 query 中的 Key，接受 `Authorization: Bearer` 和 `x-api-key`，Gemini 兼容入口额外接受 `x-goog-api-key`。
2. 从认证缓存或仓储加载 Key、User、Group、Team 及复合映射；加载失败区分不存在、过载、团队生命周期异常和内部错误。
3. 每次检查 Key 禁用状态、团队 Key 生命周期、成员限额、IP 规则，以及用户是否存在和启用。
4. 普通 Key 须绑定分组（历史未绑定返回 `GROUP_REQUIRED`）；复合 Key 按请求模型匹配映射生成普通 Key 视图；随后校验最终分组可用性、用户权限并执行 Key 级模型重定向。
5. 确认分组与提供商成员关系明确；按 `auto`、`subscription` 或 `balance` 确定资金来源，消费入口检查 Key 过期与配额、订阅窗口限额或余额；指定订阅不可用、额度不足或不覆盖最终分组时直接拒绝。
6. 写入认证上下文，路由按最终分组 `allowed_protocols` 执行协议门禁后进入 handler；`last_used_at` 更新失败不阻断已认证请求。

特殊入口与准入边界：

| 场景 | 准入与复核规则 |
| --- | --- |
| 协议门禁 | 普通 Key 不读请求体；复合 Key 在认证阶段预读正文放回并按前缀选组后走同一门禁。被禁协议按客户端协议返回 `403`（记 `LocalPolicyDenied`），在选号、重试、回退和结算前结束；Google 入口与通用入口错误检查顺序各自独立 |
| Responses WS 后续轮次 | 拿到用户槽后、转发前直接回源复核 Key、用户、团队、成员限额、IP、WS 协议许可、Key 过期/总配额、当前订阅、余额和滚动限额。失败或资格失效时释槽并关闭连接，不用旧认证缓存或 Fast 刷新失败默认值放行。连接固定原 Key ID、owner、payer、team 和结算来源，凭据重建或付款/结算来源变更时要求重连；独立快照保证已放行轮次归属不变，已产生用量照常结算 |
| 非消费与管理入口 | `/v1/usage` 和部分批量任务管理跳过消费准入（额度耗尽或过期仍可取回/清理数据），身份、用户、团队、IP 与归属校验照常执行；用量查询保留指定订阅的失效状态。模型列表对复合 Key 无需选定单个分组，仍执行适用的 Key 额度、余额和订阅检查 |
| 分组回退 | 无自动默认分组；分组不可用、客户端受限和无效请求仅回退至管理员显式配置目标，目标须重过用户、团队、协议、模型、订阅和资金检查。运行时重发有效 Key、分组、`RoutePlan` 和粘性预取，不改认证缓存；单请求回退次数有上限，已开始输出或已取消不重放 |

进入协议 handler 后的预处理顺序：

- 按端点限额读取请求体并宽容解析 JSON/Multipart，依次执行用户提示词替换（HTTP 与 WS 共用同一套策略引擎，调用点回源规则并替换）、协议解析、客户端识别、内容审查和 Ops 元数据设置。
- 用户并发槽在选号前获取；Key 并发和 RPM 在认证与资金检查后预占，规则见 [API Key 请求上限](../domains/routing_and_billing.md#api_key_request_limits)。

## 模型名称链

一个请求沿链路依次经过五个模型名：

```text
client_model
  -> composite_actual_model
  -> api_key_redirected_model
  -> group_mapped_model
  -> provider/upstream_model
```

- `client_model`：客户端传入的原始模型名（复合 Key 含分组前缀）。
- `composite_actual_model`：复合 Key 选组后去掉前缀的模型名。
- `api_key_redirected_model`：Key 级一跳重定向结果，位于选组之后、分组与提供商映射之前。
- `group_mapped_model`：分组映射结果，用于白名单检查、计费和用量映射链（白名单与计费各自配置模型来源）。
- `provider/upstream_model`：提供商映射得到的最终路由键；`requested_model`、`upstream_model` 和去重后的 `model_mapping_chain` 分别记录客户端意图、实际发送模型和变换路径。

### 名称变换、范围与协议回退

- 保留客户端形态：仅在配置映射或同型号跨平台编码转换时改名，拼写、版本分隔符、日期和 effort 后缀保持原样；后续价格、缓存和健康状态使用各阶段确定的完整模型 ID。失败回退同步记录模型链，回退前后价格互不借用。
- 每次尝试重构造：failover attempt 可按新提供商重构造请求，已解析的单跳映射只执行一次。模型列表从当前可路由目标反推别名；保存映射允许目标暂不可路由，实际请求时返回标准无提供商或无模型错误。
- 路由与尝试隔离：`routing.RoutePlan` 保存最终分组、入口协议、Key 到分组模型链及白名单（`RestrictModels`、`RestrictionModelSource`）与计费模型来源；`AttemptRoute` 仅存请求内并在 fresh 或数据库复核后重算，提供商映射按调用时读取，不写回共享缓存。
- 模型范围与通配符：提供商未配置模型范围或为空时，使用平台与认证类型默认目录；非空清单以清单为准，精确映射可引入自定义模型。末尾通配符参与匹配但不展示；手动 `*` 仍受协议、认证和端点硬能力约束。
- 协议回退与冷却顺序：候选准入与模型冷却按"协议 -> 提供商状态 -> 模型窗口"顺序判断（窗口原始冷却、overages 可调度性与剩余时间分别计算）。`protocol_fallbacks` 为每个入口存有序目标数组：未配置自动匹配，空数组仅原生协议，非空数组限定转换目标；每个候选先试原生协议，再试已有单步转换。
- 计费与平台特性解析：计费模型与实际上游模型分开解析；OpenAI 普通/Compact 映射与 Bedrock 区域路由复用 `ModelPolicy`；确认 Lite 标记后，OAuth 提供商做完整工具规范化，API Key 提供商限制并行工具。
- 调用点延迟读取：协议地址（含 Kimi/Zhipu/DeepSeek 适配）、RPM/会话/串行/窗口配置、Codex 图片桥接/工具/指纹模式及 Header 覆写均在调用点读取最新提供商字段。
- Codex 请求内状态：Codex 身份和指纹的请求内状态仅归 HTTP 适配层，Header 与正文共用同次生成 ID，failover 按固定顺序覆盖，异步完成不持有 HTTP 状态。

### 响应模型采集

`ResponseModelObserver` 仅从上游响应元数据采集模型名（正文、工具参数与转换器生成值不参与），按值随结果和完成快照传递，不影响失败记录与结算资格。每次出站尝试独立观察，重试或恢复时清空上次值，WS 按 turn 和 response ID 区分：

| 协议 | 采集规则 |
| --- | --- |
| Responses | 优先采用终态中声明的模型 |
| Chat / Messages | 采用第一次有效声明 |
| Gemini | 采用最后一次有效版本 |

<a id="account_selection_and_failover"></a>
## 提供商选择与故障转移

选择输入包括分组、请求模型、分组映射结果、会话 hash、已失败提供商集合、端点能力和可选强制平台。先处理客户端限制等分组回退，再按最终分组的 `scheduler_type` 选择 `basic` 或 `advanced`；候选覆盖组内所有平台，组外不参与。

选择器综合评估：

- 组内成员关系、专用入口强制平台、分组和提供商模型范围及一跳映射。
- 提供商启用状态、有效期、代理、凭据、上游资格、临时不可调度、模型与提供商限流及配额状态。
- 调度快照可用性、粘性会话、优先级与负载、最近使用时间、并发槽和可等待队列。
- 特殊端点能力（图片、Realtime/WS、Grok 付费媒体资格或站点特有模型能力）。

模式与租约规则：

- 排序与计费解耦：候选排序和高级评分只看调度信号，上游声明倍率、`upstream_cost` 权重和 OAuth 参考倍率不参与评分；提供商本地 `rate_multiplier` 与上游计费模型来源在选号与映射完成后用于结算。
- 调度模式：`basic` 走历史路径；`advanced` 在硬过滤后调用通用评分核心按 Top-K 加权尝试并复核并发槽，参数按分组覆盖 -> 全局运行时设置合并（详见[调度器模式](provider_scheduling_and_cache.md#advanced_scheduler_selection)）。
- 反馈与无槽入口：仅本次实际使用 `advanced` 时回写错误率、TTFT 和切换统计；`count_tokens` 与可用性探测等无槽入口同样按最终分组选模式，不占并发槽与会话数。
- 租约与等待协议：`AcquireUser` 返回请求 `Lease` 和带计数归属的 `WaitResult`；`Lease.Select` 返回 `AttemptLease` 及可选 `WaitPlan`（scheduler 执行等待循环，HTTP 同步发心跳）。
- 槽位回滚与复查：仅释放确认拿到的等待计数，提供商补全失败等后续错误立即归还槽位，重复释放幂等；会话是否保留由 `Finish` 裁决；用户等待结束后原位复查权益。

### 计数与辅助入口

| 入口 | 执行顺序与资源边界 |
| --- | --- |
| Messages `count_tokens` | 先资金预检，再无槽选择，每次尝试从原报文重做分组映射；Anthropic/OpenAI 走上游，Gemini 在 OAuth scope 不足时回退，Grok/Kimi/Zhipu/DeepSeek 本地计数，Qoder/Antigravity/Bedrock 返回不支持；不计费不完成，与文本共用兼容指标采样器 |
| OpenAI 兼容计数 / Responses `input_tokens` / Grok 估算 | OpenAI 兼容计数先路由规划、资金检查再无槽选择；Responses 预检先资金检查再规划并归还选号槽位；Grok 本地估算完全本地完成（跳过资金、选号和上游）；均无完成提交 |
| AlphaSearch / Embeddings | 复用固定请求与响应处理，搜索授权元数据由 provider 模块提供 |
| Live / sideband | 读体前执行分组协议与功能门禁，顺序为审核 -> 资金 -> 立即取用户槽；会话、observer 取消与等待计数独立管理并在关闭阶段按序释放，用量费用为零；报文外模型重定向与 WS 共用单跳映射和追踪规则 |

### 会话哈希

客户端会话 Header、Grok 选号、强制平台、手动信号、内容回退、无状态图片入口与用量日志各有优先级，仅用于日志的 Header 不参与通用选号。内容种子、Grok 模型隔离种子和 Gemini 摘要格式由 `gateway/session` 提供；新旧两种哈希从同一种子派生，旧哈希通过请求状态交给粘性读取。

### 故障转移

故障转移只处理包装为 `forward.UpstreamFailoverError` 的可切换错误（含错误阶段、归属、原始响应和重试信息）；OpenAI 特有的容量与请求过大错误在 provider 层识别。`FailoverState` 按提供商 pool-mode 重试预算决定同提供商重试、排除换号、短暂等待或放弃。

- 退避策略：普通同提供商重试固定等待 500ms；标记为请求级瞬时故障的容量错误按 500ms、1s、2s、4s 指数退避，上限 8s；客户端取消立即打断等待。
- 冷却与停调：提供商健康模块按错误分类写入临时不可调度；Messages 重试用尽后的冷却跳过请求级瞬时故障和池模式提供商；HTTP 非 2xx 本身不直接停调提供商。
- 粘性换号与上限：已绑定粘性的请求换提供商时，可能把普通输入按缓存读取计费以反映缓存未命中成本；选号耗尽后的单提供商重试与等待有固定上限，客户端取消立即停止选号和等待。

<a id="protocol_conversion_boundary"></a>
## 转发与流式输出

### 平台转发

每次 attempt 用原始或规范化请求结合本次提供商重建供应商请求，注入凭据、代理、TLS 指纹、客户端标识、Thinking/工具配置与上游模型。

通用报文与转换算法在 `protocol/{anthropic,openai,gemini,google,bridge}`，采样与 Max effort 策略在 `gateway/forward`，每次请求或 attempt 创建独立转换状态。HTTP 层负责同步写入与 Flush，upstream 负责首次输出判定、取消、失败排水和提供商内重试，外层网关决定换号。

- Anthropic / Claude（普通、API Key 透传、Vertex、Bedrock）：每次 attempt 单独保存 Beta 过滤、工具名映射和错误诊断；重试用尽后的兼容冷却按最新池模式判定；调试追踪句柄待请求和后台工作全部结束后关闭。
- Anthropic 跨协议模型采集：承接 Chat Completions 或 Responses 时，转换读取器先在改写模型前采集原始 `message_start` 声明的模型（流式与缓冲一致，未声明留空）。
- Gemini / Antigravity：按 attempt 创建图片计数和工具名恢复状态；图片数取单个响应片段内联图片最大值，未观测到图片才回退原模型名；HTTP 层负责三种客户端协议错误格式、规则覆盖和 Ops 写入顺序。
- OpenAI / Grok 回合状态头：首次输出暂存时不登记来源，实际提交后写入按 API Key 与客户端原始会话区分的来源表；请求带回的值仅在确认来自另一提供商时移除（来源未知、同提供商或过期继续透传）；配额头和回合状态强制透传时机由流读取器触发。
- 非流式有界读取：`infra/httpclient` 默认上限 128 MiB（多读 1 字节判超限并保留原错误链），超限时记录 Ops 并输出对应协议 502；读取函数不关响应体，重试和取消由执行链决定。

### 流式输出与重试窗口

- 不可逆点：调用上游前通过 `HeartbeatAdjustedWrittenSize` 记录扣除全部心跳后的写出字节数；一旦写出实际业务内容，不可换提供商（防止拼出损坏流），已记录的心跳不算业务输出。
- 暂存窗口：Responses 前导事件（`response.created`、`response.in_progress`）与等待终态判定的可重试 `error` 帧可留在 attempt 缓冲以保留 failover 窗口；不可重试错误及时转发。实际输出开始后只能追加协议允许的流错误事件或断开连接；非流式仅在未写响应前可 failover。
- 错误分类与协议拒绝：错误分为本地准入、业务能力不足、调度容量不足、可切换上游错误、不可切换转发错误五类。协议准入拒绝（选号前）错误码：Anthropic 返回 `permission_error`，OpenAI 返回 `protocol_not_allowed`，Google 返回 `PERMISSION_DENIED`。
- 观测与脱敏：Ops 记录归属、endpoint、实际平台（未选号记 `unknown`）、模型与提供商；对外错误隐去凭据、内部代理和数据库细节。
- 特殊释放与独立租约：Qoder 流式请求进入上游后采用完成后释放（客户端断开停写下游，在预算内收完尾部 usage 后释放槽位）；等待中和非流式按各自取消策略释放。WS 入站连接与 Live 租约各自续租、失租自取消，与请求 Lease 独立。

<a id="sse_keepalive"></a>
## SSE 保活

保活按连接分别维护：上游心跳只说明 TokenRouter 与提供商之间有数据到达，下游反向代理与 CDN 活跃由装在 `TextRequestTiming` 后的统一组件 `DownstreamSSEKeepalive` 负责。

该组件覆盖 Messages、Chat Completions、Responses、Gemini 和 Qoder 的文本输出（直通与转换共用），文本读取器只保留上游空闲超时与断连后用量收集。

### 配置与阶段

| 配置项 | 默认值 | 生效范围与边界 |
| --- | --- | --- |
| `gateway.streaming.keepalive_interval_seconds` | 10 秒（允许 5-30 秒，0 关闭） | 按下游最近一次字节写入计时发送心跳 |
| `gateway.streaming.data_idle_timeout_seconds` | 180 秒（0 不限） | 仅限制已提交 SSE 正文停滞，不作用于上游响应头等待期（后者由传输层响应头超时和首输出保护限制） |

两个可写入阶段：

1. 上游等待：流式文本经 `timing.Do` 发起交换后，响应未提交且空闲满间隔时提前提交 `200` 与 `text/event-stream` 并写心跳 Flush；handler 取可写 Header 或开始输出时关窗（只读快照不关窗，已返头但首事件暂存时仍保活）。非流式、明确非 200 或非 SSE 不提前提交；`timing.Do` 独立计数未返头交换，不误判上游已返头。
2. 已提交 SSE：状态 2xx、内容类型 `text/event-stream` 且停在完整帧边界时按下游空闲发心跳（半帧不插字节；LF 与 CRLF 可跨多次 Write 混用）。普通正文和排队心跳重置下游时钟，上游心跳不重置。

### 心跳帧格式与停发条件

| 协议 / 客户端 | 下游心跳内容 |
| --- | --- |
| Messages（默认） | `event: ping` + `data: {"type": "ping"}` |
| Messages（Claude Code UA >= `2.1.193` 且存在打开的受支持内容块） | 对应空 delta（工具块发空 `input_json_delta.partial_json`），块关闭后恢复 ping |
| Gemini 原生 | `\n\n` 空事件（避免 Google SDK 把注释当 JSON） |
| 其他文本协议 | `: keepalive` 注释 |

- 终止帧停发：识别 `[DONE]`、`message_stop`、`error`、`response.completed`、`response.failed`、`response.incomplete` 后停发；前缀识别仅查帧前 4096 字节（大帧继续跟踪边界）。
- 结构化终态通知：读取器也可通过 `OutputEvent.Terminal` 通知终态（不受 4096 字节前缀限制），终态字节经完整 writer 链写完后停发，与定时器互斥，短写返回 `io.ErrShortWrite`。
- 正文停滞上限：取上游活动（发请求、收响应头、实际读字节，含注释和 ping）与最近一次 handler 写入的较晚时间（中央心跳不刷新这两个时钟）；绕过传输观察的路径亦可以实际写入维持保活。超过上限后停发心跳，读取器按原上游超时处理。下游写失败或 Hijack 立即停发；请求结束等待心跳 goroutine 退出。

### 字节计数、提前提交与上游过滤

- 心跳字节口径：`StreamHeartbeatBytes` 汇总排队、图片流读取器、Compact/图片 SSE、图片 JSON 空白和统一文本心跳；`HeartbeatAdjustedWrittenSize` 同口径计算（仅心跳返回 Gin `-1`，无心跳保留原 Size 含仅提交 Header 后的 0，读取异步计数时锁定 writer 大小）。
- 提交与重试口径：`OutputWriter.Written()` 判 HTTP 是否已提交（能否发头），`ContentWritten()` 判业务输出与重试窗口。
- 提前提交 200 约束：后续错误以客户端协议的 SSE 错误结束，不可再改 HTTP 状态；迟到的上游响应头不可发送也不登记归属。响应头在读取器私有 map 中准备，`ResponseSink.Begin` 先关闭提前提交窗口再复查提交态，仅仍可应用的头才触发归属登记；已提交后 HTTP writer 返回 Header 副本防止并发冲突；只有心跳的请求仍可在预算内重试或换号。
- Gemini 元数据与尾帧：成功请求 ID 等元数据仅在 `OutputContext` 私有 Header 准备，首个业务事件前不碰 Gin Header；原生与转换共用有界消费，终态后用量尾帧最多继续收集 5 秒，见 [Gemini 流生命周期](../interfaces/gemini_upstream.md#gemini_stream_lifecycle)。
- 上游心跳过滤：读取侧消费注释、`ping`/`heartbeat`/`keepalive` 与空 data 帧；JSON `type` 优先于 event 名（`event: ping` 携带业务数据时保留）。逐行缓冲上限 16 行、16 KiB，遇未知字段、超限或 EOF 不完整候选时保守释放非注释行；明确错误事件不因 data 为空忽略；上游活动计时在过滤前。
- 独立路径与 TTFT：排队心跳由 `WaitObserver` 在请求 goroutine 写入（Gemini 排队同发空 SSE 事件）；Compact 与图片保留专用控制器，图片错误按已提交 Content-Type 选 SSE 或 JSON（JSON 空白心跳后仍返合法 JSON）。注释、ping 与空文本、thinking、工具参数 delta 不建立 TTFT 数据事件。

## 用量与结算

### 采集与中断规则

- 计费桶与价卡继承：统一文本保留上游 usage 的 5 分钟和 1 小时缓存创建分桶及 Speed，缓存读取只扣一次；用户售价与提供商成本分算，价卡按"分组价 -> 共享价 -> 内置价"继承；记录平台取实际执行提供商。
- 中断部分用量：Anthropic 网关与 OpenAI 兼容 Messages、Responses、Chat 在终止事件前中断时，只要随错返回部分结果，仍提交已观测 usage（无结果不记）；`UpstreamFailoverError` 不带部分结果，重试成功只计费一次。
- 国产供应商转换流中断：原生 Anthropic 转 Responses 流在客户端写失败后停止下游输出，继续读完上游并推进状态机，直到末尾 `message_delta` 最终 token 或达有界读超时。
- OpenAI OAuth 图片中断：HTTP 成功后若上游 body 传输中断，仅在未写出实际图片内容前（JSON keepalive 空白不算）按 502 进入提供商策略和 failover；客户端取消、deadline、响应体超限及首字节后中断不换号。

### 完成器与任务边界

- 异步有界队列：完成执行器在 `gateway/completion` 实现，worker 使用与已结束请求取消脱钩但自带超时的 Context；队列满按各入口配置的 drop、sample、sync 或 mandatory 回退策略处理，并通过指标和日志暴露压力。
- 停止与快照捕获：同步提交前捕获提供商、Key、付款主体、用量、计费时刻、请求 ID 和额度更新标记的独立快照（Forward 与 OpenAI 倍率缓存每分钟定期清理）；停止时等待排队任务与已接受同步溢出任务，扩缩容与停机共用屏障，停止后无法重开。
- 创作台与批量图片：creative 和 batchimage 自行管理完成资格、预占、捕获、释放和恢复，不进入网关完成队列；创作台生成阶段经 `scheduler.Lease` 管理用户槽与提供商槽，上游返回即释放，交付和结算不占槽。

### 结算步骤

1. 归一化不同协议的 token 分桶、媒体尺寸和时长、缓存与长上下文计费规则。
2. 按计费模型来源、共享价格配置、提供商成本、用户/分组/订阅倍率、高峰倍率及分时倍率计算费用（WebSocket 多轮按当前 turn 开始时刻算时变价）。
3. 用 `request_id + api_key_id` 认领结算幂等键，并用请求指纹防止同一 ID 被不同 payload 复用。
4. 在单个 PostgreSQL 事务里锁定付款用户，按模式分配订阅或余额，并累计团队成员、Key 配额和速率及上游提供商额度。已放行普通请求超出指定订阅剩余额度时，订阅用量封顶，超出部分按余额倍率扣付款主体余额（允许为负）；该回退仅适用于已放行或并发在途请求，绑定已耗尽订阅的新请求仍直接拒绝，批量图片预占仍要求指定订阅完整覆盖。
5. 结算成功则写已结算 Usage Log 并更新缓存与最后使用时间；失败则写含计算成本的待对账 Usage Log（`actual_cost` 记 0）并返回 worker 错误（状态如实标为失败）。Usage Log 写入失败不会导致重复扣费。后台结算失败无法改变已发给客户端的响应，通过监控、幂等重试和对账处理。

<a id="moderation_search_boundaries"></a>
## 审核与搜索协作

- 内容审核：网关经 `moderation.Check` 取得本地裁决（仅提取当前轮内容），行为用户与付款用户分传；供应商错误识别在 upstream，推理重试归网关；创作台不留存媒体；事务保证见[内容审核](../domains/content_moderation.md#content_moderation_decision_pipeline)。
- Brave 与 Tavily 搜索：由 `search` 选商并预占额度，失败释放已确认预占；请求取消后不试他商、不标代理故障，额度回滚使用独立最多 3 秒清理预算；Redis 结果不确定按故障放行处理；配置替换后在途请求沿用旧快照，停机时全版本一起等待，见[搜索编排](../domains/search_orchestration.md)。
- 工具模拟与原生搜索：`gateway/searchtools` 负责工具识别、提供商和分组启用裁决、协议事件及合成 usage，重试与完成归请求编排；Grok 原生搜索与 OpenAI AlphaSearch 归对应 upstream。资金是否提交只以资金事务为准，通知、搜索配额和审核记录均不能作证。
- 独立 Web / X 搜索：按解析 -> 认证 -> 资金 -> 审核 -> 选号顺序执行，相同查询每次调用生成独立资金请求 ID；完成前同步取得提供商快照并提交后才释放提供商资源，异步记录仅读快照。

## SystemOne 决策请求

`POST /v1/systemone` 使用共同鉴权与模型映射，由 `gateway/systemone` 管理同步请求的提供商尝试，`upstream/jev` 完成单次交换。

- 每次尝试执行资金复查与价格预检；答案有效时按用量提交完成记录或用量告警，捕获完成快照后释放提供商槽。
- 复合 Key 端点识别和错误输出按 SystemOne 规则处理；429、529 的 `Retry-After` 由 Jev 解析，通用健康组件按提供商配置应用冷却，见 [Jev 与 SystemOne](../interfaces/jev_upstream.md)。

## 新增入口检查清单

新增网关入口或平台适配器时，逐项核对：

- body 和 header 限制、request ID、Ops error logger 和 API Key 错误格式。
- 普通 Key 与复合 Key 支持、模型提取位置，以及非模型管理入口的资源归属校验。
- 选组、Key 重定向、分组映射和提供商映射的一跳顺序，以及模型列表与响应模型恢复同步。
- 用户和提供商并发、会话隔离、粘性和取消路径的完整槽位释放。
- 允许同提供商重试或换号的错误边界，以及业务输出开始后不会误入 failover。
- 稳定的 request ID、请求指纹、`requested_model`/`upstream_model` 和实际平台归属。
- HTTP 适配、业务用例、存储、前端调用方，以及 API 接口测试与协议测试同步更新。

<a id="qoder_gateway_execution"></a>
## Qoder Chat 请求编排

Qoder 提供商参与普通文本入口的提供商循环，在尚未写出业务内容前支持一次同提供商凭据刷新重试。站点、模型和认证能力由 Qoder 适配器检查；单步协议转换仅覆盖既定路线，不支持任意会话恢复。

- 准入与重试：选号前做资金预检，等待后复查资金，RPM 只累计一次；已提交实际输出不重放。
- 释放与完成：流式上游有独立执行预算，客户端取消后在途提供商槽待完成捕获结束再释放；部分结果只进入一次完成处理，记录或结算失败不重调供应商。
- 排空顺序：平台尝试与统一 HTTP 入口均受网关请求屏障保护，停机时先拒新请求并等待在途请求，再停完成队列和共享连接，超退出预算时报告未完成。

<a id="upstream_attempt_ownership"></a>
## 平台执行与资源归属

### 分层与会话归属

- 上下游隔离：供应商交换、请求构造与原生读取在 `upstream`；`gateway/provider` 在调用点组装提供商、出站策略和错误观察，Gin 与完整配置留在上层。OpenAI 响应读取、图片和辅助查询各自维护取消与终态，WS relay 和连接池独立于入站 WS 编排；续接报文和失效密文剥离为纯函数。
- 会话归属校验：HTTP Responses 归属由 `gateway/session` 校验（HTTP 仅存已认证 user 和 key 标识），兼容同用户跨 Key 与历史仅按 Key 记录的归属，读取失败拒绝授权。OpenAI 会话归属存储在 HTTP 与 WS 间共享且按需建连；WS 每轮价格快照由入站连接持有；缺失 usage 的低频诊断采样状态独立于 Gin 和完整提供商信息。

### 观测、风控与平台特性

- 响应观测：WS 重放输入为私有字段不进结果 JSON；每次 attempt 或 turn 的 `ResponseObserver` 独立记录模型与实际服务档位（终态声明优先，冲突按回退规则，出站档位在完成计费时与观测值合并）。
- 上游风控警告：统一经 `forward.UpstreamWarning` 传递，收到警告不代表请求可结算。
- OpenAI `cyber_policy`：在 provider 层识别，当前 HTTP 或 WS turn 首次标记与清除由 HTTP 层调 `moderationflow.Mark`（保留标记键、截断正文和已观测用量，清除后下一 turn 才接新证据）；已透传错误经哨兵通知收尾，不影响换号、写入和扣费资格。
- Grok 执行边界：文本、图片、视频和 Voice 共用执行器、取消策略与活动屏障；Chat 转 Responses 不适用时回到同次派发的原生 Chat 路径；档位规则在调用点读取设置和价格（WS 复用当前 turn 快照）；运行时只有一个切换循环、一个资金记录器和一套缓存。
- OpenAI 兼容文本：Raw Chat、原生 Anthropic、Messages 与 passthrough 共用提供商切换循环；CompatResponses 按提供商、Key 和提示缓存区分隔离键，并有独立 TTL 与续接禁用规则；Codex 额度观察按固定节流间隔执行并异步写回后台任务。

### 图片与 Compact 流控及性能快照

- 保活与转换：流式图片拿到提供商槽后、HTTP 发送前启动保活；上游返 SSE 头后交读取器接续，普通 JSON 和 HTTP 错误继续保活至有界读体结束。OpenAI API Key 的客户端模式与提供商端点模式分别解析；标准图片 JSON 按 `data[]` 条目转完成事件（仅 URL 时先下载为 Base64），SSE 也可聚合为非流式 JSON。
- 计费与错误边界：图片和 usage 从原始响应观察一次，总 usage 随最后一张图片输出；转换失败后已观测完成的图片仍参与完成处理；跨尝试保留的注释字节不算实际输出。首次心跳提交 200 后，流式图片错误改写 SSE 错误事件，非流式图片仍返合法 JSON。
- 性能与写入互斥：TTFT 由统一性能快照按全局首事件或首有效内容设置派生，与 HTTP 提交、重试窗口分开判断；完成任务一次写入完整性能和 usage（请求摘要留生命周期与失败诊断，详情按请求 ID 关联 usage）。写入包装器仅在 HTTP 内部使用，停止与写入互斥。

### 停机排空顺序

`GatewayRequestsAndAttempts` 与 `HTTPRequests` 是两个并列的等待屏障，停机按固定顺序排空：

1. 先取消额度恢复操作。
2. 拒绝新的请求和平台尝试进入，等待请求尾部的完成快照入队以及在途尝试结束（`HTTPRequests` 完成时间与停止监听时间独立）。
3. 关闭完成队列和共享存储。
4. 停止提供商授权会话和底层配额服务。按需创建的 Live 和 WS 资源在应用启动时不提前打开。

相关文档：[系统架构](system_architecture.md)、[提供商调度与缓存一致性](provider_scheduling_and_cache.md)、[网关策略控制](../domains/gateway_policy_controls.md)、[上游提供商能力矩阵](../interfaces/upstream_provider_matrix.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)、[领域目录](../domains/index.md)、[接口目录](../interfaces/index.md)。
