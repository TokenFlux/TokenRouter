# S03：提取协议、能力目录与纯定价

## 1. 基线与实施约定

以当前 `main`、HEAD `9be7627f232899022b9007bb0165c55a7b44af84` 为起点，完成 S03.0—S03.4。保持现有 HTTP、协议、配置、数据库、缓存和资金行为兼容，不扩展账号调度、平台转发或结算事务的迁移范围。

实施前将本计划原样保存到 `refactor/S03-protocol-capability-pricing.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，清单及脱敏验证证据保存到 `refactor/baseline/S03/`；沿用前阶段约定，不另存 `.agents/plans/`，不改写 S00—S02 冻结资料。

已核实 Go 1.27.0、golangci-lint 2.13.2 和 Docker 可用；当前 apicompat、googleapi、antigravity、domain 的普通测试通过。保留 AGENTS.md、其他任务计划和 diagnostics，不自动提交、推送或切换分支。

按用户选择，本阶段修正过时的分组集成测试数据，保留原断言及数据库约束；生产缺陷另行复现和登记。

## 2. S03.0—S03.3：协议与能力边界

### S03.0：冻结输入与逐项清点

记录 HEAD、索引、工作区、工具版本、源文件摘要、SQL checksum 和 Ent/Wire 生成物摘要。

建立迁移清单，覆盖通用协议类型、转换器、混合文件、定价类型与方法，逐项记录目标、直接消费者、构建标签、测试、Wire、文档锚点和兼容入口退出阶段。混合文件按符号职责拆分，不按文件名前缀整体搬迁。

保存 S02 的测试失败名单和未截断 lint 诊断，后续按路径映射、测试名、规则及消息比较。

### S03.1：协议报文与转换唯一实现

- `protocol` 根包拥有 `ProtocolID` 和必要的公共值；Anthropic、OpenAI、Gemini、Google 子包分别拥有各自报文、编解码及 wire 常量；`bridge` 拥有跨协议转换和每条流的转换状态。协议包不反向依赖 bridge、能力目录或具体平台。
- 先迁移 apicompat 的类型与 Anthropic ↔ Responses 非流转换，再迁移 Chat ↔ Responses、Chat ↔ Anthropic，随后完成对应流转换。类型的方法和直接消费者同批迁移；旧 apicompat 只保留类型别名与函数委托，不保留第二份算法或状态。
- Gemini 通用报文和 Anthropic ↔ Gemini 的纯转换从旧 Gemini/Antigravity 实现中提取。HTTP 读取、写出、刷新、重试和取消仍由旧执行层负责，新 bridge 按输入报文或事件推进状态，返回转换输出、结束状态及已观测 usage，不引入整流缓冲。
- 保留现有 `RawMessage`、未知字段承载、nil/空值、JSON 省略和自定义编解码行为。已有兼容报文在 `max_tokens`、metadata、tools 等字段上存在不同形状时，使用明确命名的 wire 变体保留差异，不强行合并为改变载荷的类型。
- Google 错误结构与纯解析迁入 `protocol/google`；HTTP 状态映射迁入协议 HTTP Adapter，激活诊断留旧 Gemini 适配等待 S09。Claude 纯 wire 常量迁入协议包，默认 Header 组合、模型目录和平台策略保留原归属。
- OpenAI/Claude 入站客户端识别的纯字符串解析归入 `gateway/clientmeta`；许可裁决、CLI 版本环境读取与平台默认值仍留对应旧入口，登记 S09/S11。

JSON 宽容修复迁入 `protocol/openai`，仅处理字节输入，保留 BOM、字符串控制字节、转义和规范化后大小限制。核心返回携带 Limit 的独立超限错误；旧 httputil 与 HTTP Adapter 转成原 `*http.MaxBytesError`，保持错误链识别。读取及解压继续使用 S01 的 httpx 实现，不扩大哪些入口会执行宽容修复。

### S03.2：纯能力目录与 HTTP 展示分离

- `routing/capability` 唯一拥有平台/账号类型常量、协议能力描述、原生集合、默认集合、校验和单步转换规则。旧 domain 常量与类型按需别名或委托；新模块不依赖 domain。
- 新候选判断接收平台、账号类型、认证模式、启用协议、客户端协议及 fallback 映射的显式投影，不接收 Account、Group、凭据或 Context。旧账号/分组入口负责解析历史字段及投影，保留字段缺省与显式空集合的区别、排序、错误 reason 和候选逐次重算。
- 保留当前 24 项目录、9 个平台和既有账号认证差异，包括批量图片 provider 绑定、Responses 图片策略及辅助操作。单步 fallback 不变成转换图搜索，不扩大账号原生能力。
- HTTP 方法、路径、别名和 WebSocket 标记在 `gateway/httpapi` 唯一声明；现有路由门禁使用该声明。app 将展示投影注入 `routing/httpapi` 的目录 handler，后者不反向 import gateway HTTP 包。管理员目录保留原 URL、中间件顺序、JSON、数组顺序及非 HTTP 条目的展示内容。
- 协议 effort 值归 protocol；管理员映射和上限的纯规则归 routing。请求字段读取、改写时机及审计仍留旧网关，保留显式请求与桥接默认值的区别。

修正分组集成测试中缺失的协议默认数据，使测试执行到原本要验证的 CRUD、复制和事务回滚。仅补齐缺省夹具；显式空集合及非法配置测试保持原意，不调用统一补全掩盖这些场景，也不修改 SQL 约束。

### S03.3：平台条件通过显式输入传入

- 通用转换器接收明确的 schema、thinking、signature 和工具处理选项。平台/模型判断与 option 选择留旧平台适配；新转换器不自行识别账号、读取配置或查询存储。
- 保留 Gemini 与 Antigravity 已有 schema 清理差异、整数独占下界处理、真实/占位签名、thinking 降级、预算调整及工具配对。Antigravity 的 v1internal 包装、project、身份补丁、session ID、模型回退及 usage hook 仍由旧平台适配拥有，等待 S09。
- 时间与随机 ID 生成通过外层注入，保持原格式及生成时机；纯转换的诊断返回给外层记录，不直接安装日志后端。流状态按请求或 attempt 创建，不增加共享状态。
- 请求追踪、响应模型恢复策略、真实输出判定及重试循环保留在原执行链，登记 S09/S11；本阶段只迁其中明确的 wire 解析，不顺便修改现有恢复或重试语义。

## 3. S03.4：纯定价与数据加载分离

### 纯定价接口

`billing/pricing` 拥有价卡、区间、时间规则、模型价格、用量输入、解析结果、费用明细和缺价错误，以及这些类型的校验、复制、匹配和计算方法。

新计算入口仅接收模型身份候选、已投影价卡、目录价格、用量、计费时刻、服务层级、最终 effort 和倍率。不得接收 config、Group、Account、ChannelService、BillingService 或具体 provider。

旧 ModelPricingResolver 保留渠道读取及错误降级，旧 BillingService 保留配置投影和旧签名，计算全部委托新实现。迁移时保持原有查价顺序和按需读取，不能因构造输入而增加无须执行的查询。资金、额度和缓存接口继续留 S04。

必须保留：

- 分组 → 渠道 → 目录/内置回退，显式零价与缺价分离，纯倍率继承和覆盖、默认桶与区间补全。
- `(min, max]` 区间、总输入量、内置长上下文开关，以及显式区间优先于内置阶梯。
- Fast/Flex、Max effort、分时及高峰倍率的适用范围；普通请求和 WS turn 原有取时点。
- 用户价格与账号统计成本独立，free Fast 仅改变适用的用户价格；账号倍率不影响用户扣款。
- token、按次、图片、视频、搜索和音频计量；缓存创建 5m/1h 及缺失明细回退；创作/批量图片的单张价回退。
- 现有浮点类型、计算顺序及量化边界，不借迁移改金额算法。

### 定价 provider 与装配

`billing/provider` 唯一拥有本地/远端价格加载、hash、fallback/override、缓存、热更新和运行状态；旧 repository 远端客户端转接此实现。构造使用独立 Options，app 投影配置并沿用 S02 的 Initialize、Start、Stop 顺序。

保留原文件格式、字段浅合并与 null 删除、失败时旧目录可用性、代理失败策略、URL 校验、超时及更新周期。纯 JSON 解析和合并返回结果与诊断，文件和网络操作由 provider 执行。

模型身份候选与价格回退保持区别：Grok 动态默认模型及旧平台别名能力通过 app/legacybridge 提供，每次查询读取一次快照；纯定价不引用 xai/openai 旧包，也不建立第二份别名缓存。元数据查询不得继承跨型号价格回退。公开市场价格与实际计算继续使用同一纯规则。

旧 PricingService 只保留构造转接和方法委托，不复制锁、目录、定时器或后台任务。修改手写装配后只生成 Wire，不运行 Ent 生成。

## 4. 依赖门禁与验证

每批同步更新现有 depguard：覆盖 protocol、clientmeta、capability、pricing、provider、HTTP Adapter 和 app 装配。保留原 service/handler 规则及 protocol 精确标准库限制；纯散列等新增依赖按实际文件精确许可，不恢复宽泛标准库或目录级历史豁免。迁出文件删除旧例外。

使用可丢弃夹具验证合法方向、旧入口许可、同目录新增违规、旧文件新增禁止 import、迁出后例外失效、协议反向依赖和正常 Adapter。覆盖普通/unit/integration，并核对 wireinject、embed、Darwin/Linux 文件选择，保留诊断后删除夹具。

| 验证面 | 关键证据 |
| --- | --- |
| 协议 | 三类文本协议双向非流/流、Gemini 转换、未知字段、工具 ID/namespace/custom tools、reasoning、部分 usage、结束与错误事件 |
| 执行链 | 每 attempt 重建状态；前导事件与真实输出后的重试边界；写失败、取消和末尾 usage 保持原行为 |
| 输入修复 | BOM、控制字节、转义、原报文、大小边界及旧 HTTP 错误类型 |
| 能力目录 | 24 项完整 JSON、前端 fixture/类型、空集合、认证模式、单步 fallback、别名/子资源、管理员鉴权 |
| 定价 | 优先级、零价/缺价、倍率、区间边界、长上下文、媒体、账号成本及市场展示一致性 |
| provider | 初始加载、失败回退、override 热重载、并发读、动态别名、代理错误、重复启停与关闭等待 |
| 分组存储 | 修正夹具后的原 CRUD、复制、协议保存及 outbox 失败回滚实际执行 |

每批运行新包、旧转接及直接消费者的普通/unit 测试；存储契约使用隔离 PostgreSQL/Redis。provider 和涉及共享状态的兼容层运行定向 race，不扩展全仓 race 或 benchmark。

收尾统一使用 `GOTOOLCHAIN=go1.27.0`：

```bash
# backend 目录
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

