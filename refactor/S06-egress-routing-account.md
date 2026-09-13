# S06：迁移出站策略、路由与账号管理

## 1. 基线与实施约定

以当前 `main`、HEAD `d0ce5504c09c9b733a75da1fc35826ebf53f6af4` 为起点，完成 S06.0—S06.4。保持现有 HTTP、配置、数据库、缓存、金额精度及 standard/simple 行为兼容。

实施前将本计划原样保存为 `refactor/S06-egress-routing-account.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，清单与脱敏证据保存到 `refactor/baseline/S06/`；沿用阶段约定，不另存 `.agents/plans/`，不改写 S00—S05 冻结资料。

已核实：

- 项目工具链 Go 1.27.0；系统 Go 1.27.1；golangci-lint 2.13.2；Docker 29.5.2 可用。
- 相关 service 定向 unit 测试通过；分组、复制、代理到期及代理身份变更的 PostgreSQL 测试取得 47 条通过事件，无失败或跳过。
- 工作区只有其他任务的未跟踪计划和 diagnostics，保持其内容；不自动提交、推送或切换分支。
- 本阶段只生成 Wire，不生成 Ent，不修改 SQL migration、缓存版本或协议。
- 按用户确认，相关历史缺陷先在原 HEAD 保存复现，再最小修复并单独记录行为变化。
- 保留现有 Redis 发布订阅、锁和 outbox 兼容性，不扩大多实例支持范围；S04 平台额度协调仍限于单服务进程。

## 2. 模块接口与事务边界

### 所有权与运行时投影

| 模块 | 本阶段拥有 | 对外接口 |
| --- | --- | --- |
| egress | 代理管理、有效性与 fallback、TLS Profile/Router、Header 安全规则、目标策略、传输回退策略 | 根据显式请求输入返回 `EgressPolicy`；管理与探测通过各自用例调用 |
| routing | 分组、渠道、默认组与回退、模型目录与市场、价卡存取、协议选路、分组可用性探测 | `Plan` 生成 `RoutePlan`，`ResolveCandidate` 为候选生成当次尝试计划 |
| account | 账号管理、配置与凭据持久化、影子关系、刷新协调、导入、健康维护、测试及上游用量查询编排 | 输出 `AccountSnapshot`；凭据通过独立的受控读取/刷新入口取得 |
| billing | 账号消费累计、资金窗口及消费重置 | 账号管理发出重置意图，billing 提供同连接参与能力 |
| scheduler、旧平台执行层 | 本次选择、评分、槽位、重试循环、平台协议与具体供应商交换 | 本阶段通过窄接口接入，分别留 S07、S09/S11 |

接口约定：

- 新核心不接收旧 `Account/Group/Proxy`、完整 config、Gin、Ent、SQL/Redis 客户端或具体 Adapter；静态 Options、时钟和日期对象由 app 注入，动态设置继续按原时机读取。
- `AccountSnapshot` 只包含消费者需要的身份、关联、资格、模型配置和运行状态，不携带可供任意读取的完整凭据。凭据快照与管理 DTO 分离，禁止进入公开 JSON 或普通日志。
- `RoutePlan` 包含最终分组、平台、入口协议、模型链、渠道与策略投影，不预先固定账号或最终上游协议。候选结果每次 attempt、fresh/DB 复核重新计算，不写入共享账号缓存。
- `EgressPolicy` 包含代理选择、TLS 身份、允许的 Header 和目标/重定向策略。HTTP Adapter 将其投影为 S01 transport options；核心不创建客户端或执行拨号。
- map、slice、指针和嵌套策略在跨缓存、请求、管理输入的边界提供独立副本。旧完整实体保留必要投影与委托，不整体递归别名，不复制算法或缓存。
- 分组高级调度参数的值类型、复制、合并和校验提取到 `scheduler/policy` 纯叶子；评分、EWMA、抽样和调度执行仍留 S07。现有 domain/accessview 入口按需别名。

### 事务与失效

- routing 拥有分组默认切换、账号关系复制/替换、渠道及价卡写入的现有闭合操作；account 拥有账号配置、凭据、影子关系和账号侧绑组操作。
- `account_groups` 写入收敛到 account 存储参与实现，由 routing 的事务通过 app 注入；保留各入口原有事务范围、锁顺序及隔离级别。
- 代理修改中的账号快照清理、代理到期改投、账号额度重置等跨模块写入使用命名明确的参与能力。只有 Adapter 接收现有 Ent/SQL 事务，沿用原 Ent context，不新增事务 key。
- 事务内参与方法不得独立提交或发布成功缓存失效。原来同事务的 outbox 继续同连接写入；原来提交后尽力发布的事件保留原失败语义，不统一升级为强事务。
- 调度 outbox 编码、合并及消费实现仍由现有实现提供，存储侧通过 app 注入同连接 writer；account/routing/egress 核心不引用旧 scheduler。消费器、bucket/epoch/tombstone 和重建逻辑留 S07。
- 普通配置修改只写其拥有的字段，不回写读取时的 last-used、消费累计、窗口或并发健康快照。明确的重置与恢复操作单独表达。

## 3. 实施步骤

### S06.0：冻结输入与逐项清点

记录实际 HEAD、索引、工作区、工具版本、源文件摘要、SQL checksum、Ent/Wire 摘要及 S05 完整验证结果。

按文件和符号登记目标、消费者、构建条件、测试、Wire、文档锚点、事务及兼容入口退出阶段，重点覆盖：

- AdminService 剩余分组、账号和代理用例，以及 handler 中的批量修改、导入导出、探测与状态恢复。
- Account 中配置、凭据、平台解析、健康、资金和请求临时状态；Channel 中模型规则与请求体改写。
- 后台刷新、请求路径刷新、手动刷新、CRS 刷新各自的锁、持久化及后置处理。
- TLS 订阅/采集监听、渠道缓存、用量查询 singleflight、维护 worker、Deferred 和按需任务的唯一生产实例。
- S01 HTTP/2 策略、S02 Deferred、S03 协议/渠道、S04 账号资金字段、S05 分组投影的交接项。

历史缺陷优先检查刷新覆盖管理员新凭据、普通账号更新覆盖运行字段、缓存副本污染，以及停止过程中仍启动维护操作。先复现再修复，不把文档描述直接当作已有保证。

### S06.1：egress 与传输策略

- 迁移代理 CRUD、批量操作、导入导出、质量/延迟探测和到期维护。HTTP 与探测网络执行分别进入 Adapter，核心拥有管理规则和结果判定。
- 保留 `none/proxy/direct`、链式 fallback、循环/缺失处理、origin 恢复、每个代理的子事务及原批次后置通知。代理错误不得隐式直连；代理身份变化继续清理关联的 Ollama 状态并拒绝旧探测结果。
- TLS Profile/Router 的配置、校验、规则顺序、正则、默认选择和缓存迁入 egress；保留账号直接绑定、Router、特殊请求用途间的现有优先级。
- TLS 采集业务归 egress，监听、证书和 ClientHello 捕获进入其技术 Adapter。保持管理员权限、按需开启、会话限制、脱敏和关闭等待，不随应用启动提前开启监听。
- Header 名称和值校验、禁止项及响应 Header 编译规则归 egress；账号类型适用性归 account。实际写入仍在 HTTP/旧平台 Adapter，保留应用顺序和 `x-codex-routing-hint` 等网关控制头约束。
- S01 遗留的 OpenAI HTTP/2 回退状态与决策迁入 egress，使用明确的传输类别及技术观测输入；保留原代理键、计数窗口、阈值、TTL 和错误分类。Grok CLI Header 与窄范围 403 回退继续留 S09。
- 复用唯一 HTTP 池及原隔离键；保留代理解析、取消、ALPN、握手上限、逐跳校验和响应关闭释放。维持当前校验与实际连接分别解析的方式，不引入 DNS pinning。

### S06.2：routing、目录与价卡存取

- 迁移分组与渠道管理、默认选择、复制恢复、排序、账号关联、模型配置、价卡存取和唯一渠道缓存。保留每组最多一个渠道、默认组显式优先及历史名称回退。
- 保留字段省略/null/清空、显式空协议集合、旧输入转换、平台隔离、渠道冲突及原错误 reason。分组删除继续保留原 Key GroupID、授权和外键语义。
- 接入 `Plan` 与候选解析接口，复用 capability/modelmap/pricing。保持复合选组 → Key 改写 → 渠道 → 账号映射、一跳规则、账号原生优先及单步协议 fallback。
- 普通、无效请求和不可用分组回退分别保留原触发条件。最终分组重新执行权限、协议、计费和 scheduler_type 解析；请求体、响应恢复、会话和重试时序仍留旧网关。
- 迁移可请求模型、公开市场和容量/可用性编排。平台默认模型和专有模型资格通过注入端口提供，市场不再持有具体 GatewayService 或 BillingService。
- 价格通过 billing 报价接口和 pricing 类型组合，保留懒加载、零价/缺价、歧义价格、目录元数据与价格回退的区别，以及用户价格和账号成本独立。
- 保留预取失败后逐组回退、稳定排序、查询成功但为空时不恢复默认列表、辅助观测失败不阻断市场响应。
- 分组可用性 runner 归 routing：保留 PostgreSQL 租约、最多五个即时 worker、轮次不重叠、独立预算、缺省重试三次与显式零次，以及每轮只保存最终结果。实际账号选择/探测通过端口接入。
- S05 Key 分组读取、S04 billing 分组/渠道读取及其他已迁模块改为 app 对新 routing 的直接投影，删除对应旧桥接。

### S06.3：account、凭据与维护

**管理和写权限**

- 迁移创建、编辑、批量操作、复制恢复、删除、影子关系、凭据清理/脱敏及导入。保留九个平台、现有账号类型、原生协议和认证差异，不恢复废弃探测字段。
- 批量协议更新继续先验证全部对象再原子写入；其他导入/批量入口保留各自部分成功和补偿语义，不统一改成全量事务。
- 影子账号保留一母一影、父账号资格、无自有认证凭据、允许的模型映射、代理继承、级联与禁止操作。现有 create+bind 补偿流程与复制事务分别保留。
- CRS HTTP 客户端进入 account/provider；同步规则归 account。OAuth 交换和供应商解析经旧 Adapter 提供，保留选择过滤、状态归一化、代理映射和逐条结果。
- 账号额度累计与重置移入 billing 的唯一实现。account 负责权限和影子账号拒绝；重置消费与清除账号 cooldown 保持原原子操作，不清除其他停调状态。配置写入不得覆盖并发结算字段。

**刷新和凭据缓存**

- account 拥有统一刷新协调、候选分页、平台并发/QPS、重试预算、token version、缓存失效和关闭等待。请求路径、后台及手动入口复用同一协调实例，同时保留各入口资格和错误差异。
- 具体 OAuth 交换、PAT/Agent Identity/Qoder 会话及平台专有解析留旧执行 Adapter，S09 退出；不能把旧 OAuthService 整体搬入 account。
- 保留现有缓存键、TTL、项目/账号备用键清理、锁等待、Redis 故障降级、invalid_grant 竞争恢复及取消后禁止写入。
- 当前成功刷新只有 Grok 使用凭据/代理 CAS；其他平台需在原 HEAD 验证与管理员改凭据的交错。复现覆盖后，使用已有字段比较进行条件写入，不新增版本列；CAS 未命中重新读取并校验当前状态，不覆盖新值，也不因此再次交换 token。
- 保留 Grok 成功持久化后的回读、缓存清理及结果不明时的错误分类；不同平台的持久化失败与恢复语义分别测试，不能统一改成盲目重试。

**测试、健康及后台资源**

- 账号测试改为 `Test(ctx, TestRequest, EventSink)`，核心不接收 Gin。HTTP Adapter 按原格式写 SSE 并 Flush，后台直接消费事件结果；供应商执行通过窄端口提供，不再用 httptest 模拟 HTTP 作为生产编排。
- 保留 text/image 显式选择、历史缺省判断、Compact 固定载荷、协议选择、错误/结束事件和部分输出；写失败与取消继续传给实际执行层。
- OAuth 用量和 API Key 上游用量查询分别迁移编排，保留缓存作用域、查询前后身份复核、singleflight 与等待者取消语义。手动 API Key 查询不写健康、调度或账单；周期 CN/Ollama 等监控保持各自快照/CAS 规则。
- 健康状态、阈值决策、临时停调和恢复归 account；供应商错误与额度报文解析由平台 Adapter 提供。管理员禁用、过期、凭据错误与临时 cooldown 不互相替代。
- 迁移账号到期、计划测试、刷新和 Deferred。构造无启动，重复 Start/Stop 幂等；停止新调度、领取和入队，取消等待，再等待在途及最终 flush。
- 保留原周期、立即首轮与计划测试十秒偏移；将停止期间不可取消的等待改为受生命周期 context 约束。沿用 HTTP 五秒、后台总计三十秒预算；未完成项报告超时，共享依赖不得提前关闭。

### S06.4：HTTP、装配、门禁与交接

- 分组、渠道、市场 HTTP 进入 routing/httpapi；账号、导入、计划测试和用量查询进入 account/httpapi；代理、TLS 和采集管理进入 egress/httpapi。
- 路由直接绑定新 handler，保留 URL、中间件顺序、幂等 helper、管理员字段、分页排序、导入导出和 SSE 契约。HTTP 不直接调用存储。
- 高级调度评分诊断、Ops/用量统计和供应商 OAuth 的未迁实现通过窄接口接入，分别登记 S07/S08/S09；旧 AdminService 只保留未迁编排及兼容委托。
- app 持有唯一 Store、缓存、刷新协调器与 worker，完成配置投影、跨模块参与工厂和生命周期绑定。legacybridge 只调用与投影，不承载规则、缓存或锁。
- depguard 覆盖三个核心、Adapter、scheduler/policy 和装配；历史许可精确到文件/import，迁出即删除，保留原 service/handler 和 protocol 规则。
- 同步现有架构、路由结算、网关策略、协议能力、模型市场、账号维护、传输安全、HTTP 和开发文档，修正文档与实际实现的差异，保留稳定锚点。

## 4. 验证安排

项目命令统一使用 `GOTOOLCHAIN=go1.27.0`。每批验证新模块、旧委托和直接消费者；数据库、锁、缓存及事务采用隔离 PostgreSQL/Redis。

| 验证面 | 必须取得的行为证据 |
| --- | --- |
| egress | fallback 链/循环/到期/恢复、代理失败不直连、身份变更与旧探测 CAS、TLS 选择与缓存失效、HTTP/2 回退、池隔离与关闭释放 |
| routing | 默认组竞争、复制/替换/outbox 失败回滚、平台修改失效、空协议集合、逐候选重算、回退后再次授权、模型与价卡一致 |
| account | 批量原子性和部分成功边界、影子约束、凭据脱敏、普通修改不覆盖运行/资金字段、复制恢复、CRS 补偿 |
| 刷新 | 请求/后台/手动竞争、锁取消和降级、管理员换凭据交错、旧失败不污染新身份、CAS 未命中、持久化失败、旧缓存兼容 |
| 探测与维护 | SSE 次序/写失败、后台事件结果、查询身份变化、旧快照保留、禁止手动查询修改调度、自动恢复限制、cron 与租约 |
| 生命周期 | 唯一实例、构造无启动、重复启停、停止后不认领、在途等待、Deferred 最终 flush、超时报告、Redis/SQL 最后关闭 |
| 跨模块回归 | Key v40、sched:v2 投影兼容、注册默认组、订阅范围、账号成本与用户实扣、任务资金及 S04/S05 历史修复 |

- 本地 HTTP/TLS/代理与录制脱敏夹具验证供应商 Adapter；不使用生产凭据。真实供应商、外部 TLS 捕获及 E2E 限制单独登记，跳过不计通过。
- 缓存、刷新协调、订阅及 Deferred 运行定向 race；真实 PostgreSQL/Redis 竞争运行 integration race，不扩展全仓 race 或 benchmark。
- depguard 可丢弃夹具覆盖合法依赖、旧入口许可、同目录新增违规、旧文件新增禁止 import、迁出例外失效、核心反向依赖及正常 Adapter；验证后删除夹具并保留诊断。
- 使用 go list 核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 选择，JSON 测试事件确认关键测试实际执行。

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

另验证两个维护命令构建、Wire 再生成无差异、真实前端产物下 embed 测试，以及 standard/simple 启动和 SIGTERM 的维护资源停止顺序。

S05 lint 基线为 **1/285/19**，按路径映射、规则和完整消息比较。新增问题必须解决；原有问题独立归档，不扩大忽略规则。

## 5. 完成与回退

完成须同时满足：

- egress、routing、account 的本阶段生产调用链接入唯一实现，新核心无旧业务、config、I/O 或具体 Adapter 反向依赖。
- RoutePlan、AccountSnapshot、EgressPolicy 已被真实消费者使用；配置、资金、健康、凭据和调度写权限可核对。
- 事务、缓存失效、刷新竞争、探测输出和有界停止取得行为证据；相关历史修复保留原 HEAD 复现和独立说明。
- 所有兼容入口、构建条件、Wire、文档、依赖例外及 S07—S16 剩余职责有明确归属。
- SQL、Ent、S00—S05 冻结资料及其他任务文件无意外变化；Wire 差异可解释，已有与新增文件的 diff 检查通过。
- 必要验证未完成时保持“待验”，不凭编译或跳过宣布完成。

满足后 roadmap 更新为 **7 / 17**、S06 已完成，下一步编写 S07 子计划。交付可审查差异，不自动提交，不提交 SYNC.md 或其他任务内容。

回退按子步骤恢复代码、规则、装配和文档，不涉及数据库或缓存格式降级；历史修复回退会恢复的风险单列记录。


## 执行记录

### S06.0 · 2026-09-12

已原样保存计划并冻结实施输入（见 [快照](baseline/S06/start.json.gz)、[声明清点](baseline/S06/initial-ast.json.gz)）。实际 HEAD 与计划一致；索引无暂存变更，保留其他任务的计划及 diagnostics。S05 验证沿用其冻结资料；后续逐项补充本阶段迁移与验证证据。当前进入 egress 提取。

### S06.1 · 2026-09-12 · 首批接通

代理值类型/基本管理/fallback、管理员代理规则、Header 编译与校验、TLS Profile/Router 及缓存、采集会话和监听 Adapter、OpenAI HTTP/2 回退策略已接入生产。app 构造唯一代理管理、存储与 TLS 实例；Wire 由手写装配生成。代理 SQL 中的账号快照清理与改投已通过同连接 account/postgres 参与能力执行，原 outbox 编码仍由旧 publisher 提供。

历史复现：[TLS 副本污染](baseline/S06/original-tls-snapshot.result.json)包含 Profile 切片和 Router 可空字段两项失败；新实现已在缓存写入及返回边界深复制。[代理到期 worker](baseline/S06/original-proxy-expiry.result.json)复现重复 Start 执行两次首轮、Stop 后 Start 仍执行扫描；新实现增加一次启动与停止屏障，StopContext 取消并等待在途扫描。

验证：[定向 unit](baseline/S06/egress-final-unit.result.json)、[真实 PostgreSQL/Redis](baseline/S06/egress-storage-integration-3.result.json)及 [egress race](baseline/S06/egress-core-race.result.json)均退出 0。真实存储组包含 105 条通过事件、无失败/跳过；race 包含 60 条通过事件、无失败/跳过。较早的编译/规则诊断作为迁移试验记录保留，不能当作最终结果。

剩余：代理混合数据交换外壳在 S06.4 随 account 导入格式拆分；精确门禁还需最终正反夹具验证，TLS 及所有新运行资源在阶段收尾再次核对；不标记 S06.1 或整个阶段已完成。继续 routing 的纯配置、分组/渠道和生产投影。

### S06.2 · 2026-09-12 · 分组与渠道首批接通

渠道模型、唯一缓存、校验、SQL 和 HTTP 已迁至 routing；分组配置、管理、复制、默认选择、排序和存储已迁至 routing。账号关联与用户权限清理使用 account/postgres、identity/postgres 的同连接参与方法，原有原子范围及 outbox 失败语义保持。高级调度参数的值、复制、校验和合并迁至 scheduler/policy，运行时设置读取和调度执行仍留 S07。

app 持有唯一 GroupStore/ChannelService/GroupAdmin，Key、身份管理、订阅展示和 billing 查价直接投影新实现；移除对应旧仓储桥接。旧 AdminService 的已迁方法只委托；只被 unit 测试调用的私有兼容入口移入测试文件。分组 CRUD/复制/排序 HTTP 与分层 DTO 已迁；用量、容量、关联资源与 Live 能力端点仍由旧外壳提供，随后继续拆分。

验证：[跨模块定向 unit](baseline/S06/routing-consumers-unit-2.result.json)、[真实存储](baseline/S06/routing-group-storage-integration-2.result.json)、[HTTP/DTO](baseline/S06/routing-group-http-unit.result.json)均退出 0；[Wire](baseline/S06/routing-group-http-wire.result.json)由手写装配生成。迁移引入的 Group 协议查询完整投影造成一次分配，已改为仅投影协议集合，原零分配断言通过。原指针同一性断言迁至新核心测试，全部断言保留；高层渠道夹具通过读取入口建立配置，不再直接修改私有缓存。

本阶段仍在实施中：依赖门禁正在增量校验；渠道嵌套数据隔离、RoutePlan、可请求模型/市场/容量/探测 runner、account 的全部管理与维护迁移以及全量收尾尚未完成。本记录不是 S06.2 或阶段完成声明。

渠道隔离历史缺陷：原 HEAD 的 [两个测试](baseline/S06/original-channel-snapshot.result.json)复现嵌套 JSON 数组和发布输入共享；新实现递归复制数组元素并在缓存发布时复制渠道与分组平台表。该变更使已发布快照不再受外部修改影响，不改变存储/JSON 格式、查价顺序或缓存 TTL。回退该修复会恢复配置副本污染风险。

### S06.2 · 2026-09-12 · 分组主动探测

探测 runner、重试和最终结果编排已迁至 routing；PostgreSQL 租约/保存/聚合在 routing/postgres，cron 日历技术在 routing/provider。旧平台选择和账号测试经 app/legacybridge 投影，执行层的 httptest 背景模拟仍待 S06.3 随账号 Test/EventSink 接口替换。

[原 HEAD 停止后启动复现](baseline/S06/original-probe-lifecycle.result.json)失败；新实现设置停止屏障，取消维护、领取及尝试上下文，StopContext 等待在途和调度器，超时如实报告。[原轮次/重试及直接消费者](baseline/S06/routing-probe-tests-2.result.json)、[定向 race](baseline/S06/routing-probe-race.result.json)、[真实 PostgreSQL](baseline/S06/routing-probe-integration.result.json)通过。新增存储证据包括双并发领取仅一方获租、租约到期再认领、保存下一次时间失败使已插入结果回滚，成功后释放租约并取得聚合样本。原 SQL、锁字段、60 秒 cron 边界、每轮最多五个 worker 和结果保存顺序保持；关闭取消是本阶段明确修正。

### S06.3 · 2026-09-12 · 账号写入缺陷复现与首批修复

[原 HEAD 刷新复现](baseline/S06/original-refresh-credentials-race.result.json)在 OpenAI、Anthropic、Gemini、Antigravity 四组旧凭据交换/管理员更新交错中均覆盖新凭据。新增 account 的条件写入契约及 account/postgres 的凭据比较/行锁事务，暂通过旧存储回调保留原凭据清理与 outbox 编码，后续随完整账号存储搬迁改绑；不引入版本列。非 Grok 刷新已使用该接口，比较失败只读取当前状态，不再次交换。Grok 原有 CAS、回读、缓存清理和错误隔离路径保持。

[刷新及直接消费者 unit](baseline/S06/account-refresh-cas-unit-2.result.json)通过；[PostgreSQL 同连接/原子性](baseline/S06/account-refresh-cas-integration.result.json)验证成功保存、管理员修改和禁用后的拒绝、外层事务提交前不可见及回滚、outbox 失败整体回滚。

[原 HEAD 普通编辑复现](baseline/S06/original-account-runtime-overwrite.result.json)证明名称编辑可清掉并发 last_used_at/限流数据。已从普通 Update 中移除 last-used、限流、过载及会话窗口七类字段的回写，保留其专门运行写入口；[AccountRepoSuite 与新增回归](baseline/S06/account-runtime-fields-integration-2.result.json)通过。状态、凭据、配置与健康的完整字段意图拆分仍待账号管理迁移完成，不能将本次窄修复当作所有写入竞争均已解决。回退这两项修复会恢复对应覆盖风险。

当前普通定向 lint 已回到 [原有一项 account QF1001](baseline/S06/routing-current-lint.result.json)；unit/integration 的全量诊断比较和门禁正反夹具尚待阶段收尾。

追加：[真实刷新公开入口 integration race](baseline/S06/account-refresh-integration-race.result.json)通过。测试在交换阻塞期间通过真实 PostgreSQL 提交管理员新凭据，再放行旧响应，验证返回当前状态、旧结果未写入且交换次数为一；同批保留外层回滚及 outbox 原子性测试。

### S06.2 / S06.3 · 2026-09-12 · 容量与运行配置

分组容量聚合迁至 routing，保留批量/逐组选择、稳定顺序、重复账号计数边界和辅助计数失败语义。account 提供无凭据、无原始 Extra 的 CapacitySnapshot，并拥有会话数/RPM/窗口阈值的纯配置读取；存储读取和 OpenAI 配额派生资格暂由旧账号能力投影，待账号存储与健康迁移改绑。[定向 unit](baseline/S06/routing-capacity-tests.result.json)与 [Wire](baseline/S06/routing-capacity-wire.result.json)通过；未宣布账号用例整体迁移完成。

### S06.4 · 2026-09-12 · 分组 HTTP 收口

分组全部 HTTP 端点已进入 routing/httpapi，原路径和路由顺序保持；handler 聚合字段直接引用新类型，旧 GroupHandler 只剩别名。关联 Key 列表转由 apikey 管理接口提供，分组倍率/RPM 配置的剩余规则进入 billing.GroupRateAdmin；旧 AdminService 相应方法只委托。用量和 Live 能力使用 app/legacybridge 的读取投影，分别交接 S08/S09。

[Wire](baseline/S06/routing-full-group-http-wire.result.json)与 [HTTP/DTO/业务定向 unit](baseline/S06/routing-full-group-http-tests.result.json)通过。该批只完成分组 HTTP；市场、账号和代理数据交换的后续工作及整个阶段验收仍待执行。

### S06.3 · 2026-09-12 · 账号记录纯规则与共享值

账号记录的纯字段读取、配置、窗口与阈值规则已迁入 account.Record；旧 Account 按每个方法需要的字段投影并委托，保留旧实体的执行临时状态和未迁平台能力。Record 的凭据/Extra 不直接序列化，日志只输出账号 ID；时区加载由外层输入。关联实体和完整账号存储/管理仍待后续批次迁移，Record 尚不等同于阶段要求的对外 AccountSnapshot。

[账号、额度、模型映射、刷新和直接消费者 unit](baseline/S06/account-record-unit-2.result.json)通过。分组的无递归值形状及唯一深复制进入 routing/accessview，根 routing.Group 在该值形状上继续拥有规则；推理映射值复用同一叶子定义。[共享值及消费者 unit](baseline/S06/routing-shared-value-tests-2.result.json)通过。

阶段完整计划前缀 SHA256 仍为 `8f430b3cd642eb1b84b57614af5c7acd1cb82f0653fd159006539b78f49b282e`；本轮核对 SQL 309、Ent 379、冻结资料 1628 和其他任务文件 48 项均无内容变化。

### S06.3 · 2026-09-12 · 账号存储接通

account/postgres.AccountStore 已接通创建、复制、单条/批量读取、关联装载、列表/资格筛选、刷新分页、普通更新和凭据写入。app 持有唯一实例，旧 repository 只对已迁方法做类型投影；其他写入方法继续逐项迁移。原 outbox 编码与调度缓存发布留在旧 publisher，通过 app/legacybridge 调用，不在新账号存储复制缓存规则。

[读取与创建 unit](baseline/S06/account-data-unit.result.json)、[真实 PostgreSQL](baseline/S06/account-data-integration.result.json)、[查询/分页 PostgreSQL](baseline/S06/account-queries-integration.result.json)与 [查询 unit](baseline/S06/account-queries-unit.result.json)通过。原 HEAD 的 [Ent 嵌套凭据投影复现](baseline/S06/original-account-entity-copy.result.json)失败，新记录及关联图采用深复制，保留 nil/空、数值类型和循环非法输入的 JSON 拒绝路径；[复制 race](baseline/S06/account-clone-race-current.result.json)通过。

截至本条，AccountSnapshot、完整账号管理/HTTP、模型市场与 RoutePlan、刷新/健康/测试/Deferred 的所有权迁移及全量收尾仍未完成，roadmap 保持实施中。


### S06.3 · 2026-09-12 · 账号存储收口与刷新协调器

账号错误/限流/会话窗口/到期状态、账号绑组、批量补丁、CN 快照和 Ollama 整组会话/CAS/到期筛选已迁入 AccountStore。事件端口由 app 显式绑定原 scheduler outbox 编码及快照 publisher，保留原同步/尽力通知差异；删除已无消费者的旧存储私有辅助函数。关联组查询仅依赖纯 accessview，代理连接身份由 egress 值类型唯一投影。非 Grok 凭据比较已直接绑定新 AccountStore 的 UpdateCredentials，不再经过旧存储回调。

账号消费递增与重置 SQL 进入 billing/postgres.AccountUsageParticipant；旧入口暂保留原事件发布。真实 PostgreSQL [同连接参与测试](baseline/S06/account-usage-participant-integration-2.result.json)验证配置与消费修改在提交前对外不可见、外层回滚全部恢复、参与方法不产生 outbox，以及重置不清除长期错误/过载/停调状态。完整账号管理用例及配置字段意图仍需继续迁移。

[账号状态存储](baseline/S06/account-state-integration.result.json)、[Grok 条件写入](baseline/S06/account-conditional-state-integration.result.json)、[批量/CN 存储](baseline/S06/account-patch-integration.result.json)、[Ollama 共享身份组与旧探测拒绝](baseline/S06/account-ollama-integration-2.result.json)、[配置与直接消费者 unit](baseline/S06/account-patch-unit.result.json)均退出 0。本批 [normal lint](baseline/S06/account-store-lint-2.result.json)仅原有 account QF1001；后续新增刷新代码继续独立验证，不能复用此前 lint 结果作为收尾。

[原 HEAD 交换快照复现](baseline/S06/original-refresh-attempt-snapshot.result.json)证明嵌套凭据在执行器修改后污染交换前快照；新实现复用账号深复制。初次修复的定向 race 揭示旧 nil 凭据会转换为空对象的兼容细节，已恢复该归一语义，保留全部原断言。回退此修复会恢复对旧凭据版本的错误比较风险。

统一 OAuthRefreshAPI 的锁表、重读、成功 CAS、竞争恢复和版本写入已迁入 account；旧平台消费者只通过原形状委托新实例。app 直接绑定 AccountStore、原 token cache、观察回调与旧平台策略投影；[Wire](baseline/S06/account-refresh-lifecycle-wire.result.json)由手写装配生成。[迁移后刷新及账号 unit](baseline/S06/account-refresh-core-unit-2.result.json)通过。白盒锁/默认 TTL 测试跟随新核心，旧平台及池健康行为测试继续从旧消费者执行。

协调器新登记关闭责任：阻止新认领，取消锁等待与交换，预算内等待并固定停止结果；迟到响应禁止持久化。关闭/race、真实刷新事务及新门禁仍在验证中。周期分页、QPS/重试、手动刷新、完整健康/测试 EventSink/Deferred、AccountSnapshot、RoutePlan、市场、数据交换 HTTP 与阶段全量验收均未完成；roadmap 保持 6 / 17、S06 实施中。


追加：统一刷新协调器 [定向 race](baseline/S06/account-refresh-core-race-2.result.json)取得 45 条通过事件，[真实 PostgreSQL 刷新/账号/Ollama 回归](baseline/S06/account-refresh-current-integration.result.json)取得 112 条通过事件，均无失败或跳过。关闭测试包括等待者取消、拒绝新调用、不响应取消时的停止超时、固定停止结果和迟到成功不写入。首次 race 报告的兼容层锁前完整复制问题已修复：锁前仍只读取原缓存键与账号 ID，完整值在锁内重读后复制。

[不可变输入复核](baseline/S06/account-store-invariants.json)确认 309 个 migration、379 个 Ent 文件、1628 个前阶段冻结文件及 48 个其他任务文件摘要未变，计划原文前缀摘要保持不变。


### S06.3 · 2026-09-12 · Deferred 与账号到期维护

[原 HEAD 的 Deferred 复现](baseline/S06/original-deferred-pending-activity.result.json)证明失败批次会把同账号随后入队的新活动时间覆盖为旧值。唯一队列与批量写回已迁入 account.DeferredService；取批次与入队共用锁，失败只回填空位。停止取消新周期和等待者，等待在途写入后做最终 flush，最终写入同时受原十秒限制与剩余退出预算约束；超时固定为未完成结果。旧网关/Ollama 测试改从实际批量写入端口观察，保留“指定账号记录活动/其他账号不记录/取消不记录”的全部断言。

[Deferred 及直接消费者 race](baseline/S06/account-deferred-race-2.result.json)通过；验证最终 flush 串行、失败数据保留、后到活动不被覆盖、阻塞写入时的有界停止和停止后拒绝入队。原时间轮实例和 deferred:last_used key 保持，旧 service 仅为别名/构造转接。回退此修复会恢复活动时间回填覆盖风险。

[原 HEAD 到期 worker 复现](baseline/S06/original-account-expiry-restart.result.json)证明 Stop 后 Start 仍扫描。扫描和生命周期已迁入 account.ExpiryService，app 注入唯一 AccountStore、时钟和原一分钟周期；保留立即首轮、原五秒扫描预算，新增一次启动、取消、等待和停止后不重开。新 [Wire](baseline/S06/account-expiry-wire.result.json)仅生成应用装配；到期 worker 与上述运行资源正在进行合并定向 race。S06 整体仍未完成。


### S06.3 · 2026-09-12 · 管理入口与协议配置继续收口

账号管理的列表/筛选/读取、影子级联删除、错误恢复、调度开关及额度重置十个入口已进入 account.Admin；app 注入新 AccountStore、唯一 billing AccountUsageStore 与原运行阻断接口。旧 AdminService 只投影并委托这些方法，创建/编辑/批量、影子创建和隐私操作继续拆分。[管理及直接消费者 unit](baseline/S06/account-admin-first-unit-3.result.json)通过；新 Deferred 曾使零值测试夹具入队时缺失时钟，已恢复原零值入队行为，未删除网关结算断言。

管理输入类型、持久化秘密清理、敏感字段缺省保留、影子凭据允许字段及 Header 类型适用性已归 account；[相关管理/DTO unit](baseline/S06/account-config-values-unit.result.json)通过。通用 Header 校验仍在 egress，HTTP Header 实际写出留旧执行 Adapter。

协议配置、认证方式投影、原生集合解析/校验、旧工作负载与文本路由转换已迁至 account，复用 capability 唯一目录；pkg/openai_compat 仅转接，wire 枚举仍由 protocol/openai 拥有。原转发副本的 resolvedProtocol 优先读取留旧入口，以显式旧协议变体传入纯配置读取，不进入 Record 或缓存。保存路径保留显式空集合、非字符串拒绝、CN 分协议地址迁移、旧字段清理及原错误 reason。本批编译通过，行为与 lint 验证正在执行；不能将此记录视为 S06 收尾。


### S06.3 · 2026-09-12 · 创建配置与隐私链路

用量适配器名称/展示目录、配置解析、CN 自动选择及错误对象已由 account 唯一提供；具体 HTTP 适配器只绑定实现，URL 格式检查进入 egress 并保留原宽严差异。[用量配置及管理入口 unit](baseline/S06/account-usage-config-unit-2.result.json)、[CN/Grok/Header 校验](baseline/S06/account-input-rules-unit.result.json)通过。

账号日/周固定窗口日期规则迁至 billing，account 只投影历史 JSON 与应用结果；时区加载、当前时间由外层传入。新文件首次以 _windows.go 结尾而被 Go 当成 Windows 专属文件，编译已指出并更名为 account_window_rules.go / config_quota_window.go；新增文件的最终构建选择继续按阶段计划核对。[窗口及账号原有 unit](baseline/S06/account-quota-windows-unit-2.result.json)、[创建值/指纹/窗口组合验证](baseline/S06/account-create-record-unit-2.result.json)通过。严格正数限额保留 NaN 不触发窗口重置的原比较语义，日/周算法不替代其它资金窗口。

指纹 seed 校验、清理和输入规则已进入 account，随机生成通过函数注入；账号创建前的纯值构造已委托 BuildAccountForCreate。创建、编辑与复制流程的 I/O 编排仍继续迁移。

[原 HEAD 隐私代理复现](baseline/S06/original-privacy-proxy-bypass.result.json)通过本地真实 HTTP 证明管理员普通/强制隐私设置在代理回源失败后各发起一次直连；[后台刷新同类复现](baseline/S06/original-refresh-privacy-proxy-bypass.result.json)覆盖回源错误及缺失代理读取端。新的 account.PrivacyService 统一管理与后台入口，明确区分未配置代理与代理不可用；后者不执行平台请求、不写入隐私状态。具体平台交换仍通过 app/legacybridge 调用旧 OpenAI/Antigravity Adapter，S09 改绑。各入口原有的缓存写入、返回 mode、日志以及内存更新差异保留，当前正在验证。回退该修复会恢复绕过显式代理的风险。

Project Doc 的账号协议配置章节已同步实际归属；OpenAI 文档旧 v35 说明校正为代码现有 v40，未改变缓存版本。废弃 Responses 探测相关注释已校正，不引入新的探测或配置行为。阶段仍在实施中。


### S06.3 · 2026-09-12 · 创建、复制与影子用例

account.Admin 的创建流程已接通唯一 AccountStore、routing 默认分组与混合渠道检查、原 Qoder 凭据准备/验证端口，以及 app 生命周期任务跟踪。创建后隐私使用独立账号值；[快照与维护 race](baseline/S06/account-create-snapshot-race.result.json)通过。规范化产生的具名协议 ID 切片也纳入深复制，防止仅支持 []any 的复制遗漏实际生产类型。

[创建及管理兼容 unit](baseline/S06/account-create-flow-unit-2.result.json)通过。原存储参数与返回值指针同一性的断言跟随创建实现迁至新核心，断言保留；旧 HTTP/服务仍通过投影验证外部形状。

复制/恢复、配置深复制、运行字段过滤、名字 rune 截断和操作 ID SHA-256 已进入 account；账号与关联仍通过 AccountStore 的同一事务保存。[复制及直接消费者 unit](baseline/S06/account-duplicate-unit-2.result.json)通过。影子创建、分组存在性校验和代理传播正在迁移：存在性规则由 routing 执行，模型别名仍每次由原平台表投影，未建立第二份缓存。普通更新、CRS、代理传播的完整字段写权限仍待后续收口。

新的 [隐私用例/HTTP/直接消费者](baseline/S06/account-privacy-unit.result.json)通过，实际修复了已复现的显式代理失败后直连；原管理与刷新入口的失败日志、返回 mode、写入和内存更新差异均保留。必要的 PostgreSQL、完整生命周期、门禁夹具及全量收尾仍未完成，roadmap 不变。


### S06.3 · 2026-09-13 · 编辑、批量规则与配置写权限

账号编辑、Extra 增量、批量预校验、筛选分页与影子守卫进入 account.Admin；旧 AdminService 只投影和委托。批量 OpenAI 配置规则唯一进入 account，保留原来先验证全部目标再一次写入的边界。[管理、平台及 HTTP unit](baseline/S06/account-admin-edit-unit-4.result.json)通过；[完整本批调用方 unit](baseline/S06/account-config-callers-unit.result.json)通过。Qoder 站点识别及 PAT 实际验证继续由旧平台端口承担，迁移未把网络调用放入数据库锁内。

上游用量身份散列、CN 身份及 Ollama 身份规则由 account 拥有；Ollama 官方 URL 格式校验进入 egress。散列保留原 JSON 字段顺序、失败降级和 SHA-256 字节，具体平台的 URL 解析仍由旧执行层传入。原 OpenAI endpoint 判断也进入 account，Grok 媒体资格通过惰性函数投影；原 QF1001 从 service/account.go 对应分支迁到 account/endpoint_rule.go，规则及消息未改变。

[原 HEAD 基础更新复现](baseline/S06/original-account-config-authority.result.json)和[管理员更新复现](baseline/S06/original-admin-config-authority.result.json)在真实 PostgreSQL 上证明名称、Extra、状态及脱敏凭据编辑会覆盖读取之后的新 API Key。新 ConfigurationChange 声明实际字段意图；AccountStore 在原 Ent 事务中先锁定最新行，再合并本次字段。敏感子键未提供时继承锁内最新值；显式清空/旋转保持原输入语义。普通修改保留并发 error/schedulable、消费累计及专用观测，影子代理只写代理，CRS 继续显式写其来源状态/调度字段。平台/运行专用入口仍保留各自写权限，不改缓存格式或 SQL schema。

[存储回归](baseline/S06/account-edit-storage-integration.result.json)、[复制与窗口 race](baseline/S06/account-config-contract-race.result.json)、[配置同连接/outbox 回滚 integration race](baseline/S06/account-config-integration-race-2.result.json)通过；真实事务内可见配置及 outbox，外层回滚或 outbox SQL 失败后均无残留。固定窗口以锁内最新累计按原两个取时点分别计算，未跨窗口不清消费。回退这一修复会恢复普通编辑覆盖新凭据及消费/健康状态的风险。

未进入生产 Wire 图的旧 AccountService 基础表面已委托 account.BasicAccounts；原查询顺序、错误包装和测试断言保留，不额外构造生产实例。[基础与旧调用方 unit](baseline/S06/account-basic-unit.result.json)通过。其兼容表面在 S15 清零删除；真实管理生产入口仍是唯一 account.Admin。相关 Project Doc 已同步当前写权限和事务结构。

[Wire 生成](baseline/S06/account-edit-wire.result.json)通过。本批 scoped normal lint 清理后只剩原 QF1001，见[诊断](baseline/S06/logs/account-edit-scoped-lint-4.log.gz)；未增加忽略规则。各标签实际测试事件见[事件摘要](baseline/S06/account-edit-event-summary.json)。阶段继续实施：AccountSnapshot、RoutePlan、市场、完整维护/测试/刷新编排及最终全量门禁验证仍未完成，roadmap 保持 6 / 17。


### S06.2 / S06.3 · 2026-09-13 · 候选协议快照与派生值隔离

不含凭据的 account.AccountSnapshot 已用于实际 ResolveProtocolRoute 入口；routing.Plan 复制分组协议策略，ResolveCandidate 为每个 fresh/数据库候选重新计算原生或单步 fallback。[候选与旧协议 unit](baseline/S06/routing-candidate-unit-2.result.json)、[快照/策略隔离 race](baseline/S06/routing-candidate-race.result.json)通过。当前只接管候选协议部分，完整模型链、市场及最终分组准入仍继续迁移，不能据此宣布 RoutePlan 全部交付。

[原 HEAD 模型映射复现](baseline/S06/original-model-mapping-snapshot.result.json)与[Header 覆写复现](baseline/S06/original-header-override-snapshot.result.json)均出现返回 map 污染后续读取及并发读 race。新 account.ResolveModelMapping / Record.HeaderOverrides 使用纯规则并返回独立值，删除旧账号中读时更新的派生缓存字段，不引入替代共享缓存。原实现每次命中仍遍历/排序输入以校验签名；本次直接解析并隔离输出，保留原配置替换、长度变化、原地值变化的可见性。外层按需提供 Antigravity/Google One 默认目录，没有新别名表；Header 类型裁决由 account 拥有，安全值规则由 egress 拥有。

[模型映射 race](baseline/S06/account-model-mapping-race.result.json)和[模型/Header/通配行为 race](baseline/S06/account-derived-values-race.result.json)通过。回退会恢复返回 map 共享及读取时写缓存的竞争风险；数据库、Redis、调度缓存版本和 HTTP 载荷均未变。相关协议、模型目录与传输安全 Project Doc 已同步实际归属。

[本批不变量](baseline/S06/account-edit-invariants.json)确认 SQL 309、Ent 379、既有冻结资料 1628、其他任务文件 48 项摘要不变；完整计划前缀摘要不变。HEAD 仍为实施起点，索引无变更，git diff --check 通过。全阶段仍在实施中，不提交。


### S06.2 · 2026-09-13 · 可请求模型与公开市场

account 已拥有模型支持、最终白名单及可请求模型配置规则；原生平台/Qoder 特有资格通过惰性端口传入，单次判断复用同一映射快照。[模型/账号/平台 unit](baseline/S06/account-model-policy-unit-2.result.json)通过。routing.RequestableResolver 接管候选合并、渠道限制、R → C → U、歧义价格以及渠道失败回退；旧 Gateway 只调用新规则并提供原数据/平台观测入口。[可请求模型回归](baseline/S06/routing-requestable-unit.result.json)通过。

公开市场编排进入 routing.Marketplace，app 直接注入新分组 Store、settings.Store、唯一 billing.PriceResolver/Calculator、容量和可用性接口。平台默认目录、专有模型资格/限流观测经 app/legacybridge 接入，桥接不持有额外共享缓存。billing.PublicQuote 统一展示报价，保留免费 Fast 适用范围与副本修改；[报价回归](baseline/S06/marketplace-quote-unit.result.json)通过。

HTTP/DTO 进入 routing/httpapi，生产 Wire 直接绑定新 MarketplaceHandler；首页公开计数通过窄端口委托 Dashboard，S08 改绑。旧 MarketplaceService 和 handler 名称只保留兼容构造/投影，生产图不额外构造旧实例。[Wire](baseline/S06/routing-marketplace-http-wire.result.json)、[HTTP/app/unit](baseline/S06/routing-marketplace-http-unit.result.json)、[普通标签回归](baseline/S06/routing-marketplace-normal-2.result.json)通过。既有市场测试实际无 unit 构建约束，迁移时的测试转接已修正为普通 _test.go，未改变原测试标签或断言。

[测试事件](baseline/S06/routing-marketplace-events.json)记录原预取一次、全局优先级排序、空结果、不歧义报价及能力元数据等实际执行结果。[scoped normal lint](baseline/S06/logs/routing-marketplace-scoped-lint-3.log.gz)只剩迁移后的原 QF1001。旧网关模型列表缓存和模型可用性诊断仍继续迁移；AccountSnapshot/RoutePlan 的完整模型链、账号 HTTP/导入、刷新/维护与阶段全量验证尚未完成。

配置写权限复核另外补齐了锁内继承最新秘密后再次执行原 SSO/密码/cookie 清理的契约，防止继承操作重引入历史残留；[定向 race](baseline/S06/account-configuration-sanitize-race.result.json)通过。


### S06.2 / S06.3 · 2026-09-13 · 模型读取与计划测试生命周期

模型列表短缓存、nil/空集合、键与按维度失效规则进入 routing.ModelList；app 在旧 Gateway 构造前创建唯一实例并显式注入，兼容字段只引用同一缓存，原进程指标也只保留一份。构造不启动 janitor，仍由原应用时间轮清理。[Wire](baseline/S06/routing-model-list-wire.result.json)、[模型缓存/市场 unit](baseline/S06/routing-model-list-unit.result.json)、[缓存 race](baseline/S06/routing-model-list-race.result.json)通过。routing.ModelAvailability 接管持久可用性诊断，保留 general/OpenAI 兼容入口的不同查询范围、simple/mixed 规则、渠道映射时机和失败时的保守 503 结果；[诊断与 HTTP 分类 unit](baseline/S06/routing-model-availability-unit.result.json)通过。

计划测试类型、CRUD/结果保留、SQL、HTTP 与执行器已进入 account。无消费者的旧计划仓储、handler 和构造入口删除，实际生产图直接绑定新实例。[Wire](baseline/S06/account-scheduled-final-wire.result.json)通过。原样迁移文件首次取名 scheduled_test.go，被 Go 当成测试文件排除；已更名 scheduled_plans.go，旧仓储 ProviderSet 的内联注册也一并删除，避免生成图重复绑定。

[原 HEAD](baseline/S06/original-scheduled-runner-stop.result.json)及[复现代码](baseline/S06/original-scheduled-stop-test.go.txt)证明 Stop 后仍能启动 cron。新执行器保留每分钟与十秒偏移、十个并发 worker、每轮五分钟预算和原轮次策略，停止则取消偏移/槽位等待与在途 context，永远阻止新启动。cron Stop 或在途任务忽略取消时有界返回具体未完成错误；后续调用复用首次停止结果，不能把迟到完成改报为成功。

[执行器及真实 cron race](baseline/S06/account-scheduled-runner-race-2.result.json)通过，覆盖构造无启动、重复启停、偏移取消、部分启动清理、阻塞端口/在途超时及等待。新 PostgreSQL 夹具先修正了不可表示的纳秒时间差，按数据库微秒精度检验边界；[真实存储验证](baseline/S06/account-scheduled-storage-integration-2.result.json)通过，覆盖到期边界、默认保留数、裁剪及级联删除，未修改 SQL/schema。回退停止修复会恢复 Stop 后启动、十秒不可取消等待和无法报告在途超时的风险。后台平台测试本身仍在旧 AccountTestService，后续改为直接事件输出；本阶段未完成。


### S06.3 / S06.4 · 2026-09-13 · 测试事件与独立展示值

即时测试、计划测试和分组后台探测已使用唯一 account.TestService。核心不接收 Gin/HTTP，请求验证、类型选择、事件状态、错误聚合和写失败取消由核心持有；HTTP 仅按原 SSE 格式输出，后台直接消费类型事件。旧平台执行继续归 S09，已移除生产测试代码内的 Gin 与 httptest，不改变自动探测标记、上下文恢复或固定 Compact 载荷。

[原 HEAD 写失败复现](baseline/S06/original-account-test-write-error.result.json)证明旧流处理在 SSE 写失败后仍返回成功；新入口返回写入错误并取消平台执行，保留首个失败及部分结果。[unit](baseline/S06/account-test-events-unit.result.json)、[normal](baseline/S06/account-test-events-normal.result.json)、[事件和 Header/Flush race](baseline/S06/account-test-events-race.result.json)、[Wire](baseline/S06/account-test-events-wire-2.result.json)通过。旧测试入口只在 _test.go 提供转接，既有断言不变；平台客户端识别保留惰性 Header 读取与原判断顺序。

账号 DTO 与凭据脱敏唯一迁入 account/httpapi/dto，代理 DTO 分离为 egress/httpapi/dto 叶子，旧 DTO 类型别名与映射委托保留原输出形状。[原 HEAD DTO 复现](baseline/S06/original-account-dto-snapshot.result.json)证明嵌套模型映射/Extra 被展示调用方修改后污染原实体；新投影隔离可变值。[DTO/脱敏/Ollama/代理 unit](baseline/S06/account-dto-unit-5.result.json)通过。账号队列模式的纯字段读取进入 account.RuntimeConfig，队列执行仍归 S07/S09。相关 Project Doc 已同步当前事实，完整账号 HTTP 尚未全部迁出。

### S06.3 · 2026-09-13 · API Key 用量查询编排

只读查询的身份预读、指纹 singleflight、四个并发槽、批量错误收集、归一化校验及查询指标迁入 account.UpstreamUsageService。app 直接绑定唯一 AccountStore 和旧供应商执行端口；旧构造/方法仅兼容转接，生产图不额外构造旧核心。供应商固定协议、URL/TLS/代理及 Header 执行仍保留原 Adapter，等待 S09；查询不修改健康、调度或账单。

[原 HEAD 结果共享复现](baseline/S06/original-upstream-usage-snapshot.result.json)证明合并的两个请求拿到同一可变金额对象，修改第一个结果污染第二个。新核心只合并网络操作，每个等待方复制结果，保持浮点类型、nil/空集合和 JSON。[迁移后的原用量/刷新/HTTP unit](baseline/S06/account-upstream-queries-unit-2.result.json)通过；定向 race 和新增停止契约正在执行。查询与刷新共享账号活动跟踪实现，保留等待方独立取消，在应用 Stop 时取消并等待已脱离等待方的工作，超时固定记录未完成结果。

回退本批历史修复将分别恢复写失败被吞、DTO/合并查询结果共享污染风险。本阶段继续实施；全量验证、门禁夹具、完整维护/刷新/HTTP 与最终清单尚未完成，roadmap 保持实施中，不提交。


### S06.3 / S06.4 · 2026-09-13 · 只读查询与国产监控收口

用量查询和即时测试 HTTP 已进入 account/httpapi，原 URL 直接绑定新 handler；旧管理员方法只委托，实际核心实例由 app 注入。[HTTP/app/unit](baseline/S06/account-query-http-unit-2.result.json)、[Wire](baseline/S06/account-query-http-wire.result.json)通过。[查询、刷新和 DTO race](baseline/S06/account-upstream-queries-race.result.json)取得 75 个通过事件，包括等待方独立取消、Stop 持有脱离等待方的共享查询、阻塞执行有界退出和首次超时结果保持。

CN 周期监控、身份复核、成功快照保留、余额阈值及恢复原因规则已进入 account.CNUsageMonitor。app 直接注入唯一 AccountStore/UpstreamUsageService，Redis/DB 技术锁仍使用原实现，锁名、owner、TTL、竞争跳过、故障回退及无后端行为保持。默认关闭、首个完整周期后执行和有界停止有独立测试。旧后台类及无消费者构造已删除；白盒的“无后台 context”和“Stop 后 Start”断言随生命周期拥有者迁至 account，旧供应商协议断言继续覆盖真实原 Adapter。

[原 HEAD 交错复现](baseline/S06/original-cn-monitor-interleaving.result.json)确认 Stop 后仍启动循环，以及查询后管理员换凭据仍被旧余额结果暂停。新停止屏障永久拒绝新认领；健康写入使用现有 updated_at 的条件 SQL，恢复额外核对原原因，未新增 schema、锁或缓存协议。过期条件未命中不发布成功失效，成功写入后的 outbox 失败仍保持原尽力语义。回退会恢复旧观测暂停新身份、恢复覆盖其它停调及停止后启动风险。

[监控和原供应商 unit](baseline/S06/account-cn-monitor-unit-2.result.json)通过；[监控与用量 race](baseline/S06/account-cn-monitor-race.result.json)通过，101 个通过事件。[真实 PostgreSQL](baseline/S06/account-cn-monitor-integration-2.result.json)验证 stale 拒绝、当前身份写入、更长 cooldown 保留、原因保护与 outbox 失败时已提交状态保留。首次新夹具使用结构比较 time.Time，时刻一致但 Location 不同；已改为比较同一时刻，未改生产时间规则。

[实际测试事件清单](baseline/S06/account-events-dto-usage-monitor-events.json)同时记录本批通过/失败/跳过。DTO 选择中有一个原 WS `other_event_type` 子场景跳过，未计作行为通过，其他对应场景与后续全量验证分别登记。

### S06.1 / S06.4 · 2026-09-13 · 代理文件导入导出

代理导入/导出的筛选分页、账号数排序、身份键、状态同步、备援名称映射、批内引用、部分成功及导入后延迟探测进入 egress.ProxyTransfer。HTTP 只读取参数与输出文件 envelope，app 直接提供新 handler 和同一生命周期任务拥有者。旧独立构造仍委托原后台任务入口；生产不再经过旧代理 handler 外壳。

account/transfer 保存账号与代理共享备份文件的纯值和自定义 JSON 解码；egress 拥有代理条目和值校验。账号字段省略/null 标记保持，代理接口仍先校验完整 envelope，未改为宽松忽略无法解码的账号内容。管理员显式备份仍含原凭据，普通 DTO 脱敏不改变该行为；账号备份完整用例继续迁移。

[原导入导出/代理 unit](baseline/S06/egress-transfer-unit-2.result.json)、[Wire](baseline/S06/egress-transfer-wire.result.json)通过。[当前 scoped normal lint](baseline/S06/logs/s06-transfer-monitor-scoped-lint.log.gz)只剩迁移后的原 QF1001；没有新增忽略规则。S06 尚未完成，继续处理账号导入、完整管理 HTTP、刷新/用量/维护、完整策略投影及最终验证。


### S06.3 / S06.4 · 2026-09-13 · 完整备份用例与按需导入探测

账号备份读取、影子排除、逐项导入、模板读取时机、身份提示补齐及隐私后置处理进入 account.Archive。HTTP/幂等进入 account/httpapi，实际路由直接绑定 ArchiveHandler，导出仍保留原 step-up，中间件与 URL 未变。显式备份仍输出原凭据；输出和导入条目独立复制，不能回写来源记录或动态模板。OpenAI 模板值由 account/transfer 拥有，动态默认内容继续由原设置用例提供；旧 pkg/openai 的导入 ID Token 解码经 provider 端口复用，不把导入提示升级为认证，S09 退出该精确依赖。

账号/代理文件的代理部分共用 egress.ProxyTransfer 的唯一实现，保留账号导入的 created_at 分页、复用时再次读取、静默状态更新错误，以及独立代理入口的 id 分页、状态错误明细和复用项探测。未用同一“简化”策略抹平入口差异。Codex 文件导入暂经旧分页转接调用新账号读取，完整 Codex/CRS 导入仍待本阶段后续拆分。

Grok 导入的按需队列进入 account.GrokImportProbeScheduler，app 为账号与 Grok OAuth handler 注入同一实例，删除旧全局队列。Schedule 只接收无凭据的 AccountSnapshot；具体探测只返回脱敏观测投影。保留三 worker、64 个排队项、账号去重、排队不消耗单次 25 秒预算及失败日志脱敏。Stop 取消未领取的尽力项，取消并等待在途；迟到完成不改变首次超时结果，未把取消项记作探测成功。旧白盒队列断言随实现迁入 account，HTTP 供应商替身与原响应断言保留。

[账号/代理/管理整包 unit](baseline/S06/account-transfer-monitor-unit-3.result.json)、[导入队列 unit](baseline/S06/account-import-probes-unit.result.json)、[队列/HTTP race](baseline/S06/account-import-probes-race.result.json)、[备份与实际路由 unit](baseline/S06/account-archive-http-unit-2.result.json)、[备份和队列 race](baseline/S06/account-archive-race.result.json)通过。新增测试验证账号先读、include_proxies 后校验、代理先写、模板后读一次、逐项失败与模板/凭据副本隔离。[队列 Wire](baseline/S06/account-import-probes-wire.result.json)、[备份 Wire](baseline/S06/account-archive-wire.result.json)通过；[scoped lint](baseline/S06/logs/account-archive-import-scoped-lint-2.log.gz)只剩原 QF1001。

CN 条件健康写入另外通过[真实 PostgreSQL integration race](baseline/S06/account-cn-monitor-integration-race.result.json)。[最近不变量核对](baseline/S06/account-transfer-monitor-invariants.json)确认 SQL 309、Ent 379、冻结资料 1628、其他任务 48 项未变，计划原文摘要、HEAD 和索引保持，diff --check 通过。本阶段仍在实施中，尚需完整后台/手动刷新、OAuth 用量/Ollama维护、Codex/CRS、剩余账号 HTTP、完整 RoutePlan/EgressPolicy 和最终全量验证。


### S06.3 · 2026-09-13 · 后台刷新生命周期与基础契约

[原 HEAD](baseline/S06/original-refresh-duplicate-start.result.json)证明重复 Start 会同时启动两轮立即扫描。周期拥有者进入 account.RefreshLoop，构造不启动，Start/Stop 具有并发屏障，app 将剩余退出预算传给 StopContext；立即首轮、最小间隔回退及原日志字段保持。阻塞轮次会报告未完成，迟到退出不修改首次超时结果。[刷新/对账 unit](baseline/S06/account-refresh-loop-unit.result.json)、[生命周期 race](baseline/S06/account-refresh-loop-race.result.json)通过。

速率预约、取消等待、并发槽位唯一实现进入 account.RefreshRateGate / RefreshConcurrencyGate；旧对象只持有新实现并委托，同一平台仍复用同一组状态。后台 skipped 策略、四类失败结果及错误链进入 account，旧错误类型别名保留 errors.Is/As；导出的内部原因/状态字段使用 json:"-"，不暴露内部原因。[错误/策略 unit](baseline/S06/account-background-errors-unit.result.json)、[门槛 unit](baseline/S06/account-refresh-gates-unit.result.json)、[刷新/Grok/门槛 race](baseline/S06/account-refresh-gates-race.result.json)通过。候选分页、注册表、重试及刷新后编排仍在旧 TokenRefreshService，本项不代表完整后台迁移完成。

### S06.3 · 2026-09-13 · 旧刷新失败的身份保护

[首次原 HEAD 复现](baseline/S06/original-background-failure-identity.result.json)及[扩展复现](baseline/S06/original-background-failure-identity-expanded.result.json)覆盖 Anthropic/OpenAI/Gemini/Antigravity/Qoder、永久失败/重试耗尽和直接/统一 API 两条路径。旧失败在管理员替换凭据后仍调用无条件健康写入；[原始夹具](baseline/S06/original-background-failure-test.go.txt)与[扩展夹具](baseline/S06/original-background-failure-expanded-test.go.txt)保留。

新 RefreshFailureWriter 在 PostgreSQL 单条条件操作中比较平台、类型、状态、完整凭据、代理和调度开关；过期身份不写入、不失效或阻断，也不再次交换。普通改名仍可与失败操作并存。同身份已经有更长 cooldown 时保持原字段和计数语义；健康写入后的 outbox 继续尽力，不改成失败回滚。[真实 PostgreSQL](baseline/S06/account-background-failure-integration-4.result.json)验证全部身份维度、普通改名、更长窗口和通知失败。新夹具修正了 Ent 可空字符串的断言类型，未改存储输出。

统一 API 的后台取消路径仅在已取得交换快照时附带该快照，错误不变；请求路径返回行为保持。后台不在统一锁之前复制可能共享的调用参数；未取得快照的读取/排队失败不依据旧参数写健康。旧测试专用快照曾被误用于运行入口，普通构建检查发现后已改为生产投影，unit 通过不作为普通构建替代。

[原 HEAD 内存桥接复现](baseline/S06/original-refresh-runtime-identity.result.json)证明账号 ID 级旧阻断会影响新凭据。刷新失败现在使用 account.RefreshFailureBlocks 的凭据身份作用域，原账号级配额/容量阻断保留。写入前准备发布资格；显式清理使旧发布失效，普通临时阻断/回滚不改变这个清理代次。多份在途凭据各自保持期限，旧通知不覆盖新版本；普通无阻断请求不计算身份散列。该状态暂由唯一旧 OpenAI 网关实例持有作兼容接入，完整 account 健康编排继续收口；没有增加 Redis 字段或缓存版本。

Antigravity 请求标记清理改为在原 Extra/outbox 事务中复核凭据/代理及状态；失败写入后只清理仍属于该身份的标记，避免后置操作遗漏保护。永久失败的标记清理改为条件健康写入成功后执行，失败/过期时保留标记，这是竞态修复的明确时机变化。SQL 错误、缓存失效以及 Grok 特有的失败分类分别保持原分支，不统一改为盲目重试。

[扩展 unit](baseline/S06/account-background-failure-expanded-unit.result.json)、[后置行为 unit](baseline/S06/account-refresh-side-effects-unit.result.json)、[PostgreSQL integration race](baseline/S06/account-refresh-side-effects-integration.result.json)、[刷新及后置行为 race](baseline/S06/account-refresh-side-effects-race.result.json)、[发布代次/Grok 交错 race](baseline/S06/account-refresh-publication-race-2.result.json)通过。[scoped lint](baseline/S06/logs/account-refresh-side-effects-scoped-lint-2.log.gz)只剩原 QF1001。回退这些修复会恢复重复扫描、旧失败覆盖新健康状态、内存跨凭据阻断及后置标记清理风险。

[增量源码账本](baseline/S06/incremental-source-ledger.json.gz)记录当前改动的声明、imports、构建声明与文档锚点；这是本批快照，不替代最终 go list 实际选择、消费者和退出阶段核对。S06 仍在实施中，完整后台刷新/手动刷新、CRS/Codex、OAuth 用量/Ollama、剩余 HTTP、完整 RoutePlan/EgressPolicy 和全量验证尚未完成。


### 2026-09-13：CRS 同步、导入刷新及直接 HTTP

- `account.CRSSync` 拥有原六类来源的预览、选择、逐条创建/修改、影子约束、字段清理和统计；`account/provider.CRSClient` 复用原共享 HTTP 池、20 秒超时、URL 约束、登录/导出读取上限与错误文本。`account/transfer` 保存原 JSON 形状，代理匹配/创建规则由 `egress.MatchOrCreateCRSProxy` 拥有。
- app 直接注入唯一 AccountStore、ProxyStore 和 OAuthRefreshAPI；服务兼容层只保留原平台交换及投影，生产 `/sync/crs` 与 `/sync/crs/preview` 直接绑定新 HTTP。Claude/OpenAI/Gemini 导入均保留原尽力刷新时机，不改变部分成功计数；不启动原来没有的代理探测。
- 历史修复：在原 HEAD 的真实 CRS 同步入口用本地来源 HTTP 和可控 OAuth 交换复现管理员新凭据被迟到刷新覆盖。证据：[原 HEAD 测试](baseline/S06/original-crs-stale-credentials-test.go.txt)、[失败日志](baseline/S06/logs/original-crs-stale-credentials.log.gz)、[命令](baseline/S06/original-crs-stale-credentials.result.json)。新 `RefreshImported` 与后台共享进程/Redis 锁和停止跟踪，锁内重新读取并比较导入身份，交换后条件写入；取消不写入，冲突只重读，不再次交换。保留导入对非 active 状态的显式尝试及原 `_token_version`，未套用后台过期资格。
- 扩大刷新测试发现上一批背景快照返回影响旧取消形状，已改为后台显式 `RefreshWithAttemptSnapshot`；普通 API 继续返回原 nil 取消结果，原断言未删除。
- 已通过：[定向 unit](baseline/S06/account-crs-boundaries-unit-2.result.json)、[刷新 race](baseline/S06/account-crs-refresh-race.result.json)、[真实 PostgreSQL race](baseline/S06/account-crs-integration-race-2.result.json)、[Wire](baseline/S06/account-crs-wire-2.result.json)。集成覆盖成功、管理员改凭据、禁用、取消和 outbox 回滚，以及已有同连接回滚断言；无真实供应商访问。
- 旧 CRS 构造及辅助函数用于兼容测试，生产唯一实现已接入；具体 OAuth 交换 S09 退出。Codex 导入、完整后台刷新与 OAuth 用量编排、RoutePlan/EgressPolicy 完整接入和全量收尾仍未完成。S06 继续实施中。


### 2026-09-13：Codex 导入与消费者扩大验证

- `account.CodexImporter` 拥有输入解析、JWT 提示、身份索引、逐项去重、有效期、原部分成功、管理调用和失效顺序。时间和平台私钥验证由 Options 注入，Agent Identity 不读取 OAuth 有效期时钟。`account/httpapi` 保留原校验和 `admin.accounts.import_codex_session` 幂等范围，实际路由直接使用新 handler；app 复用唯一 Admin、Archive 和旧平台失效端口。
- 旧 OAuth 批量创建仍消费导入合并、保护字段和令牌摘要三项纯规则，已改为委托；其平台交换归 S09。旧 Codex 白盒断言未删除，私有辅助入口转为测试专用别名和投影，旧分页入口消费者已清零删除。
- 原 HEAD 复现索引借用嵌套凭据：[测试](baseline/S06/original-codex-index-snapshot-test.go.txt)、[日志](baseline/S06/logs/original-codex-index-snapshot.log.gz)、[命令](baseline/S06/original-codex-index-snapshot.result.json)。新索引在写入/返回边界复制，合并仍保留原键覆盖顺序、nil/空值；直接新核心测试避免兼容层复制掩盖缺陷。回退该修复会恢复调用方修改污染后续索引结果的风险。
- 已通过 [导入 race](baseline/S06/account-imports-race-2.result.json) 62 项（零失败/跳过）、[真实 PostgreSQL + HTTP](baseline/S06/account-codex-http-integration.result.json)、[Wire](baseline/S06/account-codex-wire.result.json)。真实 HTTP 覆盖创建、更新、部分失败和敏感字段边界，不执行供应商隐私任务。
- 扩大消费者 unit 首轮 15,459 个通过事件、3 项失败和 2 项跳过；三项失败均为旧后台测试替身缺少新条件写契约。补齐身份比较、原失败注入和提交后取消后，[原路径定向测试](baseline/S06/account-background-paths-unit.result.json)及[完整 service unit](baseline/S06/account-imports-service-unit.result.json)通过，后者 12,919 个通过事件、零失败、2 项原跳过。完整事件归档于[增量矩阵](baseline/S06/account-imports-test-events.json)，跳过未计行为通过。
- [scoped lint](baseline/S06/account-imports-scoped-lint-2.result.json)只剩原 `account/endpoint_rule.go` QF1001；未扩大忽略。新 CRS 无消费者包装删除，测试专用投影单独保留。SQL、Ent、冻结资料、其它任务、HEAD、索引及原计划正文摘要见[不变量](baseline/S06/account-imports-invariants.json)。首次新增文件空白检查发现先前搬迁 SQL 字符串一处行尾空白，已移除，SQL migration 内容不变。
- 后续仍需完整后台/手动刷新、OAuth 用量及 Ollama 维护、剩余账号 HTTP、完整 RoutePlan/EgressPolicy、依赖夹具和最终全量验证。S06 尚未完成，roadmap 保持 6 / 17、实施中。


### 2026-09-13：Ollama 共享会话与查询拥有者

- `account.OllamaCloudUsageService` 接管原共享身份会话、四槽 singleflight、主从状态投影、手动/周期查询、失败保留旧成功快照和重试调度。设置的解析、默认值、验证、防抖、最短抓取间隔与退避规则进入 account，Cookie 验证进入 egress。实际 HTTP 与 HTML 供应商解析经仅返回技术观测的旧执行句柄接入，明确 S09 退出；未把平台响应解析搬进账号核心。
- app 直接注入唯一 AccountStore、原 SecretEncryptor、动态设置端口、随机/时钟与 Redis/DB 技术租约；生产 HTTP 的七个方法直接绑定新 handler。旧服务不再持有查询缓存、槽位、singleflight 或 goroutine；旧 provider 无消费者后删除，兼容构造只用于原消费者/测试。只生成 Wire，未运行 Ent 生成。
- 在原 HEAD 复现 Stop 只等待周期任务、未取消管理员手动刷新：[测试](baseline/S06/original-ollama-manual-stop-test.go.txt)、[日志](baseline/S06/logs/original-ollama-manual-stop.log.gz)、[命令](baseline/S06/original-ollama-manual-stop.result.json)。新 `OllamaUsageRuntime` 把循环及手动查询纳入同一活动表，周期锁等待支持取消；Stop 有界等待并固定首次结果。取消后的迟到观测不再写快照。回退会恢复手动查询脱离拥有者退出、可能在依赖关闭后写入的风险。
- 原查询、Cookie、到期、HTML、共享组和 HTTP 断言全部保留；[新图 unit](baseline/S06/account-ollama-wired-unit.result.json) 113 项和[真实 PostgreSQL](baseline/S06/account-ollama-wired-integration.result.json)31 项通过，均无失败/跳过；[新图 race](baseline/S06/account-ollama-wired-race.result.json)、[Wire](baseline/S06/account-ollama-wire.result.json)通过。真实 PostgreSQL race 继续归档独立结果。
- [scoped lint](baseline/S06/account-ollama-scoped-lint-3.result.json)只剩原 QF1001。所需 httpguts/并发库、UUID 和旧装配依赖按完整文件和精确 import 登记，原角色限制保留。真实浏览器登录/供应商环境没有执行，本地响应夹具不计真实外部验证。
- 完整 TokenRefreshService 重试/分页、手动刷新协调、OAuth AccountUsageService、剩余账号管理 HTTP、完整 RoutePlan/EgressPolicy、门禁夹具及最终全量验收仍待继续。S06 保持实施中。


### 2026-09-13：OAuth 用量核心、共享缓存与恢复边界

- `account.OAuthUsageService` 拥有查询入口、Anthropic 主/被动流程、六并发批量及原回写顺序；`OAuthUsageCache` 保存唯一缓存/flight 状态，旧 UsageCache/展示值仅别名或委托。Grok 纯展示字段落于 `account/usageview`，xai 仍拥有供应商解析，没有令账号核心反向 import xai。
- 窗口展示、日期解析、Qoder 额度恢复判定和 TTL 规则已迁入纯核心，时钟按原取时点注入，未合并原本不同的边界。`LocalUsageStatistics` 保留五字段查询投影、懒读取、批量优先和八并发回退；实际 SQL 与详细 usage 报告 S08 退出。实际 HTTP 直接绑定新 handler，原快照 key、30 秒 TTL、ETag/304/Vary 保留；通用 If-None-Match 匹配迁入 httpx，旧 Ops 入口也委托同一算法。
- 历史修复一：[原缓存共享复现](baseline/S06/original-oauth-usage-cache-snapshot.result.json)确认 Antigravity/Qoder 返回同一可变对象，Antigravity 原位刷新倒计时。新缓存与请求边界使用类型明确的深复制，保留浮点、指针、nil/空容器，不通过 JSON 往返复制。
- 历史修复二：[原恢复身份复现](baseline/S06/original-usage-recovery-identity.result.json)确认用量成功后的迟到恢复能清除管理员新凭据、禁用、代理或错误。`UsageRecoveryWriter` 现在在原单条 SQL 更新中比较身份及原错误；仍只改 status/error_message/updated_at，未借恢复重新开启 schedulable；outbox 失败仍为原尽力语义。[真实 PostgreSQL race](baseline/S06/account-oauth-recovery-integration.result.json)验证成功、改名、身份变化、取消及 outbox 失败。
- 历史修复三：[原批量结果 race](baseline/S06/original-usage-batch-results.result.json)确认缺失账号与 worker 失败同时写结果 map。新核心共用原 worker 互斥边界，不改查询顺序、并发数或响应字段。
- app 直接绑定唯一 AccountStore/缓存/统计/核心，旧执行句柄只提供平台调用。Antigravity/Qoder 的独立 30 秒查询也登记在核心拥有者内，保留等待方独立取消；Stop 取消并等待在途，未完成固定报告，构造不开始查询。旧生产方法复用 app 绑定的统计对象，旧核心包装与缓存 key 帮助函数在消费者清零后删除或移到 unit 测试兼容文件。
- [新图 unit](baseline/S06/account-oauth-wired-unit.result.json)、[新图 race](baseline/S06/account-oauth-wired-race.result.json)、[Wire](baseline/S06/account-oauth-wire.result.json)通过；[扩大普通测试](baseline/S06/account-oauth-consumers-normal.result.json)7,346 个通过事件、零失败、2 个跳过；[scoped lint](baseline/S06/account-oauth-wired-lint-2.result.json)只剩原 QF1001。实际测试/跳过条目见[事件矩阵](baseline/S06/account-usage-test-events.json)，跳过未计为行为通过。
- Ollama 后续[真实 PostgreSQL race](baseline/S06/account-ollama-wired-integration-race.result.json)31 项通过，无失败/跳过。
- 尚未完成：各平台内部用量查询、快照和健康编排，完整后台与手动刷新协调，剩余账号管理 HTTP，完整 RoutePlan/EgressPolicy，以及最终门禁夹具、全量测试/lint/构建/进程验收。当前不能宣布 S06 完成。


### 2026-09-13：平台用量编排、共享探测停止及条件写回

- Antigravity/Qoder 查询选择、共享抓取、降级缓存与窗口规则进入 account；Gemini 统计归类、Grok 计费新鲜度与本地统计组合、OpenAI 主/影子查询决策与节流也进入唯一用量核心。旧 service 保留供应商 HTTP、认证和报文解析；无生产消费者的私有委托移到对应构建标签的测试文件。
- Grok 共享探测与异步模型同步使用 `account.ProbeRuntime`。调用方取消策略及 25/15 秒预算保持，Stop 拒绝新任务并取消等待在途，超时固定首次结果；app 登记 GrokQuotaProbes，Wire 通过原入口生成。后台模型输入及共享额度结果深复制。旧调度免费额度统计接入原后台完成屏障，统计算法、context 与缓存仍交接 S07。
- 原 HEAD 证据：`original-grok-probe-owner`（缺少停止拥有者）、`original-grok-probe-copy`（共享嵌套结果污染）、`original-grok-existing-races` / `original-grok-scheduler-fixture-race`（夹具无锁访问与替换在用 sync.Map）、`original-qoder-observation-identity`（旧身份结果写快照及限流）、`original-qoder-cache-identity`（换凭据仍命中旧展示）、`original-openai-observation-identity`（异步写入旧身份快照）。对应 .log、.result.json 与必要测试原稿均位于 baseline/S06；生产副本仍保留原 HEAD。
- 最小行为修复：Qoder/OpenAI 快照使用现有字段原子比较，保留原 JSONB 合并及 outbox 范围；Qoder 设置/清除限流维持独立提交和尽力事件，清除额外核对原健康窗口。Qoder 内存缓存/flight 增加来源校验，账号 key、TTL 和数据库/Redis 格式不变。OpenAI 独立五秒写回登记在派发前，停止预算实际约束等待。
- 实际验证逐测试事件见 [平台用量验证](baseline/S06/account-platform-usage-test-events.json)。Grok 定向 unit 544 条通过、扩展 race 552 条通过；OpenAI/相关平台定向 race 已取得通过；真实 PostgreSQL identity/window/outbox/cancel 矩阵取得 integration race 通过。停止竞争重复 race 和后续验证以该账本最新命令结果为准；过程中的 nil 包装及已接纳派生任务停止时序问题独立保留失败日志，不删原断言。
- 当前范围 normal lint 在 `account-usage-current-lint.log` 仅保留既有 `account/endpoint_rule.go:40` QF1001；unit/integration 全量 lint 及阶段全量验证仍待收尾。无消费者的测试私有包装随后清理，持续更新门禁而不增加忽略。
- 剩余：完整后台刷新分页/重试/后置编排、其他平台观测与健康条件、剩余账号管理 HTTP、完整 RoutePlan/EgressPolicy 实际调用、最终消费者/构建集/门禁夹具及全量验证。S06 继续实施中，roadmap 仍为 6 / 17，不自动提交。


### 2026-09-13：后台刷新运行实例与管理对账

- `account.BackgroundRefreshService` 统一持有周期、候选恢复游标、平台准入实例和 Grok 管理对账；app 直接构造/绑定/登记它，旧 TokenRefreshService 不再持有循环、游标锁或平台门缓存。静态配置由 app 投影，真实 AccountStore 与同一个 OAuthRefreshAPI 直接注入。
- 候选页序/元数据/游标进入 RefreshCandidateScan；平台分组与 worker 结果进入 RefreshPageProcessor；连续失败隔离进入 RefreshProviderState；共享准入集合进入 RefreshProviderGates；预算与退避进入 RefreshTuning；完整单账号尝试/重试进入 RefreshAttempts，成功后置顺序进入 RefreshPostActions。旧私有行为测试的包装按实际构建标签迁至测试文件，原断言继续执行。供应商交换及错误解析、旧调度缓存投影仍经精确兼容端口，S07/S09 改绑。
- GrokReconciliationService 拥有原对账分类、分页、dry-run/apply、逐条 CAS/刷新/部分失败结果。构造无启动；周期/手动扫描/对账停止入口同时取消并等待，重复停止固定首次结果，未完成报告超时；app 在其后才处理共享依赖。
- 补充原 HEAD 复现 `original-refresh-success-cooldown` 与 `original-refresh-success-cache`：迟到成功会清除管理员的新 cooldown；正常清理后发布的对象仍带旧 cooldown。最小修复使用现有身份与原期限/原因条件清理；冲突跳过新缓存清除、旧快照发布及后续维护；成功先更新本轮健康投影。原单条 SQL、独立提交和尽力 outbox 不变，SQL/缓存格式不变。
- 验证见 [后台刷新逐测试事件](baseline/S06/account-background-refresh-test-events.json)：核心及旧入口定向 unit 146 条通过；新运行实例定向 race 143 条通过；真实 PostgreSQL cooldown 条件矩阵 integration race 9 条通过；两个入口停止竞争 20 轮 race 60 条通过。Wire 生成命令 `account-background-refresh-wire` 已通过。最新全部直接消费者测试与 lint 的结果继续追加，不将该组定向证据代替阶段全量验收。
- 过程回归单列保存：尝试委托曾回写初始记录而覆盖原测试中的较新持久状态，已修正为只保持旧直接交换路径的原对象赋值；实际统一 API 回读结果不回写初始快照。旧候选仓储替身补齐凭据更新与条件清理的真实行为，保留成功/失败/取消/CAS/部分成功断言。未用扩大忽略规则掩盖问题。
- 最新增量源码/声明清单在 `baseline/S06/incremental-source-ledger.json.gz`，仍是执行期账本；完整消费者、OS/标签集合、门禁夹具及阶段总验收尚待完成。S06 继续实施中，不更新完成数量，不提交。


### 2026-09-13：手动刷新接入共享协调与条件配置写入（继续拆分中）

- 旧单账号/批量刷新生产入口已注入同一个 OAuthRefreshAPI；WithManagedRefresh 在原平台锁命名空间下重读账号，并把显式管理流程纳入停止等待。非 OAuth 与 Spark 影子仍在锁/交换前拒绝；显式禁用账号资格不套用后台 active/到期条件。
- 已保存原 HEAD 的 [手动刷新覆盖复现](baseline/S06/original-manual-refresh-identity.result.json)。内部 UpdateAccountInput/ConfigurationChange 增加不可由 JSON 提供的 ExpectedCredentials；管理校验及原配置事务不拆分，锁内验证平台/type/status/凭据/代理。冲突只回读最新账号，不再次交换、不执行旧结果的后置失效；没有新增数据库列、锁表、事务 context 或缓存格式。
- `account-manual-refresh-unit`、`account-manual-refresh-wire` 与真实 PostgreSQL `account-managed-credentials-integration-race` 已通过；覆盖配置更新保留无关字段、换身份拒绝、outbox 失败回滚及取消。`account-manual-refresh-lint` 仅保留既有 QF1001；共享锁/停止的定向 race 结果另存 `account-managed-refresh-race`。
- 本批只完成手动刷新安全接入；旧 HTTP 中的平台交换适配及管理编排仍需继续迁出，AG 缺失 project_id 的原多步清理与警告顺序仍需随完整用例做进一步交错核对。S06 尚未完成。


### 2026-09-13：账号管理、健康恢复与运行展示

- 完整单条/批量刷新、重新授权、CRUD、批量创建/删除/清错、字段批改和调度/额度管理接入 account 的唯一用例，管理路由直接绑定 `ManagementHandler`。原 idempotency scope、缺失/空集合、逐项错误和部分成功保持；旧 handler 只保留已迁方法委托和未迁入口。批量创建的异步隐私任务仍经原完成屏障，实际请求由 `PrivacyService` 取消并等待；停止后不发起新请求、不接受迟到写回。
- 已在原 HEAD 复现批量凭据字段更新覆盖交换后的 token/未选配置：[原复现](baseline/S06/original-credential-field-patch.result.json)。现在 UpdateAccount 的内部字段补丁在原配置事务行锁内合并最新凭据，HTTP 不能直接提供该内部参数。首次真实集成暴露 MergeCredentials 修改输入 delta 的问题，失败保留在 `account-credential-field-integration-race-2`；修正为合并副本后，[PostgreSQL race](baseline/S06/account-credential-field-integration-race-3.result.json)通过，原 outbox 失败仍整体回滚。回退该修复会恢复旧 token/配置被覆盖的风险。
- `RecoveryService` 接管限流/临时停调清理、可恢复状态判断及缓存查询；app 直接绑定唯一 AccountStore，原 Redis 状态类型用别名保持 JSON 兼容。即时、计划测试及管理恢复直接调用新核心，删除已清零的旧计划恢复桥接；独立写入顺序、失败即停止/尽力日志及 token/调度通知时机不变。原十二项恢复行为测试迁入 account，断言保留。
- `ManagementList`、`SchedulerScoreView` 和 `RuntimeStatusReader` 拥有列表的批量读取/展示组合，评分执行仍经 S07 原实例提供；`RuntimePresenter` 拥有唯一管理 DTO 与母账号展示，生产不再经旧 handler 展示桥接。OpenAI 配额自动暂停纯判断进入 account，旧入口只投影请求设置；管理员阈值、5h/7d 选择、缺失时间戳和陈旧快照语义保持。
- [逐测试事件及命令](baseline/S06/account-management-test-events.json)区分真实执行、失败与跳过，覆盖新核心、旧委托、列表筛选/分页/ETag、单条展示、隐私停止、管理凭据与 PostgreSQL 交错。只生成 Wire，后续仍需最终重复生成/全量验证。
- 迁移期间的编译/门禁失败分别留存，逐条修正新增问题，不扩大忽略规则。模型/tier、剩余健康与供应商维护、完整 RoutePlan/EgressPolicy、门禁夹具和阶段全量验收继续待完成；S06 保持实施中、6 / 17。


### 2026-09-13：剩余账号 HTTP、tier 与隐私身份修复

- 全部管理员账号路由（含列表、模型目录/预览同步、tier、统计、默认映射和高级调度诊断）直接绑定新 HTTP；删除生产 AdminHandlers.Account、旧 ProvideAccountHandler 装配。诊断安全值进入 scheduler/policy，算法/反馈仍归 S07；app 重建原诊断绑定顺序，只有一个生产实例。供应商 OAuth handler 继续 S09，未整体搬迁旧 AccountHandler 中混合的供应商方法。
- `routing.AdminCatalog` 通过显式输入组合目录，原平台目录/动态 Grok 来源经精确端口注入；保留读取顺序、Qoder 站点错误、默认与配置项排序，以及 OpenAI/Grok/Claude 三类 JSON 变体、nil/空集合和零值。纯目录契约与原管理列表测试通过。详细统计仍为 S08 的只读投影，日期对象由 app 显式注入。
- 历史修复：[原 tier 覆盖复现](baseline/S06/original-tier-refresh-identity.result.json)确认 Drive 查询期间管理员换 token 后会被旧快照覆盖。`TierManagement` 现在冻结并复核身份，凭据/Extra 的内部字段补丁在原配置事务行锁内合并；只写本次 tier/Drive 字段，同事务 outbox 失败仍回滚。[真实 PostgreSQL race](baseline/S06/account-tier-integration-race.result.json)覆盖成功、查询中和取锁前身份变化、取消及 outbox 失败。回退会恢复旧凭据和非 tier 配置覆盖风险。
- 历史修复：[原隐私身份覆盖复现](baseline/S06/original-privacy-identity.result.json)使用本地真实 HTTP，确认旧请求将 privacy_mode 写到管理员新身份。新 PrivacyStore 必须提供条件写入，沿用原 Extra/outbox 事务和字段比较，不新增列或缓存格式。查询身份已变时不写入也不更新旧成功投影；普通 DB 失败仍保持尽力语义。[PostgreSQL race](baseline/S06/account-privacy-identity-integration-race-2.result.json)覆盖 OpenAI/AG 的成功、换凭据、禁用与 outbox 失败。回退恢复旧观测污染新身份的风险。
- tier、隐私与实时模型同步均有按需活动表，关闭取消并等待在途，超时固定报告；不在启动时开启供应商查询。重复停止及迟到写入定向 race 通过。
- [本批命令与事件汇总](baseline/S06/account-management-latest-results.json)保留各次新增失败及补验。较大范围 normal/unit 首次分别因旧 Wire 及两处旧路由夹具未编译；其它已执行测试无失败，修正装配/夹具后 routes/app normal/unit 通过，不删除原路由断言。scoped lint 仅原 QF1001；这不是阶段全量验收结果。
- `Tier` 宽泛测试筛选暴露原 `TestForwardStreaming_ServiceTierPropagatedToResult` 的 GetMultiple 夹具 panic；[原 HEAD 同名独立复现](baseline/S06/original-streaming-tier-fixture.result.json)已保存，未改平台业务来规避。需在阶段全量结果中按具体测试和诊断比较，不只比较失败数。
- 更新账号维护、HTTP 边界和模型目录文档。完整 RoutePlan/EgressPolicy、剩余通用健康策略/观测边界、依赖夹具、全量测试/构建/进程与最终清单仍待完成；roadmap 保持 6 / 17、S06 实施中。不提交。


### 2026-09-13：请求策略、通用健康与条件恢复

- EgressPolicy 已接入原 HTTP transport/TLS 选择和账号 Header 应用入口，冻结技术身份、Header 及目标/重定向参数。Router→直接 Profile→默认的原选择和 WS 隔离标识保留；复制所有嵌套参数，不修改共享池客户端。Header 仍在原调用点写入，Grok CLI/403 专有策略仍交接 S09。普通/race 的原 HTTP/TLS/池/安全断言通过。
- RoutePlan 保存 Key 后模型、渠道映射和分组/协议投影；实际 handler/Live 入口使用同一渠道结果，候选按当前分组和账号重新生成。账号模型规则在原解析时点读取，动态默认模型没有被提前缓存。新模型链/副本隔离和旧转换/路由断言通过；请求体改写、响应恢复和重试时序仍在旧网关，S09/S11 退出。
- HealthService 接管通用临时停调匹配、重复 401 阈值、流超时规则以及账号/模型额度阈值；原窗口和独立写入顺序保留。旧 RateLimitService 只投影这些能力，供应商报文解释和调度反馈仍经原接口接入。原纯阈值测试迁到 account，剩余测试私有包装按 unit 标签保留，无消费者包装删除。
- GeminiQuotaService 持有唯一 tier/动态策略缓存，GeminiPrecheck 持有原独立日统计缓存；app 投影配置与洛杉矶日期，settings 读取和原 S08 统计通过接口注入。策略返回副本，原 HEAD 的 `original-gemini-policy-snapshot` 已复现共享 map 污染；新并发副本与真实 PostgreSQL 设置热读取验证通过。保留 V1/V2 优先级、冷却、日/分钟限制与 TTL。TierManagement 停止后不会继续启动队列中尚未执行的供应商查询，重复 race 通过。
- 历史修复：`original-ag-managed-recovery` 在原 HEAD 的真实旧 handler/SDK 配合本地响应复现“刷新恢复覆盖管理员新禁用/凭据”。ManagedRecoveryWriter 在原五个独立清理步骤逐一比较交换身份、错误与本步骤原窗口/JSON 键，冲突停止后续步骤；没有增大事务或修改 schema。运行时阻断沿用原代次作条件清理，避免清掉后来安装的阻断；最终凭据写入继续走原管理 CAS。
- 新 AG 恢复 unit 与 PostgreSQL integration race（35 个子场景）通过，覆盖各步骤身份/错误/窗口变化、取消、写失败及尽力 outbox。首次集成夹具误把原合并 outbox 的行数当成发布次数，失败日志保留；修正为分别核对成功步骤和合并后的持久事件，未改业务来满足计数。回退该修复会恢复迟到刷新撤销管理员新状态的风险。
- 逐测试与命令见 [本批证据](baseline/S06/policy-health-recovery-results.json)；最新范围 lint 仅剩原 QF1001。已保存的 Wire 不是最后验收，后续须重新生成和验证幂等。剩余观测身份边界、迁移/门禁清单、可丢弃夹具及全量构建/进程验证仍待完成；S06 保持实施中、6 / 17，不提交。


### 2026-09-13：观测身份、健康策略与 Redis 所有权

- [原 HEAD 三项复现](baseline/S06/original-usage-identity-boundary-2.result.json)确认 AG 用量缓存跨凭据复用、Anthropic 负缓存跨身份复用、旧主动用量回写新身份。原稿使用真实旧查询/SDK 与本地响应；首轮夹具的 int/int64 key 和缺少统计依赖问题留存于 `original-usage-identity-boundary`，补齐后全部三项触达原行为并失败，不把夹具 panic 当成业务缺陷。
- OAuthUsageCache 的 AG 与 Anthropic 命名空间现在保存进程内来源标识，共享 flight 同样核对来源；账号 key、TTL、负缓存时间和独立查询取消语义不变。不同身份等待者不消费旧 flight 的结果。返回副本及真实 singleflight 的确定性并发测试通过 10 轮 race。
- 主动→被动回写使用查询时身份，Extra 与窗口列仍独立提交；Extra 条件不匹配停止后续写回，普通写失败仍尝试原后续窗口操作。窗口列额外比较原结束时间，避免清除较新窗口。真实 PostgreSQL 覆盖查询前换身份、两步间换身份/窗口、取消、约束导致 Extra 失败及窗口 outbox 尽力失败。观测 Extra 原本不发 bucket 重建事件，只同步单账号快照；没有把该路径升级为新的 outbox 事务。
- ErrorPolicyResult 与决策规则、显式错误策略、认证失败/过载、403 累计升级、CN 并发/余额/Coding Plan 可恢复冷却和 429 默认回避进入 account.HealthService。供应商错误/HTML/Header 解析仍在旧执行 Adapter；默认窗口、设置读取、阻断先于写入、失败日志及是否切号保留。原同一输入断言在新核心保留，旧类型边界断言完整投影；没有删掉失败时保留运行阻断的契约。
- 三个原 Redis 实现（临时停调及 API Key 健康滚动窗口、403 计数、超时计数）迁至 account/rediscache；app 直接构造唯一实例，旧 repository 构造只委托。Lua、键前缀、Cluster hash tag、阈值清理及只首次设置 TTL 不变。原普通 Redis 夹具迁入新包，真实 Redis integration race 验证新旧入口互读、四十并发计数、TTL 不续期、只延长停调与阈值后删除。
- 本批 [逐测试与命令](baseline/S06/health-and-observation-results.json.gz)保存过程失败和补验。`s06-current-all-normal` 全仓普通测试通过；其后新增健康/Redis 迁移的定向测试通过。全仓 unit 首轮因旧构造把缺失仓储包装成非 nil 接口而 panic，已恢复可选依赖 nil 语义，原调度排除原因/阈值定向验证通过；`s06-current-all-unit-2` 正在补验。该问题属于本阶段回归，未归为历史问题。
- 最新 normal lint 仍只剩原 QF1001。依赖夹具首次发现 app/account_recovery.go 新配置投影缺少精确 config 许可；仅添加该文件的 config$，其余基线限制保留，首轮证据归档 `gates-before-health-config/`。后续正反夹具通过；新增 Redis 角色继续补验。清理重复生成的 S06 中文说明只改变注释，不改规则。
- SQL 309、Ent 379、旧阶段资料 1628 及其他任务文件 48 项最近一次摘要核对均未变化，索引为空。最终 Wire/构建集/清单、全量 unit/integration/lint/前端/构建/进程验证仍未完成；S06 保持实施中、6 / 17。


<a id="s06_completion"></a>
### 2026-09-13：S06.0—S06.4 完成与交接

S06 已完成。当前仍为 `main`、HEAD `d0ce5504c09c9b733a75da1fc35826ebf53f6af4`，交付未提交的可审查差异；没有自动提交、推送、切分支或处理其它任务文件。roadmap 更新为 **7 / 17**，下一步编写 S07 子计划。

| 子步骤 | 最终结果 |
| --- | --- |
| S06.0 | 原计划正文、初始 HEAD/索引/工作区/版本与摘要保留；最终文件和逐声明归属、类型引用、真实构建选择及资金参与账本已补齐。 |
| S06.1 | 代理、fallback、TLS 配置/采集、Header 与传输回退进入 egress；EgressPolicy 接入实际 HTTP/TLS/安全入口，技术池与原隔离键保持唯一。 |
| S06.2 | routing 拥有分组/渠道、价卡、目录/市场/容量及探测 runner；RoutePlan 接入真实模型链与逐候选解析，已迁读取者直接投影 routing。 |
| S06.3 | account 拥有管理、凭据协调、事件测试、用量查询、健康与维护；AccountSnapshot 不包含凭据。普通配置、消费、运行状态和凭据条件写入分别受控，账号资金写入委托 billing。 |
| S06.4 | HTTP 路由直接绑定三个模块，app 持有唯一生产实例及同连接参与工厂；依赖规则、生命周期、文档和旧入口退出账本已同步。 |

最终装配核对还将分组管理和容量读取直接绑定 AccountStore/KeyStore，删除旧账号仓储桥接。配置模型仍按原使用时点读取，容量保留批量失败回退、空结果与逐行取时点；平台目录和旧请求上下文设置的窄投影继续交接 S07/S09/S11。最后一次 Wire 改动只来自这些手写 provider，生成后全量验证通过，重复生成无差异。

| 验证 | 最终实际结果与证据 |
| --- | --- |
| 普通测试 | 11,308 条通过、4 条既有跳过、0 失败；[命令](baseline/S06/s06-final-test-normal.result.json)。 |
| unit 测试 | 19,346 条通过、8 条既有跳过、0 失败；[命令](baseline/S06/s06-final-test-unit.result.json)。 |
| integration 测试 | 12,211 条通过、5 条既有跳过、0 失败；[命令](baseline/S06/s06-final-test-integration.result.json)。 |
| lint | normal/unit/integration 为 1/285/19，均为原诊断。逐文件、规则、源代码与完整消息核对；只有既有 QF1001 随声明从 service/account.go 映射到 account/endpoint_rule.go，[逐项比较](baseline/S06/lint-comparison.json)。 |
| 真实进程与存储 | standard/simple、SIGTERM、端口占用、setup/CLI/AUTO_SETUP、维护资源唯一启动和停止顺序均通过；[最终图回归](baseline/S06/s06-final-graph-integration.result.json)，完整进程子场景也包含在最终 integration 中。 |
| 构建与前端 | 普通、Linux、两个维护命令、前端 lint/typecheck/关键测试与构建、真实前端产物的 embed 构建/测试均通过；[构建索引](baseline/S06/completion.json)。 |
| Wire | 两次原入口生成成功且摘要一致；[证据](baseline/S06/wire-reproducible.json)。没有运行 Ent 生成。 |
| 定向 race | 刷新/缓存/请求副本/停止、真实 PostgreSQL 和 Redis 竞争均取得通过证据；[近期健康与观测](baseline/S06/health-and-observation-results.json.gz)、[最后投影](baseline/S06/s06-direct-projection-race.result.json)及前述各批事件矩阵。没有扩展全仓 race 或 benchmark。 |
| 依赖门禁 | 正反夹具覆盖普通/unit/integration/Wire/embed 与 Darwin/Linux；合法组合零诊断，非法组合每项准确命中。已删除迁出的 S05 identity_admin 文件例外，重新创建该旧文件也不能继承许可；[夹具/配置摘要](baseline/S06/dependency-fixtures.json)。 |

通过事件包含父测试和子测试，不能当作独立测试函数数量；全部跳过名称与原因见[最终测试摘要](baseline/S06/final-test-summary.json)。真实供应商、Qoder 本地授权、外部 TLS 与 E2E 仍按既有限制登记为待验，不算真实行为通过；既有并发缓存 TODO 与特定 WS 子场景跳过分别交 S07/S11。单独运行的 streaming service-tier 测试曾在原 HEAD 复现缺少 GetMultiple 夹具依赖，完整测试集合通过；该独立验证限制单列 S09/S11，不修改生产策略来规避它。

最终交接资料：

- [文件/逐声明归属](baseline/S06/file-ownership.json.gz)：897 个变更文件、4,337 个旧生产声明；迁出声明按实际接收者、类型别名、签名和人工消歧定位，未决清单为空。过程中清零的 72 个测试兼容声明单列[删除记录](baseline/S06/removed-test-compatibility.json)，未删除业务断言。
- [静态消费者分析](baseline/S06/consumer-analysis-results.json)：normal/unit/integration 分别有 152,174 / 197,598 / 159,622 条类型解析引用，包含结构性接口实现候选。动态生产绑定以实际 Wire/装配为准，不把同名符号或接口候选当作运行图。
- [实际构建选择](baseline/S06/build-selections.json.gz)：普通/unit/integration/wireinject/embed/Darwin/Linux 均 191 包，e2e 192 包；记录 GoFiles、TestGoFiles、IgnoredGoFiles 与依赖。跨平台编译不冒充 Linux 运行验证。
- [生命周期 Hook](baseline/S06/lifecycle-hooks.json)：76 个真实注册点及启停函数/顺序表达式；生产者、按需任务、订阅、队列与共享资源的关闭由 app 统一协调。
- [精确依赖例外](baseline/S06/dependency-exceptions.json)、[规则变更](baseline/S06/dependency-rules.json)：没有失效的精确文件匹配，原 service/handler 禁止方向保持。
- [资金与事务参与](baseline/S06/funding-writes.json)、[S07—S16 交接](baseline/S06/handoff.json)：分组关联、代理联动与账号资金参与保持既有连接/原子范围；S04 平台额度协调仍限单服务进程。
- [文档链接](baseline/S06/document-links.json)、[证据链接](baseline/S06/evidence-links.json)与[日志归档](baseline/S06/log-archive.json)：12 篇现有 Project Doc 同步，134 项文档/代码锚点检查通过；678 份验证日志已压缩，保留退出码及过程失败。日志中的长令牌/JWT/URL 密码模式检查未发现新增匹配，保留 old/new 等短测试哨兵以便核对复现。

相关历史修复已分别保存原 HEAD 复现与修复后行为；迁移期间的新编译、投影、可选依赖和夹具问题也保留失败/补验记录。按子步骤回退会恢复原凭据/运行字段覆盖、缓存污染或迟到任务风险，详见各批记录；不涉及数据库、缓存格式或数据降级。SQL 309、Ent 379、旧阶段资料 1,628 及其它任务资料 48 项的冻结摘要均保持，最终 diff/索引核对见完成记录。
