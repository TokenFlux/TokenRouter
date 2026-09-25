# 系统架构

本文说明单个应用实例的组件、依赖装配、启停顺序和数据所有权。包职责与依赖图见[后端模块地图](backend_modules.md)，单次请求的处理顺序见[网关请求生命周期](gateway_request_lifecycle.md)。

## 章节导航

- [运行组件](#运行组件)：识别进程内外边界。
- [依赖层次](#依赖层次)：修改 Wire 和模块所有权时读取。
- [启动与关闭](#启动与关闭)：修改初始化、后台服务或资源释放时读取。
- [数据所有权](#数据所有权)：判断 PostgreSQL、Redis 和对象存储的职责。
- [HTTP 与前端交付](#http-与前端交付)：修改 server、路由或嵌入式前端时读取。
- [多实例与故障边界](#多实例与故障边界)：修改锁、缓存或降级策略时读取。
- [备份与系统维护装配](#backup_and_maintenance)：修改备份、系统操作或精简初始化的装配时读取。

## 运行组件

发布形态以一个 Go 应用进程为中心。该进程同时承载面板 API、AI 网关、支付回调、健康检查和多类后台运行时；启用 `embed` 构建标签时还承载 Vue SPA。PostgreSQL 与 Redis 是正常业务模式的外部依赖，大对象和导出文件再按功能选择本地数据目录或 S3 兼容存储。

```text
浏览器 / AI 客户端 / 支付回调
                |
        边缘代理或直接 HTTP
                |
      Gin server + 全局中间件
       /        |          \
面板/API    网关处理器    后台运行时
       \        |          /
       各模块业务用例
          |             |
 模块存储/基础设施   上游供应商
       |       |        |
 PostgreSQL  Redis   对象存储/HTTP
```

前端通过 `frontend/src/api/` 调用 `/api/v1`，通过 Pinia 保存浏览器会话状态，并由路由守卫根据公开设置、当前用户、角色和功能开关控制页面访问。安全与授权仍由后端路由和中间件执行，前端隐藏页面不构成权限边界。

<a id="dependency_layers"></a>
## 依赖层次

`backend/internal/app/wire.go` 是完整应用依赖图的手写入口，`wire_gen.go` 是生成结果。根入口引用按模块划分的 `assembly_*_wire.go` 集合；具体提供者和跨模块接口绑定在同一集合内，生命周期登记使用独立集合。包目录、职责和关系图见[后端模块地图](backend_modules.md)。

| 层 | 主要职责 |
| --- | --- |
| cmd/server、config、app/bootstrap | 参数与版本信息、配置加载、数据库引导、迁移及持久密钥 |
| app、app/lifecycle | 组合生产实例、配置投影、端口绑定、启停和失败回收 |
| 业务模块 | 按身份、账号、路由、资金、任务等主题维护用例与规则 |
| 模块内 HTTP、PostgreSQL、Redis、provider | 处理协议、存储和外部能力，向核心实现所需端口 |
| gateway、upstream、protocol | 分别负责入站编排、供应商交换和报文/转换规则 |
| infra、pkg | 提供技术资源、通用值类型和计算工具 |

### 装配与共享实例

app 构造 settings.Store，并把同一存储交给各领域设置读取器。公开站点信息由 site 投影，综合设置由 settings/composite 组合读取、准备、保存和应用。幂等 HTTP 处理器显式绑定同一个协调器；协调器和清理任务共用 SQL 连接池，各自接收日志观察出口。

账号、路由、调度、计费与用量的存储和缓存也由 app 构造。账号健康、快速阻断、模型短暂失败与快照节流共享实际拥有者；选择、固定账号 WS 复核和管理诊断复用资格规则与调度反馈。Agent Identity 协调器只构造一次，已持久账号共享进程内按账号锁，各入口分别管理未持久账号的互斥。

OpenAI 文本、Responses、WS、Images 和辅助执行器共用请求构造、响应输出、凭据与连接资源。Messages、Google 和 Grok 执行适配分别连接所属平台。app 先构造完成 Recorder，再绑定执行器；完成输入在请求期冻结，异步任务不读取 Gin Context。平台分派、部分结果和重试边界见[请求生命周期](gateway_request_lifecycle.md)。

通知接收业务模块已确定的事件；identity 持有验证码和重置凭据，billing/account 判断阈值。search 的配置、注册表与在途跟踪在网关间共享。site 拥有公告、公开设置和页面权限，web 消费公开投影；这些职责的专题入口见[模块导航](backend_modules.md#module_topics)。

### 规则与存储边界

`protocol` 维护协议值、报文与转换状态，`routing/capability` 判断原生协议、准入与单步转换，`routing/modelmap` 提供模型匹配，routing 决定路由与 effort 映射。`billing/pricing` 负责纯查价和费用计算，billing/provider 加载目录并维护热更新；纯协议和定价算法不执行配置读取或 I/O。

`gateway/provider.ExecutionAccount` 组合 `account.Record` 与当次 `AttemptRoute`，不参与数据库或缓存编码。管理 DTO、无凭据候选与执行目标使用不同投影。配置、CAS、outbox、消费累计和快照编码分别由所属存储与规则实现维护，app 只绑定端口和转换投影。

普通结算通过 billing/postgres 的闭合事务完成。身份、团队、支付和任务等外层事务使用明确的同连接参与能力；参与者不自行提交或发布成功副作用。creative 与 batchimage 独立管理任务、输出和完成资格，共享 billing.Funds；退款的外部渠道调用放在短事务之间。用量记录、订单状态与通知结果不能代替资金提交事实。

领域错误由所属模块定义，HTTP 映射和跨模块判断保留错误对象、`errors.Is/As` 与错误链，不能用同文案的新错误替代。接口、存储和后台代码的允许依赖由 depguard 按角色及具体文件约束，见[开发规范](../operations/development_workflow.md#backend_dependency_rules)。

### 传输与生成入口

`app/http_transport.go` 投影传输参数，`gateway/provider/transport` 组合请求策略和平台传输能力。`infra/httpclient.UpstreamPool` 持有客户端缓存与请求占用，成功请求在响应体关闭时归还占用。OpenAI HTTP/2 回退归 egress，Grok CLI Header 与可重放 403 策略归 upstream/grok；隐私请求使用共享 req 池、30 秒超时及 Chrome 指纹。

`cmd/server` 保留参数解析、构建版本变量及 `go generate ./cmd/server` 入口。配置只加载一次，日志、数据库引导与应用图共用它；JWT secret 在数据库引导后补齐并重新校验。修改 Wire provider 后通过该生成入口更新结果，纯装配变更不运行 Ent 生成。

<a id="startup_and_shutdown"></a>
## 启动与关闭

主入口先处理 `-version` 和 `-setup`：前者只输出构建信息，后者执行 CLI 安装。正常启动时，未安装则执行 AUTO_SETUP 或启动独立 setup server，已配置则构造完整应用。setup 的迁移调用精简 bootstrap，不构造业务 worker。

完整应用先初始化日志，再由 bootstrap 初始化时区和 PostgreSQL，在原十分钟迁移预算内执行迁移与暂时错误重试，补齐持久 JWT secret、完整校验配置，并在 simple 模式补齐默认分组和管理员并发。Ent 与 SQL 共享连接，只有一个关闭拥有者。

app 在 bootstrap 成功后固定共享 Calendar，显式传入用量、支付、推广、团队 HTTP 和计费、账号、路由装配。用量存储、聚合事务、仪表盘按日缓存、团队日统计、平台额度存储、资金结算、Key 按日计数与公开站点时区展示均使用注入的日期对象。用户时区覆盖及无效值回退仍按各入口原有规则执行，团队查询保持只使用服务端时区。

`pkg/timezone` 提供 Calendar 与时间文本解析。生产中只有 `app/bootstrap.InitTimezone` 写入 `time.Local`，保留默认上海时区、错误文本和初始化顺序。

Wire 构造对象并登记资源后，lifecycle 才启动后台工作。时间轮和设置/定价预热先完成，再启动缓存订阅及消费队列，最后启动周期生产者、任务拉取和 HTTP。原有首次执行、预热降级、功能开关和动态 worker 数量保持各模块语义。构造或部分启动失败时回收已取得及已尝试启动的资源，错误链保留原始原因。

定价 provider 由 `app/pricing.go` 投影独立 Options，按初始化、启动、停止的顺序接入生命周期。远端客户端和运行实例直接使用 billing/provider。Calculator、PriceResolver 和 ChannelService 也由 app 直接提供实例，供计算与查价消费者共享。平台模型别名和动态 Grok 默认值由 gateway/provider/modelidentity 投影，每次查价只取得一次快照；纯定价不读取平台运行状态。Key 与渠道模型追踪由 gateway/modeltrace 组合。

billing 的余额/Key 缓存队列、平台额度 flusher 和订阅过期提醒由 app 绑定到现有生命周期。提醒保留立即首轮、每分钟扫描和既有 Redis/数据库 leader 策略，停止时取消并等待在途操作。未接入生产图的订阅维护队列不启动。

搜索运行时先于 HTTP 开放初始化，所有配置代次共享在途计数；配置替换只退役旧客户端的空闲连接，不取消已进入的搜索。关闭时停止新搜索并等待额度清理。审核先封闭队列并等待；邮件队列在通知生产者之后排空，预算耗尽会取消 SMTP 并报告未完成项。HTTP 五秒与后台三十秒总预算保持独立，超时不能被报告成排空成功。

任务 HTTP 的提交、下载与管理调用由 TaskRequestsAndDownloads 关闭屏障跟踪。HTTP 退出后先封闭新调用、等待已有调用，再停止任务拉取与恢复循环。worker 停止不可逆，重复停止共享结果；创作台生成阶段的用户/账号槽由 scheduler.Lease 逆序释放。任何阶段超时都保留后续共享依赖，不能把预算结束描述成任务全部完成。

identity 的会话、TOTP、资料操作和 pending 存取，以及 apikey 的过期、活动时间、滥用限制与 outbox 取时，由 app 在构造时注入系统时钟函数。各原取时点继续独立读取，JWT 库内验证与签发使用相同来源；团队与成员额度的日期对象继续保留原时区和 DST 边界。

认证缓存构造不启动后台任务，Start 才创建 L1 和 Redis 订阅。停止时先拒绝新的认证认领，等待在途认证、活动时间写入和失效调用，再关闭订阅与 L1；预算超时保留正在使用的依赖并报告未完成。outbox worker 停止新认领并等待当前批次，持久化重试及延迟二次失效留待后续启动，不宣称全部排空。钉钉资料同步使用统一任务跟踪器，继续与请求取消解耦并保留 30 秒单任务预算。

SIGINT、SIGTERM、监听失败和 Linux 手动重启进入同一关闭流程。HTTP 有独立五秒优雅关闭预算，随后后台清理使用独立三十秒总预算：先关闭额外监听、Live 本地观察与 hijack 连接，等待完整 handler 返回，再停止周期生产者和任务拉取，逐层排空用量、缓存写入、额度镜像、延迟写回、通知和审计，最后关闭订阅、时间轮、空闲 HTTP 连接、Redis、Ent/SQL 和日志文件。

请求跟踪只包装 Handler，保留原 ResponseWriter 的 Flush/Hijack 能力；客户端断开后仍按原策略收集用量的 handler 必须先完成，不能直接以连接断开替代请求清理完成。现有异步额度写入、通知、探针和快照任务通过消费者侧的完成接口登记，保留其 context、并发和参数求值语义；每层关闭后等待该层派生的副作用，再关闭下层依赖。OpenAILiveObservers 在阶段 5 停止，OpenAIWSConnections 在阶段 10 关闭。

Live 观察停止只关闭本地资源，不因进程退出提前把远端会话判定为已结束。错误透传、TLS profile/router 和鉴权缓存订阅会等待最后一次回调结束。运维错误日志队列会处理完已入队批次；QPS WS 缓存的空闲定时器和按需刷新也在 HTTP 收尾后停止。WS 池关闭后禁止按需重建或重新预热，已租赁连接保留原请求收尾策略，在租约归还时释放。

Stop 和 Cleanup 共享一次执行结果。超时报告未完成任务，停止推进依赖资源的关闭并以失败状态结束进程；进程退出不代表 drain 成功。各组件的停止等待与日志报告都受应用总预算约束。日志轮转仍使用原 lumberjack 算法，文件句柄由应用最终关闭；该库内部维护循环保留其既有进程生命周期，不把它宣称为可单独停止的应用 worker。

应用派生任务由 `lifecycle.Tasks` 统一等待，网关快照写回、免费额度统计刷新、Live observer、审核和结算副作用通过实例端口接入。其关闭仍先于邮件及共享存储资源，保持在途任务派生工作的原有等待规则。

新增 goroutine、定时器、队列或连接时，必须登记实际拥有者、启动点、接收封闭方式和完成等待。按需资源由已有拥有者管理，不能在运行时从业务模块反向调用 app 注册新组件。应用清理表按职责拆在 app 的运行时绑定文件中，并与 Wire 图一起验证。

调度快照、并发、串行队列和运行反馈由 app 绑定唯一 scheduler 实例，构造不启动。快照启停与网关读取直接调用 `scheduler.SnapshotService`，账号事件通过其窄发布端口更新快照；执行目标的投影只在执行边界进行。模型目录短缓存由同一时间轮直接清理 `routing.ModelList`。完整 handler 结束后停止调度新认领、取消等待并等待在途资源释放，再关闭 Redis/SQL。快照的初始重建仍异步，重复启动不重建，停止后不重开；遗留持久 outbox 留待下次消费或周期重建恢复。

usage 聚合器在停止时取消运行 context 和重试等待，拒绝新重算并等待已进入的工作；审计与系统日志 sink 的重复 Start 不会创建第二个 worker，Stop 后不能重开。Ops 错误采集队列仍在第一次入队时启动工作，队列实例与清理 hook 已在 app 构造期绑定。实时采样与订阅计数由 Ops 持有，WebSocket 握手和帧由 HTTP Adapter 处理；空闲停止与应用停止使用同一实例。

## 数据所有权

| 存储 | 所有权与使用方式 | 失败或丢失影响 |
| --- | --- | --- |
| PostgreSQL | 用户、身份、团队、Key、分组、渠道、账号、设置、订单、订阅、持久任务、用量和审计的权威状态 | 连接、迁移或密钥初始化失败会阻止完整应用启动；写失败不得由缓存结果伪装成成功 |
| Redis | 缓存、限流、并发槽、会话/粘性、分布式锁、调度快照、队列及跨实例失效；个别短期任务按 TTL 保存在 Redis | 影响依功能而异：安全入口可 fail-close，调度可受控回源，缓存可重建，在途短期任务可能丢失；必须由具体契约定义 |
| 本地数据目录 | 定价快照、日志、前端覆盖及部分部署配置 | 多实例默认不共享；容器部署必须挂载持久卷并在备份计划中显式纳入 |
| S3 兼容存储 | 备份等可选大对象 | 备份客户端按运行时设置构造，不能在启动时固定旧凭据；对象可用性与数据库元数据生命周期必须协同 |
| Google Cloud Storage | Vertex 批量图片输入、输出和中间 JSONL | 由 Vertex 批量图片 provider 按作业前缀管理；不得向 API 用户暴露内部 URI |
| 创作台临时数据 | Redis `creative:payload:`、`creative:input:`、`creative:mask:`、`creative:output:` 键（TTL 默认 30 分钟）与 `creative:queue:*` 队列；PostgreSQL 存 `creative_runs`/`creative_run_outputs` 元数据及 `creative_run_outbox` durable 动作 | 素材与 prompt 明文不入 PostgreSQL，因此不进入备份；临时输出过期即不可恢复，任务降级 `result_lost`，客户端 ack 先写元数据再删除输出键，删除失败由 reconciler 补偿 |
| 上游供应商 | 模型推理、OAuth、配额与供应商任务 | 失败通过平台适配器、账号状态和故障转移收敛；不能把上游瞬时错误写成永久本地事实 |

Ent schema 是主要实体的代码模型，手写 SQL 迁移是已部署数据库的演进权威。各模块 PostgreSQL Adapter 使用 Ent 和底层 `*sql.DB` 完成复杂聚合、批量更新及显式事务；两种访问方式共享同一连接池。

## HTTP 与前端交付

app 将进程配置投影为 `server.Options`，并提供已装配的全局 middleware 与路由注册函数。各模块拥有注册函数，app 的认证、用户、管理员、网关和支付装配分别注入处理器。综合设置、预聚合及创作设置也直接绑定处理器；`settings/composite` 组合所属模块的读取、准备和应用，不持有第二份缓存。CORS/CSP 参数在 `server/httpconfig`，认证与 Backend 模式门禁由身份 HTTP 实现。

`ProvideHTTPServer` 统一设置监听地址、请求头限制、header/idle timeout、可选全局请求体限制和 h2c。长时间 SSE 与 WebSocket 要求不设置全局 `WriteTimeout`，大请求体也使服务不设置全局 `ReadTimeout`；更细的 body 限制、并发和超时由路由或上游客户端执行。

Gin engine 的顺序为 Recovery、可信代理设置、全局日志/客户端指纹/CORS/CSP/Server-Timing、可选嵌入前端 middleware，随后注册健康检查、`/api/v1` 面板 API 和不带面板前缀的网关入口。嵌入前端 middleware 会绕过 API 与协议路径；`/models` 同时是模型广场页面和 API，因此按方法、认证信号、查询参数及 `Accept` 协商。

`embed` 构建从 `backend/internal/web/dist/` 提供静态资源，向 HTML 注入公开设置和 CSP nonce，并允许 `data/public` 覆盖静态文件。非 `embed` 构建不注册 SPA middleware，API 进程可以与 Vite 或外部静态服务器分离部署。

## 多实例与故障边界

- 数据库迁移使用 PostgreSQL advisory lock 串行化；多实例可同时启动，但只有持锁连接执行迁移。
- 调度快照、认证缓存失效、限流、并发槽和许多 leader job 依赖 Redis 协调。修改 key 命名、TTL 或 Lua 原子操作等同于修改跨实例契约。
- 存储缓存命中不能跳过必要的运行时资格检查；调度缓存未就绪或不可信时只能按对应服务定义的受控回源策略处理。
- 初始化失败分硬失败和可降级失败。数据库、迁移、最终配置校验和 HTTP server 构造属于硬门槛；例如远程定价初始化失败会记录警告并使用本地回退。新增降级必须明确是否会放宽认证、计费或 SSRF 等安全边界。
- 每个实例都会构造完整后台服务集合；已有单执行者任务继续使用各自的数据库/Redis 锁。用户平台额度的管理、回源、累计与镜像写回采用共享进程内用户锁，当前修复边界是单服务进程，不提供多个实例之间的重置协调。

出站、路由与账号的运行接口分别为 EgressPolicy、RoutePlan 和 AccountSnapshot。策略与模型配置跨请求边界提供独立副本；候选协议和账号映射在原 attempt/使用时点重新求值。account 的刷新协调、管理/用量查询、周期维护与 Deferred 由 app 持有并登记停止，egress 的采集监听仍按需开启。`account/postgres`、`routing/postgres`、`egress/postgres` 拥有各自存储，Redis 健康计数与 TLS 缓存在所属 Adapter 中；共享 SQL/Redis/HTTP 池仍只有原技术实例。

分组管理直接读取 AccountStore 与 KeyStore，容量查询直接读取账号轻量投影，身份/Key/billing 的分组和渠道读取直接绑定 routing。平台目录、动态设置和调度来源由 app 按职责直接组合，执行消费者共享已装配的规则与缓存实例。`account_groups`、代理联动及账号资金重置由同连接参与能力协作，不新增事务 context；配置更新不覆盖独立消费与运行字段。具体契约见[路由与计费](../domains/routing_and_billing.md)、[账号维护](../operations/account_maintenance.md)和[出站传输](../operations/upstream_transport_security.md)。

<a id="backup_and_maintenance"></a>
## 备份与系统维护装配

app 构造唯一 backup 核心、归档执行器、动态存储工厂、ops/maintenance 更新用例与系统操作锁。维护的停止认领和取消先于 HTTP 请求等待，维护收尾及备份任务等待完成后才关闭共享 SQL/Redis。备份、维护和精简命令直接使用入口；cron、存储缓存及操作锁均由所属模块唯一持有。

setup 只调用 app/bootstrap 的连接测试、迁移和身份初始化能力；identity/postgres 拥有首次管理员及 simple 管理员并发补齐，routing/postgres 拥有 simple 默认分组。它们不走普通注册或管理用例，不触发赠送、通知或后台 worker。两个维护命令在主体返回前关闭已取得连接，再由 main 决定退出码。

相关入口：[项目总览](../project_overview.md)、[架构目录](index.md)、[运维目录](../operations/index.md)。
