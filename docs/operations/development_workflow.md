# 开发、验证与上游同步

本文记录 TokenRouter 的工具链、代码生成、依赖规则、文件组织、验证策略、发布和上游同步流程。具体版本以仓库 manifest 和 CI 为准；个人凭据和本机临时配置不写入文档。

## 章节导航

- [工具链与本地运行](#工具链与本地运行)：准备环境或更新依赖时读取。
- [依赖规则](#backend_dependency_rules)：新增后端模块、调整 import 或文件许可时读取。
- [编码约定](#编码约定)：写注释、修改前端时读取。
- [后端文件组织](#backend_file_layout)：新增、拆分、合并或改名后端 Go 文件和测试文件，或调整文件内声明顺序时读取。
- [生成代码与迁移](#生成代码与迁移)：修改 Ent schema、Wire 或数据库时读取。
- [验证策略](#验证策略)：实现完成、提交之前读取。
- [提交与文档](#提交与文档)：形成提交、创建 Issue 或 PR、维护 Project Doc 时读取。
- [同步上游](#同步上游)：引入 upstream 的 PR 或 commit 时读取。
- [发布](#发布)：创建版本 tag 之前读取。

## 工具链与本地运行

| 工具 | 版本来源 | 当前要求 |
| --- | --- | --- |
| Go | `backend/go.mod`、CI | `1.27.4` |
| Node.js | `.node-version` | `26.12.1` |
| pnpm | `frontend/package.json` 的 `packageManager` | `12.9.1`；本地 pnpm 按该字段自动切换 |
| golangci-lint | `.golangci-version` | 本地和 CI 使用同一完整版本，配置在 `backend/.golangci.yml` |
| gofumpt | golangci-lint 内置 | 使用默认规则，不开启 extra，不单独维护版本 |
| arch-go | `tools/architecture/go.mod` | `v2.1.2`；通过 Go API 在独立工具模块中运行 |
| dst | `tools/declorder/go.mod` | `tools/declorder` 用它重排声明并保留注释 |
| PostgreSQL、Redis | Compose 和集成测试 | 生产必需；测试由 Testcontainers 或 Compose 提供 |

在仓库根目录安装与 CI 一致的 `golangci-lint`，并将 Go 二进制目录加入 `PATH`：

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@"$(cat .golangci-version)"
```

- 版本对齐：`tools/golangci-lint.sh` 校验本地版本，不匹配直接拒绝；根 Makefile 根据 `backend/go.mod` 设置 `GOTOOLCHAIN` 自动下载目标 Go，workflow 通过 `go-version-file` 安装；Node 与 pnpm 分别由 `.node-version` 和 `packageManager` 驱动。
- pnpm 锁文件：pnpm 12 在 `pnpm-lock.yaml` 开头记录自身版本及平台安装包，缺失会导致冻结安装失败。升级 pnpm 时先更新 `packageManager`，再用新版本执行 `pnpm install` 并提交锁文件。
- ESLint 与 TypeScript：ESLint 10 使用 `eslint.config.js`，依赖覆盖与构建许可维护在 `frontend/pnpm-workspace.yaml`。TypeScript 使用 typescript-eslint 支持的最新 6.x，升 7.x 前核对解析器 `peerDependencies`。
- 本地配置：使用未提交的环境文件或 `backend/config.yaml`，可提交样例位于 `deploy/`。前端开发服务器通过 `VITE_DEV_PROXY_TARGET` 代理后端，端口由 `VITE_DEV_PORT` 控制。

```bash
# 后端
cd backend
go run ./cmd/server

# 前端
cd frontend
pnpm install --frozen-lockfile
pnpm run dev

# 完整源码 Compose
docker compose -f deploy/docker-compose.dev.yml up --build
```

<a id="backend_dependency_rules"></a>
## 依赖规则

业务测试放在所属模块，跨模块接口测试放在 `tests/integration`。模块职责与依赖方向见[后端模块地图](../architecture/backend_modules.md)。通用技术包位于 `internal/infra`，HTTP 工具在 `server/httpx` 和 `server/clientip`，纯工具在明确列出的 `pkg` 包中。

### 架构检查

架构检查位于独立工具模块 `tools/architecture/`，与后端应用依赖隔离：

- 规则组成：`catalog.go` 登记技术库、模块协作、纯叶子包、平台和文件许可；`policy.go` 按目录角色复用约束；arch-go 执行静态依赖检查。同一模块同时运行[后端文件布局检查](#backend_file_layout)。
- 继承与登记：辅助子包继承父目录角色；未登记的顶层模块与平台仅允许极少基础标准库，新增业务或 I/O 依赖需先登记。任意嵌套命名（如 `postgres`）不会自动获得适配层权限。
- 扫描范围：读取全部手写 Go 文件（含测试、构建标签、平台文件和 `wireinject`），跳过生成代码。静态检查不代替各构建组合的实际编译与运行验证。
- 执行入口：`make lint-go` 第一步以 `-count=1` 运行架构检查（避免跨模块源码缓存命中），并验证正反例夹具、扫描覆盖和许可使用方测试。CI 的 `go-lint` 和推送前快检均运行此步骤；架构白名单不放进 golangci-lint。

### 通用规则与文件许可

- 分层隔离：核心不依赖旧实现、框架或存储实现；HTTP 适配层经用例访问数据，不直连数据库。上游平台互不导入；技术包不反向读取完整 config 或业务 service；旧包路径和 `legacybridge` 全局禁止。
- 装配边界：`app` 负责装配，`bootstrap` 与 `lifecycle` 保持独立范围；`setup` 仅入口文件引用精简 bootstrap；业务模块不能反向依赖 `app`。综合设置新增字段时在静态参与者中声明唯一所有者以保持原子保存，提交后应用失败需显式标记已持久化。
- 纯包白名单：`protocol` 等纯角色共用明确的标准库白名单（内存解析、编码、同步、测试和调用方流接口），文件或 HTTP 测试按单文件许可且不继承。`ipmatch` 与 `urlpolicy` 额外允许 `net` 地址类型。arch-go v2 将 `golang.org/x` 视为标准库，适配层第三方库须显式许可；`io`/`net` 副作用仍需人工审查。
- 文件级许可：历史依赖按精确源文件和 import 登记，同目录新文件与子包不继承。规则变更须同时验证无标签和 `integration` 两个集合；删除或迁出文件后须同步移除过时许可（使用方测试会拒绝多余条目），并核对删后重建同名文件、旧文件增禁导、同目录新文件及非法子包四种场景。
- 人工审查范围：静态检查无法发现"通过接口绕过业务用例"或"借已有 import 扩大耦合"，需在代码审查时把关。

同类基础能力收敛为单一默认实现，模块范围与例外许可由 `tools/architecture/policy.go` 锁定：

| 能力 | 默认实现 | 受限例外与门禁 |
| --- | --- | --- |
| JSON 解析与改写 | `encoding/json`，安全敏感与计费用量载荷先经 `protocol/wirejson` 校验重复键与大小写冲突 | `tidwall/gjson` 与 `sjson` 仅限 `gateway`、`protocol`、`upstream` 报文层及已登记的单文件许可 |
| 出站 HTTP 客户端 | `infra/httpclient` 共享标准库连接池 | `imroc/req/v3` 仅限共享浏览器指纹池与已登记的 Cloudflare 站点客户端，详见[上游出站传输安全](upstream_transport_security.md) |
| WebSocket 与配置解码 | `coder/websocket`、`gopkg.in/yaml.v3` | 架构门禁拒绝引入同类替代库 |
| 后端生产日志 | `infra/telemetry/logging`（Zap）与 `log/slog` | `internal/` 非测试文件禁止直接导入标准库 `log`（`telemetry/logging/logger.go` 桥接除外） |
| 进程内缓存与非加密哈希 | 固定小集合用 `pkg/querycache.TTLMap`，高基数限容缓存用 `dgraph-io/ristretto` | 大载荷消息摘要用 `cespare/xxhash/v2`，确定性短键种子与 PostgreSQL 咨询锁保留标准库 `hash/fnv` |

### 各模块依赖边界

| 模块 | 核心边界与专项约束 |
| --- | --- |
| `plugin` | 核心可引用独立 SDK 的 manifest、索引与生成 proto 类型；go-plugin、gRPC、进程、包文件与测试宿主归适配或测试层；禁止核心引用 SDK 运行时与文件实现 |
| `creative`、`batchimage` | 核心、HTTP、PostgreSQL、Redis 与平台适配层分角色门禁；`creative/provider/openai.go` 按单文件许可读取 `upstream/openai` 共享 Images 解析器；任务资金存储参与者不自启也不提交事务 |
| `billing` | PostgreSQL 与 Redis 适配层不互继承存储客户端权限；同连接资金参与和跨存储读取限定至具体文件与目标包；公告与用户读取直接使用 `identity` 数据 |
| `identity`、`team`、`apikey` | 仅测试使用的私有转接放进匹配标签的 `_test.go`，生产代码不保留算法副本 |
| `usage`、`audit`、`ops` | 用户、Key、团队用量 SQL 参与函数复用调用方连接（禁逐条查或分页后排序）；核心不依赖旧实体、Gin 或具体存储；`querycache` 与 `logevent` 按纯包约束；历史许可精确到文件与 import |
| `upstream` | 具体平台互不导入，不接收旧 Provider、Gin 或完整 config；共享 Google 认证在 `upstream/internal/googleauth`，纯报文转换在 `protocol`，授权会话与凭据持久化在 `provider`，重试时机由 `gateway` 决策 |
| `notification`、`site`、`moderation`、`search` | 邮件凭据归 `identity`，阈值归 `billing`，审核跨身份事务复用 SQL 连接，文件读取归 `site/filesystem`，搜索 HTTP 与 Redis 分离；适配层不复制核心状态或算法 |
| `gateway` | `gateway/execution` 叶子包定义请求值与 `Execute`，禁反导根执行器、`text` 或适配层；提供商循环、同提供商恢复与协议转换各保唯一实现；执行适配层仅负责单步调用与数据整理 |
| `scheduler` | 评分、排序、会话与等待不依赖旧实体、Gin 或存储客户端；`scheduler/rediscache/codec` 维护缓存编解码格式，详见[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md) |
| `payment`、`promotion` | `payment` 根包不直接依赖 Ent、config 或 Wire；billing 值、套餐 HTTP 复用和微信身份辅助按文件与 import 单独许可，不继承 |
| 纯规则包 | `pricing`、`capability`、`clientmeta` 等仅用白名单标准库（含 `crypto/sha256`、`encoding/binary`、`iter`），禁止文件或网络读取 |
| `gateway/provider/transport` | 传输测试随本包运行，不继承旧 repository 测试许可 |

## 编码约定

- 注释规范：编写、修改或审查手写代码时遵循 [code-comments](../../.agents/skills/code-comments/SKILL.md)（含原无注释代码与私有声明），使用英文并核对实现约束、失败行为与设计原因。审查覆盖新增/修改文件、文件总览及受影响调用方，连贯阅读整文件通过后再报告完成。生成文件由生成命令更新。
- 文字与文档：文档、提交信息和界面文案遵循 [humanizer](../../.agents/skills/humanizer/SKILL.md)。跨模块不变量同步到 Project Doc，并在关键手写入口标注唯一 `@project-doc` 锚点。
- 前端框架与文案：使用 Vue 3、TypeScript、Pinia、Vue Router、Vue I18n 与自建组件，组件和样式见[前端 UI 规范](../architecture/frontend_ui_conventions.md)。可见文案写入 `src/i18n/locales/` 且中英文 key 一致。
- 状态、API 与锁文件：API 类型与请求集中在 `src/api/`（ESLint 禁止 `src/views/` 与 `src/components/` 直接调用全局 `fetch`），跨页状态放入 store 或 composable，避免在 view 中重复协议。改动依赖须同步更新 `frontend/pnpm-lock.yaml`（CI 使用冻结锁文件）。
- 支付 SDK 分包：支付页动态导入 Stripe 和 Airwallex SDK，`frontend/vite.config.ts` 通过 Rolldown `codeSplitting` 拆成独立 vendor 包（Airwallex 加载时预取远程脚本，须与同命名空间依赖同包）。调整分包后须核对生产构建依赖图，确保仅由支付流程触发加载。

<a id="backend_file_layout"></a>
## 后端文件组织

本节约束 `backend/` 下手写 Go 文件的命名、拆分和结构；生成文件（`ent/`、`wire_gen.go` 等）由生成命令管理。

### 源文件

- 主题命名：单文件单主题，采用 `snake_case`（如 `redeem_admin.go`）。去掉重复包名前缀（`ops/alerts.go`、`apikey/service.go`，避免 `api_key_service.go`）；文件名与包名相同时（`ops/ops.go`）放置包主入口。
- 禁止模糊名：`helper.go`、`helpers.go`、`util.go`、`utils.go`、`common.go`、`misc.go`、`shared.go`、`other.go` 均须改为具体主题名；`helpers` 仅用于测试辅助文件。
- 小文件合并：去除空行、注释和 import 后不足 30 行且无独立主题的文件，并入主题相同或主要调用方文件。`doc.go`、`wire.go`、带平台后缀或 `//go:build` 约束的文件，以及包内唯一源文件不受此限。
- 大包前缀：按主题统一前缀聚合文件（如 `app` 包的 `gateway_*`、`provider_*`），新文件沿用已有前缀。

<a id="backend_declaration_order"></a>
### 文件内的声明顺序

手写 Go 文件顶层声明按 `import`、`const`、`var`、`type`、`func` 排列：

1. `const` 与 `var` 各自合并为一个括号块，块内按用途空行分组并写组注释；多组 `iota` 枚举时，从第二组起单独成块并在块前加 `//nolint:decorder` 及原因。
2. `type` 可单独或分块声明。
3. `func` 中 `init` 居首，构造函数（以 `New` 或 `Must` 开头、返回 `T` 或 `*T` 的导出函数）排在 `T` 的首个方法前；其余函数按调用方在前排列。

- 工具支持：`make fmt` 先用 `tools/declorder` 重排声明并合并 `const`/`var` 块，再交由 `gofumpt` 与 `gci` 格式化。重排保持同类声明及 `var` 初始化相对顺序，相邻无注释单行声明连写对齐，隔空行注释随声明移动并在 stderr 提示复核。
- Lint 强制检查：`make lint-go` 通过 `decorder` 检查声明顺序和块数，通过 `funcorder` 检查构造函数位置。
- 已知限制：`decorder` 会把初始化函数字面量内的 `var`（如 `var x = func() T { var y T; ... }()`）误计为顶层声明，应改为具名辅助函数（如 `var catalog = loadCatalog()`）。
- 合并文件：直接迁入声明后运行 `make fmt` 即可自动归位。

<a id="backend_file_overview"></a>
### 文件总览

新增或修改的手写生产文件须提供文件总览，置于 `import` 之后（无 `import` 时置于 `package` 之后），与首个声明文档注释隔一个空行（`doc.go` 使用包说明格式）。

- 内容规范：首句以实际文件名（含扩展名）为主语说明职责，如 `request_timing.go records request durations and builds Server-Timing headers.`；随后说明背景、主要入口配合及设计原因（含并发协调、信息保留、持久化顺序或多状态计算的条件与后果）。
- 篇幅与边界：篇幅按解释任务分档，不以代码行数决定，详见 [code-comments](../../.agents/skills/code-comments/SKILL.md#mandatory-coverage-and-minimum-lengths)。文件总览不替代 `doc.go`、函数规则注释或特殊分支注释。
- 位置复核：`tools/declorder` 排序后须确认总览未被移入 `const`/`var` 块或文件中部；`tools/architecture/layout.go` 检查包注释位置，总览覆盖与词数由人工或 Agent 按技能核对。

### 包说明 doc.go

手写源文件达到 15 个的包须维护 `doc.go`，仅包含包注释与 `package` 子句，依次写明三部分：

1. 首句以 `Package <包名>` 开头概述职责。
2. 阅读入口：列举 2 至 3 个优先阅读文件，每项一句。
3. 文件分组：按前缀或主题列出分组与代表文件，每组一句。

```go
// Package billing calculates request costs and manages balances, subscriptions, and redeem codes.
//
// Start here:
//   - calculator.go: calculates per-request costs from model prices and usage.
//   - settlement.go: handles charges, refunds, and task fund settlement.
//
// File groups:
//   - subscription*.go: subscription allocation, quota windows, and expiration.
//   - redeem_*.go: redeem code generation, redemption, and administration.
//   - eligibility*.go: balance and quota checks before requests.
package billing
```

其他文件的 `package` 上方留空。新增分组或入口文件改名、删除时同步更新 `doc.go`，文中列出的每个文件都必须存在。

### 测试文件

测试文件按以下命名规则与源文件对应（便于 GoLand 折叠与检索）：

| 文件名 | 内容 | 要求 |
| --- | --- | --- |
| `X_test.go` | `X.go` 的单元测试、基准测试和模糊测试 | 同目录有 `X.go`；存在不启用 `integration` 的构建组合 |
| `X_integration_test.go` | `X.go` 的集成测试 | 同目录有 `X.go`；构建需要 `integration`，可附加平台等约束 |
| `X_external_test.go` | `X.go` 的外部测试包（`package <包名>_test`）单元测试 | `X_test.go` 是包内测试，两者无法合并时使用 |
| `X_external_integration_test.go` | 上一行的集成测试版本 | `//go:build integration` |
| `main_test.go` | 包的 `TestMain` | 包里另有集成测试的 `TestMain` 时，加 `//go:build !integration` |
| `main_integration_test.go` | 集成测试的 `TestMain` | `//go:build integration` |
| `helpers_test.go` | 两个以上测试文件共用的 fixture、替身和断言函数 | 每个包一个 |
| `helpers_integration_test.go` | 集成测试共用的辅助代码 | `//go:build integration` |
| `helpers_external_test.go` | 外部测试包共用的辅助代码 | 包内与包外测试都需要共享夹具时使用 |
| `helpers_external_integration_test.go` | 外部集成测试包共用的辅助代码 | `//go:build integration` |
| `<场景>_scenario_test.go` | 跨多个源文件、找不到单一被测文件的场景测试 | 文件开头的注释写出覆盖的源文件 |
| `<场景>_scenario_integration_test.go` | 上一行的集成测试版本 | `//go:build integration` |

- 归属规则：测试归入被测函数/方法所在源文件的对应测试文件（HTTP handler 归 handler 所在文件）；多源文件混合的测试拆回各文件，被测函数迁移时测试随之迁移。
- 夹具与命名：单文件独用夹具留在本文件，多文件共用放入 `helpers_test.go`。场景或类型写进函数名（如 `TestCalculator_ImagePricingRegression`），避免 `_contract`、`_fixture`、`_unit`、`_regression`、`_lifecycle`、`_compat` 文件后缀。
- 外部测试包与场景文件：能改包内测试的优先合并，仅在导入 `testkit` 等形成循环依赖时使用 `X_external_test.go`；`_scenario_test.go` 仅用于确跨多源文件的场景。`backend/tests/` 与 `backend/migrations/` 按场景命名。
- IDE 折叠：在 GoLand 的 Appearance -> File Nesting 中为 `.go` 父后缀配置子后缀 `_test.go; _integration_test.go; _external_test.go; _external_integration_test.go`。
- 自动布局检查：`tools/architecture/layout.go`（随 `make lint-go`、推送前快检与 CI 运行）检查 `test-name`、`test-tag`、`test-external`、`test-main`、`file-stutter`、`file-vague`、`package-doc` 和 `package-comment`。
- 构建标签校验：布局检查固定 `integration` 取值并核验其余标签组合的可满足性（支持 `!windows` 等约束）。关闭 `integration` 时集成文件须全部排除，开启时至少有一种组合可参与构建。

## 生成代码与迁移

`backend/ent/` 与 `backend/internal/app/wire_gen.go` 由代码生成维护：

```bash
make -C backend generate
```

- 局部生成：依次执行 `go generate ./ent` 和 `go generate ./cmd/server`；仅调整 Wire 装配时可单独执行 `(cd backend && go generate ./cmd/server)`，构建版本仍通过 `-X main.Version` 等参数注入。提交前检查生成 diff 仅含预期变更。
- Go 1.27 说明：jsonv2 生成代码可能将 Ent JSON 字段表示为 `encoding/json/jsontext.Value`，属正常输出。
- 生产迁移：Ent schema 不承担生产迁移；数据库变更一律新增 `backend/migrations/*.sql`，不启用 Ent auto-migrate，不修改已有迁移。编号、`_notx.sql` 与校验规则见[部署与数据库迁移](deployment_and_migrations.md)。

## 验证策略

| 阶段 | 入口 | 内容 |
| --- | --- | --- |
| 开发 | 局部命令 | 改动所在的包或组件 |
| 推送 | `pre-push` hook，等同 `make check` | 按改动文件选择的快检，在当前工作区执行 |
| CI | `.github/workflows/ci.yml` | 全量 lint、单元、集成、前端、构建和脚本检查 |
| 本地全量 | `make verify` | 与 CI 相同的检查，需要 Docker |

### 测试分层与夹具

后端测试按构建标签分三层，新测试默认无标签，仅需 Docker 的测试加 `integration`：

| 层级 | 标签与命令 | 依赖与执行说明 |
| --- | --- | --- |
| 单元测试 | 默认无标签（可附平台标签）；`make test-go` | SQLite、miniredis、sqlmock 或本地 HTTP 夹具 |
| 集成测试 | `//go:build integration`；`make test-integration` | Testcontainers 启动 PG/Redis，仅运行含标签包（并发 4），同时编译同包无标签测试 |
| Embed 测试 | `//go:build embed`；`make test-embed` | 读取前端生产构建产物，检查并测试 `internal/web` 和 `cmd/server` |

- 标签隔离：同包无标签文件与 `integration` 文件会同时编译，测试与辅助函数不可重名；仅单元测试使用的文件标 `//go:build !integration`（如 `tests/integration/identity/suite_test.go`）。本地无 Docker 时在交付说明标明未跑集成测试；涉及迁移须跑 runner 及 schema/数据回归测试。
- E2E 与线上探针：`e2e` 标签测试（`backend/tests/integration`，`make test-e2e`）从环境变量读地址和凭据，未配置时仅验证编译。
- 真实上游开关：调用线上供应商的测试默认跳过，分别由 `OPENAI_API_KEY`、`QODER_RUN_REAL_API_TESTS`、`QODER_RUN_LOCAL_AUTH_TESTS` 和 `TLSFINGERPRINT_NETWORK_TESTS` 开启。
- 跨模块测试布局：`tests/integration/pricing_contract` 属无库单元测试；身份注册和批量任务在 SQLite/miniredis 验规则，事务和并发走 PG/Redis 集成测试；`team/postgres` 同包验有序更新，sqlmock 须登记关闭预期。
- 网关与用量夹具：网关协议测试复用 `gateway/httpapi`、`protocol/openai` 和 `gateway/testkit`；用量测试显式指定 Calendar 验证时区回退、夏令时和清理错误链。
- 容器夹具生命周期：
  - `postgrescontainer.Suite`（identity、promotion）在测试进程内复用 PG 容器：模板库执行全量迁移后关闭连接，单测克隆独立库并用 `DROP DATABASE ... WITH (FORCE)` 清理（失败即单测失败），`TestMain` 回收容器；迁移、恢复与实例级测试使用独立容器。
  - `rediscontainer.Run` 启动独立容器并等待监听及启动日志，上限 1 分钟（覆盖库默认 10 秒以容纳 Docker 并发启动），支持调用方提前取消。
  - 清理顺序：Testcontainers 启动出错时也可能返回非空容器，须先登记容器清理再处理错误，随后登记客户端/连接池清理使其先于容器关闭。清理使用独立有界 background context（`t.Context()` 此时已取消）；`TestMain` 在内部 runner 返回后设置退出码以保证 defer 执行，清理失败令测试失败；保持 Ryuk 开启。
- 虚拟时间与退避：纯内存重试与超时使用 `testing/synctest` 推进虚拟时间；网络测试用本地服务加短退避，退避递增和上限单独单测。

### 推送前快检

每个本地检出执行一次 `make hooks`（配置 `core.hooksPath=.githooks`）。`pre-push` 调用 `tools/check_changed.py`，以待推送引用远端旧提交（新分支或多引用取共同祖先）到当前工作区（含未提交和未跟踪文件）为范围：

| 改动 | 检查 |
| --- | --- |
| 任意文件 | `git diff --check` |
| `backend/**/*.go` | Go 格式、arch-go，改动包的 golangci-lint（`--new-from-rev`，带 integration 标签）和 `go test` |
| `backend/go.mod`、`backend/go.sum` | 同上，lint 和测试范围扩大到整个模块 |
| `tools/architecture/` | 重新扫描后端源码，执行架构和文件布局检查，禁用 Go 测试结果缓存 |
| `frontend/src`、`frontend/public` | 全量前端格式、ESLint、注释、依赖、UI token、类型和检查脚本测试；应用测试用 `vitest related`，删除源文件时运行完整 Vitest |
| `frontend` 下的其他文件 | 完整前端静态检查、检查脚本测试和 Vitest；package、锁文件或 workspace 配置改动时先冻结安装 |
| `backend/internal/pkg/locale/*.json` | 引用这些文件的前端测试 |
| `deploy/`、`tools/goreleaser*` | `make test-scripts` |
| `tools/*.py`、`tools/declorder/`、`.githooks/` | `make test-tools` |

包级单测复用 Go 缓存，架构测试以 `-count=1` 每次重扫；集成测试、前端生产构建与下游调用方由 CI 覆盖。确需跳过可用 `git push --no-verify`；手动执行 `make check` 可传 `BASE=<提交>`。

### CI 与本地全量验证

`.github/workflows/ci.yml` 在每次 push 和 PR 并行运行以下 job（设置 `GOFLAGS=-count=1`，同分支新提交取消旧运行）：

| job | 内容 |
| --- | --- |
| `go-lint` | `make lint-go`、`make test-tools`、`make fmt-go-check` |
| `plugin-sdk` | 独立 Go SDK 的 lint、测试和 proto 重生成检查，Web SDK 的格式、类型、构建测试及最小 Vue SFC 消费夹具 |
| `go-test` | `make test-go` |
| `go-integration` | 安装 PostgreSQL 18 客户端，再执行 `make test-integration` |
| `frontend` | `make lint-frontend`、`make test-frontend` |
| `build` | `make build`、`make test-embed` |
| `scripts` | 在 macOS 上执行 `make test-scripts`，覆盖系统自带的 Bash 3.2 |
| `installer` | `make test-installer`，Linux 安装器依赖 Bash 4+ 和 `sha256sum` |

- CI 缓存：`.github/actions/setup-go` 分 `lint`、`test`、`build` 三组缓存（键含组名、`go.sum` 与 `.golangci-version` 哈希、SHA），`main` push 成功保存，`go-integration` 复用 `test` 组；pnpm 缓存由 `actions/setup-node` 管理。
- 本地全量：`make verify` 依次执行 lint、test-tools、test、test-integration、build、test-embed、test-scripts 和 test-installer（遇错即停），需 Docker、PG 18 客户端、`golangci-lint` 和 Python 3.10+（macOS 用 Ubuntu 24.04 容器跑安装器测试）。

常用局部验证命令：

```bash
# 后端改动包与集成测试
(cd backend && go test ./internal/provider/... ./internal/routing/...)
(cd backend && go test -tags=integration ./internal/usage/postgres/...)
make lint-go

# 前端静态检查、测试与完整构建
make lint-frontend
make test-frontend
make build

# 部署脚本与安全扫描
make test-scripts
make test-installer
make security
```

### 各模块验证要求

| 领域 | 必测证据与环境要求 |
| --- | --- |
| 资金与结算 | 真实 PostgreSQL 事务验证回滚、持久化去重、8/10 位精度与 Redis 用户锁交错（SQLite/mock 不可替代） |
| 身份与鉴权 | SDK 校验、令牌消费和认证缓存需单元测试加 PG/Redis 集成测试 |
| 用量与观测 | 真实队列、事务、取消事件和查询次数证据，仅编译或跳过无效 |
| 上游平台 | 分别验证 HTTP 提交、语义输出、可重试窗口和已观测用量（本地 HTTP/TLS/WS/存储夹具不代替真实供应商验证） |
| 通知、站点、审核、搜索 | 真实 SMTP/TLS 夹具、页面文件访问边界、PG 审核回滚、Redis 预占释放、配置交错与有界关闭 |
| 网关 | 核对实际输出、HTTP 提交和重试窗口；对完成队列、模型快照、WS/Live、规则发布跑定向 `-race`；共享第三方全局设置的顶层测试可分开执行并保留日志 |
| 创作台与批量图片 | 真实 PG 验证写任务表失败回滚与旧请求 ID 重放；本地夹具代替收费生成；分别验证元数据、输出保存（失败不重调上游）和资金变化，临时 Redis 故障不直接判结果丢失 |
| 调度 | 兼容报文验证存储格式；核对实际资源数量以验证取得、故障放行、重复释放、锁过期继任和取消 |
| 纯规则与目录 | 直接传值测试规则，并在调用方验证平台选择、失败取消和热更新；组合测试对照前端 fixture 校验完整管理员目录 JSON、25 项顺序与 TS 类型，禁改夹具掩盖差异 |
| 推广与支付 | 真实 PG 验证 Promo、返利、订单履约与退款短事务；本地夹具模拟渠道；回退新退款代码前须核实并保留 `REFUND_PREPARED` 记录，不可直接替换二进制重退 |
| 备份与维护 | 隔离 PG、本地 S3/HTTP 夹具与临时可执行文件；恢复覆盖提交、SQL 失败回滚、输入中断和取消；系统锁覆盖同业务 ID 不同认领版本；初始化与维护命令验证退出前释放连接，Wire 可重复生成，Ent 与已发布 SQL 不变 |
| 装配与环境 | 覆盖无标签、`integration`、`wireinject`、`embed` 和多 OS 文件选择；备份恢复需 PATH 中有 PG 18 的 `pg_dump`/`psql`；时间戳断言对比库内读回值（Ent 内存纳秒值不可直接对比 PG 微秒值） |

### 依赖安全扫描与最低版本约束

`make security` 汇总 `security-go` 与 `security-frontend`，由 Security Scan workflow 分别调用：

- `security-go`：在 `backend` 和 `plugin-sdk/go` 分别运行固定版本（`.govulncheck-version`）的 `govulncheck`（漏洞库在线更新），覆盖后端未引用的 SDK 包。
- `security-frontend`（主前端）：按锁文件冻结安装并审计生产依赖，保留 stderr，校验报告非空、无错误对象且高危计数与明细一致，按 `.github/audit-exceptions.yml` 核对高危/严重例外与有效期。
- `security-frontend`（Web SDK）：直接运行 `pnpm audit --prod --audit-level=high`，不套用主前端例外。Security Scan 缓存键包含主前端和 Web SDK 两个模块的锁文件。

| 依赖 | 最低版本与固定方式 | 修复要点与升级验证 |
| --- | --- | --- |
| Vue | `3.5.42` | 修复 `@vue/server-renderer` 属性名 XSS |
| `source-map-js` | `1.2.2`（`frontend/pnpm-workspace.yaml` 提升） | 修复 Vue 编译器与 i18n 引入的索引偏移 DoS |
| Vue I18n / `@intlify/message-compiler` | 同固定 `11.4.16` | 修复 [GHSA-83rm-6c8r-g728](https://github.com/advisories/GHSA-83rm-6c8r-g728) `srcdoc` 转义；Composition API + Vite runtime + 默认 JIT，不设 `__INTLIFY_JIT_COMPILATION__`；升级后验证词条、占位符、HTML 转义、语言切换与锁文件清理 |
| `follow-redirects` | `1.16.1`（workspace override） | 修复 [GHSA-q3jh-wrc6-jgq8](https://github.com/advisories/GHSA-q3jh-wrc6-jgq8) 与 [GHSA-8r9p-f939-6h3c](https://github.com/advisories/GHSA-8r9p-f939-6h3c) 跨域重定向敏感头剥离 |
| DOMPurify | `3.4.17`（直接依赖与 workspace override） | 修复 [GHSA-cg2g-xh64-v4q7](https://github.com/advisories/GHSA-cg2g-xh64-v4q7) 与 [GHSA-mrv2-3pqr-j5jr](https://github.com/advisories/GHSA-mrv2-3pqr-j5jr)；用于 Markdown、公告、支付说明和 SVG 清理，升级后验证清理与 SVG 滤镜保留 |

## 提交与文档

提交信息遵循 Conventional Commits（如 `feat(gateway): ...`、`fix(billing): ...`、`docs(project): ...`），每次提交围绕单一可验证目的，并与生成文件、迁移和测试一起提交。新增、移动或重组 Project Doc 文档或 `@project-doc` 锚点后，运行 `make check-docs` 校验索引、内部相对链接和锚点完整性（`python3 tools/check_docs.py --stats` 可同步查看长段落密度）。

### GitHub Issue 与 PR

- 人工入口：Issue 用 [`.github/ISSUE_TEMPLATE/`](../../.github/ISSUE_TEMPLATE/) 英文表单（关闭空白入口），PR 用[英文模板](../../.github/PULL_REQUEST_TEMPLATE.md)。全英文、无 emoji、原样保留用户引用。
- 人工提交材料：新功能先关联 Issue，大改动先与维护者对齐；验证记录写明实际命令与观察结果，UI 变更附截图或录屏，Bug 修复写复现与修复表现。
- Agent 入口：遵循 [AGENTS.md](../../AGENTS.md)，使用 [Issue 模板](../../.agents/github/ISSUE.md)与 [PR 模板](../../.agents/github/PR.md)，缺事实时先追问。项目所有者 PR 默认用普通模板；外部贡献者 Agent PR 须声明 AI 参与。
- 受理边界：依据[上游提供商能力矩阵](../interfaces/upstream_provider_matrix.md)和平台文档，TokenRouter 自身转发和错误处理按模板受理，纯上游原样报错或第三方服务问题先交提供方处理。模板来源与许可见[模板说明](../../.github/NOTICE.templates.md)。

### 前端格式化与静态检查

- 格式化入口：Prettier 版本固定在 `frontend/package.json`。`make fmt-frontend`（`pnpm --dir frontend format`）写入格式，`make fmt-frontend-check`（`format:check`）仅检查，均覆盖全部配置内前端文件（`BASE` 不缩小范围）。
- 风格规则：2 空格、单引号、无分号、100 列、尾逗号、LF；Vue `script`/`style` 保持块内缩进，HTML 用 CSS 空白规则，Vue 用 strict 规则。项目 Prettier HTML 插件格式化 `script`/`style` 并关闭块内字符串嵌入格式化，独立脚本直接关闭嵌入格式化。
- 覆盖范围：包含手写 Vue、TS/JS（含模块与 JSX）、CSS、HTML、JSON、YAML、手写 `.d.ts` 及本地适配图标；资源、许可证、锁文件、缓存、构建产物和生成文件由 `.prettierignore` 排除。
- 静态检查：`lint:check` 依次执行格式、ESLint、`check:comments`（英文注释与文件名总览，深度按 `code-comments` 人工复核）、`check:imports`（API、组件与页面私有模块依赖方向）和 UI token；`make lint-frontend` 随后执行类型检查。
- 脚本测试：`test:tools` 通过 Node 测试运行器验证检查脚本，由 `make test-frontend` 和推送前快检执行。

### Go 格式化

每次提交前在仓库根目录运行 `make fmt` 和 `make fmt-check`（汇总 Go 与前端）。Go 专用入口为 `make fmt-go` 与 `make fmt-go-check`，依赖 Python 3 和精确版本 `golangci-lint`。

- 格式化步骤：`tools/format_go.py` 先校验 `golangci-lint` 版本（不符即停、不改文件），再调用 `tools/declorder` 重排[文件内的声明顺序](#backend_declaration_order)，最后执行 `golangci-lint fmt --config backend/.golangci.yml`（检查模式加 `--diff`）。
- 格式化规则：启用 gofumpt 默认规则、gci 分组（标准库、第三方、本仓库）及 gofmt 重写（`interface{}` -> `any`、`a[b:len(a)]` -> `a[b:]`）。
- 筛选范围：处理后端和工具模块下暂存、未暂存与未跟踪的完整 Go 文件；跳过已删文件、符号链接、`vendor`、`node_modules` 和 `package` 前带 `// Code generated ... DO NOT EDIT.` 的生成文件（手写 Ent schema 正常格式化）。命令不修改 Git 暂存区，部分暂存文件须逐块核对后重新暂存。
- 已提交检查：`make fmt-go-check BASE=<基准提交>` 按 `BASE..HEAD` 检查（干净工作区亦生效）。CI PR 取目标分支与源提交共同祖先；普通 push 比较推送前后；新分支首推比较默认分支共同祖先；默认分支首推比较空树；基准无效时直接失败。
- 静态检查范围：`make lint-go` 带 `integration` 标签检查全部手写文件，`make test-embed` 检查 embed 文件。启用 `errcheck`、`gosec`、`govet`、`staticcheck`、`whitespace`、`unconvert`、`copyloopvar`、`usestdlibvars`、`intrange` 与 `nolintlint`（`//nolint` 须注 linter 名和原因）。
- 注释与风格规则：`godot` 要求顶层文档注释以 `.` 或 `。` 结尾（`@project-doc` 与 `// GET /api/...` 除外）；`revive` 查接收者名、多余 import 别名、`TestMain` 多余 `os.Exit` 及冗余 `if`/`else`；`gocritic` 启 `assignOp`、`captLocal`、`emptyStringTest` 等项。
- 现代写法检查：`modernize` 要求使用 `min`、`max`、`slices.Contains`、`maps.Copy`、`sync.WaitGroup.Go` 和 `errors.AsType`；会改 JSON/字段类型/测试 context 行为的规则以及 Go 1.26+ 新语法已关闭。
- 具名指针错误陷阱：被调函数返回具体指针（如 `*BetaBlockedError`）而外层返回 `error` 时，`if err := f(); err != nil { return err }; return nil` 不能简化为 `return f()`（避免 nil 指针包成非 nil `error`）；在 `if` 上方加 `//nolint:revive` 并写明原因。
- 工具测试：`tools/test_format_go.py` 与 `tools/declorder` Go 测试由 `make test-tools` 在 CI `go-lint` 中执行。

### 本地文件与文档维护

- 本地排除文件：`SYNC.md` 与 `refactor/` 受 `.gitignore` 保护不提交；Codex 计划模式按项目指令将计划保存至 `.agents/plans/`（不纳入提交）。勿覆盖来源不明的工作区修改。
- Project Doc 同步：按[工程文档目录](../index.md)评估文档影响，遵循 `project-doc` 上下文复用规则。架构、领域不变量、接口或运维流程变更时同步更新正文、目录与锚点；局部实现细节不单独成文，README 保持精简入口。

## 同步上游

以 upstream 单个 PR 或 commit 为最小审查单位，逐项保留 fork 在产品、计费、安全与部署上的设计。解决冲突后运行对应测试并按 Conventional Commits 提交，`SYNC.md` 仅留本地。

两条 fork 专属规则：

1. 上游新增 `backend/migrations/` 文件时，按上游提交顺序将数字前缀改写为本 fork 当前最大迁移 ID 依次加一，并更新所有精确引用原文件名之处，不可照搬原名。
2. 上游在 `README.md` 中新增的工程文档不直接并入 README，改归入 `docs/` 对应 Project Doc 或用户手册。

同步完成后除核对 `git diff upstream/...` 外，还须检查迁移顺序、默认配置、i18n、生成文件、部署样例、文档链接和安全扫描，确保已有 fork 改动未被静默回退。

## 发布

发布由 [tr-release 技能](../../.agents/skills/tr-release/SKILL.md)编排，运行 `python3 .agents/skills/tr-release/scripts/tr_release.py --prepare` 获取待发布范围。Release 默认英文，完成后在底部「中文翻译」折叠区追加完整中文译文（覆盖更新说明、安装和文档）。

### SDK 发布门禁

网关发版前由 `tr-release` 分别核对 Go SDK 与 Web SDK 相对各自最近版本的源码变化，并直接完成必要的 SDK 发布（无独立 SDK 发布 CI）：

1. 版本策略：两个 SDK 各自维护 `0.x.y`，兼容修复递增 `y`，新增能力或不兼容变更递增 `x`；纯测试、格式和开发文档变化不单独发版，审阅者须逐项说明交付影响。
2. 固定顺序：SDK 版本准备先提交并通过 CI，再固定网关与 SDK 的发布 SHA。Go SDK 使用模块标签与非 Latest GitHub Release；Web SDK 发至 npm 并打独立来源标签。
3. 完整性校验：网关发版必须携带仓库外的 SDK 审阅文件，脚本核验远端版本、来源提交、Go 公共代理下载与 npm 完整性；缺失、冲突或部分发布均阻断打标。重试时复用已完成的 SDK 发布，不覆盖旧标签或重复发包。

### Release Workflow 与公告

`.github/workflows/release.yml` 由 `v*` tag 或手动 dispatch 触发：

- 二进制与归档：标准发布只构建一次前端，再并行编译 Linux、Windows、macOS 五个 Go 目标，经 `tools/goreleaser_prebuilt.sh` 导入 GoReleaser（`tokenrouter` build ID），生成五个含 `tokenrouter` 的归档与 `checksums.txt`。
- 镜像与精简发布：每架构镜像仅构建一次并复用 Linux 二进制，同时打 GHCR 与可选 DockerHub 标签（未配 DockerHub 不创建占位镜像）。simple release 跳过二进制矩阵仅构建精简镜像。
- 版本回写：从 annotated tag 正文读取 release notes，发布成功后将 `backend/cmd/server/VERSION` 同步回默认分支。
- Discord 公告：`release` 与 `sync-version-file` 成功后（标准与 simple release 均执行），`notify-discord` 经 `DISCORD_RELEASE_WEBHOOK_URL` 和 `tools/notify_discord_release.py` 发送英文公告并附 workflow 链接（拒草稿、超长截断留链接、禁提及）。
- 公告异常处理：缺少 Secret 警告跳过，发送失败仅警告不令发布失败；超时须人工核查，重跑会再发一条。仓库提交动态另由 push Webhook 推送。
- 发布核对：发布前确认提交已推送、CI 全绿、迁移符合单实例整体升级流程且备份已验证；发布后核对 Release、镜像、二进制、`VERSION` 回写及部署冒烟测试。

相关文档：[项目总览](../project_overview.md)、[系统架构](../architecture/system_architecture.md)、[配置](../interfaces/configuration.md)、[部署与数据库迁移](deployment_and_migrations.md)、[版本升级说明](upgrade_notes.md)、[运维目录](index.md)。