另验证两个维护命令可构建、Wire 再次生成无差异、真实产物下 embed 测试，以及定价运行时相关的启动/SIGTERM 回归。通过 go list 和 JSON 测试事件确认关键测试实际运行，跳过不计通过。

既有 lint 问题按 S02 未截断诊断逐项比较。修正测试数据后暴露的生产问题须在原 HEAD 使用相同夹具复现后登记；S03 引入的问题必须解决，不删除断言或扩大忽略规则。真实供应商 E2E 与外部 TLS 环境限制沿用既有记录。

## 5. 完成、交接与回退

S03 完成须满足：

- 协议转换、能力规则和定价计算已接入唯一生产实现，新纯模块无旧业务、配置、I/O 或具体 Adapter 反向依赖。
- 兼容入口、剩余消费者、构建条件、Wire 和例外均可追踪；S04 的结算、S06 的账号/路由、S09 的平台、S11 的网关及 S15/S16 清理事项明确登记。
- 受影响行为验证通过；必要验证未完成时保持“待验”，不能仅凭编译通过宣布完成。
- 同步现有系统架构、协议能力、相关上游、路由计费、模型市场和开发流程文档，保留有效稳定锚点，描述实际新旧共存结构。
- SQL、Ent、既有冻结资料和其他任务文件无意外变化；Wire 差异可解释，暂存前后及新增文件的 `git diff --check` 均通过。

