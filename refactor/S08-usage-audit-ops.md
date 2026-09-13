# S08：迁移用量查询、审计与 Ops，冻结历史问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `98292d5e50a8f255270910918fa7dab0ce7b929d` 为起点，完成 S08.0—S08.4。

实施前将本计划原样保存为 `refactor/S08-usage-audit-ops.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，清单和脱敏证据保存到 `refactor/baseline/S08/`。不另存 `.agents/plans/`，不改写 S00—S07 冻结资料。

保持 HTTP、金额算法、SQL schema、缓存格式与命名空间、standard/simple 和供应商取消策略兼容。使用 Go 1.27.0、golangci-lint 2.13.2；只生成 Wire，不生成 Ent。不自动提交、推送或切换分支，保留其他任务计划和 diagnostics，不提交 `SYNC.md`。

**问题处理范围已经确认：**

- 历史问题主动排查已在本次计划阶段进行，本阶段只修复下表 B01—B05。
- 计划确认后，仅开展迁移、固定修复和约定验证，不继续寻找其他历史问题或顺带优化。
- 本次改动引入的回归必须修复。
- 验证偶然发现清单外历史问题时，只登记。即使阻塞验收，也暂停相关工作、说明证据并请求调整计划，不自行增加复现或修复范围。
- 保留 S07 outbox 周期重建恢复限制，不扩大多实例支持范围。

## 2. 计划阶段证据与固定修复

原实现复现夹具、日志、源码摘要和查询基线已保存在仓库外的 `/tmp/tokenrouter-s08-planning/`。S08.0 将其归档，不把预期失败的复现测试记为通过。

现有定向测试取得普通 **335**、unit race **317**、integration race **327** 条通过事件，无失败或跳过。批量查询 4 个和 8 个用户/Key 均执行 **1 条 SQL**；用户消费排行的查询与小型 PostgreSQL 夹具 Explain 已保存。这些结果是定向基线，不代替阶段全量验收。

| 编号 | 已复现问题 | 确定修复 |
| --- | --- | --- |
| B01 | 手工回填读取旧状态后，全量保存会覆盖后台推进的实时水位；PostgreSQL 已复现水位回退 | 聚合状态改为命名明确、按字段所有权更新的存储操作。手工请求只修改目标和游标；后台更新不覆盖新手工请求，游标推进和清除按原目标/游标条件更新。短事务锁定状态行，不在聚合 SQL 执行期间持锁，不新增版本列。 |
| B02 | 审计清空先提交 TRUNCATE，随后留痕失败；返回错误时原记录已经删除 | PostgreSQL 提供闭合的清空留痕操作：同事务取得表锁、计数、清空、插入留痕后提交。任一步失败整体回滚；保留原序列行为、管理员鉴权和 TOTP 要求。 |
| B03 | Audit 与系统日志 sink 重复 Start 会产生多个写入协程，系统日志退避状态也随之分裂 | 分别增加统一启停屏障；重复 Start 不产生新 worker，Stop 幂等，停止后不能重开。队列、健康计数和退避状态各自只有一份，不改变正常过载策略。 |
| B04 | 聚合 Stop 不取消支持 context 的在途重算 | 聚合器持有运行 context，停止时取消工作及重试等待，阻止新任务并等待已进入的工作。清理等待受应用剩余预算约束，超时报告未完成项。 |
| B05 | 用量清理删除一批后取消，没有触发聚合重算；PostgreSQL 中原始记录剩 1 条而聚合仍为 2 条 | 把已发生删除后的重算登记集中到任务收尾，覆盖成功、取消及已删除后的中断/失败路径。重算独立于已取消的任务 context，受运行时管理；取消状态保持取消，不标记清理成功。保留异步修复方式，不新增持久修复队列或崩溃恢复承诺。 |

B02 只改变必须留痕的审计清空操作。普通管理员审计继续采用原有尽力异步写入；Ops 系统日志清理的尽力留痕、支付专项审计及退款事务保持各自原有保证。

## 3. 迁移步骤与接口决策

### S08.0：冻结输入和所有权

记录 HEAD、索引、工作区、工具版本、源码摘要、SQL checksum、Ent/Wire 摘要和 S07 完整验证结果，归档规划阶段证据。

逐文件、逐符号登记目标、生产消费者、测试标签、Wire、文档锚点、共享实例及退出阶段。覆盖：

- usage 实体、写入批处理、查询、Dashboard、聚合状态、回填、清理和查询缓存。
- audit 写入、脱敏、保留期、中间件、查询及清空。
- Ops 采集队列、运行设置、指标、告警、报告、清理、实时连接、升级查询和维护命令。
- S05—S07 遗留的用户/Key/团队用量读取、账号统计和 billing 窗口统计来源。

混合文件按职责拆分，不根据 `usage` 或 `ops` 文件名前缀整体搬迁。

### S08.1：usage 写事实、查询与聚合

**模型与写入**

- usage 拥有记录值类型、请求类型、查询过滤、统计口径、写入结果和错误；原 `pkg/usagestats` 按需保留别名。
- 新记录不包含旧 User、APIKey、Account 等完整实体。查询关联数据使用独立展示投影；旧 UsageLog 形状通过转换兼容。
- 明确区分已插入、重复、确定未写入及结果不明。保留原批量窗口、容量、取消等待、死锁重试、冲突处理和同步兜底。
- PostgreSQL Adapter 保留现有 Ent 事务 context：事务内写入使用原连接，不进入异步批处理，不自行提交。
- 供应商 usage 归一化、请求 ID 选择、资金结算和 `UsageRecordWorkerPool` 的完成任务仍留旧网关。日志失败不能再次扣款，结算失败仍保存原有未结算记录。

**查询与缓存**

- 用户、管理员、公共用量、Dashboard、排行及导出迁入 usage 用例和 HTTP Adapter。
- 保留付款主体与行为成员、用户费用与账号成本、原始客户端模型与内部模型、删除用户展示、分页前排序及各接口过滤差异。
- 保留现有批量 SQL。identity 的用量排序通过存储查询参与接口组合到原主查询；团队统计和 Key 最近使用 IP 改绑 usage 的批量投影，不改成分页后排序或逐条查询。
- 查询缓存下沉到所属模块，保留各自作用域、TTL、key、singleflight 和 stale refresh。公共缓存机制可提取到 `pkg/querycache`；ETag、304 和 HTTP Header 留在 HTTP 层。跨请求结果提供独立副本，不合并不同数据面的缓存实例。

**聚合、回填和清理**

- 保留旧 Dashboard 与多维 Usage 聚合、UTC 桶、配置时区重分桶、覆盖水位、raw 回退和限频诊断。
- 保留 `[start,end)` 查询，以及现有清理包含结束端点的差异，不统一时间边界。
- 统一预聚合设置控制器迁入 `settings/preaggregation`，保留同一设置键、JSON、15 秒缓存和更新通知；usage、ops 通过各自窄接口读取投影。
- 落实 B01、B04、B05，保留实时/历史回填预算、首轮、锁、游标、分区维护和 retention 周期。
- 去重记录归档由 billing 的命名明确能力承担，保留原“先归档再删除”SQL和调用周期；usage 不拥有资金去重表写权限。
- 清理分析记录不退款，不修改余额、订阅或结算事实。

### S08.2：audit 完整纵向迁移

- audit 拥有审计记录、动作、脱敏、尽力写入、同步持久写入、查询、清空和保留期；SQL 与 HTTP 分别进入 Adapter。
- 脱敏所需账号及支付敏感字段清单由 app 投影注入，保留原键名归一化、递归、非 JSON 省略、请求体大小和凭据遮罩规则。
- HTTP 中间件保留请求体恢复、动作分类、捕获时点和原顺序。身份安全事件通过窄记录接口接入。
- 落实 B02、B03。普通异步审计继续保留原队列容量、批次、丢弃与失败计数。
- 强制持久操作使用闭合存储操作；需要参与外层事务时，仅 Adapter 接收原 Ent/SQL Tx，参与方法不提交、不发布成功副作用。
- 审计清空继续拒绝管理员 API Key，并验证原 TOTP 凭据；不改用另一套 step-up 流程。
- 支付、退款及其他专项审计留所属业务，不能统一转入可丢弃队列。

### S08.3：ops 观测、运行任务与升级查询

- ops 拥有错误/attempt/入口拒绝、系统日志、指标、健康报告、趋势、直方图、SLA、告警、静默、运行设置、清理和实时采样。
- 核心通过窄快照接口读取 scheduler、account、apikey、billing 和网关完成队列健康；不持有其可变服务或完整凭据。
- 主机、cgroup、SQL/Redis 连接池观测放技术 Adapter；业务表查询进入 ops/postgres。技术 telemetry 不认识业务表。
- 保留 hard switch、运行设置刷新、raw/聚合回退、超时降级、错误分类、忽略状态码和安全字段裁剪。
- 系统日志 sink 落实 B03，保留 2 秒起、60 秒封顶的退避、过载丢弃和健康计数。入口拒绝聚合保留容量、批次重试和关闭报告。
- 告警、报告和清理保留周期、Cron 时区、持续触发、静默、邮件限流、leader/advisory lock、维护锁及 heartbeat。邮件和备份通过窄接口转接，留 S10/S14。
- 实时采样与订阅由 ops 持有；WebSocket 升级、认证、Origin、帧和写超时留 HTTP Adapter。保留按需启动、空闲停止、关闭码及连接退出等待。
- GitHub release 查询、版本比较和缓存迁入 ops/provider 及对应核心。下载替换二进制、执行回滚、系统操作锁和重启编排留旧维护用例，S14 退出。
- `cleanup-ingress-reject-logs` 的分类与清理用例归 ops，命令使用精简装配；保留 dry-run 默认值、参数、输出和分类版本，不启动完整 worker。

网关 HTTP Context 的捕获、供应商错误解析及重试判断仍留 S09/S11；只把明确的观测输入投影给 ops。

### S08.4：装配、HTTP、门禁与文档

- 路由直接绑定 usage、audit、ops 的新 handler，保留 URL、中间件顺序、权限、状态码、JSON、CSV、分页和实时协议。
- app 持有唯一存储、查询缓存、聚合器、审计/日志队列和 Ops 运行实例；配置与跨模块投影在 app 完成。旧入口仅别名、投影或委托。
- 保留 HTTP 5 秒、后台总计 30 秒的退出预算；停止入口和生产者后等待在途及队列，再关闭依赖。超时不等同于 drain 成功。
- 删除已迁出的旧依赖例外；depguard 覆盖核心、HTTP、PostgreSQL、Redis、provider、纯缓存叶子和装配，许可精确到文件/import。
- 同步现有架构、数据生命周期、预聚合、监控告警、HTTP、配置和开发文档，保留稳定锚点，描述实际新旧共存结构。

## 4. 验证与验收

每批执行迁移包、旧委托和直接消费者的普通/unit 测试；存储和缓存使用隔离 PostgreSQL/Redis。验证范围预先固定如下：

| 验证面 | 必须取得的证据 |
| --- | --- |
| 固定修复 | B01—B05 原复现转为通过；回填与实时更新两种交错、清空留痕失败回滚、重复启停、聚合取消、部分清理后的统计重算 |
| 写事实与资金 | 批处理、重复、取消和结果不明；同连接事务回滚；日志失败不重复扣款；删除用量不退款 |
| 查询 | 付款/行为主体、账号成本、模型维度、团队权限、已删除实体、分页排序、CSV 和敏感字段 |
| 聚合 | raw/小时/日组合、UTC/本地日期与 DST、覆盖缺口、回填恢复、清理结束桶及失败回退 |
| 性能与缓存 | 对照规划 SQL 次数和 Explain；批量大小增加不产生 N+1；原 key/TTL、快照隔离及缓存失效 |
| 审计 | 请求体恢复与脱敏、异步过载、同步失败、TOTP、管理员 Key 拒绝、专项审计回归 |
| Ops | 队列满、退避/恢复、容量上限、设置刷新、告警/静默/邮件失败、报告去重、保留期及 dry-run |
| 生命周期 | 构造无启动、唯一实例、停止后不入队、在途等待、实时连接退出、超时报告、Redis/SQL 最后关闭 |

共享状态运行定向 race；固定竞争和事务场景运行 integration race，不扩展全仓 race 或 benchmark。性能夹具只证明对应规模下的查询形状，不宣称生产容量。

depguard 使用可丢弃夹具验证合法方向、精确旧许可、同目录新文件拒绝、旧文件新增禁止 import、迁出例外失效和正常 Adapter；核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 构建选择，保存诊断后删除夹具。

收尾使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

```bash
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

