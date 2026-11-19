# 上游提供商能力矩阵

本文汇总 TokenRouter 11 个平台、7 类提供商与公开网关协议的支持等级，作为查找提供商能力的总入口。认证、协议转换、限流和诊断细节见各平台文档；数据导入器能保存的历史组合不代表正式支持。

## 章节导航

- [支持等级](#支持等级)：矩阵中各等级的含义。
- [提供商支持矩阵](#提供商支持矩阵)：11 个平台与 7 类提供商的对应关系。
- [Kimi、Zhipu 与 DeepSeek 平台规则](#cn_provider_protocols)：国产平台模式、协议集合与特殊转换。
- [公开网关协议](#public_gateway_protocols)：客户端入口与平台支持范围。
- [跨层约束](#跨层约束)：提供商已创建却无法被调度的常见原因。
- [已移除的接入方式](#已移除的接入方式)：历史类型的停用与替代方案。

## 支持等级

后端常量定义 11 个平台（`anthropic`、`openai`、`gemini`、`antigravity`、`grok`、`qoder`、`kimi`、`zhipu`、`deepseek`、`jev`、`opencode`）与 7 类提供商（`oauth`、`setup-token`、`apikey`、`upstream`、`bedrock`、`service_account`、`cosy`）：

- 正式支持：管理端提供创建或授权流程，运行时具备完整的凭据、转发与维护实现。
- 兼容保留：通用创建或导入层可保存，但平台能力不完整，管理端不推荐。
- 不支持：创建校验直接拒绝，或该类型专属于其他平台。
- 约定冲突：管理端与运行时对组合语义理解不一致，统一前不作正式支持。

分组可关联任意平台的提供商；平台仅属于提供商与实际执行记录，不能凭分组名或模型名推断。

## 提供商支持矩阵

| 平台 | OAuth | Setup Token | API Key | Upstream | Bedrock | Service Account | Cosy |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Anthropic | 正式支持 | 正式支持 | 正式支持 | 兼容导入，无正式转发 | 正式支持 | 正式支持（Vertex AI） | 不支持 |
| OpenAI | 正式支持 | 兼容导入，无正式转发 | 正式支持 | 兼容导入，无正式转发 | 兼容导入，无正式转发 | 兼容导入，无正式转发 | 不支持 |
| Gemini | 正式支持 | 兼容导入，无正式转发 | 正式支持 | 兼容导入，无正式转发 | 兼容导入，无正式转发 | 正式支持（Vertex AI） | 不支持 |
| Antigravity | 正式支持 | 不支持 | 已移除 | 已移除 | 不支持 | 不支持 | 不支持 |
| Grok | 正式支持 | 兼容导入，无正式转发 | 正式支持 | 兼容导入，无正式转发 | 兼容导入，无正式转发 | 兼容导入，无正式转发 | 不支持 |
| Qoder | 不支持 | 不支持 | 不支持 | 不支持 | 不支持 | 不支持 | 正式支持 |
| Kimi | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| Zhipu | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| DeepSeek | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| OpenCode | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| Jev | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |

API Key 上游用量查询摘要（详情见 [API Key 上游用量查询](upstream_usage.md)）：

- 兼容上游默认使用 Sub2API 适配器，New API、Zivv 与 derouter.ai Account Key 需手动选择；Kimi、Zhipu、DeepSeek 按平台与 `provider_mode` 自动绑定固定只读适配器（Zhipu `payg` 无公开余额协议）。
- 手动查询错误仅影响展示，不影响转发资格；仅手动开启周期监控时才写入统一快照并触发临时停调。

<a id="cn_provider_protocols"></a>
## Kimi、Zhipu 与 DeepSeek 平台规则

| 平台 | 允许提供商类型 | 支持模式（`provider_mode`） | 原生协议集合（`credentials.upstream_protocols`） | 特殊规则 |
| --- | --- | --- | --- | --- |
| Kimi | `apikey` | `payg`、`coding` | Messages、Responses、Chat | 表单默认全选，可全部关闭 |
| Zhipu | `apikey` | `payg`、`coding` | Messages、Chat | `payg` 不支持余额查询 |
| DeepSeek | `apikey` | `payg`（历史 `coding` 仅兼容读取，保存报错） | Messages、Responses、Chat | 原生 Chat 发送前将 `developer` 指令转为 `system`（文本分片用空行拼接，保留位置）；故障转移从原请求重建 |

补充约定：

- 旧输入缺省按 `payg + chat_completions` 转换，旧 `adaptive` 转全部原生项，非法值返回 `400 CN_PROVIDER_PROTOCOL_INVALID`。自定义协议地址存 `api_base_urls`，用量查询与模型同步各自使用独立地址。
- OpenCode（`payg`/`go`）见 [OpenCode 上游](opencode_upstream.md)；Jev 公开入口 `POST /v1/systemone` 见 [Jev 与 SystemOne](jev_upstream.md)；其余平台文档见[Anthropic](anthropic_upstream.md)、[OpenAI](openai_upstream.md)、[Gemini](gemini_upstream.md)、[Antigravity](antigravity_upstream.md)、[Grok](grok_upstream.md)、[Qoder](qoder_upstream.md)。

<a id="public_gateway_protocols"></a>
## 公开网关协议

22 个客户端入口由分组 `allowed_protocols` 控制，另 3 个上游专用项仅配置在提供商侧，完整目录见[统一协议能力](protocol_capabilities.md#protocol_catalog)。原生协议优先，其次按分组自动模式或有序 `protocol_fallbacks` 做单步转换（空数组仅走原生）。HTTP 与 SSE 共用一项，Responses WebSocket 独立。

| 协议族或入口 | 支持情况 | 参考文档 |
| --- | --- | --- |
| Anthropic Messages（`/v1/messages`） | 分组允许后选组内匹配模型的提供商，按实际提供商原生转发或转换 | [网关请求生命周期](../architecture/gateway_request_lifecycle.md) |
| Anthropic count_tokens（`/v1/messages/count_tokens`） | Anthropic/OpenAI/Gemini 走各自路径；Grok/OpenCode/Kimi/Zhipu/DeepSeek 本地估算；Antigravity/Qoder 返回 404；Bedrock 不支持 | 各平台文档 |
| OpenAI Responses（`/v1/responses` 及子路径） | 除 Jev 外 10 个平台支持；Kimi/Zhipu 不要求上游原生 Responses；DeepSeek 可选原生 `/responses`；Qoder 不支持子路径与 WS | [OpenAI 上游](openai_upstream.md) |
| OpenAI Chat（`/v1/chat/completions`） | 分组允许后按候选原生能力与允许转换路由 | 各平台文档 |
| 模型与用量（`/v1/models`、`/v1/usage`） | 返回 TokenRouter 本地解析结果，非上游原样转发 | [模型目录与市场](model_catalog_and_marketplace.md) |
| Embeddings（`/v1/embeddings`） | 仅限启用了 Embeddings 能力的 OpenAI 提供商 | [OpenAI 上游](openai_upstream.md) |
| Realtime、Live、Alpha Search | 仅 OpenAI 平台，受分组与提供商能力限制 | [OpenAI 上游](openai_upstream.md) |
| 同步图片生成与编辑 | 仅 OpenAI 与 Grok | [OpenAI 上游](openai_upstream.md)、[Grok 上游](grok_upstream.md) |
| 批量图片作业 | 仅 Gemini API 与 Vertex | [批量图片作业](../domains/batch_image_jobs.md) |
| 视频生成/编辑/扩展/下载 | 新任务仅 Grok；复合 Key 可凭任务绑定查已有任务 | [Grok 上游](grok_upstream.md) |
| Gemini v1beta（`/v1beta/models/*`） | Gemini、Antigravity 与 OpenCode 支持；模型列表 GET 不受开关限制 | [Gemini 上游](gemini_upstream.md)、[Antigravity 上游](antigravity_upstream.md) |
| Antigravity 专用（`/antigravity/*`） | 在当前分组内额外限定 Antigravity 提供商 | [Antigravity 上游](antigravity_upstream.md) |

说明：Gemini 已支持正式的非流式与 SSE Responses 转换（保留 reasoning、工具调用、usage、结束原因与首 Token），写出首字节后不 failover。`GET /v1/sub2api/billing` 未注册，返回 404。

## 跨层约束

提供商参与某次请求必须同时满足以下条件：

1. 平台与提供商类型具备实际凭据或签名实现（不只被导入器接受）。
2. 提供商处于 active、schedulable、未过期、不在提供商或模型冷却期，且已关联目标分组。
3. 分组允许入口协议与媒体能力，分组与提供商模型规则均允许最终模型。
4. OAuth-only、privacy、客户端限制、transport capability 及站点/区域策略通过。
5. 并发槽、等待队列与粘性约束允许本次选择。

补充规则：

- 白名单为空时使用平台与认证类型默认目录；`*` 仍受认证、协议与端点硬能力约束。未关联分组的提供商不参与调度。
- 混合分组无需 `mixed_scheduling` 开关，但同一会话内的签名、上游响应 ID、WS 与 Live 状态不可跨提供商复用。

## 已移除的接入方式

Antigravity 的 `apikey` 与历史 `upstream` 接入已移除，协议目录仅列 OAuth（存量静态提供商由迁移 286 停用，历史记录保留原 ID）。如需接中转网关，按对方协议创建 Anthropic 或 Gemini API Key 提供商并填写完整前缀 URL。

相关文档：[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[网关策略控制](../domains/gateway_policy_controls.md)、[提供商维护](../operations/provider_maintenance.md)、[上游传输安全](../operations/upstream_transport_security.md)。
