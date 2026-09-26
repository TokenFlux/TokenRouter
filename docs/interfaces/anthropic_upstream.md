# Anthropic 上游

本文描述 Anthropic 平台账号、认证、协议转换、模型与缓存策略、额度限制和失败恢复边界。它不固化上游动态模型清单，也不替代公共网关请求生命周期或用户接入教程。

## 章节导航

- [账号与认证](#账号与认证)：修改 OAuth、Setup Token、API Key、Bedrock 或 Vertex 凭据时读取。
- [协议分派](#协议分派)：修改 Messages、Responses 或 Chat 转换时读取。
- [模型与请求策略](#模型与请求策略)：修改模型映射、thinking、beta 或缓存行为时读取。
- [配额与调度](#配额与调度)：修改账号资格、粘性、等待或配额信号时读取。
- [错误与诊断](#错误与诊断)：修改刷新、重试、故障转移或错误响应时读取。

<a id="anthropic_account_and_transport"></a>
## 账号与认证

Anthropic 管理端正式支持以下账号：

| 类型 | 上游认证和边界 |
| --- | --- |
| `oauth` | 使用 access/refresh token；后台刷新服务和请求路径 token provider 都可刷新临近过期凭据 |
| `setup-token` | 使用推理范围的 setup token；按保存的 access token 转发，不等同于可刷新完整 OAuth 账号 |
| `apikey` | 使用 Anthropic API Key 或配置的 Bearer 方案，可配 base URL 和 Header override |
| `bedrock` | `sigv4` 使用 AWS 凭据和区域签名；`apikey` 使用 Bedrock API Key；可配置全局端点和模型映射 |
| `service_account` | 使用 Google Service Account 换取 Vertex AI token，并携带 project/location 等 Vertex 上下文 |

Messages 及其协议转换入口的凭据选择由 `account.MessageCredentialSource` 拥有，app 固定绑定同一 Claude/Vertex token 源。OAuth 与 setup-token 保持不同的刷新资格；普通 API Key、Grok 存量凭据及 Bedrock 签名分支仍按原顺序读取。

Claude 浏览器 OAuth 固定从 `https://claude.com/cai/oauth/authorize` 发起授权，token 交换仍使用 `https://platform.claude.com/v1/oauth/token`，回调仍是 `https://platform.claude.com/oauth/code/callback`。三者分别承担授权、换取凭据和接收授权码，不能因域名相近而互相替换。

`upstream` 和其它历史类型即使能被通用导入器保存，也没有 Anthropic 平台的正式 token provider 契约。完整分类见[上游账号能力矩阵](upstream_account_matrix.md)。所有 base URL、代理和自定义 Header 仍受[上游传输安全](../operations/upstream_transport_security.md)约束。

## 协议分派

Anthropic wire 类型与纯 Beta 常量由 `protocol/anthropic` 拥有，跨 Responses/Chat 的转换由 `protocol/bridge` 唯一实现。`gateway/clientmeta` 只解析客户端字符串与版本；CLI 版本环境覆盖、默认 Header 和平台指纹常量由 `upstream/anthropic` 持有，仍在进程初始化时解析一次。入站许可裁决继续由调用方执行。

Anthropic 原生入口是 `POST /v1/messages` 和 `POST /v1/messages/count_tokens`。同一 Anthropic 分组还可从 OpenAI Chat Completions 和 Responses 入口进入：处理器先把客户端形状归一化为 Anthropic 请求，按 attempt 选账号并转发，再把非流或 SSE 结果恢复成原协议。

Anthropic 分组支持 Messages、Responses 和 Chat，新建时默认只启用 Messages；三项都可关闭，迁移前已有分组按旧行为启用三项。被关闭的协议会在读取正文和账号调度前返回对应客户端形状的 `403`，不会产生上游 attempt 或结算。

API Key 和 OAuth/Setup Token 使用 Anthropic HTTP 路径；Bedrock 走独立签名与响应适配；Service Account 走 Vertex Claude 路径。协议转换不能抹平这些传输差异，尤其是 beta header、模型名称、错误结构和 token usage 的来源。

流式请求只在首个客户端分块写出前允许重试或换账号。每次 attempt 都从原始请求重建转换状态，工具名、停止原因、thinking block、usage 和错误事件必须与客户端协议一致。

Responses 请求转换为 Anthropic Messages 时，只发送 Anthropic 入站协议可识别的内容块。OpenAI `reasoning`、`reasoning_text`、未知专有分片、空内容消息和纯空白文本块会被过滤；空白文本与合法图片并存时仅删除坏文本，保留图片。`function_call` / `function_call_output` 仍按调用 ID 转为相邻的 `tool_use` / `tool_result`，过滤过程不能破坏工具配对、角色交替或历史顺序。

## 模型与请求策略

模型依次经过 Key 重定向、分组映射和账号映射；可请求列表是分组策略和当前账号能力的交集，不是默认模型常量的直接输出。Bedrock/Vertex 的供应商模型标识可与客户端 Anthropic 名称不同，计费模型也可以由价格配置单独指定。

Anthropic 请求策略包括：

- beta header 过滤、补充或阻断，避免把账号不允许的实验能力直接发往上游。
- thinking、tool use、图片和长上下文的协议保真；三个入口对显式推理档位执行分组映射、上限或拒绝。`xhigh` 与 `max` 在协议转换后仍保持独立，原始和实际转发档位分别记入用量；计价口径见[路由与结算](../domains/routing_and_billing.md)。
- prompt caching、cache TTL 注入和消息缓存重写；缓存读写 token 进入用量与定价，而不是仅作为诊断字段。
- 可选 web search emulation、Claude Code 客户端约束、metadata/header 策略和长上下文计价。

<a id="bedrock_region_routing"></a>
### Bedrock 模型与来源区域

Bedrock 的账号模型映射先于区域解析执行。已登记的 Claude 基础模型或完整推理 ID，由统一解析器按该型号自身的精确推理 ID、请求来源区域和 `aws_force_global` 选择最终目标；不能仅凭 `ap-`、`us-` 等区域名拼接前缀，也不能从相邻型号推断支持能力。大阪与墨尔本在支持日本、澳大利亚地域推理的型号上分别使用 `jp.`、`au.`；旧型号仍可能使用有独立来源区域限制的 `apac.`。

未开启强制全局时，默认别名和地域预设只使用当前来源区域已核实的地域推理；显式裸基础 ID 在已确认支持单区域调用的来源区域保留原样。缺少有效目标不会自动切换全球或其它地域。开启后仅选择支持该来源区域的 `global.` ID。HTTP 端点和 SigV4 签名继续使用账号原有 `aws_region`（空值默认 `us-east-1`），不把推理范围当作签名区域。GovCloud 的精确 ID 和来源区域独立核对，不能套用商业区域或推断全局支持。

区域规则在 `backend/internal/upstream/bedrock/model_routing.go` 集中维护，并逐型号保留来源 URL 和核对日期。已确认不支持、来源区域未收录、文档未给出精确地域 ID 分别保留相应诊断，不将未核实信息表述为官方不支持。未核实组合不自动生成 ID；未知完整供应商 ID、其它平台模型及自定义 ARN 保持显式值透传，由上游验证。解析不会迁移账号数据或兼容历史错误的 `-v1` 写法，也不改变合法版本和日期后缀。

调度、可请求模型列表、正式 Bedrock 转发和管理员账号测试共用同一解析结果。无有效路由的账号在模型筛选阶段被排除，同组其它有效账号仍可使用；管理员测试给出具体模型和来源区域诊断，仅在此区域已支持全局时提示启用该选项。请求路径中的二次校验失败不会调用上游、写入凭据失效状态或发起无意义重试。普通客户端沿用既有错误格式，不暴露账号区域细节。该静态判定不代替 AWS IAM、SCP 或账号模型权限校验。

<a id="claude_billing_fingerprint"></a>
### Claude 请求指纹

Claude Code-only 约束会在 CLI UA 之后校验必需 Header、metadata 与官方 system 特征。OAuth 账号级客户端指纹只接受稳定的 `<product>/<major>.<minor>.<patch>` User-Agent，拒绝本地构建后缀、超长值和远超当前内置版本的 Claude CLI 哨兵主版本；首次创建与版本升级共用该校验，历史非法缓存会在读取时用合法客户端 UA 或默认指纹自愈，并保留原 `ClientID`。

Auto mode 安全分类请求可在监视器提示词前后携带独立会话上下文块；校验器会遍历所有文本 system 块查找同时满足固定前缀、长度下限和全部结构标记的提示词，不会因附加上下文误拒，也不会仅凭上下文块放行。

Messages 和 CountTokens 的 OAuth 出站请求中，`x-anthropic-billing-header` 的 `cc_version` 必须匹配最终 User-Agent。启用 Claude Code 伪装时使用运行时 CLI 默认头（包括合法的 CLI 版本环境覆盖），即使没有账号指纹服务或指纹统一被关闭也要同步；普通指纹转发使用账号缓存 UA。三位十六进制指纹后缀包含版本和用户消息信息，版本同步时必须重算，并保持重复处理幂等、用户消息不变；该步骤在最终出站请求体构造前完成。

具体启用条件可能来自全局运行设置、分组/渠道和账号 extra。层级边界见[网关策略控制](../domains/gateway_policy_controls.md)。

## 配额与调度

账号必须通过状态、分组、模型、endpoint、凭据、限流和并发筛选。粘性会话尽量复用同一账号；账号失效、模型限流或策略变化会丢弃旧绑定并重新选择。等待队列只等待可能恢复的并发/限流条件，不会把永久凭据错误变成无限等待。

API Key/Bedrock 可配置本地账号配额和亲和策略。可用的上游用量/配额状态、账号优先级与近期错误可以参与资格判断或调度，但不替代用户余额、订阅和用户平台额度。Anthropic 不再采集上游站点声明倍率，也不按该值排序或评分；账户本地 `rate_multiplier` 仅保留为结算输入。Antigravity 账号只有显式启用 mixed scheduling 后才能加入 Anthropic 候选，并继续遵守 Anthropic 分组语义。

## 错误与诊断

凭据临近过期优先刷新；刷新失败会更新账号错误状态并同步调度快照。401/403 需要区分 token 失效、权限或 beta/模型拒绝；429 需要区分账号、模型和共享容量；可重试 5xx/网络错误只在响应未开始时换账号。

最终错误先经过平台分类，再应用管理员配置的[网关错误响应策略](gateway_error_policy.md)。错误正文、凭据、内部 project/region 和上游标识不得无条件返回客户端。排障应关联 request ID、requested/upstream model、账号 attempt、token refresh、代理/TLS 路由、限流恢复时间和结算记录。

<a id="anthropic_native_execution"></a>
## 平台执行与应用装配

`upstream/anthropic.Executor` 拥有单次账号内交换、签名/预算恢复及标准或 API Key 直通响应处理。两条恢复策略分别保留：API Key 直通不新增 400 请求体降级，也不补入旧路径没有的上游接受回调。`upstream/bedrock.Executor` 独立处理签名请求、来源区域和 AWS EventStream；具体平台之间不互相引用。

请求指纹由 `upstream/anthropic.RequestFingerprint` 与其 Redis Adapter 持有，app 直接注入 Messages 原生执行器。账号 ID、masking 开关和请求 Header 在本次执行中投影，用户登录身份仍由 identity 拥有。原 `fingerprint:`、`masked_session:` 键、TTL、UA 升级和遮罩语义保持。Claude 授权会话及完成编排由 `account.ClaudeAuthorization` 持有，app 直接构造并绑定其生命周期；provider 组合协议参数，OAuth HTTP handler 位于 account/httpapi。管理、CRS 和刷新使用同一原生授权实例；实际交换及 usage HTTP 客户端位于原生平台包。

gateway/forward 组织请求准备、转换与错误策略次序；gateway/httpapi 拥有同步输出和协议错误，gateway/completion 拥有完成处理。动态设置、凭据和账号观测通过固定的单步 Adapter 投影。流处理在原来的事件位置读取缓存分类投影，64 KiB Scanner 缓冲由唯一技术池复用。输出适配器带入已有 Header 和提交状态，保留等待心跳之后的重试边界。应用登记同步原生尝试，等待其释放响应体；超时不报告已排空。

### 请求规则与执行观测

Beta 配置值和模型白名单、消息缓存断点、messages/count_tokens 请求构造由 `upstream/anthropic` 唯一实现；动态设置由 gateway/provider 注入的读取端口提供。纯 thinking/tool 字节修复在 `protocol/anthropic`，`gateway/provider/modelidentity` 解释模型的 thinking 协议族，调用方再传入过滤与签名选项；官方严格校验、第三方原样回传和未知模型保守处理保持独立，平台之间不反向引用实现。

Claude token 读取和回填、版本比较、刷新资格及凭据合并归 `account`，继续复用原缓存与刷新协调器。Vertex 交换已绑定 `upstream/vertex` 和 `upstream/internal/googleauth`；账号缓存协调由 account 拥有，详见 [Vertex 服务账号与对象流](gemini_upstream.md#vertex_service_account_execution)。执行接口分别报告已观测用量（包括显式零）、语义输出、终态与旧 TTFT；网关的 text/forward、HTTP 与 completion 分别拥有尝试、展示和完成次序，结算资格由完成器按入口规则判断。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[账号维护](../operations/account_maintenance.md)。
