# S11：迁移完整网关编排，冻结历史问题清单

## 1. 基线与执行边界

以当前 `main`、HEAD `beb09fb536d9e3c220dac04e8ffb2d4ffeed628b` 为起点，完成 S11.0—S11.6，将剩余 HTTP、SSE、WebSocket、Live、网关会话和完成处理接入新模块。

实施前，将本计划原样保存为：

`/Users/daodaoneko/GolandProjects/TokenRouter/refactor/S11-gateway-orchestration.md`

随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，证据保存到同仓库 `refactor/baseline/S11/`；沿用本次重构约定，不另存 `.agents/plans/`，不改写 S00—S10 冻结资料。

已核实：

- Go 1.27.0、golangci-lint 2.13.2、Docker 29.5.2 可用。
- 已跟踪工作区和索引干净，保留原有 50 个其他任务未跟踪文件。
- 规划期间 11,771 个已跟踪文件摘要保持不变；未修改仓库生产代码或测试。
- 定向普通测试 **7,153 条通过、2 项既有跳过**；unit race **2,176 条通过、1 项既有跳过**；真实 PostgreSQL/Redis integration race **36 条通过、无跳过**。均无失败，事件包含父子测试、集合存在重叠，不能相加或代替阶段全量验收。
- 规划证据位于 `/tmp/tokenrouter-s11-planning/`。固定问题使用仓库外 Go overlay 复现，race 模式重复三轮均触发预期失败断言，不计为测试通过。

**问题处理范围固定：**

- 用户已确认 B01—B06 全部纳入；历史问题主动排查到此结束。
- 执行阶段仅开展迁移、固定修复和约定验证，不继续寻找历史问题、不扩展故障注入、不顺带优化。
- 本次改动引入的回归必须修复。
- 验证偶然发现清单外历史问题，只保存当次证据并登记；即使阻塞验收，也暂停相关工作并请求调整计划，不自行追加复现或修复。

保持 HTTP、配置、数据库 schema、缓存键与版本、金额算法、standard/simple 和各平台取消策略兼容。只生成 Wire，不生成 Ent；不自动提交、推送、切换分支，不提交 `SYNC.md`。

## 2. 固定问题与修复决策

| 编号 | 原实现复现 | 本阶段修复 |
| --- | --- | --- |
| B01 | 完成队列溢出后内联执行任务，`Stop` 只等待 pond 队列，仍有同步任务运行时已经返回 | 完成执行器统一登记排队和内联任务；封闭提交与登记使用同一屏障，停止等待所有已接受任务。保留 drop/sample/sync、mandatory 兜底及任务超时；应用预算耗尽报告未完成项。 |
| B02 | 完成队列先 Stop 后 Start，仍创建扩缩容 worker，后续 Stop 无法再次清理 | Start/Stop 共用生命周期状态，停止不可逆；重复调用共享结果，禁止停止后启动扩缩容或重新接受异步任务。 |
| B03 | 旧错误规则回源晚于管理员禁用完成，重新发布旧规则 | 唯一规则实例使用可取消的更新协调器，将回源、管理写入及 Redis/本地发布串行协调。旧回源不能在新写入发布后覆盖缓存；匹配热路径仍读取已发布快照。保留数据库权威性、通知协议和写后独立刷新预算。 |
| B04 | 缓存持有输入指针，`MatchRule` 返回同一可变规则；修改返回值可污染后续匹配 | 缓存接收、编译和返回边界深复制 slice、指针与响应动作；发布快照不可变。保留排序及匹配行为。此项是调用边界复现，未发现当前生产调用方主动修改返回值。 |
| B05 | 错误规则启动回源使用 Background；Stop 无法取消支持 context 的阻塞读取 | 使用受生命周期管理的运行 context，贯穿预热、订阅回调和回源；停止先取消，再等待在途操作，等待受应用预算约束。保留同步预热、失败回退和订阅时机。 |
| B06 | Live sideband 取得控制权后取消，仍等待 250ms 并查询执行账号 | 交接等待响应取消，并在后续查询和拨号前复查；归还本次控制权，保留原远端会话及 observer 恢复规则。正常等待仍为 250ms，清理沿用独立 Redis 操作预算。 |

