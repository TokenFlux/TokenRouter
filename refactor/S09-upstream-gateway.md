# S09：迁移上游平台与首条网关链，冻结历史问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `1d2519723af97970ff1a429e3d04cfa804f522b2` 为起点，完成 S09.0—S09.8。

实施前将本计划原样保存为 `/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S09-upstream-gateway.md`，再登记 roadmap 链接和“实施中”状态。沿用阶段约定，不另存 `.agents/plans/`。执行记录追加到阶段文件，较大证据保存到 `refactor/baseline/S09/`。

保持 HTTP、配置、数据库 schema、缓存格式、金额算法、standard/simple 和各平台既有取消策略兼容。使用 Go 1.27.0、golangci-lint 2.13.2；Docker 可用。只生成 Wire，不生成 Ent，不自动提交、推送或切换分支；保留其他任务文件和 S00—S08 冻结资料。

**问题处理边界：**

- 计划阶段的问题排查到本计划定稿为止。执行阶段只落实迁移、B01—B04、T01 和约定验证。
- 不继续寻找其他历史问题，不扩展故障注入或顺带优化。
- 本次迁移引入的回归必须修复。
- 常规验证偶然发现清单外历史问题时，只登记；**确实阻塞约定验收时，允许必要复现和最小修复**，单独记录阻塞证据、原实现结果、行为变化及修复结果。
- 原有 lint、已登记跳过及真实供应商环境限制本身不构成扩展修复范围的理由。
- 不扩大多实例支持范围，保留 S07 outbox 周期重建恢复限制。

## 2. 规划证据与固定修复

规划资料已保存在仓库外 `/tmp/tokenrouter-s09-planning/`，包括原实现复现、临时测试覆盖层、日志、源码摘要和固定问题清单。仓库生产代码及测试文件均未修改；S09.0 归档这些资料。

已取得的定向结果：

- 平台包普通测试：**422 条通过、2 条既有跳过**。
- 补齐设置夹具的临时覆盖层下，消费者 unit race：**3987 条通过、1 条失败、2 条既有跳过**；失败为 B04，不能记为整组通过。
- 真实 PostgreSQL/Redis integration race：**61 条通过，无失败或跳过**。
- OpenAI WS 原测试单独重复五次通过，但完整集合和受控并发覆盖层均捕获相同字段竞争，保留全部结果。
- 以上为包含父子测试的事件数，不代替阶段全量验收。

| 编号 | 已复现问题 | 本阶段确定修复 |
| --- | --- | --- |
| B01 | Qoder 三种 SSE 输出已经处理正文和 usage，随后上游错误使读取器返回空结果；Chat 转发层继续丢弃结果，错误路径不提交完成处理 | 允许结果与错误同时返回，保留已观测用量和输出状态。按用户确认，已发生服务且有已观测用量的失败请求，走一次现有结算与记录；保留失败状态，不估算缺失用量、不重新推理 |
| B02 | Qoder 请求在进入流式转发前已取消，仍因提前脱离取消而启动一次上游推理 | 在准备、取得凭据及启动推理的边界检查原请求取消。取消后不新开 attempt；已经进入上游执行的流保持原十五分钟预算内收集尾部 usage |
| B03 | Vertex 获取刷新锁失败后的 200ms 等待忽略取消，之后仍回读缓存并返回 token 成功 | 等待使用 context；取消后立即返回取消错误，不继续回读或交换。保留正常等待时长、锁 key、TTL 和 Redis 故障降级 |
| B04 | OpenAI WS 上行更新 `sessionRequestModel`，下行恢复响应模型时无同步读取，race 检测失败 | 会话默认模型使用统一原子读写，消除直接字段读取；继续使用原有每轮固化的模型链、档位与用量快照，不串行化双向 relay |
| T01 | OpenAI OAuth passthrough 设置替身缺少 `GetMultiple`，独立或部分集合运行时 panic | 按已有 `values` 返回所请求的键，使用现有测试工具隔离设置缓存。保留原断言，不修改生产设置逻辑 |

B01 不把失败改成成功，不执行成功专属的粘性绑定或反馈；保持原会话回滚与“不提交完整会话”的规则。结算失败继续使用既有失败记录机制，日志失败不得再次扣款。

## 3. 接口与迁移顺序

### S09.0：冻结输入并建立首条链契约

记录实际 HEAD、索引、工作区、工具版本、源码摘要、SQL checksum、Ent/Wire 摘要和 S08 完整诊断。逐符号登记平台逻辑、混合文件、生产消费者、测试标签、共享状态、装配、文档锚点及退出阶段。

首条链采用现有 **Qoder Chat Completions** 的非流和 SSE 分支，接口固定如下：

- gateway 使用既有 `AccessSnapshot`、`RoutePlan`、`AccountSnapshot` 和 scheduler Lease，不重新定义这些实体。
- upstream 的单次执行入口为 `Execute(ctx, AttemptInput, OutputSink) (AttemptResult, error)`。输入包含本次模型、协议、请求字节、显式平台选项和受控执行目标，不接收旧实体、Gin 或完整 config。
- `AttemptResult` 独立保存已观测 usage、是否发生服务、上游请求标识、实际模型、档位、耗时、取消与失败分类；非空结果可以与错误同时返回。
- HTTP Adapter 独占响应写入、Header、Flush 和错误 envelope。`OutputSink` 同步接收事件并报告写失败，不建立无界缓冲或每帧 goroutine。
- 分别记录 HTTP 是否提交、是否允许重试、是否出现语义输出；TTFT 单独记录。保留各协议现有边界，包括 OpenAI 结构进度可关闭重试窗口但不产生 TTFT。
- upstream 关闭本次响应体和连接资源；gateway 根据执行结果调用 `AttemptLease.Finish`，请求最终释放 Lease。关闭、失败补偿和重复释放均幂等。
- WS 继续使用独立 `FrameConn`/relay 契约，媒体下载使用独立流式资源接口，不强迫其实现首条 HTTP 接口。
- moderation、错误改写和完成 worker 通过 app/legacybridge 的窄接口接入，分别登记 S10/S11 退出；保持唯一生产实例。

账号授权用例拥有授权会话、一次性消费、凭据缓存、刷新协调及持久化；upstream 只执行供应商交换、签名、解析和原生会话协议。app 将账号投影转换为平台输入，不让 upstream 反向依赖 account。

### S09.1：Qoder 与完整请求链

按“平台客户端 → 非流完整链 → SSE 完整链 → 其余 Qoder 协议”顺序实施。

- 将 COSY 客户端、站点配置、PAT/OAuth 交换、签名、模型能力、会话增量、工具转换和原生流解析迁入 `upstream/qoder`。
- 授权及 token provider 的账号侧职责进入 account，复用已有刷新协调器、CAS 和生命周期；不复制 session cache、singleflight 或锁。
- Qoder Chat 路由直接接入新 gateway/httpapi，贯通鉴权、路由、资金预检、用户等待后二次检查、账号选择、上游和完成处理。
- 同一请求只有一个账号尝试循环；同账号认证恢复保留原次数、资格及预算，不在新 gateway 外再包旧 failover。
- 落实 B01/B02，保持站点/PAT/Cosy 差异、模型映射、thinking、上下文、工具配对、usage 差额及原错误分类。
- 已进入上游的流式请求断开后停止下游写入，继续有界读取 usage，完成读取后释放槽位；等待和非流请求传播取消。断开后不刷新并启动新推理。

**开始 S09.2 前必须通过首条链门禁：**非流成功、SSE 渐进输出、前导后失败、真实输出后失败、等待取消、非流取消、流式断开收尾、上游错误/超时、慢客户端、结算失败及记录失败。

重放测试验证完成处理与持久化幂等，不新增推理响应缓存，也不向真实供应商重复发送请求作影子比较。

### S09.2—S09.8：逐平台迁移

每个平台独立完成“唯一实现、生产改绑、契约测试、依赖门禁及清单更新”，再进入下一平台。后续平台实现首条链验证过的适用接口，其余客户端编排仍保留到 S11。

| 子步骤 | 迁移与保留边界 |
| --- | --- |
| **S09.2 Anthropic / Bedrock** | Anthropic OAuth、Setup Token、请求指纹、metadata、Header、缓存桶、token/usage 客户端进入对应 upstream。旧 `IdentityService` 更名为请求指纹能力，避免与用户 identity 混淆。Bedrock 独立拥有 SigV4/API Key、区域及模型解析、事件帧和错误。纯报文继续复用 protocol；账号状态写入通过 account |
| **S09.3 Gemini / Code Assist** | 迁移 OAuth 变体、project/tier、Drive/Code Assist、原生与兼容流、signature、工具 schema、错误及批量调用原语。保留第三方 API Key 与 Google 官方配额差异。账号发现/保存由 account 编排，批量任务状态机留 S13 |
| **S09.4 Vertex** | 迁移 Service Account JWT 交换、project/location、Claude/Gemini 端点、GCS/JSONL 与 Batch 操作；落实 B03。与 Gemini 共用的 Google 技术原语放在 `upstream/internal/googleauth`，不通过两个具体平台互相 import。保留缓存身份、签名、代理和对象流关闭责任 |
| **S09.5 Antigravity** | 迁移 v1internal 封装、project/身份补丁、原生客户端、转换选项、credits、QuotaPlatform、错误与限流解释。保持混合池资格、模型容量去重及现有有界等待。账号选择和资金规则不进入平台包 |
| **S09.6 CN / Ollama / usageprovider** | 分别迁移 Kimi、Zhipu、DeepSeek、Ollama 和通用上游用量适配。保留平台模式、协议集合、端点、余额/订阅口径与查询限制；account 继续拥有查询 singleflight、身份复核、监控及健康决策。不新增 Ollama 独立平台承诺 |
| **S09.7 Grok** | 迁移 xAI OAuth/API Key、模型、媒体、Voice、搜索、配额及错误解析。Redis OAuth 会话和故障回退由账号授权用例及其存储 Adapter 承接，保留原 key/TTL/一次性消费。CLI Header 与窄范围可重放 403 回退从旧 HTTP Adapter 移入 Grok；视频归属和结算编排仍由旧网关/任务拥有 |
| **S09.8 OpenAI** | 迁移 OAuth/API Key/Codex/PAT/Agent Identity、Responses/Chat/Images/Live、工具、压缩、额度、WS 池、relay 和 liveattestation；落实 B04/T01。保留每轮模型及定价快照、continuation、加密内容恢复、首输出边界、关闭帧和 Darwin/非 Darwin 差异。完整入站 WS 编排留 S11 |

各平台统一遵守以下边界：

- 同账号的协议恢复、刷新交换和端点回退保留既有条件、次数及预算；账号切换、用户计费和全局重试由调用方拥有。
- GetToken/Refresh/Probe 返回平台结果和健康观测，条件写入、缓存失效及调度发布由 account 决定，沿用 S06 的身份比较和失败语义。
- egress 提供策略，app 投影技术参数，upstream 复用唯一 HTTP 池；不修改共享客户端，不引入 DNS pinning。
- 通用 wire、纯转换继续由 protocol 唯一实现；具体平台不得依赖另一具体平台实现。
- 可变状态保持原作用域及唯一实例；跨请求数据提供独立副本，凭据不进入公共结果或普通日志。
- 构造不启动后台任务；授权会话清理、凭据构建、平台缓存和按需连接由 app 登记拥有者。停止不提前关闭仍被在途任务使用的依赖。

