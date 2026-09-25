# 可观测性与数据生命周期

本文说明 TokenRouter 的日志、用量、计费、审计及聚合数据如何产生、保存、清理和备份。具体监控告警和预聚合机制由独立专题拥有；修改采集队列、运维查询、保留策略或备份范围前应先由本文路由。

## 章节导航

- [数据面的职责](#数据面的职责)：判断一条记录是否可以丢弃或清理时读取。
- [专题路由](#专题路由)：进入监控、聚合或账号维护详细文档。
- [关联与脱敏](#关联与脱敏)：修改日志字段、错误透传或查询筛选时读取。
- [后台运行时](#后台运行时)：修改队列、worker、leader lock 或心跳时读取。
- [聚合与查询](#聚合与查询)：修改仪表盘和 Ops 读取路径时读取。
- [清理与留存](#清理与留存)：删除历史数据或修改 retention 时读取。
- [创作台数据生命周期](#创作台数据生命周期)：判断创作台素材与任务元数据何时失效时读取。
- [备份与恢复范围](#备份与恢复范围)：修改 dump 内容或恢复流程时读取。

<a id="observation_ownership"></a>
## 数据面的职责

| 数据面 | 主要持久化 | 作用 | 关键边界 |
| --- | --- | --- | --- |
| 进程日志 | stdout/stderr | 启动、运行和异常诊断 | 由部署日志系统负责最终留存 |
| Ops 系统日志 | `ops_system_logs` | 索引 warning/error、HTTP access、audit 类事件 | 有界异步队列，拥塞时允许丢弃并累计健康计数 |
| Ops 错误与指标 | `ops_error_logs`、metrics/alert/heartbeat 表 | 上游错误、窗口指标、告警、任务状态 | 可关闭监控；是观测数据，不是资金账本 |
| 用量日志 | `usage_logs` 及分区/聚合表 | 用户查询、成本分析、模型/端点/时延统计 | 分析记录可异步写入并受清理策略影响 |
| 计费事实 | `billing_usage_entries`、`usage_billing_dedup`、余额/订阅/额度相关表 | 幂等扣费、配额和资金效果 | 事务性领域事实；不能通过删除用量日志撤销 |
| 操作审计 | `audit_logs`、支付/迁移专项审计表 | 记录管理员或敏感业务动作 | 与普通系统日志的保留和访问范围分开 |

`usage`、`audit`、`ops` 分别拥有对应记录、用例和存储 Adapter；app 统一持有生产实例。供应商用量解析、请求 ID 选择、结算和 `UsageRecordWorkerPool` 的完成任务仍在网关；资金去重归档由 billing 的命名能力执行。

用量生产消费者直接使用 `usage.UsageLog`、查询接口和同一 PostgreSQL Store，保持用量事实与展示投影分离。关联用户、Key、账号仍为展示投影。网关完成记录在写入前保留客户端模型展示覆盖，只影响 `requested_model`，不改变实际模型、映射链或计费输入。

用量日志不是扣费账本，Ops 事件也不是请求成功的唯一证据。结算失败时保留的用量日志仍包含计算成本，但以 `actual_cost=0` 标识未成功扣费；这类记录必须与结算表对账，不能作为扣费成功的证据。排查金额时以结算事务和账单分配为准，再用 `request_id`、用户、Key、账号和时间窗口关联用量/Ops 数据。清理分析记录不会退款，也不能修复一笔错误结算。

## 专题路由

| 主题 | 规范文档 |
| --- | --- |
| Ops 指标、实时流量、错误、告警和邮件报告 | [运维监控与告警](ops_monitoring_and_alerting.md) |
| Usage/Ops 小时日聚合、水位、回填和查询降级 | [使用记录与运维预聚合](pre_aggregation.md) |
| 上游账号刷新、测试、配额探测和自动恢复 | [账号维护](account_maintenance.md) |
| 内容审核日志、风险命中和自动处置 | [内容审核与风险处置](../domains/content_moderation.md) |
| 用户九平台额度的 Redis/DB 同步 | [用户平台额度](../domains/platform_quotas.md) |

## 关联与脱敏

网关为每次请求生成内部 client request ID，并把归一化 endpoint、platform、requested/upstream model、用户、API Key、账号和团队等维度带入允许的用量/Ops 记录。入站 `X-Client-Request-ID` 仅作为受限的 `parent_client_request_id` 保存，用于跨 TokenRouter/Sub2API 链路排障，不参与权限、路由或结算幂等；服务生成的内部 ID 通过 `X-Sub2API-Request-ID` 暴露给下游诊断。客户端提供的 session ID 只作为显式关联字段，不从 prompt 或缓存键推导。

网关入口只接受字符受限的 `X-Client-Request-ID` 作为父级关联值；缺失或不安全时，响应回退使用服务生成的内部 ID，服务不会把生成的关联 ID 加入上游请求。内部 ID 通过 `X-Sub2API-Request-ID` 响应头标识，调用方 ID 与内部 ID 均会进入访问日志，但只有内部 ID 能作为结算幂等来源。流式网关的 `http.access` 记录还会尽力写入 `request_content_length`、`account_slot_acquired_ms`、`upstream_get_conn_ms`、`upstream_got_conn_ms`、`upstream_wrote_request_ms`、`upstream_first_response_byte_ms`、`upstream_first_sse_data_ms`、`first_visible_output_ms` 和 `first_downstream_flush_ms`；`upstream_attempt_count`、各阶段计数、连接复用和写入错误字段用于识别连接池等待、重试与传输异常。阶段字段只包含时间、计数和连接复用状态，不包含请求体或凭据。

凭据、Authorization、Cookie、refresh token、支付密钥、对象存储 secret、完整上游 body 和用户提示不能直接写入日志。上游错误只透传允许的安全字段；系统日志 sink 在落库前再次整理字段并限制长度。新增日志字段时要同时检查：结构化 logger、Ops sink 的字段白名单/脱敏、管理端 DTO、导出和测试夹具。

审计 Redactor 由 app 直接注入账号与支付模块的敏感字段清单，HTTP 捕获共享该实例。Ops 构造、日志 sink 与报告任务也直接使用所属模块实例。

多实例中每条系统日志带 host，便于区分进程来源。关联 ID 不是授权凭据；管理端详情、实时流和 WebSocket 仍必须经过管理员鉴权与相应 step-up 门禁。

<a id="background_runtimes"></a>
## 后台运行时

观测对象构造不启动后台任务；app 完成依赖和回调绑定后，通过统一生命周期启动周期任务，按需资源仍在首次使用时启动。HTTP 使用五秒优雅关闭预算，后台清理共用独立的三十秒预算。各队列的故障和交付保证分别保留：

- Usage record worker 使用有界队列；默认拥塞策略可同步降级，也支持 sample/drop。队列深度、成功、失败、丢弃和同步降级计数必须进入运行时诊断。显式 drop/sample 溢出仍按运维配置丢弃；池已停止的关停窗口则使用独立提交状态，计费任务在调用侧内联同步兜底。
- Ops 错误采集队列在进入队列前清理敏感字段，并保留原批次窗口、工作数、条数/字节上限和过载丢弃；关闭先封闭入队，再等待已取得的批次完成。HTTP 响应捕获、SSE 分片观察和 writer 复用由 `gateway/httpapi` 拥有，app 显式注入同一 Ops 队列及只读身份/拒绝投影；认证失败时已加载的 Key 不构成认证主体。Cyber 专项记录复用该队列，不安装全局队列绑定。HTTP 捕获与供应商错误分类只提交观测输入。Ops 的错误类型、阶段、严重性与 SLA 排除判断由 `ops.ClassifyRequestError` 等纯规则统一执行；HTTP 只固化本地限制、路由容量、账号认证和上游响应事实，不把 Gin Context 传入分类核心。
- Ops system log sink 只索引选定等级/组件，按批写 PostgreSQL；队列满时不阻塞主请求，而是增加 dropped counter。落库连续失败后从 2 秒开始指数退避，最长 60 秒；退避期间直接丢弃观测批次并计入 dropped，避免日志链路持续占用数据库连接，任意一次成功会立即恢复正常写入。重复 Start 不产生新写入协程或独立退避状态；Stop 幂等且不能重开。停止等待受应用剩余预算约束，超时会报告未完成项，不能记为排空成功。
- 运维指标采集器、小时/日聚合器、告警评估器、计划报告和清理任务各自维护周期、开关、leader lock 与 job heartbeat。
- Usage cleanup 任务持久化为 pending/running/succeeded/failed/canceled，分批删除；进程中断后 stale running 任务可以重新抢占继续执行。
- Audit、scheduler snapshot outbox、幂等记录和批量图片清理有各自 worker。不能用某一 worker 健康推断整个后台体系健康。

需要全局唯一执行的任务优先使用 Redis leader lock，部分服务在 Redis 不可用时回退 PostgreSQL advisory lock。锁失败通常应跳过本轮而非并发执行；管理恢复时应先确认 heartbeat、锁 TTL 和数据库维护状态，不能直接启动第二套清理任务。

关闭 Ops hard switch 会禁止其采集/查询能力，但不应关闭网关核心转发和计费。数据库运行时设置通过原子快照供热路径读取，并由后台刷新在多实例间最终同步；设置读取失败时要遵守各服务定义的 fail-open/fail-closed 语义。

<a id="usage_query_contracts"></a>
## 聚合与查询

原始 `usage_logs`、Ops 原始事件和系统指标是聚合来源，hourly/daily rollup 及缓存是读取优化。仪表盘、趋势、直方图、SLA 和排名查询可以按配置选择预聚合或原始表，并在覆盖不足时回退；任何聚合结果都不参与余额或订阅扣减。

用户、管理员与公开 `/usage` 查询直接绑定 usage；API Key/身份/团队只通过批量投影或 SQL 查询参与函数组合，保留原付款/行为主体、用户费用/账号成本和模型口径。查询缓存按各原数据面分别持有，key、TTL、singleflight 与 stale refresh 不合并；缓存边界返回独立副本。`pkg/querycache` 只提供缓存机制，ETag、304 和响应 Header 由 HTTP Adapter 决定。

用量 HTTP 日期解析使用 app 注入的 Calendar。未提供或无法解析用户时区时回退该服务端位置；日期型结束值按日历增加一天形成排他上界，跨夏令时的一天可以是 23 或 25 小时。带偏移的明确时间不追加一天。公开查询、排行和仪表盘继续保留各自默认范围与取时点，不统一为同一时间窗口。

用量 PostgreSQL 存储和 Dashboard 聚合持有相同的日期对象，事务内派生的存储继续携带它；SQL 按日分组使用该命名时区。多维 usage 聚合仍采用 UTC 桶，再按查询时区重分桶。团队日统计沿用团队仓储的 Calendar，仪表盘批量用户缓存的日期字段与原 key 格式保持不变。

聚合作业按闭合时间桶推进 watermark，避免把仍变化的当前桶当作完整历史。回填、重算或清理后要使受影响的 dashboard/查询缓存失效，并检查覆盖区间；不能只看“任务成功”就假设所有时间范围都已可由 rollup 回答。

实时 Ops 视图来自短窗口查询、运行时计数器和 WebSocket 推送。它适合排障而非长期审计；页面显示空数据时要区分监控关闭、过滤条件、聚合覆盖、采集丢弃、查询超时和确实没有流量。

<a id="data_cleanup"></a>
## 清理与留存

Ops cleanup 按运行时设置清理错误日志、系统日志、指标/聚合、告警、心跳及清理审计等表，并以批处理和暂停降低数据库压力。多实例只由 leader 执行，数据库维护任务运行时跳过。保留天数为 `0` 的具体含义由目标计划决定，修改默认值前必须覆盖 truncate/delete 计划测试。

Usage cleanup 是管理员显式创建的持久任务，必须提供时间范围，可再按用户、团队、Key、账号、分组、模型、请求类型、流式或计费类型收窄。删除按批推进并记录操作人、进度、取消和错误。只要任务已经删除记录，成功、取消或随后失败都会在统一收尾登记受影响范围的异步聚合重算；重算使用聚合运行时 context，取消状态仍为 canceled。它没有新增持久修复队列，也不承诺进程崩溃后的修复投递。对大范围清理应先备份并估算分区、索引、vacuum 和查询影响。

通用审计的清空操作由 audit/postgres 在同一事务内锁表、计数、TRUNCATE 并插入留痕；任何一步失败都回滚，序列继续沿用原行为。管理员 API Key 仍被拒绝，并继续验证原 TOTP 凭据。普通管理员审计保持尽力异步写入；Ops 系统日志清理的留痕仍尽力执行，支付专项审计与退款事务维持原保证。

清理前按数据所有权判断：

- 可观测/分析记录可按合规和容量策略清理，但会降低历史排障和报表完整性。
- 计费幂等、余额流水、订单、订阅和审计事实不随 usage/Ops retention 自动删除。
- Redis 中的限流、并发、粘性、任务和缓存键各自依赖 TTL 或专用清理器，不能用数据库清理替代。
- 对象存储产物与数据库元数据必须协调删除；先删元数据会失去对象定位，先删对象会留下不可下载记录。

## 创作台数据生命周期

创作台分别保存任务元数据和临时素材，排查丢失或合规问题前先读[创作台](../domains/creative_studio.md)：

- 素材不进入 PostgreSQL、备份或普通日志：原图、mask、生成图和 prompt 明文只存于 Redis 临时键（`creative:payload:`/`creative:input:`/`creative:mask:`/`creative:output:`），默认 TTL 30 分钟；PostgreSQL 存 `creative_runs`/`creative_run_outputs` 元数据和 `creative_run_outbox` 动作，备份因此不包含素材本体，恢复数据库也不会恢复图片。
- ack 即删：客户端确认保存后服务端先落 `acked` 元数据再删除对应临时输出键；删除失败由 transient cleanup reconciler 周期重试，取消与失败路径同样保留 `release_pending` 直到补偿完成。
- 重点指标包括 `provider_succeeded`/`settlement_pending`/`release_pending` 数量、outbox lag、lease lost、结算/释放重试次数、`result_lost` 和 transient cleanup 失败；这些状态不能只依赖普通日志判断。
- 结果丢失按 `result_lost` 处理：临时输出过期或缺失时任务从 `succeeded` 降级为 `result_lost`，不返回可交付成功状态，也没有服务端恢复；上游已成功的丢失任务仍保留计费捕获和 `usage_logs`（`creative_settle:{run_id}`），未执行的丢失路径释放预占。
- 计费与用量事实（hold/capture/release 幂等记录、`usage_logs`）属于普通资金与统计数据面，按本文其余章节的留存和清理规则处理，不随临时素材的 TTL 消失。

## 备份与恢复范围

backup 使用 PostgreSQL dump，并把产物流式写入本地或 S3 兼容存储；配置、记录、定时调度和恢复受维护锁保护。默认内容策略会排除 `backupContentTableDataGroups` 归类的 usage records、Ops logs、专项 audit history 和 runtime data，四类都必须显式选择。备份显示 completed 只证明已写出所选内容，不代表这些类别全部存在于文件中。

S3 的 `multipart` 上传模式把 gzip 流作为一个对象低内存上传，适合兼容标准 multipart 签名的存储；`spooled_put` 面向只能可靠接受已知长度 `PutObject` 的兼容服务。后者超过 4 GiB 时边压缩边封装独立分卷，临时磁盘只保留当前一卷；备份记录保存每卷顺序、对象键、大小和 SHA-256。任一卷上传失败、服务重启或删除失败时，必须尝试清理全部已登记对象，并在清理不完整时保留可诊断记录。

`usage_records` 备份组不只包含 `usage_logs` 和聚合表，还包含 `billing_usage_entries`、`usage_billing_dedup` 及其 archive。若恢复目标需要保留历史结算明细或请求去重证据，必须启用该组并评估体积；当前余额/订阅状态被恢复不等于这些历史证据也被恢复。

恢复前读取备份记录中的 storage type/key，而不是仅使用当前存储配置；这允许切换本地/S3 后仍恢复旧记录。分卷备份会先按序下载到权限受限的临时归档并校验每卷大小和 SHA-256，只有全部校验通过后才解压进入数据库，避免缺卷或坏卷造成部分恢复；旧单文件记录继续流式恢复。恢复数据库不会自动恢复 Redis 或对象存储文件。完整灾备需要分别定义 PostgreSQL、Redis、`DATA_DIR` 和外部对象的恢复点，并验证它们在同一业务时间窗口内一致。

敏感 S3 配置入库前依赖稳定的加密密钥；临时生成的密钥不能用于保存新 secret。备份保留清理必须同时删除产物和记录，并在删除失败时保留可诊断状态。

本地备份文件操作先核对真实目标，再通过同一个受根目录约束的句柄执行读取、创建或删除；根内符号链接保留，根外目标被拒绝。配置中的本地根路径不对外扩展可访问范围。恢复错误与运行记录写入错误分开报告：running 登记失败不得启动恢复，已提交恢复的最终记录失败不会撤销数据库变化。具体关闭与恢复保证见[维护执行](deployment_and_migrations.md#maintenance_execution)。

相关文档：[运维监控与告警](ops_monitoring_and_alerting.md)、[路由与计费](../domains/routing_and_billing.md)、[部署与数据库迁移](deployment_and_migrations.md)、[配置边界](../interfaces/configuration.md)、[运维目录](index.md)。
