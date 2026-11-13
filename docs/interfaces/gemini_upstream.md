# Gemini 上游

本文描述 Gemini 的 OAuth、API Key 和 Service Account 提供商，Gemini v1beta 的原生入口，以及 Anthropic、OpenAI 兼容转换的现状。上游的动态模型清单会变化，本文不列出；Antigravity 提供商也属于 Gemini 协议族，但它的规则见 [Antigravity 上游](antigravity_upstream.md)。

## 章节导航

- [提供商与认证](#提供商与认证)：修改 OAuth 变体、API Key 或 Vertex 凭据时读取。
- [协议分派](#协议分派)：修改 Gemini 原生、Messages、Responses 或 Chat 转换时读取。
- [模型与会话](#模型与会话)：修改模型解析、thinking、signature 或缓存连续性时读取。
- [配额与调度](#配额与调度)：修改 tier、模型限流或粘性时读取。
- [错误与诊断](#错误与诊断)：修改 Google 错误、刷新或 failover 时读取。
- [SSE 生命周期](#gemini_stream_lifecycle)：修改心跳、帧读取、终态、空闲超时或流内错误时读取。
- [原生执行与提供商授权](#gemini_native_execution)：修改执行器、授权会话或批量调用时读取。
- [Vertex 服务账号与对象流](#vertex_service_account_execution)：修改 Vertex token、Batch 或 GCS 时读取。

## 提供商与认证

Gemini 正式支持：

| 类型 | 说明 |
| --- | --- |
| `oauth` | 支持 `code_assist`、`google_one` 和 `ai_studio` 三种变体；前两种使用内置的 Gemini CLI 客户端，AI Studio 需要配置 OAuth client |
| `apikey` | 用 Base URL 和 API Key 直连；`credentials.provider_type=third_party` 表示兼容 Gemini 的第三方提供商，没有这个字段或值为 `official` 表示 Google AI Studio 官方接入 |
| `service_account` | 用 Google Service Account 换取 Vertex token，并解析 project 和 location |

提供商原生集合是 GenerateContent；API Key 另外有 Gemini Batch，Vertex Service Account 另外有 Vertex Batch。分组开放 `image_batches` 后，通过 provider 绑定使用对应的专用协议。统一的配置字段和入口门禁见[统一协议能力](protocol_capabilities.md)。

提供商的原生 Record 负责：手动指定的 project 的优先级、历史凭据字段，以及按模型选择 location；服务账号 JSON 的校验和 project 提取只在 `upstream/vertex` 实现。提供商测试、批量任务和在线转发都在调用时使用这两部分，按需解析凭据。

Code Assist 和 Google One 需要有效的 project；AI Studio 的 project 可选，并使用所选的 tier。第三方 API Key 保持 `type=apikey` 和兼容 Gemini 的请求格式，但需要配置非 Google 官方域名的 Base URL；它没有 Google 官方的提供商等级，所以不写 `tier_id`，也不参与本地模拟的 RPD、RPM 预检和用量窗口。本地配额预检由 app 构造一个 `provider.GeminiPrecheck`，直接绑定给执行方，按洛杉矶时间的日界读取 usage 批量数据和缓存。

Gemini 平台的第三方上游实际返回 `429` 时，使用配置的通用冷却，不按 Google 日配额的重置规则解析。OpenCode 等非 Google 账户的提供商只复用 Gemini 报文处理，不适用 Google 的项目配置换号、countTokens 估算兜底和本地重试；健康观察使用共享错误策略及 Retry-After，同一提供商重试最多等待 8 秒。普通重试由外层提供商预算控制，协议签名修复单独有界执行。OAuth refresh 会重试，并兼容历史的 client 元数据；token provider 在过期前提前刷新，并用并发锁防止同一提供商重复刷新。其他导入类型没有 Gemini 的正式转发实现，见[上游提供商能力矩阵](upstream_provider_matrix.md)。

<a id="gemini_protocol_dispatch"></a>
## 协议分派

Gemini 通用的报文格式和 Google 的错误结构，分别在 `protocol/gemini` 和 `protocol/google`；Google 的 HTTP 状态映射在 `gateway/httpapi`，激活诊断在 `upstream/gemini/codeassist`。Anthropic 和 Gemini 之间的纯转换在 `protocol/bridge`，原生和内部方言各自接收明确的选项，schema、工具配对、签名和预算的差异都保留下来。平台的 HTTP 交换、响应流和提供商内重试只在 `upstream/gemini` 实现；入站许可、最终的提供商选择和完成处理，由 gateway 的准入、选择和完成接口负责。

Gemini SDK 和 CLI 使用 `/v1beta/models`、`/v1beta/models/{model}` 和 `{model}:{action}` 三种路径，请求、流和错误都按 Google 的规范处理。前两个模型资源，由分组统一的可请求目录生成：只保留支持 Gemini 协议的模型，并补充本地元数据，不会选某个上游提供商去读它的目录。分组需要开放 Gemini 协议；强制平台的路径和复合 Key 分别检查分组权限和具体的模型范围。Anthropic Messages、Count Tokens、OpenAI Responses 和 Chat Completions 入口先把请求归一化，再由 `gateway/provider/googleforward.Gemini` 准备上游请求，响应转回客户端原来的协议。

GenerateContent、StreamGenerateContent 和 CountTokens 的 POST 动作，受分组的 Gemini 协议开关控制；模型列表的 GET 不受影响。分组可以开放哪些入口，见[统一协议能力](protocol_capabilities.md#group_protocol_routes)。

Responses 转换是正式支持的分支：非流式和 SSE 共用 Gemini 的上游执行、重试和响应适配，保留模型、reasoning、工具调用、usage、结束原因和首 Token 指标。上游失败时，只有心跳的响应仍可按既有策略换提供商；非心跳协议输出之后，错误按当前 SSE 协议结束，不再换号或追加普通 JSON。

Messages、Chat 和 Responses 转换先按最终服务与模型解析能力，再发送 thinking 控制。Gemini 2.5 的预算型条目使用 `thinkingBudget`，档位型条目使用 `thinkingLevel`；两个字段互斥。客户端的关闭、effort 和预算来自转换前的请求，明确的零预算会保留。原生 GenerateContent 入口按同一采样规则处理 `generationConfig` 和 `generation_config`，以及 `topP` 和 `top_p`。`reasoning=true` 与可调选项分开判断：选项缺失时继续转发客户端控制，空集合表示没有可配置控制。参数集合与管理员覆盖见[统一模型属性](model_catalog_and_marketplace.md#model_attributes)。

API Key、OAuth 和 Vertex Service Account 在认证头、base URL、project 和 location、错误结构上都不同，协议转换后这些传输差异仍然保留。每次 failover attempt 都重新选择提供商并重建 payload；真正的协议输出关闭重试窗口，排队与空闲心跳不关闭。

Antigravity 的专用入口 `/antigravity/v1beta/*` 强制选择 Antigravity 提供商，规则见 [Antigravity 上游](antigravity_upstream.md)。

### 创作台的图片请求

创作台的 Gemini 图片请求统一使用 `generateContent` 的 inlineData，不使用 File API。自定义 base URL 校验失败时，请求直接失败，不会改用 Google 官方地址。本地按 base64 编码后的 JSON 请求总大小估算，上限 20 MiB，超出时在提交前返回输入过大的错误。

## 模型与会话

可请求的模型由分组和提供商能力共同决定。客户端模型依次经过 Key 重定向、分组映射和提供商映射；Vertex 或 AI Studio 最终的模型标识，可以和计费模型不同。模型列表按实际可调度的提供商生成，默认常量和没有提供商支持的目标都不会出现在里面。

兼容层维护 thinking 和推理字段、tool 和 schema、图片输入、usage、finish reason 和 Gemini 的 thought signature。工具 schema 会递归删掉 Gemini 不支持的字段；INTEGER 类型的整数 `exclusiveMinimum` 转成加一后的、包含边界的 `minimum`，已有的更严格的下界保持不变；无法等价转换的独占下界只删除，不会编造新的下界。需要跨轮次保持 signature、session 和缓存连续性时，粘性会话优先复用同一个提供商；换提供商时，重新评估能否继续，另一个提供商的内部状态不能当作通用的上下文使用。

Messages 的流式和非流式响应把 Gemini 的推理摘要放在独立的 thinking 块，签名字段留空。客户端可能把同一段历史带到其他协议或提供商。普通请求回传历史时，转换器省略 thinking 和 redacted_thinking 块及仅含这些块的回合。

Messages、Chat 和 Responses 的客户端调用 ID 对应原始 Gemini 工具调用 ID 和签名。网关在内存中保留这份关联，按 API Key、提供商和实际转发模型查找。命中时，调用和结果使用同一个 Gemini ID，签名回到最初携带它的调用。关联最多保留一小时，受条数和内存上限限制，重启后重新建立。未命中时沿用按名称和兼容签名回传的方式。同一消息内连续的工具结果按调用顺序提交，便于无 ID 的上游按位置关联。上游拒绝缓存中的签名时，兼容入口可以用旧格式重试。

原生和 Claude 兼容的生图响应，按上游实际返回的 `inlineData` 或 `inline_data` 图片 part 数计费，自定义的模型别名也一样。流式响应取单个 payload 里观测到的最大图片数，累积式的 SSE 因此不会重复计费；没有观测到内联图片时，才按请求的模型名或映射后的模型名判断是不是生图模型。

## 配额与调度

Gemini 的 tier、上游配额和按模型的 reset 信息，是提供商资格和容量的信号。普通提供商收到 429 时，可以更新提供商或模型的 `rate_limited_until`，恢复时间之前，调度会过滤掉这个候选。公共池提供商的 429 由请求级的同提供商重试或换号处理，不写默认的本地提供商限流；管理员手动配置的自定义错误策略仍然优先。实时配额查询失败时，剩余额度显示为未知。

Antigravity 提供商可以和 Gemini 提供商在同一个分组里参与调度，同样要满足目标分组、模型、endpoint、额度、并发和凭据的约束。普通的 Gemini 请求按 Gemini 协议处理，计费归属不变；专用的 Antigravity 路由只选择 Antigravity 提供商。

## 错误与诊断

OAuth refresh、Service Account token、project 和 tier 发现、上游请求的错误分别记录。401 和 403 需要区分凭据、project 或 region、API 未启用，还是策略拒绝；429 解析 reset 时间并更新限流；网络错误和 5xx 只在响应还没开始时才换提供商。

Gemini 原生入口返回 Google 格式的错误，Anthropic 和 OpenAI 入口返回对应客户端的格式。最终错误可以应用[网关错误响应策略](gateway_error_policy.md)，但 project、service account JSON、token、API key 和上游的内部响应，默认都不透传。排查问题时，核对 OAuth 变体、project、location 和 tier、最终的模型、thought 和 session 状态、配额 reset 时间和 attempt 链。

<a id="gemini_stream_lifecycle"></a>
## SSE 生命周期

Gemini REST、OAuth、Vertex 共用的 SSE 读取，以及 Antigravity 的 Gemini 原生输出，使用 `upstream.ReadGeminiStream`。Messages、Chat、Responses 转换和非流式客户端汇总上游 SSE 也遵守相同的读取规则。普通 JSON 上游不经过这个读取器。

读取器按完整事件处理 LF、CRLF 和多行 data，允许 JSON 完整但缺少末尾空行的最后一帧。`MaxLineSize` 在这里约束完整帧字节，包括忽略的注释和元数据。无效 JSON、超限帧、空流以及缺失终态的 EOF 都返回失败。原生输出只包含单行 `data: <JSON>\n\n`，平台解包后仍保留未知 JSON 字段；event、id、retry 和上游心跳不转给 Google SDK。中央及排队保活均发送 `\n\n` 空事件，计入心跳字节，且不建立业务输出、TTFT 或重试提交。第三方上游仅含空 candidates 的占位帧也按空事件消费，不建立首事件时间；带有其他实际元数据的帧仍保留。

候选索引按最终请求的 `candidateCount` 跟踪，默认一个候选；一个候选结束不能提前完成其他候选。所有预期候选完成或出现明确的 `promptFeedback.blockReason` 时建立终态。有效候选内容之后的 `[DONE]` 也作为兼容完成信号，但该字符串不下发；只有用量、心跳或 DONE 的响应仍判为空流。工具调用、安全阻止和 `MAX_TOKENS` 等合法结果保留原来的转换语义。

等待响应头由传输层超时控制。响应头到达后，`stream_data_interval_timeout` 限制实际读到上游字节之间的空闲，默认 180 秒，0 禁用；上游心跳会更新这个时钟，本地下游心跳不会。有效终态后停止下游心跳，并继续接收用量尾帧，直到 EOF、DONE 或固定五秒上限。尾部传输中断或到期记录 `gemini.stream_tail_incomplete`，保留已完成结果与已有用量；明确的上游业务错误仍返回失败。每次结束都关闭响应体并等待读取协程退出。

HTTP 200 中的 Google error 在转换之前识别，不能被吞掉或补成成功结束。本地读超时、截断和格式故障按 502 进入既有策略；上游明确错误保留状态及安全消息。尚未提交 HTTP 时使用 JSON 错误，已提交原生 SSE 时使用 data-only Google error 对象，包含 code、message 和 status；转换入口使用各自的流式错误，且只呈现一次。读取失败保留已观测的部分结果，结算资格仍由原有入口规则判断。

汇总上游 SSE 时，各候选分别累积内容与终态，不能被另一个候选覆盖。相邻文本只有在其他元数据完全一致时才合并，以保留不同的 thought signature。稀疏用量尾帧仅更新出现且非 null 的字段，明确的零值仍有效；缺失字段不会抹掉先前观察到的输入或缓存计数。

Messages、Chat 和 Responses 的流式转换按 Gemini 调用 ID 区分工具调用。已结束的调用收到补发签名时，网关更新原客户端调用的记录。缺少 ID 时，同一帧里的每个调用单独输出，兼容上游在下一帧重发整批完整调用时跳过重复前缀。流式输出和非流式收集共用参数拼接规则，非流式按调用 ID 合并分片。回传历史时，每个工具步骤的首个调用缺少签名则使用兼容标记，后续已知的无签名并行调用保留原样。

Messages、Chat 和 Responses 的流式及非流式转换都返回上游已报告的缓存读取数。Messages 普通输入扣除缓存；OpenAI 两种协议的总输入包含缓存，不能直接使用扣除后的普通输入。客户端最终用量与后台记录使用同一快照，字段规则见[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)。

<a id="gemini_native_execution"></a>
## 原生执行与提供商授权

`upstream/gemini.Executor` 完成一次平台交换、协议输出和关闭响应体。Messages、原生、Chat 和 Responses 各自有流式、非流式和 Code Assist 缓冲的分支；转换调用 `protocol/bridge`，SSE 通过 `OutputSink` 同步写出。三个入口复用 `RequestPlan`，在调用时取得凭据和 project，原生输入和兼容 REST 的净化分开处理。原生的错误、重试和配额时间解析不写提供商数据。

`gateway/provider/googleforward.Gemini` 组合每次执行，`provider/provider.GeminiErrorObserver` 区分官方日配额、第三方冷却和池模式；HTTP 适配层负责错误改写，最终完成进入完成器。app 只传入静态的读取上限和目标策略，凭据和配额在调用时读取。

执行结果分别报告：已观测的 usage（包括明确的零）、语义输出、旧的 TTFT、内联图片张数和失败分类。`countTokens` 的本地估算单独携带，不计入实际的 usage 和资金记录；图片的回退判断和结算条件由调用方分别处理。流式输出不增加整流缓冲，各入口的断开处理保持各自的方式。

Gemini 的授权会话、三类 OAuth 的编排、project 和 tier 发现、token 回填和刷新资格，由 `provider.GeminiAuthorization` 负责；协议交换、Drive 和 Resource Manager 在 `upstream/gemini/codeassist`。app 直接构造授权实例，并整理完整的配置；provider 组合协议参数，动态的 OAuth 配置在调用时读取。管理员刷新 tier 使用 provider 的管理选项。project 和提供商的缓存键、刷新时的 CAS 保持兼容。构造时不启动清理，由 app 统一启动和停止；停止超时时保留未完成的状态，有限次数的重试没结束时，不会报告已排空。

Batch 客户端和 JSONL 编码、创作台的 generateContent 调用和图片解码，都使用原生实现。`protocol/gemini` 为批量和创作保留了明确的报文变体；任务输入只取必要的字段，任务状态机、最后的完成和清理、资金处理由任务用例负责。Vertex 的 URL 和 token 接口绑定 `upstream/vertex` 和提供商的缓存协调；Gemini 不 import Vertex，原生执行通过手动传入的 URL 和认证输入复用协议输出。

<a id="vertex_service_account_execution"></a>
## Vertex 服务账号与对象流

`upstream/vertex` 负责 project 和 location 的端点、Claude 模型的日期和 body 变体、Beta 过滤，以及 Batch 和 GCS 的技术调用。签名交换使用 `upstream/internal/googleauth` 的 RSA JWT 原语，只接收已经整理好的密钥和代理，不读取提供商、缓存或完整的配置。凭据 JSON 的历史字段选择、手动指定的 project 和按模型的 location 覆盖，由 `provider` 负责。私钥不会出现在通用的执行结果里。

访问 token 使用 `vertex:service_account:` 身份摘要，以及对应的 Redis 缓存和锁、TTL 和五分钟的提前量。`provider/provider` 组合凭据解析、身份摘要、代理和交换（只有一处实现），Claude、Gemini 和批量图片共用这个入口。竞争者正常等待 200ms 后再读缓存；等待被取消时立即返回取消错误，不再回读，也不发起交换。Redis 故障时降级处理。

Claude 和 Gemini 的单次执行复用各自的协议输出链，Vertex 通过调用方传入的参数接入，平台之间没有 import，也没有新的提供商切换循环。Batch 的提交、读取、取消，以及 GCS 的上传、分页、删除和对象流，只有一份技术实现；对象流由接收方关闭。任务归属、结果状态转换、受控的清理和资金捕获，由 batchimage 和 creative 的任务用例负责。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[提供商维护](../operations/provider_maintenance.md)。

模型名称与通用候选来自[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)。提供商空白名单允许任意型号，平台的请求格式、认证和协议规则分别检查；默认测试型号用于探测请求。
