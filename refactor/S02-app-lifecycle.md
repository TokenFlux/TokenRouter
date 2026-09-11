# S02：建立组合根、统一生命周期并迁移首批独立用例

## 1. 基线与实施约定

以当前 `main`、HEAD `5d3717f09ea5efcf4ae6e9c708031b51de1c668b` 为起点，完成 S02.0—S02.4。实施前重新记录 HEAD、索引、工作区与源文件摘要，保留现有 AGENTS.md、其他计划及 diagnostics 的改动。

开始实施前，将本计划原样保存为 `refactor/S02-app-lifecycle.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，较大清单及脱敏证据保存到 `refactor/baseline/S02/`，不改写 S00/S01 冻结资料。

已确认的实施选择：

- **统一接管完整生产依赖图的启停**，包括尚未迁包的旧 worker；业务算法与模块搬迁仍归所属阶段。
- **有界退出**：HTTP 优雅关闭保留 5 秒，后台清理使用独立的 30 秒总预算；超时报告未完成项，不把退出等同于 drain 成功。
- 保持 HTTP、数据库、缓存、配置、standard/simple、setup、embed 和 CLI 契约。生命周期修正属于本阶段明确变更。
- 使用 Go 1.27.0、golangci-lint 2.13.2；Docker 当前可用。不自动提交、推送或切换分支。

## 2. S02.0—S02.2：组合根与生命周期

### S02.0：冻结实施输入

建立逐项迁移和生命周期清单，记录实际文件、构造入口、启动副作用、生产调用方、资源拥有者、关闭依赖、测试标签、Wire 绑定、文档锚点及退出阶段。

清点覆盖显式 Start、构造函数内部启动、嵌套会话清理、订阅、定时器、队列和按需创建的运行资源。重点补入现有 Cleanup 遗漏的时间轮、Deferred、Dashboard 聚合、内容审核后台任务等；不只清点现有 Cleanup 参数。

同时保存 SQL checksum、Ent/Wire 生成物摘要及 S01 原有失败明细，作为增量比较基准。

### S02.1：建立唯一组合根

- 将完整 Wire 应用图、资源持有、跨层绑定及 Cleanup 移到 `internal/app`。按基础设施、旧图装配、运行时绑定和新模块分组，避免形成另一个巨型启动文件。
- `cmd/server` 保留参数解析、VERSION、`main.Version/Commit/Date/BuildType` 和最终退出决定。内部构建信息由 app 投影给旧消费者，旧模块不引用 app 类型。
- 配置只加载一次：同一份 bootstrap 配置用于日志初始化、数据库引导、完整校验和依赖图，消除当前 Wire 再次加载配置的路径。
- logger 配置转换、Redis/AES/PostgreSQL 参数投影、时区全局初始化移入 app。保留时区初始化顺序、`time.Local`、日志唯一后端及各连接池唯一实例。
- 保留 `go generate ./cmd/server`，由原入口委托 app 的 Wire 生成。修改手写 provider 后只生成 Wire；不手改生成结果，不运行 Ent 生成。

数据库初始化拆成两部分：

- `infra/postgres` 拥有迁移执行和启动暂时错误重试，接收数据库连接与迁移 FS。完整保留锁 ID、专用连接、排序、checksum 兼容集合、事务与 `_notx` 执行规则、索引恢复和 Atlas baseline。
- `app/bootstrap` 拥有连接、Ent 包装、迁移、密钥补齐、完整配置校验和 simple 默认数据的编排。连接建立后立即登记释放；Ent 与底层 SQL 连接只关闭一次。

普通服务、JWT 和维护命令改用同一精简初始化入口；setup 只调用其迁移能力。app 不反向 import setup，setup 不构造完整业务图。首次管理员创建、配置文件及安装锁流程保持现状，S14 再完成剩余精简。移除反向依赖风险，不能让旧 repository 包转发到 app。

server 保留 HTTP 参数、中间件和路由汇总；websearch builder、设置回调绑定和两个固定窗口限流器的技术构造移到 app。认证路由与面板继续使用原限流接口、前缀和故障策略。

### S02.2：统一启停、失败清理与重启

建立 `app/lifecycle`，统一管理启动、停止等待和资源释放。生命周期接口使用带 context 的启动/停止函数；旧方法通过 app 适配，业务模块不 import lifecycle。

- **构造与启动分离**：完整生产图构造完成、回调绑定完成后再启动后台任务。移出 provider 和构造函数中的启动副作用，同批调整直接调用者及测试。数据库引导等必要初始化 I/O 保留明确失败点。
- **启动顺序**：先基础资源与时间轮，再缓存、订阅和队列消费者，随后周期任务及任务生产者，最后开放 HTTP。现有同步预热、立即首轮执行和可降级初始化保持原语义。
- **失败清理**：每个成功取得的资源立即登记；构造失败释放已取得资源，启动失败停止已启动项，并清理失败启动项的部分资源。保留原始错误，附加清理错误。
- **关闭顺序**：先停止 HTTP 与其他监听入口，再停周期生产者和任务拉取，等待在途工作，随后排空用量、计费缓存、配额、延迟写回、通知和审计队列；取消时间轮任务及订阅后，最后关闭 Redis、Ent/SQL 和日志资源。仅无依赖的步骤并行。
- **超时与重复调用**：Stop/Cleanup 幂等，共用一次执行结果。context 实际约束等待；超时记录任务名、阶段和未完成状态。仍被未结束任务使用的共享资源不能提前报告关闭成功；最终由进程退出回收。
- **退出路径**：SIGINT、SIGTERM、监听失败和手动重启进入同一关闭流程。正常关闭和 Linux 重启保持成功退出；启动失败或清理超时返回失败。移除工作 goroutine 中绕过清理的 `log.Fatal/os.Exit`。
- **按需任务**：TLS 捕获监听、WS 池、动态 worker 扩缩和请求触发的后台任务由所属拥有者登记、停止；不在启动时提前开启原本按需运行的功能，也不改变平台请求取消策略。

时间轮实现迁入 `infra/timingwheel`。Start 真正启动底层运行，Stop 阻止新调度和 recurring 回调重新入队，并等待已执行回调结束。保持 tick、槽数、任务 key 和调度语义。Deferred 的账号业务留 S06，但本阶段接入停止、取消和最终 flush，防止 flush 与周期回调并发重入。

Redis leader lock 技术实现迁入 infra，数据库 advisory lock 技术按相同原则提取；锁名、owner、TTL、竞争失败、Redis 故障回退数据库及无后端行为继续由使用方决定，不引入新的锁策略。

重启实现移至 lifecycle，管理员和 setup 接收消费者侧的窄重启接口。保留 Linux 生效、其他 OS 无操作和现有响应内容、幂等/操作锁语义；原约 600 毫秒延迟后发起关闭。清零消费者后删除 sysutil 重启实现，避免保留第二条退出路径。

## 3. S02.3—S02.4：独立用例与过渡适配

### S02.3：settings 存取与完整 idempotency

**settings**

- 新模块拥有 Setting 值类型、错误、存取接口、PostgreSQL 实现、现有版本字段和通用更新通知。所有旧读写通过唯一新实现进入原 settings 表。
- 旧 SettingService 保留业务解析、范围校验、敏感值处理、页面聚合及领域缓存；不得整体搬迁或复制为新的 SettingsService。
- 通过 app 显式绑定公开设置、CSP 来源、websearch 重建和动态 worker 回调。通用通知支持安全订阅和注销；旧单回调入口保持替换语义。
- 保留“业务校验 → 原子写入 → 原有缓存刷新 → 原有通知”的时机和失败行为。单键设置不自动获得原先没有的广播，写入失败不刷新内存。
- 保留省略与显式清空、敏感值保留、局部更新后重新读取、TTL 和多实例传播方式。版本字段保持当前赋值及 JSON 省略行为，不新增数据库 revision 或 Redis 通知协议。
- web 继续只接收公开投影；server 不再持有具体 SettingService 来完成前端/CSP 装配。

**idempotency**

- 将认领、指纹、重放、冲突、过期回收、退避、响应存储、指标和清理迁入新模块；SQL 实现迁入其 PostgreSQL Adapter。
- 核心接受独立 Options，使用 apperror/logredact；日志输出通过注入的观察接口适配。默认 coordinator 和指标保持唯一状态，旧入口仅作别名或委托。
- 接入用户/管理员幂等 helper 和实际管理命令。保留 scope/key hash、observe-only、缺省 coordinator、TTL、UTF-8 截断、脱敏、Retry-After、重放 Header 和存储故障行为。
- 清理 worker 保留默认 60 秒、批次 500、启动首轮和单轮超时，新增可等待的停止语义。
- 系统操作锁继续留旧维护用例，改用新幂等契约；不改变续租、全局锁作用域或资金幂等语义，退出归 S14。

### S02.4：公告完整纵向迁移

- `site` 拥有公告实体、targeting 校验与匹配、CRUD、用户可见性、已读和到期归档；HTTP handler/DTO 迁入其 HTTP Adapter，两个 repository 迁入 PostgreSQL Adapter。
- site 定义用户查询、用户分页和有效订阅 ID 的只读接口。投影只含 ID、邮箱、用户名、余额和计划 ID；`app/legacybridge` 调用旧能力并转换，资格判断仍在 site。
- 保持现有查询顺序和失败语义，不顺便合并查询或调整订阅资格。桥接分别在 billing/identity 可直接提供接口时退出。
- 用户和管理员路由直接接入新 handler，保留路径、中间件顺序、状态码、JSON、分页、排序、时间字段清空及管理员身份语义。
- 保留读前归档、开始/结束边界、启动立即归档和每分钟扫描。重复标记已读仍保留第一次读取时间。
- 存储 Adapter 沿用现有 Ent 事务 context 和错误映射，不创建新的事务 key，不增加或拆开原子范围。
- 旧 domain 公告类型因 Ent schema/生成代码仍引用，保留指向 site 的类型别名；实现只有一份，本阶段不触发 Ent 生成。其他旧公告入口消费者清零后删除，必要兼容项登记 S15/S16。
- 站点页面和其余内容能力继续留 S10。

所有为新模块提供旧能力的临时适配集中在 `app/legacybridge`，只做调用和投影，不持有业务缓存或执行业务规则。

每批迁移同步更新 depguard：新核心、HTTP、存储、infra、bootstrap 和 lifecycle 按角色约束；旧图装配、Wire 生成文件及 legacybridge 的必要 import 精确到文件登记。setup 调用精简 bootstrap 的特殊方向只许可实际入口文件和精确目标，不开放整个 app。迁出文件删除原例外，保留 S01 protocol 白名单及原有规则。

## 4. 验证安排

所有项目命令使用 `GOTOOLCHAIN=go1.27.0`。每批运行新模块、旧转接及直接消费者测试；涉及数据库、锁、队列或订阅时运行真实 PostgreSQL/Redis 集成测试。

| 契约 | 必须取得的证据 |
| --- | --- |
| 生命周期 | 构造期间无后台启动；绑定先于 Start；重复启停；构造失败、部分启动失败、监听失败的清理；消费者最后处理完毕后才关闭依赖；阻塞 Stop 有界返回 |
| 进程入口 | standard/simple、CLI setup、Web setup、AUTO_SETUP、SIGTERM、重启及端口占用；版本注入和 `-version`；setup 不启动完整 worker |
| 初始化 | 空库与已有库、并发密钥引导、迁移锁、checksum 拒绝、事务与非事务迁移重放；暂时错误重试、永久错误立即失败；初始化失败关闭连接 |
| 队列与时间轮 | 禁止停止后重新入队；在途回调结束；Deferred 最终 flush；用量/资金队列 drain；动态 worker 缩容；订阅主动停止；正常关闭无 goroutine 泄漏 |
| settings | 批量写入原子性、写失败无通知、省略/清空与敏感值语义、缓存刷新失败、回调顺序和注销；HTML/CSP 更新、websearch 代理缺失不直连 |
| idempotency | 并发同 key 单次副作用、不同载荷冲突、重放/回收/退避、observe-only、存储故障、UTF-8/脱敏、指标唯一性、清理停止等待及维护锁续租 |
| 公告 | targeting 组合与金额比较、时间边界、CRUD/分页、无资格用户、重复已读、事务回滚、归档失败、用户和管理员 HTTP 契约 |

生命周期单测使用可控任务和短预算；进程级测试验证真实信号、退出码及资源顺序。存储行为使用隔离容器和临时配置，不连接生产环境。共享状态及队列做针对性 race，真实 Redis 竞争场景做 integration race，不扩展为全仓 race 或 benchmark。

depguard 使用可丢弃夹具验证合法依赖、旧装配许可、同目录新增违规、旧文件新增禁止 import、迁出后不继承例外、核心反向依赖及正常 Adapter。覆盖普通/unit/integration，并核对 wireinject、embed、Darwin/Linux 选择；完成后删除夹具，保留诊断。

阶段收尾统一执行：

```bash
# backend 目录
export GOTOOLCHAIN=go1.27.0

