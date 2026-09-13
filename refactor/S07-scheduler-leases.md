# S07：迁移调度、并发与会话选择，提前冻结问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `d0ce5504c09c9b733a75da1fc35826ebf53f6af4` **加上已完成、尚未提交的 S06 工作树**为实施基线。不退回 HEAD 覆盖 S06，不自动提交、推送或切换分支。

实施前重新记录索引、工作区、源码摘要、SQL checksum 和 Ent/Wire 摘要，将本计划原样保存为：

`/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S07-scheduler-leases.md`

证据保存到同仓库 `refactor/baseline/S07/`，登记 roadmap 为“实施中”。保留 S00—S06 冻结资料、AGENTS.md、其他任务计划及 diagnostics；不另存 `.agents/plans/`。

本阶段保持 HTTP、金额算法、数据库 schema、缓存命名空间与版本、standard/simple 及供应商取消策略兼容，只生成 Wire。

**问题处理规则已经确认：**

- 主动排查历史问题的工作在计划阶段完成，执行阶段只落实下表 B01—B05、迁移和约定验证。
- 不再开展额外历史问题审计、扩展故障注入或顺带优化。
- 本次改动引入的回归必须修复。
- 常规验证偶然发现清单外历史问题时，通常只登记；**确实阻止 S07 迁移或验收时，允许必要复现和最小修复**，单列原因、行为变化和证据，不据此扩大排查。

## 2. 计划阶段已确认的问题

复现使用当前原实现及仓库外的临时 Go 测试覆盖层，没有修改工作区生产代码。

| 编号 | 已复现问题 | 本阶段确定的修复 |
| --- | --- | --- |
| B01 | 快照服务重复 Start 会再次重建，Stop 后仍可启动；Stop 不取消支持 context 的在途重建 | 增加统一启动/停止屏障和运行 context；取消周期、排队与在途操作，有界等待，停止后不能重新开启 |
| B02 | 等待计数增加失败后故障放行，调用方仍减计数，可能扣掉其他等待者的名额 | 分离“允许等待”和“确认取得计数”；等待资源只释放自己确认取得的计数，保持故障放行 |
| B03 | 通用 Gateway 已取得账号槽后，完整账号补全失败，释放函数丢失 | 获取即登记资源；补全、协议复核或后续准备失败时统一回滚，释放幂等 |
| B04 | Redis 脚本缓存丢失后，会话批量查询返回缺失数据，容量与管理展示可能漏报 | 批量路径使用可冷启动执行的 Lua 调用，保持时间源、部分失败和返回语义 |
| B05 | bucket 锁过期后，旧持有者无条件 DEL 会删除继任者的新锁 | 返回带所有者令牌的锁句柄，比较令牌后释放；保持原 key、字符串存储类型和 TTL |

补充确定事项：

- 原 `TestGetAccountsLoadBatch` 的跳过原因当前未复现：临时取消 Skip 后，原断言连续五轮通过。实施时恢复该测试，不修改生产算法或削弱断言。
- 已用真实 PostgreSQL 复现较小 ID 的 outbox 事件迟提交后被消费水位跳过。**按用户选择保留现有周期全量重建的恢复边界**，不增加取批写入屏障，不宣称逐事件严格消费；明确十秒清理宽限不能保证该事件重新被轮询。
- 已取得定向 unit race **599 条通过事件、无失败或跳过**；真实 PostgreSQL/Redis race **53 条通过事件、一个上述已补验的跳过项**。这些是测试事件数量，包含父子测试。

## 3. 模块接口与迁移步骤

### S07.0：冻结输入和交接账本

建立迁移清单，覆盖快照/outbox、评分、粘性、并发、RPM、会话限制、串行队列、诊断和混合文件中的相关符号。记录消费者、构建标签、测试、Wire、文档锚点、共享实例、资源拥有者及退出阶段。

归档本次规划阶段的复现夹具、失败日志、源码摘要及通过结果。B01—B05 成为固定修复清单，不将“继续寻找类似问题”列为实施任务。

### S07.1：候选快照、缓存与 outbox

