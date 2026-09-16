# Antigravity 上游

Antigravity 原生 OAuth 只显示 GenerateContent 的平台适配变体，Messages/Responses/Chat 是分组转换入口；历史 upstream 类型保留 Messages 原生直连，静态 API Key 的既有契约冲突不因目录统一而扩大支持范围。统一配置字段与入口门禁见[统一协议能力](protocol_capabilities.md)。

本文描述 Antigravity 账号接入、专用 Claude/Gemini 端点、协议转换、混合调度与上游失败语义。它用于修改 Antigravity 适配器时保持平台隔离，不记录终端用户的 Claude Code 操作技巧，也不承诺上游未验证的模型能力。

## 章节导航

- [账号与凭据](#账号与凭据)：修改 OAuth 导入、刷新或账号资格时读取。
- [专用端点](#专用端点)：修改强制平台路由时读取。
- [协议适配](#协议适配)：修改 Claude/Gemini/OpenAI 转换时读取。
- [混合调度](#混合调度)：修改与 Anthropic/Gemini 分组共用账号时读取。
- [模型与额度](#模型与额度)：修改可见模型、配额或价格归属时读取。
- [失败与恢复](#失败与恢复)：修改限流、重试、切换或凭据错误时读取。

<a id="antigravity_account_contract"></a>
## 账号与凭据

Antigravity 账号的 `platform` 为 `antigravity`。管理端通过 `/api/v1/admin/antigravity/oauth/*` 生成授权 URL、交换 code 或验证/刷新 refresh token，再使用通用账号创建/更新路径保存凭据。OAuth access token 由 token provider 在使用前刷新；project ID、subscription/tier、privacy mode、额度和上游 user agent 等属于账号/运行时元数据。

管理端还提供“静态上游”表单，但当前把它保存为 `type=apikey`；Antigravity Claude 直连和 token provider 的历史静态分支只识别 `type=upstream`。这两种类型目前不能视为等价，新的静态账号不构成完整正式支持。历史 `upstream` 账号仅用于 Claude 直连兼容。

原生兼容转发要求 `type=oauth` 的 Antigravity 账号。setup token、upstream 或 API-key 类型不能被当成原生 OAuth 兼容账号；服务应返回可操作错误而不是把不匹配凭据发送给上游。standard-tier 账号缺少必要 project ID 时也要显式拒绝。完整平台/账号分类和已知冲突见[上游账号能力矩阵](upstream_account_matrix.md)。

账号导入、凭据刷新和批量导入后会检查/设置适用的隐私状态。凭据、refresh token、project ID 和上游响应中的内部标识不得出现在客户端错误或模型列表中。

## 专用端点

专用路由在 API Key 鉴权前写入 `ForcePlatform=antigravity`，因此只选择 Antigravity 账号，不受混合调度开关影响：

| 入口 | 客户端协议 | 处理 |
| --- | --- | --- |
| `GET /antigravity/models` | Claude 风格模型列表 | 返回当前 Key/分组可请求的 Antigravity 模型 |
| `POST /antigravity/v1/messages` | Anthropic Messages | 转换并转发 Claude 请求 |
| `POST /antigravity/v1/messages/count_tokens` | Anthropic token count | 路由入口保留；当前明确返回 `404`，客户端应回退本地估算 |
| `GET /antigravity/v1/models`、`/usage` | Claude 风格自省 | 模型与 Key 用量 |
| `/antigravity/v1beta/models/*` | Gemini v1beta | list/get/generate/stream/countTokens 等 Gemini 形状 |

Claude Code 可把 base URL 指向部署地址的 `/antigravity`，认证值仍是 TokenRouter API Key。Gemini 入口中的 `x-goog-api-key` 同样接收 TokenRouter Key，不是 Google 上游 key。

专用路由仍执行请求体限制、client request ID、Ops error logger、endpoint 归一化、分组和订阅/余额准入。强制平台只限制候选账号，不能绕过 Key、团队、分组或模型权限。

## 协议适配

通用 Claude/Gemini wire 变体、schema 清理、非流及 SSE 状态已由 `protocol` 唯一实现；与 OpenAI 兼容报文不同的 `max_tokens`、metadata、tools 形状保留明确变体。`upstream/antigravity` 拥有 v1internal 外壳、project/身份补丁、原生 session ID、模型回退和流式协议事实；账号授权、项目发现、token 回填和健康写入归 `account`。旧 HTTP 适配继续承接错误展示、Ops 投影与入站完成处理。平台/模型判断选择 thinking/signature/tool 选项后传入 bridge，纯转换不反向读取这些平台状态。

Antigravity 分组支持 Messages、Responses、Chat 和 Gemini GenerateContent，新建时默认启用 Messages 与 Gemini GenerateContent；四项都可关闭，迁移前已有分组启用四项。通用入口和 `/antigravity/*` 别名都按最终分组执行对应协议门禁；Gemini 模型列表 GET 不受生成协议开关影响。

Anthropic Messages 经过 Antigravity request transformer 生成上游 Gemini/内部请求形状，响应和 SSE 再恢复为 Anthropic 协议。工具定义、tool choice、thinking、缓存断点、图片输入、token 用量和停止原因都需要双向转换；schema cleaner 会移除上游不接受的 JSON Schema 表达。

通用 OpenAI Chat Completions 和 Responses 在选到原生 Antigravity OAuth 账号时走兼容适配器：

```text
Chat Completions / Responses
  -> OpenAI-compatible normalized form
  -> Anthropic request
  -> Antigravity/Gemini upstream
  -> Anthropic response/stream
  -> original OpenAI protocol
```

每次 failover attempt 都必须从原始请求重新转换，并刷新工具名回程映射。流开始后遵守共同不可切换边界。Gemini v1beta 专用入口保持 Google 错误/流形状，不经过 OpenAI envelope。

兼容层把 Chat 请求中的正数 `max_completion_tokens`（缺省时使用 `max_tokens`）在转换为 Anthropic 请求前封顶为 64000；零、负数或缺省值不会覆盖转换器已有的默认上限，避免超大客户端参数被上游拒绝。

## 混合调度

Antigravity 账号 `extra.mixed_scheduling` 为布尔 `true` 时，可以作为 Anthropic 或 Gemini 原生分组的候选账号。缺失、`false` 或字符串 `"true"` 都视为未启用。候选账号还必须属于目标分组、状态 active/schedulable，并满足模型、额度、并发、资格和 endpoint 能力。

混合调度只扩大账号候选集，不改变请求平台的产品语义：

- Anthropic 分组的请求仍按 Anthropic 入口、分组倍率和错误形状处理。
- Gemini 分组的请求仍按 Gemini 入口和模型 URL 处理。
- 粘性会话命中已关闭混合调度的 Antigravity 账号时，必须丢弃旧绑定并重新选择原生或合格混合账号。
- 专用 `/antigravity/*` 入口永远强制平台；普通 Antigravity 分组也不因该开关混入其它平台账号。

账号的混合调度状态或分组关系变化后要重建原生平台和 Antigravity 相关调度快照，清理旧粘性状态。Anthropic 与 Antigravity Claude 不能在同一显式会话里无约束切换；会话隔离、粘性和缓存计费规则用于防止上下文跨账号语义漂移。

## 模型与额度

Antigravity 同时提供 Claude 与 Gemini 模型族。Gemini 3.6 Flash 的基础、high、low、medium 与 tiered 五种模型 ID 均进入默认模型目录和身份映射；账号存在自定义映射时，只要没有覆盖它们的通配符，这些精确直通映射仍会自动保留。可见模型来自默认映射、分组/渠道限制、账号资格和当前可请求解析；API Key 精确别名可投影到列表，目标不可请求时不展示。模型能力不能只由名称前缀推断，thinking/image 等能力由适配器与账号详情共同约束。

额度查询按账号和模型 scope 保存上游 reset/remaining 状态，并可包含 AI Credits。429/503 分类区分模型限流、credits 耗尽和共享容量不足；请求结算的 `QuotaPlatform` 必须保留 Antigravity，即使客户端从 Anthropic/OpenAI 兼容入口进入。账号成本和用户扣费仍遵守渠道计价与分组倍率边界。

## 失败与恢复

- 短 `RetryInfo` 可以在同账号做一次受限等待；长模型限流标记模型/账号并请求调度层切换。
- 单账号模式允许有总等待上限的退避重试，多账号模式优先切换；Context 取消立即停止。
- `MODEL_CAPACITY_EXHAUSTED` 被视为共享模型容量，使用全局去重和有界重试，不能通过快速轮换账号放大上游压力。
- 粘性会话切换账号时可把普通输入按 cache-read 计费，反映缓存失效；该标志必须进入结算输入。
- OAuth credential 被刷新后仍遭拒绝时返回要求重新授权并检查 project ID 的脱敏提示，同时把账号标记为可恢复错误。
- 只有白名单中的安全上游提示可以透传；响应体日志受开关、字节上限和脱敏约束。

修改适配器时应覆盖非流/流、Claude/Gemini/OpenAI 三种客户端形状、工具/thinking、单/多账号限流、混合调度关闭后的快照失效和用量归属测试。

相关文档：[上游账号能力矩阵](upstream_account_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口边界](http_api.md)、[接口目录](index.md)。


<a id="antigravity_native_execution"></a>
## 原生执行与账号拥有者

`upstream/antigravity.Executor` 接入 Claude、Gemini、Chat、Responses 和历史静态 upstream 五条生产链，闭合单次平台交换、恢复、输出及最终响应体关闭。账号内普通重试、智能重试、credits 请求和共享模型容量去重只有一份实现；全局账号切换、付款主体和资金完成由旧入站编排持有，等待 S11。`Probe` 复用同一平台重试，只测试指定账号，不取得用户或账号的请求槽。

流通过同步 `OutputSink` 输出，保留原来每种协议的前导缓冲、非流收集、心跳、首 token 与断开后的尾部读取规则。结果区分已观测 usage、是否服务和错误；HTTP 提交及重试关闭与语义输出分开，不用新观测改变 Antigravity 旧失败结算规则。平台用量归一化、外层冻结的 QuotaPlatform 与后台完成输入保持原链：强制平台路由优先，否则按分组平台；后台不能用缺少原路由上下文的 context 重新计算。

OAuth 会话、交换后的一次性删除、项目与套餐发现、隐私设置及验证由 `account.AntigravityAuthorization` 编排。原生客户端只执行供应商协议，wire 变体由 `protocol/google` 保留。token provider 的 project 回填冷却、缓存键、八秒请求刷新预算、后台十五分钟刷新资格与原 CAS 不变。额度展示、credits/模型窗口及 INTERNAL 500 惩罚归账号；共享缓存、计数器和发布端口复用原实例。

app 统一登记已进入的原生尝试和授权活动。构造不启动清理；停止取消运行操作并等待，仍保留原有限重试的收尾行为。预算耗尽报告未完成项，不视作排空成功。管理授权 URL、DTO、错误响应及旧常量/私有测试入口通过兼容层衔接；只供 unit 使用的私有转接不进入生产构建。