B01/B02 不新增持久完成队列或保证进程崩溃后恢复；B03 不增加分布式锁、数据库版本列或缓存协议。保留单服务进程部署边界及 S07 outbox 周期重建限制。

## 3. 接口与迁移步骤

### S11.0：冻结输入与逐链清点

记录实际 HEAD、索引、工作区、工具版本、源码摘要、SQL checksum、Ent/Wire 摘要和 S10 完整验证结果，归档规划资料。

建立逐符号迁移清单及“入口 × 平台 × 原生/转换 × 传输”契约矩阵，记录生产消费者、构建标签、共享状态、资源拥有者、测试、Wire、文档锚点和退出阶段。重点覆盖认证中间件、混合网关服务、模型恢复、会话缓存、搜索模拟、媒体完成及异步任务闭包。

记录每条链的准入顺序、请求体读取时点、重试窗口、取消策略和完成资格，不按文件名前缀整体搬迁。

### S11.1：请求、准入与执行契约

- 复用 `AccessSnapshot`、`RoutePlan`、`AccountSnapshot`、scheduler Lease 和 upstream `AttemptInput/AttemptResult/OutputSink`；gateway 不再定义同名实体。
- 单次 HTTP 执行提供 `Execute(ctx, Request, OutputSink) (ExecutionResult, error)`。固定依赖由 app 构造时注入，HTTP 调用方不再逐请求组装选择、刷新、计费和完成回调。
- 请求携带端点意图、已认证主体、客户端元数据及受控报文读取能力。保留延迟读取：普通 Key 的协议门禁不新增 body 读取；复合 Key 仍在原位置读取模型并恢复报文。不得因提前解析改变认证、资金、协议和格式错误的优先级。
- 凭据提取与身份认证继续由 apikey 承担；复合选组、分组回退、Key 模型改写、资金来源解析及网关准入进入明确用例。按原顺序执行资金检查与 RPM，等待后只复查原有权益项。
- 将业务 `ctxkey`、Gin 中可变请求状态改为显式请求/attempt/turn 状态；telemetry 仅保留关联信息。异步完成输入取得独立快照，不捕获 Gin Context 或之后还会变化的报文、模型链和账号视图。
- 静态配置由 app 投影为 Options，动态设置通过窄读取接口取得。网关提示替换、客户端许可、转发开关和错误规则的解释归所属模块；保留原缓存作用域、TTL、默认值及更新时机。

### S11.2：剩余非流 HTTP 完整链

按 Messages、Responses、Chat Completions、Gemini 原生的顺序迁移，每条链通过契约验证后再切换下一条。

- gateway 内部组织准入、审核、用户等待、候选复核、账号尝试及完成处理；每请求只有一套账号切换循环。
- 普通、无效请求及不可用分组回退保留各自触发条件；切换最终分组后重新执行授权、协议、指定订阅范围、计费和调度类型检查。
- 复合选组 → Key 一跳改写 → 渠道 → 账号映射的顺序不变；每次 attempt 从原始或规范化报文重新构造，不对上一 attempt 的已改写报文重复映射。
- 账号凭据、刷新及健康写入调用 account；出站策略调用 egress；平台原语继续复用 upstream。app 绑定平台执行器，gateway 核心不导入具体平台实现。
- Qoder Chat 同批删除 S09 的临时请求回调和旧完成桥接，继续使用唯一生产链。旧服务只保留未迁任务消费者所需的投影与委托。

### S11.3：SSE、输出状态与取消