满足后将 roadmap 更新为 **4 / 17**、S03 已完成，下一步为 S04 子计划。交付可审查差异，不自动提交。

回退按子步骤撤销代码、规则、文档和 Wire 变更，恢复旧调用链；本阶段不引入数据库或缓存格式变更。


## 6. 执行记录

### 2026-09-11—2026-09-12：S03.0 与首批协议迁移

- 原样保存以上计划，正文 SHA-256 为 `45db596a165703ff40c611753efb7b2a3d6f152ed7886616f04c70b4829610b6`；[开始快照](baseline/S03/start.json)记录实际 HEAD、空索引、3076 项源文件摘要及 49 个其他任务文件。完整逐符号初始清点见[压缩清单](baseline/S03/initial-inventory.json.gz)。
- apicompat 的类型/编解码迁入各协议包，转换与流状态迁入 bridge；时间与随机源通过 Runtime 注入，旧入口保持函数签名。行为测试随实现迁移，直接 wire 消费者同批切换；[协议测试](baseline/S03/protocol-first.result.json)通过。
- ProtocolID、能力表、单步候选规则、路由声明与目录展示拆分；app 显式投影 HTTP 描述，目录跨模块夹具测试归 app。前端完整目录 JSON 与类型核对通过；[能力及旧入口测试](baseline/S03/capability-second.result.json)、[旧直接消费者普通测试](baseline/S03/protocol-consumers-normal.result.json)通过。
- Gemini/Antigravity 通用 wire、原生转换、内部 schema 方言、响应/SSE 状态及生成选项已接入新协议实现；平台解包、模型判断、随机回退、身份/session 与 usage hook 接入留旧适配。[原生 Gemini unit](baseline/S03/native-gemini-unit.result.json)、[内部方言 unit](baseline/S03/gemini-options-unit.result.json)通过。
- JSON 宽容修复的 HTTP 超限类型由旧适配恢复，客户端纯解析及管理员推理规则独立；[推理/协议 unit](baseline/S03/reasoning-unit.result.json)通过。迁移中编译与 lint 诊断均单独保留，不能作为既有基线。
- 本批仍在实施：继续核对依赖门禁、分组真实集成测试、协议迁移完整性，再执行 S03.4 定价、全量验证和文档交接。目前不宣布 S03 完成。

