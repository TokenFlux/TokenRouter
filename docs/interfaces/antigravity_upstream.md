# Antigravity 上游

本文描述 Antigravity 提供商的接入、Claude 和 Gemini 的专用端点、协议转换、跨平台的分组选号和上游失败的处理。修改 Antigravity 适配器时，用本文保持它和其他平台相互隔离。终端用户使用 Claude Code 的技巧不在本文范围内；上游没有验证过的模型能力，本文也不作承诺。

## 章节导航

- [提供商与凭据](#提供商与凭据)：修改 OAuth 导入、刷新或提供商资格时读取。
- [专用端点](#专用端点)：修改强制平台路由时读取。
- [协议适配](#协议适配)：修改 Claude、Gemini 和 OpenAI 的转换时读取。
- [跨平台分组选号](#跨平台分组选号)：修改提供商参与分组调度和专用入口过滤时读取。
- [模型与额度](#模型与额度)：修改可见模型、配额或价格归属时读取。
- [失败与恢复](#失败与恢复)：修改限流、重试、切换或凭据错误时读取。
- [平台执行与提供商职责](#antigravity_native_execution)：修改执行器、授权或探针时读取。

<a id="antigravity_account_contract"></a>
## 提供商与凭据

Antigravity 提供商的 `platform` 是 `antigravity`。管理端通过 `/api/v1/admin/antigravity/oauth/*` 生成授权 URL、交换 code，或者验证和刷新 refresh token，再通过通用的提供商创建和更新路径保存凭据。OAuth 的 access token 由 token provider 在使用前刷新；project ID、订阅和 tier、privacy mode、额度和上游的 user agent，属于提供商和运行时的元数据。

Antigravity 只支持 OAuth，协议目录声明 GenerateContent 的平台适配形式，Messages、Responses 和 Chat 通过分组转换开放。请求发往 `v1internal` 端点，内容使用 Gemini 格式，并带有 project、model 和 request 外壳。统一的配置字段和入口门禁见[统一协议能力](protocol_capabilities.md)。

API Key 和历史 `upstream` 接入已移除。迁移 286 将存量静态提供商停用，记录 ID、凭据、分组关联和历史用量引用保留。需要连接中转网关时，按网关提供的协议创建 Anthropic 或 Gemini API Key 提供商，Base URL 填写完整网关前缀。Google 签发的 Gemini API Key 使用 Gemini 提供商。

standard-tier 的 OAuth 提供商缺少必要的 project ID 时直接拒绝。平台和提供商的完整分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。

导入提供商、刷新凭据和批量导入之后，会检查并设置适用的隐私状态。凭据、refresh token、project ID 和上游响应里的内部标识，不会出现在客户端的错误或模型列表里。

## 专用端点

专用路由在 API Key 鉴权之前写入 `ForcePlatform=antigravity`，所以只会选择 Antigravity 提供商，同时照常遵守 Key 绑定分组的权限、模型和协议规则：

| 入口 | 客户端协议 | 处理 |
| --- | --- | --- |
| `GET /antigravity/models` | Claude 风格的模型列表 | 返回当前 Key 和分组可以请求的 Antigravity 模型 |
| `POST /antigravity/v1/messages` | Anthropic Messages | 转换并转发 Claude 请求 |
| `POST /antigravity/v1/messages/count_tokens` | Anthropic token count | 路由保留，目前返回 `404`，客户端应改用本地估算 |
| `GET /antigravity/v1/models`、`/usage` | Claude 风格的自省 | 模型和 Key 用量 |
| `/antigravity/v1beta/models/*` | Gemini v1beta | list、get、generate、stream、countTokens 等 Gemini 格式的接口 |

Claude Code 可以把 base URL 指向部署地址的 `/antigravity`，认证用的仍是 TokenRouter 的 API Key。Gemini 入口里的 `x-goog-api-key` 同样填 TokenRouter 的 Key，不是 Google 上游的 key。

专用路由照常执行请求体限制、client request ID、Ops error logger、endpoint 归一化、分组，以及订阅和余额的准入。强制平台只限制候选的提供商，Key、团队、分组和模型的权限照常检查。

## 协议适配

Claude 和 Gemini 通用的报文变体、schema 清理、非流式和 SSE 的状态，都只在 `protocol` 里实现；`max_tokens`、metadata 和 tools 的格式与 OpenAI 兼容报文不同，各自保留明确的变体。`upstream/antigravity` 负责 v1internal 外壳、project 和身份补丁、原生 session ID、模型回退和流式协议；提供商的授权、项目发现、token 回填和健康写入在 `provider`。

`gateway/httpapi.AntigravityExecutor` 负责 HTTP 的错误展示和 Ops 数据，入站的完成处理使用原生的完成器。平台和模型的判断选出 thinking、signature 和 tool 的选项后，传给 bridge；纯转换代码不会反过来读取这些平台状态。

通用入口和 `/antigravity/*` 别名，都按最终分组执行对应的协议门禁；Gemini 模型列表的 GET 不受生成协议开关的影响。分组可以开放哪些入口，见[统一协议能力](protocol_capabilities.md#group_protocol_routes)。

Anthropic Messages 经过 Antigravity 的 request transformer，生成上游 Gemini 或内部的请求格式，响应和 SSE 再转回 Anthropic 协议。工具定义、tool choice、thinking、缓存断点、图片输入、token 用量和停止原因都需要双向转换；schema cleaner 会删掉上游不接受的 JSON Schema 写法。`web_search` 保留已选的模型，不会强制改成 Gemini 2.5 Flash；不支持的能力由现有的检查或上游返回错误。

通用的 OpenAI Chat Completions 和 Responses，选中原生的 Antigravity OAuth 提供商时，走兼容适配器：

```text
Chat Completions / Responses
  -> OpenAI-compatible normalized form
  -> Anthropic request
  -> Antigravity/Gemini upstream
  -> Anthropic response/stream
  -> original OpenAI protocol
```

每次 failover attempt 都从原始请求重新转换，并刷新工具名的回程映射。流开始之后，遵守共同的"不再切换"规则。Gemini v1beta 专用入口保持 Google 的错误和流格式，不经过 OpenAI 的 envelope。

Chat 的输出转换上限为 64000。Messages 的 Claude 路线为 64000，其他模型为 65000，并取目录更小值。Gemini 直通入口读取其本身的能力。管理员覆盖与路线条件通过[统一解析器](model_catalog_and_marketplace.md#model_attributes)生效。

转换器根据已解析的预算或档位集合发送 thinking 控制。已登记 Opus 4.6/4.7/4.8 的 adaptive 编码使用 24576 预算；客户端给出的 effort 单独转换。签名恢复时清除恢复阶段禁用的 thinking 控制，预算恢复使用修正后的预算。

## 跨平台分组选号

Antigravity 提供商可以和其他平台的提供商关联到同一个分组。选择器依次检查分组成员、模型范围、协议路线、提供商状态、额度和并发，不需要额外的混合调度开关。`/antigravity/*` 专用入口另外强制使用 Antigravity 提供商。

客户端使用的 Messages、Responses、Chat 或 Gemini 协议，决定响应的格式。分组保存权限、倍率和策略，实际选中的提供商决定上游的认证和转发协议。提供商移出分组或资格变化后，刷新所属分组的共享快照和相关的平台桶。已有的会话仍受签名、会话隔离、粘性和缓存计费的约束。

## 模型与额度

Antigravity 的名称和通用候选来自统一目录。默认映射保留 `gemini-3.1-pro-high -> gemini-pro-agent` 这一内部路由改写；管理员保存的映射按配置执行。提供商白名单为空时允许任意型号。推理参数过滤、thinking 后缀和额度键由平台执行规则处理。身份提示词使用通用 Antigravity 补丁，型号由请求中的 model 字段指定。

提供商的模型先做一次映射，再加上这次请求的 thinking 后缀，最后检查最终模型的白名单。白名单只允许 thinking 变体时，基础名称加 thinking 的请求可以通过；白名单只允许基础模型时，thinking 请求会被拒绝。最终的模型不会再被当作输入，做第二次提供商映射。

额度查询按提供商和模型的 scope，保存上游的 reset 和 remaining 状态，可以包含 AI Credits。429 和 503 的分类区分模型限流、credits 用完和共享容量不足。上游的提供商额度和用户的余额、订阅、Key 限额是不同的东西；使用记录里的平台取实际执行的提供商。提供商成本和用户售价分别解析，用户价格不会因为最终选中了 Antigravity 提供商而改变。

## 失败与恢复

- `RetryInfo` 给出的等待时间较短时，可以在同一提供商上做一次有上限的等待；模型限流的时间较长时，标记模型或提供商，并请求调度层换号。
- 单提供商模式允许退避重试，总等待时间有上限；多提供商模式优先换号。Context 取消时立即停止。
- `MODEL_CAPACITY_EXHAUSTED` 视为共享的模型容量问题，在当前提供商内有限重试。重试耗尽后，按非空模型名在进程内记录 10 秒冷却，命中冷却的后续请求跳过容量重试。冷却写入前的并发请求仍可各自重试；快速轮换提供商会继续访问同一共享容量池。
- 粘性会话换提供商时，可以把普通输入按 cache-read 计费，反映缓存失效的成本；这个标志要传进结算的输入。
- OAuth 凭据刷新后仍被拒绝时，返回脱敏的提示，要求重新授权并检查 project ID，同时把提供商标记为可恢复的错误。
- 只有白名单里的安全上游提示可以透传；记录响应体日志受开关、字节上限和脱敏的约束。

修改适配器时，测试覆盖：非流式和流式；Claude、Gemini、OpenAI 三种客户端格式；工具和 thinking；单提供商和多提供商的限流；移出分组后的快照失效；用量归属。

<a id="antigravity_native_execution"></a>
## 平台执行与提供商职责

`upstream/antigravity.Executor` 处理 Claude、Gemini、Chat 和 Responses 请求，完成一次平台交换、恢复和输出，并关闭最终的响应体。它负责提供商内的普通重试、智能重试、credits 请求和进程内的模型容量冷却。网关编排负责提供商切换、付款主体和资金完成。`gateway/provider/googleforward.Antigravity` 传入本次凭据、转换选项和同步输出。`Probe` 复用平台重试并测试指定的提供商，探测请求无需获取用户或提供商的请求槽。

流通过同步的 `OutputSink` 输出，每种协议保留前导缓冲、非流式收集、首 token 和断开后的尾部读取规则；下游心跳由 HTTP 层统一负责，上游心跳在读取侧消费，见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。结果区分已观测的 usage、是否已经服务和错误；HTTP 提交和重试窗口的关闭，与语义输出分开判断，失败时的结算按 Antigravity 入口的完成资格判断。使用记录在请求期间固定实际提供商的平台，后台完成器只使用这份固定下来的结果。

Gemini、Messages、Chat 和 Responses 输出及其非流式 SSE 汇总使用共享的 [Gemini 流生命周期](gemini_upstream.md#gemini_stream_lifecycle)，包括完整帧规范化、多候选终态、空事件保活、空闲截止和五秒用量尾部上限。各协议保留自己的内容转换器；收到候选 finish reason 后先结束内容，在用量尾帧收集结束后才发送最终 usage 和协议终态。尾部显式上游错误不能生成成功结束；已观察到的用量仍保留。Gemini 原生输出不向客户端发送 DONE 或注释心跳。

OAuth 会话、交换后的一次性删除、项目和套餐发现、隐私设置和验证，由 `provider.AntigravityAuthorization` 编排。原生客户端只执行供应商协议，报文变体在 `protocol/google`。token provider 的 project 回填冷却、缓存键、八秒的请求刷新预算、后台十五分钟的刷新资格和 CAS 都保持兼容。额度展示、credits 和模型窗口、INTERNAL 500 的惩罚，由提供商模块负责；共享缓存、计数器和发布接口使用已有的实例。

app 直接构造 `provider.AntigravityAuthorization` 并登记授权活动，provider 组合原生协议客户端和代理读取接口。管理、刷新和恢复使用同一个授权实例。构造时不启动清理；停止时取消运行中的操作并等待，有限次数的重试会完成收尾。预算用完时报告未完成项，不算排空成功。管理授权 URL、DTO 和错误响应的 HTTP 格式保持兼容。

管理端和后台对指定提供商的测试，由 `provider/provider.AntigravityProbe` 完整执行，复用同一个 `AntigravityRetry` 和平台循环。探针的 UA 是请求里明确的字段，不通过专门的 Context 键传递；测试不记录 Ops 错误，也不操作粘性会话。app 绑定健康、计数器和发布接口（各只有一个实例），探测和转发共用尝试的关闭屏障。令牌、模型映射、最小提示词、错误正文上限和响应体的关闭顺序保持兼容。

错误观测由 `provider/provider.AntigravityErrorObserver` 组合提供商的健康接口。模型窗口先于一般错误处理；503 共享容量不足不会升级为提供商冷却；429 缺少模型信息时，使用最终的模型，并按提供商级的默认规则处理。app 注入同一个健康、发布和平台观测实例，平台准备器只传入本次的 thinking、错误报文和清除粘性的动作。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)、[接口目录](index.md)。
