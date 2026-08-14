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
       service / payment 领域编排
          |             |
 repository/基础设施   上游供应商
       |       |        |
 PostgreSQL  Redis   对象存储/HTTP
```

前端不是独立的业务后端。它通过 `frontend/src/api/` 调用 `/api/v1`，通过 Pinia 保存浏览器会话状态，并由路由守卫根据公开设置、当前用户、角色和功能开关控制页面访问。安全与授权仍由后端路由和中间件执行，前端隐藏页面不构成权限边界。

登录后的普通业务路由在根 `RouterView` 外共享同一个 `AppLayout`，Header、Sidebar 和 onboarding 生命周期不会因业务页面切换而重建。登录、注册、验证和 OAuth 回调路由同样共享 `AuthShell`，使 Lottie 背景不随认证步骤重载；页面自己的 `AuthLayout` 只负责品牌卡片和 footer 组合。需要独立全屏形态的页面通过路由元数据自行管理布局；公开模型与支付回跳页面继续按各自认证和窗口上下文选择外壳。

<a id="dependency_layers"></a>
## 依赖层次

`backend/cmd/server/wire.go` 是完整应用依赖图的手写入口，`wire_gen.go` 是生成结果。装配顺序由依赖关系决定，概念上的所有权如下：

| 层 | 主要路径 | 责任 |
| --- | --- | --- |
| 配置 | `internal/config` | 读取启动配置、设置默认值、归一化并校验；向后续层提供不可变启动快照 |
| 基础设施与仓储 | `internal/repository`、`ent/schema` | PostgreSQL/Ent、Redis、缓存、对象存储、上游基础客户端和 repository 接口实现 |
| 领域与应用服务 | `internal/service`、`internal/payment` | 业务不变量、跨仓储事务、调度、计费、协议转换、后台任务和支付 provider 选择 |
| 接口适配 | `internal/handler`、`internal/server/middleware` | HTTP 输入输出、认证上下文、协议错误、请求 attempt 编排和审计 |
| 服务器 | `internal/server` | Gin engine、全局中间件、路由族、前端 middleware 和 `http.Server` 参数 |

这不是由 Go import 强制的纯单向分层。`repository` 会实现 `service` 中定义的端口，handler 也会协调多个 service；判断所有权应看不变量落在哪里，而不是只看包名。禁止把 Wire 生成文件当作编辑源：新增 provider 或修改依赖时改各层 `wire.go`，再执行 `go generate ./cmd/server`。

应用级 Wire provider set 依次包含配置、repository、service、payment、middleware、handler 和 server。`Application` 最终只暴露 `*http.Server` 与 `Cleanup`，其余服务通过依赖图被实例化并由关闭函数持有。

<a id="startup_and_shutdown"></a>
## 启动与关闭

主入口有三条互斥路径：

1. `-version` 仅输出构建信息后退出；`-setup` 运行 CLI 设置流程。
2. 未完成设置时，若启用容器自动设置就从环境生成配置并迁移；否则只启动 setup server 和可用的嵌入前端。
3. 已配置时进入完整应用路径。

完整应用启动顺序为：

1. 初始化 bootstrap 日志并用 `LoadForBootstrap` 读取配置。启动阶段允许 JWT secret 暂空，但会使用临时值完成初次结构校验。
2. 初始化正式日志；`simple` 模式在此明确发出跳过计费和配额的警告。
3. Wire 构建依赖图。`repository.InitEnt` 先初始化时区和 PostgreSQL 连接池，在十分钟超时内执行嵌入式 SQL 迁移，再从配置或数据库补齐系统密钥并执行完整配置校验。`simple` 模式还会补齐默认分组与管理员并发值。
4. 创建 Redis 客户端、仓储、服务、handler、中间件和 Gin server。多个 provider 会在构造后立即启动各自 worker，例如 token 刷新、到期处理、调度快照、用量记录、聚合、清理、备份、批量图片作业和支付订单过期处理。
5. 在 goroutine 中调用 `ListenAndServe`，主 goroutine 等待 `SIGINT` 或 `SIGTERM`。

收到终止信号后先给 HTTP server 五秒完成优雅关闭，停止接收新请求；函数返回时执行应用 `Cleanup`。关闭过程有独立三十秒上下文：大部分互不依赖的 worker 并行停止，然后按顺序停止数据共享/配额等需要 drain 或 flush 的服务，最后关闭 Redis 和 Ent/PostgreSQL。单个关闭步骤失败会记录日志并继续，超时会告警而不会无限阻塞进程退出。

新增有 goroutine、定时器、缓冲写或外部连接的服务时，必须同时回答三个问题：由哪个 provider 启动、停止方法是否幂等、在 Redis/PostgreSQL 关闭前需要完成什么 drain/flush。只加入 Wire provider set 而不加入 `provideCleanup` 会留下关闭竞态。

## 数据所有权

| 存储 | 所有权与使用方式 | 失败或丢失影响 |
| --- | --- | --- |
| PostgreSQL | 用户、身份、团队、Key、分组、渠道、账号、设置、订单、订阅、持久任务、用量和审计的权威状态 | 连接、迁移或密钥初始化失败会阻止完整应用启动；写失败不得由缓存结果伪装成成功 |
| Redis | 缓存、限流、并发槽、会话/粘性、分布式锁、调度快照、队列及跨实例失效；个别短期任务按 TTL 保存在 Redis | 影响依功能而异：安全入口可 fail-close，调度可受控回源，缓存可重建，在途短期任务可能丢失；必须由具体契约定义 |
| 本地数据目录 | 定价快照、日志、前端覆盖、数据共享导出及部分部署配置 | 多实例默认不共享；容器部署必须挂载持久卷并在备份计划中显式纳入 |
| S3 兼容存储 | 备份等可选大对象 | 备份客户端按运行时设置构造，不能在启动时固定旧凭据；对象可用性与数据库元数据生命周期必须协同 |
| Google Cloud Storage | Vertex 批量图片输入、输出和中间 JSONL | 由 Vertex 批量图片 provider 按作业前缀管理；不得向 API 用户暴露内部 URI |
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
- 每个实例都会构造完整后台服务集合。需要单执行者的任务必须使用数据库/Redis 锁或幂等持久化，不能依赖“生产只有一个副本”的假设。

相关入口：[项目总览](../project_overview.md)、[架构目录](index.md)、[运维目录](../operations/index.md)。