- `scheduler` 拥有候选读取、bucket 生命周期、重建、outbox 消费及受控 DB fallback；Redis 与 SQL 实现分别进入其 Adapter。
- 保留 `sched:v2:` 完整/轻量投影、编码、分块、旧版本宽限、epoch/tombstone、分组生命周期锁、last-used 单调更新，以及现有事件合并、消费水位和清理策略。
- 新核心读取无凭据候选投影。完整账号兼容编码留在 Adapter，执行凭据通过 account 的受控入口提供，不向评分或诊断暴露凭据。
- account、routing、egress、billing 的现有 outbox 写入改绑新存储参与能力，沿用原连接、事务和尽力发布边界，不增加重复事件。
- 保留 fallback 的开关、超时、QPS 上限、simple 分组处理和错误类别；候选与完整账号读取继续使用各自原有时点。
- 落实 B01、B05。初始重建仍异步启动，outbox 保留立即首轮；停止取消运行 context，等待当前操作，不把未消费的持久事件称为已经排空。

### S07.2：基础/高级选择、资格与反馈

新 `SelectionInput` 接收最终 `RoutePlan`、独立账号投影、排除集合、会话连续性及必要请求能力，不接收 Gin、旧 Account/Group、完整 config 或具体网关服务。

- 迁移基础排序、高级评分、Top-K 抽样、共享 EWMA、运行时参数读取与诊断计算；复用已有 `scheduler/policy`。
- 保留最终分组覆盖优先级、显式零值、同分决胜、缺失观测中性值、随机种子和抽样方式。反馈携带本次选择固化的参数，基础请求不更新高级反馈。
- 平台资格通过窄接口注入：保留 OpenAI previous-response、订阅优先、transport/Compact，Grok 媒体资格，以及 Anthropic/Gemini mixed 规则；供应商解析与交换仍留 S09。
- 每次候选尝试、fresh 和 DB 复核重新调用 routing 的候选解析。保持硬过滤、抢槽、补全和错误输出的原顺序与查询预算。
- RPM 计数与准入规则归 scheduler，保持资金检查后的调用时点、用户/分组 override、simple 跳过、故障放行和上游成功后的账号软计数。
- 金额、消费累计和资金窗口能力委托 billing；现有 usage 聚合通过只读接口衔接并登记 S08。健康写入直接绑定 account，不把旧 RateLimitService 整体搬入 scheduler。

### S07.3：Lease、等待与串行队列

采用两层资源拥有关系：

| 接口/类型 | 责任 |
| --- | --- |
| `AcquireUser` → `Lease`、`WaitResult` | 拥有用户槽、API Key 统计槽及实际取得的等待资源 |
| `Lease.Select` → `AttemptLease`、选择结果 | 拥有本次账号槽、串行锁和适用的会话登记，携带当次模型/协议解析结果 |
| `AttemptLease.Finish(outcome)` | 根据成功、部分服务或失败结果处理会话保留，再释放本次资源 |
| `Lease.Release` | 幂等释放请求剩余资源；异常返回时完成组合清理 |
| `SelectOnly` | 供 count_tokens、探测与诊断使用，不取得请求槽或注册会话 |

- gateway 仍决定是否重试及何时结束 attempt；scheduler 不创建第二套 failover 循环。
- 等待循环进入 scheduler，通过同步的等待观察接口通知 HTTP 输出心跳。核心不持有 ResponseWriter；HTTP 保留 SSE 格式、Header、Flush、错误映射和输出状态。
- 保持用户槽先于账号槽、用户等待后再次检查权益；不统一各入口已有的等待计数时点、容量与超时。
- 落实 B02、B03。明确确认取得、故障放行与结果不明的资源状态；未确认取得的计数不执行补偿递减，沿用 TTL 自愈。
- 迁移账号/用户并发、API Key 统计、消息串行锁和清理任务，保持 Redis TIME、索引、请求 ID、TTL、轮询退避和 RPM 自适应延迟。
- 图片本地限流保持原独立作用域，纳入同一资源释放契约，不合并为账号 Redis 池。
- WS 入站连接租约与 Live 并发租约保持独立，保留续租、丢失取消及普通槽替换语义；远端会话、观察连接及结算仍留原执行层。
- `ReleaseMode` 由调用方明确选择取消释放或完成释放。**Qoder 已进入上游的流式请求使用完成释放**；客户端断开只停止下游写入，在原预算内收集尾部 usage 后释放。等待和非流取消保持原行为。

### S07.4：粘性、诊断、装配和门禁

