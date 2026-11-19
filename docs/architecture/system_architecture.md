# 系统架构

本文说明 TokenRouter 单实例部署的运行组件、依赖层次、启停顺序、数据归属和故障降级约束。包职责和依赖图见[后端模块地图](backend_modules.md)，单次请求处理顺序见[网关请求生命周期](gateway_request_lifecycle.md)。

## 章节导航

- [运行组件](#运行组件)：进程内组件、外部依赖与前后端边界。
- [依赖层次](#dependency_layers)：分层职责、模块边界、事务协作与配置加载。
- [启动与关闭](#startup_and_shutdown)：初始化步骤、后台服务时序、关闭顺序与组件停止行为。
- [数据归属](#数据归属)：PostgreSQL、Redis、本地目录与对象存储的分工。
- [HTTP 与前端交付](#http-与前端交付)：HTTP 超时、中间件顺序与嵌入式 SPA 交付。
- [单实例部署与故障处理](#deployment_and_failures)：升级流程、锁机制与降级边界。
- [备份与系统维护装配](#backup_and_maintenance)：备份、系统维护与 setup 边界。

## 运行组件

TokenRouter 部署为一个 Go 后端进程和一个 Vue 前端实例，前后端同版本整体升级。后端提供面板 API、AI 网关、支付回调、健康检查和后台任务；使用 `embed` 构建标签时同时托管 Vue SPA。外部强依赖为 PostgreSQL 和 Redis，大对象和导出文件按功能存放于本地数据目录或 S3 兼容存储。

```text
浏览器 / AI 客户端 / 支付回调
                |
        边缘代理或直接 HTTP
                |
      Gin server + 全局中间件
       /        |          \
面板/API    网关处理器    后台任务
       \        |          /
       各模块业务用例
          |             |
 模块存储/基础设施   上游供应商
       |       |        |
 PostgreSQL  Redis   对象存储/HTTP
```

前端通过 `frontend/src/api/` 调用 `/api/v1`，使用 Pinia 保存浏览器会话状态，路由守卫按公开设置、当前用户、角色和功能开关决定页面入口。权限判定以服务端路由和中间件为准，前端隐藏仅影响展示。

<a id="dependency_layers"></a>
## 依赖层次

`backend/internal/app/wire.go` 是应用依赖图的手写入口（`wire_gen.go` 为生成产物），按模块拆分为 `assembly_*_wire.go` 集合，生命周期登记独立归组。包目录和关系图见[后端模块地图](backend_modules.md)。

| 层 | 主要职责 |
| --- | --- |
| `cmd/server`、`config`、`app/bootstrap` | 命令行参数、构建版本、配置加载、数据库引导、迁移和持久密钥 |
| `app`、`app/lifecycle` | 生产实例装配、配置分发、接口绑定、启停编排和失败回收 |
| 业务模块 | 按身份、提供商、路由、资金、任务等主题实现用例与规则 |
| 模块内 `httpapi`/`postgres`/`rediscache`/`provider` 子包 | 协议适配、存储与外部服务集成，实现核心声明的接口 |
| `gateway`、`upstream`、`protocol` | 入站编排、与供应商交换报文、报文格式与转换规则 |
| `infra`、`pkg` | 技术基础设施、通用值类型与计算工具 |

### 模块边界与状态共享

- 单实例共享运行态：设置存储、幂等协调器、提供商健康状态/快速阻断/短暂失败/快照节流、Agent Identity 协调器（已持久化提供商共用进程内按提供商锁，未持久化提供商由入口自管互斥）及搜索在途计数均在进程内共享。选号、固定提供商的 WS 复核和管理端诊断使用同一套资格与反馈规则。
- 网关异步记录边界：完成记录器（Recorder）的输入在请求期间固化为副本，异步任务只读副本，Gin Context 留在请求内。平台分派、部分结果和重试规则见[网关请求生命周期](gateway_request_lifecycle.md)。
- 通知与站点边界：通知模块接收上游已确定好的事件（验证码和重置凭据由 `identity` 生成，余额与额度阈值由 `billing` 和 `provider` 判定）。公告、公开设置和页面权限归 `site`，`web` 读取其公开输出。各模块专题见[模块导航](backend_modules.md#module_topics)。
- 纯计算与 I/O 分离：`protocol` 管报文与转换状态，`routing/capability` 与 `routing/modelmap` 管协议准入、转换与模型匹配，`billing/pricing` 只做查价算费，`modelcatalog` 管模型目录热更新。I/O 由调用方完成，平台别名和动态 Grok 默认值每次查价只取一次快照传入纯定价代码。
- 模型隔离与事务协作：管理端 DTO、脱敏候选数据和执行目标（`provider.Record` + `AttemptRoute`）保持独立结构；配置更新只写配置字段，不覆盖独立消费与运行字段。资金结算以 `billing` 事务提交为准（用量记录、订单状态和通知均为旁证）；身份、团队、支付、创作台和批量图片等外层事务将资金或权益操作纳入同一数据库事务，退款的外部渠道调用位于两个短事务之间。
- 领域错误与依赖门禁：领域错误由所属模块定义，跨模块与 HTTP 映射传递原错误链并用 `errors.Is`/`errors.As` 判断（不可按文案新建同名错误）。包依赖受 `tools/architecture/` 的 arch-go 测试约束，见[依赖规则](../operations/development_workflow.md#backend_dependency_rules)。
- MCP 管理边界：MCP 与普通管理 HTTP 共用业务 handler；全部管理路由挂载后校验操作目录，目录不完整时中止启动。内部请求通过私有 context 携带来源身份，进入管理链重新核对权限，见 [MCP 管理接口](../interfaces/mcp_management.md)。

### 传输与配置加载

- 上游传输：`infra/httpclient.UpstreamPool` 维护客户端缓存和请求占用计数，成功请求在响应体关闭时归还占用。OpenAI HTTP/2 回退由 egress 处理；Grok CLI Header 与可重放 403 策略由 `upstream/grok` 处理；隐私请求共用 30 秒超时、Chrome 指纹的 req 客户端池。
- 配置与生成：`cmd/server` 负责参数解析、版本变量和 `go generate ./cmd/server`（改 Wire provider 无需重跑 Ent 生成）。配置只加载一次，依次执行版本升级、未知键检查和环境引用解析，数据库引导补齐持久 JWT secret 后复检。依赖图构造前发布 Anthropic、Antigravity 和 Grok 启动身份参数。

<a id="startup_and_shutdown"></a>
## 启动与关闭

### 启动顺序

主入口优先处理 `-version`、`-migrate-config` 和 `-setup`。正常启动时，尚未安装走 `AUTO_SETUP` 或独立 setup server，已安装则构造完整应用；setup 迁移使用精简 bootstrap，不构造业务 worker。

完整应用初始化顺序：

1. 初始化日志。
2. bootstrap 初始化时区（默认上海，生产代码仅 `app/bootstrap.InitTimezone` 写 `time.Local`）和 PostgreSQL，在 10 分钟总预算内执行迁移并重试瞬时错误。
3. 补齐持久 JWT secret 并完整校验配置。首次创建的管理员默认并发为 5（已有管理员保持不变），默认分组由管理员自行创建。
4. Ent 与底层 `*sql.DB` 共用连接池，由单一所有者负责关闭。
5. 固定进程级共享 `Calendar`（供用量、聚合、仪表盘按日缓存、团队日统计、资金结算、Key 按日计数和站点时区展示共用；用户自定义时区与无效值回退由各入口处理，团队查询使用服务端时区）。

对象构造和资源登记完成后，lifecycle 按三阶段启动后台工作：

1. 时间轮、运行设置与定价预热（`RuntimeSettingsInitialization` 先于 `ModelCatalogInitialization` 执行，加载转发设置并完成 Grok 默认模型迁移）。
2. 缓存订阅与消费队列。
3. 周期性生产者、任务拉取与 HTTP 服务。

各模块的首次执行时机、预热降级、功能开关和动态 worker 数由模块自行决定。构造或启动中途失败时，回收已获取和已启动的资源，错误链保留最初根因。

### 关键后台服务时序

| 组件 | 启动与停止顺序 | 关键行为与预算 |
| --- | --- | --- |
| 模型目录（`ModelCatalogService`） | 排在 `RuntimeSettingsInitialization` 之后 | 日志名 `service.modelcatalog`，提供统一模型目录及热更新 |
| 提供商余额告警（`BalanceAlertMonitor`） | StartOrder `980`，StopOrder `20`（共享上游查询在 StopOrder `25` 关闭） | 取消并等待检查、发信与结果落库，取消后落库保留独立 5 秒预算；运行状态属备份 `runtime_data` |
| 订阅过期提醒 | 接入生命周期，启动后立即首轮 | 之后每分钟扫描，执行前抢占 Redis 或数据库任务锁；停止时取消并等待在途操作 |
| `bStats` 上报服务 | StartOrder `980`，StopOrder `20` | 构造期不采集不上报，读到有效设置并持久保存实例 UUID 后启动计时器；初始化失败不阻断启动；关闭先等上报完成再释放设置和日志。综合设置提交后更新四项控制，开关串行切换，日志更新只替换快照不重置周期。见 [bStats 安装统计](../interfaces/configuration.md#bstats_reporting) |
| 插件体系（`PluginCatalog`/`PluginKV`/`PluginRuntime`） | StartOrder `974` / `975` / `976`，均在 StopOrder `27` 停止 | Catalog 确保内置索引并管后台刷新；KV 管采样和过期清理；Runtime 异步恢复持久化安装与显式开发目录，停止时取消定时任务、排空并终止插件进程（预算 8 秒）；插件回调依赖的共享服务在之后关闭。见[插件系统](plugin_system.md#leases_and_callbacks) |

### 关闭顺序

SIGINT、SIGTERM、监听失败和 Linux 手动重启共用同一关闭流程。HTTP 优雅关闭预算 5 秒，之后的后台清理总预算 30 秒，两个预算分开计算。

后台清理顺序：

1. 关闭额外监听、Live 本地观察和被 hijack 的连接（`OpenAILiveObservers` 阶段 5，Responses WS 设置刷新阶段 9，`OpenAIWSConnections` 阶段 10）。
2. 等待所有 HTTP handler 返回（请求跟踪保留 `Flush` 和 `Hijack`；客户端断开后继续统计用量的 handler 完成后才算结束）。
3. 停止周期性生产者和任务拉取。
4. 逐层排空用量、缓存写入、延迟写回、通知和审计（`lifecycle.Tasks` 统一等待派生副作用，早于邮件和共享存储关闭）。
5. 关闭订阅、时间轮、空闲 HTTP 连接、Redis、Ent/SQL 和日志文件（lumberjack 文件句柄最后关闭，其内部维护循环随进程退出）。

关闭纪律与超时语义：

- `Stop` 和 `Cleanup` 共享同一次执行结果。任一预算超时时报告未完成任务、停止关闭仍被依赖的资源，进程以失败状态退出（退出时 drain 可能未完成）。
- 新增 goroutine、定时器、队列或连接须静态登记所有者、启动点、停止接收方式和完成等待；禁止业务模块在运行期反向向 `app` 注册新组件。

### 各组件的停止行为

| 组件 | 停止与排空规则 |
| --- | --- |
| 搜索 | HTTP 开放前初始化，各配置版本共用在途计数；换配置仅关旧客户端空闲连接、在途继续；关闭时拒绝新搜索并等待额度清理 |
| 审核与邮件 | 审核先关队列再等待；邮件队列在通知生产者后排空，预算用完取消 SMTP 并报告未完成邮件 |
| 任务 HTTP 与创作台 | `TaskRequestsAndDownloads` 屏障在 HTTP 退出后拒新调用、等已有调用，再停拉取和恢复循环（worker 停止后不可重启，重复停止返回同结果）；创作台生成阶段占用的用户槽和提供商槽按相反顺序释放；超时时保留下游共享依赖打开 |
| 身份、API Key 与认证缓存 | 统一注入系统时钟（JWT 校验和签发共用），团队和成员额度按配置时区处理夏令时。认证缓存调用 `Start` 才建 L1 和 Redis 订阅；停止时拒新认领，等在途认证、活动写入和失效完成后再关订阅和 L1；outbox worker 停领新任务、等当前批次，未完重试和延迟二次失效留待下次启动。钉钉资料同步独立于请求取消，单任务预算 30 秒 |
| Live、WS 与运维错误日志 | Live 仅关闭本地资源（远端状态以远端为准）；错误透传、TLS profile/router 和鉴权缓存订阅等最后回调结束；运维错误日志队列处理完已入队批次；QPS WS 缓存空闲定时器和按需刷新在 HTTP 收尾后停止；WS 池关闭后不重建或预热，已租出连接归还时释放 |
| 调度 | 构造时不启动，首次快照重建异步（重复启动跳过，停止后不可重开）；所有 handler 结束后停领新请求、取消等待并等待在途资源释放，再关 Redis/SQL；遗留持久 outbox 由下次启动消费或周期重建处理 |
| 用量、审计与 Ops | usage 聚合器取消运行 context 和重试等待、拒新重算、等已开始工作完成；审计和系统日志 sink 重复 `Start` 复用 worker，`Stop` 后不可重开；Ops 错误采集队列首次入队启动，实时采样空闲停止与应用停止作用于同一实例 |

## 数据归属

| 存储 | 存什么、怎么用 | 失败或丢失的影响 |
| --- | --- | --- |
| PostgreSQL | 用户、身份、团队、Key、分组、价格配置、提供商、设置、订单、订阅、持久任务、用量和审计的主数据 | 连接、迁移或密钥初始化失败时完整应用无法启动；写入失败如实返回错误 |
| Redis | 缓存、限流、并发槽、会话和粘性、任务锁、调度快照、队列、缓存失效通知，以及少数带 TTL 的短期任务 | 按功能决定：安全入口 fail-close 拒请求，调度按设计回源，缓存可重建，在途短期任务可能丢失 |
| 本地数据目录 | 定价快照、日志、短期用量导出 CSV、前端覆盖文件和部分部署配置 | 归属当前部署，升级需保留；容器部署须挂载持久卷并纳入备份 |
| S3 兼容存储 | 备份等可选大对象 | 备份客户端按运行时设置构造，改凭据立即生效；对象可用性需与数据库元数据同步管理 |
| Google Cloud Storage | Vertex 批量图片的输入、输出和中间 JSONL | 由 Vertex 批量图片 provider 按作业前缀管理，内部 URI 对 API 用户隐藏 |
| 创作台临时数据 | Redis 键 `creative:payload:`、`creative:input:`、`creative:mask:`、`creative:output:`（默认 TTL 30 分钟）和 `creative:queue:*`；PostgreSQL 表 `creative_runs`、`creative_run_outputs`、`creative_run_outbox` | 素材和 prompt 明文只存 Redis，不在数据库备份中；临时输出过期无法恢复并标 `result_lost`；客户端确认先写元数据再删输出键，删除失败由 reconciler 补做 |
| 上游供应商 | 模型推理、OAuth、配额和供应商侧任务 | 由平台适配器、提供商状态和故障转移处理，瞬时错误记为瞬时状态 |

Ent schema 是主要实体的代码模型，已部署数据库结构以手写 SQL 迁移为准。各模块 PostgreSQL 适配层共用同一连接池，结合 Ent 和底层 `*sql.DB` 完成复杂聚合、批量更新和手动事务。

## HTTP 与前端交付

- Server 超时与协议：统一配置监听地址、请求头大小限制、header/idle 超时、可选全局请求体限制和 h2c。长连接 SSE 和 WebSocket 要求全局 `WriteTimeout` 留空，大请求体要求全局 `ReadTimeout` 留空，细粒度限制由路由或上游客户端控制。`settings/composite` 组合各模块读写应用，自身不缓存。
- 中间件与路由顺序：Gin 中间件依次为 Recovery -> 可信代理 -> 全局日志 -> 客户端指纹 -> CORS -> CSP -> Server-Timing -> 可选嵌入前端；随后注册健康检查、`/api/v1` 和无前缀网关入口。嵌入前端放行 API 和协议路径；`/models` 按方法、认证、查询参数和 `Accept` 区分页面与 API。
- 前端静态交付：`embed` 构建从 `backend/internal/web/dist/` 提供静态资源，注入公开设置和 CSP nonce，`data/public` 可覆盖静态文件；社交分享标签在语言缓存外补齐请求地址并计入 ETag（见[社交分享](../interfaces/configuration.md#社交分享)）。非 `embed` 构建不注册 SPA 中间件，可分离部署。

<a id="deployment_and_failures"></a>
## 单实例部署与故障处理

- 停机升级流程：升级时停止接收新流量，按关闭流程停止旧后端并确认在途请求和后台任务结束，前后端一起切换版本，再启动后端执行迁移和初始化，验证后恢复流量。备份和回退见[部署与数据库迁移](../operations/deployment_and_migrations.md#升级与恢复)。
- 迁移与 Redis 兼容：数据库迁移使用 PostgreSQL advisory lock 保证串行互斥。调整调度快照、缓存失效、限流、并发槽或任务锁的 Redis key、TTL、Lua 逻辑时，须兼顾持久状态、在途任务和升级恢复。
- 缓存与回源：存储缓存命中后仍执行运行时资格检查；调度缓存未就绪或不可信时按对应服务设计回源。
- 初始化降级边界：数据库、迁移、最终配置校验和 HTTP server 构造失败直接终止启动；远程定价初始化失败记录警告并降级使用本地数据。新增降级逻辑须确认不放宽认证、计费或 SSRF 防护。
- 后台任务并发：后台任务按数据库锁、Redis 锁或进程内运行标记控制并发，失败重试与恢复由所属模块处理。
- 跨模块运行态：`EgressPolicy`、`RoutePlan` 和 `ProviderSnapshot` 跨请求提供独立副本，候选协议和提供商映射每次 attempt 重算；`provider_groups`、代理联动和提供商资金重置在共享事务内协作，SQL、Redis 和 HTTP 连接池各保持唯一实例。详情见[路由与结算](../domains/routing_and_billing.md)、[提供商维护](../operations/provider_maintenance.md)和[上游传输安全](../operations/upstream_transport_security.md)。

<a id="backup_and_maintenance"></a>
## 备份与系统维护装配

- 关闭顺序约束：备份核心、动态存储工厂、系统维护用例（`ops/maintenance`）和系统操作锁在进程内保持唯一实例。关闭时维护任务先停止认领并取消，再等待 HTTP 请求；维护收尾和备份任务全部完成后，才关闭共享 SQL 和 Redis。
- `setup` 与维护命令：`setup` 调用精简 bootstrap 完成连接测试、迁移和首次管理员创建，不创建默认分组，且绕开普通注册与管理用例，不触发赠送、通知或后台 worker。维护命令返回前关闭已打开连接，退出码由 `main` 决定。

相关入口：[项目总览](../project_overview.md)、[架构目录](index.md)、[运维目录](../operations/index.md)。
