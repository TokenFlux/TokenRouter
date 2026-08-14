# 网关请求生命周期

本文描述 AI 网关请求从 Gin 路由进入到认证、模型归一化、账号调度、上游转发、故障转移和用量结算的共同阶段。它用于修改跨协议热路径时保持顺序和失败语义，不枚举全部端点、供应商字段或某个平台的模型能力。

## 章节导航

- [入口族与处理器](#入口族与处理器)：确定请求由哪个协议分支拥有。
- [共同处理管线](#共同处理管线)：核对不可随意交换的阶段顺序。
- [认证与准入](#认证与准入)：修改 API Key、权益或请求上下文时读取。
- [模型名称链](#模型名称链)：修改模型映射、列表或响应恢复时读取。
- [账号选择与故障转移](#账号选择与故障转移)：修改调度、并发、粘性或重试时读取。
- [转发与流式边界](#转发与流式边界)：修改上游调用和错误返回时读取。
- [用量与结算](#用量与结算)：修改记录、价格或扣费时读取。
- [扩展约束](#扩展约束)：新增入口或平台时检查。

## 入口族与处理器

`RegisterGatewayRoutes` 在面板 `/api/v1` 之外注册客户端协议入口。核心入口族为：

| 入口族 | 主要用途 | 处理器分派 |
| --- | --- | --- |
| `/v1`、裸 `/models`/`responses` 等兼容别名 | Anthropic Messages、OpenAI Responses/Chat/Embeddings、图片、视频、模型与用量 | 根据所选分组平台进入 `GatewayHandler`、`OpenAIGatewayHandler` 或 `QoderGatewayHandler` |
| `/v1beta` | Gemini 原生模型、生成、流式生成、token 统计 | Google 形状的 API Key 认证和 Gemini/Antigravity 兼容服务 |
| `/antigravity/v1`、`/antigravity/v1beta` | 强制 Antigravity 平台的 Claude/Gemini 专用入口 | 在上下文写入 force platform，再复用通用 handler 与调度 |
| `/backend-api/codex` | Codex/ChatGPT 风格 Responses、Realtime 和 sideband | OpenAI handler；部分路径有专用认证/路由限制 |
| 批量图片管理 | 提交、查询、下载、取消和清理任务 | 专用 handler/service；查询类入口只按任务归属认证，不重新选择模型账号 |

同一个 URL 可能因方法、请求意图或分组平台走不同处理器。例如 `/v1/messages` 对 OpenAI/Grok 分组走 OpenAI 协议桥，对 Qoder 走 Qoder handler，其余走通用 Anthropic handler。路由层负责这个分派，service 层不能假设路径名唯一决定上游平台。

公开入口不再注册 `/v1/sub2api/billing`。它不是模型、用量或结算管线的别名，访问时直接得到普通 `404`，也不会进入 API Key 非消费请求分支。

<a id="gateway_pipeline"></a>
## 共同处理管线

```text
请求体/连接限制、request ID、Ops 采集
                 |
             API Key 认证
                 |
       复合 Key 选组 -> Key 级模型重定向
                 |
       用户/团队/分组/IP/权益准入
                 |
    RequireGroup + 客户端协议准入门禁
                 |
             协议 handler 分派
                 |
  解析与归一化 -> 内容策略 -> 用户并发槽
                 |
          等待后的权益二次检查
                 |
  会话/渠道/能力解析 -> 账号选择 -> 账号并发槽
                 |
       请求转换、凭据/代理和上游转发
                 |
      可重试错误 -> 受限故障转移循环
                 |
  成功响应/流 -> 用量解析 -> 幂等结算 -> 记录
                 |
      响应模型元数据恢复与 Ops 完成采集
```

这条管线有几个不能交换的约束：

- 复合 Key 必须先用客户端的完整 `前缀/模型` 选择分组，Key 级模型重定向再处理去前缀后的模型。
- 客户端协议准入必须使用普通 Key 的绑定分组或复合 Key 最终选中的分组；拒绝发生在协议 handler、账号选择和计费之前。
- 用户并发等待可能跨越余额、订阅或额度变化，获取用户槽后必须通过 BillingCache 再检查一次权益。
- 模型权限、渠道限制和账号资格必须基于逐层解析后的对应模型，不能用客户端别名直接替代最终路由模型。
- 上游成功后才增加相应 RPM 软计数并安排正常用量结算；本地拦截、内容拒绝和上游失败使用各自独立的审计/运维记录语义。
- 响应别名恢复只改协议元数据字段，不能替换正文中恰好相同的字符串。

## 认证与准入

通用 API Key 认证依次执行：

1. 对无效认证滥用和过大 header 做入口限制；拒绝通用网关的 query API Key，接受 `Authorization: Bearer`、`x-api-key`，并为 Gemini 兼容 `x-goog-api-key`。
2. 从认证缓存/仓储加载 Key 以及必要的 User、Group、Team 和复合映射。加载失败按不存在、过载、团队生命周期或内部错误区分。
3. 始终校验 Key 禁用状态、团队 Key 生命周期、成员限额、IP 规则、用户存在与启用状态。
4. 若为复合 Key，从请求模型选中映射并得到本次请求的普通 Key 视图；随后校验选中分组是否可用、用户是否获准使用，并应用 Key 级模型重定向。
5. `simple` 模式在写入认证上下文后跳过正常计费准入，但 `/v1/usage` 仍解析 Key 的结算来源用于准确展示。`standard` 模式按 Key 的 `auto`、`subscription` 或 `balance` 策略解析资金来源，并对消费入口检查 Key 过期/配额、订阅窗口限额或余额；指定订阅不可用、额度不足或不覆盖最终分组时直接拒绝。
6. 写入 API Key、认证主体、角色、分组和可选订阅上下文。路由随后以最终分组的 `allowed_client_protocols` 执行协议准入，再进入 handler；`last_used_at` 更新失败不阻断已认证请求。

`/v1/usage` 及部分批任务管理会跳过消费准入，使额度耗尽或 Key 过期后仍可取回或清理自己的数据；它们仍执行身份、用户、团队、IP 和资源归属检查。用量查询为指定订阅保留其失效状态，不把显示来源改成余额。模型列表虽然对复合 Key 不需要选中一个分组，但仍要执行适用的 Key 额度、余额和订阅检查。

绑定分组的默认组/不可用组回退，以及 Antigravity 等 handler 内的特殊回退，必须在切换后的最终分组上重新检查指定订阅套餐范围和额度。否则认证阶段已验证的原分组会在后续请求中被错误扩大为套餐外分组。

普通 Key 的协议门禁不会读取请求体；复合 Key 的认证阶段会先读取并恢复请求体，以模型前缀确定最终分组，然后才执行同一门禁。禁用协议返回客户端协议原生的 `403`，记录 `LocalPolicyDenied`，不进入账号选择、重试、fallback 或结算。进入协议 handler 后，请求体按端点限制读取并做宽容 JSON/Multipart 处理，再完成用户提示词替换、协议解析、客户端识别、内容审查和 Ops 元数据设置。用户并发槽位在账号选择前获取，避免为已经超过用户并发的请求消耗调度资源。

## 模型名称链

一个请求可能同时存在以下名称：

```text
client_model
  -> composite_actual_model
  -> api_key_redirected_model
  -> channel_mapped_model
  -> account/upstream_model
```

- `client_model` 是客户端原始模型；复合 Key 时保留分组前缀。
- `composite_actual_model` 是选组后去掉前缀的模型。
- Key 级重定向是一跳匹配，发生在选组之后、渠道与账号映射之前。
- 渠道映射同时参与模型限制、计费模型来源和用量映射链；账号映射得到最终供应商路由键。
- `requested_model`、`upstream_model` 和去重后的 `model_mapping_chain` 分别保存客户端意图、实际发送模型和变换路径。

协议 handler 可以在每次 failover attempt 重新基于所选账号构造请求，但不得对已经解析过的一跳映射再次递归。模型列表需要从当前可请求目标反推可展示别名；保存映射时允许目标暂时不可路由，真正请求仍使用标准无账号/无模型错误。

<a id="account_selection_and_failover"></a>
## 账号选择与故障转移

账号选择的输入至少包含本次分组、平台、请求模型、渠道解析结果、会话 hash、已失败账号集合和端点能力。先解析 Claude Code-only 等分组回退，再以最终目标分组的 `scheduler_type` 选择基础或高级调度器；无分组路径固定使用基础调度器。选择器综合以下约束：

- 分组与渠道关联、平台或 force platform、渠道/账号模型限制和模型映射。
- 账号启用、过期、代理、凭据、上游资格、临时不可调度、模型/账号限流及配额状态。
- 调度快照的可用性、粘性会话、优先级/负载、最近使用、并发槽和可等待队列。
- 特殊端点能力，例如图片、Realtime/WS、Grok 付费媒体资格或站点特定模型能力。

候选排序与高级评分不读取上游声明倍率，也没有 `upstream_cost` 权重或 OAuth 参考倍率。账户本地 `rate_multiplier` 与渠道上游计费模型来源仍在账号选定和模型映射完成后参与结算，不作为候选资格或排序信号。

`basic` 保留历史选择路径。`advanced` 在上述硬约束完成后调用通用评分核心，按 Top-K 加权顺序尝试候选并在每次尝试前复核并发槽。有效 Top-K、权重和粘性开关按最终高级分组逐字段合并：分组 `advanced_scheduler_overrides` 优先于网关运行时设置，缺失字段继续使用全局值；空对象等于全部继承。OpenAI/Grok 在这一核心上附加 previous response、订阅、transport、Compact 与额度能力；其它平台只提供各自已存在的候选与硬过滤。运行时只对本次实际走高级模式的选择回写错误率、TTFT 和切换统计，基础请求不会污染高级评分。`count_tokens`、可用性探测等仅选账号入口同样按最终分组决定模式，但使用无槽选择，不占用账号并发槽或会话数量。

选择结果可能已经持有账号并发槽，也可能携带 WaitPlan。后一种情况由 handler 先增加有界等待计数，再在超时内获取账号槽；成功后绑定粘性会话。客户端取消、队列满或等待超时必须释放等待计数和已获取的用户/账号槽。

故障转移只处理 service 明确包装为 `UpstreamFailoverError` 的可切换错误。`FailoverState` 记录切换次数、失败账号和最后错误，并根据账号 pool-mode 重试次数决定同账号重试、排除后选择下一个账号、短暂等待或耗尽。普通同账号重试固定等待 500ms；被标记为请求级瞬时故障的容量错误按 500ms、1s、2s、4s 指数退避，后续单次等待封顶 8s，客户端取消会立即打断等待。临时不可调度标记由 service 根据错误分类写入，不是所有 HTTP 非 2xx 都应封禁账号。

粘性会话已经绑定账号时，切换账号可能要求把普通输入按缓存读取计费，以反映缓存不再命中的成本语义。选择耗尽后的单账号重试和等待有严格上限；客户端 Context 取消必须立即终止，不继续选择或休眠。

## 转发与流式边界

每次 attempt 都以原始/规范化请求和本次账号重新构造供应商请求，注入凭据、代理、TLS 指纹、客户端标识、Thinking/工具配置及上游模型。平台适配器负责协议转换、上游响应限制和供应商错误解析，handler 负责在客户端协议中返回最终结果。

流式响应有不可逆边界：在调用上游前记录 `ResponseWriter` 已写字节数；如果 attempt 已向客户端写出真实业务输出，就不能再选择账号，否则会把两个上游响应拼接为损坏的单流。旧版 Compact 桥接心跳、Responses 的 `response.created` / `response.in_progress` 前导事件，以及等待终态判定的可重试 `error` 帧不算业务输出，可以留在 attempt 缓冲中为 pre-output failover 保留空间；不可重试错误仍按事件边界及时转发。真实输出开始后，错误只能按当前协议追加允许的流错误事件或结束连接。非流式且尚未写响应时，才可以安全地进入下一次 failover。

错误分为本地准入、业务能力不足、调度容量不足、上游可切换错误和不可切换转发错误。协议准入拒绝分别使用 Anthropic `permission_error`、OpenAI `protocol_not_allowed` 和 Google `PERMISSION_DENIED`，且没有所选账号。Ops 采集会记录归属、endpoint、平台、模型和所选账号，但返回客户端的错误不能泄露凭据、内部代理或数据库错误。

## 用量与结算

上游转发产生可计量 usage 后，handler 把解析出的 token/图片/视频用量、客户端与上游模型、endpoint、账号、订阅快照、请求标识和渠道映射交给有界 UsageRecord worker pool。Anthropic 流在终止事件前中断时，若已观测到上游 token，部分 usage 仍与转发错误一起返回并入账；无 token 不生成记录，failover 错误不携带部分结果，避免重试成功后双重计费。OpenAI OAuth 图片响应在 HTTP 成功后若发生上游 body 传输中断，仅在尚未向客户端写出真实图片内容时按 502 进入账号策略和 failover；JSON keepalive 空白不算真实输出，客户端取消、deadline、响应体超限以及首字节后的中断不会换号。worker 使用脱离已结束请求取消信号但受自身超时约束的 Context；队列策略可以同步回退或丢弃，并通过指标/日志暴露压力，不能为每个请求创建无界 goroutine。

标准模式中的共同顺序为：

1. 归一化不同协议的 token 桶、媒体尺寸/时长、缓存和长上下文语义。
2. 根据计费模型来源、渠道价格、账号成本、用户/分组/订阅倍率及高峰倍率计算费用。
3. 使用 `request_id + api_key_id` 认领结算幂等键，并用请求指纹检测 ID 被不同 payload 复用。
4. 在一个 PostgreSQL 事务中锁定付款用户，按结算模式分配订阅或余额，并同步累计团队成员、Key 配额/速率和适用的上游账号额度；已通过准入并完成上游调用的普通请求若跨过指定订阅剩余额度，订阅用量封顶，溢出部分按余额倍率扣入付款主体余额并允许形成欠费。该回退只结算已放行或并发在途的请求，后续绑定已耗尽订阅的新请求仍直接拒绝；批量图片提交前的额度预占仍要求指定订阅完整覆盖。
5. 结算成功后尽力写已结算 Usage Log、更新缓存/最后使用时间并旁路采集数据共享。结算失败时仍写入包含计算成本的待对账 Usage Log，但将 `actual_cost` 置零，随后返回 worker 错误，且不采集数据共享或伪造成功结算；Usage Log 写失败不得导致同一请求重复扣费。

`simple` 模式跳过结算事务，只尽力写 Usage Log 并更新账号最后使用时间。请求热路径已经把响应交给客户端时，后台结算失败无法改写该响应，因此相关错误必须可观测并由幂等重试/对账流程处理。

## 扩展约束

新增网关入口或平台适配器时至少核对：

- 是否应用正确的 body/header 限制、request ID、Ops error logger 和 API Key 错误形状。
- 是否支持普通/复合 Key，模型从何处读取，哪些无模型管理入口只校验资源归属。
- 选组、Key 重定向、渠道映射和账号映射是否保持一跳顺序，模型列表与响应恢复是否同步。
- 用户/账号并发、会话隔离、粘性和取消路径是否能完整释放槽位。
- 哪些错误允许同账号重试或换账号，流开始后是否会错误进入 failover。
- 用量能否得到稳定 request ID、请求指纹、requested/upstream model 和正确平台归属。
- handler、service、repository、前端调用方与 API contract/协议测试是否一起更新。

相关文档：[系统架构](system_architecture.md)、[账号调度与缓存一致性](account_scheduling_and_cache.md)、[网关策略控制](../domains/gateway_policy_controls.md)、[上游账号能力矩阵](../interfaces/upstream_account_matrix.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)、[领域目录](../domains/index.md)、[接口目录](../interfaces/index.md)。
