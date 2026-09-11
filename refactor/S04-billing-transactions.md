# S04：迁移 billing 结算、权益与事务参与能力

## 1. 基线与实施约定

以当前 `main`、HEAD `8b93af63aa15c668b94cd4b8dee9e02384b6aa23` 为起点，完成 S04.0—S04.6。

实施前将本计划原样保存到 `refactor/S04-billing-transactions.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，清单及脱敏证据保存到 `refactor/baseline/S04/`；沿用本次重构的阶段文件约定，不另存 `.agents/plans/`，不改写 S00—S03 冻结资料。

已确认：

- 使用 Go 1.27.0、golangci-lint 2.13.2；Docker 可用。规划期间相关 service 定向 unit 测试通过。
- 保留 AGENTS.md、其他计划及 diagnostics 的已有改动，不自动提交、推送或切换分支。
- 维持 HTTP、运行模式、金额精度、持久化幂等键、数据库及缓存格式。本阶段只生成 Wire，不生成 Ent。
- 按用户选择，本次链路中复现的历史缺陷同阶段做最小修复，先保存旧 HEAD 复现，再单独记录行为变化。
- 按**单实例部署**修复平台额度重置与 flusher 的竞态：采用共享的进程内按用户互斥，不引入跨实例锁、数据库版本列或新缓存协议。该修复不声明支持多个服务进程之间的协调。

## 2. 模块边界与接口决策

### billing 的所有权

| 能力 | 本阶段归属与接口 |
| --- | --- |
| 资金主体与结算 | billing 拥有付款主体、行为主体、团队、Key、账号、资金来源、定价快照、分配结果及错误；提供 `Check`、`Settle`。 |
| 任务资金 | billing 提供 `Reserve`、`Capture`、`Release`，输入资金动作 ID、主体、金额、价格快照和受控任务引用。 |
| 订阅与套餐 | billing 拥有套餐 CRUD、订阅发放、排队、撤销、恢复、窗口维护、资格及进度规则。 |
| 兑换与调账 | billing 拥有兑换管理、兑换权益、兑换记录及原子余额 set/add/subtract。 |
| 额度与倍率 | billing 拥有消费累计、资金窗口、平台额度、用户分组倍率及相关缓存；配置来源通过明确投影传入。 |
| 存储与 HTTP | SQL/Ent 实现进入 billing/postgres，Redis 实现进入 billing/rediscache，订阅、兑换、套餐及额度 HTTP/DTO 进入 billing/httpapi。 |

新核心不接收旧 `User`、`Account`、`Group`、完整 config、Gin、Ent 或 SQL 事务。独立 Options、时钟和日期计算对象由 app 注入，保留现有取时点及各类窗口差异。

订阅及兑换关联用户使用只读展示投影，覆盖当前浅层 JSON 所需字段，不复制完整身份实体。可直接兼容的旧类型使用别名；必须保留旧形状的入口只做投影和委托，不保留第二份规则。

### 事务与副作用

- 普通 `Settle` 保留现有闭合 `Apply` 事务：认领去重键、锁付款用户、按稳定顺序处理订阅、更新余额及 Key/成员/账号累计、写必要 outbox，最后一次提交。死锁重试重新开启整个事务。
- 订阅和兑换采用命名明确的闭合存储操作。最新状态读取及锁留在 PostgreSQL Adapter，校验、窗口、时间链和资金分配算法由 billing 的规则执行。
- billing/postgres 另提供明确接收现有 Ent Tx 或 SQL Tx 的事务内操作。参与方法不得 Begin、Commit、Rollback，也不得执行缓存失效或通知；普通结算不因 context 中存在 Ent Tx 就自动变成事务参与者。
- 旧外层事务通过精确的过渡 Adapter 接入。沿用现有 Ent 事务 context，验证实际使用同一连接，不新增事务 context key。
- 提交后的缓存、认证失效、通知和返利按原入口的顺序及失败语义执行。当前为空的订阅缓存方法不视作有效失效机制，也不借迁移增加原先没有的广播。
- 管理员调账保持原子 set/add/subtract；返利和调整记录仍按既有尽力语义处理，不改成失败即回滚充值。

### 旧能力与装配

所有新模块对旧能力的依赖集中由 app/legacybridge 实现窄接口，负责调用和投影，不承载资金规则。

- 渠道价卡、账号统计规则、动态模型候选通过桥接提供。保留 S03 的按需查价顺序、零价与缺价、用户价格与账号成本分离。
- RPM 裁决留在旧准入编排，安排 S05/S06/S11 退出；保持资金检查后再累计 RPM、simple 跳过及现有错误顺序。
- Usage Log、供应商用量归一化、请求 ID 选择、取消策略和完成 worker 留在旧网关；资金预检、计算、分配及提交后资金处理委托 billing。
- 余额及额度阈值判断进入 billing；邮件模板、收件人处理和发送保留旧通知能力，等待 S10。
- app 持有唯一生产实例及生命周期绑定。既有隔离缓存保留各自作用域，不因迁移复制实例，也不顺便合并缓存策略。

## 3. 子步骤与实施顺序

### S04.0：冻结输入与资金账本

记录实际 HEAD、索引、工作区、工具版本、源文件摘要、SQL checksum 和 Ent/Wire 摘要。

更新十二组资金写入清单，逐方法记录写入字段、事务拥有者、生产调用者、提交后副作用、测试标签、目标和退出阶段。混合文件按符号拆分，重点包含：

- 普通结算与 creative/batchimage 预占；
- `SubscriptionService`、`RedeemService`；
- `PaymentConfigService` 中的套餐 CRUD；
- `AdminService` 中的兑换管理、调整记录及余额操作；
- 额度 handler 中直接进行的存储、校验、失效和审计；
- 用户分组倍率、平台额度、通知阈值及生命周期。

以 S03 全量测试通过结果和未截断 lint 诊断为基线；lint 原有 normal/unit/integration 分别为 1/285/19 项，比较文件、规则和完整消息，不只比较数量。

### S04.1：提取契约、计算入口与投影

- 将结算命令、结果、资金分配、订阅/套餐/兑换实体和相关错误迁入 billing；domain 的资金与订阅类型保留必要别名。
- `Check` 使用明确的用户、Key、分组及订阅快照；事务内仍复核最新可消费状态，预检结果不能代替最终授权。
- 统一接入 S03 pricing，迁移剩余查价编排、倍率及账号成本入口。保持渠道/目录的懒加载，普通请求与 WS turn 的定价时刻不变。
- 保留普通结算指纹先使用原始金额、后量化的顺序；余额及 Key 等 8 位计数与订阅、用量事实的 10 位精度边界保持独立。
- 将额度只读展示规则从 quotaview 提取到 billing；HTTP 序列化归 HTTP Adapter。
- 为新核心建立 depguard，旧包装同步改为委托。

### S04.2：迁移普通结算及提交后资金处理

先迁移现有 SQL 和死锁重试，再提取其中可纯化的分配规则，分别验证。

必须保持：

- `(request_id, api_key_id)` 去重、历史归档去重、原指纹及冲突行为；
- 团队 owner 付款、ActorUserID 累计成员用量；
- auto、balance、指定订阅模式及请求进入时固化的资金来源；
- 指定订阅准入不回退，已放行普通请求的超额部分形成余额欠费；
- Key 总额/滚动窗口、团队成员、账号累计与 scheduler outbox 的原子范围；
- 显式账号零成本、账号倍率及用户扣款相互独立。

将余额缓存同步、资金窗口累计及确定后的通知事件接入 billing，保持重复结算时的处理差异。旧网关继续拥有日志与完成处理：日志失败不再次扣款，结算失败仍保留现有待对账事实，simple 只执行适用的记录路径。

### S04.3：迁移权益、额度、兑换及原子调账

**订阅与套餐**

- 迁移发放、来源订单去重、排队、延长、目标有效期调整、撤销/恢复、自助耗尽撤销、后续时间链平移和 Key 改绑。
- 将 PaymentConfigService 的套餐规则及存储迁入 billing，旧支付入口改用值类型投影；订单快照和提供商配置留 S12。
- 保留套餐省略/null/显式清空、分组映射同步、排序、展示币种、在售状态及历史 Ent JSON 输出形状。
- 保留日历日、7/30 天窗口、尾段保护、一次性日额度和预期窗口比较；不统一成一种窗口算法。
- 订阅过期、提醒及现有维护执行器同步接入 app 生命周期；保留立即首轮、周期、锁策略及停止等待。
- site 的有效订阅读取改由新 billing 提供，删除 S02 对旧订阅仓储的桥接；只做必要投影，不改变公告资格查询语义。

**兑换与管理员接口拆分**

- 兑换码锁、权益发放、usage、次数和状态快照保持同一事务；保留余额、并发数、订阅、邀请码的现有支持及拒绝边界。
- 并发数兑换的用户字段更新暂由命名明确的事务参与实现承担，登记 S05 退出；不能为解耦拆成独立提交。
- AdminService 中的兑换管理迁入 billing 的管理用例，新兑换 handler 不再依赖整个 AdminService。
- 旧 AdminService 保留用户管理和调账编排，注入窄调账/调整记录接口；无消费者的兑换管理方法及构造依赖删除，必要旧入口只委托。
- 提取 user_repo 中已确认的原子调账及兑换余额操作；保留正数兑换累计充值、普通加款和管理员调账之间的现有差异。
- 并发验证 set 返回的旧余额与实际调整量；若复现旧快照问题，改为在同一原子操作中锁定并取得真实旧值，不能退化成无锁读后覆盖。

**平台额度及单实例竞态修复**

- 迁移 Redis-first 预检、singleflight、Lua 累计、dirty set、数据库镜像、管理替换/重置和 flusher。
- 在旧 HEAD 复现“flusher 读旧快照 → 管理员重置 → flusher 覆盖重置”，再增加修复后的确定性交错测试。
- billing 持有共享的按用户协调器，app 只构造一次；管理入口、缓存维护及 flusher 使用同一实例。
- flusher 可先 Pop dirty key，但必须在读取 Redis 快照前取得相关用户锁，持有至该批数据库写回结束。多用户按 ID 升序取锁、逆序释放，保留原批次上限和单批原子性。
- 管理重置和配置替换从读取必要状态开始持锁，覆盖数据库提交及对应缓存失效。缓存回填、跨窗口刷新和用量写入同样参与协调；加锁后重新读取，禁止等待后继续写入旧快照。
- singleflight 内完成受保护的回源与回填，调用方不在锁外重复写回结果。普通只读缓存命中不增加数据库查询。
- 锁等待计入现有操作预算，支持取消；失败不得绕过锁写旧快照。flusher 失败沿用对应 dirty 回填与停止报告规则，锁表清理不得让同一用户出现两把锁。
- 保留 Redis 故障时已有降级及告警，不把进程内互斥描述成 PostgreSQL/Redis 原子提交。故障期间的缓存滞后单独记录。

### S04.4：接入通用任务资金接口

- 新任务接口使用受控任务种类和引用，核心不暴露 BatchImage 名称或 CreativeEntity 布尔分支；旧命令在过渡入口转换。
- PostgreSQL Adapter 暂保留对两张任务表的明确投影写入，与资金预占、分配快照、预记标记同事务提交；该耦合登记 S13 删除。
- 保留 v1/v2/v3 价格快照、原请求 ID、指纹编码及旧任务重放。新任务引用不自动加入历史指纹。
- 保留严格预占、实际金额捕获、差额释放、已删除 Key/已退出成员以及只回退原窗口预记的规则。
- creative/batchimage 的供应商、任务状态机、恢复和 UI 留旧模块，资金实现只保留一份。

### S04.5：接入外层事务与过渡调用者

逐条接入旧注册/身份默认订阅、支付履约、管理和任务消费者，验证参与方法使用调用方现有事务。

- 订阅与兑换已迁能力统一调用新实现；旧外层事务仍由原拥有者提交，提交后才运行该入口原有副作用。
- 注册及首次绑定的 savepoint、fail-open 和默认赠送边界保持，剩余身份写入登记 S05。
- Promo、返利转余额、退款仍保留完整旧闭合事务，不单独抽走其中一条资金语句再独立提交，退出归 S12。
- Key/成员/账号配置及维护留 S05—S07，初始化/恢复留 S14。明确配置、累计、重置各自的写权限，普通更新不得覆盖并发消费字段。
- 所有已迁和未迁项分别标记“资金操作归属”与“业务编排归属”，不提前宣布全项目资金写入已经收敛。

### S04.6：迁移 HTTP、门禁与文档收尾

- 用户及管理员订阅、兑换、平台额度、套餐 handler/DTO 进入 billing/httpapi，路由直接绑定新 handler。
- 保留 URL、中间件顺序、鉴权、幂等 helper、状态码、reason、分页、排序、CSV 导出、时间清空和管理员字段边界。
- 从旧用户 handler 提取额度用例；用户存在性通过只读接口投影，HTTP 不直接访问仓储或数据库。
- 公共 usage 和 Key 权益展示只消费 billing 的只读投影，区分绑定订阅、自动分配与余额，不迁移 usage 查询领域。
- 每批更新现有 depguard：核心、HTTP、PostgreSQL、Redis、app 及桥接按角色限制；历史许可精确到文件/import，迁出即删除旧例外。
- 同步现有架构、路由结算、支付权益、平台额度、任务资金、HTTP 和开发流程文档，描述实际结构及单实例修复边界。

## 4. 验证安排

所有项目命令使用 `GOTOOLCHAIN=go1.27.0`。每批运行新包、旧转接及直接消费者的普通/unit 测试；存储、事务和缓存使用隔离 PostgreSQL/Redis，关键事务不得仅用 SQLite 或 mock 验收。

| 验证面 | 必须取得的证据 |
| --- | --- |
| 普通结算 | 同 ID 重放/冲突、并发消费与调账、owner/actor、三种资金模式、指定订阅溢出、8/10 位精度、outbox 失败回滚、simple 和日志失败。 |
| 外层事务 | 资金写入后 usage、次数、Key 改绑、业务记录或审计失败时整体回滚；提交前无成功副作用；重试与并发不重复发放。 |
| 权益 | 来源订单去重、排队时间链、撤销恢复、尾段窗口、DST/时区、套餐省略/null、兑换负数边界、邀请码拒绝及支付跳过重复返利。 |
| 任务资金 | 两种任务、各历史快照版本、严格预占、部分捕获/释放、重复动作、删除 Key/成员退出、窗口变化及任务投影失败回滚。 |
| 缓存与竞态 | 单实例可控 goroutine 复现；重置前后两种顺序、多个 flusher、配置删除、回源/刷新/累计交错、取消、不同用户并行及锁回收。 |
| 生命周期 | 构造无启动、唯一实例、停止后不入队、在途完成、缓存队列与 dirty mirror 排空、预算超时报告及 Redis 最后关闭。 |
| HTTP | 用户/管理员权限、历史 JSON、套餐旧 Ent 输出、CSV、幂等重放、公共权益及余额/订阅区别。 |
| 未迁流程 | 注册默认赠送、Promo、返利、支付履约及退款公开入口回归，尤其退款审计失败整体回滚。 |

共享缓存、协调器和队列做定向 race；真实 PostgreSQL/Redis 竞争做 integration race，不扩大为全仓 race 或 benchmark。

使用可丢弃 depguard 夹具验证合法方向、旧入口许可、同目录新增违规、旧文件新增禁止 import、迁出后例外失效、核心反向依赖及正常 Adapter。覆盖普通/unit/integration，并核对 wireinject、embed、Darwin/Linux；保留诊断后删除夹具。

阶段收尾：

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

另验证两个维护命令构建、Wire 再次生成无差异、真实前端产物下 embed 测试，以及 standard/simple 启动与 SIGTERM 资金队列停止顺序。

通过 go list 和 JSON 测试事件确认关键测试实际执行。S04 新问题必须解决；本次链路历史缺陷保留旧 HEAD 复现和修复证据，其余既有 lint 独立归档，不扩大忽略规则。真实供应商 E2E 与外部 TLS 限制沿用既有记录，跳过不计通过。

## 5. 完成、交接与回退

S04 完成须同时满足：

- 普通结算、订阅/套餐、兑换、平台额度和本阶段资金操作接入唯一 billing 实现；新核心无旧业务、config、数据库或具体 Adapter 反向依赖。
- 外层事务、任务资金、提交后副作用和单实例额度竞态取得真实行为证据。
- AdminService、PaymentConfigService、旧网关及任务入口的保留职责明确；所有兼容入口、消费者、构建条件、Wire 和依赖例外可追踪。
- 本次已复现的相关历史缺陷已最小修复并独立说明；必要验证未完成时保持“待验”。
- SQL checksum、Ent、S00—S03 冻结资料及其他任务文件无意外变化；Wire 差异可解释，新增及已有文件的 diff 检查通过。

完成后 roadmap 更新为 **5 / 17**、S04 已完成，下一步为 S05 子计划。后续身份/团队/Key、账号路由、usage、通知、网关、支付推广、任务及初始化恢复分别登记 S05—S16 的退出事项。

按子步骤回退代码、规则、文档和 Wire，恢复对应旧调用链；不涉及数据库或缓存格式降级。竞态修复回退会恢复原有覆盖风险，执行记录须明确说明。交付可审查差异，不自动提交，不提交 SYNC.md 或其他任务内容。


## 执行记录

### 2026-09-12：S04.0 与首批契约迁移

- 原样计划 SHA256：`ce83ac8e15439d7637249b5d382fda7409e9ea3dd7ea0d45ce3e05c02c1fd1b2`；实施 HEAD 为 `8b93af63aa15c668b94cd4b8dee9e02384b6aa23`，工作区与索引见 [初始快照](baseline/S04/start.json)。
- 原生产代码的 PostgreSQL 复现确认两项旧缺陷：平台额度重置被旧镜像覆盖；并发结算时原子 set 返回陈旧旧值。完整失败事件见 [原始复现](baseline/S04/original-defects.result.json)，夹具见 [复现源码](baseline/S04/original-defects_test.go.txt)。尚未将这两项标为修复完成。
- 资金分配、普通结算指纹/量化、通用任务命令及权益值类型已开始迁入 billing；旧任务命令保留字段投影，CreativeEntity 只留旧入口，具体任务表映射只在 PostgreSQL Adapter。
- 普通 SQL 与死锁单测随实现迁入 billing/postgres；真实集成测试继续通过旧生产入口覆盖新实现。账号 outbox 仍显式参与同一事务。首批普通契约见 [结果](baseline/S04/settlement-core-tests.result.json)。
- 当前仍在实施 S04.1—S04.2，继续完成装配、权益事务、额度协调和完整验证，roadmap 保持实施中。

### 2026-09-12：S04.1—S04.4 实现与定向验证进展

- 结算 SQL、Redis 缓存、订阅/兑换存取、管理员原子调账及平台额度已进入 billing 对应 Adapter；旧入口保留类型别名、输入投影和委托。生成 Wire 已确认平台额度管理、准入及 flusher 共用同一按用户协调器。
- 两项原 HEAD 缺陷已有修复行为证据：真实 PostgreSQL 的 set 返回锁定后的旧余额为 80，见 [回归](baseline/S04/balance-regression-fixed.result.json)；真实 PostgreSQL/Redis 的 flusher/回源与管理重置确定性交错见 [回归](baseline/S04/quota-real-coordination.result.json)。不声明跨进程协调或 PostgreSQL/Redis 原子提交。
- 订阅、兑换、额度及套餐 HTTP 已迁入 billing/httpapi，管理员套餐保留旧 Ent JSON 形状，公开套餐保留独立展示投影。套餐与支付回归见 [结果](baseline/S04/plan-contracts-fixed.result.json)。支付订单、供应商配置及退款闭合事务保留 S12。
- 订阅过期扫描及提醒资格迁入 billing；app 注入旧通知、设置及锁策略，生命周期保留立即首轮并增加可取消停止等待。[订阅维护测试](baseline/S04/expiry-contracts.result.json)覆盖重复启动、阻塞停止预算和发送超时隔离。没有生产消费者的订阅维护队列仅迁移唯一实现，不新增后台实例。公告已直接读取新 billing 的有效订阅投影，删除 S02 的旧订阅桥接。
- 用户分组倍率缓存、查价与计费编排、阈值判定及提交后缓存/额度处理迁入 billing。两个网关的倍率缓存仍隔离；纯定价复用 S03 pricing。定向计费、通知及 RecordUsage 回归见 [结果](baseline/S04/funds-effects-contracts.result.json)。关闭 flusher 时的异步镜像也纳入共享用户协调；该分支的追加交错证据仍待补齐。
- 当前仍在实施：继续核对外层事务、任务资金、依赖门禁、完整矩阵、文档和全量验证。此记录不代表 S04 已完成。

### 2026-09-12：事务复核、普通分配规则与依赖门禁

- 普通结算从已锁定的 SQL 行投影为 `SettlementSubscription`，billing 生成有序分配及窗口更新；PostgreSQL Adapter 保留查询、锁、写入和 outbox，浮点计算顺序及 8/10 位边界不变。[普通/任务资金与退款的真实事务回归](baseline/S04/allocation-real-transactions.result.json)已通过。
- 新增明确的 `BalanceInTx`、`SubscriptionsInTx`、`RedeemInTx` 参与入口。外层未提交用户/套餐可见性、兑换余额/并发/订阅真实写入后 usage 失败、提交前无认证失效及退款审计失败回滚见 [权益事务](baseline/S04/entitlement-real-transactions-fixed.result.json)。
- 追加复现并修复的历史问题：有效期修改独立提交见 [原 HEAD](baseline/S04/original-validity.result.json)；两次 +7 天被旧快照覆盖为一次见 [原 HEAD](baseline/S04/original-subscription-concurrency.result.json)。权益管理现在持有同一用户锁后重新读取、校验和修改时间链，[修复回归](baseline/S04/entitlement-concurrency-fixed.result.json)通过。旧 SQLite 单测只保留事务身份断言，真实锁行为由 PostgreSQL 测试验收。
- 平台额度缓存失效后 Lua 的缺失键分支会丢弃在途请求新用量，已在旧 HEAD 复现并保存 [源码与失败](baseline/S04/original-quota-after-reset_test.go.txt)。锁内回填后再累计的 [同一断言回归](baseline/S04/quota-after-reset-fixed.result.json)通过。两种重置顺序、异步镜像等待、多 flusher、配置删除和关闭拒绝后的锁释放见 [扩展交错](baseline/S04/quota-coordination-expanded.result.json)。
- 首轮普通全量测试已通过（[结果](baseline/S04/billing-normal-preflight.result.json)）；后续分配规则提取、锁修正和测试夹具调整继续执行增量及最终全量验证。初次全量 unit 中暴露 SQLite 不支持行锁的旧测试限制，已用可观察事务端口保持其原身份断言，未放宽生产锁规则。
- 门禁夹具已覆盖普通/unit/integration、wireinject、embed 与 Darwin/Linux：正例零诊断；反例精确命中 17/18 项，未出现缺失或额外诊断，临时模块已删除。证据见 [依赖夹具](baseline/S04/dependency-fixtures.json)。生产规则保留原六项 unit depguard 违规，未转成许可。
- 已同步系统架构、路由结算、支付权益、平台额度、任务资金、HTTP 与开发流程文档，特别说明单实例锁边界和订阅空缓存兼容方法不能作为广播证据。S04 继续保持实施中，尚需完成最终矩阵、全量 lint/构建/race/进程回归、消费者账本和完整性核对。

### 2026-09-12：S04.0—S04.6 完成与交接

| 子步骤 | 最终结果 | 主要证据 |
| --- | --- | --- |
| S04.0 | 实施输入、原索引、工具与源摘要冻结；十二组资金账本全部可定位 | [初始快照](baseline/S04/start.json)、[资金账本](baseline/S04/funding-writes.md) |
| S04.1 | 资金值/错误、显式快照、查价与倍率、账户成本及额度展示进入 billing，旧入口投影/委托 | [迁移清单](baseline/S04/migration-map.md)、[逐文件/声明](baseline/S04/file-ownership.json.gz) |
| S04.2 | 普通闭合 SQL 事务与纯分配唯一实现接入；提交后资金缓存、额度和阈值判断迁入 billing | [真实结算与回滚](baseline/S04/allocation-real-transactions.result.json) |
| S04.3 | 订阅/套餐/兑换/管理调账、平台额度及维护生命周期完成；相关历史缺陷保留旧 HEAD 复现并修复 | [权益事务](baseline/S04/entitlement-real-transactions-fixed.result.json)、[并发权益](baseline/S04/entitlement-concurrency-fixed.result.json)、[额度交错](baseline/S04/quota-coordination-expanded.result.json) |
| S04.4 | 通用 Reserve/Capture/Release 通过旧任务命令投影进入唯一资金实现；两表投影耦合明确保留至 S13 | [资金账本 F11](baseline/S04/funding-writes.md)、[真实存储 race](baseline/S04/final-storage-race.result.json) |
| S04.5 | 注册/支付/管理员/任务保留原外层提交者，已迁参与入口复用原 Ent Tx；未迁闭合事务逐组保留 | [参与事务](baseline/S04/entitlement-real-transactions-fixed.result.json)、[全部行为事件](baseline/S04/contract-events.json.gz) |
| S04.6 | HTTP/DTO、路由接入、精确门禁、文档与构建收尾完成 | [JSON 对照](baseline/S04/final-billing-http-json.result.json)、[依赖夹具](baseline/S04/dependency-fixtures.json)、[验证摘要](baseline/S04/verification.md) |

- 清单覆盖 317 个相关源文件记录、1,096 项混合声明定位、十二组 218 个原资金方法及 54 个本阶段展开入口；没有未定位资金方法或混合声明。旧基础设施初始化的四个方法按 S02 的实际新位置登记，没有误算为 S04 新迁移。
- 普通、unit、integration 全量测试分别为 11,064 / 19,049 / 11,769 个通过事件，失败均为 0；事件含父测试及子测试。5 / 9 / 6 个已有跳过单独记录，不计通过。unit 最终使用串行完整运行，避开 Ent schema 测试共享临时目录冲突。
- lint 逐文件、规则及完整消息对比 S03 后仍为 1 / 285 / 19 项；新增为 0，六项原 unit depguard 违规没有转成许可。新套餐 HTTP 契约测试只对其准确文件许可 Gin；最终六组门禁正例均零诊断，反例精确命中 19/20 项，夹具目录已删除。
- 普通服务、真实前端、embed、Linux 与两个维护命令构建全部通过；前端 14 个测试文件、199 个测试通过。真实 embed 注入、standard/simple SIGTERM 和资金组件唯一启停/Redis 后关验证通过。核心、旧兼容缓存以及真实 PostgreSQL/Redis 竞争的定向 race 均通过。
- 五项修复均有旧 HEAD 失败与新实现行为证据：flusher 覆盖重置、原子 set 旧余额陈旧、订阅有效期提前提交、订阅并发延长丢失、缓存失效后新额度累计丢失。平台协调仅覆盖一个服务进程，不新增跨实例锁、数据库版本列或缓存协议。
- [完整性记录](baseline/S04/integrity.json)确认 SQL migration、Ent、Go 依赖、S00—S03 冻结资料及其他任务文件未变；原暂存区未动，阶段计划原文 SHA256 保持不变，Wire 重复生成一致，现有及新增文本的 diff 检查通过。没有执行 Ent 生成。
- 必要资金/存储/生命周期环境验证已完成。真实供应商 E2E、外部 TLS 以及既有 sentinel/历史夹具跳过沿原限制留后续阶段，详见 [限制清单](baseline/S04/verification-limitations.json)，不据此宣称这些环境已验证。

后续按 [迁移清单](baseline/S04/migration-map.md)交接：S05 身份/团队/Key 及并发字段；S06/S07 渠道、账户及 outbox；S08 用量/审计；S09/S11 平台与网关；S10 通知投递；S12 支付/推广/退款；S13 任务状态机和任务表投影；S14 初始化/恢复；S15/S16 清理已登记兼容入口与生成类型引用。当前仍存在合法的未迁资金写入，不宣布全项目资金已收敛。

本阶段状态为**已完成**，roadmap 更新为 **5 / 17**；下一步编写 S05 子计划。本次交付保持未提交，不提交 SYNC.md 或其他任务内容。按子步骤回退代码/规则/文档/Wire 可恢复旧链路，不需要数据格式降级；回退五项修复也会恢复其旧风险。
