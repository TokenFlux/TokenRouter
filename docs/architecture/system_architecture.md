# 系统架构

本文描述 TokenRouter 单个应用实例的运行组件、依赖装配、启动关闭和数据所有权，帮助修改进程入口、依赖注入、基础设施实现或前端交付方式时保持跨层契约。本文不展开单次网关请求的模型路由与计费顺序，也不替代具体部署命令。

## 章节导航

- [运行组件](#运行组件)：识别进程内外边界。
- [依赖层次](#依赖层次)：修改 Wire 和模块所有权时读取。
- [启动与关闭](#启动与关闭)：修改初始化、后台服务或资源释放时读取。
- [数据所有权](#数据所有权)：判断 PostgreSQL、Redis 和对象存储的职责。
- [HTTP 与前端交付](#http-与前端交付)：修改 server、路由或嵌入式前端时读取。
- [多实例与故障边界](#多实例与故障边界)：修改锁、缓存或降级策略时读取。

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
       service / payment 与已迁用例
          |             |
 repository/基础设施   上游供应商
       |       |        |
 PostgreSQL  Redis   对象存储/HTTP
```

前端不是独立的业务后端。它通过 `frontend/src/api/` 调用 `/api/v1`，通过 Pinia 保存浏览器会话状态，并由路由守卫根据公开设置、当前用户、角色和功能开关控制页面访问。安全与授权仍由后端路由和中间件执行，前端隐藏页面不构成权限边界。

<a id="dependency_layers"></a>
## 依赖层次

`backend/internal/app/wire.go` 是完整应用依赖图的手写入口，旁边的 `wire_gen.go` 是生成结果。`cmd/server` 保留参数解析、构建版本变量和原 `go generate ./cmd/server` 入口。配置只加载一次，日志、数据库引导和应用图共用该配置；JWT secret 在数据库引导完成后补齐并重新校验。

| 层 | 主要路径 | 当前责任 |
| --- | --- | --- |
| 组合根 | `internal/app`、`app/bootstrap`、`app/lifecycle` | 配置投影、Wire 绑定、初始化、统一启停、失败回收和重启请求 |
| 配置 | `internal/config` | 默认值、YAML/环境变量加载、归一化与启动校验 |
| 已迁用例 | `internal/settings`、`idempotency`、`site`、`billing`、`identity`、`team`、`apikey`、`routing`、`account`、`egress` | 设置、幂等、公告、资金与权益、身份/团队/Key、路由目录、账号管理与维护、出站策略 |
| 旧业务图 | `internal/service`、`payment`、`repository` | 尚未迁移的业务规则、事务和适配实现；原 provider set 继续参与构造 |
| 通用技术实现 | `internal/infra` | PostgreSQL/迁移、Redis/会话/限流/锁、HTTP 池、proxy/TLS、时间轮、日志/timing 和 AES |
| HTTP 适配与服务器 | `internal/handler`、`site/httpapi`、`billing/httpapi`、`identity/httpapi`、`team/httpapi`、`apikey/httpapi`、`idempotency/httpapi`、`routing/httpapi`、`account/httpapi`、`egress/httpapi`、`server`、`web` | 输入输出、认证中间件、路由汇总、HTTP 参数与静态资源 |

settings 的通用实现位于 `settings` 与 `settings/postgres`；旧 `SettingService` 继续解释业务设置和维护领域缓存。idempotency 的核心、观察出口与 SQL Adapter 已独立，旧默认入口只委托唯一实例。site 拥有公告实体、targeting、用例和到期 worker，HTTP 与 PostgreSQL Adapter 分开；旧 domain 公告类型只作为 Ent 生成代码引用的别名。

`protocol` 拥有协议值、各方言报文和 `bridge` 转换状态；`routing/capability` 拥有原生集合、准入及单步 fallback，`routing` 拥有 effort 映射规则。`billing/pricing` 拥有价卡、目录解析、费用与展示计算，`billing/provider` 拥有目录加载、热更新及唯一运行缓存。旧 apicompat/domain/PricingService 保留必要转接。billing 的 Calculator、PriceResolver 和资金分配规则接收显式投影；普通结算与任务资金由 Funds 进入 billing/postgres 的闭合事务，Redis 缓存位于 billing/rediscache。账号选择、平台传输、用量记录和支付订单编排仍在旧图，纯定价和协议不读取配置或 I/O。

公告与 billing 的用户读取由 app 直接投影 identity；公告有效订阅直接适配 billing 存取接口，匹配规则仍由 site 执行。模块不导入 app。旧图的跨层构造暂留原 provider，应用级启停和新旧模块绑定由 app 管理，不能通过搬动目录给新代码继承历史依赖许可。

身份的注册、绑定、会话和强认证进入 identity，团队事务进入 team，Key 的访问快照、L1/L2 与认证 outbox 进入 apikey。app 构造唯一生产实例与事务参与工厂，旧 service/repository 只保留形状转换和委托；跨模块写入沿用现有 Ent context 与调用方连接。分组/渠道由 routing、账号管理与维护由 account、代理与 TLS 策略由 egress 提供；用量、通知、推广支付、调度执行与供应商交换仍经窄端口连接旧图。模型匹配由纯 `routing/modelmap` 共享，网关仍拥有请求改写顺序。

`pkg/apperror`、`pagination`、`timezone`、`ipmatch`、`oauthpkce`、`logredact` 提供通用值类型与计算；`server/httpx`、`server/clientip` 拥有 HTTP 适配。旧 pkg/util 入口保留必要的类型别名和委托，不复制实现或状态。

`repository.NewHTTPUpstream` 仍解释配置与平台策略，`infra/httpclient.UpstreamPool` 拥有客户端缓存和请求释放。OpenAI HTTP/2 回退及 Grok CLI 策略尚未迁出旧适配层。修改 provider 后运行保留的 Wire 生成命令，不编辑生成文件，也不因纯装配变化运行 Ent 生成。

<a id="startup_and_shutdown"></a>
## 启动与关闭

主入口保持三条互斥路径：`-version` 只输出构建信息，`-setup` 执行 CLI 安装；未安装时执行 AUTO_SETUP 或启动独立 setup server；已配置时构造完整应用。setup 的迁移调用精简 bootstrap，不构造业务 worker。

完整应用先初始化日志，再由 bootstrap 初始化时区和 PostgreSQL，在原十分钟迁移预算内执行迁移与暂时错误重试，补齐持久 JWT secret、完整校验配置，并在 simple 模式补齐默认分组和管理员并发。Ent 与原生 SQL 共享连接，只有一个关闭拥有者。

Wire 构造对象并登记资源后，lifecycle 才启动后台工作。时间轮和设置/定价预热先完成，再启动缓存订阅及消费队列，最后启动周期生产者、任务拉取和 HTTP。原有首次执行、预热降级、功能开关和动态 worker 数量保持各模块语义。构造或部分启动失败时回收已取得及已尝试启动的资源，错误链保留原始原因。

定价 provider 由 `app/pricing.go` 投影独立 Options，继续走原 PricingInitialization 与 PricingService hook 的 Initialize → Start → Stop 顺序。旧远端 repository 客户端委托 provider；旧 PricingService 不再持有目录锁、ticker 或第二份缓存。平台模型别名和动态 Grok 默认值通过 `app/legacybridge` 注入，每次查价只取得一次快照。

billing 的余额/Key 缓存队列、平台额度 flusher 和订阅过期提醒由 app 绑定到现有生命周期。提醒保留立即首轮、每分钟扫描和既有 Redis/数据库 leader 策略，停止时取消并等待在途操作。没有生产消费者的订阅维护队列不会因迁包自动启动。

identity 的会话、TOTP、资料操作和 pending 存取，以及 apikey 的过期、活动时间、滥用限制与 outbox 取时，由 app 在构造时注入系统时钟函数。各原取时点继续独立读取，JWT 库内验证与签发使用相同来源；团队与成员额度的日期对象继续保留原时区和 DST 边界。

认证缓存构造不启动后台任务，Start 才创建 L1 和 Redis 订阅。停止时先拒绝新的认证认领，等待在途认证、活动时间写入和失效调用，再关闭订阅与 L1；预算超时保留正在使用的依赖并报告未完成。outbox worker 停止新认领并等待当前批次，持久化重试及延迟二次失效留待后续启动，不宣称全部排空。钉钉资料同步使用统一任务跟踪器，继续与请求取消解耦并保留 30 秒单任务预算。

SIGINT、SIGTERM、监听失败和 Linux 手动重启进入同一关闭流程。HTTP 有独立五秒优雅关闭预算，随后后台清理使用独立三十秒总预算：先关闭额外监听、Live 本地观察与 hijack 连接，等待完整 handler 返回，再停止周期生产者和任务拉取，逐层排空用量、缓存写入、额度镜像、延迟写回、通知和审计，最后关闭订阅、时间轮、空闲 HTTP 连接、Redis、Ent/SQL 和日志文件。

请求跟踪只包装 Handler，保留原 ResponseWriter 的 Flush/Hijack 能力；客户端断开后仍按原策略收集用量的 handler 必须先完成，不能直接以连接断开替代请求清理完成。现有异步额度写入、通知、探针和快照任务通过消费者侧的完成接口登记，保留其 context、并发和参数求值语义；每层关闭后等待该层派生的副作用，再关闭下层依赖。Live 观察停止只关闭本地资源，不因进程退出提前把远端会话判定为已结束。错误透传、TLS profile/router 和鉴权缓存订阅会等待最后一次回调结束。运维错误日志队列会处理完已入队批次；QPS WS 缓存的空闲定时器和按需刷新也在 HTTP 收尾后停止。WS 池关闭后禁止按需重建或重新预热，已租赁连接保留原请求收尾策略，在租约归还时释放。

Stop 和 Cleanup 共享一次执行结果。超时报告未完成任务，停止推进依赖资源的关闭并以失败状态结束进程；进程退出不代表 drain 成功。旧单步 Stop 的无界等待以及日志报告都受应用总预算约束。日志轮转仍使用原 lumberjack 算法，文件句柄由应用最终关闭；该库内部维护循环保留其既有进程生命周期，不把它宣称为可单独停止的应用 worker。

新增 goroutine、定时器、队列或连接时，必须登记实际拥有者、启动点、接收封闭方式和完成等待。按需资源由已有拥有者管理，不能在运行时从业务模块反向调用 app 注册新组件。应用清理表按职责拆在 app 的运行时绑定文件中，并与 Wire 图一起验证。

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

Ent schema 是主要实体的代码模型，手写 SQL 迁移是已部署数据库的演进权威。repository 同时使用 Ent 和底层 `*sql.DB` 完成复杂聚合、批量更新及显式事务；两种访问方式共享同一连接池。

## HTTP 与前端交付

`ProvideHTTPServer` 统一设置监听地址、请求头限制、header/idle timeout、可选全局请求体限制和 h2c。长时间 SSE 与 WebSocket 要求不设置全局 `WriteTimeout`，大请求体也使服务不设置全局 `ReadTimeout`；更细的 body 限制、并发和超时由路由或上游客户端执行。

Gin engine 的顺序为 Recovery、可信代理设置、全局日志/客户端指纹/CORS/CSP/Server-Timing、可选嵌入前端 middleware，随后注册健康检查、`/api/v1` 面板 API 和不带面板前缀的网关入口。嵌入前端 middleware 会绕过 API 与协议路径；`/models` 同时是模型广场页面和 API，因此按方法、认证信号、查询参数及 `Accept` 协商。

`embed` 构建从 `backend/internal/web/dist/` 提供静态资源，向 HTML 注入公开设置和 CSP nonce，并允许 `data/public` 覆盖静态文件。非 `embed` 构建不注册 SPA middleware，API 进程可以与 Vite 或外部静态服务器分离部署。

## 多实例与故障边界

- 数据库迁移使用 PostgreSQL advisory lock 串行化；多实例可同时启动，但只有持锁连接执行迁移。
- 调度快照、认证缓存失效、限流、并发槽和许多 leader job 依赖 Redis 协调。修改 key 命名、TTL 或 Lua 原子操作等同于修改跨实例契约。
- repository 缓存命中不能跳过必要的运行时资格检查；调度缓存未就绪或不可信时只能按对应服务定义的受控回源策略处理。
- 初始化失败分硬失败和可降级失败。数据库、迁移、最终配置校验和 HTTP server 构造属于硬门槛；例如远程定价初始化失败会记录警告并使用本地回退。新增降级必须明确是否会放宽认证、计费或 SSRF 等安全边界。
- 每个实例都会构造完整后台服务集合；已有单执行者任务继续使用各自的数据库/Redis 锁。用户平台额度的管理、回源、累计与镜像写回采用共享进程内用户锁，当前修复边界是单服务进程，不提供多个实例之间的重置协调。

相关入口：[项目总览](../project_overview.md)、[架构目录](index.md)、[运维目录](../operations/index.md)。

出站、路由与账号的运行接口分别为 EgressPolicy、RoutePlan 和 AccountSnapshot。策略与模型配置跨请求边界提供独立副本；候选协议和账号映射在原 attempt/使用时点重新求值。account 的刷新协调、管理/用量查询、周期维护与 Deferred 由 app 持有并登记停止，egress 的采集监听仍按需开启。`account/postgres`、`routing/postgres`、`egress/postgres` 拥有各自存储，Redis 健康计数与 TLS 缓存在所属 Adapter 中；共享 SQL/Redis/HTTP 池仍只有原技术实例。

分组管理直接读取 AccountStore 与 KeyStore，容量查询直接读取账号轻量投影，身份/Key/billing 的分组和渠道读取直接绑定 routing。平台目录、旧请求上下文设置和调度反馈仍通过 app/legacybridge 提供。`account_groups`、代理联动及账号资金重置由同连接参与能力协作，不新增事务 context；配置更新不覆盖独立消费与运行字段。具体契约见[路由与计费](../domains/routing_and_billing.md)、[账号维护](../operations/account_maintenance.md)和[出站传输](../operations/upstream_transport_security.md)。
