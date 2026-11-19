# 使用记录与运维预聚合

本文记录 Usage 与 Ops 预聚合数据的生成、组合查询路由、透明降级、历史回填及清理规则。预聚合属于可重建查询数据，不参与余额、订阅、额度或账单结算；计费事实始终以结算事务和账本表为准。

## 章节导航

- [控制面与管理接口](#preaggregation_control)：部署硬开关、运行时设置与回填 API。
- [使用记录聚合](#使用记录聚合)：Usage 小时/日表维度、实时推进与历史回填。
- [运维聚合](#运维聚合)：Ops 小时/日桶调度、零值桶与 heartbeat。
- [查询路由与透明降级](#query_routing_and_fallback)：组合数据源拆分规则与强制 raw 场景。
- [一致性、清理与备份](#一致性清理与备份)：删除重算、retention 清理与恢复边界。
- [故障定位](#故障定位)：页面为空或变慢时的排查顺序。

<a id="preaggregation_control"></a>
## 控制面与管理接口

数据库运行时配置保存在单键 `pre_aggregation_settings` 中（缓存 15 秒，更新后立即通知后台任务；读取失败时热路径沿用缓存，否则用部署默认值）：

```json
{
  "usage": { "enabled": true, "interval_seconds": 60 },
  "ops": { "enabled": true }
}
```

- `usage.interval_seconds` 范围 30 到 3600 秒。
- 部署配置定义不可被运行时设置绕过的硬上限（修改后需重启）：`dashboard_aggregation`（含 `enabled`、`interval_seconds`、`lookback_seconds`、`backfill_enabled`、`backfill_max_days` 与 `retention`）和 `ops.enabled` / `ops.aggregation.enabled`。另外数据库开关 `ops_monitoring_enabled` 关闭也会阻止 Ops 聚合。
- 管理接口位于 `/api/v1/admin/settings`：
  - `GET /pre-aggregation`：返回规范化设置、部署能力及 `usage_status`、`ops_status`。
  - `PUT /pre-aggregation`：完整替换 Usage 与 Ops 运行时设置（不局部合并）。
  - `POST /pre-aggregation/backfill`：提交 `{"days": N}` 异步回填最近 N 天 Usage，返回 `202 Accepted`（需部署开启回填、Usage 启用且不超最大天数）；Ops 暂无手工回填接口。

## 使用记录聚合

核心表结构：

| 表名 | 职责 |
| --- | --- |
| `usage_analytics_hourly` | UTC 小时粒度的多维汇总明细 |
| `usage_analytics_daily` | 从小时表滚动重建的 UTC 日桶 |
| `usage_analytics_aggregation_state` | 单行状态：实时水位、连续覆盖起点、原始数据起点、自动/手工回填游标及最近结果 |

维度与口径约定：

- 聚合维度包含用户、计费用户、团队、API Key、分组、内部请求模型（已去复合前缀并经 Key 重定向，列名沿用 `requested_model`）、请求类型、流式标记、计费类型、计费模式、平台快照、入站端点与请求来源（用户/系统）。系统归属在多维桶用零标记；原始客户端模型、上游提供商、request ID、上游模型等未入表维度须走原始表。
- 平台归属读取写入时的 `usage_logs.platform` 快照（未选定提供商为 `unknown`），后续提供商或分组调整不改历史归属。管理员仪表盘业务请求数、Token 和时延仅统计用户请求，提供商成本包含系统开销。
- 实时推进与加锁：实时任务按周期运行并用 `lookback_seconds` 重算水位附近小时以吸收迟到记录；触及已闭合 UTC 日时同步重建日表。并发控制优先用 Redis 任务锁，不可用时回退 PostgreSQL advisory lock，叠加进程内原子标记防重入。

<a id="usage_state_ownership"></a>
### 历史回填与状态所有权

自动回填从新到旧按 UTC 小时推进，约束如下：

- 最多每分钟启动一轮，每轮上限 24 小时块、数据库预算 10 秒。
- 完成至少 1 小时后预算耗尽会保存游标并保持 `backfill`（不报错误）；首小时耗尽仍未完成才进入 `error`。
- 跨过完整 UTC 日前须先成功重建对应日表；重启后从持久游标续跑，UPSERT 幂等。

手工回填使用独立目标与游标，共用同一任务锁与预算。状态行按字段所有权隔离：手工请求只改目标与游标，实时任务只改水位与结果，后台推进/清除手工游标时做 CAS 校验，避免覆盖新提交的手工请求（聚合 SQL 执行期间不持行锁）。

关键状态字段：`phase`（`disabled`/`idle`/`live`/`backfill`/`error`/`unavailable`）、`live_watermark`、`lag_seconds`、`coverage_start`、`source_oldest_at` 及 `last_*`。仅看 `phase=idle` 不能证明目标时间范围已完整覆盖。

## 运维聚合

- 表与粒度：`ops_metrics_hourly` 按全局、平台、平台+分组保存成功数、错误分类、Token、耗时和首 Token 延迟，无流量小时写入全局零值桶以区分「无流量」和「未聚合」；`ops_metrics_daily` 由小时表滚动生成（长窗口查询目前仍以小时表为主）。
- 调度节奏：小时任务每 10 分钟重算减去 5 分钟安全延迟后的最近稳定 UTC 小时；日任务每小时生成最近闭合 UTC 日，不自动大范围回填更早历史。任务锁同样优先 Redis、回退 PG advisory lock，结果写入 `ops_job_heartbeats`。
- 状态 phase 为 `disabled`/`pending`/`idle`/`error`/`unavailable`；查询能否走聚合按目标小时逐桶检查覆盖。

<a id="query_routing_and_fallback"></a>
## 查询路由与透明降级

所有查询区间采用半开区间 `[start, end)`，确保日表、小时表与原始表边界不重叠。

### Usage 组合查询

支持的查询将 `[start, end)` 拆分为四段合并：

1. `coverage_start` 之前的头部：读 `usage_logs`。
2. 连续覆盖内的完整 UTC 日：读 `usage_analytics_daily`。
3. 聚合区间两端不足一整天的部分：读 `usage_analytics_hourly`。
4. `live_watermark` 之后或尾部不足 1 小时的部分：读 `usage_logs`。

具体场景与边界：

- 时区与实时指标：趋势查询先组合 UTC 桶再按配置时区重分桶；半小时或四十五分钟偏移时区会切开 UTC 小时桶，直接读原始表。最近 5 分钟 RPM/TPM 始终读原始表。
- 复用入口：团队总览/成员趋势、公开 `/usage` 累计与今日摘要、管理端分组费用汇总、管理员用户消费排行与 Top 趋势均复用组合源。用户排行按 `billing_user_id` 汇总（团队 Key 归付款主体 Team Owner，缺失回退 `user_id`）再关联 `users` 取最新用户名/邮箱，不把身份字段固化进聚合表。
- 直接走 raw（非故障）：聚合关闭、覆盖不足 1 完整小时、按提供商/request ID/非请求模型筛选、目标统计无聚合实现。聚合 SQL 报错时透明重试完整 raw 查询（同操作告警限每分钟 1 次），不混加部分聚合与 raw 结果。

### Ops 查询路由

外部接口不接受数据源参数，后端统一解析为 `auto` 或 `raw`：

- `auto` 对稳定完整小时读 `ops_metrics_hourly`，两端不完整片段读原始表；任一小时缺覆盖、数据异常或 SQL 报错时自动重跑完整 raw 查询。
- 强制 raw 场景：聚合关闭、配置了自定义忽略状态码（未入聚合维度）、实时流量与告警计算等严格即时查询。
- 概览和合并快照查询原始数据时，延迟统计有独立的 2 秒预算。预算耗尽后，只要请求仍有效且其余查询成功，接口就返回 200。超时片段保留请求数、Token 数和错误统计，请求耗时与首 Token 延迟只汇总可用的样本。

## 一致性、清理与备份

- 管理员清理触发重算：Usage 清理任务一旦删过原始记录，无论成功、取消或后续失败，收尾均在聚合器独立 context 中登记异步重算（不受 `backfill_enabled` 或运行时开关限制，不推进正常水位）。
- 周期清理：Usage 定时任务每 6 小时按 retention 清理小时/日聚合与 `usage_logs`，并归档删除 `usage_billing_dedup`（关闭部署层 Usage 聚合会连带停止周期清理）。Ops cleanup 按自身配置清理 Ops 聚合表。
- 备份与停机：聚合表属于备份可选数据组，恢复备份后需重新核对水位、覆盖桶与 heartbeat；原始 retention 越过缺口后无法回填重建。切勿手工篡改 watermark/coverage/heartbeat 掩盖故障。停止服务时取消在途聚合 SQL 并等待完成（受应用关闭预算约束）。

## 故障定位

页面为空或变慢时按序排查：

1. 检查部署硬开关、`pre_aggregation_settings` 与 `ops_monitoring_enabled`。
2. Usage 核对 `live_watermark`、`coverage_start` 与 `source_oldest_at`；Ops 核对小时 heartbeat 与零值桶连续性。
3. 检查 `last_error`、耗时、leader lock 与数据库 statement timeout。
4. 用同一半开区间与筛选条件对比聚合与 raw 结果，核对本地日期与 UTC 桶边界。
5. 核查 retention 或主动清理是否已删除原始记录。

新增聚合维度必须同步更新表唯一键、小时/日 UPSERT、查询构造器、覆盖测试、清理/备份表组和迁移，避免静默回退 raw 或错误合并数据。

相关文档：[可观测性与数据生命周期](observability_and_data_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[配置](../interfaces/configuration.md)、[部署与数据库迁移](deployment_and_migrations.md)、[运维目录](index.md)。
