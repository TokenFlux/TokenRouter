# 上游提供商能力矩阵

本文汇总 TokenRouter 十个平台、七类提供商和公开网关协议目前的支持情况，是查找提供商能力的入口。认证、转换、限流和诊断的细节见各平台的文档。数据导入器能保存的历史组合，不等于正式支持。

## 章节导航

- [支持等级](#支持等级)：矩阵里各个等级的含义。
- [提供商支持矩阵](#提供商支持矩阵)：平台和提供商类型的组合。
- [公开网关协议](#公开网关协议)：从入口找到对应的平台文档。
- [跨层约束](#跨层约束)：提供商创建成功、却仍然调度不到的原因。
- [已移除的接入方式](#已移除的接入方式)：历史类型的停用和替代方式。

## 支持等级

后端常量定义了十个平台：`anthropic`、`openai`、`gemini`、`antigravity`、`grok`、`qoder`、`kimi`、`zhipu`、`deepseek`、`jev`；七类提供商：`oauth`、`setup-token`、`apikey`、`upstream`、`bedrock`、`service_account`、`cosy`。矩阵使用以下等级：

- 正式支持：管理端有创建或授权流程，平台运行时也有对应的凭据、转发和维护实现。
- 兼容保留：通用的创建或导入层可以保存，或者旧的运行路径还能识别，但管理端不推荐这个组合；它的平台能力并不完整。
- 不支持：创建校验明确拒绝，或者这个类型只属于另一个平台。
- 约定冲突：管理端和运行时对同一组合的类型理解不一致；统一之前，不作为正式支持。

除了 Qoder 和 COSY 的双向限制，通用数据导入器接受多种历史组合，这只是为了迁移兼容。能否调度，由平台的 token provider、协议处理器、提供商状态、模型和 endpoint 能力共同决定。分组可以关联任何平台的提供商；平台只属于提供商和实际的执行记录，分组名称和模型族都看不出平台。

## 提供商支持矩阵

| 平台 | OAuth | Setup Token | API Key | Upstream | Bedrock | Service Account | Cosy |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Anthropic | 正式支持 | 正式支持 | 正式支持 | 兼容导入，没有正式的转发实现 | 正式支持 | 正式支持（Vertex AI） | 不支持 |
| OpenAI | 正式支持 | 兼容导入，没有正式的转发实现 | 正式支持 | 兼容导入，没有正式的转发实现 | 兼容导入，没有正式的转发实现 | 兼容导入，没有正式的转发实现 | 不支持 |
| Gemini | 正式支持 | 兼容导入，没有正式的转发实现 | 正式支持 | 兼容导入，没有正式的转发实现 | 兼容导入，没有正式的转发实现 | 正式支持（Vertex AI） | 不支持 |
| Antigravity | 正式支持 | 不支持 | 已移除 | 已移除 | 不支持 | 不支持 | 不支持 |
| Grok | 正式支持 | 兼容导入，没有正式的转发实现 | 正式支持 | 兼容导入，没有正式的转发实现 | 兼容导入，没有正式的转发实现 | 兼容导入，没有正式的转发实现 | 不支持 |
| Qoder | 不支持 | 不支持 | 不支持 | 不支持 | 不支持 | 不支持 | 正式支持 |
| Kimi | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| Zhipu | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| DeepSeek | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |
| Jev | 不支持 | 不支持 | 正式支持 | 不支持 | 不支持 | 不支持 | 不支持 |

API Key 提供商可以在管理员列表里配置并手动查询上游用量。普通的兼容上游默认使用 Sub2API 适配器，New API 和 Zivv 需要手动选择；Kimi、Zhipu、DeepSeek 按平台和 `provider_mode` 自动选择固定的只读适配器，Zhipu payg 没有公开的余额协议，所以不支持查询。手动查询的协议错误只影响展示，转发资格不受影响。API Key 行同时展示两类数据：TokenRouter 本地的今日统计和本地配额，以及上游的余额和周期限额。只有手动开启的 CN 周期监控，才会把同样的查询结果写进统一快照，并据此对这个身份临时停调，详见 [API Key 上游用量查询](upstream_usage.md)。

<a id="cn_provider_protocols"></a>
### 国产平台提供商协议

Kimi、Zhipu 和 DeepSeek 只接受 `type=apikey`。提供商模式和原生协议集合互相独立：DeepSeek 只有 `payg`，Kimi 和 Zhipu 支持 `payg` 和 `coding`。DeepSeek 和 Kimi 的原生集合包括 Messages、Responses、Chat；Zhipu 只有 Messages 和 Chat。原生集合统一保存在 `credentials.upstream_protocols`，可以全部关闭；非法组合被拒绝。

新建 CN 提供商的表单默认启用全部原生项。旧输入缺省时，按 `payg + chat_completions` 转换；旧的 `adaptive` 转成全部原生项，其他值转成对应的单项。旧协议出错时返回 HTTP 400 / `CN_PROVIDER_PROTOCOL_INVALID`。`api_base_urls` 保存自定义地址，详见[统一协议能力](protocol_capabilities.md#account_native_protocols)。用量查询和模型同步各自使用自己的地址。

### 平台文档

- [Anthropic 上游](anthropic_upstream.md)
- [OpenAI 上游](openai_upstream.md)
- [Gemini 上游](gemini_upstream.md)
- [Antigravity 上游](antigravity_upstream.md)
- [Grok / xAI 上游](grok_upstream.md)
- [Qoder 原生上游](qoder_upstream.md)

Kimi、Zhipu、DeepSeek 的提供商类型、模式和协议矩阵，写在本页和 [API Key 上游用量查询](upstream_usage.md) 里；如果要为它们新增独立的认证、OAuth 或供应商专属的管理 API，先建立对应的平台文档。

<a id="public_gateway_protocols"></a>
Jev 的公开入口为 `POST /v1/systemone`，原生协议、模型同步和用量异常处理见 [Jev 与 SystemOne](jev_upstream.md)。

## 公开网关协议

22 个客户端业务入口都由分组的 `allowed_protocols` 控制；另外 3 个上游专用项只出现在提供商的集合里。完整的清单和认证规则见[统一协议能力](protocol_capabilities.md#protocol_catalog)。原生协议优先；无法原生处理时，按分组的自动模式或有序的目标列表选择已有的单步转换；目标列表明确为空时，只允许原生协议。HTTP 和 SSE 共用一项，Responses WebSocket 单独一项。

| 协议族或入口 | 平台支持情况 | 相关文档 |
| --- | --- | --- |
| Anthropic Messages：`/v1/messages` | 分组允许 Messages 后，从组内选出能处理这个模型的提供商，再按实际的提供商转换或原生转发 | 各平台文档；共同链路见[网关请求生命周期](../architecture/gateway_request_lifecycle.md) |
| Anthropic token count：`/v1/messages/count_tokens`、`/messages/count_tokens` | Anthropic、OpenAI、Gemini 走各自的统计路径，Grok 和三个 CN 平台在本地估算；Antigravity 和 Qoder 返回 `404`，Anthropic 的 Bedrock 提供商也不支持 | 各平台文档；客户端应保留本地估算作为回退 |
| OpenAI Responses：`/v1/responses`、`/responses` 和允许的子路径 | 最终分组允许 Responses 时，按选中的提供商进入十个平台各自的适配；Kimi 和 Zhipu 不要求提供商有上游原生的 Responses，DeepSeek 可以手动使用它的 `/responses`；Qoder 不支持 Responses 子路径和 WebSocket | 各平台文档；WebSocket 和 Realtime 见 [OpenAI 上游](openai_upstream.md) |
| OpenAI Chat Completions：`/v1/chat/completions`、`/chat/completions` | 最终分组允许 Chat 后，按组内候选的原生能力和允许的转换路线选择提供商 | 各平台文档 |
| 模型和用量：`/v1/models`、`/models`、`/v1/usage` | 按 Key、分组和提供商解析可以请求的模型和本地额度；返回的是 TokenRouter 的结果，不是上游模型列表或账单的原样转发 | [模型目录与市场](model_catalog_and_marketplace.md) 和各平台文档 |
| Embeddings：`/v1/embeddings`、`/embeddings` | 分组允许 Embeddings，并且候选提供商具备 OpenAI Embeddings 能力 | [OpenAI 上游](openai_upstream.md) |
| Realtime、Live 和 Alpha Search | Live 和 sideband、Codex realtime、alpha search 只支持 OpenAI 平台；是否可用还受分组和提供商能力限制 | [OpenAI 上游](openai_upstream.md) |
| 同步图片生成和编辑 | 只支持 OpenAI 和 Grok；对应的 Images 入口和提供商能力会进一步缩小范围 | [OpenAI 上游](openai_upstream.md)、[Grok / xAI 上游](grok_upstream.md) |
| 批量图片作业 | Gemini 和 Vertex 使用独立的任务生命周期；供应商范围见批量图片文档 | [批量图片作业](../domains/batch_image_jobs.md) |
| 视频生成、编辑、扩展、查询和下载 | 新任务只支持 Grok；复合 Key 可以凭持久化的任务绑定查询已有任务 | [Grok / xAI 上游](grok_upstream.md) |
| Gemini v1beta：`/v1beta/models/*` | 分组允许 Gemini 协议，并且候选的 Gemini 或 Antigravity 提供商具备相应能力时，处理生成、流式生成和 token 统计；模型列表的 GET 不受开关影响 | [Gemini 上游](gemini_upstream.md)、[Antigravity 上游](antigravity_upstream.md) |
| Antigravity 专用入口：`/antigravity/*` | 在当前分组的成员里，进一步限定为 Antigravity 提供商 | [Antigravity 上游](antigravity_upstream.md) |

路由存在，不代表任何分组或提供商类型都能处理。协议门禁在选择提供商之前，按最终分组执行；通过之后，按提供商快照校验模型、原生协议、允许的转换、transport、endpoint capability、媒体资格和其他分组策略，选中提供商后再调用对应平台的执行器。Gemini 的 Responses 已经有正式的非流式和 SSE 转换，保留 reasoning、工具调用、usage、结束原因和首 Token 指标；写出第一个客户端字节之后，不再 failover。

公开协议里没有 Key 账单自省和上游声明倍率的入口；`GET /v1/sub2api/billing` 没有注册，返回普通的 `404`。提供商本地的 `rate_multiplier` 和价格配置里的上游计费模型来源属于结算配置，和从上游探测到的倍率无关。管理员的 API Key 用量查询是独立的手动展示接口，详见 [API Key 上游用量查询](upstream_usage.md)。

## 跨层约束

一个提供商要能处理请求，需要同时满足：

1. 平台和提供商类型有实际的 token 或签名实现，而不只是导入器能接受这些字段。
2. 提供商处于 active、schedulable，没有过期，不在提供商级或模型级的限流期内，并且属于目标分组。
3. 分组允许对应的协议或媒体能力，分组和提供商的模型规则都允许最终的模型。
4. OAuth-only、隐私状态、客户端限制、transport capability 和站点、区域等平台策略都通过。
5. 并发槽、等待队列和粘性约束允许这次选择。

提供商的模型白名单为空时，使用所属平台和认证类型的默认目录；手动配置的模型、映射或末尾通配符可以声明自定义的范围。`*` 同样受本页的认证、协议和端点能力限制。提供商可以不加入任何分组，standard 和 simple 两种模式都不会因此把它加进别的分组。

混合提供商不再触发 Anthropic 和 Antigravity 的关联确认，也不需要 `mixed_scheduling` 开关。同一个会话里的签名、上游响应 ID、WS 和 Live 状态，仍按实际的提供商约束；同一分组里的不同供应商之间不能复用这些状态。

提供商选择和快照一致性见[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)，分组和价格策略见[网关策略控制](../domains/gateway_policy_controls.md)，凭据和健康恢复见[提供商维护](../operations/provider_maintenance.md)。

## 已移除的接入方式

Antigravity 的 API Key 和历史 `upstream` 接入已移除，协议目录只列 OAuth。存量静态提供商由迁移 286 停用，历史记录继续使用原 ID。通用导入器可以保存部分历史组合，调度仍需通过平台的协议能力判断。连接中转网关时，按其协议创建 Anthropic 或 Gemini API Key 提供商，并填写完整网关地址。

相关文档：[接口目录](index.md)、[提供商维护](../operations/provider_maintenance.md)、[上游传输安全](../operations/upstream_transport_security.md)。