- 将共同编排推广至对应 SSE 分支。HTTP Adapter 独占状态码、Header、写入、Flush、心跳及错误 envelope；输出同步反馈写失败，不增加每帧 goroutine 或整流缓冲。
- 独立记录 HTTP 已提交、attempt 是否还能重试、语义输出和 TTFT。保留前导缓冲、Compact keepalive、空白 padding、结构进度及终态错误各自的既有边界。
- 已关闭重试窗口后不得换号；失败结果可以同时携带已观测 usage。完成资格按各入口当前规则确定，不将 Qoder 部分失败结算扩大到所有平台或 WS。
- 保留各平台取消策略：等待和适用非流请求传播取消；Qoder 已进入流式上游后，在原十五分钟预算内收集尾部 usage，停止下游输出后不得新开推理。其他平台的排水、读超时及取消分别保持。
- upstream 关闭响应体及连接资源；gateway 完成 attempt 后调用 `Finish`，最终释放请求 Lease。准备失败、补全失败、写失败和重复清理均覆盖原有资源。

### S11.4：辅助入口、媒体、搜索与会话

- 迁移 count/input tokens、模型入口、Embeddings、图片、Grok 视频、搜索、音频和 Voice 编排；模型和公共 usage 查询继续调用 routing/usage 的唯一实现。
- 保留裸路径别名、平台强制路径、Responses 子路径白名单、Google 错误形状和不支持入口的原拒绝顺序。资源读取、取消、下载及声音管理不套用生成请求准入。
- count_tokens 使用无槽选择，保留本地估算、上游回退及两种协议返回形状；估算值不作为实际结算用量。
- 搜索工具识别、启用裁决、合成协议事件和合成 usage 归 gateway；Brave/Tavily 配额继续由 search 拥有，原生 Grok/AlphaSearch 留 upstream。
- 公共 Grok 视频的归属校验、创建快照、完成认领及任务级结算 ID 进入 gateway，保留 pending/billed 键和失败释放语义。creative/batchimage 状态机与资金预占仍留 S13。
- 网关会话隔离、摘要会话、previous-response 归属、reasoning/失效密文记录进入明确会话实现；Redis 部分进入 gateway Adapter。粘性选号继续由 scheduler 拥有，平台连续性算法留 upstream，不建立万能缓存或复制现有状态。

### S11.5：WebSocket、Live 与 sideband

- 使用独立的会话执行接口和 `FrameConn`，不强制 WS 实现单次 HTTP 接口。升级、Origin、子协议、关闭帧和写超时留 HTTP Adapter。
- gateway 拥有每 turn 的准入、模型链、定价时刻、审核、Lease 与完成快照；upstream 保留连接池、帧解析、relay 和既有恢复原语。
- 保留硬 previous-response 绑定、可移动条件、当前 turn 重试载荷、首输出限制及闲置连接行为；恢复不得重放已完成 turn。
- 入站连接租约、每 turn 用户/账号槽和 Live 长会话租约分别管理。保留正常 turn 次序及双向 relay，不为迁移统一串行化帧传输。
- 落实 B06。Live 创建、controller 接管、observer、到期和关闭仍按原保证执行；当前 **Live 仅记录零费用用量**，本阶段不实现其计费 TODO。
- 按需连接和 observer 统一登记到 app 生命周期，停止后不再接收新操作，等待在途后再释放依赖；不主动删除原本应保留的远端会话。

### S11.6：完成处理、错误规则与装配收尾

- 完成 worker 和用量归一化编排归 gateway；billing 继续独占价格与资金操作，usage 写分析事实，ops 接收窄观测输入。
- 完成输入固化付款/行为主体、资金来源、请求 ID、指纹、模型链、时间、档位及观测用量。保留普通请求与 WS turn 的取时点、用户费用与账号成本差异。
- 保留结算成功后写记录、结算失败写待对账事实、日志失败不再次扣款、simple 跳过资金事务的行为；不增加供应商重试或完成任务自动重试。
- 落实 B01/B02。保留默认 worker、容量、扩缩容、任务预算、显式 drop/sample/sync、图片 mandatory 和停止池同步兜底；所有已接受的执行均有停止等待所有者。
- 错误规则实体、匹配、管理及缓存迁入 gateway，SQL/Redis/HTTP 分别进入 Adapter，落实 B03—B05。规则仍只影响客户端错误展示和原 Ops 跳过语义，不改变健康分类、重试、审核或结算。
- app 持有唯一网关运行时、会话状态、完成执行器和错误规则实例，删除已完成的 legacybridge。旧任务及维护调用只保留登记过的窄兼容入口。
- 网关路由直接绑定新 handler，server 汇总路由和全局中间件；S13 未迁任务路由通过明确入口注入，不让新 HTTP Adapter 依赖旧聚合 Handlers。
- 每批同步 depguard、消费者清单和 Project Doc，删除迁出文件的精确旧许可，不扩大忽略规则。

