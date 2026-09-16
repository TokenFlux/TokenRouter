# S10：迁移通知、站点、审核与搜索，冻结历史问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `23eec43d4df55244f7f169a0ebfe348e7658b91b` 为起点，完成 S10.0—S10.5。

实施前将本计划原样保存为 `/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S10-notification-site-moderation-search.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，证据保存到同仓库 `refactor/baseline/S10/`；沿用阶段约定，不另存 `.agents/plans/`，不改写 S00—S09 冻结资料。

已核实 Go 1.27.0、golangci-lint 2.13.2、Docker 29.5.2 可用。工作区与索引无已跟踪改动，保留原有 50 个其他任务未跟踪文件。不自动提交、推送或切换分支；只生成 Wire，不生成 Ent，不修改 SQL migration、缓存协议或金额算法。

**执行范围固定：**

- 历史问题排查在计划阶段结束，仅修复下表 B01—B07，用户已确认全部纳入。
- 执行阶段仅开展迁移、固定修复和约定验证，不寻找其他历史问题、不扩展故障注入、不顺带优化。
- 本次迁移引入的回归必须修复。
- 验证偶然发现清单外历史问题，只保存当次证据并登记；即使阻塞验收，也暂停相关工作并请求调整计划，不自行追加复现或修复。
- 保留单服务进程部署边界，不增加跨实例协调、邮件恰好一次投递或持久任务恢复承诺。

规划资料位于 `/tmp/tokenrouter-s10-planning/`，包含源码摘要、临时 Go overlay、原实现失败和基线结果。仓库生产代码及测试均未修改。

已取得定向普通测试 **248**、unit race **414**、搜索包 race **53**、存储相关集合 **46**、真实 PostgreSQL 装配契约 **6** 条通过事件，无失败或跳过。各集合存在重叠，事件包含父子测试，不能相加或代替阶段全量验收。固定问题的预期失败单独保存，不记作通过。

## 2. 固定问题与修复决策

| 编号 | 原实现复现 | 本阶段修复 |
| --- | --- | --- |
| B01 | 同一通知去重键并发调用，实际 SMTP 收到两封邮件 | 唯一 notification 实例持有按 delivery key 的可取消协调器，覆盖去重读取、发送及成功标记。不同 key 并行，锁表安全回收；保留新旧去重键兼容和无去重键事件的行为。 |
| B02 | 首次并发生成退订密钥，两个令牌中一个立即失效 | 在唯一实例内串行化密钥读取与首次生成，取得保护后重新读取。保留设置键、HMAC、令牌格式和有效期，不轮换已有密钥。 |
| B03 | SMTP 忽略发送前及在途取消；问候读取需外部关闭连接才能退出 | SMTP Adapter 全链传递 context，拨号、TLS 和读写响应取消及较早截止时间。保留原连接/I/O 上限、TLS 回退条件和 DATA 成功后的发送结果；取消或结果不明不触发自动重发。 |
| B04 | 已配置 Markdown 页面通过符号链接返回页面目录外文件 | 文件 Adapter 验证真实目标位于页面根内，再通过受根目录约束的相对路径打开；使用同一句柄检查、限量读取。越界返回原“未找到”响应，保留根内链接和 1 MiB 限制。 |
| B05 | 旧搜索配置回源晚于保存完成，覆盖新缓存 | 配置加载、保存发布及 Manager 重建使用同一实例内的发布代次。旧加载不得覆盖新保存，也不得发布旧 Manager；写入失败不更新运行状态。 |
| B06 | 搜索配置保存和读取共享调用方引用，可污染运行配置 | 保存、读取、管理展示及 provider 构造边界深复制 slice 和指针；运行快照不可变，旧入口委托同一实现。 |
| B07 | 搜索取消后使用已取消 context 回滚，真实 Redis 中预占额度仍为 1 | 已确认预占由单次释放句柄管理，失败时使用脱离请求取消、最多 3 秒的清理 context 回滚。取消后停止 provider 尝试，不记作代理故障；未确认预占不递减。 |

B01 保留“SMTP 已接收但成功标记失败”的原有不确定边界，不增加发送重试、分布式锁或事务性邮件 outbox。B07 保留原计数 key、TTL、超限处理和 Redis 故障放行；回滚失败继续可观测，不宣称 Redis 故障下保证退额。

## 3. 迁移步骤与接口

### S10.0：冻结输入和所有权清单

记录实际 HEAD、索引、工作区、工具版本、源码摘要、SQL checksum、Ent/Wire 摘要及 S09 完整验证结果，归档规划证据。

逐文件、逐符号记录目标、生产消费者、测试标签、共享实例、事务、生命周期、Wire、文档锚点和兼容入口退出阶段。重点拆分旧 EmailService、BalanceNotifyService、SettingService、审核大文件及网关搜索文件，不按文件名整体搬迁。

### S10.1：notification 与身份邮件衔接

- notification 拥有通知事件、模板、语言选择、变量渲染、偏好/退订、投递去重、发送队列和错误分类；SMTP、设置存储、HTTP 分别进入 Adapter。
- 发送入口接受明确的 `SendRequest`：事件、收件人、语言、来源标识、提醒标识及模板变量。核心不读取用户、订单、订阅或账号来判断业务是否发生。
- identity 接管旧邮件服务中验证码和密码重置令牌的生成、验证、消费、冷却逻辑，复用现有值类型和 Redis 契约；凭据缓存归 identity 的 Redis Adapter。通知模块只负责呈现和发送。
- 队列接受注入的任务处理接口，保留验证码在 worker 执行时生成、先存验证码再发送、通知邮箱先发后存、重置令牌复用等不同顺序，不统一失败语义。
- 保留 13 类事件、中英文模板、可信 HTML 占位符白名单、Header/MIME 转义、语言记忆、新旧偏好/投递键，以及只有模板或配置错误才使用旧正文回退的规则。
- 余额、额度阈值及提醒资格继续由 billing/account/业务调用方确定；已确认事件和展示参数投影给 notification。Ops 提供报告变量，不让 notification 反向依赖 Ops。
- 模板管理、SMTP 测试和公开退订迁入 notification HTTP Adapter，保留 URL、权限、请求字段和响应。
- 落实 B01—B03。app 持有唯一发送器、协调器和队列；构造不启动。保留默认 3 worker、容量 100、单任务 30 秒及队满行为；停止封闭入队并排空，预算耗尽取消 SMTP、报告未完成数量，不把超时记作成功。

### S10.2：site 页面与公开信息

- site 拥有页面 slug/可见性规则、站点品牌与菜单、公开信息聚合和公开展示类型；文件读取进入文件 Adapter，HTTP 独占状态码、JSON、Markdown 和文件响应。
- 保留 Markdown 的 JWT 权限、管理员页面限制、管理员页面列表，以及公开图片只允许普通可见页面的差异。落实 B04，保留嵌套图片、路径编码和原错误形状。
- 从旧公开设置实现提取 API 与 embed 注入投影，保留两者现有字段及省略差异、默认值、版本、登录协议 revision、菜单过滤和 CSP 来源。
- 跨模块公开字段由消费者侧窄接口提供，app 绑定对应模块或尚未迁移能力的只读桥接；不复制认证、支付或功能资格规则，不把完整配置和敏感设置传给 web。
- 保留批量读取及原有额外查询时点，避免拆分后变成逐字段查询。设置更新后沿用原 HTML 缓存失效和 CSP 刷新顺序。
- S02 公告实现继续复用，仅同步装配和回归，不重新迁移其规则。

### S10.3：moderation 完整纵向迁移

- moderation 拥有审核输入、规则、配置、裁决、关键词匹配、hash、API Key 池健康、观测队列、记录与清理；HTTP、PostgreSQL、Redis、外部审核和媒体读取分别进入 Adapter。
- `Check(ctx, CheckInput)` 接收请求字节和明确身份/分组/模型投影，返回 `Decision`；核心不接收 Gin、旧实体或完整 config。当前轮提取和纯解析保持唯一实现。
- 保留 off/observe/pre_block、分组/模型过滤、抽样、关键词三种模式、分块重叠、批处理降级、部分失败仍阻断及现有 fail-open 行为。队列容量、字节预算、动态 worker、运行配置缓存和密钥冻结保持。
- 保留团队行为主体与付款主体区别、普通命中和 Cyber warning 的不同计数及处置规则，不借迁移实施现有 TODO 或统一管理员豁免差异。
- 普通处置通过 identity 的窄命令执行状态写入及原失效。Cyber warning 继续由 moderation PostgreSQL Adapter 拥有闭合事务，注入 identity 的同连接参与能力，保持“锁用户 → 写 warning/媒体 → 计数与禁用 → 提交”。参与方法不提交、不发布成功失效。
- 保留普通审核记录的尽力边界和 Cyber 事务边界，通知与认证失效仍在原位置执行；通知失败不回滚已提交处置。
- `NoMediaRetention` 保持不快照、不存正文/摘录，仅保留既有元数据；媒体获取保留地址验证、逐跳检查、类型及大小限制。
- 上游错误识别仍由 protocol/upstream 提供，网关负责捕获时机；moderation 消费观测并作本地裁决，不接管供应商重试。
- 管理配置、日志、媒体、hash、Cyber 和解封路由直接绑定新 HTTP Adapter。停止保持原封闭队列与等待语义，由应用预算报告未完成项，不额外改变正常请求取消策略。

### S10.4：search 核心、配额和供应商

- search 拥有搜索请求/结果、配置校验、供应商选择、预占/回滚及代理可用性规则；Brave/Tavily HTTP 和 Redis 状态分别进入 Adapter。
- 对外提供搜索、管理测试、用量读取/重置及配置读写；输入使用查询参数与明确代理投影，不接收旧 Account、Channel、Gin 或 Redis 客户端。
- 保留剩余额度加权、无限额候选顺序、同次选择随机因子、过期边界、订阅日期计算、TTL 修复及按 provider 类型共享计数。
- 保留账号代理优先、供应商代理回退、缺失代理不直连、5 分钟不可用标记、原超时和 HTTP 客户端缓存作用域；技术客户端缓存由 Adapter 唯一持有。
- 落实 B05—B07，保持空 API Key 保留旧值、缺键/错误缓存 TTL、管理脱敏、测试搜索不占额度和管理重置语义。
- 配置及 Manager 由 app 绑定唯一运行实例；旧 manager/配置入口仅委托。更新中的在途请求使用已取得快照，停止等待搜索及额度清理后再释放依赖。
- 网关只通过搜索接口取得结果。工具识别、账号/渠道启用裁决、协议事件、合成 usage、重试及完成处理继续留 S11；Grok 原生搜索和 OpenAI AlphaSearch 仍由其 upstream 拥有。

### S10.5：装配、门禁与文档

- app 直接绑定 identity、team、billing、account、Ops、site 和新模块，删除已完成的通知/审核/搜索 legacybridge；剩余支付、任务、维护及网关适配分别登记 S11—S16。
- 新核心不依赖旧 service/repository/handler、config、Gin 或具体 Adapter；跨模块用消费者接口协作，旧入口只别名、投影或委托。
- 同批更新现有 depguard，覆盖核心、HTTP、存储、provider、文件 Adapter 和装配。历史许可精确到文件/import，迁出即删除，保留原 service/handler/protocol 规则。
- 同步现有架构、审核风险、身份邮件、配置、HTTP、网关生命周期、监控通知及开发文档，保留稳定锚点，只描述实际实现。

## 4. 验证安排

每批验证新模块、旧委托和直接消费者。测试范围在此固定：

| 验证面 | 必须取得的证据 |
| --- | --- |
| B01—B03 | 同 key 并发仅发送一次、不同 key 并行、协调等待取消；首次退订初始化及旧令牌；SMTP 发送前/在途取消、TLS 两种路径、成功 ACK 与不确定结果 |
| notification | 13 类事件及语言、变量转义、可信 HTML、模板回退、退订和旧键兼容；身份邮件缓存顺序；发送失败不回滚权益 |
| site | 页面角色与菜单范围、根内/根外链接、大小边界、图片路径；API/embed 字段及敏感值、HTML/CSP 刷新、公告回归 |
| moderation | 模式/过滤/关键词/hash/API、部分失败、代理失败不直连、团队主体、无媒体留存、队列预算与清理 |
| 审核事务 | 真实 PostgreSQL 下 warning/媒体/用户状态原子提交与回滚、锁顺序、提交后失效；普通处置保持原尽力边界 |
| B05—B07 与搜索 | 回源和保存两种交错、连续保存发布顺序、深复制；真实 Redis 下成功计数、失败/取消回滚、未确认预占、清理超时 |
| 搜索消费者 | provider 耗尽/故障、代理优先级、管理测试与重置、旧工具响应事件及搜索用量 |
| 生命周期 | 构造无启动、唯一实例、重复启停、停止后拒绝入队、在途/清理等待、预算超时及 Redis/SQL 最后关闭 |

SMTP、搜索与审核使用本地 HTTP/TLS/SMTP 夹具；存储与事务使用隔离 PostgreSQL/Redis。共享快照、协调器和队列执行定向 race，不扩展全仓 race 或 benchmark。复现测试转为回归时保留行为断言，协调方式不得依赖修复前必然发生的错误交错。

depguard 可丢弃夹具验证合法依赖、精确旧许可、同目录新增违规、旧文件新增禁止 import、迁出例外失效及正常 Adapter。核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 构建选择；保留诊断后删除夹具。

收尾统一使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

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

integration 限制包并发为 4，沿用 S09 已验证的容器资源安排，不降低测试内部并发或修改断言。另验证两个维护命令、Wire 再生成无差异、真实前端产物的 embed 测试，以及 standard/simple 启动和 SIGTERM 通知/审核/搜索停止顺序。

S09 lint 基线为 **1 / 284 / 17**，按文件映射、规则和完整消息逐项比较。既有跳过、偶发测试观察和真实供应商/TLS/E2E 限制单独保留；跳过与仅编译不算行为通过，不扩大忽略规则。

## 5. 完成、交接与回退

完成须同时满足：

- 四个模块生产链使用唯一实现，身份凭据、业务触发条件和网关工具编排归属明确。
- B01—B07 取得修复后行为证据，原失败日志完整保留。
- HTTP、模板、缓存、事务、无媒体留存和生命周期契约验证通过；必要验证未完成时保持“待验”。
- 兼容入口、消费者、Wire、构建条件、依赖许可和 S11—S16 交接事项可追踪。
- SQL、Ent、S00—S09 冻结资料及其他任务文件无意外变化；Wire 差异可解释，新增与已有文件的 `git diff --check` 通过。

满足后 roadmap 更新为 **11 / 17**、S10 已完成，下一步编写 S11 子计划。交付可审查差异，不自动提交，不提交 `SYNC.md`。

按子步骤回退代码、规则、装配和文档，不涉及数据库或缓存格式降级。分别记录撤销 B01—B07 后恢复的重复投递、退订失效、取消失效、页面越界、配置覆盖/污染及额度遗漏风险。


## 执行记录

### 2026-09-16 — S10.0 冻结输入

- 原计划按正文保存，摘要见 [plan-original.json](baseline/S10/plan-original.json)。HEAD、工具链、索引见 [initial-state.json](baseline/S10/initial-state.json)。
- 规划源码与其他文件摘要、B01—B07 原失败和基线已归档到 [planning](baseline/S10/planning/)。原有 50 个未跟踪文件保持原样；不新增历史问题排查。
- 下一步：拆分 notification/identity 邮件职责并落实 B01—B03。

### 2026-09-16 — notification 与 site 首批生产改绑

- 通知模板/退订/去重、SMTP 与 MIME、队列已进入 notification；身份验证码与重置凭据进入 identity，Redis 邮箱凭据迁到 identity/rediscache。B01—B03 与队列关闭定向 race 通过，见 [notification-fixed-race](baseline/S10/notification-fixed-race.result.json)。身份邮件消费者 unit 通过 185 条。
- 余额与账号额度通知拆成 billing 判定和 notification 投递，保留原阈值与按需回源，原相关 unit 通过 439 条。app 直接绑定 Ops/team/billing 通知，移除对应旧 bridge；尚需完成阶段交叉验证与依赖门禁。
- site 已接管页面可见性、文件读取、公开 API/HTML/CSP 投影。B04 在文件 Adapter 使用受根约束打开和同句柄限量读取；公开站点值来源暂由精确的 S15 设置聚合桥接提供，不向 site/web 提供原始 OAuth secret。
- 页面/公开设置/协议文档/团队消费者集合通过 97 条。迁移中出现的旧团队 nil 用户读取适配回归已修复，原断言保留，失败与复跑见 site-public-unit 与 site-fixed-unit。
- 当前仍为实施中，后续继续审核与搜索迁移，再统一验收；这些局部结果不代替阶段完成。

### 2026-09-16 — 审核、搜索改绑与固定修复结果

- moderation 统一拥有原审核规则、输入、hash、观测与清理；HTTP/SQL/Redis/审核调用/媒体快照进入独立 Adapter。Cyber warning 的用户锁与禁用改由 identity/postgres 在调用方 SQL Tx 内参与，普通处置使用 identity 状态命令，原事务与后置失效顺序不变。
- 原审核普通/图片/创作台回归通过 273 条；四个模块整体定向 unit race 通过 289 条。SQL mock 迁移夹具补齐参与工厂后保留原 SQL 断言，失败与修复结果均归档。
- search 的选择、状态、客户端与配置实现已接入同一生产运行时。B05/B06 已验证旧回源、旧 Manager 构建、连续保存、输入/输出深复制和写失败不发布；B07 已验证确认预占单次释放及取消后不继续尝试/标记代理。
- [真实存储 integration race](baseline/S10/storage-fixed-integration-race.result.json)通过 18 条、无失败或跳过，覆盖 Redis 搜索取消回滚、TTL 修复、邮箱缓存，以及 PostgreSQL 审核回滚、设置和公告。
- 未新增历史问题修复。当前进入全量验收与门禁夹具，roadmap 仍为实施中。

### 2026-09-16 — S10.5 门禁、文档与最终核对

- 通知、站点、审核、搜索及 identity/billing/team/Ops 接点全部接入唯一生产实现。逐文件、逐符号、构建条件、真实静态消费者与 Wire 见 [迁移账本](baseline/S10/migration-ledger.md)。临时旧兼容入口清理阶段和混合文件未迁职责已单列。
- 完整 lint 清除本次未使用包装、测试辅助代码、精确许可与格式残留后，与 S09 按路径映射、规则及消息完全一致，仍为 **1 / 284 / 17**。原 SMTP 三条诊断只映射迁移路径；无新增忽略规则。57 项 depguard 夹具在普通/unit/integration 均命中预期，临时文件已删除。
- 全量普通 **11,505**、unit **19,543**、integration **12,449** 条通过事件，零失败，分别有 4/8/4 条既有跳过。后续只整理兼容符号与补充约定测试，直接消费者普通 **7,875**、unit **589** 通过；不将局部补验冒充全量重跑。详细命令、日志和标签见 [验收表](baseline/S10/acceptance.md)。
- 前端 199 项测试通过；普通、真实产物 embed、Linux amd64 及两个维护命令构建通过；embed 行为 101 条通过。Wire 再生成无差异。standard/simple 的真实 SIGTERM 验证了搜索、审核、通知队列各只启停一次，并先于 Redis/SQL 关闭。
- B01—B07 全部取得正向行为证据；最后补齐 SMTP DATA ACK/不确定结果、实际页面 HTTP、搜索三秒清理预算，以及 PostgreSQL warning/媒体/用户状态共同提交回滚。共享状态与真实存储的定向 race 通过。没有开展清单外历史问题复现或修复。
- SQL、Ent、S00—S09、原有 50 个其他任务文件和原计划正文保持；Wire 差异仅来自组合根改绑。文档同步真实共存结构，锚点及新增/已有文件 diff 检查见 [完整性证据](baseline/S10/final-integrity-check.json)。不自动提交或推送，索引保持原样。

<a id="s10_completion"></a>
## S10 完成与交接

S10.0—S10.5 **已完成**。roadmap 更新为 **11 / 17**，下一步编写 S11 子计划。交付依据为 [验收与环境限制](baseline/S10/acceptance.md)、[固定问题证据](baseline/S10/fixed-issues-validation.json)、[迁移账本](baseline/S10/migration-ledger.md)和 [后续职责与回退](baseline/S10/handoff.md)。

真实供应商、硬件、外部 TLS 捕获/E2E 的既有限制继续保留；没有把跳过或仅编译当作行为通过。通知协调、首次退订密钥及搜索配置发布代次只保证单服务进程内协作；SMTP 接收后标记失败仍不确定，搜索清理超时/Redis 故障不保证退额。S04 单实例额度边界与 S07 outbox 周期重建恢复限制不变。

回退按子步骤恢复代码、Wire、规则与文档，不需要数据库或缓存数据降级。B01—B07 分别恢复重复投递、退订失效、SMTP 取消失效、页面越界、配置旧值覆盖、共享副本污染和取消退额遗漏风险；保留其他任务文件和既有冻结证据。