另验证两个维护命令、Wire 再生成无差异、真实前端产物的 embed 测试，以及 standard/simple 启动与 SIGTERM 的观测资源停止顺序。

S07 lint 基线 **1/285/19** 按文件映射、规则和完整消息比较，不能只比较数量。不得扩大忽略规则或削弱断言；跳过、仅编译及真实供应商/TLS/E2E 限制分别记录。

## 5. 完成、交接与回退

完成须同时满足：

- usage、audit、ops 的本阶段生产链使用唯一实现，核心无旧业务、config、Gin 或具体存储反向依赖。
- B01—B05 全部取得修复后行为证据；必要验证未完成时保持“待验”。
- 查询次数、事务、缓存、队列和生命周期保证可核对；未把分析记录或 Ops 事件当作资金提交证明。
- 兼容入口、消费者、构建条件、Wire、依赖许可和文档可追踪，S09—S16 剩余职责明确。
- SQL、Ent、S00—S07 冻结资料及其他任务文件无意外变化；Wire 差异可解释，新增与已有文件的 `git diff --check` 通过。

完成后 roadmap 更新为 **9 / 17**、S08 已完成，下一步编写 S09 子计划。

按子步骤回退代码、装配、规则和文档，不涉及数据库或缓存格式降级。回退 B01—B05 会恢复对应水位覆盖、审计链断裂、重复 worker、停止等待及聚合滞后风险，执行记录分别说明。交付可审查差异，不自动提交。