## 4. 验证、门禁与文档

每批运行新包、旧转接及直接消费者的普通/unit 测试；锁、认证缓存和资金链采用隔离 PostgreSQL/Redis。固定问题复现转为回归测试，原失败日志保留。

| 验证面 | 必须取得的证据 |
| --- | --- |
| 首条完整链 | 实际新模块贯通 HTTP 至结算/usage；每请求 attempt、供应商调用、资源数量及结算次数可核对 |
| B01/B02 | 三种 Qoder SSE 的结果与错误共存；已观测用量仅结算一次；推理前取消不发送；断开后的尾部用量和完成释放保持 |
| B03/B04/T01 | Vertex 等锁取消；WS 双向受控竞争及原多轮模型断言；OpenAI 夹具独立、集合及缓存冷启动执行 |
| 协议 | 非流报文与流事件顺序、未知字段、工具 ID/namespace、thinking/signature、usage、终态和错误；不能只比较最终文本 |
| 认证与传输 | OAuth 差异、刷新竞争/CAS、代理失败、Header 顺序、TLS/HTTP2、重定向、请求重放资格及响应体关闭 |
| 媒体与任务 | 图片实际产出、视频完成触发、Voice 音频观测、搜索计量、Batch/GCS/JSONL 流及取消；资金算法与任务状态机不变 |
| 生命周期 | 唯一实例、按需启动、停止后不认领、在途等待、超时报告、Redis/SQL 最后关闭 |
| 装配 | 原路由、中间件顺序、账号授权/测试/用量接口、standard/simple、维护命令和版本注入 |

共享状态、平台会话、WS 池执行定向 race；真实存储竞争执行 integration race，不扩展全仓 race 或 benchmark。外部认证和供应商协议使用本地 HTTP/TLS/WS、签名验证及脱敏夹具，不使用生产凭据。

每批同步 depguard：约束 gateway 核心、upstream 契约、具体平台、技术共享叶子、Redis/HTTP Adapter 和 app。删除迁出文件的旧许可；新增许可精确到文件/import，保留原 service/handler/protocol 规则。

可丢弃夹具验证合法方向、旧许可、新文件拒绝、旧文件新增禁止依赖、迁出例外失效、平台互引拒绝及正常 Adapter。用 go list 核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux，保存诊断后删除夹具。

阶段收尾统一使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

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

另验证两个维护命令构建、Wire 再生成无差异、真实前端产物的 embed 测试、standard/simple 启动及 SIGTERM 上游资源停止顺序。

S08 lint 基线为 **1 / 285 / 17**，按路径映射、规则和完整消息比较。不得扩大忽略规则或削弱断言；跳过和仅编译不算行为通过。真实供应商、硬件及外部 TLS/E2E 限制继续单列。

同步现有系统架构、网关生命周期、各平台上游、账号维护、传输安全、协议能力、HTTP、配置及开发文档，保留稳定锚点，描述实际的新旧共存结构。

## 5. 完成与交接

S09 完成须同时满足：

- Qoder 非流/SSE 完整链先于其余平台验收，并成为对应路由的唯一执行路径。
- 各平台实现已接入生产调用及适用的新接口；核心不反向依赖旧业务、Gin、config 或具体存储。
- B01—B04、T01 验证通过；任何验收阻塞例外都有独立复现和最小修复记录。
- 调度、凭据、输出、部分用量、完成处理和资源释放的所有者明确，没有第二套状态、资金实现或全局重试。
- 兼容入口、消费者、构建条件、Wire、依赖许可及文档可追踪；审核/搜索交 S10，其余入站编排和完成处理交 S11，商业与任务交 S12/S13，维护和兼容清理交 S14—S16。
- SQL、Ent、S00—S08 冻结资料及其他任务文件无意外变化；Wire 差异可解释，新增及已有文件的 diff 检查通过。必要验证未完成时保持“待验”。

满足后 roadmap 更新为 **10 / 17**、S09 已完成，下一步编写 S10 子计划。交付可审查差异，不自动提交。

回退按子步骤恢复代码、装配、规则和文档，不涉及数据库或缓存格式降级。B01—B04 回退会分别恢复部分用量遗漏、取消后推理、取消等待失效及 WS 数据竞争，执行记录须明确标注。


---

## 执行记录

### S09.0：冻结实施输入（2026-09-14）

计划原文已保存，实际 HEAD 与计划一致，索引为空；源码、SQL、Ent/Wire、既有冻结资料及其他任务文件摘要见 [开始快照](baseline/S09/start.json) 与 [文件摘要](baseline/S09/initial-file-checksums.json.gz)。规划复现和定向结果已原样归档到 [planning](baseline/S09/planning/planning-state.json)，固定修复范围为 B01—B04、T01。当前开始 Qoder 平台提取，首条链门禁通过前不进入 S09.2。


### S09.1：Qoder 平台与 Chat 接入（2026-09-14，首条链待验）

Qoder 客户端、请求/流转换和会话增量状态已迁至 upstream/qoder，旧入口保留别名与委托；本地凭据读取已隔离。原始单行 500 MiB 扫描上限、站点/签名、请求字段和取消预算保持。协议 metadata 解析及会话标识原语按实际共享消费者提取，未启动 Anthropic 平台迁移。

B01/B02 原复现与既有取消释放回归已有 [13 条 race 通过事件](baseline/S09/qoder-fixed-regressions.result.json)。新 Execute 接入后的 [450 条普通事件](baseline/S09/qoder-executor-wired-normal.result.json)通过。Chat 的新 gateway 核心、HTTP 及 app/Wire 已接入实际路由，[Wire](baseline/S09/qoder-chat-wire.result.json)和[当前调用者回归](baseline/S09/qoder-chat-tests.result.json)通过；这些结果尚不能代替完整链资金/Redis 验收，S09.2 尚未开始。

当前正在补齐首条链真实存储、等待与完成验证，并清理无消费者的生成兼容入口。所有中间编译/门禁失败与后续结果分别留档，不扩大忽略规则。


### 首条链的真实存储组合验证（2026-09-14）

[PostgreSQL/Redis 集成 race](baseline/S09/qoder-http-storage-chain-2.result.json)4 条事件通过，无跳过。测试通过真实 Key 存储与 apikey 认证、routing 计划、账号读库复核、scheduler Lease/Redis、Qoder 原生 HTTP 客户端、本地供应商、原完成 worker、pricing、billing 闭合事务与 usage 批处理，覆盖非流、SSE 和部分失败。完成动作重放不重新调用供应商，余额/Key 累计与用量事实只写一次，用户费用和账号成本分别校验，用户/账号槽位最终为零。

首次夹具运行因 AccountStore 缺少其必需的 Group 投影而失败，已按实际 app 装配补齐，未改生产存储。该组合验证不等同于完整应用进程验收，也尚未覆盖首条链门禁的全部取消/超时/失败场景。S09.2 继续保持未开始。


### Qoder 账号职责和首条链门禁进展（2026-09-14）

账号会话缓存、授权完成认领及刷新资格/凭据合并已迁入 account；平台交换进入 upstream/qoder；授权 HTTP 进入 account/httpapi。app 为凭据构建登记取消和等待，授权停止也等待已进入交换。旧入口仅调用与投影，未复制单飞、缓存、会话或刷新 CAS。详见[迁移账本](baseline/S09/migration-ledger.md)。

- [凭据等待者与停止 race](baseline/S09/qoder-cache-race.result.json)：484 条通过，无失败或跳过。
- [授权及直接消费者 unit race](baseline/S09/qoder-authorization-race.result.json)：522 条通过，无失败或跳过；后续刷新拆分的[普通回归](baseline/S09/qoder-refresh-move-tests.result.json)425 条通过。
- [真实 PostgreSQL/Redis 首链与等待门禁](baseline/S09/qoder-storage-wait-gates.result.json)：9 条通过，含用户等待取消、心跳写失败、实际等待后的资金复核、非流/SSE、部分失败、上游超时及断开后尾部用量。未跳过。
- [同步背压、非流取消、前导失败和停止门禁](baseline/S09/qoder-final-first-chain.result.json)：16 条 race 通过。前导/usage 可以提交响应并关闭重试窗口，不产生语义首输出，也不据此结算。
- [实际旧完成用例失败门禁](baseline/S09/qoder-completion-failures.result.json)：3 条 unit race 通过。供应商部分失败后只调用完成一次；结算失败保留未结算事实，日志失败不再次扣款。该项存储错误使用既有替身，真实原子性由前述 PostgreSQL/Redis 用例独立证明。

中间拆分引入的编译/未使用声明问题已修正，诊断保留。一次 integration lint 与尚未退出的普通 lint 冲突，未实际检查；已登记并改为三集合串行补验，不能把命令锁退出计为验收结果。当前收尾 Qoder 依赖门禁，尚未开始 S09.2，也未宣布 S09 完成。


### S09.1 首条链门禁通过（2026-09-14）

[门禁结论及逐项证据](baseline/S09/qoder-first-chain-gate.json)已记录。当前消费者 unit race 共 [560 条通过](baseline/S09/qoder-final-consumer-race.result.json)，真实存储的最终复验 [9 条通过](baseline/S09/qoder-final-storage-race.result.json)，无失败或跳过；生命周期接入后 [443 条定向 race 通过](baseline/S09/qoder-lifecycle-race.result.json)。这些集合有重复，不相加作为唯一覆盖数量。

app 以 `QoderRequestsAndAttempts` 同步登记新 Chat 全请求、其余旧 Qoder HTTP 请求和实际平台尝试。停止顺序 15 先封闭新进入并等待在途，完成之后才停止生产者/队列/连接；超时保留未完成状态及依赖，不修改客户端取消或十五分钟上游预算。凭据会话停止顺序 30，原完成队列顺序保持。

[21 项 depguard 夹具](baseline/S09/qoder-depguard-fixtures.json)覆盖普通/unit/integration 的合法 Adapter、新文件拒绝、旧文件新增禁止 import、实际迁出后例外失效、精确 import 子包拒绝和平台互引。夹具全部删除。[构建集合](baseline/S09/qoder-buildsets.json)核对普通/unit/integration/wireinject/embed/e2e 及 Darwin/Linux 文件，未把 go list 当成行为测试。

[lint 逐项比较](baseline/S09/qoder-lint-comparison.json)为 1 / 285 / 17，与 S08 的路径、规则和消息一致；只规范化终端文本丢失的消息尾部空白。新增兼容死代码及 integration 测试断言检查已解决，原六项 unit depguard 仍在。此后可以开始 S09.2 Anthropic/Bedrock；S09 的其余平台、完整进程和全量收尾仍待执行。


### S09.2 中间进展与偶遇测试现象（2026-09-14）

