# TokenRouter 项目总览

本文说明 TokenRouter 的产品责任、运行边界、仓库所有权和跨模块核心术语，供首次进入仓库或判断变更归属时建立共同上下文。本文不枚举具体 HTTP 字段、平台模型表、部署命令或单个函数行为；这些内容由对应分类文档或库外用户手册负责。

## 章节导航

- [项目定位与边界](#项目定位与边界)：判断一个需求是否属于本仓库。
- [运行时组成](#运行时组成)：理解进程、数据存储和可选依赖。
- [仓库地图](#仓库地图)：确定代码和资料的规范所有者。
- [核心术语](#核心术语)：统一路由、身份与计费语义。
- [运行形态](#运行形态)：区分首次设置、业务模式和前端交付方式。
- [事实来源](#事实来源)：在实现、测试和文档冲突时选择核实入口。

## 项目定位与边界

TokenRouter 是基于上游 Sub2API 持续演进的 AI API 网关与管理平台。调用方使用平台签发的 API Key 访问 Anthropic、OpenAI、Gemini 及其他兼容入口；平台负责认证、模型路由、上游账号调度、协议转换、故障转移、并发与限额控制、用量记录和计费。浏览器管理端同时提供用户自助、管理员运营、支付订阅、用量分析和运行观测能力。

本仓库拥有以下边界：

- 面向 AI 客户端的网关入口和协议兼容行为。
- 用户、团队、API Key、分组、渠道、上游账号及其权限和调度关系。
- 用量、价格、余额、订阅、兑换和支付订单的内部状态与结算。
- 管理前端、公开页面、首次设置流程和服务端配置。
- PostgreSQL schema 演进、Redis 运行状态、后台任务以及官方构建部署资产。

上游供应商自身的可用性、服务条款、模型真实性和外部支付机构的最终结算不由本系统保证。`docs/legal/` 是运行时展示的法律材料，部署与产品使用手册也可以位于 `docs/`；除非被目录规范列出，它们都不是 Project Doc 的当前状态权威来源。

仓库仍保留若干 `sub2api` 名称，包括二进制、服务名、环境变量、数据路径和 Go 包内兼容标识。它们是现有部署及上游兼容契约，不应仅为品牌统一而机械替换。

## 运行时组成

| 组件 | 责任 | 持久性与边界 |
| --- | --- | --- |
| Go 服务 | Gin HTTP 入口、业务服务、上游客户端、后台 worker 和管理 API | 主进程；通过 Wire 装配 handler、service、repository 和基础设施依赖 |
| Vue 3 前端 | 用户与管理员控制台、公开页面和首次设置界面 | Vite 构建；发布构建可嵌入 Go 二进制，开发构建可独立运行 |
| PostgreSQL | 用户、路由、订单、订阅、用量、任务、运行设置和审计等权威数据 | 由 Ent schema、手写 repository 和前向 SQL 迁移共同维护 |
| Redis | 缓存、限流、并发计数、会话/粘性状态、分布式锁、队列和跨实例失效通知 | 运行依赖；不能被当作业务权威数据库，部分功能在 Redis 故障时按安全要求关闭或降级 |
| 对象/文件存储 | 批量图片、备份和数据共享导出等大对象 | 按功能使用本地数据目录、S3 兼容存储或供应商对象存储；生命周期由各专题拥有 |
| 外部上游 | Anthropic、OpenAI、Gemini、Grok、Qoder 等模型或账号服务 | 通过平台适配器访问；账号能力、代理、渠道和分组共同限制可调度范围 |

Go 模块路径为 `github.com/BrandonVee/TokenRouter`。后端以 `backend/go.mod` 声明的 Go 版本为准；前端使用 Vue 3、TypeScript、Vite、Pinia 和 pnpm，Node 版本由 CI workflow 固定。README 徽章或旧手册中的版本只用于展示，不能覆盖 manifest 和 CI。

## 仓库地图

| 路径 | 规范责任 | 注意事项 |
| --- | --- | --- |
| `backend/cmd/server/` | 主进程入口、版本信息、Wire 依赖图和有序关闭 | `wire_gen.go` 是生成物；修改依赖图应改 `wire.go` 后重新生成 |
| `backend/internal/server/` | HTTP server、中间件顺序和路由注册 | 路由只负责接口装配，业务不变量应留在 service/domain 层 |
| `backend/internal/handler/` | HTTP/协议适配、输入输出和网关 attempt 编排 | 同时包含普通面板 handler 和多协议网关 handler |
| `backend/internal/service/` | 核心业务、调度、计费、协议转换和后台运行时 | 跨 repository 的不变量通常由这里拥有 |
| `backend/internal/repository/` | PostgreSQL、Redis、对象存储和外部基础设施实现 | 包含迁移执行器和缓存实现；失败语义会影响 service 层降级 |
| `backend/ent/schema/` | 主要持久实体的 Ent schema 源 | `backend/ent/` 下其余大部分文件为生成代码 |
| `backend/migrations/` | 已发布数据库的前向演进 | SQL 被嵌入二进制并按文件名执行；已应用文件不可改写 |
| `backend/internal/config/` | 启动配置结构、默认值、环境映射和校验 | 数据库中的运行时设置由 Setting 相关 service/handler 负责，不等同于启动配置 |
| `frontend/src/` | Vue 应用、路由、API 客户端、Pinia store、视图、组件和 i18n | 后端 API 契约变化通常需要同步类型、调用方和前端测试 |
| `deploy/` | Compose、安装脚本、反向代理基线和运行配置示例 | 与根 Dockerfile、GoReleaser 和 workflow 共同定义发布形态 |
| `.github/workflows/` | 后端 CI、安全扫描和 release 自动化 | 实际工具链版本和发布触发条件以 workflow 为准 |
| `docs/` | Project Doc 与库外用户/法律资料 | 只有各级 `index.md` 规范列出的文档属于 Project Doc |
| `skills/`、`tools/` | 仓库专用操作技能和维护工具 | 不属于应用运行时；变更时仍需遵守相应输入输出契约 |

## 核心术语

| 术语 | 含义 |
| --- | --- |
| 用户（User） | 登录控制台并拥有余额、并发、API Key、订阅及可选团队关系的主体；管理员是带特殊角色的用户 |
| 认证身份（Auth Identity） | 邮箱密码、OAuth、Passkey 等外部或本地登录身份与用户之间的绑定，不等同于网关 API Key |
| 团队（Team） | 共享所有权、成员角色和配额作用域；团队 Key 的用量归属和权限不能退化为个人 Key 规则 |
| API Key | 网关凭据及请求级配额/模型规则载体；普通 Key 绑定一个分组，复合 Key 通过前缀选择多个分组之一 |
| 分组（Group） | 用户可购买或获准使用的产品/路由边界，定义平台、模型、倍率、限额及可关联账号 |
| 渠道（Channel） | 分组与上游能力之间的配置层，拥有模型映射、价格、功能和限制；同一分组当前最多关联一个渠道 |
| 账号（Account） | 实际上游凭据与运行状态的载体，包含平台类型、代理、资格、模型能力、限流和调度属性 |
| 平台（Platform） | 决定协议处理器和上游适配器家族的标识，例如 Anthropic、OpenAI、Gemini、Grok 或 Qoder |
| 请求模型 | 客户端表达的模型名；可能依次经过复合 Key 去前缀、Key 级重定向、渠道映射和账号映射 |
| 上游模型 | 最终发送给供应商的模型或路由键；与客户端模型、计费模型不必相同 |
| 使用记录（Usage Log） | 一次请求的归属、模型链、token/媒体用量、价格、状态和诊断信息；也是聚合与运维查询的原始来源 |
| 权益 | 允许消费的余额、订阅窗口、额度包、用户×平台配额或 Key 自身配额；不同来源按结算策略共同判定 |
| 运行时设置（Setting） | 保存在 PostgreSQL、可由管理端更新的站点或功能策略；与启动时 YAML/环境变量配置分属不同生命周期 |

## 运行形态

进程启动前先判断是否需要首次设置。未配置且未启用自动初始化时，仅启动 setup 路由和可用的嵌入前端；CLI `-setup` 走终端设置流程。配置完整后，主服务加载启动配置、初始化日志、构建完整 Wire 依赖图，启动 HTTP server 与后台运行时，并在收到终止信号时有序停止 worker、刷新缓冲数据、关闭 Redis 和 PostgreSQL。

业务模式分为：

- `standard`：默认完整模式，启用用户面、余额/订阅/配额校验和 SaaS 相关功能。
- `simple`：内部简化模式，隐藏或拒绝部分用户/支付接口，并跳过正常的余额和订阅扣费；它不是仅用于调试的开关。

前端交付分为：

- `embed` 构建标签：把 `backend/internal/web/dist/` 嵌入 Go 二进制，由同一 HTTP server 提供 SPA、注入公开设置并应用 CSP nonce。
- 非 `embed` 构建：Go 服务只提供 API，前端需要由 Vite 开发服务器或外部静态站点单独提供。

官方运行资产覆盖发布镜像三服务 Compose、仅应用容器配合外部 PostgreSQL/Redis、源码构建和 Apple container 本地栈。具体步骤以库外 [中文部署指南](guides/deployment/index.md)、[Docker 镜像说明](../deploy/DOCKER.md) 和 [Apple container 指南](guides/deployment/apple_container.md) 为准；工程生命周期约束由[运维文档目录](operations/index.md)路由。

## 事实来源

核实当前行为时按知识所有权选择证据，而不是固定认为某种文件永远优先：

1. 对外或跨模块行为先看当前任务、路由/接口实现、可执行契约测试和前端调用方。
2. 数据结构看 Ent schema、SQL 迁移和 repository 查询；生成的 Ent 文件用于验证结果，不作为首选编辑源。
3. 启动配置看 `backend/internal/config/config.go`、部署示例和配置测试；运行时设置看 Setting service、handler 及对应前端。
4. 构建测试和发布看 manifest、Makefile、GoReleaser 与 `.github/workflows/`。
5. Project Doc 记录跨文件后的当前结论；若与代码冲突，先把它视为漂移候选并用测试、调用方和历史核实，不凭文档反向猜测实现。

继续阅读：[架构](architecture/index.md)、[领域](domains/index.md)、[接口](interfaces/index.md)、[运维](operations/index.md)。
