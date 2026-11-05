# TokenRouter 项目总览

本文介绍 TokenRouter 的产品职责、运行组成、仓库目录和核心术语，适合第一次接触项目或判断变更归属时阅读。HTTP 字段、各平台模型表、部署命令和单个函数的行为写在对应分类文档或库外用户手册里。

## 章节导航

- [项目定位与范围](#项目定位与范围)：判断一个需求是否属于本仓库。
- [运行时组成](#运行时组成)：了解进程、数据存储和可选依赖。
- [仓库地图](#仓库地图)：找到代码和资料所在的目录。
- [核心术语](#核心术语)：路由、身份和计费相关名词的含义。
- [运行形态](#运行形态)：首次设置、正常运行和前端交付方式。
- [事实来源](#事实来源)：实现、测试和文档冲突时去哪里核实。

## 项目定位与范围

TokenRouter 是从上游 Sub2API 持续演进而来的 AI API 网关和管理平台。调用方用平台签发的 API Key 访问 Anthropic、OpenAI、Gemini 和其他兼容入口。平台负责认证、模型路由、上游提供商调度、协议转换、故障转移、并发和限额控制、用量记录和计费。浏览器管理端提供用户自助、管理员运营、支付订阅、用量分析和运行观测。

本仓库负责：

- 面向 AI 客户端的网关入口和协议兼容行为。
- 用户、团队、API Key、分组、价格配置、上游提供商，以及它们之间的权限和调度关系。
- 用量、价格、余额、订阅、兑换和支付订单的内部状态与结算。
- 管理前端、公开页面、首次设置流程和服务端配置。
- PostgreSQL schema 演进、Redis 运行状态、后台任务，以及官方构建和部署资产。

上游供应商的可用性、服务条款、模型是否名副其实，以及外部支付机构的最终结算，由各自的服务方负责。`docs/legal/` 存放运行时展示的法律材料，部署和产品使用手册也放在 `docs/` 下；它们需要被目录列出，才算 Project Doc 的当前状态文档。

产品展示名是 TokenRouter，新安装和新发布的技术标识使用 `tokenrouter`。旧的 `sub2api` 配置、导入格式、客户端协议和部署资源可以继续使用；Codex 稳定身份的哈希种子仍是改名前的值。上游 Sub2API 的致谢、用量适配协议和历史 SQL 迁移沿用 Sub2API 这个名字。升级时的兼容规则见[部署与数据库迁移](operations/deployment_and_migrations.md#product_name_compatibility)。

## 运行时组成

TokenRouter 按单实例部署：一个后端进程和一个前端实例，前后端作为同一版本整体升级。前端可以嵌入后端二进制，也可以由独立静态服务器交付；交付方式不改变部署数量和升级约定。

| 组件 | 职责 | 持久性和依赖 |
| --- | --- | --- |
| Go 服务 | Gin HTTP 入口、业务服务、上游客户端、后台 worker 和管理 API | 主进程；用 Wire 装配 handler、service、repository 和基础设施 |
| Vue 3 前端 | 用户和管理员控制台、公开页面和首次设置界面 | Vite 构建；发布构建可以嵌入 Go 二进制，开发时可以单独运行 |
| PostgreSQL | 用户、路由、订单、订阅、用量、任务、运行设置和审计等主数据 | 由 Ent schema、手写 repository 和前向 SQL 迁移维护 |
| Redis | 缓存、限流、并发计数、会话和粘性状态、任务锁、队列和缓存失效通知 | 运行依赖；业务数据以 PostgreSQL 为准。Redis 故障时，部分功能按安全要求关闭或降级 |
| 对象或文件存储 | 批量图片、备份等大对象 | 按功能使用本地数据目录、S3 兼容存储或供应商的对象存储；生命周期见各专题文档 |
| 外部上游 | Anthropic、OpenAI、Gemini、Grok、Qoder 等模型服务 | 经平台适配器访问；可调度范围由提供商能力、代理、价格配置和分组共同决定 |

Go 模块路径是 `github.com/TokenFlux/TokenRouter`。后端 Go 版本以 `backend/go.mod` 为准。前端使用 Vue 3、TypeScript、Vite、Pinia 和 pnpm，Node 版本在 CI workflow 中固定。README 徽章和旧手册里的版本号仅供展示，实际版本以 manifest 和 CI 为准。

## 仓库地图

| 路径 | 内容 | 注意事项 |
| --- | --- | --- |
| `backend/cmd/server/` | 命令行参数、版本信息和进程退出 | 包含版本参数和 Wire 生成入口 |
| `backend/internal/app/` | 应用装配、精简初始化和生命周期 | 改了手写装配代码后要重新生成 Wire；每类资源由所属模块创建和持有 |
| `backend/internal/server/` | HTTP server、中间件顺序和路由注册 | 路由层负责把接口接到 handler，业务规则写在所属模块里 |
| `backend/internal/gateway/` | 入站请求编排、准入、会话、输出和完成处理 | HTTP 适配和单次平台执行分别由各自的模块提供 |
| `backend/internal/upstream/` | 与各平台交换报文、原生请求格式和连接资源 | 凭据持久化在 provider 模块，资金在 billing 模块 |
| `backend/internal/<module>/` | 各业务模块的用例，以及对应的 PostgreSQL、Redis、HTTP 和 provider 适配 | 通用技术实现放在 infra；各模块职责见下方链接的模块地图 |
| `backend/ent/schema/` | 主要持久实体的 Ent schema 源文件 | `backend/ent/` 下其余文件大多是生成代码 |
| `backend/migrations/` | 已发布数据库的前向演进 | SQL 嵌入二进制，按文件名顺序执行；已应用的文件保持原样 |
| `backend/internal/config/` | 启动配置结构、版本升级、显式环境引用和校验 | 数据库里的运行时设置由 settings 和各模块的读取器管理，和启动配置是两套东西 |
| `frontend/src/` | Vue 应用、路由、API 客户端、Pinia store、视图、组件和 i18n | 后端接口变化时，通常要同步类型、调用方和前端测试 |
| `deploy/` | Compose、安装脚本、反向代理基线和运行配置示例 | 和根目录 Dockerfile、GoReleaser、workflow 一起决定发布形态 |
| `.github/workflows/` | 后端 CI、安全扫描和 release 自动化 | 实际的工具链版本和发布触发条件以 workflow 为准 |
| `docs/` | Project Doc，以及库外的用户手册和法律材料 | 各级 `index.md` 列出的文档属于 Project Doc |
| `.agents/skills/`、`tools/` | Agent 技能和仓库维护脚本 | 应用运行时用不到它们；修改时保持脚本的输入输出格式 |

后端完整的包结构、每个包的职责和依赖关系见[后端模块地图](architecture/backend_modules.md)。

## 核心术语

| 术语 | 含义 |
| --- | --- |
| 用户（User） | 登录控制台的主体，拥有余额、并发额度、API Key、订阅，可以加入团队；管理员是带特殊角色的用户 |
| 认证身份（Auth Identity） | 邮箱密码、OAuth、Passkey 等登录身份与用户之间的绑定，和网关 API Key 是两回事 |
| 团队（Team） | 共享的所有权、成员角色和配额范围；团队 Key 的用量归属和权限按团队规则计算 |
| API Key | 网关凭据，同时承载请求级的配额和模型规则；普通 Key 绑定一个分组，复合 Key 用前缀从多个分组中选一个 |
| 分组（Group） | 用户购买或获准使用的产品和路由单位，规定模型、客户端协议、倍率、限额和可关联的提供商，可以同时包含不同平台的提供商 |
| 价格配置（PricingConfig） | 多个分组共享的价格表，按模型和计费规则解析，不按平台拆分；一个分组最多关联一个价格配置，模型映射、白名单和功能设置属于分组 |
| 提供商（Provider） | 上游凭据和运行状态，包含平台类型、代理、资格、模型能力、限流和调度属性 |
| 平台（Platform） | 提供商所属的上游适配器家族，例如 Anthropic、OpenAI、Gemini、Grok 或 Qoder；选中提供商后据此决定认证、协议转换和执行方式 |
| 请求模型 | 客户端传入的模型名，依次经过复合 Key 去前缀、Key 级重定向、分组映射和提供商映射 |
| 上游模型 | 最终发给供应商的模型名或路由键；可以和客户端模型、计费模型不同 |
| 使用记录（Usage Log） | 一次请求的归属、实际执行平台、模型链、token 和媒体用量、价格、状态和诊断信息；聚合和运维查询都从这里取数 |
| 权益 | 可以消费的余额、订阅窗口、额度包、团队限制或 Key 自身配额；结算时按策略综合判断 |
| 运行时设置（Setting） | 存在 PostgreSQL、可以在管理端修改的站点或功能策略；和启动时的版本化 YAML 配置有不同的生命周期 |

提供商（Provider）专指本地配置的上游接入。用户登录账户、Google Service Account、支付渠道账户和第三方协议里的 `account` 字段保持各自原来的含义。SDK 对象、官方域名、授权端点和上游错误原文照原样保留。

## 运行形态

进程启动前先判断是否需要首次设置。尚未配置、也没有开启自动初始化时，进程只启动 setup 路由和嵌入的前端（如果有）；命令行参数 `-setup` 进入终端设置流程。配置完整后，主服务加载启动配置、初始化日志、构建完整的 Wire 依赖图，启动 HTTP server 和后台任务。收到终止信号时，依次停止 worker、刷新缓冲数据，再关闭 Redis 和 PostgreSQL 连接。

所有部署都启用余额、订阅、配额校验和正常结算。页面和接口的访问由角色权限和各自的功能开关控制。Key 和提供商需要关联分组，提供商关联了某个分组，才会进入这个分组的候选池。

前端有两种交付方式：

- 使用 `embed` 构建标签时，`backend/internal/web/dist/` 嵌入 Go 二进制，由同一个 HTTP server 提供 SPA、注入公开设置并添加 CSP nonce。
- 不使用 `embed` 时，Go 服务只提供 API，前端由 Vite 开发服务器或外部静态站点提供。

官方运行资产覆盖四种部署：发布镜像加三服务 Compose、单独的应用容器连接外部 PostgreSQL 和 Redis、源码构建，以及 Apple container 本地环境。具体步骤见库外的[中文部署指南](guides/deployment/index.md)、[Docker 镜像说明](../deploy/DOCKER.md)和 [Apple container 指南](guides/deployment/apple_container.md)；工程上的生命周期约束从[运维文档目录](operations/index.md)进入。

## 事实来源

核实当前行为时，按知识归属选择证据：

1. 对外或跨模块行为，先看当前任务、路由和接口实现、可执行的接口测试和前端调用方。
2. 数据结构看 Ent schema、SQL 迁移和 repository 查询。生成的 Ent 文件用来核对结果，修改时改 schema 源文件。
3. 启动配置看 `backend/internal/config/config.go`、部署示例和配置测试；运行时设置看 Setting service、handler 和对应的前端页面。
4. 构建、测试和发布看 manifest、Makefile、GoReleaser 和 `.github/workflows/`。
5. Project Doc 记录综合多个文件后得出的当前结论。它和代码冲突时，先记为待核实的不一致，再用测试、调用方和提交历史确认实际行为。

继续阅读：[架构](architecture/index.md)、[领域](domains/index.md)、[接口](interfaces/index.md)、[运维](operations/index.md)。
