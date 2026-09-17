# S14：迁移备份、初始化与维护入口，冻结历史问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `165284ddf9a4141bc6df5ae897f3aeaf3f3fe1a1` 为起点，完成 S14.0—S14.4。

实施前将本计划原样保存到：

`/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S14-backup-setup-maintenance.md`

随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，证据保存到同仓库 `refactor/baseline/S14/`；沿用阶段约定，不另存 `.agents/plans/`。

已核实：

- Go 1.27.0、golangci-lint 2.13.2、Docker 29.5.2，以及本地 `pg_dump/psql` 可用。
- 定向普通测试 **117**、unit race **162**、初始化 integration race **7**、迁移/幂等存储 integration race **6** 条通过事件，无失败或跳过。集合存在重叠，不相加、不代替全量验收。
- 规划期间 **14,290 个已跟踪文件摘要未变**，索引与工作区保持原状，保留原有 50 个其他任务未跟踪文件。
- 规划覆盖层、原实现失败日志和摘要位于 `/tmp/tokenrouter-s14-planning/`。固定问题在 race 模式下重复三轮取得一致结果，预期失败不计为通过。
- **全程串行，不使用 subagent。** 不自动提交、推送或切换分支，不提交 `SYNC.md`。

保持 HTTP、配置键、数据库 schema、缓存协议、备份格式、金额及 standard/simple 契约；只生成 Wire，不生成 Ent、不修改已发布 SQL。

历史问题排查到此结束。用户已确认 B01—B06 全部纳入；执行阶段只开展迁移、固定修复和约定验证。本次引入的回归必须修复；清单外历史问题只保存当次证据并登记，即使阻塞验收也先请求调整计划，不自行追加复现或修复。

## 2. 固定问题与修复决策

| 编号 | 原实现复现 | 确定修复 |
| --- | --- | --- |
| B01 | 备份重复 Start 创建第二个 cron；Stop 后还能重新启动 | 统一启停与任务登记屏障，启动幂等、停止不可逆；启动恢复和定时配置只执行一次 |
| B02 | Stop 返回后，启动配置回源仍未取消；原停止逻辑最多五分钟后才取消后台操作 | 按用户选择，停止时立即封闭认领并取消运行 context，在应用剩余预算内等待回源、任务、子进程和清理 |
| B03 | 本地备份可通过符号链接读取、写入及删除根目录外文件 | 本地 Adapter 使用受根目录约束的文件操作，保留根内链接；拒绝越界链接与路径，不只做字符串清理 |
| B04 | PostgreSQL 恢复 SQL 出错、事务已回滚，但 Restore 返回 nil | `psql` 开启 `ON_ERROR_STOP`，保留单事务恢复；SQL、输入读取及进程退出错误传播至恢复状态 |
| B05 | 系统锁接管后仍保留旧指纹，新持有者无法续租；旧持有者释放会覆盖新锁 | 使用独立的认领代次，接管原子更新身份，续租和释放均比较当前所有者；重复释放共享结果 |
| B06 | `RestoreStatus=running` 保存失败，仍接受请求并执行数据库恢复 | 异步恢复必须先成功登记，再启动任务；登记失败释放维护锁并返回错误，不下载或执行恢复 |

B05 复用现有幂等表和系统锁 scope/key，不新增列。为系统操作提供专用认领接口：业务 operation ID 保持原值，随机所有者令牌存于 processing 记录的现有 `response_body` 元数据中；续租与释放同时匹配状态、operation ID 和令牌。旧记录无令牌时，未过期继续视为忙，过期后可接管。普通 HTTP 幂等接口及行为不变。

B02 保留原异步任务脱离浏览器取消的行为；应用停机取消与浏览器断开分开处理。取消恢复依赖 PostgreSQL 回滚，不把已经成功提交的恢复描述成已撤销。收尾持久化使用独立且受剩余预算限制的 context，失败如实报告。

## 3. 实施步骤与模块接口

### S14.0：冻结输入与维护权限清单

记录 HEAD、索引、工作区、工具版本、源码、SQL、Ent/Wire 摘要及 S13 完整验证结果，归档规划证据。

逐项登记备份、恢复、更新、回退、重启、setup 和维护命令的生产消费者、构建条件、存储写权限、资源拥有者、Wire、文档锚点及兼容入口退出阶段。更新资金写入账本中的初始化/恢复条目，明确它们是受控维护权限，不是运行态充值或结算入口。

### S14.1：backup 完整模块与资源生命周期