## 执行记录

### S08.0：实施输入已冻结

- 2026-09-14：实际 HEAD 与计划一致，索引为空；保留既有未跟踪计划和 diagnostics。
- 原计划正文的长度与 SHA-256 见 [start.json](baseline/S08/start.json)，后续只追加执行记录。
- 源码、SQL、Ent/Wire、S00—S07 冻结资料与其他任务内容摘要见 [source-checksums.json.gz](baseline/S08/source-checksums.json.gz)。
- 已归档 [规划复现与查询证据](baseline/S08/planning/manifest.json)；固定清单 B01—B05 不扩展。

### S08.1 / S08.2：基础实现与首批验证

- 2026-09-14：统计值、请求类型、写入结果、排行规则和预聚合设置控制器已迁入 usage / settings/preaggregation；旧记录保留独立投影，不整体别名递归实体。
- usage 查询、Dashboard、聚合/清理以及 PostgreSQL SQL 和批处理迁入唯一实现。去重归档 SQL 迁入 billing/postgres，保留单条 SQL 的先归档后删除及原调度周期。
- B01 的状态更新按职责应用并在 PostgreSQL 短事务内锁定状态行；B04 使用运行 context 取消在途聚合；B05 在清理统一收尾登记已有删除的重算。定向 race [aggregation-fixed-unit-race.result.json](baseline/S08/aggregation-fixed-unit-race.result.json) 取得 113 条通过事件。
- 迁移存储的普通定向测试 [usage-storage-normal.result.json](baseline/S08/usage-storage-normal.result.json) 为 186 条通过；迁入新包的真实 PostgreSQL 测试 [usage-storage-integration.result.json](baseline/S08/usage-storage-integration.result.json) 为 180 条通过，无失败或跳过。
- audit 核心、SQL、HTTP 中间件/管理入口已迁移；清空与留痕同事务，重复 Start 由统一屏障阻止。原脱敏及 HTTP 等定向 race [audit-contracts.result.json](baseline/S08/audit-contracts.result.json) 取得 18 条通过。
- 尚未达到阶段完成标准：usage HTTP/查询缓存、跨模块读取改绑、Ops 全链、门禁、文档和全量验收继续实施；不以以上定向结果替代最终验收。