- 迁移调度粘性读取/写入、旧 hash 兼容、TTL 和连续性规则；会话标识解析由旧入口投影，登录、OAuth 授权和上游会话数据不迁入 scheduler。
- 保留硬 previous-response 绑定、可移动时的加权选择、单次逃逸不改原绑定，以及满槽溢出后返回原粘性的语义。
- Anthropic 会话限制保留成功/可结算部分结果后的空闲窗口；失败及放弃 attempt 按原规则注销。落实 B04。
- 评分诊断核心与生产选择共用参数、候选规则和反馈实例；HTTP 进入 scheduler Adapter，保持原 URL、管理员鉴权、JSON 及安全输入限制。诊断不抢槽、注册会话、改粘性或写反馈。
- app 构造唯一快照、反馈、参数缓存、并发和队列实例，绑定 account/routing/billing 的直接投影。旧桥接只保留 S08/S09/S11 所需调用，不持有第二份规则或状态。
- 同步 depguard 的核心、叶子、HTTP、PostgreSQL、Redis 和装配规则；历史许可精确到文件/import，迁出即删除。旧入口只委托；不能表达资源所有权的增减计数对在消费者清零后删除。
- 同步现有调度缓存、网关生命周期、系统架构、路由计费、配置、HTTP 和开发文档，保留稳定锚点。

## 4. 验证安排

统一使用 `GOTOOLCHAIN=go1.27.0`、golangci-lint 2.13.2。每批只执行迁移验证、固定修复回归和直接消费者测试。

| 验证面 | 必须覆盖 |
| --- | --- |
| 固定修复 | B01—B05 的原复现与修复后断言；等待失败放行、重复释放及补全失败的实际资源数量 |
| 快照/outbox | 乱序重建、退休/重开、旧 writer、last-used、同事务回滚、批量事件、消费失败及周期重建恢复 |
| 选择与评分 | basic/advanced、最终组覆盖、零权重、缺省观测、Top-K、混合池、逐候选复核、DB fallback 上限 |
| 等待与会话 | 队列满、取消、超时、心跳写失败、串行锁归属、成功保留会话、失败注销和租约过期 |
| 平台衔接 | 硬 previous-response、粘性逃逸、WS/Live、Qoder 流式与非流释放、无槽探测 |
| 生命周期 | 构造不启动、重复启停、停止后不认领、取消等待、在途完成、预算超时及 Redis/SQL 最后关闭 |

共享状态执行定向 race；涉及锁、缓存和 outbox 的场景使用隔离 PostgreSQL/Redis，并运行适用 integration race。恢复原负载测试的 Skip，保留全部原断言。

depguard 使用可丢弃夹具验证合法方向、精确旧许可、新文件拒绝、迁出例外失效及正常 Adapter。核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 文件选择；实际测试事件和跳过分别记录。

收尾串行执行：

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

另验证两个维护命令、Wire 再生成无差异、真实前端产物的 embed 测试，以及 standard/simple 启动和 SIGTERM 的调度资源停止顺序。

S06 lint 基线 **1/285/19** 按文件映射、规则和完整消息比较。不得扩大忽略规则或削弱断言；真实供应商、外部 TLS/E2E 的既有限制继续单列。

## 5. 完成与交接

S07 完成需要：

- 快照、评分、并发、队列和调度会话生产链使用唯一实现；新核心无旧业务、config、Gin 或具体存储反向依赖。
- Lease 覆盖实际获取、失败补偿、取消与完成释放；B01—B05 验证通过。
- 已确认的 outbox 恢复限制和其他环境限制准确登记，未执行的必要验证保持待验。
- 兼容入口、消费者、构建条件、Wire、依赖许可与文档可追踪；供应商资格、usage/Ops、网关及任务的剩余职责分别交接 S08—S13，兼容清理交 S15/S16。
- SQL、Ent、旧冻结资料及其他任务内容无意外变化；Wire 差异可解释，新增及已有文件的 diff 检查通过。

满足后 roadmap 更新为 **8 / 17**、S07 已完成，下一步编写 S08 子计划。

按子步骤撤销 S07 代码、装配、规则和文档即可回退，保留原 S06 工作树。回退 B01—B05 会恢复对应启停、计数、槽位或锁风险；不涉及数据库或缓存数据降级。交付可审查差异，不自动提交。


## 执行记录

### S07.0 · 2026-09-13

按用户先提交现有更改的要求，S06 已单独提交为 `1da54c8091e7f56f5cb9d8621bea5918f87471a6`，未推送。该提交是 S07 的实际基线；上方完整计划原文保留。开始时索引为空，其他任务资料保留。计划原文长度 13256 字节，SHA256 `676816011475e4cbbc07081a944075ee025a007749c4988bbb9c71dfe4733dc5`。

