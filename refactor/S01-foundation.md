# S01：启用依赖门禁并提取通用基础能力

## 1. 基线与实施约定

以已提交的 S00（`20105ee8e`）为起点，完成 S01.0—S01.4。当前工作区只有其他任务的未跟踪计划和 diagnostics，实施时重新记录 HEAD、索引和文件摘要，保留这些内容。

开始实施前，将本计划原样保存为 `refactor/S01-foundation.md`，随后登记 roadmap 的链接和“实施中”状态。执行记录、迁移清单、剩余消费者和验证证据追加到阶段文件；较大证据保存到 `refactor/baseline/S01/`，不改写 S00 的冻结资料。

本阶段保持外部 HTTP、数据库、缓存、配置和运行模式兼容。沿用当前 `main`、Go 1.27.0 和本地 golangci-lint 2.13.2，不自动提交或推送。Project Doc 按实际实现同步现有 `docs/`，不提前描述后续阶段的目标架构。

## 2. S01.0：落实可执行的依赖门禁

在现有 `backend/.golangci.yml` 中完成规则，不另建一套架构检查器。

- **按职责匹配文件。** 分别覆盖业务核心、纯叶子契约、protocol、具体 upstream、infra、HTTP/存储/provider Adapter、app 和保留路径。业务核心禁止旧 `service/repository/handler/domain/model`、config、Gin、Ent、数据库客户端及具体 Adapter；模块禁止 import app。
- **落实方向约束。** protocol 不依赖业务和 I/O；具体 upstream 不依赖旧业务或其他平台实现；纯叶子不反向依赖所属根包；HTTP Adapter 不直接依赖仓储和数据库客户端。infra 仅允许明确的技术依赖链，例如 HTTP client → proxy/TLS/timing，不能反向依赖业务策略。
- **保留原有规则。** `service-no-repository`、`handler-no-repository` 及现有排除项保持有效。S00 的六项 unit depguard 违规继续独立记录，不转成许可。
- **精确处理旧依赖。** 根据 S00 的 67 类路径、1805 条 import 账本审核实际冲突项；202 个候选不是自动白名单。payment、待迁 pkg、server/config/setup/web/testutil 分别按角色处理，纯工具规则只覆盖已经确认的纯包。
- **避免例外扩大。** 普通文件应用角色规则；有历史依赖的文件应用包含同等基础限制的专用规则，只增加准确的 import 许可。使用完整文件名和以 `$` 结尾的精确 import，不允许目录级历史豁免。规则旁写中文用途、来源和退出子步骤。depguard 的规则会叠加匹配，不能用另一条 allow 抵消仍然命中的 deny。[配置依据](https://golangci-lint.run/docs/linters/configuration/#depguard)
- **实际证明规则生效。** 在可丢弃夹具中验证合法依赖、已登记旧依赖、同目录新增文件违规、旧文件新增禁止依赖、迁出文件不继承例外、非法子包依赖以及正常 Adapter 依赖。分别覆盖普通、unit、integration，并核对 wireinject、embed、OS 文件选择。夹具完成后删除，保留命令和诊断证据。

每个后续子步骤同批更新规则和过渡账本。新代码调用已经提取的目标包；旧文件新增耦合逻辑仍需代码审查，import 门禁不能替代这项判断。

## 3. S01.1—S01.4：迁移顺序与接口决策

所有能力只保留一份实现。已有消费者可通过旧入口转接；薄包装使用类型别名和函数委托，不复制缓存、状态或算法。阶段清单逐项记录旧入口、目标、剩余消费者及删除阶段。

### S01.1：纯工具与日期计算

| 能力 | 本阶段实现与兼容方式 |
| --- | --- |
| pagination | 保留当前包和行为，纳入纯包门禁。 |
| logredact | 将通用实现和行为测试迁入 `pkg/logredact`；旧 util 入口委托新实现。 |
| IP/CIDR | 将编译、匹配和格式验证提取到 `pkg/ipmatch`。保留无效规则数量等现有语义；Key 黑白名单裁决暂留旧入口，S05 归入 apikey。 |
| PKCE | 建立 `pkg/oauthpkce`，复用随机生成、编码和 S256。保留 OpenAI 的 64 字节十六进制 verifier，以及 Claude/Gemini/xai/Qoder 的 32 字节 Base64URL verifier；平台授权 URL、state、session 生命周期仍由旧平台代码拥有。 |
| timezone | 增加持有显式 `*time.Location` 的日期计算对象，旧函数委托它。保持默认时区、用户时区回退、周起点和日界；`Init`、`time.Local` 与启动顺序暂留兼容路径，S02 再调整装配。 |

### S01.2：错误与 HTTP 适配

- 建立 `pkg/apperror` 作为唯一错误实现，拥有类别、reason、message、metadata、cause、复制和错误链比较。新入口使用具名 `Category`；兼容期类别标识沿用旧 code 数值，保留旧 `Status` 字段与类型别名，避免改变现有 `errors.Is/As` 和字段访问。
- 将 HTTP 状态映射、`ToHTTP` 和响应 envelope 实现迁入 `server/httpx`。旧 `pkg/errors` 保留数值构造器、`Code`、`ToHTTP` 等转接；新核心使用类别和 reason。旧 HTTP 兼容入口登记消费者，于 S15/S16 清理。
- 保持 nil、普通错误、包装错误、自定义状态码、499/502、metadata 深复制、错误字符串和脱敏结果。泛化错误继续使用当前安全消息，不顺便调整错误暴露策略。
- 请求体读取与 gzip/zstd/deflate 解压迁入 `server/httpx`，保留预分配、Header/ContentLength 修改和当前 64 MiB 读取边界。JSON 宽容修复暂留旧 `pkg/httputil`，由其组合新读取函数，S03 再归入协议转换；新 httpx 不反向引用旧包。
- Gin 客户端地址提取迁入 `server/clientip`。保留自定义 Header 优先级、可信代理模式、请求快照、私网回退和现有地址分类，旧 `pkg/ip` 委托新实现。

### S01.3：技术实现与平台策略分离

| 能力 | 本阶段归属及保留边界 |
| --- | --- |
| logging/timing | 迁入 `infra/telemetry/logging`、`timing`。复用现有日志 Options；配置转换暂留旧装配入口，S02 移入 app。HTTP 输出权限仍由 server middleware 决定。 |
| Redis/session | 客户端构造与 instrumentation 迁入 `infra/redis`；通用会话实现迁入其 `session` 子包。保持键前缀、TTL、序列化、一次性消费和故障语义。 |
| proxy/TLS | 合并技术实现到 `infra/httpclient/proxy`、`tlsfingerprint`，保留代理认证、解析差异、拨号取消、握手上限、ALPN 和指纹缓存标识。Profile/Router 管理仍留 S06。 |
| HTTP 池 | 普通共享客户端、req 客户端池和账号隔离的上游池迁入 `infra/httpclient`，分别保留原有缓存命名空间及 key，不能合并成一个新池策略。 |
| PostgreSQL | 提取连接打开、连接池配置、SQL timing、扫描和死锁重试技术实现。Ent 包装、迁移执行编排、密钥补齐、配置校验和 simple 初始化暂留旧装配，S02 继续处理。Ent 错误到业务错误的转换仍留仓储适配层。 |
| AES | `infra/crypto` 接受解码后的密钥，返回具体加密器；旧构造器保留配置读取和现有错误文本。保持 AES-GCM 的 nonce、密文、tag 和 Base64 格式。 |

上游池使用闭合的 `Do(request, transportOptions)` 接口，内部完成获取、执行、解压、失败释放和响应体关闭后的释放。传入的是技术参数快照，不接收整个 config、service 实体或平台枚举。

旧 `repository.NewHTTPUpstream` 继续实现现有 `service.HTTPUpstream`，负责从旧请求上下文和配置生成参数。OpenAI HTTP/2 回退状态与判断、Grok CLI Header 和窄范围 403 回退留在这个旧适配层，分别登记 S06/S09 退出；通过每请求的客户端派生或 transport 包装接入，不能修改共享客户端。

URL 格式、allowlist 和目标策略提取到 egress；DNS 解析执行放入 HTTP 技术实现，必要的纯 IP 判断由 ipmatch 复用。保持当前“校验与实际连接分开”的解析方式、超时、缓存和重定向顺序，不在本阶段引入 DNS pinning 或改变取消语义。

保持原构造器时不修改 Wire 生成结果；确实改变 provider 签名时，修改手写装配后只生成 Wire，并核对差异，不运行无关 Ent 生成。

### S01.4：固定窗口限流

- 将 Lua 计数、TTL 修复、PTTL 查询和 `Allow` 实现迁入 `infra/redis`。保留 `rate_limit:` 前缀、首次设置 TTL、后续不续期、毫秒下限和 RetryAfter 回退。
- server middleware 通过小型 `Allow(ctx, key, limit, window)` 接口使用它，拥有客户端 IP、fail-open/fail-close、429 body 和 Header 格式。
- 接入 auth routes 和 PanelRateLimiter；保持认证路由故障关闭、面板故障放行、按用户/公网 IP 分桶、管理员豁免和设置缓存行为。
- 第二处 middleware 仅保留必要转接；消费者清零即可删除，否则登记 S15 退出。测试改用接口替身，技术集成测试继续使用真实 Redis。

## 4. 验证安排

项目命令统一使用 `GOTOOLCHAIN=go1.27.0`。每个子步骤运行新包、旧包装及直接调用者的普通和 unit 测试；涉及存储、会话、连接或限流时加入 integration。使用 `go list` 和 JSON 测试事件确认构建选择与关键测试实际执行。

重点复用并补齐以下契约：

- 错误新旧入口的比较、包装、状态码、JSON 和脱敏一致；请求体解压、损坏输入、截断边界及原报文保留。
- IP Header 优先级与请求快照、无效白名单仍拒绝；两种 PKCE 格式及 S256 向量；时区与 DST 日期边界。
- 日志后端和 timing collector 的唯一状态，trace 不重复安装，响应首次提交前输出 timing。
- 代理错误不直连、HTTP/SOCKS/HTTPS 代理路径、TLS 身份隔离、池满与在途不可逐出、取消和重复关闭释放。
- OpenAI HTTP/2 协商及回退；Grok 可重放 CLI 403 才回退，权益拒绝保持原响应；公网下载逐跳校验。
- Redis session 一次性消费、限流 TTL 不续期及丢失修复、auth 故障关闭和面板故障放行。
- PostgreSQL 连接参数、启动暂时错误重试、永久错误立即失败、死锁整段重试与取消；AES 旧密文跨实例解密和篡改拒绝。

迁移共享状态的包运行针对性 race，真实 Redis 竞争场景使用 integration race；不扩大为全仓 race 或 benchmark。

阶段收尾统一运行：

```bash
# backend 目录
export GOTOOLCHAIN=go1.27.0

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

S00 的分组 integration 失败和 lint 诊断按原文件、规则及行为核对，不只比较数量。新增问题必须解决；无关原有失败继续归档，不能增加忽略规则来制造通过。真实 E2E 和外部 TLS 捕获环境限制沿用 S00，跳过不计为通过。

## 5. 完成、交接与回退

S01 完成须同时满足：

- 五个子步骤完成，目标实现已接入生产调用路径，旧入口只做已登记的兼容或平台策略适配。
- 新路径、新文件和历史例外均有门禁命中证据；新技术包不反向依赖 config、旧业务或 app。
- 受影响行为验证通过，全量验证只有明确复现并归档的既有问题；必要环境验证未完成时保持“待验”。
- 迁移清单覆盖实际文件、测试标签、直接消费者、Wire 和文档引用；后续 S02、S03、S05、S06、S09、S15/S16 的剩余事项有明确归属。
- 相关 Project Doc 描述真实的新旧共存结构，链接和锚点有效；SQL checksum、生成物和其他任务文件无意外变化，`git diff --check` 通过。

满足后将 roadmap 更新为 **2 / 17**、S01 已完成，下一步为 S02 子计划。回退按子步骤撤销本阶段代码、规则和文档差异，恢复对应旧调用链；本阶段不引入数据库或缓存格式变更，不涉及数据降级。


## 6. 执行记录

### 2026-09-10 · 实施准备

- 原样计划已保存；实施 HEAD 为 `20105ee8e6b6f995867ffc2e27977866ab5c7393`，分支 `main`。
- [工作区快照](baseline/S01/workspace-snapshot.json) 已记录原始后端摘要、索引和其他任务文件，roadmap 标记实施中。
- 下一步：落实 S01.0 精确依赖规则并运行拒绝夹具。

### 2026-09-10 · S01.0 依赖门禁

- 已在既有 golangci-lint 配置新增分角色规则，保留两个旧规则及六项 unit 违规。精确例外见 [dependency-exceptions.json](baseline/S01/dependency-exceptions.json)，覆盖范围见 [dependency-rules.json](baseline/S01/dependency-rules.json)。
- [临时夹具结果](baseline/S01/depguard-fixtures.json) 证明合法 Adapter/core 可通过，十类违规在普通、unit、integration 中被拒绝。首次夹具发现递归 glob 未匹配直属文件，已补充直属模式后重验通过，夹具已清理。
- 后续迁移继续删减过期例外、记录目标依赖及新增文件命中证据。

### 2026-09-10 · S01.1 纯工具

- logredact、IP/CIDR 匹配、PKCE 已提取；timezone 新增显式 Calendar，旧全局入口委托。
- 普通直接消费者测试 10807 通过、5 跳过；unit 18792 通过、9 跳过。结果见 [普通](baseline/S01/s01-1-normal-final.result.json)、[unit](baseline/S01/s01-1-unit-final.result.json)。新增固定向量、编码差异、DST 和无效规则语义测试通过。
- 日期提取中一次机械替换错误已修正；PKCE 提取后两个未使用私有编码函数已删除，变更包 [unit lint](baseline/S01/s01-1-lint-changed.result.json) 为零诊断。一次 lint 并行锁拒绝已保留原日志，之后改为串行执行。

### 2026-09-10 · S01.2 HTTP 与错误

- apperror 唯一拥有错误实体；旧错误类型保留别名。httpx 拥有响应、HTTP 映射和请求体读取，clientip 拥有请求快照及可信代理选择；JSON 宽容修复和 Key ACL 裁决仍在旧入口等待 S03/S05。
- 普通直接消费者测试 10824 通过、5 跳过；unit 18809 通过、9 跳过，见 [普通](baseline/S01/s01-2-normal.result.json)、[unit](baseline/S01/s01-2-unit.result.json)。
- 新旧错误交叉比较、自定义码/499/502、metadata 复制、64 MiB 解压截断及外层 MaxBytesError 契约通过。
- 全量 lint 普通 2 项、unit 68 项，规则分类与 S00 一致；没有新增诊断。

### 2026-09-10 · S01.3 技术实现（进行中）

- logging/timing、Redis/session、proxy/TLS、HTTP/req 池、PostgreSQL 技术和 AES 已提取，旧配置/业务装配仍留原入口。
- 上游池通过技术参数快照闭合获取/执行/释放；OpenAI 回退状态及 Grok CLI 策略由旧适配拥有。机制测试迁到目标包；平台测试通过正式 Do 契约和传输替身观察结果，不为测试导出缓存内部结构。
- [定向池测试](baseline/S01/s01-3-pool-unit-verified.result.json) 66 项通过。仍需完整普通/unit/integration、定向 race、依赖检查及直接消费者回归。

### 2026-09-11T01:31:25.085385+08:00 · S01.3 完成与 S01.4 限流接入

- S01.3 普通直接消费者测试 10944 项通过，unit 18929 项通过；integration 仍只有 S00 的 37 个分组约束失败。技术包 unit race 150 项通过，真实资金存储定向 integration race 22 项通过。
- 固定窗口计数与 TTL 归 infra/redis，HTTP 层通过返回标量结果的计数接口注入；没有为了共享结果类型新增通用 DTO 包，也没有让 HTTP 中间件引用 Redis 实现。
- auth 和面板维度、fail-close/fail-open、Retry-After 与键前缀保持兼容；真实 Redis 的 TTL、并发计数、session 单次消费与 integration race 通过。旧 internal/middleware 已无消费者并删除。
- 两个既有路由装配中的 Redis 技术构造精确登记为 S02 退出；新增旧平台适配测试的 service/profile 准入精确到文件，S06/S09 随测试迁出，新增 config 依赖在三个集合均被拒绝。
- 日志兼容入口直接引用唯一 LegacyPrintf 函数以保留 caller；新旧后端身份与实际日志来源测试通过。

### 2026-09-11T01:31:25.085385+08:00 · 收尾兼容审查与文档

- 补齐初始化前 Now 的单调时钟兼容，以及随机字节生成失败时返回 nil 的旧约定；普通/unit 局部补验通过，最终时钟测试各 13 项通过，变更包 lint 为零诊断。最后运行源码已重新完成普通/embed/Linux 构建。
- 实施期间 AGENTS.md 外部新增“代码不要刻意压行，保持可读性”；保留该改动，并整理本阶段函数、参数和结构体格式。该文件不计入本阶段交付，见 [外部工作区增量](baseline/S01/external-workspace-change.json)。
- 已同步现有 Project Doc 的系统架构、开发工作流、上游传输安全、入口安全和 HTTP 接口边界，新增稳定锚点随当前实现落位；未把业务模块描述为已迁移。

### 2026-09-11T01:31:25.085385+08:00 · S01 阶段结论

- S01.0—S01.4 已完成；roadmap 更新为 2 / 17。最终源码有 2717 个 Go 文件、127 个 Go 目录，受影响 Go 文件 167 个（含删除项）。
- [迁移与交接](baseline/S01/migration.md)、[逐文件清单](baseline/S01/inventory.json)、[剩余消费者](baseline/S01/remaining-consumers.json)、[契约矩阵](baseline/S01/contracts.md) 和 [验证报告](baseline/S01/verification.md) 已提供可核对输入。
- 全量普通测试 11006 项通过、5 跳过；unit 18991 项通过、9 跳过；integration 11637 项通过、37 个原有失败条目、6 跳过。末尾兼容和测试 import 补验单独记录，不与全量计数重复相加。
- lint 普通/unit/integration 仍为 2 / 68 / 10 项；按原路径、规则与消息对齐后没有新增诊断。原有分组协议失败继续归 S03/S06，不修改业务、夹具或忽略规则制造通过。
- 普通、真实前端、embed、Linux 构建通过；定向 race 通过。真实 E2E 与 Linux attestation 运行限制继续保留，跳过不算通过。
- 实际 depguard 共 521 条规则、164 个精确过渡 import；[文件规则覆盖](baseline/S01/rule-coverage.json) 没有遗漏新增文件。18 类基础拒绝场景及新增契约测试的三组拒绝夹具均已执行并清理。
- [最终核对](baseline/S01/final-checks.json) 确认计划正文、HEAD、暂存区保持不变，309 个 SQL migration 和 Ent/Wire 生成物未变，源码摘要、构建选择、例外 import 和代码锚点有效。
- 未提交或推送；其余计划、diagnostics、SYNC.md 和外部 AGENTS.md 改动保留。下一步进入计划模式编制 S02，优先接手配置投影、初始化编排和路由技术装配。

### 2026-09-11T01:50:22.329762+08:00 · 用户授权提交

- 用户明确要求先提交再 review；本次只提交 S01 代码、文档和基线证据，随后审查提交差异。
- 提交前核对 167 个受影响 Go 路径，源码摘要与已归档验证一致，沿用阶段验证结果。其他工作区改动保持原状态，不推送。
