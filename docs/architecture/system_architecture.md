# 系统架构

本文说明单个应用实例的组件、依赖装配、启停顺序和数据归属。包职责和依赖图见[后端模块地图](backend_modules.md)，单次请求的处理顺序见[网关请求生命周期](gateway_request_lifecycle.md)。

## 章节导航

- [运行组件](#运行组件)：哪些东西在进程内，哪些在进程外。
- [依赖层次](#依赖层次)：修改 Wire 装配或调整模块归属时读取。
- [启动与关闭](#启动与关闭)：修改初始化、后台服务或资源释放时读取。
- [数据归属](#数据归属)：PostgreSQL、Redis 和对象存储各存什么。
- [HTTP 与前端交付](#http-与前端交付)：修改 server、路由或嵌入式前端时读取。
- [多实例与故障处理](#多实例与故障处理)：修改锁、缓存或降级策略时读取。
- [备份与系统维护装配](#backup_and_maintenance)：修改备份、系统操作或精简初始化的装配时读取。

## 运行组件

发布形态以一个 Go 应用进程为中心。这个进程同时提供面板 API、AI 网关、支付回调、健康检查和多种后台任务；使用 `embed` 构建标签时还提供 Vue SPA。正常运行需要外部的 PostgreSQL 和 Redis，大对象和导出文件按功能存到本地数据目录或 S3 兼容存储。

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

前端通过 `frontend/src/api/` 调用 `/api/v1`，用 Pinia 保存浏览器会话状态，由路由守卫根据公开设置、当前用户、角色和功能开关决定能打开哪些页面。权限由后端路由和中间件判定，前端隐藏页面只影响界面展示。

<a id="dependency_layers"></a>
## 依赖层次

`backend/internal/app/wire.go` 是完整应用依赖图的手写入口，`wire_gen.go` 是生成结果。根入口引用按模块拆分的 `assembly_*_wire.go` 集合：具体的 provider 和跨模块接口绑定写在同一个集合里，生命周期登记放在单独的集合。包目录、职责和关系图见[后端模块地图](backend_modules.md)。

| 层 | 主要职责 |
| --- | --- |
| cmd/server、config、app/bootstrap | 命令行参数和版本信息、配置加载、数据库引导、迁移和持久密钥 |
| app、app/lifecycle | 组装生产实例、把配置拆给各模块、绑定接口实现、启停和失败回收 |
| 业务模块 | 按身份、提供商、路由、资金、任务等主题实现用例和规则 |
| 模块内的 HTTP、PostgreSQL、Redis、provider 子包 | 处理协议、存储和外部服务，实现核心代码声明的接口 |
| gateway、upstream、protocol | 依次负责入站编排、和供应商交换报文、报文格式与转换规则 |
| infra、pkg | 技术资源、通用值类型和计算工具 |

### 装配与共享实例

app 构造一个 `settings.Store`，把它交给各领域的设置读取器。公开站点信息由 site 模块整理输出，综合设置页由 `settings/composite` 负责读取、准备、保存和应用。幂等 HTTP 处理器都绑定同一个协调器；协调器和它的清理任务共用 SQL 连接池，各自接收一个日志输出。

提供商、路由、调度、计费和用量的存储和缓存也由 app 构造。提供商健康状态、快速阻断、模型短暂失败记录和快照节流，各有一个实例供所有使用方共享。选号、固定提供商的 WS 复核和管理端诊断使用同一套资格规则和调度反馈。Agent Identity 协调器只构造一次：已持久化的提供商共用进程内的按提供商锁，尚未持久化的提供商由各入口自己处理互斥。

OpenAI 的文本、Responses、WS、Images 和辅助执行器共用请求构造、响应输出、凭据和连接资源。Messages、Google 和 Grok 的执行适配器分别连接各自的平台。app 先构造完成记录器（Recorder），再绑定执行器。完成记录需要的输入在请求期间就固定下来，异步任务读取这份副本，Gin Context 留在请求内。平台分派、部分结果和重试规则见[网关请求生命周期](gateway_request_lifecycle.md)。

通知模块接收业务模块已经确定好的事件：验证码和重置凭据由 identity 生成，余额和额度阈值由 billing 和 provider 判断。search 的配置、注册表和在途请求计数由所有网关共享。公告、公开设置和页面权限归 site 模块，web 模块读取 site 输出的公开数据。各模块的专题文档入口见[模块导航](backend_modules.md#module_topics)。

### 规则与存储的分工

`protocol` 维护协议取值、报文和转换状态。`routing/capability` 判断原生支持的协议、准入和单步转换，`routing/modelmap` 做模型匹配，routing 决定路由和 effort 映射。`billing/pricing` 只做查价和费用计算，`modelcatalog/provider.Service` 加载统一模型目录并负责热更新。协议算法和定价算法都是纯计算，配置读取和 I/O 由调用方完成。

`gateway/provider.ExecutionProvider` 由 `provider.Record` 和本次的 `AttemptRoute` 组成，数据库和缓存的编码由各自的存储负责。管理端 DTO、去掉凭据的候选数据和执行目标是三份不同的数据结构。配置、CAS 更新、outbox、消费累计和快照编码由各自所属的存储和规则实现，app 负责把实现绑定到接口并做数据转换。

普通结算在 billing/postgres 的一个完整事务里完成。身份、团队、支付和任务等外层事务通过明确传入的同连接参与能力让其他模块加入；加入的模块在外层事务里执行，提交和成功后的副作用由外层负责。creative 和 batchimage 各自管理任务、输出和完成资格，都通过 `billing.Funds` 处理资金；退款时调用外部渠道的步骤放在两个短事务之间。资金是否到账以资金事务的提交为准，用量记录、订单状态和通知结果都只是旁证。

领域错误由所属模块定义。HTTP 映射和跨模块判断传递原始错误对象，使用 `errors.Is/As` 和错误链判断；按文案新建一个同名错误会让这些判断失效。接口、存储和后台代码能依赖哪些包，由 `tools/architecture/` 的 arch-go 架构测试按角色和具体文件约束，见[依赖规则](../operations/development_workflow.md#backend_dependency_rules)。

### 传输与生成入口

`app/http_transport.go` 把传输相关配置整理给下游，`gateway/provider/transport` 组合请求策略和各平台的传输能力。`infra/httpclient.UpstreamPool` 持有客户端缓存和请求占用计数，成功的请求在响应体关闭时归还占用。OpenAI 的 HTTP/2 回退由 egress 处理，Grok CLI Header 和可重放的 403 策略由 upstream/grok 处理。隐私请求使用共享的 req 客户端池，超时 30 秒，使用 Chrome 指纹。

`cmd/server` 负责参数解析、构建版本变量和 `go generate ./cmd/server` 入口。配置只加载一次，日志、数据库引导和应用依赖图共用这份配置；JWT secret 在数据库引导后补齐，然后重新校验配置。修改 Wire provider 后通过这个生成入口更新结果；只改装配时无需运行 Ent 生成。

<a id="startup_and_shutdown"></a>
## 启动与关闭

### 启动顺序

主入口先处理 `-version` 和 `-setup`：前者输出构建信息后退出，后者执行命令行安装。正常启动时，尚未安装就执行 AUTO_SETUP 或启动独立的 setup server，已经配置好就构造完整应用。setup 里的迁移调用精简版 bootstrap，业务 worker 留到完整应用启动后再构造。

完整应用启动时：

1. 初始化日志。
2. bootstrap 初始化时区和 PostgreSQL，在十分钟总预算内执行迁移，遇到暂时性错误会重试。
3. 补齐持久 JWT secret，完整校验配置。首次创建的管理员默认并发为 5，已有管理员的配置保持不变；默认分组需要管理员自己创建。
4. Ent 和 SQL 共用一个连接，由一个所有者负责关闭。
5. bootstrap 成功后，app 固定一个共享的 Calendar，传给用量、支付、推广、团队 HTTP、计费、提供商和路由的装配。用量存储、聚合事务、仪表盘按日缓存、团队日统计、资金结算、Key 按日计数和公开站点的时区展示都使用这个日期对象。用户自定义时区和无效值回退由各入口自己处理，团队查询使用服务端时区。

`pkg/timezone` 提供 Calendar 和时间文本解析。生产代码里只有 `app/bootstrap.InitTimezone` 会写 `time.Local`，默认时区为上海。

Wire 构造完对象、登记完资源后，lifecycle 才启动后台工作，顺序如下：

1. 时间轮，以及设置和定价的预热。
2. 缓存订阅和消费队列。
3. 周期性生产者、任务拉取和 HTTP。

各模块的首次执行时机、预热失败时的降级、功能开关和动态 worker 数量由模块自己决定。构造或启动到一半失败时，已经取得和已经尝试启动的资源都会回收，返回的错误链保留最初的原因。

### 模型目录与运行设置

统一模型目录的配置由 `app/model_catalog.go` 单独整理成 Options，远端客户端和运行实例在 `modelcatalog/provider`。计费、默认价格和模型属性的使用方从单独的 Wire 装配拿到同一个 `Service`。目录的生命周期登记为 `ModelCatalogInitialization` 和 `ModelCatalogService`，日志名为 `service.modelcatalog`。

`app/runtime_model_catalog.go` 登记目录生命周期，`app/runtime_settings.go` 用 `RuntimeSettingsInitialization` 登记转发设置的加载和 Grok 默认模型迁移。两者各有一个就绪标记参与装配，运行设置先于目录初始化。

`billing/provider` 负责计费回退告警和时区适配。Calculator、PriceResolver 和 PricingConfigService 由 app 直接提供，计算和查价的使用方共享这些实例。平台模型别名和动态的 Grok 默认值由 `gateway/provider/modelidentity` 提供，每次查价只取一次快照。平台运行状态由 modelidentity 读取，纯定价代码只接收它给出的快照。Key 和分组的模型追踪由 `gateway/modeltrace` 组合。

billing 的余额和 Key 缓存队列、订阅过期提醒由 app 接入生命周期。提醒任务启动后立即执行第一轮，之后每分钟扫描一次，用 Redis 或数据库选出 leader；停止时取消并等待正在执行的操作。

### 关闭顺序

SIGINT、SIGTERM、监听失败和 Linux 上的手动重启走同一个关闭流程。HTTP 有五秒的优雅关闭预算，之后的后台清理有三十秒的总预算，两个预算分开计算。后台清理的顺序：

1. 关闭额外监听、Live 本地观察和被 hijack 的连接。
2. 等待所有 handler 返回。
3. 停止周期性生产者和任务拉取。
4. 逐层排空用量、缓存写入、延迟写回、通知和审计。
5. 关闭订阅、时间轮、空闲 HTTP 连接、Redis、Ent/SQL 和日志文件。

`OpenAILiveObservers` 在阶段 5 停止，Responses WS 设置刷新在阶段 9 停止，`OpenAIWSConnections` 在阶段 10 关闭。

请求跟踪包装的是 Handler，ResponseWriter 原样交给下游，Flush 和 Hijack 仍然可用。有些 handler 在客户端断开后还要继续收集用量，它们完成后请求清理才算结束。异步额度写入、通知、探针和快照任务在使用方一侧通过完成接口登记，它们的 context、并发数和参数取值时机保持各自的设计。每一层关闭后，先等这一层派生出的副作用完成，再关闭下一层依赖。

`Stop` 和 `Cleanup` 共享同一次执行的结果。超时时报告还没完成的任务，停止关闭依赖这些任务的资源，进程以失败状态退出；进程退出时 drain 可能尚未完成。各组件的停止等待和日志都算在应用总预算内。日志轮转使用 lumberjack，文件句柄由应用最后关闭；lumberjack 内部的维护循环随进程结束，无法作为应用 worker 单独停止。

应用派生的任务统一由 `lifecycle.Tasks` 等待。网关快照写回、免费额度统计刷新、Live observer、审核和结算副作用都通过实例接口接入，它们的关闭早于邮件和共享存储资源。

新增 goroutine、定时器、队列或连接时，需要登记它的所有者、启动点、停止接收的方式和完成等待。按需创建的资源由已有的所有者管理；业务模块在运行期间调用 app 注册新组件会形成反向依赖。应用的清理表按职责拆在 app 的运行时绑定文件里，和 Wire 图一起验证。

### 各组件的停止行为

搜索：搜索运行时在 HTTP 开放前初始化，所有配置版本共用一个在途计数。替换配置时只关闭旧客户端的空闲连接，已经开始的搜索继续执行。关闭时停止接收新搜索，并等待额度清理完成。

审核和邮件：审核先关闭队列再等待。邮件队列在通知生产者之后排空；预算用完时取消 SMTP 并报告未完成的邮件。HTTP 五秒和后台三十秒两个预算分开计算，任何一个超时都报告为未排空。

任务 HTTP：提交、下载和管理调用由 `TaskRequestsAndDownloads` 关闭屏障跟踪。HTTP 退出后先拒绝新调用、等待已有调用，再停止任务拉取和恢复循环。worker 停止后无法重启，多次停止返回同一个结果。创作台生成阶段占用的用户槽和提供商槽由 `scheduler.Lease` 按相反顺序释放。任何阶段超时时，后面阶段还要用的共享依赖保持打开，日志里如实报告任务未全部完成。

身份和 API Key：identity 的会话、TOTP、资料操作和 pending 存取，以及 apikey 的过期判断、活动时间、滥用限制和 outbox 取时，都在构造时由 app 注入系统时钟函数，每个调用点各自读取当前时间。JWT 库内部的验证和签发使用同一个时钟来源。团队和成员额度的日期对象使用各自配置的时区，并处理夏令时切换。

认证缓存：构造时不启动后台任务，调用 Start 才创建 L1 缓存和 Redis 订阅。停止时先拒绝新的认证认领，等待在途认证、活动时间写入和失效调用完成，再关闭订阅和 L1。预算超时时，正在使用的依赖保持打开，并报告未完成。outbox worker 停止时不再认领新任务，等当前批次处理完；持久化的重试和延迟二次失效留到下次启动继续，日志里注明未全部排空。钉钉资料同步使用统一的任务跟踪器，与请求的取消互不影响，单个任务的预算为 30 秒。

Live 观察：停止时只关闭本地资源，远端会话的状态以远端为准。错误透传、TLS profile/router 和鉴权缓存订阅会等最后一次回调结束。运维错误日志队列处理完已入队的批次。QPS WS 缓存的空闲定时器和按需刷新在 HTTP 收尾后停止。WS 池关闭后不再按需重建或重新预热；已经租出的连接按原请求的收尾方式处理，归还时释放。

调度：调度快照、并发控制、串行队列和运行反馈由 app 绑定到一个 scheduler 实例，构造时不启动。快照的启停和网关读取直接调用 `scheduler.SnapshotService`，提供商事件通过它的发布接口更新快照，执行目标的数据转换在执行入口处完成。模型目录查询使用请求内的提供商模型规则快照。所有 handler 结束后，调度停止认领新请求、取消等待并等待在途资源释放，然后才关闭 Redis 和 SQL。快照的首次重建是异步的，重复启动时跳过重建，停止后无法重新打开；遗留的持久 outbox 由下次启动的消费或周期重建处理。

用量和审计：usage 聚合器停止时取消运行 context 和重试等待，拒绝新的重算，等待已经开始的工作完成。审计和系统日志 sink 重复调用 Start 时复用已有的 worker，Stop 之后无法重新打开。Ops 错误采集队列在第一次入队时启动工作，队列实例和清理 hook 在 app 构造期间就绑定好了。实时采样和订阅计数由 Ops 持有，WebSocket 握手和帧由 HTTP 适配层处理；空闲停止和应用停止操作的是同一个实例。

## 数据归属

| 存储 | 存什么、怎么用 | 失败或丢失的影响 |
| --- | --- | --- |
| PostgreSQL | 用户、身份、团队、Key、分组、价格配置、提供商、设置、订单、订阅、持久任务、用量和审计的主数据 | 连接、迁移或密钥初始化失败时，完整应用无法启动；写入失败时如实返回失败 |
| Redis | 缓存、限流、并发槽、会话和粘性、分布式锁、调度快照、队列和跨实例失效；少数短期任务按 TTL 存在 Redis | 影响因功能而异：安全入口可以直接拒绝请求，调度可以按设计回源，缓存可以重建，在途的短期任务可能丢失；每个功能的具体行为写在对应文档里 |
| 本地数据目录 | 定价快照、日志、前端覆盖文件和部分部署配置 | 多实例之间默认不共享；容器部署需要挂载持久卷，并在备份计划里列出 |
| S3 兼容存储 | 备份等可选大对象 | 备份客户端每次按运行时设置构造，修改凭据后立即生效；对象的可用性需要和数据库里的元数据同步管理 |
| Google Cloud Storage | Vertex 批量图片的输入、输出和中间 JSONL | 由 Vertex 批量图片 provider 按作业前缀管理；内部 URI 对 API 用户隐藏 |
| 创作台临时数据 | Redis 的 `creative:payload:`、`creative:input:`、`creative:mask:`、`creative:output:` 键（TTL 默认 30 分钟）和 `creative:queue:*` 队列；PostgreSQL 的 `creative_runs`、`creative_run_outputs` 元数据和 `creative_run_outbox` 持久动作 | 素材和 prompt 明文只存在 Redis，所以也不在数据库备份里；临时输出过期后无法恢复，任务标记为 `result_lost`；客户端确认时先写元数据再删除输出键，删除失败由 reconciler 补做 |
| 上游供应商 | 模型推理、OAuth、配额和供应商侧任务 | 失败由平台适配器、提供商状态和故障转移处理；上游的瞬时错误按瞬时状态记录 |

Ent schema 是主要实体的代码模型，已部署数据库的结构以手写 SQL 迁移为准。各模块的 PostgreSQL 适配层用 Ent 和底层 `*sql.DB` 完成复杂聚合、批量更新和手动事务，两种访问方式共用同一个连接池。

## HTTP 与前端交付

app 把进程配置整理成 `server.Options`，并提供装配好的全局 middleware 和路由注册函数。注册函数由各模块提供，app 为认证、用户、管理员、网关和支付装配分别注入 handler。综合设置、预聚合和创作台设置也直接绑定 handler；`settings/composite` 调用各模块的读取、准备和应用逻辑，自身不保存缓存。CORS 和 CSP 参数在 `server/httpconfig`，认证和 Backend 模式的门禁由身份模块的 HTTP 层实现。

`ProvideHTTPServer` 统一设置监听地址、请求头大小限制、header 和 idle 超时、可选的全局请求体限制和 h2c。长时间的 SSE 和 WebSocket 要求全局 `WriteTimeout` 留空，大请求体要求全局 `ReadTimeout` 留空；更细的请求体限制、并发和超时由各路由或上游客户端设置。

Gin engine 的中间件顺序：Recovery、可信代理设置、全局日志、客户端指纹、CORS、CSP、Server-Timing、可选的嵌入前端 middleware。之后注册健康检查、`/api/v1` 面板 API，以及不带面板前缀的网关入口。嵌入前端 middleware 会放行 API 和协议路径。`/models` 既是模型广场页面也是 API，按请求方法、认证信息、查询参数和 `Accept` 头区分。

`embed` 构建从 `backend/internal/web/dist/` 提供静态资源，在 HTML 里注入公开设置和 CSP nonce，`data/public` 下的文件可以覆盖静态文件。非 `embed` 构建不注册 SPA middleware，API 进程可以和 Vite 或外部静态服务器分开部署。

## 多实例与故障处理

- 数据库迁移用 PostgreSQL advisory lock 串行执行。多个实例可以同时启动，拿到锁的连接执行迁移。
- 调度快照、认证缓存失效、限流、并发槽和许多 leader 任务靠 Redis 协调。修改 key 命名、TTL 或 Lua 原子操作，会影响所有实例之间的约定。
- 存储缓存命中后，运行时资格检查照常执行。调度缓存未就绪或不可信时，按对应服务设计的回源策略处理。
- 初始化失败分为硬失败和可降级失败。数据库、迁移、最终配置校验和 HTTP server 构造失败会终止启动；远程定价初始化失败时记录警告，改用本地数据。新增降级逻辑时，要说明它会不会放宽认证、计费或 SSRF 防护。
- 每个实例都构造完整的后台服务；只能由一个实例执行的任务，用各自的数据库锁或 Redis 锁选出执行者。

出站、路由和提供商的运行接口分别是 `EgressPolicy`、`RoutePlan` 和 `ProviderSnapshot`。策略和模型配置跨请求使用时，每次提供一份独立副本；候选协议和提供商映射在每次 attempt 使用时重新计算。provider 的刷新协调、管理和用量查询、周期维护和 Deferred 任务由 app 持有并登记停止，egress 的采集监听按需开启。`provider/postgres`、`routing/postgres` 和 `egress/postgres` 各自管理存储，Redis 健康计数和 TLS 缓存放在对应的适配层里；SQL、Redis 和 HTTP 连接池各只有一个技术实例。

分组管理直接读取 ProviderStore 和 KeyStore，容量查询读取提供商的轻量数据。身份、Key 和 billing 用到的分组策略和价格配置，直接绑定 routing 的读取接口。平台目录、动态设置和调度数据源由 app 按职责组合，执行环节共用已经装配好的规则和缓存实例。`provider_groups` 表、代理联动和提供商的资金重置通过同连接参与能力协作，共用一个事务；配置更新只写配置字段，独立的消费字段和运行字段保持原值。详细约定见[路由与结算](../domains/routing_and_billing.md)、[提供商维护](../operations/provider_maintenance.md)和[上游传输安全](../operations/upstream_transport_security.md)。

<a id="backup_and_maintenance"></a>
## 备份与系统维护装配

app 构造唯一的 backup 核心、归档执行器、动态存储工厂、`ops/maintenance` 更新用例和系统操作锁。关闭时，维护任务先停止认领并取消，然后才等待 HTTP 请求；维护收尾和备份任务完成后，才关闭共享的 SQL 和 Redis。备份、维护和精简命令直接调用这些入口；cron、存储缓存和操作锁各由所属模块持有一份。

setup 调用 `app/bootstrap` 的连接测试、迁移和身份初始化。首次管理员由 `identity/postgres` 创建，初始化过程不创建分组。setup 绕开普通注册和管理用例，因此也不会触发赠送、通知或后台 worker。两个维护命令在返回前关闭已经打开的连接，退出码由 main 决定。

相关入口：[项目总览](../project_overview.md)、[架构目录](index.md)、[运维目录](../operations/index.md)。