[工作区与工具快照](baseline/S07/start.json)、[源码/SQL/Ent/Wire/冻结资料摘要](baseline/S07/source-checksums.json.gz)、[初始符号清单](baseline/S07/input-inventory.json.gz)及 [规划原始测试覆盖说明](baseline/S07/planning/original-overlay.json)已保存。规划日志和夹具位于 `baseline/S07/planning/`；B01—B05 为固定修复项，outbox 迟提交保留周期重建恢复边界。

当前实施 S07.1；后续记录仅描述本阶段迁移、固定修复及约定验证。

### S07.1 / S07.3 · 2026-09-13 · 事件与会话存储首批接通

事件契约、outbox SQL/去重指纹及 RPM 缓存已迁入 scheduler，旧入口委托。资金/路由/代理装配改绑同连接 publisher；账号快照发布的剩余投影继续随 S07.1 收口。五小时费用缓存已拆到 billing/rediscache；旧 SessionLimitCache 仅组合两类能力，不复制状态。B04 的批量会话查询改用冷启动可执行的 Lua，原 Redis key、TTL、部分失败语义保持。原负载测试删除已失效的 Skip，保留断言。首轮 outbox 测试指出迁出的测试还引用旧 payload helper，已将唯一纯 helper 同批迁出；该编译回归单独留存。

### S07.1 / S07.3 · 2026-09-13 · 资源所有权与固定修复接入

新增 scheduler.Lease/AttemptLease 与显式 ReleaseMode，旧通用/Qoder 释放 helper 委托它；Qoder 流式仍仅在上游完成后释放。B03 在通用选择结果补全失败时归还已取得槽位。[释放契约 race](baseline/S07/lease-release-unit-race.result.json)通过。

B02 以 WaitResult 区分 Allowed 与确认/不明计数，所有生产等待入口改为释放自己取得的结果；旧 Service/HTTP 中不带所有权的增减方法已删除，旧队列满/故障放行断言改从结果读取。B05 将生产 bucket 锁接口改为带持有者的句柄，删除按桶名无条件解锁；旧夹具适配新端口并保留原锁控制。测试适配首轮遗漏一个永不获锁的夹具，编译问题保留于 bucket-lease-unit，已改为保持其失败返回。

[B02/B04/B05 与快照回放真实 PostgreSQL/Redis race](baseline/S07/fixed-leases-integration-race.result.json)通过，无跳过。B01 及完整快照/选择/并发队列迁移尚未完成；roadmap 保持 7/17 实施中。

### S07.1—S07.3 · 2026-09-13 · 启停、并发与纯评分

B01 以 scheduler.WorkerRuntime 接管实际快照初始重建、outbox 与周期任务，取消传入 SQL/Redis 上下文，重建合并的排队等待可取消；app 传入剩余关闭预算。原计划三个复现已经变为通过断言。[B01 与原快照 race](baseline/S07/snapshot-runtime-contracts.result.json)通过。首次 scoped lint 的四个新 nil Context 诊断已改为显式 Background，未加忽略。

账号/用户/API Key 并发、短期负载缓存、WS 入站租约、请求 ID 及串行队列唯一迁入 scheduler；Redis 实现和原行为/集成测试随迁，旧 service/repository 只委托。Live 改读窄租约端口，原返回值和错误保持。直接消费者编译时发现该私有字段依赖及一次转接签名遗漏，失败记录保留于 concurrency-core-unit/2；[修正后 unit race](baseline/S07/queue-concurrency-unit-race.result.json)取得 307 条通过事件。[迁后真实 Redis 与旧回放 integration race](baseline/S07/redis-adapters-integration-race.result.json)取得 47 条通过事件，均无失败或跳过。

纯评分、EWMA、归一化、Top-K 和随机顺序已迁入 scheduler，输入只有评分字段及显式平台观测回调；旧入口投影并委托，不复制统计或算法。随机时钟仍在原按需位置读取。[纯评分与直接消费者普通测试](baseline/S07/scoring-core-normal.result.json)通过。完整候选读取/选择、等待循环、粘性和诊断用例及 app 装配继续实施，未宣布 S07 完成。

### S07.1—S07.4 · 2026-09-13 · 快照装配、等待与运行参数

