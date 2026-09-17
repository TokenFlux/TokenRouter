# S13：迁移创作台与批量图片任务，冻结历史问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `611eec21439efff507fd7215158fde84786a4f6e` 为起点，完成 S13.0—S13.5。

实施前将本计划原样保存到 `/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S13-creative-batchimage.md`，再登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，证据放入 `refactor/baseline/S13/`；不另存 `.agents/plans/`，不改写 S00—S12 冻结资料。

已核实：

- Go 1.27.0、golangci-lint 2.13.2、Docker 29.5.2 可用。
- 规划期间 13,842 个已跟踪文件摘要未变，索引和工作区保持原状。
- 定向普通测试 2 条通过；unit race 360 条通过、1 条失败，失败来自下表 T01；真实 PostgreSQL 任务及资金 integration race 19 条通过，无失败或跳过。事件包含父子测试，不能相加或代替全量验收。
- 原实现复现、仓库外 Go overlay、日志及摘要保存在 `/tmp/tokenrouter-s13-planning/`，实施时归档。
- **全程串行，不使用 subagent。** 不自动提交、推送或切换分支，保留其他任务文件，不提交 `SYNC.md`。

保持 HTTP、配置键、数据库 schema、缓存键与存储格式、历史任务、金额算法及 standard/simple 契约。只生成 Wire，不生成 Ent。

**问题范围已经冻结：**

- 用户已确认 B01—B05、T01 全部纳入。
- 执行阶段只进行迁移、固定修复和约定验证，不继续寻找历史问题或顺带优化。
- 本次改动引入的回归必须修复。
- 偶遇清单外历史问题，只保存当次证据并登记；即使阻塞验收，也暂停相关工作并请求调整计划，不自行追加复现或修复。
- 保留单服务进程部署边界，不新增跨数据库/Redis 原子提交、供应商恰好一次执行或未记录结果的崩溃恢复承诺。

## 2. 固定问题与修复决策

| 编号 | 已复现问题 | 本阶段修复 |
| --- | --- | --- |
| B01 | 创作台、批量图片 runtime 在 Stop 后仍可 Start | 统一不可逆生命周期屏障，重复 Start 幂等，停止后不再启动 worker、恢复循环或动态扩容 |
| B02 | 批量图片第一次 Stop 仍等待在途工作，第二次 Stop 已返回成功 | 所有停止调用共享完成信号与首次结果；等待受调用方及应用剩余预算约束，超时报告未完成项 |
| B03 | 批量清理 Start 后立即 Stop，后台读取已清空的 `done`，触发 `close of nil channel` | 启动闭包持有本次运行的固定 context/完成信号；停止不提前清空运行状态，不允许重开 |
| B04 | 批量锁续期忽略 token 不匹配；旧 ACK 删除接管者 active 记录，真实 Redis 已复现 | 复用现有锁 token，明确返回所有权状态；失去所有权取消处理，队列写入原子校验 token，禁止旧 worker ACK、重排或续心跳 |
| B05 | 创作台上游已成功，Redis 保存输出失败后再次调用上游，实际生成次数为 2 | 成功后只恢复结果保存与结算，不重新推理；无法交付标记 `result_lost`，按已确认成功输出计费 |
| T01 | 并行创作测试包装只保护部分仓储方法，继承的状态更新发生 race | 补齐测试替身共享数据的同步与必要副本，保留真实并行执行及原业务断言 |

B01、B02、B05 的仓库外回归在原实现 race 模式三轮均失败；B03 已捕获 panic 和竞争诊断。B04 的两个真实 Redis 场景三轮均失败。它们是问题证据，不计为测试通过。

### B04 的边界