go generate ./cmd/server

go test -count=1 -json ./...
go test -count=1 -json -tags=unit ./...
go test -count=1 -json -tags=integration ./...

golangci-lint run --timeout=30m ./...
golangci-lint run --timeout=30m --build-tags=unit ./...
golangci-lint run --timeout=30m --build-tags=integration ./...

make build
make -C .. build-frontend
go build -tags=embed -o bin/server-embed ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/server-linux-amd64 ./cmd/server
```

另验证两个维护命令可构建、Wire 再次生成无差异，以及真实前端产物下的 embed 注入测试。使用 go list 和 JSON 测试事件确认关键行为实际执行；跳过和仅编译不计行为通过。

S01 的分组 integration 失败及 lint 诊断按文件、测试、规则和消息核对；路径搬迁保留映射。新增问题必须解决，原有失败独立归档，不扩大忽略规则。真实供应商 E2E 和外部 TLS 捕获限制沿用既有记录。

## 5. 完成、交接与回退

S02 完成须同时满足：

- app 成为唯一组合根，完整生产图的启动、失败清理和关闭均有明确拥有者；必要的停止等待与超时行为已验证。
- settings 通用存取、完整幂等和公告纵向链已接入唯一生产实现，新核心不依赖旧包、config、具体 Adapter 或 app。
- 迁移清单覆盖文件、消费者、构建条件、Wire、依赖许可和文档；S04/S05、S06、S08、S10、S14、S15/S16 的剩余职责逐项登记。
- 受影响行为验证通过；全量结果只剩已复现的既有问题。必要环境验证未完成时保持“待验”。
- 同步现有架构、配置、部署迁移、HTTP 和开发流程文档，描述实际的新旧共存结构；不提前宣布后续模块迁移。
- SQL checksum、Ent 生成物及其他任务文件无意外变化，Wire 差异可解释，`git diff --check` 通过。

满足后将 roadmap 更新为 **3 / 17**、S02 已完成，下一步为 S03 子计划。阶段文件保留完整计划正文并追加执行与完成证据。

回退按子步骤撤销代码、规则、文档和 Wire 差异，恢复对应调用及启停链；本阶段不引入数据库或缓存格式变更。交付可审查差异，不自动提交，不提交 SYNC.md 或其他任务内容。

## 6. 执行记录

### 2026-09-11：S02.0 开始

- 原计划已原样保存；实施基线与其他任务文件摘要见 [开始快照](baseline/S02/start.json)。
- main HEAD 为 `5d3717f09ea5efcf4ae6e9c708031b51de1c668b`，原索引为空；保持其他工作区内容。
- 完整生产图统一接管启停，HTTP 5 秒与后台 30 秒有界退出；下一步迁移初始化与建立 lifecycle。

### 2026-09-11：S02.1—S02.4 实现与第一轮验证

- 应用图已移至 app，原 `go generate ./cmd/server` 入口实际生成新 Wire 图；启动配置只从入口加载一次。数据库引导、迁移 runner、Redis/AES/logging 投影已分离。
- settings 存取与通知、idempotency 核心/SQL、site 公告核心/HTTP/存储已迁入目标路径；旧公告 domain 仅保留 Ent 生成引用所需别名。
- 完整 runtime 登记按 boot/auth/maintenance/ops/queues/jobs/core 分组。除原 Cleanup 清单外，补入时间轮、Deferred、Dashboard、内容审核、槽清理、消息队列清理、缓存 janitor、用量仓储批处理和 Live observer。
- HTTP tracker 不包装 ResponseWriter，等待 handler 的尾部清理；对 hijack 连接显式触发断开。异步额度写入、通知和账号快照通过消费者侧接口跟踪完成，原 context/参数求值与并发策略保留；分层完成屏障后再停止依赖。
- 原仅发信号或等待 3 秒的 Stop 已补足实际等待；应用仍统一使用 HTTP 5 秒与后台 30 秒预算。超时保留未完成资源清单，不计 drain 成功。
- [初始化首轮验证](baseline/S02/bootstrap-first.result.json)：106 个测试事件通过；[新模块与生命周期定向 race](baseline/S02/lifecycle-contract-race.result.json)：56 个通过；[unit 全仓可编译性](baseline/S02/compile-unit-second.result.json)通过，编译不计行为验证。
- [第一轮依赖门禁复验](baseline/S02/depguard-second.result.json)普通集合通过；清理旧 import 许可并登记准确装配许可。runtime 文件随后拆分，下一轮同步其逐文件规则。
- 受影响行为首轮发现两个 Dashboard 测试未显式启动；已调整测试进入新生命周期，等待复验。其余实施中构建/测试诊断保留原日志，不作为既有基线失败。
- 当前仍为实施中：正在完成停止等待审计、真实存储/进程契约、夹具、全量验证、文档与交接清单。未提交或推送。

### 2026-09-11：最终退出竞争核对

- 补齐错误透传的同步预热/订阅、Dashboard 与内容审核缓存刷新、兑换失效及 Key 窗口重置的后台拥有者。按需 TLS 捕获禁止在应用退出后重开；WS 池封闭创建和预热，已租赁请求仍按原策略收尾。
- HTTP hijack 晚于关闭快照时也立即进入原断开路径。Deferred 周期与最后写回串行；最后写回失败上报原错误。配额最后排空不再把超过单轮上限的积压遗留给下一 tick，周期上限、原失败回填和资金算法保持不变。
- 新增真实 PostgreSQL 迁移并发锁/重放/回滚、初始化失败连接回收、真实 Redis 订阅回调停止等待；普通与 unit 全量已分别复验。最终所有结果以 baseline/S02/verification.md 汇总为准，中间失败仅保留为修正记录。

### 2026-09-11：最后一轮差异处置

- HTTP 层的按需运维错误日志队列和 QPS 刷新/空闲定时器已登记；修复 worker 关闭时继续读取已清空全局队列引用的问题，等待真实 drain。
- UsageCleanup 的手动即时派发也进入所属服务等待，不能只等待 timewheel 回调；其 Dashboard 测试替身的并发计数改为原子读取。
- 资金 integration 发现一次时钟前提波动。原始 HEAD 100 次未自然复现，1ms 偏差夹具触发同一残留；仅将测试改为数据库同一时间源，全部金额断言不变，详见 [时钟证据](baseline/S02/clock-fixture.md)。资金运行代码未改。
- 默认 lint 对同文诊断存在截断，出现“数量相同、文件不同”；新增未截断的 HEAD/当前工作区比较，作为最终逐条核对依据，不增加任何忽略规则。

### 2026-09-11：S02.0—S02.4 完成与交接

- S02.0 完成：原始计划、HEAD/索引/工作区及源文件摘要冻结；307 个 Go 文件的增量清单、45 个移除路径的承接映射、68 处静态生命周期 Hook 与动态屏障/HTTP tracker 已登记。
- S02.1 完成：app 成为唯一完整图组合根，配置只加载一次；bootstrap 与 infra/postgres 拆分引导/迁移，原 `go generate ./cmd/server` 保留，Wire 二次生成摘要相同。
- S02.2 完成：构造与启动分离，完整生产图及按需资源有明确拥有者；HTTP 5 秒、后台 30 秒总预算，失败回收、幂等停止、后台 drain 与 Linux 重启均取得证据。
- S02.3 完成：settings 通用存取/版本/通知与完整 idempotency 接入唯一实现；旧业务解释、敏感值、缓存时序与维护锁策略保留。
- S02.4 完成：site 公告的实体/校验/用例/HTTP/存储/归档纵向链已接入；旧 domain 只保留 Ent 所需别名，桥接集中在 app/legacybridge。
- 最终普通测试 11049 个 pass 事件、unit 19034 个；integration 11698 个 pass、37 个既有 fail，失败名单和具体约束消息与 S01 相同。完整 lint 对照 HEAD 无新增：普通 2→2、unit 289→286、integration 21→20。默认截断报告不作为逐文件等价依据。
- 普通/真实前端/embed/Linux amd64 与 arm64/维护命令构建、embed 注入、定向 race、真实 Redis 竞争与订阅、PostgreSQL 原子性/迁移/回滚、进程入口均已验证。Linux 管理员真实重启及重放、SIGTERM 均退出 0；源码相同的生命周期副本通过 goleak。
- SQL 309 个文件、Ent 379 个文件、go.mod/go.sum、49 个原有其他任务文件、计划正文与索引保持原状；原 depguard 两条旧规则和 protocol 规则保持有效，新增/迁出文件例外经可丢弃夹具验证。没有改 S00/S01 冻结资料。
- [验证摘要](baseline/S02/verification.md)、[契约矩阵](baseline/S02/contracts.md)、[迁移/剩余消费者](baseline/S02/migration.md)、[生命周期](baseline/S02/lifecycle.md)、[完整性核对](baseline/S02/checks.json)作为交付证据。S04/S05、S06、S08、S10、S14、S15/S16 的剩余职责均在清单中登记。
- 真实供应商 E2E 与外部 TLS 环境限制沿用既有记录；时钟前提和默认 lint 截断的处置已单列说明，不改变资金语义或忽略规则。
- roadmap 更新为 **3 / 17**、S02 已完成，下一步为 S03 子计划。本阶段未提交、未推送、未切换分支；不提交 SYNC.md 或其他任务内容。
