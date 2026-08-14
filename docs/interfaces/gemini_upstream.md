# Gemini 上游

本文描述 Gemini OAuth、API Key 和 Service Account 账号，Gemini v1beta 原生入口，以及 Anthropic/OpenAI 兼容转换的当前边界。它不固化上游动态模型清单，也不把 Antigravity 混合账号等同于 Gemini 原生账号。

## 章节导航

- [账号与认证](#账号与认证)：修改 OAuth 变体、API Key 或 Vertex 凭据时读取。
- [协议分派](#协议分派)：修改 Gemini 原生、Messages、Responses 或 Chat 转换时读取。
- [模型与会话](#模型与会话)：修改模型解析、thinking、signature 或缓存连续性时读取。
- [配额与调度](#配额与调度)：修改 tier、模型限流、混合调度或粘性时读取。
- [错误与诊断](#错误与诊断)：修改 Google 错误、刷新或 failover 时读取。

## 账号与认证

Gemini 正式支持：

| 类型 | 当前契约 |
| --- | --- |
| `oauth` | 支持 `code_assist`、`google_one` 和 `ai_studio` 变体；前两者使用内置 Gemini CLI 客户端，AI Studio 需要配置的 OAuth client |
| `apikey` | 使用 Google AI Studio key 和 base URL 直连；不参与 OAuth refresh |
| `service_account` | 使用 Google Service Account 换取 Vertex token，并解析 project/location 上下文 |

Code Assist/Google One 需要有效 project；AI Studio 的 project 可选并使用选择的 tier。OAuth refresh 会重试并兼容历史 client 元数据；token provider 使用过期前偏移和并发锁，避免同账号重复刷新。其它导入类型没有 Gemini 正式转发契约，见[上游账号能力矩阵](upstream_account_matrix.md)。

<a id="gemini_protocol_dispatch"></a>
## 协议分派

Gemini SDK/CLI 使用 `/v1beta/models`、`/v1beta/models/{model}` 和 `{model}:{action}` 形状，保持 Google 请求、流和错误语义。Anthropic Messages、Count Tokens、OpenAI Responses 与 Chat Completions 入口则先归一化，再由 Gemini 兼容服务转换为上游请求，响应恢复为原客户端协议。

Gemini 分组支持 Messages、Responses、Chat 和 Gemini GenerateContent，新建时默认只启用 GenerateContent；四项都可关闭，迁移前已有分组启用四项。GenerateContent、StreamGenerateContent 和 CountTokens 的 POST 动作受 Gemini 协议开关控制，模型列表 GET 不受影响。

Responses 转换是正式分支：非流和 SSE 共用 Gemini 上游执行、重试与响应适配，保留模型、reasoning、工具调用、usage、结束原因和首次 Token 指标。上游失败只在客户端收到首个字节前允许切换账号；流开始后按 Responses SSE 语义结束，不能回写普通 JSON 或换账号。

API Key、OAuth 和 Vertex Service Account 在认证头、base URL、project/location 和错误结构上不同；协议转换层必须保留这些传输差异。每次 failover attempt 都重新选择账号并重建 payload，流式输出开始后不再切换。

Antigravity 专用 `/antigravity/v1beta/*` 强制选择 Antigravity 账号，其契约由[Antigravity 上游](antigravity_upstream.md)拥有，不属于 Gemini 原生账号支持范围。

## 模型与会话

可请求模型由分组、渠道和账号能力共同解析。客户端模型依次经过 Key 重定向、渠道映射和账号映射；Vertex 或 AI Studio 的最终模型标识与计费模型可以不同。模型列表不能仅回显默认常量，也不能展示没有可调度账号支持的目标。

兼容层维护 thinking/推理字段、tool/schema、图片输入、usage、finish reason 和 Gemini thought signature。工具 schema 会递归移除 Gemini 不支持的字段；INTEGER 的整数 `exclusiveMinimum` 转换为加一后的包含式 `minimum`，且不覆盖更严格的既有下界，无法等价转换的独占下界只清理不伪造。需要跨轮次的 signature、session 和 cache 连续性时，粘性会话优先复用账号；切换账号必须重新评估可继续性，不能把另一个账号的内部状态当作通用上下文。

原生与 Claude 兼容生图响应按上游实际返回的 `inlineData`/`inline_data` 图片 part 数量计费，自定义模型别名也适用。流式响应按单个 payload 中观测到的最大图片数记录，避免累积式 SSE 重复计费；未观测到内联图片时才回退到请求模型名或映射后模型名的生图启发式。

## 配额与调度

Gemini tier、上游配额和按模型 reset 信息作为账号资格与容量信号。普通账号的 429 响应可以更新账号或模型的 `rate_limited_until`，后续调度在恢复时间前过滤该候选；公共池账号由请求级同账号重试或切号消化 429，不写默认本地账号限流，但管理员显式配置的自定义错误策略仍然优先。实时配额查询失败不应伪造剩余额度。

显式开启 mixed scheduling 的 Antigravity 账号可以加入 Gemini 分组候选，但仍需满足目标分组、模型、endpoint、额度、并发和凭据约束。普通 Gemini 请求保持 Gemini 协议和计费归属；专用 Antigravity 路由不混入 Gemini 账号。

## 错误与诊断

OAuth refresh、Service Account token、project/tier 发现和上游请求错误分别记录。401/403 需要区分凭据、project/region、API 未启用或策略拒绝；429 解析 reset 并更新限流；网络/5xx 只在响应未开始时允许换账号。

Gemini 原生入口返回 Google 形状，Anthropic/OpenAI 入口返回对应客户端形状。最终错误可应用[网关错误响应策略](gateway_error_policy.md)，但 project、service account JSON、token、API key 和内部上游响应不能无条件透传。排障应核对 OAuth variant、project/location/tier、最终模型、thought/session 状态、quota reset 和 attempt 链。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[账号维护](../operations/account_maintenance.md)。
