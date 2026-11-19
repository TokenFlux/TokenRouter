# Anthropic 上游

本文记录 Anthropic 平台的提供商类型、认证凭据、协议转换、模型与缓存策略、Bedrock 区域路由、请求指纹、配额调度和错误处理。完整平台支持矩阵见[上游提供商能力矩阵](upstream_provider_matrix.md)，动态模型清单由[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)解析。

## 章节导航

- [提供商与认证](#anthropic_account_and_transport)：OAuth、Setup Token、API Key、Bedrock 与 Vertex 凭据及授权端点。
- [协议分派与流解析](#协议分派与流解析)：原生与 OpenAI 兼容转换、SSE 帧限制、用量归一化与内容过滤。
- [模型与请求策略](#模型与请求策略)：模型链、推理档位与通用请求策略。
- [Claude 缓存 TTL 策略](#claude_cache_ttl_policy)：全局与 API Key 级 1 小时缓存改写及计费归属。
- [Bedrock 模型与来源区域](#bedrock_region_routing)：区域推理 ID 解析、强制全局、GovCloud 与诊断边界。
- [Claude 请求指纹](#claude_billing_fingerprint)：CLI UA 校验、Auto mode 分类与 `cc_version` 同步。
- [配额、调度与错误处理](#配额调度与错误处理)：筛选、粘性、等待队列、错误分类与排查线索。
- [平台执行边界](#anthropic_native_execution)：直通恢复边界、缓存键兼容、缓冲复用与模块归属。

<a id="anthropic_account_and_transport"></a>
## 提供商与认证

Anthropic 管理端正式支持五类提供商：

| 类型 | 上游认证方式 | 关键约束 |
| --- | --- | --- |
| `oauth` | access token + refresh token | 后台刷新服务与请求路径均自动刷新快过期凭据 |
| `setup-token` | 推理范围 setup token | 按保存的 access token 转发，不支持自动刷新 |
| `apikey` | Anthropic API Key 或 Bearer 方案 | 支持自定义 base URL 与 Header override |
| `bedrock` | `sigv4`（AWS 凭据与区域签名）或 `apikey` | 支持全局端点开关与独立模型映射 |
| `service_account` | Google Service Account 换取 Vertex token | 携带 project 与 location 等 Vertex 上下文 |

凭据与端点规则：

- 凭据解析顺序：Messages 及其协议转换入口按固定分支顺序解析 Claude/Vertex 令牌、普通 API Key、存量 Grok 凭据与 Bedrock 签名；OAuth 与 `setup-token` 的刷新资格分开判断。
- Claude 浏览器 OAuth 端点：授权使用 `https://claude.com/cai/oauth/authorize`，令牌交换使用 `https://platform.claude.com/v1/oauth/token`，回调地址为 `https://platform.claude.com/oauth/code/callback`。三个域名相近但职责独立，不可互换。
- 未支持类型：`upstream` 及其他历史类型即使可由通用导入器保存，Anthropic 平台也没有为其实现正式 token provider，分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。
- 传输安全：所有 base URL、代理和自定义 Header 均受[上游传输安全](../operations/upstream_transport_security.md)约束。

## 协议分派与流解析

### 入口与协议转换

- 原生入口：`POST /v1/messages` 与 `POST /v1/messages/count_tokens`。
- 兼容入口：OpenAI Chat Completions 与 Responses 入口先把请求归一化为 Anthropic 格式，每次 attempt 选提供商并转发，再把非流式或 SSE 结果转回原协议。
- 协议门禁：三个入口的开放状态由分组 `allowed_protocols` 控制（见[统一协议能力](protocol_capabilities.md)）。协议关闭时，在读取正文和调度之前返回对应客户端格式的 `403`，不产生上游 attempt 或结算。
- 模型日期规则：OAuth 和 `setup-token` 不自动补全 Claude 日期，提供商测试与正式转发均使用映射后的型号；Vertex 仅把调用方指定的日期转为 `@日期` 格式；Bedrock 保留同一型号的区域资源编码。
- 传输差异：API Key、OAuth 与 `setup-token` 走 Anthropic HTTP；Bedrock 走独立签名与 AWS EventStream 适配；Service Account 走 Vertex Claude 路径。这些传输层在 beta header、模型名、错误结构和 usage 来源上的差异在协议转换后仍然保留。
- 流式换号窗口：流式请求仅在写出首个客户端分块前允许重试或换提供商。每次 attempt 从原始请求重建转换状态，保持工具名、停止原因、thinking block、usage 与错误事件和客户端协议一致。

### SSE 帧解析与用量归一化

Messages 上游转 Responses 或 Chat Completions 时，流式和非流式客户端共用同一套 SSE 帧解析：

- 帧边界：按空行划分事件并合并多行 `data:`，支持省略 `event:`、冒号后无空格与帧内注释。事件类型优先读 JSON `type`，缺失时读 `event:`；末尾缺少空行时，正常 EOF 仍交付最后一帧。
- 帧大小上限：单帧累计字节受部署配置 `MaxLineSize` 限制，默认 500 MiB。缓冲每行前累计 `data`、`event`、注释与换行符字节，空行后重置；超限时释放缓冲并返回错误，同时保留已观测用量。
- 连接中断处理：异常断开时，完整 JSON 仍用于提取 usage 与停止原因，截断 JSON 丢弃，读取错误随结果返回。未收到停止原因且读取失败时，流式响应发出协议错误事件，非流式返回 `502`。
- 交付状态：HTTP 输出层在完整 JSON 或流式终态写出成功后标记已交付，外层据此跳过补发错误；读取错误继续返回给日志与用量处理，可用 `errors.Is` 检查原始错误。
- 用量与缓存归一：先归一化兼容缓存别名和 `cache_creation` 的 5m/1h 分档，再交给客户端转换和计费。仅含缓存读写而普通输入/输出为零的观测同样有效；缺失正数写入总量时由已报告分档补齐，已有聚合数不与分档重复相加。
- OpenAI 兼容总输入：包含普通输入、缓存读取与缓存写入，详见[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)。

### Responses 转 Messages 内容过滤

Responses 转换为 Anthropic Messages 时仅发送 Anthropic 入站协议认识的内容块：

- 过滤 OpenAI `reasoning`、`reasoning_text`、未知专有分片、空消息和纯空白文本块；空白文本与合法图片共存时只删空白文本并保留图片。
- `function_call` 与 `function_call_output` 按调用 ID 转为相邻的 `tool_use` 与 `tool_result`，过滤后保持工具配对、角色交替和历史顺序完整。

## 模型与请求策略

模型名依次经过 Key 重定向、分组映射和提供商映射，可请求列表取分组策略与当前提供商能力的交集。Bedrock 和 Vertex 的供应商模型标识可与客户端 Anthropic 名称不同，计费模型可由价格配置单独指定。

模型名称与通用候选来自[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)。提供商白名单为空时允许任意型号（格式、认证与协议仍各自检查），默认测试型号用于探测请求。

通用请求策略（开关来自全局运行设置、分组或提供商 extra，分层分工见[网关策略控制](../domains/gateway_policy_controls.md)）：

- Beta 控制：过滤、补充或阻断 beta header，避免向不允许实验能力的提供商透传实验头。
- 推理与内容：保持 thinking、tool use、图片和长上下文在协议转换中的完整性。三个入口均对客户端推理档位执行分组映射、上限或拒绝；`xhigh` 与 `max` 转换后保持独立，原始档位和实际转发档位分别记入用量，计价规则见[路由与结算](../domains/routing_and_billing.md)。
- 缓存与附加策略：支持 prompt caching、cache TTL 注入和消息缓存改写（缓存读写 token 计入用量和定价），以及可选 web search 模拟、Claude Code 客户端约束、metadata/header 策略和长上下文计价。

<a id="claude_cache_ttl_policy"></a>
### Claude 缓存 TTL 策略

网关的 1 小时缓存改写（`InjectAnthropicCacheControlTTL1h`）仅把请求中已有的 `ephemeral` `cache_control`（覆盖顶层 `cache_control`、`system`、`tools` 与 `messages` 内容块）设为 `"ttl": "1h"`；原本无缓存断点的块保持无缓存。

| 触发入口 | 适用范围 | 计费分档处理 |
| --- | --- | --- |
| 全局 `enable_anthropic_cache_ttl_1h_injection` | 仅 Anthropic 平台的 `oauth` 与 `setup-token` | 提供商 `Extra` 未启用 `cache_ttl_override_enabled` 时，响应默认将缓存写入归入 `5m` 分档 |
| API Key `force_claude_1h_cache`（默认 `false`，迁移 `313_api_key_force_claude_1h_cache.sql`） | 原生 `/v1/messages` 覆盖 Anthropic 全类型（含直通、Vertex、Bedrock）及 OpenCode、OpenAI 兼容原生 Messages；Responses/Chat 转 Anthropic 仅在 Anthropic `oauth` 伪装（`Mimic`）注入断点后生效 | 仅由 Key 触发时不覆盖响应分档，按上游实际返回的 1 小时缓存写入单价结算 |

补充规则：

- 缓存分发：`force_claude_1h_cache` 随认证快照下发，快照版本与失效机制见[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)。
- 模型资格：`ShouldForceClaude1hCache` 按最终映射后的上游模型判断；GLM、Kimi、DeepSeek、MiniMax、Qwen Thinking 等原样回传家族，以及 Kimi、Zhipu、DeepSeek 等非 Claude 平台保持原有 `cache_control` 不变。
- Bedrock 清理：注入后继续执行 Bedrock 型号能力清理，Claude 4.5 及以上保留 `1h`，不支持 TTL 的旧型号剥离 `ttl` 字段。

<a id="bedrock_region_routing"></a>
### Bedrock 模型与来源区域

Bedrock 先执行提供商模型映射，再解析区域。已登记的 Claude 基础模型或完整推理 ID 由统一解析器根据精确推理 ID、来源区域与 `aws_force_global` 选出目标：

1. 禁止推断前缀：区域前缀不可按 `ap-`、`us-` 拼接，也不能由相邻型号推断。支持日本/澳大利亚地域推理的型号在大阪、墨尔本分别使用 `jp.`、`au.`；旧型号可能仍使用带独立区域限制的 `apac.`。
2. 未开启强制全局：默认别名与地域预设仅使用当前来源区域已核实的地域推理；在已核实支持单区域调用的来源区域，手动填写的裸基础 ID 保持原样。无有效目标时不自动回退至全球或其他地域。
3. 开启强制全局：仅选择支持当前来源区域的 `global.` ID。HTTP 端点与 SigV4 签名始终使用提供商 `aws_region`（空值默认 `us-east-1`），推理范围与签名区域互不混淆。
4. GovCloud 隔离：GovCloud 的精确 ID 和来源区域单独核对，商业区域规则与全局支持不适用于 GovCloud。
5. 未收录与透传：未知完整供应商 ID、其他平台模型与自定义 ARN 按手动值透传给上游验证；不迁移旧数据，也不兼容历史错误的 `-v1` 写法，合法版本与日期后缀保持原样。

规则表维护在 `backend/internal/upstream/bedrock/model_routing.go`，每条记录附来源 URL 和核对日期，并区分「已确认不支持」「来源区域未收录」「文档未给出精确地域 ID」三类诊断，不把未核实信息报成官方不支持，也不自动合成 ID。

调度、可请求模型列表、正式 Bedrock 转发与管理端提供商测试共用同一解析结果：

- 无有效路由的提供商在模型筛选阶段被排除，同组其他有效提供商照常可用。
- 管理员测试展示具体模型与来源区域诊断，仅在该区域已支持全局时提示开启强制全局。
- 请求路径二次校验失败时不调用上游、不标记凭据失效、不重试；客户端收到标准格式错误，不暴露提供商区域细节。静态校验不能替代 AWS IAM、SCP 与模型权限检查。

<a id="claude_billing_fingerprint"></a>
### Claude 请求指纹

- CLI 约束与 UA 校验：Claude Code-only 约束在 CLI UA 之后校验必需 Header、metadata 与官方 system 特征。OAuth 提供商指纹只接受 `<product>/<major>.<minor>.<patch>` 格式，拒绝本地构建后缀、超长值与主版本远超内置版本的哨兵值。
- 历史缓存修正：创建与升级共用同一 UA 校验；历史非法缓存值在读取时用合法客户端 UA 或默认指纹修正，同时保留原 `ClientID`。
- Auto mode 安全分类：允许在监视器提示词前后附带独立会话上下文块。校验器扫描全部文本 system 块，匹配同时满足固定前缀、最小长度和全部结构标记的块；附加块不导致误拒，仅含上下文块也无法通过。
- `cc_version` 同步：Messages 和 CountTokens 的 OAuth 出站请求中，`x-anthropic-billing-header` 的 `cc_version` 须与最终 UA 一致。开启 Claude Code 伪装时使用运行时 CLI 默认头（含合法版本覆盖），即使未启用指纹服务或指纹统一也要同步；普通指纹转发使用提供商缓存 UA。
- 指纹后缀重算：3 位十六进制指纹后缀由版本与用户消息计算，同步版本时幂等重算且不改动用户消息，在构造最终出站正文前完成。

## 配额、调度与错误处理

### 资格筛选与配额

- 候选筛选：提供商依次通过状态、分组、模型、endpoint、凭据、限流与并发检查。粘性会话优先复用原提供商，在提供商失效、模型限流或策略变化时丢弃旧绑定并重选。等待队列只等待可恢复的并发或限流条件，永久性凭据错误直接返回。
- 配额与倍率：API Key 与 Bedrock 支持本地提供商配额和亲和策略；上游用量/配额、优先级与近期错误可参与资格或调度，但与用户余额、订阅无关。Anthropic 不采集上游站点倍率，本地 `rate_multiplier` 仅用于结算。
- 跨平台同组：Antigravity 提供商可与 Anthropic 提供商在同一分组参与调度，无需额外开关，见[上游提供商能力矩阵](upstream_provider_matrix.md#跨层约束)。

### 错误分类与诊断

- 刷新与状态码：凭据快过期时优先刷新；刷新失败更新提供商错误状态并同步调度快照。`401`/`403` 区分令牌失效、权限不足与 beta/模型拒绝；`429` 区分提供商级、模型级与共享容量限流；可重试 `5xx` 与网络错误仅在响应未输出前换提供商。
- 错误脱敏与策略：最终错误先经平台分类，再应用[网关错误响应策略](gateway_error_policy.md)。上游错误正文、凭据、内部 project/region 与上游标识默认不返回客户端。
- 排查入口：按 request ID 关联 requested/upstream model、提供商 attempt、令牌刷新、代理/TLS 路由、限流恢复时间与结算记录。

<a id="anthropic_native_execution"></a>
## 平台执行边界

- 协议与转换归属：Anthropic 报文、Beta 常量与 thinking/tool 字节修复归 `protocol/anthropic`；Responses/Chat 转换归 `protocol/bridge`；客户端 UA 解析归 `gateway/clientmeta`。
- 平台与凭据归属：`provider.anthropic.claude_cli_version` 覆盖（进程初始化解析一次）、默认 Header、指纹常量、Beta 白名单与 Messages/CountTokens 构造归 `upstream/anthropic`。Bedrock 签名、区域与 AWS EventStream 隔离在 `upstream/bedrock`（两者互不引用）；Claude 令牌读写、版本比较、刷新资格与合并归 `provider`。
- 直通与标准恢复分离：API Key 直通路径与标准路径的恢复策略独立，API Key 直通不做 400 请求体降级，也不触发上游接受回调。
- 指纹存储兼容：Redis 的 `fingerprint:` 与 `masked_session:` 键格式、TTL、UA 升级与遮罩规则保持向后兼容。
- 流缓冲与关停排空：流处理按事件位置读取缓存分类并池化复用 64 KiB Scanner 缓冲；输出层分别记录 HTTP 提交与正文交付，仅发心跳时保留重试窗口（见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)）。进程停止时等待在途同步尝试释放响应体，超时时不报告已排空。
- Thinking 协议族隔离：`gateway/provider/modelidentity` 将模型归为官方严格校验、第三方原样回传与未知模型保守处理三条独立分支，平台间不互相引用实现。
- Vertex 与执行回报：Vertex 交换使用 `upstream/vertex` 与 `upstream/internal/googleauth`，缓存协调见[Vertex 服务账号与对象流](gemini_upstream.md#vertex_service_account_execution)。
- 执行结果语义：单次执行独立上报已观测用量（含明确零值）、语义输出、终态与旧版 TTFT，结算资格由完成器按入口规则判断。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[提供商维护](../operations/provider_maintenance.md)。