## 4. 验证与验收

每批验证新模块、旧委托和直接消费者；使用本地 HTTP/TLS/WS 夹具及隔离 PostgreSQL/Redis，不调用生产凭据或向真实供应商重复推理作影子比较。

| 验证面 | 必须取得的证据 |
| --- | --- |
| B01/B02 | 同步溢出任务未结束时停止不能成功；排队/内联/mandatory 路径；重复启停、Stop 后 Start、超时报告 |
| B03—B05 | 两种回源/保存交错、连续管理写入、输入与返回值隔离、Redis/本地发布、通知兼容、预热及订阅回源取消 |
| B06 | Live 交接期间取消及时返回，不继续为该 sideband 查账号或拨号；控制权清理和 observer 恢复 |
| 准入 | 通用/Google 凭据差异、复合 Key、普通门禁不读 body、错误优先级、三种资金来源、fallback 后再次授权 |
| HTTP/SSE | 各原生/转换路线的字段与事件次序、前导与真实输出、每 attempt 重建、写失败、慢客户端、取消及尾部 usage |
| 媒体与搜索 | 图片实际产出、视频完成认领、音频计量、工具事件及搜索用量；资源归属和非消费入口 |
| WS/Live | 多 turn 模型/价格快照、硬绑定、恢复与当前载荷、租约丢失、连接关闭、observer 和零费用 Live 记录 |
| 完成与资金 | 同 ID 重放/冲突、部分失败资格、结算失败、日志失败、队列溢出、真实事务与一次资金效果 |
| 生命周期 | 构造无启动、唯一实例、停止新请求/attempt 后等待，再停止完成队列，最后关闭 Redis/SQL |

共享状态、会话和完成执行器执行定向 race；存储竞争执行适用 integration race，不扩展全仓 race 或 benchmark。固定问题回归保留行为断言，但协调方式不得依赖修复前必然发生的错误交错。

depguard 夹具覆盖合法方向、精确旧许可、同目录新文件拒绝、旧文件新增禁止 import、迁出例外失效、核心反向依赖及正常 Adapter；验证后删除夹具并保留诊断。使用 go list 核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux，JSON 事件确认实际测试执行。

收尾在 backend 目录使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

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

另验证两个维护命令、Wire 再生成无差异、真实前端产物下 embed 测试，以及 standard/simple 启动和 SIGTERM 网关/会话/完成队列停止顺序。

S10 lint 基线 **1 / 284 / 17** 按路径映射、规则和完整消息比较。原有跳过及真实供应商、硬件、外部 TLS/E2E 限制单列；跳过和仅编译不计行为通过。

## 5. 完成、交接与回退

完成须同时满足：

- 本阶段所有网关生产链使用唯一编排实现，新核心无旧 service、Gin、完整 config 或具体存储反向依赖。
- B01—B06 全部取得修复后行为证据，原失败资料保留。
- 准入、输出、取消、会话、部分用量、完成任务和资源释放的责任清晰；未新增第二套重试、资金实现或缓存。
- 路由、兼容入口、消费者、Wire、构建条件和依赖许可可追踪；支付推广交 S12，任务交 S13，维护交 S14，聚合与兼容清理交 S15/S16。
- 同步系统架构、网关生命周期、策略、错误响应、相关上游、路由结算、HTTP、配置和开发文档，保留稳定锚点。
- SQL、Ent、S00—S10 冻结资料和其他任务文件无意外变化；Wire 差异可解释，新增及已有文件的 `git diff --check` 通过。必要验证未完成时保持“待验”。

