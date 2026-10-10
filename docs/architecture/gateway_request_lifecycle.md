# 网关请求生命周期

本文描述一个 AI 网关请求从 Gin 路由进入，经过认证、模型名称处理、提供商调度、上游转发、故障转移，到用量结算的各个共同阶段。修改跨协议的热路径时，用本文核对阶段顺序和失败处理。具体端点、供应商字段和各平台的模型能力见接口分类的平台文档。

## 章节导航

- [入口族与处理器](#入口族与处理器)：请求由哪个协议分支处理。
- [共同处理管线](#共同处理管线)：各阶段的顺序和不能调换的约束。
- [认证与准入](#认证与准入)：修改 API Key、权益或请求上下文时读取。
- [模型名称链](#模型名称链)：修改模型映射、列表或响应模型恢复时读取。
- [提供商选择与故障转移](#提供商选择与故障转移)：修改调度、并发、粘性或重试时读取。
- [转发与流式输出](#转发与流式输出)：修改上游调用和错误返回时读取。
- [用量与结算](#用量与结算)：修改用量记录、价格或扣费时读取。
- [审核与搜索协作](#moderation_search_boundaries)：修改审核裁决、搜索额度和工具模拟时读取。
- [新增入口检查清单](#新增入口检查清单)：新增入口或平台时逐项核对。
- [Qoder Chat 请求编排](#qoder_gateway_execution)：修改 Qoder Chat 的准入、尝试和完成顺序时读取。
- [平台执行与资源归属](#upstream_attempt_ownership)：修改单次执行、流输出和关闭顺序时读取。

## 入口族与处理器

`RegisterGatewayRoutes` 在面板 `/api/v1` 之外注册客户端协议入口。主要入口族：

| 入口族 | 主要用途 | 处理器分派 |
| --- | --- | --- |
| `/v1`，以及裸 `/models`、`/responses` 等兼容别名 | Anthropic Messages、OpenAI Responses/Chat/Embeddings、图片、视频、模型列表和用量 | 进入 app 绑定的统一文本、媒体和模型入口，选号后按实际提供商执行 |
| `/v1beta` | Gemini 原生的模型、生成、流式生成和 token 统计 | 按 Google 的方式认证后进入 `GeminiNativeHandler` 或 `ModelsHandler` |
| `/antigravity/v1`、`/antigravity/v1beta` | 限定 Antigravity 平台的 Claude 和 Gemini 专用入口 | 在上下文写入强制平台，再走通用 handler 和调度 |
| `/backend-api/codex` | Codex/ChatGPT 风格的 Responses、Realtime 和 sideband | `OpenAITextHandler`、`ResponsesWSHandler` 和 `LiveHandler`；部分路径有专门的认证和路由限制 |
| 批量图片管理 | 提交、查询、下载、取消和清理任务 | 专用 handler 和 service；查询类入口按任务归属认证，沿用任务已有的提供商 |

普通的 `/v1/messages`、`/v1/responses` 和 `/v1/chat/completions` 共用文本提供商循环。HTTP 路由层不看分组平台；`UnifiedTextExecutor` 在选号后调用实际提供商所属平台的单次执行器。媒体、WS 和 Live 各自管理资源的生命周期。`/antigravity/*` 入口额外加上强制平台过滤，分组回退后这个过滤仍然生效。

`/v1/sub2api/billing` 已从公开入口移除，访问时返回普通的 `404`，请求在路由层就结束，也不会进入 API Key 的非消费请求分支。

<a id="gateway_pipeline"></a>
## 共同处理管线

网关路由在请求体限制和鉴权之前设置英文响应语言，内置错误使用英文。网站语言由面板路由和 HTML 请求处理。

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

管线里有几条顺序约束：

- 复合 Key 先用客户端传来的完整 `前缀/模型` 选出分组，Key 级模型重定向再处理去掉前缀后的模型。
- 客户端协议准入使用普通 Key 绑定的分组，或者复合 Key 最终选中的分组。被拒绝的请求在协议 handler、提供商选择和计费之前就结束。
- 等待用户并发槽的过程中，余额、订阅或额度可能变化，所以拿到用户槽后要通过 BillingCache 再检查一次权益。
- 模型权限、分组白名单和提供商资格，都用逐层解析后对应那一层的模型名判断；客户端别名和最终路由模型是两个不同的名字。
- 统一文本入口和 Gemini 的生成动作在提供商确定后检查计费模型是否有价格，每次换号都重新检查。Responses WebSocket 每轮执行相同检查。缺价时返回本地错误，手动零价可以放行；适用范围和配置方式见[缺价处理](../domains/routing_and_billing.md#missing_model_pricing)。
- 上游成功后才增加 RPM 软计数，并安排正常的用量结算。本地拦截、内容拒绝和上游失败分别写各自的审计和运维记录。
- HTTP 200 里出现的失败事件，按事件表达的实际状态执行提供商策略。WS 桥已经执行过的副作用，通过本请求的 `ResponseFailureEffects` 交给输出端，输出端只处理一次。HTTP 提交、重试窗口和实际输出三者分开记录。
- 响应里的模型别名恢复只修改协议的元数据字段，正文里恰好相同的字符串保持原样。工具恢复状态由 `requeststate.ResponseTools` 按请求和 turn 持有，WS 会话更新时，还在输出的 turn 保持自己的状态。协议算法在 `protocol/bridge`，HTTP 输出适配在 `gateway/httpapi`。

### 执行器装配

HTTP 入口由 app 构造，执行器、会话和资源都由 app 提供，路由直接拿到需要的接口实现。

- 文本：OpenAI Responses、Chat 和 Messages 的 HTTP 绑定直接接收用户槽和图片槽资源（各一份）、Cyber、审核、归属读取和资金接口。`gateway/httpapi/openaiattempt.Runtime` 绑定平台单次执行能力、调度反馈、槽位和完成器，每次 `Open` 创建一份独立的尝试状态。三个文本入口通过 `UnifiedTextExecutor` 调用所选提供商的单次执行器。强制 Antigravity 和 Gemini 原生入口使用 `textattempt.Runtime`，同样是 app 绑定平台调用、选择反馈和资源，每次 `Open` 只创建请求和尝试状态。
- WS：入站和每轮单步执行的接口由 `gateway/ws/httpapi` 装配，和文本运行时共用同一套尝试绑定。
- 媒体：图片、视频、音频、Embeddings 和 Alpha Search 的请求适配和完成捕获由 `gateway/httpapi/mediaentry` 装配，失败输出和资源释放与文本共用。
- Wire 手动绑定执行器和完成器，接口测试使用相同的绑定和函数句柄夹具。平台单次交换和 WS relay 直接绑定执行器。
- 图片：单次执行绑定 `OpenAIImagesExecutor`，和文本共用 `OpenAIRequests`、`OpenAIResponseOutput` 和应用活动屏障；图片工具冷却通过提供商接口写入。图片意图提示由 HTTP 按每次尝试保存，分组改写后重新判断，所以上一次尝试的提示不会带到下一个提供商。
- `gateway/text` 负责文本提供商循环和计数预检，预算各自独立。`gateway/requeststate` 保存报文副本、引导规范化结果和请求内的模型替换缓存。`gateway/modeltrace` 维护响应模型的恢复链。
- `forward` 组织通用的请求准备和转换步骤，技术层的 provider 和 HTTP 适配负责交换、读写和 Flush。平台单次执行由 upstream 和 gateway/provider 提供，提供商切换循环只有一个。
- Responses：执行器负责请求准备、转换和 HTTP 单次执行，复用已有的 `OpenAIRequests`、`OpenAITextExecutor` 和输出实例。协议转换和 Compact 错误恢复在当前提供商内完成，不重新跑提供商循环。失效密文的读写在 HTTP 和 WS 之间共用会话存储和 TTL；请求转入 WS 时，模型映射和请求变换沿用 HTTP 阶段的结果。
- 文本入口把 `requeststate.ExecutionHints` 和 `RoutingState` 作为参数传给执行器。前者携带客户端识别、图片意图、粘性预取等执行提示，后者携带分组、路由计划和客户端协议。分组在写入和读取时都复制一份，后续 attempt 绑定变更后的分组，之前的请求快照保持不变。各执行适配层读取同一份状态，telemetry 只保存观测用的关联信息。
- Messages 在选号前解析 Claude Code、版本和 Thinking 身份；选号后才执行平台专有策略、warmup 拦截和串行队列。
- 提供商循环和完成规则由 text 和 completion 包负责，app 只绑定平台单次执行接口。执行入口使用 `gateway/provider.SelectionResult`，其中包含实际的提供商目标、等待计划和本次反馈参数；调度核心只读取去掉凭据的候选。
- OpenAI HTTP 的并发辅助和本地图片限制器由 app 构造成一个 `OpenAIHTTPResources`，文本、媒体、WS 和其余兼容入口共用。这个资源对象只接收静态的图片限制参数；用户槽绑定进入等待前的请求 context，context 取消时释放；图片的等待、拒绝和独立作用域各自保留。
- `gateway/searchtools` 组织工具模拟，`gateway/moderationflow` 准备审核完成所需的输入，`completion.Recorder` 读取资金和用量的独立快照。`ws` 和 `live` 各自管理连接和 turn 状态；摘要、隔离和归属值由 `session` 提供，Redis 协议由 `rediscache` 适配。错误规则和发布后不可变的快照在 `errorpolicy`，调度健康和重试由其他模块决定。

WS 每轮读取在线参数快照，并通过请求、输出、会话和选择接口执行。部署默认值由 app 提供，管理端可以覆盖。连接池在第一次使用时启动，关闭屏障保证退出后连接池无法重建。Grok、Live 和 WS 共用拨号器，Agent Identity 凭据失效也作用于这个池。入站、池化、透传和 HTTP 桥接各有自己的恢复和取消规则，测试直接验证帧执行和共享状态。

<a id="apikey_authentication"></a>
## 认证与准入

凭据提取和认证错误展示由 `apikey/httpapi` 处理。Key、用户、团队和 IP 校验在 `apikey.Authenticate` 里完成，返回的 `AccessSnapshot` 区分 owner、payer、actor 和 team。`gateway/httpapi` 的通用认证入口和 Google 认证入口组合了复合 Key 选组、模型改写和 `gateway/admission` 的资金准入，HTTP 适配层负责请求上下文和观测数据。普通协议门禁在读取请求体之前执行；Google 入口和通用入口各自的错误检查顺序不同。

认证缓存版本为 47。来源数据和请求里的嵌套 map、slice、指针都会复制，复合 Key 选组改动的是副本，共享快照保持不变；分组手动设置的 Fast 策略在缓存读写中完整保存。

通用认证入口在最终选组授权后绑定 `AccessSnapshot` 和 Fast 策略，付款用户取这个请求的付款主体；Google 分支在自己的时机绑定。认证失败时，供 Ops 使用的已加载 Key 信息和认证成功的快照分开存放，Key 能加载出来并不代表授权成功。

通用 API Key 认证的步骤：

1. 对无效认证的滥用和过大的 header 做入口限制。通用网关拒绝放在 query 里的 API Key，接受 `Authorization: Bearer` 和 `x-api-key`，Gemini 兼容入口还接受 `x-goog-api-key`。
2. 从认证缓存或仓储加载 Key，以及需要的 User、Group、Team 和复合映射。加载失败时区分不存在、过载、团队生命周期问题和内部错误。
3. 每次都检查 Key 是否禁用、团队 Key 的生命周期、成员限额、IP 规则、用户是否存在和启用。
4. 普通 Key 需要绑定分组，历史上没有绑定分组的 Key 调用模型时返回 `GROUP_REQUIRED`。复合 Key 根据请求模型选中一个映射，得到本次请求对应的普通 Key 视图。然后检查最终分组是否可用、用户是否有权限，并应用 Key 级模型重定向。
5. 鉴权要求分组和提供商成员关系都已明确。按 Key 的 `auto`、`subscription` 或 `balance` 策略确定资金来源，消费类入口还要检查 Key 的过期和配额、订阅窗口限额或余额。指定的订阅不可用、额度不足或不覆盖最终分组时，直接拒绝。
6. 写入 API Key、认证主体、角色、分组和可选的订阅上下文。路由随后按最终分组的 `allowed_protocols` 做协议准入，再进入 handler。`last_used_at` 更新失败时，已认证的请求照常继续。

Responses WebSocket 的后续轮次，在拿到用户槽之后、转发上游之前，直接回源复核 Key、用户、团队、成员限额、IP 规则和分组的 WS 协议许可，检查 Key 是否过期和总配额，并重新解析当前订阅、检查余额和滚动限额。回源失败或资格失效时，释放用户槽并关闭连接；旧的认证缓存和 Fast 策略刷新失败时的默认值都不能用来放行。连接固定原来的 Key ID、所有者、付款主体、团队和结算来源；同一凭据重建、付款主体或结算来源变化时要求客户端重连。复核使用独立快照，已放行轮次的计费归属保持不变；已经产生用量的轮次照常完成结算。

`/v1/usage` 和部分批量任务管理接口跳过消费准入，这样额度用完或 Key 过期后，用户仍能取回或清理自己的数据；身份、用户、团队、IP 和资源归属检查照常执行。用量查询对指定订阅保留它的失效状态，显示来源仍是该订阅。模型列表对复合 Key 无需选中某个分组，但仍要做适用的 Key 额度、余额和订阅检查。

系统里没有自动默认分组。分组不可用、客户端受限和无效请求，只能回退到管理员明确配置的目标，目标分组要重新通过用户、团队、协议、模型、订阅和资金检查。运行时回退会重新发布有效的 Key、分组、`RoutePlan` 和粘性预取，认证缓存保持原样。每个请求的回退次数有固定上限，已经开始输出或已经取消的请求不再重放。

身份读取使用 authctx 里的记录。已认证的 Key 和供 Ops 使用的加载失败数据，由 apikey HTTP 层分开读取；加载失败的数据不代表任何授权状态。

普通 Key 的协议门禁不读取请求体。复合 Key 在认证阶段先读取请求体并放回原处，用模型前缀确定最终分组，然后执行同一个门禁。被禁用的协议返回该客户端协议自己的 `403`，记录为 `LocalPolicyDenied`，请求在提供商选择、重试、fallback 和结算之前结束。

进入协议 handler 后，请求体按端点的限制读取，JSON 和 Multipart 按宽容模式解析，然后依次做用户提示词替换、协议解析、客户端识别、内容审查和 Ops 元数据设置。HTTP 和 WS 使用 app 注入的同一个 promptpolicy 实例，规则回源和替换在这些调用点执行。用户并发槽在提供商选择之前获取，已经超过用户并发的请求不会占用调度资源。Key 的并发和 RPM 在认证、资金检查之后预占，具体计数和释放规则见 [API Key 请求上限](../domains/routing_and_billing.md#api_key_request_limits)。

## 模型名称链

一个请求可能同时有以下几个模型名：

```text
client_model
  -> composite_actual_model
  -> api_key_redirected_model
  -> group_mapped_model
  -> provider/upstream_model
```

- `client_model` 是客户端传来的原始模型名；复合 Key 的情况下带分组前缀。
- `composite_actual_model` 是选组后去掉前缀的模型名。
- Key 级重定向只匹配一跳，发生在选组之后、分组映射和提供商映射之前。
- 分组映射产生白名单检查、计费和用量映射链要用的模型候选，白名单检查和计费各自配置模型来源；提供商映射得到最终发给供应商的路由键。
- `requested_model`、`upstream_model` 和去重后的 `model_mapping_chain` 分别保存客户端意图、实际发送的模型和变换路径。

模型名只在配置了映射，或者同一型号需要平台编码转换时才会改变。拼写、版本分隔符、日期和 effort 后缀都保持客户端原样；请求之后的价格、缓存和健康状态使用各自已确定的完整模型 ID。管理员配置的失败回退也会记录模型链，回退前后的模型价格互不借用。

协议 handler 可以在每次 failover attempt 时根据所选提供商重新构造请求，已经解析过的一跳映射只用一次。模型列表要从当前可请求的目标反推可展示的别名。保存映射时允许目标暂时不可路由，实际请求时返回标准的无提供商或无模型错误。

`routing.RoutePlan` 保存本次的最终分组、入口协议和从 Key 到分组的模型链，handler 把它传给后续的候选解析。模型链快照还保存分组白名单的 `RestrictModels`、`RestrictionModelSource` 和单独的计费模型来源，转换为 `GroupMappingResult` 时全部带上。候选仍然按当前提供商快照和最终分组复核协议；提供商映射在使用时才读取，attempt 的结果只属于当前请求。分组发生回退时要重新授权，平台请求改写和响应模型恢复仍由执行链负责。

`requeststate.AttemptRoute` 记录本次的候选结果和协议，`RoutingState.ResolveAttempt` 在 fresh 复核或数据库复核后重新解析。这个状态只存在于请求中，提供商的持久记录不保存它。协议相关的地址和 CN 适配规则由 `provider.ProtocolTarget` 根据协议和提供商记录计算。执行适配层使用的 `ExecutionProvider` 由提供商记录和本次路线组成；模型映射在调用时读取，上一次尝试的结果不会写回共享的提供商缓存。

候选协议快照、协议准入和模型冷却判断，都由 `gateway/provider.ModelPolicy` 根据提供商记录和本次 `AttemptRoute` 计算。选择和诊断按"协议、提供商状态、模型窗口"的顺序检查。原始窗口是否在冷却、允许 overages 时是否可调度、剩余时间，这三项分别计算。模型目录和候选协议复核使用同一份快照，动态模型映射在需要时才读取。

提供商没有配置模型范围或范围为空时，使用平台和认证类型对应的默认目录；配置了非空清单时，以清单为准，精确映射可以引入自定义模型。末尾的通配符参与匹配，但不会作为模型名展示；手动配置的 `*` 同样受协议、认证和端点硬能力的限制。`protocol_fallbacks` 为每个入口保存一个有序的目标数组：不配置时自动匹配，空数组表示只走原生协议，非空数组限定可以转换到的目标。每个候选先尝试原生协议，再尝试已有的单步转换。

计费模型和实际上游模型分开解析；OpenAI 普通和 Compact 映射、Bedrock 区域路由直接使用 `ModelPolicy`。入口确认 Lite 标记后，平台适配层按提供商类型选择：OAuth 提供商做完整的工具规范化，API Key 提供商限制并行工具，适用的入口范围保持不变。

协议地址通过 `provider.ProtocolTarget` 读取。RPM、会话数量、串行队列和窗口配置通过 `provider.RuntimeConfig` 读取，执行时按调用时点读取字段。Codex 图片桥接覆盖、工具策略和指纹模式由 provider 模块管理，平台资格、顶层和嵌套设置的优先级、旧值兼容都在这里处理。提供商的 Header 覆写由 `provider/provider` 在调用点应用 egress 策略；即使绑定成方法回调，也是在执行时读取提供商的最新字段，凭据和覆写表在调用时才取用。

Codex 身份和指纹的请求内状态只由 HTTP 适配层持有。提供商模块负责组合输入，平台库只做纯计算和报文修改。状态同步发布，Header 和请求体使用同一组本次生成的 ID，failover 时按固定顺序覆盖；异步的完成处理不持有这份 HTTP 状态。

上游响应里的模型名通过协议层的 `ResponseModelObserver` 采集，随执行结果和完成快照按值传递。每次实际出站的尝试单独观察；恢复或重试时清空上一次尝试的模型，WebSocket 按 turn 和 response ID 区分。Responses 优先采用终态里声明的模型，Chat 和 Messages 采用第一次有效的声明，Gemini 采用最后一次有效的版本。采集只读元数据，正文、工具参数和转换器生成的模型名都不参与；失败记录和结算资格不受采集影响。

<a id="account_selection_and_failover"></a>
## 提供商选择与故障转移

提供商选择的输入有：本次的分组、请求模型、分组映射结果、会话 hash、已失败的提供商集合、端点能力和可选的强制平台。先处理客户端限制等需要回退的情况，再按最终分组的 `scheduler_type` 选择基础调度器或高级调度器。候选覆盖组内所有平台的提供商，组外的提供商不参与。选择器综合考虑：

- 组内成员关系、专用入口的强制平台、分组和提供商的模型范围，以及一跳映射。
- 提供商是否启用、是否过期、代理、凭据、上游资格、临时不可调度、模型和提供商的限流、配额状态。
- 调度快照的可用性、粘性会话、优先级和负载、最近使用时间、并发槽和可等待队列。
- 特殊端点能力，例如图片、Realtime/WS、Grok 付费媒体资格，或站点特有的模型能力。

候选排序和高级评分只看调度信号；上游声明倍率、`upstream_cost` 权重和 OAuth 参考倍率都不在评分里。提供商本地的 `rate_multiplier` 和价格配置里的上游计费模型来源，在选定提供商、完成模型映射之后用于结算，不影响候选资格和排序。

`basic` 走历史的选择路径。`advanced` 在上面的硬约束都通过后调用通用评分核心，按 Top-K 加权顺序尝试候选，每次尝试前复核并发槽。Top-K、权重和粘性开关按字段合并：分组的 `advanced_scheduler_overrides` 优先，缺的字段使用网关运行时设置里的全局值，空对象表示全部继承。OpenAI 和 Grok 在评分核心上加入 previous response、订阅、transport、Compact 和额度能力；其他平台提供各自的候选和硬过滤。

运行时只在本次实际使用高级模式时回写错误率、TTFT 和切换统计，基础模式的请求不影响高级评分。`count_tokens`、可用性探测等只需选出提供商的入口，同样按最终分组决定模式；它们使用无槽选择，提供商并发槽和会话数量保持不变。

提供商选择由 `scheduler` 里的通用选择器、兼容平台选择器和 Gemini 选择器执行，`gateway/provider/selection` 提供平台资格和带凭据的执行目标。Messages、文本、媒体、WS/Live、计数和任务等使用方，由 app 绑定到对应的选择实例；平台执行通过已绑定的单次执行接口完成。`SelectionInput` 使用最终的 `RoutePlan` 和独立的提供商候选；每次 attempt、fresh 复核和数据库复核都重新解析候选，模型和协议的解析结果只留在当前请求里。

`AcquireUser` 返回请求的 Lease 和带计数归属的 WaitResult；`Lease.Select` 返回本次的 AttemptLease。选择结果可能带一个 WaitPlan，由 scheduler 执行等待循环，HTTP 同步观察并输出心跳。等待计数只释放确实拿到的那部分；提供商信息补全失败等后续准备错误，会立即归还已登记的槽位。请求和尝试的组合释放可以重复调用，结果相同；成功或部分成功时会话是否保留，由 `Finish` 决定。用户等待结束后，在同一位置复查权益。

### 计数与辅助入口

Messages 的 `count_tokens` 由 app 直接构造 HTTP Handler。HTTP 只持有受控的计数目标和去掉凭据的提供商快照，选择和平台执行由 app 连接。流程是先做资金预检，再无槽选择，按实际提供商的平台执行计数，每次尝试都从原始报文重新做分组映射。Anthropic 和 OpenAI 使用已有的执行器，Gemini 在 OAuth scope 不足时回退，Grok 和 CN 平台在本地计数；Qoder、Antigravity 和 Bedrock 返回不支持。Responses 的 `input_tokens` 同样不占提供商槽。计数既不提交费用，也不完成任务。文本入口和计数入口共用兼容指标的采样计数器。

OpenAI 兼容计数、Grok 本地估算和 Responses 输入 token 预检，由 app 单独构造的 `OpenAITokensHandler` 处理，绑定同一个请求生命周期屏障。计数先做分组路由规划，再检查资金，然后做一次无槽选择。Responses 预检先检查资金，再做分组路由规划，选择器交来的每个提供商槽都会释放。Grok 本地估算完全在本地完成，跳过资金检查、选号和上游请求。这些入口不生成请求，所以也没有完成提交接口。计数执行绑定 `OpenAIAuxiliary`，路由计划和选择分别使用 `RoutePlanner` 和 `Compatible`；提供商目标只在受控的转发方法内部携带凭据。

AlphaSearch 和 Embeddings 也复用固定的请求实例和响应实例，搜索授权的元数据由 `provider.OpenAIAuthorization` 提供。

Live 和 sideband 的 HTTP 入口由 app 直接构造，`LivePorts` 共用审核、资金准入和并发服务。分组的协议和功能门禁在读取请求体之前执行；先审核，再检查资金，然后立即拿用户槽。会话创建、身份归属和 relay 绑定 `OpenAILiveExecutor`；纯会话编排在 `gateway/live`，存储、租约和拨号器与其他入口共用。observer 的取消表和等待计数由这个执行器持有，app 把它登记在对应的关闭阶段。Live 记录的用量费用为零。报文之外的模型重定向由 HTTP 适配层的一个函数统一提供，Live 和 WS 使用同样的一跳映射和追踪规则。

### 会话哈希

客户端会话 Header、Grok 实际选号、强制平台、手动 Header 的判断，以及 OpenAI 会话哈希的请求绑定，由 `gateway/httpapi` 负责。内容种子、Grok 模型隔离种子和 Gemini 摘要格式由 `gateway/session` 提供。手动信号、内容回退、无状态的图片入口和用量日志各有自己的优先级，只用于日志的 Header 不参与通用选号。新旧两种哈希从同一个种子派生，旧哈希通过 requeststate 交给现有的粘性读取逻辑。Qoder 兼容入口调用通用的请求哈希函数。

### 故障转移

故障转移只处理适配器包装成 `forward.UpstreamFailoverError` 的可切换错误。这个值保存错误阶段、归属、原始响应和重试相关信息。OpenAI 特有的容量错误和请求过大错误在 gateway/provider 里识别，通用代码不导入具体平台。`failover.FailoverState` 记录切换次数、失败的提供商和最后一个错误，根据提供商 pool-mode 的重试次数决定：在同一个提供商上重试、排除它后选下一个、短暂等待，或者放弃。

普通的同提供商重试固定等待 500ms。被标记为请求级瞬时故障的容量错误按 500ms、1s、2s、4s 指数退避，之后每次最多等 8s；客户端取消会立即打断等待。提供商健康模块按错误分类写入临时不可调度标记。Messages 重试用尽后的冷却会跳过请求级瞬时故障和池模式提供商；HTTP 返回非 2xx 本身不会让提供商停止调度。

粘性会话已经绑定提供商时，换提供商可能需要把普通输入按缓存读取计费，反映缓存没有命中的实际成本。选择耗尽后的单提供商重试和等待都有固定上限；客户端 Context 取消时，选号和等待立即停止。

<a id="protocol_conversion_boundary"></a>
## 转发与流式输出

### 平台转发

Messages、Claude 的 Chat/Responses 转换和 `count_tokens` 使用 `gateway/provider/messageforward.Runtime`。app 绑定凭据来源、HTTP 池、健康反馈、TLS、分组策略和搜索实例；普通、API Key 透传、Vertex 和 Bedrock 分支调用各自的 upstream 执行器。每次尝试单独保存 Beta 过滤结果、工具名映射和错误诊断，HTTP 适配层负责响应提交、Header、Flush 和错误报文。

Anthropic 上游承接 Chat Completions 或 Responses 时，单独的转换读取器在改写客户端模型之前，先采集原始 `message_start` 里声明的模型。流式和缓冲两条路径都把这个声明写入转发结果，再进入完成快照；上游没有声明时，这个字段保持为空。

Messages、计数和 Qoder 的路由计划由 `gateway/provider.RoutePlanner` 读取分组策略并调用 routing，摘要和隔离使用 `gateway/session`。重试用尽后的兼容冷却由 `provider.RetryCooldown` 读取最新的池模式后决定。完成器绑定 app 构造的记录器。调试输出的文件句柄由一个 `requestdebug.Trace` 持有，请求和后台工作都结束后才关闭。

Gemini 和 Antigravity 的凭据来源、传输和动态读取接口由 app 注入 `gateway/provider/googleforward`。平台准备器不接触 Gin 和完整配置；`gateway/httpapi` 负责三种客户端协议各自的错误格式、规则覆盖和 Ops 写入顺序。图片计数和工具名恢复状态按 attempt 创建；图片数取单个响应片段里内联图片数的最大值，没有观测到图片时才回退到原模型名。

OpenAI 和 Grok 共用的响应回合状态头由 `gateway/httpapi.CodexTurnStateHeaders` 处理。首次输出还在暂存时不登记来源，实际提交后才写入会话组件的提供商来源表。请求带回的值，只有确认来自另一个提供商时才移除；来源未知、来自同一提供商或已过期时继续透传。来源按 API Key 和客户端原始会话区分。配额头和回合状态的强制透传也在 HTTP 适配层，调用时机由流读取器决定。

上游非流式响应的有界读取由 infra/httpclient 执行，默认上限 128 MiB，多读一个字节来判断是否超限，并保留原始错误链。gateway/httpapi 记录 Ops 并输出 Anthropic 或 OpenAI 格式的 502；读取函数不负责关闭响应体，重试和取消由执行链决定。

每次 attempt 都用原始或规范化后的请求，结合本次的提供商重新构造供应商请求，注入凭据、代理、TLS 指纹、客户端标识、Thinking 和工具配置以及上游模型。平台适配器负责协议转换、上游响应限制和供应商错误解析，handler 用客户端协议返回最终结果。

通用的报文和转换算法在 `protocol/{anthropic,openai,gemini,google,bridge}`。采样和 Max effort 选项由 `gateway/forward` 的型号策略提供；平台适配层选择 schema、thinking、签名和工具选项，并注入时间和 ID 生成器；每个请求或 attempt 创建独立的转换状态。Gemini 的 Messages 流和 OpenAI 兼容流按各自的 thinking、index 和 usage 观测顺序逐事件返回输出。HTTP 适配层提供同步写入和 Flush；upstream 执行器负责首次输出判定、取消、失败后的排水和提供商内重试；换号由外层网关决定。流式输出保持逐事件转发，实际输出开始后的重试规则保持不变。

### 流式输出与重试窗口

流式响应有一个不可逆的时间点。调用上游前记录 `ResponseWriter` 已写出的字节数；如果本次 attempt 已经向客户端写出实际业务内容，就不能再换提供商，否则两个上游的响应会拼成一条损坏的流。以下内容不算业务输出，可以留在 attempt 缓冲里，为输出前的 failover 留出空间：旧版 Compact 桥接心跳、Responses 的 `response.created` 和 `response.in_progress` 前导事件、等待终态判定的可重试 `error` 帧。不可重试的错误按事件及时转发。实际输出开始后，错误只能按当前协议追加允许的流错误事件，或者直接结束连接。非流式请求在还没写响应时，才能进入下一次 failover。

错误分为五类：本地准入、业务能力不足、调度容量不足、可切换的上游错误、不可切换的转发错误。协议准入拒绝时，Anthropic 返回 `permission_error`，OpenAI 返回 `protocol_not_allowed`，Google 返回 `PERMISSION_DENIED`，此时还没有选中提供商。Ops 采集记录归属、endpoint、实际提供商平台、模型和所选提供商；没有选中提供商时平台记为 unknown。返回给客户端的错误隐去凭据、内部代理和数据库错误。

Qoder 流式请求进入上游后采用完成后释放：客户端断开时停止下游输出，在预算内收集完尾部 usage 后再释放槽位。等待中的请求和非流式请求按各自的取消策略释放。WS 入站连接和 Live 租约各自续租，租约丢失时各自取消，两者与请求 Lease 互相独立。

## 用量与结算

统一文本执行保留上游 usage 里缓存创建的 5 分钟和 1 小时分桶以及 Speed，完成器据此选择对应平台的计费方式，缓存读取只扣一次。用户售价和提供商成本分别计算；价卡缓存按分组和模型索引，继承顺序是分组价、共享价、内置价。使用记录里的平台在写入时取实际执行的提供商。

上游转发产生可计量的 usage 后，handler 把解析出的 token、图片和视频用量、客户端模型和上游模型、endpoint、提供商、订阅快照、请求标识和分组映射交给有界的 UsageRecord worker pool。Anthropic 网关和 OpenAI 兼容的 Messages、Responses、Chat 三条链，如果在终止事件之前中断，只要 service 随错误返回了部分结果，handler 仍然提交已经观测到的 usage；没有结果时不生成记录。`UpstreamFailoverError` 不携带部分结果，所以重试成功后只计费一次。

国产供应商的原生 Anthropic 转 Responses 流，在客户端写失败后停止下游输出，但继续读完上游并推进状态机，直到读到末尾 `message_delta` 里的最终 token，或者达到有界读超时。OpenAI OAuth 图片响应在 HTTP 成功后，如果上游 body 传输中断，只有在还没向客户端写出实际图片内容时，才按 502 进入提供商策略和 failover。JSON keepalive 空白不算实际输出。客户端取消、deadline、响应体超限，以及首字节之后的中断，都不换号。

worker 使用一个和已结束请求的取消信号脱钩、但有自己超时的 Context。队列满时可以同步执行或丢弃，并通过指标和日志反映压力；worker 数量有上限，请求和 goroutine 不一一对应。

### 完成器

完成执行器只在 `gateway/completion` 实现一份，配置由 app 传入。停止时既等待排队的任务，也等待已经接受的同步溢出任务；扩缩容和停止共用一个屏障，停止后无法重开。手动配置的 drop、sample、sync 和 mandatory 回退按各入口的设置执行，完成记录和结算编排都由 `gateway/completion.Recorder` 实现。同步提交时，用 `gateway/provider.CaptureMessages`、`CaptureOpenAI` 和 `CaptureCyber` 读取提供商、Key、付款主体和用量，再生成一份独立的 `completion.Input`。

app 用价格、资金、用量和提交后处理接口构造 Forward 和 OpenAI 两个完成器，再把同一个实例绑定到各执行入口。两条链各有自己的倍率缓存，由 app 的一分钟时间轮任务清理；后台副作用由 `ApplicationBackgroundTasks` 统一管理。媒体请求和 WS turn 同样在入队前取得快照，计费时刻、请求 ID 和额度更新标记各自保持。creative 和 batchimage 自己管理任务完成资格、预占、捕获、释放和恢复，不进入网关完成队列。

创作台的执行目标由 `gateway/provider.CreativeTargets` 按本次的提供商构造，复用凭据、传输和活动屏障；任务执行由 app 绑定具体的生成能力。任务生成阶段通过 `scheduler.Lease` 管理用户槽和提供商槽，供应商返回后立即释放，结果交付和结算不占槽。

### 结算步骤

1. 归一化不同协议的 token 分桶、媒体尺寸和时长、缓存和长上下文计费规则。
2. 根据计费模型来源、共享价格配置、提供商成本、用户、分组和订阅倍率、高峰倍率，以及共享价格配置的分时倍率计算费用。WebSocket 多轮请求的时间相关价格，按当前 turn 开始的时刻计算。
3. 用 `request_id + api_key_id` 认领结算幂等键，用请求指纹检测同一个 ID 被不同 payload 复用。
4. 在一个 PostgreSQL 事务里锁定付款用户，按结算模式分配订阅或余额，同时累计团队成员、Key 的配额和速率，以及适用的上游提供商额度。已经通过准入、完成上游调用的普通请求，如果超过了指定订阅的剩余额度，订阅用量封顶，超出部分按余额倍率从付款主体的余额扣除，余额可以变成负数。这个回退只用于已放行或并发在途的请求；之后绑定已耗尽订阅的新请求仍然直接拒绝。批量图片提交前的额度预占，仍要求指定订阅能完整覆盖。
5. 结算成功后尽量写入已结算的 Usage Log，并更新缓存和最后使用时间。结算失败时，仍然写入包含计算成本的待对账 Usage Log，`actual_cost` 记为零，随后返回 worker 错误，结算状态如实标为失败。Usage Log 写入失败不会导致同一请求重复扣费。

热路径已经把响应交给客户端之后，后台结算失败也改变不了这个响应。这类错误需要在监控里能看到，并通过幂等重试和对账流程处理。

<a id="moderation_search_boundaries"></a>
## 审核与搜索协作

网关通过 `moderation.Check` 取得本地裁决，请求解析只提取当前轮的内容。实际行为用户和付款用户分开传入；供应商错误的识别由 upstream 提供，推理重试由网关负责。创作台选择不留存媒体。普通审核和 Cyber warning 的事务保证见[内容审核](../domains/content_moderation.md#content_moderation_decision_pipeline)。

Brave 和 Tavily 搜索由 search 选择供应商并预占额度。失败时释放自己已确认的预占。请求取消后，既不尝试其他供应商，也不标记代理故障；额度回滚使用单独的最多三秒的清理预算。Redis 结果不确定时按故障放行处理，不去猜测计数是否已经取得。配置替换后，已经开始的请求使用旧版本的快照，停机时所有版本一起等待。额度窗口和故障处理见[搜索编排](../domains/search_orchestration.md)。

`gateway/searchtools` 负责工具识别、提供商和分组的启用裁决、协议事件和合成 usage。app 通过 gateway/provider 绑定同一个 `search.ConfigService`、Registry 和分组策略读取实例，配置更换时发布到这个注册表。重试和完成处理由请求编排负责。Grok 原生搜索和 OpenAI AlphaSearch 由对应的 upstream 处理。资金是否提交以资金事务为准，通知成功、搜索配额和审核记录都不能作为证明。

独立的 Web 和 X 搜索由 app 直接构造 `gateway/httpapi.SearchHandler` 和固定的 `SearchPorts`。HTTP 按解析、认证、资金、审核、选号的顺序执行；平台报文和单次交换由 `gateway/provider` 组合 Grok 的底层调用和共享传输完成。选择接口只返回当前请求的受控目标，完成前同步取得提供商快照，异步记录读取快照，Gin Context 留在请求内。相同查询的每次调用都生成独立的资金请求 ID，完成快照提交后才释放提供商资源。

## 新增入口检查清单

新增网关入口或平台适配器时，至少逐项核对：

- body 和 header 限制、request ID、Ops error logger 和 API Key 错误格式是否正确。
- 是否支持普通 Key 和复合 Key，模型从哪里读取，哪些不涉及模型的管理入口只校验资源归属。
- 选组、Key 重定向、分组映射和提供商映射是否保持一跳顺序，模型列表和响应模型恢复是否同步。
- 用户和提供商并发、会话隔离、粘性和取消路径，是否都能完整释放槽位。
- 哪些错误允许在同一提供商重试或换提供商，流开始后是否会误入 failover。
- 用量能否拿到稳定的 request ID、请求指纹、requested/upstream model 和正确的平台归属。
- HTTP 适配、业务用例、存储实现、前端调用方，以及 API 接口测试和协议测试是否一起更新。

<a id="qoder_gateway_execution"></a>
## Qoder Chat 请求编排

Qoder 提供商参与普通文本入口的提供商循环，`UnifiedTextExecutor` 复用 `QoderChatExecutor` 和兼容单次执行器。站点、模型和认证能力由 Qoder 适配器检查；已有的单步协议转换只覆盖既定的转换路线，任意会话恢复不在支持范围内。

选号前做资金预检；等待结束后复查资金，RPM 只累计一次。已经提交实际输出的请求不会重放。Qoder 流式上游有自己的执行预算，客户端取消后，正在执行的提供商槽要等完成捕获后才释放。部分结果只进入一次完成处理，记录或结算失败时也不会重新调用供应商。

平台尝试登记在 app 的 `QoderRequestsAndAttempts`，统一 HTTP 入口同时受网关请求屏障保护。停止时先拒绝新请求并等待在途请求，然后才停止完成队列和共享连接；超过退出预算时报告未完成。

<a id="upstream_attempt_ownership"></a>
## 平台执行与资源归属

各平台的供应商交换、请求构造和原生读取在 upstream。gateway/provider 在调用点把提供商、出站策略和错误观察整理成接口参数，平台执行只拿到这些参数，Gin 和完整配置都留在上层。OpenAI 的响应读取、图片和辅助查询各自有取消和终态处理，WS relay 和连接池与完整的入站 WS 编排相互独立。续接报文和失效密文剥离使用平台的纯函数。HTTP Responses 的归属校验由 `gateway/session` 执行，HTTP 只保存已认证的 user 和 key 标识。同一用户跨 Key 使用、历史上只按 Key 记录归属的数据，都按兼容规则处理；归属读取失败时拒绝授权。

app 构造一个 OpenAI 会话状态存储，供 HTTP 和 WS 共用，归属缓存只有这一份，连接在使用时才打开；每轮的价格快照由入站连接持有。缺失 usage 的低频诊断由 `gateway/telemetry` 持有一份采样状态，日志读取这份状态，Gin 和完整提供商信息不在其中。

OpenAI 和 Messages 的转发结果在 `gateway/forward`，WS ingress hook 和 turn capture 在 `gateway/ws`。WS 重放输入是私有字段，不会出现在结果 JSON 里；各协议的结果字段各不相同。每次 attempt 或 turn 的 `forward.ResponseObserver` 单独记录模型和实际服务档位，Gin 的读写由 HTTP 适配层负责。终态声明优先，冲突时按回退规则处理；出站档位在完成计费时才和观测值合并。

Grok 的文本、图片、视频和 Voice 共用装配好的 `GrokExecutor`，使用相同的请求取消策略和应用活动屏障。Chat 转 Responses 不适用时，回到同一次派发的原生 Chat 路径；档位规则通过 `ExecutionFastPolicy` 在调用点读取设置和价格，WS 复用当前 turn 的设置快照。运行时只有一个提供商切换循环、一个资金记录器和一套缓存。

OpenAI 兼容文本的单次执行由 `OpenAITextExecutor` 组合各协议执行器，目标、凭据头、TLS 和客户端策略通过 `OpenAIRequests` 按固定顺序取得。HTTP 适配层同步处理输出；Raw Chat、原生 Anthropic、Messages 和 passthrough 都使用这同一个提供商切换循环。CompatResponses 按提供商、Key 和提示缓存区分隔离键，并有自己的 TTL 和续接禁用规则；Codex 额度观察按固定的节流间隔执行，写回进入应用管理的后台任务。

上游风控警告统一用 `gateway/forward.UpstreamWarning` 传递，HTTP、WS 和 Grok 适配层共用这个值类型和错误链格式。收到警告不代表请求可以结算，完成资格、失败状态和通知按各入口的规则决定。

Compact/SSE 的注释心跳、非流式图片 JSON 的空白心跳，以及扣除心跳字节后的输出判定，由 `gateway/httpapi` 管理。图片的第一次心跳会提交 200，之后到达的错误仍以合法 JSON 写回；这些空白不会关闭安全重试窗口。TTFT 按语义输出还是可见输出计算，由 gateway/provider 根据平台事件解析决定，它和 HTTP 提交、重试窗口是三个独立的判断。写入包装器只在 HTTP 内部使用，停止和写入之间互斥。

OpenAI `cyber_policy` 事件的识别在 gateway/provider，当前 HTTP 或 WS turn 的第一个标记和清除由 `gateway/httpapi` 管理，直接调用 `moderationflow.Mark`。标记键、正文截断和已观测的用量照原样记录；清除之后，下一个 turn 才能接受新的证据。已经透传的错误通过 forward 的哨兵错误通知收尾，换号、响应写入和扣费资格都不受影响。

`GatewayRequestsAndAttempts` 由 app 构造一次，HTTP 入口和平台尝试引用同一个进入屏障。停止后拒绝新的进入，等待请求尾部的完成快照入队、在途尝试结束，这一步早于完成队列和共享存储的关闭。它和 `HTTPRequests` 是两个并列的等待屏障；额度恢复操作先取消，再等待请求结束，`HTTPRequests` 的完成时间和停止监听的时间是两回事。之后停止提供商授权会话和底层配额服务；按需创建的 Live 和 WS 资源在应用构造时并不打开。

相关文档：[系统架构](system_architecture.md)、[提供商调度与缓存一致性](provider_scheduling_and_cache.md)、[网关策略控制](../domains/gateway_policy_controls.md)、[上游提供商能力矩阵](../interfaces/upstream_provider_matrix.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)、[领域目录](../domains/index.md)、[接口目录](../interfaces/index.md)。