- 领取成功后取得的锁句柄拥有续期、心跳、ACK、重排和释放能力；已取得所有权的队列修改由 Lua 比较现有锁 token 后执行。
- 续期返回不匹配即视为失去租约；Redis 故障导致无法确认继续持有时，停止本轮推进，交给现有恢复机制。
- processor 在供应商返回、结果索引及状态/资金推进前响应取消，不把失去租约当作供应商失败。
- 锁冲突路径不得移动有效持有者的 active 记录；孤立、过期任务继续由现有 stale recovery 接管。
- 保留现有 key、字符串 token、TTL 和队列结构；不将这些检查描述为 Redis 与 PostgreSQL 的分布式事务。

### B05 的结果与资金规则

用户已选择：**不重新推理，已成功服务仍计费。**

- 将已确认的供应商成功与可交付成功分开。复用现有成功时间、账号、输出元数据、状态和 outbox，在 PostgreSQL 闭合操作中记录成功事实；不保存图片、prompt 或原始供应商报文。
- 当前 worker 在原租约内持有输出字节，保存最多尝试三次、间隔一秒，总预算五秒；只重试保存，不重新执行供应商请求。应用停止或租约丢失终止保存。
- 成功事实未落库时，只允许重试记录事实，不重新进入生成分支；持续数据库故障报告未完成。未持久化事实前发生进程崩溃，仍不承诺恢复已发生的上游结果。
- 成功事实已落库后，恢复仅处理输出与结算。明确缺失/保存耗尽时，按成功输出数捕获一次资金并进入 `result_lost`；Redis 暂时不可用不能直接解释为永久丢失。
- 可交付终态必须同时满足结果可读取与结算完成。已有取消竞争保留 `cancelled`，仍按已确认服务计费；不回写成 `succeeded`。
- 原断言中“不能假成功”继续保留；只调整经本次授权改变的失败恢复预期。

## 3. 迁移步骤与接口

### S13.0：冻结输入与逐链清点

记录实际 HEAD、索引、工作区、工具版本、源码摘要、SQL checksum、Ent/Wire 摘要和 S12 完整诊断，归档规划证据。

逐文件、逐符号登记任务实体、状态机、HTTP、队列、资金、供应商、下载、清理和共享实例，记录生产消费者、测试标签、事务拥有者、Wire、文档锚点及兼容入口退出阶段。混合文件按职责拆分。

### S13.1：creative 完整纵向迁移

- creative 拥有模型目录、创建校验、工作区权限、幂等、任务状态、输出元数据、provisioning、outbox、结果读取/ACK 和恢复用例；PostgreSQL、Redis、HTTP 分别进入 Adapter。
- 核心使用独立 Options、时钟、身份/分组/账号投影，不接收旧实体、完整 config、Gin 或具体数据库客户端。
- 托管 Key 生命周期继续由 apikey 拥有，creative 发出供应意图。保留用户+分组幂等、`creative_studio` 标记、普通管理入口隐藏和原资金来源。
- 保留 JWT 与工作区双重隔离、旧空工作区任务不可见、请求指纹、幂等范围、单张输出、输入大小/MIME/mask 校验、模型白名单及关闭时响应。
- 内容审核直接调用 moderation，保持 `NoMediaRetention`、团队主体及原 fail-open/阻断行为。
- PostgreSQL 继续只保存元数据；素材、prompt 和图片只进入现有 Redis 临时键。TTL、ACK 即删、清理补偿及不跨浏览器恢复的边界不变。
- 落实 B05。provisioning、结果保存、资金动作和队列之间继续通过原状态/outbox 恢复，不引入跨存储长事务。

### S13.2：通用任务资金与事务参与

保留唯一 `billing.Funds.Reserve/Capture/Release`，移除 billing 对两张任务表的了解：

