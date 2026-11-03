# 运维监控与告警

本文描述 Ops 的信号采集、实时视图、告警评估、邮件报告、健康诊断和发布查询。它是[可观测性与数据生命周期](observability_and_data_lifecycle.md)下的详细文档；Usage 的结算、预聚合的实现和备份的内容策略，见各自的文档。

## 章节导航

- [信号流水线](#信号流水线)：修改指标、错误或系统日志的采集时读取。
- [实时与历史查询](#实时与历史查询)：修改 dashboard、WebSocket 或查询模式时读取。
- [告警评估](#告警评估)：修改规则、持续时间、静默或通知时读取。
- [计划报告](#计划报告)：修改日报、周报或健康摘要时读取。
- [健康与失效](#健康与失效)：排查面板为空、漏报或后台任务故障时读取。
- [发布查询与维护命令](#ops_release_and_maintenance)：检查版本、回退候选或清理历史的入口拒绝时读取。

<a id="ops_signal_pipeline"></a>
## 信号流水线

`ops` 负责观测规则和运行实例；`ops/postgres` 执行业务表的查询，`ops/rediscache` 提供锁和缓存，`ops/provider` 读取主机、cgroup、连接池和发布信息，`ops/httpapi` 负责管理和实时协议。采样核心只读取提供商、身份、并发和认证的健康数据。

Ops 同时接收请求错误、单独的上游 attempt 错误、入口准入拒绝、系统日志、并发、提供商可用性、实时流量和系统指标。每类信号有自己的 repository 和队列，一类的计数不能代替另一类：一次最终的客户端失败，可能对应多个上游错误；一次本地拒绝，也可能根本没有上游 attempt。

`OpsMetricsCollector` 在 Ops 和 monitoring 开关都开启时，周期采集数据库、Redis、主机和容器、提供商负载和运行时的指标。采集轮次通过 Redis 任务锁控制执行资格，必要的路径使用 PostgreSQL advisory lock；运行结果写入 job heartbeat、耗时和错误。

系统日志 sink 和请求、错误的采集，使用相互独立的有界队列。审计和系统日志 sink 重复调用 Start 时，由各自的屏障合并，Stop 之后无法重新打开；系统日志的退避和健康计数只有一份。队列拥塞时，按各自的策略丢弃或降级，并累计 dropped 和健康计数；它们不会反过来拖慢网关的核心转发。系统日志写库失败时，执行从 2 秒开始、上限 60 秒的指数退避，退避期间的批次计入 dropped，不访问数据库，成功后立即清除失败状态。敏感字段在写入存储之前清理，request ID、平台、分组、提供商和 endpoint 用于关联。

## 实时与历史查询

管理员的 Ops API 提供：concurrency、user concurrency、provider availability、realtime traffic、错误、上游错误和请求详情、入口拒绝、系统日志，以及 dashboard 的 snapshot、trend、histogram 和 token stats。概览、错误列表和请求明细弹窗，共用当前的时间范围；自定义范围使用同一组 `start_time` 和 `end_time` 半开区间，请求明细的窗口标签显示对应的起止日期和时间；已经选了自定义模式时，再修改起止时间也会刷新数据。

错误详情按错误摘要、请求信息、上游尝试、原始响应排列。摘要优先使用已记录的上游消息，HTML 和没有可提取消息的 JSON 显示响应格式，不作为根因结论。原始响应默认折叠；相同内容合并展示并保留全部来源标签，复制使用保存的原文。关联上游错误按时间升序分页加载，加载失败提供局部重试；部分记录无法读取详情时，显示实际读取数量。

起止时间缺任何一个时，回退到 `1h`，字面量 `custom` 不会传给后端。QPS WebSocket 用于短窗口的实时展示，同样需要管理员鉴权，它不是长期的审计数据源。Ops 持有按需的采样、连接计数和三十秒空闲停止；HTTP 适配层负责 Origin 和可信代理的判断、管理员认证、帧、关闭码和写超时。连接断开不代表停止等待已经完成。

平台筛选根据提供商或请求记录里的平台。实时的分组指标，汇总组内符合筛选条件的提供商，不返回分组的平台；同一个提供商可以计入多个分组。历史的成功记录和错误记录，都读取保存时的平台快照，没有选中提供商的错误归到 `unknown`，不会根据当前的分组或提供商反推。

历史 dashboard 的查询，可以按配置使用原始表或预聚合，覆盖不足时回退到原始表。聚合、水位和回填见[使用记录与运维预聚合](pre_aggregation.md)。页面为空时，要区分：monitoring 关闭、过滤条件、采集丢弃、聚合覆盖不足、查询超时，还是确实没有流量。

## 告警评估

告警规则包括 enabled、metric type、operator、threshold、window、sustained minutes、scope 和 filter，以及通知动作。评估器按运行设置周期执行，执行前取得任务锁；连续超过阈值的时间达到持续要求后，创建或更新 active 的事件，恢复后关闭，或标记为 resolved。

静默记录只在匹配的范围和时间内，抑制通知和事件动作，原始指标不受影响。管理员可以确认、解决事件，并配置邮件通知。邮件有全局和运行时的限流；发送失败时写进 heartbeat 和日志，事件不会被标成已送达。

最终错误透传规则的 `skip_monitoring`，只跳过指定客户端错误的常规监控记录；系统健康、访问、计费和安全审计的记录照常写入。详见[网关错误响应策略](../interfaces/gateway_error_policy.md)。

## 计划报告

计划报告支持日报、周报、错误摘要和提供商健康。Cron 使用分钟级的五字段表达式，时区取应用配置；每分钟的调度器只执行已经到期的报告，并用 Redis 任务锁和 last-run key 防止重复发送。

报告的收件人、启用项、错误的最小计数和提供商错误率的阈值，来自数据库的运行设置。邮件报告配置的写入，严格校验结构字段：旧的 `report.account_health_*` 返回 400，要使用 `report.provider_health_*` 保存。生成失败或邮件失败时，写入任务的 heartbeat。报告只是观测摘要，扣费、SLA 赔付和提供商自动恢复，都不能只以它为依据。

Ops 的构造和启动分开，app 在完成全部绑定后，才启动采样、聚合、告警、报告和清理。停止时等待当前的采样、聚合和 cron 作业结束；清理器还等待之前 Reload 留下的在途 cron。单次的局部超时不代表停止已经完成，最终的退出受应用总的清理预算约束，详见[启动与关闭](../architecture/system_architecture.md#startup_and_shutdown)。

告警和报告的业务触发、收件人和变量由 Ops 确定，app 直接交给 notification 实例（只有一个）。通知模块只负责模板、偏好、投递和 SMTP，不回读指标，也不重新判断告警；报告的摘要占位符使用通知纯叶子包定义的格式，实际的变量由 Ops 提供。投递失败不会引起资金或处置的回滚，详见[通知与邮件投递](../domains/notification_delivery.md)。

## 健康与失效

- 关闭 Ops 硬开关时，采集、查询和后台评估都停止，网关的转发、认证和计费照常进行。
- 分别观察 collector、evaluator、report、aggregation 和 cleanup 各自的 heartbeat、leader lock、最近一次的耗时和错误；只检查进程是否存活是不够的。
- 规则和运行设置通过数据库持久化，由运行时快照供任务读取；更新后，检查快照刷新结果和下一轮任务行为。
- Retention 和 cleanup 只删除观测数据；告警、报告或面板缺少历史数据，不影响资金账本，但会降低诊断的完整性。

<a id="ops_release_and_maintenance"></a>
## 发布查询与维护命令

发布查询由 Ops 的 ReleaseQuery 和 GitHub provider 提供，负责版本比较、回退候选的过滤，结果缓存二十分钟；GitHub Token 的可信范围和重定向规则见[部署指南](../guides/deployment/index.md#升级)。下载校验和二进制替换由 Ops 的技术适配层执行；`ops/maintenance` 负责更新和回退、系统操作锁和重启请求的编排。实际的进程退出，由 `app/lifecycle`（只有一处）执行。

`GET /api/v1/admin/system/check-updates` 返回当前版本、最新目标版本、更新标志和构建类型。`release_info` 包含该目标版本的发布名称、`body`、`published_at`、`html_url` 和下载资源；`body` 是 GitHub Release 的完整 Markdown，英文正文、中文 `<details>/<summary>` 折叠区、代码块和换行均原样保留，空正文仍返回空字符串。后端不按语言拆分正文，也不生成摘要或渲染 HTML。

普通查询优先使用有效缓存；`force=true` 跳过首次缓存读取并请求 GitHub。远端失败时，强制刷新也会回退到有效缓存，返回缓存中的版本及其完整发布信息，设置 `cached=true` 并附带 `warning`。失败不会覆盖缓存或续期；没有可用缓存时，最新版本回落为当前版本，`has_update=false`，响应省略 `release_info` 并返回失败原因。正文经过缓存往返后仍与同一目标版本关联。

管理员可从桌面端顶栏版本菜单打开“查看更新日志”；移动端隐藏版本入口。源码构建和正式构建发现新版本后，都在站内弹窗展示最新目标版本的说明；弹窗读取浏览器 store 中的 `release_info`，打开时不额外查询 GitHub。前端解析并净化 Markdown，保留英文正文和中文折叠区，提供有效的发布日期与发布页链接；缺少正文时显示空态。打开日志会关闭维护菜单，并将 Tab 导航限制在弹窗内；退出后焦点回到版本徽标。升级、重启和回退继续使用原有维护入口，阅读日志不触发这些操作。

`cleanup-ingress-reject-logs` 用精简版 bootstrap 装配 Ops 的分类和清理能力，不启动完整的 worker。它默认 dry-run，参数是 `--before`、`--batch-size`、`--execute`，分类版本是 `ingress-reject-v1`；它只清理匹配的分析事件。

相关文档：[可观测性与数据生命周期](observability_and_data_lifecycle.md)、[使用记录与运维预聚合](pre_aggregation.md)、[提供商维护](provider_maintenance.md)。
