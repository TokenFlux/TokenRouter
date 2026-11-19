# 可观测性与数据生命周期

本文说明 TokenRouter 的日志、用量、计费、审计与聚合数据的生成、保存、清理和备份边界。监控告警与预聚合机制另有专文，修改采集队列、查询路径、保留策略或备份范围前请对照对应文档。

## 章节导航

- [各类数据的作用](#observation_ownership)：判断记录能否丢弃或清理时读取。
- [相关文档](#相关文档)：进入监控、聚合或提供商维护的详细文档。
- [关联与脱敏](#关联与脱敏)：修改日志字段、错误透传或查询筛选时读取。
- [后台运行时](#background_runtimes)：修改队列、worker、leader lock 或心跳时读取。
- [聚合与查询](#usage_query_contracts)：修改仪表盘和 Ops 读取路径时读取。
- [用户最近使用时间](#user_activity_summary)：修改活动汇总、用户排序或用量清理时读取。
- [清理与留存](#data_cleanup)：删除历史数据或修改 retention 时读取。
- [创作台数据生命周期](#创作台数据生命周期)：判断创作台素材与任务元数据何时失效时读取。
- [备份与恢复范围](#备份与恢复范围)：修改 dump 内容或恢复流程时读取。

<a id="observation_ownership"></a>
## 各类数据的作用

| 数据 | 主要持久化 | 作用 | 要点 |
| --- | --- | --- | --- |
| 请求摘要 | `request_records` | 按统一请求 ID 查询调用结果与关联记录 | 独立于 Ops 开关和采样，默认保留 30 天 |
| 进程日志 | stdout、stderr | 启动、运行和异常诊断 | 最终留存由部署日志系统负责 |
| Ops 系统日志 | `ops_system_logs` | 索引 warning、error、HTTP access 和 audit 事件 | 有界异步队列，拥塞时可丢弃并累计健康计数 |
| Ops 错误与指标 | `ops_error_logs` 及 metrics、alert、heartbeat 表 | 上游错误、窗口指标、告警、任务状态 | 监控可关闭；属观测数据，非资金账本 |
| 用量日志 | `usage_logs` 及其分区、聚合表 | 用户查询、成本分析、模型、端点和时延统计 | 分析记录可异步写入，受清理策略影响 |
| 计费记录 | `billing_usage_entries`、`usage_billing_dedup` 及余额、订阅、额度表 | 幂等扣费、配额和资金变化 | 事务性领域数据；删用量日志不撤销扣费 |
| 操作审计 | `audit_logs` 及支付、迁移专项审计表 | 记录管理员和敏感业务操作 | 保留期与访问范围独立于普通系统日志 |

- 模块归属：`usage`、`audit`、`ops` 分别维护各自的记录、用例和存储；网关负责上游用量解析、请求 ID 选择、结算和完成队列；资金去重归档归 `billing`。
- 日志级别解析：标准库日志与 `LegacyPrintf` 优先识别开头显式前缀（`[DEBUG]`/`[INFO]`/`[WARN]`/`[ERROR]` 或 `debug:`/`info:`/`warn:`/`error:`），无前缀时再按英文关键词推断。显式前缀覆盖插值参数中的错误词，旧式消息应保留前缀以免改变 stdout/stderr 分流或 Ops 收录范围。
- 展示数据边界：用量记录关联的用户、Key 和提供商仅用于展示。网关完成记录保留客户端模型展示覆盖，仅影响 `requested_model`，不改变实际模型、映射链和计费输入。
- 账本对账边界：结算失败时保留的用量日志仍含计算成本，但以 `actual_cost=0` 标明未扣费。金额排查以结算事务和账本为准，再用 `request_id`、用户、Key、提供商和时间窗关联观测数据；清理分析记录不会退款或修复错误结算。

## 相关文档

| 主题 | 文档 |
| --- | --- |
| Ops 指标、实时流量、错误、告警和邮件报告 | [运维监控与告警](ops_monitoring_and_alerting.md) |
| Usage 和 Ops 的小时、日聚合，水位、回填和查询降级 | [使用记录与运维预聚合](pre_aggregation.md) |
| 上游提供商的刷新、测试、配额探测和自动恢复 | [提供商维护](provider_maintenance.md) |
| 内容审核日志、风险命中和自动处置 | [内容审核与风险处置](../domains/content_moderation.md) |

## 关联与脱敏

- 请求 ID 与别名：入口生成的 `request_id` 贯穿各类诊断记录，供应商与调用方 ID 存为外部别名，访问权限由认证主体与归属决定。`billing_key` 兼容历史资金去重值，界面展示 `request_id`，详见[请求 ID 与请求查询](request_lookup.md)。
- 性能数据来源：`usage_logs.timings` 是使用记录性能的唯一持久化来源，完成任务冻结对应尝试观测并与 usage 一次写入，供列表、导出、TTFT/TPS 和聚合读取（不受 Ops 开关或访问日志采样影响），字段见 [HTTP 接口](../interfaces/http_api.md#response_errors)。
- 请求摘要：`request_records` 保存请求起止时间、状态、上游尝试、完成结果及无 usage 请求的失败阶段计时（鉴权失败、准入拒绝、排队超时、连接失败）。A 失败后 B 产生 usage 时，A 留失败记录、B 经 usage 关联性能，摘要不复制完整成功快照。
- 访问日志：`http.access` 保留状态、访问耗时、路由、大小、身份和错误上下文，不附加完整阶段快照；`ops_system_logs.extra` 不参与计时查询。
- 写入语义：入队仅代表待处理，数据库回执才算确认写入。批写超时未返回时记为结果未知，详情查询后续通过 usage 关联确认。队列拒绝、任务异常和最终写失败仅保留诊断，不补建 usage 也不重做结算；进程崩溃或数据库持续不可用可能丢失尚未落库的内存观测。
- 脱敏规则：凭据、Authorization、Cookie、refresh token、支付密钥、对象存储 secret、完整上游 body 与用户提示词均不直接落日志。上游错误仅透传安全字段，系统日志 sink 落库前再整理限长；审计脱敏覆盖提供商与支付敏感字段清单。
- 变更核对与权限：新增日志字段须同步检查 logger、Ops sink 白名单、管理端 DTO、导出和夹具。系统日志附 `host` 字段；关联 ID 不作鉴权凭据，详情、实时流和 WebSocket 须管理员鉴权与 step-up。

<a id="background_runtimes"></a>
## 后台运行时

观测组件构造时不启动后台任务，周期任务由统一生命周期启动，按需资源首次使用时启动。HTTP 优雅关闭预算 5 秒，后台清理共用独立 30 秒预算。

| 运行时组件 | 队列与持久化行为 | 过载与停机语义 |
| --- | --- | --- |
| 请求摘要 | 按 ID 在内存合并，批写失败逐条重试 | 缓冲满时同步写；正常关闭排空，异常退出丢待写快照，见[请求记录与写入](request_lookup.md#request_storage) |
| Usage record worker | 有界队列；记录深度、成败、丢弃和同步降级计数 | 默认拥塞降级同步写，也支持 `sample`/`drop`；池停机窗口使用独立提交状态，计费任务在调用侧同步执行 |
| Ops 错误采集队列 | 入队前脱敏，按批窗、worker、条数和字节上限写库；Cyber 专项记录复用此队列；纯规则分类不接收 Gin Context，认证失败的 Key 不算主体 | 过载丢弃；停止先禁入队再等待已取出批次完成 |
| Ops system log sink | 仅索引选定等级与组件并批量写入 PostgreSQL | 满队不阻塞请求（计入 `dropped`）；连续写库失败按 2 秒至 60 秒指数退避（退避期丢弃并计 `dropped`，单次成功即恢复）；`Start`/`Stop` 幂等且停后不可重启，超时不算排空 |
| 指标、聚合、告警、报告与清理 | 各自维护周期、开关、leader lock 与 job heartbeat | 互斥任务优先 Redis 锁（部分故障回退 PG advisory lock），争抢失败跳过本轮 |
| Usage cleanup | 持久状态 `pending`/`running`/`succeeded`/`failed`/`canceled`，分批删除 | 进程中断后超龄 `running` 任务可重新抢占继续执行 |
| Usage export | 持久状态含 `expired`；创建经事务锁原子校验活动配额；按 `(created_at, id)` 降序流式写根目录受限临时 CSV 并原子重命名 | 单任务超时直标终态 `failed` 避免占满 worker；优雅停机中断重置回 `pending`，异常退出超龄 `running` 重新从头生成，半成品不外露 |
| 插件 KV worker | 单实例每 5 分钟采额度（失败保留旧快照），每 10 分钟清过期行（每批 1000 行、单轮最多 30 秒） | 配额含保留的过期数据，详见[KV 存储与维护](../architecture/plugin_system.md#kv_storage) |
| 专项清理 worker | 审计、scheduler snapshot outbox、幂等记录和批量图片各有独立 worker | 单一 worker 正常不代表全体后台健康 |

人工恢复清理任务前须核对 heartbeat、锁 TTL 与数据库维护状态，避免并发启动第二套清理。关闭 Ops 硬开关仅停止 Ops 采集与查询，不影响网关转发和计费；数据库运行时设置经原子快照供热路径读取并后台刷新，读取失败按各服务 fail-open 或 fail-closed 规则处理。

<a id="usage_query_contracts"></a>
## 聚合与查询

原始 `usage_logs`、Ops 事件与系统指标是权威数据源，小时/日预聚合及缓存仅作读优化，覆盖不足时回退原始表；聚合结果绝不参与余额或订阅扣减。

- 系统请求口径：管理员用量默认含用户请求与系统模型请求，支持 `request_source` 筛选（`system` 指全部系统来源，`all` 或省略为全部），汇总返回已知成本与 `incomplete_cost_requests`（含费用未知和部分成本）。系统请求仅管理员可见（列表、统计、导出、清理共用筛选），计入提供商成本，排除出用户消费排行与 Ops 流量指标。
- 统计维度与缓存：付款主体与行为主体、用户费用与提供商成本、各模型分别统计。各数据集独立持有缓存 key、TTL、singleflight 与 stale refresh，返回独立副本；`pkg/querycache` 仅管缓存，ETag、304 与响应头归 HTTP 层。
- 同查询合并：管理用量统计、仪表盘图表和组合快照经 `GetOrLoadContext` 合并相同查询。各等待者独立响应取消与截止时间，最后等待者离开即取消底层查询（继承首个调用的上下文值）；无等待者立即移出合并表，已取消查询的迟到结果丢弃。快照加载使用预解析参数，Gin Context 留在 HTTP 协程。
- 日历与时区：日期解析在缺省或无效用户时区时回退服务端时区；纯日期结束值按日历加一天作排他上界（跨夏令时可为 23 或 25 小时），带偏移的明确时间不加一天。公开查询、排行与仪表盘各自保持默认窗口。用量存储与仪表盘共用日历对象（事务派生继承）按命名时区做日分组；多维聚合先用 UTC 桶再按查询时区重分桶；团队日统计用团队日历。
- 水位与实时视图：预聚合仅在闭合时间桶推进 watermark，当前活跃桶不作最终历史；回填、重算或清理后须失效相关缓存并核对覆盖区间。实时 Ops 视图来自短窗口、计数器和 WebSocket，适合排障而非长期审计；页面无数据时需区分监控关闭、筛选条件、覆盖不足、采集丢弃、超时或无流量。

<a id="user_activity_summary"></a>
## 用户最近使用时间

`usage_user_activity` 为每个仍有用户用量记录的用户维护最大 `created_at`（按 `usage_logs.user_id` 归属，含零费用用户记录，排除无用户归属的系统探针与测试）。用户列表、详情和最近使用排序读取此表（无记录返回空时间）；提供商仍使用各自批量更新的 `last_used_at`。

- 事务内触发器：`usage_logs` 语句级触发器在同一事务中维护活动时间（插入合并最大值，删改经 `(user_id, created_at)` 索引重算），按用户 ID 升序取事务锁，随事务一起回滚；不受预聚合开关控制。
- 清空、分区与备份：清空用量同步清空此表。保留期任务经 `usage_drop_partition_with_activity` 删分区并重建汇总；运维手工调整分区后须在同一维护事务中调用 `usage_rebuild_user_activity()`（锁住用量写入并按索引重建）。备份的"用量记录"选项同时包含或排除该表数据。

<a id="data_cleanup"></a>
## 清理与留存

- Ops cleanup：按运行时设置分批清理错误日志、系统日志、指标与聚合、告警、心跳和清理审计，批间暂停以减压；执行前抢任务锁，数据库维护期间跳过。保留天数 `0` 的含义由各目标计划决定，改默认值前须覆盖 truncate 与 delete 测试。
- Usage cleanup：管理员手动创建的持久任务，必填时间范围，可按用户、团队、Key、提供商、分组、模型、请求类型、流式或计费类型筛选。大范围清理前先备份并评估分区、索引、vacuum 与查询影响。
- 清理后重算：凡已删除过记录的清理任务，无论终态为 `succeeded`、`canceled` 还是 `failed`，统一收尾都会登记异步聚合重算（使用聚合运行时 context，不改 `canceled` 状态）。系统无持久化修复队列，进程崩溃不保证投递。
- Usage export 清理：产物默认保留 24 小时（`usage.export_tasks.retention_hours`）。周期扫描到期的 `succeeded` 任务，先删根目录受限本地 CSV（文件不存在视同成功），再将记录置为 `expired`；保留元数据供面板查看，过期下载返回 `410 USAGE_EXPORT_EXPIRED`。
- 审计清空：通用审计清空在单事务内锁表、计数、`TRUNCATE` 并插留痕记录（任一步失败全回滚，序列行为不变），拒绝管理员 API Key 且照常验 TOTP。普通管理审计与 Ops 日志清理留痕尽力写入；支付专项审计与退款事务保持各自保证。

清理前按数据归属把关：

- 观测和分析记录可按容量与合规策略清理，但会缩减历史排查与报表覆盖。
- 计费幂等、余额流水、订单、订阅和审计记录不随 usage 或 Ops retention 自动删除。
- Redis 中的限流、并发、粘性、任务和缓存键依赖各自 TTL 或专用清理器，数据库清理无法替代。
- 对象存储产物与数据库元数据须协调删除：先删元数据会遗留孤儿对象，先删对象会留下无法下载的记录。

## 创作台数据生命周期

创作台分离保存任务元数据与临时素材，详情见[创作台](../domains/creative_studio.md)与[数据归属](../architecture/system_architecture.md#数据归属)：

- 素材不入库：原图、mask、生成图和 prompt 明文仅存 Redis 临时键（默认 TTL 30 分钟），不在 PostgreSQL、数据库备份或普通日志中，恢复数据库不恢复图片。
- 确认与补偿：客户端确认保存后先写 `acked` 元数据再删临时输出键，删除失败由 transient cleanup reconciler 周期重试；取消与失败路径保持 `release_pending` 直至补偿完成。
- 核心指标：重点监控 `provider_succeeded`、`settlement_pending`、`release_pending` 数量、outbox 延迟、lease lost、结算/释放重试、`result_lost` 及 transient cleanup 失败。
- 资金与用量独立：hold、capture、release 幂等记录与 `usage_logs` 按常规资金和统计留存规则处理，不随临时素材 TTL 消失。

## 备份与恢复范围

`backup` 将 PostgreSQL dump 流式写至本地或 S3 兼容存储，配置、记录、定时和恢复均受维护锁保护。新建备份统一命名为 `tr_YYYYMMDD_HHMMSS.sql.gz`（固定 `tr` 前缀）；下载与恢复读取记录中保存的名称与对象键，历史 `sub2api` 前缀备份继续可访问。

- 默认排除组：默认策略排除 `backupContentTableDataGroups` 的四类数据（须手动勾选）：`usage_records`、Ops 日志、专项审计历史与 `runtime_data`。`completed` 仅代表所选内容写入完成。
- 运行态排除项：提供商余额提醒状态与 `usage_export_tasks` 属 `runtime_data`（配置留在提供商 Extra）。本地 `exports/usage` CSV 不在 dump 中，恢复后缺文件的完成任务下载返回已过期。
- `usage_records` 组：同时包含 `usage_logs`、聚合表、`billing_usage_entries`、`usage_billing_dedup` 及归档。恢复后如需保留历史结算明细或请求去重证据须勾选并评估体积。
- 始终包含的插件数据：`plugin_indexes`、`plugin_installations`、`plugin_kv` 和 `plugin_ledger` 始终包含（台账承载幂等证据）。恢复数据库须同步恢复固定 TOTP 加密密钥；本地插件包不在 dump 中（上传来源留原归档，索引来源按已接受摘要重下）。

| S3 上传模式 | 适用场景 | 分卷与失败清理 |
| --- | --- | --- |
| `multipart` | 兼容标准 multipart 签名的对象存储 | 将 gzip 流作为单对象低内存上传 |
| `spooled_put` | 仅可靠接受已知长度 `PutObject` 的兼容服务 | 超 4 GiB 边压缩边封装独立分卷，磁盘仅留当前卷，记录每卷顺序、对象键、大小和 SHA-256；任一卷上传/删除失败或重启时清理已登记对象，清理不全保留可诊断记录 |

- 恢复边界：恢复以备份记录中的 storage type 和 key 为准，切换本地/S3 后仍可恢复旧记录。分卷备份先按序下载到受限临时目录并逐卷校验大小与 SHA-256 后再解压写库（单文件流式恢复）。数据库恢复不恢复 Redis 或对象存储，完整灾备须定义 PostgreSQL、Redis、`DATA_DIR` 与外部对象的同窗口恢复点。
- 加密与保留清理：敏感 S3 配置落库须配置稳定加密密钥，拒绝临时生成密钥。保留清理同时删除产物和记录，失败保留可诊断状态。
- 本地文件与恢复状态：本地备份经受根目录约束句柄核对真实目标（允许根目录内符号链接，拒绝逃逸链接，配置路径不扩权）。`running` 记录登记失败则不启动恢复；数据库已提交而终态记录写入失败时不回滚库变更，详见[备份与维护执行](deployment_and_migrations.md#maintenance_execution)。

相关文档：[运维监控与告警](ops_monitoring_and_alerting.md)、[路由与结算](../domains/routing_and_billing.md)、[部署与数据库迁移](deployment_and_migrations.md)、[配置](../interfaces/configuration.md)、[运维目录](index.md)。
