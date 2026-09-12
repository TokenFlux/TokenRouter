# S05：迁移用户身份、团队与 API Key

## 1. 基线与实施约定

以当前 `main`、HEAD `ed5380db2fa48f173746315679d6a2238208cb7a` 为起点，完成 S05.0—S05.4。保持 HTTP、认证凭据、数据库、缓存、金额精度及 standard/simple 行为兼容。

实施前将本计划原样保存为 `refactor/S05-identity-team-apikey.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件；清单与脱敏证据保存到 `refactor/baseline/S05/`，不改写 S00—S04 冻结资料，不另存 `.agents/plans/`。

已确认：

- Go 1.27.0、golangci-lint 2.13.2、Docker 可用。
- 身份/团队/Key 定向 unit、认证 HTTP，以及 PostgreSQL 团队并发邀请、用户删除原子性、认证失效触发器测试通过。
- 保留 AGENTS.md、其他计划和 diagnostics；不自动提交、推送或切换分支。
- 本阶段只生成 Wire，不生成 Ent，不修改 SQL migration 或缓存协议。
- 按用户确认，相关历史缺陷先在旧 HEAD 保存复现，再做最小修复并独立记录行为变化。
- 保持现有 Redis 发布订阅和 outbox 能力，验证其兼容性；不扩大多实例部署支持范围，S04 平台额度协调继续按单实例实现。

## 2. 模块接口与事务边界

### 模块所有权

| 模块 | 本阶段拥有的能力 | 对外协作方式 |
| --- | --- | --- |
| identity | 用户资料与状态、登录身份、注册/绑定/接纳、密码、JWT/refresh 会话、TOTP/Passkey、用户属性和通知邮箱管理 | 输出验证后的 `Principal` 和只读用户投影；跨模块能力通过消费者接口注入 |
| team | 团队、成员、邀请、所有权转移、资源归属及成员配置 | 输出团队状态、付款 owner、行为成员及成员生命周期投影 |
| apikey | Key CRUD、管理操作、复合映射、访问策略、IP 裁决、认证快照、缓存和失效 outbox | `Authenticate` 输出 `AccessSnapshot`；资金检查委托 billing |
| billing | 余额、订阅权益、Key/成员消费累计及资金窗口 | 提供注册赠送、兑换并发参与之外的资金操作，以及消费重置能力 |
| 各模块 Adapter | PostgreSQL/Redis、外部认证与 captcha、HTTP/DTO | 核心不接收 Gin、完整 config、Ent/SQL 事务或旧 service 实体 |

新核心不相互持有具体 Service。静态 Options、时钟和日期对象由 app 投影；运行时认证、注册和 provider 设置通过读取接口取得，保留当前动态生效时机。

`Principal` 只表达验证后的用户、角色、会话及凭据种类；付款、额度和分组策略不混入安全身份。`AccessSnapshot` 明确区分 Key 所有者、付款用户、行为成员、团队和访问策略，资金来源由 billing 单独解析。

旧 `User` 与 `APIKey` 存在递归关联，不整体互相别名。新模型使用消费者所需的只读投影；旧形状通过转换兼容。可直接兼容的值类型、错误使用别名，其余旧入口只投影和委托，不保留第二份规则或缓存。

### 跨模块事务

核心使用命名明确的闭合操作和事务内能力接口；实际事务由 PostgreSQL Adapter 管理。app 绑定参与工厂，只有 Adapter 接收现有 `*ent.Tx` 或 `*sql.Tx`。沿用原 Ent context，不新增事务 key；参与方法不得自行提交、回滚或发布成功失效。

| 操作 | 事务拥有者与参与方式 |
| --- | --- |
| 注册、身份绑定、接纳及首次赠送 | identity 拥有原有流程边界；billing 的余额、邀请码和订阅操作参与相同事务；保留原有补偿与 savepoint |
| 删除用户及其 Key | identity 拥有闭合删除；apikey 参与墓碑和 Key 删除，身份关联清理及数据库约束保持 |
| 成员移除、重新加入、解散及 owner 转移 | team 保留 SQL 事务与隔离级别；Key 生命周期写入由 apikey 的同连接参与方法完成 |
| 用户专属分组替换 | identity 管理用例拥有“授予新权限 → 迁移 Key → 移除旧权限”；apikey 参与原 Ent 事务 |
| 管理员给 Key 绑定专属分组 | apikey 管理用例拥有事务，identity 提供分组授权参与能力 |
| 兑换并发数 | billing 继续拥有兑换事务；用户并发字段由 identity 参与实现写入 |
| Key/成员消费重置 | apikey/team 决定权限和操作意图，billing 写消费字段；与同请求配置修改保持原子性 |

提交后失效、通知、返利及审计保持各入口原顺序和失败语义。现有数据库触发器继续生成认证 outbox，不额外重复入队，也不把尽力通知升级为事务成功条件。

## 3. 实施步骤

### S05.0：冻结输入与拆分清单

记录 HEAD、索引、工作区、工具版本、源文件摘要、SQL checksum、Ent/Wire 摘要和 S04 完整验证结果。

逐文件、逐符号登记目标、消费者、构建标签、测试、Wire、文档锚点及兼容入口退出阶段。特别覆盖：

- OAuth handler 中的外部 HTTP、身份校验、事务、pending 消费和令牌签发。
- AdminService 的用户、Key、专属分组替换与尚未迁移的分组/账号操作。
- `User`、`APIKey`、认证缓存中的身份、资金、团队、路由和展示字段。
- 用户创建、首次绑定、兑换并发、Key/成员配置、累计和重置的写权限。
- API Key L1/L2、订阅、outbox worker、活动时间节流及其他共享状态的生产实例和生命周期。

更新十二组资金账本中 S05 对应条目。上游请求指纹 `IdentityService`、供应商 `OAuthService`、账号消息队列和上游 session 不属于用户身份，继续留所属后续阶段。

### S05.1：identity 完整纵向迁移

**资料、安全状态与管理**

- 迁移用户资料、密码、状态、头像、属性、身份摘要、通知邮箱验证和管理用例；用户 HTTP/管理员 DTO 分开，保持敏感字段、省略/清空、已删除用户和排序语义。
- 普通资料更新只写明确字段，不回写余额、冻结余额、累计充值或并发消费快照。管理员调账委托 billing，保留调整记录、返利和缓存的原后置行为。
- 从 AdminService 提取用户管理接口及输入类型。旧聚合接口为未迁消费者委托新实例；分组、账号、代理管理仍留 S06 及所属阶段。
- 用户列表中的订阅、最后活动和用量排序保持现有查询顺序与分页，不能改成分页后补排序。暂存的跨表只读 SQL 单列 S08 退出项。

**登录、注册与身份绑定**

- 将七类身份及 pending/adoption 值类型迁入 identity。HTTP handler 中的创建、接纳、绑定、解绑、首次赠送、事务和补偿进入 identity 用例及 PostgreSQL Adapter。
- 保留 provider 三元组唯一性、邮箱及 alias 规范化、注册域名额度锁、事务内设置复查、最后可登录身份保护和解绑后撤销范围。
- 保留各 OAuth 流程已有的事务划分、补偿删除、默认赠送和 fail-open savepoint；不统一改造成一条更大的事务。失败不能遗留已成功绑定但账户创建失败的半状态。
- 注册及首次绑定余额通过 billing 的命名明确操作写入，保持其与普通充值、累计充值的差别；默认订阅和邀请码消费使用同事务参与接口。并发数写入归 identity，平台额度默认值调用 billing。
- 通知、Promo、返利、运行时设置等未迁能力由 app/legacybridge 转接；桥接只调用和投影，规则仍由实际所属用例执行。

**会话、强认证与 provider**

- JWT 签发/验证、token version 派生、refresh family、撤销和绑定摘要进入 identity；保留声明、算法限制、TTL、Redis key、序列化和旧 token 缺少绑定字段时的兼容行为。
- TOTP 保留加密格式、挑战 TTL、尝试次数和 step-up grant；Passkey 保留 RP、Origin、用户验证、challenge 和凭据更新规则。
- Passkey 核心不接收 `*http.Request`。通过验证端口复用现有 WebAuthn SDK 的 body 解析与验证能力，保持“消费会话 → 解析/验证”的顺序；损坏响应后的重放行为不得因提前解析而改变。
- 外部 OAuth 客户端、Google ID Token 验证及三个 captcha 客户端进入 identity/provider；HTTP Adapter 保留 cookie、state、callback、redirect 和响应。Google One Tap 继续使用官方验证器及现有严格声明检查。
- captcha 互斥、动作覆盖及故障关闭保持；不增加 GitHub/Google 自助绑定等新入口。
- `jwtgen` 使用精简装配取得用户查询和令牌签发能力，保持参数与输出，不构造完整认证图或后台 worker。

### S05.2：team 与资源归属

- 迁移团队创建、成员、邀请、预览/接受/拒绝、重新发送、所有权转移、强制转移、暂停和解散，以及用户/管理员 HTTP。
- 保留邀请邮箱和 token 摘要、到期边界、成员上限、唯一活动团队/owner、锁顺序和原 SQL 隔离级别。
- 成员离开后重新加入不恢复旧生命周期 Key；owner 独立禁用标记不能由普通成员解除。
- 团队用例决定资源权限，Key 列表/启停/删除调用 apikey 能力；原先与成员状态同事务的 Key 写入继续使用事务参与接口。
- 成员限额配置归 team，消费累计和窗口重置归 billing。保持现有日/周/月日历边界，普通限额修改不得覆盖消费字段。
- 团队用量展示保留查询和过滤语义，作为只读投影单列 S08 退出项，不扩大用量领域迁移。
- 邮件、设置与缓存失效改用窄接口，保留邀请已持久化但邮件失败时的原行为。

### S05.3：apikey、认证缓存与访问规则

**生命周期和认证**

- 迁移普通/托管 Key、创建数量锁、名称与自定义 Key 校验、复合映射、资金绑定配置、IP 规则、删除墓碑和管理用例。
- 管理员 Key 分组修改与消费重置退出旧 AdminService；用户专属分组替换按前述 identity 事务接口接入。
- `Authenticate` 校验 Key、当前用户及团队生命周期，返回独立 `AccessSnapshot`。过期/额度耗尽与禁用保持区别，查询已有用量和任务的入口仍可跳过消费检查。
- Key 可选分组使用用户授权、团队付款主体和订阅范围的只读投影；保留管理员绑定与用户绑定的不同校验规则。
- 将 S01 留下的 IP 黑白名单裁决归入 apikey，复用 ipmatch；无效白名单仍拒绝。可信代理解析继续由 server/clientip 拥有。

**规则与网关衔接**

- apikey 拥有复合前缀、模型映射配置校验和别名展示规则；抽取 `routing/modelmap` 作为唯一纯匹配实现，旧 Account 和 Key 方法共同委托，保留精确优先、最长尾通配和一跳行为。
- 请求体、multipart、Gemini URL、工具模型字段、响应恢复和追踪继续由网关适配层负责。通过接口组合到新认证入口，避免 apikey/httpapi 引用旧 service。
- 保留“复合选组 → Key 改写 → 渠道/账号映射”的顺序，以及 WS/Live 限制、默认分组和不可用分组回退后的再次授权。
- 分组/调度配置只投影认证及旧网关所需字段，不搬迁分组规则或构造第二份路由缓存；S06 改绑其来源。
- RPM 和并发准入仍留旧网关编排，保持资金检查后的执行顺序，登记 S06/S07/S11 退出。

**缓存与资金字段**

- 认证 L1/L2、负缓存、singleflight、回源并发槽、无效认证滥用限制、last-used 节流及 outbox worker 迁入唯一实现。
- 保持快照 v40、全部 JSON 字段、nil/空集合、缓存 key、TTL/jitter、发布订阅和延迟二次失效；旧缓存可由新代码读取，新代码不因迁包提升版本。
- 返回请求独立副本；复合选组、回退及模型处理不能污染缓存中的 map、slice 或嵌套策略。
- Key/成员累计与重置写入归 billing，旧累计入口委托或在消费者清零后删除。配置与重置同请求时使用同事务，保留状态恢复及失效顺序。
- app 统一绑定缓存和 worker 生命周期：构造不启动，停止新认领后等待在途操作，再关闭订阅/L1；持久化待重试和延迟 outbox 留待下次启动，不宣称已全部排空。

### S05.4：HTTP、装配、门禁与交接

- JWT、管理员认证、角色、session binding、step-up 进入 identity/httpapi；Key 凭据入口进入 apikey/httpapi；路由直接绑定新用户、团队、Key handler。
- 保留全部 URL、中间件顺序、Header 优先级、Google 与通用入口差异、WebSocket 子协议、cookie、错误 reason、幂等 helper、分页和字段边界。
- app 组合认证、旧网关请求策略和 billing 准入。Ops/审计通过窄观察接口接入；普通 Key 协议门禁不新增请求体读取。
- 认证主体以新类型为唯一来源，旧 context 读取入口临时投影；资源级授权继续由用例检查，管理员凭据不能替代真实 `sid` 的 step-up。
- 删除 S02 公告用户读取及 S04 billing 用户读取的旧桥接，改为 app 对 identity 的直接投影绑定；通知、推广、路由、用量及 Ops 的未迁桥接逐项登记退出阶段。
- 每批同步 depguard：覆盖三个核心、HTTP、PostgreSQL、Redis、provider、纯匹配叶子和装配。必要依赖精确到文件/import，迁出即删旧例外，保留原 service/handler 与 protocol 规则。
- 同步现有身份租户、HTTP、网关生命周期、复合 Key、模型重定向、路由计费和开发流程文档，描述实际新旧共存结构。

## 4. 验证安排

项目统一使用 `GOTOOLCHAIN=go1.27.0`。每批运行新包、旧转接及直接消费者的普通/unit 测试；事务、缓存、订阅和锁使用隔离 PostgreSQL/Redis。

| 验证面 | 必须取得的行为证据 |
| --- | --- |
| 注册与绑定 | 并发同邮箱/alias/域名、同一外部主体竞争、pending 重放、最后身份解绑、首次赠送幂等、邀请码失败、savepoint fail-open及补偿 |
| 会话安全 | token version、refresh 轮换/撤销范围、绑定异常、禁用/删除用户、TOTP、Passkey 损坏响应和真实验证、管理员 Key 与 step-up |
| 外部认证 | 七类 provider 的原流程差异、Google One Tap 声明、cookie/state/redirect、验证码互斥与故障关闭 |
| 团队事务 | 并发邀请上限、离开再加入、owner 转移、成员移除与 Key 禁用、Key 操作失败整体回滚、owner 不可直接删除 |
| 用户/Key管理 | 用户删除与墓碑原子性、专属分组替换回滚、创建数量并发、托管 Key 隐藏、普通修改不覆盖消费 |
| 网关兼容 | 通用/Google 凭据差异、IP、复合前缀、JSON/multipart/Gemini/工具模型、单跳映射、响应别名、WS 限制、simple/非消费入口 |
| 资金衔接 | 同连接参与、赠送与累计充值差别、兑换并发数、配置加重置原子性、指定订阅最终分组复查、S04 五项修复回归 |
| 缓存/生命周期 | v40 双向读取、快照隔离、双实例发布订阅兼容、触发器/outbox 回滚与重试、断线恢复、重复 Start/Stop、在途停止及 Redis 最后关闭 |
| 展示与装配 | 用户/管理员敏感字段、已删除用户、原分页排序、site/billing 投影、JWT 工具与完整应用唯一实例 |

外部认证使用本地 HTTP/JWKS/验证码夹具和真实验证库，不调用生产账号。未执行的真实供应商、硬件 Passkey 或外部 TLS 环境验证单独记录，不能把 mock、跳过或仅编译算作真实外部验证。

共享状态、快照和会话执行定向 race；PostgreSQL/Redis 竞争执行 integration race，不扩展全仓 race 或 benchmark。

depguard 使用可丢弃夹具验证合法方向、旧入口许可、同目录新增违规、旧文件新增禁止 import、迁出例外失效、核心反向依赖及正常 Adapter；覆盖普通/unit/integration，并核对 wireinject、embed、Darwin/Linux。保留诊断后删除夹具。

收尾分别、串行执行全量测试，避免 S04 已确认的 Ent schema loader 临时目录竞争：

```bash
# backend 目录
export GOTOOLCHAIN=go1.27.0