- `TaskReference` 改为通用 scope、任务 ID 与预占动作 ID；具体 scope 由 app 注册，未知 scope 拒绝。旧入口在兼容层完成原零值和任务类型转换。
- billing PostgreSQL Adapter 接收 app 注入的事务参与工厂；工厂使用本次 `*sql.Tx` 创建任务投影参与者，承接预占分配及 `allowance_reserved` 写入。
- creative/postgres、batchimage/postgres 各自实现参与者；不自行 Begin、Commit、Rollback 或执行提交后失效。死锁重试必须重建整个事务与参与者。
- 去重认领、付款用户锁、余额/订阅分配、Key/成员额度和任务资金投影保持原原子范围。投影写入失败整体回滚。
- 保留 v1/v2/v3 快照、原请求 ID、原指纹编码、8/10 位精度、严格指定订阅、差额释放、删除 Key/退出成员及只回退原窗口规则。
- 新 scope/参与信息不得进入历史指纹。历史 reason、动作 ID、日志及兼容字符串保持；代码内部不再以 BatchImage/CreativeEntity 分支决定资金行为。
- 清零后删除旧过渡 Adapter；不得保留第二份资金算法。

### S13.3：batchimage 完整纵向迁移

- batchimage 拥有提交、幂等、任务/条目、provider 绑定、状态查询、取消、队列、结果索引、结算恢复、下载和清理。
- 保留当前“创建与预占 → 上传/提交 → 持久化供应商引用 → 入队 → 轮询/索引 → 结算”的阶段边界，不把网络调用放入数据库事务。
- 保留 Key/付款/行为主体、三层模型身份、价格和资金来源快照；已提交任务不因后来修改 Key、分组或价卡而重算。
- provider 仍限现有 Gemini API Key 与 Vertex Service Account 能力。供应商交换、Batch/GCS/JSONL 原语复用 upstream，任务状态判断留 batchimage。
- 保留取消与供应商成功竞争、部分成功计费、索引去重、结算重放、提交心跳、未提交任务资金恢复和原重试预算。
- 下载返回受控流，由 HTTP Adapter 写出并关闭；保留所有权、JSONL/ZIP、大小/时长/并发限制、manifest、安全文件名和输出删除后的 410。
- GCS/供应商引用只内部使用，清理只接受服务端生成且通过原前缀校验的路径；保留输入/输出留存及原清理审计失败语义。
- 两类任务状态机、素材生命周期和缓存实例保持独立，不建立统一 JobService。

### S13.4：调度、平台与生命周期

- creative 通过 scheduler Lease 完成原非阻塞用户/账号准入；暂时无槽继续短延迟重排，不增加执行次数或重复资金预占。槽位只覆盖生成阶段。
- routing 提供当次候选与模型解析，account 提供受控凭据，egress 提供传输策略；平台 Adapter 调用 upstream。移除对旧 GatewayService 的具体依赖，不通过本地 HTTP 回环调用。
- batchimage 保持任务固化的账号/provider，不在轮询时自动换号或重新提交。
- app 持有唯一服务、存储、provider registry、worker、恢复器和下载限流实例；静态配置在 app 投影，动态设置按原时机读取。
- 落实 B01—B04、T01。创作台缩容仍等待当前任务完成；停止封闭领取和扩容、取消等待及执行，再等待实际在途工作。
- 保留 HTTP 五秒、后台总计三十秒预算；重复停止共享结果，超时列出未完成工作，Redis/SQL 不提前关闭。
- 队列清理使用有界独立 context，且必须仍拥有相应 token；失去所有权不得用清理预算覆盖新持有者。持久未完成任务留待现有恢复路径，不宣称停止时全部完成。

### S13.5：HTTP、装配、门禁与交接

- 路由直接绑定 creative/httpapi、batchimage/httpapi；保持 URL、中间件顺序、JWT/Key 差异、multipart/JSON、错误 reason、分页、下载 Header 及公开字段。
- S11 预留的任务路由入口直接注入新 handler；旧入口仅保留可追踪的别名、投影或委托。
- 同步现有 depguard，覆盖核心、HTTP、PostgreSQL、Redis、平台 Adapter 和 app。删除迁出文件的旧许可；必要许可精确到文件/import，不扩大忽略规则。
- 同步创作台、批量任务、资金、架构、网关生命周期、HTTP、配置和开发文档，明确 B05 的成功事实、交付与计费差异。
- 后续维护交 S14，HTTP/设置聚合交 S15，兼容入口删除和最终验收交 S16。

