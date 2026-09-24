# 系统架构

本文描述 TokenRouter 单个应用实例的运行组件、依赖装配、启动关闭和数据所有权，帮助修改进程入口、依赖注入、基础设施实现或前端交付方式时保持跨层契约。本文不展开单次网关请求的模型路由与计费顺序，也不替代具体部署命令。

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
       service 与已迁用例
          |             |
 模块存储/基础设施   上游供应商
       |       |        |
 PostgreSQL  Redis   对象存储/HTTP
```

前端不是独立的业务后端。它通过 `frontend/src/api/` 调用 `/api/v1`，通过 Pinia 保存浏览器会话状态，并由路由守卫根据公开设置、当前用户、角色和功能开关控制页面访问。安全与授权仍由后端路由和中间件执行，前端隐藏页面不构成权限边界。

<a id="dependency_layers"></a>
## 依赖层次

`backend/internal/app/wire.go` 是完整应用依赖图的手写入口，旁边的 `wire_gen.go` 是生成结果。 根入口按模块引用 `assembly_*_wire.go` 集合，保留现有子集合；跨模块接口绑定与具体提供者放在同一 Wire 集合内。生命周期登记另有独立集合，这些文件只参与 wireinject 构建。剩余执行构造在 app 的 `gatewayExecutionProviders` 分组，旧 service ProviderSet 已删除；响应头配置也由 app 投影给 egress，再把不可变编译结果注入各执行入口。`cmd/server` 保留参数解析、构建版本变量和原 `go generate ./cmd/server` 入口。配置只加载一次，日志、数据库引导和应用图共用该配置；JWT secret 在数据库引导完成后补齐并重新校验。

| 层 | 主要路径 | 当前责任 |
| --- | --- | --- |
| 组合根 | `internal/app`、`app/bootstrap`、`app/lifecycle` | 配置投影、Wire 绑定、初始化、统一启停、失败回收和重启请求 |
| 配置 | `internal/config` | 默认值、YAML/环境变量加载、归一化与启动校验 |
| 已迁用例 | `internal/settings`、`idempotency`、`site`、`billing`、`identity`、`team`、`apikey`、`routing`、`account`、`egress`、`scheduler`、`usage`、`audit`、`ops`、`notification`、`moderation`、`search`、`creative`、`batchimage` | 设置、幂等、公告、资金与权益、身份/团队/Key、路由目录、账号管理与维护、出站策略、调度/并发/会话选择、用量/观测、通知、审核、搜索及创作/批量任务 |
| 剩余执行适配 | `internal/service` | 平台单次执行、WS/Live 与少量请求策略适配通过 app 固定端口接入；旧聚合 ProviderSet 与 repository 包已删除 |
| 平台执行 | `internal/upstream` 与各平台子包 | 供应商交换、原生报文、媒体、单次执行和连接资源；业务凭据写入由 account 提供 |
| 通用技术实现 | `internal/infra` | PostgreSQL/迁移、Redis/会话/限流/锁、HTTP 池、proxy/TLS、时间轮、日志/timing 和 AES |
| HTTP 适配与服务器 | `site/httpapi`、`billing/httpapi`、`identity/httpapi`、`team/httpapi`、`apikey/httpapi`、`idempotency/httpapi`、`routing/httpapi`、`account/httpapi`、`egress/httpapi`、`scheduler/httpapi`、`notification/httpapi`、`moderation/httpapi`、`search/httpapi`、`gateway/httpapi`、`creative/httpapi`、`batchimage/httpapi`、`server`、`web` | 输入输出、认证中间件、路由汇总、HTTP 参数与静态资源 |

settings 的通用实现位于 `settings` 与 `settings/postgres`，app 直接构造唯一 Store，并将同一对象绑定到存取接口；旧 `SettingService` 聚合、构造和转接已经删除，生产与测试直接使用所属模块能力。idempotency 的核心、观察出口与 SQL Adapter 已独立，app 从 Ent 驱动取得同一 SQL 连接池，直接投影协调器和清理任务的配置；所有需要幂等的用户与管理员 HTTP 处理器由 app 显式绑定同一协调器，默认期限随该实例读取；协调器与清理任务各自接收日志观察出口，旧 service 委托、进程默认协调器和全局观察绑定已删除。时间轮也由 app 直接构造，仍由统一生命周期启动。资金、任务、探测和上游客户端的原生存储与构造器由 app 分组绑定，不再经过 repository 聚合入口。site 拥有公告实体、targeting、用例和到期 worker，HTTP 与 PostgreSQL Adapter 分开；Ent schema 与生成代码直接使用所属模块类型，旧 domain/model 包已删除。

执行账号不再使用旧 Account/AccountGroup 实体。`gateway/provider.ExecutionAccount` 只组合原生 `account.Record` 与独立 `AttemptRoute`；它不参与数据库或缓存编码，账号规则仍由 Record 唯一实现。管理 DTO、无凭据候选快照和执行目标保持分开。app 中的执行存储适配只调用同一 account/billing PostgreSQL 实例并投影结果，CAS、事务、字段保护、outbox 和消费累计没有移入 app。

账号健康观测、恢复、Team 联动和运行时阻断由 app 独立构造并直接发布；旧 RateLimitService 已删除。平台执行与调度诊断不再经健康服务取得参数，生产共享反馈和参数缓存仍由 schedulerSharedState 拥有。 账号选择、无槽探测、会话粘性和评分诊断直接绑定 `gateway/provider/selection`，由原生 scheduler 核心执行；旧网关选择方法与事后调度绑定已删除。选择与固定账号 WS 复核共享同一资格实现、运行阻断和模型短暂失败状态。

`protocol` 拥有协议值、各方言报文和 `bridge` 转换状态；`routing/capability` 拥有原生集合、准入及单步 fallback，`routing` 拥有 effort 映射规则。`billing/pricing` 拥有价卡、目录解析、费用与展示计算，`billing/provider` 拥有目录加载、热更新及唯一运行缓存。旧 apicompat/domain、PricingService、BillingService 和 ModelPricingResolver 转接已删除，消费者直接使用原生目录、计算器与解析器。billing 的 Calculator、PriceResolver 和资金分配规则接收显式投影；普通结算与任务资金由 Funds 进入 billing/postgres 的闭合事务，Redis 缓存位于 billing/rediscache。供应商交换、报文与流解析由 upstream 各平台实现；网关完成处理由 gateway/completion 消费已冻结输入，其记录器、隔离倍率缓存和提交后端口直接在 app 装配。支付订单编排由 payment 拥有，纯定价和协议不读取配置或 I/O。

公告与 billing 的用户读取由 app 直接投影 identity；公告有效订阅直接适配 billing 存取接口，匹配规则仍由 site 执行。模块不导入 app。剩余执行适配由 app 分组构造，应用级启停和模块绑定由 app 管理，不能通过搬动目录给新代码继承历史依赖许可。

身份的注册、绑定、会话和强认证进入 identity，团队事务进入 team，Key 的访问快照、L1/L2 与认证 outbox 进入 apikey。旧 `service.AuthService` 及其注册、绑定和 OAuth 转接已删除；认证测试直接组合 identity 原生用例与存储端口，JWT 中间件测试直接使用 SessionService。仍保留的 HTTP 测试门面显式接收事务连接，不再通过旧认证服务取回核心或连接。app 构造唯一生产实例与事务参与工厂，旧 repository 包已删除，存储直接绑定所属模块；跨模块写入沿用现有 Ent context 与调用方连接。分组/渠道由 routing、账号管理与维护由 account、代理与 TLS 策略由 egress 提供；通知直接绑定 notification；推广支付仍经窄端口连接旧图，账号授权与刷新通过 provider 端口调用 upstream 的供应商交换。 Agent Identity 协调器也由 app 构造一次；各入口保留自身未持久账号互斥，已持久账号仍共用原进程内按账号锁。模型匹配由纯 `routing/modelmap` 共享，网关仍拥有请求改写顺序。

app 直接构造身份、账号、路由、推广、用量、审计、面板与后台模式的设置读取器，共享同一 settings.Store；网关的执行端口只引用这些已构造实例，不再借助旧设置聚合取得缓存；认证与配置合同测试也使用同一组原生接口。站点名称、菜单和前端地址由 site.DisplaySettings 按原时点读取，公开投影、搜索运行时和提示规则不依赖旧设置聚合的构造。登录、TOTP、Passkey 与邮件挑战直接绑定原生能力；普通和管理员 Key、管理员用户、用户属性的 HTTP 构造由 app 完成。旧 AdminService 聚合及其构造器已删除；身份、Key、分组、账号、代理和兑换管理分别直接使用 identity、apikey、routing、account、egress、billing 的用例，跨模块授权和资金写入仍通过原连接参与接口协作。订阅与兑换的旧 service 构造转接也已删除，调用方直接绑定 billing 原生用例和 PostgreSQL 事务参与端口。邮件呈现与验证码生命周期分别属于 notification 和 identity，生产图不再构造旧 EmailService。Messages、OpenAI 文本、WS、媒体和辅助入口，以及 Qoder、搜索、Live 与计数均由 app 直接构造原生 HTTP 适配。OpenAI 的文本/WS/媒体共享同一尝试端口、资源与完成器；Wire 不再构造旧 Handler，也不需要其事后完成绑定屏障。旧 handler 包及其精确许可已删除；单模块合同迁到实际所有者，跨入口合同由 app 的原生绑定配合函数句柄夹具执行，并发替身统一在 HTTP testkit，禁止进入生产。剩余平台单步与策略适配仍在 service，最终全项验收也尚未完成，不能据此宣布 S16 完成。

Messages、Claude 兼容转换和计数的固定执行依赖由 app 注入 `gateway/provider/messageforward`，生产图已删除通用 `GatewayService`。其路由计划、会话、冷却与完成处理分别直接绑定原生拥有者；调试文件由 requestdebug 共享并登记关闭。Gemini 与 Antigravity 的请求准备已进入 `gateway/provider/googleforward`，HTTP 输出由 `GeminiExecutor`、`AntigravityExecutor` 接入。账号健康、凭据与平台重试复用原生拥有者，旧两个平台 Service 已删除。OpenAI、Grok、WS/Live 及共享转接仍待退出，S16 尚未完成最终验收。

`usage` 拥有用量事实、统计口径、查询缓存、Dashboard、聚合与清理；`audit` 拥有通用操作审计；`ops` 拥有观测查询、队列、采样、告警、报告和发布查询。各模块的 HTTP、PostgreSQL、Redis 与技术 provider 通过独立端口接入。app 绑定唯一生产实例并投影身份、账号、并发和认证健康数据；旧 `UsageLog` 的关联形状只通过展示投影兼容，不进入新事实模型。用户最后活动排序、Key 最近使用 IP 和团队用量由 `usage/postgres/query` 参与调用方原有连接与查询，排序继续发生在分页前。

`upstream` 按平台持有供应商认证交换、签名、原生请求/响应、媒体和连接资源；通用 wire 仍使用 protocol。账号授权会话、凭据缓存与条件写入由 account 拥有。HTTP、SSE、WebSocket、Live、计数与模型入口直接绑定 gateway/httpapi；文本、媒体和会话编排分别由 gateway/text、media、ws、live 拥有。Qoder Chat 继续使用固定 gateway.Execute，Messages/Responses 显式保留其字节提交和等待契约差异。平台执行的混合适配随 S11 逐批收敛，保持每请求唯一账号尝试循环。每次上游执行只接收明确投影，通过同步输出端口写出，完整输入授权、结算与完成队列不下沉到具体平台。

Anthropic 请求指纹由原生 `RequestFingerprint` 直接注入，与用户身份模块无关。Qoder 授权 HTTP、刷新消费者和生命周期直接绑定 `account/provider.QoderAuthorization` 及其唯一账号授权状态；app 只提供代理读取。仪表盘聚合和支付订单维护的生命周期也直接绑定 usage/payment 的原生拥有者，不再经旧服务包装启动。

notification 拥有模板、语言/退订、投递协调、队列与 SMTP Adapter；identity 拥有验证码和重置凭据，billing/account/业务用例先确定通知事件，通知模块不反向查询资金或账号。site 同时拥有公告、页面权限和公开信息投影，文件读取位于 site/filesystem；web 只接收公开投影。moderation 的规则、裁决、观测和记录使用自己的核心与 Adapter，用户写入通过 identity 命令或同连接参与能力完成。search 拥有配置发布、供应商选择和额度意图，Brave/Tavily HTTP 与 Redis 状态分别进入 Adapter；gateway/searchtools 拥有工具协议、账号三态启用规则与合成结果，app 直接绑定唯一 search 注册表，旧全局 Manager 转接已经删除；gateway/completion 保持原完成资格、资金与分析事实的次序。

creative 与 batchimage 各自拥有任务创建、状态、恢复、结果读取和完成资格；HTTP、PostgreSQL、Redis 与平台 Adapter 分离。app 固定两个 Public 核心，创作 worker 直接接收原生 Executor，批量任务提交、轮询、下载和清理共享一个 provider registry；两类任务仍保留独立队列与素材生命周期。资金只调用 billing.Funds，任务表更新通过本次 SQL Tx 的参与者完成。创作台先持久化供应商成功元数据与 outbox，再短暂重试 Redis 输出保存；结果不可交付时按已确认成功捕获资金并记为 result_lost，不重新推理。

`pkg/apperror`、`pagination`、`timezone`、`ipmatch`、`oauthpkce`、`logredact` 提供通用值类型与计算；`server/httpx`、`server/clientip` 拥有 HTTP 适配。错误、日志、代理/TLS、IP、搜索及统计的旧 pkg/util 转接已删除，技术日志事件由 `infra/telemetry/logevent` 定义；协议转换和请求状态的剩余旧入口仍在收尾。

领域错误由所属模块定义，调用方直接引用该错误，不再经旧 service 变量转接。存储缺失、HTTP 映射和跨模块判定继续使用原错误对象及错误链；迁包不能重新创建同文案错误来代替其身份。

`app/http_transport.go` 按原读取时点投影传输参数，`gateway/provider/transport` 组合请求策略与平台传输原语，`infra/httpclient.UpstreamPool` 拥有唯一客户端缓存和请求释放。OpenAI HTTP/2 回退策略由 egress 提供，Grok CLI Header 与可重放 403 回退由 upstream/grok 提供；传输适配不引用旧 service 或完整 config。隐私请求仍使用原共享 req 池、30 秒超时和 Chrome 指纹。修改 provider 后运行保留的 Wire 生成命令，不编辑生成文件，也不因纯装配变化运行 Ent 生成。

<a id="startup_and_shutdown"></a>
promotion 拥有邀请码/关系、返利、转入余额及 Promo，payment 拥有配置、渠道绑定、下单、查单、履约和退款。各自 HTTP/PostgreSQL Adapter 只负责传输与存储；billing 同连接参与资金写入，notification 接收已确定的支付事件。app 持有唯一配置、注册表、选择器、订单用例与后台任务，旧 service/handler 名称只保留兼容投影。退款渠道调用位于短事务之间，恢复记录使用既有支付审计表，具体保证见[支付与权益](../domains/payments_and_entitlements.md#退款)。

## 启动与关闭

主入口保持三条互斥路径：`-version` 只输出构建信息，`-setup` 执行 CLI 安装；未安装时执行 AUTO_SETUP 或启动独立 setup server；已配置时构造完整应用。setup 的迁移调用精简 bootstrap，不构造业务 worker。

完整应用先初始化日志，再由 bootstrap 初始化时区和 PostgreSQL，在原十分钟迁移预算内执行迁移与暂时错误重试，补齐持久 JWT secret、完整校验配置，并在 simple 模式补齐默认分组和管理员并发。Ent 与原生 SQL 共享连接，只有一个关闭拥有者。

app 在 bootstrap 成功后固定共享 Calendar，显式传入用量、支付、推广、团队 HTTP 和计费、账号、路由装配。用量存储、聚合事务、仪表盘按日缓存、团队日统计、平台额度存储、资金结算、Key 按日计数与公开站点时区展示均使用注入的日期对象。用户时区覆盖及无效值回退仍按各入口原有规则执行，团队查询保持只使用服务端时区。

`pkg/timezone` 只保留 Calendar 与时间文本解析，旧全局状态、日期函数、初始化和日志入口已删除。生产中只有 `app/bootstrap.InitTimezone` 写入 `time.Local`，保留默认上海时区、错误文本和初始化顺序。仍待清理的旧装配和适配显式捕获 `time.Local`，不再通过全局 timezone 包取得状态；这些消费者随旧应用图清理，不代表 S16 已全部完成。

Wire 构造对象并登记资源后，lifecycle 才启动后台工作。时间轮和设置/定价预热先完成，再启动缓存订阅及消费队列，最后启动周期生产者、任务拉取和 HTTP。原有首次执行、预热降级、功能开关和动态 worker 数量保持各模块语义。构造或部分启动失败时回收已取得及已尝试启动的资源，错误链保留原始原因。

定价 provider 由 `app/pricing.go` 投影独立 Options，继续走原 PricingInitialization 与 PricingService hook 的 Initialize → Start → Stop 顺序。远端客户端和运行实例直接使用 billing/provider，旧 PricingService 包装已删除。Calculator、PriceResolver 和 ChannelService 也由 app 直接提供原生实例，计算与查价消费者不再通过旧服务取回内嵌对象。平台模型别名和动态 Grok 默认值由 gateway/provider/modelidentity 投影，每次查价只取得一次快照；纯定价不读取平台运行状态。Key 与渠道模型追踪由 gateway/modeltrace 组合。`app/legacybridge` 已删除，剩余旧 service 适配仍逐项清理。

billing 的余额/Key 缓存队列、平台额度 flusher 和订阅过期提醒由 app 绑定到现有生命周期。提醒保留立即首轮、每分钟扫描和既有 Redis/数据库 leader 策略，停止时取消并等待在途操作。没有生产消费者的订阅维护队列不会因迁包自动启动。

搜索运行时先于 HTTP 开放初始化，所有配置代次共享在途计数；配置替换只退役旧客户端的空闲连接，不取消已进入的搜索。关闭时停止新搜索并等待额度清理。审核先封闭队列并等待；邮件队列在通知生产者之后排空，预算耗尽会取消 SMTP 并报告未完成项。HTTP 五秒与后台三十秒总预算保持独立，超时不能被报告成排空成功。

任务 HTTP 的提交、下载与管理调用由 TaskRequestsAndDownloads 关闭屏障跟踪。HTTP 退出后先封闭新调用、等待已有调用，再停止任务拉取与恢复循环。worker 停止不可逆，重复停止共享结果；创作台生成阶段的用户/账号槽由 scheduler.Lease 逆序释放。任何阶段超时都保留后续共享依赖，不能把预算结束描述成任务全部完成。

identity 的会话、TOTP、资料操作和 pending 存取，以及 apikey 的过期、活动时间、滥用限制与 outbox 取时，由 app 在构造时注入系统时钟函数。各原取时点继续独立读取，JWT 库内验证与签发使用相同来源；团队与成员额度的日期对象继续保留原时区和 DST 边界。

认证缓存构造不启动后台任务，Start 才创建 L1 和 Redis 订阅。停止时先拒绝新的认证认领，等待在途认证、活动时间写入和失效调用，再关闭订阅与 L1；预算超时保留正在使用的依赖并报告未完成。outbox worker 停止新认领并等待当前批次，持久化重试及延迟二次失效留待后续启动，不宣称全部排空。钉钉资料同步使用统一任务跟踪器，继续与请求取消解耦并保留 30 秒单任务预算。

SIGINT、SIGTERM、监听失败和 Linux 手动重启进入同一关闭流程。HTTP 有独立五秒优雅关闭预算，随后后台清理使用独立三十秒总预算：先关闭额外监听、Live 本地观察与 hijack 连接，等待完整 handler 返回，再停止周期生产者和任务拉取，逐层排空用量、缓存写入、额度镜像、延迟写回、通知和审计，最后关闭订阅、时间轮、空闲 HTTP 连接、Redis、Ent/SQL 和日志文件。

请求跟踪只包装 Handler，保留原 ResponseWriter 的 Flush/Hijack 能力；客户端断开后仍按原策略收集用量的 handler 必须先完成，不能直接以连接断开替代请求清理完成。现有异步额度写入、通知、探针和快照任务通过消费者侧的完成接口登记，保留其 context、并发和参数求值语义；每层关闭后等待该层派生的副作用，再关闭下层依赖。Live 观察停止只关闭本地资源，不因进程退出提前把远端会话判定为已结束。错误透传、TLS profile/router 和鉴权缓存订阅会等待最后一次回调结束。运维错误日志队列会处理完已入队批次；QPS WS 缓存的空闲定时器和按需刷新也在 HTTP 收尾后停止。WS 池关闭后禁止按需重建或重新预热，已租赁连接保留原请求收尾策略，在租约归还时释放。

Stop 和 Cleanup 共享一次执行结果。超时报告未完成任务，停止推进依赖资源的关闭并以失败状态结束进程；进程退出不代表 drain 成功。旧单步 Stop 的无界等待以及日志报告都受应用总预算约束。日志轮转仍使用原 lumberjack 算法，文件句柄由应用最终关闭；该库内部维护循环保留其既有进程生命周期，不把它宣称为可单独停止的应用 worker。

应用派生任务由 `lifecycle.Tasks` 统一等待，网关快照写回、免费额度统计刷新、Live observer、审核和结算副作用通过实例端口接入；不再安装旧全局后台任务 runner。其关闭仍先于邮件及共享存储资源，保持在途任务派生工作的原有等待规则。

新增 goroutine、定时器、队列或连接时，必须登记实际拥有者、启动点、接收封闭方式和完成等待。按需资源由已有拥有者管理，不能在运行时从业务模块反向调用 app 注册新组件。应用清理表按职责拆在 app 的运行时绑定文件中，并与 Wire 图一起验证。

调度快照、并发、串行队列和运行反馈由 app 绑定唯一 scheduler 实例，构造不启动。快照启停与网关读取直接调用 `scheduler.SnapshotService`，账号事件通过其窄发布端口更新快照；旧执行实体的转换只留在执行边界。模型目录短缓存由同一时间轮直接清理 `routing.ModelList`，不经旧网关包装。完整 handler 结束后停止调度新认领、取消等待并等待在途资源释放，再关闭 Redis/SQL。快照的初始重建仍异步，重复启动不重建，停止后不重开；遗留持久 outbox 留待下次消费或周期重建恢复。

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

Ent schema 是主要实体的代码模型，手写 SQL 迁移是已部署数据库的演进权威。各模块 PostgreSQL Adapter 与保留的 repository 同时使用 Ent 和底层 `*sql.DB` 完成复杂聚合、批量更新及显式事务；两种访问方式共享同一连接池。

## HTTP 与前端交付

app 将进程配置投影为 `server.Options`，并提供已装配的全局 middleware 与路由注册函数。server 不接收旧 APIKey、订阅、Ops 或 SettingService；各模块拥有注册函数，app 的认证、用户、管理员、网关和支付装配分别注入原生处理器；全局 Handlers/AdminHandlers、旧 server/routes 和旧 DTO 聚合已经删除。综合设置、预聚合及创作设置也直接绑定原生处理器；`settings/composite` 组合所属模块的读取、准备和应用，不持有第二份缓存。CORS/CSP 参数在 `server/httpconfig`，认证与 Backend 模式门禁由身份 HTTP 实现。`ProvideHTTPServer` 统一设置监听地址、请求头限制、header/idle timeout、可选全局请求体限制和 h2c。长时间 SSE 与 WebSocket 要求不设置全局 `WriteTimeout`，大请求体也使服务不设置全局 `ReadTimeout`；更细的 body 限制、并发和超时由路由或上游客户端执行。

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

分组管理直接读取 AccountStore 与 KeyStore，容量查询直接读取账号轻量投影，身份/Key/billing 的分组和渠道读取直接绑定 routing。平台目录、动态设置和调度来源由 app 按职责直接组合；剩余平台执行消费者继续通过现有 service 适配，未复制规则或缓存。`account_groups`、代理联动及账号资金重置由同连接参与能力协作，不新增事务 context；配置更新不覆盖独立消费与运行字段。具体契约见[路由与计费](../domains/routing_and_billing.md)、[账号维护](../operations/account_maintenance.md)和[出站传输](../operations/upstream_transport_security.md)。

<a id="backup_and_maintenance"></a>
## 备份与系统维护装配

app 构造唯一 backup 核心、归档执行器、动态存储工厂、ops/maintenance 更新用例与系统操作锁。维护的停止认领和取消先于 HTTP 请求等待，维护收尾及备份任务等待完成后才关闭共享 SQL/Redis。旧 service/repository/handler 入口只提供已登记的构造投影或委托，不持有第二个 cron、存储缓存或操作锁。

setup 只调用 app/bootstrap 的连接测试、迁移和身份初始化能力；identity/postgres 拥有首次管理员及 simple 管理员并发补齐，routing/postgres 拥有 simple 默认分组。它们不走普通注册或管理用例，不触发赠送、通知或后台 worker。两个维护命令在主体返回前关闭已取得连接，再由 main 决定退出码。