Anthropic 的常量、授权原语、请求指纹、Redis 指纹存储、OAuth/usage 客户端、请求规范化、工具映射及标准/直通流响应逐批接入原生实现；Bedrock 的区域规则、请求、签名、二进制帧、同账号重试及 Execute 已改绑。账号授权的状态和完成编排进入 account，旧入口保留投影与委托。此子步骤仍在实施，尚未验收完成。

[宽范围 unit 消费者运行](baseline/S09/anthropic-exchange-unit.result.json)在 `TestForwardStreaming_ServiceTierPropagatedToResult` 触发 `openAIFastPolicyRepoStub.GetMultiple` 的 panic。这不是冻结 T01 的替身，不能将两者混记。该文件与 HEAD 相同，但尚未在旧 HEAD 执行该选择，因此只保存[观察记录](baseline/S09/incidental-observations.json)，不宣称历史复现或修复。当前不扩展排查，先完成本平台定向验证；S09.8/全量验收确被其阻塞时再按已授权的例外处理。

响应适配带入已有 Header/提交状态，以保持等待心跳后的原 `Written` 判定。新增回归依赖既有 unit 工具，已修正其构建标签；中间编译失败照实保留，不能计作行为通过。


### Anthropic/Bedrock 执行接口进展（2026-09-14）

标准和 API Key 直通的生产链已调用原生 `Executor`，响应体及单账号恢复循环归平台所有；Bedrock 已使用独立 `Executor`。直通仍不增加 400 请求体降级或原来没有的接受回调。当前[请求规范化普通回归](baseline/S09/anthropic-body-tests-4.result.json)620 条通过，[原生交换与响应的定向 unit](baseline/S09/anthropic-execute-unit.result.json)160 条通过，均无失败或跳过。直接 SDK/状态测试与消费者分开记录，无匹配的包级结果不计入行为通过。

新增输出状态回归 `TestS09AnthropicPriorHeartbeatPreservesReadFailureBoundary` 已在其实际 unit 集合执行通过，证明已有心跳提交后不会错误地恢复为“尚未提交”从而进入另一条 failover 路径。该修正属于迁移适配，未变更原实现的重试策略。[当前迁移账本](baseline/S09/migration-ledger.md)说明仍保留的调用方策略和后续退出位置。S09.2 的剩余构造/门禁/完整定向验收继续实施，尚未进入 S09.3。


### Anthropic/Bedrock 请求、账号与观测边界（2026-09-14，仍在本批验收）

Beta 值与规则、messages 缓存断点及 count_tokens 的独立构造已迁入平台；纯 thinking/tool 字节修复进入 protocol/anthropic，账号/模型适用性继续由调用方显式选择。原修复使用的零拷贝切片只在实际 repair.go 精确许可 unsafe，不开放其它协议文件。

Claude token 读取、回填政策、版本比较和刷新资格/合并进入 account，旧 provider 使用原 token cache 和 S06 刷新协调器的窄投影；未新增锁、缓存或改变平台等待策略。运行时配置和动态设置仍保留各自原取值时点。

[请求修复及 count_tokens unit](baseline/S09/anthropic-repair-unit2.result.json)取得 1018 条通过事件；[Anthropic 新 Execute 的 race](baseline/S09/anthropic-execute-contract-race4.result.json)9 条、[Bedrock 新 Execute 的 race](baseline/S09/bedrock-execute-contract-race.result.json)3 条通过。新接口分别报告显式零用量、语义输出与旧 TTFT，不把前导当语义。标准 Anthropic 终态 error 的原读取器返回空结果，新接口通过同步观测保留此前事实，旧调用方的失败/完成决策仍保持。测试夹具自身的本地服务关闭等待已修正，首次失败/中断日志保留，不记作通过。

[Redis 指纹及 Bedrock 消费者 integration race](baseline/S09/anthropic-bedrock-storage-race.result.json)240 条事件通过，无失败或跳过。事件数包括常规父子测试，不能全部称为数据库事务测试；具体测试名及运行容器以 JSON 日志为准。正在进行最后的本批定向 race、三种 lint 逐项比较和依赖夹具验证，尚未开始 S09.3。


### 定向 race 的测试隔离与标签核对（2026-09-14）

[默认并发消费者 race](baseline/S09/anthropic-consumer-race.result.json)未通过：1177 条通过、73 条失败事件，同时 repository unit 缺少迁移后的私有 `fingerprintKey` 测试入口。后者属于本批迁移，已把原测试及全部断言移到实际 Redis Adapter 包，不复制函数。

race 栈指向旧并行测试的 Gin 全局模式设置，已列入 [I02](baseline/S09/incidental-observations.json)，未主动扩展历史复现或修复。后续旧 HTTP 消费者 race 使用 `-parallel=1` 隔离测试级全局状态，各测试内部并发仍执行；默认并发失败保留，不能替换成“默认集合通过”。

标签核对确认上述 240 条 integration 事件中真实存储为 Redis 指纹套件；Bedrock 区域路由文件名虽含 integration，实际构建标签为 unit，采用本地 HTTP 签名夹具。已纠正文字描述，不把它称为 PostgreSQL 行为证据；数据库身份/CAS 与资金链继续由原 S06 及本阶段对应真实存储用例提供证据。


### S09.2 子步骤门禁通过（2026-09-14）

[本批门禁结论](baseline/S09/anthropic-bedrock-gate.json)已冻结，随后进入 S09.3。最终普通定向 [928 条通过](baseline/S09/anthropic-final-normal.result.json)、隔离测试级全局状态的 unit race [1388 条通过](baseline/S09/anthropic-consumer-isolated-race.result.json)、原生执行/授权/指纹等 race [49 条通过](baseline/S09/anthropic-bedrock-final-native-race.result.json)；集合重叠，不相加。真实 PostgreSQL/Redis 的新 Claude 凭据链 [3 条通过](baseline/S09/claude-native-credential-storage-race.result.json)，既有 CAS/外层回滚和 token cache [6 条通过](baseline/S09/anthropic-credential-storage-race.result.json)。上述通过集合均无失败或跳过，默认并发 race 的 I02 失败仍单列。

[三种 lint](baseline/S09/anthropic-lint-comparison.json)与 S08 的 1 / 285 / 17 逐文件、规则、消息和重数一致。[24 项夹具](baseline/S09/anthropic-bedrock-depguard-fixtures.json)全部匹配预期，临时文件已删除；[各标签/OS 选择](baseline/S09/anthropic-bedrock-buildsets.json)、[Wire](baseline/S09/anthropic-wire.result.json)及[逐符号账本](baseline/S09/migration-ledger.md)齐备。原计划前缀、SQL/Ent、旧冻结资料及其他任务内容的[中间核对](baseline/S09/anthropic-integrity-check.json)无意外变化，索引仍为空。

S09.3—S09.8、B03/B04/T01 及完整阶段验收尚未完成，roadmap 维持 9 / 17、S09 实施中。此处仅通过 Anthropic/Bedrock 子步骤，不宣布 S09 完成。


### S09.3 Gemini/Code Assist 首批迁移（2026-09-14，实施中）

模型与 OAuth/Drive/Code Assist 客户端已迁至 `upstream/gemini` 及 `codeassist`；原 `pkg/gemini`、`pkg/geminicli` 已清零删除。授权会话、三类 OAuth 编排、project/tier 发现、token 回填与刷新资格进入 account；Google OAuth、Code Assist 和 Batch 的值类型归 protocol，旧构造和用户可见类型保留投影/别名。管理授权 HTTP 已接入 account/httpapi，app 调用可等待停止。

[账号、HTTP 和客户端 unit](baseline/S09/gemini-token-http-unit.result.json)510 条通过；[共享 token/版本策略与 Claude 回归](baseline/S09/gemini-account-shared-unit.result.json)600 条通过；[响应/流移植](baseline/S09/gemini-response-unit2.result.json)470 条通过；[签名及路径原语](baseline/S09/gemini-wire-unit2.result.json)521 条通过；[唯一请求构造](baseline/S09/gemini-request-unit.result.json)520 条通过。均无失败或跳过，集合重叠不合计。

最初普通选择误含 OpenAI OAuth，命中冻结 T01 的 `openAIPassthroughSettingRepoStub.GetMultiple`：[该次结果](baseline/S09/gemini-primitives-normal.result.json)383 条通过、1 条失败，不能算整组通过。T01 继续在 S09.8 修复；本批没有新增历史问题排查。

Gemini 的 Messages/原生/Chat/Responses 流和非流响应已迁入平台，仍调用 S03 bridge，保留各自缓冲、输出和断开语义。新的 RequestPlan 已接入原生产构造点，继续在获取 token 后读取 project，API Key 与 OAuth/Vertex 的 body/端点差异保留。Batch 客户端、JSONL 编码和与创作共用的 wire 类型正在接入，任务状态机留 S13。

本批中间编译失败按原样归档；涉及移动后的私有测试函数和创作台共享报文类型，已按真实消费者补齐别名或移动测试，未删断言。原账号内重试循环、统一 Execute 接线、创作网络调用和本批完整门禁仍待完成，尚未开始 S09.4。


### Gemini 原生链与媒体验证进展（2026-09-14）

三类原入口已通过同一个平台 Execute 闭合各自原有的账号内交换/响应流程；[接线定向 unit](baseline/S09/gemini-execute-unit2.result.json)520 条通过。估算回退使用独立字段，保留 count_tokens 原早退，不写入实际 usage。输出事实与原 TTFT 分开，保留部分失败的已观测数据，但旧网关不因此改变失败请求的结算条件。

- [本地 HTTP/SSE 原生执行 race](baseline/S09/gemini-native-execute-race.result.json)：10 条通过，覆盖四种非流协议、显式零用量、渐进输出、部分失败、取消和估算回退。
- [Gemini 原生刷新→原 CAS→Redis 回填](baseline/S09/gemini-token-storage-race.result.json)：真实 PostgreSQL/Redis 下 3 条通过，管理员交错凭据不被旧刷新覆盖。
- [图片/Batch 本地 TLS race](baseline/S09/gemini-media-race.result.json)：3 条通过，原请求字段、最终 Header 覆写、最后图片选择、上传/查询/取消/下载及关闭保持。
- [流与授权生命周期 race](baseline/S09/gemini-lifecycle-stream-race.result.json)：15 条通过；测试超时保留关闭失败结果，并等待旧有限刷新流程结束，不修改其既有重试次数。
- [Resource Manager 本地协议与账号回退](baseline/S09/gemini-resource-local-unit.result.json)：42 条通过。账号测试通过新端口隔离原外部查询失败依赖，原断言不变，真实查询解析由本地 HTTP 夹具覆盖。

共用 SSE 完整帧含义提取到 protocol/bridge 后，一次宽选择再次命中 I01 对应的 OpenAI 设置替身：[该次结果](baseline/S09/gemini-qoder-output-unit.result.json)保留失败。随后限定实际 Gemini/Qoder/创作消费者的[unit 回归](baseline/S09/gemini-shared-compat-unit.result.json)1076 条通过；未修改 I01/T01，后续仍按既定范围处理。

当前还在清理迁移后的无消费者兼容项、依赖门禁及完成批次验证。S09.3 尚未宣布通过，不进入 S09.4。


### S09.3 完成门禁（2026-09-14）