- `backup` 拥有配置、内容选择、记录、定时调度、备份/恢复编排、保留清理和错误。核心接收独立 Options、时钟、设置存取、加密、维护锁及归档执行接口，不接收完整 config、SQL、Gin 或旧 service。
- `backup/provider` 唯一拥有 `pg_dump/psql`、gzip 流、临时分卷、S3 和本地文件操作。归档执行入口负责管道、子进程、对象及临时文件的完整关闭；进度同步回调，不增加无界缓冲。
- 保留设置与记录 JSON、最多 100 条记录、旧 S3 配置合并、稳定密钥要求、运行时凭据读取和存储客户端缓存。历史记录按原 storage type/key 选择后端。
- 保留四类内容开关及自定义排除项、multipart/spooled_put、默认 4 GiB 分卷、顺序/大小/SHA-256 校验、单文件流式恢复，以及上传失败后的对象清理。
- 保留数据库重维护 advisory lock 的名称、专用连接、忙错误及既有无数据库行为；与 Ops 重任务继续共享同一锁。该锁不被描述成暂停全部业务写入。
- 落实 B01—B04、B06。所有手动、定时、恢复和清理操作经过同一运行时登记；停止先拒绝新操作、取消 cron/回源/执行，再等待已接受操作。重复 Stop 共享结果，超时不得报告完成或提前关闭依赖。
- 备份 HTTP 直接绑定新用例，保留管理员权限、step-up、恢复密码复核、ID 校验、下载响应和状态字段。密码复核通过 identity 窄接口，不把完整用户实体传入 backup。
- data management 迁为 backup 的明确兼容入口，保持 `DATA_MANAGEMENT_DEPRECATED` 和现有响应；不恢复 Unix Socket/gRPC 功能或构建已移除的守护进程。

### S14.2：setup 与精简初始化

- setup 保留 CLI/Web/AUTO_SETUP 的输入、展示、配置文件和安装锁流程，数据库能力通过 `app/bootstrap` 精简入口取得；app 不反向导入 setup。
- 数据库连接、迁移执行和必要重试继续复用 infra。首次管理员写入由 identity 的初始化能力承担；simple 默认分组由 routing 承担，管理员并发补齐由 identity 承担，app 负责原执行顺序。
- 精简初始化接口只供装配调用，不构造完整认证、路由、通知或后台 worker。迁移、默认数据和维护命令不得借用普通注册/创建接口而触发赠送、通知或新广播。
- 保留 `DATA_DIR`、SKIP_SETUP、配置或安装锁存在时的判断、连接测试、迁移预算、首个管理员创建条件、密码算法及一次性输出。保留配置 `0600`、安装锁 `0400` 和现有失败顺序，不把跨文件/数据库步骤改造成虚假的整体事务。
- 保留 JWT secret 原子生成与配置校验顺序、时区初始化、simple 平台集合、Grok 自动创建标记条件及管理员并发升级标记。
- 每次取得连接即登记释放；Ent/SQL 共享连接只关闭一次。删除 setup/bootstrap 对旧 service/domain 的实际依赖，必要测试兼容项登记 S16。

### S14.3：系统更新、锁、重启与维护命令

- 更新、指定版本回退和本地 `.backup` 回退编排进入 `ops/maintenance`；发布查询继续复用唯一 `ops.ReleaseQuery`。文件下载、归档提取及二进制替换进入技术 Adapter。
- 核心通过发布查询、二进制安装器、操作锁和重启请求接口工作。保留当前平台资产选择、允许回退列表、URL/重定向策略、可选 checksum、500 MiB 上限、同目录临时文件、替换失败恢复及 `.backup` 保留方式。
- 系统操作锁的业务 scope、operation ID、TTL、续租周期和忙错误归维护用例；专用存储能力由 idempotency 的 PostgreSQL Adapter 实现，落实 B05。确认失去所有权后停止后续维护阶段；原瞬时续租错误继续按既有节奏重试。
- 保留更新/回退脱离浏览器取消的十五分钟预算。应用停机先封闭维护入口并取消尚在下载等阶段的操作，再等待 HTTP 与维护收尾；进入二进制替换的小型临界区后，必须完成替换或恢复，不能在两次 rename 之间直接退出。
- 重启仍调用唯一 lifecycle Restarter，保留约 600ms 响应窗口、Linux 生效及其它 OS 无操作。HTTP 幂等、锁释放、响应内容和错误顺序保持。
- `jwtgen`、`cleanup-ingress-reject-logs` 使用各自精简装配，保留参数、输出、分类版本和默认 dry-run。命令主体返回错误并先执行资源释放，由 main 最终决定退出码，避免取得连接后通过 `log.Fatal` 跳过清理。

