# S16：删除旧包、收敛装配与最终验收

## 1. 基线与执行边界

以当前 `main`、HEAD `bb3eaa22ced655dbac5c03126836ff2d160c976f` 为起点，完成 S16.0—S16.7。

实施前将本计划原样保存为：

`/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S16-final-cleanup.md`

随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，证据保存到 `refactor/baseline/S16/`；不另存 `.agents/plans/`，不改写 S00—S15 冻结资料。

已核实：

- 项目 Go 1.27.0，系统 Go 1.27.1，golangci-lint 2.13.2，Docker 29.5.2。
- 当前索引和已跟踪工作区干净；规划期间 **15,201 个已跟踪文件摘要不变**，保留原有 50 个其他任务文件。
- 旧 service/repository/handler 下仍有 **900 个非测试 Go 文件**；其中包括转接、装配、投影及仍承担实际工作的适配实现，不能一律当作空壳删除。
- `app/legacybridge` 还有 30 个文件；13 个 Ent schema 文件引用旧 domain/model。
- 规划证据位于 `/tmp/tokenrouter-s16-planning/`，实施时归档。

**全程串行，不使用 subagent；不自动提交、推送或切换分支，不提交 `SYNC.md`。**

保持 HTTP、协议、配置、数据库结构、缓存键与版本、金额算法及 standard/simple/setup/embed 契约。本阶段按总计划修改 Ent 手写类型引用并重新生成 Ent，同时生成 Wire；不手改生成结果，不修改已发布 SQL，不升级依赖。

历史问题排查到本计划结束为止。执行阶段只开展迁移、下列固定修复和约定验证；迁移引入的回归必须修复。清单外历史问题只保存当次证据并登记，即使阻塞验收，也先请求调整计划，不自行追加复现或修复。

## 2. 固定修复与规划证据

| 编号 | 已确认事项 | 本阶段处理 |
| --- | --- | --- |
| B01 | 并行测试反复调用 `gin.SetMode`，竞争 Gin 全局状态 | 在对应测试进程运行测试前统一初始化模式，移除测试中的重复设置。保留 `t.Parallel`、原业务断言和协议行为，不修改生产 Gin 行为。 |
| L01 | S15 普通/unit/integration lint 为 **1/283/17**，去重后 299 项 | 按用户选择全部清零。以已归档文件、规则和消息固定范围，逐项记录修复或迁移后的归宿，不扩大忽略规则。 |
| E01 | `test-e2e` 引用不存在的脚本，`test-e2e-local` 仍指向旧目录 | 迁移测试目录后，让 `test-e2e` 委托现有 Go E2E 入口，保留环境变量、标签和超时；不新增供应商环境部署脚本。 |

规划验证：

- 普通边界测试 **677 条通过事件**，无失败或跳过。
- 原定向 unit race **1,517 条通过、28 条失败**，捕获 15 份 Gin 模式竞争报告，不能记作整组通过。
- B01 最小原实现复现保留；仓库外覆盖层统一初始化后，两个场景三轮执行共 **6 条通过事件**。
- 相同覆盖层下，原定向 unit race 集合 **1,545 条全部通过**。
- 真实 PostgreSQL/Redis 事务、任务资金与缓存定向 integration race **11 条通过**，无失败或跳过。

事件包含父子测试，集合存在重叠，不求和、不代替最终验收。

L01 的处理方式固定：

- 错误返回按测试语义检查；预期失败明确断言，不批量改成忽略错误。
- HTTPS 代理测试信任本地测试证书，保留 CONNECT 与目标握手失败断言，不通过跳过验证或新增豁免消除 gosec。
- nil context、非规范 Header 键等兼容测试保留原输入覆盖，以明确测试用例表达，不能换成普通输入后删掉原契约。
- 历史支付密文使用固定测试向量验证解密，不删除兼容测试来消除 deprecated 诊断。
- 无消费者的辅助代码可删除；有效测试必须迁移或由同等断言替代，留下对应关系。

## 3. 实施步骤与接口决策

### S16.0：冻结最终清理账本

记录实际 HEAD、索引、工作区、工具版本、源码、SQL、Ent/Wire 摘要，归档规划资料与 S15 完整验收结果。

以总计划 112 个旧目录和后续新增文件为底稿，建立逐文件、逐符号账本，记录：

- 最终所有者、实际生产及测试消费者、构建条件；
- 实现、转接、装配、缓存编码或测试夹具的性质；
- 共享实例、事务参与、资源关闭、Wire 与文档引用；
- 删除前验证和最终替代位置。

不得只凭文件名、注释中的“已迁移”或 `Legacy` 名称判定可以删除。

### S16.1：消除旧值类型、工具入口与 Ent 源引用

- domain/model 的协议、能力、调度策略、公告、资金、TLS 和错误规则消费者直接使用所属模块类型。先改消费者与 schema，再删除无引用的别名和常量转接。
- Ent schema 使用 protocol、routing、scheduler/policy、identity、billing、site、promotion、egress 等实际所有者的类型及常量；保持字段、默认值、JSON、索引、约束和迁移行为。
- 使用现有命令生成 Ent。核对生成表定义及 schema 描述不变，并验证历史 JSON、事务和软删除行为；不产生 SQL migration。
- 删除旧 pkg 的协议、错误、HTTP、日志、代理、TLS、OAuth、会话、搜索和统计包装，消费者直接引用已有唯一实现。
- `pkg` 最终保留 apperror、pagination、ipmatch、oauthpkce、logredact、timezone、querycache 等通用能力。技术日志事件移入 telemetry 的纯事件子包。
- timezone 的初始化、日志及 `time.Local` 写入归 app/bootstrap；日期计算通过显式 Calendar/Location 注入，保持初始化顺序、默认时区、用户回退和原取时点。
- 删除 `pkg/ctxkey`：身份由原生认证类型承载，业务执行参数进入显式请求状态；telemetry 只保留关联和时延信息。错误数值、错误链、脱敏及自定义 HTTP 状态保持。

### S16.2：收敛实体投影、缓存编码和存储入口

- identity、apikey、account、routing、billing、usage 等消费者直接使用原生模型或窄投影，删除旧 User/APIKey/Account/Group/UsageLog 的往返转换。
- 不新增共享“万能实体”包，不把递归旧实体整体别名到新模块。管理记录、认证身份、选号快照、执行凭据和公开 DTO 继续分开。
- `sched:v2` 完整与轻量 JSON 的编码归 scheduler Redis Adapter，使用明确的存储形状；原字段、nil/空集合、凭据过滤、epoch、tombstone 和 last-used 规则保持。
- scheduler 核心只看到重建元数据和无凭据候选投影；完整账号解码通过受控账号读取接口提供，不向评分、诊断或公开输出暴露完整记录。
- Key v40、授权会话及其他缓存保持新旧载荷兼容。删除转换层不能增加数据库查询、提前读取动态配置或复制缓存实例。
- repository 的剩余转接改为原生存储绑定；HTTP 池的配置投影移到 app，egress 决策、技术执行及 Grok 回退继续由已有所有者承担。
- 所有跨模块写入沿用现有 Ent/SQL 连接。事务参与方法不自行提交或发布成功副作用；配置写入不覆盖消费、健康或运行字段。

### S16.3：清理账号、平台与健康适配残留

按授权/刷新、账号测试、用量查询、健康反馈、传输衔接的顺序处理旧 service 中仍有实际行为的代码：

- 授权会话、凭据缓存、CAS、刷新协调和持久化归 account；供应商交换、签名、原生解析归 upstream。
- 账号测试继续使用 `TestRequest/TestTarget/EventSink`。目标加载与平台适配进入 account/provider，HTTP 只写原 SSE，后台直接消费事件。
- RateLimitService 的残留拆回 account 健康决策、scheduler 反馈及 upstream 错误观测；不整体搬成新的跨模块 Service。
- 原生网关使用已有执行接口。具体平台适配进入 gateway/provider 或相应 upstream；核心不导入具体平台、完整 config 或存储客户端。
- 保持刷新资格、取消、CAS 未命中、代理、缓存、查询时机及结果不明语义。保留唯一 HTTP 池、WS 池、刷新协调器和后台资源。

纯调用和投影由 app 显式绑定；规则、锁和缓存不能转移到新的 app 聚合对象中。

### S16.4：清理网关、HTTP Context、Ops 与任务转接

- 原生 HTTP Handler 直接由 app 构造，不再从旧 GatewayHandler/OpenAIGatewayHandler/QoderGatewayHandler 创建门面。
- 保留 gateway 已有请求/attempt/turn、Lease、执行、输出和完成契约；固定依赖在构造时注入，不恢复逐请求回调拼装或第二套重试循环。
- HTTP 边界读取原生 Principal/AccessSnapshot。认证失败供 Ops 使用的已加载 Key 投影与已认证主体分开，不能因移除旧 Context 混淆授权状态。
- 请求、attempt 和 turn 的模型链、最终分组、资金来源及观测值使用显式独立状态；异步完成仅持有冻结快照。
- 旧 Ops writer、请求捕获及协议输出观察进入 gateway HTTP Adapter；分类和持久化调用原生 Ops。保留 Flush/Hijack、前导、终态错误、脱敏和监控跳过规则。
- creative/batchimage 的构造、registry、资金和下载转接直接绑定原生实例；任务状态机及 billing 的同事务任务投影保持唯一实现。
- 保持 **693 条 method/path**、中间件次序、错误优先级和公开字段；特别回归普通 Key 门禁不读 body、Qoder 断开收尾、WS 多 turn 与 Live 零费用记录。

### S16.5：清零旧应用图与生命周期转接

- app 直接构造各模块、存储和运行时，删除 service/repository/handler ProviderSet、旧聚合服务和已清零的构造器。
- settings 的读取器、缓存、参与者和提交后应用器直接构造并共享同一 Store，不再通过 SettingService 取回原生实例。
- 删除全部 `app/legacybridge`。仍必要的原生模块接口适配留在 app 按职责分文件，只做调用与投影，不改名保留旧图。
- 生命周期直接绑定原生拥有者，删除旧全局绑定和默认实例转接；保存旧、新实例及 hook 对照，证明没有重复创建、遗漏启动或提前关闭。
- 保持 HTTP 五秒、后台三十秒预算，以及构造失败、部分启动失败、监听失败、信号和重启的关闭路径。按需资源不提前启动，超时不报告 drain 成功。
- Wire 按基础设施和模块分组，避免一个新的巨型 provider 集合；生成两次必须无差异。

### S16.6：迁移测试、修正入口并清零 lint

- 单模块测试迁到实际所有者，同包测试继续访问本模块私有实现，不为迁移扩大生产 API。
- 跨模块事务和应用契约放入 `backend/tests/integration`；原 `internal/integration` 的三个 E2E 文件同批移动，保留各自构建标签。
- 业务 fixture/stub 归模块 testkit 或测试文件；通用 Testcontainers、临时资源工具继续共用。明确 miniredis 与真实 Redis 的证据区别。
- 不在测试目录重建整套旧 service。仅删除纯包装重复测试，并记录原断言由哪个原生测试承接。
- 落实 B01 和 L01；普通/unit/integration lint 均须退出零。
- 修正 Makefile 的 E2E 路径和缺失脚本入口，同步实际受影响的 CI、testdata 和相对路径；不恢复已删除的 data-management 守护进程。

### S16.7：最终门禁、资金销项与文档

- 删除所有旧包、空壳及精确历史许可。最终生产、测试、生成代码和全部构建集合均不得 import 已删除路径。
- depguard 保持核心、叶子、HTTP、存储、provider、infra、app 的方向约束；删除旧目录规则时保留对应目标角色限制。
- 对十二组资金写入逐方法销项：记录资金操作所有者、业务编排所有者、事务参与、提交后副作用和回归证据。
- 初始化、恢复和已发布 SQL 保留操作级长期权限；任务资金、退款、Promo、返利、Key/成员/账号消费不存在未登记旧写入口。
- 同步所有受旧路径及所有权变化影响的现有 Project Doc、架构图、开发命令和代码锚点。历史阶段资料中的旧路径保留，由最终映射提供定位。
- 最终报告明确保留的跨模块同事务协作，以及单实例额度协调、outbox 周期重建、Live 计费、外部环境等既有限制。

## 4. 验证与验收

每批运行新所有者、原测试对应集合和直接消费者测试。测试迁移前后按名称、标签及业务断言核对，不只比较数量。

| 验证面 | 必须取得的证据 |
| --- | --- |
| 类型与生成 | 历史 JSON、nil/空集合、错误链、Ent 默认值及表定义不变；SQL checksum 不变；Ent/Wire 再生成稳定 |
| 缓存 | Key v40、sched:v2 完整/轻量载荷双向兼容；快照隔离、凭据边界、失效和查询次数保持 |
| HTTP与网关 | 693 条路由、中间件顺序、认证失败投影、延迟读取、模型链、SSE/WS/Live、Qoder 尾部 usage及资源释放 |
| 账号与传输 | 原生测试事件、刷新竞争/CAS、健康反馈、HTTP/2、Grok 回退、代理失败不直连及池释放 |
| 资金与事务 | 注册/绑定、团队/Key、普通结算、任务资金、Promo/返利、履约/退款的同连接参与、回滚及一次资金效果 |
| 设置与生命周期 | 295 字段和19参与者、一次保存、提交后应用错误、唯一实例、构造无启动、有界停止及依赖关闭顺序 |
| 固定清理 | B01 原并发断言、299项 lint 逐项销项、E2E 入口及测试路径可执行 |

共享缓存、账号刷新、输出状态及完成执行器运行定向 race；关键锁与事务使用隔离 PostgreSQL/Redis，执行适用 integration race。不扩展全仓 race 或 benchmark。

依赖门禁夹具覆盖合法方向、旧包引用拒绝、同目录新文件、非法子包、核心反向依赖和正常 Adapter；记录诊断后删除。核对 normal/unit/integration/wireinject/embed/e2e/Darwin/Linux 文件选择。

阶段收尾在 backend 使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

```bash
go generate ./ent
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

另验证两个维护命令、真实前端产物下 embed 测试、生成稳定性，以及 standard/simple、CLI/Web/AUTO_SETUP、SIGTERM、监听失败和重启的进程契约。按现有发布矩阵核对其他 OS/架构构建。

真实供应商、硬件及外部 TLS/E2E 限制继续单列，不使用生产凭据，不把跳过、仅编译或本地协议夹具当作真实外部验收。必要本地行为验证未完成时保持待验。

## 5. 完成、交接与回退

完成须同时满足：

- 总计划旧目录及增量文件全部有最终归属；旧包、旧类型转接、legacybridge 和旧 ProviderSet 清零。
- 所有生产链使用原生唯一实现，app 不承载搬来的业务规则，新核心方向符合门禁。
- B01 回归通过，普通/unit/integration lint **0/0/0**，没有降低规则或削弱测试。
- HTTP、缓存、资金、事务、生成代码与生命周期取得实际行为证据。
- SQL、S00—S15 冻结资料和其他任务文件不变；Ent/Wire 差异可解释；已有及新增文件的 diff 检查通过。

满足后更新总计划状态与第8节验收清单，roadmap 标记 **17 / 17、S16 已完成**；保留完整阶段计划、最终映射、验收结果和运行限制。交付可审查差异，不自动提交。

回退按子步骤恢复代码、测试、装配、规则及相应生成物，不改数据库或缓存格式，不删除退款、任务、备份等持久事实。回退 B01 会恢复测试模式竞争；不在活跃维护、恢复或二进制替换期间切换版本。


## 执行记录

### S16.0 与 S16.1 首批进展（2026-09-18）

- 原计划正文已保存（16,353 字节，SHA-256 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`）。实施 HEAD、15,201 个源码摘要、SQL/Ent/Wire 与规划证据已归档至 `baseline/S16/`，原索引为空。
- 13 个 Ent schema 的 domain/model 引用已替换并经原生成器生成；`ent/migrate/schema.go` 摘要未变。旧 domain/model 的生产、测试和生成引用均清零，包已删除。Antigravity 默认模型表及测试迁至所属平台，Bedrock 原断言单独迁移。
- 错误、日志、代理/TLS、会话、IP、HTTP 读取、Google 错误、搜索值、统计、OAuth 和 Header 工具消费者直接使用原生实现。`util` 旧入口清零；原字节错误解析移至 upstream，Header 策略与输出分别使用 egress 和其 Adapter。
- 首批原生回归 265 条通过，错误/IP/模型定向 unit 51 条通过，后续工具与 Header 契约 27 条通过。事件存在父子与集合重叠，不相加。编译过程中两个数值状态参数已改为显式 Category 转换；保存初次编译日志，不计通过。
- B01 已在真实工作树按规划覆盖层方式落实，完整 race 回归仍待执行。依赖许可随已迁消费者替换；首批目标包 lint 为 0，完整普通 lint 正在复核。
- S16.1 尚未完成：apicompat、ctxkey 与时区初始化仍需收尾。S16.2—S16.7 未宣布完成，roadmap 保持 16/17、S16 实施中。


### 协议转接与固定测试清理（2026-09-18）

- B01 真实工作树定向 unit race 为 1,545 条通过事件，无失败；`b01-unit-race.json` 保留无测试包与实际跳过的区别。
- 983 个原生类型别名的外部消费者在 338 个文件中直接改绑；`service-type-alias-owners.json` 与 `service-alias-consumers.json` 保留对应关系。完整 `go build ./...` 通过，但旧 service 内部仍有未清理消费者。
- app 精确类型 import 与既有宽前缀许可产生匹配干扰，已把涉及的 37 条装配规则收紧为实际精确 import。未修改 depguard 实现或放宽限制；完整普通 lint 已为 0（`alias-lint-recheck.json`）。
- E01：三个 E2E 文件迁入 tests/integration；backend 与 deploy Makefile 的旧路径已更新，test-e2e 委托 test-e2e-local。只验证文件选择与 dry-run，没有执行真实供应商测试。
- L01：已检查原有未检查类型断言，使用不依赖业务实体的测试断言工具；保留断言失败的 panic 行为。HTTPS 代理夹具只信任本地测试证书；历史支付密文使用固定测试载荷。定向契约 151 条通过，首次漏迁共享退款夹具调用的编译失败及修复结果独立保留。
- pkg/apicompat 的全部消费者直接使用 protocol/bridge；型号选项在 gateway/forward 明确传入。原组合策略测试随所属边界迁移，只有三个测试助手组合原生选项与转换，无运行状态或算法副本。相关普通测试 1,985 条通过、1 项既有 WS 分支跳过（`protocol-native.json`）。
- 尚需完成 ctxkey/时区、旧实体与存储投影、平台/健康适配、HTTP/Ops、唯一原生装配、剩余测试和 unit/integration lint，以及最终完整验收。当前不标记 S16 或整体重构完成。

### 固定 lint 清零与原生测试装配（2026-09-18）

- L01 当前普通、unit、integration 完整 lint 均退出 0，分别见 `baseline/S16/normal-lint-clean.json`、`unit-lint-clean.json` 和 `integration-lint-clean.json`。后续代码迁移仍须重新验收，当前结果不代表 S16 已完成。
- 原 unit 诊断中的错误关闭、类型断言及未使用辅助代码已处理；nil context 和非规范 Header 兼容输入保留。批量字节夹具保留原内容，历史支付密文继续使用固定向量；未增加忽略项或删除业务断言。
- 注册/登录身份同步、邮箱绑定的跨模块测试迁入 `tests/integration`，保留 `unit` 标签，直接构造原生 identity、UserStore、AuthState 和 EmailChallenges。动态设置组合现有身份与推广读取器，测试不再构造旧 AuthService、UserRepository 或 SettingService。原生身份定向 race 为 24 条通过事件，无失败或跳过。
- 批量任务运行时测试迁至同目录，直接组合 batchimage Runtime、worker 与 Redis Adapter；微信测试直接调用 billing 的兑换存储。逐文件位置、断言与绑定见 `baseline/S16/test-migration-l01.json`。API Key 旧包装测试使用缓存端口错误，原生 apikey 测试仍保留 `redis.Nil` 输入及回源断言。
- 本轮固定清理回归为 44 条通过事件；协议/错误/IP 普通回归为 581 条通过事件，无实际测试失败或跳过；无测试包单独保留在结果中。各集合存在重叠，不求和。
- 当前环境不能写默认 Go 缓存，本轮改用 `/tmp/tokenrouter-s16-go-cache` 和 `/tmp/tokenrouter-s16-lint-cache`；未改变项目工具链或依赖。miniredis 实际启动失败于 `listen tcp 127.0.0.1:0: bind: operation not permitted`（`native-auth-unit.log`），该运行时断言待验，不修改测试绕过环境限制；涉及监听的最终 HTTP/Redis/进程验收也尚未完成。
- 原计划前 16,353 字节的 SHA-256 仍为 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`；342 个 SQL migration checksum 和 Ent 表定义摘要未变，S00—S15 冻结资料无差异。`git diff --check` 通过。
- S16.1 的 ctxkey/时区、S16.2—S16.5 的旧实体、实际适配及装配，和 S16.6 剩余测试迁移、S16.7 全面门禁/资金销项仍待完成。roadmap 保持 **16 / 17，S16 实施中**。

### 常量消费者与 import 命名清理（2026-09-18）

- 943 个直接常量转接已改绑原生所有者，逐符号归属见 `baseline/S16/service-constant-owners.json`；外部 241 个文件、旧 service 内部 622 个文件的消费者分别归档。改绑采用语法和类型信息，不改常量值或隐式/iota 常量组。
- 删除 7 个消费者清零的空转接文件；两个链式常量随后清理，其中 Grok 默认模型直接引用所属平台常量，无消费者的默认用量 Adapter 常量删除。首次构建捕获的遗漏引用及修复日志保留。六个无消费者字面量删除，五个仅供 unit 断言的 cooldown 字面量移入原测试文件，保留测试预期值。
- 常量改绑涉及的依赖许可只补到对应准确文件和 `$` 结尾 import；保留原拒绝规则。普通/unit/integration lint 已恢复为零，首次 integration lint 与另一 lint 进程冲突的未执行结果独立保留，随后串行补验通过。
- 用户指出 `native*` import 别名影响可读性。本轮清理 1,436 个文件中的 3,120 处临时别名：2,760 处使用默认包名，360 处因命名遮蔽或同名包保留模块/职责别名。映射见 `baseline/S16/import-alias-renames.json.gz`。不修改 import 路径、业务标识、导出 API 或运行行为；后续迁移不再保留这种状态前缀。
- 别名清理后全量构建、Wire 生成及普通/unit/integration lint 均通过（`alias-context-build.json`、`alias-wire.json`、`alias-*-lint.json`）。协议与请求状态定向回归取得 650 条通过事件，无实际测试失败或跳过；无测试包另记。未把编译或 lint 当作完整行为验收。
- 路由清单、普通 Key 门禁不读 body、错误 envelope 与 Responses 子路径拒绝顺序定向回归为 10 条通过事件，无失败或跳过（`alias-route-contracts.json`）；该集合不代替全路由行为验收。
- 原计划正文与 342 个 SQL checksum 未变，Ent 表定义、依赖版本和 S00—S15 冻结资料无差异，`git diff --check` 通过。索引仍为空，未提交。S16 仍在实施中，旧实体/适配/应用图以及显式请求状态和时区注入的剩余工作未宣布完成。

### 显式日期装配与用量测试收敛（2026-09-18）

- 本地监听限制解除后，原生身份与批量任务运行时的 unit race 补验取得 25 条通过事件，无失败或实际跳过。仅解决对应 SQLite/miniredis 夹具待验项，不代替真实 PostgreSQL/Redis 证据。
- app 在 bootstrap 之后构造共享 Calendar；用量、支付、推广、团队 HTTP 及已有计费、账号、路由日期端口直接接收它。保持用户时区回退、DST、明确时间与各接口的不同结束边界；`pkg/timezone.Init` 和剩余全局消费者仍待清理。
- 团队 HTTP 直接由 app 装配，删除用户/管理员两个旧构造器；消费者清零的管理员用量和仪表盘转接一并删除。Wire 由手写 provider 生成，未手改生成结果。
- 用量 HTTP/DTO 原测试改为原生 usage、Options、RuntimeSettings 和只读投影，删除三个测试转接文件；原测试名称、标签及断言保留。存储与 HTTP 分别核对同一缺失错误链。逐文件账本见 [calendar-and-usage-migration.md](baseline/S16/calendar-and-usage-migration.md)。
- 日期装配普通 383、unit 420 条通过事件；随后原生测试定向 113、HTTP race 120 条通过事件，均无失败或实际跳过，集合不求和。首次漏传自由函数 Calendar、Wire 标签及门禁诊断已修正，初始失败日志保留。
- 普通/unit/integration 全量 lint 再次为 0/0/0；已迁测试的旧 service/config 许可移除，三个夹具规则及排除项删除。计划正文、342 个 SQL、Ent 表定义、依赖和既有冻结资料未变，`git diff --check` 通过。S16 仍为实施中，未提交。
- 本批之后的普通全量测试退出 0：11,809 条通过、4 项既有跳过，无失败，见 `baseline/S16/checkpoint-full-normal.json`。跳过为 Qoder 真实 API/本机授权、OpenAI 真实 token 对比及既有 WS other_event_type 分支；没有把这些环境/分支跳过计为行为通过。

### 错误归属与团队 SQL 测试（2026-09-18）

- unit 全量首轮取得 19,857 条通过事件、1 项失败，原日志保留。该失败来自本阶段 L01 新增 Close 断言却漏登 sqlmock 关闭预期；已补齐并取得定向 race 通过，不归入历史问题或扩大修复范围。
- 387 个直接错误变量转接已清零，133 个旧 service 文件和 67 个外部文件直接引用原生错误；保留相同对象、消息和错误链。六个消费者清零的空转接文件删除，未发现重新赋值或取地址消费者。详见 [error-owner-migration.md](baseline/S16/error-owner-migration.md)。
- 团队所有权的原有序 SQL 测试迁至 team/postgres，旧测试包装删除，对应生产函数恢复包内可见；两次更新的 SQL、参数及事务范围不变。
- 全量构建已通过；首次 unit lint 的六条精确 import 诊断已按文件修正。完整 unit 正在复验；S16 仍为实施中，未提交。
- 随后的完整 unit 退出 0：19,858 条通过、8 项既有跳过；完整 integration（`-p=4`）退出 0：12,790 条通过、4 项既有跳过，见 `error-native-full-unit.json` 和 `checkpoint-full-integration.json`。unit 额外跳过为 SQLite 不支持的历史 NULL 数据夹具与三项外部 TLS 测试，不计行为通过。该检查点先于下述日期存储批次。

### 用量存储日期依赖（2026-09-18）

- 查询与聚合存储显式接收 Calendar，事务内派生保持同一日期对象；Dashboard 按日缓存与团队日统计同步接入。SQL 文本、查询数量、UTC 聚合、raw 回退及缓存格式不变。
- usage 生产代码不再调用全局 timezone 日期入口。纽约和 Honolulu 测试不再修改全局时区；新增聚合事务中 23/25 小时边界及命名时区 SQL 参数断言。
- 普通定向 729 条通过，后续 unit race 792 条通过，无失败或实际跳过；事件不相加。Wire 已按手写 provider 重新生成。真实 PostgreSQL/Redis 定向 race 与完整 lint 正在收尾，详见 [日期迁移账本](baseline/S16/calendar-and-usage-migration.md)。
- 随后定向 integration race 取得 1,625 条通过、1 项业务日条件跳过，但 CLI setup 进程测试及其父测试因输出缓冲竞争失败，整组未通过。该测试文件与基线完全相同，已保留[当次证据](baseline/S16/cli-process-race-observation.md)并请求调整范围；未追加复现或修复。日期装配的首次 lint 缺少精确 timezone import 许可，已补齐对应文件规则，未扩大目录许可。
- 本批最终普通/unit/integration 全量 lint 为 0/0/0，全量构建通过，Wire 重生成摘要一致。新增源码/阶段 Markdown 共 40 个文件的差异空白检查及 `git diff --check` 通过；原计划正文、342 个 SQL、Ent 表定义、依赖版本和 S00—S15 冻结资料未变。
- 当前仍有 service/repository/handler 非测试文件 605/103/145 个，legacybridge 30 个，见 `remaining-old-directory-counts.json`。这些包含实际适配和投影，未当作空壳删除；旧图、显式请求状态、其余时区消费者和最终验收尚未完成。roadmap 保持 **16 / 17、S16 实施中**，索引为空，未提交。

### 经用户确认的测试夹具补充范围（2026-09-18）

- 用户回复 `ok`，允许仅修复前述 CLI setup 输出缓冲竞争并保留原进程断言。此许可仅覆盖已保存的当次 race 证据，不扩大历史问题排查。
- `processOutput` 改为私有互斥及缓冲字段，阻止 `io.Copy` 通过提升的 `ReadFrom` 绕过 Write 的锁；文本读取使用同一互斥。未改生产进程、CLI 输入或退出与文件断言，补验结果另行追加。

### 日期纯包与资金存储收敛（2026-09-18）

- 删除 timezone 全局状态与函数，初始化及日志由 bootstrap 唯一拥有。计费存储、订阅日额度、Key 日期计数及站点 API/embed 使用显式日历；保留配置展示名、原窗口差异、取时点、SQL 和缓存格式。
- 平台额度 Store 直接在 app 装配，删除两个 repository 生产转接及四项恒等 fake 转接测试；真实存储断言保留并直接改绑。归属及测试对应见 [本批账本](baseline/S16/financial-calendar-migration.md)。
- 全量构建通过；直接消费者 unit 为 13,989 条通过、2 项既有跳过；原生定向 race 为 351 条通过；真实存储与完整进程 integration race 为 74 条通过，无失败或跳过。事件包含父子及重叠集合，不相加。原 CLI setup 输出竞争已在获准范围内修复并通过进程 race。
- 普通完整 lint 已为 0，unit/integration lint、删除转接后的补验与生成稳定性继续执行。S16 仍为实施中，不自动提交。

### 原生 usage 与 routing 装配收尾（2026-09-18）

- 删除已清零的 `app/legacybridge/apikey.go` 与 `app/legacybridge/routing_reports.go`。Key 存储的批量用量统计直接调用 `usage/postgres.ReadAPIKeyUsageTotals`；分组报表直接使用 `usage.DashboardService`，Live 能力检查直接使用 `upstream/openai/liveattestation`。两条路径只保留投影和调用，不复制业务规则、缓存或状态。
- 删除 `repository/apikey_usage_read.go` 这一层单纯转发；`repository/api_key_repo.go` 直接引用 usage 存储实现，查询参数、预聚合设置和 SQL 连接保持不变。
- 同步收紧 depguard：移除两个已删除文件的专用规则和目录排除，删除 app 对 `legacybridge` 的历史许可，补入实际 `usage/postgres` 与 `liveattestation` 精确 import。未扩大目录级许可或忽略规则。
- 受影响包普通测试取得 535 条通过事件；固定的 PostgreSQL/Redis integration race 取得 74 条通过事件，无失败或实际跳过。完整普通测试 **11,826** 条通过、4 项既有跳过；完整 unit **19,871** 条通过、8 项既有跳过；完整 integration（`-p=4`）**12,809** 条通过、5 项既有跳过。结果分别保存在 `baseline/S16/bridge-cleanup-unit-json.json`、`bridge-cleanup-integration-race.json`、`bridge-cleanup-full-normal.json`、`bridge-cleanup-full-unit.json` 和 `bridge-cleanup-full-integration.json`；无测试包的 package skip 与实际测试跳过分开保留。
- 普通、unit、integration lint 在当前批次均退出 0；`go build ./...` 退出 0。Wire 重新生成两次内容一致，`git diff --check` 通过。
- 这批未发现清单外历史问题，也未改变生产进程行为或测试断言。当前仍有旧 service/repository/handler 适配与投影、剩余 legacybridge、`ctxkey` 显式状态迁移及最终构建矩阵待处理；roadmap 保持 **16 / 17、S16 实施中**，索引为空，未提交。

### 纯账号转接进一步收敛（2026-09-18）

- 删除 9 个只做一层函数委托的 legacybridge 文件：账号刷新、后台刷新、OAuth 用量选项、健康/恢复、隐私、等级、管理目录和 Gemini 用量。app 直接调用对应的 service 端口，保留原平台资格、动态设置、统计查询及目录投影；没有复制缓存、规则或后台状态。
- `account_management.go`、`account_admin.go`/`routing_groups.go` 的目录默认值改为直接使用 service 的唯一实现；`account_gemini_quota.go` 直接调用 `service.LegacyGeminiUsageReader`。同步移除已删除文件的 legacybridge 通用排除，未扩大其他目录许可。
- app/account/routing unit race 取得 **595** 条通过事件，无失败或实际跳过；普通、unit、integration lint 均退出 0，`go build ./...` 通过，Wire 重生成前后无差异。首次门禁遗漏一个仍存在的 Gemini 转接文件，已在同批直接改绑并保存初始诊断，不属于清单外历史问题。
- 当前 legacybridge 生产文件剩余 19 个；其中仍依赖旧 service 的账号快照、凭据、刷新协调、模型目录、计费租约、分组与 scheduler 投影未在本批删除。旧 service/repository/handler 以及 `ctxkey`、最终应用图仍待 S16 后续批次，roadmap 保持 **16 / 17、S16 实施中**，索引为空，未提交。

### 容量与探测转接收敛（2026-09-18）

- 删除 `legacybridge/routing_capacity.go` 与 `legacybridge/routing_probe.go`。容量读取在 app 内直接调用 `service.AccountCapacitySettings`，探测 runner 直接接收 `service.NewGroupProbeExecution`；保留原接口形状、候选读取、探测执行和日志/租约时机。
- 删除对应目录排除项，未新增宽泛许可。定向 app/routing/account 普通测试取得 368 条通过事件，无失败或实际跳过；targeted lint 无诊断，`go build ./...` 通过，Wire 重生成稳定。
- 当前 legacybridge 生产文件剩余 17 个。剩余文件均仍包含旧平台/存储投影或跨阶段兼容逻辑，未在本批按文件名猜测删除；S16 后续仍需处理旧应用图、显式请求状态及最终全量验收。

### 账号纯转接继续收敛（2026-09-18）

- 删除 `account_codex_import.go`、`account_credentials.go`、`account_model_sync.go` 和 `account_testing.go` 四个只做适配的 legacybridge 文件。Codex 选项、凭据钩子、模型同步和测试 loader 在 app 直接组合现有 service/upstream 能力；缓存、凭据、执行规则及测试断言均保持原实现。
- `account_management.go` 直接传递已有 `GetAccountUsageStats`，未复制统计查询；管理与测试路径的输入输出及生命周期绑定不变。对应目录排除和 Codex 的过渡许可同步删除，新增 upstream import 仅精确许可到该文件。
- 定向 app/account/routing 普通测试取得 368 条通过事件，无失败或实际跳过；targeted lint 无诊断，`go build ./...` 通过，Wire 重生成稳定。首次编译发现一个已删除 import，已即时修正并保存日志。
- 当前 legacybridge 生产文件剩余 13 个；剩余文件包括账号归档/事件、刷新与凭据协调、身份、模型市场/定价、分组和 scheduler 投影，后续仍按实际消费者清零。S16 仍为实施中，索引为空，未提交。

### billing 与分组纯转接收敛（2026-09-18）

- 删除 `legacybridge/billing.go` 与 `legacybridge/routing_groups.go`。billing 装配直接调用 `service.AcquireLegacySingletonLease`，分组管理直接组合既有模型候选、请求模型规范化和校验权重；规则、缓存和状态仍各自只有原实现。
- `routing_groups.go` 新增的 `scheduler/policy` 依赖按文件精确加入 depguard；两个已删除转接文件的目录排除和专用规则同步移除，未扩大目录级历史许可。
- 定向 app/routing/billing 普通测试取得 **286** 条通过事件，无失败或实际跳过，见 `baseline/S16/billing-routing-cleanup-unit.json`；`go build ./...` 通过，Wire 生成前后内容一致。
- 当前工作树普通、unit、integration 三组完整 lint 均退出 **0**，分别见 `final-normal-lint.json`、`final-unit-lint.json`、`final-integration-lint.json`。这批未发现清单外历史问题；S16 仍在实施中，旧 service/repository/handler 适配、剩余 legacybridge、显式请求状态和最终验收尚未完成。

### 公开用量与运行状态纯投影收敛（2026-09-18）

- 删除 `legacybridge/usage_context.go` 与 `legacybridge/account_runtime_presenter.go`。公开用量在 app 直接组装 API Key、billing、订阅和审计上下文的只读投影；账号运行状态与调度评分直接调用既有 service 适配函数，未复制查询或规则。
- `public_usage.go` 的 middleware/Gin/billing 依赖按精确文件补入 depguard；两个已删除转接文件的目录排除和专用规则移除。
- 定向 app/usage/account 普通测试取得 **428** 条通过事件，无失败或实际跳过，见 `baseline/S16/usage-context-runtime-cleanup-unit.json`；定向 lint 0，`go build ./...` 通过。legacybridge 生产文件由 11 个降为 **9** 个。
- 本批仅删除已清零转接，未改变公开用量响应、上下文读取顺序或运行状态展示行为；S16 仍在实施中，最终全量 lint 需在本批之后重新确认。

### 账号归档额度投影收敛（2026-09-18）

- 删除 `legacybridge/account_archive.go`。归档和管理入口在 app 直接使用设置服务的导入默认值方法，并共用本地 Grok 额度观测投影；探测队列、资格判断和后台任务仍由 account/既有 service 拥有。
- 移除该文件的 legacybridge 目录排除和专用 depguard 规则；未增加宽泛许可。定向 app/account 测试通过，app lint 0，`go build ./...` 与 Wire 生成稳定。
- 当前 legacybridge 生产文件剩余 **8** 个；剩余项仍包含刷新协调、身份设置、调度快照事件、模型/价格目录等真实跨模块投影，不能按文件名删除。S16 继续实施中。

### 当前检查点（2026-09-18）

- 归档投影批次之后，普通、unit、integration lint 重新串行执行并均退出 **0**，证据为 `final3-normal-lint.json`、`final3-unit-lint.json`、`final3-integration-lint.json`；`go build ./...` 退出 0。
- `go generate ./cmd/server` 后 `wire_gen.go` 与生成前一致；计划正文前 16,353 字节 SHA-256 仍为 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`，`git diff --check` 通过，索引为空，S00—S15 阶段文件无差异。
- 当前旧目录生产文件计数为 service **605**、repository **100**、handler **98**、legacybridge **8**。这些剩余文件仍包含实际兼容投影或旧图入口，不能据此宣布旧架构清零；S16 保持“实施中”。

### 身份、Grok、目录与调度投影收敛（2026-09-18）

- 身份动态设置直接在 app 提供：`AuthSettings` 继续嵌入同一 `SettingService`，仅转换钉钉注册策略；HTTP OAuth 设置保留每次读取、后端模式回退、WeChat URL 与默认前端回退。删除 `identity_auth.go`、`identity_http.go` 两个 bridge。
- Grok 导入的账号 CRUD、代理读取、快照转换和后台任务标签直接由 app 闭包组合；保留 account 队列、额度投影和原 AdminService 失败语义。删除 `grok_account.go`。
- 模型市场与定价目录直接使用 service/upstream 唯一实现；app 仅提供 routing/billing 所需窄投影，保留目录查询、公开统计字段、价格状态/更新和模型身份规则。删除 `model_catalogue.go`、`pricing.go`。
- 账号事件投影和调度快照来源迁入 app 私有文件，保持 scheduler outbox、快照发布、账号/分组候选查询、事务 executor 与日志时机；删除 `account_snapshots.go`、`scheduler_sources.go`。legacybridge 生产文件已清零，旧包 import 也清零。
- 定向 identity **207**、managed refresh **346**、Grok **346**、model market **223**、pricing **279**、account events **415**、scheduler sources **419** 条测试事件均通过，无实际失败或跳过；对应日志/JSON 已保存于 `baseline/S16/`。每批 app lint 均为 0；新增 app 私有投影按文件精确补入 depguard，未恢复目录级许可。

### telemetry 关联键直接归属（2026-09-18）

- 请求 ID、客户端关联 ID、平台/模型/账号观测和 HTTP 阶段时间戳等纯 telemetry context key，已从 server middleware、audit HTTP、gateway HTTP、handler、repository 及 service 的生产代码直接改绑 `internal/infra/telemetry`；业务请求状态仍保留 `pkg/ctxkey` 兼容入口，避免把网关状态迁移与本批混合。
- 关联键字符串、类型身份、写入顺序和读取时机未变化；未新增 context key，也未删除仍有业务消费者的旧入口。相关 depguard 仅对四个原有精确文件补入 `internal/infra/telemetry$`，没有扩大目录许可。
- 受影响包测试通过：server/middleware、audit/httpapi、gateway/httpapi、handler、repository 均退出 0；service 包测试退出 0（约 115 秒）。定向 lint 覆盖上述包退出 0，无诊断。证据为本次终端输出，完整 lint 将在最终门禁统一复验。
- 本批未发现清单外问题，未修改请求行为或测试断言。`ctxkey` 的业务状态迁移、剩余旧实体/适配和最终应用图仍未完成，roadmap 保持 **16 / 17、S16 实施中**。

### 空兼容文件清理（2026-09-18）

- 删除三个仅包含 package 声明、没有符号和消费者的旧 service 文件：`content_moderation_keyword_matcher.go`、`content_moderation_redact.go`、`grok_probe_copy.go`。未删除仍被测试、Wire 或旧装配引用的仓储/handler 转接。
- `internal/service` 普通测试退出 0，`go build ./...` 退出 0，service 定向 golangci-lint 退出 0；无生产行为和断言变化。S16 仍在实施中。

### telemetry 关联键第二批收敛（2026-09-18）

- API Key 模型追踪、Ops 阶段时间、OpenAI 透传输出和审核 HTTP 的纯关联字段继续直接使用 `internal/infra/telemetry`；`pkg/ctxkey` 仅保留 Group、平台强制、认证策略、会话与其他业务执行状态。
- service 与 gateway/httpapi 定向测试通过，定向 lint 退出 0；接口类型从旧别名改为 telemetry 的 `ContextKey`，字符串键和时间戳写入时机保持不变。
- 本批未迁移业务状态，也未发现清单外问题。`ctxkey` 包尚不能删除，S16.1 仍待完成。

### 定价远程客户端转接清理（2026-09-18）

- `repository/wire.go` 直接绑定 `billing/provider.NewPricingRemoteClient`，删除仅做函数委托的 `repository/pricing_service.go`；代理回退参数、provider 实例和 Wire 依赖保持不变。
- repository/app 定向测试、全仓构建及定向 lint 均退出 0，`git diff --check` 通过。未改变价格加载、缓存或网络行为。

### repository 无消费者转接清理（2026-09-18）

- 删除 `repository/ops_repo.go` 与 `repository/update_cache.go`。两者无生产或 repository 测试消费者，app 已分别直接绑定 `ops/postgres` 与 `ops/rediscache` 的唯一实现。
- repository/app 定向测试、全仓构建及定向 lint 均退出 0；未改变 Ops 存储、Redis key 或生命周期。

### 指纹与网关缓存直接绑定（2026-09-18）

- app Wire 直接构造 `upstream/anthropic/rediscache.NewFingerprintStore` 和 `gateway/rediscache.NewGatewayCache`；repository 的两个重复缓存构造器删除，repository 集成测试与 testutil 改用所属缓存实现。
- 保留 fingerprint、masked session、gateway session 的 Redis key、TTL、接口类型和白盒断言；Wire 重新生成成功，repository/app/testutil 定向测试和 lint 通过，全仓构建通过。

### billing 缓存直接绑定（2026-09-18）

- app Wire 直接构造 `billing/rediscache.NewBillingCache` 并显式绑定 `billing.BillingCache`；repository 的重复构造器删除，额度集成测试改用 billing 的原生缓存实现。
- 先由 Wire 检出具体类型缺少接口绑定，补入精确 `wire.Bind` 后重新生成成功；repository/app 测试、构建和定向 lint 均通过。Redis namespace、TTL 与额度协调逻辑未变化。

### identity 邮箱缓存直接绑定（2026-09-18）

- 删除 `repository/email_cache.go`；repository 集成测试直接使用 `identity/rediscache.NewEmailCache`，app 原有 provider 不变。
- repository 测试、全仓构建及定向 lint 通过，验证码 Redis key、序列化和 TTL 未变化。

### 当前收敛检查点（2026-09-18）

- 本轮完成后 `legacybridge` 生产文件及引用均为 0；repository 非测试文件降至 93 个。`ctxkey` 仍有 23 个生产引用，均为 Group、ForcePlatform、认证策略、会话或其他业务执行状态，不能把它们误判为 telemetry 包装。
- 全仓普通 golangci-lint 退出 0；受影响 app/repository/testutil/service/gateway 测试和构建均通过，Wire 已重新生成。原计划正文哈希、S00—S15 阶段文件、SQL checksum 和 Ent 表定义继续保持不变，`git diff --check` 通过。
- 本检查点没有清单外问题。S16.1 的业务请求状态显式化、S16.2—S16.7 剩余旧实体/适配、旧 ProviderSet、测试迁移及完整构建矩阵仍未完成；roadmap 继续保持 **16 / 17、S16 实施中**，不宣布整体重构完成。

### 缓存、构造转接与旧桥接门禁收敛（2026-09-19）

- repository 生产文件从 93 个降至 76 个。删除 OAuth token、RPM、账号健康、身份 provider/缓存、代理/TLS 缓存等重复构造入口；INTERNAL 500 计数原实现迁至 account/rediscache，leader lock 直接绑定 infra/redis。app 保留各自唯一实例，旧调用方的接口仅在 Wire 中适配。
- 逐项路径与测试映射见 [cache-direct-mapping.md](baseline/S16/cache-direct-mapping.md)。定向普通 450、unit 441、token/健康 integration race 21、健康/锁 integration race 9、身份 integration race 3 条通过事件，无失败或实际跳过；集合有重叠，不相加。原测试断言保留，INTERNAL 500 新迁移契约验证旧 key、TTL、不续期和重置。
- 移除 legacybridge 的 17 条失效规则及其 import 许可、目录排除，新增全局旧包拒绝。仓库外夹具在 normal/unit/integration 均捕获原许可文件和新文件引用旧包/子包的两条预期诊断，验证后删除。同步系统架构、账号维护和开发流程中的失效桥接描述。
- 本批首次完整 integration lint 检出新增 Wire 集合缺少 wireinject 标签，以及精确规则与 app 通用拒绝重叠；这是本批接线回归，已修正并保留初次诊断。构建通过，Wire 再生成一致；最终三组 lint 结果另行追加。
- 本批未扩展历史问题排查，未修改原计划正文、SQL、Ent 表定义或 S00—S15 阶段资料。S16 仍实施中，未暂存或提交。

### 本批验证收尾（2026-09-19）

- 普通、unit、integration 全量 lint 串行执行均退出 0，见 `cache-cleanup-final-normal-lint.log`、`cache-cleanup-final-unit-lint.log`、`cache-cleanup-final-integration-lint.log`。本批未重复全仓行为测试，新增结果为前述受影响包普通/unit 与真实存储 race，不能将其记为新一轮全仓测试。
- wireinject 专项首次受另一 lint 进程互斥阻止；串行重试检出手写 Wire 文件对已迁 HTTP、存储、缓存缺少 7 个精确许可。依照 S16 装配门禁范围补齐后，`cache-cleanup-wireinject-final-lint.log` 退出 0；旧包拒绝、模块方向及其他文件限制保持。
- `go build ./...`、Wire 再生成比较和 diff 检查通过。原计划前 16,353 字节摘要不变，已发布 SQL、Ent 表定义、S00—S15 阶段正文无差异，索引为空。可丢弃依赖夹具已删除。
- 剩余业务 ctxkey、实体转换、service/repository/handler 和旧 ProviderSet 尚未清零，最终构建矩阵及阶段全量验收仍待完成；roadmap 维持 **16 / 17**。

### 原生存储装配与 HTTP 传输收敛（2026-09-19）

- repository 的 ProviderSet 已删除，设置/幂等、权益/兑换、任务队列/存储、上游客户端由 app 分组直接绑定。设置接口共享同一 Store，SQL 继续来自原 Ent 驱动，托管任务 Key 复用同一 KeyStore。旧 repository 生产文件由 76 个降至 26 个，逐文件去向与测试迁移见 [storage-transport-mapping.md](baseline/S16/storage-transport-mapping.md)。
- 旧 HTTPUpstream 实现迁至 `gateway/provider/transport`，app 按原读取时点提供明确传输参数。原连接池、隔离键、TLS、OpenAI HTTP/2 回退、Grok Header/403 回退与逐跳校验保持；新适配器不依赖旧 service 或完整 config。隐私 req 工厂直接绑定唯一 infra 池。
- 真实存储 race 各集合通过事件为 91、16、11、11；HTTP 传输 race 64；定向 unit 450、433、424；平台包装 43。集合重叠、包含父子测试，不相加；各已完成集合无失败或实际跳过。miniredis 订阅证据与真实 Redis 分开记录。
- 普通/unit/integration 全量 lint 均退出 0，日志为 `storage-transport-full-*-lint.log`；wireinject 专项退出 0，`storage-platform-final-targeted-lint.log` 也退出 0。构建通过，Wire 再生成稳定。初次测试私有函数引用、漏改 outbox 函数值、迁移 HTTP helper 和精确依赖许可问题均为本批接线回归，修复后结果单独保存，初次失败不计通过。
- 同步系统架构与开发流程的真实装配路径。原计划前 16,353 字节摘要未变，SQL、Ent 表定义、S00—S15 已核对阶段正文无差异，索引为空，未提交。本批尚不替代最终全量行为验收，S16 保持实施中。

### 失效旧构造清理（2026-09-19）

- 按全仓 Go 标识符核对，删除 service/wire.go 中 31 个无生产、测试或 Wire 引用的构造入口，名称记录在 `baseline/S16/unused-service-providers.json`。保留仍被实际应用图使用的 provider；未改变启停行为。
- 删除其清零后的账号过期、用户属性、审计、Ops 聚合/清理/告警和备份旧构造包装。备份路由测试直接绑定原生 HTTP Handler；现有 app 已直接构造生产备份实例。
- 删除构造后首次 Wire 验证检出机械导入整理误删 Redis import；已恢复原 import，并重新生成成功。这是本批工具造成的编译回归，不涉及运行行为；后续消费者测试与完整门禁结果继续追加。

### 原生函数与幂等运行时装配（2026-09-19）

- 按 S16.2 删除团队、推广的旧仓储构造，直接调用原生存储和同连接 billing 参与者，repository 非测试文件剩 24 个。真实 PostgreSQL race 为 38 条通过事件。
- 按 S16.3/S16.5 清理已无消费者的 Ops、队列、代理、锁和 HTTP context 包装。函数改绑清单为 `function-direct-consumers.json`（33 文件、60 符号）。定向 race 129 条通过、1 项既有 WS 分支跳过；原生维护锁 race 2 条通过。跳过不计通过。
- 按 S16.5 将幂等配置投影、协调器发布和清理任务构造移入 app，Wire 直接构造 infra 时间轮，删除旧 service provider 与委托。生产实例、默认状态、清理周期、正数覆盖默认值及 ObserveOnly 语义保持。
- 按 S16.6 保留 HTTP、维护锁和 Deferred 原消费者断言；旧时间轮包装测试由原生构造/启动/停止测试承接，具体映射追加到 `storage-transport-mapping.md`。普通/unit 定向 race 为 220/80 条通过，无失败或跳过；普通定向 lint 退出 0。
- 用户提醒压缩后对照原计划，本次已重新完整读取 S16 正文。后续迁移仍受 S16.0—S16.7 和固定问题范围约束，不能把文件清理计数替代完成标准；正文前 16,353 字节摘要未变，`git diff --check` 通过。
- 仍待完成业务 ctxkey、旧实体投影、实际平台/健康/HTTP 适配、SettingService 与旧 ProviderSet 清零，以及最终完整构建、行为矩阵和资金销项。roadmap 保持 **16 / 17、S16 实施中**，未自动暂存或提交。
- 本批幂等/维护锁真实 PostgreSQL integration race 6 条行为测试通过（`idempotency-native-integration-race.json`）；无测试文件包单列，不计行为通过。unit 定向 lint 退出 0，Wire 再生成摘要一致，见 `idempotency-native-unit-lint.log`、`idempotency-wire-stability.log`。索引为空，已发布 SQL、Ent 表定义及 S00—S15 冻结证据无差异。
- wireinject 定向门禁退出 0（`idempotency-native-wireinject-lint.log`），时间轮的装配许可仅增加到原精确 `storage_wire.go` 规则，未开放其他文件或旧依赖。

### 设置读取与原生身份 HTTP 装配（2026-09-19）

- 按 S16.5，app 直接构造身份、账号、路由、推广、用量、审计、面板和后台模式读取器，向残留 SettingService 注入同一实例。新增实例一致性断言验证构造不查询、账号缓存发布可由兼容消费者读取。公开站点、搜索配置与提示规则的构造已解除对 SettingService 的反向依赖；原查询次数、默认值和发布时机保留。
- 按 S16.3/S16.4，登录/TOTP 直接使用 identity.EmailChallenges，Passkey 原生构造和 HTTP 直接绑定；删除旧 Passkey、API Key、TOTP、用户属性及三类账号授权 HTTP 包装。普通/管理员 Key、管理员用户与公开用量直接接收原生用例；邮件构造不再创建旧 EmailService。尚未迁出的网关和测试兼容接口继续登记，不能当作已清零。
- handler.ProviderSet 已删除。原生 billing/site/ops HTTP 构造进入 app/http_modules_wire.go，账号授权构造独立分组；三个旧网关构造仍明确保留在应用图，等待 S16.4 实际适配清理。删除无消费者的四个 handler 构造器，未将其实现挪入 app。
- Ollama 配置读取与写入移入 account.RuntimeSettings，保留配置键、缺键默认、错误包装和编码顺序；原验证测试移至 account/ollama_usage_settings_test.go。API Key 校验及禁止枚举断言移至 apikey/httpapi；五个管理员用户测试移至 identity/httpapi，仅使用身份窄端口替身，保留原测试名、标签与断言。
- 定向 race 证据：`settings-key-http-race.json` 12 条通过，`settings-key-http-unit-race.json` 33 条，`native-settings-consumer-race.json` 23 条，`admin-native-contracts-race-final.json` 30 条，`native-http-unit-race-final.json` 78 条。集合重叠、不相加，无测试文件/无匹配包不算行为验证。初次夹具字段改名、路由参数和 Wire 接口集合错误已作为迁移回归修复，初次日志保留；新一轮 lint 和最终全量验收尚在执行。
- 本轮继续依照原计划串行实施，无 subagent、无新增历史问题排查、未提交。剩余旧实体、账号/平台/健康适配、显式业务请求状态、旧 service 图及完整验收仍需完成，roadmap 保持实施中。

### 原生用户值与存储入口（2026-09-19）

- 按 S16.1，强制平台与入站端点使用 apikey 已有请求投影，删除对应旧 ctxkey；剩余纯 telemetry 消费者直接引用其所有者。业务 Group、会话及其他执行状态尚未清零，不能视为 ctxkey 已删除。
- 按 S16.2，旧 service.User 和 UserRepository 的真实类型引用直接改为 identity.User 与 identity.UserRepository，删除双向字段映射及未被消费者使用的递归 APIKeys 字段。需要保留结构体副本的边界使用 identity.CopyUser；缓存的专用深复制仍由原缓存实现负责。类型与调用位置分别保存在 `native-user-type-mapping.json`、`native-user-function-mapping.json` 和 `native-user-repository-mapping.json`。
- app 直接交付唯一 identity/postgres.UserStore，删除 repository 用户/身份资料转接及 service 的仓储转换层。原注册、邮箱、用户删除、资金操作和外层事务仍使用同一原生存储；Key 管理的同连接参与判断改为真实 UserStore。测试直接构造原生存储，映射见 `native-user-store-local-mapping.json`。
- 首轮普通定向 race 为 1,634 条通过、1 项既有跳过；仓储收敛后 unit race 为 2,497 条通过、1 项既有跳过；真实 PostgreSQL integration race 为 126 条通过、无失败或跳过。日志分别为 `native-user-normal-race.json`、`native-user-store-unit-race.json`、`native-user-store-integration-race.json`。集合重叠，不相加；未把无匹配包计为行为通过。
- Wire 已由手写装配重新生成；初次类型迁移的重复 import 已修正并保存诊断。unit lint 的六条新身份 import 已按实际文件补齐，integration 门禁继续执行。同步身份与租户文档的当前类型和存储归属；旧 UserService、其它实体/适配及最终门禁仍需完成，roadmap 不变。

### 原生用户服务、分组与显式请求状态（2026-09-19）

- 删除旧 UserService 转接，生产 HTTP 与 app 使用同一 identity.UserService；用户资料测试迁入 identity，保留原业务断言。分组值改用 routing.Group，删除旧实体及重复协议/模型展示方法；平台相关 Messages 选择暂留旧适配，仍属后续清理范围。
- 删除 pkg/ctxkey。执行提示与路由投影进入 gateway/requeststate，文本 Request 显式携带两份状态；普通认证的付款用户与 Fast 策略使用原生 AccessSnapshot，保持 Google 入口的原有差异。context 适配只承接仍未清零的单步消费者，不重新安装业务字符串键。
- sched:v2 的递归存储形状迁入 scheduler/rediscache/codec，保留凭据存储字段、分组 AccountGroups 的历史 null、nil/空集合及轻量/完整入口。原公开账号投影仍不暴露凭据；旧快照过滤适配尚未清零。
- 定向 race：execution-hints-normal-race 为 1,046 条通过、1 项既有跳过；native-access-hints-unit-race 为 1,834 条通过、1 项既有跳过；native-group-state-unit-race 为 2,592 条通过、1 项既有跳过。集合重叠，不相加。初次 normal race 的导入环及快照夹具问题已按迁移回归修复，失败日志保留；最终全量验证尚未执行。
- 旧 metadata bridge 的 fallback 断言由原生显式零值、缺省及快照隔离断言替代；配置键继续兼容，fallback 观测为零。分组可变夹具在修改后重新绑定独立快照；原资格、查询次数和调度业务断言保留。
- 分组仓储接口直接归 routing，app 交付唯一 GroupStore，删除旧 GroupService 和 repository/group_repo.go。测试只装配原生存储及原同连接参与者，未复制存储规则。Wire、事务和门禁结果继续追加；本记录不代表 S16 已完成。

### 原生 Key、用量记录与测试归属（2026-09-19）

- 删除 service.APIKey、APIKeyRepository、APIKeyService 与 repository Key 转接，app 和消费者使用唯一原生 KeyStore/服务。分组策略直接使用 routing.Group，缓存 v40 仍由原深复制实现维护。配置装配测试辅助仅位于 apikey/testkit，不增加运行状态。
- 删除旧 UsageLog、UsageService、UsageLogRepository 和日志往返转换；app 交付同一 usage/postgres.Store。旧仓储的客户端模型展示覆盖移到 gateway/completion 写入前，保留原断言，不改变计费模型。仓储的 23 个纯类型别名消费者直接改绑所属模块。
- Key 与消费链定向 unit race 为 2,946 条通过事件；用量/计费定向 unit race 为 2,093 条；Key 测试迁移及消费者补验为 556 条；真实 PostgreSQL/Redis 用量、结算、Key 和身份集合为 366 条。集合重叠，不相加，均无测试失败或实际跳过；无测试包单列。日志见 native-key-service-unit-race-corrected、native-usage-unit-race、native-key-tests-unit-race-recheck 和 native-key-usage-integration-race-final。
- 15 个 Key 单模块测试文件迁入 apikey，保留原测试名称和断言；账本见 native-key-test-migration.json。模型转发测试仍由待清理网关消费者拥有；用户、团队与并发数据替身按测试实际端口缩小，未复制业务实现。
- 已修复本批机械迁移引入的三处额度更新器判断回归、匿名接口字段改名和测试装配遗漏，初次失败与修正日志保留。没有扩大历史问题修复范围。Wire 重新生成与全量构建通过，用量迁移后的 unit lint 退出零；测试迁移后继续按准确文件/import 更新门禁并复验。
- 同步身份租户与数据生命周期文档。原计划正文保留，旧 Account、实际平台/健康/HTTP 适配、剩余应用图及最终验收仍需继续，roadmap 保持实施中，不将本批通过当作 S16 完成。

### 指纹、授权与传输装配清理（2026-09-19）

- 重新完整核对 S16 计划后继续执行。Anthropic 请求指纹和 metadata 的纯转接删除，装配直接引用平台实例；三个原测试迁至 Anthropic，保留名称、标签、字段顺序及缓存断言。仪表盘聚合和支付维护生命周期直接绑定唯一原生实例，保留 hook 名称与顺序。
- QoderOAuthService 和旧 HTTP 构造删除，app 投影代理读取，直接使用 account/provider 授权对象；会话、一次完成认领与凭据生成仍由已有 account 实现拥有。授权及 HTTP 测试迁至对应模块，原 session 构造验证组合相同的原生缓存和平台构造器。
- TLS 旧账号感知服务删除。调用方显式投影 TLSSelection，egress/provider.TLSProfiles 仅转换技术指纹，app 直接启停原生 egress 模板服务。没有复制缓存、规则或增加供应商调用。
- 指纹/聚合/支付后台定向 race 130 条通过；Qoder 授权及消费者 92 条通过；TLS/Qoder 相关集合 540 条通过，均无实际失败或跳过，集合不相加。日志见 native-fingerprint-runtime-unit-race、native-qoder-auth-unit-race-final、native-tls-unit-race；无匹配包单列。
- 保存并修复本次测试迁移遗漏共用字符串助手、跨模块 session 构造和断言类型转换，以及 Wire 引用遗漏。Qoder 批次完整 unit lint 已为零；TLS 批次继续复核。账本 native-fingerprint-*、native-runtime-external、native-qoder-auth-*、native-tls-* 保留消费者映射与初始诊断。S16 仍在实施，不提交。

### 纯委托、价格目录与渠道实例（2026-09-19）

- S16.2/S16.5：通过普通/unit/integration 类型信息确认 760 个同签名、原参数、单次调用函数，直接改绑原生所有者并删除对应旧声明，其中 51 个文件清空删除。没有移动带配置转换、状态或附加行为的入口。direct-function-delegates、local、external、removals 清单保留逐符号映射；被删函数没有独立 Project Doc 锚点。
- 纯委托消费者 unit 验证为 13,423 条通过、2 项既有跳过，完整 unit lint 退出零，Wire 重新生成成功。该集合只覆盖本批消费者，不代替最后全量验收。
- S16.3/S16.5：删除旧 PricingService，app 和 lifecycle 直接使用唯一 billing/provider 目录；平台型号与动态候选投影进入 gateway/provider/modelidentity。五个目录测试迁至 provider，三个型号测试迁至 modelidentity，保留热更新、零价、元数据、动态 Grok 与计算断言。旧 config 动态转接无消费者后删除，生产仍使用原 app 静态 Options。
- 价格定向 unit race 636 条、原生普通 race 195 条通过，均无失败或跳过；完整 unit lint 退出零。首次测试路径和夹具引用遗漏已修正，日志保留在 native-pricing-*。
- 删除旧 ChannelService，app、网关和任务直接使用唯一 routing.ChannelService。Key 模型追踪与报文改写归 gateway/modeltrace；旧包装的隐式测试初始化改为显式原生构造。两处反复改价场景更新同一存储替身并使原缓存失效，保留同一解析器观察最新价卡的断言。
- 渠道、计费、模型映射与用量消费者 unit race 894 条通过，无失败或跳过；单模块报文测试移至实际所有者。初次遗漏调用和夹具实例重绑定导致的测试回归已修正，证据 native-channel-*。门禁复核和其余旧实体、实际适配、应用图及最终验收继续进行；不宣布 S16 完成，不提交。

### 原生定价解析、计算与资金通知（2026-09-19）

- 重新完整读取 S16 原计划后继续执行 S16.2/S16.5/S16.6。删除旧 ModelPricingResolver、BillingService 和图片查价包装，app 直接构造唯一 billing.PriceResolver 与 Calculator。分组显式投影为 PriceGroup，动态型号候选仍由 gateway/provider/modelidentity 按原读取时点提供；创作台显式接收同一 ChannelService，不再从旧解析器取回它。
- 解析器消费者定向 unit 1,051 条通过；计算器及直接消费者 unit race 1,214 条通过；三个图片、搜索/音频、分时定价测试迁至 billing 后定向 race 15 条通过。集合重叠、不相加；完整 unit lint 为零。日志 native-resolver-*、native-calculator-* 保留改绑清单、初次编译诊断及修正结果。
- 删除旧 BalanceNotifyService。资金完成处理直接使用 app 的唯一 billing 通知实例，账号配置投影归 account/provider；有事务状态时沿用原返回状态，无状态才按原时点回源。阈值、收件人及守卫断言迁至 billing，邮件正文/Header 断言迁至 notification，账号额度投影断言迁至 account/provider。定向 race 218 条通过，无失败或跳过；完整 unit lint 退出零，Wire 生成通过，见 native-balance-*。
- 本批同步路由结算与架构文档；未修改资金事务、金额规则、SQL 或缓存协议。索引仍为空，diff 检查通过。旧 Account、设置聚合、实际平台/健康/HTTP 适配、旧 service 图和最终完整验收尚未完成，roadmap 保持实施中。

### 支付原生装配、HTTP 与退款测试归属（2026-09-19）

- 再次完整核对 S16 原计划。app 直接构造 payment.Runtime、ConfigService、ProviderBindings、Checkout、OrderQueries、OrderLifecycle 和 RefundWorkflow，旧 PaymentService/PaymentConfigService 不再进入生产应用图。付款身份、通知及同连接权益参与仍按原投影和顺序绑定，Wire 已重新生成。
- 配置、渠道限额、历史实例绑定、订单快照和统计测试迁至 payment/postgres；套餐校验及回滚测试迁至 billing/postgres；管理员订单敏感字段断言迁至 payment/httpapi。公开查单/签名续接和 Webhook 与存储的组合测试迁至 tests/integration/payment，保留 unit 标签及原字段、验签错误、ACK 断言。三个旧支付 HTTP 构造入口无消费者后删除。
- SQLite 测试资源归 testutil/sqlite，支付配置和固定历史密文归 payment/testkit；不把这些单测算作 PostgreSQL 资金事务证据。公开 HTTP 测试进程在 TestMain 一次设置 Gin 模式，保留原并行断言。文件级 depguard 继承对应角色限制，不开放 HTTP 的数据库方向。
- 原生配置定向 unit race 675 条、套餐/HTTP 集合 265 条、渠道绑定/HTTP 集合 721 条、退款及设置集合 453 条通过；集合重叠，不相加，均无失败或跳过。初次机械替换的 import、符号及夹具遗漏已修正，初始日志保留。支付渠道绑定批次完整 unit lint 退出零，后续迁移继续复验。
- 两组 PostgreSQL 退款契约迁至 tests/integration/payment，直接注入 billing.BalanceInTx；原 SQL、审计触发器、竞争屏障及业务断言保留。integration race 16 条通过，无失败或跳过，见 native-payment-refund-postgres-race.json。确认、准备、补偿、审计失败及恢复均走唯一原生实现；退款旧计划转换、HTTP/Service 转接及无消费者查询包装删除。
- 本批只收敛所有者和测试入口，未改退款时序、金额、已发布 SQL 或缓存协议。剩余支付履约/下单测试、旧 Account、设置及实际平台/HTTP 适配和最终验收仍继续；当前不标记 S16 完成。

### 支付旧图清零与履约契约（2026-09-19）

- 删除全部 service/payment 非测试文件，包括 PaymentService、PaymentConfigService、履约、查单、订单生命周期与结果转换。微信续接直接调用原生签名密钥解析；Audit 敏感字段直接使用 payment 的唯一配置清单。没有复制退款、履约或渠道状态。
- 纯履约、金额、结果与套餐校验测试分别归 payment/billing；查单、兑换、订阅和存储组合契约归 tests/integration/payment，保留 unit 标签。原订阅内存替身移入 billing/testkit；通用 SQLite 工具仍单列，不作为真实 PostgreSQL 验收。微信测试在 app 调用实际付款运行时装配，保留原密钥回退和响应断言。
- 履约 race 53 条、订单生命周期 race 118 条、退款/设置消费者 race 250 条、微信及结果 race 80 条通过，无失败或跳过；集合重叠、不相加。证据分别为 native-payment-fulfillment-race-final、native-payment-lifecycle-race-final、native-payment-recovery-race-final、native-payment-order-wechat-race。支付删除后的完整 unit lint 退出零，见 native-payment-complete-unit-lint-final.log。
- 迁移初次暴露的 UTC 测试进程前提缺失、跨文件夹具引用及重名测试已修正，失败日志保留。原查单用例 TestVerifyOrderPublicRejectsBlankOutTradeNo 改名加 UseCase 后缀，与已有 HTTP 同名断言区分；断言与行为不变。后续账号及最终全量验收仍继续，索引保持为空。

### 账号协议、候选状态与原生用量查询（2026-09-19）

- 重新完整读取 S16 原计划后执行 S16.2/S16.3/S16.6。CN 协议与地址规则迁至 account.ProtocolTarget；gateway/requeststate.AttemptRoute 独占每次候选结果和协议，保留 fresh 重新解析、分组回退和模型映射懒读取。旧 Account 尚未删除，当前仅把对应规则委托给原生实现，不能据此宣布实体清零。
- 模型默认目录的技术组合进入 account/provider，Qoder 白名单归一化由 upstream/qoder 拥有，OpenAI OAuth 可服务型号资格由 account 拥有。删除旧默认目录包装和重复平台规则，生产管理及网关消费者直接绑定；没有复制型号缓存。
- API Key 上游查询由 app 直接构造唯一 account.UpstreamUsageService，技术请求、代理/TLS/Header 投影进入 account/provider。旧 service 查询实例和管理员 HTTP 转接删除；纯配置、解析、规范化、地址安全与 HTTP 查询测试分别迁至实际所有者。URL 策略保持缺省、显式关闭 allowlist 和私网选项的原区别。
- 账号协议定向 race 146 条、模型消费者 1,698 条、上游查询 1,194 条、最终协议/查询 unit race 2,251 条通过；集合重叠，不相加，均无失败或跳过。对应日志为 native-account-protocol-race-final、native-account-model-race、native-upstream-usage-race-corrected、native-account-usage-contract-race。精确依赖门禁随文件迁移更新，最终全量门禁另行执行。
- Ollama 原生装配已删除旧 OllamaCloudUsageService、传输和存储投影包装；app 直接绑定原有唯一核心及共享 HTTP 池。查询、停止和状态契约迁到 account/provider，管理展示直接测试原生 account/httpapi。定向 race 50 条、HTTP race 10 条通过，无失败或跳过；见 native-ollama-race-corrected 与 native-ollama-http-race。仓储转接及存储测试继续清理，不能把内存替身视为真实 PostgreSQL 证据。
- 测试搬迁遗漏共享助手、重定向断言符号和手动停止夹具已作为迁移回归修正，初次失败日志保留。所有者清单见 native-usage-*-test-mapping、native-ollama-*-mapping。同步上游用量与网关生命周期文档；旧实际平台/健康/HTTP 图、设置及最终验收仍待完成，roadmap 保持实施中，未提交。

### 账号会话、刷新与缓存失效（2026-09-19）

- 本轮先完整重读 S16 原计划与 Project Doc 门禁，再继续 S16.3/S16.6。Ollama SQL mock 契约 21 条通过，包含真实 PostgreSQL 的存储集合 integration race 25 条通过，无失败或跳过；25 条包含同包普通契约，不能全部称为真实数据库测试。对应 native-ollama-storage-race、native-ollama-postgres-race 保留具体事件。
- QoderTokenProvider 从 service 删除，构建与传输直接接受 account.Record；会话世代、singleflight、等待者取消、共享预算和停止仍由唯一 account.QoderSessions 实现。原会话和并发测试迁入 account/provider，Wire 改为 app 显式构造。Qoder unit race 525 条通过，无失败或跳过；native-qoder-unit-lint-final 为零。
- CompositeTokenCacheInvalidator 及平台缓存键测试迁入 account，Qoder 会话组合断言单列 provider。管理导入、刷新和健康恢复直接传原生记录，删除对应旧实体往返与旧构造器。版本比较表格保留全部预期值，改为调用所属实现，不在测试内复制比较算法。定向 unit race 300 条通过，无失败或跳过；native-token-invalidation-lint-final 为零。
- Qoder 站点刷新适配迁入 account/provider，原资格、响应合并及代理传输断言同批迁入。旧刷新入口仅投影与委托，仍有未迁管理和网关消费者，未宣称旧刷新图已清零。刷新及直接消费者 unit race 593 条通过，无失败或跳过；native-qoder-refresh-unit-lint 为零。上述集合重叠，不相加。
- native-qoder-provider-mapping、native-qoder-session-concurrency-mapping、native-qoder-refresh-test-mapping 和 native-token-invalidation-mapping 记录测试及实际所有者。初次编译中的遗漏接口、重复 import、夹具引用及未使用 import 已修正，原失败日志保留。没有新增历史问题修复；旧账号/平台/健康/HTTP/设置图和最终验收继续实施，索引保持未提交。

### 原生授权、服务账号与创建校验（2026-09-19）

- 删除 Qoder 创建校验、机器身份及授权组合的旧包装，account/provider 复用唯一平台实现；创建消费者仍保持原校验顺序。定向 unit race 556 条通过，native-qoder-validation-lint-final 为零，逐符号映射见 native-qoder-validation-mapping。
- 删除 Claude、Antigravity、Gemini、Grok 和 OpenAI 旧授权服务及其重复接口，app 直接构造原生授权实例。代理、TLS、动态配置及隐私查询参数由 account/provider 投影；会话、单次消费、刷新协调和凭据规则仍在原账号实现。纯档位和 PAT 凭据断言进入 account，技术授权断言进入 account/provider，测试名与标签保留；native-*-authorization-mapping 及补充映射列出实际去向。
- 授权消费者 unit race 1,403 条、Claude provider 补验 26 条、Gemini 464 条、OpenAI 250 条通过；集合重叠，不相加。Grok 初次 1,041 条通过、1 条迁移断言失败：旧实体已变为含时钟函数的独立记录，直接比较函数字段不能成立，现比较回投影后的全部原字段，HTTP 补验 18 条通过。原断言数据及行为未弱化，初次失败日志保留。
- Vertex 服务账号的解析、缓存键和 token 取得组合归 account/provider，Claude/Gemini/批量图片共用，删除批量图片中的重复技术组合。原平台解析断言迁入 upstream/vertex；账号缓存与取消断言迁入 account/provider，定向 race 120 条通过，native-vertex-mapping 保留对应关系。
- 真实 PostgreSQL 刷新 CAS：Claude、Google 与 OpenAI 各 3 条通过事件，包含父子测试；不相加为独立场景数量。OpenAI 覆盖正常刷新与管理员换凭据后不覆盖新值，保留一次交换及缓存断言。遗漏 import 的初次编译日志单列，修正结果见 native-openai-authorization-postgres-race-final。
- 授权批次及最终 OpenAI unit lint 均退出零，Wire 由手写装配生成。同步 Anthropic、Gemini、Antigravity、Qoder、Grok、OpenAI 文档的真实所有者。旧 token 包装、账号实体、健康/网关/设置图及最终全量验收继续实施，当前不标记 S16 完成，不提交。

### 请求凭据、CRS 与隐私所有者（2026-09-19）

- 再次核对原计划，继续 S16.3/S16.5/S16.6。Grok token source 直接接入 app、网关与配额查询；刷新资格与错误分类通过 account.AccountRefreshPlatformPolicy 绑定。原消费者定向 race 1,014 条通过，无失败或跳过，见 native-grok-token-source-race-final。
- 删除旧 CRS 同步、刷新和仓储投影包装。account.CRSAuthorization 组合原生授权及同一刷新协调器，app 直接交付 CRSSync。原 CAS、影子资格、来源清理及导入断言分别迁至 account/provider 和 account；定向 race 67 条、契约补验 35 条通过，native-crs-lint-final 为零，映射及初次迁移诊断保留。
- 手动凭据合并进入 account.ManualCredentialExchange，app 直接投影 Qoder 传输。定向 race 256 条通过，无失败或跳过；native-managed-refresh-lint-final 为零。未改变凭据字段类型、刷新资格和错误文本。
- 删除旧 OpenAI/Antigravity 隐私请求入口，app 使用原生 PrivacyService 及 provider 参数。订阅查询、账号归属与本地 HTTP 身份/代理断言迁至实际所有者；40 条定向 race 通过，测试端点按实例注入，原断言保留。
- OpenAI token source 直接使用唯一指标、缓存、原生条件写入和刷新协调器；AlphaSearch 显式绑定同一授权实例。定向 race 328 条通过、1 项原有 WS other_event_type 跳过；22 个原 token 行为测试迁至 account 后，相关契约 82 条通过。真实 PostgreSQL/Redis CAS 3 条通过事件，含父子测试，见 native-openai-token-source-postgres-race。首次遗漏 AlphaSearch 依赖及后续测试辅助未使用诊断已归档并修正。
- Claude token source 已直接装配，相关验证和门禁继续进行。上述集合有重叠，不相加；原计划正文保留，旧账号/平台/健康/HTTP/设置图和最终验收尚未完成，不更新为 17/17，不提交。

### 原生后台刷新、Codex 邀请与调度编码（2026-09-19）

- 完整重读 S16 计划，继续 S16.2/S16.3/S16.5/S16.6。app 直接构造唯一 BackgroundRefreshService、六个平台 executor、RefreshAttempts 和后置端口；生产 Wire 不再构造旧 TokenRefreshService。旧刷新类仍有测试消费者，继续按实际所有者迁移，不能视为已经删除。
- 后台刷新及消费者定向 unit race 581 条通过，无失败或跳过；见 `baseline/S16/native-background-refresh-race.jsonl`。原 refresher 契约及错误分类测试分别进入 account/account provider，映射见 native-token-refresher-test-mapping。
- 删除旧 Codex 邀请重置服务及 provider。account 拥有资格与发送/消费规则，upstream/openai 拥有 HTTP 报文，account/provider 投影 token、代理与 TLS。app 直接绑定原生 Admin 读取和唯一传输；原 11 个测试迁至实际所有者，保留资格优先、Header 和错误断言。定向 race 42 条通过，native-codex-invite-lint-final 为零；初次迁移编译诊断独立保留。
- 删除 service/scheduler_snapshot_codec.go。完整/轻量 JSON 编码和字段过滤唯一进入 scheduler/rediscache/codec，账号存储、事件发布和刷新后发布直接传递原生 Record，删除这些生产路径的旧实体往返。核心仍只读取无凭据元数据，未修改 key、epoch、TTL 或存储字段。
- Redis 缓存测试移除 service 依赖，原实体编码的 8 份字节结果冻结为 testdata，独立比较新编码。Grok 媒体资格规则归 account，JWT/档位解析通过 provider 注入；原测试断言保留。缓存及消费者定向 race 238 条、真实 Redis integration race 5 条通过，无失败或跳过；见 native-scheduler-codec-*-final.jsonl。初次测试迁移缺少原生规则调用的编译日志已归档并修正。
- 同步账号维护与调度缓存文档；上述集合含父子测试且存在重叠，不相加。依赖门禁随本批精确更新，旧账号、HTTP/设置图与最终全量验收继续实施；S16 保持实施中，不提交。

### 原生 OpenAI 额度与隐私重试（2026-09-19）

- 删除旧 OpenAIQuotaService、ProviderSet 转接与无消费者夹具。app 直接装配 account.OpenAIQuotaService；代理、TLS Router、Agent Identity 和 token 参数由 account/provider 组合，HTTP 与脱敏由 upstream 唯一执行。保留原 task 协调实例、查询时点和停止拥有者。
- 原额度、影子账号、重置、Agent Identity 恢复及并发 task 测试迁至 account/provider；窗口规则与 reset-credit 纯解析测试分别迁至 account 和 protocol/openai，原测试名和断言保留，见 native-openai-quota-test-mapping.json。
- 定向普通 race 84 条、unit race 211 条、隐私重试 race 6 条通过事件，无失败或跳过，集合不相加。证据为 native-openai-quota-race、native-openai-quota-unit-race-final、native-privacy-retry-race；初次 unit 编译发现旧管理端构造器遗漏新类型，作为本次迁移回归修正，原日志保留。
- native-openai-quota-lint-verified 退出零。新测试按实际文件/import 增加精确许可并保留角色基础限制，未扩大忽略规则。同步账号维护文档；继续迁移刷新测试及旧应用图，不宣布 S16 完成。

### 刷新旧类与重复测试实现清理（2026-09-19）

- 删除 `service.TokenRefreshService`、Grok 对账包装及其测试运行时门面；后台、手动对账、分页、并发/QPS、超时、条件写和成功后置断言直接调用 account/provider 的生产组件。注册顺序测试进入 app，保留六个平台的真实装配。映射见 native-refresh-attempt-test-mapping、native-refresh-background-test-mapping、native-refresh-final-consumer-mapping。
- 定向普通 race 32 条、unit race 177 条通过事件，无失败或跳过，集合有重叠，不相加；native-refresh-test-migration-lint-final 为零。首次迁移遗漏方法、nil 测试端口与旧管理构造依赖的诊断独立保存，已修正。
- OpenAI/Claude 请求 token 测试删除自行复制的 GetAccessToken 算法，直接组合真实 token source、刷新协调器、平台 executor 与 CAS 替身。Claude 的两个旧用例只验证测试自身忽略数据库错误的行为，未验证生产链；替代断言验证原生产读取失败不交换、持久化失败不发布新 token、回退旧 token 及原凭据不变。逐项说明见 native-request-token-test-mapping，未修改生产策略。
- 请求 token 定向 race 与 account/app unit lint 退出零，证据见 native-request-token-refresh-race-verified 和 native-request-token-lint。旧账号测试、用量/健康、网关/设置图及最终验收仍继续实施，当前不标记完成、不提交。

### 用量查询原生装配与额度包装清理（2026-09-19）

- Gemini 额度策略、Antigravity 查询和 Grok 额度展示删除旧包装，生产直接绑定 account 唯一实例。测试按规则、供应商解析与装配职责迁移，见 native-gemini-quota-mapping、native-quota-view-test-mapping。原排序、第三方豁免、账号成本口径、错误覆盖及缓存策略保持。
- Codex 用量探针和 Anthropic 技术请求进入 account/provider；Codex Header 解析及账号测试 Responses 报文进入 upstream/openai，旧调用方直接复用。app/account_oauth_usage.go 直接组合原生存储、共享 OAuthUsageCache、窗口统计和平台端口；新 HTTP、列表与报告不再依赖 AccountUsageService，Wire 已不再构造该旧类。Grok 管理探测、剩余旧 HTTP/测试消费者继续登记清理，不把本批视为 S16 全部完成。
- 依次取得 Gemini 109 条、额度展示 133 条、供应商适配 207 条、原生装配/消费者 139 条定向 race 通过事件，无失败或跳过，集合有重叠不相加；native-oauth-usage-direct-lint-final 为零。Wire 按手写装配生成。初次迁移的 import/别名遗漏与失去消费者的测试辅助符号已修正，诊断日志保留。
- 更新账号维护文档真实所有权。用量行为测试继续从旧服务迁到实际所有者；机械改写中的嵌套字面量遗漏单独归档并修正。阶段原文前 16,353 字节 SHA-256 仍为 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`，git diff --check 通过；不提交。

### 用量旧类清零与 Grok 原生探测（2026-09-19）

- 删除 AccountUsageService 类、构造器、核心及传输转接。旧 HTTP 中已无消费者的四个用量委托入口删除；管理报告仅保留窄查询端口，新生产路由早已直接使用 account/httpapi。剩余 service/account_usage_projection.go 只为账号测试、旧 Grok 独立调用及免费额度统计提供字段投影，不能视为整个 service 目录已清零。
- 原 Qoder/Codex 查询测试、窗口与 Fable 报文、身份交错、批量失败、缓存隔离及 Spark 影子测试分别迁至 account、account/provider、protocol/anthropic；对应关系见 native-oauth-usage-test-mapping、native-usage-window-test-mapping、native-usage-identity-test-mapping、native-usage-final-identity-mapping、native-usage-batch-test-mapping、native-spark-usage-test-mapping。定向 race 分别取得 94、14、24、10、15 条通过事件，集合重叠不相加；初次夹具遗漏原统计端口引起的 panic 已修正，保持原数据库查询断言。
- Grok 账单、主动额度、模型目录请求进入 account/provider.GrokQuotaTransport；平台端点资格复用 upstream，运营方 URL 约束进入 egress.OperatorURLPolicy。原生账号用例持有 ProbeRuntime，app 直接绑定管理、导入、用量及停止 hook，不再构造旧 GrokQuotaService。旧类暂留独立测试消费者，继续迁移。
- Grok 传输、绑定与迁入所有者的测试分别取得 80、123、19 条 race 通过事件。原账单重试次数、GET/POST 区别、状态映射、账号成本窗口及请求取消断言保持。native-account-usage-removed-lint-final 为零。
- 较宽的直接消费者集合 native-account-usage-removed-race-final 取得 776 条通过、1 条失败事件，不能记作整组通过。唯一失败是 `TestGrokQuotaServiceQueryQuotaPaidBillingSkipsActiveProbe`：原断言预期 2 条请求，实际捕获 3 条，现有异步模型同步可能参与计数。只保存当次证据，尚未执行旧 HEAD 追加复现或修复；依第 1 节已向用户请求限定范围确认。该项待确认，不放宽断言、不标记验收完成。

- 用户随后明确允许该项限定复现及夹具修复：在 S16 原 HEAD 取得必要证据；仅在确认原夹具问题后隔离后台模型同步，保留“两次账单请求、无主动推理”的原断言。不授权扩展到其他历史问题。复现使用仓库外 git archive，不切换分支、不覆盖工作树。

### 已批准的 Grok 夹具修复与账号测试所有者（2026-09-19）

- 用户允许后，使用 S16 原 HEAD 的仓库外 git archive 进行限定复现，生产代码未改。原异步模型查询被等待后，原“两次账单请求”断言稳定得到 3 条请求并失败；见 grok-fixture-old-head-repro、grok-fixture-repro-scope、原文件摘要及覆盖层。该失败是历史夹具证据，不记通过。
- 当前夹具提供有效模型目录快照，隔离与账单断言无关的后台同步，并等待原探测运行时停止；原两次请求及全部路径为 /v1/billing 的断言保持。精确用例 race 连续三轮通过；较宽的直接消费者补验取得 777 条通过事件，无失败或跳过，见 native-account-usage-approved-fixture-race。未修改生产模型同步、账单或推理行为，也未扩展历史问题排查。
- 账号测试的 Claude/OpenAI/Grok 纯请求载荷迁至对应 upstream；字节级模型字段替换进入 protocol/openai，原断言同批迁移。单次 TestRun 与 Gemini/Anthropic/Responses/Chat/Qoder 流事件解析进入 account/provider，移除通用键值状态；HTTP 仍独占响应写入与 Flush，真实写失败返回原错误并取消本次执行。
- Qoder 原完整测试交换与受控目标进入 account/provider；八个原测试移至 account/httpapi，组合真实 TestService 与 EventSink，不复制服务算法。定向 race 15 条通过事件，native-qoder-test-owner-lint-final 为零。旧文件仅剩其他网关测试仍使用的传输替身，其删除继续随消费者迁移。
- Gemini 请求构造与执行进入 account/provider，原两个报文测试迁入实际所有者。相关 unit race 412 条、四种凭据路径契约 5 条通过事件，无失败或跳过；native-gemini-test-owner-lint 为零。真实请求路径、Header、映射、事件顺序和自动 UA 隔离保持，未把本地传输替身称为真实供应商验证。
- Grok 账号测试改用原生记录、token source、供应商请求及健康规则；原纯额度观测与消费限额报文解析迁入 upstream/grok，旧网关复用同一实现。原消费者集合 race 985 条通过，无失败或跳过；HTTP 原断言及依赖门禁继续核对。上述集合重叠、不相加。初次遗漏符号及测试输入字段的编译诊断已保留并修正。
- 同步账号维护文档和 native-*-account-test-mapping。S16.3、旧 HTTP/设置/应用图清理及最终全量验收仍未完成；不更新 roadmap 完成数，不提交。

### 账号测试平台、客户端策略与健康规则（2026-09-19）

- Grok 六个原 HTTP 测试迁入 account/httpapi，真实 TestService、受控目标和 EventSink 直接组合；相关集合 race 9 条通过，native-grok-test-http-lint-final 为零。原测试缺失输入字段的初次编译诊断保留，已按 Type 指针契约修正。
- Anthropic、Vertex 与 Bedrock 测试执行迁入 account/provider，原签名来源区域、模型映射、403 写入、Header 及事件顺序保持。账号认证方案规则归 account，原测试分别迁入 account、account/provider 与 upstream/anthropic；定向 race 408 条通过，native-anthropic-test-owner-lint-final 为零，映射见 native-anthropic-account-test-mapping。
- OpenAI Responses、Chat、Compact 与图片执行进入 account/provider；影子凭据通过原生读取解析，task 认领继续使用原协调器，兼容入口只投影并写回原本拥有的本地观测字段。旧 test_compaction 空壳及无消费者合并函数删除。第一轮相关 race 290 条通过；后续原生目标直接接入后 race 535 条通过，见 native-openai-test-target-race-verified。
- 客户端许可规则归 account，OpenAIProbePolicy 组合原检测端口、Router、Profile、全局许可和 UA 回退，旧网关复用同一优先级。保留未配置自动探针错误及自定义检测器注入；原字符串识别、客户端原 Header 优先级、浏览器回退与强制 CLI 顺序保持。中间定向 race 548 条通过；初次遗漏已迁 Header 回调的函数值消费者和 import 的编译诊断独立保留，最终目标集合已补验。
- 429 正文与 OpenCode Go 时长解析归 upstream/openai，恢复窗口选择和观测套餐写入归 account；时间只在原需要处读取，套餐观测不回写其它凭据。原纯解析、窗口与归一化测试迁入对应所有者，native-openai-health-contract-race 122 条通过。原 Compact 附加映射、Fingerprint 组合和协商 Header 也各自只有一份实现，旧入口委托。
- 国产固定与自适应账号测试迁入 account/provider，保留原逐端点顺序、空集合拒绝和中间终态抑制；定向 race 189 条通过，native-cn-test-owner-lint 为零。原后台 Responses UA 经显式 TestRun.Automatic 传递，不借用旧 Context 重新存储业务状态。上述集合含父子测试且重叠，不相加。
- 更新 native-openai-account-test-mapping、native-openai-health-test-mapping、native-cn-account-test-mapping 及账号维护文档。模型同步、Antigravity 旧探测、旧 HTTP/设置/应用图和阶段最终验收继续执行；S16 保持实施中，不提交。

### 原生测试装配与模型目录（2026-09-19）

- app 已直接构造 TestTargets、各平台执行器和唯一 TestService；生产 Wire 不再构造 AccountTestService，计划测试与分组探测直接共用原生实例。旧独立构造暂留测试及未清零的旧管理门面，不能据此宣布整个 service/handler 已删除。
- 管理测试 Qoder 会话保留原作用域，由 AccountTestQoderSessions hook 在周期消费者之后停止；没有复制主请求会话缓存。测试和模型预览的临时 Agent Identity task 互斥由唯一 ProbeTasks 持有，持久账号仍使用原共享协调器。最终全局协调入口清理继续归 S16.5。
- 模型查询与通用列表解析迁入 account/provider.ModelCatalogue，18 个原模型契约及两个 CN 用例移至所有者。app 直接绑定原生存储、token source、URL 策略及读取上限，不经旧账号测试服务取回能力。定向 unit race 193 条通过，见 native-model-catalogue-race；原声明缺省、别名排序、请求字段、Grok 身份边界和错误脱敏断言保持。
- 真实 PostgreSQL 用量观测/恢复契约 integration race 49 条通过，无失败或跳过，覆盖原身份变化、窗口、取消及 outbox 失败边界。生产账号测试新装配在真实 PostgreSQL 下通过 OpenAI、Gemini、Anthropic、Kimi 四条原生读取至 SSE 链，取得 5 条通过事件；供应商传输为本地替身，未称为真实外部验证。
- native-account-test-factory-race 取得 94 条通过；新装配的 unit 与 integration 定向 lint 均为零，分别见 native-account-test-factory-lint-verified、native-account-test-factory-integration-lint。Wire 按手写装配生成，仍需阶段收尾二次稳定性与完整矩阵。
- 按实际删除列表移除 3 条精确旧许可及 3 条失效排除，账本见 deleted-import-permits-20260919。更新账号维护文档；计划原文摘要仍为 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`，diff 检查通过。不提交，继续清理旧测试、网关、健康和应用图。

### 删除账号测试旧类与迁移原断言（2026-09-19）

- OpenAI 的普通、429、影子、图片、Compact、自动 TLS/许可、Agent Identity，以及 CN 固定/自适应测试分别迁入 account/httpapi。原测试名、标签及业务断言保留，原 HTTP、响应体、批次请求及字段写入由窄替身记录；真实策略、平台执行与 TestService 没有在测试目录重建。
- Agent Identity 测试直接使用 ProbeTasks、原生凭据持久化和本地注册服务，验证一次恢复、两次上游调用、没有永久错误写入及一次连接失效。测试端点按实例注入，不再修改旧全局认证端点。定向普通 race 10 条通过；CN/HTTP 契约补验 race 88 条通过，无失败或跳过。
- 旧管理门面的两个独立构造测试改为接收原生测试/模型同步能力，保留批量编辑不触发探测及模型同步的原断言。Bedrock 跨链测试直接组合新账号执行器与旧转发，保留区域、签名、错误诊断、无上游调用和凭据不变的比较。
- 删除 AccountTestService、构造器、上下文执行门面及 12 个包装文件，清单见 native-account-test-deleted-wrappers。相应 ProviderSet 已删除，生产 app 和测试均不再引用该旧类。Antigravity 重试仍使用的三个 UA Context 转接函数暂留 antigravity_probe_context.go，必须随该执行适配迁出，未把它宣称为最终状态。
- 删除后的直接消费者 unit race 445 条通过，无失败或跳过，见 native-account-test-deleted-race。初次迁移遗漏传输替身的 bodies 字段、函数值消费者和 imports，以及脚本局部语法错误已修正，失败日志保留；没有删业务断言或扩展历史修复范围。两项随后暴露的无消费者测试类型已删除，定向 lint 正在核对最终结果。
- 对应测试映射见 native-openai-http-test-mapping、native-openai-image-test-mapping、native-openai-routing-test-mapping、native-cn-http-test-mapping、native-grok-failure-test-mapping、native-openai-agent-probe-test-mapping。旧额度、健康、网关/HTTP、设置和应用图清理与最终全量验收继续实施，S16 不标记完成、不提交。

### 2026-09-19：Grok 额度旧入口删除、健康窗口归属继续收敛

- 重新读取 S16 完整计划正文，并继续按 S16.3/S16.6 清理；没有扩大历史问题排查范围。
- 删除 `service.GrokQuotaService`、模型同步与统计转接；app 与管理端的剩余独立构造使用 `account/provider.NewGrokQuota`，共享一个原生 ProbeRuntime。25 项原额度测试中的账号用例断言迁入 provider，原调用者的健康存储替身仅留给仍未迁出的网关测试。普通 probe 拷贝/停止测试迁入 account。
- 已批准的 Grok 账单夹具修复随迁保留：有效目录缓存隔离异步 `/models`，等待停止后仍断言两次 `/billing`，无主动推理。删除一个永真、没有业务覆盖的类型非空断言；其同用例其他断言不变。
- `native-grok-quota-deleted-race.jsonl`：unit 定向 race 退出 0，935 条通过事件，无失败或跳过。原测试与新位置见 `native-grok-quota-test-mapping.json`。事件含父子测试，不与其他集合求和。
- Grok 自动暂停窗口规则进入 account；Anthropic 5h/7d/7d_oi 响应头解析进入 upstream，纯解析测试随迁。原 session 窗口维护、耗尽窗口与 Fable 模型级写入进入原生 HealthService，供应商 Adapter 只投影观测。保留原时钟读取位置、先清旧采样再写窗口/采样、最后恢复限流的顺序。
- `native-health-window-race.jsonl`：定向 unit race 退出 0，1,614 条通过事件，无失败或跳过；`native-health-window-lint-final.log` 为 0 issues。旧文件/测试标签映射见 `native-health-window-test-mapping.json`。中间迁移编译及 unused 诊断已处理，不作为通过证据。
- OpenAI 图片限流及能力拒绝的解析进入 upstream，账号资格和模型窗口写入进入 account；原图片转发、failover、运行阻断测试继续保留并验证。该批验证仍在执行，结果后续追加。
- 仍未完成：RateLimitService 的其余认证错误/平台默认处理，旧网关与 HTTP 聚合、settings/app 旧图，以及 S16 最终全量验收。roadmap 继续保持实施中，不能据这些定向结果宣布阶段完成。

### 2026-09-19：平台健康与模型冷却接入原生观测

- S16.3 继续拆分旧 RateLimitService，未将其整体改名搬迁：401 的凭据母账号、失效和冷却，400/402 拒绝状态，403 累计/HTML 豁免，API Key 熔断、Team 联动及模型窗口写入归 account；报文、Header、平台分类及模型规范化归 account/provider 与 upstream。调度反馈与请求重试仍保持各自原拥有者。
- Team 联动的互斥和去重表只有原生 `TeamLinkedHealth` 一份。app 绑定同一 HealthService、TeamLinkedHealth、RateLimitObserver 与 UpstreamHealth；旧入口只投影记录。原 API Key 熔断设置、滚动计数、三秒独立写入和成功不重置计数保持；原日志仍由同一技术后端写入。
- `HealthObservation` 显式携带本次模型、thinking 和 Images 端点意图。普通错误、模型不存在、套餐门控、Anthropic 硬窗口与临时规则的优先级保持；Spark 只写模型冷却，不把全局响应头套到影子窗口。Qoder/WS/重试循环没有在本批更改。
- 6 项原 401 用例（含子测试）迁入 provider；模型错误的 18 项原调用断言迁入 provider，原请求 Context 与调度跨界的两项仍留在旧测试入口。原生模型匹配继续调用 S03/S06 的唯一映射、默认目录及 Codex 规则，未创建新目录缓存。映射分别见 `native-unauthorized-test-mapping.json`、`native-model-health-test-mapping.json`。
- 已取得退出 0 的定向 race：`native-unauthorized-image-health-race.jsonl` 97 条，`native-apikey-health-race.jsonl` 10 条，`native-team-health-race-final.jsonl` 33 条，`native-platform-health-race.jsonl` 331 条，`native-upstream-health-race.jsonl` 281 条，`native-health-composed-race.jsonl` 404 条，`native-model-health-contract-race.jsonl` 66 条。均无失败或跳过，各集合重叠，不求和。
- 新的真实 PostgreSQL 装配验证直接取得 app 所绑定的原生观测入口，旧装配壳无仓储可供回退。验证唯一实例、Anthropic 耗尽窗口及 session 写回、图片能力不升级为整账号限流、影子 401 写母账号且保留凭据；`native-health-postgres-race.jsonl` 4 条通过事件，退出 0，无失败或跳过。
- 定向 unit lint `native-health-composed-lint-final.log` 为 0 issues；integration lint 单独串行补验中。首次并发启动 lint 被工具拒绝的记录保留，不记为通过。新增/删除路径按精确文件规则更新，不扩大目录豁免；`git diff --check` 通过。
- 本批仅清理迁移引入的编译/未使用符号诊断，未开展额外历史问题复现或修复。S16 仍处于实施中，旧网关、HTTP、设置和装配残留及最终矩阵尚未完成。

### 2026-09-19：健康装配直接读取原生配置

- app 的健康/恢复装配直接注入唯一 `account.RuntimeSettings`、token invalidator、计数缓存、账号存储与调度阻断端口，不再调用旧 `HealthOptions`/`RecoveryOptions` 取得生产依赖。旧网关阻断的剩余投影只转换记录；恢复和健康互相调用的绑定在发布前完成，构造不执行回调。
- Wire 生成成功，两次结果一致，证据 `native-health-wire-stability.json`；只生成 Wire，没有在本批运行 Ent 生成。新增生产依赖为原生账号配置、计数端口和已存在的 blocker，实例没有复制。
- `native-health-direct-app-postgres-race.jsonl`：真实 PostgreSQL 再验 4 条通过事件、无失败或跳过，退出 0。普通构建与对应 lint 正在补齐；该批结果不替代最终全仓命令和路由/设置/资金销项。

### 2026-09-19：补齐健康测试标签与剩余用量投影

- 普通构建发现的 unused 均为本次迁移后的无消费者包装或只由 unit 消费的夹具。将相应 HTTP/令牌测试辅助定义归入 unit 标签；原测试函数标签及断言不变。过载设置/529、429 设置/回退、OpenAI 窗口与影子守卫的原断言迁入 account/provider 或 account，旧私有执行转接删除。
- app 的 Gemini 预检改用原生 usage 端口投影，保留批量接口检测、原 SQL 参数和独立预检缓存；使用量查询与预检共用账号成本字段投影。原 `ActualCost != AccountCost` 测试移到 app，仍验证账号成本而非用户实扣。Grok 账单 URL 原断言直接组合 provider/平台唯一实现，旧纯包装删除。
- 本批退出 0：`native-health-normal.jsonl` 普通 201 条；`native-health-fixture-unit.jsonl` unit 44 条；`native-cooldown-race.jsonl` 42 条；`native-openai-window-health-race.jsonl` 68 条；`native-usage-last-helper-race.jsonl` 14 条。均无失败或跳过，集合不求和。标签及断言映射见 `native-health-fixture-build-mapping.json`、`native-cooldown-test-mapping.json`、`native-openai-window-health-test-mapping.json` 和 `native-usage-last-helper-mapping.json`。
- 定向普通 lint `native-health-normal-lint-final.log` 和 unit lint `native-health-fixtures-lint-unit.log` 均为 0 issues；app integration lint `native-health-postgres-lint-final.log` 为 0 issues。以上仍是迁移批次验证，不代替 S16 全仓收尾。
- 下一块继续按 S16.3 清理 Antigravity 管理探测及其旧重试适配，再处理其余旧网关/HTTP/装配消费者。没有新增历史问题排查任务。

### 2026-09-19：Antigravity 指定账号探测与重试绑定原生化

- `AntigravityProbe.Execute` 接管指定账号 token、project、模型、请求体与闭合平台探测。app 将其直接接入 `TestTargets`；删除旧 `TestConnection`、`TestConnectionWithProbeOptions`、日志回调与无消费者构建包装。管理/后台测试不再调用旧网关测试方法。
- `AntigravityRetry.Bind` 只组合原生平台循环与账号健康端口；重试算法继续唯一位于 upstream。credits、模型窗口、INTERNAL 500 使用同一 account 健康实例；旧转发仅投影 Ops、粘性及当前请求状态。UA 改为显式字段，删除 `antigravity_probe_context.go`，两个原 UA 断言仍保留。
- 首轮 `native-ag-retry-migration.jsonl` 有一项迁移回归：新视图未看到原传输在同请求内发布的模型限流，签名恢复错误多发请求。修复尝试视图同步后，`native-ag-retry-signature-regression.jsonl` 原一次请求/切号断言通过；没有削弱断言，也没有开展历史问题复现。
- `native-ag-retry-race.jsonl`：284 条通过事件，退出 0，无失败或跳过。`native-ag-probe-postgres-race.jsonl`：真实 PostgreSQL 装配的 3 条通过事件，验证 Claude/Gemini 请求、原 SSE 次序、显式 UA、响应关闭和停止后不新发推理。供应商使用本地传输夹具，不算真实外部验证。
- `native-ag-probe-lint-unit-final.log` 为 0 issues；普通测试/lint 和 integration lint 继续串行补齐。映射与回归说明见 `native-ag-probe-mapping.json`。Wire 已生成，SQL/Ent 不在本批修改范围。

### 2026-09-19：Antigravity 普通探测与计数断言补验

- `native-ag-probe-normal.jsonl` 普通定向测试 103 条通过事件；`native-ag-internal500-race.jsonl` 原 INTERNAL 500 解析/惩罚/计数断言迁到实际所有者后 24 条通过事件，均无失败或跳过、退出 0。旧 service 集合在后一命令中无匹配，未将其编译成功计作行为测试。
- INTERNAL 500 旧测试文件删除，仅被传输错误测试使用的最小写入观测结构归回该消费者文件。清理遵循原构建标签，没有删除业务断言。对应关系见 `native-ag-internal500-test-mapping.json`。

### 2026-09-19：Antigravity 健康转接删除与原生错误观测

- credits 清理、模型写入、家族 scope、智能重试解析及错误策略测试迁到实际 account/provider/upstream 所有者，原构建标签和业务断言保留。删除对应仅测试使用的私有包装；两个仍有生产消费者的模型限流调用随错误观测迁入原生 Adapter 后才删除。
- app 直接构造 `AntigravityErrorObserver`，与原生 retry 共用 Health、存储、发布和平台健康入口。旧转发只投影请求状态及清粘性动作；模型窗口/503 容量分类/429 兜底顺序及环境秒数覆盖保持。没有增加重试循环或共享状态。
- `native-ag-health-contract-race-final` 182 条、`native-ag-error-observer-bound-race` 158 条通过事件，均退出 0，无失败/跳过。真实 PostgreSQL app 再验 `native-ag-health-app-race` 7 条通过事件。集合重叠，不相加。
- `native-ag-health-lint-unit.log` 与 `native-ag-health-lint-normal.log` 均为 0 issues；Wire 重新生成成功。中途遗漏两个消费者的编译失败已修正，保留初始日志。映射见 `native-ag-health-test-mapping.json`。
- 继续清理管理页运行状态与评分投影，以及后续网关、HTTP、设置和旧应用图；阶段尚未完成，不提交。

### 2026-09-19：管理展示与重试测试归属继续收敛

- 管理列表评分直接接受原生账号投影、`schedulerSharedState` 的同一反馈与设置缓存；运行状态读取直接注入原生 quota settings。旧管理测试兼容入口委托 provider，不再往返转换旧账号实体。额度余量中性值、八小时有效期及次窗口折扣迁入 account 唯一实现；删除已无消费者的旧 BuildAdvancedAccountSchedulerScoreSnapshot 函数族。
- 三项评分快照契约迁入 app，保留共享 EWMA 数值、分组粘性和硬粘性断言；四项额度余量断言迁入 account，普通标签保持。`native-account-scoring-race-final` 19 条通过、无失败/跳过。定向普通 lint `native-account-scoring-lint-normal.log` 为 0 issues，Wire 按手写配置生成成功。
- 智能重试、单账号重试、credits 与自定义错误策略测试直接绑定生产 `AntigravityRetry`/upstream 循环。四项单账号 Context 读取测试迁入 requeststate，执行测试改用显式模式；保留次数、取消、响应、窗口与粘性断言。删除旧 `antigravity_compat_unit_test.go`，原最小传输替身仅保留给尚未迁出的转换链测试。
- 测试映射与各集合结果见 `native-account-scoring-test-mapping.json`、`native-ag-retry-test-ownership.json`；事件含父子测试、集合重叠，不求和。迁移期间的缺失常量、未改字段及缺少 URL 脱敏回调已按原依赖补齐，初次编译/执行失败不作为通过证据。剩余完整循环补验和 unit lint 继续执行。

### 2026-09-19：代理旧仓储删除与账号配置契约归属

- 删除无生产消费者的 `repository.NewProxyRepository`/`newProxyRepositoryWithSQL` 及测试转接。排序纯测试归 egress/postgres；代理身份更新、到期转移、账号快照清理和 outbox 事务测试迁至 tests/integration/account，保持原 normal/integration 标签、事务回滚与断言。复用原生 ProxyStore、同连接 ProxyChangesInTx 和调度 writer。
- `native-proxy-store-normal-race-final` 10 条通过；隔离 PostgreSQL 的 `native-proxy-store-postgres-race` 25 条通过；`native-proxy-store-lint-integration.log` 为 0 issues。迁移时遗漏的 Ent runtime 初始化已补齐，原失败日志保留。映射见 `native-proxy-store-test-mapping.json`。
- `native-account-batch-unit` 对 account 全部子包、Antigravity 和 app 取得 2,203 条通过事件、没有失败或测试级跳过；三个无测试文件包单列，不计作行为验证。该结果不代替阶段全仓验收。
- 账号固定窗口/日期、OpenAI 配置清理、并发数、凭据错误判断及 Grok 管理错误形状按所有者迁移。显式提供原时钟、时区加载和 seed，省略/空值及错误断言保留；删除两份 S06 unit 转接与无消费者的旧全局日期函数。`native-account-config-contract-race` 58 条、`native-account-config-boundaries-race` 32 条通过，无失败/跳过，映射见 `native-account-config-test-mapping.json`。
- 旧智能/单账号/credits 循环补验分别保留 29/23/28 条通过事件，完整错误/预检循环为 22 条；套餐/隐私/模型/thinking 断言 51 条，临时停调直接消费者 11 条。集合有重叠，不能相加；对应命令与日志分别归档。`native-account-display-lint-unit-complete.log` 为 0 issues。
- 继续保持实施中。当前仍有旧网关、HTTP、实体和设置/应用图需要清理，最终全仓 0/0/0、路由/设置/资金账本及进程矩阵尚未验收，不将定向结果作为完成依据。

### 2026-09-19：模型/端点投影与 B01 剩余重复初始化

- 模型匹配、OpenAI OAuth 可用模型、配置更新后映射和纯通配符断言迁到 account/provider 与 routing/modelmap，原命名、标签和并行声明保留。定向 race `native-account-model-rules-race` 101 条通过；两份匹配测试转接删除。
- Grok 文本/媒体端点选择进入原生 provider，旧账号仅委托；普通/Gemini 地址和创建边界直接验证 account。`native-account-endpoint-race` 46 条通过；Ollama 导入管理字段契约迁移后的 normal race 1 条通过，旧 buildAccountForCreate 包装删除。
- 国产平台数值/时间/额度解析归 usageprovider、kimi、zhipu；CN 窗口及阈值断言归 account，管理错误形状归 HTTP，平台归一化归 routing。`native-cn-contracts-race-final` 85 条通过，无失败/跳过。两个 Responses 端点/报文投影接入 openaiforward，保留原调用时机；`native-cn-request-adapter-race` 273 条通过、1 项已登记 WS 非 response.create 夹具跳过，未计为通过。映射见 `native-account-model-endpoint-cn-mapping.json`。
- 全仓普通 lint 检查点只报一个新 unused：Responses 默认 URL 的旧纯包装。将原 7 个地址子用例迁至 infra/httpclient 后删除该包装，不删断言。后续全量 lint 继续补验。
- B01 最终清理发现的重复设置仍属于已确认范围：17 个测试进程、265 个文件原有 1,488 处 TestMode 设置已归并到 TestMain；逐文件测试名和 t.Parallel 数量核对未变，清单见 `b01-remaining-mode-ownership.json`。这不改变生产 Gin 模式。
- 首次 B01 扩充回归捕获本次迁移的空观测短路回归：旧 UpdateSessionWindow 在缺少 status Header 时先返回，新包装提前访问 HealthCore，导致未装配可选依赖的原会话测试 panic。按原 HEAD 源码恢复空观测先返回，15 条定向 race 通过，原业务断言保留；证据 `session-window-original-short-circuit.go.txt` 与 `session-window-migration-regression-race.jsonl`。未扩展历史问题修复，原失败日志保留，B01 整组复验仍在进行。

- B01 剩余初始化归并后的整组复验退出 0：`b01-remaining-race-final.json` 记录 3,826 条通过事件、1 项既有 WS 夹具跳过，无失败或 race 报告。原短路回归失败日志与源码对照均保留。

### 2026-09-19：全仓 lint 中间检查点

- 串行执行完整 normal/unit/integration lint，不截断诊断。normal、unit 首次退出 0；integration 的 10 个原生 Key/分组 import 许可遗漏和 1 个格式诊断按实际文件修正，复验退出 0，得到 0/0/0。命令、版本、退出码见 `checkpoint-lint-20260919.json` 与 `checkpoint-lint-integration-20260919-verified.json`。
- 编辑 integration 末尾规则时，临时脚本误覆盖 gosec 段标题，保留配置解析失败日志并恢复原段标题；排除项、严重性和置信度未变。没有扩大忽略规则；仍引用旧实体的测试许可继续登记，须随剩余迁移删除。
- 这是累计清理的检查点，最终代码、规则和旧包清零后仍需按计划完成全量测试、构建、门禁夹具和进程验收。继续处理旧 HTTP 及应用图，不标记 S16 完成。

### 2026-09-20：管理 HTTP 转接与断言归属收敛

- 按 S16.4/S16.6 迁移账号批量删除、创建与凭据 HTTP 断言，原生 ManagementHandler 直接组合窄管理端口。分别取得 race 3、2、8 条通过事件，无失败或跳过；不再通过旧 AdminService 执行这些断言。
- 兑换校验、有效期、批量 null 清空、CSV 排序及完整端点断言迁入 billing/httpapi；只使用兑换管理端口和原生 RedeemService。18 条定向 race 通过；旧 RedeemHandler 删除，原混合路由测试中的兑换条目由同名原生测试承接。
- 删除 12 个已核对无生产/测试消费者的旧 HTTP 文件，清单见 http-unused-wrapper-deletions.json。订阅、公开兑换、Ops 和创作的剩余构造引用直接使用所属模块；Wire 按手写类型生成成功。原路由/响应契约没有更改。
- Ops 告警/时间桶测试迁入所属同包，删除专供旧测试访问的四个 Compat 导出；系统更新/回退/重启测试使用原生 maintenance 锁及窄认领替身，保留原状态、预算和响应断言。代理导入/导出、排序及端点测试迁入 egress/httpapi，异步探测由测试拥有并在退出前等待；分组字段省略/显式值及平台枚举测试迁入 routing/httpapi，构建标签保持。
- 管理员幂等测试归入 idempotency/httpapi；原 HTTP 重放/失败夹具迁入其 testkit，供剩余跨模块测试共享。该内存夹具不作为真实 PostgreSQL 事务证明，也不重建旧 service 聚合。
- 删除 8 个经消费者核对已无引用的 service 构造/纯投影，见 service-unused-wrapper-deletions.json；没有把仍有方法消费者的文件误删。各原测试对应、构建文件和验证事件见 native-admin-http-test-mapping.json，事件集合重叠，不相加。
- 继续清理旧应用图与网关；本批 lint 和后续最终矩阵未完成，roadmap 保持实施中，不提交。

### 2026-09-20：账号管理边界与维护事务继续去旧入口

- 分组复制/恢复测试直接调用 routing/httpapi；管理员鉴权投影改为 identity/httpapi。原失败落库后的恢复、重放及副作用次数断言保留；与其他管理消费者的定向 race 59 条通过。
- 账号影子刷新早拒和诊断敏感字段拒绝直接验证原生账号/调度能力；删除旧诊断门面和 setter。账号重新授权、Extra 增量合并、废弃字段、Qoder/Grok/Antigravity 管理刷新、Gemini tier CAS 断言迁入 account/httpapi，原字段及取消/身份比较不变。中间遗漏列表端口及嵌入端口歧义属于迁移编译问题，已修正；失败日志保留，验证结果以 verified 文件为准。
- 账号复制、列表 ETag/304、压缩大小、评分分页/过滤和批量查询次数直接验证原生用例。删除相应旧 List、Duplicate、CheckMixedChannel、ApplyOAuthCredentials 及无消费者的 refreshSingleAccount；不将仍有消费者的管理门面宣称清零。
- Key 管理 HTTP 断言迁入 apikey/httpapi；原 S05 PostgreSQL 回滚测试直接组合 apikey.Admin、KeyStore 和身份同连接参与能力。两项真实 integration race 通过：非法分组不能先重置消费，数据库配置更新拒绝必须整体回滚，解除拒绝后同请求原子成功。测试还暂存原 repository 测试目录，后续随其余存储契约迁移。
- 维护锁与真实 pg_dump/psql 回归迁入 tests/integration/maintenance，沿用原镜像、SQL 与业务断言；直接使用原生维护锁及 OperationLeaseStore，删除旧 service 构造。4 条 integration race 通过，无失败/跳过，包括成功恢复、SQL/取消/损坏输入回滚和锁接管后旧持有者拒绝。
- 读取失败的无报文诊断实现归 gateway/httpapi，媒体解析直接调用同包既有诊断；原读取/解析日志及大小上限测试同步迁入，删除旧日志委托与测试常量。新测试进程在 TestMain 初始化 Gin 模式，不引入测试内重复设置。
- 本批测试映射及事件见 native-admin-http-test-mapping.json；迁移中未增加历史缺陷审计或修复范围。管理清理定向 unit lint 为 0 issues，完整 normal/unit/integration 检查继续串行执行；S16 最终清零与验收仍在进行。

### 2026-09-20：目录投影、HTTP 检查点与剩余项

- 平台管理目录的唯一投影移入 routing/provider，app 直接绑定；逐调用读取动态 Grok 目录、Google One 保守模型和 Qoder 站点差异保持。旧 service.AccountAdminCatalog 包装删除，相关账号模型展示/同步断言迁入原生 HTTP，未改同步失败的错误脱敏或读取上限。账号维护及模型目录文档同步实际来源。
- 最后一项“批量更新不得发起探测”断言按生产结构分别绑定 ManagementHandler 与 TestHandler，仍观察真实平台执行器的请求与状态写回计数；删除旧 accountProbeBindings、Test 方法及构造参数。原 API 契约及导入消费者同步调整，定向 race 通过；具体事件见 native-admin-http-test-mapping.json。
- 完整 normal/unit/integration lint 串行检查为 0/0/0，命令与日志见 http-cleanup-lint-20260920.json。这是当前累积检查点，不代替剩余迁移完成后的最终矩阵。
- 原计划前 16,353 字节 SHA-256 仍为 1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9；索引未暂存，HEAD 未变，SQL 与 S00—S15 冻结证据无改动，git diff --check 通过。
- 当前 service/repository/handler 仍分别有 419/16/103 个生产 Go 文件（本次后续删除会增量更新）；其中有真实网关适配、实体投影和旧装配，不能一概删除或提前标记最终完成。继续按 S16.2—S16.7 清理，roadmap 保持实施中，不提交。

### 2026-09-20：旧账号 HTTP 门面清零

- Codex 原 19 项纯规则断言迁入 account，显式输入原时钟与 OAuth client 值；索引副本隔离旧包装与已存在的原生同名测试逐项一致，删除重复包装并记录映射。导入流程、Agent Identity 校验和 HTTP 废弃字段回归直接组合 CodexImporter、Archive 及原生私钥解析，删除旧 Codex handler 和所有测试转接。
- 账号导入/导出 HTTP 测试直接绑定 Archive，模板读取使用原生 RuntimeSettings；保留缺省/显式 null、白名单、导出凭据、影子排除、选中 ID、排序和代理复用断言。迁移初次遗漏两个响应值类型导致编译失败，已补齐实际 transfer 类型；初始失败日志保留，验收使用后续删除门面整组复验。
- 最后两个 AccountHandler 构造消费者（API 契约和无自动 Grok 探测的创建）改绑原生管理 Handler；API 契约仍调用真实 account.Admin 的批量规则，窄存储端口保留原写入观察，没有替换成固定 HTTP 结果。
- 删除旧 AccountHandler、管理/刷新/归档投影和影子展示包装。原请求 DTO、展示 DTO 和新 handler 全部直接引用所属模块，未在测试目录重建旧 AccountHandler。Grok OAuth 的未迁兼容构造仍保留，其队列与授权迁移另行继续处理。
- 纯 Codex、Codex 流程和删除账号门面的定向 race 均退出 0，无失败或跳过；事件及映射见 native-admin-http-test-mapping.json / native-codex-core-test-mapping.json。全仓 unit lint 继续补验，阶段仍在实施中，不提交。

### 2026-09-20：旧管理员 HTTP 包删除与原生设置装配

- 旧 Grok/OpenAI 授权、用户管理、搜索截断及辅助 HTTP 断言已改绑所属原生入口。生产 app 不再构造 AdminService；账号导入探测、诊断读取直接注入账号/代理/分组能力，剩余诊断实体投影仍登记后续清理，未把它描述为全部旧 service 已清零。
- 迁移用户 CRUD 夹具时，临时复制工具误覆盖本阶段已有的未跟踪 identity HTTP 夹具；已从本任务原始 apply_patch 完整恢复，并将新增窄端口夹具另存 admin_user_crud_fixture_test.go。原角色、活动、已删除用户、批量限制及新 CRUD 断言复验通过；identity-fixture-recovery.json 保存恢复来源。复制工具增加目标存在即拒绝保护；没有提交或恢复其它任务文件。
- SMTP TLS、预聚合读取/更新和全部旧综合设置 HTTP 测试迁至原生 HTTP 包或 app 组合契约。旧 internal/handler/admin 目录已删除；路由形状夹具直接使用原生端点，原服务 API 契约的 GET 设置入口直接绑定 settings/httpapi。综合输入、step-up、局部字段、敏感值、一次写入和提交后失败断言保留，不在测试目录重建旧 SettingHandler。
- 管理设置普通集合取得 50 条通过事件；设置、路由与直接消费者定向 race 取得 104 条通过事件，无失败或跳过。迁移初次漏接 CreativeWorkerStatus 回调导致 500，已改绑原生 worker.Status 的未运行零值；原状态字段断言不变。编译遗漏、失败日志均保留，使用 native-settings-binding-race.jsonl 作为本批复验结果。
- 账号测试、模型同步、Grok 额度和 OpenAI 授权直接注入唯一 gateway.RuntimeSettings；OAuth 用量读取直接注入 account.QuotaSettingsCache。Grok 端点映射归 gateway/provider，历史默认模型升级归 gateway.RuntimeSettings；旧入口只对尚存消费者委托。启动不再借 SettingService 安装创作回调，生产综合应用器继续直接绑定唯一 worker；Wire 生成成功。
- OAuth 配置回退、身份默认赠送读取、Grok 历史默认升级和允许 Codex 插件断言改绑原生设置实现；18 条定向 race 通过。映射与不相加的验证集合见 native-admin-http-test-mapping.json、native-settings-http-cleanup-results.json。继续处理旧 SettingsService、网关与存储残留，阶段未完成。

### 2026-09-20：设置发布、公开投影与分组运行读取去旧依赖

- 公开 API/embed 的功能、OAuth 来源、敏感字段和版本断言改为直接调用 app 的真实 site 装配；原公开设置 Handler 删除。用户用量 API 契约直接调用 usage/httpapi，Key 只通过窄查询投影，旧 UsageHandler 包装删除。公开/身份直接消费者 race 44 条、公开 HTTP/用量/转发设置 race 38 条通过，无失败或测试级跳过。
- backend mode 五项读取/缺键/错误/缓存断言归 admission；迟回源覆盖管理更新回归改用真实综合 HTTP、composite.Runtime 与 app 应用器，仍保留并发屏障及最终值断言。该组合与身份额度读取取得 15 条 race 通过事件；未匹配测试的 service 包仅作编译检查，不计行为验证。
- 额度合并与来源读取归 identity，系统及来源额度提交通过原生准备器、UpdateSession 和单次 Store 提交验证。空 map 清空、nil 保留、显式零和负数拒绝断言全部保留，13 条 race 通过；旧合并 helper 和无消费者的两个整体更新包装删除。
- 复制 backend mode 断言的临时 AST 工具不支持接收者通配符，首次复制失败后误执行了删除；已从原 HEAD 精确提取这五项断言及窄 GetValue 替身，改为原生构造并验证通过。其它已迁移内容未回退；后续复制与删除分开检查退出码。
- 删除旧设置的无消费者代理注入、版本 setter、创作状态回调和状态读取。app 的账号能力直接依赖原生设置；其生产共享缓存不增加实例。旧综合写入/读取和网关消费者仍在后续清理范围，没有提前声称 SettingService 清零。
- 分组默认模型候选唯一投影归 routing/provider；管理校验的动态调度权重读取归 scheduler，保持每次单批回源、显式零、非法覆盖回退和读取失败错误，未借用请求热缓存。app 直接注入 Store/AdminDefaults，旧分组夹具委托同一实现。分组/装配/调度定向 race 158 条通过；Wire 生成成功。各集合有重叠，不相加。
- native-settings-cleanup-lint-unit-final.log 在本轮较早检查点为 0 issues；后续新路径与删除继续补验。映射和结果更新至 native-admin-http-test-mapping.json、native-settings-http-cleanup-results.json。git diff --check 通过，roadmap 仍为实施中，不提交。

### 2026-09-20：资料 HTTP、请求资源与 Ops 观测实现归属

- 用户资料、身份摘要、绑定/解绑、登录用户展示及全部会话撤销断言直接使用 identity HTTP/核心和 app 的原生设置投影；原 UserHandler 聚合删除，推广路由单独绑定推广 Handler。异步资料副作用由测试自己的 lifecycle.Tasks 等待，不借旧全局后台入口。
- 首次绑定复验发现新夹具遗漏原无数据库 AuthRepository，已绑定原生 AuthState/AuthRepository 并保留 nil 数据库边界；没有修改绑定规则。修复后 native-user-profile-http-race-final 14 条通过，初次 panic 日志保留。
- 用户幂等 helper 测试归 idempotency/httpapi，复用已有内存 testkit，不保留第二份内存状态算法；实际重放和并发单次副作用断言不变。图片独立限流的五项测试归 scheduler，旧类型别名删除，HTTP 直接使用同一个 ImageConcurrencyLimiter。native-request-helpers-race 13 条通过，无失败或跳过。
- Ops 请求观测、HTTP/WS 客户端传输标记及错误规则绑定迁入 gateway/httpapi。两个旧实现文件删除；旧 client transport 文件只剩尚未清理的平台协议决策。147 个生产/测试消费者直接绑定新符号，旧键、错误字段、首个错误规则、每 turn 去重和跳过监控判定保持。映射见 native-observation-symbol-ownership.json、native-http-observation-consumers.json。
- 迁移工具为既有默认 import 再加显式同名 alias，首次编译报告重复 import；已按同一路径去重并修正工具保护。之后普通契约 59 条、定向 race 75 条通过，无失败；日志与映射见 native-observation-cleanup-results.json。仍有旧 Ops 捕获 writer、队列绑定、网关编排和实体需要迁移，不将本批声明为 S16.4 全部完成。
- 本轮原计划正文摘要未变；diff 检查通过。全仓 unit lint 在前一批检查点仍为 0，本批新路径检查正在进行；最终验收矩阵未开始，阶段保持实施中，不提交。

### 2026-09-20：观测脱敏与并发错误转接继续清零

- Ops 中供调用层使用的 URL query/fragment 截断迁入 pkg/logredact，保持原字符串处理及取时点，调用者直接引用唯一 SafeUpstreamURL；删除 CompatSafeUpstreamURL 及原 service 测试入口。原七个输入子项随测试迁移，脱敏与直接转发消费者定向 race 34 条通过。
- 删除 handler 并发错误映射包装及三个状态/错误码别名，调用方直接使用 gateway/httpapi；原超时、队满、取消、deadline 和 Redis 错误断言迁入同包，6 条 race 通过。实际流错误和 failover 消费者继续补验，未把只编译的包记为行为通过。
- native-http-observation-lint-unit.log 为 0 issues；这一检查覆盖本轮 Ops 实现和147个调用者改绑，后续变更继续增量验证。阶段未完成，不提交。

### 2026-09-20：Ops 纯分类、端点与等待资源边界

- 错误类型、阶段、严重性、所有者/来源及 SLA 排除规则归 ops；HTTP 只提供 ErrorClassificationInput 观测值。原纯类型断言迁入 ops，其余请求标记/上游归属断言继续调用真实分类；147 条定向 race 通过。遗漏的路由容量提示调用已改为同一原生函数，初始编译失败保留。
- 规范入站路径、Responses/Compact 子路径及实际上游端点 Context 归 gateway/httpapi。两个仍依赖旧账号/转发结果的断言继续保留到后续迁移，原其它端点断言直接进入所属模块；217 条 race 通过。中间的复制工具导入去重编译错误和测试自引用已修正，失败日志不计通过。
- AST 改绑造成的限定名跨行/注释夹在选择符中已在42个受影响文件内整理，保持注释与代码语义；工具改为保留已有 import，不再追加同路径别名。修改范围见 native-import-layout-repaired.json，未对全仓执行无关格式化。
- 请求取消释放直接调用 scheduler.WrapRelease，并显式传入原 ReleaseOnCancel；旧包的释放/退避包装删除，原六项释放与六项退避测试及 benchmark 源码迁至 scheduler（不执行 benchmark）。客户端识别与心跳/等待转接直接调用 gateway/httpapi；累计定向 race 50 条，原生所有者补验19条，集合重叠不相加。
- 旧 NewConcurrencyHelper 与 gateway_helper.go 删除，生产改用原生等待构造，Key 统计从已完成准入的 gateway_effective_key 读取。真实授权入口原本同时写该视图与旧投影；统计测试改为构造相同已准入视图，保留 ID=77 和一次取得/释放断言。等待、槽位、取消与 Qoder 直接消费者49条 race通过；十秒心跳默认值归同一 HTTP 实现，未改变 Qoder 完成释放策略。
- Google token payload 验证断言归 identity/provider；删除 Google 与钉钉旧类型别名和验证委托，HTTP状态/大小常量直接引用 identity/httpapi。首次自动改绑误指 gateway/httpapi，已按真实所有者更正；27条声明校验/One Tap/钉钉回归通过，原失败日志保留。
- 流错误/failover消费者44条 race通过；映射与结果均已追加。unit lint 正在补查本批所有路径；旧网关/HTTP 捕获/设置/存储及完整应用图仍待清零，最终全量与进程矩阵尚未验收。计划正文摘要、SQL和冻结资料要求不变，阶段继续实施，不提交。

### 2026-09-20：HTTP 响应捕获与唯一 Ops 队列显式绑定

- 旧 Ops writer、SSE 分片解析、流错误和请求捕获完整迁入 gateway/httpapi，错误分类继续由 ops 唯一实现。app 注入原生队列和只读观测端口，真实拒绝标记及已加载失败 Key 与认证主体分开；原响应复用、在途写入等待及专用 Cyber 只记录一次的断言保留。
- 删除旧全局队列构造、健康转接与中间件包装。旧 Cyber 独立测试直接注入自己的同步观察队列，不再通过生产全局替换；生产只使用生命周期登记的同一个队列。HTTP 队列端口只开放 Enqueue，停止与健康由 app/ops 持有。
- capture 迁移定向 race 155 条通过；删除全局转接后的结果另列 native-ops-explicit-queue-race.jsonl，各集合重叠不相加。初次编译遗漏旧 Live 测试的私有 key 已按原字符串补齐；unit lint 的一项无消费者测试 flush helper 随旧队列夹具清理删除，不增加豁免。
- UTF-8 字节截断归 pkg/logredact，保持不 trim 的原 helper 与需要 trim 的值处理两种语义；删除 Ops 导出的兼容转接。消费者与断言映射已更新，后续继续清理文本 HTTP 和应用图；最终验收尚未完成，不提交。

### 2026-09-20：文本输出、失败契约与完成提交收敛

- 文本流终态、请求 ID、错误码、严格 stream 字段及额度错误映射断言直接迁入 gateway/httpapi；旧 stream_error_event、openai_stream_validation 和 logging 包装删除。文本定向 race 79 条、连同额度/审核直接消费者复验100条通过，集合重叠不相加。
- UpstreamFailoverError、失败阶段/归属/换号指令和 RetryFailure 投影归 gateway/forward，旧类型定义删除，实际消费者直接引用原生类型。响应头使用相同 map 值形状，HTTP 测试通过 Header 类型读取原大小写语义；核心不导入 net/http。OpenAI 容量/大小识别留 gateway/provider，不向纯契约引入具体平台。
- 旧 failover_loop 删除，原重试状态、退避、取消、预算及缓存计费测试直接组合原生契约和 failover 算法；HTTP 断开标记单独归 HTTP Adapter。广泛直接消费者 race551条、迁移原断言后404条通过。首次自动替换误改一个同名结构字段、遗漏 nil 的类型与嵌套常量，已修复；原失败日志保留。
- 完成提交的冻结、入队和同步兜底归 completion.SubmitTask；HTTP 保留不同日志组件及入口策略，旧对象只选择原策略并委托。普通 drop/sample、停止池兜底、图片 mandatory、Qoder 的池拒绝和无池脱离取消分别保持，未增加队列、重试或生命周期承诺。原任务测试改为原生提交器，50条race通过；无消费者的 OpenAI 普通包装由 lint 指出后删除。
- Wire 已生成；unit lint 曾发现 responses_attempts 重复 import，已统一引用；后续审核批次继续补验。所有映射、日志与各集合结果已归档，计划正文摘要不变，索引为空。

### 2026-09-20：审核生产外壳与私有测试导出清零

- app、网关、创作任务及生命周期直接接收唯一 moderation.ContentModerationService，删除旧 ContentModerationService 和 Wrap/构造/用户/代理适配。真实 app 的领域端口投影继续保留，未复制队列、缓存或规则。
- 无媒体留存与对照组、内存图片的摘录断言移入 moderation 同包；旧测试整套审核存储替身删除，复用原生包既有相同替身。HTTP 链夹具直接装配原审核客户端，并由当前测试等待后台任务；首次清理遗漏 Stop 的 error 返回，已修复并检查退出错误。
- 逐符号确认151个 Legacy 私有导出没有消费者后，删除 moderation 的四个 legacy 出口文件；没有为迁移扩大生产 API。原测试标签与断言保留，本地 HTTP 夹具的精确文件许可随原测试迁移，不放开生产核心或整个目录。
- 审核/Cyber/无媒体留存相关race187条通过，无失败或跳过；Wire 成功。完成任务和审核变更的 unit lint 只指出已无消费者的旧提交包装，删除后继续补验。最终全量、进程与旧图清零仍待完成，roadmap 保持实施中，不提交。

### 2026-09-20：身份纯契约、验证码入口与任务 HTTP 包装清理

- OIDC 身份标识、授权 URL、真实 JWK/签名解析，LinuxDo 用户信息、token/error 解析及重定向断言分别迁入 identity、provider 和 HTTP；原跨模块 OAuth/pending/绑定测试继续调用真实流程。删除对应旧配置投影与无消费者的 AuthResponse，现有 cookie、类型和函数别名直接改绑原所有者。
- 自动改绑首次遇到测试声明及 map key，已分别保留声明名、改绑实际常量键；微信两个可变测试端点被误识别为常量转接，已恢复其原局部变量与本地 HTTP 夹具，未更改真实默认端点。两项变量随剩余微信 HTTP 夹具迁移继续清理。修复后的 OAuth/绑定/provider 定向 race112条通过，失败日志保留。
- 原验证码 JSON 八类输入断言直接使用原生请求结构，删除旧 unit 类型副本。OAuth 启动与 Passkey 门禁直接装配原生 Session、Pending 和六个提供方 Handler；腾讯设置仍由真实 RuntimeSettings 读取，验证替身只观察票据。实际门禁之后的无关端口保持未安装，确保缺票据时不创建 cookie、授权请求或 ceremony。
- 批量图片旧 Handler/构造/模型别名转接删除。app 直接返回原生 HTTP Handler 并绑定请求 activity，访问投影按原方式读取；Gemini 与批量图片别名断言移入所属 HTTP 包。路由/任务/模型定向race207条通过。认证主体与任务权限没有新增放行路径。
- 审核/完成提交批次 unit lint 已为0，身份别名/任务 HTTP 批次 unit lint 也为0；后续验证码/错误类型增量继续补验。Wire 已成功生成，原计划正文与其它阶段冻结资料保留，不提交。

### 2026-09-20：分组错误值与原生基础资源绑定

- 分组模型不支持的值类型、错误文案及二十项截断归 routing，旧错误定义与唯一消费者清零的字符串复制 helper 删除；HTTP/调度消费者直接识别原生类型。保留 nil、空模型列表、原请求模型和默认错误形状，定向race20条通过。
- 验证码与 Passkey 原断言迁移后race41条通过；首次设置替身误写指针返回类型已修正，未改变设置读取或验证码失败语义。
- 代理到期构造直接归 app，删除 service 的构造转接；OAuth 用量缓存、摘要会话存储和 Anthropic 指纹原生构造不再经 service.ProviderSet。其余旧执行适配仍在原集合，未改名伪装成已清零应用图；生命周期顺序保持。
- 普通构建集合已对本轮 HTTP、身份、审核、Ops、转发及 app 直接消费者执行定向复验，独立结果归档 native-http-owner-normal.jsonl，不替代最终全量验收。工作区 diff 检查通过，SQL migration 与 S00—S15 冻结证据无差异，计划正文摘要保持不变，索引为空。

### 2026-09-20：创作运行设置与容量读取退出旧聚合

- 创作运行开关/模型列表改由 app 直接构造 creative.RuntimeSettings，任务拿到同一 Store 的即时读取器；原三个读取/规范化测试迁入 creative，删除 SettingService 的对应方法、字段与 once 包装。未增加缓存、数据库查询或改变缺键默认开启。
- 容量查询直接注入唯一 account.QuotaSettingsCache，读取时点仍在获得容量记录后；不再借 SettingService 和旧容量读取接口取回同一个缓存。旧账号投影与单独测试的历史接口继续登记清理。
- 把原生构造移出旧 Wire 集合时，Wire 指出 egress.NewProxyService 是原集合内未使用项；该项删除而非强行加入新图。最初生成失败使后续编译仍读旧生成物，失败日志已归档；成功重新生成后，合并复验通过，见 native-creative-capacity-providers-race-final.jsonl。
- 不提交；阶段仍在实施中。后续必须继续处理旧身份 HTTP/设置构造、账号实体与执行投影、任务适配和剩余应用图，再完成全量矩阵，不能以当前定向通过代替 S16 完成。

### 2026-09-20：并发构造、日志注入与会话租约测试归属

- service.NewConcurrencyService 与 LegacySchedulerDiagnostics 删除；app、原测试和剩余适配直接构造 scheduler.ConcurrencyService，并显式注入原日志后端。键值字段转为 zap.Field 的唯一技术实现移到 infra/telemetry/logging.Event；原 error/warn/debug 分派、非法键和孤立尾值处理不变。
- 并发、等待、槽位、生命周期相关定向race201条通过，unit lint为0。没有新增并发实例或默认全局日志绑定；原测试继续保留相同缓存与参数。
- 真实存储复验首次在 repository.TestMain 启动 PostgreSQL 容器时因 Docker socket 超时失败，两项测试均未执行。Docker 后续响应正常，本次容器已由测试框架回收；未改动其它容器或重启 Docker。原命令不变重跑取得2条实际Redis测试通过，不把启动失败或无执行记录当作行为通过。
- S07 的两个纯 Redis 租约测试随后移至 scheduler/rediscache，改用同一 AccountCodec 与原生 SnapshotCache，复用该包已存在的真实Redis隔离夹具，删除旧 repository 测试文件。比较所有者令牌、重复释放、等待故障放行与实际计数断言未改；新所有者补验日志为 native-scheduler-lease-owner-integration-race.jsonl。
- 创作/容量/基础装配复验165条通过，unit lint为0；普通定向集合1433条通过，provider无匹配测试包单列且不计行为证据。以上集合重叠不相加。仍未完成旧包清零及最终全量矩阵，不提交。

### 2026-09-20：容量测试与平台薄包装继续清零

- app 容量装配的会话输入直接使用 scheduler.SessionLimitCache，复用原窄视图与唯一实例，文件不再依赖 service。原容量测试及查询投影夹具移入 routing，以 account.Record 和原生 CapacitySnapshot 承接；旧仅供这些测试使用的容量转接删除。7条定向race通过，仍保留批量查询、配额过滤、去重和计数断言。
- 删除平台 token key、Bedrock signer、Grok codec、模型缺失判定、Gemini 签名清理、错误规则构造和 Compact 输出的薄包装。直接调用保持原 AccountRecordView 投影、uuid.NewString 注入、占位签名及日志观察；不提前生成 ID、不查询额外数据或复制平台状态。
- 函数值消费者首次漏改导致编译失败，已把原参数投影显式保留在实际 Adapter 中；修复后首批相关race1220条通过，无失败或跳过。继续删除账号错误/敏感字段与平台规则的只读别名；扩展复验日志和逐符号归属单列，不能把首批通过当作全部后续改动通过。
- 原计划正文不变，保持 main 和原 HEAD，未提交。仍有旧账号/执行/身份/任务适配及最终验收工作，不更新为17/17。

### 2026-09-20：Responses Lite 实际报文算法归属

- Responses Lite 的 namespace/additional_tools 合并、重复冲突判断、reasoning 全轮次、并行工具约束及 JSON 重建迁入 upstream/openai，算法仅保留一份。14个原纯报文测试随实现迁移，原 unit 标签、未知字段/大整数及稳定载荷断言保留。
- 类型化校验错误归同一平台包，实际 HTTP 前置适配通过 Parameter 读取原 param；不为测试暴露字段或改写错误文本。账号是否适用 Lite 以及 OAuth/API Key 选择仍由尚存执行适配显式裁决，未提前读取凭据或改变资格边界。
- 非流/透传/WS 消费者调用原生算法，定向race结果记录在 native-responses-lite-race.jsonl。账号选择小型过渡方法保留到实体/执行适配清理，不将本批描述为整个旧网关已经迁完。
- 前一批平台包装扩展race1363条通过，unit lint为0；两项中途lint为旧文件EOF格式与容量夹具无用嵌入字段，已删除/格式化后复验为0。原失败日志保留，未增加忽略规则。

### 2026-09-20：服务档位 wire 契约与策略错误归属

- service_tier 的字段形状、合法值、fast 归一化及64字节错误展示限制归 protocol/openai，原类型化校验错误与消费者直接改绑；Fast 拒绝错误归 gateway/tierpolicy。原断言移至协议包，保留普通构建标签、巨大输入与省略/null边界。
- 定向race结果见 native-service-tier-race.jsonl；unit lint为0。动态设置、用户/分组/Key 的策略优先级和计费裁决没有在这次 wire 拆分中提前读取或改写。Responses Lite和档位的实际所有权同步到现有上游文档。

### 2026-09-20：纯档位规则、身份测试与基础构造清理

- Fast 用户范围及首条命中求值迁入 gateway/tierpolicy；认证作用域归 capability，模型白名单动作归 modelmap，由 Anthropic Beta 与 Fast 共用一份实现，未引入具体平台互引。四个原求值测试和默认 Beta 模型矩阵迁至实际所有者；相关定向race460条通过，unit lint为0。
- 验证码的47条原断言直接装配 identity.RuntimeSettings 与认证核心，读取次数、失败关闭和互斥断言保留；验证码/TOTP与HTTP直接消费者复验68条通过。四个旧安全构造器删除；TOTP测试的未使用设置和存储端口不构造旧业务图。
- WS状态存储直接构造原生实例，仍在原once位置注入原观察器；Deferred原调用参数直接投影。删除仅剩测试调用的分组协议包装及旧patch类型，保留该测试中的独立副本动作。构造/协议/平台消费者race774条通过，原WS other_event_type跳过单列；unit lint为0。

### 2026-09-20：创作生产应用图直接绑定

- app 直接构造 creative.Public、Results、原生worker及运行时，HTTP和设置管理共享同一个Public；普通任务资金绑定唯一billing.Funds，订阅读取绑定同一SettlementStore，原本订阅优先和价格按需读取不变。
- 账号目录使用原生Record的受控目录能力，默认模型继续按方法调用时读取。首次Wire验证指出Record的方法需要显式ModelMappingDefaults，已补入原平台默认读取投影后成功生成；保留原失败日志。
- 完整设置读取参数由app独立投影，原worker首轮仍执行一次GetAll与同一composite.Parse，保留其模型发布副作用；构造不读取设置、不启动任务，热更新与原生命周期hook保持。删除无消费者的旧创作用户/倍率/outbox provider及旧worker provider。
- 创作、设置、完成与直接消费者定向race440条通过；provider包无匹配测试单列。用量写入的逐次快照封装归completion，新旧调用直接复用，独立副本及异步失败后的同步兜底断言1条race通过。首次lint指出删除适配后已无消费者的usageLogBestEffortWriter，已删除并补验。
- 旧创作测试门面、平台执行投影、批量任务和旧身份/账号/网关仍需继续清理，本批不代表旧图清零；计划正文摘要未变，不提交，最终矩阵仍待执行。

### 2026-09-20：WS 报文、传输与错误所有权收敛

- 从旧 WS 混合文件按符号拆出协议事件/usage/模型字段恢复到 protocol/openai；客户端会话 Header 与连接读循环进入 gateway/httpapi；供应商错误分类和恢复载荷进入 upstream/openai。WS response终态仍不包含独立error事件，未复用语义更宽的SSE终态判断。原测试保留普通标签、字段及事件断言，旧hotpath与logutil文件清零。
- 技术诊断与关闭/拨号观察归gateway/provider，日志组件、截断与采样限额保持，未安装第二个后端。无外部生产消费者的摘要/分类helper保持私有，相关断言迁入同包；没有为测试扩大生产API。
- 传输枚举、协议决策和优先级归egress，输入是认证模式与账号资格投影；gateway/provider调用原生账号规则，HTTP入站覆盖保持在HTTP层。原配置/账号矩阵改用原生Record及独立Options，旧interface尚随旧执行对象保留至后续清理。
- 客户端关闭值类型与状态提取归HTTP Adapter，通用策略错误归gateway/ws；清除池和抢占错误的旧别名与无消费者构造函数。正常关闭归因、取消后读循环等待及错误链断言迁入实际所有者。
- 分批定向race分别504、505、506、378、411、24条通过，后续读循环集合见native-ws-client-read-race.jsonl；集合重叠不相加。既有other_event_type跳过始终单列。途中旧测试引用私有摘要、同包限定符以及新文件残留import别名导致编译失败，修复并保留原失败日志，未削弱断言。
- 全仓unit lint本轮为0（native-ws-ownership-lint-unit.log）。无生产消费者的视频价格族旧算法删除，原六项别名断言由实际Grok原生规范化承接，分辨率断言归pricing；迁移时测试名误改与既有名字冲突已修正，保留原测试名和失败证据。
- 创作生产改绑后的真实standard/simple进程回归正在补验。S16旧实体、身份/账号/任务/网关残留和最终全量矩阵仍未完成，roadmap不变，不提交。

### 2026-09-20：审计全局绑定与 Ops/团队测试归属

- 创作改绑后的真实 standard/simple 进程启动和SIGTERM回归通过，3条事件（包含父测试），见native-creative-ws-process-integration.jsonl；原HTTP/后台预算和资源关闭顺序断言保留。
- app直接投影账号/支付敏感字段构造Redactor，删除旧全局atomic/once、Bind/Current/默认回退。脱敏与来源表覆盖断言归app，掩码/保留期归audit，会话哈希归identity；定向结果见native-audit-redactor-race.jsonl。
- 旧Ops服务、日志sink、计划报告和邮件投影构造器删除；HTTP测试直接使用原生Options与日志控制器。报告及模板断言迁入Ops，复用原生仓储替身；本地SMTP与内存设置夹具收敛到notification/testkit，不读取外部凭据或构造旧图。
- 团队原测试直接使用原生TeamService、UserSnapshot、站点设置和通知发送器，删除旧团队构造及token哈希转接。首次同包测试触发apikey回引循环，改为外部测试包；随后修正遗漏的哈希限定符，原成员范围、邀请/模板/限流断言保持，补验日志见native-team-owner-race-verified.jsonl。
- 逐符号核对76个Ops兼容导出，完成实际消费者改绑后删除。日志默认/校验断言回到私有实现；实际Adapter使用原舍入实现，顶层map浅复制归querycache且保留nil到空map和嵌套引用语义。不是把旧主体或业务算法复制到testkit。
- 保留中途编译失败与修复后日志；尚需补验本批门禁和后续旧图清零，S16仍在实施中，不提交。

### 2026-09-20：通知、租约与设置残留清理

- 旧EmailService聚合与通知构造转接删除；验证码/重置缓存仍由identity.EmailChallenges持有，SMTP和模板由notification.Mailer/NotificationEmailService持有。旧认证夹具直接装配相同实例，邮件正文转义断言迁入notification；认证/提醒/锁消费者race805条通过。
- 订阅提醒测试直接使用app的生产投影。所有周期任务继续共享原Redis锁实例，app绑定原生Leader端口并投影数据库咨询锁；删除service锁接口与两层同义转接，无后端放行、竞争跳过及故障回退保持。Wire成功生成。
- pending OAuth签发/用途/密钥/长度和保留邮箱断言归identity，直接使用SessionService与原生类型，8条race通过；删除相应旧测试别名。账号阈值缓存、预聚合缺省与通知断言归各所有者，6条race通过。
- service/domain_constants删除：剩余消费者改绑实际所有者，未使用的声明删除；affiliate_balance仅为用户删除测试的历史记录值，保留原字面值，不发布新兑换类型。
- 全仓unit lint补验发现usage/postgres测试仍调用旧预聚合构造，已改用相同独立Options。原查询断言29条通过，真实PostgreSQL查询测试另行通过（native-usage-preaggregation-integration.jsonl），没有改变查询或聚合策略。

### 2026-09-20：上游警告契约与 thinking 分类

- HTTP、WS、Grok和旧执行入口的同形警告值统一归gateway/forward，实际消费者直接引用，错误链方法名及载荷保持。删除旧类型时工具连带删除同名方法，三项原风控断言捕获此回归；按原方法恢复并加入接口编译检查后复验通过。失败日志保留，此为迁移回归修复，不是新增历史问题范围。
- thinking协议族的纯模型判断归gateway/provider/modelidentity，保留官方签名、第三方原样回传及unknown保守分支。搜索历史过滤仍由searchtools拥有，调用者传显式选项，未让纯协议包按平台或账号自行判断。
- 旧账号/身份/任务/网关应用图尚未清零，S16最终全量矩阵和总销项仍未完成；继续实施，不提交、不标记17/17。

- 本轮警告合并修复后449条定向race通过，thinking/搜索结果见native-thinking-search-race.jsonl；全仓unit lint复验为0（native-owner-cleanup-lint-unit.log）。失败及原有跳过单独保留，未扩大规则。

- 警告错误链提取及响应体副本归gateway/forward；OpenAI风控分类、脱敏和警告包装归gateway/provider，删除旧service实现，保留原错误链和HTTP状态。两批警告/风控定向race结果分别归档native-warning-extraction-race.jsonl与native-warning-policy-race.jsonl。普通lint发现两个仅unit消费的夹具与一项默认模型薄包装，已按原消费者标签和原生调用清理，复验为0。

- 警告/风控收敛后的普通、unit、integration lint均为0，见native-warning-policy-lint-*.log。随后清除旧返利构造入口：认证夹具共享原生推广设置，资金竞争夹具直接构造推广服务；真实PostgreSQL并发上限race1条、推广/认证消费者22条race通过，后续unit lint继续为0。未改变返利计算、事务或上限断言。

### 2026-09-20：转发结果与 WS turn 契约退出旧 service

- OpenAIForwardResult 与通用 ForwardResult 的实际消费者直接改用 gateway/forward.OpenAIResult、MessagesResult；旧类型定义删除，没有类型别名或旧实体往返转换。HTTP Header 在核心中保持相同map值，HTTP适配在原位置转换为Header后Clone/Set，保留独立副本和大小写语义。
- OpenAI结果的WS恢复输入继续私有持有，生产通过明确方法在原时点读写；没有增加复制、解析或重放。原终态调度反馈断言迁入forward，并增加JSON不输出恢复报文的边界断言。
- WS入站hooks与turn完成值归gateway/ws，字段、回调顺序和取消context保持；旧循环与账号适配继续登记清理，不将仅契约迁移描述为完整入站已清零。
- 图片结果的尺寸注解归forward，计算复用唯一pricing；原尺寸分类与输出优先断言归pricing。Qoder/Bedrock基础观测投影归forward.MessagesFromAttempt，未补入原先不输出的字段或改变取时点。
- 首次编译指出HTTP边界的Header方法需显式转换，已在原Clone/Set位置改绑；保留原失败日志及修复后定向结果，不修改断言或扩大依赖许可。

### 2026-09-20：响应模型观测与账号模型短暂状态

- 单次 attempt／WS turn 的模型与实际档位观测迁入 gateway/forward.ResponseObserver；Gin 存取、SSE 逐帧桥接进入 gateway/httpapi。保留终态优先、冲突回退、200 字符截断及出站档位不被响应回显覆盖。旧实现删除，原普通/unit 测试按原标签迁入所有者。
- 首次编译指出两个剩余档位标准化调用点，已直接绑定同一原生实现；原失败日志保留，修复后定向 race 477 条通过、无失败或跳过。此前 Qoder/Bedrock 结果投影 race 636 条通过，详见 native-observation-cleanup-results.json。
- 账号与模型的短暂失败状态进入 account，原互斥、容量、30 分钟状态窗口及 10/45 秒冷却保持；网关继续持有原唯一实例并在原调用点读写。此批验证继续进行，不将局部完成记作 S16 最终验收。

- 账号模型状态原测试与调度消费者定向 race 167 条通过。代理断流熔断状态移入 egress，原五组状态机测试迁入同包，容量私有字段继续只由同包测试检查；配置投影、唯一实例、取消错误排除与第二次 fail-open 调用顺序不变。首轮编译指出一处链式调用未改绑，修正后 race 123 条通过；handler 无匹配测试的跳过单列，不计行为通过（见 native-observation-cleanup-results.json）。

### 2026-09-20：模型目录、兼容模型策略与 Qoder 流转接

- 模型市场平台默认目录及显示名投影归 routing/provider，app 直接构造 options；删除无消费者的 WrapModelMarketplace/CoreMarketplace 入口。旧市场聚合仅为尚未迁出的测试提供组合，不参与生产装配。平台动态目录、Qoder 排序与独立别名语义保持。
- Messages 模型后缀和 Codex 模型规则组合归 gateway/provider，实际模型 effort 资格归 routing/capability；原纯测试迁入对应所有者，原完整转发测试继续验证改写时点与显式输入优先级。结果详见 native-marketplace-model-race、native-marketplace-default-original-race、native-compat-model-race。
- Wire 生成成功；阶段中间普通 lint 发现只剩 unit 测试引用的 Qoder 旧目录变量，测试直接引用原生目录后普通 lint 为 0；随后 unit lint 为 0。对应日志 native-state-marketplace-lint-normal-final.log、native-model-policy-lint-unit.log。
- Qoder 流薄包装已无生产消费者，原测试改为将相同 Gin Writer 直接投影给已有 upstream.OutputContext，未复制流算法或修改输出断言；删除旧包装文件，定向验证进行中。

### 2026-09-20：Qoder 测试归属与会话标识收敛

- Qoder 的 50 组流输出断言迁入 gateway/httpapi，继续通过真实 Gin Writer 验证 Header、未提交响应、写失败、工具事件和尾部 usage；迁移前后对应集合均为 260 条通过事件、无失败或跳过，日志分别为 native-qoder-stream-direct-race 与 native-qoder-stream-http-race。B01 部分用量回归及两项元数据优先级断言也进入实际 HTTP 所有者。
- Key ID 读取与 Qoder 请求元数据投影进入 gateway/httpapi，沿用 api_key 键、原生 apikey 类型、请求头副本及客户端标记；没有改用失败诊断投影授权。相关 race 286 条通过。旧 Qoder 会话键转接删除，仍有消费者的旧执行账号 ID 投影暂留执行适配。
- prompt cache key 派生归 gateway/provider；保留 compat_cc_ 与 anthropic-cache- 格式、字段顺序和首轮锚点。首次编译捕获遗漏的显式 cache_control 锚点入口，已直接改绑唯一实现，原失败日志保留；实际结果见 native-prompt-cache-race-final。
- 另外 50 组 Qoder 报文、模型、工具调用、用量与会话过期断言迁入 upstream/qoder 的外部测试包，生产算法未修改；补迁纯总量补全及 HTTP 非流错误前未提交断言，测试映射持续登记。旧生产图和最终验收仍未完成，不标记 17/17。

- prompt cache key 与直接消费者 race 修正后 276 条通过。Qoder 报文测试迁移后集合 309 条通过；新增文件没有继承旧测试文件级 errcheck 豁免，82 处解码类型断言改为显式检查，载荷及顺序断言保持，复验仍为 309 条通过。重复 upstream import 合并后，全仓 unit lint 为 0（native-qoder-tests-lint-unit-final.log），原诊断保留。

### 2026-09-20：中途全量验证观察（待范围确认）

- 普通/unit/integration lint 当前均为 0（native-qoder-tests-lint-normal.log、native-qoder-tests-lint-unit-final.log、native-qoder-tests-lint-integration.log）。
- 普通全量在 TestBatchClientLocalTLSAndStreamCancellation 的 media_test.go:124 捕获取消后 ReadAll 返回 nil，而原断言要求错误。internal/upstream/gemini 当前无差异；仅保存本次事件与源码摘要到 unplanned-gemini-cancel-observation.json，尚未追加复现或修复。已按第1节请求用户调整范围，其他独立清理继续。此失败不记为通过，不据此宣布最终验收完成。

### 2026-09-20：已批准的 Gemini 夹具修复与订阅测试初始化回归

- 用户明确允许 Gemini 下载取消测试的限定复现和夹具修复。在原 HEAD archive 中原测试 race 30 次、普通100次未复现；进一步限定普通1000次取得999通过、1次与本轮完全相同的失败。源码摘要相同，原失败完整保留。仅为本地 /bytes 响应声明仍有未发送内容的 Content-Length，避免取消与正常 chunked EOF 竞争；原 require.Error 与上游退出等待不变，生产代码不改。旧 HEAD 使用这一夹具覆盖层1000次通过，见 gemini-cancel-old-head-*.jsonl 与 gemini-cancel-repro-scope.json。
- 普通全量中订阅用户行锁测试出现 Ent interceptor 未初始化。原 HEAD 精确用例通过，属于清理测试依赖后的迁移回归。测试移入 billing/postgres，直接构造原生 SubscriptionService/SubscriptionMutations，并明确加载 ent/runtime；保持原 SELECT FOR UPDATE、用户ID、锁前禁止分组读取、错误链和回滚断言。SQL mock只作为锁序证据，不替代真实PostgreSQL资金事务验收。
- 中途普通全量结果为11874条通过事件、2个失败测试；package失败单列，不计为额外测试。132项跳过包含无测试包与已有环境限制；原日志不改写，两项修复后的定向与全量复验另存。

- 上述两项修正后，普通全量复验11876条通过事件、无失败；132项跳过继续单列（interim-qoder-cleanup-test-normal-final.jsonl/.json）。这仍是中途验证，旧图未清零，不作为最终完成声明。

### 2026-09-20：Qoder 原生刷新器接入

- 删除已无生产调用的管理员刷新器构造和两项旧 AdminService 取回传输方法；原传输/TLS实例断言由 account/provider 的真实刷新器测试承接，并保留代理URL、账号ID、并发值的行为验证。
- 删除旧 QoderTokenRefresher 类型与构造文件。请求执行持有同一原生 account/provider 刷新器，供应商交换由现有 QoderRefreshOptions 注入；未创建新的令牌缓存、锁或注册表。旧 OAuthRefreshAPI 接口暂由仅投影的执行器衔接，持久化和CAS顺序保持。
- 失败凭据身份与请求期刷新资格归 account.NeedsRefreshQoderAfterFailure；原断言迁入 account，保留令牌轮换、无过期字段、无关映射变化、缺失refresh_token与常规到期路径。旧网关nil刷新器守卫保持，未扩大刷新资格。

- Qoder 刷新锁等待与错误哨兵归 account；原立即回读、100ms轮询、3秒预算、最后读取错误和失效时点保持。旧执行接口只提供本次持久读取与凭据身份投影，未创建新锁或重复交换。
- Qoder 请求错误的APIError解析及账号限流/过载写入归 account/provider，原三项错误/取消/非流观测断言同批迁入；旧文件不再承载这些规则。共享传输替身失去全部旧包消费者后删除，仅保留所属模块夹具。此批结果见 native-qoder-failed-refresh-race、native-qoder-refresh-wait-race、native-qoder-health-observation-race。

### 2026-09-20：请求期刷新唯一装配与原生协调器测试

- unit 全量中途验证19898条通过事件、无失败，118项跳过单列，见 interim-qoder-cleanup-test-unit.jsonl/.json。
- QoderRequestRefresh 归 account/provider，直接使用原生存储、OAuthRefreshAPI 与 QoderTokenProvider；app 构造一次，Qoder Chat及尚未清零的其他入口共享该实例。Wire 初次生成指出最后一项 provideLegacyAccountRefresh 已无消费者，删除后生成通过，未手改生成物。
- Qoder 六项请求刷新原断言迁入 account/provider，CAS、轮换等待与缓存失效直接组合真实协调器；旧网关删除 refreshAPI/newRefresher 字段。默认令牌源构造时仍与刷新失效指向同一实例。
- OAuthRefreshAPI 原 unit 测试及管理员凭据竞争测试迁入 account，使用原生记录与明确 Options；移除旧大仓储嵌入，只保留实际读取和条件写替身。首次编译指出旧错误/请求标记别名，已改为原生符号；失败日志保留，修正后的结果见 native-oauth-refresh-owner-race-final。

- 原生请求刷新装配后，standard/simple 真实进程及 SIGTERM 断言3条通过（native-qoder-refresh-process-integration）；首轮筛选遗漏 -sigterm 后缀只执行了父级准备，单列 selection-only 日志，不计行为证据。
- Grok 令牌源14项原测试迁入 account/provider，复用所属模块的授权与存储替身，令牌查询、锁等待、手动测试和失效均直接调用原生实现。初次缺少共享过期凭据夹具的编译诊断保留，补齐原数据后复验通过；旧包剩余夹具仍有混合凭据失败测试消费者，继续登记。
- 刷新CAS、同连接外层回滚及outbox失败断言从旧repository迁到 tests/integration/account，使用原生 AccountStore/OAuthRefreshAPI 与原SQL故障触发器；真实PostgreSQL race验证进行中，未更改持久化和业务断言。

### 2026-09-20：账号存储契约与 Grok 分类清理

- 刷新、手动配置、CRS导入和cooldown的真实事务测试直接使用 AccountStore；配置失败仍在原事务执行缺失表SQL，未以普通模拟错误替换数据库回滚证据。观测、隐私、分时档位、被动窗口与管理员恢复同批迁到 tests/integration/account，保持原独立提交与尽力outbox差异。结果见 native-refresh-transaction-owner-integration-race、native-account-refresh-config-integration-race、native-account-observation-owner-integration-race。
- Grok凭据失败分类与reason常量进入gateway/forward，仅读取错误及代理是否存在；错误顺序、HTTP消息与持久化reason值保持。比较快照仍私有，全部分类字段保持原不输出JSON的形状。旧网关继续在原时点写状态和失效，未扩大历史问题修复。原分类断言迁入实际所有者，其余直接消费者回归见 native-grok-credential-class-race。

### 2026-09-20：Qoder 执行目标与会话唯一实例

- gateway/provider.QoderRuntime 接收原生账号记录，拥有唯一 upstream.Executor 与 ConversationStore；app 直接构造并同时供新Chat与剩余旧HTTP入口使用。原会话增量、SSE、取消与重试均继续由既有实现执行。
- 旧service的模型映射、目标创建与执行器构造已改为投影/委托；删除无消费者的旧 PrepareQoderTarget、生命周期绑定、站点客户端及传输包装。客户端按需选择仍保留原自定义客户端注入、CN/Global profile和同一HTTP池。
- 原代理/TLS/账号ID断言迁入account/provider，直接调用QoderRequestDoer；旧传输替身对应消费者清零后删除。Wire由手写provider生成，原生runtime只构造一次；native-qoder-runtime-race验证结果已登记，后续消费者与启停复验继续。

### 2026-09-20：删除旧 QoderGatewayService

- 三个协议入口共享 gateway/provider.QoderRuntime；主Chat直接使用原生gateway执行器，Messages/Responses及兼容Chat尝试由gateway/httpapi.ForwardQoderAttempt同步写出。旧 QoderGatewayService 及 ProviderSet 构造节点已删除，账号刷新仍由原生 QoderRequestRefresh 唯一执行。请求体映射、部分结果资格和结果字段保持，没有新增账号尝试或使用统一结果投影补入原先缺省字段。
- 仍使用旧类型的会话/协议测试迁至 HTTP Adapter；思考与窗口纯断言归 upstream/qoder。gateway/testkit 只提供原生装配与可控客户端，不实现 Forward、重试、缓存或会话算法。原构造身份断言进入 app，并增加从实际运行时读取同一会话缓存的验证。初次两个跨文件断言 helper 缺失的编译日志保留；修正后315条定向race通过，无失败或跳过。
- Qoder令牌源的传输绑定移入app。provideHTTPUpstream提供唯一具体 transport.Client，Wire将其绑定到原生QoderTransport及剩余旧端口；新增Qoder装配文件不引用service。testkit使用现有角色与准确文件/import许可，未扩大生产核心的依赖范围。

- 原 qoderForwardContext 仅为测试中的旧预算副本，已删除；同名原断言改为穿过实际原生Execute，在上游已进入后取消父请求，验证流式继续取得尾部usage、非流返回取消以及原15分钟预算。定向race3条通过，未扩展生产取消策略或修复范围。
- 旧service.HTTPUpstream接口声明已删除；29个实际消费者改用infra/httpclient.UpstreamTransport，方法签名、参数、TLS类型及响应体释放责任保持。Wire将同一个具体Client绑定到各消费者端口；依赖变化及验证单列native-http-transport-port-*。

### 2026-09-20：原生资金准入与删除 BillingCacheService

- gateway/admission.FundingAdmission 通过窄端口固定资金检查、运行模式读取和RPM次序；资金拒绝不累计RPM，Qoder等待后只复查资金。实际有效Key只投影原资金检查读取的用户ID、Key窗口、最终分组及RPM字段，不读取请求体或新增存储查询。原资金模块仍唯一拥有缓存、额度、窗口和任务队列。
- 所有HTTP入口及无效请求分组回退已直接绑定原生准入。身份管理、Key、推广、旧网关完成处理和队列生命周期直接使用同一billing.Eligibility；provideLegacyBillingEligibility与旧BillingCacheService类型/构造文件已删除。保留BillingCacheService生命周期显示名称及原启停顺序。
- 原余额singleflight、额度sentinel/TTL/窗口、余额扣减后失效、订阅优先规则分别迁入billing；RPM原断言迁入scheduler。独立缓存夹具保留原异步回填，RPM纯规则夹具不再启动无关资金队列。原构造中未进入资金投影的PreferredSubscriptionID不增加到新KeySnapshot；实际模式、订阅和分组范围断言不变。
- HTTP及装配全包unit race1640条通过；资金相关后续批次见native-observation-cleanup-results.json，测试事件存在重叠、不相加。初次方法值/构造器改绑及测试常量缺失的编译日志保留，修正后重新执行；真实额度重置后累计复验与最终门禁继续。

- 缓存与订阅测试迁移后的21项unused诊断均来自已清零的旧测试替身及纯委托，按实际引用删除；没有新增忽略规则。unit lint复验为0（native-billing-cache-removed-lint-unit-final.log）。资金准入与缓存所有者文档已按实际实现同步。

### 2026-09-20：分组管理原测试归属收敛

分组复制、平台变更缓存失效、54 项分组管理顶层测试及其剩余端口场景迁到 routing；协议持久化和 Key 快照隔离同时改用原生构造。保留原名称、unit 标签、业务断言、存储复制边界与按需模型投影，不在测试中重建旧管理服务。删除清零的七个私有测试转接；首次编译发现旧私有方法名，改为既有原生 ValidateFallbackGroup 后通过。

证据：[映射](baseline/S16/native-routing-group-owner-mapping.json)、[定向 race 结果](baseline/S16/native-routing-group-owner-results.json)。最终定向集合通过，无失败或跳过；集合重叠，不相加。旧聚合仍有其他消费者，S16 保持实施中。

### 2026-09-20：管理员分组、倍率及用户测试转接清理

删除已无消费者的旧分组复制、列表、模型候选、排序与倍率管理委托。分组删除四个原测试迁到 routing，专属倍率管理迁到 billing；用户角色、资料字段、批量限额、分页与批量回退、RPM 聚合、创建默认权益迁到 identity。所有原断言及 unit 标签保留；创建设置组合 RuntimeSettings/GrantSettings，同一替身供两者读取，不复制解析逻辑。迁移中的缺失字段/构造名编译诊断已修正，失败日志单独保留。

证据：[逐文件映射](baseline/S16/native-admin-owner-mapping.json)、[定向 race](baseline/S16/native-admin-owner-results.json)。测试只迁到所属模块，未添加历史缺陷修复或修改生产金额、存储和缓存行为。

### 2026-09-20：账号管理、Spark 资格与测试清理

账号编辑、凭据合并、额度重置、错误恢复、影子创建/绑定/补偿/复制/配置与隐私保护的原测试改用 account 原生记录及管理用例。保留构造输入、旧字段清理、同一母账号限制、深复制和原断言；周窗口夹具补回旧投影原有的 LoadLocation 注入。迁移期间的类型名/导入/夹具遗漏编译失败独立保留，不算通过。

`account.ParentHealthyForShadow` 接管母账号资格规则；`account/provider.DefaultSparkShadowModels` 直接读取上游唯一别名表。已切换调度/诊断/WS 消费者，删除旧 shadow_routing.go 及无消费者的管理员转接、复制 Adapter。app 账号管理不再引用 service，重置只接收 RuntimeUnblocker；已重新生成 Wire。同步调度架构文档的实际所有权。

证据：[迁移对应](baseline/S16/native-account-admin-owner-mapping.json)、[定向 race](baseline/S16/native-account-admin-owner-results.json)、[Wire](baseline/S16/native-account-admin-wire.log)。此前管理员清理的全仓 unit lint 为 0（native-admin-owner-lint-unit-final.log）；当前批次仍需增量 lint 和最终全量验收。

### 2026-09-20：旧管理员构造器退出

账号/代理/兑换码列表、代理更新/删除、兑换码修改与批量删除、用户删除原断言迁到所属模块。保留用户及 Key 删除调用次序和成功后失效；无数据库单测继续使用原 PostgreSQL Adapter 的无连接分支，不计真实事务证据。

旧构造器最后一个消费者 `s06_account_config_authority_test.go` 迁至 `tests/integration/account/configuration_authority_integration_test.go`，沿用真实 PostgreSQL、同一 outbox writer 和六种确定性交错断言；验证通过后删除 NewAdminService、AdminService、Administration、旧代理聚合与无消费者的公开构造转接。账号配置输入仍不能覆盖并发凭据、消费和健康状态。

证据：[结果](baseline/S16/native-admin-constructor-retired-results.json)、[真实配置写权限 race](baseline/S16/native-account-configuration-authority-integration-race.jsonl)。规则按准确文件/import 登记；配置编辑中产生的缩进错误已在运行 lint 前修正，YAML 解析及 diff 检查通过。未改 SQL 或缓存协议。其余旧管理测试的内部组合暂存，继续逐项迁移。

### 2026-09-20：管理员与基础账号聚合清零

管理员身份绑定/邮箱规则、原子调账与返利失败语义、Key 授权、批量账号协议保存及最后的账号列表测试全部直接调用原生模块。保留原 SQLite 身份绑定测试，并明确它不替代真实 PostgreSQL 事务证据；普通标签的协议保存测试仍为普通标签。联合定向 race 通过后删除私有 adminServiceImpl 及 admin_user/admin_account/admin_group、身份与 Key 管理桥接，未把聚合移到测试目录。

基础 AccountService 仅剩的删除及 Ollama 受管字段测试也改用 account.BasicAccounts；原 AccountService 与账号管理/基础/配置投影 Adapter 删除。剩余健康预检还使用的账号列表纯投影保留在既有 account_projection.go，记录为后续账号形状清理，不能据此宣称 Account 实体已清零。同步系统架构与路由计费文档。

证据：[联合结果](baseline/S16/native-admin-aggregate-removed-results.json)。迁移期间漏带的旧常量引用、重复测试辅助函数已修正，原日志保留；失败不算通过。完整 S16 门禁仍待最后旧包清零及全量验收。

### 2026-09-20：管理员清理后的普通全量补验

普通全量首次因完成处理测试引用 unit 专用资格夹具而未通过编译，属于本次迁移回归。将唯一夹具移到普通测试文件 billing_completion_fixture_test.go，不改变缓存选项或后台调用；重跑普通全量退出 0。结果见 [JSON 事件](baseline/S16/interim-admin-cleanup-test-normal-final.jsonl) 与 [摘要](baseline/S16/interim-admin-cleanup-test-normal-final.summary.json)，原失败日志保留，跳过逐项单列。

账号复制与分组/outbox 的真实 PostgreSQL race 通过（native-account-duplicate-transaction-integration-race.jsonl）。据此删除旧账号复制接口及仓储方法，旧只读消费者仅保留 AccountRepository 投影；已重新生成 Wire，不构造第二份存储。

### 2026-09-20：识别、摘要、缓存端口及维护入口清理

Claude 客户端识别原测试迁到 clientmeta，context 值读写测试回到 requeststate；测试 HTTP 输入仅投影到既有纯输入类型，原提示词夹具逐字节复制并保存摘要。删除旧 ClaudeCodeValidator，Grok 的 UA 消费者直接使用 clientmeta。Anthropic 摘要原测试直接调用 upstream/anthropic 的报文算法，保持原 RawMessage 载荷与链、key、TTL 断言，删除无生产消费者的摘要转接。

旧 SessionLimitCache 组合接口删除；网关分别接收 scheduler 会话端口与 billing 窗口费用端口，app 仍各构造一次、共享原 Redis 客户端。窗口预取、会话释放、调度与完成路径定向 race 通过；真实 Redis 的会话原测试及新增应用装配契约通过，验证窗口 key/30 秒 TTL、批量读取和两个数据面互不删除。Wire 再生成，初次遗漏旧 provider 引用的编译失败日志保留，修正后生成成功。

模型读取上限投影与原测试迁至 app，两个实际查询构造使用同一投影；倍率解析器直接构造 billing 原实现，缓存及 singleflight 入参原样传递。更新查询/安装复用 ops/provider.ReleaseClient，数据管理废弃响应测试迁回 backup；删除旧 update_service/data_management 常量与构造包装，不恢复守护进程。

证据：[汇总](baseline/S16/native-remaining-ports-results.json)、[缓存消费者](baseline/S16/native-session-window-consumers.json)、[倍率消费者](baseline/S16/native-group-rate-resolver-consumers.json)、[提示词夹具摘要](baseline/S16/native-claude-validator-fixture.json)。同步会话生命周期文档，S16 继续实施中。

### 2026-09-21：按完整能力闭包推进与合并验证

按用户追加要求，后续以实际依赖闭包划定能力批次，同批完成生产调用、测试、替身和门禁，再清除旧入口。每批先编译，再执行必要的定向验证；多个稳定批次合并做全仓检查，最终矩阵不缩减。不使用子 Agent，不扩大历史问题修复范围，不自动提交。

**刷新兼容层批次**：生产统一使用 account 协调器；删除旧 OAuthRefreshAPI/结果/执行器及 Claude/OpenAI/Grok 刷新器包装，策略原测试迁回 account，Grok 网关直接注入原生协调器。剩余测试记录适配只转换旧网关输入和实际存储能力，不复制锁或刷新规则。normal/unit 编译通过，定向 race 与真实 PostgreSQL 结果见 [批次范围](baseline/S16/native-refresh-compat-batch.json)、[结果](baseline/S16/native-refresh-compat-results.json)；依赖门禁退出 0。编译发现计数替身签名遗漏，行为测试发现日志回调遗漏，均为本次迁移夹具问题，已修正并保存失败日志。更新账号维护文档。

**批量图片供应商与注册表批次**：生产、提交/下载/清理/worker 的消费端、供应商原测试和三类任务替身同批使用原生 provider/Registry。Vertex 配置投影移到 app；四个旧任务构造器显式接收已装配的唯一注册表，删除无实际调用者的隐式构造 fallback。账号仍在原操作边界投影，不提前冻结凭据。normal 图的 Wire 与 unit/integration 编译通过，供应商/调用方 race 196 条事件、真实 PostgreSQL/Redis 13 条事件通过，无失败或跳过；35 个顶层原测试名称和标签完全保留。证据见 [闭包](baseline/S16/native-batch-provider-scope.json)、[测试对应](baseline/S16/native-batch-provider-test-mapping.json)、[结果](baseline/S16/native-batch-provider-results.json)。更新批量图片文档；其余任务编排转接尚未清零，未提前宣称整个 batchimage 清理完成。

这两个稳定批次现在合并执行全仓检查；S16 整体仍在实施。

### 2026-09-21：合并检查补齐 HTTP 刷新消费者

刷新与批量 provider 批次合并 unit 全仓检查时，HTTP 切号测试仍引用已删除的 NewOAuthRefreshAPI，导致 handler 包编译失败；其余包的结果保留在 interim-refresh-batch-test-unit.summary.json。将遗漏的 HTTP 替身同批改为原生 Record/协调器，保持其原只读仓储能力和日志选项，补编译后完整原切号场景 race 40 条事件通过。这里只修复本次迁移遗漏，未扩展历史问题范围。随后仅重跑 handler 全包 unit；原全仓命令仍记录退出 1，不将包级补验伪装成该命令成功。

### 2026-09-21：订阅与兑换旧构造边界完整收尾

本批一并清理旧构造、10 个规则测试文件、分组/日期替身、认证中间件、HTTP 契约和 PostgreSQL 集成调用者；生产 app 原先已直接构造 billing 唯一实例，本批未改变资金算法。74 个顶层原测试名称和 normal/unit 标签保持一致；不再构造旧 service 订阅或兑换服务。数据库参与端口仍接收原 client/Ent context，原有外层未提交可见、回滚和提交后失效断言保留。

编译时发现重复时间指针辅助函数、普通 Google 认证测试需要共享夹具，以及 integration 调用者尚需原生改绑，均在本批修正。行为验证发现迁出的测试依赖原 service 测试进程的 UTC 初始化；改为每个订阅实例与夹具显式 UTC，不修改生产日期规则或其它包的 time.Local。原失败日志保留。

普通/unit/integration 编译通过；规则 race 106、HTTP/认证 90、真实 PostgreSQL race 9 条事件通过，无失败或跳过。见 [范围](baseline/S16/native-entitlement-constructor-scope.json)、[名称与标签](baseline/S16/native-entitlement-test-mapping.json)、[结果](baseline/S16/native-entitlement-results.json)。更新系统架构文档。前批遗漏的 HTTP 刷新消费者已补验整个 handler 包 unit（同结果文件），全仓失败原记录仍保留。现在合并运行三个稳定批次的全仓测试与 lint，最终验收仍按计划完整执行。


### 2026-09-21：身份认证构造与消费者整批收敛（验收中）

- 删除旧 AuthService、注册/绑定/OAuth 转接和私有测试兼容方法；HTTP 门面直接接收 identity.AuthService 与显式 Ent 连接，JWT 测试直接构造原生 SessionService。
- 63 个原测试名称及 unit 标签逐项对应到 identity；邀请码竞争及注册 Key 上限同批迁移。剩余设置测试只保留数据仓储替身。映射见 `baseline/S16/native-auth-test-mapping.json`。
- 普通、unit、integration 编译通过；原测试 race 80 条事件通过，无失败/跳过。HTTP 消费者首轮暴露本次测试装配遗漏 JWT 分钟 TTL，已补齐原四项 JWT 参数；原失败保留，不归类为历史问题。补验及真实数据库回归进行中。
- 生产认证算法、数据库结构和缓存格式未改；仍有其他能力未清理，S16 保持实施中。

认证批次补验：JWT 定向补验、真实 PostgreSQL/Redis 四个顶层事务/刷新回归通过；unit 与 integration 受影响集合 lint 均 0。明细见 `baseline/S16/native-auth-results.json`。旧 service 用户仓储替身的消费者已清零并删除；身份测试复用已有原生数据替身。


### 2026-09-21：市场展示、报价与组合消费者批次

- 删除旧 ModelMarketplaceService 与私有方法测试转接；生产一直由 app 直接构造 routing.Marketplace，剩余组合消费者改为直接构造原生市场。网关目录来源仍由已登记的平台/账号读取端口提供，本批不提前改动模型限流或请求映射时点。
- 23 个纯展示/报价/元数据测试迁入 routing；三个实际网关目录交叉测试保留真实解析链。五个直接消费者文件共 70 个顶层测试全部有去向，见 `baseline/S16/native-market-test-mapping.json`。
- 普通、unit、integration 编译通过；首轮定向 race 127 条事件通过，无失败/跳过。其余三个消费者测试补验中，门禁检查随后执行。


### 2026-09-21：批量图片下载、清理与生命周期批次（验证中）

- 生产 HTTP 直接接收 batchimage.Download/Cleanup；app 直接绑定 account.Postgres Store 和已共享的 provider registry。新增 ResultAccess 只装配当前任务的供应商操作，分别保留下载的资格/错误映射和清理的原错误传播；供应商操作逐次复制原生 Record，保留旧投影的副本边界。
- 清理循环直接复用 Cleanup.Run 和 Runtime，唯一 app hook 名称、启动/停止顺序不变，删除 BatchImageDownloadService/BatchImageCleanupService。Wire 只生成，不手改。
- 下载和清理的 8 个原顶层测试均有去向；7 个规则测试迁入 batchimage，结算写到期时间的断言仍在结算消费者集合；立即停止回归同批迁入所属模块。MVP 已改用原生用例，数据仓储及 ZIP 替身保留原断言。
- 普通/unit/integration 编译通过；定向 race 原断言均实际执行。真实 standard/simple SIGTERM 验证及门禁收尾中。详见 `baseline/S16/native-batch-results-test-mapping.json` 和同前缀日志。

批量结果批次补验：standard/simple 真实进程 SIGTERM 三条事件（含父测试）通过，保留任务请求 → 清理/worker → Redis/SQL 的原顺序断言。已启动认证、市场和批量结果三个稳定批次的普通/unit 全量与三种 lint 合并检查；日志前缀为 `interim-auth-market-results-`，本轮不重复运行全量存储矩阵，S16 最终矩阵仍必须完整执行。

上述三批合并检查全部退出 0：普通全量、unit 全量及 normal/unit/integration lint；完整命令见 `baseline/S16/interim-auth-market-results-check.json`，实际事件与既有跳过见 `baseline/S16/interim-auth-market-results-events.json`。这些是中途合并检查，不替代 S16.7 的最终完整验收。


### 2026-09-21：OpenAI 客户端资格与传输选择批次

- 删除旧 Codex 客户端检测器、结果/原因别名和 WS resolver；网关与 Live 直接消费 account 检测端口，自动探针不再经旧 Account 往返投影。自定义检测端口仍取得独立 Record 副本。
- 三个原客户端规则顶层测试迁入 account，保留 HTTP Header 输入与按需读取；直接消费者和探针定向 race 51 条事件通过。
- 对每处 WS resolver 构造以 AST 核对使用同一 cfg 后删除冗余字段；默认配置仍于每次决策投影。账号筛选、SSE、WS、图片、限流、容量及旧原生传输测试共 213 个顶层名称纳入验证，见 `baseline/S16/native-client-transport-consumers.json`。原子状态、双向 relay 和恢复算法未改。
- 普通/unit 编译及传输集合 race 通过；具体事件见 `baseline/S16/native-client-policy-results.json`。首次 lint 的重复 import 别名已合并，未改规则；门禁补验中。


### 2026-09-21：批量图片处理、报价、结算与恢复整批收敛

- app 直接装配 ProviderProcessor、ResultIndexer、Settlement、BillingRecovery、worker Runtime，复用唯一 billing.Funds、账号 Store、registry、日志及价格实例。报价的懒分组读取规则进入 batchimage.Pricing，app 仅转换只读字段；提交兼容消费者也使用同一原生报价接口。
- 删除旧处理、索引、结算、恢复与 worker 类型/构造器；原 worker、处理、结算和恢复测试迁入 batchimage，配置投影测试进入 app。原测试仍需的跨任务数据替身留在独立 fixture 文件，不重建旧服务；清零的测试方法/常量包装同批删除。
- 顶层测试映射与实际执行结果见 `baseline/S16/native-batch-pipeline-test-mapping.json`、`baseline/S16/native-batch-pipeline-results.json`。普通/unit/integration 编译、原定向 race、真实 PostgreSQL 任务投影/回滚，以及 standard/simple SIGTERM 均通过；受影响 unit lint 为 0。未修改资金算法、发布 SQL 或缓存格式。
- 客户端/传输批次另有一项原 WS 非 response.create 子场景跳过，原因仍为原 relay 夹具无响应；不计通过，完整日志位于 `native-client-transport-race.jsonl`。


### 2026-09-21：批量图片提交与候选链收尾（存储/门禁验证中）

- app 直接构造 batchimage.Public 并注册 HTTP；Candidates 从唯一 AccountStore 按原范围读取，只投影候选、协议与模型规则。通用账号一跳映射归 account，指定订阅的受限读取归 billing；原网关调用复用同一方法，未新建缓存或查询。
- 提交、任务状态与 MVP 原测试均迁入 batchimage；资金替身直接接收 TaskFundsCommand，原 BatchID 断言对应 Task.ID。认证失效仍观测同一个注入替身，候选替换复用同一 registry；角色、字段、查询次数、模型链和资金断言保留。
- 删除旧批量服务及 13 个已无消费者的临时测试装配/数据文件；列表见 `baseline/S16/native-batch-submit-removed-fixtures.json`。S13 混合生命周期测试按 creative/batchimage 子场景分归所有者，原子场景名与停止断言保持。
- 顶层映射、原生完整任务链与共同模型消费者的 race 结果见 `baseline/S16/native-batch-submit-test-mapping.json`、`baseline/S16/native-batch-submit-results.json`。普通/unit/integration 编译已通过；PostgreSQL 与真实进程补验、最终门禁正在执行。
- 共用的旧 UsageBillingRepository/任务资金命令仍有创作/网关与旧测试消费者，尚未作为全局资金销项完成；S16 继续实施，不更新 17/17。

提交批次收尾：原生任务链 race 195 条事件、共同模型/协议消费者 race 84 条事件、真实 PostgreSQL 11 条事件及 standard/simple SIGTERM 3 条事件均通过，无失败或跳过；unit lint 0。最终明细见 `baseline/S16/native-batch-submit-results.json`。下一批只清理创作公开/结果/worker 已有能力，供应商执行与剩余网关能力分批继续。


### 2026-09-21：创作公开、结果与 worker 成批收敛（收尾验证中）

- 删除旧 CreativePublicService、查询/结果/恢复与 worker 包装，生产继续绑定同一个原生 Public、Results、资金和运行实例；账号目录适配进入 creative/provider 并被 app 使用。平台执行器只移除未使用的账号仓储、设置构造参数，执行和选号逻辑留其独立批次。
- 51 个原顶层规则/状态/worker/生命周期测试有明确目标；目录六项断言归 creative，隐藏 Key 幂等供应归 apikey，设置聚合的写入断言留设置消费者。参见 `baseline/S16/native-creative-test-mapping.json`。
- 仓储目录中的冒烟用例是本次迁移遗漏的消费者，首次编译失败已保留；修复装配后移到 tests/integration，以原生 Redis adapter + miniredis 运行原创建/执行/取回/ack 断言通过，不算真实 Redis 服务验证。
- 定向事件、失败与补验独立记录在 `baseline/S16/native-creative-results.json`；原生核心及共享价格消费者通过，PostgreSQL 与进程/门禁正在收尾。未增加历史问题修复或供应商调用。

创作批次补验：真实 PostgreSQL 成功事实/回滚、原生资金重放三项通过；工作区隔离属于 unit 集合，另行补验，不记为 PostgreSQL 证据；standard/simple SIGTERM 通过；受影响 unit lint 0。遗漏的 miniredis 冒烟已在新位置通过。后续继续处理两类任务都已退出的共用旧资金命令与存储签名，保留全部历史指纹/分配/真实事务断言。


### 2026-09-21：共用旧任务资金命令收尾

- 已确认两类任务生产链均直接使用 billing.Funds，删除 BatchImageBalanceHoldCommand、转换/归一化转接、Plan/Total/Effective 旧入口，以及 repository 的 Reserve/Capture/ReleaseBatchImageBalance 方法。普通结算的 UsageBillingRepository 暂时只保留 Apply，随剩余网关绑定继续清理。
- 六项原指纹与分配测试进入 billing；九项 PostgreSQL 原任务资金测试进入 tests/integration/billing，使用隔离 PostgreSQL、同一真实迁移和原生日历/参与工厂。批量 ID 对应明确 TaskReference，历史请求 ID 不变；真实写入字段、并发与回滚断言保留。
- 普通/unit/integration 编译通过，单测与 PostgreSQL race 通过，结果及逐测试映射见 `baseline/S16/native-task-funds-results.json` 和 `baseline/S16/native-task-funds-test-mapping.json`。SQL 与缓存协议未改。
- 创作工作区隔离已按实际 unit 标签补验；其 SQLite/Ent 证据独立保留在 `native-creative-workspace-unit-race.jsonl`，未归入真实 PostgreSQL 结果。


### 2026-09-21：任务能力合并检查与普通结算收尾

- 合并普通测试 11,882 条通过、4 项跳过；unit 19,906 条通过、8 项跳过。首次 integration 为 Redis TestMain 容器启动超时，单包补验及全量重跑通过（12,880 条通过、4 项跳过）；原失败保留，不记为首次通过。三种 lint 均为 0，详见 `baseline/S16/interim-tasks-creative-funds-results.json`。
- 倍率测试四项移到 gateway/completion，删除仅为旧测试保留的生产/测试转接；原倍率与订阅包含规则不变。
- 普通结算消费者直接使用 completion.Store，app 绑定现有 SettlementStore；Funds.Settle 原为同一 Apply 调用，闭合事务与订阅读取保持。删除旧资金仓储生产包装及旧接口，真实数据库测试同批迁到 tests/integration/billing，生产与测试不再通过旧构造器。
- 本批普通/unit 编译通过；integration 首次因两个旧类型断言和重复测试帮助函数失败，修复测试迁移后继续编译、定向 race 和门禁，不涉及历史问题修复。S16 仍实施中。


普通结算批次完成：148 条完成处理/倍率消费者 unit race 事件与 30 条 PostgreSQL integration race 事件通过，无失败或跳过；三种编译通过，unit/integration lint 0。测试辅助函数随着消费者迁出清零，详见 `baseline/S16/native-settlement-results.json`。

### 2026-09-21：生产运行设置读取依赖收敛（验证中）

- 网关、健康、调度、审核与搜索的动态设置直接使用各模块唯一实例；RuntimeReaders 只承载已注入端口与 Antigravity 静态预算，没有复制规则、缓存、锁或后台任务。两个身份补丁 getter 的唯一实现归 gateway，保持原单键读取、精确字符串与失败默认。
- app 不再构造旧 SettingService；UA resolver 的安装转移到同一运行输入装配。HTTP 客户端版本与公开余额单位直接绑定原生能力；Wire 由原入口生成。剩余旧 SettingService/Auth 门面仅有兼容测试消费者，未宣布整个旧包清零。
- 测试同批改绑原生读取器或原数据的实例投影，装配断言改为实际八个消费者端口共享和提交后缓存命中；删除为已退出生产构造新增的临时旧图夹具。普通/unit/integration 编译通过，定向行为与门禁继续执行。

运行设置批次：定向 unit race 933 条、本地 HTTP Fast 策略 1 条、真实 PostgreSQL 的 Antigravity 装配 3 条、进程矩阵 11 条通过，无失败或跳过；unit lint 0。阈值选择文件虽然命名 integration，实际为 unit 标签，首次 integration 命令无匹配，不算通过，安排按标签补验。详见 `baseline/S16/native-runtime-readers-results.json`。


### 2026-09-21：认证 HTTP 门面、共享夹具与调用者成批收敛

- 普通结算/运行设置两批合并全量验证通过：普通 11,888、unit 19,912、integration 12,886 条通过事件；既有跳过分别 4/8/4，三种 lint 0。详见 `baseline/S16/interim-settlement-runtime-readers-results.json`，不替代 S16 最终验收。
- 120 个原认证顶层测试逐项映射：DingTalk 纯规则进入 identity，其余七类身份/pending/会话/微信支付衔接的共享数据库夹具进入 tests/integration/identityhttp，保留各自普通/unit 标签。SQLite/Ent 与本地 HTTP 证据不记为真实外部认证。
- 原请求直接调用原生 AuthenticationHandler 与 payment.WeChatPaymentHandler；夹具只装配领域设置读取器、会话存储和受控验证端口，不复制认证流程、缓存或旧 SettingService。原注入失败、补偿、Cookie、签名及响应断言保留。
- 删除旧 AuthHandler、OAuth 构造/私有转接和微信支付授权包装；server 合同与 app 路由夹具直接绑定原生接口。普通/unit/integration 编译通过，普通 race 117、unit race 110、真实 PostgreSQL 注册第二阶段 Begin 失败补偿 1 条事件通过，无失败或跳过。消费者与门禁继续收尾。


认证门面批次收尾：HTTP/身份普通 race 117、unit race 110、PostgreSQL 补偿 1、直接消费者 race 20 条事件通过，无失败或跳过；unit/integration 门禁已通过，见 `baseline/S16/native-auth-http-results.json`。此前阈值文件的两项断言也已按 unit 标签实际执行。

### 2026-09-21：旧设置聚合完整删除（最终定向验证中）

- 网关测试从原设置替身直接构造 gateway/testkit 的原生读取端口，已无旧 SettingService；Fast 持久化、账号图片冷却及 Grok 默认端点断言归入所属模块，定向 race 948 条通过，unit/integration lint 0。映射见 `baseline/S16/native-runtime-settings-test-mapping.json`。
- 综合设置读取、字段准备、默认套餐校验、转发配置、缓存发布与一次提交的原测试进入 tests/integration/settings，数据替身同批迁移。组合夹具只装配实际模块及调用 settings.UpdateSession，不保留旧聚合 getter/算法。
- 首次组合夹具把原独立写入的准备态显式发布标记替换为回读快照，造成配额缓存断言失败；已修正该测试装配，使独立写入断言通过实际领域应用端口接收同一准备态。HTTP 继续验证 Runtime 的回读契约，生产代码未作行为修改。91 条组合 race 事件及用量读取原断言补验通过。
- 中间件的 JWT/管理员/step-up、Backend 与面板限流，路由的分组读取，以及注册存储测试均直接使用所属模块端口。旧 SettingService、构造器、SettingReaders 与套餐读取旧接口已删除；162 个转接方法、26 个来源文件的原委托记录见 `baseline/S16/removed-setting-delegates.json`。剩余 firstNonEmpty 是仍被使用的通用适配辅助，未按原文件名前缀误删。
- 普通/unit/integration 编译通过，最终直接消费者和门禁正在执行；尚有账号、调度缓存、平台及网关适配需继续清理，S16 不标记完成。


旧设置删除补验完成：API 配置回退合同的首次失败来自夹具在构造原生默认值快照后才更改启动配置，已调整为先准备再装配，原完整 JSON 断言保留。API 17 条补验、PostgreSQL 补偿 1 条通过，unit/integration lint 0。认证与设置两批合并全量普通/unit/integration 和三种 lint 全部退出零，详见 `baseline/S16/interim-auth-settings-closed-results.json`。

### 2026-09-21：搜索注册表、启用裁决与工具装配完整收尾

- app 持有的 search.Registry 直接被 ConfigService 和网关工具适配引用，删除 service 的全局 atomic 注册表、Manager setter、配置/用量/测试搜索转接。原生命令的配额、代次、停止及代理策略不变。
- 账号搜索三态与历史布尔值的纯判断进入 gateway/searchtools；诊断由 gateway/provider 保留原日志字段与级别，旧 Account 中不再复制该规则。配置、内容解析和启用策略测试与数据替身同批归属，注册表每个测试独占并保留 Retire 清理；逐项映射见 `baseline/S16/native-search-closure-test-mapping.json`。
- 普通/unit/integration 编译及 Wire 通过；核心/协议 race 143 条、组合策略 race 9 条、含真实 Redis 的 provider integration race 31 条通过，无失败或跳过；unit/integration lint 0，见 `baseline/S16/native-search-closure-results.json`。
- 尚有 272/15/59 个旧 service/repository/handler 生产文件（按当前物理文件计，非完成比例）；账号旧端口已按类型追踪到 40 个文件、105 个使用位置，见 `remaining-account-port-uses.json`。其存储、调度快照和执行态模型关系需按依赖继续整体收敛，不把当前通过结果作为最终 17/17 验收。

### 2026-09-21：模型目录、市场与模型策略批次

- 新增 `routing.RequestableCatalogue`，由 app 直接绑定 account PostgreSQL 存储和唯一 `routing.ModelList`；模型市场和请求目录共用 R → C → U 查询边界，旧 GatewayService 仅保留兼容委托。
- 新增 `gateway/provider.ModelPolicy`，统一实际转发与模型目录的默认模型、映射、协议、Bedrock 地域、Anthropic/Vertex、Antigravity thinking、OpenAI passthrough、限流 key 和上游模型判断；路线状态与账号记录分开传入，未复制缓存或平台规则。
- 目录原测试逐项迁移到 `tests/integration/catalogue`，模型短缓存、空结果不回退、市场排序、价格基准、区域可用性和查询次数断言保留；旧 service 的无消费者目录辅助、市场夹具和模型限流辅助已删除或按 unit 标签归属。
- 新增文件的 depguard 许可精确到文件/import；normal、unit、integration 编译与门禁均通过。模型目录/账号/路由定向 race 取得 484 条通过事件，短缓存兼容回归取得 47 条通过事件，无失败或跳过。
- 合并检查 `interim-search-catalogue-check.json` 全部退出 0：普通、unit、integration（`-p=4`）测试及 normal/unit/integration lint 均通过。计划正文前 16353 字节 SHA-256 仍为 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`。
- 仍未完成 S16：账号旧端口、调度快照兼容层、RateLimitService 健康适配和剩余 service/repository/handler 需按完整能力继续收敛；不更新 17 / 17 状态。
- 模型短缓存原有 service 测试已迁为 routing ModelList unit 回归，保留一分钟 TTL、回源次数、OpenAI passthrough、nil 结果、全局/按分组/按平台失效及全进程指标断言；无消费者的 `GetAvailableModels`/`InvalidateAvailableModelsCache` 兼容入口已删除。该缓存批次新增 47 条定向通过事件；unit 编译和 lint 复核退出 0。
- 删除旧入口后目录/缓存回归再次取得 42 条 unit race 通过事件，无失败或跳过，证据见 `baseline/S16/native-catalogue-final-race.log`。

### 2026-09-22：快照与目录缓存的原生生命周期绑定

- 续接时实际 HEAD 为 `0c3748ef5`，保留外部已提交进度，本轮未自动提交。快照启停直接绑定唯一 `scheduler.SnapshotService`；目录缓存到期清理直接绑定同一 `routing.ModelList`，hook 名称、启动/停止顺序与一分钟清理周期不变。
- 原 `TestSchedulerSnapshotOutboxReplay` 从 repository 迁至 app 集成测试，直接使用 account PostgreSQL 存储、生产 outbox 事件绑定和 scheduler Redis codec；原 last-used 回放断言保留。真实 PostgreSQL/Redis race 通过后删除旧测试及旧快照 Start/Stop/StopContext 转接。
- 修正上一批手工构造兼容目录时额外创建缓存的迁移偏差：未注入 ModelList 时保持原无缓存读取，不改变生产共享缓存。原生拥有者 race 121、直接消费者 unit race 36、真实进程矩阵 11、outbox integration race 1 条通过事件，无失败或跳过；对应日志以 `native-runtime-` 和 `native-snapshot-outbox-` 为前缀。
- 普通与 integration 编译、对应定向 lint 均通过；Wire 二次生成 SHA-256 均为 `2f3c9096482243169fe953d9ff4f7a2d45f46b392656482f07070c357dfc76b7`。未把编译计作行为验收。
- 调度快照的旧账号形状读取、剩余网关与健康适配仍待清零；本批不代表 S16 完成。

### 2026-09-21：阶段性分批提交

- 本次按用户明确授权提交已有 S16 工作区改动，保持 `main`，未推送；未继续展开调度快照或健康恢复的后续实施。
- `78214f9f7`：迁移三个 E2E 文件，修正 backend/deploy Makefile 的测试入口，并为 `backend/tests/` 添加精确的 Git 忽略例外。此前该目录被旧 `tests` 规则整体忽略；本次全部 86 个迁移测试均已纳入版本控制。
- `20e4fac2b`：提交当前原生模块、应用装配、兼容入口清理、Ent/Wire 生成代码、迁移测试与 24 篇同步工程文档。既有源码内容保持原样，强关联的模块和生成文件同批提交。
- `7e12e29f0`：归档 2,030 份原始验证资料，保留通过、失败、跳过与补验记录。原始资料共 925,574,964 字节，归档为 58,858,023 字节，逐文件 SHA-256 全部核对一致；原始文件继续保留在本地。读取方式见 [验证资料说明](baseline/S16/README.md)。
- 本次重新使用 `GOTOOLCHAIN=go1.27.0`，串行执行普通、unit、integration 三套 `go test -run=^$ -p=4` 编译检查，全部退出 0；编译检查不计为业务测试通过。
- `go test -tags=unit -race -count=1 -json ./internal/routing ./tests/integration/catalogue` 取得 399 条通过事件，无失败或跳过。普通、unit、integration 三套 lint 均退出 0，结果为 `0 issues`；E2E 仅运行编译与 Makefile dry-run，未执行真实供应商请求。
- 24 篇变更文档的结构与新增/迁移锚点检查通过。本轮命令、退出码、定向事件和既有全量日志统计见 [提交验证记录](baseline/S16/commit-verification.json)，完整输出保存在 `baseline/S16/commit-*.log.gz`。
- 原有 50 个其他任务文件保留在工作区，不纳入 S16 提交；`SYNC.md` 未提交。计划正文前 16353 字节 SHA-256 仍为 `1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9`。
- S16 继续保持“实施中”，roadmap 仍为 16 / 17。调度快照兼容层、RateLimitService 健康/恢复残留、其余旧包清理及最终完整验收仍待执行。

### 2026-09-22：快照生产读取与发布能力收尾

- 四个网关及目录构造直接接收唯一 scheduler.SnapshotService，旧 SchedulerSnapshotService 类型及 app 包装 provider 删除。候选与完整账号保持原读取时点；OpenAI 的分组读取由 app 单独绑定原 routing 存储，Gemini 内部选号沿用同一分组端口。
- 账号事件只接收 scheduler.SnapshotPublicationCache；无消费者的旧 Redis 缓存构造及数据转接删除。旧快照构造和缓存端口已退出生产文件，仅在尚未迁出的执行消费者测试中保留夹具，不复制快照规则或状态。
- 原快照写入四项断言迁至 scheduler；HTTP 的取消、预热及会话限制替身直接实现原生快照接口。消费者 race 421 条、真实 PostgreSQL 账号存储 race 75 条、真实 outbox 回放 1 条事件通过，无失败或跳过，日志分别为 native-snapshot-consumers-race.jsonl、native-snapshot-publication-integration-race.jsonl、native-snapshot-final-integration-race.jsonl。
- 夹具初次误加 unit 标签导致无标签消费者编译失败，已去掉限制并复核 integration 编译；原断言与生产行为未改。unit lint 0，精确新增 scheduler 许可不扩大目录豁免。旧执行账号转换仍需随账号/网关能力清理，不能将本批等同于 S16 完成。

### 2026-09-22：原生调度反馈与参数契约

- 删除 advancedAccountRuntimeStats、openAIAccountRuntimeStats 和私有反馈参数包装；各选号、诊断、回写消费者直接使用 app 持有的 scheduler.RuntimeStats 及 policy.FeedbackConfig，未增加反馈实例或改变回写时点。
- 四项独立反馈测试迁至 scheduler/runtime_feedback_original_test.go，保留 EWMA、时间、样本数和 16 worker 并发断言。混合选号测试同步使用原生参数。第一轮定向 race 338 条通过，unit lint 0。
- 按 Go 类型对象改绑 RuntimeSettings、EffectiveSettings 和 StickyEscapeConfig 字段，删除旧参数结构及往返转换；保留原全局到分组的选择性字段投影和配置权重校验，未借清理调整参数规则。映射见 native-policy-type-mapping.json。
- 本批核对发现快照迁移遗漏通用 Gateway 临时选号对象的分组读取绑定，已补回；新增三入口合同验证无请求分组时读取一次并应用 TopK 覆盖，4 条 race 事件通过。参数/评分/粘性消费者另有 338 条 race 事件通过，集合重叠不相加。该问题属于本次迁移回归，不计入历史修复范围。
- 快照及参数两批进入串行合并检查，见 interim-snapshot-feedback-check.json；仍按 S16 未完成状态管理后续账号、网关及旧图清零，不提交。

### 2026-09-22：设置与粘性状态实例化、合并验证

- 删除旧调度设置及粘性统计的全局 atomic 指针和绑定函数。app 将唯一设置实例直接绑定给平台消费者，OpenAI 粘性统计与日志读取同一执行实例；独立测试构造只拥有自身缓存。保留设置提交后发布、读取时点和 TTL。
- 同批删除测试对全局缓存的重置，粘性测试改用明确观测实例，原旧键回退与双写计数断言保留。Wire、全部 unit 编译通过；调度/粘性/设置消费者 race 493 条通过，无失败或跳过；unit lint 0。
- 上两批合并普通测试 11,881 条通过、4 项跳过；unit 19,916 条通过、8 项跳过。integration 因 Docker 获取容器状态超时而退出 1，相关失败发生于容器初始化，未执行目标业务断言；完整原日志保留在 interim-snapshot-feedback-integration.jsonl。
- 环境恢复后，将受影响的 infra/redis、ops/postgres、repository、app 四包以 -p=1 补验，1,074 条通过，无失败或跳过，见 native-instance-storage-retry.jsonl。原失败与补验分别登记，不修改断言或归类为生产缺陷。
- 下一批统一改绑上游错误决策的 HTTP/SSE/WS/媒体消费者至 account 原生决策，删除旧方法包装；仍保留执行账号的窄只读策略投影，随后随执行实体清零。

### 2026-09-22：错误决策、后台任务与串行消息队列批次

- HTTP、SSE、WS、图片、音频及 Grok/Gemini 消费者统一使用 account.UpstreamErrorDecision；删除旧决策类型、方法包装及无持久化包装，保留即时只读账号策略投影和各入口默认值。消费者定向 race 628 条通过，unit lint 0。
- 五项独立健康策略测试由 service/error_policy_test.go 迁到 account/provider/error_policy_original_test.go，直接构造原生 Health/UpstreamHealth，保留原错误码、池模式、写入次数与重试断言；39 条 race 事件通过。仍涉及网关副作用的测试未删除。
- 删除 service/background_tasks.go 的全局 runner、安装/恢复函数和通用闭包包装；Codex 快照、Grok 统计、Live observer、审核与资金副作用均显式使用应用任务拥有者。关闭 hook 改为 ApplicationBackgroundTasks，顺序保持；进程断言同步改名。
- 后台实例批次全部 unit 编译通过，定向 race 742 条通过，无失败或跳过；真实 standard/simple 及 SIGTERM 进程验证通过，见 native-background-process.jsonl。新增任务拥有者契约验证等待未完成项、结束后关闭和拒绝停止后派发。
- 删除串行消息队列旧构造器与 HTTP 类型别名；解析资格移入 gateway/requeststate，HTTP 直接调用 gateway/httpapi.UserMsgQueueHelper，app 继续唯一构造 scheduler 队列。新增资格边界断言，编译与定向 race 通过；本批 unit lint 0，删除对应精确旧许可。

### 2026-09-22：幂等 HTTP 与观察实例收敛

- 十一类用户/管理员 HTTP 处理器通过 app 启动前绑定同一 IdempotencyCoordinator；helper 改为各处理器持有的 Executor，默认写入/维护 TTL 随协调器实例读取。删除进程默认协调器及应用安装/恢复 hook，不改变 scope、hash、payload、降级与重放 Header。
- 协调器和清理任务显式接收原日志出口，删除全局观察绑定；原进程累计指标保持。测试改用独立实例，不再通过全局安装/清空隔离。
- 全部 unit 编译通过；HTTP 消费者普通 race 396 条、装配/清理 unit race 20 条、进程及标签集合 race 29 条通过，无失败或跳过。后者未选中旧目录存储测试，明确不计为存储验收。
- 原 repository/idempotency_repo_integration_test.go 的三个测试迁到 idempotency/postgres，保留原认领、回收和成功记录断言及事务回滚；隔离 PostgreSQL 真实迁移下 race 全部通过。应用原 24 并发认领与重放/冲突/清理合同另行补验。
- 精确登记 management.go 的原生幂等 HTTP 依赖及 app 幂等装配测试 import，unit lint 0。阶段原文前 16353 字节摘要仍为 1a03e5525668efaf2b47669135a8f37f3c050b0004729944a959d64314ad0ed9。

### 2026-09-22：合并全仓结果与平台客户端测试归属

- interim-native-owners-check.json 记录本轮完整普通/unit/integration：11,897/19,932/12,895 条通过，4/8/4 项既有跳过；三组测试退出均为零。普通与 unit lint 0；integration 首轮仅缺新迁移夹具的 migrations FS 精确许可，补齐后全仓 integration lint 0，见 native-owners-lint-integration-verified.log。
- 账号授权刷新 CAS 的四个平台与 Vertex 等锁取消测试迁到 tests/integration/account，直接使用 AccountStore、原生 token 源及 CloneRecord，删除对旧仓储与旧账号转换的依赖。真实 PostgreSQL/Redis race 16 条通过，无失败或跳过。
- Anthropic OAuth/usage 客户端测试与原 HTTP 夹具迁到 upstream/anthropic；OpenAI OAuth 超时及 Code Assist HTTP 版本断言各归所属 upstream。Gemini token 缓存删除/故障测试归 account/rediscache。普通/unit 定向 race 25 条通过；真实 Redis 工具集中于 testutil/rediscontainer，不以 miniredis 代替。
- 删除 GeminiTokenCacheKey、AntigravityTokenCacheKey 的旧 service 包装；测试直接读取所属原生键函数。文件映射见 native-platform-test-mapping.json、native-platform-test-split.json；账号执行视图的 token 投影仍有生产消费者，未提前删除。

### 2026-09-22：账号存储合同与备用装配清零

- 账号 SQL 字段保护、自动暂停、托管 Extra、凭据 CAS、查询投影和参数上限测试直接构造 AccountStore；纯 Extra 规则与 Ent 行复制测试归 account/postgres。跨账号/调度/资金合同归 tests/integration/account，使用现有真实 outbox、SnapshotPublisher 与 billing AccountUsageStore，不复制规则。
- 原完整 AccountRepoSuite、附属排序/协议补丁、影子与选号数据库查询测试同批迁移。原业务断言、事务、取消后的发布、真实 SQL 次数/列、8 位消费重置和健康字段保护均保留；分拆协议迁移文件时只移动账号方法，原 SQL 与分组测试保留。
- 单元定向 race 61 条、首批 PostgreSQL 29 条、完整原套件 76 条通过；合并账号存储包及跨模块 integration race 367 条通过，均无失败或跳过，集合重叠不相加。映射见 native-account-store-test-mapping.json。
- NewAccountRepository/newAccountRepositoryWithSQL 测试消费者清零后删除；旧 repository 不再持有 Ent/SQL/缓存备用字段，不再构造 AccountStore 或 AccountUsageStore。account_event_binding、account_snapshot_publisher、account_usage_binding 删除，生产转接直接引用 app 持有的两个存储。
- 删除已无消费者的 Ent/批量账号转换辅助及 LegacyUsageOptions；全部 unit 编译通过。新夹具增加的实际 import 精确许可并保留角色限制；旧辅助测试函数无消费者后删除，不削弱断言。剩余执行账号投影仍明确保留，尚未宣称 repository/service 全部删除。

### 2026-09-22：原生 Redis 契约与共享技术夹具

- Key 创建限频、计费余额/窗口、兑换、身份邮件、Anthropic 请求指纹和网关会话缓存测试迁至各实际 Redis Adapter；保留命名空间、TTL、错误、一次性消费和竞争断言。Key 键格式测试直接调用原生函数，删除两个仅为旧测试存在的别名/函数包装。
- testutil/rediscontainer.Suite 每套件仅启动一个隔离 Redis，逐测试沿用原前缀 Hook 与清理；旧 repository 的重复套件、前缀 Hook 和 TTL 辅助删除，剩余测试复用同一工具。未用 miniredis 替代真实存储，也未修改生产缓存协议。
- 六类缓存编译通过，integration race 64 条通过，无失败或跳过。精确登记实际文件使用 rediscontainer 的依赖，保留对应角色的原基础限制；映射见 native-cache-test-mapping.json。

### 2026-09-22：身份与 Key 存储测试完整归属

- 上一轮存储/缓存合并检查普通、unit、integration 分别取得 11,897/19,932/12,895 条通过事件及 4/8/4 项既有跳过。普通 lint 首轮发现账号资金夹具仅被 integration 使用；已将该夹具与消费者对齐构建标签，普通/unit lint 恢复零，不新增忽略。
- 邮箱归一化、别名、接纳及兑换字段断言连同 SQLite/SQL 替身迁入 identity/postgres，31 条 race 事件通过。用户状态常量直接引用 identity 原值；仅为 SQLite 驱动和真实迁移 FS 增加精确测试依赖。
- Key CRUD、排序、字段保护、认证投影、最后活动、数量限制与并发累计完整迁入 tests/integration/apikey；分组纯投影断言归 routing/postgres。真实 PostgreSQL 与原 SQLite 合同共 51 条 race 事件通过，无失败或跳过。
- 用户主存储、身份档案、排序、关联、字段保护及软删除查询套件迁入 tests/integration/identity，去掉最后的旧执行账号测试数据引用；保留真实提交、逐项清理与外层事务边界。隔离容器统一复用 testutil/postgrescontainer，未启动业务 worker。
- 身份/Key 两批定向 integration lint 为零；映射和原测试事件保存在 native-identity-store-unit-mapping.json、native-key-store-test-mapping.json、native-user-store-integration-mapping.json 及对应 race 日志。该结果属于中间批次，不替代 S16 最终验收。

### 2026-09-22：身份事务与 Grok 免费层能力收尾

- 用户存储迁移后的真实 PostgreSQL race 取得 81 条通过事件。用户删除/墓碑回滚、分组替换、团队参与、注册赠送、消费重置与认证触发器合同随后同批迁入 tests/integration/identity；合并取得 98 条通过，无失败或跳过。使用原生参与工厂和原 SQL/Ent 连接，未修改生产事务。
- Grok 免费层裁决、阈值、正负缓存、刷新去重与淘汰归 account.FreeQuotaGate；usage 保留批量优先的窗口统计读取，app 只投影配置、日志及后台任务拥有者。删除旧进程缓存与按缓存索引的全局在途表。普通两条选择链分别持有缓存，高级调度器仍逐实例持有；不合并作用域，不改变首次放行或负缓存。
- 原门禁断言迁至 account，同步保留三个实际选择入口测试，新增应用缓存作用域与任务关闭合同。Wire 和 unit 编译通过，定向 race 71 条事件通过，无失败或跳过；映射见 native-free-quota-mapping.json。
- 更新 Grok 上游文档的当前所有权。全仓 unit lint 首轮仅发现已迁完普通消费者的两个 Key 装配夹具只供 integration 使用，已同步构建标签；不增加忽略规则。S16 仍未完成，后续执行实体、HTTP 与旧图继续清零。

### 2026-09-22：Gemini 配额预检直连与装配回归

- Gemini 执行消费者直接绑定唯一 account.GeminiPrecheck；删除旧健康聚合中的三个转接、备用缓存构造及重复 usage 投影。RateLimitService 两个构造入口和应用 provider 不再接收 GeminiQuotaService，所有调用者与替身同批调整；Wire 重新生成。
- 原第三方 API Key 豁免/官方日配额断言迁至 app 的真实配额装配测试；两个实际 429 冷却消费者显式注入相同原生预检。编译通过；Gemini、健康错误、调度和直接消费者 race 510 条事件通过，无失败或跳过，unit 定向 lint 为零。
- 前一轮 interim-identity-quota 普通全量仅新增装配测试失败：其 defer cancel 先于 Cleanup 执行，Cleanup 使用已取消 context 产生随机选择。已使用独立五秒清理预算，原作用域和任务拥有者断言保留；连续十轮 race 通过。该项为本次新增测试回归，未扩展历史修复范围，原失败日志保留。
- 重新串行执行合并完整检查，结果写入 interim-native-quota-verified-check.json；此为中间验证，旧执行模型、HTTP 及旧图清零完成前不更新 17/17。

- 账号旧端口按 Go 类型对象补齐直接方法消费者，见 remaining-account-method-consumers.json：共 18 个直接选择器方法。该清单不涵盖隐式接口满足和运行时类型断言，不能仅凭没有直接调用删除方法或将整个旧仓储包装改名迁入 app；后续结合原生参与端口、执行形状及实际构造一起清零。

- interim-native-quota-verified 普通全量 11,898 条通过/4 项跳过，unit 19,933 条通过/8 项跳过；integration 除 app 外 12,558 条通过/4 项跳过。app 首轮因两个外部集成测试的 NewS16AccountRecovery 别名仍保留已删除参数而未编译，原 build-output 保留；同步调用后 app 全量 integration 补验通过，包含 version、standard/simple SIGTERM、两个精简命令、引导失败释放、监听失败、Web/CLI/AUTO_SETUP。
- native-quota-lint-final-check.json 记录普通/unit/integration 三套全仓 lint 均退出 0。源 SQL、Ent 与暂存区无变化，diff 检查通过；上述结果仍为中间批次验收，后续旧执行模型与接口继续按 S16 清零。

### 2026-09-22：账号运行时停调、恢复代次与回滚所有权

- account.RuntimeBlockState 统一拥有原七项停调/重试/代次状态；停调延长、清除、过期、刷新失败发布和 Grok 暂定回滚同批迁移，保留锁顺序及原两个两分钟预算。app 独立构造一个实例，后台刷新和管理员恢复直接绑定原生 RefreshFailureObserver/RuntimeUnblocker；执行消费者使用同一实例。
- 原同账号 OpenAI 429 资格/分类顺序由 account/provider 复用新状态，实际重试循环、响应输出与请求取消仍由网关决定。未建立第二套重试或改变刷新存储失败边界。Wire 已生成，unit/integration 编译通过。
- 停调不缩短和显式清理的私有状态断言迁到 account；凭据版本阻断测试同批迁移。消费者时间边界通过显式测试时钟验证，不导出可变缓存给测试。初轮定向 race 82 条通过；扩大至原凭据竞争、回滚和恢复消费者的回归另有记录，集合重叠不相加。unit 定向 lint 为零。
- 恢复的所有生产调用已直连 account.RecoveryService，删除 RateLimitService 的五个无消费者恢复转接；仍被旧健康装配使用的 RecoveryCore 投影暂留，不能据此宣布旧健康聚合已删除。映射见 native-runtime-block-mapping.json。

### 2026-09-22：模型目录 HTTP 整批改绑

- app 直接构造 gateway/httpapi.ModelsHandler 并绑定同一 RequestableCatalogue；删除旧 GatewayHandler 的 Models、AntigravityModels、Gemini List/Get 四个方法及 models_http_adapter.go。平台展示投影归 gateway/provider，原展示值、默认列表及稳定合并归纯叶子 gateway/modeldisplay；JSON 字段、空值和排序保持。
- 原模型 HTTP 与 Gemini 目录测试迁至 app，目录数据使用原生 account.Record 和 routing.RequestableCatalogue；Codex client_version 路由测试同步直接注入 ModelsHTTP。仍为其它转发测试使用的渠道构造/数据替身单列到执行测试夹具，未删除其调用者或复制业务规则。
- 模型目录、原生 Gemini、路由、渠道改写和直接消费者 unit race 116 条事件通过，无失败或跳过；Wire 与编译通过。新增纯叶子门禁，精确登记装配、HTTP 和平台数据投影依赖并删除旧文件许可。旧 Gemini 远端选择/GET 尚由 app 只读目标端口衔接，未把该剩余执行能力宣称已迁完。
- 模型重定向文档锚点迁至原生 HTTP 实现，目录与市场文档同步当前结构。映射见 native-models-http-mapping.json；本批不替代 S16 最终验收。

- 模型目录消费者清零后，删除旧 GatewayService 的目录入口、绑定字段、缓存字段与备用构造；移除三个生产兼容文件及 xhigh 展示包装。原 TTL 配置断言归 app，Grok 别名/Bedrock 市场一致性断言归 tests/integration/catalogue；剩余结算执行交叉测试只保留原生目录的数据装配，不再调用旧目录方法。扩大模型/价格/地域/路由回归取得 771 条 race 通过事件，无失败或跳过，见 native-model-catalogue-detach-race.jsonl。
- 模型纯叶子的合法、HTTP 依赖拒绝、根包反向依赖拒绝分别在普通/unit/integration 共 9 个场景验证，夹具已删除。初次格式诊断及叠加规则先报告 core-gateway 的结果独立保留；最终实际判定见 models-leaf-depguard.json。

- 模型目录整批清理后定向 lint 为零，已有与新增 Go 文件的 whitespace 检查均通过。运行时停调/恢复与模型目录两批合并进入普通、unit、integration（-p=4）及三种 lint 检查，独立结果写入 interim-runtime-models-check.json。未完成 S16 前不更新 roadmap 完成状态，不自动提交。

### 客户端限制回退批次（2026-09-22）

- 通用网关与 OpenAI 调度入口共同委托 routing.ResolveClientGroup；保留两个入口的缺失快照、非正回退 ID、强制平台及读取/识别顺序差异。旧错误常量消费者直接使用 routing，未复制规则。
- 原循环检测测试迁入 routing，补充读取顺序及差异契约。编译、目标 lint 与定向 unit race 均通过；证据见 `baseline/S16/native-client-group-{final-compile,lint}.log`、`native-client-group-race.jsonl`。策略文档及稳定锚点同步。

### 推广、权益存储及公开用量批次（2026-09-22）

- 推广返利和 Promo 四个原测试文件迁入 tests/integration/promotion，直接绑定原生 promotion、billing 事务参与者。真实 PostgreSQL race 13 条通过，无失败或跳过；原测试名称及断言保留。首次测试装配遗漏 Ent runtime 已修正，初次失败日志留存，不计通过。
- 订阅、兑换、平台额度七个集成测试文件迁入 tests/integration/billing；套餐投影测试迁入 billing/postgres。真实 PostgreSQL/Redis race 定向 63 条通过，无失败或跳过。保留外层事务、管理延长竞争、dirty 回填及重置后的额度判断断言，测试用户投影不再 import service。
- 公开用量三项原展示断言迁入 usage/httpapi；删除旧 Gateway 用量转接、用户/用量/余额单位字段及构造参数，app 删除不再使用的余额单位 provider，Wire 由生成器更新。编译通过，生产路由继续绑定既有 providePublicUsage。
- 迁出文件的历史许可已删除，目标测试逐文件使用精确 import 门禁；对应 native-*-mapping.json 记录路径。HTTP 文档同步，阶段最终验收未宣布完成。

### 身份、Redis、历史迁移与分组测试收敛（2026-09-22）

- 六个身份会话测试文件迁入 tests/integration/identity，真实 PostgreSQL/Redis 定向 race 11 条通过。保留邮箱创建补偿、pending 竞争、refresh 单次轮换、Passkey 真实 SDK 和双缓存重订阅原断言。
- 账号健康计数、批任务租约分别归 account/rediscache、batchimage/rediscache；三个缓存订阅停止契约归跨模块 subscriptions 集合，共13条真实 Redis race 通过。旧 repository 的 Redis 测试消费者清零，已删除其容器启动及连接转接。
- 历史 SQL 测试迁入 tests/integration/migrations，45 条 race 通过；原 SQL、schema 和约束不变。两个混合文件的 GroupRepoSuite 方法按符号归 routing，分组存储集合39条 race通过。扫描 SQL 直接调用 infra，不保留 scanSingleRow 包装。
- 资金存储集合补齐兑换查询与余额锁后，完整 tests/integration/billing race 105 条通过。清单见 native-identity-session、native-remaining-redis、native-migration、native-routing-store 映射，原编译错误日志保留，未计作通过。

### 存储批次合并检查及计数 HTTP 解耦（2026-09-22）

- repository 测试及共用测试启动/夹具已清零；剩余11个生产文件仍是旧执行账号形状转接，未声明旧 repository 包已删除。最后迁移的授权分组、软删除、账号策略、计划测试、探测和任务资金契约定向 race 14 条通过。
- 团队、设置、任务存储和 outbox 集合 race 48 条通过，创作 workspace unit race 3 条通过。团队测试恢复原 UTC 进程初始化；首次遗漏导致的 PostgreSQL Local 时区失败为本次夹具迁移回归，已修正，生产查询和原断言未变。
- 合并检查 Wire、普通及 unit 全量通过：11,904 / 19,938 条通过事件，4 / 8 项既有跳过。普通、unit、integration lint 经精确测试门禁补齐后均为0；保留初次 unit SQLite 门禁诊断。日志前缀 interim-native-storage。此次没有宣称全量 integration 通过；既有 EasyPay 观察仍待范围确认。
- count_tokens 原生 HTTP 由 app 直接构造，删除旧工厂及 CountTokens 转接。受控 CountTarget 保留原选择、计数转发和失败释放时点，不向 HTTP 暴露旧账号；app 只连接尚未迁出的执行原语。共享报文准备、分组错误、Anthropic 错误展示及兼容指标采样保持唯一实现。
- 原 SSE 防拼接断言迁入 gateway/httpapi；新增完整 HTTP 计数契约验证资金预检顺序、强制平台、两次独立改写、一次失败释放及同步请求类型。定向 race 100 条通过，目标 lint 0，Wire 和编译通过。映射 native-count-tokens-mapping.json，生命周期文档已同步。

### Qoder Chat 旧 HTTP 依赖清理（2026-09-22）

- Chat 原生构造不再接收 QoderGatewayHandler；错误阶段映射进入 gateway/httpapi，供应商错误解释进入 gateway/provider，删除 qoder_http_compat.go 及无消费者的 Chat 辅助导出。Messages/Responses 暂余的旧单步执行复用同一错误展示器。
- QoderRequestsAndAttempts 由 app 独立 provider 构造一次，Chat、兼容 HTTP 与平台尝试共享原 StopOrder=15 和进入屏障，未新建 worker。真实 standard/simple SIGTERM 两个场景通过，检查了原资源停止顺序。
- 原4个 API错误断言迁到 provider，规则覆盖断言迁到 app，兼容换号断言迁到 upstream/qoder。首次迁移曾误复用 Chat 对普通错误的换号策略，原断言捕获后已恢复两个入口的历史差异；该本次回归及失败日志保留。最终定向 race371条通过、目标 lint0，Wire与编译通过。映射 native-qoder-chat-http-mapping.json。

### Qoder 兼容能力整体收尾（2026-09-22）

- Messages/Responses 的生产构造、固定端口、受控账号目标、等待/重试资源、刷新和完成投影已同批迁入 gateway/httpapi 与 app。删除 QoderGatewayHandler、构造器及两份旧单步适配；生产、测试与 Wire 对旧类型的引用清零。
- 原等待队列拒绝、计数释放、断开后的完成释放、SSE/JSON 错误和成功粘性绑定测试迁入实际所有者。原无生产消费者的刷新集合 helper 删除，排除集合及三账号预算由同名测试直接执行 text.RunQoderCompatible；其 nil 包装断言不再作为生产证据。
- 两种协议的完整 HTTP 契约新增成功/部分用量分支，检查资金预检先于选择、同一次完成输入、原报文与改写报文区别、失败不刷新/换号/绑定、释放及反馈次序。新增测试最初把 int token 值写成 int64 断言，已修正夹具类型，保留初次日志。
- 定向 unit race376条通过，目标 lint0；真实 standard/simple SIGTERM通过，唯一 QoderRequestsAndAttempts、队列和共享资源关闭顺序保持。映射 native-qoder-compatible-mapping.json，文档已更新。app 中仍保留旧执行账号的受控调用投影，尚未声明整个旧网关服务清零。

### 模型拒绝展示与 Qoder 原测试归属（2026-09-22）

- 分组模型不支持提示的资格过滤、排序、显式空集合与错误构造进入 routing；站点默认目录与账号规则投影进入 gateway/provider。三个生产选择入口直接调用原生规则，旧 service 仅保留候选读取端口投影，旧 qoder_site 和聚合算法文件删除。
- 原 Qoder 站点/白名单/映射断言直接使用原生 Record；developer 消息、max_completion_tokens 和并发 conversation store 测试迁至 upstream/qoder。新增规则读取顺序与 nil/空集合契约，定向 unit race503条通过，目标 lint0，编译通过。映射 native-model-rejection-test-mapping.json。
- 准备将上述稳定批次合并运行计划规定的普通、unit、integration及三组 lint；这属于常规迁移验收，不改变清单外历史问题的修复范围。

### HTTP/平台批次合并全量结果（2026-09-22）

- interim-native-http 的 Wire、普通/unit/integration 全量以及三组 lint 全部退出0。事件摘要见 interim-native-http-summary.json，跳过仍单列；该轮覆盖已迁存储、CountTokens、完整 Qoder HTTP 与模型拒绝规则，不替代 S16 全部旧包清零后的最终验收。
- 本轮常规全量中的 EasyPay empty_response 通过，支付源文件和夹具未作本阶段修改。此前一次 CloseIdleConnections 失败仍保留为观察，未追加清单外定向复现或修复。

### 提示词固定依赖收尾（2026-09-22）

- HTTP 的 MessagesPrompt 直接使用 promptpolicy.Service 已有方法；原 service 两个提示词转接和 RuntimeReaders.Prompts 字段删除。HTTP 构造及 WS 服务直接接收 app 唯一提示词实例，消费者、测试替身、Wire 和精确门禁同批调整。
- 普通175条、unit race206条通过，无失败或跳过，目标 lint0；真实双计数 HTTP 验证同一个缓存只回源一次、构造不回源。WS followup 保留原替换内容断言；settings 包装的旧指针断言由该实际行为契约承接。配置和生命周期文档同步，native-prompt-binding-mapping.json 记录对应关系。

### 响应头过滤与执行装配批次（2026-09-22）

- egress 响应头编译从 service 配置读取转为 app 投影 `provideResponseHeaderFilter`，Gateway/Gemini/OpenAI 三个执行构造接收同一编译结果；默认白名单、强制删除、逐跳头和输入切片隔离保持。原测试夹具迁入 app/handler/middleware 精确位置。
- service ProviderSet 删除，剩余执行构造由 app 的 wireinject-only `gatewayExecutionProviders` 装配，普通生成代码继续使用可见的 `provideOpenAITLSRouters`。Wire、受影响编译通过，响应头普通399（1既有跳过）、race534（1既有跳过）通过，目标 lint0。
- 首次把 provider-only 装配函数放在 wireinject 文件导致生成代码普通 lint typecheck 失败，已将纯 provider 投影移到普通 app 文件；失败日志保留。

### 本轮批次检查的环境阻塞与回退记录（2026-09-23）

- 日志前缀 s16-final 仅为此次 runner 的文件名，旧 service/handler/repository 仍未清零，不视为 S16 最终完成证据。Ent/Wire、普通和 unit 全量退出0；integration 三项 identity 用例因 Docker socket/端口就绪超时未执行到业务断言。原失败事件保存，必要存储验证保持待复验。
- 为减少容器启动尝试的共享 sync.Once 夹具错误地把资源挂到首个测试清理，且首次启动失败后导致后续 nil；这一临时修改已全部撤销，恢复 identityDatabase 的独立资源和清理实现，不改生产代码或断言。后续不要以此共享版本作为基线。
- 当前 unit/integration 定向 lint复验0；本轮非最终 runner 中 build-linux 未设置交叉环境且未执行，不得当作 Linux 构建证据。真正最终验收必须完整按计划另跑。

### 创作执行、图片意图及原生认证边界（2026-09-23）

- 创作 worker 直接接收 app 构造的 creative.Executor；删除旧 CreativeExecutor、CreativeExecution、选择包装及四份测试转接。原请求/结果纯断言迁入 creative/provider，两项旧技术 transport 绑定仍在 service 的明确目标投影测试中。106 条定向 race 通过，额外装配测试验证构造不回源及保留两次分组读取；Wire/编译通过。
- OpenAI API Key 健康错误归因进入 gateway/provider，原 RateLimit 的观察/空成功方法删除，两个实际失败入口仍调用唯一 account.HealthService；取消、请求级/供应商级错误与独立重试不进入计数。11 条定向 race 通过。
- 图片意图生产与测试消费者直接使用 media 唯一策略，provider 只绑定平台纯工具解析及 Grok 被动声明例外；稳定错误消息直接引用 media 常量。旧图片意图文件删除，混合测试中的图片输出计数归 protocol/openai，显式/被动工具及 benchmark 原断言迁入 provider（未运行 benchmark）。与创作批次合并普通95、unit race253通过，目标 lint0。
- Key、失败 Ops 投影和强制平台上下文归 apikey/httpapi；资金来源读取归 gateway/httpapi，identity 根包及旧 middleware 主体/角色/Principal 转接删除，消费者直接读 authctx。字符串编码和取值时机不变。普通98、unit race147通过。
- JWT、管理员、step-up、会话绑定、后台模式原测试直接验证 identity/httpapi，113条定向race通过；旧七份测试构造/克隆包装删除。废弃 typed-nil 包装的结构断言随包装删除，未认证且 nil 设置仍返回401的业务断言保留。旧 middleware 的认证类型、SecurityClientIP、错误包装和复合模型转接清零，AdminOnly 无调用者删除；原生路由类型、header/响应结构保持。
- 认证上下文与剩余消费者完整unit lint复验0，映射见 native-auth-context、native-middleware-types、native-identity-http-original-tests。此前三项 Docker 启动超时的原 identity 测试恢复独立夹具后复验通过（5条含父子事件），未改业务逻辑。

### 原生独立测试按符号批量归属（2026-09-23）

- 使用 go/types 记录旧 service 的 unit 集合逐文件实际符号依赖。确认无旧生产符号、无其他测试文件依赖及无反向测试调用后，将29个旧文件拆为30个所属模块或跨模块测试文件，共133个 Test/Benchmark 声明；原标签、名字及断言保持。
- 覆盖平台报文、身份/加密续接/重放、用量类型、通知与注册邮箱、Key快照及渠道规则。domain_constants 按两个所属常量拆分；没有按文件名推定所有权。
- 全仓 unit 编译通过；精确原测试名单普通242、unit race294条通过，无失败或跳过。benchmark仅迁移、未执行。路径映射见 native-independent-tests-mapping.json，符号依赖见 remaining-service-test-symbol-dependencies.json。

### 流终态与 HTTP 续接归属收敛（2026-09-23）

- 旧 OpenAI stream helpers 按职责拆分：失败 SSE 构造归 gateway/provider，通用失败 wire 与 WS 请求视图归 protocol/openai，缺失 usage 的采样及日志归 gateway/telemetry。调用点只提取状态码和 ID，采样窗口、累计、错误文本及终态规则不变。
- Codex ID、版本组合、工具空身份、usage 复制与终态测试直达原生实现，删除五个纯测试转接；混合终态文件按协议、平台与采样拆分，保留原测试及 benchmark（未运行）。普通319、race424通过，各1项既有跳过。
- HTTP续接归属校验进入 gateway/session，标记及读取进入 gateway/httpapi；删除旧 Set/Validate/Bind 公开转接。app 构造唯一 OpenAIWSStateStore 并由生成Wire注入，旧执行只保留原生存储取得及有序写入投影。141条相关race通过，原同用户跨Key和未知响应拒绝断言保持。
- integration全仓以 -exec=/usr/bin/true 仅编译测试二进制，不启动TestMain容器，此项不作为行为验证；存储与进程验收仍须正常执行测试。

### 账号策略、请求档位与 Fast 裁决批次（2026-09-23）

- 10个旧账号策略测试文件直接使用 account.Record/RuntimeConfig，保留日期加载注入及原断言；普通116、unit race160条通过。Vertex project 的凭据解析复用 upstream/vertex 唯一函数，账号测试、批量任务及在线转发都保留按需调用，旧 Vertex/Anthropic 认证头实体转接删除；相关race130条通过。
- 请求原档位、最终档位、模型候选推导、策略快照及字段改写进入 gateway/requeststate；显式 map 字段解析归 protocol/openai。HTTP 分组策略和用量标记、WS 策略与 UsageDecoder 接入同一实现，原静态规则测试移到 routing，混合用例保留真实转发断言。普通143、最终定向race231条通过。
- Fast 系统/分组/Key 裁决及 HTTP 改写归 tierpolicy，WS 帧和错误事件归 ws，HTTP/SSE 策略错误归 httpapi。生产和测试删除旧算法入口，旧账号边界仅投影资格与惰性读取；原实际转发与配置测试继续验证生产消费者。新增短路/按需查价顺序契约，80条定向race通过。
- 首次编译中的 HTTP 残留 service 引用、混合测试引用及脚本生成的 import 顺序错误均为本次迁移问题，已修正；原日志保留。各批编译已通过，门禁随准确文件/import更新。native-account-policy-tests、native-credential-projection、native-effort、native-fast-policy 保存对应清单及行为证据；旧执行图仍未清零，不宣布 S16 完成。

### Compact、报文视图与 Responses 兼容整批收尾（2026-09-23）

- Compact 路径、body-signal、种子、协商 Header 和日志已归原生 HTTP；触发项规则归 protocol/openai。OpenAITextHandler 直接执行归一化和结果日志，旧 backend 三项回调及 ForTest 生产入口删除，原先的 ForceCodexCLI 日志字段改为静态 Options 投影。原路径/JSON/日志测试迁入目标；仍被其他真实入口使用的日志捕获夹具暂保留原测试位置。
- 原 OpenAIRequestView 包装删除，生产及测试直接使用 requeststate；完整解码继续使用 UseNumber 与原错误前缀。原“不写 Gin 缓存”测试改为执行实际剩余 forward Adapter，断言保持。Compact 与视图定向race222条通过。
- `none` 保留/过滤与 Compact max→xhigh 的平台资格和报文实现归 gateway/provider，原资格测试改为实际 Prelude.CompactEffort，相关race18条通过。协议/日期无新行为变化。
- 旧 Responses 历史输入、工具 ID、schema、孤立工具输出及 WS 兼容算法整批迁入 provider，在线调用、独立原测试及精确门禁同批更新。恒 false 截断函数及对应生产端口删除；原“不截断”测试现在执行真实 WS 归一化并检查完整结果，原大文本真实转发测试继续保留，原已解码对象的补丁同步时点不变。168条定向race通过。
- 早期编译捕获的协商 Header 函数名、目标包自引用和遗漏测试常量均为本次机械迁移回归，已修正并保留日志。映射 native-compact、native-request-view、native-response-effort、native-responses-pipeline 记录生产及测试归属。此前档位批次全仓unit lint已退出0；本批门禁继续复核，后续合并稳定批次执行全仓验证。

### 报文与策略批次合并验证（2026-09-23）

- interim-native-policies：Wire、普通全量11930条通过（4既有跳过）、unit全量19964条通过（8既有跳过），退出0。integration取得12922条通过、4项既有跳过，3项失败均为 PostgreSQL 容器在业务断言前启动超时；不是整组通过。
- 原3项（幂等回收、S02存储合同、outbox合并）保持原断言，使用独立容器串行补验退出0；失败容器已由测试框架清理，未执行额外删除或修改超时。补验不替代最终 -p=4 全量验收。
- 同一冻结代码的普通/unit/integration三组lint均退出0，记录在 interim-native-policies-recovery-checks.json。原计划正文摘要、SQL与S00—S15冻结资料不变，索引为空。剩余旧生产文件为service248、handler49、repository11；仍需完成旧执行形状、共享健康/调度及HTTP装配等清理，roadmap保持16/17。

### 失败展示、分组派发与无消费预检完整绑定（2026-09-23）

- 空 Chat/Responses 终态的失败事实、稳定 reason 和客户端安全消息归 gateway/forward；观察及展示投影归 gateway/httpapi。删除旧静默拒绝构造与旧失败投影函数，保持两类 Ops 消息差异、原内部失败编码及规则次序。83条定向race通过，目标unit lint退出0。
- Messages 精确/系列映射归 routing，Grok动态目录由 provider按命中时点读取；HTTP读取已认证Key的分组并登记模型链。旧service派发文件及handler三项包装删除，原规则和HTTP断言直接迁至所有者，22条race通过，全仓unit lint退出0。
- OpenAI/Grok计数及Responses输入token预检由app直接构造OpenAITokensHandler，生产路由、受控账号目标、固定端口、Wire、测试及门禁同批改绑。删除旧两个执行Adapter和两个HTTP转接文件；不再通过旧OpenAIGatewayHandler创建计数入口。
- 21条相关race通过：原无槽计数资金顺序、Grok本地估算、错误权限、支持/不支持平台路由断言保持；新增原生HTTP合同验证输入预检两次尝试各释放账号槽、仅一次资金预检及原报文映射顺序。首次新增夹具漏填分组平台，误将空额度平台断言为openai，已补齐夹具而未改生产行为，原失败日志保留。
- 原计数顺序测试和最小替身已迁入gateway/httpapi；app构造/停止合同通过，证明缺少依赖和共享活动屏障停止均先于body读取。全仓unit lint再次退出0。清单native-error-surface、native-messages-dispatch、native-openai-tokens记录替代位置；完整最终验收仍在全部旧包清零后执行。

### 原生账号夹具与旧仓储写权限收窄（2026-09-23）

- account DTO 的原脱敏/嵌套副本测试直接使用原生Record及Mapper，删除旧记录投影与测试别名，12条race通过。usage/postgres的8个integration文件及共享夹具直接使用account.Record，原Ent写入、数据和断言不变，187条真实PostgreSQL integration race通过。首次重复import的机械迁移错误已修正，未修改业务实现。
- testutil四个业务构造函数在整个backend中无定义外引用，删除fixtures.go及其历史许可；没有把旧服务图重建到测试目录。native-account-fixtures-mapping.json记录所有消费者。
- unit/integration的Go类型使用清单确认旧AccountRepository候选方法无直接调用或运行时能力断言。按配置入口成批删除17项接口声明及实际转接，包括创建/删除、绑组、代理回退和额度重置；原生account/billing存储继续唯一执行这些操作。
- 编译揭示BulkUpdate仍通过account.OpenAIPlanWriter承担套餐观测的隐式接口义务，已恢复这一方法并登记实际绑定。不能仅凭直接调用计数判定可删除。两个构建集合全仓编译通过（仅编译不算行为），健康定向race32条、原生账号存储integration race155条通过。account-repository-config-prune.json保存删除项、保留义务及证据。
- 旧仓储仍保留执行读取、凭据、健康与尚需的累计转接，未宣布repository或旧实体清零；最终资金销项仍须逐条完成。

### 仓储能力断言、原生健康与完成实例所有权（2026-09-23）

- 在整个内部依赖图按方法名保守登记unit/integration运行时接口断言，补足直接调用与签名文本匹配不能证明的能力需求。已删17项没有运行时断言义务；再删除11个无调用的额外仓储转接、3个空文件及旧OAuth分页类型。两组全仓编译通过，原生刷新/凭据定向race430条通过，真实存储补验81条通过。首次误选internal/account/postgres只执行了1个投影测试，明确不计数据库行为证据，已改用tests/integration/account的实际存储合同补验。
- 使用现有完整golangci配置在仓库外可丢弃模块验证24个normal/unit/integration门禁场景：合法Adapter、精确既有绑定、新文件拒绝、既有文件新增禁止依赖、改路径后例外失效、非法子包、核心反向依赖、protocol I/O均取得预期结果。初始合法夹具错误选用了未获准的account根包，保留拒绝日志；随后用合法的gateway契约验证，没有放宽规则。所有临时夹具已删除。
- app独立构造accountHealthRuntime，再以单向纯绑定向旧执行发布同一Health/Recovery/Team/Limits/Upstream实例；Antigravity重试直接依赖原生运行时。unit race3条、真实PostgreSQL及本地协议装配race7条通过，Wire和目标integration lint通过。
- 完成记录器直接由app组合原生价格、资金、用量、健康及副作用端口。Forward/OpenAI各自保留一份隔离倍率缓存，与对应旧执行入口共享；旧入口接收同一已构造Recorder，生产不再回取或重建完成实例。共用ApplicationBackgroundTasks，构造不查价、不启动工作。删除三项无消费者的旧准备投影API，渠道统计与完成日志出口只保留一份原生实现。
- 完成/计费定向race409条通过；真实PostgreSQL两条记录链各自验证同ID重放只扣款一次、Key累计一次且只保留一条用量事实，共3条含父子事件通过。首次新夹具漏写QuotaUpdates标记导致其Key累计断言失败，已补齐与真实生产捕获一致的输入，未改算法。
- standard/simple真实启动与SIGTERM两个场景通过，父子共3事件，停止次序保持。首次进程过滤器缺少“-sigterm”后缀只执行了父级准备，日志明确不计行为通过，随后以正确过滤器补验。当前unit完整lint与目标integration lint均退出0。新的interim-native-completion全仓合并验证正在执行；这些结果不替代旧包全部清零后的最终验收。

### 完成实例合并验证及定价测试所有权收尾（2026-09-23）

- interim-native-completion：Wire、普通全量11940条通过（4既有跳过）、unit全量19974条通过（8既有跳过），退出0。integration取得12940条通过、4既有跳过、1项失败；失败为分组复制回滚测试在业务断言前启动PostgreSQL容器超时，原断言串行补验通过。该首次失败保留，不记作整组通过；后续完整三组lint均退出0，见interim-native-completion-recovery-checks.json。
- 旧计算器、倍率及DeepSeek的原测试迁入billing，独立测试输入构造仍调用原生Calculator，纯函数直接使用pricing；208条定向race通过。删除14项已无消费者的测试转接，完整unit lint退出0。
- 渠道测试数据与可注入存储替身归routing/testkit，只提供输入，不复制渠道缓存、匹配或查价实现。原目录别名测试保留更新存储后InvalidateCache的断言。解析器、统一计算和目录别名三组原测试迁入billing；跨网关消费者保留原实际执行路径，共享无状态的billing/testkit构造器，删除重复构造和4个无人调用的区间函数转接。
- 首次编译暴露了两个可变渠道替身引用，以及混合测试对已迁私有构造器的反向引用；已完整改绑对应消费者。所有原失败日志保留，最终编译通过，定向race与门禁结果见native-resolver-tests-*。未扩大历史问题范围；剩余生产执行及HTTP装配仍按S16继续清理，不标记完成。

### 完成捕获与原生 Live HTTP 批次（2026-09-23）

- 定价/渠道整批最终为538条race通过，unit完整lint退出0。原断言名称、标签、共享测试数据及构造器位置见native-resolver-test-names.json和两个迁移映射；不会把测试构造器当作生产缓存实现。
- Messages/OpenAI/Cyber完成入参、主体/用量快照和请求ID捕获已归gateway/provider，生产提交点及测试直接使用原生接口；旧service捕获函数和三个入参结构删除。剩余执行账号仅在同步边界提供必要Record字段，不查询存储、不提前冻结；捕获后异步任务不持有原报文或凭据。
- 历史长上下文专用入口已无生产消费者，删除结构、方法及两层重复字段转接；原取消上下文资金断言改用真实统一完成入口，原固定金额及调用次数断言保留。Header请求ID、Key隔离、Cyber快照和强制请求ID原测试迁入所有者，新增捕获时刻、WS时刻和带类型nil能力标记的合同。614条unit race、真实PostgreSQL一次资金效果3条父子事件通过；unit完整lint退出0。
- Live HTTP由app直接构造，共用审核、资金准入和并发实例，旧Live Handler门面及工厂删除；app只投影原Live用例结果。Live/WS非报文Key映射、审核端点读取同步归原生HTTP，旧重复入口删除。原解析、权限、审核先于资金与错误形状测试迁入gateway/httpapi，并继续使用真实本地审核HTTP客户端。
- Live/路由/审核相关race364条通过，原生app构造与共享停止屏障合同另1条通过；Wire、编译及unit完整lint退出0。首次捕获迁移的重复import、遗漏私有测试引用和准确门禁许可均已修正，原日志保留，没有修改生产算法或扩大忽略。
- 本批同步网关生命周期与路由计费文档，保存native-completion-capture、native-live-http映射及日志。尚存的旧执行、账号形状与其他HTTP装配仍未清零；最终全量验收和17/17状态不得提前宣告。

### 定价、完成捕获和 Live 合并全仓检查（2026-09-23）

- interim-native-capture-live：Wire及普通/unit/integration三组全量命令均退出0，分别取得11945/19979/12946条通过事件；既有跳过分别4/8/4项，名单保留在checks.json，不计为行为通过。integration使用原定-p=4，无容器超时补验或断言调整。
- 首次普通lint发现两个新迁入的目录夹具符号仅由unit测试使用；给该辅助文件补齐unit标签后，普通/unit/integration三组lint均退出0。没有删除有效测试或新增忽略项，首次诊断与复核分别归档。
- 原计划正文摘要、SQL与S00—S15冻结资料无变化，索引为空，diff检查通过。此时剩余旧生产文件service242、handler43、repository8；跨包生产引用分别58、4、1个文件，逐符号清单见remaining-boundaries-after-live.json。下一批按会话标识与哈希能力继续清理；以上为阶段中间证据，不代替最后一次完整验收。

### 会话标识、摘要缓存与 Cyber HTTP 状态收尾（2026-09-23）

- 35项会话符号的52个调用文件按类型信息改绑；Header和认证分组判定归HTTP，OpenAI内容种子、Grok隔离种子与Gemini摘要格式归session，续接ID分类归protocol/openai，旧哈希上下文读写同批归requeststate。删除旧OpenAI哈希方法及客户端ID入口，Qoder直接调用已有通用请求哈希，保留观察日志及fallback命名空间。
- 原Header/优先级、内容稳定性、续接分类、Gemini摘要及会话连续性测试迁入所属模块；混合执行测试保留实际生产链。原benchmark及其唯一共享夹具同批移动，仅编译、未扩展benchmark执行。会话定向race538条、Qoder21条通过；integration全仓仅编译通过，真实Redis会话/归属/TTL竞争另11条父子事件通过，二者分别记载。
- 首次脚本遇到同名protocol目标文件，已改为独立kind文件并恢复原RemovePreviousResponseIDFromBody实现；重复import和benchmark夹具反向引用也已修正。import整理脚本误将两个重复字符串返回当作重复import删除，经语法树核对受影响文件确认仅两处，已恢复原选号quota_auto_pause与测试JSON回退返回；对应51条race通过。回归核对记录见session-import-dedupe-return-*，不是清单外历史修复。
- Messages→OpenAI摘要缓存由原生session.AnthropicPromptCache唯一实现，app构造并绑定同一实例；旧执行只投影账号/Key ID、摘要和既有TTL。原最长前缀、命名空间、旧链删除与到期时机保持，无新增清理任务。摘要/真实转发及装配定向race121条通过。
- Cyber标记复用moderationflow.Mark，HTTP拥有首个标记与turn清除，provider解析供应商事件，forward持有已透传哨兵；旧service文件及消费者全部改绑。相关实际WS/HTTP、摘要与装配race64条通过，完整unit lint退出0。源码、测试、门禁与文档同批更新，映射native-session-identity、native-anthropic-prompt-cache和native-cyber-marker保留全部位置及证据。

### 会话批次全仓检查与候选资格收敛（2026-09-23）

- interim-native-session-cyber的Wire、普通/unit/integration全量及三组lint均退出0。实际通过事件11948/19982/12949，既有跳过4/8/4；所有命令和名单独立保存，未使用跳过替代行为证据。
- 原账号上的协议准入、模型窗口、剩余时间与可调度性方法改为gateway/provider.ModelPolicy，选择、诊断和跨平台转发调用同一能力；目录与选择共用候选快照。删除model_rate_limit.go与antigravity_quota_scope.go实现及仅供旧测试的常量转接，原取时点、窗口差异和overages许可保持。
- 原窗口测试、Antigravity状态断言和完整协议保存矩阵随能力迁入provider；混合真实执行测试继续保留其生产入口。首次编译补齐了原Go隐式取地址调用的显式地址，未更改数据或断言。最终编译通过、544条定向race通过、完整unit lint退出0，见native-model-qualification-*。

### HTTP 心跳与输出边界完整收尾（2026-09-23）

- 图片JSON心跳、计时器和Writer包装完整归gateway/httpapi，生产错误展示、媒体尝试和handler都直接使用新入口，旧service实现删除。HTTP状态断言迁到所有者，实际OAuth响应与心跳后故障切换的两项测试继续运行原执行链；等待辅助函数改为读取生产包装器带锁的Written状态，没有增加只供测试访问的生产API。64条定向race通过，完整unit lint退出0。
- 普通流心跳字节登记、实际输出判断归HTTP，TTFT条件组合归provider。Compact与透传心跳的两组原测试及纯HTTP上下文/SSE夹具同批迁移；旧writer测试别名删除，原生CompactKeepaliveWriter收回为包私有类型。80条定向race通过，编译及完整unit lint退出0，原断言和平台取消语义保持。
- native-image-keepalive与native-output-boundaries记录源/目标、消费者和日志；同步网关生命周期文档。候选资格和本批输出边界将在后续稳定批次合并做全仓检查，最终验收仍待全部旧包清零后完整执行。

### 完成记录原生验证与旧资金装配删除（2026-09-23）

- interim-native-qualification-output的Wire、普通/unit/integration全量及三组lint均退出0，通过事件11948/19982/12949，既有跳过4/8/4。后续构造器删除仍须再做合并验证。
- 原完成记录工厂直接组合gateway/testkit.Recording中的原生Dependencies/Options，不再创建旧Gateway/OpenAI对象。十二个共享存储/额度替身同批迁入gateway/testkit；原断言迁入gateway/completion，真实HTTP和资金闭环继续由原生生产实例验证。旧RecordUsage、Cyber补记、历史金额计算方法及私有转接已删除，混合价格/市场测试直接调用同一Recorder计算。
- 迁移首次暴露两处峰值测试依赖旧service测试进程把time.Local设为UTC。将这两处固定时刻改为明确使用其峰值配置的本地时区，保留12:00处于11:59—12:01及原金额断言，没有修改生产时间策略。记录迁移race154条、计算交叉race528条通过；首次失败日志保留。
- 删除旧网关按需构造完成器的兜底代码，Getter只返回app绑定实例。HTTP测试补齐显式完成依赖，并复用原输入实例；最初五项WS用量等待失败及未检查到的完成任务panic均已清除，HTTP单包399条通过。最终完成/消费者race799条通过，真实PostgreSQL一次资金效果3条父子事件通过。
- 两个旧执行构造器各移除七项仅供旧完成装配使用的资金/身份/通知依赖，并删除计费测试时钟字段；app装配参数同批收敛。Wire识别已无消费者的provideUserRepository后删除其注册与函数，再按手写装配生成。Native记录器、资金存储及通知实例均保持唯一。
- 旧倍率代理方法、缓存字段、singleflight及重复构造清零。Forward/OpenAI两份原生缓存由app直接持有，原RuntimeLocalCaches的一分钟任务调用同一实例；未改变键、TTL、隔离或停止等待。原缓存/singleflight三组断言迁入billing，TTL断言迁入app；175条相关race通过，最终unit lint为0。
- 后续试删Gateway的usageLogRepo/deferredService被编译拒绝：窗口回源、调度阈值及Anthropic/传输观测仍需要它们。已完整恢复字段、构造输入及原测试值，114条相关race通过，没有保留删除或行为变化。native-gateway-unused-deps-restored.json单独说明，不能将原删项算作完成。
- 本批清单、消费者、构造器索引及验证见native-recording-*、native-completion-*、native-bound-completion-*与native-rate-*。仍有service233、handler43、repository8个旧生产文件；S16保持实施中，后续继续完成执行账号、平台与HTTP装配的剩余职责。

### 完成与倍率合并验证、账号配置读取收敛（2026-09-23）

- interim-native-recording-rates的Wire及普通/unit/integration全量均退出0，通过11948/19982/12949条事件，既有跳过4/8/4。首次普通lint发现四项仅供unit测试的旧辅助入口；将三个仍有unit消费者的夹具归到正确标签、删除已无生产消费者的完成账号转接后，三种lint均退出0，复核见interim-native-recording-rates-lint-rechecks.json。
- 账号请求头资格/配置直接调用原生Record，请求覆写唯一调用account/provider及egress。真实转发、账号用量查询与Grok查询同批改绑，原配置/非法Header/大小写/副本隔离测试迁入所有者；84条race通过，unit lint退出0。方法值使用原账号指针的延迟字段投影，保持配置在回调执行时读取，未扩大生产API供私有测试使用。
- 删除旧Account的八项协议地址方法，消费者显式使用已有ProtocolTarget；编译通过，协议/真实转发race330条通过。RPM、会话、队列及窗口的十一项旧方法也删除，消费者直接使用RuntimeConfig，GetCurrentWindowStartTime继续在原调用点传入time.Now；445条race通过。
- Codex图片桥接覆盖、显式工具策略归account，旧键、嵌套优先级、remove/drop兼容与显式false保持；指纹模式的资格判断归Record，读取Extra布尔值直接复用原生函数。原指纹模式矩阵迁入account，实际图片与指纹生产消费者继续验证，110条race通过。首次门禁中两处重复import与一个遗漏精确许可已修正，没有放宽目录规则。
- native-account-header/protocol/runtime/config清单记录消费者和验证；旧账号仍暂存当次AttemptRoute，尚未将其与全部执行输入分离，不能把这批方法删除等同于Account或S16完成。


### 账号能力与独立搜索整批收敛（2026-09-23）

- `interim-native-account-config-checks.json`：Wire、普通、unit、integration（`-p=4`）及三套完整 lint 全部退出零；实际测试事件分别为 11,948 / 19,982 / 12,949 通过，4 / 8 / 4 项既有跳过。这里只是合并批次检查，不替代 S16 最终验收。
- 账号模型映射、白名单、可配置型号、Grok 媒体资格和端点能力消费者同批改用原生规则；删除旧 Account 对应七个方法及三个 Grok 地址方法。拒绝诊断的隐式接口也改绑已有 `ModelRejectionAccount`。当前尝试映射仍通过 `ModelPolicy` 的独立 Route 处理；未把尝试状态放入持久记录。
- 相关 Qoder、映射快照、端点与 Grok 纯规则测试迁到 account/provider；原断言、标签与混合执行链测试保留。编译首轮定位的隐式接口遗漏已修复，定向 unit race 1,035 条通过、无失败或跳过；完整 unit lint 零。对应 `native-account-model-*` 与 `native-grok-account-address-mapping.json`。
- 独立 Web/X 搜索 HTTP、平台载荷、单次传输、固定依赖构造、完成捕获、原测试和门禁成批迁移。app 直接构造原生 SearchHandler，旧搜索 Handler 方法及测试包装删除；Grok 单次搜索交换退出旧 GatewayService，共享原 HTTP 池，保留凭据/URL读取、Header、4 MiB 读取边界、错误分类和关闭语义。原生搜索 ProviderSet 单独分组，选号暂通过已登记的旧选择器窄端口投影。
- 原生 HTTP 契约新增同查询的独立结算 ID、每次单一资金/记录效果和完成快照后释放断言；首次新增夹具的分组倍率缺省为零，补齐为明确倍率 1，未修改生产金额行为。初次编译遗漏、测试结果和修复后日志分别保留。搜索传输及整组定向验证继续记入 `native-search-*`。
- 文档同步实际账号与搜索所有者；仍有旧文本/媒体执行、账号实体与存储投影、装配及最终门禁未收尾，roadmap 保持 16/17。

- 搜索整批最终定向 unit race 为 293 条通过、无失败或跳过，完整 unit lint 退出零（`native-search-execution-race-recheck.jsonl`、`native-search-execution-lint-recheck.log`）。其中新增实际 HTTP/完成链断言和传输契约已执行；`native-account-search-verification-summary.json` 同时保留初次构建遗漏及新增夹具倍率问题，未把失败计为通过。正在执行账号模型与搜索合并全量检查（`interim-native-search-model-checks.json`）。


### 执行凭据完整能力批次与环境补验（2026-09-23）

- 账号模型/搜索合并普通、unit 全量分别为 11,962 / 19,996 条通过，4 / 8 项既有跳过。integration 全量初次在 5 个容器启动场景遇到 Docker socket deadline（含父测试 6 个失败事件）；失败发生于业务断言前，记录于 `interim-native-search-model-environment-failures.json`。Docker 可用后，原 5 个场景补验 8 条通过、无跳过，三套完整 lint 全部退出零（`interim-native-search-model-followup-checks.json`）。不把该初次全量记为通过，后续合并与最终验收仍须完整 integration。
- Messages 凭据选择迁入 account.MessageCredentialSource；app 复用同一 Claude/Vertex 源。旧 GatewayService.GetAccessToken/getOAuthToken 及原构造器 Claude 字段退出，消费者直接使用原生输入；Grok 存量 token 的纯读取也归 account。编译、Wire、549 条定向 unit race 与完整 unit lint 通过。
- OpenAI/Grok 凭据选择、影子读透及 setup-token 边界迁入 account.OpenAIExecutionCredentials。旧 OpenAIGatewayService.GetAccessToken 删除；app 绑定原 PostgreSQL 父账号读取和两个 token 源，并在构造完成、开放请求前接入原运行阻断回调。没有第二份缓存、刷新器或查询。
- 原独立影子/setup-token 测试迁到 account；HTTP 和执行链的存储/token 替身显式绑定原生凭据源，保留原实例及业务断言，不通过生产回退工厂恢复旧图。OpenAI 批次编译、Wire、1,044 条定向 unit race 和完整 unit lint 全部通过；初次遗漏 app 合同测试新增参数的编译结果单独保留。
- `native-message-credentials-*`、`native-openai-credentials-*` 保留消费者、测试映射、命令和日志。Anthropic/OpenAI 上游文档同步实际凭据所有者。继续收敛旧执行账号、文本/媒体 HTTP、存储与装配，最终验收尚未完成。


### 原生执行账号与旧 repository 清零（2026-09-23）

- 凭据合并完整验证 `interim-native-execution-credentials-checks.json`：普通 11,977、unit 20,011、integration 12,977 条通过，既有跳过 4/8/5（integration 另含 TLS profile 环境跳过）；Wire 及三套 lint 均退出零。前一合并批次的 Docker 容器启动失败未更改代码、断言或超时，后续完整 integration 已取得通过结果。
- 先按 Go 类型核对字段、方法、关联和序列化消费者，再整体替换旧 Account/AccountGroup。删除旧实体、逐方法包装及逐字段递归往返转换；ExecutionAccount 只组合原生 Record 与独立 AttemptRoute，没有复制账号规则、字段定义或缓存。所有旧方法消费者直接读取 Record，原生账号 CloneRecord/CopyRecordInto 是唯一账号图复制实现。
- 保留按值复制后替换字段、nil 接收者、已绑定方法在应用新记录后读取最新值、自引用关联、nil/空集合与原复制时点；日期读取仍由原显式时钟/位置函数提供。执行目标不参与持久化或公开 JSON，调度完整/轻量编码仍归原 Redis Codec。业务配置、凭据与请求路线未合并成通用共享实体。
- `execution-account-preview-*`、`execution-account-applied.json`、`execution-account-owner-mapping.json` 记录消费者和所有者。批次普通/unit 编译通过，integration 全包编译通过（仅编译单列），定向 unit race 1,665 条、真实 Redis/PostgreSQL integration race 180 条通过，无失败或跳过；unit lint 零，Linux 服务构建通过。首轮 AST 新 import 插入导致单文件重复 Record 访问的编译问题已修正，初次日志保留。
- 旧 repository 的八个文件删除。执行存储契约由 gateway/provider 持有，app 的读/写适配仅投影并调用已有 account/postgres、billing/postgres 实例；原可选 CAS、BulkUpdate、资金累计和配置接口全部保留，未把事务、锁、字段保护、outbox 或缓存搬入 app。旧 service.AccountRepository 及仅为测试存在的生产 CN 接口删除，CN 替身在测试文件声明其需要的能力。
- 新增真实组合根存储合同，验证原生实例、请求副本隔离、配置保留消费字段及外层 Ent 事务写入/回滚；与原健康、完成、窗口及 outbox 合同共 10 条 integration race 通过。新增夹具最初遗漏必要装配参数，随后误用普通读取接口验证未提交值；已分别改为真实生产装配和事务拥有者读取，保留资金/回滚断言，未改变普通 GetByID 的生产语义。失败与最终结果分别保存在 `execution-store-integration-race*.jsonl`。
- `execution-store-owner-mapping.json` 保留旧文件归宿。旧包实际 Go import 清零，相关测试命令注释和 Project Doc 已同步；三套合并全量与剩余文本/媒体执行清理继续进行，S16 尚未完成，不更新 17/17。

- 实体/存储合并普通全量 11,980 条通过、4 项既有跳过；unit 全量定位到一个本次类型迁移的比较问题：原 Account 没有时钟函数，新 Record 的函数依赖不能作为账号业务字段直接深比较。原 Kimi 持久化失败后的运行阻断断言保留完整业务字段和路线比较，只剔除新增的函数依赖；未改运行逻辑或历史业务断言。初次全量与定向失败保留，后续结果写入 `execution-account-clock-comparison-*` 和 `interim-native-execution-target-store-recheck-*`。


### Codex 身份与指纹能力收敛（2026-09-23）

- 实体/存储合并复核完成：unit 20,014、integration 12,982 条通过，既有跳过 8/4；三套完整 lint 退出零（`interim-native-execution-target-store-recheck-checks.json`）。普通集合此前 11,980 条通过；仅 unit 夹具的技术依赖比较调整后未重复无关普通集合。
- Codex 命名空间、指纹模式与请求头配置组合进入 account/provider；纯 UUID/会话派生测试进入 upstream/openai。HTTP Adapter 分别发布显式身份与指纹状态，沿用 Gin 同步读写，不引入可变共享状态盒；保留原覆盖时点、nil 覆盖、影子继承、跨账号拒绝和 Header/body 共用 IDs。
- 身份来源仍按原时点解析母账号，保留原目标记录引用，未提前冻结命名空间或增加查询。受控凭据读取与 ChatGPT Header 组合进入 gateway/provider，影子资格算法仍由 account 唯一实现。四个旧 service 文件删除；原生配置/状态/原语测试迁至实际所有者，实际 HTTP/WS 请求构造测试继续验证真实调用链。
- 编译通过，定向 unit race 723 条通过、1 项原有 WS 分支跳过，unit lint 退出零；`native-codex-identity-*` 记录消费者、测试归宿和结果。继续验证本批门禁与 Agent Identity 装配，未增加历史问题修复，S16 仍实施中。


### Agent Identity 运行能力与门禁（2026-09-23）

- 执行账号/原生存储/Codex 边界的 12 类可丢弃夹具分别覆盖普通、unit、integration，36/36 合同通过。包含精确旧执行许可、同目录新文件拒绝、迁出后许可失效、非法子包、核心反向引用、已删除 repository 拒绝和具体平台互引拒绝；夹具已删除，证据 `execution-codex-boundary-gates.json`。八类 go list 集合（普通/unit/integration/wireinject/embed/e2e/Darwin/Linux）无包错误或旧 repository 引用，仅作为选择/可构建性证据。
- Agent Identity 的旧密钥结构和签名/解密包装删除，原语与协议测试直接使用 upstream/openai，注册客户端复用 account/provider。原定向 20 条 race 通过；初始源和测试映射记录在 `native-agent-key-*`。
- app 显式构造唯一 OpenAITaskCoordinator，Wire 将同一变量交给请求执行、额度查询、用量查询和账号探测；删除生产 SharedOpenAITaskCoordinator 及可变注册 URL 全局入口。各入口未持久账号互斥保留，未增加跨进程协调、取消承诺或注册重试。
- 注册、签名请求头、任务恢复、凭据写回及脱敏的执行适配归 gateway/provider.ExecutionAgentIdentity；旧 openai_agent_identity/account_credentials_persistence 实现文件删除。适配只投影原调用与记录赋值，账号协调器仍拥有锁和复查，原生存储仍拥有持久化。恢复标记与错误分别归 requeststate/forward，WS 错误解释归 upstream/openai。
- 原注册协议、持久化与共享锁测试迁到实际所有者；实际 HTTP/WS/Live 调用继续回归。运行批次 372 条定向 unit race 通过，迁移测试后的对应集合 282 条通过（命令集合不同，不相加），完整 unit lint 退出零；退役测试替身已删除。消费者、装配及映射见 `native-agent-runtime-*`，唯一协调器的 Wire 证据见 `native-agent-coordinator-wire-ownership.json`。
- 继续执行合并完整检查及剩余 HTTP/执行适配清理，不将当前进展等同于 S16 完成。


### 调度纯策略入口与合并验证补记（2026-09-23）

- Agent Identity 合并普通/unit/integration 分别为 11,980 / 20,014 / 12,982 条通过，既有跳过 4/8/4。普通 lint 定位到仅供 unit 使用的影子账号替身尚参与普通构建，已按真实消费者补齐 unit 标签；没有移动或跳过实际测试。三套完整 lint 最终均退出零（`interim-native-agent-identity-lint-final-checks.json`）。
- 调度分组覆盖、权重校验、全局权重覆盖和粘性参数归一化直接使用 scheduler/policy，删除五个旧转接；配置权重有效性使用相同字段与求和顺序的原生 ValidGlobal。原运行参数中的额外标志在该纯解析器中不参与计算，删除值拷贝未改变其解释。
- 原六组分组/全局覆盖、显式零值、溢出及指针复制测试迁入 scheduler/policy；编译、54 条定向 unit race 和完整 unit lint 通过。映射与日志为 `native-scheduler-policy-*`。继续收敛调度共享状态、剩余平台执行和 HTTP 适配；S16 未完成。


### 调度共享状态从健康适配退出（2026-09-23）

- RateLimitService 不再拥有或构造调度反馈和设置缓存；原五参数构造器、反馈/缓存取回接口与无消费者的粘性开关转接删除。app 将同一个 Feedback/Settings 直接绑定 Messages、OpenAI、Gemini 和诊断。
- Messages/Gemini 构造器不再先创建未使用的反馈实例；现有独立执行测试保留本对象的惰性原生缓存，生产在开放请求前完成明确绑定。诊断也使用同一参数缓存与反馈，未修改评分、Top-K、EWMA、组覆盖、缺省观测或查询顺序。
- 原装配合同改为通过真实调度上报观察同一反馈；新增同包绑定合同检查三个执行器和诊断的缓存/反馈身份。原调度测试显式注入原生反馈，业务断言保持。编译与 Wire 通过，定向 unit race 373 条通过，unit lint 最终为零；初次接口参数遗漏与退役方法诊断保留。
- 证据为 `native-scheduler-state-*`。设置仓储和配置解释的剩余旧投影、HTTP 及平台执行仍需收尾，当前状态仍为 16/17、S16 实施中。


### 组合根 Wire 分组与稳定性（2026-09-23）

- 调度状态合并完整验证为普通 11,981、unit 20,015、integration 12,983 条通过，既有跳过 4/8/4；Wire 与三套完整 lint 全部退出零（`interim-native-scheduler-state-checks.json`）。
- 按 Go 类型的真实返回值、接口绑定与现有集合定义清点 361 个 Wire 登记项，整理为 25 个模块装配集合；只调整手写 wireinject 结构，不移动实现、重建资源或把剩余旧图视为已迁完。原有集合仍只引用一次。
- 首次生成提示六个跨模块 Bind 必须与具体提供者同处集合，按 Wire 实际约束调整归属后生成成功，原失败保留。初始化函数逐字节规范化比较完全相同，构造顺序不变；生成文件仅移除原先携带的两个 Wire-only 集合声明及无用 import。再次生成完全无差异。
- 编译与 wireinject lint 通过。清单、生成差异解释及稳定性证据为 `wire-provider-inventory.json`、`wire-module-groups.json`、`wire-regroup-initializer-comparison.json`、`wire-regroup-repeat.json` 和 `wire-regroup-*.log`。25 个新文件沿用 app 角色限制，必要旧 Handler import 仅许可实际装配文件，并保留 S16 退出标记。
- 继续按剩余执行函数的真实依赖成批收尾；旧 service/handler 尚未清零，最终全项验收和 17/17 标记仍待完成。


### 模型决策及响应读取整批清理（2026-09-23）

- OpenAI 普通/Compact/计费与上游模型解析、错误调度型号、Bedrock 路由与 Lite 资格消费者直接使用原生 ModelPolicy/平台原语；删除四个旧纯包装文件并清理混合文件中的对应符号。保留账号映射取值、Passthrough 与 Compact 优先级、区域输入及错误回退。
- 纯模型决策、Bedrock ID/区域与来源表、价格候选保留及 Lite 测试分别迁至 gateway/provider、modelidentity 和 upstream/bedrock；实际 HTTP/WS/调度测试继续回归。编译、534 条定向 unit race 和完整 unit lint 通过，证据为 `native-model-decisions-*`。初次工具遗漏裸类型与函数值消费者的编译错误及修正日志保留。
- 上游响应有界读取与共享超限错误归 infra/httpclient；Ops 与原两种 502 envelope 归 gateway/httpapi。读取上限、nil/I/O 错误、onTooLarge 时点和关闭责任保持，流读取错误分类直接注入同一原生错误。配置默认值未改，也未为常量引入额外配置依赖；剩余旧执行器仅保留配置标量投影，待其 Options 收尾时删除。
- 原读取合同迁到实际所有者，并验证两种错误 envelope；编译、132 条定向 unit race 与完整 unit lint 通过，证据为 `native-response-limit-*`。继续串行运行这两批合并全量检查，未扩大历史问题修复范围。


### 调度参数与评分类型整批收尾（2026-09-23）

- 模型/响应合并普通与 unit 全量分别为 11,984 / 20,018 条通过，4/8 项既有跳过；integration 初次 12,985 条通过、4 项跳过，一项容器启动在业务断言前遇到 Docker socket deadline。未更改该测试、生产逻辑或超时，原场景补验通过；三套完整 lint 为零，详见 `interim-native-model-response-followup-checks.json`。初次 integration 不记作全量通过。
- scheduler.Parameters 由 app 投影静态参数并绑定原生设置仓储及唯一 SettingsRuntime；Messages、OpenAI、Gemini 和只读诊断直接共享它，删除临时 OpenAI 服务、健康适配设置回取及惰性设置构造。最终分组读取顺序、动态读取时点、五秒缓存、全局/分组覆盖和明确零值保持。
- 原参数覆盖与溢出断言迁到 scheduler，实际平台和装配替身显式绑定参数；原生参数及消费者定向 unit race 405 条通过，无失败或跳过。初次编译遗漏生成参数与测试入口、测试搬迁的常量拼写问题已修正，失败日志单列。
- Gemini 评分直接使用无凭据 ScoreAccount/ScoreInput 和原核心；删除旧评分实体、往返转换、抽样别名及生产中仅供测试使用的分数转换。原纯评分/Top-K/随机测试及已有 benchmark 随唯一算法迁移；实际 fresh/DB、配额和计费隔离合同仍验证原平台链，不改变业务断言、不运行新增 benchmark。测试映射见 `native-score-test-mapping.json`。
- 参数与评分的定向结果保存在 `native-scheduler-parameters-score-verification.json`；继续完整能力清理与合并检查，S16 及最终验收尚未完成。

- 参数/评分合并检查的 Wire、普通、unit、integration 和三套 lint 均退出零，但逐测试标签对照发现本次迁移给原普通参数测试误加了 unit 标签，导致普通/integration 各少执行五个父子事件。已恢复原无标签构建条件，并补跑原生普通与 integration 合同；不将该标签遗漏算作通过验收，也不削弱任何原断言。


### 健康观测与文本 HTTP 固定绑定（2026-09-23）

- 参数/评分合并检查最终 Wire、三套测试与三套 lint 均退出零；恢复无标签的原参数测试后，普通与 integration 各补验五条原断言事件。原先被误加的 unit 标签已删除，后续完整检查仍包括这些场景。
- 健康模型进入请求 ExecutionHints 的独立值，thinking 保留三态；平台观测在 gateway/provider 固化原始模型、规范模型及端点。通用错误生产调用直接进入账号原生观测，执行账号仍取得独立记录，返回后仅回写原有 Credentials/Extra，未扩大字段回写。原临时规则结果直接使用 account 类型；旧请求键、四个聚合错误入口与错误策略投影文件删除。
- 健康批次编译、954 条定向 unit race 和完整 unit lint 通过；初次漏改 OpenAI fastpath 匹配调用与被丢弃 bool 表达式、精确门禁和重复 import 的诊断及修正均保留。证据 `native-health-observation-*`、`native-health-inputs-*`。
- 选择结果归 gateway/provider.SelectionResult，Gemini 会话选项归 forward；所有生产/测试/装配消费者成批改绑，旧定义和构造转接删除。选择、Lease、反馈快照、nil 选项及按顺序覆盖保持；699 条定向 unit race 通过，映射为 `native-selection-contract-consumers.json`。
- Messages、通用 Responses/Chat 与 Gemini 原生 HTTP 的绑定实现退出旧 handler；app 直接构造三条原生 HTTP 路径，共享同一原生业务端口与无状态等待 helper。旧测试构造器仅委托，实际执行器构造仍单独登记待清零，未将其标成已经删除。规则与副作用不搬到 app。
- HTTP 批次 Wire、编译、484 条定向 unit race 与完整 unit lint 通过；初次编译暴露共享旧 backend 的两个入口依赖后，已同批迁完其 HTTP 绑定。路由、前置顺序、会话、审核与流式断言保留；证据 `native-text-http-*`、`native-messages-http-*`。继续合并完整检查与执行器/健康装配清理，roadmap 仍为 16/17。

- 健康观测/选择契约/文本 HTTP 合并完整检查已结束：Wire、普通 11,986、unit 20,020、integration（-p=4）12,988 条通过，既有跳过 4/8/4，三套未截断 lint 均退出零。完整命令、事件与跳过位于 `interim-native-health-text-checks.json`；这些是稳定批次检查，不能代替旧包全部清零后的最终 S16 验收。


### 文本选项、终止输出与用户授权检查点（2026-09-23）

- 新参数、健康和 HTTP 边界的 11 类可丢弃夹具在普通/unit/integration 下全部符合预期，共 33/33，通过后已删除夹具；证据为 `native-health-text-boundary-gates.json`。
- Thinking 的平台型号选择进入 gateway/provider，报文字节修复仍由 requeststate/protocol 唯一实现；混合原测试按平台、协议和解析 benchmark 的实际职责拆分，标签及断言保留。整数解析助手退出 unit 生产集合，作为原 benchmark 的测试资源保留。强制缓存计费标记移入 requeststate 的私有尝试键，原 false/错误类型/父 context 不变断言仍执行；令牌转换原矩阵直接调用实际响应转换函数，不保留测试内的加法副本。
- 文本选项和状态编译、559 条定向 unit race、完整 unit lint 通过；初次遗漏函数值消费者和测试私有助手的编译结果独立保留。旧函数、公开 context key 与四个旧文件退出，映射见 `native-thinking-*`、`native-cache-billing-consumers.json`。
- Anthropic/Messages、Responses、Chat 与 Gemini 的终止错误适配归 gateway/httpapi.MessagesErrorOutput；旧 Handler 的错误方法删除，固定依赖、直接消费者与原 fallback 测试同批迁移。保留前导/真实输出区别、凭据安全提示、Retry-After、规则匹配及 Ops 标记顺序。编译、741 条定向 unit race 和完整 unit lint 通过；初次值接收者/测试空壳构造的编译修正记录在 `native-text-error-*`。
- 用户要求后续完整能力批次验证后提交进度，本次据此制作 S16 阶段检查点；该要求覆盖原计划的“不自动提交”约定，不自动推送。当前构建及原生包普通补验通过，详细事件在 `progress-20260923-verification.json`。S16 尚未完成，保留 16/17；不提交 AGENTS.md、SYNC.md、其他任务计划或 diagnostics。


### 通用文本尝试运行时与第二检查点（2026-09-23）

- `gateway/httpapi/textattempt.Runtime` 接管 Messages、通用 Responses/Chat、Gemini 的六组单次尝试适配及固定依赖。入口、平台调用和资源按 Forward/Selection 与原生资源分组注入；运行时无旧 GatewayHandler 字段，也无每请求回退构造。app 直接绑定同一运行时，Wire 已不构造旧 GatewayHandler；旧类型暂仅由历史测试夹具消费，后续清零。
- HTTP 输出统一调用 MessagesErrorOutput；完成仍使用唯一 Recorder/Submission，资金、调度、并发及平台算法未复制。请求 Open 继续取得独立状态，平台取消与完成资格、序列锁、会话和每 attempt 解析顺序保持。共享的选择快照投影归 gateway/provider，供 OpenAI 与通用文本复用。
- 消息队列模式值归 scheduler/policy；配置字符串、默认模式和等待预算不变。原固定状态隔离与平台资格测试跟随实现，跨入口字段替身、Wire 和门禁同批改绑；新测试包在 TestMain 只初始化一次 Gin 模式。
- 编译、899 条定向 unit race 通过；Wire 再生成摘要一致。普通/unit 全量分别为 11,986 / 20,020 条通过，既有跳过 4/8。integration 本次为 12,980 条通过、5 项跳过、7 个失败事件：六个场景均在容器启动读取 Docker socket 时超时，尚未进入业务断言，另一个事件为父测试失败；业务日完整小时不足的原跳过单列。
- Docker 可用后保持代码、断言和超时不变，原场景按包串行补验 8 条通过，无失败或跳过；构建和三套完整 lint 均退出零。初次全量 integration 仍登记失败，不以补验冒充整组通过。记录见 `interim-native-message-runtime-checks.json` 和 `native-message-runtime-followup-checks.json`。
- 原生运行时/精确 app 绑定/同目录新文件/迁出许可/旧包、config、存储反向依赖的 10 类夹具覆盖三种标签，30/30 符合预期，夹具已删除。格式整理后的构建及实际路由/固定状态补验 10 条通过；693 路由合同保持。完整索引为 `progress-20260923-textattempt-verification.json`。
- 本批按用户要求验证后提交检查点，不推送，不修改 16/17 状态；S16 剩余旧测试构造、OpenAI/WS/媒体 HTTP 及平台执行适配仍需收尾，最终验收尚未完成。


### 旧通用 GatewayHandler 及构造入口清零（2026-09-23）

- 余下取消、会话、渠道、图片模型与输入校验测试改为直接调用原生 HTTP 处理函数。夹具仅持有函数句柄并接入被验证的真实单次能力，不复制旧 Handler/Service 实现、缓存或规则；应用路由夹具直接调用实际 app 原生构造函数。订阅展示的两条原断言迁入 billing，删除仅服务于旧类型的测试委托。
- 删除 GatewayHandler 类型及 11 个旧构造/转接文件，并从共用文件删除其完成和会话方法。生产、测试与生成代码的 `GatewayHandler` / `NewGatewayHandler` 精确类型引用清零；剩余 OpenAIGatewayHandler 不在此项中混算。Gemini 文档锚点移动到真实原生入口。
- 首轮新夹具误把原 typed-nil 提示词端口替换为 nil interface，渠道图片拒绝合同因此 panic；恢复原 typed-nil 后，原业务断言保持，297 条定向 unit race 与 1,480 条相关普通测试通过。初次失败与修正结果分别保留，没有修复清单外历史问题。
- Wire、构建及普通/unit/integration 三套完整 lint 均退出零；Wire 生成无差异，SQL/Ent 未改。旧精确文件许可随入口删除，新增及已有文件差异检查通过。本批提交检查点，不标记 S16 完成；索引 `progress-20260923-generic-retirement-verification.json`。


### OpenAI 入站亲缘与执行提示收敛（2026-09-23）

- Codex 审查模型、Header/metadata 声明冲突与父线程解析归 clientmeta；HTTP 保留原模型短路并复用 scheduler 散列，requeststate 保存请求内的当前/旧散列及父绑定保护。普通模型、缺失或冲突线索继续忽略；原当前分组粘性读取时点与兼容键不变。旧选择器仅剩原缓存读取适配，没有新增账号查询或缓存实例。
- HTTP 透传标记的写入/读取成对迁入 requeststate，分组 Responses 图片许可直接使用 routing 的唯一规则。原 HTTP 线索合同迁到实际所有者，真实选择与父绑定保护合同继续验证生产选择器。
- 编译、516 条定向 unit race 通过，1 项原 WS 分支跳过单列；普通合同 37 条通过，构建和三套完整 lint 退出零。原计划、SQL/Ent 及其他任务文件未变；精确门禁及文档同批更新，详见 `native-openai-hints-*`。本批作为进度提交，OpenAI HTTP 资源/输出与剩余旧执行适配继续收尾。


### OpenAI 共用错误输出归属（2026-09-23）

- HTTP 的 OpenAIErrorOutput 唯一拥有普通 JSON、SSE 终止、Anthropic 兼容错误及已写响应兜底。文本 Handler 继续传入原 backend 观测端口；旧文本/WS/媒体调用直接使用同一默认输出器，不再为写错误重建 Handler。保留 compact/image 心跳停止后的 Writer 判断、仅保活与真实输出区别、错误转义、Cyber 原消息和 SLA 观测差异。
- 六个旧 Handler 错误方法及旧“已告知”/日志分类入口删除，函数值消费者同批改绑；11 组原输出断言迁到实际 HTTP 所有者，标签与断言保持。未为测试扩大旧入口，也没有复制第二份算法。
- 编译、573 条定向 unit race、263 条相关普通合同及构建通过；普通/unit/integration 三套完整 lint 为零。测试迁移中的空旧构造和重复 import 已清理，初次诊断保留；测试与消费者映射见 `native-openai-error-output-*`。错误策略文档已同步，SQL/Ent 与冻结资料未变。本批按授权提交进度，S16 仍实施中。


### OpenAI HTTP 准入与唯一资源（2026-09-23）

- 用户槽与图片槽 HTTP 适配归 OpenAIHTTPResources，依赖缺失检查归 OpenAIDependencies，工具输出输入验证直接调用原生 HTTP 函数。五个旧 Handler 准入/验证方法删除，生产、函数值消费者及测试同批改绑。保持依赖缺失项目顺序、未提交时的 503、工具错误优先级、等待前 context 的取消释放、图片拒绝/等待/旁路和原响应。
- app 构造唯一并发 helper 与本地图片 limiter，Wire 显式交给尚未清零的 OpenAI Handler；构造器收到资源时不再创建备用实例。原生与兼容入口的容量合同验证同一槽位状态、阻塞与重复释放；剩余旧入口仅投影现有指针和配置，不复制 limiter 状态。
- 原图片槽 HTTP 合同迁到实际所有者，新增资源共享合同通过。普通合同 48 条、定向 unit race 137 条通过，构建与三套完整 lint 为零；Wire 再生成摘要一致。源码/测试/门禁及文档已同步，SQL/Ent 未变，证据为 `native-openai-controls-*`。按授权保存进度，S16 的直接 OpenAI HTTP/Cyber 绑定及其他残留继续收尾。


### Cyber 会话与 HTTP 固定装配完整批次（2026-09-23）

- 转录前缀与显式/scope 键、旧单键缓存兼容、开关/TTL 读取、写入与查找统一进入 gateway/session。核心没有 Gin/config 或日志后端依赖，HTTP 仅提供同步惰性的显式会话读取。保持原哈希前缀、API Key 隔离、scope 前置、256 候选上限、溢出阻断、错误 fail-open 与动态读取时点。
- app 直接构造唯一 CyberBlocks 和 CyberHandler，复用同一 GatewayCache、moderation 设置、ApplicationBackgroundTasks、Recorder 与 Ops 队列；旧 Handler backend 删除，旧服务仅保留实例绑定/无状态测试投影，不保留规则或缓存。真实请求的记录、异步提交与关闭拥有者不变。
- 原会话测试迁到实际 HTTP/会话链，真实选择调用继续回归；48 条定向 unit race 通过，真实 Redis 组合根合同 1 条 integration race 通过，分别保留内存替身与真实存储证据。初次 app 测试构造参数遗漏已补齐，没有削弱断言或修复范围外问题。
- 合并全仓普通/unit/integration 为 11,987 / 20,021 / 12,990 条通过，既有跳过 4/8/4，三套完整 lint 均为零；Wire 再生成稳定。9 类门禁在三种标签下共 27/27 通过，夹具已删除。SQL/Ent、原计划及冻结资料未变，验证见 `interim-native-cyber-checks.json` 与 `progress-20260923-cyber-verification.json`。保存本批进度，S16 仍未完成。


### OpenAI 文本 HTTP 固定绑定与图片意图（2026-09-23）

- Responses、Chat、Messages 的 HTTP backend 直接由 app 绑定原生资源、审核、Cyber、资金、路由及响应归属端口，生产不再通过旧 Handler 构造 HTTP 门面。选择/转发单次执行器仍是单独登记的过渡端口，本批不把它算作已清零；同一资源与完成器未复制。
- 图片意图的 true/false/未知、WS 旁路和每 attempt 提示进入 HTTP 所有者；渠道映射、报文替换与判断使用唯一组合函数。保留 Compact 重新判断及调用次数，实际 Forward 测试仍验证生产消费者。原入口合同及图片提示测试同批迁移，原无标签选择保持，删除三处无消费者包装。
- 编译、Wire 和构建通过；普通定向 674 条、unit race 1,132 条通过，各有一项既有 WS 跳过；真实组合根及路由读取/关闭边界追加 race 42 条通过。三套完整 lint 最终为零。初次测试私有 backend 搬迁遗漏、重复测试助手、旧包装 unused 和精确测试许可诊断分别保留，没有削弱原断言。
- Wire 再生成摘要相同；其构造顺序变化仅将已有 Cyber/资源取得提前到文本 HTTP 装配，没有提前 Start。原计划、SQL/Ent、前阶段资料及 51 个受保护文件未变，文档同步实际新旧共存结构。本批验证索引为 `progress-20260923-openai-text-verification.json`；S16 仍实施中、16/17。


### OpenAI 文本单次运行时与共享尝试适配（2026-09-23）

- `gateway/httpapi/openaiattempt.Runtime` 接管 Responses、Chat、Messages 四组固定执行与请求状态；app 直接绑定平台单步能力、同一选择反馈、完成器、原生槽位与 Cyber。新运行时没有旧 Handler/service/config 字段或 import，文本生产链不再经过旧 Handler；账号切换与预算仍由 gateway/text 唯一管理。
- 跨模式 reasoning、账号模型/端点观测、代理安全字段、等待槽位、失败输出及 Cyber 完成快照保持一份实现。剩余 WS/媒体直接调用原生 Support；删除 13 个旧方法及无消费者的槽位枚举/包装，不保留第二份规则。原分组解析诊断、定价时刻、取消、终态与部分结果资格保持。
- 原 cross-mode、代理日志、前导/取消与诊断合同和替身跟随实际所有者；模型报文缓存与调度结果测试分别归 requeststate/forward。无标签与 unit 标签逐项一致，断言映射见 `native-openai-attempt-test-mapping.json`。编译、Wire、构建通过，定向普通 2,131 条、unit race 2,823 条通过，各有两项原供应商/WS 跳过；原生拥有者追加 race 28 条通过。
- 11 类依赖夹具在普通/unit/integration 下 33/33 符合预期，已删除；临时旧构造只许可其实际原生 import，迁出文件及同目录新文件不继承许可。Wire 再生成稳定，SQL/Ent、原计划正文、前阶段资料及其他任务文件核对未变。初次 unused 诊断保留，对应旧方法已在消费者清零后删除。
- 正在执行这两批的合并完整验证；全项完成后在此追加真实结果，再提交进度。S16 仍为实施中、16/17，WS/媒体和旧平台执行适配及最终验收尚未清零。

- 合并全仓检查最终普通/unit/integration 分别为 11,994 / 20,028 / 12,997 条通过，既有跳过 4/8/4，三套完整 lint 均为零。删除旧委托后再次构建并补跑定向 race 903 条通过，跳过 0 项；完整命令与实际事件见 `progress-20260923-openai-attempt-verification.json`。本批按授权提交进度，不更新为 S16 完成。


### WS 入站、每轮目标与完成投影整批迁移（2026-09-23）

- WS 升级 backend、EntryPorts、账号执行目标、每 turn hooks 与日志适配进入 `gateway/httpapi/wsentry`。app 直接构造 WS Handler，文本与 WS 共用 Wire 唯一的 openaiattempt.Bindings/Support、并发资源、完成器与 Cyber；旧 Handler 不再参与 WS 生产装配。单次供应商交换及配置默认值的剩余旧端口仍逐文件登记，未把整个旧平台图搬进新模块。
- HTTP/WS 原结果投影归 gateway/provider，四个旧结果函数及原文件删除。保留所有已观测字段、Header、turn-state、replay、nil、终态和完成时刻；原始 Key 读取、动态 Fast 策略、连接/turn 租约、先后次序和双向 relay 不变。旧入站构造仅委托，三处无消费者辅助函数及一个旧常量清零。
- 编译、Wire 与构建通过；定向普通 644 条、unit race 818 条通过，各有一项既有 WS 跳过。请求投影副本/延迟刷新和真实组合根的升级、依赖、关闭拒绝合同同批执行；并发拥有者补充 unit race 10 条，真实 Redis 连接租约及会话归属 integration race 10 条通过，无失败或跳过。
- 三套完整 lint 最终为零；初次仅报退役常量 unused，删除后补验，未增加忽略。12 类边界夹具在三套标签下 36/36 通过，夹具已删除；Wire 再生成稳定，生产只构造一份共享绑定。SQL/Ent、冻结正文/资料及其他任务内容未变，文档同步实际结构。
- 结果索引 `progress-20260923-ws-entry-verification.json`。本批按授权保存提交，下一完整能力为媒体/辅助入口；前一稳定批次的完整全仓 11,994 / 20,028 / 12,997 结果保留，后续稳定批次再合并全仓检查，最终 S16 验收仍需完整执行。当前保持 16/17、实施中。


### 媒体/辅助入口完整能力与旧 Handler 生产图退出（2026-09-24）

- 图片、Grok 视频/音频、Embeddings 和 Alpha Search 的请求适配、完整完成输入及 HTTP 绑定进入 `gateway/httpapi/mediaentry`，循环与任务认领仍由 gateway/media 唯一持有。app 直接构造唯一 Runtime，并复用文本/WS 的选择反馈、槽位、完成器与原生资源；视频 getter 保留原读取时点及同一缓存。任务状态机、价格和金额算法未扩展。
- Grok Codec 的原纯规则投影归 gateway/provider，四个旧函数及全部直接消费者同批改绑；资格门禁调用账号既有规则，仅在未观测时调用原探测端口。配额能力的原赋值、图片 mandatory、搜索/音频提交策略、视频完成认领/失败释放及预算保持。原测试、替身、unit 标签与真实作用域有逐项映射。
- 生产 Wire 已不再引用或构造旧 handler，也不再依赖其事后完成绑定屏障；普通/unit/integration 的 server 依赖图均验证旧 handler 为空。余下旧 Handler 只有测试兼容消费者，尚需整体清零；service 的单次平台与策略适配仍待收尾。不能将本批完成视为 S16 完成。
- 编译、构建和定向普通 599 条、unit race 1,238 条通过，各有一项既有 WS 跳过。真实 Redis 视频归属/并发认领和 PostgreSQL 一次资金效果的 integration race 4 条通过。初次 lint 配置缩进问题已修正，原 handler-no-repository 仍完整生效；之后四处清零消费者的旧提交/审核包装删除，初次诊断均保留。
- 首次全仓普通为 12,003 条通过、3 个失败事件、4 项跳过：两个已迁文件的静态额度平台合同仍读取旧路径，连同父用例失败。将合同迁至实际 HTTP 所有者并改为扫描真实完成构造，保留原四个子用例与字段断言，增加非空命中检查；定向 5 条通过。此为本批迁移遗漏，未扩大历史问题修复范围。
- 修正后重新完整执行：普通 12,006、unit 20,040、integration（-p=4）13,009 条通过，既有跳过 4/8/4；三套未截断 lint 均为零。14 类依赖夹具在三套标签下 42/42 通过，夹具删除；Wire 再生成稳定。SQL/Ent、原计划正文、S00—S15 冻结资料及其他任务文件未变，文档已同步实际生产结构。
- 完整证据见 `progress-20260924-media-entry-verification.json`；本批按授权提交进度，下一步清零剩余旧 HTTP 测试兼容与平台执行适配。当前仍保持 16/17、实施中，最终前后端/进程/生成物等全项验收不降低要求。


### 旧 HTTP 包、测试兼容及历史许可清零（2026-09-24）

- 旧 handler 目录及最后 17 个 Go 文件（包括旧测试 TestMain）删除，空 quotaview 目录一并退出。生产、测试与生成代码不再 import 该包，也没有 OpenAIGatewayHandler 类型引用。剩余平台单次与调度策略仍在 service，未提前宣布全部旧包清零。
- 单模块合同按 HTTP、openaiattempt、requeststate、ws、provider、httpx 归属迁移；跨入口合同由 app 实际绑定函数配合函数句柄夹具执行，不复制执行、金额或缓存规则。显式零值切换预算、测试配置修改、typed-nil 语义及同一并发/图片实例保持。并发计数/错误替身统一进入 HTTP testkit，生产 import 被门禁禁止。
- 143 项原测试/benchmark 声明及标签逐项找到替代位置，benchmark 未运行。Panic 合同直接验证原生恢复入口，依赖合同使用真实原生依赖值，元数据预期散列直接引用 scheduler；删除无生产消费者的转接后，原风险记录断言改为调用原生 Support，真实 WS 终态链合同继续执行。原字段、状态、错误和调用次数断言保留。
- 提取测试替身时的局部变量命名及搬迁后的私有助手/旧散列 import 环修正均属本批迁移修正，初次编译日志保留。最终编译、构建及定向普通 2,021 条、unit race 1,569 条通过，无失败或跳过；测试工厂只保留实际使用的端点句柄。
- 全仓普通 12,006、unit 20,040、integration（-p=4）13,009 条通过，既有跳过 4/8/4，数量与迁移前一致；三套完整 lint 均为零。删除 41 项旧路径规则，新增目标角色的精确许可并保留同等限制；旧 handler 全局拒绝及 testkit 生产隔离的 12 类夹具三套标签共 36/36 通过，临时夹具删除。
- 普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 的受影响文件选择已核对；选择记录不替代行为证据。Wire 再生成稳定，SQL/Ent、冻结正文/资料及其他任务文件无改动，已有与新增差异检查通过。系统架构和网关生命周期文档同步当前结构。
- 索引 `progress-20260924-handler-retirement-verification.json`。按用户授权提交本完整批次；继续清理 service 的平台/调度/健康适配及最终全项验收，roadmap 仍为 16/17、S16 实施中。


### 剩余能力批次账本与账号健康完整退出（2026-09-24）

- 按用户最新要求调整实施粒度和验证频率，原计划正文不变。复用现有函数调用账本，一次性覆盖剩余 208 个生产文件，按健康、选号/诊断、Messages/Bedrock、Gemini/Antigravity、Grok、OpenAI、WS/Live 与共享边界登记依赖和退出条件；不按文件数量机械迁包。统一索引为 `baseline/S16/service-capability-exit-ledger.json`，后续只增量更新。
- 账号健康批次删除旧 RateLimitService 及健康、恢复、Team 构造回绑四个生产文件。app 直接发布唯一 UpstreamHealth，所有平台消费者与构造签名同步改绑；Health/Recovery/Team/429 观测与 RuntimeBlockState 仍使用已有实例，规则、缓存和存储未复制。gateway/provider 只保留执行快照与显式观测投影，写回范围及空观测短路保持。
- 调度参数与健康测试依赖分离，评分诊断构造删除未使用的健康参数；原有选择指标断言保留。九个健康合同文件迁至实际执行适配旁，原标签和 32 项测试声明保留；共用健康/错误记录替身及设置内存替身进入所属 testkit。余下旧 service 测试只验证尚未退出的平台执行，不重建旧限流服务。
- 旧 service 生产文件 208 → 204（减少 4），测试文件 331 → 322（净减少 9）。普通直接消费者 4,240 条、unit 7,091 条通过，各保留 2 项既有跳过；定向 unit race 399 条、真实 PostgreSQL 装配 integration race 3 条、真实 Redis 兼容 integration race 6 条通过，无失败或跳过。批内 unit lint 为零；完整命令、初次编译/迁移修正及跳过见 `baseline/S16/service-health-verification.json`。
- Wire 再生成稳定。因删掉旧健康构造对设置读取器的依赖，Wire 推迟构造这些读取器；搜索仍只登记原 StartOrder=181/StopOrder=30 hook，没有提前初始化或启动。SQL/Ent、原计划正文、前阶段冻结资料及 51 个受保护文件未变；文档同步实际健康所有权。
- 本批只归档新增证据，不重新打包以往全量资料。按授权提交进度；下一能力批次继续消除选号/诊断的资格、窗口/RPM、渠道与模型投影依赖。相关稳定批次合并执行全仓三类测试、三套 lint 与门禁矩阵，最终全部验收不降低要求。S16 保持实施中、16/17。


### 持久模型诊断完整退出与两批合并验证（2026-09-24）

- 选号依赖账本中的持久模型诊断已经独立退出：删除通用/OpenAI 诊断和旧读取投影三个生产文件。app 直接把原 account 存储和唯一渠道实例交给 routing.ModelAvailability，固定 Messages、兼容模型、已解析模型三种意图；诊断仍绕过瞬时调度快照，不取得槽位或读取执行凭据入口。
- HTTP 文本/媒体/WS 共用的尝试绑定及两类计数入口显式接收原生诊断端口，不再从旧 GatewayService 获得它。测试同批显式绑定原仓储、渠道及配置；原未配置诊断的计数替身保持意外调用失败，不以空成功替身隐藏额外查询。渠道映射的唯一规则归 ChannelService.ResolveRoutingModel，OpenAI 选择/诊断/WS 的兼容模型判定共同使用 ModelPolicy.SupportsCompatibleRouting。
- 12 项旧模型诊断测试及 unit 标签迁至实际适配所有者；冷却中不可选号但仍支持已配置模型、空池、平台、映射和 nil 的断言保留。新增真实 PostgreSQL 组合根合同验证显式分组、standard/simple 范围、瞬时冷却与持久禁用。首次夹具遗漏分组投影、误把普通别名映射当白名单的失败保留，均仅修正本次新增夹具，未改生产算法或扩大历史修复。
- 本批旧 service 生产文件 204 → 201（减少 3），测试文件 322 → 321（减少 1）。定向合同 456 条通过、1 项既有跳过；真实 PostgreSQL integration race 5 条通过。与账号健康批次合并的全仓普通/unit/integration（-p=4）分别为 12,006 / 20,040 / 13,014 条通过，既有跳过 4/8/4。
- 合并普通 lint 捕获上一批健康测试辅助文件未加 unit 标签的两个 unused，已按实际消费者补标签。修正后该包普通 420 条、unit 572 条通过，测试名称集合与全量记录完全一致，integration 编译通过；三套全仓 lint 最终为 0/0/0。没有业务代码修正、忽略项扩大或断言削弱。
- 合并依赖夹具 15 类 × 三种标签，共 45/45 符合预期，临时目录已删除。八种构建选择完成核对；无标签测试目录的预期排除单独保留，选择不计行为通过。Wire 再生成稳定，SQL/Ent、前阶段冻结资料、原计划正文及 51 个受保护文件无改动，已有/新增差异检查通过。
- 统一索引为 `baseline/S16/service-selection-read-verification.json`，账本只增量更新。本次只归档新产生的证据，不重复打包上一健康批次。高级评分诊断、候选补全、窗口/RPM、previous-response 和槽位仍是剩余选号依赖，平台单次执行及 WS/Live 也尚未全部退出。按授权提交本完整能力进度，不将 S16 标记完成；继续沿总计划与阶段计划执行，最终全项验收保留。

### 选择、评分、粘性与固定装配完整能力批次（2026-09-24）

- 按已冻结的能力清单完成 Generic、Compatible、Gemini、诊断及无槽探测的实现、生产消费者、测试替身、装配和门禁改绑。`scheduler` 保持唯一选择/评分规则，`gateway/provider/selection` 只提供受控目标和资格投影；OpenAI/Grok 普通选择与 WS 固定账号复核共用 provider 的资格实现。
- Messages、文本、媒体、WS/Live、计数、Qoder 和创作消费者直接使用原生选择实例。旧选择方法、picker 类型、窗口适配和调度事后绑定已删除；选择专用字段与无用构造参数同批退出。快照解码保留在 Redis Adapter，分组隐私、配额阈值和单次代理绕过进入请求值快照。
- 305 个迁移测试均有原路径、目标路径、标签、函数摘要及断言调用对应；无缺失，原断言调用数量一致。循环、模型、槽位释放、查询与缓存边界继续通过原生实现验证。迁移期间出现的夹具响应状态缺失、静态 Options 场景与构造签名遗漏均已修正，失败日志保留；未扩展历史问题修复范围。
- 本批受影响范围普通 / unit / integration（`-p=4`）取得 **5,923 / 8,799 / 6,054** 条通过事件，各保留原来的两个跳过：外部 OpenAI token 对照及 WS passthrough 的 `other_event_type` 子场景。三套 lint **0 / 0 / 0**。定向 owner / 混合执行 / app / 真实 Redis race 分别通过 **1,302 / 5 / 237 / 64** 条事件；Fast 策略 middleware 直接消费者另行完成编译、race 和三标签 lint。事件含父子测试且集合重叠，不求和。
- **99 项**门禁合同通过，覆盖普通/unit/integration；普通、unit、integration、wireinject、embed、e2e、Darwin、Linux 的文件选择核对通过。Wire 再生成无差异，SQL、Ent、旧冻结资料及 51 个受保护文件未变。已同步调度缓存、网关生命周期和系统架构文档。
- 相对 `1f734d4d4`，旧 service 生产文件 **201 → 186（减少 15）**，测试文件 **321 → 290（减少 31）**。减少量只统计旧目录；不把新文件数量作为架构完成证明。
- 统一台账 `service-capability-exit-ledger.json` 已标记本能力验证完成；剩余共享编码、请求散列/观测和 originator 投影有准确消费者与退出批次。它们由尚未退出的平台执行、WS/Live 使用，不拥有第二份选择算法。下一批仍按 Messages、Google、Grok、OpenAI、WS/Live 与共享边界顺序完整退出，不重新扫描或扩展历史问题。
- [本批验证索引](baseline/S16/service-selection-owner-verification.json)；[增量证据清单](baseline/S16/progress-20260924-selection-manifest.json)。本批归档接续 model-read 父链，只保存新增或变化资料，逐成员校验通过。

S16 与总计划仍为 **实施中、16 / 17**；本批定向验收不替代阶段最终全仓测试、三套 lint、构建及进程验收。按用户后续授权，每个完整验证能力批次提交一次；不提交 AGENTS.md、SYNC.md 或其他任务内容。

### Messages、转换、计数与通用执行聚合退出（2026-09-24）

- 本批以 `440ffdd02` 为输入，按完整请求能力处理实现、生产调用、测试替身、装配和门禁。Messages、Claude Chat/Responses 转换、count、API Key 透传、Vertex 与 Bedrock 单次执行接入 `gateway/provider/messageforward.Runtime`，继续调用已有 forward/upstream 状态机。通用 `GatewayService`、构造器及全部方法已删除。
- app 直接绑定 RoutePlanner、原生会话存储、RetryCooldown、完成记录器与唯一调试输出句柄；临时停调仍先检查请求级瞬时故障及最新池模式。请求内 Beta、工具恢复和诊断状态不再借用 Gin 通用键。共享非 JSON 响应失败处理、Fast 规则、传输错误分类及客户端字符串判断各保留一份实现。
- 旧 service 从 **186 个生产 / 290 个测试文件**降至 **169 / 259**，减少 **17 / 31**。252 个原测试按名称、构建标签和断言调用数量核对，均无遗漏；5 个原 benchmark 迁移并编译，未执行 benchmark。20 个仍供其他平台使用的共享符号已逐项登记退出批次，未为清空文件机械搬包。
- 合并验证：全仓普通 **12,007**、unit **20,041**、integration（`-p=4`）**13,014** 条通过事件，分别有 **4 / 8 / 5** 项已登记跳过，无失败。数字包含父子事件，不相加。标准/simple SIGTERM、版本、维护命令、初始化失败释放、监听失败、CLI/Web/AUTO_SETUP 及路由清单合同实际执行。真实 PostgreSQL 完成链 integration race 为 **3** 条通过，最终定向 race **933** 条通过。
- 移植夹具补齐原默认 Beta 回调，保持响应读取默认 **128 MiB**；诊断开关关闭且无需保存错误上下文时，继续跳过报文解析。它们均为本次移植核对，不扩大历史问题范围。最终受影响普通合同 **142** 条通过；HTTP 输出测试归 HTTP Adapter，纯转换测试保留核心方向限制，补验 **32** 条合同通过。
- 全仓普通/unit/integration lint 均为 **0**。84 项可丢弃门禁合同通过，八种构建集合无意外错误；夹具已删除。Wire 两次重生成摘要一致；后端、两个维护命令和 Linux 构建通过。SQL、Ent、S00—S15 冻结资料及 51 个保留文件无变化，原计划正文摘要不变，diff 检查通过。
- 验证入口：[`service-messages-owner-verification.json`](baseline/S16/service-messages-owner-verification.json)。完整日志和清单按本批前缀增量归档；本批不代替 S16 最终前端、embed、跨平台及全项验收。下一完整能力批次为 Google 执行，之后继续 OpenAI/Grok、WS/Live 和共享边界；roadmap 仍为 **16 / 17**。


### Google 执行完整批次（2026-09-24）

- GeminiMessagesCompatService、AntigravityGatewayService 的生产调用、HTTP、测试及装配已一起退出。请求准备进入 `gateway/provider/googleforward`，响应 Header、错误格式与 Ops 输出留在 `gateway/httpapi`；Gemini 健康处理进入 `account/provider.GeminiErrorObserver`。Antigravity 转发与账号探测继续共享原健康和重试实例，没有新增账号切换循环。
- 图片计数和工具名恢复改为每次 attempt 的显式状态，保留累计流片段取最大图片数、无图片时的模型回退及各入口原失败结算规则。凭据回填仍在读取后同步到执行目标；健康观察只回写原有凭据与 Extra 字段。静态配置由 app 直接投影，RuntimeReaders 的 Antigravity 配置转接及旧事后绑定已删除。
- 本批旧 service 从 **169 个生产 / 259 个测试文件降至 149 / 246**，减少 **20 / 13**。`creative_executor_gemini.go` 实际只有 OpenAIGatewayService 的 URL 校验，已在统一账本归到 OpenAI 批次；`gemini_multiplatform_test.go` 仅为其他旧平台保留查询替身，不含 Google 执行实现。
- **127 个原测试**完成名称、标签与断言调用核对，缺失为 0、断言调用数量变化为 0。无生产消费者的前缀辅助方法删除，原十项断言由纯 modelmap 匹配实现承接；4 个原 benchmark 保留并可编译，本阶段未执行。测试构造直接组合原生端口，私有响应边界仅由 `export_test.go` 开放，没有扩大生产 API。
- 最终受影响普通测试 **1,603 条通过事件**；前序账号/provider 定向 unit race **1,408 条通过**，收尾 Google/modelmap race **295 条通过**，动态路由绑定补验 **10 条通过**；真实 PostgreSQL 探测装配 integration race **3 条通过**。这些集合包含父子测试且有重叠，不求和；均无失败或跳过。迁移夹具漏绑回调、旧构造参数和编译问题的初始日志保留，未追加历史问题修复。
- 受影响普通、unit、integration lint 均退出 0；最终门禁夹具 **48/48** 通过并删除，八类构建选择无意外错误。Wire 重复生成稳定，全仓生产构建通过。证据索引为 [Google 批次验证](baseline/S16/service-google-owner-verification.json)，较大日志与逐符号映射由本批增量归档提供。
- SQL、Ent、S00—S15、原计划正文与 51 个其他任务文件保持不变；现有架构、网关生命周期、Gemini 和 Antigravity 文档同步，31 处链接及稳定锚点有效。按用户调整后的频率，本批不重复全仓三套测试与前端验收；待后续稳定能力合并复核，S16 最终完整验收保持不变。
- 下一批继续 Grok，再处理 OpenAI、WS/Live 与共享残留。roadmap 保持 **16 / 17、S16 实施中**；本批回退只恢复执行、装配、规则与文档，不涉及数据格式。


### 共享请求凭据完整能力（2026-09-24）

- 梳理 Grok 与 OpenAI/媒体/WS 的依赖后，先把共享请求凭据作为可独立退出的能力完成。`GetRequestCredential`、失败处理、持久确认和旧网关互斥表已删除；`account.GrokCredentialRecovery` 拥有条件写入和运行时回滚，`gateway/provider.RequestCredentials` 拥有请求级分类与恢复，HTTP Adapter 只关联预算和 Ops。
- 所有生产调用直接使用同一 app 实例。保留十五秒跨尝试预算、五秒写入、250 毫秒提交确认及 500 毫秒缓存清理；没有新增锁策略或数据格式。原生模型读写仍通过原存储执行，SQL 和 outbox 原子范围未变。
- 旧 service 从 **149 个生产 / 246 个测试文件降至 148 / 236**，减少 **1 / 10**。27 个原测试完成名称、标签与断言核对，缺失和断言调用数量变化均为 0；刷新替身的运行时接口能力也同步迁移。占锁测试通过真实 Apply 建立屏障，没有增加测试专用生产 API。
- 原凭据定向 unit race **93 条通过事件**，直接消费者 unit race **818 条通过**，普通消费者 **246 条通过**，真实 PostgreSQL 条件写入与 outbox integration race **11 条通过**；无失败或跳过，集合有重叠，不求和。首次选错存储包的零测试结果不计通过，后续实际执行证据单列。
- 受影响三类 lint 均退出 0，门禁 **45/45** 通过并删除夹具；首次门禁运行的 lint 进程锁冲突已保留，串行重跑通过，未改规则。全仓生产构建、普通/integration 消费者编译和八类构建选择通过，Wire 再生成稳定。索引为 [共享凭据验证](baseline/S16/service-grok-credentials-owner-verification.json)。
- 本批只清除共享凭据能力；其余 Grok 文本、媒体和共享 Responses 输出继续实施，OpenAI、WS/Live 及最终合并验收仍未完成。roadmap 保持 **16 / 17**。按用户要求，完整验证的能力批次提交一次；不提交其他任务文件。


### Google 与共享凭据合并全仓复核（2026-09-24）

- 在 `fe716affc` 固定工作树串行运行全仓普通、unit、integration（`-p=4`）及三套 lint，期间未改生产代码。普通 **12,008 通过 / 4 跳过**，unit **20,042 / 8**，integration **13,016 / 4**，均无失败；三套全仓 lint 均为 0。
- 跳过项逐测试保存，仍为已登记的供应商/本地授权、TLS、legacy-null 与 WS 分支限制，没有把整个存储集合跳过当作通过。详见 [合并复核](baseline/S16/interim-google-request-credentials-verification.json)。
- 这次合并复核覆盖最近两个稳定能力，不代替 S16 最终验收。后续继续 Grok/OpenAI/WS-Live、共享响应与装配退出，旧 service 仍为 **148 个生产 / 236 个测试文件**；roadmap 保持 **16 / 17**。


### 共享响应回合来源与 Header 完整能力（2026-09-24）

- Codex 回合来源表迁入 `gateway/session.CodexTurnOrigins`，HTTP 暂存、提交、回带过滤和响应头透传进入 `gateway/httpapi`，所有调用直接绑定同一 app 实例；旧网关来源表、计数器及对应方法已删除。未合并不同作用域的 WS 回合状态缓存。
- 保留原 API Key/原始会话键、TTL、严格 After 到期判断、每 256 次写入清理，以及“暂存不登记、真正提交才登记、未知/同账号/过期来源透传”。来源表仍只保存账号 ID；配额头放行和缺失回合状态的清理顺序不变。
- 旧 service 从 **148 / 236 降至 147 个生产 / 235 个测试文件**，减少 **1 / 1**。8 个原测试全部迁移；两处私有 any 类型断言随生产接口改为静态 int64 返回及非零 ID 断言，存在性、确切 ID 和未提交不登记的业务断言保留，单列映射说明。补验到期时刻、256 次清理及并发请求隔离。
- 定向 unit race **21 条通过事件**，消费者普通 **426 通过 / 1 项既有 WS 分支跳过**；普通/unit/integration 受影响 lint 均为 0，门禁 **36/36** 通过。Wire 重复生成稳定，全仓生产构建、integration 消费者编译及八类构建选择通过；17 处文档链接/锚点有效，旧冻结资料和 51 个其他任务文件未变。
- 证据索引：[共享响应状态验证](baseline/S16/service-response-state-owner-verification.json)。Grok/OpenAI 共享响应读取、健康副作用、媒体及 WS/Live 仍继续收尾；roadmap 保持 **16 / 17**，最终全项验收未完成。

### Grok 健康与快照节流完整批次（2026-09-24）

- 额度观测、错误策略、模型与团队冷却、精确恢复统一接入 `account/provider.GrokHealth`。app 复用账号存储、运行阻断和模型瞬态状态；Grok/OpenAI 使用同一普通快照节流器，原关键状态绕过和 OpenAI 空字段默认节流保留。
- 文本、媒体、Voice、计数与 WS 消费者直接传入当次模型和观测。旧健康方法、观测转接、限流转接和私有团队模型 context key 已删除；没有改动重试、取消预算、缓存字段或条件写规则。
- 32 个原健康测试迁至原生测试，名称、标签、断言数量均保持；新增共享节流并发契约。原生 race、消费者 race、普通消费者与真实 PostgreSQL 代次保护均通过，事件详见[验证摘要](baseline/S16/service-grok-health-owner-verification.json)。首次编译和 lint 的迁移反馈已修正并保留日志。
- 受影响普通/unit/integration lint 均为 0；39/39 门禁、8 个构建集合、Wire 两次生成稳定及生产构建通过。51 个保护文件、SQL、Ent 和早期冻结资料未变，原计划正文保持。
- 旧 service 生产文件 147 → 144，测试文件 235 → 234；32 个用例来自混合文件，因此不以用例数量代替文件减少量。剩余阻塞是 Grok/OpenAI 共用响应输出、模型恢复及 HTTP/WS/Live 执行图。S16 继续实施，最终全量验收尚未完成。

### 共享请求/响应报文大批次与合并验证（2026-09-25）

- 按用户调整后的粒度，将工具/namespace 改写、Codex 请求选项、WS 工具继承、Compact 恢复、响应终态/usage 和工具修正一起迁完生产调用、测试、装配与门禁；没有为每个文件单独验证或提交。
- protocol 唯一执行纯报文操作，gateway/provider 投影账号与平台选项，`requeststate.ResponseTools` 持有尝试/turn 状态，HTTP 负责读取状态、恢复输出和观测。WS 下一轮声明与当前 turn 名称分开；原工具 ID、未知字段、大数及 nil/空状态保持。
- Compact 直接使用原 `Recovery` 和 `Failure`，app 固定静态配置，HTTP Executor 按原顺序观察、关闭旧响应并更新模型。未新增推理、换号循环、资金操作或持久状态。
- 118 个原测试名称、标签和断言调用数量对齐；相关 race 1,340 条通过、无失败或跳过。原有 benchmark 只编译，不作为性能证明。合并前两批完成完整普通/unit/integration 测试及三套 lint，具体事件、既有跳过和命令见[验证摘要](baseline/S16/service-response-output-owner-verification.json)。
- 45/45 门禁、8 个构建集合、Wire 连续生成稳定、生产构建和文档链接检查通过。SQL、Ent、S00—S15 冻结资料、原计划正文及51个保护文件保持。
- 本批旧 service 生产 144 → 135，测试 234 → 228。下一批按剩余共用响应的11文件/43方法依赖闭包整体处理健康副作用、流错误、首输出和代理反馈，再退出 Grok/OpenAI/WS/Live 旧执行图。S16 仍在实施，最终前端/跨平台/进程等完整验收尚未完成。

### 共用响应执行大批次（2026-09-25）

- 将 Responses、SSE、passthrough、Chat、Messages、Raw Chat、Anthropic 与图片的共用响应适配一并接入 `OpenAIResponseOutput`，同批处理健康、错误、超时、代理反馈、诊断、结果类型及生产消费者。没有把旧聚合 Service 整体改名搬包。
- app 固定绑定原生拥有者，共享运行阻断、模型状态、代理熔断、工具修正器、响应归属及缓存。推理历史通过同一可选端口读取与写入，保留七天 TTL、两秒预算和失败放行；WS 桥的已执行副作用只消费一次。
- 76 个原测试迁至拥有者，名称、标签和断言调用数量保持；共享测试解析和规则替身只保留一份。首轮因19处旧字段迟绑定出现的23个失败事件已修正测试装配，同集合3,761条通过、2项既有跳过；没有放宽断言或新增历史修复。
- 定向 race 3,901条通过、2项既有跳过；真实 Redis/PostgreSQL race分别3/4条通过，无失败或跳过。全量普通/unit/integration分别12,013/20,047/13,022条通过，既有跳过4/8/4逐名对齐。完整lint最终0/0/0；首次发现的2个无生产消费者旧包装及中断的冷分析均留有记录。
- 最终60/60门禁、8个构建集合、Wire连续生成稳定和生产构建通过。SQL、Ent、旧冻结资料、原计划正文与51个保护文件保持；[验证摘要](baseline/S16/service-common-response-owner-verification.json)区分了行为通过、跳过和仅编译。
- 旧service生产135→110、测试228→220，完整批次验证后提交。剩余Grok/OpenAI请求执行、WS/Live与旧装配继续按能力收尾；S16最终前端、跨平台、进程及资金销项尚待完成，roadmap保持16/17。