Gemini/Code Assist 已接入唯一原生实现，三类执行、授权/token、Batch 与创作技术调用的生产消费者已改绑。详见[本批门禁](baseline/S09/gemini-gate.json)及[迁移清单](baseline/S09/migration-ledger.md)。定向普通 640、消费者 race 1108、真实存储 race 24 条通过事件均无失败或跳过；集合相互重叠，不累计为独立测试数量。消费者 race 采用测试级串行，保留每个测试内部并发；默认并发全局 Gin 设置竞争的失败证据继续保留。

[21 项依赖夹具](baseline/S09/gemini-depguard-fixtures.json)全部符合预期并已删除；[lint 逐项核对](baseline/S09/gemini-lint-comparison.json)保持 1 / 285 / 17，无新增/消失诊断。[构建选择](baseline/S09/gemini-buildsets.json)与符号引用账本只作为归属及可加载证据。T01/I01 已出现的宽泛选择失败不计通过，不在本批提前修复。

计划原文、SQL/Ent、原冻结资料与其他任务文件摘要核对无异常。下一步 S09.4 Vertex；S09 仍实施中，roadmap 不增加完成数量。


### S09.4 执行与 B03（2026-09-14，收尾验证中）

Vertex 的端点、Claude body/Beta、Batch/GCS/JSONL 已由 upstream/vertex 唯一实现；服务账号 JWT 与代理交换下沉 upstream/internal/googleauth。账号历史凭据字段、project/location 选择和缓存/锁协调归 account，旧入口只投影。Gemini/Claude 使用既有单次 Execute 链，通过显式端点和认证输入接入 Vertex，不建立两个具体平台的 import，也不新增全局重试。

B03 只修正锁竞争后的取消等待：保留 200ms、key、TTL、故障降级；原复现 [planning/vertex-repro.jsonl](baseline/S09/planning/vertex-repro.jsonl) 为预期失败，新 [vertex-request-race](baseline/S09/vertex-request-race.result.json) 包含原断言通过；[真实 Redis 回归](baseline/S09/vertex-storage-race.result.json)还验证取消者不释放其他持有者的锁、缓存命中不交换。该组同时回归既有 Gemini/Claude CAS，共 10 条通过事件，无失败/跳过。

[消费者定向 race](baseline/S09/vertex-final-consumer-race.result.json) 988 条通过事件，测试级串行隔离已登记 Gin 全局状态，保留测试内并发；[本地 RSA/TLS/GCS 行为](baseline/S09/vertex-native-race.result.json) 8 条通过事件。[Wire 生成](baseline/S09/vertex-wire.result.json)完成，只生成 Wire。lint 中本批引入的未使用转接和未检查类型断言已修正；尚待最终三标签比较及依赖夹具，不先宣布本平台或 S09 全量完成。


### S09.4 本批门禁通过（2026-09-14）

[Vertex 门禁](baseline/S09/vertex-gate.json)通过：[21 项方向夹具](baseline/S09/vertex-depguard-fixtures.json)全部符合预期并已删除，[lint 逐项比较](baseline/S09/vertex-lint-comparison.json)仍为 1 / 285 / 17，路径/规则/完整消息无新增或消失。[4536 项保护摘要](baseline/S09/vertex-integrity-check.json)及原计划正文保持；索引为空。下一步 S09.5 Antigravity，S09 总阶段仍实施中。


### S09.5 已迁授权与输出（2026-09-14，实施中）

旧 pkg/antigravity 已删除并改绑唯一 upstream 实现，授权会话/发现/隐私/token 冷却与刷新规则进入 account，管理 HTTP 已改绑。流处理从旧 service 提取到同步 sink，保留原前导缓冲、非流收集、终态、失败与断开 drain 的差异。机械迁移引入的 wire 类型/桥接 Runtime 参数错误已修正，[定向 unit](baseline/S09/antigravity-response-unit2.result.json) 749 条通过事件，无失败/跳过；详见[清单](baseline/S09/migration-ledger.md)。下一步原账号内重试、额度观测和完整单次执行改绑；B04/T01 仍保留到 S09.8，不扩大问题排查。


### S09.5 执行链及存储验证（2026-09-14，门禁收尾中）

五条 Antigravity 分支与探测已接入 native Execute/Probe，签名/budget/模型恢复和 credits/容量重试不再保留第二份实现。旧入站适配保留 HTTP/账号动作和完成结果，尚未迁移的全局重试/资金完成仍交 S11。授权、quota、健康状态规则归 account；app 绑定同一 NativeUpstreamAttempts，授权 Stop 使用原独立总预算剩余 context。

[真实 PostgreSQL/Redis CAS](baseline/S09/antigravity-native-storage-race.result.json) 3 条通过事件，[授权会话/关闭 race](baseline/S09/antigravity-auth-lifecycle-race.result.json) 2 条通过事件，均无失败或跳过。[精确额度测试](baseline/S09/antigravity-quota-selected-unit.result.json) 57 条通过事件；宽泛 Tier 选择再次命中的 I01 失败单独保留，不在本批修复。unit lint 经旧/新路径映射已与 S08 的 285 项逐条一致；normal/integration 中只供 unit 的旧转接已移至带 unit 标签文件，尚待重新验证。不以这些定向结果代替阶段全量验收。


### S09.5 本批门禁通过（2026-09-14）

[Antigravity 门禁](baseline/S09/antigravity-gate.json)通过：[21 项方向夹具](baseline/S09/antigravity-depguard-fixtures.json)全部符合预期并已删除；[lint 路径映射逐项比较](baseline/S09/antigravity-lint-comparison.json)保持 1 / 285 / 17，旧两项 roundTripperFunc unused 随路径迁移保留。消费者定向 race 435、普通 128 条通过事件，均无失败/跳过；原生整包普通 race 47、unit race 183 条通过事件，后续新增静态分支 3 条及完整 Execute 15 条另有独立结果，不累加为独立测试数量。

新 AttemptResult 的部分 usage 观测补齐了分批更新保留：读取错误不能用后来的不完整字段清掉之前已观察的数值；这是本次新接口的实现修正，未增加历史问题清单，不改变旧 Antigravity 入口返回错误后的结算规则。[新结果回归](baseline/S09/antigravity-observed-partial-race.result.json) 15 条、[直接错误/断开消费者](baseline/S09/antigravity-output-consumer-race.result.json) 15 条通过事件。账号会话消费/停止和真实 PostgreSQL/Redis CAS 证据分别保留。

[47 文件声明清单](baseline/S09/antigravity-file-declarations.json.gz)、三个标签的类型引用和[构建选择](baseline/S09/antigravity-buildsets.json)已更新；[4536 项保护摘要](baseline/S09/antigravity-integrity-check.json)及原计划正文保持，索引为空。下一步 S09.6，S09 总阶段仍实施中，roadmap 保持 9 / 17。


### 2026-09-14：S09.6 查询与 Ollama 原生实现接入

七种只读查询实现、Ollama HTML/抓取/Chat wire 规则及其明确技术契约完成提取，账号查询和周期维护继续持有唯一共享状态，详见 [迁移账本](baseline/S09/migration-ledger.md)。实际查询注册器已由 app 直接绑定；旧入口只做参数投影和委托。

- 原消费者 unit race：261 条通过事件，无失败/跳过（`usage-consumer-race.result.json`）。首次同名运行因本阶段编译输入变更而未执行测试，其诊断单列 `usage-consumer-race-concurrent-edit`，重跑前停止源码变更。
- 七适配器本地 TLS 与迁移后的 Ollama HTML 原断言：15 条通过事件（`usage-native-tls2.result.json`）。首轮新增夹具的 limits 模式和 Sub2API planName 不完整已按原解析契约补齐，未修改生产校验；首轮失败日志保留。
- 普通 lint 恢复原有 1 项，unit/integration 诊断与真实存储 race 尚在核对。本子步骤保持实施中，未进入 S09.7。


### 2026-09-14：S09.6 门禁通过

本批普通定向 148、消费者 unit race 261、本地 TLS/原 HTML race 21、真实 PostgreSQL/Redis integration race 80 条通过事件，各自无失败或跳过，不累计重叠事件。24 组 depguard 夹具符合预期且已删除，七种构建集合无加载错误；三标签类型引用清单分别为 5981 / 6040 / 5981 条，31 个文件已登记声明与摘要。

normal/unit/integration lint 按 S08 最终原文件、规则和完整消息比较为 1 / 285 / 17，无新增或减少。Wire 生成成功，4,536 项受保护输入及计划原文摘要不变，索引为空，差异检查通过。详见 [S09.6 验收门禁](baseline/S09/usage-gate.json)。本批未修复清单外历史问题；继续 S09.7 Grok，S09 总体仍实施中。


### 2026-09-14：S09.7 Grok 授权与凭据迁移进行中

- 旧 `pkg/xai` 原生协议、动态模型、OAuth/SSO、额度和账单解析已整体迁入 `upstream/grok`，所有现存 import 改绑且保留原 xai 别名；未复制动态模型状态。会话存储从混合 oauth.go 拆入 `account.GrokSessionStore` 和 `account/rediscache`，保留 `oauth:session:xai`、30 分钟 TTL、五分钟清理、一次性标记和仅 Redis 写失败才允许本地回退。
- `repository.GrokOAuthClient` 的 HTTP/SSO/密码交换实现迁入 `upstream/grok.OAuthClient`；原 OAuth unit 断言同迁，旧构造器只返回该唯一客户端。共享 req/HTTP 客户端仍使用原 infra 池。CLI 403 回退移入平台的请求派生 transport，保留资格、64KiB 缓冲、请求重放、Header 删除和失败原响应；传输 CLI 最低版本判断与账单/原生 CLI 判断原本不同，保持各自原条件，未统一。
- 账号 `GrokAuthorization` 接管授权校验、会话消费、令牌解析及凭据组装；输入为 wire 和显式函数端口，不依赖旧 service/config。加入本阶段要求的活动拥有与 StopContext，app 将退出剩余预算传入。旧 `GrokOAuthService` 仅投影和委托，同一会话指针供兼容测试读取。
- `account.GrokTokenSource` 与 `GrokTokenRefresher` 接管请求/手动凭据读取、刷新等待、预热窗口和合并；通过原 S06 刷新 API 端口保持 CAS/回读及错误身份，未创建第二把锁或缓存。凭据失败快照封装进入 account，网关仍执行原错误分类、账号切换和失败处理预算。
- 为避免原生层反向引用账号/旧包装，Grok 展示值与复制移动到中立 `upstream/usageview`，账号旧叶子委托；令牌交换和授权输入归 `protocol/grok`。静态 URL 验证实现下沉到仍由 egress 拥有的 `egress/urlpolicy` 纯叶子，原入口委托；这是接入所需的依赖拆分，不改变 allowlist、私网判断或 DNS 执行。
- 定向验证：初始原生 54 条通过；客户端/原 transport/Redis fallback/URL 73 条通过；授权与凭据两轮 unit 分别 954 条通过，无失败或跳过。前一轮 transport 私有测试类型引用编译失败已随原测试改绑，失败日志保留。当前普通 lint 仅剩迁移后的无生产消费者包装，正核对 unit 后删除无用入口。