### S14.4：装配、门禁与文档

app 持有唯一备份运行时、存储工厂、维护执行器和操作锁。关闭使用明确阶段：先封闭并取消维护操作，再等待 HTTP/下载与维护收尾，最后关闭共享资源；HTTP 五秒和后台三十秒总预算保持。

旧入口只保留登记过的别名、投影或委托。同步 backup、ops 维护、idempotency 专用存储、初始化 Adapter、setup、cmd 的 depguard；历史许可精确到文件/import，迁出即删除，不扩大已有忽略规则。

同步现有架构、部署迁移、数据生命周期、Ops、配置、HTTP 和开发文档。修正 data management 守护进程的过时描述，明确恢复成功、状态保存失败、取消和备份内容范围的区别。

## 4. 验证与验收

每批验证新模块、旧委托和直接消费者。固定范围如下：

| 验证面 | 必须取得的证据 |
| --- | --- |
| B01/B02 | 重复启停、Stop 后 Start、配置回源取消、启动与停止交错、在途 dump/恢复退出、收尾超时 |
| B03 | 根内链接可用，根外读/写/删除拒绝，路径替换仍受根约束；只使用测试临时目录 |
| B04/B06 | 真实 PostgreSQL 成功恢复；SQL 错误、坏 gzip、输入中断和取消不报成功；登记失败不启动恢复 |
| B05 | 首次认领、正常释放后再认领、过期接管、同 operation ID 再认领、旧续租/释放拒绝、重复释放、瞬时故障 |
| 备份格式 | 本地/S3、两种上传模式、旧记录、分卷边界与校验、凭据切换、保留清理、进度和对象清理失败 |
| 初始化 | 空库、已有用户/管理员、CLI/Web/AUTO_SETUP、配置/安装锁、secret 竞争、迁移锁/checksum、simple 默认数据及失败释放 |
| 更新与命令 | 本地 HTTP/归档夹具下下载、校验、替换/恢复、允许回退列表；JWT 与清理输出、dry-run 和错误退出 |
| 进程与装配 | 唯一实例、构造无启动、standard/simple、真实 SIGTERM、手动重启响应与资源顺序、Darwin/Linux 差异 |

恢复只针对隔离 PostgreSQL，二进制替换只针对临时测试可执行文件。本地 S3 协议夹具验证对象行为，不访问用户存储、部署文件或生产数据库。共享状态执行定向 race，真实存储竞争执行 integration race，不扩展全仓 race 或 benchmark。

depguard 可丢弃夹具覆盖合法方向、精确旧许可、新文件拒绝、旧文件新增禁止 import、迁出例外失效及正常 Adapter。核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 文件选择，保存诊断后删除夹具。

收尾在 backend 使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