满足后 roadmap 更新为 **12 / 17**、S11 已完成，下一步编写 S12 子计划。交付可审查差异，不自动提交。

回退按子步骤恢复代码、装配、规则和文档，不涉及数据库或缓存格式降级；分别记录撤销 B01—B06 后恢复的漏等任务、停止后重开、旧规则覆盖、引用污染、回源取消失效及 Live 交接取消失效风险。



---

## 执行记录

### 2026-09-16：冻结与首批公共依赖

- 原样保存已确认正文，17012 字节；SHA256 `0b37ed644248a3a50a433610b3610e6e899f663c448b7a6dd7f5fd012f7f59be`。HEAD 与计划一致，索引为空，原有 50 个未跟踪文件登记保护。
- 规划证据归档至 [baseline/S11](baseline/S11/README.md)，不改 S00—S10。
- 提取完成执行器与错误规则，生产 Wire 改绑唯一实例；日志经注入端口进入原后端。B01—B05 核心 race 与 B06 直接消费者回归取得通过结果。
- 第一批核心 race 90、直接消费者 unit 174、真实 PostgreSQL/Redis 5 条通过事件，无失败或跳过；集合重叠不可相加。第一批普通 lint 通过。
- 新模型追踪、摘要会话及隔离规则继续迁移。全阶段仍为实施中；请求编排、WS/Live、完成命令和最终全量验收未完成。
- 先提取公共依赖是后续请求链复用的准备步骤，不改变 Messages→Responses→Chat→Gemini 的正式切换顺序；未扩大固定问题清单。


### 2026-09-16：请求投影、固定 Qoder 依赖与完成快照

- 用户明确授权三个并行子 Agent；分别推进 WS/Live、完成计费、媒体，主任务继续准入和文本链。完成器算法与 Live 子链已接入，尚未完成的主循环继续执行，不能据此宣布子步骤完成。
- 准入、复合 Key、模型恢复、请求体解析、心跳与串行队列适配进入 gateway。保持原通用/Google 差异和延迟读取时机；旧入口只提供兼容投影。
- Qoder Chat 生产装配使用固定 `Execute`，删除旧 `app/legacybridge/qoder_chat.go`。完成结果与错误独立；入队前生成独立完成输入。Wire 仅按 provider 变化重新生成。
- 通过的定向结果：认证消费者 unit 373；原生请求/会话/HTTP/认证 race 514；Qoder 与解析/等待直接消费者 unit 433；固定 Execute 核心 race 19。集合重叠，不相加，不代替全量验收。详见各 `.result.json` 和压缩日志。
- 并行编辑中的暂态编译错误单独保存；尚未通过的完成提交消费者和 lint 不计为通过，待相关主循环完成后重跑。
- WS 扩大约定消费者筛选时偶然捕获旧 `openai_compat_model_test.go` 并行 `gin.SetMode` 竞争，只保留当次证据；未追加复现或修复，也未扩大 B01—B06 清单。
- 剩余：Messages→Responses→Chat→Gemini 正式核心切换、媒体/WS 剩余主循环、搜索工具、全部消费者/例外/文档及全量验收。roadmap 保持 11 / 17、S11 实施中。

### 2026-09-16：文本循环、辅助计数与原生 HTTP 接入