go generate ./cmd/server

go test -count=1 -json ./...
go test -count=1 -json -tags=unit ./...
go test -count=1 -json -tags=integration ./...

golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 ./...
golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 --build-tags=unit ./...
golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 --build-tags=integration ./...

make build
make -C .. test-frontend
make -C .. build-frontend
go build -tags=embed -o bin/server-embed ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/server-linux-amd64 ./cmd/server
```

另验证两个维护命令构建、Wire 再次生成无差异、真实前端产物的 embed 测试，以及 standard/simple 启动和 SIGTERM 认证资源停止顺序。通过 go list 和 JSON 事件确认关键测试实际执行。

S04 lint 基线 normal/unit/integration 为 **1/285/19**；按路径映射、规则和完整消息逐项比较。新增问题必须解决，既有问题独立归档，不扩大忽略规则。

## 5. 完成、交接与回退

完成须同时满足：

- identity、team、apikey 的生产用例、HTTP 和存储接入唯一实现，新核心不依赖旧业务、config、数据库或具体 Adapter。
- 非资金跨模块事务、注册赠送、兑换并发参与和 Key/成员资金字段写权限取得真实行为证据。
- 新主体与快照、旧入口、消费者、生命周期、构建条件和依赖例外均可追踪；旧 AdminService 不再保留已迁用户/Key规则。
- S05 相关历史缺陷已复现并最小修复；必要验证未完成时保持“待验”。
- SQL、Ent、S00—S04 冻结资料和其他任务文件无意外变化；Wire 差异可解释，新增及已有文件的 diff 检查通过。

完成后 roadmap 更新为 **6 / 17**、S05 已完成，下一步编写 S06 子计划。路由/账号、调度、用量/审计、供应商、通知、网关、推广支付、任务、初始化及兼容清理分别交接 S06—S16。

按子步骤回退代码、装配、规则和文档，恢复旧调用链；不涉及数据库或缓存格式降级。历史缺陷修复的回退风险单列记录。交付可审查差异，不自动提交，不提交 SYNC.md 或其他任务内容。


---

## 执行记录

### S05.0：实施输入冻结

- 2026-09-12T07:27:21.005207+08:00：原样保存计划，记录 main 与 HEAD ed5380db2；索引保持原状，保留其他任务文件。
- [工作区快照](baseline/S05/start.json) 保存 5404 个原始文件摘要；[初始清单](baseline/S05/initial-inventory.json.gz) 保存 217 个身份、团队、Key 及混合入口文件的声明、import、构建标签和锚点。逐项迁移归属在执行中继续补齐。
- [工具版本](baseline/S05/tools.json)已核对。首批提取邮箱/会话/属性/验证码/Passkey，兼容入口保持委托；[定向 unit](baseline/S05/identity-security-unit.result.json)通过。完整 S05 仍为实施中。

### 2026-09-12：认证缓存与 HTTP 认证增量

- 已迁移 API Key 主实现、L1/L2、singleflight、回源并发槽、无效认证限制、活动时间节流及 outbox worker；旧入口投影到同一实例。Key 配置仍使用原 Ent UPDATE，资金字段由 billing 在同一 builder 上写入。
- `apikey-unit-behavior-2.result.json` 记录 Key、团队、旧网关认证及 handler 定向 unit 通过；`identity-http-auth-unit.result.json` 记录 JWT、管理员认证、step-up 与绑定指纹迁移后的回归通过。
- 历史缺陷 S05-H01：原始 HEAD 的快照来源及物化结果共享嵌套 map/slice/指针，单个请求可污染同进程认证缓存。隔离原始源码新增相同回归测试后，34 个隔离子场景失败；新实现按类型复制，未更改 v40 字段或序列化格式。
- 历史缺陷 S05-H02：复合 Key 分组快照遗漏 `OpenAIFastPolicy`，`force_off` 与 `force_ultrafast` 经缓存后丢失。原始 HEAD 两个子场景失败；新实现补齐已有 v40 字段的赋值和还原。旧缓存中缺失的值仍按原缺省语义读取，重新回源后恢复显式策略。
- 旧 HEAD 复现：[`original-snapshot-regressions.result.json`](baseline/S05/original-snapshot-regressions.result.json) 与 JSON 测试事件；修复验证：[`snapshot-fixed-2.result.json`](baseline/S05/snapshot-fixed-2.result.json)。撤销 H01 将恢复请求间共享修改风险；撤销 H02 将恢复复合分组显式 Fast 策略丢失。
- 本阶段仍在实施，HTTP 用户/provider/管理事务与最终全量验证尚未完成；以上定向证据不等同于 S05 完成。

### 2026-09-12：身份 HTTP 与管理员事务增量

- 用户资料、通知邮箱、TOTP、Passkey 和属性管理迁入 `identity/httpapi`；JWT/管理员认证/会话绑定/step-up 使用同一新身份主体，原中间件为兼容转接。Passkey 仍在消费会话后由 SDK 解析 credential body。原 DTO 的递归展示通过泛型 DTO 参数保持，新身份实体不反向引用 API Key。
- 用户管理规则迁入 `identity.UserAdmin`；Key 分组管理及消费重置意图迁入 `apikey.Admin`。专属分组替换由 identity 的闭合 Ent 操作管理，Key 与身份授权分别通过显式 `*ent.Tx` 参与实现接入。尚待收尾的生产装配和管理员 HTTP 见后续记录。
- [`identity-handlers-unit.result.json`](baseline/S05/identity-handlers-unit.result.json)、[`identity-apikey-admin-unit-3.result.json`](baseline/S05/identity-apikey-admin-unit-3.result.json) 记录定向 unit 通过。旧 SQLite 用例只作为既有回归，不替代事务验收。
- [`admin-participants-integration-2.result.json`](baseline/S05/admin-participants-integration-2.result.json) 记录真实 PostgreSQL 验证：删除墓碑、分组迁移或 Key 配置写入后注入错误，关联用户/权限/Key/outbox 整体回滚；成功路径可提交。原外层用户删除、成员移除、兑换并发数与初始余额测试同时实际执行。
- `apikey.Authenticate` 输出独立 `AccessSnapshot`，分别记录 owner/payer/actor/team。通用与 Google 凭据入口迁入 `apikey/httpapi`；复合选组、请求模型改写与资金准入仍留旧网关后半段，顺序不变。[`apikey-access-http-unit-2.result.json`](baseline/S05/apikey-access-http-unit-2.result.json) 记录相关回归通过。

### 2026-09-12：Key HTTP 与外部验证器增量

- Key CRUD、分组选项和管理员用户 HTTP 已接入新 handler；旧名称保留类型/参数投影。分组展示与容量仍由原路由能力提供，Key DTO 不携带付款用户对象。相关验证见 [`apikey-handler-unit-2.result.json`](baseline/S05/apikey-handler-unit-2.result.json)、[`identity-admin-http-unit.result.json`](baseline/S05/identity-admin-http-unit.result.json)。
- LinuxDo/OIDC HTTP 客户端与解析、Google One Tap 官方验证器、钉钉 client/token 缓存及部门读取迁入 identity/provider；钉钉配置改变仍替换同一持有者中的 client。相关原有本地 HTTP/JWK/声明及回调回归通过，见 [`oauth-provider-unit-5.result.json`](baseline/S05/oauth-provider-unit-5.result.json)。不将这些夹具称为真实供应商验证。
- 历史缺陷 S05-H03：原管理员 Key HTTP 先独立重置，再校验/写分组，后一步失败会留下已提交重置。旧 HEAD 在真实 PostgreSQL 通过同一 PUT 请求复现，见 [`original-admin-key-reset.result.json`](baseline/S05/original-admin-key-reset.result.json)。新 `UpdateManagedFields` 校验后将两个字段集合合并为同一个 UPDATE；专属分组授权与合并 UPDATE 继续使用一个 Ent Tx。成功失效移至完整操作之后，避免失败请求部分生效；撤销修复会恢复该风险。unit 结果见 [`admin-key-atomic-unit-2.result.json`](baseline/S05/admin-key-atomic-unit-2.result.json)，数据库验收见后续记录。

### 2026-09-12：原子 Key 管理修复与 pending 存储增量

- H03 的真实 PostgreSQL 验收通过：非法分组失败不会清零；数据库触发器拒绝分组写入时，三个消费计数及窗口一并回滚；移除测试触发器后同一合法请求成功修改分组并重置窗口。见 [`admin-key-atomic-integration-2.result.json`](baseline/S05/admin-key-atomic-integration-2.result.json)。原 SQL migration 没有修改，触发器只属于隔离测试夹具并在测试后移除。
- GitHub/Google OAuth 邮箱客户端与微信 OAuth 客户端已迁入 identity/provider，保留验证顺序、超时、URL 和错误截断语义。完整相关定向 unit 通过，见 [`auth-all-provider-unit-2.result.json`](baseline/S05/auth-all-provider-unit-2.result.json)。
- pending 完成状态、接纳规则和纯值计算迁入 identity；原 Ent 绑定、身份合并、接纳/消费事务迁入 identity/postgres。旧 HTTP 仍有创建/补偿和事务编排待收敛，这一批没有宣布 OAuth 纵向迁移完成。见 [`pending-storage-unit-3.result.json`](baseline/S05/pending-storage-unit-3.result.json)。

### 2026-09-12：pending 核心流程与注册邀请码参与能力

- `identity.PendingFlow` 接管接纳选择与第二段账户绑定事务失败后的补偿；`PendingFlowDatabase` 仅接收所属核心实例与 Ent 连接，在原事务中完成绑定、默认权益及 pending 消费。失败阶段保留原响应与 cookie 清理差异。定向回归见 [`pending-flow-unit.result.json`](baseline/S05/pending-flow-unit.result.json)。
- [`pending-flow-integration.result.json`](baseline/S05/pending-flow-integration.result.json) 使用真实 PostgreSQL 验证：同事务可见绑定和消费、其他连接在提交前不可见；提交前注入失败后绑定和消费回滚，已创建用户按原流程补偿删除；成功路径正常提交。
- 注册邀请码消费与补偿字段写入迁入 billing/postgres，使用显式 `RegistrationInvitationsInTx` 参与工厂；普通兑换与注册邀请码保留原不同语义，不新增注册 usage 记录。兑换字段赋值共用 billing 唯一 builder。见 [`registration-invitation-unit.result.json`](baseline/S05/registration-invitation-unit.result.json)、[`registration-invitation-integration-3.result.json`](baseline/S05/registration-invitation-integration-3.result.json)。该集成测试前两次因新夹具超出既有兑换码长度限制失败，随后缩短测试值；生产校验和 schema 均未修改。
- 当前完整源码普通构建选择已通过包级编译核对，见 [`s05-all-packages-build.result.json`](baseline/S05/s05-all-packages-build.result.json)。这只是中途可构建性证据，收尾仍需实际全量测试、lint、Wire、前端及进程验证。

### 2026-09-12：零值兼容回归、OAuth HTTP 与身份装配增量

- 上一轮完整 handler unit 暴露两个 WebSocket EOF。日志定位到旧 `APIKeyService{}` 零值经新嵌入指针触发 panic；现恢复旧无仓储入口的 `get api key` 包装及未找到错误链，不创建第二份缓存。新增零值回归和两个 WS 原用例通过，见 [`ws-zero-value-regression.result.json`](baseline/S05/ws-zero-value-regression.result.json)。这是本阶段引入后修复的问题，不归入历史缺陷。
- 认证快照从分组倍率仓储取得的 RPM override 指针也纳入复制，避免来源对象后续修改影响缓存。v40 字段、序列化及 key 保持原状。
- OAuth 身份查询和微信身份/通道/旧 OpenID 查找迁入 `identity/postgres`；接口只返回身份投影，保留查询顺序和 provider 间不同的错误包装。见 [`oauth-lookup-unit-2.result.json`](baseline/S05/oauth-lookup-unit-2.result.json)。
- 登录、注册、密码重置、refresh、登出、当前用户 HTTP 迁入 `identity/httpapi.SessionHandler`，通用 pending 创建/绑定/交换迁入 `PendingHandler`。HTTP 不依赖 Ent 或旧 service；接纳、资格判断、注册失败补偿由 identity 用例负责。TOTP pending 仍先绑定、再独立消费，成功后才删除登录挑战。见 [`session-http-unit.result.json`](baseline/S05/session-http-unit.result.json)、[`pending-http-compensation-unit.result.json`](baseline/S05/pending-http-compensation-unit.result.json)。各供应商专用回调和剩余装配仍继续迁移，不宣布纵向链收尾完成。
- 钉钉企业资料同步、首次用户名更新和属性同步规则归入 identity；部门 HTTP 查询由 provider 通过窄接口提供。日志经观察端口保持，配置和后置异步调度尚在旧入口投影。见 [`dingtalk-sync-unit.result.json`](baseline/S05/dingtalk-sync-unit.result.json)。
- app 直接构造唯一 UserStore、身份认证图和资料用例；旧 UserRepository/AuthService/UserService 仅包装同一实例。公告和 billing 用户读取改为直接 identity 投影，删除 S02/S04 对应旧桥接。只生成 Wire，见 [`identity-users-wire.result.json`](baseline/S05/identity-users-wire.result.json)、[`identity-auth-wire-2.result.json`](baseline/S05/identity-auth-wire-2.result.json)。构造成功属于接线证据；完整启停与唯一状态的行为验证仍待收尾。

### 2026-09-12：完整受影响 unit 与 refresh 原子轮换修复

- [`s05-identity-team-key-full-unit-2.result.json`](baseline/S05/s05-identity-team-key-full-unit-2.result.json) 记录 identity/apikey/team、旧 service/handler、认证 middleware 与 app 全包 unit 实际通过。随后全普通构建集合的 depguard 检查通过，见 [`s05-depguard-audit-5.result.json`](baseline/S05/s05-depguard-audit-5.result.json)；新增许可只匹配实际源文件/import，保留父角色其余限制，并删除已退出的公告用户桥接许可。unit/integration 门禁及违规夹具仍待最终收尾。
- 历史缺陷 S05-H04：旧 refresh 流程读取凭据后忽略无条件 DEL 的消费结果，两个请求都可从同一 token 签发后继凭据；DEL 报错时也继续签发。原始 HEAD 使用真实 Redis 与受控读取屏障复现两个成功，并注入删除故障复现仍返回成功，见 [`original-refresh-rotation-regressions.result.json`](baseline/S05/original-refresh-rotation-regressions.result.json)。
- 新 `ConsumeRefreshToken` 在原所有校验完成后检查 Redis DEL 的返回数量：只有实际删除一条凭据才允许签发；已被消费返回原无效 token 错误，存储故障返回原服务不可用错误。登出清理仍使用幂等删除。未引入缓存 key、JSON、TTL 或 family 索引格式变化，不提前消费校验失败的凭据，不改变后继签发失败时旧凭据已经失效的语义。真实 Redis 验收见 [`refresh-rotation-fixed-integration.result.json`](baseline/S05/refresh-rotation-fixed-integration.result.json)。撤销此修复会恢复并发重复签发和存储故障期间凭据持续可用的风险。

### 2026-09-12：pending 事务消费竞态修复

- 历史缺陷 S05-H05：原事务内消费先读未使用状态，再无条件 UPDATE；真实 PostgreSQL 的两个独立事务经写入屏障交错后都成功提交。旧 HEAD 复现见 [`original-pending-consume-regression.result.json`](baseline/S05/original-pending-consume-regression.result.json)。测试仅通过 integration 适配导出旧私有函数，运行源码一致性及夹具摘要见 [`original-auth-reproduction-sources.json`](baseline/S05/original-auth-reproduction-sources.json)。
- 新 UPDATE 增加 `consumed_at IS NULL` 条件；竞争失败重新读取并返回已有的会话已使用错误。保留原到期、浏览器绑定判断和 Ent 事务拥有者，参与方法没有增加 Begin/Commit/Rollback。撤销会恢复并发重复消费风险。
- [`pending-consume-fixed-integration.result.json`](baseline/S05/pending-consume-fixed-integration.result.json) 记录修复后只有一个事务完成，同时重跑账户最终绑定/消费/补偿及注册邀请码同事务用例通过。refresh 修复后的身份/旧 HTTP 定向 unit 见 [`refresh-auth-unit.result.json`](baseline/S05/refresh-auth-unit.result.json)。定向 integration race 正在独立验证，不把编译结果计作行为通过。

### 2026-09-12：身份主体、Google HTTP 与精简命令增量

- 请求认证记录以 `Principal` 为身份来源，旧主体/角色读取只返回已固定的兼容投影。团队 Key 仍分别保留行为用户和付款用户；付款用户的管理员角色不赋予 Key 的安全身份。step-up 读取验证后的凭据类型和 `sid`，保留旧 JWT 无 sid 的摘要回退；旧字段不能把管理员 Key 改造成 JWT 会话。见 [`canonical-principal-unit.result.json`](baseline/S05/canonical-principal-unit.result.json)、[`canonical-principal-stepup-unit.result.json`](baseline/S05/canonical-principal-stepup-unit.result.json)。
- Google One Tap HTTP 迁入 identity/httpapi，严格声明验证通过 identity 端口使用官方 provider；已验证邮箱资料及 pending 草稿生成归 identity，原 cookie、限流前请求体边界和响应不变。见 [`google-one-tap-http-unit-2.result.json`](baseline/S05/google-one-tap-http-unit-2.result.json)。仍需完成其他供应商专用回调及最终 app HTTP 直接装配。
- `jwtgen` 通过 bootstrap 精简装配用户读取与访问 token 签发，使用既有数据库引导和同一连接关闭责任，不构造完整 AuthService、refresh 缓存或后台 worker。构建通过，见 [`jwtgen-minimal-build.result.json`](baseline/S05/jwtgen-minimal-build.result.json)；命令参数/实际输出的进程验证仍待收尾。
- refresh 和 pending 并发修复的真实 PostgreSQL/Redis 定向 race 通过，见 [`s05-session-pending-integration-race.result.json`](baseline/S05/s05-session-pending-integration-race.result.json)。这不是全仓 race，也不代表外部提供方或硬件认证验证。
- 团队用例、存储和邀请限流器改由 app 直接构造；用户读取直接投影 identity，Key 缓存失效保持删除 L2 后发布的旧顺序，邮件模板仍留通知能力。成员资金窗口的 Calendar 与团队时钟由 app 注入。仅生成 Wire，见 [`team-direct-wire.result.json`](baseline/S05/team-direct-wire.result.json)。

### 2026-09-12：认证资源停止等待增量

- Key 认证、last-used 与外部失效入口通过同一操作计数器停止新认领；`StopContext` 等待在途操作退出，再取消并等待订阅、关闭两个 L1。关闭预算耗尽只返回超时，不能提前清理仍在使用的缓存。Start/Stop 与订阅的并发调用已串行保护，缓存指针使用原子发布，构造仍不启动后台任务。
- outbox worker 停止新认领后取消工作 context 并等待当前批次退出；app 直接传入剩余关闭预算。未来重试、延迟事件与未完成租约仍保留给下次启动，不宣称持久化 outbox 已排空。
- 定向 race 覆盖构造无启动、重复/并发启停、阻塞存储、超时后缓存仍可用、在途完成后订阅/L1 才关闭，以及停止后不再认领。首次测试哨兵受 Ristretto 容量影响未被接纳，修正夹具容量并先断言实际接纳，再验证资源顺序；见 [`apikey-lifecycle-race-2.result.json`](baseline/S05/apikey-lifecycle-race-2.result.json)。后续仍需真实订阅/断线恢复与进程 SIGTERM 验证。
- 团队装配后的定向 unit 通过，见 [`team-direct-unit-2.result.json`](baseline/S05/team-direct-unit-2.result.json)。首次编译发现日期注入误用于自由函数，现将用量查询缺省日期提取为显式接收时刻的纯函数，旧入口委托并保持原取时点。

### 2026-09-12：LinuxDo/OIDC/邮箱 OAuth 与成员资金边界增量

- LinuxDo、OIDC、GitHub/Google 的 start、callback 和完成注册 HTTP 已接入 identity/httpapi。网络请求、OIDC 的 JWT/JWK 验证经身份端口调用 provider，HTTP 不引用 SDK JWT claims、旧 service/config 或 Ent。签名绑定 cookie、已验证资料、合成邮箱和各提供方的不同接纳草稿由身份契约与纯规则拥有。原有回调测试见 [`linuxdo-http-unit.result.json`](baseline/S05/linuxdo-http-unit.result.json)、[`oidc-http-unit.result.json`](baseline/S05/oidc-http-unit.result.json)、[`email-http-unit-2.result.json`](baseline/S05/email-http-unit-2.result.json)。微信与钉钉专用 HTTP 及最终直接装配仍继续实施。
- 历史缺陷 S05-H06：邮箱 OAuth 用户创建提交后，第二段绑定事务 Begin 失败未执行原补偿。旧 HEAD 使用真实 PostgreSQL，在用户创建后仅拒绝下一次 Begin，复现用户仍存在；见 [`original-email-oauth-begin-regression.result.json`](baseline/S05/original-email-oauth-begin-regression.result.json)。
- `FinalizeVerifiedAccount` 保留 Begin 后、绑定前的独立接纳写入顺序及两段事务；将已有补偿扩展到 Begin 失败。原邮箱流程的尽力补偿和失败响应保持，不把补偿失败改为新通知协议。验收见 [`email-oauth-begin-fixed-integration.result.json`](baseline/S05/email-oauth-begin-fixed-integration.result.json)。撤销修复将恢复绑定事务无法开始时遗留用户的风险。
- 成员日/周/月限额和读取窗口规则由 billing 的 `MemberQuotaSnapshot` 处理；apikey 决定 owner 豁免，team/postgres 只转换原字段，app 注入日期对象。未改变限额比较顺序、浮点值、窗口起点或持久化事务。见 [`member-quota-ownership-unit.result.json`](baseline/S05/member-quota-ownership-unit.result.json)、[`member-quota-calendar-unit.result.json`](baseline/S05/member-quota-calendar-unit.result.json)。
- 认证停止修改后的 Key、缓存、last-used 及旧 WebSocket 回归通过，见 [`apikey-after-lifecycle-unit.result.json`](baseline/S05/apikey-after-lifecycle-unit.result.json)。


### 2026-09-12：生产认证装配、真实会话验证及旧私有入口清理

- 微信和钉钉登录 HTTP 已迁入 identity/httpapi，保留微信完成注册后的独立消费顺序、钉钉企业内强校验/跨组织降级和注册豁免；旧入口仅委托。微信支付 OAuth 保留独立旧支付适配，退出 S12。
- app 构造唯一认证 HTTP、DingTalk 客户端槽、身份/Key 管理用例与同连接事务参与工厂；Key 存储/L1/L2/outbox、Passkey/TOTP/验证码及会话存储由 app 直接构造。旧模型转换继续服务未迁消费者。
- `s05-core-and-adapters-unit` 完整受影响包 unit 通过；`identity-http-production-unit-2` 路由与登录定向测试通过。`s05-real-auth-contracts-2` 取得真实 PostgreSQL/Redis 的注册、绑定、团队、刷新轮换、pending 消费、失效与 Passkey 行为证据。
- 新增软件 WebAuthn 认证器使用真实 ES256 签名与现有 SDK，验证注册、错误 Origin、篡改签名、消费先于解析、重放拒绝及签名计数更新；不视作真实硬件/浏览器验证。测试 harness 原命名空间未覆盖 GETDEL，已只修正测试命令前缀；第一次失败归档，未修改生产缓存协议。
- 真实 Redis 双实例认证订阅在连接被关闭后重新订阅并失效两边快照，Stop 完成后 Redis 仍可用；该证据只覆盖既有认证缓存协议，不扩大平台额度的单进程协调范围。
- 使用旧 HEAD 的真实 v40 类型生成完整及空集合 JSON 夹具；新包验证字段与形状一致。扫描新旧生产字符串后发现并恢复一处机械重命名误改的内部错误文本 `check key exists`。
- 全量 lint 发现失去生产消费者的私有兼容辅助；逐项删除无消费者声明，仅将仍被测试消费的转接移入相应构建条件的 `_test.go`。逐项处置见 `baseline/S05/private-entry-dispositions.json`；完整收尾验证仍在进行，S05 暂不标记完成。


### 2026-09-12：最后装配核对与时钟契约

- 核对完成标准时补齐身份和 Key 的显式时钟注入；app 传入 time.Now，旧构造兼容默认时钟。JWT 签发/库内验证、TOTP 原验证窗口、profile 活动时间/通知码、pending 状态、Key 过期与 outbox 均在原位置读取，不缓存启动时刻，不改变时区或预算计时。
- `clock-contracts-unit` 通过；新增契约核对固定时间下 JWT 过期、TOTP 前后一步及 Key 相等边界，通知码 CreatedAt/ExpiresAt 保留两次读取。该补齐不列为历史缺陷行为修复；正在重跑全量结果。
- 旧钉钉 disabled 哨兵已换成使用现有设置夹具的实际 unit 契约，验证重定向且不签发状态 cookie。原 NULL pending_auth_session_id 历史形状的 SQLite 测试仍单独保留已有跳过原因，不计通过。


## 2026-09-12：S05 最终完成与交接

S05.0—S05.4 已完成，以下完成记录覆盖前文执行过程中的“实施中/待验”描述；原计划正文保持原样。

| 子步骤 | 实际交付 |
| --- | --- |
| S05.0 | 起点 HEAD、索引、工具及文件摘要；544 个文件、逐声明清点、消费者、构建条件、Wire 和十二组资金账本 |
| S05.1 | identity 资料/身份/注册绑定/会话/强认证/属性及存储/provider/HTTP；app 固定唯一实例，jwtgen 精简装配 |
| S05.2 | team 用例、HTTP、存储、Key 生命周期与 billing 成员消费参与；保留原隔离级别、锁顺序和邮件失败语义 |
| S05.3 | apikey 生命周期、AccessSnapshot、复合规则、认证 L1/L2/outbox、失效与停止等待；routing/modelmap 唯一纯匹配；快照 v40 保持 |
| S05.4 | 生产 HTTP/装配、canonical Principal、字段和事务边界、精确门禁、现有 Project Doc 与 S06—S16 交接 |

最终全量结果：普通 **11,125 pass / 0 fail / 4 skip**；unit **19,120 / 0 / 8**；integration **11,851 / 0 / 5**。数字为 Go 测试事件，包含父/子测试，跳过不计通过。身份/Key/团队 race、S05 PostgreSQL/Redis integration race、前端测试和构建、普通/embed/Linux 构建、两个维护命令、真实产物 embed、standard/simple/SIGTERM/jwtgen 进程契约均通过；Wire 重复生成无差异。

lint 普通/unit/integration 为 **1/285/19**，退出码均为 1；已与 S04 按源文件、规则、完整消息及源码上下文匹配，**新增 0、删除 0**。保留原六项 unit depguard 违规；12 个门禁正反场景和各构建选择均符合预期，夹具已删除。外部供应商、硬件 Passkey、外部 TLS 及历史 NULL session 形状限制单独记录，未冒充行为通过。

相关历史修复 H01—H06 已保存原 HEAD 复现和最小修复证据：快照隔离、复合 Fast 字段、管理员 Key 配置+重置原子性、refresh 唯一消费、pending 条件消费及邮箱 OAuth Begin 失败补偿。时钟注入保持 app 的系统时钟默认值、原取时点及窗口参数，不列为额外业务变更。

完成证据与交接入口：

- [验证摘要、历史修复与日志导航](baseline/S05/verification.md)
- [文件/声明/消费者/Wire 清单](baseline/S05/file-ownership.json.gz)
- [跨模块事务与字段写权限](baseline/S05/transactions-and-fields.md)
- [十二组资金写入](baseline/S05/funding-writes.json)
- [生命周期及 S06—S16 剩余职责](baseline/S05/lifecycle-and-handoff.md)
- [依赖门禁夹具](baseline/S05/dependency-fixtures.json)、[lint 逐项比较](baseline/S05/lint-comparison.json)
- [冻结资料与索引核对](baseline/S05/invariants-final.json)、[差异检查](baseline/S05/diff-check.json)

SQL 342 项、Ent 379 项、S00—S04 冻结资料及阶段文件 1,098 项、其他任务内容 50 项的起始摘要无意外变化；原计划前 18,472 字节 SHA256 仍为 `b35167d920ed02a9e5c12baaadecf6904ca090fb304053b12557bba5f061c2d9`。HEAD 保持 `ed5380db2fa48f173746315679d6a2238208cb7a`、分支 main，索引未改。未自动提交、推送或提交其他任务文件。

roadmap 更新为 **6 / 17**；下一步编写 S06 子计划。后续仍分别迁移路由/账号、调度、用量/审计、供应商、通知、网关、推广支付、任务、初始化与兼容清理，不提前宣布这些模块已迁移。