### 2026-09-12：S03.1—S03.3 协议与能力边界完成

- 三类文本协议双向非流/流使用 protocol 的唯一实现；Gemini 原生与内部方言保持明确 wire/option 差异。补查混合 HTTP 循环后，将两条原生 Gemini SSE 状态分别提取为逐事件迭代，保留写出、刷新、usage 更新及 ID 生成时序。
- 最后复查移出了文本转换中残留的 GPT 型号判断：capability 保存原桥接型号策略，旧 apicompat 选择显式 RequestOptions。97 个依赖型号选择的测试/辅助声明使用初始原文回到兼容入口；纯转换另测不透明模型名与显式选项，不复制选择算法。
- 24 项目录、9 平台、认证差异、单步 fallback、显式空集合和原 reason 保持；gateway/httpapi 声明 HTTP 路由，routing/httpapi 接收 app 展示投影。真实管理员路由的 401/403、认证/审计顺序和完整 JSON 已验证。
- JSON 修复、Google 错误与客户端纯解析完成分离，旧 HTTP 超限错误可识别；平台 Header/env/身份/session/重试/取消仍由原入口拥有。
- 分组四个集成测试文件共 45 个缺省夹具补齐默认协议数据，原断言、显式空/非法场景和 SQL 约束保持；GroupRepoSuite、复制和 outbox 事务回滚均在真实 PostgreSQL 通过。未发现需要扩大本阶段修复的分组生产缺陷。

### 2026-09-12：S03.4 定价与运行 provider 完成

- billing/pricing 唯一拥有价卡、区间、时间、解析/回退、计算和展示规则；保留浮点顺序、零价/缺价、Fast/Flex/Max、缓存桶、长上下文、媒体与用户/账号独立口径。旧 Resolver 保留按需读取，旧 BillingService 只投影并委托计算；配额缓存常量与接口留 S04。
- billing/provider 唯一持有目录、hash、fallback/override、锁/热更新与启动状态；旧 PricingService 不复制缓存或任务，repository 远端入口转接。app 投影 Options 并复用 S02 hook；legacybridge 按次冻结 Grok 动态默认值，元数据不继承价格跨型号回退。
- 新包及兼容层的定向 race、并发读/加载、快照隔离、单次候选工厂、重复启动/停止、standard/simple 真实 SIGTERM 顺序均通过。Wire 原命令生成并复验无差异，没有 Ent 生成。

### 2026-09-12：收尾与交接

- [迁移清单](baseline/S03/migration.md)关联 320 个变更 Go 文件、3665 个初始声明条目、构建选择、直接消费者与 Wire；两个不再使用的旧请求体预分配常量明确退休。S04、S06、S09、S11、S15/S16 的兼容/混合职责逐项登记，不宣布平台、调度或资金事务已迁移。
- [最终验证](baseline/S03/verification.md)：普通、unit、integration 均实际通过；已有跳过及真实 E2E/TLS 限制独立保留。前端 lint/typecheck、14 文件 199 个关键测试、真实前端 build/embed 注入、普通/embed/Linux 服务与两个维护命令构建通过。
- [未截断 lint 对比](baseline/S03/lint-comparison.json)：普通 1、unit 285、integration 19 项，新增 0；迁移目录的原 QF1003 改为等价 tagged switch 后消除，其余诊断逐项相同。六项原 unit depguard 仍未放行，不用忽略规则制造通过。
- depguard 的十二组可丢弃正反例核验通过，包含标签/OS 选择；部分并发锁阻止执行的命令已串行补验，不将未执行结果计为通过。精确文件/import 许可与原规则摘要见依赖账本。
- 现有系统架构、协议能力、四上游、路由计费、模型市场及开发文档已同步真实结构，稳定代码锚点均有效。[完整性记录](baseline/S03/integrity.json)确认计划原文、SQL/Ent、Go 依赖、528 项旧冻结资料、49 个其他任务文件和索引不变；既有及新增文件 diff 检查通过。
- S03.0—S03.4 完成，roadmap 更新为 **4 / 17**，下一步编写 S04 子计划。没有自动提交、推送或切换分支；回退按原计划逐子步骤恢复调用链，不涉及数据库或缓存格式回退。