完整快照重建、outbox 处理、回退和 Redis 版本/epoch/tombstone 算法已迁至 scheduler 与 rediscache。核心只读取无凭据元数据，旧完整账号 JSON 编码唯一保留于 `service/LegacySchedulerCodec`，供 `sched:v2` 完整/轻量报文兼容；不把 account.Record 的管理 JSON 当作等价编码。生产查询通过 app/legacybridge 直接绑定 account/routing Store，旧仓储和服务构造只用于兼容调用。账号/资金事件已改绑 scheduler 的同连接 outbox writer 与提交后尽力 SnapshotPublisher。原周期重建恢复限制保持。[快照与消费者 unit](baseline/S07/snapshot-redis-migration-unit-3.result.json)取得 241 条通过事件。搬迁引起的重复 import、旧分块构造和共享测试辅助遗漏均已修正，失败日志保留，属于本次迁移编译回归。

等待循环和串行队列延迟迁入核心，同步 WaitObserver 只通知 HTTP 输出；SSE 格式、Header、Flush 和首次写出标记保留于 HTTP。`AcquireUser` 组合用户槽、等待所有权和 API Key 统计，故障放行不会递减未确认的计数。RPM 准入规则迁入 scheduler，资金检查后的调用位置和 simple 跳过不变。图片限流保持独立本地作用域，复用 Lease 幂等释放。[RPM/图片/等待 race](baseline/S07/rpm-image-wait-unit-race.result.json)取得 220 条通过事件。

按需槽位、等待、串行锁及 WS 入站租约纳入运行时停止账本；停止取消排队和读取，但不会把未完成释放报告为成功。脱离请求取消的短时查询仍使用原预算，应用关闭时可取消。[调度与消费者 race](baseline/S07/request-runtime-unit-race-2.result.json)取得 474 条通过事件；[短预算和副本契约](baseline/S07/request-runtime-contracts-race-2.result.json)5 条通过，验证实际释放先于 Stop 成功、超时结果保持、等待取消、观察写失败和设置副本隔离。

高级设置 TTL/singleflight、解析和旧设置更新转接到唯一 SettingsRuntime；保留批量失败逐键降级、缺省值与显式零值。基础排序、同分随机、LRU 和窗口优先规则迁到无凭据投影；OpenAI 旧 hash 回读/双写与统计、Redis 粘性键存取迁到 scheduler，远端会话和异步资金数据继续留旧执行层。[高级设置 race](baseline/S07/runtime-settings-unit-race-3.result.json)304 条、[基础选择 race](baseline/S07/basic-selection-unit-race.result.json)360 条、[粘性与排序 race](baseline/S07/sticky-selection-unit-race.result.json)233 条通过，均无失败或跳过。

app 新增 scheduler 构造组，复用唯一 Redis、快照、并发和消息队列实例，Wire 生成成功。[当前相关包普通 lint](baseline/S07/s07-current-scoped-lint-3.result.json)通过。unit 白盒辅助按实际标签拆分，未扩大忽略；旧 `sched:v2` 语料所需的 service import 仅精确许可对应测试文件。诊断 HTTP 已迁入 scheduler/httpapi；完整选择编排、诊断核心、依赖门禁夹具、全量和进程验收继续实施，roadmap 仍为 7/17。

### S07.2—S07.4 · 2026-09-14 · 诊断、候选 Lease 与通用选择

诊断的分组汇总、候选过滤编排、分数/公式、Top-K 概率、设置来源与策略解释已迁至 `scheduler.DiagnosticService`，HTTP 路由直接绑定 `scheduler/httpapi`。旧入口只投影数据及平台资格端口；关联表只在当前调用存在，核心不接收凭据，且同 ID 的不同读取快照不被合并。[诊断核心定向 unit](baseline/S07/diagnostic-core-unit-2.result.json)取得 83 条通过事件；[HTTP/路由](baseline/S07/diagnostics-http-contracts.result.json)15 条通过。

`Lease.Select` 接管排序后候选的槽位获取、fresh/DB 复核、容量变化重获与失败补偿；协议复核以 `SelectionInput.RoutePlan` 和独立 AccountSnapshot 逐次调用 routing。用户租约通过请求 context 传递给后续账号与串行资源，Qoder 完成释放不变。[收紧后的选择链 race](baseline/S07/attempt-selection-focused-race.result.json)184 条通过。