```bash
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

另验证两个维护命令、Wire 再生成无差异、真实前端产物下 embed 测试及上述进程场景。

S13 lint 基线 **1 / 283 / 17** 按路径映射、规则和完整消息比较；新增问题必须解决，不扩大忽略规则。跳过、仅编译和真实外部环境限制单列，不计行为通过。

## 5. 完成、交接与回退

完成须同时满足：

- backup、系统维护、setup 和命令生产链接入唯一实现，核心方向符合门禁。
- B01—B06 全部取得修复后证据，原失败日志保留。
- 初始化/恢复写权限、维护互斥、文件/子进程拥有者和关闭顺序可核对；未增加全系统停写或完整灾备保证。
- 兼容入口、消费者、构建条件、Wire、例外和文档可追踪；HTTP/设置聚合交 S15，最终兼容清理交 S16。
- SQL、Ent、S00—S13 冻结资料及其他任务文件无意外变化；原计划正文保留，新增与已有文件的 diff 检查通过。必要验证未完成时保持“待验”。

满足后 roadmap 更新为 **15 / 17**、S14 已完成，下一步编写 S15 子计划。交付可审查差异，不自动提交。

回退按子步骤恢复代码、装配、规则和文档，不删除备份对象或恢复记录。回退前停止维护操作并确认无活跃新锁认领，不能在二进制替换或数据库恢复途中切换实现。撤销 B01—B06 将恢复对应启停、取消、目录越界、恢复误报、锁所有权和未登记恢复风险，执行记录分别说明。

## 执行记录

### 2026-09-17：S14.0—S14.3 实施与定向验证

计划正文已原样冻结，摘要见 baseline/S14/initial-state.json。规划资料已归档；原失败保留为失败，不计入通过。未切换分支、未暂存或提交，未使用 subagent。

backup 核心与归档/provider、HTTP 已接入唯一实现；setup 的连接与数据库写入改为精简 bootstrap/identity/routing；系统更新与独立代次锁迁入 ops/maintenance 和 idempotency 专用存储。Wire 已重新生成。六项固定问题的回归与约定恢复测试进行中，编译及投影接线产生的问题作为迁移回归修正；没有扩展历史问题排查。

首轮迁移 unit race 120 条通过；固定启停/所有者集合三轮 39 条通过；真实 PostgreSQL 原修复集合三轮 12 条通过。扩大到成功恢复、SQL 失败、输入中断与取消的单轮真实数据库契约 4 条通过。数字为父子测试事件，不相加；最终采用结果及全部日志见各 result.json。

下一步：完成门禁夹具、全量测试、构建、真实进程与文档/冻结资料校验；未取得全部证据前保持实施中。

### 2026-09-17：S14.1 备份与资源收敛

配置、记录和定时调度进入 backup；归档、分卷、S3、本地根约束及 pg_dump/psql 进入 provider。原 backup_archive 空兼容文件已删除，旧公共构造仅作投影。B01/B02/B03/B04/B06 均有回归证据；已补充启动无效 cron 的原降级、根内最终链接删除、缺失根目录读取及停机前已开始清理的预算契约。

### 2026-09-17：S14.2 精简初始化与命令

setup 的连接测试、迁移连接和管理员入口委托 app/bootstrap；身份与默认分组写入分别由 identity/postgres、routing/postgres 执行。首次管理员条件、simple 标记与配置文件顺序保持。两个命令主体先返回/清理，再由 main 决定退出码。真实进程覆盖 JWT 验证、清理 dry-run/execute、参数错误、CLI/AUTO_SETUP、初始化失败与 SIGTERM；没有启动完整维护命令 worker 图。

### 2026-09-17：S14.3 系统维护与 B05

ops/maintenance 接管更新、回退和重启请求编排，二进制操作由技术 Adapter 持有。原幂等表通过专用认领/续租/完成接口比较独立所有者令牌；旧记录、同 operation ID 接管和迟到释放均已验证。下载保持浏览器断开后的原预算，应用停止会取消准备阶段；二进制替换临界区完成替换或恢复后才退出。

### 2026-09-17：S14.4 门禁、文档与增量修正

三组 lint 为 **1 / 283 / 17**，按迁移路径、规则和完整消息与 S13 对照，没有新增诊断；六项原 unit depguard 违规未被转为许可。42 项可丢弃门禁夹具全部符合预期，夹具已删除；8 个标签/OS 集合完成选择核对。

执行中的问题均为本次迁移引入或测试接线问题：空依赖兼容构造器提前解引用、迁包后私有测试标记和夹具别名、归档测试访问及新返回值检查。已保留失败日志、修正新代码或测试适配并重跑；未扩展历史问题审计或修复。全量命令串行执行；早期 Wire 与编译检查曾短时重叠，最终 Wire、测试和构建均独立取得成功结果。

<a id="s14_completion"></a>
## 完成与验收证据

S14.0—S14.4 已完成。普通全量 **11,765**、unit 全量 **19,813**、integration 全量 **12,748** 条通过事件；既有跳过分别为 **4 / 8 / 4**。后续补充的最终原生 race **143**、真实存储 race **11**、真实进程 **11**、系统维护 HTTP race **11**、恢复密码 HTTP race **8** 条通过事件均无跳过。事件包含父子测试且集合重叠，不相加。

后端与前端约定检查/构建、embed、Linux amd64、两个维护命令构建通过；真实前端产物的 embed 测试 **106** 条通过。Wire 再生成无差异。当前主机为 Darwin，Linux 为交叉构建证据；平台重启分支使用参数化生命周期测试，不把编译当作 Linux 部署行为验证。

证据入口：[验收索引](baseline/S14/README.md)、[完整命令与结果](baseline/S14/completion-evidence.json)、[契约矩阵](baseline/S14/contract-matrix.json)、[迁移清单](baseline/S14/migration-files.json)、[资金/维护权限](baseline/S14/funds-and-lifecycle.md)、[兼容与回退](baseline/S14/handoff.md)。

SQL、Ent、S00—S13 冻结资料和原计划正文保持不变；原有 50 个其他任务文件保留。没有自动提交、推送或切换分支，没有使用 subagent。Roadmap 更新为 **15 / 17**，下一步编写 S15 子计划。