### S08.1—S08.3：固定修复行为证据与观测实现迁移

- 2026-09-14：B01/B02/B05 的真实 PostgreSQL 回归取得 [3 条通过事件](baseline/S08/fixed-postgres-race.result.json)，验证回填状态两种交错、清空留痕失败回滚以及取消清理后的统计重算与余额不变。B03 两类 writer 的重复启动回归及 B04 取消等待继续使用已归档测试。
- usage 用户/管理员 HTTP 与 DTO、Dashboard 查询缓存已进入所属模块；`pkg/querycache` 只拥有 TTL/singleflight/副本机制，HTTP 保留 ETag 和 304。缓存及直接消费者 [189 条定向 race 事件通过](baseline/S08/usage-query-cache-unit-race-2.result.json)。
- Ops 查询、设置、告警、报表、聚合、清理与主机采集编排进入 `ops`，SQL/Redis/日志后端/主机观测分别进入 Adapter。原维护锁、周期、退避、通知与清理失败语义保持；没有加入清单外历史修复。
- Ops 原存储测试已迁入新包，真实 PostgreSQL [42 条 integration race 事件通过](baseline/S08/ops-store-integration-race.result.json)，无跳过。源码中的混合 outbox 测试继续留在原仓储测试文件，未因文件名整体迁移。
- 实时采样、连接计数与空闲停止由 Ops 的唯一运行实例持有；HTTP 保留 Origin、握手和帧。发布查询、版本规则和缓存进入 Ops，旧 UpdateService 继续执行二进制替换/回滚。更新查询、Ops 和直接消费者 [354 条 unit race 事件通过](baseline/S08/ops-runtime-unit-race.result.json)。
- app 已登记新 Ops provider、独立 Options 和原资源的生命周期，旧入口复用同一实现；仅重新生成 Wire。用户/Key/团队跨表用量读取开始改绑 `usage/postgres/query`，保持调用方连接与原主查询。
- 当前仍为实施中：错误采集队列、公共用量入口、剩余缓存/查询交接、依赖门禁、文档和全量验收尚未全部完成。上述定向结果不代替阶段验收；所有中间编译错误保留日志并由后续成功结果覆盖，不计为既有问题。