S09.7 尚未通过门禁：媒体、Voice、搜索、探测、HTTP 和适用 Execute 改绑及本批完整验证仍待完成。S09.8 的 B04/T01 仍未实施，S09 整体不宣布完成。


#### S09.7 增量：原生观测、帧中继与 Voice Execute

搜索调用去重、内容/解码错误分类、Grok ping 过滤、固定 Header 与 Realtime 双向帧 relay 已进入 `upstream/grok`。SSE data/event 公共解析复用 `protocol/openai`；`upstream.FrameConn` 保留 WS 独立同步帧契约，旧 SDK Adapter 不改变两方向的读取/写入顺序。音频单位使用 `protocol.AudioUsage`，原 TTS/STT/Realtime 计算顺序和下限保持，不与 Qoder 的禁止缺失用量估算规则混用。

Voice HTTP 实际入口改绑 `VoiceExecutor.Execute`，持有响应体并使用同步 OutputSink；旧入口保留取得凭据、既有脱离取消策略、错误处理和稳定资金 ID。应用唯一 NativeUpstreamAttempts 同批覆盖 OpenAIGateway 的原生执行。原消费者 unit 935 条通过；新增本地 HTTP 原生成功/错误/关闭/计量测试 race 5 条通过，无失败或跳过。代码迁移过程的编译差异已分别保留失败日志，均为本阶段回归，不属于历史问题审计。

Grok 媒体请求/几何转换正在迁移到无状态 MediaCodec，使用外层注入的原纯归一化函数；模型与任务资金规则仍在原调用方。共享 ImageUpload/data URL 与 multipart 模型字段改写由 upstream 技术层持有，避免 Grok 反向依赖具体 OpenAI 平台。媒体单次执行、授权 HTTP、探测和 S09.7 完整门禁仍待完成。

### 2026-09-15：S09.7 HTTP、媒体及文本适配继续迁移

Grok 额度查询的合并与状态编排进入 `account.GrokQuotaService`，共享原 `ProbeRuntime`；原生账单/主动探测 HTTP、限制读取及关闭由 upstream/grok 执行。SSO 导入的去重、三 worker、部分成功和 panic 隔离进入 account，实际路由使用 account/httpapi，app 绑定原队列、授权与配额唯一实例。旧 handler 只保留兼容构造和委托；Wire 已生成验证。

Voice、媒体单次执行及独立视频内容资源已生产改绑。视频的原 ContentLength 在新资源投影中曾遗漏，原 `TestForwardGrokMediaContentStreamsFullResponseWithSafeDefaults` 捕获；补齐同一响应的 ContentLength 后通过，未改变原语义。保留该阶段自身回归的失败/修复日志（`grok-video-content-unit` / `grok-video-content-unit2`）。本地 TLS/WS 验证分别记录双向帧、signed/relay Range、无凭据泄露和响应关闭，未调用真实供应商。

Grok 文本 body、Compact、工具能力、图片输入与缓存 seed/工具路由已提取到无共享状态的原生 codec。通用 UseNumber/严格单 JSON、非转义序列化、工具续链及 SSE 文档 scanner 进入 protocol；HTTP 读取仍在所属适配器。原缓存种子算法通过显式回调在原分支按需调用，不建立第二份缓存。一次同账号 opaque replay 解码恢复循环迁入平台；保留一次上限、400 条件、原裁剪和错误恢复顺序。通用工具流 IO 管道进入 upstream 技术层，保留原逐帧 Flush/上限/关闭语义。

[文本和缓存定向 unit](baseline/S09/grok-body-cache-unit.result.json) 1010 条、[请求/恢复与工具流定向 unit](baseline/S09/grok-exchange-stream-unit.result.json) 1015 条测试事件通过，无失败/跳过。[HTTP 及消费者 race](baseline/S09/grok-final-http-race.result.json) 976 条通过（顶层测试 `-parallel=1` 隔离已登记的 Gin 全局状态，保留测试内并发）。这些集合存在重叠，不累计为独立测试数量。仍需完成 S09.7 剩余平台执行、账号观测、门禁和最终证据，再进入 S09.8；S09 仍实施中。

### 2026-09-15：S09.7 结果观察与账号健康收敛

Responses 原生执行结果分别接收 HTTP 提交、重试窗口关闭、语义输出、TTFT 与已观测 usage；不从响应 ID 推断已服务。共享流读取器保持原错误返回语义，新增观察入口可保留错误旁的结果，旧 Grok 完成入口仍在错误时按原规则返回，不扩大 B01 的 Qoder 结算变更。

[原读取链定向回归](baseline/S09/grok-observation-unit.result.json) 1002 条、[Grok Chat/账号定向回归](baseline/S09/grok-chat-execute-unit2.result.json) 953 条通过事件，无失败/跳过。[原生 Execute 本地 HTTP 与部分观察 race](baseline/S09/grok-native-response-race.result.json) 2 条通过，核对一次解码重试、前一响应关闭、活动归零及错误与结果共存。Chat→Responses 同批接入单次 Execute，保留不启用解码恢复和不增加帧过滤的原分支差异。

账号团队/模型进程内健康覆盖表移入 account，保留各自唯一锁、TTL 与缓存 key，候选过滤与当次模型解析仍归调用方。账号额度展示、历史快照、账单窗口和软性重新认证判断迁入 account；供应商 JWT 档位和 Heavy 信号仍由原生函数通过端口解释。

环境在本批一度切换为受限沙箱，导致 lint 加载失败和本地 HTTP/WS 监听被拒；原失败结果独立保留，允许本地验证后重跑通过，不算历史代码问题（`grok-current-unit-lint`、`grok-local-health-unit` 与对应 unrestricted 结果）。已删除经普通和 unit 两集合确认无消费者的兼容声明，未删除测试断言；清单见 [零消费者记录](baseline/S09/grok-zero-consumer-unit-pruned.json)。S09.7 门禁仍在收尾，S09.8 尚未开始。

### 2026-09-15：S09.7 本批门禁通过

[Grok 门禁](baseline/S09/grok-gate.json)通过。普通定向 235、消费者 unit race 1058、原生/会话 unit race 109、原生 HTTP/观察 race 2、真实 Redis 1、PostgreSQL 8 条通过事件，各组无失败/跳过，不累加重叠测试。27 组 depguard 夹具符合预期且已删除，七种构建集合加载成功；80 文件声明与三个标签的符号引用清单已归档。

lint 按 S08 原文件、规则和完整消息映射核对为 **1 / 284 / 17**；unit 减少的一项来自旧 `repository/grok_oauth_client_test.go` 的未检查类型断言，具体原生构造器使断言不再存在，原行为断言仍保留。无新增诊断，未扩大忽略规则。Wire 生成、保护摘要及 diff 检查通过，索引为空。

Grok 原生包、账号授权/配额、同步 Execute 和独立媒体/Realtime 资源已接入生产；共享 OpenAI 客户端响应适配继续随 S09.8 改绑，其余入站失败/重试编排与完成 worker 留 S11，视频任务与创作状态机留 S13。下一步 S09.8 OpenAI，落实已冻结 B04/T01；总 roadmap 保持 **9 / 17**，S09 仍实施中。

### 2026-09-15：S09.8 开始，B04/T01 固定修复通过

B04 将会话缺省模型改为单一原子指针，构造、session.update、请求缺省读取和下行恢复均通过统一入口访问；不串行化双向 relay，不改变原每轮模型/档位/用量快照。T01 按替身已有 values 支持 GetMultiple，并在原独立测试中使用已有设置缓存隔离 helper，未改生产设置逻辑。规划中的原竞争覆盖层已转为回归测试。

[固定问题定向 race](baseline/S09/openai-fixed-race.result.json) 7 条通过；[原 WS 多轮与独立设置测试各五轮](baseline/S09/openai-fixed-original-ws-race.result.json)共 10 条通过，无失败/跳过。原规划失败和原 WS 单独通过结果继续原样保留，未将规划失败记为通过。

旧 pkg/openai 已迁为 upstream/openai，原内嵌 instructions 文本随包原样移动；WS v2 数据面和 Live attestation 分别迁入其 wsrelay/liveattestation 子包。账号 OAuth 会话进入 account，原两项停止测试同迁（重命名避免与 Gemini 同包测试冲突）。首轮本阶段测试名称碰撞日志保留，修正后原生包/会话定向 race 62 条通过。

规范 Codex 身份解析器及唯一动态 UA resolver、OAuth 交换/TLS 请求与共享 req 池已改绑原生实现；HTTP 技术 context key 迁入 upstream，旧入口委托同一 key。原 HTTP OAuth 测试同迁，修正迁移的 helper/import 别名后 [66 条定向 unit](baseline/S09/openai-client-unit4.result.json)通过。隐私、accounts/check、subscriptions 网络/解析迁入原生无状态客户端，OAuth wire 值归 protocol/openai；[115 条定向回归](baseline/S09/openai-privacy-unit.result.json)通过。原环境监听限制的跳过条件保留，不把跳过计为通过。

S09.8 的账号授权、其余平台执行和完整门禁仍实施中；以上结果不代表 OpenAI 或整个 S09 完成，未新增历史问题排查。

### 2026-09-16：S09.8 账号授权、凭据与原生客户端增量

账号授权的 state 校验、代理选择、成功消费、刷新元数据补全、个人/workspace 订阅组合和凭据组装已进入唯一 `account.OpenAIAuthorization`；旧服务通过闭包在原时点投影 TLS、设置和客户端。PAT whoami 的交换、校验和有界读取进入原生包，PAT 凭据清理与结果组装归 account。授权实例接入 `StopContext`，组合根传入剩余预算；停止取消并等待在途交换，超时报告未完成，不能重新启动。新增停止/会话 race 4 条通过，相关 unit 112 条通过，无失败/跳过。

`account.OpenAITokenSource` 与 `OpenAITokenRefresher` 接管原缓存命名空间、TTL、刷新等待/退避、指标与凭据合并；旧入口复用相同缓存/协调器/metrics 指针，无第二份计数状态。令牌与刷新定向 unit 118 条通过。Agent Identity 的 Ed25519/Curve25519 签名解密和原生 task 注册已提取到 upstream/openai，时间由原调用点传入，状态锁与条件写入仍待继续改绑；22 条定向 unit 通过。

WS 客户端和代理 HTTP/TLS 缓存已迁入原生包，原错误身份与 Header 复制继续由唯一实现提供；旧连接池定向消费者 race 14 条通过。WS relay 中原先借 Grok 包调用的独立 reasoning token 数值关系已归通用 protocol，两个平台委托同一算法，不允许具体平台互相 import。迁移产生的符号/import 遗漏及同包测试重名已修正并保留失败日志，未引入清单外历史修复。

原生包整包 race、后续连接池、Agent Identity 账号写入、Images/Live、共享响应适配和完整 HTTP 改绑仍在进行；阶段门禁和全量验证未完成，roadmap 继续为 9 / 17、S09 实施中。


### 2026-09-16：S09.8 WS 池、账号 task 与图片原语增量