## 4. 验证安排

所有项目命令使用 `GOTOOLCHAIN=go1.27.0`。每批执行新模块、旧委托及直接消费者测试；存储和资金使用隔离 PostgreSQL/Redis，供应商使用本地 HTTP/TLS/GCS 夹具，不真实生成收费图片。

| 验证面 | 必须覆盖 |
| --- | --- |
| 固定问题 | 停止后重开、重复停止等待、清理立即停止；锁过期接管及旧操作拒绝；保存失败不重复生成；并行替身 race |
| creative | 工作区/用户隔离、幂等冲突、托管 Key、模型能力、审核无留存、provisioning、TTL、ACK、结果丢失 |
| B05 | 成功记录回滚、Redis 保存重试/耗尽、暂时故障与明确缺失、恢复不重新推理、取消竞争、只捕获一次 |
| batchimage | 提交/取消竞争、provider 固定、轮询、部分结果、重复索引、JSONL/GCS、下载限制、手动/TTL 清理 |
| 资金 | 同连接投影失败整体回滚、原动作 ID/指纹、历史快照、三种资金来源、窗口变化、Key/成员删除、重复 capture/release |
| 生命周期 | 构造无启动、唯一实例、动态缩容、停止后不领取、在途等待、预算超时、Redis/SQL 最后关闭 |

共享状态与 worker 执行定向 race，真实存储竞争执行 integration race，不扩展全仓 race 或 benchmark。固定问题测试保留行为断言，屏障不能依赖错误交错必然发生。

depguard 夹具覆盖合法方向、精确旧许可、新文件拒绝、旧文件新增禁止依赖、迁出失效和正常 Adapter；保留诊断后删除夹具。核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 构建选择及真实测试事件。

收尾在 backend 目录串行执行：

```bash
export GOTOOLCHAIN=go1.27.0

go generate ./cmd/server
go test -count=1 -json ./...
go test -count=1 -json -tags=unit ./...
go test -count=1 -json -tags=integration -p=4 ./...

golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 ./...
golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 --build-tags=unit ./...
golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 --build-tags=integration ./...

make build
make -C .. test-frontend
make -C .. build-frontend
go build -tags=embed -o bin/server-embed ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/server-linux-amd64 ./cmd/server
```

另验证两个维护命令、Wire 再生成无差异、真实前端产物的 embed 测试，以及 standard/simple 启动与 SIGTERM 任务资源停止顺序。

S12 lint 基线 **1 / 284 / 17** 按路径映射、规则和完整消息比较。保留既有跳过、外部供应商/TLS/E2E 限制；跳过和仅编译不计行为通过。

## 5. 完成与回退

完成须同时满足：

- 两类任务生产链使用各自唯一实现，核心不反向依赖旧业务、config、Gin 或具体存储。
- billing 不再读取或更新具体任务表，任务资金参与与历史重放取得真实事务证据。
- B01—B05、T01 全部通过；原失败资料保留，没有新增推理重试或资金实现。
- HTTP、临时素材、任务恢复、下载与有界停止契约可核对；必要验证未完成时保持待验。
- SQL、Ent、S00—S12 冻结资料及其他任务文件无意外变化；Wire 差异可解释，新增与已有文件的 diff 检查通过。

满足后 roadmap 更新为 **14 / 17**、S13 已完成，下一步编写 S14 子计划。交付可审查差异，不自动提交。

按子步骤回退代码、装配、规则和文档，无数据库 schema 或缓存格式降级。回退 B05 前处理或登记成功已确认但尚未交付/结算的任务，防止旧版本重新推理；不得删除成功事实、资金去重或恢复记录。其余固定修复回退会恢复对应启停、等待、panic、所有权及测试竞争风险。


## 执行记录

### 2026-09-17：输入冻结、任务契约与资金参与

