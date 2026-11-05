# Anthropic 上游

本文描述 Anthropic 平台的提供商、认证、协议转换、模型和缓存策略、额度限制，以及失败后如何恢复。上游的动态模型清单会变化，本文不列出；公共网关的请求生命周期和用户接入教程见其他文档。

## 章节导航

- [提供商与认证](#提供商与认证)：修改 OAuth、Setup Token、API Key、Bedrock 或 Vertex 凭据时读取。
- [协议分派](#协议分派)：修改 Messages、Responses 或 Chat 转换时读取。
- [模型与请求策略](#模型与请求策略)：修改模型映射、thinking、beta 或缓存行为时读取。
- [配额与调度](#配额与调度)：修改提供商资格、粘性、等待或配额信号时读取。
- [错误与诊断](#错误与诊断)：修改刷新、重试、故障转移或错误响应时读取。
- [平台执行与应用装配](#anthropic_native_execution)：修改执行器、请求指纹或授权会话时读取。

<a id="anthropic_account_and_transport"></a>
## 提供商与认证

Anthropic 管理端正式支持以下提供商：

| 类型 | 上游认证方式 |
| --- | --- |
| `oauth` | 使用 access token 和 refresh token；后台刷新服务和请求路径上的 token provider 都会刷新快过期的凭据 |
| `setup-token` | 使用推理范围的 setup token，按保存的 access token 转发；它不能像完整的 OAuth 提供商那样刷新 |
| `apikey` | 使用 Anthropic API Key 或配置的 Bearer 方案，可以配置 base URL 和 Header override |
| `bedrock` | `sigv4` 用 AWS 凭据和区域签名；`apikey` 用 Bedrock API Key；可以配置全局端点和模型映射 |
| `service_account` | 用 Google Service Account 换取 Vertex AI token，并带上 project、location 等 Vertex 上下文 |

Messages 和它的协议转换入口，由 `provider.MessageCredentialSource` 选择凭据，app 绑定同一个 Claude 和 Vertex 的 token 来源。OAuth 和 setup-token 的刷新资格不同；普通 API Key、Grok 的存量凭据和 Bedrock 签名分支，按固定的顺序读取。

Claude 浏览器 OAuth 从 `https://claude.com/cai/oauth/authorize` 发起授权，token 交换使用 `https://platform.claude.com/v1/oauth/token`，回调地址是 `https://platform.claude.com/oauth/code/callback`。三个地址分别负责授权、换取凭据和接收授权码，域名相近但不能互换。

`upstream` 和其他历史类型即使能被通用导入器保存，Anthropic 平台也没有为它们实现正式的 token provider。完整的分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。所有 base URL、代理和自定义 Header 都受[上游传输安全](../operations/upstream_transport_security.md)约束。

## 协议分派

Anthropic 的报文类型和 Beta 常量在 `protocol/anthropic`，Responses 和 Chat 之间的转换只在 `protocol/bridge` 实现。`gateway/clientmeta` 只解析客户端字符串和版本；`provider.anthropic.claude_cli_version` 覆盖、默认 Header 和平台指纹常量在 `upstream/anthropic`，在进程初始化时解析一次。入站许可由调用方判断。

Anthropic 的原生入口是 `POST /v1/messages` 和 `POST /v1/messages/count_tokens`。客户端也可以从 OpenAI Chat Completions 和 Responses 入口发来请求：处理器先把请求归一化成 Anthropic 格式，每次 attempt 选提供商并转发，再把非流式或 SSE 结果转回客户端的协议。分组是否开放这三个入口，由分组的 `allowed_protocols` 决定，见[统一协议能力](protocol_capabilities.md)。被关闭的协议，在读取正文和提供商调度之前，就返回对应客户端格式的 `403`，不会产生上游 attempt 或结算。

OAuth 和 Setup Token 不会自动补全 Claude 的日期，提供商测试和正式转发都使用映射后的型号。Vertex 只把调用方已经指定的日期转成 `@日期` 格式，Bedrock 保留同一型号的区域资源编码。

API Key、OAuth 和 Setup Token 走 Anthropic 的 HTTP 路径；Bedrock 有单独的签名和响应适配；Service Account 走 Vertex 的 Claude 路径。这些传输方式之间的差异在协议转换后仍然存在，尤其是 beta header、模型名称、错误结构和 token usage 的来源。

流式请求只在写出第一个客户端分块之前允许重试或换提供商。每次 attempt 都从原始请求重新构建转换状态，工具名、停止原因、thinking block、usage 和错误事件要和客户端协议一致。

Messages 上游转 Responses 或 Chat Completions 时，流式和非流式客户端共用 SSE 帧解析。转换器按空行划分事件并合并多行 `data:`，支持省略 `event:`、冒号后无空格和帧内注释。事件类型优先读取 JSON 的 `type`，缺失时读取 `event:`。末尾没有空行时，正常 EOF 会交付该帧。连接异常时，完整 JSON 仍用于读取 usage 和停止原因，截断 JSON 则丢弃，读取错误随结果返回。读取失败且尚未收到停止原因时，流式响应发送协议错误事件，非流式响应返回 502。文本、工具调用和 usage 在解析后进入各自的转换流程。

单帧累计大小使用部署配置的 `MaxLineSize`，未配置时为 500 MiB。转换器在缓存每行之前累计字节数，data、event、注释和换行符均计入限制，空行结束帧后重置。超限时释放帧缓冲并返回错误，已读取的用量随错误结果保留。 HTTP 输出层在完整 JSON 或流式终态写出成功后标记交付状态，外层据此跳过补发错误。读取错误继续返回给日志和用量处理，调用方可用 `errors.Is` 检查原始错误。

转换读取器先归一化兼容缓存别名和 `cache_creation` 的 5 分钟、1 小时分档，再把累计用量交给客户端转换与计费。只有缓存读取或写入、普通输入与输出都为零的观察同样有效；没有正数写入总量时由已报告分档补齐，已有聚合数不再与分档相加。OpenAI 输出的总输入包含普通输入、缓存读取和缓存写入，详见[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)。

Responses 请求转换成 Anthropic Messages 时，只发送 Anthropic 入站协议认识的内容块。OpenAI 的 `reasoning`、`reasoning_text`、未知的专有分片、空内容的消息和纯空白的文本块会被过滤；空白文本和合法的图片在一起时，只删掉坏的文本，图片保留。`function_call` 和 `function_call_output` 仍按调用 ID 转成相邻的 `tool_use` 和 `tool_result`，过滤之后，工具调用的配对、角色交替和历史顺序都保持完整。

## 模型与请求策略

模型名依次经过 Key 重定向、分组映射和提供商映射；可请求的列表取分组策略和当前提供商能力的交集。Bedrock 和 Vertex 的供应商模型标识可以和客户端的 Anthropic 名称不同，计费模型也可以由价格配置单独指定。

Anthropic 的请求策略包括：

- 过滤、补充或阻断 beta header，提供商不允许的实验能力不会被直接发往上游。
- 保持 thinking、tool use、图片和长上下文在协议转换中的完整性。三个入口对客户端指定的推理档位，执行分组的映射、上限或拒绝；`xhigh` 和 `max` 在协议转换后仍然是两个档位，原始档位和实际转发的档位分别记进用量。价格规则见[路由与结算](../domains/routing_and_billing.md)。
- prompt caching、cache TTL 注入和消息缓存的改写；缓存读写的 token 计入用量和定价。
- 可选的 web search 模拟、Claude Code 客户端约束、metadata 和 header 策略，以及长上下文计价。

<a id="bedrock_region_routing"></a>
### Bedrock 模型与来源区域

Bedrock 先执行提供商的模型映射，再解析区域。已经登记的 Claude 基础模型或完整推理 ID，由统一的解析器根据这个型号自己的精确推理 ID、请求的来源区域和 `aws_force_global` 选出最终目标。区域前缀不能按 `ap-`、`us-` 这样的区域名拼出来，也不能从相邻的型号推断支持情况。在支持日本、澳大利亚地域推理的型号上，大阪和墨尔本分别使用 `jp.` 和 `au.`；旧型号仍可能使用 `apac.`，它对来源区域有单独的限制。

没有开启强制全局时，默认别名和地域预设只使用当前来源区域已经核实过的地域推理；在已经确认支持单区域调用的来源区域，手动写的裸基础 ID 保持原样。找不到有效目标时，不会自动切换到全球或其他地域。开启强制全局后，只选择支持这个来源区域的 `global.` ID。HTTP 端点和 SigV4 签名仍使用提供商的 `aws_region`（为空时默认 `us-east-1`），推理范围和签名区域是两回事。GovCloud 的精确 ID 和来源区域单独核对，商业区域的规则和全局支持都不能套用过去。

区域规则集中在 `backend/internal/upstream/bedrock/model_routing.go` 维护，每个型号都保留来源 URL 和核对日期。"已确认不支持""来源区域未收录""文档没给出精确的地域 ID"三种情况，各有对应的诊断信息；没核实的信息不会被说成官方不支持。没核实的组合不会自动生成 ID；未知的完整供应商 ID、其他平台的模型和自定义 ARN，按手动填写的值透传，由上游验证。解析不迁移提供商数据，也不兼容历史上错误的 `-v1` 写法，合法的版本和日期后缀保持不变。

调度、可请求的模型列表、正式的 Bedrock 转发和管理端的提供商测试，使用同一个解析结果。找不到有效路由的提供商，在模型筛选阶段被排除，同组其他有效的提供商照常可用。管理员测试会给出具体的模型和来源区域诊断，只有在这个区域已经支持全局时，才提示开启强制全局。请求路径上的二次校验失败时，不调用上游，不写入凭据失效状态，也不做无意义的重试。普通客户端收到的是标准格式的错误，提供商的区域细节不会暴露。这个静态判断不能代替 AWS IAM、SCP 和提供商的模型权限校验。

<a id="claude_billing_fingerprint"></a>
### Claude 请求指纹

Claude Code-only 约束在 CLI UA 之后，校验必需的 Header、metadata 和官方 system 特征。OAuth 提供商级的客户端指纹，只接受稳定的 `<product>/<major>.<minor>.<patch>` 格式的 User-Agent；带本地构建后缀的、过长的，以及主版本远超当前内置版本的 Claude CLI 哨兵值都会被拒绝。首次创建和版本升级使用同一个校验；历史上缓存的非法值，读取时会用合法的客户端 UA 或默认指纹修正，原来的 `ClientID` 保留。

Auto mode 的安全分类请求，可以在监视器提示词前后带上独立的会话上下文块。校验器遍历所有文本 system 块，查找同时满足固定前缀、最小长度和全部结构标记的提示词；附加的上下文不会导致误拒，但光有上下文块也通不过校验。

Messages 和 CountTokens 的 OAuth 出站请求里，`x-anthropic-billing-header` 的 `cc_version` 需要和最终的 User-Agent 一致。开启 Claude Code 伪装时，使用运行时的 CLI 默认头（包括合法的 CLI 版本配置覆盖），即使没有提供商指纹服务、或者指纹统一功能被关闭，也要同步版本；普通的指纹转发使用提供商缓存的 UA。三位十六进制的指纹后缀由版本和用户消息计算得出，版本同步时要重新计算，重复处理的结果相同，用户消息保持不变；这一步在构造最终的出站请求体之前完成。

这些功能是否启用，可能来自全局运行设置、分组或提供商的 extra。各层的分工见[网关策略控制](../domains/gateway_policy_controls.md)。

## 配额与调度

提供商要通过状态、分组、模型、endpoint、凭据、限流和并发的筛选。粘性会话尽量复用同一个提供商；提供商失效、模型被限流或策略变化时，丢弃旧绑定并重新选择。等待队列只等待可能恢复的并发和限流条件，永久性的凭据错误直接返回，不进入等待。

API Key 和 Bedrock 可以配置本地的提供商配额和亲和策略。可用的上游用量和配额状态、提供商优先级和近期错误，可以参与资格判断或调度，但它们和用户的余额、订阅无关。Anthropic 不采集上游站点声明的倍率，也不按它排序或评分；提供商本地的 `rate_multiplier` 只用于结算。Antigravity 提供商可以和 Anthropic 提供商在同一个分组里一起参与调度，不需要额外的开关，见[上游提供商能力矩阵](upstream_provider_matrix.md#跨层约束)。

## 错误与诊断

凭据快过期时优先刷新；刷新失败时，更新提供商的错误状态，并同步调度快照。401 和 403 需要区分 token 失效、权限不足，还是 beta 或模型被拒绝；429 需要区分提供商级、模型级和共享容量的限制；可以重试的 5xx 和网络错误，只在响应还没开始时换提供商。

最终错误先经过平台的分类，再应用管理员配置的[网关错误响应策略](gateway_error_policy.md)。错误正文、凭据、内部的 project 和 region、上游标识，默认都不返回给客户端。排查问题时，关联 request ID、requested 和 upstream model、提供商 attempt、token 刷新、代理和 TLS 路由、限流恢复时间和结算记录。

<a id="anthropic_native_execution"></a>
## 平台执行与应用装配

`upstream/anthropic.Executor` 负责一次提供商内的交换、签名和预算恢复，以及标准和 API Key 直通两种响应的处理。两种恢复策略分开：API Key 直通没有 400 请求体降级，也没有上游接受回调。`upstream/bedrock.Executor` 单独处理签名请求、来源区域和 AWS EventStream；两个平台的代码互不引用。

请求指纹由 `upstream/anthropic.RequestFingerprint` 和它的 Redis 适配层持有，app 直接注入 Messages 的执行器。提供商 ID、masking 开关和请求 Header 在每次执行时传入，用户的登录身份由 identity 负责。Redis 的 `fingerprint:` 和 `masked_session:` 键、TTL、UA 升级和遮罩规则都保持兼容。Claude 的授权会话和完成编排由 `provider.ClaudeAuthorization` 持有，app 直接构造它并绑定生命周期；provider 组合协议参数，OAuth 的 HTTP handler 在 `provider/httpapi`。管理、CRS 和刷新使用同一个授权实例；实际的交换和 usage 的 HTTP 客户端在平台包里。

`gateway/forward` 安排请求准备、转换和错误策略的顺序；`gateway/httpapi` 负责同步输出和协议错误，`gateway/completion` 负责完成处理。动态设置、凭据和提供商观测，通过固定的单步适配层传入。流处理在事件的位置读取缓存分类数据，64 KiB 的 Scanner 缓冲由一个技术池复用。输出适配器分别记录 HTTP 提交和正文交付，只有心跳时保留重试窗口；下游计时、Claude Code 空 delta 和上游心跳消费见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。应用登记每次同步的尝试，并等待它释放响应体；超时时不会报告已排空。

### 请求规则与执行观测

Beta 配置值和模型白名单、消息缓存断点、messages 和 count_tokens 的请求构造，只在 `upstream/anthropic` 实现；动态设置通过 gateway/provider 注入的读取接口提供。thinking 和 tool 的纯字节修复在 `protocol/anthropic`；`gateway/provider/modelidentity` 判断模型属于哪种 thinking 协议族，调用方再传入过滤和签名选项。官方的严格校验、第三方的原样回传和未知模型的保守处理，各是一条独立的分支，平台之间不互相引用实现。

Claude token 的读取和回填、版本比较、刷新资格和凭据合并在 `provider`，使用现有的缓存和刷新协调器。Vertex 的交换使用 `upstream/vertex` 和 `upstream/internal/googleauth`；提供商缓存的协调由 provider 负责，详见 [Vertex 服务账号与对象流](gemini_upstream.md#vertex_service_account_execution)。执行接口分别报告已观测的用量（包括明确的零）、语义输出、终态和旧的 TTFT；网关的 text 和 forward、HTTP、completion 分别负责尝试、展示和完成的顺序，结算资格由完成器按入口的规则判断。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[提供商维护](../operations/provider_maintenance.md)。

模型名称与通用候选来自[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)。提供商空白名单允许任意型号，平台的请求格式、认证和协议规则分别检查；默认测试型号用于探测请求。