WS 池已整体迁入 `upstream/openai`，只接收独立 WSPoolOptions 和无凭据的 WSPoolAccount。旧入口投影账号指纹模式与配置，首次请求的原拥有者显式 Start；连接、预热、指标与队列没有第二份状态。原池测试随实现迁移，旧入站测试通过只读连接/租约数量投影验证资源；新增构造无启动、并发重复启停和关闭后禁止重开契约。[池及直接消费者 race](baseline/S09/openai-pool-consumer-race.result.json)取得 278 条通过事件，无失败/跳过。迁移中的字段可见性与夹具命名编译失败均已修正并独立保留日志。

Agent Identity 账号任务登记、按账号共享锁和锁内最新凭据复查进入 `account.OpenAITaskCoordinator`，兼容消费者引用同一共享实例；母账号资格解析进入 account，旧返回值保持原实体引用。原供应商注册与加密在 upstream，写入仍调用原专用字段能力。[账号及影子定向 unit](baseline/S09/openai-agent-account-unit.result.json)45 条通过；未改变原锁等待、持久化失败或 invalid-task 恢复条件。

Responses 工具 schema 的字节解析与修复进入 `protocol/openai`，平台适用性仍由原入口决定，原平台边界断言保留。纯解析测试随实现迁移，[80 条定向事件](baseline/S09/openai-schema-unit.result.json)通过。图片 pointer/inline 解析、原生资源下载及 URL→base64 回填进入 upstream/openai，保留签名 URL、认证头范围、原重试及 20 MiB/60 秒边界；账号开关、传输与失败日志只通过显式投影接入。[图片及 Agent 定向 unit](baseline/S09/openai-image-resources-unit2.result.json)857 条通过，无失败/跳过；一次变长委托参数生成错误已修正，原编译日志保留。

Live 创建、sideband 与 attestation 技术实现继续迁移中，尚未取得 S09.8 和阶段全量门禁。以上定向集合可能重叠，不累计为独立测试数量；没有新增历史缺陷排查或清单外修复。


### 2026-09-16：S09.8 响应读取与验收阻塞夹具记录

Live 原生创建、sideband 建连与证明加密已接入原调用链，[34 条 Live unit 事件](baseline/S09/openai-live-native-unit.result.json)通过。租约、会话接管及资金完成仍归入站拥有者，未开启原本按需的连接。通用 Responses usage/终态/工具参数与图片计数进一步归 protocol；[wire 定向 unit](baseline/S09/openai-response-wire-unit.result.json)1307 条通过、1 条原跳过单列。首输出暂存器与技术测试迁入原生包，[21 条暂存/HTTP 入口测试](baseline/S09/openai-stage-native-unit2.result.json)通过。普通和 unit 两集合核对后的无消费者包装/测试替身已删除，保留原断言使用的 unit 转接；[清单](baseline/S09/openai-mid-zero-consumer-pruned.json)。批量 goimports 的纯 import 排版噪声按冻结输入恢复，[恢复记录](baseline/S09/openai-unrelated-format-restored.json)不涉及任何语义差异。

原生 SSE 读取循环使用同步 OutputSink，账号健康、监控、重试裁决和 HTTP 上下文由适配端口保留；[218 条定向 unit](baseline/S09/openai-native-stream-unit2.result.json)通过。扩大 race 集合取得 4284 条通过、21 条失败、2 条跳过，不能视为整组通过。失败日志显示同一影子用量夹具共享对象引发竞争并波及同进程其它测试；按计划第 1 节验收阻塞例外执行下述最小复现与修复，没有扩展故障注入。

**X01（验收阻塞，测试夹具）**：原 `TestGetOpenAIUsage_SparkShadow_WritesExtraAndReturnsNonEmptyWindows` 的请求 shadow 与模拟仓库记录指向同一实体；测试兼容入口 ApplyAccountRecord 写回与后台 CAS 读取并发。原 HEAD `1d2519723af97970ff1a429e3d04cfa804f522b2` 在仓库外只运行该原测试 `-race -count=10`，9 次通过、1 次竞争失败（[原结果](baseline/S09/openai-original-shadow-race.result.json)、[输入及源码对照](baseline/S09/openai-acceptance-original-input.json)）。最小修复仅让该夹具的存储记录与请求对象采用独立快照，符合真实数据库读取的对象隔离，所有原断言保留；[相同测试十轮 race](baseline/S09/openai-shadow-fixture-fixed-race.result.json)全部通过。无生产语义、事务或并发策略变化；回退只恢复测试夹具的偶发竞争。

非流/SSE→JSON 迁移中原成功入口允许 nil 账号，而新 Options 投影提前解引用造成 panic；这是本阶段回归，已补 nil 投影保护，正在重跑。阶段和 S09.8 门禁仍未完成，roadmap 保持实施中。


### 2026-09-16：S09.8 响应读取回归通过，继续供应商执行迁移

X01 修复后，[扩大 OpenAI/Grok 定向 race](baseline/S09/openai-response-wide-race3.result.json)取得 4305 条通过事件、0 失败、2 项原跳过，原失败日志保留。非流投影中的 nil 账号与 `*UpstreamFailoverError(nil)` 转 error 后非空的问题均为迁移引入的回归，按原断言修正；[非流/终态 122 条定向事件](baseline/S09/openai-native-nonstream-unit3.result.json)通过。首输出上限与实际网关 defaultMaxLineSize 的原比较留在旧直接消费者测试中，通过只读 Limit 核对，未把该依赖关系替换为固定数字断言。

Embeddings 已接入 `upstream/openai.EmbeddingsExecutor.Execute`，只接收显式目标、Header、传输与原错误观察端口，输出交同步 sink，响应体先于活动结束关闭。[原消费者 unit](baseline/S09/openai-native-embeddings-unit.result.json)12 条通过；[本地 TLS 成功/错误/关闭 race](baseline/S09/openai-native-embeddings-tls-race.result.json)3 条通过。原模型/资金投影及错误分类仍留入站，未改变失败结算或取消策略。

标准 Responses 的 HTTP 首响应头预算、迟到响应关闭与 context 释放迁入 `ExchangeHTTP`，保留构造失败、传输失败与超时的原先后顺序；[49 条定向回归](baseline/S09/openai-native-http-exchange-unit.result.json)通过。同账号恢复循环、其它 HTTP/WS 完整执行适配仍在后续本批工作中，不能据此宣布 S09.8 完成。

Codex 报文转换、输入过滤与工具 ID 配对进入原生 codec；模型规则通过纯函数端口按原时点调用，跨平台账号选择仍由外层决定。实际 device ID、图片能力与 Spark 判断由兼容入口投影，不让原生包接收 Account。预留 Python 工具别名、碰撞检查与 raw JSON 恢复同批迁移，反向映射仍是原请求/WS 会话状态。[614 条 Codex/工具/直接消费者测试](baseline/S09/openai-tool-names-native-unit.result.json)通过，无失败/跳过。迁移时的未改齐调用签名/import 编译失败已保留，各原断言不变；未新增历史问题修复。

[保护检查](baseline/S09/openai-response-integrity-check.json)确认原计划前 16117 字节、4536 项受保护输入、HEAD/main 与空索引均保持，git diff --check 通过。该检查是当前增量证据，不替代最终 Wire/构建/进程/全量验收；roadmap 仍为 9 / 17，S09 实施中。


### 2026-09-16：S09.8 额度、授权 HTTP 与图片读取增量

额度 wire 与窗口规范化进入 protocol/openai，原生 quota client 保留 Header、20 秒请求与 512 KiB 读取边界。account.OpenAIQuotaService 拥有查询、重置、回读及 extra 投影；account.OpenAIQuotaActions 拥有管理员操作后的恢复/缓存/部分警告顺序。HTTP 入口进入 account/httpapi，app 直接绑定同一账号与授权实例；原 handler 仅保留兼容构造和测试投影。PAT/OAuth 导入与账号刷新持久化归 account 的命名用例，原查询顺序和补全行为保持。

[授权/额度生命周期 race](baseline/S09/openai-quota-auth-lifecycle-race.result.json)35 条通过；[HTTP 与导入 unit](baseline/S09/openai-account-import-unit.result.json)2963 条通过、2 条既有跳过单列；[额度操作 HTTP race](baseline/S09/openai-quota-actions-http-race.result.json)12 条通过，[服务与操作停止 race](baseline/S09/openai-quota-workflow-lifecycle-race.result.json)4 条通过。额度消费仍使用客户端取消，消费后的原八秒恢复阶段忽略客户端断开但服从应用停止。app 将操作、底层额度服务分别登记 StopOrder 14/35，依赖关闭前取消并等待；Wire 已重新生成，最终二次无差异仍待收尾验收。

图片 JSON/multipart 解析归 upstream 的通用图片输入，路由模型与能力裁决留原入站；OAuth Responses 图片 codec、尺寸和错误解析归 upstream/openai，时间仍在原调用点生成。401 条 codec 定向事件通过。OAuth 流与非流读取迁入原生实现后，原心跳测试捕获提前读取 Header 造成心跳停止的本阶段回归；已将非流 OutputContext 创建推迟到原响应写入点，[309 条定向事件](baseline/S09/openai-images-readers-unit2.result.json)通过。编译期间漏改符号/import 的失败日志保留，未削弱断言。API Key 图片读取、实际 Execute 改绑及完整 OpenAI 门禁继续实施；S09 仍未完成。


### 2026-09-16：S09.8 图片 Execute 与三类响应适配增量

API Key/OAuth 图片的实际网络发送、错误体关闭/回卷、响应读取和活动结束已接入 `upstream/openai.ImagesExecutor.Execute`；原账号健康和 Agent task 恢复通过旧端口保留。普通成功、流式部分产出与错误同时返回仍按原入口投影，未改变计费张数回退或图片取消策略。[旧消费者 unit](baseline/S09/openai-images-execute-unit.result.json)309 条通过；[本地 TLS Execute race](baseline/S09/openai-images-execute-tls-race.result.json)6 条通过，验证真实请求、渐进图片事件、失败后事实和响应体先于活动释放。原生新增观测不改变旧完成处理的资金决策。

Responses passthrough 的 SSE、非流和 SSE→JSON 已迁入唯一原生读取器；前导暂存、完整事件边界 Flush、裸 error 等待 authoritative failed、尾部 usage 与 keepalive 仍沿用原顺序。[定向 unit](baseline/S09/openai-passthrough-readers-unit.result.json)199 条通过、1 条原跳过；[图片与透传消费者 race](baseline/S09/openai-images-passthrough-consumer-race.result.json)490 条通过、1 条原跳过。集合有重叠，未相加为独立测试总数。

OpenAI 错误码、容量/上下文/账号凭据状态信号与首输出判断归原生纯规则；旧账号健康写入和跨账号 failover 编排仍由原入口持有。移除原生读取器上的同算法回调，[823 条规则及消费者 unit](baseline/S09/openai-error-rules-unit2.result.json)通过、1 条原跳过。共用缓冲终态读取及错误链同迁，[30 条定向 unit](baseline/S09/openai-buffered-reader-unit.result.json)通过；空响应检测器每次尝试独立，[11 条回归](baseline/S09/openai-silent-detector-unit.result.json)通过。未更改各入口终态 usage 的覆盖优先级。

