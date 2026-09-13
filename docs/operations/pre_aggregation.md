# 使用记录与运维预聚合

本文说明 TokenRouter 如何生成 Usage 与 Ops 预聚合数据、组合聚合表和原始表回答查询，并在覆盖不足或查询失败时降级。修改聚合任务、Usage/仪表盘查询、运维趋势、时间桶、历史回填、清理或备份范围前应先读本文。

预聚合只是一层可重建的读取优化，不参与余额、订阅、额度或账单结算。计费事实仍以结算事务和账本表为准；查询结果异常不能通过直接修改聚合表修复资金状态。

## 章节导航

- [控制面与管理接口](#控制面与管理接口)：修改部署能力、运行时开关或管理端设置时读取。
- [使用记录聚合](#使用记录聚合)：修改多维用量表、水位或历史回填时读取。
- [运维聚合](#运维聚合)：修改运维小时桶、日桶、任务锁或 heartbeat 时读取。
- [查询路由与透明降级](#query_routing_and_fallback)：修改聚合/原始表组合、覆盖判断或强制 raw 场景时读取。
- [一致性、清理与备份](#一致性清理与备份)：删除原始记录、调整 retention 或恢复历史数据时读取。
- [故障定位](#故障定位)：页面变慢、数据为空或聚合状态报错时读取。

<a id="preaggregation_control"></a>
## 控制面与管理接口

`settings/preaggregation` 拥有唯一控制器，app 向 usage 与 ops 注入各自只读端口；部署 Options 由 app 投影。数据库运行时配置仍只有 `pre_aggregation_settings` 一个设置键：

```json
{
  "usage": {
    "enabled": true,
    "interval_seconds": 60
  },
  "ops": {
    "enabled": true
  }
}
```

- `usage.enabled` 控制 Usage 定时聚合及支持该聚合的数据查询路由。
- `usage.interval_seconds` 允许 30 至 3600 秒。设置服务缓存 15 秒；本实例更新后立即通知任务，多实例通过缓存失效最终看到新值。
- `ops.enabled` 控制 Ops 小时/日聚合及运维查询的自动数据源选择。
- 设置不存在时使用部署配置默认值。读取数据库设置失败时，热路径优先沿用缓存，否则使用部署默认值并记录错误。

部署配置定义不可被运行时设置绕过的能力上限：

```yaml
dashboard_aggregation:
  enabled: true
  interval_seconds: 60
  lookback_seconds: 120
  backfill_enabled: false
  backfill_max_days: 31
  retention:
    usage_logs_days: 90
    usage_billing_dedup_days: 365
    hourly_days: 180
    daily_days: 730

ops:
  enabled: true
  aggregation:
    enabled: true
```

`dashboard_aggregation.enabled=false` 会强制关闭 Usage 周期聚合；`ops.enabled=false` 或 `ops.aggregation.enabled=false` 会强制关闭 Ops 聚合。数据库设置 `ops_monitoring_enabled` 也会阻止 Ops 聚合作业。修改部署能力需要重启，日常启停通过管理端统一预聚合设置完成。

管理员接口注册在 `/api/v1/admin/settings` 下：

- `GET /pre-aggregation` 返回规范化设置、部署能力以及 `usage_status`、`ops_status`。
- `PUT /pre-aggregation` 完整替换 Usage 与 Ops 运行时设置，不执行局部合并。
- `POST /pre-aggregation/backfill` 接受 `{"days": N}`，异步请求最近 N 天的 Usage 回填；必须同时满足部署允许回填、Usage 已启用和最大天数限制。

回填接口返回 `202 Accepted` 只表示目标和游标已持久化并唤醒后台任务，不表示历史数据已经完成。Ops 当前没有对应的手工历史回填接口。

## 使用记录聚合

迁移 `229_usage_analytics_rollups.sql` 只创建空表、索引和单行状态，不扫描或回填 `usage_logs`；`230_pre_aggregation_manual_backfill.sql` 为状态表增加手工回填目标和游标。`232_reset_usage_analytics_model_dimension.sql` 在请求模型维度切换为内部模型时清空可重建桶并重置覆盖状态，不扫描或改写原始使用记录。核心表为：

- `usage_analytics_hourly`：以 UTC 小时为桶的多维明细。
- `usage_analytics_daily`：从小时表重建的 UTC 日桶。
- `usage_analytics_aggregation_state`：保存实时水位、连续覆盖起点、原始数据起点、自动/手工回填游标和最近任务状态。

聚合维度包括用户、计费用户、团队、API Key、分组、内部请求模型、请求类型、流式标记、计费类型、计费模式、有效平台和入站端点。内部请求模型已移除复合 Key 前缀并完成 Key 级重定向，表中的遗留列名仍为 `requested_model`；原始客户端模型只保存在 `usage_logs` 明细中。指标包括请求数、各类 Token、总费用、实际费用、账号费用及请求耗时。上游账号、request ID、上游模型和模型映射结果等未进入表的维度不能由这组 rollup 回答。

实时任务按运行时周期执行，并用 `lookback_seconds` 重算水位附近的小时范围以吸收迟到记录。每轮先刷新小时表；实际回看范围触及已闭合 UTC 日期时，再从小时表重建对应日表。新实例不会在第一次实时任务中扫描全部历史，历史范围由反向回填逐步覆盖。

多实例先尝试 Redis leader lock，Redis 不可用时回退 PostgreSQL advisory lock。同一实例还用原子运行标记阻止任务重入；取得锁失败时跳过本轮，不并发写同一水位。

<a id="usage_state_ownership"></a>
### 历史回填

自动回填从新到旧按 UTC 小时推进：

- 最多每分钟启动一轮，每轮最多处理 24 个小时块。
- 每轮数据库工作预算为 10 秒；完成至少一个小时后预算不足会保存游标并保持 `backfill`，不记为错误。
- 首个小时耗尽完整预算仍未完成时进入 `error`。
- 开始下一块前参考上一块耗时，剩余预算不足时提前停止。
- 小时块完成后立即保存游标；跨过完整 UTC 日期前必须先成功重建对应日表。
- 失败、进程重启或 leader 切换后从持久化游标继续，重复 UPSERT 保持幂等。

手工回填使用独立目标和游标，只重算管理员要求的最近 N 天，但复用同一把分布式锁、运行标记和预算。状态写入按字段所有权分开：手工请求只修改目标与游标，实时任务更新其水位和结果；后台手工游标推进/清除须匹配读到的目标与游标，不能覆盖后来提交的手工请求。PostgreSQL 用短事务锁定单行状态，聚合 SQL 执行期间不持有这把行锁，不依赖新增版本列。完成后自动历史回填从原覆盖位置继续。

Usage 状态的主要字段为：

- `phase`：`disabled`、`idle`、`live`、`backfill`、`error` 或 `unavailable`。
- `live_watermark`、`lag_seconds`：实时数据已处理位置及其延迟。
- `coverage_start`、`source_oldest_at`：连续聚合覆盖起点与原始数据最早时间。
- `last_run_at`、`last_success_at`、`last_error_at`、`last_duration_ms`、`last_error`：最近执行结果。

正常情况下水位延迟应接近运行周期，`coverage_start` 会随自动回填向 `source_oldest_at` 移动。只看 `phase=idle` 不能证明目标查询范围已完全覆盖。

## 运维聚合

Ops 使用 `ops_metrics_hourly` 和 `ops_metrics_daily`。当前小时表按全局、平台以及平台+分组三种粒度保存成功数、错误分类、Token、请求耗时和首 Token 延迟；任务会为无流量小时写入全局零值桶，使覆盖检查能区分“确实无数据”和“尚未聚合”。日表由小时表滚动生成。

小时任务启动时立即运行，之后每 10 分钟运行一次，只重复计算减去 5 分钟安全延迟后的最近稳定 UTC 小时，以吸收该窗口内的迟到记录。日任务启动时立即运行，之后每小时运行一次，只生成最近闭合的 UTC 日期。当前任务不会自动向更早的 Ops 历史执行大范围回填。两类任务都在 Redis leader lock 失败时尝试 PostgreSQL advisory lock，并通过 `ops_job_heartbeats` 保存成功、错误、耗时和窗口。

Ops 聚合使用生成桶时读取到的忽略状态码计算 SLA 和错误分类。当前查询路径主要读取小时表；日表仍由任务维护并进入清理和备份范围，不能据此假定所有长窗口查询已经切换到日表。

Ops 状态来自小时任务 heartbeat，主要 phase 为 `disabled`、`pending`、`idle`、`error` 或 `unavailable`。它没有 Usage 的连续历史覆盖游标；能否走聚合仍由查询时对目标完整小时逐桶检查。

<a id="query_routing_and_fallback"></a>
## 查询路由与透明降级

所有时间范围采用半开区间 `[start, end)`，防止日表、小时表和原始表在边界重复计数。

### Usage 查询

支持的查询把数据源拆为：

1. `coverage_start` 之前的头部从 `usage_logs` 读取。
2. 连续覆盖内的完整 UTC 日从 `usage_analytics_daily` 读取。
3. 聚合区间的非完整日边界从 `usage_analytics_hourly` 读取。
4. `live_watermark` 之后或历史结束时间不足一小时的尾部从 `usage_logs` 读取。

趋势查询先组合 UTC 桶，再按配置时区重新分桶，因此可处理非 UTC 时区和夏令时切换。最近五分钟 RPM/TPM 始终读取原始记录，避免聚合延迟污染实时指标。

管理端分组列表的今日、昨日和累计费用也复用这套组合源：完整 UTC 桶读取 `usage_analytics_hourly/daily`，两侧不完整部分读取 `usage_logs`，自然日边界取服务端配置时区。它不另建面向分组的写入触发器或独立日桶，避免在高频 Usage 写入路径增加锁竞争。

管理员用户消费排行和 Top 用户趋势无论由组合聚合源还是原始表回答，都先按 `billing_user_id` 汇总，再在查询时关联 `users` 表读取当前 `username` 与 `email`。团队 Key 的 `user_id` 仍保留实际行为成员，但其消费、Token 和请求统计归到付款主体（团队 Owner）；历史缺失付款主体的原始记录回退到 `user_id`。身份字段不固化进可重建聚合表。管理端依次用非空用户名、非空邮箱和付款主体 ID 作为排行/趋势标签。

以下场景直接使用原始表，不视为故障：聚合关闭、覆盖区间无法形成完整小时、账号或 request ID 过滤、按非请求模型维度筛选，以及目标统计未实现聚合版本。聚合 SQL 真正失败时调用方透明重试原始查询，并按操作把告警限制为每分钟一次；回退不会把部分聚合结果与完整原始结果再次相加。

### Ops 查询

外部运维接口不接收数据源模式，由后端统一设置解析为 `auto` 或内部 `raw`：

- `auto` 对稳定完整小时读取 `ops_metrics_hourly`，窗口两侧不完整片段读取原始 `usage_logs`/`ops_error_logs`。
- 任一目标小时缺少覆盖、聚合行异常或聚合查询报错时，服务用原筛选条件重新执行完整 raw 查询。
- 自定义忽略状态码没有作为聚合维度保存，因此强制 raw，保证设置立即生效。
- 实时流量、告警计算等需要即时或严格原始语义的内部调用显式指定 raw。
- 聚合功能关闭时统一解析为 raw；调用方不能通过请求参数绕过运行时策略。

Raw 运维查询本身仍有超时和降级规则。例如耗时分位数或峰值子查询超时后，仪表盘可用平均/当前值构造降级摘要；这与“预聚合失败后重试 raw”是两个不同层次的降级。

## 一致性、清理与备份

- 管理员 Usage 清理任务已经删除原始记录时，成功、取消或后续失败均在收尾登记异步重算。这个内部一致性修复不依赖 `backfill_enabled` 或运行时 Usage 开关，也不推进正常水位；它使用聚合器运行 context，而不是已取消的清理任务 context。任务仍保留 canceled/failed 状态，不提供新的持久投递或崩溃恢复保证。
- Usage 定时任务每六小时检查 retention，清理 Usage 小时/日聚合和原始 `usage_logs`，并调用 billing 的去重归档能力按原 SQL 先归档再删除 `usage_billing_dedup`。关闭部署层 Usage 聚合会停止该任务，也会停止由它承载的周期清理。
- Ops cleanup 按自身设置清理 `ops_metrics_hourly` 和 `ops_metrics_daily`；它与统一预聚合开关不是同一个生命周期控制面。
- Usage 与 Ops 聚合表都属于备份的可选数据组。默认备份策略可能排除这些表的数据而只保留结构；恢复后必须重新检查水位、覆盖桶和 heartbeat，不能直接信任恢复前状态。
- 聚合表可以从仍保留的原始数据重建。若原始 retention 已越过缺口，回填无法恢复该时间范围，报表只能接受缺失或从独立备份恢复。

聚合器在 Start 后持有独立运行 context。Stop 取消支持 context 的在途 SQL 和重试等待，禁止新重算并等待正在执行的工作；等待受应用剩余预算限制，超时只报告未完成，不伪造 drain 成功。

不要手工推进 watermark、coverage 或 heartbeat 来隐藏任务故障；这些字段参与查询路由，伪造完成状态可能让查询读取不完整聚合结果。

## 故障定位

页面为空或变慢时按以下顺序判断：

1. 检查部署 hard switch、`pre_aggregation_settings` 和 `ops_monitoring_enabled`，区分不可用、关闭和读取设置失败。
2. 对 Usage 比较 `live_watermark`、`coverage_start`、查询起止时间和 `source_oldest_at`；对 Ops 检查小时任务 heartbeat 与目标小时零值桶是否连续。
3. 查看 `last_error`、任务耗时、leader lock 和数据库 statement timeout；多实例中确认只有一个 leader 在执行。
4. 用同一半开时间范围和同一筛选条件比较聚合路径与 raw 路径；不要混用本地日期和 UTC 桶边界。
5. 检查 retention 或主动清理是否已经删除原始来源，再决定重算还是恢复备份。

新增聚合维度时必须同时修改表唯一键、小时/日 UPSERT、查询构造器、覆盖测试、清理/备份表组和迁移；只改查询 DTO 会导致请求静默回退 raw，或更严重地返回被错误合并的数据。

相关文档：[可观测性与数据生命周期](observability_and_data_lifecycle.md)、[路由与计费](../domains/routing_and_billing.md)、[配置边界](../interfaces/configuration.md)、[部署与数据库迁移](deployment_and_migrations.md)、[运维目录](index.md)。