### S08.1—S08.4：生产绑定、门禁与约定验证完成

- 2026-09-14：公共 `/v1/usage` 与 Antigravity 用量入口、用户/管理员用量及 Dashboard、audit、Ops HTTP 已接入唯一新实现。原 gateway 的资金完成、供应商解析及取消/重试仍留所属 S09/S11；未迁支付专项审计与退款事务保持独立。
- 用户/Key/团队读取使用 `usage/postgres/query` 的原连接参与；账号和窗口统计由 app 对新 usage 直接投影。原批量 4/8 用户和 Key 各执行一条 SQL，消费排行一条 SQL，规划夹具 Explain 节点不变；见 [查询形状对比](baseline/S08/query-shape-comparison.json)。这不代表生产容量。
- 错误采集队列、系统日志、运行设置、实时采样、聚合器与各缓存均由所属模块唯一持有。app 绑定新生产实例并保留原启动与停止顺序；原 handler 仅提供消费接口/投影。完整边界见 [资源账本](baseline/S08/lifecycle-ledger.md)。
- 400 个当前 Go 文件、5338 个声明，以及 236 个原文件、4302 个声明已登记；130 个原文件删除/搬迁。129 个当前文件为测试；混合文件中的保留声明不计作 S08 迁移完成量。[逐文件索引](baseline/S08/file-index.md)、[逐符号账本](baseline/S08/ownership-ledger.json.gz)、[40219 条合并静态引用](baseline/S08/consumer-edges.json.gz)与 [342 个结构接口实现候选](baseline/S08/interface-implementations.json.gz)可核对。动态生产绑定以 Wire/app 引用为准，测试替身不算生产实例。
- 依赖规则按核心/HTTP/SQL/Redis/provider/纯缓存叶子限制，精确历史文件/import 的基础禁止项保持。[14 次依赖夹具验证](baseline/S08/dependency-fixtures.json)全部符合预期，覆盖普通/unit/integration/wireinject/embed/e2e/Darwin/Linux，夹具已删除。SQL Adapter 不反向取得 Redis 客户端，Redis Adapter 不取得 SQL；嵌套新文件也不能继承旧许可。原 service/handler 与 protocol 规则保留。
- 已同步架构、数据生命周期、预聚合、监控告警、HTTP、配置和开发文档；[链接与代码锚点核验](baseline/S08/document-links.json)无失效目标。

### 最终验证与基线比较

