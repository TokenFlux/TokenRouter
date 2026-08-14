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

## 额度与能力探测

平台可维护独立的上游额度快照：OpenAI/Codex 窗口、Gemini tier/model quota、Antigravity credits、Grok 计费/媒体资格、Qoder Credits 等。快照用于调度、容量展示和诊断，不是 TokenRouter 用户余额或订阅账本。

OpenAI 重置次数查询把带到期时间的完整结果保存为账号展示快照；上游只返回正数次数却缺少到期明细时，实时结果仍返回给调用方，但旧快照必须保留。直接调用重置 API 成功消费次数后，服务先在脱离客户端取消信号的有界上下文中恢复账号 error、限流和临时不可调度状态，再回读额度快照与最新账号投影；恢复不修改人工 `schedulable` 开关。后续步骤部分失败时响应使用 `cache_refreshed`、`account_state_recovered` 和 `warning_code` 明确区分，调用方不得把已消费的次数当作可重试失败。

OpenAI API Key 可探测 Responses、Chat、Embeddings 等 endpoint capability。Responses 探测只有在响应足以下结论时才写入能力标记：2xx 响应若仍因 `max_output_tokens` 未完成，或响应状态为 `failed`，应保持 unknown，不能持久化为“不支持 Responses”；完成但没有 `function_call` 的响应仍判定为不支持。Ollama Cloud 等兼容 API Key 上游可以保存其管理会话和用量快照，但只有明确匹配的账号才进入探测，不能把探测协议推广到所有 `apikey`。Grok 计费与媒体资格、各平台额度探测也继续使用各自独立协议。

通用的上游声明倍率探测已移除，不再有定时任务、手动操作、快照或公开账单自省接口。账号创建、编辑、批量更新、复制、CRS 同步和仓储写入都会丢弃历史 `upstream_billing_probe` 与 `upstream_billing_probe_enabled` 键；这项清理不得影响 Ollama Cloud 会话/用量、endpoint capability 或其它额度状态。

实时探测失败时保留最近成功快照并同时暴露当前错误，不把旧数据标为实时。任何配额耗尽或 capability 变化都要触发相关调度投影失效。

## 运维诊断

- 观察每 provider 的候选数、刷新成功/失败、节流、超时和最长积压，而不只看总成功率。
- 关联账号测试、刷新、quota probe、代理健康和调度过滤原因，区分凭据故障与出站网络故障。
- 检查账号数据库状态、当前进程投影和跨实例失效是否一致；手工改数据库后等待周期重建不等于即时生效。
- 自动恢复或批量导入后抽查实际协议，避免仅凭 token endpoint 成功误判推理可用。

相关文档：[上游账号能力矩阵](../interfaces/upstream_account_matrix.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[上游传输安全](upstream_transport_security.md)。