- 原样保存本计划，正文 15,662 字节，SHA256 `372f925465bc3567abf04d248f315996c0446f3444ac06a6a567b0f9768e1f3a`；实施 HEAD 与计划一致。原 50 个其他任务文件与空索引保持，规划资料已归档。
- 两类任务值类型/状态规则、元数据存储、Redis 队列与创作台临时存储已进入所属模块。billing 的两类表分支已替换为受控 scope、原预占 ID 和同 SQL Tx 参与工厂；原九项 PostgreSQL 资金 race 通过，任务单测 36 条通过。
- 新增 PostgreSQL/Redis 固定回归五条通过：旧 token 拒绝续期/ACK、成功元数据与 outbox 失败回滚、任务资金投影失败整体回滚。中间编译与迁移适配失败原日志保留，均不算通过。
- B01—B03 的运行拥有者接入应用剩余退出预算；T01 补齐共享替身方法同步。相关三轮 race 27 条通过。B04 持有者队列操作与 B05 结果保存失败不重新生成已接入；固定组合回归 44 条通过，结果用例迁移后的旧消费者 unit 65 条通过。
- 创作台 Results/Funding/Queries 与 HTTP Adapter 已开始接入生产委托路径；创建/目录、平台执行、批量业务编排及最终 app 直接装配仍在实施。尚未完成 S13，不更新完成数量；全程未使用 subagent，不提交。

### 2026-09-17：创建、目录、平台执行与共享装配

- 创作台 Public/Executor 和批量 Public/Pipeline/ProviderProcessor/Settlement/Cleanup/Download 已进入所属核心；原 HTTP 实现分别迁入两个 httpapi。托管 Key 的创建与并发冲突回读归 apikey.ManagedKeys；核心只收到 Key ID。平台报文及执行位于 creative/provider、batchimage/provider，旧入口转接唯一实现。
- 创作创建相关 race 87 条通过；批量用例与 Adapter 185 条通过；创作平台迁移后普通 90 条通过。新交付确认读取暴露并行测试替身未锁 LoadOutput，属于 B05 新读取路径的测试适配；补齐与 SaveOutput 相同屏障及独立副本后，三轮 race 270 条通过，失败日志单独保留。
- app 固定两个 Public 及下载/清理核心实例；批量 HTTP、提交、轮询、下载与清理共用注入的注册表，不再各自构造生产注册表。HTTP 直接消费核心实例；旧认证上下文仍以窄投影转接，归 S15/S16 清理。Wire 首次生成通过，仅生成 Wire。
- 新文件依赖许可已按角色和实际 import 扩充精确账本，正在验证。尚需完成剩余生产桥接清理、实际 HTTP/装配和存储行为、全量验证、门禁夹具、文档及完整清单；S13 保持实施中。

- 任务 HTTP 通过 app 的 TaskRequestsAndDownloads 屏障等待实际调用结束，预算超时不关闭 worker/Redis；真实 HTTP Adapter 关闭测试 race 通过。任务资金生产投影直接取得 app 的 billing.Funds，不再经过 CreativeEntity 旧命令转换；旧形状仅供兼容测试保留。
- 扩展既定直接消费者集合时，旧全链冒烟替身缺少 B05 新增 ProviderOutcomeStore，进入成功事实重试；保存栈后终止该测试并补齐新端口及错误元数据字段，不修改原断言。该中间退出不算通过。

### 2026-09-17：增量门禁与最终验收启动

- 两类任务的资金、生命周期与回退账本见 [funds-and-lifecycle.md](baseline/S13/funds-and-lifecycle.md)。混合文件与目标职责记录于 migration-files.json，逐符号和真实引用账本按普通/unit/integration 生成。
- 普通全量预验 11,755 条通过、4 条跳过，unit 全量预验 19,793 条通过、8 条跳过；后续任务纯规则移交与 HTTP 契约分别完成定向补验。真实 PostgreSQL/Redis 任务与资金 race 25 条通过，无失败/跳过。
- 三套 lint 按完整消息比对 S12：1 / 283 / 17，无新增。B05 修改的测试断言使用受检查的类型断言，原同文件 1 项 errcheck 消失；不扩大忽略规则。中间新增诊断与修复前日志均保留。
- 普通/unit/integration 的 51 项可丢弃 depguard 夹具全部匹配预期，夹具无残留；Wire 再生成 SHA256 不变。最终串行测试、构建、构建集合、引用清单与保护文件核对仍在执行，roadmap 暂不标完成。