- 按 Messages、Responses、Chat、Gemini 顺序把账号尝试循环接到 `gateway/text`。普通/兼容路线分别显式保留部分失败完成资格、输出后禁止重试、同账号预算、OAuth 429 及 Gemini signature 清理时点；每次尝试重新准备报文。
- 通用 Messages、Responses、Chat 与 Gemini HTTP 校验迁入 `gateway/httpapi`；app 构造固定入口并直接绑定路由。并行子任务继续完成 OpenAI HTTP 和 WS HTTP 的最后改绑；这些子步骤仍未宣布验收完成。
- Qoder Messages/Responses 的既有字节输出边界与无等待后额外权益复查单独表达；核心只有该请求的一套账号循环。S09 Chat 仍使用固定 `Execute`。三种错误报文输出迁入 HTTP Adapter，完成输入在入队前冻结。
- 本地 token 估算迁入 `gateway/tokenestimate`，旧入口委托；通用 count_tokens 的无槽选择与重试进入 `gateway/text`，HTTP 校验进入新适配。估算不成为结算用量。
- 已取得的定向结果：本地计数及消费者 race 34；Qoder 核心/HTTP/消费者 unit 508；Qoder/计数输出边界 race 35；扩展路由装配测试及 Wire 结果按独立日志登记。计数均为包含父子测试的事件，集合重叠不能相加。曾因并行子任务尚未写完而失败的编译日志保留，并用完成后的重跑结果区分。
- 门禁按精确文件登记新值类型依赖，媒体 provider 单独应用 I/O Adapter 角色；没有向核心开放 HTTP/数据库。全阶段全量验收、最终构建选择/例外夹具、逐符号矩阵及文档收尾仍待完成。

### 2026-09-16：完整入口改绑与统一请求停止屏障

- OpenAI Responses/Messages/Chat、两类 count_tokens、Responses WebSocket 及模型资源查询已在 app 固定构造并直接绑定新 HTTP Handler；旧方法为兼容委托。Responses 输入 token 预检继续保持其独立选择与重试预算，不套用普通生成请求恢复策略。
- Codex delegation/automation 引导报文、模型替换缓存、客户端会话清洗和 Messages metadata 粘性推导进入纯模块；62 条原引导报文 race 行为事件、86 条会话相关 unit race 事件通过。客户端预热分类与合成响应分别进入 clientmeta/HTTP，原延时和计数展示不改变。
- 新 HTTP 请求与原生平台尝试在 app 引用同一个 `GatewayRequestsAndAttempts` 屏障，仍在停止阶段 15 等待，先于完成队列与共享存储。请求/attempt 不新增取消策略；预算耗尽仍报告未完成数量。Qoder 原有独立活动及 HTTPRequests 并列屏障继续存在。进程回归同步核对新拥有者名称与原顺序。
- 当前新 gateway 普通集合实际通过 401 条测试事件，无失败或跳过；不能据此宣布旧消费者、unit/integration 或全量验收通过。并行迁移期间的暂态编译错误继续保留，与最终补验分开记录。
- 再次核对 6,647 个受保护文件无变化；HEAD 与 main 未变、索引为空，原计划正文摘要不变。剩余平台执行混合文件、审核完成输入、完整依赖/消费者矩阵、文档及最终验证继续执行。

### 2026-09-16：进程、真实存储和门禁夹具

- standard/simple 真实进程启动与 SIGTERM 子测试通过，核对 HTTP/网关请求、完成队列、原生资源与 Redis/Ent 的实际停止日志顺序。事件为 3 条（含父测试），不是三个运行模式。
- Qoder 真实 PostgreSQL/Redis 夹具改走固定 `Execute`，HTTP 只投影认证与报文；保留原资金、用量、重放和取消断言。连同错误规则存储/订阅回归，共 14 条 integration race 事件通过，无失败或跳过。
- 原通用执行消费者的 Gin 全局模式竞争只保留证据；17 个受影响顶层测试分别运行，保留内部 goroutine 与全部断言后取得 22 条 race 通过事件，没有修复或追加调查该历史测试问题。
- 普通/unit/integration 的 57 项 depguard 夹具全部符合预期，覆盖新文件、迁出文件、精确 import 子包、核心反向依赖、纯执行契约叶子和正常 Adapter。初轮捕获本次删旧 Qoder 规则后未清除普通规则排除项的遗漏，已同时清除；这是 S11 自身门禁改动的回归，失败证据单独保存，不新增 B01—B06 历史修复项。
- 夹具及临时子目录全部删除，摘要见 `depguard-fixture-checkpoint.json`。后续新增文件仍需普通全量 lint 核对，最终规则账本与当前配置继续同步。
- 新的工作引用账本工具只做类型引用与接口实现清点，不作为另一套架构检查器。当前工作快照仍含并行编辑时的诊断，明确标记不完整；最终稳定工作树下重新生成并核对。