一次过宽的定向正则还选中了旧 Compact SSE 并行测试，触发 Gin 全局 SetMode 竞争。原夹具文件与 S06 HEAD 完全一致，属于清单外验证限制，仅归档，不扩展复现或修改这些测试。[证据](baseline/S07/incidental-gin-race.json)保存原失败日志、源文件摘要，以及[协议消费者普通测试](baseline/S07/protocol-consumers-normal.result.json)259 条通过结果；没有把该 race 失败计为通过。

账号 RPM 三区与缓冲规则归 scheduler/policy，会话 Register/Finish 保留成功/可结算部分结果的空闲窗口。费用窗口三区、取时、批量读取与回填归 billing，原标准费用口径和 Redis 缓存继续复用；仅缓存缺失时计算当前窗口并查询原 usage 只读端口。[RPM/会话/租约 race](baseline/S07/session-rpm-leases-unit-race.result.json)116 条、[费用窗口与诊断 race](baseline/S07/window-cost-migration-unit-race.result.json)65 条通过。

通用网关的路由优先候选、粘性、负载选择、等待计划、单平台/混合选择迁入 `scheduler.GenericSelector`，所有排序与评分复用已有纯实现，旧入口只转换执行账号形状。[负载选择定向 unit](baseline/S07/generic-selection-binding-unit.result.json)741 条、[加入单平台/混合与 SelectOnly 后](baseline/S07/generic-routes-migration-unit.result.json)793 条通过。OpenAI 硬绑定/订阅优先与剩余装配继续收口；全量验收尚未执行，roadmap 不变。

### S07.2—S07.4 · 2026-09-14 · 平台选择、共享状态与最终装配

OpenAI/Grok 选择的 previous-response/guardian/sticky、订阅优先池、基础/高级负载选择、预算内 fresh/DB 复核与回退已迁至 `scheduler.PlatformSelector`；通用和平台路径使用同一纯评分、排序、反馈及 Lease 资源契约。旧平台入口仅提供资格、观测和执行账号投影，Grok 专属免费额度观测、上游交换及取消语义留 S09/S11。[高级选择 unit](baseline/S07/platform-selection-binding-unit-2.result.json)277 条、[基础选择 unit](baseline/S07/platform-basic-binding-unit.result.json)323 条通过。

app 明确持有唯一反馈、动态参数和粘性统计实例，Wire 直接构造新快照、并发和队列；旧构造签名保留，生产使用 app 注入。[共享状态消费者 unit](baseline/S07/shared-state-unit.result.json)320 条、[实际生产绑定 race](baseline/S07/scheduler-state-contract-race.result.json)1 条通过。

`SessionAttempts` 用实际成功、可结算部分结果或失败完成 AttemptLease；取消造成物理槽释放时，不提前改变最终会话保留结果。Qoder 流式仍采用完成释放。[会话与直接消费者 race](baseline/S07/session-attempt-completion-race.result.json)78 条、[最终选择与租约定向 race](baseline/S07/scheduler-final-targeted-race.result.json)360 条通过。

本批只修正迁移引入的回归：应用关闭检查最初误改变已取消请求的无限制槽放行行为，失败断言保存于 [unlimited-slot-cancel-regression](baseline/S07/unlimited-slot-cancel-regression.result.json)，现已区分请求取消与应用停止；Responses 追加请求租约时曾覆盖原生图意图 context，现只向既有 context 追加 Lease。无消费者的私有入口删除，白盒兼容函数进入实际 unit `_test.go`，不保留生产算法副本。未扩大历史问题发现或修复清单。

### S07.0 / S07.4 · 2026-09-14 · 清单、门禁与文档交接

[迁移与交接账本](baseline/S07/migration-ledger.md)、[逐符号所有权与直接消费者](baseline/S07/ownership-ledger.json.gz)覆盖 193 个当前 Go 文件、3,984 个声明和 27 个移出/删除文件。完整静态类型引用分别保存于 [普通](baseline/S07/consumer-refs-normal.json.gz)、[unit](baseline/S07/consumer-refs-unit.json.gz)、[integration](baseline/S07/consumer-refs-integration.json.gz)；涉及本阶段文件的去重引用为 41,291 条。接口结构实现候选与真实 Wire 绑定分开登记，不把静态接口调用当成实际装配证明。