Chat/Messages 的 Responses 转换读取分别进入 `response_chat`、`response_messages`，复用唯一 protocol bridge。模型恢复与档位观察、账号副作用、HTTP 错误及 Grok 费用资格仍由显式端口传入；不让两个具体平台互相 import。延迟输出适配只在真正触碰输出时取得 Header，保留原等待心跳及前导失败边界。[Chat unit](baseline/S09/openai-chat-native-response-unit.result.json)109 条、[Messages 及直接消费者 unit](baseline/S09/openai-messages-native-response-unit.result.json)110 条通过，无失败/跳过。

本批 lint 检查发现的迁移新增重复 import、未检查类型断言与条件表达式已修正；无消费者包装与精确依赖许可仍在核对。Raw Chat、其余请求/原生状态边界、OpenAI 完整门禁和阶段全量验收继续实施；roadmap 维持 9 / 17，S09 仍未完成，没有增加清单外历史审计或修复。


### 2026-09-16：S09.8 扩大 race 通过，Raw Chat 继续迁移

[扩大直接消费者 race](baseline/S09/openai-response-batch-wide-race.result.json)取得 **4311 条通过、0 失败、2 条原跳过**；包含父子事件且与此前集合重叠。当前保护检查确认 [4536 项输入、空索引及原计划前缀未变](baseline/S09/openai-images-chat-integrity-check.json)。普通/unit lint 对照后，[72 项零消费者转接](baseline/S09/openai-response-batch-zero-consumer-pruned.json)删除；[13 项 unit 独占转接](baseline/S09/openai-response-unit-only-compat.json)移入同包带 unit 标签的测试兼容文件，原测试和断言未删除。原生读取和输出的行为验证不以 lint 编译替代。

Raw Chat 的工具空身份字段处理、usage-only 检测、CC 档位事件和三类终态信号进入 protocol/openai；读取错误类型/哨兵进入 upstream/openai，旧入口保留同一错误链，本地响应体超限错误由旧 HTTP 边界传入。[142 条定向 unit](baseline/S09/openai-raw-wire-unit.result.json)通过。CC 的共享 JSON/SSE 读取移入原生包，原每轮独立档位观察器通过端口传入，[57 条定向 unit](baseline/S09/openai-cc-reader-unit.result.json)通过。

Raw Chat 直通及 CC→Responses/Messages 的输出循环进入 `response_raw_chat`，继续复用 protocol/bridge，原随机源在同一生成点调用。各路径的取消成功收尾、上游截断、Usage 覆盖和 ClientDisconnect 字段差异分别保留；reasoning 内容回填仍调用同一旧缓存能力，剩余入站上下文归 S11。修正首次迁移漏传 bridge.Runtime 的编译错误后，[65 条定向事件](baseline/S09/openai-raw-native-response-unit2.result.json)通过；原编译失败证据保留。[五项旧 CC 读取/输出入口](baseline/S09/openai-cc-zero-consumer-pruned.json)消费者清零后删除。

本批仍需完成请求构造与适用 Execute 改绑、平台剩余边界、OpenAI 门禁和阶段全量验收。S09 保持实施中，未自动提交。


### 2026-09-16：S09.8 Raw 响应 race 与请求构造增量

Raw Chat、Responses/Messages 转换及直接消费者的 [167 条定向 race](baseline/S09/openai-raw-response-race.result.json)通过，无失败或跳过。本轮转接清点继续将 unit 独占符号移至测试兼容文件，并删除零消费者符号及空文件；[逐项清单](baseline/S09/openai-raw-zero-consumer-pruned.json)。

请求体的 Compact 白名单、reasoning replay、store=false、并行工具、schema、空 Base64 图片及跨模式加密条目清理进入 `upstream/openai/request_compatibility`；原平台适用选择仍由旧入站提供。记录用 effort 归 `protocol/openai/recorded_effort`，保留 none/minimal 不进入记录档位的原规则。[749 条定向 unit](baseline/S09/openai-request-codec-unit2.result.json)通过。首次编译时漏映射的两个 helper 已修正，失败日志保留。

Responses 实际 Header 组合进入 `request_headers`，只接收目标 URL、已有 Header 与身份/策略端口；账号 Header、session 隔离、UA、指纹、账号覆写和 beta/routing-hint 的执行先后不变。[145 条 Header/身份 unit](baseline/S09/openai-request-headers-unit2.result.json)通过。CC 请求构造与实际发送进入 `request_chat`，原端点观察、代理/TLS 和 Grok 专属 Header 由旧适配在原时点提供，具体平台不互引；[145 条发送及消费者 unit](baseline/S09/openai-chat-send-unit.result.json)通过。响应所有者仍负责在读取后关闭响应体，不增加新的全局重试循环。

延迟输出适配的 InitialOutput 保留先读取 Header（暂停原等待心跳）、后读取提交状态的原顺序；仅新 OutputState 方法单独读取状态而不触碰 Header。当前继续执行 lint 与直接消费者回归，OpenAI 和全阶段门禁仍未完成，未提交。


### 2026-09-16：S09.8 缓存、身份与验收阻塞 X02

[普通/unit/integration 完整 lint 对照](baseline/S09/openai-incremental-lint-comparison.json)为 **1 / 284 / 17**，按 S08 原文件映射、规则、完整消息和重复次数核对均匹配；unit 减少的一项仍是已在 S09.7 解释的 Grok 构造器类型断言移除，无新增忽略规则。该结果对应本次增量检查，不代替后续变更后的最终验收。[扩大消费者 race](baseline/S09/openai-current-full-consumer-race.result.json)4360 条通过、0 失败、2 条原跳过。

共享 OAuth token/refresh-lock Redis 实现进入 `account/rediscache/oauth_token`，保留 oauth:token:/oauth:refresh_lock:、Redis Nil、TTL 及旧锁操作；app 直接构造唯一实例，旧 GeminiTokenCache 名称转接统一 account.AccessTokenCache 契约。Wire 生成通过。[89 条缓存与 provider unit](baseline/S09/openai-shared-token-cache-unit.result.json)通过；[本地原生 OAuth + 真实 PostgreSQL/Redis race](baseline/S09/openai-token-postgres-redis-race.result.json)6 条通过，覆盖管理员换凭据的原 CAS、旧缓存键读取和重复调用不再次交换；另 [Codex 导入/凭据 outbox 回滚](baseline/S09/openai-import-postgres-race.result.json)2 条通过。仅使用本地 TLS 和隔离容器，未使用生产凭据。

Responses 透传请求构造也已迁入原生 Header 实现，仍按原过滤和 token 替换顺序执行，[168 条定向 unit](baseline/S09/openai-all-request-headers-unit.result.json)通过。账号身份 namespace、稳定 UUID 和 metadata/Header 作用域派生迁入 `upstream/openai/account_identity`，只接收必要字符串投影；[131 条身份定向 unit](baseline/S09/openai-account-identity-native-unit.result.json)通过。重命名误改的四处错误字符串已还原，[逐函数字符串/哈希种子对照](baseline/S09/openai-account-identity-literal-parity.json)与原 HEAD 一致，未改标识编码。

**X02（原 I01，验收阻塞，测试夹具）**：已登记的 `openAIFastPolicyRepoStub.GetMultiple` panic 阻止 OpenAI 档位传播用例独立执行。按计划第 1 节例外，在原 HEAD 的仓库外副本只执行同一 `TestForwardStreaming_ServiceTierPropagatedToResult`，实际失败于同一替身（[原失败](baseline/S09/openai-fast-fixture-original.result.json)）；[两个输入文件 SHA](baseline/S09/openai-fast-fixture-original-input.json)确认修复前均与 HEAD 逐字一致。最小修复仅让 GetMultiple 从已有 values 返回请求键，并在原用例使用既有设置缓存隔离 helper，未改生产逻辑或断言。[同一用例五轮 race](baseline/S09/openai-fast-fixture-fixed-race.result.json)通过。回退只恢复独立运行的夹具 panic；未扩展到其它历史问题排查。

S09 仍实施中，平台剩余边界、完整依赖夹具、全量测试/构建及进程验证尚未完成；无自动提交。


### 2026-09-16：S09.8 字段重试与 Alpha Search

Responses 被拒字段的解析、降级与循环防护进入 `upstream/openai/rejected_field_retry`；旧 Gin context 仍持有同一请求共享预算，每个账号尝试新建去重状态。原测试继续比较同一个预算对象，仅改用只读 Budget 句柄，六次预算和 SHA 去重不变。[87 条定向 unit](baseline/S09/openai-rejected-field-native-unit3.result.json)通过，迁移期间漏映射的 schema helper 和跨包 remember 可见性编译错误已修正，日志保留。

Alpha Search 的请求/响应纯转换与单次网络/响应执行迁入 `upstream/openai/alpha_search_codec`、`alpha_search`。原入口只传递准备后的技术请求和账号副作用端口；只有原 2xx 路径产生一次 WebSearchCalls，非 2xx 仍按原形状输出且不计费。PAT 的 hosted web_search 回退与原截断/引用输出保持，[17 条原 Alpha Search unit](baseline/S09/openai-alpha-native-unit.result.json)及[字段重试/Alpha/X02 集合 105 条 race](baseline/S09/openai-alpha-retry-race.result.json)通过，无跳过。

S09 进程验证已增加原生尝试、账号授权会话与额度操作停止一次、先于 Redis/Ent 关闭的断言。首次[进程验证](baseline/S09/openai-process-modes.result.json)仅 version 子测试通过，随后 Testcontainers reaper 启动超时，未执行服务启停断言，不能记整组通过。Docker 查询显示引擎可用、0 个运行容器且失败容器已回收；保持原测试配置补跑，未改业务代码或扩展历史修复。


### 2026-09-16：S09.8 真实进程通过与指纹状态归属

第二次进程运行进入真实服务，新增 S09 断言错误地把 HTTPRequests（等待 handler 完成）当成停止监听，导致额度操作取消与请求等待之间的合法顺序被判失败。该错误仅属于本次新增测试；已保持原生产 StopOrder 不动，把断言改为：入口关闭后取消操作、请求/原生屏障全部早于完成队列，授权和共享存储在依赖结束后关闭。[完整进程补验](baseline/S09/openai-process-modes-order.result.json)10 条通过，无失败/跳过，包含 version、standard/simple SIGTERM、jwtgen、初始化失败释放、监听失败、Web setup、CLI setup、AUTO_SETUP。前两次失败日志保留，未删除旧断言或改变原业务退出语义。

Codex 指纹 ID 生成、Header 与 client_metadata 改写进入 `upstream/openai/fingerprint`。旧 Gin context 只存储同一尝试的指针并校验账号归属，模式/种子仍由 account 提供；原时钟与随机 ID 生成点、原始 body session 捕获与 prompt_cache_key 判断保持。接口模式投影为字符串，测试继续检查原模式值，未改变账号配置枚举。新公开字段明确不参与 JSON，保留旧私有状态序列化行为。[164 条指纹/身份/请求/WS unit](baseline/S09/openai-fingerprint-native-unit.result.json)通过。


### 2026-09-16：X03 响应体释放验收阻塞

