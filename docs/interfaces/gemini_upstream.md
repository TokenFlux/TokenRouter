# Gemini 上游

本文记录 Gemini 平台的提供商类型、认证凭据、v1beta 原生与兼容协议转换、会话状态、SSE 生命周期、配额错误处理及 Vertex 规则。动态模型清单见[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)，同协议族的 Antigravity 见[Antigravity 上游](antigravity_upstream.md)。

## 章节导航

- [提供商与认证](#提供商与认证)：OAuth 变体、官方与第三方 API Key、Vertex 凭据及限流差异。
- [协议分派](#gemini_protocol_dispatch)：v1beta 原生路由、跨协议转换、thinking 策略与创作台图片限制。
- [模型、会话与生图计费](#模型会话与生图计费)：模型链、schema 清理、工具签名缓存与图片计费。
- [配额、调度与错误处理](#配额调度与错误处理)：Tier、429 冷却、Antigravity 同组调度与错误脱敏。
- [SSE 生命周期](#gemini_stream_lifecycle)：帧规范化、多候选终态、保活与超时、用量尾帧及流内错误。
- [原生执行与提供商授权](#gemini_native_execution)：执行回报、授权生命周期与批量/创作复用边界。
- [Vertex 服务账号与对象流](#vertex_service_account_execution)：JWT 交换、Redis 令牌缓存与 Batch/GCS 对象流。

## 提供商与认证

Gemini 正式支持三类提供商（其余通用导入类型无正式转发实现，见[上游提供商能力矩阵](upstream_provider_matrix.md)）：

| 类型 | 认证与变体 | 关键约束 |
| --- | --- | --- |
| `oauth` | `code_assist`、`ai_studio` | `code_assist` 使用内置 Gemini CLI 客户端（`IDE_UNSPECIFIED`、`GeminiCLI` User-Agent）且须绑定有效 GCP project（拒绝纯数字 Project Number）；`ai_studio` 需管理员配置自定义 OAuth client，project 可选并按所选 tier 运行。个人与 Google One（AI Pro / AI Ultra）账号通过 [Antigravity](antigravity_upstream.md) 接入 |
| `apikey` | Base URL + API Key | `credentials.provider_type=third_party` 为兼容第三方（须用非 Google 官方域名）；缺失或 `official` 为 Google AI Studio 官方接入 |
| `service_account` | Google Service Account 换取 Vertex token | 按需解析 project 与按模型覆盖的 location |

协议与凭据规则：

- 原生协议集合：三类提供商均支持 GenerateContent；API Key 另支持 Gemini Batch，Vertex Service Account 另支持 Vertex Batch。分组开放 `image_batches` 后通过提供商绑定走对应批量协议（见[统一协议能力](protocol_capabilities.md)）。
- 凭据与 project 解析：手动 project 优先级、历史凭据字段与按模型选择 location 在调用时从提供商记录解析；服务账号 JSON 校验与 project 提取只在 `upstream/vertex` 执行。提供商测试、批量任务与在线转发共用该按需解析逻辑。
- Code Assist 项目发现与免重登补填：换码阶段依次尝试 `loadCodeAssist`（携带可选 `cloudaicompanionProject` 与 `metadata.duetProject`）、`onboardUser`（默认兜底 `legacy-tier`，长任务经 `GET /v1internal/{name}` 轮询）及 Cloud Resource Manager ACTIVE 项目回退。遇到缺少 `project_id`、`SERVICE_DISABLED` 或 `VALIDATION_REQUIRED` 时，会话在原 TTL 内按 `session_id + state + sha256(code)` 缓存已换取令牌，管理员补填 GCP Project ID 或完成验证后可直接重试换码；`UNSUPPORTED_CLIENT` 与 `TOS_VIOLATION` 立即销毁会话并返回诊断说明。
- 第三方 API Key 差异：无 Google 等级，不写 `tier_id`，不参与按洛杉矶日界计算的本地 RPD/RPM 预检与用量窗口；收到 `429` 时走通用冷却，不按 Google 日配额重置解析。
- 非 Google 账户差异：OpenCode 等非 Google 提供商仅复用 Gemini 报文处理，不走 Google 项目换号、`countTokens` 估算兜底与本地重试；健康状态用共享错误策略与 Retry-After，同提供商重试最多等 8 秒（普通重试受外层预算限制，签名修复单独有界执行）。
- OAuth 刷新：刷新支持重试并兼容历史 client 元数据；token provider 在过期前提前刷新，并通过并发锁避免同一提供商重复刷新。

<a id="gemini_protocol_dispatch"></a>
## 协议分派

### 路由与转换入口

- 原生路由与目录：Gemini SDK/CLI 使用 `/v1beta/models`、`/v1beta/models/{model}` 与 `{model}:{action}`，按 Google 规范处理。前两个 GET 资源由分组可请求目录过滤生成（仅保留 Gemini 协议模型并补本地元数据），不向上游拉取目录。
- 原生入口门禁：POST 动作（GenerateContent、StreamGenerateContent、CountTokens）受分组 Gemini 协议开关控制，GET 模型列表不受影响（见[统一协议能力](protocol_capabilities.md#group_protocol_routes)）；强制平台路径与复合 Key 分别校验分组权限和模型范围。
- 兼容转换入口：Anthropic Messages、Count Tokens、OpenAI Responses 与 Chat Completions 先归一化请求，再准备上游 Gemini 请求并转回原协议。
- Responses 转换细节：支持非流式与 SSE，保留模型、reasoning、工具调用、usage、结束原因与首 Token 指标；仅发心跳失败可换号，产出非心跳协议数据后按 SSE 协议报错结束，不换号也不追加普通 JSON。
- 传输与重试边界：API Key、OAuth 与 Vertex Service Account 在认证头、base URL、project/location 与错误结构上的差异在转换后保持保留。每次 failover 重选提供商并重建载荷；真实协议输出关闭重试窗口，排队与空闲心跳不关闭。
- Antigravity 隔离：`/antigravity/v1beta/*` 强制选择 Antigravity 提供商，见[Antigravity 上游](antigravity_upstream.md)。
- 协议与平台归属：通用报文归 `protocol/gemini`，Google 错误归 `protocol/google`，Anthropic/Gemini 纯转换归 `protocol/bridge`（保留原生与内部方言在 schema、工具配对、签名与预算上的差异）；激活诊断归 `upstream/gemini/codeassist`，HTTP 交换与重试归 `upstream/gemini`，状态映射归 `gateway/httpapi`。

### Thinking 与采样控制

- 预算与档位互斥：Messages、Chat、Responses 转换先按最终服务与模型解析能力再发 thinking 控制；Gemini 2.5 预算型用 `thinkingBudget`，档位型用 `thinkingLevel`，两者互斥。
- 客户端意图保留：关闭、effort 与预算取自转换前请求，显式零预算予以保留。原生 GenerateContent 对 `generationConfig`/`generation_config` 和 `topP`/`top_p` 执行同一采样规则。
- 能力判定：`reasoning=true` 与可调选项分开判断；选项缺失时继续转发客户端控制，空集合表示无可配置控制。参数与管理员覆盖见[统一模型属性](model_catalog_and_marketplace.md#model_attributes)。

### 创作台图片请求

- 创作台 Gemini 图片统一使用 `generateContent` 的 `inlineData`，不使用 File API。
- 自定义 base URL 校验失败时直接失败，不回退到 Google 官方地址。
- 本地按 base64 编码后的 JSON 总大小估算，上限 20 MiB，超出在提交前返回输入过大错误。

## 模型、会话与生图计费

- 模型链与目录：可请求模型由分组与实际可调度提供商能力决定（不含默认常量或无提供商支持的目标）。模型名依次经 Key 重定向、分组映射与提供商映射，Vertex 或 AI Studio 上游标识可与计费模型不同。
- 通用目录与白名单：通用候选来自[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)；白名单为空允许任意型号（格式、认证与协议仍各自检查），默认测试型号用于探测。
- Schema 清理：兼容层维护 thinking、tool/schema、图片、usage、finish reason 与 thought signature。工具 schema 递归删 Gemini 不支持字段；INTEGER 整数 `exclusiveMinimum` 转为加 1 后的 `minimum`（保留更严格下界），无法等价转换的独占下界直接删除。
- 会话连续性：跨轮次依赖 signature、session 或缓存连续性时优先粘性复用原提供商；换号时重评续接，不把另一提供商内部状态当作通用上下文。
- Messages 推理历史：Messages 响应把 Gemini 推理摘要放入独立 thinking 块且签名留空；普通请求回传历史时，转换器省略 `thinking`、`redacted_thinking` 块及仅含这些块的回合。
- 工具调用 ID 与签名关联：网关在内存中按「API Key + 提供商 + 实际转发模型」关联客户端调用 ID 与原始 Gemini 工具调用 ID 和签名。命中时调用与结果共用原 Gemini ID 且签名回填至原调用；最多保留 1 小时并受条数和内存上限约束，重启后重建。未命中时按名称和兼容签名回传；同一消息内连续工具结果按调用顺序提交。上游拒绝缓存签名时，兼容入口可用旧格式重试。
- 生图计费：原生和 Claude 兼容生图按上游实际返回的 `inlineData`/`inline_data` 图片 part 数计费（自定义别名同效）。流式取单个 payload 观测到的最大张数，避免累积 SSE 重复计费；仅在未观测到内联图片时回退按请求或映射后模型名判断是否生图模型。

## 配额、调度与错误处理

- 配额与限流：Gemini tier、上游配额与按模型 reset 信息作为资格与容量信号。普通提供商遇 `429` 更新提供商或模型 `rate_limited_until`，恢复前被调度排除；公共池提供商的 `429` 走请求级同提供商重试或换号，不写默认本地限流（管理员自定义错误策略优先）。实时配额查询失败时剩余额度显示为未知。
- 跨平台同组：Antigravity 可与 Gemini 提供商同组调度，遵循相同的分组、模型、endpoint、额度、并发和凭据约束；普通 Gemini 请求按 Gemini 协议处理且计费归属不变，Antigravity 专用路由只选 Antigravity。
- 错误分类：OAuth 刷新、Service Account 换令牌、project/tier 发现与上游请求错误分别记录。`401`/`403` 区分凭据、project/region、API 未启用与策略拒绝；`429` 解析 reset 时间并更新限流；网络与 `5xx` 仅在响应输出前换号。
- 响应格式与脱敏：原生入口返回 Google 格式错误，Anthropic/OpenAI 入口返回对应客户端格式，并可应用[网关错误响应策略](gateway_error_policy.md)；project、service account JSON、token、API key 与上游内部响应默认不透传。
- 排查入口：依次核对 OAuth 变体、project/location/tier、最终模型、thought/session 状态、配额 reset 时间与 attempt 链。

<a id="gemini_stream_lifecycle"></a>
## SSE 生命周期

Gemini REST、OAuth、Vertex 与 Antigravity 的原生 Gemini 输出，以及 Messages、Chat、Responses 的流式转换和非流式 SSE 汇总，共用统一读取器（`upstream.ReadGeminiStream`）；普通 JSON 上游不经过此读取器。

### 帧读取与保活

- 帧规范化：按完整事件处理 LF、CRLF 与多行 `data:`，允许末尾缺少空行但 JSON 完整的最后一帧。`MaxLineSize` 限制完整帧字节（含忽略的注释与元数据）。无效 JSON、超限帧、空流和缺失终态的 EOF 均返回失败。
- 原生输出净化：原生输出只发单行 `data: <JSON>\n\n`，解包后保留未知 JSON 字段；不向 Google SDK 转发 `event`、`id`、`retry` 或上游心跳。
- 空事件与心跳：中央及排队保活发送 `\n\n`，计入心跳字节但不建立业务输出、TTFT 或重试提交。第三方上游仅含空 `candidates` 的占位帧也按空事件消费，不建立首事件时间；带其他实际元数据的帧保留。

### 候选终态与超时

- 多候选终态：按最终请求 `candidateCount`（默认 1）跟踪各候选索引，单个候选完成不提前结束其他候选。全部预期候选完成或出现明确 `promptFeedback.blockReason` 时建立终态。有效内容之后的 `[DONE]` 视为兼容完成信号但不下发；仅含用量、心跳或 DONE 判为空流。工具调用、安全阻止与 `MAX_TOKENS` 保留原语义。
- 空闲超时：等待响应头由传输层超时控制；收到响应头后，`stream_data_interval_timeout` 限制相邻上游字节间隔（默认 180 秒，`0` 禁用），上游心跳刷新该时钟，本地下游心跳不刷新。
- 用量尾帧与收尾：有效终态后停止下游心跳，继续收用量尾帧至 EOF、DONE 或固定 5 秒上限。尾部中断或超时记 `gemini.stream_tail_incomplete` 并保留已完成结果与已有用量，明确上游业务错误仍返回失败。结束时关闭响应体并等待读协程退出。

### 流内错误、汇总与用量

- 流内错误：HTTP 200 内的 Google error 在转换前识别，不吞错也不补成成功。本地读超时、截断与格式故障按 `502` 进既有策略；明确上游错误保留状态与安全消息。
- 错误交付格式：未提交 HTTP 时发 JSON 错误；已提交原生 SSE 时发 data-only Google error（含 `code`、`message`、`status`）；转换入口发各自流式错误且只发一次。读取失败保留已观测部分结果，结算资格按原入口规则判定。
- 非流式 SSE 汇总：各候选独立累积内容与终态，互不覆盖；相邻文本仅在其他元数据完全一致时合并，以保留不同 thought signature。稀疏用量尾帧只更新出现且非 `null` 字段，保留显式零值，不抹除已有输入或缓存计数。
- 工具调用分片与补签：流式转换按 Gemini 调用 ID 区分工具调用，已结束调用收到补发签名时更新原记录；缺少 ID 时同帧逐个输出，并在上游整批重发时跳过重复前缀。流式与非流式共用参数拼接规则（非流式按 ID 合并）。回传历史时，每步首个无签名调用补兼容标记，后续已知无签名并行调用保持原样。
- 缓存用量口径：流式与非流式均返回上游缓存读取数；Messages 普通输入扣除缓存，OpenAI 协议总输入包含缓存；客户端用量与后台记录共享同一快照（见[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)）。

<a id="gemini_native_execution"></a>
## 原生执行与提供商授权

- 执行边界：单次平台交换、协议输出与关闭响应体归 `upstream/gemini`，Messages、原生、Chat、Responses 各自保留流式、非流式与 Code Assist 缓冲分支，原生输入与兼容 REST 净化分开处理；原生错误、重试与配额时间解析不修改提供商持久化数据。流式输出不加整流缓冲，各入口保持各自的断开处理。
- 结果上报：单次执行分别返回已观测 usage（含明确零值）、语义输出、旧版 TTFT、内联图片张数与失败分类；`countTokens` 本地估算单独携带，不计入实际 usage 与资金记录；图片回退判断与结算条件由调用方处理。
- 授权与清理：`code_assist` 与 `ai_studio` 两类 OAuth 编排、project/tier 发现、同会话免重登令牌缓存、token 回填与刷新资格归 `provider`，协议交换、`GetOperation` 轮询与 Resource Manager 查询归 `upstream/gemini/codeassist`。动态 OAuth 配置在调用时读取，缓存键与刷新 CAS 兼容；关停超时且有限重试未结束时不报告已排空。
- 批量与创作复用：Batch JSONL 编码与创作台图片调用使用原生实现（`protocol/gemini` 保留显式报文变体），任务状态机、清理与资金由任务用例负责。Gemini 不依赖 Vertex 包，原生执行通过传入 URL 与认证参数复用同一协议输出。

<a id="vertex_service_account_execution"></a>
## Vertex 服务账号与对象流

- 职责边界：`upstream/vertex` 负责 project/location 端点、Claude 日期与 body 变体、Beta 过滤以及 Batch/GCS 技术调用；历史凭据字段、手动 project 与按模型 location 覆盖归 `provider`。
- 签名隔离：`upstream/internal/googleauth` 仅接收整理好的密钥与代理执行 RSA JWT 签名，不读提供商、缓存或完整配置，私钥不出现在执行结果中。
- 令牌缓存与并发：访问令牌按 `vertex:service_account:` 身份摘要缓存于 Redis（带并发锁、TTL 与 5 分钟提前刷新），由 Claude、Gemini 与批量图片共用。锁竞争者等待 200ms 后重读缓存；等待期间被取消立即返回取消错误，不回读也不发起交换；Redis 故障时降级处理。
- 协议复用与对象流：Claude 和 Gemini 单次执行通过调用方传入的 Vertex 参数复用各自协议输出链，平台间无交叉依赖，也不引入嵌套换号循环。Batch（提交、读取、取消）与 GCS（上传、分页、删除、对象流）各自只有一份技术实现；对象流由接收方关闭，任务归属、状态转换、受控清理与资金捕获由 batchimage 和 creative 任务用例负责。

相关文档：[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[提供商维护](../operations/provider_maintenance.md)。
