# 内容审核与风险处置

本文记录 `moderation` 模块的审核模式、决策顺序、规则缓存、自动封禁、上游 Cyber Policy 及无媒体留存边界。上游供应商自带的内容政策不在本文范围内，完整用户内容不写入普通系统日志。

## 章节导航

- [作用域与模式](#作用域与模式)：三种审核模式、提取范围与责任主体。
- [决策流水线](#content_moderation_decision_pipeline)：同步阻断与异步观测的执行顺序。
- [规则与缓存](#规则与缓存)：审核 Key 池、代理隔离与规则缓存策略。
- [风险处置与事务](#风险处置与事务)：自动封禁、Cyber Warning 事务与上游 Cyber Policy。
- [记录与数据保护](#记录与数据保护)：留存边界、`NoMediaRetention` 与客户端安全提示。
- [失败处理与验证](#失败处理与验证)：超时、降级与测试覆盖要点。

## 作用域与模式

| 模式 | 行为 |
| --- | --- |
| `off` | 跳过内容审核 |
| `observe` | 不阻断主请求，异步执行审核并写入观测与风险记录 |
| `pre_block` | 在调用上游与结算前同步审核，命中阻断直接返回安全错误 |

范围与主体：

- 规则支持按分组范围、抽样率（sample rate）及模型 include/exclude 过滤。
- 提取器覆盖 Anthropic Messages、OpenAI Responses、Chat Completions、Gemini 与图片请求，仅提取当前轮用户文本、工具结果和允许的图片输入，不重复审核历史消息。
- 团队 Key 场景下，风险处置主体为实际调用的成员用户，与付款 Owner 分离；监控探测与内部标记请求可按策略跳过审核。

<a id="content_moderation_decision_pipeline"></a>
## 决策流水线

1. 按全局模式、分组、模型、协议和抽样率判定是否进入审核。
2. 提取带长度上限的文本、工具输出与媒体单元，计算预审核 hash。
3. 依次匹配阻断 hash、关键词和缓存，再按 `keyword_only`、`keyword_and_api` 或 `api_only` 决定是否调用外部审核。
4. 从健康 API Key 池按优先级选择凭据，在并发、超时和重试上限内调用外部审核。
5. 按类别阈值生成 `allow`、`block`、`hash_block`、`keyword_block` 或 `error` 结论并落库。
6. `pre_block` 模式在调用上游前拦截；`observe` 模式放行主请求并异步记账。

多单元合并原则：任一单元确认命中阻断即拒绝请求（即使其他单元审核出错）；若无明确阻断但链路不完整，记为 `incomplete` 或 `error`，不视为「已审核且安全」。

## 规则与缓存

- 凭据池与代理：审核 API Key 带优先级、健康状态与连续失败冻结期（到期或健康检查通过后恢复）。外部审核代理引用管理端 Proxy 并短缓存，保存配置立即失效本进程缓存；代理失败直接记审核错误，绝不回退直连暴露源站出口。
- 缓存与顺序：阻断 hash 存 Redis 集合，规则与配置在数据库与进程内快照双存并按 TTL 回源（刷新失败保留旧快照退避重试）。规则匹配严格按列表顺序遍历，不依赖 map 遍历顺序。

## 风险处置与事务

- 自动封禁：在配置窗口内累计用户命中次数达到阈值后自动封禁，解封须管理员手动执行；自动封禁写库失败会触发告警。Cyber Warning 拥有独立计数、阈值与状态，与普通 block 分开。
- 风险记录须携带行为用户、API Key、分组、模型、request ID、命中类别与规则来源；所有敏感管理读取需管理员鉴权并记审计。

<a id="moderation_transactions"></a>
### 处置与事务

- 普通命中：按序执行状态更新、通知与日志写入，不强绑同一事务。
- Cyber Warning：在单 SQL 事务内锁用户、写 warning 与媒体、更新计数并禁用用户；认证缓存失效与邮件通知在提交后执行，失败不回滚已提交处置。

<a id="upstream_cyber_policy"></a>
### 上游 Cyber Policy

- OpenAI WebSocket 的 `error` 和 `response.failed` 事件携带 `cyber_policy` 时，服务层先解析用量并落存风控证据，再走重试/故障转移或返回分支（与 `rate_limit_error` 同现时同样保留证据）。
- WS `AfterTurn` 按该标记执行风控、用量与会话阻断，并在当轮结束后清除标记，避免污染下一轮。
- 会话阻断（`gateway/session.CyberBlocks`）：查找时先查手动会话键，再在来源 scope 内做转录前缀匹配（候选超限时仍返回阻断，不把截断集合当放行依据）。Redis 故障时 fail-open，兼容读取旧单键缓存。

## 记录与数据保护

- 普通 Ops 与系统日志只写元数据、摘要与错误，不写完整 prompt、图片、审核密钥与上游原始响应。
- 无媒体留存（`NoMediaRetention=true`，创作台强制开启）：不对命中媒体做快照，不保存 `input_excerpt` 与正文输入，审核日志仅留输入 hash、类别、分数和决策，详见[创作台](creative_studio.md)。
- 客户端安全提示：仅说明被本地策略拒绝，不泄露关键词、hash、阈值或可用于绕过的规则细节；上游内容政策错误独立记录。
- 停机排空：关闭时先停止新入队，再等待在途异步审核与延迟清理完成，因配置读取失败残留的任务按未完成计数报告。

## 失败处理与验证

- `observe` 模式下队列满、worker 缺失或落库失败不阻断主请求，累计 `dropped` 与 error 健康信号；`pre_block` 受总超时、重试上限与 context 取消约束。
- 代理不可用算外部审核错误并按当前模式处理，不绕过代理直连；配置或缓存应用失败如实返回给管理端。
- 回归测试至少覆盖：`off`/`observe`/`pre_block`、关键词/hash/API 组合、include/exclude、部分失败仍阻断、团队行为用户归属、缓存失效、日志脱敏与无媒体留存。

相关文档：[网关策略控制](gateway_policy_controls.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)、[可观测性与数据生命周期](../operations/observability_and_data_lifecycle.md)。