**X03（验收阻塞，Alpha Search 响应拥有权）**：按 S09 的“上游关闭本次响应体”约定，在原 Alpha failover 用例全部错误/输出断言之外增加原始响应体 Close 次数验证；当前迁移链为 0 次，验收失败（[当前结果](baseline/S09/openai-alpha-resource-contract.result.json)）。错误分支会回卷 `resp.Body` 为新 reader，原 defer 在返回时读取了被替换的字段。

仅将同一夹具放到原 HEAD `1d2519723af97970ff1a429e3d04cfa804f522b2` 的仓库外副本，仍为 0 次；[原失败结果](baseline/S09/openai-alpha-resource-original.result.json)与[源码/相同夹具摘要](baseline/S09/openai-alpha-resource-original-input.json)已保存。该问题阻塞明确的响应释放契约，按计划第 1 节允许例外做最小修复：Alpha 单次执行取得响应后保存原始 Body，并 defer 关闭它，不受错误处理回卷影响。未扩展其它路径排查，未调整 HTTP、账号副作用、重试或资金语义。

[同一资源断言及原 ForwardAlphaSearch 用例三轮 race](baseline/S09/openai-alpha-resource-fixed-race.result.json)39 条通过、无失败/跳过，原断言保留。回退 X03 会恢复 failover 时真实响应未关闭的资源释放风险。


### 2026-09-16：S09.8 查询与请求投影收敛

Alpha Search 请求头组合进入 upstream/openai，PAT 元数据验证、合并和持久化顺序进入 account；旧网关只保留策略与参数投影。[111 条 Header unit](baseline/S09/openai-alpha-headers-unit.result.json)、[27 条元数据 unit](baseline/S09/openai-alpha-metadata-unit.result.json)及[315 条图片请求 unit](baseline/S09/openai-image-request-headers-unit.result.json)的结果以对应文件为准。图片请求继续复用原技术池，不合并账号隔离策略。

Anthropic 兼容 token 计数的请求构造和单次网络读取进入原生查询能力，不把计数结果作为推理用量；[75 条查询 unit](baseline/S09/openai-count-query-unit.result.json)通过，1 条既有跳过单列。原生 Responses 计数出口与 WS 续接报文正在同批收敛；两种计数输出形状、读取边界和缺省回退分别保留。当前 unit lint 新增诊断仅为已无消费者的 Alpha Header 包装，继续按消费者清点移除，不扩大忽略规则。


### 2026-09-16：S09.8 续接与依赖门禁

WS 续接报文、严格比较和失效密文剥离迁入原生纯实现；旧会话归属缓存、每轮价格快照和入站重试继续归 S11。原生 Responses 计数出口与 Anthropic 兼容出口分别保留完整 JSON/数值投影及原错误分支。[623 条计数/续接 unit](baseline/S09/openai-ws-payload-count-unit.result.json)通过、1 条原跳过；WS 握手头同批迁入平台，[1092 条相关 unit](baseline/S09/openai-ws-headers-unit.result.json)通过、1 条原跳过。原断言及零拷贝正文所有权保持。

[八项零消费者包装](baseline/S09/openai-ws-zero-consumer-pruned.json)已移除；[OpenAI 的 33 项可丢弃依赖夹具](baseline/S09/openai-depguard-fixtures.json)在普通/unit/integration 全部命中预期，覆盖平台互引、Redis、同目录新文件、旧文件新依赖、迁出失去例外、精确散列许可和正常 Adapter。临时文件已删除。[七个构建集合](baseline/S09/openai-buildsets.json)无加载错误，Darwin/Linux 与 wireinject/embed/e2e 选择已保存，构建选择不计行为验证通过。

当前源代码冻结进入最终消费者 race、原生 race 和阶段全量串行验证。尚未完成的门禁继续标待验，roadmap 保持实施中。


### 2026-09-16：全量普通通过，unit 单次心跳失败待核对

[全量普通](baseline/S09/final-normal.result.json)11487 条通过、0 失败、4 条既有跳过。[首次全量 unit](baseline/S09/final-unit.result.json)19524 条通过、1 条失败、8 条既有跳过；失败是 `TestOpenAIStreamingPreambleKeepaliveUsesDownstreamIdle` 的 1.5 秒夹具未观察到心跳。

按验收阻塞边界，只比较这一项：测试正文与原 HEAD [SHA 一致](baseline/S09/openai-keepalive-input-comparison.json)，[当前十轮](baseline/S09/openai-keepalive-current-repeat.result.json)和[原 HEAD 十轮](baseline/S09/openai-keepalive-original-repeat.result.json)均通过。尚未确认归因，不能当作已复现历史缺陷或全量通过；登记 I03，不改生产计时算法或原断言。保持源代码不变补跑相同全量 unit，原失败日志保留。


### 2026-09-16：全量 unit 补验通过与 integration 环境记录

源码不变，[全量 unit 重跑](baseline/S09/final-unit-repeat.result.json)19525 条通过、0 失败、8 条既有跳过；I03 原心跳测试实际通过。保留第一次失败和原 HEAD/当前十轮结果，未把未确认的偶发现象升级为生产修复。

integration 首轮中 routes 的认证限流用例与 usage/postgres 的 TestMain 未能启动 Redis，Docker API 获取容器状态超时，未进入对应业务断言。该环境失败与日志单独登记，不作行为通过。首轮结束后将以 `go test -count=1 -json -tags=integration -p=4 ./...` 重跑整个集合，降低包级容器启动并发；所有测试断言、内部并发和原超时不变。


### 2026-09-16：阶段行为测试完成补验

[最终消费者 race](baseline/S09/openai-final-consumer-race.result.json)4361 条通过、0 失败、2 条既有跳过；[原生平台/池和缓存 race](baseline/S09/openai-final-native-race.result.json)252 条通过、无失败/跳过。集合有重叠，不相加为独立测试数。

integration 以包级并发四重跑，[完整结果](baseline/S09/final-integration-repeat.result.json)12423 条通过、0 失败、4 条既有跳过。首轮两个容器启动失败的包均已执行通过；[环境记录](baseline/S09/environment-observations.json)保留首轮失败与补验命令。全量普通11487、unit19525、integration12423条通过事件分别独立登记，跳过和仅编译均不计行为通过。下一步完成最终 lint、构建、生成及资料核对，暂不提前更新完成数量。


### 2026-09-16：最终 lint 与源码清点

[三套完整 lint](baseline/S09/final-lint-comparison.json)为 **1 / 284 / 17**，按 S08 路径映射、规则、完整消息和重复次数逐项匹配；无新增诊断。unit 唯一减少项仍是 S09.7 删除旧 Grok 构造器断言后消失的 errcheck，未增大任何忽略范围。已删除的两个 legacybridge 文件对应 [旧例外同批清理](baseline/S09/deleted-file-depguard-cleanup.json)。

[文件与符号清点](baseline/S09/final-file-summary.json)覆盖实际1027个 Go 文件差异（含删除项），并保存13476个声明；[Go 类型消费者](baseline/S09/final-consumers-summary.json)覆盖普通/unit/integration 的引用与结构实现候选，实际绑定由 app/Wire 引用核对。[交接清单](baseline/S09/handoff.md)明确完整入站、会话归属缓存、完成处理及商业/任务等后续所有者，未宣称这些业务已迁移。

现有架构、网关生命周期、各平台、账号维护和开发文档已按实际新旧共存结构同步；新的单次执行资源锚点已由 upstream.Executor 引用。构建及最终完整性检查仍在执行，完成记录稍后追加。


<a id="s09_completion"></a>
## S09 完成记录（2026-09-16）

S09.0—S09.8 已完成。Qoder Chat 非流/SSE 首条完整链先于后续平台通过门禁；Anthropic/Bedrock、Gemini、Vertex、Antigravity、CN/Ollama/usageprovider、Grok、OpenAI 依次完成唯一实现、生产改绑及契约验证。各平台 [子门禁与最终结果](baseline/S09/completion.json)、[验收矩阵](baseline/S09/acceptance-matrix.md)和[迁移账本](baseline/S09/migration-ledger.md)可直接核对。

| 最终验证 | 实际结果 |
| --- | --- |
| 全量普通 | 11487 条通过、0 失败、4 条既有跳过 |
| 全量 unit 原样重跑 | 19525 条通过、0 失败、8 条既有跳过 |
| 全量 integration，包级并发四 | 12423 条通过、0 失败、4 条既有跳过 |
| OpenAI 最终消费者 / 原生 race | 分别4361 / 252条通过；消费者2条既有跳过，原生无跳过 |
| lint 普通 / unit / integration | 1 / 284 / 17项；逐项映射与基线匹配，无新增诊断 |
| 前端测试 / 真实产物 embed 测试 | 199个前端测试、101条 embed 事件通过 |
| 构建 | 普通服务、前端、embed、Linux amd64、jwtgen、清理维护命令全部成功 |
| Wire | 实际再次生成，前后 SHA 相同 |
| 进程 | standard/simple、SIGTERM、监听失败、初始化释放、setup/CLI/AUTO_SETUP、jwtgen及版本验证通过 |

测试数量是包含父子项的执行事件，不把重叠集合相加；[全部结果](baseline/S09/final-test-results.json)、[跳过明细](baseline/S09/final-skipped-tests.json)与[失败记录](baseline/S09/final-failed-events.json)分别保留。首次 unit 的单次心跳失败没有确认根因；相同测试原 HEAD/当前各十轮及原样全量重跑均通过，未改生产算法或断言。首次 integration 的两个 Redis 容器启动超时经整个集合 `-p=4` 补验通过，未修改原超时或测试内部并发。这些失败没有从日志中删除。

固定 B01—B04、T01 的修复和原复现保持独立记录。额外验收阻塞仅有 X01/X02 测试夹具与 X03 Alpha 原始响应体关闭，均有原 HEAD 证据及最小差异；没有扩展到其他历史问题审计。[交接与回退](baseline/S09/handoff.md)明确每项回退风险、S07 outbox 周期恢复和单实例边界。

[最终完整性](baseline/S09/final-integrity-check.json)确认4546项受保护输入未变、计划前16117字节保持原文、HEAD/main未变、索引为空；SQL、Ent、旧冻结资料、AGENTS.md和其他任务文件无意外差异。Wire差异对应平台/账号授权/缓存/HTTP装配；已有与新增文本的 diff 检查通过。两个已删除旧桥接的[六项重建同名文件夹具](baseline/S09/deleted-bridge-depguard-fixtures.json)确认旧许可不再生效，临时文件已删除。

现有系统架构、网关生命周期、各上游、账号维护与开发文档同步实际结构，稳定锚点保留。真实供应商账号、硬件、外部 TLS 与真实 E2E 的既有限制继续单列，不把本地夹具或跳过记为真实外部验证。完整入站与完成处理仍归 S11，审核/搜索归 S10，商业/任务/维护和兼容清理分别按 S12—S16 交接。

Roadmap 更新为 **10 / 17**，S09 已完成；下一步编写 S10 子计划，并继续在计划阶段冻结历史问题清单。当前交付可审查工作区差异，没有自动提交、推送或切换分支。