- 2026-09-14：全量普通、unit、integration 串行执行，实际通过事件分别 **11338 / 19377 / 12251**，失败为零；跳过 **4 / 8 / 4**，按测试名与 S07 相同，完整原因见 [skips.json](baseline/S08/skips.json)。数量包含父子测试事件，未把跳过、仅编译或无测试文件的包算作行为通过。
- 全量 lint 为 **1 / 285 / 17**，按路径映射、规则及完整消息比较无新增；见 [lint-comparison.json](baseline/S08/lint-comparison.json)。两项原 integration errcheck 因构造器返回具体 Store 后删除冗余类型断言而消失；原行为断言保留，未扩大忽略规则，六项原 unit depguard 违规继续独立记录。
- 常规服务、Linux、真实前端产物下 embed 及两个维护命令均构建通过；前端 **14 个文件 / 199 个测试**通过，embed 测试 **101 条通过事件**。Wire 重复生成 SHA256 完全一致，仅 `internal/app/wire_gen.go` 为预期生成差异，没有 Ent 生成。
- standard/simple 真正启动及 SIGTERM 的 [10 条进程级事件](baseline/S08/s08-process-observation-contracts.result.json)通过，检查观测 Hook 只执行一次、HTTP/生产者/队列/依赖的顺序、UsageCleanup 先于聚合器、错误队列先于系统 sink。
- B01—B05 的原失败复现与修复后真实 PostgreSQL、定向 race 证据见 [固定问题说明](baseline/S08/fixed-issues.md)。真实缓存/查询/固定竞争的最后组合取得 [12 条 integration race 通过事件](baseline/S08/query-cache-read-contracts-integration-race.result.json)，无失败或跳过。
- 收尾补齐计划已明确列出的审计 HTTP 权限证据，只增加测试，不改变生产代码或门禁。管理员 Key 拒绝、缺失身份/验证码、TOTP 未启用/错误、留痕失败和成功的原契约，普通/unit/integration 定向 race 各 **15 条通过事件**，对应 lint 为零。全量结果与补验分别记载，没有虚报全量重复执行或重复累加。
- 全部命令、退出码、耗时、日志及实际事件见 [验证摘要](baseline/S08/verification-summary.md) 和 [机器可核对结果](baseline/S08/verification-summary.json)，契约事件见 [矩阵](baseline/S08/contract-matrix.json.gz)。中间迁移失败保留并由后续成功记录覆盖，不作为既有生产问题；规划预期失败不计通过。
- 真实供应商/E2E、硬件 Passkey、外部 TLS 与原 WS/SQLite 夹具限制仍单列，未对生产账号或环境进行验证。S07 outbox 迟提交事件仍依赖周期重建恢复，不宣称逐事件严格消费；B05 不新增持久修复或崩溃恢复保证。

<a id="s08_completion"></a>

### S08 完成与交接

- 2026-09-14：S08.0—S08.4 完成，roadmap 更新为 **9 / 17**，下一步编写 S09 子计划。阶段交付是可审查工作区差异，**没有自动提交、推送、切换分支或修改真实暂存区**。
- 本阶段历史问题只修复冻结的 B01—B05；没有开展新的历史审计、扩展故障注入或清单外修复。S09—S16 的供应商、通知、网关完成、支付审计、任务、维护与兼容入口退出项见 [迁移及交接账本](baseline/S08/migration-ledger.md)。
- [最终完整性检查](baseline/S08/final-integrity.json)确认 SQL 342 份、Ent 379 份、S00—S07 冻结资料 3403 份、初始其他任务 48 份无变化；计划原文前缀 SHA256 保持。实施中出现的两个其他任务前端计划仅记录 [外部增量](baseline/S08/external-additions.json)，没有修改或纳入 S08 差异。
- 现有与新增文件的 diff 检查均通过；新增资料使用仓库外临时索引检查，真实暂存区仍为空。Wire 差异全部来自新 provider/类型及实际唯一实例绑定，重复生成无变化。
- 回退按模块撤销 S08 代码、装配、规则、文档与 Wire 差异，保留原工作区及前阶段资料。回退 B01—B05 分别恢复水位覆盖、清空留痕断裂、重复 worker、停止取消不足及部分清理统计滞后风险；不涉及数据库或缓存格式降级。