- 最终 integration 首轮在新增 S13 进程顺序断言处失败：TaskRequestsAndDownloads 与既有 HTTPRequests 同属阶段 15，日志顺序不固定；其余集合无新增失败。此为本次新增装配顺序问题，已将任务屏障改为阶段 16，保证 HTTPRequests(15) → 任务屏障(16) → worker(20)。保留断言与失败日志，执行定向及全量重验，不归为历史缺陷。

<a id="s13_completion"></a>
## S13 完成与交接（2026-09-17）

S13.0—S13.5 已完成，生产任务使用各自唯一核心、存储、HTTP 与平台实现。app 固定运行实例和共享 provider registry；任务资金直达唯一 billing.Funds，任务表写入由同 SQL Tx 参与者承担。旧入口只保留已登记的形状投影和测试兼容，不新增缓存、推理重试或资金实现。

| 子步骤 | 完成证据 |
| --- | --- |
| S13.0 | HEAD/索引/源码摘要冻结；原计划 15,662 字节与原 SHA256 保持，规划失败材料已归档。 |
| S13.1 | creative 的创建/目录、工作区、幂等、托管 Key、结果与恢复、HTTP/存储/平台 Adapter 已改绑；B05 事实、交付、取消与一次资金效果取得证据。 |
| S13.2 | 通用 scope + 原预占 ID，同连接参与工厂、整段重试、精度/窗口与历史重放验证；billing 生产实现不再引用两张任务表。 |
| S13.3 | batchimage 提交、固定 provider、轮询/索引、结算、下载、清理及原阶段补偿已迁入唯一实现。 |
| S13.4 | scheduler.Lease 管理生成阶段资源；B01—B04、T01 及有界关闭通过。HTTPRequests(15) 后等待任务屏障(16)，然后停 worker(20)，最后释放依赖。 |
| S13.5 | 新 HTTP 直接接入，51 项依赖夹具匹配；文档、标签、Wire 和真实符号/消费者账本齐备。 |

最终普通全量 **11,757** 条通过 / 4 跳过；unit 全量 **19,797** / 8；integration 全量 **12,732** / 4。额外 B04 processor race 三轮 **12** 条、B05 交付契约 race **7** 条、真实存储任务 race **25** 条、进程回归 **10** 条、真实产物 embed **106** 条通过。集合有重叠且含父子测试，不能相加；跳过不算行为通过。三套 lint 为 **1 / 283 / 17**，与 S12 比较无新增；一项旧测试 errcheck 在 B05 断言调整中消失。

后端、前端 lint/typecheck/约定测试与构建、embed/Linux 和两个维护命令均通过。Wire 再生成无差异，8,176 个保护文件无变化，原 50 个其他任务文件保留，新增与已有文件 diff 检查通过。原失败、迁移中间失败及补验日志完整保留；本次新增关闭阶段顺序问题已修正，没有清单外历史问题修复。

详细记录见 [证据目录](baseline/S13/README.md)、[完成证据](baseline/S13/completion-evidence.json)、[契约矩阵](baseline/S13/contract-matrix.json)、[资金/生命周期及回退边界](baseline/S13/funds-and-lifecycle.md)。真实收费供应商、外部 TLS/E2E 环境限制沿用已登记范围，不扩大部署或崩溃恢复承诺。

交接：S14 继续备份、初始化和维护；S15 收敛 HTTP、设置聚合与旧构造；S16 删除清单中保留的旧签名、上下文和测试转接并做最终验收。当前未提交，未推送，未切换分支，未使用 subagent；下一步编写 S14 子计划。