[构建选择](baseline/S07/build-selections.json.gz)已覆盖普通/unit/integration/wireinject/embed/e2e/Darwin/Linux；全部清点文件均有实际构建集合归属。[精确许可](baseline/S07/dependency-permissions.json)保存 32 组新规则及受影响 app 规则、源文件、实际 import 和配置内容。迁出文件没有残留原精确豁免。

[14 组依赖门禁夹具](baseline/S07/dependency-fixtures.json)及[三组叶子反向依赖夹具](baseline/S07/dependency-leaf-fixtures.json)均取得预期 depguard 诊断：合法核心/HTTP/SQL/Redis 通过；同目录新文件、旧文件新增禁止 import、迁出文件、非法子包、核心反向 app、HTTP 直接 SQL、纯叶子反向根包均被拒绝。普通/unit/integration 与其他标签、OS 选择分别验证，临时夹具目录已删除；没有另建门禁或扩大忽略规则。

同步现有调度缓存、网关生命周期、系统架构、路由结算、配置、HTTP 与开发流程文档，保留稳定锚点。旧 service 的平台资格/观测投影、远端会话、usage、Ops、网关和任务消费者分别交接 S08—S13，最终兼容清理交 S15/S16。outbox 低 ID 迟提交仍依赖周期全量重建恢复，十秒清理宽限不保证重新轮询该事件。

<a id="s07_completion"></a>
### S07.0—S07.4 · 2026-09-14 · 完成与验收

五个子步骤已完成，B01—B05 全部取得最终测试事件，S07 交付可审查工作区差异，未自动暂存、提交或推送。[验证汇总](baseline/S07/verification-summary.json)保存命令、版本、退出码、关键测试事件与每个跳过的实际输出；Go 数量包含父子测试，跳过不计通过。

| 验证 | 实际结果 |
| --- | --- |
| 普通全量 `go test -count=1 -json ./...` | 退出 0；11,329 条 pass、0 fail、4 skip |
| unit 全量 | 退出 0；19,368 条 pass、0 fail、8 skip |
| integration 全量 | 退出 0；12,236 条 pass、0 fail、4 skip |
| normal/unit/integration lint | 退出均为 1；诊断为 1 / 285 / 19；[逐文件映射、规则、完整消息与源码行对比](baseline/S07/lint-comparison.json)均与 S06 一致，新增 0、减少 0 |
| B01—B05、原负载测试恢复 | 最终普通/unit/integration 事件全部通过；原 Skip 已移除，原断言保留 |
| 真正进程与资源顺序 | standard/simple SIGTERM、监听失败、精简 JWT、setup 等真实进程通过；调度快照/并发/串行队列各一次启动停止，HTTPRequests 完成后、Redis/Ent 关闭前停止 |
| 定向 race / 真实 Redis 与 PostgreSQL race | 固定修复、快照、选择、等待、会话和共享状态的证据均归档；先前过宽正则命中的旧 Gin 全局 race 单列限制，没有算作通过 |
| 前端门禁 | lint、类型检查及 14 个测试文件、199 个测试通过 |
| 普通、embed、Linux 服务与两个维护命令 | 全部构建通过；跨平台构建只证明可构建性 |
| 真实前端产物下 embed | 101 条通过事件，无失败或跳过 |
| Wire | 仅生成 Wire；再次生成 SHA256 完全相同 |

保留既有真实供应商、外部 TLS/硬件环境及 E2E 限制；Qoder 实际账号、外部 token 估算比较等跳过逐项在汇总中列出，未用本地夹具冒充供应商验证。本阶段没有未完成的必要本地迁移验证。

[最终不变量核对](baseline/S07/final-invariants.json)确认：计划正文前 13,256 字节 SHA256 仍为 `676816011475e4cbbc07081a944075ee025a007749c4988bbb9c71dfe4733dc5`；342 项迁移目录摘要、379 项 Ent 摘要、3,089 项 S00—S06 冻结资料及 48 项其他任务文件均未改变。索引为空、分支仍为 main、HEAD 保持已提交 S06，SYNC.md 未触碰。跟踪文件、暂存区及新增文本文件的 diff 检查通过，受影响文档链接与代码锚点有效。

roadmap 更新为 **8 / 17**、S07 已完成，下一步编写 S08 子计划。回退仅撤销 S07 的代码、规则、Wire 和文档；保留 S06 提交，不涉及数据库或缓存格式降级。回退 B01—B05 会恢复对应重复启停、等待计数、账号槽泄漏、脚本缓存漏报或旧持有者解锁风险。
