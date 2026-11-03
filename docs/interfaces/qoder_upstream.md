# Qoder 原生上游

TokenRouter 通过 Qoder COSY 网关路径，支持 Qoder 原生的上游提供商。对外公开的别名会映射到 Qoder 的路由键，原始路由键也可以直接作为请求模型，用于兼容和运维。

本文说明 Qoder 的提供商、站点、模型能力、请求适配、定价、配额和失败处理。TokenRouter 共用的调度和账本规则见其他文档；代码里还没有实现的 Qoder 企业登录变体，不在支持范围内。

## 章节导航

- [提供商类型](#提供商类型)：修改凭据导入、OAuth、刷新或站点选择时读取。
- [客户端协议](#客户端协议)：修改 Messages、Responses 或 Chat 的准入时读取。
- [模型别名与映射](#模型别名与映射)：修改模型目录、路由键或限制时读取。
- [站点思考控制](#站点思考控制)：修改协议原生的推理控制时读取。
- [上下文窗口](#上下文窗口)：修改各站点的上下文能力或请求载荷时读取。
- [平台执行与输出](#qoder_execution_boundary)：修改执行器、流输出、凭据缓存或授权时读取。
- [计费范围](#计费范围)：修改 Qoder 的价格查找或零费用行为时读取。
- [上游提供商用量](#上游提供商用量)：修改配额探测或调度冷却时读取。
- [运维](#运维)：修改故障转移、错误分类或导入导出时读取。

<a id="qoder_account_contract"></a>
## 提供商类型

- `cosy` 提供商可以在国际站（`global`）或中国站（`cn`）使用 PAT 引导或设备 OAuth 凭据。
- 创建和导入时，要求 `platform=qoder` 和 `type=cosy` 互相对应；OAuth、API Key、Upstream、Bedrock 和 Service Account 都不能保存为 Qoder 提供商。
- 创建和编辑时的凭据校验，由 provider 的规则和 `provider/provider.CreateCredentialHooks` 组合完成；站点的 PAT 交换和机器身份准备使用原生的提供商记录，保留 CN 和 Global 的差异，并在调用时执行。
- `credentials.site` 选择站点。为了兼容已有的提供商，没有这个字段时按 `global` 处理。
- `credentials.refresh_mode` 记录令牌的来源。没有时按 `cosy` 处理；中国站的标准 OAuth 使用 `qodercn20`。
- 手工导入可以只提供 `pat`，也可以提供一组现有的 COSY 令牌。
- 现有的 COSY 令牌凭据包括 `security_oauth_token`、`refresh_token`、`machine_id`、`machine_token`、`machine_type`、`uid` 或 `aid`，以及可选的组织元数据。
- 国际站的 OAuth 和手工 COSY 凭据，通过国际站的 Center 流程刷新。中国站的标准 OAuth 先刷新 OpenAPI 令牌，再完成 `userinfo -> status`；中国站的手工 COSY 凭据使用 Gateway 的旧刷新路径。PAT 会话根据原始的 PAT 重建。
- 中国站支持标准的 QODER_PAT 和 QoderCN20 登录。企业专属域名的 `PERSONAL_TOKEN`、组织选择、AK/SK 和区域发现都不支持。

## 客户端协议

Qoder Cosy 的原生协议只有 `qoder_chat`；对外的 Messages、Responses 和 Chat，由分组明确映射到这个协议。分组可以开放哪些入口，见[统一协议能力](protocol_capabilities.md#group_protocol_routes)。管理员关闭全部文本协议时，用户"使用 Key"的界面显示没有可用的文本协议，网关在提供商选择和计费之前，返回对应协议的 `403`。

协议开关不影响 Qoder 的站点、模型路由、思考控制和提供商资格。Responses 子路径和 WebSocket 不属于 Qoder 的能力，启用 Responses 协议也不会开放它们。

## 模型别名与映射

国际站的公开别名：

- `claude-opus-4-6`
- `auto`
- `performance`
- `efficient`
- `lite`
- `qwen3.8-max`
- `qwen3.7-max`
- `qwen3.7-plus`
- `kimi-k3`
- `kimi-k2.7-code`
- `glm-5.3`
- `glm-5.2`
- `deepseek-v4-pro`
- `deepseek-v4-flash`
- `minimax-m3`

中国站的公开别名：

- `auto`
- `qwen3.8-max`
- `qwen3.7-max`
- `qwen3.7-plus`
- `qwen3.6-flash`
- `deepseek-v4-pro`
- `deepseek-v4-flash`
- `glm-5.3`
- `glm-5.2`
- `kimi-k2.7-code`
- `minimax-m2.7`

提供商的模型列表和默认别名解析，按 `credentials.site` 进行。路由候选直接从两个站点的 alias 表枚举、去重并按 ID 排序。名称和通用模型候选由统一目录提供。混合了两个站点的分组，公开其可调度的提供商支持的并集，但站点专属的别名和路由键，只会调度给兼容的提供商。手动配置的提供商映射仍然可以覆盖这些规则，未知的原始路由键继续透传。

两个站点的 `qwen3.8-max` 都映射到正式路由 `qmodel_38max`。已经移除的 `qwen3.8-max-preview` 别名和 `qmodel_preview` 路由，不会被悄悄重定向。还需要使用旧请求名的提供商，要配置手动的 `model_mapping`，例如 `qwen3.8-max-preview -> qmodel_38max`。

Qoder 提供商的 `model_mapping` 和其他平台使用相同的改写规则：

- 键：这一层路由接受的模型名。
- 值：最终的 Qoder 路由或上游模型名。
- 映射本身不限制可以请求的模型范围。

需要把提供商限制在特定的最终路由或上游模型上时，使用 `model_whitelist`。网关先应用映射，再检查白名单；没有配置白名单的提供商不受限制。分组级的映射同样只改写一步，所以不要配置 `custom -> 公共别名 -> 路由键` 这样的别名链，直接配置 `模型 -> 上游路由键`。

## 站点思考控制

站点的能力快照已经基于 Qoder 国际站和中国站的 1.24.2 版本验证过。这个版本号通过 OpenAPI 的 User-Agent、`Cosy-Version`、签名载荷里的 `cosyVersion` 和推理请求里的 `business.version` 传递。

能力查找发生在提供商级的模型映射和公共别名解析之后，所以直接映射到已知路由键的自定义请求模型，会得到相同的处理。国际站和中国站共用的路由键，思考能力相同。未知的路由键，以及没有验证过的国际站专属模型，保持原样。

| 站点 | 公开模型 | 路由键 | 思考能力 | 下游映射 |
| --- | --- | --- | --- | --- |
| 国际站 | `qwen3.8-max` | `qmodel_38max` | 只有开关 | 任何有效的强度、启用或自适应开关、正数的预算，都开启思考，不发送级别 |
| 国际站 | `qwen3.7-max` | `qmodel_latest` | 只有开关 | 与 Qwen3.8-Max 相同 |
| 国际站 | `qwen3.7-plus` | `qmodel` | 只有开关 | 与 Qwen3.8-Max 相同 |
| 国际站 | `deepseek-v4-pro` | `dmodel` | High / Max | Minimal、Low、Medium 映射为 High；High、Very High、Max 映射为 Max；任何正数预算映射为 Max |
| 国际站 | `deepseek-v4-flash` | `dfmodel` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 国际站 | `glm-5.3` | `gmodel` | Low / High / Max | Minimal、Low 映射为 Low；Medium、High 映射为 High；Very High、Max 映射为 Max；任何正数预算映射为 Max |
| 国际站 | `glm-5.2` | `gm51model` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 中国站 | `auto` | `auto` | 用户无法设置 | 保持原样 |
| 中国站 | `qwen3.8-max` | `qmodel_38max` | 只有开关 | 与国际站的 Qwen3.8-Max 相同 |
| 中国站 | `qwen3.7-max` | `qmodel_latest` | 只有开关 | 与 Qwen3.8-Max 相同 |
| 中国站 | `qwen3.7-plus` | `qmodel` | 只有开关 | 与 Qwen3.8-Max 相同 |
| 中国站 | `qwen3.6-flash` | `q36fmodel` | 用户无法设置 | 保持原样 |
| 中国站 | `deepseek-v4-pro` | `dmodel` | High / Max | Minimal、Low、Medium 映射为 High；High、Very High、Max 映射为 Max；任何正数预算映射为 Max |
| 中国站 | `deepseek-v4-flash` | `dfmodel` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 中国站 | `glm-5.3` | `gmodel` | Low / High / Max | 与国际站的 GLM-5.3 相同 |
| 中国站 | `glm-5.2` | `gm51model` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 中国站 | `kimi-k2.7-code` | `kmodel` | 用户无法设置 | 保持原样 |
| 中国站 | `minimax-m2.7` | `mmodel` | 用户无法设置 | 保持原样 |

网关从各入站协议读取原生的控制字段：

- Chat Completions：读取 `reasoning_effort`，没有时读 `reasoning.effort`。
- Responses：读取 `reasoning.effort`，没有时读 `reasoning_effort`。
- Anthropic Messages：读取 `output_config.effort`、`thinking.type` 和 `thinking.budget_tokens`。

明确的 `thinking.type=disabled` 或强度 `none` 始终优先。其次是明确的有效强度，然后是正数的预算，最后是 `enabled` 或 `adaptive`；字段缺失或无效时，思考保持关闭。可以切换的模型在关闭时，发送 Qoder 的 `reasoning_effort=none`，请求不会退回上游的默认值。虽然 Qoder 把 Qwen3.8-Max 的思考标为默认开启，这里仍按 TokenRouter 明确控制的规则处理。未知的强度字符串会被忽略，请求照常执行。

## 上下文窗口

输入上限由 `modelcatalog/qoder.go` 的固定路由表提供，写入统一属性的 `input_limit`。有明确公共身份的模型继续读取目录的总上下文，auto、performance、efficient、lite 等动态档位的总上下文保持未知。每次请求在提供商映射和站点选择后解析属性，再叠加管理员覆盖。故障转移换站点时，使用同一请求快照重新计算目标路线。

| 站点 | 最大输入 Token | 路由键 |
| --- | ---: | --- |
| 国际站 | 1,000,000 | `ultimate`、`performance`、`qmodel_38max`、`qmodel_latest`、`qmodel`、`kmodel_latest`、`gmodel`、`gm51model`、`dmodel`、`dfmodel`、`mmodel` |
| 国际站 | 256,000 | `kmodel` |
| 国际站 | 180,000 | `auto`、`efficient`、`lite` |
| 中国站 | 1,000,000 | `qmodel_38max`、`qmodel_latest`、`qmodel`、`q36fmodel`、`dmodel`、`dfmodel`、`gmodel`、`gm51model` |
| 中国站 | 256,000 | `kmodel` |
| 中国站 | 200,000 | `mmodel` |
| 中国站 | 180,000 | `auto` |

有官方运行时 `contextConfig` 的路由，把所选的上限写进 `model_config.max_input_tokens`、`chat_context.extra.ideModelConfigOverride.max_input_tokens` 和 `parameters.context_length`。最大值固定的路由只写 `model_config.max_input_tokens`。未知 raw route 继续透传，输入上限使用 200,000 的执行默认值，来源标为 `execution_default`。

TokenRouter 不读取客户端声明的上下文上限。Chat Completions、Responses 和 Anthropic Messages 里的输出 Token 字段只控制输出。客户端自己维护模型目录、压缩阈值和截断行为；`/v1/models` 和 `/models` 不公开非标准的上下文元数据。

<a id="qoder_execution_boundary"></a>
## 平台执行与输出

Qoder 的原生客户端、站点和模型能力、签名、报文转换和会话增量状态，只在 `upstream/qoder` 实现。`Executor.Execute` 接收本次的协议、整理好的目标和同步输出接口；平台代码不读取 Gin、旧的提供商实体或配置对象。`gateway/provider.QoderRuntime` 持有平台执行器和会话存储（各只有一个），app 为 Chat 和其他入口绑定同一个实例；目标使用原生的提供商记录，令牌和客户端在调用时取得。主 Chat 链路直接使用 gateway 的执行器，Messages、Responses 和兼容的 Chat 尝试，通过 `gateway/httpapi.ForwardQoderAttempt` 同步输出，共用同一个运行时，没有另外的尝试循环。

只用于手动导入和 opt-in 测试的本地凭据读取，隔离在 `upstream/qoder/localauth`；正常运行的服务不会自动读取本机的 Qoder 登录资料。

实际的写入和 Flush 由 HTTP 适配器负责，转换器逐段输出，整条 SSE 不会被聚合。`gateway/httpapi.QoderRequestMetadata` 复制本次的请求头，并整理出原来的 Key ID 和客户端标记，平台的会话键计算只在 upstream 执行。HTTP 测试连接同一个输出 Writer 和平台流实现。

客户端输出按[统一用量口径](protocol_capabilities.md#client_usage_cache)呈现已观察到的缓存读取与写入。OpenAI 输入总量包含全部输入桶，Messages 保持普通输入与缓存分离；`cacheable_tokens` 只表示可缓存量，不当作缓存写入。当前 Qoder 解析没有生成非零写入桶，序列化器仅保留调用方已经提供的真实计数。

流里已经开始服务、并且收到了 usage 之后，上游如果继续报错，会同时返回部分结果和错误：完成入口用这些已经观测到的用量结算一次，同时返回失败响应、失败反馈并回滚会话；这个失败不会被记成成功的会话，系统也不会重新推理或估算缺失的用量。还没有开始服务、或者没有观测到用量的失败，不做这种部分结算。

解析器补上的工具类型 `function`，不作为工具调用的身份。并行工具流里，缺少 ID、索引和名称的参数片段，不会被写进任意一个工具；单个工具的参数片段，继续写入已有的调用，不会因为默认类型被拆成新的块。

准备和取得凭据之后、发起推理之前，检查原请求是否已经取消，取消后不再启动新的推理。已经进入上游的流式请求，和客户端的取消脱钩，在十五分钟的执行预算内收集尾部的 usage；非流式请求按取消传播。响应体由平台执行关闭，提供商和用户的 Lease 由请求编排负责释放。

### 授权与凭据缓存

提供商授权的十分钟会话、完成认领、pending 和成功重放，由 `provider.QoderAuthorization` 持有，`provider/provider` 只整理交换的结果。刷新资格和新旧凭据的合并在 provider，实际的站点交换在 upstream；持久化经过已有的刷新协调和身份 CAS。请求失败后，凭据身份的判断和刷新锁的等待也由 provider 负责：立即回读一次，之后每 100ms 轮询，总预算 3 秒；只有凭据确实轮换了才重试，旧凭据不会被当作刷新成功返回。供应商错误导致的限流或过载写入，由 `provider/provider` 执行，使用和请求取消脱钩的 5 秒预算，尽力而为，失败时不影响请求。

运行时的凭据缓存由 `provider.QoderSessions` 持有（只有一个），带身份世代和 90 秒的共享构建预算。`provider/provider.QoderTokenProvider` 负责凭据整理和供应商构建，`QoderTokenRefresher` 组合站点交换和提供商凭据的合并，传输复用 HTTP 池和 TLS 策略。单个等待者取消不影响其他等待者；应用停止时，取消共享的构建，等待已经进入的操作，并拒绝迟到的回填。各平台令牌和 Qoder 会话的失效，统一由 `provider.CompositeTokenCacheInvalidator` 调用各自的缓存接口，同时清理备用键，删除失败时只尽力而为。授权的 HTTP 实现在 `provider/httpapi`。

## 计费范围

Qoder 和其他平台使用同一套价格解析规则，公开别名和路由键不需要手工定价。先按价格配置的 `billing_model_source`，选定请求模型、分组映射模型或上游模型，再为这个模型解析价格；三者之间不会互相查找另一个模型的价格。

1. 共享价格配置里手动填写的单价或有效区间优先。
2. 分组没有关联价格配置时，使用统一的目录价格。
3. 价卡只填了 Fast/Flex、Max 推理或分时倍率时，保留继承来的全部基础价格，只覆盖对应的倍率。
4. 手动价卡里没填的字段，按通用的回退规则处理，填 `0` 表示免费。空的价卡不会遮住目录价格。

模型市场、管理界面的默认价格填充和实际结算，都使用上面的规则。有目录价格的公开别名，可以正常展示和扣费；没有任何 token 基础价的路由键，显示为未定价。图片请求和其他平台一样，使用同一型号手动配置的目录图片报价。分组的模型白名单单独保存，模型权限和共享价格配置无关。

提供商统计优先使用独立的成本规则，没有命中时，和其他平台一样查询上游模型的目录价格，不使用用户的自定义价格，也不会因为 Qoder 的请求别名而禁止回退。Qoder 自定义统计规则按请求名、分组映射名、上游名的顺序匹配。

成功的零费用请求，同样写入完整的使用记录，并以零金额走完正常的订阅和余额结算流程。

## 上游提供商用量

Qoder 有独立的上游月度 Credits 配额。TokenRouter 只把它当作提供商的用量和容量信息，它和 TokenRouter 的用户余额、订阅相互独立。

提供商用量界面查询所选站点的 Gateway `/api/v2/quota/usage` 端点，并把最近一次成功的快照保存在 `provider.extra.qoder_quota_snapshot`。国际站的请求都使用 COSY 签名；中国站的 `qodercn20` 和 PAT 提供商同样使用 COSY 签名，旧版或导入的 COSY 会话按官方客户端的方式，使用 `security_oauth_token` 的 Bearer 鉴权。中国站的请求在有缓存的 `orgId` 时带上它，1.24.2 版本的常规配额查询不发送 `quota_key`。

实时查询失败时，管理界面可以同时显示缓存的快照和降级的用量错误。完整的上游月度 Credit 余额，是 `userQuota`、`addOnQuota` 和 `orgResourcePackage`（或 `sharedQuota`）之和，和 qodercli 的用量视图一致。对于非个人的零配额提供商，`isQuotaExceeded=true`，或者正数的合计配额已经用完时，会把正常的提供商调度信号 `rate_limited_until` 设到 Qoder 的 `expiresAt`；还有附加 Credit 或组织 Credit 时，会阻止或清除过期的配额锁。

观测到的 `personal_standard` 结构，如果 `total=0`、`remaining=0`，并且 `expiresAt` 远在未来，只用于展示，等真实的请求错误确认限制后才生效。请求时的错误码 `115`、`agentLimitResetTime` 或 HTTP 429，走正常的提供商限流冷却。

## 运维

Qoder 以 `qoder` 平台键参与调度快照、错误透传、故障转移和管理端的平台用量视图。对于可以重试的上游故障，例如 Qoder 权益拒绝错误码 `112`、Agent 限制、429 或 5xx，网关可以在写出任何流式分块之前换到另一个提供商；流式输出开始后，只返回符合流协议的错误，不再换提供商。错误码 `112` 视为模型或提供商的权益拒绝，和认证令牌无关，所以不会触发令牌刷新。

管理端的提供商数据导出和导入，会保留 `qoder`、`cosy` 提供商和它们的凭据，用于备份和迁移。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)和[接口目录](index.md)。
