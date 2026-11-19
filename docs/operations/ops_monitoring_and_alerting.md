# 运维监控与告警

本文记录 Ops 的信号采集、实时与历史查询、告警评估、计划报告、健康诊断及发布查询。它是[可观测性与数据生命周期](observability_and_data_lifecycle.md)下的专题文档；Usage 结算、预聚合和备份策略见各自文档。

## 章节导航

- [信号流水线](#ops_signal_pipeline)：指标、错误与系统日志的采集与退避规则。
- [实时与历史查询](#实时与历史查询)：dashboard、错误详情与 QPS WebSocket 的查询口径。
- [告警评估](#告警评估)：告警规则、静默与通知投递边界。
- [计划报告](#计划报告)：日报、周报、Cron 防重与生命周期。
- [健康与失效](#健康与失效)：排查面板为空、漏报或后台任务故障。
- [发布查询与维护命令](#ops_release_and_maintenance)：版本检查、更新日志展示与历史拒绝日志清理。

<a id="ops_signal_pipeline"></a>
## 信号流水线

模块分工：`ops` 拥有观测规则与运行实例，`ops/postgres` 和 `ops/rediscache` 负责查询与锁缓存，`ops/provider` 采集主机、cgroup、连接池与发布信息，`ops/httpapi` 提供管理和实时协议。

- 信号隔离：请求错误、单次上游 attempt 错误、入口拒绝、系统日志、并发、提供商可用性、实时流量和系统指标各自使用独立仓储与有界队列。一次最终客户端失败可能对应多个上游 attempt 错误，一次本地拒绝可能无上游 attempt，各类计数不可互代。
- 周期指标采集：`OpsMetricsCollector` 在 Ops 与 monitoring 开关均开启时周期采集数据库、Redis、主机/容器、提供商负载及运行时指标，执行前竞争 Redis 任务锁（必要路径用 PostgreSQL advisory lock），结果写入 job heartbeat、耗时与错误。
- 队列拥塞与退避：日志 sink 和请求/错误采集队列满时按各自策略丢弃并累计 `dropped` 与健康计数，不阻塞网关转发。审计与系统日志 sink 多次 Start 合并生效、Stop 后不可重开。系统日志写库失败按 2 秒起、上限 60 秒指数退避，退避期间批次计入 `dropped` 且不访问数据库，成功后立即清零。
- 脱敏与关联：敏感字段在落库前脱敏，按 request ID、平台、分组、提供商和 endpoint 关联。

## 实时与历史查询

- 时间窗口：概览、错误列表和请求明细弹窗共用当前时间范围；自定义范围使用半开区间 `[start_time, end_time)`，缺任一端时回退 `1h`（不向后端传字面量 `custom`）。
- 错误详情展示：按错误摘要、请求信息、上游尝试、原始响应排序。摘要优先取上游消息，HTML 或无消息 JSON 仅标明格式而不当根因。原始响应默认折叠，同内容合并展示并保留全部来源标签；关联上游错误按时间升序分页加载，支持局部重试。
- QPS WebSocket：仅作短窗口实时展示（需管理员鉴权，非长期审计数据源），按需采样并在空闲 30 秒后停止；HTTP 适配层校验 Origin、可信代理、关闭码与写超时。
- 平台与历史口径：实时分组指标汇总组内符合筛选的提供商（单提供商可计入多分组），不返回分组平台；历史记录读取保存时的平台快照，未选定提供商归为 `unknown`。历史 dashboard 按配置走预聚合或原始表，覆盖不足自动回退原始表，详见[使用记录与运维预聚合](pre_aggregation.md)。
- 面板为空排查：依次区分 monitoring 关闭、筛选条件、采集丢弃、聚合覆盖不足、查询超时与真实无流量。

## 告警评估

- 评估流程：告警规则包含 `enabled`、metric type、operator、threshold、window、sustained minutes、scope/filter 与通知动作。评估器竞争任务锁周期运行，连续超阈值达到持续时间后创建或更新 active 事件，恢复后关闭或标为 resolved。
- 静默与通知：静默仅在匹配范围与时间窗口内抑制通知与动作，不影响原始指标采集。邮件通知受全局与运行时限流约束，发送失败写入 heartbeat 与日志且不标为已送达。
- 监控跳过边界：错误规则的 `skip_monitoring` 仅跳过指定客户端错误的常规监控记录，不影响健康、访问、计费与安全审计，详见[网关错误响应策略](../interfaces/gateway_error_policy.md)。

## 计划报告

- 调度与防重：支持日报、周报、错误摘要与提供商健康报告，使用应用配置时区的 5 字段分钟级 Cron，通过 Redis 任务锁与 last-run key 防重发。
- 配置校验：收件人、启用项与阈值存数据库运行设置，严格校验结构字段（旧 `report.account_health_*` 返回 400，须使用 `report.provider_health_*`）。
- 边界与启停：Ops 负责业务触发与变量组装，邮件模板与 SMTP 投递归[通知与邮件投递](../domains/notification_delivery.md)（投递失败不回滚业务）。报告仅作观测摘要，不可作为计费、SLA 赔付或自动恢复依据。停止时等待在途采样、聚合与 cron（含 Reload 遗留任务）结束，受总关闭预算约束，见[启动与关闭](../architecture/system_architecture.md#startup_and_shutdown)。

## 健康与失效

- 关闭 Ops 硬开关会停止采集、查询与后台评估，网关转发、认证与计费不受影响。
- 监控后台任务需分别核对 collector、evaluator、report、aggregation、cleanup 的 heartbeat、leader lock、耗时与错误，不能只看进程存活。
- Retention 与 cleanup 仅清理观测数据，缺失历史观测不影响资金账本，但会降低诊断完整性。

<a id="ops_release_and_maintenance"></a>
## 发布查询与维护命令

- 版本检查缓存：`GET /api/v1/admin/system/check-updates` 查询 GitHub Release 并缓存 20 分钟（GitHub Token 可信范围见[部署指南](../guides/deployment/index.md#升级)）。`release_info.body` 原样保留完整 Markdown（含英文正文、中文 `<details>/<summary>` 折叠区与代码块），后端不拆分或渲染 HTML。
- 强制刷新与降级：`force=true` 跳过首次缓存查远端；远端失败时回退有效缓存并返回 `cached=true` 与 `warning`（不覆盖或续期缓存）。无可用缓存时最新版本回落当前版本、`has_update=false`、省略 `release_info` 并返回原因。
- 前端更新日志弹窗：桌面端顶栏版本菜单提供「查看更新日志」（移动端隐藏），直接读取浏览器 store 中的 `release_info` 净化渲染，不额外请求 GitHub；弹窗捕获 Tab 焦点，关闭后焦点回到版本徽标，阅读日志不触发升级或回退。
- 维护命令与替换：下载校验、二进制替换与系统操作锁归 `ops/maintenance`，进程退出统一由 `app/lifecycle` 执行。`cleanup-ingress-reject-logs` 使用精简引导清理 `ingress-reject-v1` 分析事件，默认 dry-run，支持 `--before`、`--batch-size`、`--execute`。

相关文档：[可观测性与数据生命周期](observability_and_data_lifecycle.md)、[使用记录与运维预聚合](pre_aggregation.md)、[提供商维护](provider_maintenance.md)。
