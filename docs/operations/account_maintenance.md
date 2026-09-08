# 账号维护

本文描述上游账号凭据刷新、健康测试、配额与能力探测、临时不可调度和自动恢复的后台流程。它不定义创建表单字段或请求内调度评分；这些由平台专题与调度架构拥有。

## 章节导航

- [凭据刷新](#凭据刷新)：修改 refresh 候选、并发、重试或状态同步时读取。
- [状态与临时不可调度](#状态与临时不可调度)：修改错误、限流或恢复时间时读取。
- [账号测试与自动恢复](#账号测试与自动恢复)：修改定时测试或恢复条件时读取。
- [额度与能力探测](#额度与能力探测)：修改上游 usage、quota 或 endpoint capability 时读取。
- [运维诊断](#运维诊断)：排查刷新堆积、误封禁或账号抖动时读取。

<a id="account_credential_refresh"></a>
## 凭据刷新

`TokenRefreshService` 分页读取需要维护的账号，按平台 refresher 判断资格，并对每个 provider 应用独立并发/QPS 门槛、单次 attempt 超时、周期总超时和有界退避。OAuth、Setup Token 和 Qoder COSY 的候选规则不同；API Key、Bedrock 和 Service Account 通常由各自请求路径或签名 provider 管理，不应统一假设有 refresh token。

刷新成功后要原子更新凭据/过期时间，清理可恢复错误，并同步账号缓存与调度快照。OpenAI/Antigravity 还可在刷新后确保 privacy 状态。刷新失败按失败阈值记录，不立即把一次瞬时网络错误等同于永久禁用；凭据明确撤销或账号归属失效时才进入需要重新授权的状态。

请求路径 token provider 仍会在使用前检查过期偏移，并用账号级锁避免并发刷新。后台刷新降低热路径延迟，但不是唯一正确性来源；两条路径必须使用相同的凭据版本/CAS 保护，避免旧请求覆盖新 token。

## 状态与临时不可调度

账号长期状态、`schedulable`、全账号限流、模型限流和临时不可调度规则是不同层次：

- 凭据/配置错误可以记录 recoverable error 并要求人工重新授权。
- 429、明确 reset time 或短期网络/供应商故障使用恢复时间，在到期前过滤账号或模型。
- 管理员策略和代理过期可能临时移出调度，但不删除账号。
- 账号到期可由维护任务自动暂停；重新启用前仍需验证凭据和关联资源。

状态写入要携带凭据快照或版本条件。较早请求不能在新凭据生效后再次设置旧错误；恢复同样不能清除另一个请求刚确认的永久错误。

## 账号测试与自动恢复

管理端即时测试和 `scheduled-test-plans` 使用平台测试服务调用真实凭据/模型，并保存测试结果。计划使用分钟级 cron 表达式；每个计划可配置自动恢复。成功测试可以清除符合条件的 error、rate limit、temporary unschedulable 和模型限流，但不能绕过管理员禁用、账号过期或类型不匹配。

测试本身应使用受控超时、代理/TLS 路由和脱敏日志。一个模型测试成功只证明该路径当时可用，不证明所有 endpoint capability 或媒体资格。失败结果需区分认证、模型、配额、代理、TLS 和上游容量，以免自动恢复形成启停抖动。

Kimi、Zhipu、DeepSeek 的连接测试按账号 `api_protocol` 选择原生 Anthropic Messages、OpenAI Chat Completions 或 DeepSeek Responses 端点，不得始终假设 Chat 形状。测试复用账号自定义 Base URL、代理、TLS 指纹和受保护 Header Override；Anthropic 协议的自定义中继在模型同步等 OpenAI 格式请求中只移除末尾 `/anthropic`，不能改回官方 host 或丢弃此前的路径前缀。

管理端连接测试请求必须显式选择 `test_type=text|image` 并传入同一字段的自定义 `prompt`。文字测试不再因为模型名称包含图片标记而切换端点；图片测试也不再依赖模型名称命中规则，而是由 OpenAI、Gemini 或 Grok 账号的平台图片端点执行。OpenAI 的 `compact` 与 `legacy_compact` 仅执行固定载荷的连接测试，不显示或使用自定义提示词。未携带 `test_type` 的历史调用才允许回退到旧模型名判断。图片和文字的结果分别通过 SSE 图片事件和内容事件返回；不支持图片端点的平台应直接返回可诊断的错误，不得静默改成文字测试。

## 额度与能力探测

平台可维护独立的上游额度快照：OpenAI/Codex 窗口、Gemini tier/model quota、Antigravity credits、Grok 计费/媒体资格、Qoder Credits，以及 Kimi/Zhipu/DeepSeek 的统一用量监控快照等。快照用于调度、容量展示和诊断，不是 TokenRouter 用户余额或订阅账本。

OpenAI 重置次数查询把带到期时间的完整结果保存为账号展示快照；上游只返回正数次数却缺少到期明细时，实时结果仍返回给调用方，但旧快照必须保留。直接调用重置 API 成功消费次数后，服务先在脱离客户端取消信号的有界上下文中恢复账号 error、限流和临时不可调度状态，再回读额度快照与最新账号投影；恢复不修改人工 `schedulable` 开关。后续步骤部分失败时响应使用 `cache_refreshed`、`account_state_recovered` 和 `warning_code` 明确区分，调用方不得把已消费的次数当作可重试失败。

OpenAI API Key 不再自动探测 Responses 能力；创建、编辑、批量更新和复制均以管理员选择的上游协议为准，历史探测字段被清理且不参与调度。两类压缩也由独立管理员开关决定，手动连接测试不更新能力配置，但仍保留额度观测、401 认证错误记录与 429 限流处理。账号与 OAuth 导入模板共用历史输入清理边界，模板读取也不返回旧探测状态或自动模式。API Key 文字测试可显式选择 Responses 或 Chat Completions，OAuth 仍使用 Codex Responses。HTTP continuation 为独立开关，缺失时关闭。

调度投影必须保留工作负载、文本路由、两类压缩开关与 continuation 设置，配置变化沿用账号投影失效机制。国产供应商继续按其 `api_protocol` 做无网络路由配置同步。Grok 计费/媒体资格、Ollama Cloud 与各平台额度探测保持各自独立流程。

通用的上游声明倍率探测已移除，不再有定时任务、手动操作、快照或公开账单自省接口。账号创建、编辑、批量更新、复制、CRS 同步和仓储写入都会丢弃历史 `upstream_billing_probe` 与 `upstream_billing_probe_enabled` 键；这项清理不得影响 Ollama Cloud 会话/用量、endpoint capability 或其它额度状态。

实时探测失败时保留最近成功快照并同时暴露当前错误，不把旧数据标为实时。任何配额耗尽或 capability 变化都要触发相关调度投影失效。

## API Key 上游用量查询

API Key 上游用量由独立的 `UpstreamUsageService` 提供，和 OAuth/Setup Token 的 `AccountUsageService` 语义分离。它只服务管理员展示，不参与调度、自动暂停、倍率、本地配额或结算；列表加载、滚动和自动刷新都不会产生上游流量。管理员手动查询时，服务按账号和规范化配置指纹合并并发请求，单次约 60 秒超时、512 KiB 响应体上限、禁止重定向，并复用代理、TLS 指纹、Header Override 和既有 `HTTPUpstream`。

配置缺失时，普通 API Key 默认启用 Sub2API；New API 和 Zivv 必须显式选择对应适配器。CN API Key 则按平台/mode 自动选择 Kimi 余额、Kimi Coding、Zhipu Coding 或 DeepSeek 多币种余额适配器，Zhipu payg 明确不支持。Sub2API 的钱包负余额可以展示，`remaining=-1` 只在适配器内部转换为 `unlimited=true`。New API 用 `/api/usage/token/` 读取 Key 配额，再通过固定的钱包端点或受保护的用户访问令牌查询用户钱包；只有钱包结果进入 `balance`，Token 配额进入 `limits`/`subscription`。Zivv 用 `/v1/user/balance` 同时读取钱包、累计用量、Key 限额和套餐，`key_limit=0` 显示为不限量。`unlimited_quota=true` 时忽略上游可能溢出的额度字段，状态接口失败时使用默认单位比例；官方实例未配置用户访问令牌且不提供 API Key 钱包端点时返回 `UPSTREAM_USAGE_WALLET_UNAVAILABLE`，不把 Token quota 当钱包余额。手动查询失败不修改账号状态或运行快照，也不自动回退到另一个协议。

浏览器只缓存成功的归一化结果五分钟，缓存键隔离管理员身份、账号 `updated_at`、代理/Base URL 和配置；失败不缓存，账号凭据、代理或配置变化立即失效。审计仅记录管理员动作和脱敏元数据，不记录 API Key 或上游原始响应。该功能与已移除的 `upstream_billing_probe` 完全不同，不恢复旧的自动倍率探测。

CN 周期监控默认关闭；启用后只把统一快照写入 `extra.cn_usage_monitor_snapshot`，用身份 hash 和账号 `updated_at` CAS 防止旧探测覆盖新凭据。失败保留最近成功结果，余额低于阈值只写带同一身份 hash 的临时不可调度原因，恢复也只清理由该身份创建的状态。多实例同轮由 leader lock 串行化，自定义中继必须命中启用的 URL allowlist。详细字段、适配器和超时语义见[API Key 上游用量查询](../interfaces/upstream_usage.md)。

## 运维诊断

- 观察每 provider 的候选数、刷新成功/失败、节流、超时和最长积压，而不只看总成功率。
- 关联账号测试、刷新、quota probe、代理健康和调度过滤原因，区分凭据故障与出站网络故障。
- 检查账号数据库状态、当前进程投影和跨实例失效是否一致；手工改数据库后等待周期重建不等于即时生效。
- 自动恢复或批量导入后抽查实际协议，避免仅凭 token endpoint 成功误判推理可用。
- OpenAI Chat 排障应同时核对入站协议、`openai_text_route_mode` 和 Usage Log 的 `upstream_endpoint`；默认模式应记录 `/v1/chat/completions`，不能因历史探测状态改变上游协议。

相关文档：[上游账号能力矩阵](../interfaces/upstream_account_matrix.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[上游传输安全](upstream_transport_security.md)。
