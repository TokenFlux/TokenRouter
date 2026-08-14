# 部署与数据库迁移

本文记录 TokenRouter 构建产物、运行拓扑、首次初始化、数据库迁移、升级和恢复之间的工程边界。逐命令安装步骤由既有部署手册维护；修改启动装配、镜像、Compose、setup、SQL 迁移或在线更新前应先读本文。

## 章节导航

- [构建与运行形态](#构建与运行形态)：修改产物或部署拓扑时读取。
- [初始化与启动](#初始化与启动)：修改 setup、配置或健康检查时读取。
- [迁移执行](#migration_execution)：修改 runner 或迁移格式时读取。
- [新增与同步迁移](#新增与同步迁移)：创建本 fork 迁移或同步上游时读取。
- [升级与恢复](#升级与恢复)：修改更新、备份或回退流程时读取。

## 构建与运行形态

标准发布先生成一次前端静态资源，再由独立 runner 把同一份前端嵌入各平台 Go 二进制并行交叉编译；最终发布阶段统一归档二进制，并用 Linux `amd64`、`arm64` 产物组装多架构镜像和必要运行时工具。GitHub Release 同时发布 Linux `amd64`、Linux `arm64` 等产物，具体矩阵以 [release workflow](../../.github/workflows/release.yml) 为准。

仓库支持以下运行形态：

| 形态 | 权威入口 | 持久依赖与边界 |
| --- | --- | --- |
| 单二进制/systemd | `deploy/install.sh` | 外置 PostgreSQL、Redis；安装器管理二进制版本和服务 |
| 完整 Compose | `deploy/docker-compose.yml`、`docker-compose.local.yml` | 应用、PostgreSQL、Redis；分别使用命名卷或本地目录 |
| 独立应用容器 | `deploy/docker-compose.standalone.yml` | PostgreSQL、Redis 由部署环境提供 |
| 源码开发 Compose | `deploy/docker-compose.dev.yml` | 本地构建应用并启动配套依赖 |
| Apple Container | `deploy/apple-container.sh` | 独立脚本管理容器、卷和健康状态 |

应用至少依赖 PostgreSQL 和 Redis。`/app/data` 或等价 `DATA_DIR` 保存配置、安装锁及本地运维产物；数据库、Redis 和对象存储各有独立生命周期，不能只备份应用数据目录就宣称完成系统备份。

逐步操作见 [中文部署指南](../guides/deployment/index.md)、[Docker 镜像说明](../../deploy/DOCKER.md) 和 [Apple Container 指南](../guides/deployment/apple_container.md)。这些是部署者手册，不替代本文的工程约束。

管理后台的数据管理功能还依赖一个通过 Unix Socket 通信的可选 `datamanagementd` 进程。本仓库保留主进程客户端、systemd unit 和安装脚本，但当前检出内容不包含 `datamanagement/` 源码目录，因此根 Makefile 的构建目标和安装脚本的 `--source` 模式不能在本仓库单独完成构建。只有在另行取得兼容二进制或完整源码时才应启用；现成二进制的部署步骤见 [datamanagementd 指南](../guides/deployment/datamanagementd.md)。

## 初始化与启动

进程入口先判断是否需要 setup。未安装时可使用 Web setup、`--setup` CLI 或容器的 `AUTO_SETUP`；setup 测试 PostgreSQL/Redis，执行迁移，创建首个管理员，写入配置，最后创建只读安装锁。安装锁用于阻止重新初始化攻击，不能用删除它的方式修复普通配置问题。

正常启动在依赖注入创建 Ent 客户端时再次运行同一套嵌入迁移，因此每个新版本在监听 HTTP 前完成 schema 对齐。迁移或安全密钥初始化失败会使应用初始化失败，不允许带着部分 schema 提供流量。默认的兼容迁移允许多实例滚动启动，并由迁移锁保证只有一个实例执行 SQL；“升级与恢复”中标记为一次性或破坏性的变更优先于该默认规则，必须按专题停机顺序执行。

`GET /health` 是容器健康检查入口。健康响应只能说明当前进程可服务，不能替代升级后的业务抽样、账本核对或后台任务检查。

<a id="migration_execution"></a>
## 迁移执行

`backend/migrations/*.sql` 通过 `go:embed` 编入二进制，文件名是迁移身份并按字典序决定顺序。执行器使用固定 PostgreSQL advisory lock 串行化多实例迁移；`schema_migrations` 记录文件名、去除首尾空白后的 SHA-256 和应用时间。已有文件校验和不匹配时启动失败，只有 runner 中逐文件列出的历史兼容集合可以放行。

普通 `*.sql` 在单个事务中执行，SQL 与迁移记录一起提交或回滚。包含 `CREATE INDEX CONCURRENTLY` 或 `DROP INDEX CONCURRENTLY` 的文件必须以 `_notx.sql` 结尾；该模式逐语句在事务外执行，只接受带 `IF NOT EXISTS`/`IF EXISTS` 的并发索引语句，也禁止 `BEGIN`、`COMMIT` 和 `ROLLBACK`。非事务迁移可能在 SQL 成功但记录写入前中断，因此每条语句必须可安全重放。

首次检测到旧 `schema_migrations` 且缺少 Atlas 记录时，runner 会用当前最后一个迁移建立 `atlas_schema_revisions` 基线。该兼容记录不改变 SQL 文件仍为 schema 权威来源的事实。

迁移是前向且不可变的：

- 已进入任何环境的文件不得修改、删除或改名；修正必须使用新迁移。
- 文件内不能放可执行的 Down 段；runner 不解析 Goose/Up/Down 标记。
- 普通迁移应尽量幂等并在 SQL 中写中文注释解释变更原因和兼容窗口。
- schema、数据回填、Repository 查询和 Ent schema 变化应在同一兼容序列中设计；若明确无法支持新旧二进制共存，必须在本页记录停机升级、备份与回滚步骤。

## 新增与同步迁移

新增文件使用 `<递增数字>_<snake_case 描述>.sql`；并发索引使用 `<递增数字>_<描述>_notx.sql`。仓库历史上存在重复编号和字母后缀，不能据此复用编号。每次创建前都要扫描 `backend/migrations/` 的数字前缀，取当前最大值再加一，并确认按字典序排在预期位置。

本 fork 同步上游时有额外硬约束：上游在 `backend/migrations/` 新增的迁移不能原名照搬。应按上游提交顺序逐个把数字前缀改为本 fork 当前最大编号加一；同时更新测试、runner 特例、文档或其它对原文件名的精确引用。已存在于 fork 的迁移保持原名，不为“整理顺序”重编号。

迁移变更至少验证：

- runner 单元测试，包括事务模式、`_notx` 校验、锁和 checksum；
- 受影响 schema/data migration 测试；
- 从空数据库完整应用，以及在已有 schema 上重复执行；
- `schema_migrations` 文件名与预期一致，未修改既有文件 checksum。

## 升级与恢复

升级前先创建并实际验证 PostgreSQL 备份，同时保存 Redis/对象存储中业务要求恢复的数据。后台备份服务可把数据库 dump 流式写入本地或 S3 兼容存储，并用维护锁串行化备份/恢复；敏感存储配置需要稳定的安全密钥。备份内容策略可能排除大体量历史表，恢复目标必须先核对备份范围。

### Grok 媒体、搜索与 Voice 定价迁移

迁移 `242_group_video_model_prices.sql` 为分组增加可空 JSONB `video_model_prices`，按 Grok 视频模型族和分辨率保存每秒价格；`243_group_audio_voice_pricing.sql` 增加 Realtime 每分钟、TTS 每百万字符和 STT 每小时价格；`244_group_search_price_per_1k.sql` 增加搜索每千次价格。三类价格均以 `NULL` 表示使用代码默认值，显式 `0` 表示免费。管理端和服务层会规范化模型族、拒绝负价，并保持旧 `video_price_*` 作为视频回退层。

迁移 `245_clear_non_grok_video_generation_config.sql` 清除非 Grok、非 Composite 分组的旧视频价格，避免其它平台误宣称视频能力。清理前会一次性创建 `groups_video_price_backup_245`，保存受影响分组的旧列和 JSONB；`CREATE TABLE IF NOT EXISTS` 保证重放不会覆盖首次快照。Composite 可能最终路由到 Grok，因此保留其配置。确认无需恢复后可手工删除备份表；需要恢复时按 `group_id` 从该表回填价格，不能通过删除 `schema_migrations` 记录触发逆向迁移。

这四个文件由上游迁移 217-220 按 fork 当前最大编号重新编号为 242-245。部署后应验证 Grok 分组的模型级视频价、搜索与三类音频价往返，非 Grok 清理范围和备份表内容，以及异步视频在首次完成轮询时只结算一次。

### 分组逐模型定价迁移

迁移 `246_group_model_pricing.sql` 由上游迁移 221 按 fork 当前最大编号递增而来，为 `groups` 增加默认 `TRUE` 的 `long_context_pricing_enabled` 和可空 JSONB `model_pricing`，并把全部存量分组回填为开启。前者只控制内置模型长上下文阶梯，不会压平渠道显式 token 区间；后者保存分组逐模型价卡，结算和模型市场都按“分组 > 渠道 > 内置”解析。新建管理请求省略开关也按开启处理，避免应用层显式写入布尔零值绕过数据库默认值。

该迁移只新增列，可随新版本正常前向执行；但旧实例不理解分组价卡，混跑期间不能开放或修改 `model_pricing`，否则同一分组可能因命中不同版本实例而出现展示与实扣差异。应先完成全部后端升级并确认认证缓存重建，再开放新管理端。升级后至少验证显式免费价、分组覆盖渠道价、关闭内置长上下文、渠道区间仍保留，以及模型市场单价与实际 `ActualCost` 一致。回退旧二进制不会删除新列，但会忽略新配置；需要继续服务时应先停止写入分组价卡或恢复到不依赖该配置的版本状态。

### OpenAI 账号级长上下文计费开关下线

迁移 `241_remove_openai_long_context_billing_toggle.sql` 幂等删除迁移 203 创建的两个账号同步触发器和两个函数，并从所有账号 `extra` 中移除 `openai_long_context_billing_enabled`，保留其它 JSONB 数据。新服务仍把该键视为废弃输入：账号创建、更新、批量更新、导入和 CRS 同步即使收到非法类型也会静默丢弃，不再保存或返回旧校验错误。整份替换语义的单账号更新只携带废弃键时等同未提供 `extra`，不会清空其它配置；显式 `extra:{}` 仍表示清空允许清空的字段，废弃键与有效字段并存时只处理有效字段。账号数据导入会在计算幂等指纹前丢弃该键，因此旧键缺失、任意旧值和非法类型均表示同一逻辑请求。

升级后，长上下文用户价格只由分组逐模型基础价、渠道显式区间、模型内置阶梯、分组长上下文开关和分组倍率决定。渠道显式区间优先且不会重复叠加模型内置倍率，也不受分组开关影响；没有显式区间时，开关决定是否按模型广场公开的长上下文档结算。账号统计和账号 `quota_used` 统一使用 `COALESCE(account_stats_cost, total_cost) × account_rate_multiplier`，显式零账号成本不累计额度；这不改变用户余额、订阅或 API Key 配额继续使用 `ActualCost` 的规则。

这是不支持新旧后端混跑的一次性升级。发布前必须停止接流量并排空全部旧实例，验证 PostgreSQL 备份可恢复，再只启动一个新实例执行迁移；确认触发器、函数和旧键已清理，抽样核对模型广场区间价与实扣一致后，才能扩容其它新实例。旧实例不能连接已迁移数据库，否则可能重新写入废弃键或按旧账号开关产生不同用户价格。

发布说明必须明确：此前关闭账号开关的 OpenAI 请求在超过模型阈值后，会开始按模型广场长上下文价格扣费。仅回退二进制不能恢复旧版精确行为；需要回滚时应停止全部新实例，恢复升级前 PostgreSQL 备份，再启动旧版本，不能通过手工补键或删除迁移记录代替数据库恢复。

### 通用高级调度器迁移

迁移 `238_generalize_advanced_scheduler.sql` 为 `groups` 增加受约束的 `scheduler_type`，默认 `basic`，并把旧 OpenAI 实验调度器转换为按分组选择的通用高级调度器。旧 `openai_advanced_scheduler_enabled=true` 时，仅既有 OpenAI 与 Grok 分组回填为 `advanced`；开关为 false 或不存在时，所有存量分组保持基础。其它平台不会被自动升级，新建分组始终为基础。

迁移会把旧粘性、订阅优先、Top-K 和评分权重设置复制到 `advanced_scheduler_*`，随后删除全部 `openai_advanced_scheduler_*` 键，不保留数据库别名或读取回退。部署前必须把配置中的 `gateway.openai_ws.lb_top_k`、`gateway.openai_ws.scheduler_score_weights.*`、`gateway.openai_scheduler.sticky_escape_*` 替换为 `gateway.advanced_scheduler`；新版本会拒绝旧配置，管理设置 API 也会拒绝旧字段。

迁移 `239_add_group_advanced_scheduler_overrides.sql` 为 `groups` 增加非空 JSONB `advanced_scheduler_overrides`，默认 `{}`，并约束顶层必须是对象。它不修改既有分组模式或全局权重；空对象让所有分组继续继承网关通用参数。升级后管理端可仅为高级分组保存需要偏离全局的字段，认证快照版本会再次提升以避免旧缓存缺失覆盖值。

迁移 `240_remove_account_group_priority.sql` 幂等删除 `account_groups.priority` 以及依赖该列的三个索引。该字段没有完整的产品配置入口，真实调度和模型市场统一使用 `accounts.priority`；迁移后 AccountGroup 只表达账号与分组的成员关系。迁移会先按名称删除历史索引再删除列，既支持完整历史 schema，也支持缺少部分索引的兼容数据库。

这是破坏性的一次性升级，不支持新旧二进制或新旧前端混跑。先停止全部旧实例、备份 PostgreSQL 与配置，再启动一个新实例完成迁移，确认认证快照因版本变化而重建、分组模式和通用设置符合预期，并抽样核对账号仍按全局优先级排序后，再扩容其它新实例。仅回退二进制不能恢复已删除的旧设置或 `account_groups.priority`；需要回滚时应停止新实例并恢复升级前的数据库备份和配置。

### 分组客户端协议迁移

迁移 `235_add_group_allowed_client_protocols.sql` 为 Group 增加非空 JSONB `allowed_client_protocols`，按六个平台在升级前的实际路由行为回填。OpenAI 是否加入 Messages 取自旧 `allow_messages_dispatch`；其它已有平台按各自迁移矩阵回填。旧列作为弃用管理 API 字段的数据库镜像保留，不用于支持新旧二进制共存。

数据库默认值是空数组，作为绕过管理服务直接写 Group 时的 fail-closed 默认值。空数组对所有平台都是明确且有效的策略，新代码不会按旧矩阵恢复或自动补协议；管理 API 创建字段缺省时仍使用各平台的新建默认值。

本变更按一次性升级发布，不支持新旧后端或前后端混合运行。认证快照版本随字段增加而升级，部署前遗留的 Redis 快照会因版本不匹配失效并从已回填数据库重建。完成升级后至少抽样验证 OpenAI 旧开关 true/false、任意平台空集合以及 Gemini Responses 的非流和 SSE 请求。

### 上游声明倍率探测下线

迁移 `236_remove_upstream_billing_probe.sql` 幂等删除账号 JSONB 中的 `upstream_billing_probe`、`upstream_billing_probe_enabled`，并删除设置 `upstream_billing_probe_settings`、`openai_low_upstream_rate_priority_enabled`、`openai_oauth_scheduling_rate_multiplier`、`openai_advanced_scheduler_weight_upstream_cost`。迁移不改动其它账号 extra 或设置。旧配置项 `gateway.openai_ws.scheduler_score_weights.upstream_cost` 已失去行为，升级前应从配置文件、Secret 和环境模板中移除。

这是无兼容路由和弃用期的破坏性升级。发布时先停止并确认全部旧实例退出，再备份 PostgreSQL 和旧配置，然后启动一个新实例完成迁移，最后扩容其余新实例；禁止新旧二进制混跑，否则旧进程可能重新写回已删除数据。`GET /v1/sub2api/billing` 和全部 `/api/v1/admin/accounts/*upstream-billing-probe*` 路由在新版本上返回普通 `404`。

不扫描或清理 Redis。遗留探测 leader lock 按原有 2 分钟 TTL 自然过期；这不会恢复任何探测任务。升级后应确认迁移可重复执行、无关账号 extra 和设置保持不变、Ollama Cloud/额度/endpoint capability 探测正常，以及声明倍率不再影响账号排序或评分。

只回退二进制无法恢复已删除的快照与设置。需要回滚时，先停止全部新实例，恢复升级前 PostgreSQL 备份和旧配置，再启动旧版本；不得通过手工删除迁移记录或让旧实例在已迁移数据库上重建历史数据。

### API Key 结算模式与批量图片快照

迁移 `237_add_api_key_billing_modes.sql` 为 `api_keys` 增加非空 `billing_mode`（默认 `auto`）和可空 `preferred_subscription_id`，并在 `batch_image_jobs` 增加同名的提交时结算快照列。它是纯新增列迁移，不需要为存量 Key 回填：旧记录自动保持订阅优先、余额兜底的历史行为；旧批量任务也按 `auto` 兼容结算。

迁移完成后，认证缓存版本会使旧快照失效并重建，避免缓存缺少结算字段。SQL 列本身与旧二进制兼容，但在同一部署中不能让旧实例继续处理用户新配置的指定订阅或仅余额 Key；应先完成全部后端实例升级，再在面板开放该配置。升级后至少抽样验证个人和团队 Key 的订阅选择、套餐受限分组拒绝、指定订阅额度耗尽不扣余额、仅余额不使用订阅，以及批量图片提交后修改 Key 配置仍按提交快照冻结/结算/释放。

### 自研异步图片任务下线

包含迁移 `234_remove_async_image_storage_setting.sql` 的版本会立即移除自研 OpenAI/Grok 异步图片路由、后台对象存储设置和 `image_storage_config` 数据库记录。这是破坏性升级，不提供任务排空、兼容查询或旧任务恢复。发布 tag notes 必须明确列出这项变化。

升级前先记录旧异步图片设置使用的 bucket 和 prefix，并从配置文件、Secret 管理及部署环境中移除 `image_storage` 和 `IMAGE_STORAGE_*`。旧进程会缓存已解析的对象存储客户端，因此不能与新版本滚动重叠：应先把全部旧实例移出流量并停止，再启动新版本。

历史 Redis `image_task:*` 键按原有最长 24 小时 TTL 自然过期，不执行全库扫描或清空。历史 S3/R2 图片不会自动删除；升级后先列举或 dry-run 旧前缀，确认它不与 `backups/` 或其他业务前缀重叠，再由运维使用对象存储工具定向删除。迁移完成后数据库不再保存旧存储位置，因此记录 bucket/prefix 必须发生在升级前。

旧任务 ID 在新版本上直接返回普通 `404`，在途进程内任务随旧实例停止而终止。回滚旧二进制不会恢复已删除的后台设置；只有显式恢复旧配置才能重新启用旧版本功能。

在线更新和安装脚本可以保留上一版二进制或镜像，但这只是应用回退。数据库迁移不会因镜像回退自动撤销；上线前必须确认新迁移对旧版本是否向后兼容。若 schema 已不兼容，应使用经过演练的数据库备份恢复或新增前向修复迁移，而不是手工删除 `schema_migrations` 记录。

升级完成后至少检查 `/health`、登录/API Key 鉴权、一个非流和流式网关请求、用量结算、关键后台任务及迁移表。保留旧产物和升级前备份，直到这些检查完成。

相关文档：[系统架构](../architecture/system_architecture.md)、[配置边界](../interfaces/configuration.md)、[运维目录](index.md)。
