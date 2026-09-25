# 用户平台额度

本文描述用户在九个平台上的日、周、月 USD 额度，从注册默认值、请求预检查、成功结算到 Redis/数据库同步的完整语义。它不描述上游账号套餐额度、订阅计划窗口或 API Key 自身限速。

## 章节导航

- [额度模型](#额度模型)：理解用户、平台和窗口的唯一关系。
- [请求准入](#请求准入)：修改预检查、错误码或重置时间时读取。
- [结算与刷写](#结算与刷写)：修改用量累加、Redis 或数据库镜像时读取。
- [默认值与管理操作](#默认值与管理操作)：修改注册快照、覆盖或窗口重置时读取。
- [一致性与诊断](#一致性与诊断)：排查显示偏差或超额窗口时读取。

## 额度模型

每个用户和平台最多一条 `user_platform_quotas` 活跃记录。允许的平台为 Anthropic、OpenAI、Gemini、Antigravity、Grok、Qoder、Kimi、Zhipu 和 DeepSeek。每条记录分别保存日/周/月 limit、usage 和 window start。国产供应商的 payg/coding 只是上游账号模式，用户额度仍按 Kimi、Zhipu 或 DeepSeek 平台归属，不拆成额外平台。

limit 区分三种值：

- `nil`：该窗口无限额。
- `0`：完全禁用；因 usage 从 0 开始，任何请求都会达到上限。
- 正数：以 USD 计价的上限，预检查在 `usage >= limit` 时拒绝。

日/周窗口使用站点配置时区的日首和周首，app 将同一 Calendar 注入预检、管理展示及 PostgreSQL 累加存储；月窗口是从持久化起点开始的滚动 `30 * 24h`，不是自然月。窗口过期时预检查先把本次判断中的 usage 视为 0，月窗口起点按当前时间重置，随后刷新缓存或由数据库累加路径自愈。

## 请求准入

用户平台额度只在 `standard` 的余额结算路径生效；仍由有效订阅承接的请求豁免。订阅额度耗尽并回退余额后，重新进入平台额度检查。API Key 限速和用户/分组 RPM 在其后继续独立生效。

管理规则、准入、倍率和镜像生命周期由 billing 持有；PostgreSQL 与 Redis Adapter 分别位于 billing/postgres、billing/rediscache。

检查采用 Redis-first：当前 schema 的缓存命中直接判断；MISS 或旧缓存用 singleflight 合并数据库回源并回填。Redis 故障时仍查询数据库做一次性检查；数据库也失败或请求上下文取消时，预检查放行并记录 warning，避免额度基础设施故障阻断全部网关流量。

额度耗尽返回 HTTP 429 和日/周/月专用错误码，并附 `window_resets_at`。客户端 `Retry-After` 从相同窗口口径计算，不能使用服务器 UTC 日界替代配置时区。

<a id="platform_quota_settlement_and_flush"></a>
## 结算与刷写

只有请求成功结算、平台在 allowlist、用户存在且该平台至少配置一个 limit 时才累加。累加金额是本次实际由余额承担的 USD 金额；纯订阅金额不计入。请求模型、上游模型和账号上游套餐都不能改变 `QuotaPlatform` 的平台归属。

Redis 用 Lua 同步累加三个窗口并标记脏 key，使下一次预检查立即看到新 usage，把并发超额范围限制在 in-flight 请求内。数据库有两种持久化模式：

- Flusher 关闭：成功结算后异步调用 `IncrementUsageWithReset`，数据库原子重置过期窗口并累加。
- Flusher 开启：Redis 为执法热状态，dirty set 由 `UserPlatformQuotaUsageFlusher` 分批读取绝对快照并 UPSERT 到数据库镜像。

app 只构造一个按用户协调器。管理替换/重置、singleflight 回源与回填、跨窗口刷新、累计和 flusher 共用它。Flusher 可以先 Pop dirty key，但必须按用户 ID 升序取得锁后才读取 Redis，持锁到整批 SQL 写回结束，再逆序释放；锁等待计入操作预算，取消不能绕过锁写旧值。管理锁覆盖必要读取、数据库提交及缓存失效。

Flusher 每批 Pop dirty key、批量读取 Redis、写入绝对 usage/window snapshot。普通写失败会把 key 加回 dirty set；外键违反表示用户已删除，整批数据库镜像可能暂时缺失，但 Redis 执法状态不受影响。停止服务时先取消并等待周期回调，再在清理预算内继续写回积压，不受周期单轮 16 批上限限制；失败仍按原规则回填并报告未排空。应用在停止计费写入及其异步副作用后才推进到该步骤。Flusher 是异步镜像优化，不改变 Redis 预检查和成功结算的先后关系。

重置失效缓存后，已放行请求的累计会在锁内重新读取并回填最新窗口，再调用 Lua，避免缺失 key 静默吞掉新用量。有效缓存命中不增加数据库查询。Flusher 关闭时，进程内锁保持到该次异步数据库增量结束，管理重置不会插入 Redis 累计与 SQL 镜像之间。

## 默认值与管理操作

注册时把全局默认和认证来源默认合并为用户快照；后续修改站点默认值不追溯覆盖已有用户。管理员读取、全量替换一个用户的九平台配置，或显式重置某个平台的指定窗口。更新与重置后要失效所有相关 Redis entry；失效失败意味着新 limit/reset 最长可能延迟到缓存 TTL，应产生告警。迁移只扩展数据库平台约束，不为已有用户回填 CN 行；缺失行继续沿用既有无限额语义，只有显式管理更新才创建对应记录。

管理更新不能用缺失字段表达 `0`：JSON `null` 是无限额，显式数值 `0` 是禁用。全量替换需要审计新增、修改和移除的平台，避免未提交的平台静默保留旧限制。

## 一致性与诊断

- Redis usage 是当前执法视图，数据库 usage 在异步模式下可能滞后；管理端展示要注明读取来源或刷新状态。
- Flusher 写绝对值而非 delta，重试不会重复累加；dirty key 丢失可由后续活跃请求重新标记，但低活跃用户的数据库镜像可能长期偏低。
- 管理重置、flusher、回源与累计在单实例内共用用户锁，防止旧快照覆盖新窗口。该锁不协调多个服务进程，也不把 Redis 和 PostgreSQL 变成原子提交；Redis 失效或写回故障仍可能造成 TTL 范围内的滞后，应结合告警核对。
- 用户平台额度与账号上游 quota、订阅窗口、团队成员限额、Key 5h/1d/7d 限额分别排查，不能只看一个 “quota exhausted”。

相关文档：[路由与结算](routing_and_billing.md)、[支付与权益](payments_and_entitlements.md)、[账号维护](../operations/account_maintenance.md)。