### 2026-09-16：稳定装配、清单与全量回归

- 所有文本入口改为构造时固定依赖的 Execute；HTTP 调用方不再逐请求组装选择、刷新、计费和完成回调。候选计划在选择器返回时捕获，结果用 PlanProvided 区分缺失，不增加结束后的重算或查询。app 在构造执行器前绑定同一完成 Recorder。
- 通用 Forward、OpenAI HTTP/WS、Grok、Compact 及错误展示均已接入新实现；旧无消费者的私有包装删除，只有 unit 引用的包装移入测试。更新系统架构、网关生命周期、上游、计费、错误、配置、HTTP 和开发文档。
- 稳定清单初稿覆盖 577 个本阶段 Go 文件；normal/unit/integration 类型引用加载无诊断，全部八种构建选择完成。清单随最终修复增量刷新，不改冻结输入。
- 首轮普通全量 11,748 条通过、4 项既有跳过；首轮 unit 在 CN 显式零价的六个子测试失败。原因是本次完成快照投影调用旧兼容方法而替换了调用方分组引用，已改为必要字段的只读判断。原定价断言保持，新增投影不修改源引用的回归；定向 unit 34 条通过。该项属于 S11 新增回归，不是扩大历史问题清单。
- 原失败日志保留；由于修复涉及生产投影，重新串行执行普通、unit、integration 全量。尚未取得最终全部结果，roadmap 继续保持实施中。

<a id="s11_completion"></a>
### 2026-09-16：S11 完成与交接

- S11.0—S11.6 已完成。剩余 HTTP/SSE、WebSocket/Live、辅助/媒体/搜索、会话与完成处理已接入唯一新编排；固定 Execute 在 app 构造时注入依赖，原 HTTP 及供应商取消/输出/资金资格保持。B01—B06 的原失败与修复后事件分别保留。
- 最终普通、unit、integration 全量分别 **11,749 / 19,773 / 12,698** 条通过事件，均无失败；跳过 **4 / 8 / 5**。本次 integration 增加的现有业务日时间条件跳过，其源码未变、S10 原通过证据与本次原因单独登记，未作为通过或追加历史修复。
- 三组 lint **1 / 284 / 17**，按文件、规则、完整消息与 S10 全量诊断完全相同，无新增或移除，不扩大忽略规则。普通/unit/integration 的最终 **57 项** depguard 夹具全部匹配预期且已删除。
- 普通服务、embed、Linux amd64、两个维护命令、版本输出、前端测试/构建和真实产物下 101 条 embed 行为事件通过。Wire 再生成摘要相同；standard/simple 进程启动和 SIGTERM 核对请求/attempt、完成队列及 Redis/SQL 关闭顺序。定向 race、真实存储竞争、固定修复结果见[验收汇总](baseline/S11/verification.md)。
- 578 个本阶段 Go 文件、三种构建集合的符号消费者/接口实现、八种标签/OS 选择、例外、路由场景与文档均有[迁移账本](baseline/S11/migration-ledger.md)。同步实际架构、网关生命周期、策略/上游、错误、计费、HTTP、配置和开发文档，稳定锚点有效。
- 最终保护核对 6,646 项原资料无变化，原有 50 个未跟踪任务文件保留；SQL、Ent、S00—S10、原计划正文与索引状态保持。新增和已有文件 diff 检查通过。未提交、推送、切换分支或生成 Ent。
- 后续：支付/推广 S12，creative/batchimage 状态机 S13，维护 S14，旧聚合/转接清理 S15/S16。真实供应商/硬件/TLS/E2E 和单进程/S07 outbox 恢复限制继续保留，不宣称新增保证。
- roadmap 更新为 **12 / 17**、S11 已完成；下一步编写 S12 子计划。回退按正文逐子步骤恢复；撤销 B01—B06 会恢复漏等任务、停止后重开、旧规则覆盖/引用污染、回源及 Live 取消失效风险，不涉及数据库或缓存格式降级。
