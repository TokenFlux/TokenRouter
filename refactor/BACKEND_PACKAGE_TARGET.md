# TokenRouter 后端包结构与代码解耦重构方案

> 状态：目标方案，尚未实施。本文的完成只代表方案已经编写，不代表代码迁移完成。
>
> 清点基线：2026-09-10，当前 `main`，HEAD `536407323f3be74020a972ee32576677e9d6829d`。基于工作区实际文件清点，包含当时尚未提交的内容。
>
> 范围：后端全部现有 Go 包的目标归属、混合包内部职责拆解、模块之间的 Interface，以及分阶段迁移安排。本文是重构总计划，保存在 `refactor/BACKEND_PACKAGE_TARGET.md`，不作为 Project Doc 的当前架构事实。
>
> 执行方式：每个阶段开始实施前，先用计划模式生成当前阶段的迁移子计划，原样保存到 `refactor/` 下的独立文件。具体接口、事务参与、兼容方式和阶段内顺序在子计划中决策；总计划末尾的 roadmap 只追踪阶段进度和子计划入口。

## 导航

- [1. 目标与约束](#1-目标与约束)：重构要解决什么，哪些运行契约必须保留。
- [2. 最终目录](#2-最终目录)：最终布局和模块内部约定。
- [3. 模块关系与接口设计](#3-模块关系与接口设计)：所有权、允许依赖、事务和请求生命周期。
- [4. 旧包逐项映射](#4-旧包逐项映射)：112 个现有含 Go 文件目录的去向。
- [5. 混合包的内部拆分](#5-混合包的内部拆分)：service、repository、handler、domain 等如何具体迁移。
- [6. 多步迁移计划](#6-多步迁移计划)：S00—S16 的前置条件、处理范围、代码重构和验收。
- [7. 每次实施的方法与验收](#7-每次实施的方法与验收)：提交、兼容、测试、回退和同步上游。
- [8. 最终完成标准](#8-最终完成标准)：何时能够删除旧包并认定重构完成。
- [9. Roadmap](#9-roadmap)：各阶段状态及其子计划入口。

## 1. 目标与约束

### 1.1 采用模块化单体

保持一个 Go module、一个应用进程和现有 PostgreSQL/Redis 运行形态。按业务所有权建立 Module（模块），通过小而完整的 Interface（调用方契约）隐藏内部复杂度；Interface 包括参数、返回结果、状态约束、失败语义、顺序和性能要求，不只是 Go 的 interface 声明。

本轮设计同时安排包移动和代码解耦。一个阶段的结果应让维护者能在模块内部完成常见变更，让调用方少了解几项内部规则。仅把 `service` 拆成多个目录、把复杂调用换成同样复杂的接口，都不足以验收。

设计取舍：

| 选择 | 原因与代价 |
| --- | --- |
| 业务模块内放 HTTP/存储 Adapter（具体适配实现） | 同一功能的规则、接口和存储更容易一起维护；接受少量重复的适配目录名称。 |
| 共享 PostgreSQL，显式维护跨表事务 | 现有结算、退款、团队转移和任务资金状态需要原子性；不按表拆成独立提交的服务。 |
| 协议转换独立、平台适配独立 | 一个客户端协议可以路由到多个上游；保留原生字段、透传和平台专有语义。 |
| 保留少量 `internal/pkg` | 分页、日期计算等确实通用；限制准入，避免形成新的杂物包。 |
| 逐阶段替换，允许短期旧入口转接 | 每次可交付、可回退；必须记录删除条件，不能长期保留两套规则。 |
| 继续保留 Ent 与现有 SQL 迁移体系 | 包重构不必同时迁表或替换 ORM；生成路径稳定可减少上游同步冲突。 |

### 1.2 必须维持的契约

1. HTTP 路由、认证顺序、JSON 字段、状态码、各协议错误格式、SSE 事件和 WebSocket 行为保持兼容。修复已有行为问题时单独说明与验证，不能夹在机械迁移里静默改掉。
2. 保持 `standard` / `simple`、setup、embed / 非 embed、现有 CLI、构建版本注入及部署配置。
3. 保持幂等键、金额精度、付款主体与行为主体、余额/订阅/额度事务、退款与任务补偿规则。
4. 保持缓存键、TTL、序列化格式、快照版本栅栏、outbox、leader lock 和多实例失效语义。调整这些契约必须作为显式子步骤，写明混部兼容和回退条件。
5. 保持代理、目标校验、TLS 身份隔离、凭据脱敏、取消传播和流首个真实输出后的不可重试约束。
6. 新的统一协议目录也在范围内：`ProtocolID`、账号原生集合、分组准入、单步 fallback、Responses 图片策略与旧字段兼容均不得遗漏。
7. 保持已弃用功能的既有拒绝/兼容响应；例如 data management 的迁移不能顺便重新启用功能，也不能自行删除仍被调用的兼容入口。
8. 默认始终在当前 `main` 实施，不创建或切换分支；提交使用 Conventional Commits，手写注释用中文，不提交 `SYNC.md`。

本文不安排拆微服务、改表名、替换 Ent/Wire、升级工具链或扩大平台支持范围。Bedrock、Vertex、Ollama 的独立代码归属不意味着新增同名管理平台选项。

### 1.3 基线与统计口径

按含 `.go` 文件的目录清点，合并同目录下的 `foo` / `foo_test`，包含带构建标签、OS 专属和只有测试的目录：共 **112 个目录**，其中 **54 个属于 `ent`**、**58 个为其余包**。这比只统计当前操作系统下 `go list ./...` 更完整。

主要混合包当前直属非测试文件数：`service` 536、`repository` 128、`handler` 67、`handler/admin` 61；`internal/pkg` 下 29 个小包。每次实施前重新清点增量，本文不能替代对新增代码的所有权判断。

## 2. 最终目录

### 2.1 总体布局

以下均相对 `backend/`。这是最终布局；分阶段创建实际需要的包，不提前生成空目录、空接口或无意义的包装。

保留 `internal/` 提供 Go 的导入范围限制，业务模块直接放在它下面。各目录按职责命名；目录同级不代表可以任意互相依赖，依赖方向仍遵循第 3 节的约束。

```text
backend/
├── cmd/
│   ├── server/                       入口、构建版本变量、保留 go:generate 入口
│   ├── jwtgen/                       JWT 辅助命令
│   └── cleanup-ingress-reject-logs/   Ops 清理命令
├── internal/
│   ├── app/                          唯一应用装配模块、启动与失败清理
│   │   ├── lifecycle/                启停、drain、资源关闭和重启实现
│   │   └── bridges/                  跨模块接口/投影适配，不能放业务规则
│   ├── config/                       启动配置加载与校验
│   ├── server/                       HTTP server、全局 middleware、路由汇总
│   │   ├── middleware/               Recovery、CORS、日志、CSP、入口限制
│   │   ├── httpx/                    Gin 响应与 HTTP 输入适配工具
│   │   └── clientip/                 可信代理与客户端地址提取
│   ├── identity/                     用户、外部登录身份、会话、强认证与绑定
│   ├── team/                         团队成员、邀请、归属、所有权转移
│   ├── apikey/                       Key 生命周期、复合 Key、Key 策略与认证快照
│   ├── routing/                      分组、渠道、模型目录、请求映射和协议选路
│   │   └── capability/               平台/账号/协议能力与单步转换的纯规则目录
│   ├── account/                      上游账号、凭据生命周期、健康检查、导入
│   ├── scheduler/                    候选、评分、粘性、并发、等待与调度快照
│   │   └── policy/                   独立评分参数/验证类型，供 routing 配置引用
│   ├── gateway/                      请求准入、attempt 编排、重试与完成处理
│   │   ├── clientmeta/               入站客户端识别的纯类型与解析
│   │   └── httpapi/                  Messages/Responses/Chat/Gemini/媒体入口
│   ├── egress/                       代理生命周期、TLS profile/router、出站策略
│   ├── billing/                      余额、订阅、额度、幂等资金动作
│   │   └── pricing/                  价格配置、价卡解析与纯费用计算
│   ├── payment/                      外部订单、确认、履约、退款与恢复
│   │   └── provider/                 支付宝、微信、Stripe、Airwallex、易支付
│   ├── usage/                        使用记录、查询、Dashboard、聚合、清理
│   ├── promotion/                    邀请、优惠码、返利；通过 billing 发放权益
│   ├── creative/                     创作台状态机、工作区归属、临时数据与恢复
│   ├── batchimage/                   批量图片状态机、任务归属、索引与清理
│   ├── moderation/                   内容审核、规则、处置与无留存约束
│   ├── search/                       外部搜索编排、供应商额度与选择
│   │   └── provider/                 Brave、Tavily 等搜索实现
│   ├── notification/                 邮件、模板、发送队列
│   ├── ops/                          指标、诊断、告警、系统状态与升级检查
│   ├── audit/                        安全/管理员操作审计
│   ├── backup/                       备份恢复、对象与数据库维护、旧兼容入口
│   ├── site/                         站点信息、公告、公开页面
│   ├── settings/                     运行时设置存取、版本、更新通知与页面聚合
│   ├── idempotency/                  面板命令的认领/重放/冲突与记录清理
│   ├── protocol/                     只放协议 ID 等少量公共值类型
│   │   ├── anthropic/                Messages 请求、响应、事件和 wire 常量
│   │   ├── openai/                   Responses/Chat/媒体等请求、响应与事件
│   │   ├── gemini/                   GenerateContent 等报文
│   │   ├── google/                   Google 通用错误报文
│   │   └── bridge/                   协议间转换和转换过程中的流状态
│   ├── upstream/                     最小能力 Interface 与调用结果
│   │   ├── anthropic/                API Key/OAuth、Header、指纹与用量解析
│   │   ├── openai/                   API Key/Codex/PAT/Agent Identity 等适配
│   │   │   ├── wsrelay/              现 openai_ws_v2 帧转发实现
│   │   │   └── liveattestation/      Darwin 与其它系统的本机认证适配
│   │   ├── gemini/                   原生 Gemini API Key/OAuth 适配
│   │   │   └── codeassist/           Gemini CLI/Code Assist/相关 Drive 能力
│   │   ├── antigravity/              Google 内部封装与专有转换
│   │   ├── grok/                    xAI/Grok、媒体、Voice、搜索及用量探测
│   │   ├── qoder/                   Cosy、站点、签名、会话与原生 SSE
│   │   ├── bedrock/                 AWS 签名、区域路由与事件帧
│   │   ├── vertex/                  Service Account、project/location、Batch
│   │   ├── kimi/                    Kimi/Moonshot 特有行为
│   │   ├── zhipu/                   GLM/Z.ai 特有行为
│   │   ├── deepseek/                DeepSeek 特有行为
│   │   ├── ollama/                  现有 Ollama Cloud 用量及兼容差异
│   │   └── usageprovider/           Sub2API/New API/Zivv 等用量查询适配
│   ├── infra/                        分类目录，自身不声明 Go package
│   │   ├── postgres/                连接、基础事务、SQL 扫描、死锁重试
│   │   ├── redis/                   客户端、通用命令 instrumentation
│   │   │   └── session/             通用临时会话存取与一次性消费
│   │   ├── httpclient/              连接池、超时、响应上限和传输实现
│   │   │   ├── proxy/               代理 URL 解析与拨号
│   │   │   └── tlsfingerprint/      TLS 握手实现与技术参数
│   │   ├── storage/                 通用本地/S3/GCS 客户端
│   │   ├── telemetry/               通用关联信息
│   │   │   ├── logging/             日志后端与 slog 适配
│   │   │   └── timing/              耗时 collector/httptrace
│   │   ├── crypto/                  AES 等技术实现，密钥由装配注入
│   │   └── timingwheel/             可停止的时间轮实现
│   ├── pkg/                          小型、无业务归属的通用能力
│   │   ├── apperror/                错误类别、reason、包装和安全元数据
│   │   ├── pagination/              分页值类型和计算
│   │   ├── timezone/                显式时区的日期计算
│   │   ├── ipmatch/                 IP/CIDR 解析与匹配
│   │   ├── oauthpkce/               已有重复使用的 PKCE 原语
│   │   └── logredact/               通用敏感字段脱敏
│   ├── setup/                        CLI/Web 首次初始化
│   ├── web/                          embed/noembed 与静态资源
│   └── testutil/                     通用测试设施，不依赖业务模块
├── tests/
│   └── integration/                  跨模块/端到端测试，保留原构建标签
├── ent/                              保持路径；schema 源与生成包见映射表
├── migrations/                       保持路径和全局编号、checksum
└── scripts/                          保持构建/测试脚本，同步变化的测试路径
```

补充目录约定：

- 各业务模块按实际需要具有 `httpapi/`、`postgres/`、`rediscache/`、`provider/` 和模块专用 `testkit/`。上图不对每个模块重复展开这些目录；它们的 Go import 路径统一为 `internal/<模块>/<适配类型>`。
- `account` 的 CRS HTTP 客户端放其 `provider/`；captcha/第三方登录客户端放 `identity/provider/`；定价文件/远端加载器放 `billing/provider/`；备份专用 dump/存储实现放 `backup/provider/`；Ops 的 GitHub 升级客户端放 `ops/provider/`。
- 具体供应商包按其实际内部依赖决定是否需要子包，不为每个模型、端点或认证方法都创建 package。
- `protocol` 和 `upstream` 根包只拥有必要的公共契约，不导入其子包。具体实现的集合由 `app` 装配。`bridge` 可以依赖两侧协议包，两侧不能反向依赖 `bridge`。
- 最终删除全局 `service`、`repository`、`handler`、`handler/admin`、`domain`、`model`、第二处 `middleware`、`util` 和空的 `platform` 分类。`server`、`pkg` 和 `testutil` 保留但收紧责任。
- 重构过程中可临时出现 `app/legacybridge/`，S16 删除；不能把它当作最终结构。

### 2.2 模块内部形状

以账号为例，`account` 根包放模型、Interface 和业务 Implementation（内部实现）；子目录放 Adapter。根包不能 import 自己的适配子包。

```text
internal/account/
├── account.go          账号状态与只读投影
├── service.go          对调用方提供的用例
├── ports.go            实际需要替换的外部依赖 Interface
├── credentials.go      凭据保存与刷新协调
├── health.go           健康检查与恢复规则
├── worker.go           账号后台任务
├── httpapi/            请求、响应、权限适配和模块路由
├── postgres/           账号持久化 Adapter
├── rediscache/         账号缓存 Adapter
└── provider/           CRS 等外部导入 Adapter
```

依赖是 `httpapi → account`、`postgres → account`、`rediscache → account`，`app` 将它们装配起来。Wire provider set 可以留在 Adapter 内，跨 Adapter 的 binding 和 lifecycle 在 `app`。

根包可以使用真正独立的算法子包，例如 `billing → billing/pricing`；这个子包不能再 import `billing`。需要根类型时应把类型留在较低层的已有契约中，或者暂时保留为同包文件，避免为逃避循环而新建泛化 `common/types`。

## 3. 模块关系与接口设计

### 3.1 所有权与调用方看到的能力

下表方法名是拟议 Interface 的表达，不要求现在生成代码；实施阶段结合调用方收紧参数与返回值。

| Module | 独立拥有的规则 | 调用方应看到的能力 / 不应承担的细节 |
| --- | --- | --- |
| identity | 用户状态、资料、登录身份、会话安全、绑定唯一性 | 登录/验证主体/绑定身份；不让调用方拼 JWT、事务和缓存失效。 |
| team | 团队生命周期、唯一 owner、成员资格与归属 | 解析团队主体、变更成员/所有权；输出付款 owner 与行为成员。 |
| apikey | Key 生命周期、复合映射、访问策略、认证快照 | 认证得到 `AccessSnapshot`；资金是否可消费交给 billing。 |
| routing | 分组、渠道、模型映射、能力目录、单步协议选路 | 生成/更新 `RoutePlan`；展示目录与真正请求共用规则，不拥有扣款。 |
| account | 账号资格配置、凭据持久化/刷新、影子父子约束、维护 | 提供 `AccountSnapshot`、可用凭据和健康动作；不承担请求重试循环。 |
| scheduler | 本次账号选择、负载/评分、粘性、槽位、等待、快照 | `Acquire` 返回选择及可释放的 Lease；调用方不组合多个 Redis 计数。 |
| gateway | 准入时序、attempt、流提交、有限重试、完成处理 | `Execute` 组织完整请求；HTTP 入口不再自己串接十几个服务。 |
| upstream | 平台认证请求、报文差异、调用、错误/用量观测 | 按需提供 `Invoke`、`Refresh`、`Probe`、Batch/媒体能力；不能有强制实现全部方法的万能接口。 |
| egress | 代理有效性、fallback、TLS profile/router 选择 | 返回出站策略快照；连接如何建立由 infra/httpclient 实现。 |
| billing | 消费资格、金额、订阅、额度、幂等资金动作 | `Check`、`Settle`、`Reserve`、`Capture`、`Release`、权益发放；调用者不选择 SQL 锁顺序。 |
| payment | 外部订单确认、履约与退款状态机 | 创建/确认/退款；调用 billing 发权益，外部成功不等于本地履约完成。 |
| usage | 用量查询事实、统计口径、Dashboard、聚合/清理 | 写记录/查询统计；删除日志不能撤销资金，也不把日志当结算提交标志。 |
| promotion | 邀请、促销、返利比例与冻结规则 | 计算/兑现促销与返利；余额/订阅写入仍通过 billing 的幂等入口。 |
| creative / batchimage | 各自任务、租约、输出归属、补偿与恢复 | 创建/查询/取消/处理已领取任务；provider 成功后仅重试结算，不重新推理。 |
| moderation | 审核模式、规则、阻断/处置和留存政策 | 返回审核裁决；封禁通过被影响模块的命令接口，不直接修改其表。 |
| search | 搜索供应商选择、额度预占/回滚与失败处理 | 一次搜索结果；调用者不维护 Brave/Tavily Redis 用量。 |
| notification | 消息模板、发送和队列 | 接受明确的通知请求；不反向加载订单/账号来猜测业务状态。 |
| ops / audit / backup | 观测、操作审计、备份各自的数据与生命周期 | 区分可丢观测、必须持久审计及重维护操作，不能统一为普通日志。 |
| site / settings | 站点业务；设置存取/版本与更新传播 | settings 接受各模块显式提供的校验/应用能力，不持有所有具体业务服务。 |
| idempotency | 面板命令重放、冲突与租约 | 不能替代 billing 的事务去重，也不能用它包住所有请求冒充 exactly-once。 |

### 3.2 import 规则与 Seam

Seam 指可以替换 Implementation 的位置。优先放在 I/O、供应商差异、可独立变化的策略或闭合事务处；不要为每个函数生成一个 interface。纯计算直接调用，第三方集成通过生产 Adapter 与测试 Adapter 验证，PostgreSQL/Redis 的关键语义使用真实测试依赖。

```text
cmd/server → app → 各业务模块 + 具体 Adapter + server
模块/httpapi → 所属模块 + server/httpx + 必要的鉴权适配
模块/postgres、模块/rediscache → 所属模块 + 对应 infra
gateway → 自有调用方接口 + 稳定业务投影 + protocol/upstream 契约
scheduler → 自有候选/槽位/快照接口 + 独立能力规则
account/routing/billing 等核心 → 自有接口 + 受控纯契约/算法/pkg
upstream/<平台> → upstream 根契约 + protocol + 通用技术实现
protocol/bridge → protocol 各协议包 → protocol 根值类型
infra 技术包 → pkg/标准库/外部技术库
pkg → 标准库或明确必要的纯计算库
```

必须落实到现有 `.golangci.yml` 的 depguard，而不是只靠文档：

1. 新业务核心禁止 import 旧 `service/repository/handler/domain/model`，也禁止 Gin、Ent、SQL/Redis 客户端、具体 HTTP/存储 Adapter。
2. HTTP Adapter 不直接查询仓储或 Redis；即使依赖仓储接口而非具体包，也应在评审中识别和移除绕过用例的访问。
3. `protocol` 不 import 业务、配置、Gin、数据库、Redis 或具体上游。供应商能力和账号资格不塞进协议转换器。
4. 具体上游包不 import gateway/account/billing 的具体实现，不相互 import。共享的 Google 认证或 wire 能力下沉到真实复用的纯子包，平台注册在 app 完成。
5. `app/bridges` 负责投影转换与绑定，不执行资金、账号状态或路由业务规则；模块不能 import app。
6. 跨模块读取优先使用小型只读投影，禁止传递整个 Ent 实体或 `*service.Account/User/Group`。依赖注入一个巨大 struct 不能算解耦。
7. 允许单向依赖另一个模块稳定的纯契约，不强制所有调用都绕 Interface。若出现 A→B→A，应重新确定规则所有者、合并内聚逻辑或用外部 Adapter 装配，不用反射或 `any` 绕过。
8. `billing/pricing`、`routing/capability`、`scheduler/policy` 是明确的低层叶子契约；所属根模块可 import 它们，它们不能 import 根模块。纯价格结构统一在 pricing，由 routing 保存价卡、billing 计算使用；评分参数定义在 policy，由 routing 保存配置、scheduler 执行，避免根模块之间反向引用。

这些约束从 **S01.0** 开始建立并约束新增代码，不能等到 S15 才加入检查。规则启用以源文件的迁移状态为依据，不能仅凭目录已经采用目标名称就认定整个包已完成重构。业务根包与 `httpapi/postgres/rediscache/provider/testkit` 子包分别匹配，不能用覆盖整个 `internal/**` 的禁令误伤合法 Adapter，也不能整体豁免一个业务目录。

| 路径状态 | S01.0 起的门禁方式 | 例外退出条件 |
| --- | --- | --- |
| 新建目标包、已有目标包中的新增文件 | 首次提交即匹配最终职责规则；不继承同目录旧文件的例外 | 始终适用，无历史豁免 |
| 原路径保留但尚未完成重构，例如 `internal/payment` | 默认应用核心规则；仅给 S00 已确认的旧文件登记仍需保留的具体 import。当前 `load_balancer.go` 及相关测试依赖 Ent，`wire.go` 含 Ent/config/Wire 装配，必须逐项核对 | S12.2 将数据库选择移入存储 Adapter、装配移入 app，同批删除原文件例外；其余保留路径按第 4 节所属阶段处理 |
| 仍待迁移的 `internal/pkg` 能力，例如 `websearch`、`xai` | 纯工具规则只匹配最终保留或新建的具体纯包，不能对整个 `internal/pkg/**` 立即应用纯包规则。旧文件的 Redis、Gin、config 等依赖按 S00 基线登记并受增量约束，不能自动吸收新增依赖 | `websearch` 的 Redis 协作在 S10 拆分，`xai` 的 Redis 协作在 S09.7 拆分；其余按第 4 节阶段迁移并清理旧路径 |

S00 清点所有与目标路径重合的旧包，不限于上表举例；S01.0 将记录落实为可检查的规则。每条例外记录准确源文件、允许的 import、保留原因和退出子步骤，规则旁用中文注释说明，并在对应阶段子计划中记录进度。不能只把旧文件从规则中排除而放过其全部依赖，也不能用目录通配符给新增文件放行。迁出文件按目标职责重新匹配，不能沿用原路径例外；旧文件改动也不得借既有 import 继续添加待拆的耦合逻辑。

旧 service/handler 的现有规则继续保留。`app/legacybridge` 的旧依赖、存储 Adapter 的同事务协作必须逐项登记允许的源、目标、用途和退出阶段；新核心不继承旧包的历史豁免。首次配置及新增规则范围时，用可丢弃的最小违规夹具确认规则确实会拒绝非法 import，再删除夹具；正常和适用构建标签下都检查。S15 仅收尾删除旧规则与过渡例外。

### 3.3 资金与跨模块事务

资金上的解耦指让调用者不需要知道实现细节，并不意味着把原子事务拆开。

- 普通结算由 billing 拥有闭合资金命令。一次事务认领 `(request_id, api_key_id)`，锁付款主体和订阅，再更新余额/订阅、Key 配额、团队成员用量和账号额度。最终运行态的资金调整由 billing 的受控命令负责；重构期间尚未迁移的写入必须列在下面的清单中，不能提前宣布所有资金写入已收敛。
- 资料字段仍由 identity/team/account/apikey 管理。明确到字段/操作的写权限；用户仍可以存储在同一张表，不为了目录美观提前迁表。快照的费用和限额由事务中最新状态复核，不能把调用方的预检当最终授权。
- `payment → billing` 发放/撤销权益，billing 不反向调用 payment。保留订单来源幂等、recharge code、审计去重和履约 lease 的当前行为，先迁移再单独评估业务简化。
- 退款的订单认领、资金回收、最终状态和成功审计需要同一事务。允许命名明确的事务 Adapter 将本次订单操作与 billing 的事务内操作组合，所有写入使用同一个 `*sql.Tx` 或同一 Ent Tx 对应连接；这个对象不能传入新的业务核心或 HTTP。旧 service 中已存在的外层事务可按清单暂留，不能把这一过渡许可扩展到新核心。
- 对上述闭合流程，可给存储 Adapter 一条精确的跨模块依赖例外，例如 `payment/postgres → billing/postgres` 的事务内操作。必须单向、限定包/用途、通过真实事务测试；普通仓储不能跨模块任意修改表。若无法形成单向关系，组合提升到专用事务 Adapter，由 app 注入。
- creative/batchimage 的资金预占、分配快照和任务预占标记同样不能先后独立提交。过渡期先保留完整 SQL 事务；最终由 billing 的任务资金 Interface 配合两种任务投影 Adapter 完成。任务 Adapter 接收同一事务写回本模块资金投影，billing 核心不认识 `creative_runs`、`batch_image_jobs` 或 `CreativeEntity`。
- 最终 `Reserve/Capture/Release` 使用资金动作 ID、主体、金额、定价快照和受控任务引用。删除 `BatchImageBalanceHoldCommand` 的跨业务命名与 `CreativeEntity` 布尔分支，但保留原始持久化幂等键与指纹兼容。
- 数据库和 Redis、供应商之间不伪造原子性。保留 durable outbox/租约/对账与补偿；通知或监控可采用现有异步方式，授权拒绝、扣款和关键审计不能因为引入事件而变成无保证的最终一致。

**资金写入与过渡清单**：源路径相对 `backend/internal/`。S00 必须把每组展开为实际方法和调用者；新增写入归入本表并指定退出阶段。这里区分资金增减、额度配置和历史数据恢复的权限，不把普通资料更新当作资金入口。

| 写入动作 / 当前入口 | 最终责任与原子范围 | S04 后的过渡状态 | 完成阶段 |
| --- | --- | --- | --- |
| 普通请求结算：service/usage_billing、gateway_usage_billing；repository/usage_billing_repo | billing；去重、余额/订阅、Key/成员/账号用量沿用同一事务 | 资金实现唯一迁入 billing，旧网关仅转接；完成处理 worker 随网关后迁 | S04；调用编排 S09/S11 |
| 订阅发放、排队、撤销/恢复、额度重置及兑换：service/subscription*、redeem*；对应仓储 | billing；保持用户锁、来源订单、兑换次数及订阅链/Key 改绑的既有原子范围 | 完整用例及事务一起迁；外部调用存在已有事务时，通过事务 Adapter 参与，不另行提交 | S04；外部调用者 S05/S12 |
| 管理员余额 set/add/subtract：service/admin_user；repository/user_repo 的 SetBalance/AdjustBalance 等 | billing 负责调账，identity 负责用户资料；保留余额上下限、累计充值、审计与返利的原有失败语义 | 原子资金操作提取到 billing 存储实现，旧管理入口通过适配调用；不把 set 改成读后覆盖，也不把现有尽力返利改成回滚充值 | 资金操作 S04；管理用例/HTTP S05 |
| 注册、首次身份绑定和渠道默认赠送：service/auth_*、身份绑定用例及 repository/user_repo 创建路径 | identity 决定授予时机，billing 负责权益；保持原用户/身份事务、默认赠送及 savepoint/fail-open 规则 | 保留尚未拆开的初始化写入，逐方法登记；已迁订阅发放通过事务适配使用，不能改变注册失败/成功边界 | S05 |
| Key/成员/账号限额配置、手动清零与周期维护：service/api_key*、team、admin_account、账号额度维护及对应仓储 | 配置归 apikey/team/account；消费累计与资金窗口归 billing。逐字段标明配置、重置、累计分别允许谁写 | 结算累计随 S04 迁移；旧配置/维护入口暂留，禁止普通实体更新覆盖并发累计值 | S05—S07 |
| 用户平台额度：service/user_platform_quota*、billing_cache；对应仓储 | billing；保持 Redis-first 执法、dirty set、批量数据库镜像和已有降级语义 | 核心、缓存与 flusher 一起迁；不因统一资金归属而强行并入普通 SQL 扣款事务 | S04；管理入口随所属模块 |
| Promo Code：service/promo_service.ApplyPromoCode、repository/promo_code_repo、user_repo.UpdateBalance | promotion 的事务 Adapter 组合 billing 事务内加款；锁码、加款、usage 和次数一起提交 | 整体保留旧事务和其资金写入至 S12；不得单独替换成会自行提交的 billing 加款调用 | S12.1 |
| 返利冻结/解冻与转余额：service/affiliate_service；repository/affiliate_repo.TransferQuotaToBalance 等 | promotion 拥有返利状态；转主余额时同事务组合 billing，保持返利认领、余额/累计充值和转账记录原子性 | 旧闭合事务作为明确例外保留；不复制资金实现、不提前清零返利后再独立加款 | S12.1 |
| 支付余额/订阅履约：service/payment_*；内部充值码和来源订单发放 | payment 拥有订单与 lease，billing 拥有权益发放；沿用原有多步恢复与幂等，不虚构整笔外部支付的事务 | 已迁兑换/订阅通过适配使用新 billing；订单、返利与恢复编排暂留旧实现 | S12.3 |
| 支付退款：service/payment_* 中退款认领、扣减、状态和审计 | payment/postgres 单向组合 billing/postgres；本地最终确认完整事务提交，外部退款调用不放入数据库事务 | 保留旧退款完整事务及其资金写入至 S12；不能只抽走其中的扣减语句后独立提交 | S12.4 |
| creative/batchimage 预占、捕获和释放：repository/usage_billing_repo 与任务投影写入 | billing 的资金动作 + 各任务存储投影，同事务更新 | S04 保留完整任务资金 SQL；旧命令名、任务表投影和 CreativeEntity 耦合单独登记 | 资金实现 S04；去耦 S13 |
| setup/simple 默认数据、备份恢复与已发布 SQL 数据修正 | app/setup/backup 的受控初始化或恢复流程；运行态资金用例仍归 billing | 保留现有初始化/恢复权限及迁移 checksum，登记必要的直接写入；不把数据恢复伪装成用户充值 | S14；S16 核验长期例外 |

**事务参与的具体约束**：

1. billing 对普通调用者提供完整命令；`billing/postgres` 另为存储协作提供命名明确的事务内操作。后者必须使用调用方给定的现有事务，不得自行 Begin/Commit/Rollback，也不得提前失效缓存、发送通知或宣布成功。
2. 旧外层事务暂留时，过渡 Adapter 转接旧 `TxFromContext/clientFromContext` 语义，确认使用同一事务连接。未迁整条流程前保留完整旧事务；不能依赖普通 context 里“可能有事务”就假设新实现已参与。
3. 外层事务的拥有者负责提交及提交后的失效/通知。迁移每条写入都用真实 PostgreSQL 验证“资金写入后，业务记录或审计失败”的整体回滚，并检查重复请求和并发；不只测试 billing 自己能提交。
4. S04 的完成范围是普通结算、权益用例及已列明的资金操作。只有对应 S05—S07/S12/S13/S14 记录逐项销项后，才能确认运行态资金写入已收敛；S16 明确保留初始化/恢复等有理由的操作级例外。

### 3.4 请求、调度和平台协作

```text
客户端协议适配
  → 认证/主体与 Key 快照
  → routing 选择分组、Key 一跳模型改写、协议/模型策略
  → billing 消费预检 + moderation 裁决
  → scheduler 获取用户槽（等待后再次检查权益）
  → 按 RoutePlan 对候选重新解析协议、模型与能力，获取账号 Lease
  → upstream 用凭据与出站策略执行本次 attempt
  → gateway 判断是否还有安全的重试空间
  → billing 幂等结算 + usage 写事实 + ops 完成观测
```

这张图是职责说明，实际阶段顺序以现有协议链路为准，不利用重构统一改序。设计时具体落实：

- `AccountSnapshot` 是配置/资格投影，CredentialLease 或等价接口仅在需要调用上游时提供必要凭据。凭据刷新 singleflight/锁、影子父子绑定、失效和重建在 account 中集中处理。
- scheduler 的 Lease 包含账号选择、模型/协议解析结果、取消关联和幂等 Release。用户槽、账号槽、等待计数、串行队列可能内部独立，但调用者不承担部分成功后的组合回滚。
- 客户端取消、上游执行结束和清理完成是不同信号，不能统一绑定到 HTTP 请求 Context。等待/非流请求沿用既有取消传播；Qoder 已进入上游转发的流式请求在客户端断开后停止下游写入，仍在既有 `qoderStreamTimeout` 内收集尾部 usage，按原有完成顺序显式释放槽位。Lease 必须支持这种完成时释放的方式，不能由客户端取消提前触发；上游结束、错误或执行超时仍必须收尾，重复 Release 安全。断开后不得继续选账号或启动新的推理。其它平台/传输分别保留已有策略，不统一套用立即取消或脱离取消。
- 不能把当前所有平台调度分支硬压成一个评分器：公共评分/快照负责共享规则，OpenAI previous-response、媒体、混合池等资格通过候选策略输入或注入的窄接口扩展。
- gateway 统一记录 `NotCommitted / PreludeOnly / Committed` 或等价输出状态；协议 Adapter 报告真实业务输出的不可逆边界。只在尚未提交真实输出时允许换号；取消、上下游关闭和部分用量必须显式返回。
- 用量从平台原生解析结果转换为计费输入；用户价格与账号成本分别传递，不从一个聚合金额反推另一个。HTTP/SSE/WS 每 turn 保留既有定价时刻语义。
- 同协议直接透传应保留。跨协议转换保留 `json.RawMessage` 等未知字段承载方式，避免为了通用结构发生反复 JSON 编解码和整流缓冲。
- `/models`、`/usage`、已有任务读/取消/下载、自定义声音、Live sideband 等不能机械套用生成请求准入；保留各自的协议/资源归属规则。

这些契约在 S09.0 和 S09.1 的首条真实请求链中验证，再推广到其它平台。首条链至少明确：输入/原始报文所有权、响应输出与 Flush 的控制者、首个真实输出的报告方式、部分用量与错误如何同时返回、客户端取消与上游执行的关联策略、响应体/连接/Lease 的释放责任。`AccessSnapshot`、`RoutePlan` 沿用 S05/S06 的所有者，gateway 不再定义一套同名实体。非流、SSE、WebSocket 可有不同能力接口；S09 不提前假设 WS 与 HTTP 生命周期相同，S11.5 增加 WS 能力时再验证扩展。

### 3.5 设置、缓存、后台与错误

- config 只管启动配置；settings 只管运行时值、版本和更新传播。支付配置归 payment、网关配置归 gateway/routing/scheduler，校验在所属模块；设置页面可聚合展示，不能出现一个注入所有具体服务的 SettingService。
- 每个模块拥有缓存键/投影/失效规则。通用 Redis 包只提供技术实现；身份和团队变更触发 Key 认证缓存失效，账号/分组变更触发调度快照，资金变更触发权益缓存。状态更改提交失败不得先发布成功失效事件。
- 后台 worker 归所属模块，构造函数原则上不启动 goroutine；app 构造完成后按依赖顺序 Start，失败时释放已启动资源，关闭时先停入口/生产者，再 drain 消费者，最后关闭 Redis/数据库。
- 全局 ctxkey 拆成鉴权主体、网关显式执行参数、telemetry 关联信息；不向 context 写可被多个 goroutine 无锁修改的巨大对象。
- 业务错误以类别、稳定 reason 和安全元数据表示。HTTP/协议 Adapter 映射对外状态与原生结构；保留现有 errors.Is/As 与 reason 行为，逐步迁移 HTTPCode 耦合，不一次改掉所有错误比较。
- 平台错误解析归 upstream，用户配置的错误透传/改写规则归 gateway，内容审核规则归 moderation。三者不同，不能统一塞进 moderation 或日志包。

## 4. 旧包逐项映射

这里“包”按现有 Go 源目录统计，测试包随目录处理。路径相对 `backend/`；“保留”也是明确映射。S 编号对应第 6 节；多阶段表示不同职责分别迁移，最后一个阶段才要求旧包清空。

### 4.1 运行包、测试包和入口

| 旧路径 | 最终去向 / 动作 | 阶段 |
| --- | --- | --- |
| `cmd/cleanup-ingress-reject-logs` | 保留；清理用例归 internal/ops，由命令装配必要依赖 | S08、S14 |
| `cmd/jwtgen` | 保留；通过 identity 的明确能力或独立签发工具调用，不引入整套应用 | S05、S14 |
| `cmd/server` | 保留入口；Wire 应用图与资源持有移至 internal/app，版本变量留入口 | S02、S14 |
| `internal/config` | 保留；Wire 组合移 app，各模块接受所需 Options | S02、S15 |
| `internal/domain` | 拆至 protocol、routing/capability、routing、scheduler/policy、identity、billing、site；端点展示元数据由 HTTP Adapter 组织；逐文件见 5.4，最终删除 | S02.4、S03—S07、S11、S16 |
| `internal/handler` | 各模块/httpapi；网关编排移 gateway 核心，首条链在 S09.1 接入；通用输入输出移 server/httpx；最终删除 | S04—S15 |
| `internal/handler/admin` | 管理员方法归各模块/httpapi/admin_*.go；Dashboard → usage、诊断 → ops；最终删除 | S04—S15 |
| `internal/handler/dto` | 各模块/httpapi 的 DTO；协议 DTO → protocol；凭据投影由 account 生成；最终删除 | S03—S15 |
| `internal/handler/quotaview` | 限额展示规则 → billing 的只读投影；JSON 输出 → billing/httpapi；最终删除 | S04 |
| `internal/integration` | backend/tests/integration；保留 unit/integration/e2e 标签，同步 Makefile/脚本/CI 路径 | S00 登记、S16 迁移 |
| `internal/middleware` | 限流用例/故障策略 → server/middleware；通用 Redis 固定窗口实现 → infra/redis；最终删除 | S01、S15 |
| `internal/model` | 错误规则 → internal/gateway；TLS Profile/Router 管理模型 → internal/egress；最终删除 | S06、S11 |
| `internal/payment` | 保留路径并按支付模块重构；合入旧 service 的支付用例，金额/手续费保持支付口径；数据库负载选择拆 Adapter，Wire → app | S12 |
| `internal/payment/provider` | 保留路径、各外部提供商和合同测试，收紧与支付核心的接口 | S12 |
| `internal/platform/liveattestation` | internal/upstream/openai/liveattestation；保留 OS 构建标签与不支持平台结果 | S09.8 |
| `internal/repository` | 各模块/postgres、rediscache、provider；通用技术 → infra；完整规则见 5.2，最终删除 | S01—S16 |
| `internal/server` | 保留 HTTP 装配与静态资源接入；业务实例构造和副作用 → app/各模块 | S02、S15 |
| `internal/server/middleware` | 通用部分保留；JWT/step-up → identity/httpapi，Key → apikey/httpapi，审计 → audit/httpapi；见 5.3 | S01、S05、S08、S15 |
| `internal/server/routes` | 各模块/httpapi/routes.go；全局挂载和别名汇总 → internal/server，最终删除 routes 包 | S03—S15 |
| `internal/service` | 按 5.1 职责组拆至 internal 下各业务模块及 upstream、protocol、infra、app；最终删除 | S02—S16 |
| `internal/service/openai_ws_v2` | internal/upstream/openai/wsrelay；保留帧转发与取消语义 | S09.8 |
| `internal/setup` | 保留独立 setup 入口；配置/数据库初始化调用 app 提供的精简初始化能力 | S14 |
| `internal/testutil` | 通用设施保留；业务 fixture/stub → 各模块/testkit 或模块 *_test.go | 随 S03—S15、S16 |
| `internal/util/httputil` | 上游响应/Cloudflare 诊断 → internal/upstream 的响应辅助实现；通用截断按实际复用收敛 | S09 |
| `internal/util/logredact` | internal/pkg/logredact；保留所有敏感值清理契约 | S01 |
| `internal/util/responseheaders` | 过滤配置与编译规则 → internal/egress；实际应用在 HTTP/上游 Adapter，最终删除旧包 | S06、S11 |
| `internal/util/urlvalidator` | 目标策略校验 → internal/egress；DNS/重定向实际执行 → infra/httpclient；最终删除旧包 | S01、S06、S09 |
| `internal/web` | 保留；不反向导入业务 service，公开设置/CSP 使用注入的投影 | S02、S15 |
| `migrations` | 保留 SQL embed 与部署迁移；runner 从 repository 移 infra/postgres | S02、S14、S16 |

### 4.2 internal/pkg 的 29 个包

| 旧路径 | 最终去向 / 动作 | 阶段 |
| --- | --- | --- |
| `internal/pkg/anthropicfp` | 合入 upstream/anthropic 的请求规范化文件，必要时才保留专用子包 | S09.2 |
| `internal/pkg/antigravity` | upstream/antigravity；通用协议 DTO/纯转换抽至 protocol，专有封装留平台 | S03、S09.5 |
| `internal/pkg/apicompat` | protocol/anthropic、protocol/openai、protocol/bridge；不复制转换实现 | S03、S11 |
| `internal/pkg/claude` | upstream/anthropic；纯 wire 常量 → protocol/anthropic，入站版本识别 → gateway/clientmeta | S03、S09.2、S11 |
| `internal/pkg/ctxkey` | 按身份、网关执行参数和 infra/telemetry 拆分；显式投影替代业务键，最终删除 | S01、S05、S07、S09.1、S11 |
| `internal/pkg/errors` | pkg/apperror；HTTPCode/响应映射 → server/httpx 和各协议 Adapter，兼容期保留旧错误比较 | S01、S15、S16 |
| `internal/pkg/gemini` | 协议类型 → protocol/gemini；默认模型与能力资料 → upstream/gemini，展示由 routing 组装 | S03、S09.3 |
| `internal/pkg/geminicli` | upstream/gemini/codeassist；与 Gemini/Vertex 共用部分提取为无反向引用的低层能力 | S09.3 |
| `internal/pkg/googleapi` | Google 错误报文 → protocol/google；激活诊断 → upstream/gemini/codeassist | S03、S09.3 |
| `internal/pkg/httpclient` | infra/httpclient；统一普通/指纹客户端的策略入口，保留池隔离 | S01、S06 |
| `internal/pkg/httputil` | 读取/解压 → server/httpx；JSON 宽容修复 → protocol 的适用转换辅助文件 | S01、S03 |
| `internal/pkg/ip` | Gin 客户端地址 → server/clientip；纯 IP/CIDR → pkg/ipmatch；黑白名单裁决 → apikey | S01、S05 |
| `internal/pkg/logger` | infra/telemetry/logging；config_adapter → app 的配置转换 | S01、S02 |
| `internal/pkg/oauth` | Claude OAuth → upstream/anthropic；重复 PKCE 原语 → pkg/oauthpkce；会话持有由账号授权用例负责 | S01、S09.2 |
| `internal/pkg/openai` | 授权/默认调用 → upstream/openai；入站客户端识别 → gateway/clientmeta；许可裁决 → gateway/routing | S03、S09.8、S11 |
| `internal/pkg/openai_compat` | 旧账号字段转换 → account；选路 → routing；协议枚举 → protocol/openai；按当前统一协议契约删除旧配置依赖 | S03、S06 |
| `internal/pkg/pagination` | 保留 pkg/pagination；保持不依赖任何业务或 HTTP 框架 | S01 |
| `internal/pkg/proxyurl` | infra/httpclient/proxy，与 proxyutil 合并组织 | S01 |
| `internal/pkg/proxyutil` | infra/httpclient/proxy，与 proxyurl 合并组织 | S01 |
| `internal/pkg/qoder` | upstream/qoder；站点模型/认证/签名/原生流保留归属；本地凭据读取明确隔离 | S09.1 |
| `internal/pkg/redissession` | infra/redis/session；配置与使用语义由授权用例提供 | S01、S09 |
| `internal/pkg/response` | server/httpx；保持面板响应兼容和错误脱敏 | S01、S15 |
| `internal/pkg/servertiming` | infra/telemetry/timing；HTTP 输出控制留 server/middleware | S01 |
| `internal/pkg/sysutil` | app/lifecycle；调用方持有重启请求 Interface，不导入 app 或直接 os.Exit | S02、S14 |
| `internal/pkg/timezone` | 保留 pkg/timezone 日期运算；全局初始化 → app；逐步注入时钟/时区但保持结算日界 | S01、S04、S08 |
| `internal/pkg/tlsfingerprint` | infra/httpclient/tlsfingerprint；业务 Profile/Router 管理和选择仍归 egress | S01、S06 |
| `internal/pkg/usagestats` | internal/usage 查询类型和统计口径；计费金额输入使用 billing/pricing 的专用值类型 | S04、S08 |
| `internal/pkg/websearch` | internal/search 及其 provider、rediscache 子包；额度/选择与外部调用分开 | S10 |
| `internal/pkg/xai` | upstream/grok；外部账单/订阅解析保留；Redis 适配由外层注入 | S09.7 |

### 4.3 Ent 的 54 个包

Ent 各包全部保持原路径。schema/mixins/generate.go 等手写源仍按现有生成流程维护，其余生成物不人工移动或编辑。S16 核对所有存储与测试消费者已经改为新的应用包引用；迁移期间 schema 若引用旧 domain 类型，只迁移该源引用并生成，核对 JSON 表示和迁移检测没有意外变化。

| 旧路径 | 最终去向 / 动作 | 阶段 |
| --- | --- | --- |
| `ent` | 保留 `ent`；手写 schema/生成入口按现有流程维护 | S16 核验 |
| `ent/account` | 保留 `ent/account`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/accountgroup` | 保留 `ent/accountgroup`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/announcement` | 保留 `ent/announcement`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/announcementread` | 保留 `ent/announcementread`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/apikey` | 保留 `ent/apikey`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/apikeycompositegroup` | 保留 `ent/apikeycompositegroup`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/authidentity` | 保留 `ent/authidentity`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/authidentitychannel` | 保留 `ent/authidentitychannel`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/batchimageevent` | 保留 `ent/batchimageevent`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/batchimageitem` | 保留 `ent/batchimageitem`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/batchimagejob` | 保留 `ent/batchimagejob`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/creativerun` | 保留 `ent/creativerun`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/creativerunoutbox` | 保留 `ent/creativerunoutbox`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/creativerunoutput` | 保留 `ent/creativerunoutput`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/enttest` | 保留 `ent/enttest`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/errorpassthroughrule` | 保留 `ent/errorpassthroughrule`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/group` | 保留 `ent/group`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/hook` | 保留 `ent/hook`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/idempotencyrecord` | 保留 `ent/idempotencyrecord`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/identityadoptiondecision` | 保留 `ent/identityadoptiondecision`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/intercept` | 保留 `ent/intercept`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/migrate` | 保留 `ent/migrate`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/paymentauditlog` | 保留 `ent/paymentauditlog`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/paymentorder` | 保留 `ent/paymentorder`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/paymentproviderinstance` | 保留 `ent/paymentproviderinstance`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/pendingauthsession` | 保留 `ent/pendingauthsession`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/predicate` | 保留 `ent/predicate`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/promocode` | 保留 `ent/promocode`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/promocodeusage` | 保留 `ent/promocodeusage`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/proxy` | 保留 `ent/proxy`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/redeemcode` | 保留 `ent/redeemcode`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/redeemcodeusage` | 保留 `ent/redeemcodeusage`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/runtime` | 保留 `ent/runtime`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/schema` | 保留 `ent/schema`；手写 schema/生成入口按现有流程维护 | S16 核验 |
| `ent/schema/mixins` | 保留 `ent/schema/mixins`；手写 schema/生成入口按现有流程维护 | S16 核验 |
| `ent/securitysecret` | 保留 `ent/securitysecret`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/setting` | 保留 `ent/setting`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/subscriptionplan` | 保留 `ent/subscriptionplan`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/team` | 保留 `ent/team`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/teaminvitation` | 保留 `ent/teaminvitation`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/teammembership` | 保留 `ent/teammembership`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/teamownershiptransfer` | 保留 `ent/teamownershiptransfer`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/tlsfingerprintprofile` | 保留 `ent/tlsfingerprintprofile`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/tlsfingerprintrouter` | 保留 `ent/tlsfingerprintrouter`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/usagecleanuptask` | 保留 `ent/usagecleanuptask`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/usagelog` | 保留 `ent/usagelog`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/user` | 保留 `ent/user`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/userallowedgroup` | 保留 `ent/userallowedgroup`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/userattributedefinition` | 保留 `ent/userattributedefinition`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/userattributevalue` | 保留 `ent/userattributevalue`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/userdisabledpublicgroup` | 保留 `ent/userdisabledpublicgroup`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/userplatformquota` | 保留 `ent/userplatformquota`；生成/辅助包按现有流程维护 | S16 核验 |
| `ent/usersubscription` | 保留 `ent/usersubscription`；生成/辅助包按现有流程维护 | S16 核验 |

`ent/migrate` 与 `migrations` 不可合并：前者是 Ent schema/生成能力，后者是部署数据库的前向 SQL 迁移。目录重构不改已发布 SQL 文件名、checksum 或排序。

## 5. 混合包的内部拆分

第 4 节保证每个旧 package 有去向；本节解决大包内的代码应如何分配。文件名前缀用于定位，不是自动搬迁规则。一个文件同时包含模型、HTTP、SQL 和业务编排时，先按语义拆开；具体仓储 Interface 随使用它的模块迁移。

### 5.1 service 的职责组

表中源均在 `internal/service/`；`*_test.go`、fixture 和测试辅助函数跟随被验证的能力。优先采用更具体的行，例如 `openai_gateway_grok_*` 归 Grok，不能落进普通 `openai_*` 规则。

| 现有文件/职责组 | 最终所有者 | 必须同时做的代码重构 | 阶段 |
| --- | --- | --- | --- |
| `wire.go`、BuildInfo 与生命周期装配 | app | 拆按模块的构造绑定；从构造函数移除自动 Start；保留失败清理。 | S02、S16 |
| `user.go`、`user_service.go`、`admin_user.go`、`user_attribute*`、`notify_email_entry.go` | identity；调账与资金操作 → billing | 资料、绑定和授权投影分开；余额/冻结/倍率不再通过普通用户更新写回，资金过渡见 3.3。 | 资金操作 S04；其余 S05 |
| `auth_*`、`registration_email_*`、`passkey.go`、`totp_service.go`、`session_binding.go`、`refresh_token_cache.go` | identity | 提取完整登录/绑定流程；同邮箱互斥、token 撤销、pending session 原子消费与 step-up 保持。 | S05 |
| `aliyun_captcha_service.go`、`tencent_captcha_service.go`、`turnstile_service.go` | identity + identity/provider | 保留提供方互斥和 fail-close，外部校验通过可替换 Adapter。 | S05 |
| `team.go` | team | 成员变化/owner 转移保持原子性；输出明确付款与行为主体，主动失效 Key。 | S05 |
| `api_key*`、`auth_cache_invalidation_outbox.go`、`invalid_auth_abuse_limiter.go` | apikey | Key 认证与资金准入分离；复合选组、一跳改写、缓存版本和 outbox 归统一入口。 | S05 |
| `group*`、`channel*`、`admin_group*`、`model_marketplace_service.go`、`gateway_requestable_models.go`、`upstream_models.go` | routing | 管理配置与请求投影分开；分组/渠道关系、市场价格/能力通过输入契约组合。 | S06 |
| `protocol_catalog.go`、`protocol_routing.go`、`protocol_legacy.go`、`account_protocols.go` | routing/capability、routing、account | ID/wire 与业务能力分开；旧字段只在输入适配归一化，候选选路保持单步和账号认证方式约束。 | S03、S06 |
| `account.go`、`account_service.go`、`admin_account.go`、`account_group.go`、`account_credentials_*`、`credentials_sanitize.go`、`credential_shadow.go` | account | 不再传递全功能 Account；凭据写入与脱敏、影子父子约束集中，型号算法交适配层。 | S06 |
| `account_expiry*`、`account_test*`、`account_usage*`、`scheduled_test*`、`upstream_usage_service.go`、`crs_sync_service.go` | account | 测试/同步负责业务编排；供应商调用与 CRS 请求移 provider/upstream；上游用量观测不能直接改变用户账单。 | S06、S09 |
| `token_refresh*`、`token_refresher.go`、`refresh_policy.go`、`oauth_refresh_api.go`、`token_cache_*`、`gemini_token_cache.go` | account | 将多平台刷新协调与具体交换协议分开；统一竞争控制、缓存失效和停机。 | S06、S09 |
| `*_token_provider.go`、`*_token_refresher.go`、各平台 `*_oauth_service.go`、`gemini_oauth.go` | account 授权用例 + 对应 upstream | account 负责保存/刷新状态，上游仅负责交换/刷新请求；避免平台注册包反向 import account。 | S09 各平台 |
| `proxy*`、`admin_proxy.go`、`tls_fingerprint_*`、`http_upstream_profile.go`、`account_header_override.go`、`response_header_filter.go` | egress + infra/httpclient | 配置/路由和握手实现分开；Header 禁止项、账号池隔离与代理 fallback 统一执行。 | S01、S06 |
| `http_upstream_port.go`、`upstream_path_guard.go`、`upstream_response_limit.go`、`header_util.go` | upstream 契约、egress、infra/httpclient | 显式传入出站策略/大小限制；入站业务策略不能混入普通 client。 | S01、S06、S09 |
| `advanced_scheduler*`、`scheduler_*`、`gateway_scheduling.go`、`openai_gateway_scheduling.go`、`openai_account_scheduler.go` | scheduler | 共享评分与平台资格分开；保留 basic/advanced 与缓存 epoch/tombstone；诊断使用同一选择逻辑。 | S07 |
| `concurrency_service.go`、`ratelimit*`、`rpm_cache.go`、`user_rpm_cache.go`、`session_limit_cache.go`、`user_msg_queue_service.go` | scheduler；平台错误解释归 upstream | 合并 acquire/release 生命周期，明确用户、账号、RPM、串行和等待的不同语义。 | S07、S09 |
| `temp_unsched.go`、`model_rate_limit.go`、`account_scheduling_threshold_*`、`internal500_counter.go`、`openai_403_counter.go` | account 健康状态 + scheduler 投影 | 谁写状态、谁读投影明确；供应商错误转为状态变化的分类由对应 upstream 提供。 | S06、S07、S09 |
| `deferred_service.go`、`timing_wheel_service.go` | account + infra/timingwheel | 账号 last-used 批量写归 account；通用时间轮可停止，不能丢失退出 drain。 | S02、S06 |
| `pricing_service.go`、`model_pricing_resolver.go`、`media_model_pricing.go`、`media_price_config.go`、`custom_channel_time_pricing.go` | billing/pricing + billing/provider | 配置/纯计算与远程文件加载分离；routing 保存价卡并复用同一计算口径。 | S03、S04 |
| `billing_service.go`、`billing_cache_service.go`、`usage_billing.go`、`usage_rate_multiplier.go`、`user_group_rate*`、`account_stats_pricing.go`、`service_tier_billing.go`、`image_billing_size.go`、`video_billing*` | billing | 显式区分用户价格、账号成本和资金分配；金额精度、原指纹与多来源事务保留。 | S04 |
| `subscription*`、`user_subscription*`、`user_platform_quota*`、`redeem_code.go`、`redeem_service.go` | billing | 订阅/额度/兑换权益生命周期集中；兑换码仍保留独立类型与规则；不为复用简单改名为促销。 | S04 |
| `usage_log*`、`usage_service.go`、`usage_cleanup*`、`usage_analytics_aggregation.go`、`dashboard*` | usage | 查询、聚合、水位和保留策略拥有唯一口径，业务 DTO 不放 pkg。 | S08 |
| `usage_record_worker_pool.go`、`gateway_usage_billing.go`、`openai_gateway_usage*`、`image_output_accounting.go`、`gemini_image_output_accounting.go` | gateway 完成处理 + billing + usage；原生解析归 upstream | 有界执行器仍保持原溢出/停止语义；资金命令和记录分别处理，不能把带扣款任务丢进普通日志队列。 | S04、S08、S11 |
| `payment_*` | payment；套餐实体/发放归 billing | 订单/实例配置与权益解耦；所有确认入口使用同一状态机，退款事务保持。 | S12 |
| `affiliate_service.go`、`promo_*` | promotion | 推广/促销决策与权益发放分开；返利转余额具有明确幂等和原子规则。 | S12 |
| `batch_image*` | batchimage；资金归 billing；Gemini/Vertex 协议归 upstream | 任务持有状态与输出归属；不再暴露通用资金动作的 BatchImage 名称。 | S09、S13 |
| `creative_*` | creative；资金归 billing；平台执行归 upstream | 保留隐藏 Key、工作区、无留存、outbox/租约和结果丢失语义。 | S09、S13 |
| `content_moderation*` | moderation + notification | 裁决、异步观测、处置和媒体留存分开；通知调用模块 Interface。 | S10 |
| `error_passthrough_service.go`、`error_passthrough_runtime.go` | gateway | 全局规则匹配、状态/消息改写与 Ops 跳过保持同一裁决。 | S11 |
| `email*`、`notification_email_service.go`、`balance_notify_service.go` | notification；触发阈值/权益判断归 billing | 接收已确定的事件/模板参数，通知模块不反向 import billing。 | S10 |
| `announcement*` | site | 公告/已读/到期作为完整小型纵向模块迁移。 | S02.4 |
| `setting*`、`settings_view.go`、`pre_aggregation_settings.go`、`creative_model_settings.go` | settings 的存取/聚合 + 各业务拥有的配置规则 | 网关/支付/创作/聚合的解释与校验迁回对应模块，不能整体搬成新巨型 SettingsService。 | S02、S04—S15 |
| `websearch_config.go`、`gateway_websearch_emulation.go`、`gateway_websearch_block_filter.go` | search + gateway 的工具适配 + protocol | 搜索供应商配额与模型工具调用流程分开。 | S10、S11 |
| `ops_*`、`update_service.go`、`system_operation_lock_service.go` | ops；锁技术实现 → infra，重启实现 → app/lifecycle | 观测只读投影，告警/清理可独立停止；禁止通过 Ops 暴露业务可变内部状态。 | S08、S14 |
| `audit_log*` | audit | 操作审计 Interface 区分必须持久成功与尽力记录；支付专项审计仍归 payment。 | S08 |
| `backup*`、`data_management*` | backup + backup/provider | 管理用例与 dump/S3/兼容响应分开，保持重维护互斥。 | S14 |
| `idempotency*` | idempotency | 保留认领/重放/错误语义，与 billing 去重各自独立；清理归本模块。 | S02 |
| `leader_lock.go`、`database_maintenance_lock.go`、`sql_errors.go` | 使用方窄锁接口 + infra/postgres/redis | 名称/作用域归业务，SQL 错误转译在持久 Adapter，不对错误字符串做业务判断。 | S02、S08、S14 |
| `identity_service.go`、`metadata_userid.go`、`anthropic_session.go`、`gateway_claude_oauth_body.go` | upstream/anthropic | 当前 IdentityService 是上游请求身份指纹，不是用户登录身份；重命名并移除歧义。 | S09.2 |
| `bedrock_*`、`gateway_bedrock.go`、`vertex_service_account.go` | upstream/bedrock、upstream/vertex | 认证/区域/帧转换保留平台所有权，调度与计费抽离。 | S09.2、S09.4 |
| `gemini_*`、`geminicli_*` 的转发/兼容部分 | upstream/gemini/codeassist + protocol/bridge | 账号选择交 scheduler；平台 signature/隐私/凭据仍留上游实现。 | S03、S09.3 |
| `antigravity_*` 的转发/额度部分 | upstream/antigravity；维护编排归 account | 保留混合池资格与 QuotaPlatform，不能因协议形状改变计费归属。 | S09.5 |
| `qoder_*` | upstream/qoder + account 的账号管理输入；请求编排 → gateway | 站点/思考/上下文/签名和流解析内聚；S09.1 同时验证第一条非流/SSE 完整链，平台不持有重试和资金规则。 | S09.1 |
| `grok_*`、`openai_gateway_grok_*`、`openai_x_search` 相关调用 | upstream/grok + gateway | Grok 媒体、Voice、配额/错误与 OpenAI 适配分开。 | S09.7、S11 |
| `cn_provider_*`、`upstream_usage_cn_adapters.go`、`account_test_service_cn_adaptive.go` | account 的探测编排 + upstream/kimi/zhipu/deepseek | 共用 wire 不能合并平台能力；管理员选择与当前协议集合保持。 | S06、S09.6 |
| `ollama_cloud_usage*`、`openai_gateway_ollama_cloud_*` | upstream/ollama + account 观测 | 仅迁移已有用量/兼容能力，不新增独立平台支持承诺。 | S09.6 |
| `openai_*` 其余平台转发/会话/工具/媒体代码、`codex_*` | upstream/openai；入站检测/策略归 gateway；纯转换归 protocol | 保留 OAuth/API Key/PAT/Agent Identity 差异；分离 Grok/CN/Ollama 路径。 | S09.8、S11 |
| `gateway_service.go`、`gateway_request.go`、`gateway_forward*`、`gateway_count_tokens.go`、`gateway_upstream_*`、`gateway_anthropic_passthrough.go` | gateway 编排 + upstream/anthropic + protocol | 按语义拆当前 GatewayService，避免将其整个搬成 gateway.Service。 | S09.2、S11 |
| `gateway_billing_*`、`gateway_messages_cache.go`、`gateway_tool_rewrite.go`、`user_prompt_replacement.go` | gateway 策略与会话；平台改写归 upstream | 管理策略显式传入，保留用户提示替换和缓存计费约束。 | S11 |
| `session_id.go`、`session_isolation.go`、`digest_session_store.go` | gateway 会话协调；粘性选号归 scheduler；平台连续性归 upstream | 浏览器登录会话完全独立；previous-response 与不可迁移会话显式表达。 | S07、S09、S11 |
| `request_metadata.go`、`model_response_restore.go`、`upstream_response_model.go`、`upstream_request_id.go` | gateway 显式请求投影 + protocol + telemetry | 首条链在 S09 验证输入/输出契约；保留模型链，只改响应元数据，禁止正文字符串替换；请求 ID 不改变幂等来源。 | S03、S09.0—S09.1、S11 |
| `context_error.go`、`model_not_found_error.go`、`group_model_unsupported.go`、`models_list_response_limit.go`、`image_generation_intent.go`、`thinking_protocol.go` | 对应 gateway/routing/protocol 的语义所有者 | 错误/意图/响应限制按实际责任迁移，不新建泛化 helpers 包。 | S03、S06、S11 |
| `slice_helpers.go`、`value_helpers.go`、`parse_integral_number_unit.go`、`sse_scanner_buffer_pool.go` 等局部辅助 | 随调用者内聚；S16 清点逐个决定 | 单调用族留私有函数；确有跨模块纯复用才进入 pkg；SSE 缓冲不得混入普通业务工具。 | 随所属阶段、S16 |

### 5.2 repository 的拆分

以下表格给出全部 128 个非测试源文件的迁移责任组；详细自动清点清单在本节末尾。一个源文件中跨领域方法应拆开，不意味着整文件进入一个新 package。

| 源职责 | 目标与约束 | 阶段 |
| --- | --- | --- |
| 用户/身份/Passkey/TOTP/refresh token | identity/postgres、identity/rediscache；用户余额和订阅写入分配给 billing。 | S04、S05 |
| 团队仓储/邀请限流 | team/postgres、team/rediscache；团队消费累计作为 billing 事务参与字段。 | S05 |
| Key 仓储/认证缓存/失效 outbox | apikey/postgres、apikey/rediscache；配额消费由 billing 负责。 | S05 |
| group/channel/可用性探测仓储 | routing/postgres；价卡结构使用 billing/pricing，保存与查询归 routing。 | S06 |
| account/计划测试/token 缓存/临时停调 | account/postgres、account/rediscache；平台响应 Adapter 移 upstream。 | S06、S09 |
| 并发/RPM/等待/串行队列/调度快照/outbox | scheduler/rediscache、scheduler/postgres；key/epoch 与账号/分组变更协作契约保持。 | S07 |
| `gateway_cache.go` | 会话映射、前缀缓存按 gateway/scheduler 拆分；不能创建万能 RuntimeCache。 | S07、S11 |
| `identity_cache.go` | upstream/anthropic 的请求指纹存储 Adapter；不是 identity 用户会话缓存。 | S09.2 |
| usage billing、用户倍率/平台额度/订阅、兑换 | billing/postgres、billing/rediscache；闭合资金 SQL 和 task hold 原子性保持。 | S04、S13 |
| usage log/cleanup/聚合/dashboard 缓存 | usage/postgres、usage/rediscache。 | S08 |
| promo/affiliate | promotion/postgres；跨资金动作按第 3.3 节执行。 | S12 |
| creative/batch 的任务、队列、临时数据与下载限制 | 各自 postgres/rediscache；与 billing 的事务参与投影保留。 | S13 |
| proxy/TLS 配置/延迟 | egress/postgres、egress/rediscache；探测 HTTP 技术实现归 provider/infra。 | S06 |
| 错误透传规则/缓存 | gateway/postgres、gateway/rediscache。 | S11 |
| 内容审核、公告、邮箱 | moderation/site/notification 各自 Adapter。 | S02.4、S10 |
| Ops 与操作审计 | ops/postgres、audit/postgres；原有 SQL 性能特性和审计提交要求保留。 | S08 |
| setting/idempotency | settings/postgres、idempotency/postgres；不得通过 settings 仓储直接执行别的业务更新。 | S02 |
| `*_oauth_*`、Claude usage、Gemini Drive/Code Assist、Grok OAuth | 对应 upstream；外部 HTTP 不是数据库仓储。 | S09 |
| Turnstile/腾讯/阿里验证码 | identity/provider。 | S05 |
| GitHub release/远端 pricing | ops/provider、billing/provider。 | S03、S08 |
| backup dump/S3 | backup/provider；通用对象访问组件可复用 infra/storage。 | S14 |
| HTTP upstream/client pool | infra/httpclient + 注入的 egress 策略；上游 Interface 不依赖完整 service.Account。 | S01、S06、S09 |
| Ent/SQL/Redis 连接、deadlock、扫描/分页、instrumentation | infra/postgres、infra/redis；只含技术机制。 | S01、S02 |
| AES、leader lock | infra/crypto 与使用方锁 Interface/infra 实现；密钥配置和 leader 作用域由外层给出。 | S01、S02 |
| migrations runner、security secret bootstrap、simple mode 默认组/管理员 | runner → infra/postgres；流程编排 → app，由 internal/setup 调用；实体变更走对应模块的精简初始化入口。 | S02、S14 |
| `wire.go` | app；模块独立构造可留适配包 provider set。 | 逐步至 S16 |

文件登记（128 个，逐项唯一归组；下面的分组不是额外 Go package）：

- **app：组合与初始化（S02/S14）**：`security_secret_bootstrap.go`、`simple_mode_admin_concurrency.go`、`simple_mode_default_groups.go`、`wire.go`。

- **infra/postgres：连接、迁移与 SQL 技术（S01/S02）**：`db_pool.go`、`ent.go`、`error_translate.go`、`migrations_runner.go`、`pagination.go`、`postgres_deadlock_retry.go`、`server_timing_sql.go`、`sql_scan.go`。

- **infra/redis：连接与 instrumentation（S01）**：`redis.go`、`server_timing_redis.go`。

- **infra/crypto（S01）**：`aes_encryptor.go`。

- **锁接口的技术 Adapter（S02）：infra/redis，作用域由使用方持有**：`leader_lock_cache.go`。

- **identity/postgres、rediscache（S05）**：`passkey_repo.go`、`passkey_session_store.go`、`refresh_token_cache.go`、`totp_cache.go`、`user_attribute_repo.go`、`user_profile_identity_repo.go`、`user_repo.go`。

- **identity/provider（S05）**：`aliyun_captcha_verifier.go`、`tencent_captcha_service.go`、`turnstile_service.go`。

- **team/postgres、rediscache（S05）**：`team_invitation_limiter.go`、`team_repo.go`。

- **apikey/postgres、rediscache（S05）**：`api_key_cache.go`、`api_key_repo.go`、`auth_cache_invalidation_outbox_repo.go`。

- **routing/postgres（S06）**：`channel_repo.go`、`channel_repo_account_stats_pricing.go`、`channel_repo_pricing.go`、`group_availability_probe_repo.go`、`group_repo.go`。

- **account/postgres、rediscache（S06/S09）**：`account_repo.go`、`account_repo_ollama_cloud_usage.go`、`gemini_token_cache.go`、`internal500_counter_cache.go`、`openai_403_counter_cache.go`、`scheduled_test_repo.go`、`temp_unsched_cache.go`、`timeout_counter_cache.go`。

- **scheduler/postgres、rediscache（S07）**：`concurrency_cache.go`、`rpm_cache.go`、`scheduler_cache.go`、`scheduler_outbox_repo.go`、`session_limit_cache.go`、`user_msg_queue_cache.go`、`user_rpm_cache.go`。

- **gateway/scheduler 会话 Adapter（S07/S11，按方法拆分）**：`gateway_cache.go`。

- **egress/postgres、rediscache、provider（S06）**：`proxy_latency_cache.go`、`proxy_probe_service.go`、`proxy_repo.go`、`tls_fingerprint_profile_cache.go`、`tls_fingerprint_profile_repo.go`、`tls_fingerprint_router_cache.go`、`tls_fingerprint_router_repo.go`。

- **billing/postgres、rediscache（S04/S13）**：`billing_cache.go`、`redeem_cache.go`、`redeem_code_repo.go`、`usage_billing_repo.go`、`user_group_rate_repo.go`、`user_platform_quota_repo.go`、`user_platform_quota_service_adapter.go`、`user_subscription_repo.go`。

- **billing/provider（S03）**：`pricing_service.go`。

- **usage/postgres、rediscache（S08）**：`dashboard_aggregation_repo.go`、`dashboard_cache.go`、`usage_analytics_aggregation_repo.go`、`usage_cleanup_repo.go`、`usage_log_repo.go`、`usage_log_repo_analytics.go`、`usage_log_repo_analytics_queries.go`、`usage_log_repo_dashboard.go`、`usage_log_repo_insert.go`、`usage_log_repo_query.go`、`usage_log_repo_stats.go`、`usage_log_repo_trend.go`。

- **promotion/postgres（S12）**：`affiliate_repo.go`、`promo_code_repo.go`。

- **creative/postgres、rediscache（S13）**：`creative_queue.go`、`creative_run_outbox_repo.go`、`creative_run_repo.go`、`creative_transient_store.go`。

- **batchimage/postgres、rediscache（S13）**：`batch_image_download_limiter.go`、`batch_image_queue.go`、`batch_image_repo.go`。

- **gateway/postgres、rediscache（S11）**：`error_passthrough_cache.go`、`error_passthrough_repo.go`。

- **moderation/postgres、rediscache（S10）**：`content_moderation_hash_cache.go`、`content_moderation_repo.go`。

- **site/postgres（S02.4）**：`announcement_read_repo.go`、`announcement_repo.go`。

- **notification/rediscache（S10）**：`email_cache.go`。

- **ops/postgres、rediscache、provider（S08）**：`github_release_service.go`、`ops_ingress_reject_repo.go`、`ops_repo.go`、`ops_repo_alerts.go`、`ops_repo_dashboard.go`、`ops_repo_histograms.go`、`ops_repo_latency_histogram_buckets.go`、`ops_repo_metrics.go`、`ops_repo_preagg.go`、`ops_repo_realtime_traffic.go`、`ops_repo_request_details.go`、`ops_repo_request_timings.go`、`ops_repo_token_stats.go`、`ops_repo_trends.go`、`ops_repo_window_stats.go`、`ops_sla_sql.go`、`update_cache.go`。

- **audit/postgres（S08）**：`audit_log_repo.go`。

- **settings/postgres（S02）**：`setting_repo.go`。

- **idempotency/postgres（S02）**：`idempotency_repo.go`。

- **backup/provider（S14）**：`backup_pg_dumper.go`、`backup_s3_store.go`。

- **upstream/anthropic 及其存储 Adapter（S09.2）**：`claude_oauth_service.go`、`claude_usage_service.go`、`identity_cache.go`。

- **upstream/openai（S09.8）**：`openai_oauth_service.go`。

- **upstream/grok（S09.7）**：`grok_oauth_client.go`。

- **upstream/gemini/codeassist（S09.3，原生 OAuth 部分归 gemini）**：`gemini_drive_client.go`、`gemini_oauth_client.go`、`geminicli_codeassist_client.go`。

- **infra/httpclient + egress 策略绑定（S01/S06/S09）**：`http_upstream.go`、`req_client_pool.go`。

### 5.3 handler / admin / routes / dto / middleware

| 现有族 | 目标 | 实施关注点 |
| --- | --- | --- |
| auth_*、user、Passkey、TOTP、用户属性 | identity/httpapi | handler 内的 OAuth HTTP client 移 identity/provider；保留原始 callback/state 和验证码保护。 |
| team、api_key/apikey | team/httpapi、apikey/httpapi | 资源授权从安全主体获取；身份、额度、管理员角色不互相替代。 |
| account_*、各上游 oauth、scheduled_test、codex_invite_reset | account/httpapi | 导入/测试统一调用账号用例，平台字段校验由注入的平台能力处理。 |
| group/channel/model_marketplace/protocol_capabilities | routing/httpapi | 统一协议目录和市场同源；管理员 DTO 与公开投影分开。 |
| proxy/TLS profile/router | egress/httpapi | 只处理安全输入输出，配置选择归 egress。 |
| subscription/redeem/平台额度 | billing/httpapi | 充值/订阅/兑换/额度展示调用 billing 的用例和投影。 |
| payment/payment_webhook | payment/httpapi | webhook 保留验签所需原始 body/header，验签与状态机用例协作。 |
| promo/affiliate | promotion/httpapi | 不在 handler 拼余额更新。 |
| usage/dashboard、usage_query_cache/dashboard_query_cache/snapshot cache | usage/httpapi、usage 核心/缓存 Adapter | 查询缓存下沉，HTTP 只读取报告。Ops snapshot 归 ops，不混用不同数据面的 freshness。 |
| ops_*、audit_log | ops/httpapi、audit/httpapi | 观测读取与敏感操作授权区分，保留实时流关闭行为。 |
| backup/data_management | backup/httpapi | 保留旧兼容拒绝，文件下载和执行权限不改变。 |
| announcement/page | site/httpapi | 页面正文/路径读取通过 site 用例，静态 SPA 仍归 web。 |
| creative/batch_image | creative/httpapi、batchimage/httpapi | 保留工作区/用户/任务归属，已有任务管理不再调用新生成准入。 |
| content_moderation | moderation/httpapi | 日志和策略使用模块投影，媒体保留策略不能由 handler 旁路。 |
| setting 及其 audit/email/pre_aggregation/runtime/update 文件 | settings/httpapi + 对应模块的配置/查询接口 | URL 可以继续聚合；校验、更新副作用和版本通知归模块，不把代码复制到 settings。 |
| gateway_*、openai_*、qoder_gateway、gemini_v1beta、grok_audio/media | gateway/httpapi | 按客户端协议/操作组织文件；平台路由通过 gateway 用例，不按名字整文件迁到 upstream。 |
| failover_loop、session_isolation、user_msg_queue_helper、image_concurrency_limiter | gateway/scheduler | HTTP attempt 循环、槽位/等待释放、session 协调下沉。 |
| usage_record_context、reasoning_effort_usage、ops_error_logger、stream_error_event、no_account_error | gateway 完成/错误策略 + httpapi/protocol 输出 | 显式返回部分用量与提交状态；保留重试边界和协议错误。 |
| request_body_*、logging、endpoint、concurrency_error_response | server/httpx、telemetry、gateway/httpapi | 从通用读取分离协议允许的宽容修复，EndpointID 保持唯一来源。 |
| idempotency_helper、admin/idempotency_helper | idempotency + HTTP 参数适配 | 复用一个命令认领实现，避免重复创建宽接口。 |
| handler.go / wire.go、AdminHandlers/Handlers 聚合结构 | app 与 server 模块注册参数 | 构造留 app，路由按模块注册，最终删除全局 Handler 集合。 |
| routes/auth/user/admin/payment/gateway/protocol_capabilities | 各模块/httpapi/routes.go + server 汇总 | 不改变最终 URL 与 middleware 顺序，不允许重复注册或遗漏别名。 |
| routes/common.go | server | 保留健康检查和公共运行状态入口。 |
| middleware JWT/admin_auth/admin_only/step_up/session_binding/auth_subject | identity/httpapi | 通用角色/主体契约小而稳定，业务用例仍检查资源级权限。 |
| middleware api_key_* | apikey/httpapi + gateway/routing/billing 准入 | 中间件只取凭据/请求元数据并写安全主体，策略编排进入明确用例；最终序列不改变。 |
| middleware audit_log | audit/httpapi | 操作类型/归属由业务明确，不能把敏感审计等同普通 access log。 |
| 其余全局 middleware | server/middleware | CORS/CSP/日志/recovery/body/request ID 等；Panel 限流以模块设置投影注入。 |

DTO 逐文件归属：`announcement.go → site/httpapi`、`model_marketplace.go → routing/httpapi`、`notify_email_entry.go → identity/httpapi`、`credentials_redact.go → account 的安全输出投影 + account/httpapi`、`settings.go → settings/httpapi`。`types.go`、`mappers.go` 按实体拆入上述模块，不能整体搬成另一个全局 dto 包。普通/管理员字段可在所属 httpapi 内复用，但禁止从业务根包反向 import DTO。

### 5.4 domain / model 与命名歧义

| 源文件 | 目标与说明 |
| --- | --- |
| domain/announcement.go | internal/site。 |
| domain/constants.go | 用户/角色常量 → identity；账号/平台标识 → routing/capability 的纯类型；其余按所有者拆分。 |
| domain/group_advanced_scheduler.go | internal/scheduler/policy 的算法参数/验证；routing 保存其配置投影，避免双方根模块循环。 |
| domain/group_availability_probe.go | routing 的可用性配置与状态投影。 |
| domain/group_client_protocol.go | routing/capability 的默认/校验规则，协议 ID 来自 protocol。 |
| domain/models_list_config.go | routing。 |
| domain/openai_messages_dispatch.go | routing/capability 的可选路线定义，平台执行仍归 upstream/openai。 |
| domain/protocol_catalog.go | ProtocolID → protocol；纯平台能力/转换关系 → routing/capability；HTTP 路径元数据 → gateway/httpapi 的路由声明，由 server/app 注入目录展示；routing/httpapi 不反向导入 gateway/httpapi，保持同一数据源。 |
| domain/reasoning_effort.go | 协议档位值 → protocol；管理员映射/上限规则 → routing。 |
| domain/subscription.go | billing 的权益/分配投影；去掉任务对旧 domain 的依赖。 |
| model/error_passthrough_rule.go | gateway；平台常量引用 routing/capability，HTTP 输出映射仍由协议 Adapter 执行。 |
| model/tls_fingerprint_profile.go、tls_fingerprint_router.go | egress 管理模型；底层握手技术参数由独立技术类型表达。 |

不按名字机械归类：`IdentityService` 当前管理上游 OAuth 请求指纹；`UserMessageQueueService` 当前是账号级消息串行；`xai.Billing*` 是供应商账单；`GeminiTokenCache` 被多平台刷新使用。迁移时分别重命名以表达真实责任，不延续历史误导，也不将它们错误并入用户身份、通知、用户计费或单一 Gemini 模块。

## 6. 多步迁移计划

### 6.1 阶段依赖与执行粒度

建议按 S00 → S16 的编号顺序执行。每个阶段先按 7.1 生成并持久化独立子计划，再进入实施；不提前为全部阶段生成空计划。编号是能力完成顺序，不是“一次提交迁一个大阶段”的要求。含多个子步骤的阶段按子计划逐步交付、验证和记录，阶段状态汇总到末尾 roadmap。

| 阶段 | 本阶段交付 | 最少前置 | 暂未迁移的依赖如何满足 |
| --- | --- | --- | --- |
| S00 | 清点、契约基线、增量跟踪 | 无 | 不改运行代码。 |
| S01 | 新依赖门禁、通用包、HTTP/技术基础设施 | S00 | 先启用新路径规则，旧包用薄包装调用新实现。 |
| S02 | app/生命周期、settings/idempotency、小模块试点 | S01 | app 仍装配旧图，逐项替换 provider。 |
| S03 | 协议、能力目录、纯定价 | S01—S02 | 新纯模块不需要旧业务；旧调用者转接新实现。 |
| S04 | 普通结算、权益用例及明确资金操作迁入 billing | S02—S03 | 同事务转接；其它资金写入按 3.3 保留完整旧事务并登记，后续分阶段收敛。 |
| S05 | identity → team → apikey | S04 | 通知/促销/路由状态通过 app/legacybridge 提供，不反向导入旧包。 |
| S06 | egress → routing → account | S03—S05 | 供应商维护能力和旧调度失效通过旧 Adapter 注入。 |
| S07 | scheduler | S05—S06 | 平台资格/连续性使用旧适配实现，统一选择结果和 Lease。 |
| S08 | usage → audit → ops | S04—S07 | 通知、备份只提供窄调用接口；原有日志队列语义保留。 |
| S09 | 首条网关链验证 + 各上游逐平台迁移 | S03—S08 | S09.1 先验证新 gateway 的非流/SSE 分支；未迁审核/完成处理通过旧 Adapter 提供，其余分支逐个切换。 |
| S10 | notification、site 收尾、moderation、search | S05—S09 | 旧 gateway 可先消费这些新能力。 |
| S11 | 扩展已验证的 gateway 至其余协议/传输 | S04—S10，含 S09.1 验证通过 | 复用首条链契约；支付/任务等面板能力仍使用旧入口。 |
| S12 | promotion → payment | S04、S05、S08、S10 | 旧 handler 可以先调用新商业用例，随后迁 HTTP Adapter。 |
| S13 | creative → batchimage | S04、S05—S11 | 共享新资金/调度/上游能力；任务状态与资金投影一起替换。 |
| S14 | backup、setup、维护命令 | S02、S06、S08、S10 | 不再需要业务旧依赖；保留部署兼容。 |
| S15 | HTTP 注册、DTO、设置聚合收尾 | S05—S14 | 清空旧 handler/routes 与最后跨模块适配。 |
| S16 | 删除旧包、测试/文档/构建总验收 | S01—S15 | 删除所有 legacybridge；新增代码只使用目标结构。 |

独立能力可以提前实施，但必须已经具有明确的旧 Adapter 和验收条件。不得为了“先迁文件”临时放开新核心对 `service` 的依赖。

### S00：冻结本次实施基线

**处理范围**：全部第 4 节旧包；只清点不移动。读取 `AGENTS.md`、Project Doc 的对应章节、现有 Makefile、CI、depguard、构建标签与工具链。

1. 确认工作区用户改动，记录本次 HEAD；重新枚举所有含 Go 文件目录，与第 4 节比较新增/删除项。对新增 service/repository/handler 文件补上模块和阶段归属。
2. 记录当前构建、普通/unit/integration 测试和 lint 结果。原有失败独立记录，不混成重构导致，也不能悄悄扩大忽略规则。
3. 为待迁能力定位现有契约测试；优先复用。只有缺少会暴露行为回归的测试时补充 Interface 测试，不为单纯移动代码复制一套测试。
4. 在 S00 子计划中建立源文件、目标包、调用者、测试及其构建标签、Wire/文档引用清单，后续阶段按最新代码补充各自范围。将 3.3 的资金写入组展开到方法，记录外层事务拥有者、提交后副作用与迁移阶段。
5. 按 3.2 区分新建目标路径、原路径保留旧包和待迁 pkg，列出实际源文件/import 与退出子步骤。此清单用于 S01.0 的精确过渡规则，不把 payment 等既有目录当作已迁核心，也不把整个 pkg 当作纯工具。

**验收**：不存在无人负责的旧包；能够解释 baseline 的构建/测试限制。此阶段不宣布任何业务模块已迁完。

### S01：启用依赖门禁并提取通用技术与 HTTP 基础能力

**处理范围**：现有 `.golangci.yml`；pkg 的 `pagination/errors/response/httputil/ip/logger/servertiming/httpclient/proxyurl/proxyutil/tlsfingerprint/redissession/timezone`；oauth 的重复 PKCE 原语；util 的 `logredact/urlvalidator`；第二处 middleware；repository 的纯连接/HTTP/crypto 实现。

**子步骤**：

- S01.0：按 3.2 在现有 depguard 建立新核心、协议、上游、技术实现与 Adapter 的规则，落实 S00 的路径分类与逐文件/import 例外。保留旧规则，验证原路径旧文件的已登记依赖可通过；再用可丢弃夹具验证同目录新增文件的非法依赖、旧文件新增的禁止依赖都被拒绝。后续每个新包或新增文件的首次提交必须同时证明被规则覆盖；payment 等旧包的重构仍按所属阶段完成。
- S01.1：保留 pagination；将通用脱敏迁 `pkg/logredact`；提取纯 IP 匹配和 PKCE。日期运算先迁接口，继续兼容当前项目日界与全局初始化。
- S01.2：建立 `pkg/apperror`、`server/httpx` 与 `server/clientip`。先保证旧错误类别、reason、脱敏和响应结果相等，再逐调用方去除 HTTPCode 依赖。请求体上限/解压/原始报文保留原契约。
- S01.3：建立 logging/timing、Redis session、HTTP 连接池、proxy、TLS、crypto 实现。按现有隔离 key、配置和取消测试接入；暂不改变连接池策略。
- S01.4：分离固定窗口限流技术实现与 HTTP 的失败策略，保留原 route 的 fail-open/fail-close；技术型旧函数可作为短期转接。

**代码重构**：清除工具包对整个 config/业务 service 的反向依赖，构造接受小型 Options。通用工具之间避免新的双向依赖，不要求把私有小函数逐个建包。

**验收**：新路径和已有目标目录中新增文件的 depguard 已生效；已登记旧依赖可通过，新增禁止依赖可被拒绝，例外有明确退出子步骤；受影响基础包按 7.3 运行普通及适用标签测试，所有旧调用者继续构建；Header/脱敏/错误/代理/DNS/取消契约一致。旧 pkg 错误包装暂存时必须登记消费者，不能复制维护两份错误实现。

### S02：建立组合根与可独立迁移的用例

**处理旧包**：cmd/server、config/wire、各层 wire、service 的 setting/idempotency/announcement/timing wheel/leader lock 组、对应 repository，pkg/sysutil，server/web 的依赖传递。

**子步骤**：

- S02.1：把应用图和 Cleanup 移至 app。保留 `cmd/server` 的 `main.Version` 等构建变量和原 `go generate ./cmd/server` 入口；入口可以委托新的 Wire 生成位置，Makefile/CI 命令仍有效。禁止手改 wire_gen。
- S02.2：引入 app/lifecycle。先包装已有 Start/Stop，再逐模块移出构造即启动的副作用。初始化中途失败时，逆序关闭已成功构造/启动资源；HTTP 与 worker 的 drain 顺序可验证。
- S02.3：迁 settings 的存取/版本部分和 idempotency 完整能力。旧业务设置解释暂留旧模块，通过显式 Adapter 注册；不可把旧 SettingService 整体包一层算完成。
- S02.4：以公告/已读/到期为首个完整纵向试点，迁 service/handler/admin/dto/repository 到 site，验证 routes + storage + worker + Wire 的模块形状。site 其余页面内容留 S10。

**过渡桥接**：新建的“为新模块提供旧能力”的临时 Adapter 统一放 `app/legacybridge`，可以同时引用旧 service 与新模块。旧调用方可以单向引用新模块的契约和入口；新核心不引用任何旧包，也不引用 legacybridge。桥接只转换投影/调用接口，不拥有业务状态。

**验收**：正常启动、setup 分支、初始化失败、SIGTERM 停机均保持资源释放；公告完全使用新实现；settings/idempotency 有真实调用者与清晰删除旧入口的清单。

### S03：协议、能力目录和纯定价先成为叶子能力

**处理旧包**：pkg/apicompat/googleapi/gemini 的 wire 部分，antigravity 的通用 DTO/转换，pkg/claude/openai 的纯解析部分，domain 的协议/能力/推理定义，service/protocol_*、pricing/model_pricing/media_price 等纯计算。

**子步骤**：

- S03.1：移动 apicompat 的类型和至少一组非流双向转换，再逐组迁流事件转换。最初可以同包过渡，最终按第 2 节分成各协议与 bridge；拆分时必须同批迁移类型消费者，避免两种同名 struct 长期共存。
- S03.2：建立 `protocol.ProtocolID` 与 `routing/capability`。能力目录使用纯数据输入/表定义，路由展示从同一来源派生；保留 24 项协议、账号集合、单步 fallback 与前端 fixture。目录条目数量是基线事实，实施时以实际最新目录为准。
- S03.3：把平台特有的 schema/thinking/signature 条件从通用转换器抽为显式 option 或上游预后处理；不能让转换器为选账号读取数据库。
- S03.4：建立 billing/pricing。分组/渠道价卡解析、用户价格、账号统计成本、token/媒体模式和时间倍率通过纯输入输出执行；加载缓存/远端数据的实现放 billing/provider。

**过渡**：旧代码调用新纯函数。类型 alias 只有在所有方法已迁移且未改变可变状态语义时使用；不能在旧包为非本地 alias 类型新增方法。

**验收**：转换非流/流、未知字段、工具 ID、reasoning、部分 usage 的契约测试；价卡优先级、显式零价、缺价、长上下文、分时和账号成本口径测试。业务目录、HTTP 路由与协议转换没有反向依赖。

### S04：抽出 billing 的结算、权益和事务参与能力

**处理旧包**：service 的 billing/pricing 使用方、usage_billing、subscription、redeem、user_group_rate、user_platform_quota、account_stats_pricing、video/image 计费；repository 对应资金/权益/缓存组，以及 user_repo 中已经确认可独立提取的原子调账操作；domain/subscription；handler/quotaview、订阅/兑换/额度相关 DTO/handler。其余写入逐项按 3.3 登记，不在此阶段拆散它们的外层事务。

**子步骤**：

- S04.1：提取资金主体、定价快照、分配和结果类型，明确 payer/actor/team/Key/account。旧 User/Account 的输入在桥接侧投影，不反向导入旧类型；确认每条当前写入是否属于本阶段范围、是否参与外层事务。
- S04.2：原样迁移闭合 `Apply` 事务与去重/死锁重试，再在测试保护下把复杂计算移到 pricing/资金规则中。普通结算一次提交的原子范围不变。
- S04.3：迁订阅创建/延长/排队/撤销、额度窗口、用户平台额度、兑换权益与缓存更新，并提取原子调账操作。新增普通资料更新接口不接受余额等资金字段；旧创建/默认赠送等写入按 3.3 保留到其所属阶段，不因移除字段破坏现有功能。
- S04.4：建立任务资金 Interface，并以旧 BatchImage 命令适配接入。此时允许 SQL 内仍显式认识两个旧任务投影，但不得扩展新布尔分支；将删除条件绑定 S13。
- S04.5：建立并测试 3.3 的事务内 Adapter。对本阶段已迁用例，将旧网关、管理、支付和任务调用者转接到新 billing；已有外层事务必须沿用同一事务，退回/审计/次数记录失败时整体回滚。退款、Promo 和返利等未迁闭合事务继续使用原实现并明确登记。
- S04.6：迁订阅、兑换、额度展示的 HTTP Adapter；公共 usage 查询返回的权益投影不能混淆绑定订阅和余额。更新资金写入清单及例外，分别记录“资金操作已迁”和“业务编排尚未迁”。

**验收**：同 ID 重放/指纹冲突、并发扣款与调账、付款 owner/actor、指定订阅准入与已放行请求溢出、金额 8/10 位精度、不同定价时刻、日志失败不重复扣款、任务严格预占与释放；真实 PostgreSQL 验证新操作参与旧外层事务的回滚，确认提交前不发布成功副作用。普通/unit/integration 测试及相关 Redis 测试通过；原 Promo/返利/退款流程回归保持。simple 模式仍仅写适用记录。

**停点**：S04 结束可继续使用旧网关和旧任务 UI；本阶段迁移的普通结算和权益能力只有一份 Implementation。尚未迁移的注册/管理写入、Promo/返利/退款、任务投影和初始化/恢复按 3.3 分别保留到 S05—S07、S12、S13、S14；这些例外未清理前，不把 billing 标记为全项目资金写入的唯一实现。

### S05：依次迁移用户身份、团队和 API Key

**处理旧包**：service 用户/auth/passkey/totp/session/team/api_key/认证 outbox/captcha 组，repository 对应组，handler/auth/user/team/key/admin 对应组，middleware 认证部分，dto 与相关 domain 常量。

**子步骤**：

- S05.1：identity 的资料/登录身份/会话核心及 PostgreSQL/Redis/登录与 captcha Adapter；安全身份只携带验证后的用户/会话/角色。通知和默认赠送仍通过 legacybridge 的窄接口完成。
- S05.2：team 的成员、邀请、owner 转移与资源归属，保留同一事务内维护唯一 owner 和成员关系；billing 通过付款/行为投影消费。
- S05.3：apikey 生命周期、复合 Key、访问策略及缓存/outbox。Key 拥有模型改写配置和前缀解析规则，routing 消费规范化规则完成请求的一跳改写，不复制两套映射算法。认证产出 AccessSnapshot，资金消费能力通过 billing 检查。
- S05.4：迁 HTTP JWT/admin/step-up/Key Adapter，保留 route 原鉴权顺序和入口错误形状，所有资源操作仍复核归属；管理员调账改用 billing 用例，注册/身份默认权益通过同事务 Adapter 配合，销项 3.3 中对应的旧写入例外。

**解耦重点**：identity、team、apikey 不相互持有具体 Service。必要读取通过消费者 Interface，在 app/bridges 绑定；缓存变化通过明确失效命令/现有 outbox，不靠导出可变对象。订阅限制对 Key 可选分组的校验用只读权益投影。

**验收**：邮箱并发注册、绑定/解绑最后身份、token version、refresh family、TOTP/Passkey、step-up、团队移除后再加入、转 owner、复合前缀与模型改写、Key 缓存跨实例失效、captcha 故障策略和公开/管理员字段隔离。

### S06：建立出站策略、路由与账号管理

**处理旧包**：service egress/group/channel/model/account/credentials/refresh/CRS/health 组，domain/model 剩余配置类型，repository 对应组，pkg/openai_compat、util/responseheaders/urlvalidator。

**子步骤**：

- S06.1：egress 的代理/TLS 管理、选择、fallback 与缓存失效；注入 S01 的普通/指纹传输，规则与实际拨号分开。
- S06.2：routing 的分组/渠道/市场/价卡存取/默认回退/协议路由；返回不可变 RoutePlan，引用 S03 pricing/capability 叶子规则。候选级账号模型与协议结果保持每 attempt 重算。
- S06.3：account 的管理、凭据保存/刷新协调、影子账号、导入、测试与维护。供应商 OAuth/探测依赖先通过 legacybridge 提供，不把这些旧 Service 搬进新 account。
- S06.4：迁 HTTP/仓储 Adapter 与 snapshot/outbox 生产端。管理员修改组平台/账号凭据/代理后按原语义触发渠道、认证、调度和 client 缓存更新。

**解耦重点**：静态能力、可认证、可消费、可调度分开；account 不调用具体 scheduler，向现有 durable outbox/失效 Interface 写变更；routing 不 import billing 根服务，只用 pricing 类型和报价 Interface。

**验收**：配置原子写、批量编辑/导入、影子父账号约束、账号原生集合与旧字段转换、空准入集合、单步转换、价卡一致性、代理到期 fallback、TLS 池隔离/跨实例失效、刷新竞争。无法访问真实上游的部分用录制脱敏夹具/本地 HTTP Adapter 验证，不请求生产凭据。

### S07：调度、并发与会话选择

**处理旧包**：service 的 advanced_scheduler/scheduler/gateway_scheduling/openai_account_scheduler/concurrency/ratelimit/user_msg_queue/session_limit 等；repository 对应缓存和调度 outbox；handler 的 wait/release helper。

**子步骤**：

- S07.1：迁候选快照/outbox/epoch/tombstone 和受控 DB fallback，实现独立可读的候选投影。
- S07.2：迁 basic/advanced 评分与平台资格 Interface；平台特有资格先注入旧 Adapter，后续 S09 替换。
- S07.3：将用户并发、账号并发、等待计数和串行队列的生命周期放入调度用例，返回 Lease/WaitResult；保持先用户槽、再账号槽与等待后二次权益校验的责任分工。
- S07.4：迁粘性选号与平台连续性约束输入，诊断调用同一核心；旧 handler 获取新 Lease 后按 3.4 的生命周期完成释放。等待/非流取消与 Qoder 流式完成释放分别保留，不能把全部 Lease 自动挂到客户端取消信号上；此阶段即回归既有取消与释放测试。

**解耦重点**：减少调用方需要知道的获取顺序和补偿步骤；Lease 只能释放自己持有的资源，重复释放安全。用户登录 session、平台 OAuth 授权 session、调度粘性和上游会话分别归属。

**验收**：basic/advanced 默认与覆盖、硬过滤前后顺序、同分/缺省观测、队列满/超时/取消、槽位泄漏、粘性与硬 previous-response 绑定、混合池、快照旧写覆盖、DB fallback 上限。对竞争热点运行针对性 race/多实例 Redis 测试。

### S08：用量查询、审计与 Ops

**处理旧包**：pkg/usagestats；service 的 usage/dashboard/ops/audit/update 组；repository 的所有查询/聚合/Ops/audit/cache/client 组；handler/admin 对应查询和缓存 helper。

**子步骤**：

- S08.1：usage 的写事实、查询、Dashboard、聚合/回填/清理与 HTTP Adapter。资金 worker 的命令执行责任暂留 gateway 完成处理的过渡层，不随普通分析日志一起搬走。
- S08.2：audit 的写入与查询能力，区分事务内强制审计与尽力观测；支付专项审计仍由 payment 状态机拥有。
- S08.3：ops 指标/系统日志/入口拒绝、告警、实时流、报告/清理与升级查询；通过窄快照读取 scheduler/account/billing 健康状态。

**解耦重点**：技术 telemetry 不认识业务表；Ops 可观测业务但不持有可变服务状态。仪表盘把已有跨表读取作为命名明确的查询 Adapter，保留批量 SQL，不能改成每行远程式逐条调用。

**验收**：总额/账号成本/用户成本、聚合水位与回退、删除用量不退款、敏感字段脱敏、日志过载/退避/丢弃与停机、必需审计失败回滚、实时连接关闭。查询重构核对 SQL 数量和原 Explain/性能夹具，防止 N+1。

### S09：先验证完整请求链，再逐个平台迁移上游

**共同处理范围**：第 5.1 节对应平台族、pkg 平台包、repository 外部 HTTP 客户端、service/openai_ws_v2、platform/liveattestation、util/httputil；同时提前迁移首条请求所需的 gateway/httpapi、最小执行编排、流输出状态和完成处理接口。

先完成 S09.0 和 S09.1 的首条链验收，再推广至其它平台。每个平台独立作为可交付子步骤，提取原生客户端/类型、转换/流/错误/用量，并通过 app 替换调用；平台不接管账号选择、用户计费或全局重试。S09.2 起既要保持旧入口兼容，也要验证它满足首条链已经确定的新 Interface，不能只提供适配旧 Gin handler 的接口。

**S09.0：确定首条链的契约和测试入口**

- 选用当前已支持的 Qoder Chat Completions 分支，分别覆盖 `stream=false` 与 `stream=true`；Qoder 原生流的非流聚合仍沿用现有行为。明确旧 handler、QoderGatewayService、调度/资金调用及测试夹具的具体迁移清单，不新增公网 URL 或扩大支持范围。
- 沿用 S05/S06/S07 的 AccessSnapshot、RoutePlan、AccountSnapshot 和 Lease；在 gateway/upstream 的各自契约中确定单次 attempt 输入、输出提交状态、部分 usage、错误、取消、响应体及连接释放责任。按 3.4 区分客户端断开与上游执行终止，明确 Qoder 非流传播取消、流式有界收集尾部用量及显式完成释放的策略。只定义首条链实际消费的能力，避免强制所有供应商实现一个万能接口。
- 将尚未迁移的 moderation、错误改写和完成处理通过 app/legacybridge 的窄接口注入。每项适配写明由 S10 或 S11 删除；共享 worker/缓存保持唯一实例。依赖是新 gateway 的接口指向适配实现，不允许 gateway import 旧 service。
- 建立以本地供应商 Adapter/脱敏事件夹具驱动的完整请求测试，连接实际新模块和适用的 PostgreSQL/Redis 测试依赖。S09.0 的输出是可用于 S09.1 的契约与夹具，不能单凭接口声明宣布链路验证通过。

**S09.1 的执行顺序与门禁**：先提取 Qoder 调用 Adapter，再把选定非流分支接入新 gateway，随后接入同一分支的 SSE。两条链都必须贯通 HTTP → 鉴权/路由 → 资金预检 → 调度 → 上游 → 完成处理，并接入既有路由的唯一执行路径；同一次请求只有一个 attempt/重试循环，不能在新 gateway 外面继续包一层旧 failover。未迁协议分支可继续使用旧入口，但不得复制结算、槽位或输出状态实现。

首条链必须通过以下情形，才允许开始 S09.2—S09.8：

| 情形 | 必须验证的协作结果 |
| --- | --- |
| 非流成功及重放 | 原状态/报文与模型恢复保持，用户价格/账号成本正确，同请求幂等结算、单次写事实。 |
| SSE 正常输出 | 保留事件顺序、工具 ID、结束事件和渐进输出；不能为了统一结果而缓存完整响应。 |
| 前导事件后失败 | 按既有规则判断是否可切换；等待心跳与协议前导不能误判成真实输出。 |
| 真实输出后失败 | 明确禁止换号或重新推理，返回可用部分 usage，沿原规则结算和记录。 |
| 等待中取消与非流取消 | 停止等待/重试；非流取消传到上游，按既有责任释放等待计数和已获取的资源。 |
| Qoder SSE 客户端断开 | 停止下游写入，继续在既有执行超时内读出尾部 usage；不能因客户端取消提前释放用户/账号槽，也不新开 attempt。 |
| 上游结束、错误或执行超时 | 返回已观测的可用 usage，按既有完成次序结束读取、关闭响应体并显式释放槽位；重复 Release 安全，释放后不残留等待计数。 |
| 慢客户端 | 保留流式渐进输出与有界缓冲；写失败后按上述流式断开策略完成，不增加无界 goroutine 或无限排水。 |
| 结算或记录失败 | 重试不能重新调用供应商，也不能重复扣款；保持现有资金失败恢复与记录队列语义。 |

验收记录中列出新接口、实际调用者、上述场景结果和剩余旧 Adapter。后续平台需要扩展契约时，保持已迁调用者兼容并回归这组公共场景；某能力确实只适用于一个传输时采用独立接口，不反复推倒全部已迁平台。

**S09.1 必须保留并迁移的生命周期回归**：以下为当前测试位置，迁移后更新路径并按 7.3 确认测试实际入选；上游结束/错误/超时及完整链资源释放还须由上表场景验证，不能只测 Context 或包装函数。

- [qoder_gateway_service_test.go](../backend/internal/service/qoder_gateway_service_test.go)：`TestQoderGatewayStreamClientDisconnectStillCollectsUsage` 验证断开后仍收集 usage；`TestQoderForwardContextDetachesStreamingFromClientCancellation` 同时保护流式脱离客户端取消与非流传播取消。
- [qoder_gateway_handler_test.go](../backend/internal/handler/qoder_gateway_handler_test.go)：`TestQoderStreamReleaseDoesNotFireOnClientCancel` 验证槽位等显式完成才释放且仅释放一次；`TestQoderNonStreamReleaseStillFiresOnClientCancel` 保护非流取消释放。S07 若先替换 Lease，必须提前执行这组释放回归。

| 子步骤 | 源包/文件范围 | 目标与专属验收 |
| --- | --- | --- |
| S09.1 Qoder 与首条链 | pkg/qoder、service/qoder_*、Qoder handler 的平台转换与选定 Chat 分支编排 | upstream/qoder + gateway/httpapi 与核心；先完成上述非流/SSE 纵向验收，再覆盖其余 Qoder 协议，保留站点/PAT/Cosy、签名、思考/上下文、模型别名与部分用量。 |
| S09.2 Anthropic/Bedrock | pkg/claude/oauth/anthropicfp、repository/claude_* 与 identity_cache、service/identity_service、Claude token、gateway Anthropic/Bedrock 与 bedrock_* | upstream/anthropic、bedrock；OAuth/Header、请求指纹、缓存桶、区域签名、流事件、转换和可切换错误。 |
| S09.3 Gemini/Code Assist | pkg/gemini/geminicli/googleapi 的平台部分、repository/gemini_*、service/gemini*/geminicli*、batch/creative 的 Gemini 调用 | upstream/gemini 及其 codeassist 子包；OAuth/project、thought signature、工具 schema、原生/兼容流与批量原语。 |
| S09.4 Vertex | service/vertex_service_account、batch_image_provider_vertex、Gemini/Claude 中 Vertex 分支 | upstream/vertex；Service Account/project/location、签名/端点、GCS/JSONL 与既有 Batch 操作。 |
| S09.5 Antigravity | pkg/antigravity、service/antigravity_* | upstream/antigravity；专有封装、Claude/Gemini 转换、混合资格、credits/QuotaPlatform 与限流分类。 |
| S09.6 CN/Ollama/用量查询 | service/cn_provider_*、ratelimit_cn_providers、upstream_usage_cn_adapters、ollama_cloud_usage*、openai_gateway_ollama_cloud_*，统一上游用量查询的具体分支 | upstream/kimi/zhipu/deepseek/ollama/usageprovider；协议集合、自定义端点、模型能力/价格候选、余额/订阅口径。 |
| S09.7 Grok | pkg/xai、repository/grok_oauth_client、service/grok_* 和 openai_gateway_grok_*，creative Grok 调用 | upstream/grok；OAuth/API Key、媒体/Voice/搜索、订阅/额度、错误/用量、WebSocket 兼容与协议准入。 |
| S09.8 OpenAI | pkg/openai、repository/openai_oauth_service、service/openai_* 剩余与 codex_*、openai_ws_v2、platform/liveattestation，creative OpenAI 调用 | upstream/openai 及其 wsrelay、liveattestation 子包；API Key/Codex/PAT/Agent Identity、Responses/Chat/Images/Live、WS 连接/continuation、部分 usage 和 Darwin/非 Darwin 编译。 |

**解耦重点**：共用协议形状通过 protocol 复用，通用 HTTP 经 egress 策略注入；平台注册由 app 完成。GetToken/Refresh/Probe 的返回值描述平台观测，账号状态保存由 account 决定。批量/创作的 provider 已可先改为新上游客户端，任务状态机保持到 S13。

**验收**：每个平台分别有非流、流式、错误、取消、认证/端点、用量的 Interface 测试和网关契约测试。比较流时使用事件序列/语义，不能只比较最终文本。禁止同时对真实上游发送新旧两次推理以做影子验证；只读纯转换和脱敏夹具可以做差分。

### S10：通知、站点、审核和搜索

**处理旧包**：service email/notification/balance_notify/content_moderation/websearch 组、pkg/websearch、repository 对应缓存/仓储、handler 对应业务；site 的 page/settings 内容收尾。

**子步骤**：

- S10.1 notification：模板、邮件队列、发送 Adapter；触发条件由调用模块确定，停机保证仍由 lifecycle 管理。
- S10.2 site：完成页面、站点公开信息与设置投影，保留 S02 已迁公告能力；web 只接收可公开的数据。
- S10.3 moderation：检查→裁决→处置，Hash/关键词/平台审核 Adapter、无媒体留存、异步观测分别可验证；封禁通过 identity/apikey/account 的窄命令。
- S10.4 search：Brave/Tavily 选择、额度预占/回滚、Redis 状态和供应商 Adapter；网关工具模拟只调用搜索 Interface。

**验收**：模板不泄露凭据、发送失败不回滚既有权益；审核 sync/async/fail-open/fail-close 按已有场景保持；创作台无留存；搜索耗尽/失败/代理不可用的配额回滚与降级；移除对应 legacybridge。

### S11：扩展并收敛网关的共同执行流程

**处理旧包**：剩余 gateway/openai_gateway 服务的请求编排、handler 内 failover/并发/stream/usage helpers、ctxkey/request_metadata、错误透传 model/repository、所有网关 HTTP 族。

**子步骤**：

- S11.1：复用并收紧 S09 已验证的请求元数据、执行/输出结果与部分用量契约，移除其它链的 Gin Context 业务依赖；AccessSnapshot、RoutePlan 继续由原所有者提供。客户端识别收敛到 clientmeta，策略裁决留 gateway/routing。
- S11.2：将其余 HTTP 非流分支接入已验证的共同执行流程，通过新 Key/路由/资金/调度/审核/上游接口执行。删除各旧链的重复编排和已完成依赖的 legacybridge，不重新设计首条链。
- S11.3：推广 S09 验证过的 SSE 输出状态、前导缓冲、可重试错误、部分 usage 与 release 契约，逐个替换其余 SSE 分支；不同协议的真实输出边界仍由其 Adapter 判断。逐平台核对取消与收尾策略，不把 Qoder 的断开后继续读用量套用到全部平台，也不因统一编排改成所有流都立即取消。
- S11.4：逐条迁 Messages、Responses、Chat、Gemini、count/input tokens、模型/用量、图像、视频、搜索和 Voice。每条原生/转换路线分别验证，不能用一个平台的测试代表所有路线。
- S11.5：迁 Responses WebSocket、Live 与 sideband。平台帧实现继续留 upstream；每 turn 路由、资金时刻、硬会话绑定、连接终止由明确 Interface 协作。
- S11.6：迁完成处理 worker、错误透传和 Ops 观测；区分已结算、待对账记录、失败无用量与部分成功，不新建无界 goroutine。

**解耦重点**：外部调用方执行一次请求只需给出输入/响应输出能力，不必知道如何组装槽位、BillingCache、平台 token、重试、结算和日志。模块内部仍可有独立可测 Seam，但不会全都泄露到外部 Interface。

**验收**：请求顺序、复合 Key、fallback 后权益复核、协议空集合、每候选单步转换、原生透传、真实输出后不切号、部分用量、每次请求/turn 幂等结算、用户/账号槽释放、WS turn。保留排查用 request ID/时延字段和既有开关语义。

### S12：促销和支付

**处理旧包**：internal/payment/provider、service payment/affiliate/promo 组、repository promo/affiliate，handler/admin 对应族，支付设置与通知。

**子步骤**：

- S12.1：promotion 的邀请/促销/返利状态与规则。Promo 的锁码、加款、usage、次数，以及返利认领、主余额、累计充值和转账记录，分别由 promotion 的事务 Adapter 同事务组合 billing 存储操作；不调用会独立提交的加款入口。补验加款后业务记录失败的整体回滚，再销项 3.3 的旧写入；注册默认赠送与兑换联动移除旧具体服务引用。
- S12.2：保留现有 internal/payment 及 provider 子包路径，合入旧 service 的支付用例，重构金额/币种/手续费、provider registry、实例选择、下单与订单/商品快照；从 core 抽出 Ent 查询和 provider 构造，将跨 Adapter 的 Wire 装配移 app。同批删除 S01.0 为旧 payment 文件登记的临时例外，验证全体核心文件适用最终规则。
- S12.3：统一 webhook/查单/人工恢复/超时回收的状态迁移与履约 Interface，保留 lease、source_order_id、内部 recharge code 和返利审计去重。
- S12.4：退款和查单恢复，按 3.3 的闭合事务 Adapter 原子完成资金回收/订单/审计；验证外层失败回滚后删除旧退款资金写入，销项清单。HTTP 原始签名内容不能被通用解析提前消费。

**验收**：重复/迟到回调、取消与成功竞争、履约失败接管、来源订单重复发放、pending/partial/refunded、金额币种与 provider 快照、强制退款实际扣减、审计失败回滚、通知重复。真实支付服务使用受控 fixture/测试 Adapter，不执行真实付款退款。

### S13：创作台和批量图片任务

**处理旧包**：service creative/batch_image 组，repository 两类任务/队列/临时存储/下载限制，handler/dto 对应族，billing 中过渡任务资金投影。

**子步骤**：

- S13.1：creative 模型/生命周期/工作区归属/托管 Key/租约/outbox 与 PostgreSQL/Redis Adapter。平台执行使用 S09，调度用 S07，审核用 S10。
- S13.2：creative 接入最终通用 Reserve/Capture/Release；事务内任务资金投影由其 Adapter 提供，保留数据库/Redis 间 provisioning 与清理恢复。
- S13.3：batchimage 同样迁移任务/队列/执行/索引/下载/清理，保留 provider 绑定及 GCS 对象生命周期。计费估价/预占快照与后续 capture 共用原任务版本。
- S13.4：移除 billing 内 `CreativeEntity`、对两张任务表的核心级了解与跨模块的 BatchImage 命名；核对两类任务的旧数据/请求指纹重放兼容后删除过渡 Adapter。

**解耦重点**：只复用资金引擎、调度、平台能力和确实同语义的队列技术。两种任务状态机和素材生命周期独立；不要引入统一 JobService 重新混合它们。

**验收**：同幂等键不同 payload、租约失效/接管、成功后重试不重新推理、取消与 provider 成功竞争、结果丢失、临时数据 TTL/ACK 删除、Redis 故障、预占与任务标记原子性、捕获/释放重放、旧任务价格快照、指定订阅严格预占。

### S14：备份、初始化与维护入口

**处理旧包**：backup/data_management/update/system 重启调用，repository backup/migration/secret/simple 初始化残留，setup、cmd 三个命令、pkg/sysutil 的最后入口。

**子步骤**：

- S14.1：backup 完整模块及 dump/S3/local Adapter，保持运行时凭据读取、数据重维护锁、导出/恢复和旧 data management 响应。
- S14.2：setup 精简装配、自动初始化、密钥补齐、迁移与 simple 默认数据。避免 setup 先构造全部业务 worker；已有初始化锁与校验顺序保留。核对 3.3 中的初始化/恢复写入，只保留有明确入口与作用域的必要权限，不改写已发布 SQL。
- S14.3：清理命令与 JWT 命令只装配所需能力；重启请求通过 lifecycle 执行，不由 handler/os.Exit 绕过 Cleanup。命令参数、二进制名和输出兼容。

**验收**：空库初始化、已配置启动、自动设置、simple、迁移 advisory lock、初始化失败释放、备份/恢复的受控环境演练、Linux/Darwin 差异与手动重启请求的响应/关闭顺序。

### S15：HTTP 注册、DTO 和设置聚合收尾

**处理旧包**：handler/admin/dto/quotaview、server/routes、server/middleware 的残留、setting 聚合残留、web 的旧 service 依赖。

1. 检查每条既有路由都由目标模块注册，server 只汇总和挂全局 middleware。对相同 URL/method 的注册、裸路径别名、embed SPA fallback 和 `/models` 内容协商做契约对照。
2. 删除全局 Handlers/AdminHandlers 与 dto/mappers 聚合；跨模块页面只消费明确查询/配置 Interface，普通/管理员输出字段仍分开。
3. 完成 settings 对校验/应用能力的注册，移除持有所有模块具体 Service 的结构。业务更新失败不得写成“设置成功”。
4. 清空错误 HTTPCode 转接、剩余 Gin Context 业务依赖、两处 middleware/util 重复入口。收尾 S01 起已生效的 depguard，删除不再需要的旧路径规则与过渡例外，并修复文档路由/锚点；新模块的规则不能拖到这里首次添加。

**验收**：路由和 JSON/错误契约、认证/权限/审计顺序、公开设置/CSP、embed/noembed、设置热更新；旧 handler/domain/model 最后引用有精确清单，不能用全局 alias 文件拖延。

### S16：删除旧包与全量验收

**处理范围**：所有旧包残留、legacybridge、测试工具/跨模块测试、Ent 源引用、Makefile/CI/scripts、文档和代码锚点。

1. 按第 4 节逐行验证归宿。删除 service/repository/handler/admin/dto/domain/model/旧 middleware/util/platform 与 routes 空壳；pkg 只保留约定的通用包，platform/liveattestation 已归 OpenAI。
2. `app/legacybridge` 清零；所有旧类型 alias/薄转发都有实际消费者迁移证明后删除，禁止复制旧实现到新目录后继续双维护。
3. 移动 integration 到 tests/integration，业务 testutil 随模块，通用设施保留；同步 e2e 脚本、Makefile、CI 与 testdata 的相对路径，保留构建标签。
4. 检查 Ent schema 对旧 domain/service 的引用并按需生成；无 schema 行为变化时不产生数据库迁移。保留所有已有 SQL 文件 checksum。
5. 运行第 7 节全量门禁，核对正常/简化/初始化模式和平台构建；同步当前 Project Doc 的新所有者、架构图与路径。
6. 按 3.3 的资金写入清单逐项复查代码，确认旧运行态写入已销项、初始化/恢复等长期例外有明确范围。最终报告实际完成模块、删除的过渡代码、测试结果、仍然存在的有理由的跨模块事务依赖和运维限制。

**验收**：达到第 8 节全部完成标准后才把本方案标记为已实施。执行困难或上下文不足时记录未完成项，下次继续；不能因为停止本次任务就把整个阶段勾选完成。

## 7. 每次实施的方法与验收

### 7.1 阶段子计划与实施流程

所有后端包重构计划统一放在仓库根目录的 `refactor/` 下：

```text
refactor/
├── BACKEND_PACKAGE_TARGET.md   总目标、包映射、阶段安排与 roadmap
├── STAGE_PLAN_TEMPLATE.md      阶段子计划模板
├── S00-baseline.md             示例命名，进入 S00 计划模式时才创建
└── S01-foundation.md           示例命名，进入 S01 计划模式时才创建
```

阶段子计划使用 `Sxx-<主题>.md` 命名，如 `S05-identity-team-apikey.md`；路径在 roadmap 中登记，文件未创建前使用 `—`，不放失效链接。续做阶段时读取已有子计划和末尾执行记录，不重新生成一份互相矛盾的计划。

**开始阶段前**：使用计划模式，基于最新 HEAD、当前代码、总计划和已完成阶段的结果，生成完整的当前阶段迁移计划。参考 [阶段子计划模板](STAGE_PLAN_TEMPLATE.md)，明确本阶段的实际包/文件/符号映射、依赖、尚待决策的问题与结论、事务和缓存边界、过渡代码退出条件、子步骤及验收/回退方式。影响本阶段实施的决策应在子计划中解决；无关后续阶段的问题记录归属阶段即可。

计划模式形成的完整阶段计划必须在开始实施前原样保存到独立文件，不以摘要或模板占位替代；本次重构按用户约定统一使用 `refactor/`，不再另存到 `.agents/plans/`。保存后保留计划正文，执行进度和调整理由追加到文件末尾。若范围变化需要重新规划，回到计划模式形成新的完整版本（如 `S05-identity-team-apikey-v2.md`），保留旧版并更新 roadmap 的有效计划链接。

总计划中的接口形状和阶段内方案是子计划的设计输入，遇到不完整或冲突时以代码证据在子计划中明确结论。以下已识别事项留到对应阶段决策：

| 阶段 | 子计划需要明确的事项 |
| --- | --- |
| S04—S06 | `AdminService` 的接口、输入类型、共享接收者与构造器如何拆分；兑换、用户/Key、分组/账号/代理操作分别何时迁移，旧管理入口如何逐步退出。 |
| S05—S06 | 成员移除与 Key 禁用、用户专属分组替换等非资金跨模块写入的事务所有者、参与接口、过渡方式、失败回滚与缓存失效。 |
| S02、S14 | setup、app、cmd 的调用与装配方向；S02 明确可持续迁移的边界，S14 细化并完成精简初始化链。 |

这些事项不要求现在确定实现。子计划不得静默改变第 1.2 节的行为契约；如果结论改变总目标、包映射或阶段依赖，同步修订总计划的相关条目，并在子计划末尾记录理由和后续影响。总计划不重复保存阶段执行日志。

**实施阶段时**，按已保存子计划中的可交付子步骤推进：

1. **确认范围**：先读/复用 project-doc 路由到的相关契约，记录源包/文件、调用者和本次需要改变的 Interface。仅声明“迁账号包”仍太大，应缩小到例如“账号凭据持久化与刷新入口”。
2. **先设计调用形状**：写出调用前后输入、输出、失败与顺序。检验调用方是否少知道了内部细节；若只是套一层长参数构造器，重新设计。
3. **建立替换位置**：纯计算直接提取；有外部差异/I/O 才定义 Seam。预先明确生产和测试如何替换，不为了数量增加浅层 interface；确认新包由 S01 的哪条 depguard 规则覆盖，新增路径与必要例外同批更新。
4. **移动并解耦**：先完成可编译的机械移动，再单独收紧职责/替换实现。大型步骤用独立 Conventional Commit 分开“移位置”和“改协作方式”，避免一个 diff 同时混淆两者。
5. **接入唯一运行路径**：更新 app/Wire、所有直接调用者及测试。同一能力不能继续在新旧包各有一份 Implementation；临时保留的旧入口只转发到新实现。
6. **验证与删除**：按 7.3 运行受影响包及直接调用者的普通、unit 和适用 integration 测试，并在对应标签下运行 depguard；并发变更增加适用标签的 race 检查。清理不再使用的包装、旧测试和私有暴露，迁移旧测试的语义，不机械保留只验证转发次数或私有结构的测试。
7. **更新文档和进度**：更新受影响 Project Doc 的真实状态与代码锚点，把本次完成内容、提交/验证证据、兼容 Adapter、未跑项及下一步骤追加到当前阶段子计划末尾；总计划只更新 roadmap 的状态与链接。阶段全部子步骤和必要验收通过后才标为已完成，生成或保存计划不等于完成迁移。

### 7.2 新旧共存的三种许可方式

| 方式 | 适用条件 | 退出条件 |
| --- | --- | --- |
| 旧入口调用新实现 | 新 Module 不需要旧包；旧调用者暂时未迁 | 最后一个旧消费者与测试迁完即删除。 |
| app/legacybridge 实现新模块的依赖接口 | 下游能力尚未到迁移阶段，需要调用旧 Implementation | 对应依赖迁完时改为 app/bridges 或直接绑定，并删除 legacybridge 文件。 |
| 同一闭合事务内的过渡 Adapter | 财务/任务投影必须原子更新，不能拆事务 | 两侧目标模块就绪并完成真实事务回归后替换；不得留下重复资金逻辑。 |

禁用方式：新核心 import 旧 service；新代码 import app；为解决循环复制实体；通过 `any`/map/反射隐藏业务依赖；持久化双写但没有一致性方案；发布后随机选择新旧写入实现；构造器注入一个包含所有依赖的全局 ServiceContainer。

每个临时 Adapter 必须在进度中列出：源/目标、剩余消费者、删除阶段。新旧共享内存状态时必须唯一实例，不能因为构造两次缓存/限流器而改变行为。

### 7.3 测试范围与执行命令

以下命令是未来实施步骤的验收入口，编写本文时不宣称已经运行这些代码测试。具体模块路径按已存在的新包替换，并加入本次直接调用者；不对尚未创建的包执行命令。

每个子步骤都要按构建标签选择测试，不能只在大阶段结束时补跑。审查基线中，service 有 332 个测试文件受 `unit` 标签控制，repository 有 79 个受 `integration` 标签控制；普通 `go test` 与无标签 race 都不会执行它们。普通、unit、integration 分别运行，不能默认合并标签以免改变 `!unit` 等文件的编译集合。

| 本次变更 | 子步骤必须覆盖的验证 |
| --- | --- |
| 任意包迁移/代码重构 | 新包与直接调用者的普通测试、unit 测试和对应 lint；核对迁移前后的关键测试实际入选。 |
| PostgreSQL/Redis/事务/缓存 Adapter 或其调用关系变化 | 上述检查 + 受影响 integration 测试；资金必须包含外层事务回滚，不以 mock 替代。 |
| 并发、队列、缓存、取消与释放变化 | 普通及 unit 集合的针对性 race；涉及真实存储竞争时对相关 integration 场景也运行 race。 |
| 路由/鉴权/协议或流生命周期变化 | 模块测试 + 对应请求链契约测试；已有 e2e 场景受影响时在该子步骤执行，不等 S16。 |
| 新目录、同目录新增文件或 depguard 规则变化 | 检查目标文件确实命中规则；对保留路径同时验证“已登记旧依赖可通过、同目录新增文件的非法依赖和旧文件新增禁止依赖被拒绝”；在受影响的普通/unit/integration 集合下 lint。 |

```bash
# 当前子步骤：在 backend 下运行；用真实包名替换占位符，并加入直接调用者
go test ./internal/<module>/...
go test -tags=unit ./internal/<module>/...

# 涉及存储/缓存/事务时，在准备好真实测试依赖后执行
go test -tags=integration ./internal/<module>/...

# 同时覆盖旧桥接消费者（仅迁移期间仍存在这些包时）
go test ./internal/service/... ./internal/handler/... ./internal/repository/...
go test -tags=unit ./internal/service/... ./internal/handler/... ./internal/repository/...

# 旧仓储或它与新模块的事务/缓存协作受影响时执行
go test -tags=integration ./internal/repository/...

# 每个子步骤检查已启用的新路径规则；范围也加入本次直接调用者
golangci-lint run ./internal/<module>/...
golangci-lint run --build-tags=unit ./internal/<module>/...

# 涉及 integration 集合时执行对应标签的 lint
golangci-lint run --build-tags=integration ./internal/<module>/...

# 依赖注入修改时，保持现有生成入口
go generate ./cmd/server

# Ent schema 或其被引用类型确实变化时再生成
go generate ./ent

# 可构建性：普通服务；嵌入构建需先准备正常前端产物
go build ./cmd/server
go build -tags=embed ./cmd/server

# 并发/队列/缓存迁移的针对性竞争测试
go test -race ./internal/<module>/...
go test -race -tags=unit ./internal/<module>/...

# 真实存储并发语义受影响时，缩小到相关场景后执行
go test -race -tags=integration ./internal/<module>/... -run '<相关测试名>'
```

完整阶段/S16 在仓库根使用已有入口：

```bash
make -C backend test-unit
make -C backend test-integration
make -C backend test
make -C backend test-e2e-local
git diff --check
```

- `test` 包含普通 `go test ./...` 与无标签 golangci-lint，不能替代子步骤中对应标签的检查。unit/integration/e2e 标签不能因为移动文件被丢弃；测试只在特定标签下编译的旧 testutil 不得被普通生产包引用。
- 通过 `go test -list .` 配合相同的 `-tags`/包范围核对关键测试入选；若显示 `[no test files]`、没有匹配的测试或测试被环境条件 Skip，不能把它作为已覆盖的证据。范围不变且已确认入选后无需重复清点。
- integration/e2e 需要实际 PostgreSQL/Redis/服务依赖，缺少环境应明确记录未运行及补验条件，不能用 mock 结果声称锁/事务正确。必要验证未完成的子步骤保持待验，可以继续不依赖该结果的工作。
- `test-e2e-local` 当前指向 `internal/integration`，S16 移动后必须同步成 `tests/integration`；`scripts/e2e-test.sh` 和 CI 的直接路径也一起改。
- embed 构建依赖 frontend 构建产物。使用仓库既有完整构建路径准备产物，不能提交占位 dist 来骗过编译。
- 协议目录/DTO/公开配置变化影响前端时，运行 `make test-frontend` 及相关 Vitest；需要时运行前端 build。纯 Go 路径移动且 API 不变不需要改前端。
- Darwin 专属 liveattestation 必须覆盖对应 OS 构建；Linux/non-Darwin 的不支持行为另验。只进行跨平台编译不能声称平台运行行为已验证。
- 不把当前不存在的 datamanagement 源树测试纳入必过门禁，不为了 plan 改现有工具链。

### 7.4 行为、性能与结构的联合验收

| 类别 | 每次需要的证据 |
| --- | --- |
| Interface 行为 | 输入/输出、错误、权限、取消与部分成功，通过新 Interface 测试。 |
| 原子性与幂等 | 关键跨表动作真实事务回滚/并发/重复提交；缓存更新不能掩盖数据库失败。 |
| 流与会话 | 首个真实输出、前导事件、结束/取消、工具 ID、部分 usage、跨 turn/账号约束。 |
| 性能 | 固定脱敏夹具与相同配置对照；关注数据库/Redis 请求次数、TTFT、分配、队列长度和 goroutine。只有观察到回归或热路径结构显著变化时扩展 benchmark，不无差别重复全测。 |
| 依赖结构 | 新核心不 import 旧大包/具体 Adapter；Adapter 单向；没有新增包循环或全局状态。 |
| 运维兼容 | 路由、配置、环境变量、幂等键、缓存格式、迁移 checksum、启停/失败清理。 |

不以代码行数、目录数量、interface 数或单元测试数量衡量成功。能够删除多少调用方重复规则、一次业务修改需要跨多少模块、关键行为能否从稳定 Interface 验证，才是有效证据。

### 7.5 回退与上游同步

- 纯移动/内部重构默认不改数据库和 Redis 格式，可通过经过审查的代码 revert 回退；不得用 `git reset --hard` 清掉用户工作区，也不能声称任何中间版本都可无条件部署。
- 如果子步骤必须改持久化或缓存契约，先给出旧版本读新数据、新版本读旧数据、混部以及回退支持范围；SQL 只新增前向迁移。迁移文件已应用后不改写，不把回退代码等同数据库降级。
- 同一上游/资金/任务能力只启用一条写入和执行路径。需要比较时用只读规则或脱敏夹具差分，不能双发外部推理、付款、退款、邮件或扣款。
- 持续同步 upstream 时，旧文件位置变化会降低自动合并效果。每个迁移提交在对应阶段子计划的执行记录中保留“旧路径/符号 → 新模块”的定位；上游修改应根据语义移植到新所有者，不把旧 service 包重新加回来。
- 上游新增 SQL 按 fork 最新迁移 ID 递增重编号；README 工程知识归 docs。`SYNC.md` 仍不提交。
- 在 git 历史中保留清晰的移动提交与行为重构提交，便于追踪、上游比对和回退。不要顺手全仓格式化、改品牌标识或改无关函数名。

### 7.6 Project Doc 的维护

总计划和阶段子计划描述待实施方案，不提前把目标结构写成 Project Doc 的“当前架构”。本次计划整理只维护 `refactor/`，不修改 AGENTS.md 或文档库；实施时由用户明确文档位置，再按真实代码变更维护对应文档。当前可参考的文档路由：

- [系统架构](../docs/architecture/system_architecture.md)：装配、模块依赖和 lifecycle。
- [网关生命周期](../docs/architecture/gateway_request_lifecycle.md)、[调度与缓存](../docs/architecture/account_scheduling_and_cache.md)：请求顺序、重试、快照和多实例一致性。
- [统一协议能力](../docs/interfaces/protocol_capabilities.md)、[上游能力矩阵](../docs/interfaces/upstream_account_matrix.md)：协议目录与平台支持；原生转换以各平台专题为准。
- [身份与租户](../docs/domains/identity_and_tenancy.md)、[路由与结算](../docs/domains/routing_and_billing.md)、[支付与权益](../docs/domains/payments_and_entitlements.md)：授权与原子状态规则。
- [创作台](../docs/domains/creative_studio.md)、[批量图片](../docs/domains/batch_image_jobs.md)：任务/资金/素材生命周期。
- [开发流程](../docs/operations/development_workflow.md)、[部署迁移](../docs/operations/deployment_and_migrations.md)：生成、测试、迁移与发布入口。

移动代码时同步普通路径引用与 `@project-doc` 锚点；章节语义未变则保留稳定 ID。架构决策被实施并成为持久事实时，按 project-doc 协议维护已有架构文档或必要 ADR，不为每次机械搬文件单独生成决策文档。

## 8. 最终完成标准

- [ ] 第 4 节 112 个旧目录全部核对；新增包/文件增量也已登记，迁移/合并/保留均有实际结果。
- [ ] service/repository/handler/admin/dto/domain/model/第二处 middleware/util/旧 platform/routes 不再作为运行依赖；允许保留的目录只具有约定职责。
- [ ] app/legacybridge、旧类型 alias、旧函数转接和临时财务任务耦合全部清理。
- [ ] 每个业务模块拥有自己的用例、数据投影、必要 Adapter 和 Interface 测试；不存在把旧大包整体改名的模块。
- [ ] 核心不依赖 Gin/Ent/Redis/SQL/具体 provider；protocol 不依赖业务和 I/O；depguard 覆盖新路径和允许的精确例外。
- [ ] 新包及已有目标目录中新增文件从首次引入即受 depguard 检查；原路径旧文件的临时例外按所属子步骤清除，普通及适用构建标签均有实际命中证据，S15 只清理剩余过渡规则。
- [ ] 资金、退款、团队所有权和任务投影原子性保持；没有因包拆分新增半提交状态或无保证事件。
- [ ] 第 3.3 节资金写入逐项销项，外层事务回滚已验证；长期初始化/恢复权限有明确操作范围，不残留未登记的旧资金入口。
- [ ] Key、routing、scheduler、upstream、gateway、billing、usage 各自职责明确，重复的准入/选路/结算实现已移除。
- [ ] S09.1 的非流/SSE 完整链先于其余平台通过，Qoder 断开后用量收集及完成释放有回归证据；后续接口扩展保持已迁调用者兼容，并按各平台既有策略回归生命周期场景。
- [ ] 全部 HTTP/协议/配置/缓存/数据库契约、standard/simple/setup/embed 与生命周期验证完成；环境限制明确列出且不冒充通过。
- [ ] Ent/SQL 迁移和构建生成流程保持有效；手写源与生成物一致；没有改写已发布迁移。
- [ ] 测试目录、构建标签、CI/脚本路径、Project Doc 与锚点完成同步。
- [ ] 每个子步骤记录测试标签、关键测试入选及结果；必要测试未因缺标签、无匹配或环境 Skip 被误报通过。
- [ ] 各阶段均有持久化子计划、验收证据及可审查的 Conventional Commit 或用户要求的等价变更记录；不提交 SYNC.md、其它任务的临时计划或无关文件。

## 9. Roadmap

当前 **6 / 17 个阶段完成**。S05 已完成；下一步编写 S06 子计划。

状态使用：未实施、规划中、计划就绪、实施中、待验、已完成。详细决策、执行记录和完成证据保存在子计划；文件创建后再补链接，必要验收通过后才更新为已完成。

| 阶段 | 范围 | 状态 | 子计划 |
| --- | --- | --- | --- |
| S00 | 基线与迁移清点 | 已完成 | [S00-baseline.md](S00-baseline.md) |
| S01 | 依赖门禁与通用基础 | 已完成 | [S01-foundation.md](S01-foundation.md) |
| S02 | app、生命周期与公告试点 | 已完成 | [阶段计划与执行记录](S02-app-lifecycle.md) |
| S03 | 协议、能力与纯定价 | 已完成 | [S03 子计划](S03-protocol-capability-pricing.md) |
| S04 | billing 与资金事务 | 已完成 | [S04 子计划](S04-billing-transactions.md) |
| S05 | identity、team、apikey | 已完成 | [阶段计划与完成证据](S05-identity-team-apikey.md) |
| S06 | egress、routing、account | 未实施 | — |
| S07 | scheduler 与 Lease | 未实施 | — |
| S08 | usage、audit、ops | 未实施 | — |
| S09 | 首条请求链与上游迁移 | 未实施 | — |
| S10 | 通知、站点、审核、搜索 | 未实施 | — |
| S11 | gateway 协议与传输收敛 | 未实施 | — |
| S12 | promotion、payment | 未实施 | — |
| S13 | creative、batchimage | 未实施 | — |
| S14 | backup、setup、维护入口 | 未实施 | — |
| S15 | HTTP、DTO、设置聚合收尾 | 未实施 | — |
| S16 | 旧包删除与全量验收 | 未实施 | — |
