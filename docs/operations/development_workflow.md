# 开发、验证与上游同步

本文记录 TokenRouter 当前的工具链、代码生成、依赖规则、测试分层、发布和 fork 同步流程。具体的工具版本以 manifest 和 CI 为准；本地的凭据和临时的故障记录不写进本文。

## 章节导航

- [工具链与本地运行](#工具链与本地运行)：准备环境或更新依赖时读取。
- [依赖规则](#backend_dependency_rules)：新增后端模块、调整 import 或文件许可时读取。
- [编码约定](#编码约定)：写注释、修改前端时读取。
- [后端文件组织](#backend_file_layout)：新增、拆分、合并或改名后端 Go 文件和测试文件时读取，文件内的声明顺序也在这一节。
- [生成代码与迁移](#生成代码与迁移)：修改 Ent schema、Wire 或数据库时读取。
- [验证策略](#验证策略)：实现完成、提交之前读取。
- [提交与文档](#提交与文档)：形成提交或维护 Project Doc 时读取。
- [同步上游](#同步上游)：引入 upstream 的 PR 或 commit 时读取。
- [发布](#发布)：创建版本 tag 之前读取。

## 工具链与本地运行

| 工具 | 版本来源 | 当前要求 |
| --- | --- | --- |
| Go | `backend/go.mod`、CI | `1.27.2` |
| Node.js | `.node-version` | `26.10.0` |
| pnpm | `frontend/package.json` 的 `packageManager` | `12.9.1`；本地 pnpm 读取该字段并切换到声明的版本 |
| golangci-lint | `.golangci-version` | 本地和 CI 使用同一个完整版本，配置在 `backend/.golangci.yml` |
| gofumpt | golangci-lint 内置 | 使用默认规则，不开启 extra，不单独维护版本 |
| arch-go | `tools/architecture/go.mod` | `v2.1.2`；通过 Go API 使用，由独立的工具模块运行 |
| dst | `tools/declorder/go.mod` | `tools/declorder` 用它改写声明顺序并保留注释，独立的工具模块 |
| PostgreSQL、Redis | Compose 和集成测试 | 生产必需；测试可以由 Testcontainers 或 Compose 提供 |

本地安装和 CI 相同版本的 lint，以免规则集不同，导致问题只在 CI 上出现：

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@"$(cat .golangci-version)"
```

在仓库根目录执行安装命令，并把 Go 安装目录里的二进制加入 PATH。`tools/golangci-lint.sh` 会验证实际的版本，版本不符的本地工具会被拒绝；CI 的 action 从同一个版本文件读取要安装的版本。

Go 版本在 `backend/go.mod` 声明。根 Makefile 据此设置 `GOTOOLCHAIN`，本地 Go 较旧时自动下载声明的版本；workflow 使用 `go-version-file` 安装。Node 版本在 `.node-version` 声明，CI 和 Dockerfile 按它安装。pnpm 版本写在 `frontend/package.json` 的 `packageManager` 字段，CI 的 `pnpm/action-setup` 和 Dockerfile 都读取这个字段。pnpm 12 把自身版本和各平台安装包记录在 `pnpm-lock.yaml` 开头的独立文档里，缺少这段记录时冻结安装会失败。升级 pnpm 时，先改 `packageManager`，再用新版本执行一次 `pnpm install`，把锁文件的变化一起提交。前端 ESLint 10 使用 `eslint.config.js`，pnpm 12 的依赖覆盖和构建许可维护在 `frontend/pnpm-workspace.yaml`。TypeScript 使用 typescript-eslint 支持的最新 6.x 系列，升级到 7.x 前核对解析器的 peerDependencies。

个人的数据库路径、固定的密码，或者某台机器的服务配置，不写进工程文档。开发配置使用不提交的环境文件或 `backend/config.yaml`；可以提交的样例在 `deploy/`。前端开发服务器默认通过 `VITE_DEV_PROXY_TARGET` 代理到后端，端口由 `VITE_DEV_PORT` 控制。

常用入口：

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

HTTP、用例、存储和后台资源，由 app 装配各模块的实现。业务测试放在实际负责的模块里，跨模块的接口测试放在 `tests/integration`。包的职责和依赖方向见[后端模块地图](../architecture/backend_modules.md)。通用的技术实现在 `internal/infra`，HTTP 工具在 `server/httpx` 和 `server/clientip`，纯工具在明确列出的 pkg 包里。

### 架构检查

架构检查在 `tools/architecture/`，和后端应用的依赖隔离：

- `catalog.go` 保存技术库、模块协作、纯叶子包、平台和必要的文件许可；`policy.go` 按目录角色复用约束；arch-go 执行依赖判断。
- 普通的辅助子包继承所属的角色。没有登记的顶层模块和平台，只能使用少量基础标准库，要声明业务或 I/O 依赖，需要先登记。新目录不能靠任意嵌套的 `postgres` 之类的名称，获得适配层的权限。
- 扫描器读取所有手写的 Go 文件，覆盖测试、构建标签、平台文件和 wireinject；生成代码不参与架构规则的判断。这是静态的 import 检查，所有构建组合实际编译、执行的结果，需要另外验证。

`make lint-go` 的第一步用固定版本 arch-go 的 Go API 检查规则，并运行正反例、扫描覆盖和文件许可的使用方测试。工具要读取独立模块之外的源码，所以入口使用 `-count=1` 关闭测试缓存。CI 的 go-lint job 和推送前快检都运行这一步，CI 的 lint 缓存组同时包含后端和架构工具的 Go 依赖。修改角色和模块关系时，维护规则表，不另外引入生成配置的流程。golangci-lint 负责通用的代码质量检查，架构白名单不放在它里面。

同一测试模块还运行[后端文件布局检查](#backend_file_layout)。

### 通用规则

- 核心不依赖旧的业务实现，也不依赖框架和存储的实现；HTTP 适配层通过用例访问数据，不直接访问数据库。
- 具体的上游平台不依赖其他平台的实现；技术包不反过来读取完整的 config 或业务 service。旧的包路径始终禁止。
- app 是装配的位置，bootstrap 和 lifecycle 有各自独立的依赖范围。存储、任务队列和上游客户端在 app 的 wireinject 集合里绑定；旧的聚合包和 legacybridge 路径被全局规则拒绝。setup 只有实际的入口文件可以引用精简版 bootstrap；模块不能反向依赖 app。
- 综合设置新增字段时，在 app 的静态参与者里声明这个字段和键唯一的所有者，保持一次原子保存，提交后应用失败时明确标记为已持久化。HTTP 和 DTO 使用所属模块的能力，测试夹具只提供数据或 I/O 替身。
- protocol 和其他纯角色共用一份明确的标准库白名单，允许内存里的解析、编码、同步、测试，以及调用方提供的流接口。确实需要读取文件或构造 HTTP 输入的测试，按实际文件单独许可，其他纯文件不会继承。ipmatch 和 urlpolicy 只额外允许 `net` 的地址类型。`io`、`net` 和其他允许的包里，具体调用的副作用仍然需要代码审查。arch-go v2 把 `golang.org/x` 归为标准库，适配层要按实际的第三方库单独许可，不能随标准库一起放行。

### 文件级许可

保留路径上的旧依赖，按准确的源文件和 import 登记：许可不覆盖同目录的新文件，也不覆盖允许包的其他子包。每次添加路径或修改规则，分别用无标签和 integration 两个集合验证合法依赖和违规夹具。已有的失败同样需要按源文件登记，才能通过检查。

删除或迁移文件后，同步删除对应的文件许可，并检查源目录和目标目录的角色。文件许可的使用方测试，会拒绝已经没有实际 import 的过时条目。验证要覆盖：删除后恢复同名文件、旧文件新增禁止的 import、同目录的新文件和非法的子包；只检查迁出后的正向 lint 是不够的。迁出的文件按目标目录的角色规则检查，同目录新增的文件不会继承例外。

角色检查识别不了"通过接口绕过业务用例"，也识别不了"借着已有的 import 增加耦合"，这两项仍然需要代码审查。

### 各模块的依赖规则

- billing：缓存和提醒设置的读取，按核心角色和模块关系许可；app 负责组合动态设置、价格配置、通知、推广、提供商 outbox 和支付能力。公告和 billing 的用户读取直接使用 identity 的数据。PostgreSQL 和 Redis 适配层不互相继承存储客户端的许可；同连接的资金参与、跨存储的数据读取等窄权限，限定到具体的文件和目标包。
- identity、team、apikey：生产实例和同连接事务的参与工厂由 app 绑定。只剩测试在使用的私有转接，放进对应标签的 `_test.go`，生产代码里不保留算法副本。
- usage、audit、ops：各自使用自己的核心和适配层。用户、Key、团队的用量 SQL 参与函数复用调用方的连接，不能改成逐条查询或分页之后再排序。新的核心不导入旧实体、Gin 或具体的存储；纯包 `querycache`、`logevent` 和已经迁移的统计值有独立的职责规则。历史的构造、HTTP 上下文和测试适配的许可精确到文件和 import，普通的新文件不会继承。
- upstream：具体平台之间不能互相导入，也不接收旧的 Provider、Gin 或完整的 config。共享的 Google 认证原语在 `upstream/internal/googleauth`，纯报文和转换由 protocol 提供；提供商的授权会话和凭据持久化在 provider。app 整理参数，gateway 决定重试的时机。
- notification、site、moderation、search：核心、纯接口和适配层按职责匹配架构规则。邮件凭据留在 identity，阈值留在 billing，通知接收已经确定的事件；审核跨身份的事务使用同一个 SQL 连接；文件读取在 `site/filesystem`；搜索的 HTTP 和 Redis 分开。适配层不复制核心的状态或算法，文件移动时同步清理专用的许可和排除项。角色夹具覆盖新文件、精确的历史 import、非法子包和迁出后的同名文件。
- gateway：请求值和固定的 `Execute` 接口在 `gateway/execution`，这个叶子包不能反向导入根执行器、text 或适配层。构造 HTTP 时，app 先绑定完成记录器（只有一个），再构造执行器；请求调用只传入明确的状态和同步输出。内部的提供商循环、平台同提供商的恢复和协议转换各只有一个实现，不能再通过新增回调把旧的 handler 整个包起来。执行适配层只提供单步调用和数据整理。
- creative、batchimage：核心、HTTP、PostgreSQL、Redis 和平台适配层使用各自的角色门禁。任务资金引用的 scope 只在 app 注册，所属的存储参与者不开启、也不提交自己的事务。
- scheduler：核心、HTTP、Redis、PostgreSQL 分别使用角色门禁。评分、排序、会话和等待不接收旧实体、Gin 或存储客户端；需要的 ProviderSnapshot、RoutePlan、singleflight 和散列依赖，由核心角色和模块关系许可。`scheduler/rediscache/codec` 维护 `sched:v4` 的完整提供商和轻量数据。
- payment、promotion：依赖门禁覆盖新的核心、HTTP、PostgreSQL 和 app。payment 根包不直接依赖 Ent、config 或 Wire；billing 的值、套餐 HTTP 的复用和微信身份的辅助，分别按实际的文件和 import 许可，新文件和迁出的文件不继承许可。
- 纯规则包：协议哈希和 Gemini 迭代器使用纯角色的 `crypto/sha256` 和 `iter` 许可；clientmeta 的版本库按纯角色许可；app 的定价装配和目录 HTTP 的接口测试，按对应的角色和模块关系检查。pricing、capability、clientmeta 使用明确的标准库集合，不能增加文件或网络读取。
- 传输：传输测试随 `gateway/provider/transport` 运行，旧 repository 测试的许可不会随路径迁移被继承。

## 编码约定

所有手写代码都写必要的中文注释，生成文件只由生成命令更新。注释写约束、失败时的行为，或者代码里看不出来的原因。注释、文档、提交信息和界面文案按 `.agents/skills/humanizer/` 的规则书写。跨模块的不变量同步到 Project Doc，并在关键的手写入口添加唯一的 `@project-doc` 锚点。

前端使用 Vue 3、TypeScript、Pinia、Vue Router、Vue I18n 和项目自己的组件。修改界面时：

- 组件、样式和选择框的约定见[前端 UI 规范](../architecture/frontend_ui_conventions.md)。
- 用户可见的文案放进 `src/i18n/locales/`，中英文的 key 保持一致。
- API 的类型和调用放在 `src/api/`，跨页面的状态放进 store 或 composable，view 里不重复写协议。
- 修改依赖时同步更新 `frontend/pnpm-lock.yaml`，CI 使用 frozen lockfile。

支付页动态导入 Stripe 和 Airwallex SDK，`frontend/vite.config.ts` 通过 Rolldown 的 `codeSplitting` 将它们分别放进独立的 vendor 包。Airwallex 在模块加载时会预取远程支付脚本，因此它和同命名空间的依赖一起分包。调整分包规则后，检查生产构建的依赖关系，确认支付 SDK 由支付流程触发加载。

<a id="backend_file_layout"></a>
## 后端文件组织

本节规定 `backend/` 下手写 Go 文件的拆分和命名。带生成标记的文件（`ent/` 的生成代码、`wire_gen.go` 等）由生成命令决定。

### 源文件

- 一个源文件写一个主题，文件名用 snake_case 写出主题，例如 `redeem_admin.go`。
- 文件名去掉所在目录的包名：`ops` 包里的告警文件叫 `alerts.go`，`apikey` 包里的服务文件叫 `service.go`。`api_key_service.go` 这种开头几段拼起来等于包名的写法也要去掉前缀。文件名正好等于包名时（`ops/ops.go`），用它放包的主入口。
- 源文件的名字要能看出主题。`helper.go`、`helpers.go`、`util.go`、`utils.go`、`common.go`、`misc.go`、`shared.go` 和 `other.go` 都看不出主题，换成具体名字。`helpers` 留给测试辅助文件。
- 去掉空行、注释和 import 后不到 30 行的源文件，如果没有自己的主题，就并进主题相同或者使用它最多的文件。几个常量、一两个错误变量、一个几行的小函数、一个只有一处调用的转换函数，都属于这种情况。`doc.go`、`wire.go`、带平台后缀或 `//go:build` 约束的文件，以及包里唯一的源文件，再小也单独保留。
- 大包按主题给文件加统一前缀，让同一主题的文件在目录里排在一起，例如 app 包的 `gateway_*` 和 `provider_*`。新文件沿用已有前缀。

<a id="backend_declaration_order"></a>
### 文件内的声明顺序

手写 Go 文件的顶层声明依次是 import、const、var、type、func：

- const 和 var 各写成一个括号块。块里不同用途的声明用空行隔开，每组上方写注释。iota 按块内序号计数，一个文件有多个 iota 枚举时，第二个起单独成块，块前加 `//nolint:decorder` 并写明原因。
- type 可以分开声明，也可以写成块。
- func 里 init 排在最前，构造函数（以 `New` 或 `Must` 开头、返回 `T` 或 `*T` 的导出函数）排在 `T` 的第一个方法前面。其余函数按调用关系排列，调用方在前。

`make fmt` 对改动的文件运行 `tools/declorder`，按上面的规则移动声明、合并 const 和 var，再交给 gofumpt 和 gci 格式化。它保持同类声明的相对顺序，var 的初始化顺序随之不变。合并时每条原声明在块内自成一组，组之间空一行。相邻的两条都是没有注释的单行声明时连写，由 gofmt 对齐。和下一条声明隔着空行的注释会跟着那条声明移动，工具在 stderr 里列出这些位置，需要人工确认注释仍然放在对的地方。`make lint-go` 用 golangci-lint 的 decorder 检查声明顺序和块数，用 funcorder 检查构造函数的位置。

decorder 把初始化表达式里函数字面量内的 `var` 当成顶层声明计数，`var x = func() T { var y T; ... }()` 会被误报。这种初始化写成具名函数，例如 `var catalog = loadCatalog()`。

合并文件时，把源文件的声明直接放进目标文件，再运行 `make fmt`，声明会移到对应的位置。

### 包说明 doc.go

手写源文件达到 15 个的包需要 `doc.go`，文件里只有包注释和 `package` 子句。包注释依次写三部分：

1. 第一句以 `Package <包名>` 开头，写包负责什么。
2. 阅读入口：第一次读这个包时先看哪两三个文件，每个文件一句话。
3. 文件分组：按前缀或主题列出文件分组，每组一句话，写出代表文件。

```go
// Package billing 计算请求费用，维护余额、订阅和兑换码。
//
// 阅读入口：
//   - calculator.go：按模型价格和用量计算单次请求的费用。
//   - settlement.go：扣费、退款和任务资金的结算。
//
// 文件分组：
//   - subscription*.go：订阅的分配、额度窗口和过期。
//   - redeem_*.go：兑换码的生成、兑换和后台管理。
//   - eligibility*.go：请求前的余额和额度准入。
package billing
```

包注释写在 `doc.go` 里，其他文件的 `package` 子句上方留空。新文件归入已有分组时，`doc.go` 保持原样；新增分组，或者入口文件改名、删除时，在同一次修改里更新 `doc.go`。`doc.go` 提到的每个文件名都要在目录里存在。

### 测试文件

GoLand 把 `X_test.go` 折叠在 `X.go` 下面，读者也靠这个名字从源文件找到测试。测试文件按下表命名：

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

归属和拆分按下面的规则判断：

- 测试函数直接调用的函数或方法定义在哪个源文件，测试就写进这个源文件对应的测试文件。HTTP handler 的测试归到 handler 函数所在的文件。
- 一个测试文件里的测试函数分别测不同的源文件时，按测试函数拆到各自的测试文件。被测函数移到别的文件时，测试跟着移动。
- 只有一个测试文件使用的 fixture、替身和构造函数，写在这个测试文件里；两个以上测试文件使用时，放进 `helpers_test.go`。
- 测试类型和场景写进测试函数名，例如 `TestCalculator_ImagePricingRegression`。文件名使用上表的形式，`_contract`、`_fixture`、`_unit`、`_regression`、`_lifecycle`、`_compat` 这类后缀改写进函数名。
- 包内测试和外部测试包的文件合并时，先改成包内测试 `package <包名>`。外部测试导入的包反过来依赖本包（常见于 `testkit`），改成包内测试会形成导入环时，放进 `X_external_test.go`。
- 能找到被测文件时使用 `X_test.go`，`_scenario_test.go` 留给确实跨多个源文件的场景。

`backend/tests/` 存放跨模块测试，`backend/migrations/` 的测试检查 SQL 文件，这两个目录的测试按场景命名。

`X_integration_test.go` 等三种带后缀的测试文件，需要在 GoLand 里加一条折叠规则：打开 Project 视图选项菜单里的 Appearance → File Nesting，找到父文件后缀为 `.go` 的规则（没有就新建一条），把子文件后缀改成 `_test.go; _integration_test.go; _external_test.go; _external_integration_test.go`。

布局检查在 `tools/architecture/layout.go`，由 `make lint-go`、推送前快检和 CI 运行。检查规则为 `test-name`、`test-tag`、`test-external`、`test-main`、`file-stutter`、`file-vague`、`package-doc` 和 `package-comment`，分别检查测试命名、构建标签、外部测试包、TestMain 位置、重复包名前缀、模糊源文件名、包说明及包注释位置。构建标签检查固定 `integration` 的取值，再检查其他标签是否存在可满足的组合，因此支持 `!windows` 等平台约束。集成文件在关闭 `integration` 时的所有组合都需要排除，启用时至少有一种组合能参与构建。

## 生成代码与迁移

`backend/ent/` 的大部分文件由 Ent 生成，`backend/internal/app/wire_gen.go` 由 Wire 生成。统一使用：

```bash
make -C backend generate
```

这个目标依次执行 `go generate ./ent` 和 `go generate ./cmd/server`。只改了 Wire 装配时，运行 `(cd backend && go generate ./cmd/server)` 即可；这个入口委托给 app 的生成位置，构建版本仍通过 `-X main.Version` 等变量注入。修改 `backend/ent/schema/`、生成 feature 或 Wire provider 之后，提交对应的生成差异，并检查差异里只有预期的 schema 和依赖变化。

Go 1.27 的 jsonv2 生成代码，可能把 Ent 的 JSON 字段表示为 `encoding/json/jsontext.Value`，这是预期的结果。

Ent schema 不是生产环境的迁移器。数据库的变更需要新建 `backend/migrations/*.sql`，不能依赖 Ent 的 auto-migrate，也不能修改已有的迁移。编号、`_notx.sql`、checksum 和 fork 同步上游时的重新编号规则，见[部署与数据库迁移](deployment_and_migrations.md)。

## 验证策略

| 阶段 | 入口 | 内容 |
| --- | --- | --- |
| 开发 | 局部命令 | 改动所在的包或组件 |
| 推送 | `pre-push` hook，等同 `make check` | 按改动文件选择的快检，在当前工作区执行 |
| CI | `.github/workflows/ci.yml` | 全量 lint、单元、集成、前端、构建和脚本检查 |
| 本地全量 | `make verify` | 与 CI 相同的检查，需要 Docker |

### 测试分层

后端测试按构建标签分三层：

- 单元测试可在关闭 `integration` 的构建组合中运行，也可按平台添加约束。`make test-go` 执行 `go test ./...`。外部依赖用 SQLite、miniredis、sqlmock 或本地 HTTP 夹具代替。
- 集成测试带 `//go:build integration`，通过 Testcontainers 启动 PostgreSQL 和 Redis。`make test-integration` 用 grep 找出含这个标签的包，只编译和运行这些包，包级并发为 4。集成构建同时编译同包的无标签测试，这些包的单元测试会再运行一次。
- embed 测试带 `//go:build embed`，读取前端生产构建的产物。`make test-embed` 对 `internal/web` 和 `cmd/server` 运行 lint 和测试。

新测试默认不加标签，需要 Docker 的测试才加 `integration`。同一个包的无标签文件和 integration 文件会一起编译，两边的测试函数和辅助函数需要使用不同的名字。只给单元测试构建用的文件标记 `//go:build !integration`，例如 `tests/integration/identity/suite_test.go`。测试文件的命名和归属见[后端文件组织](#backend_file_layout)。

集成测试需要 Docker。本地没有 Docker 时，交付说明里写明集成测试没有运行。涉及迁移时，还要运行 migration runner 和对应的 schema、数据回归测试。

外部 E2E 测试带 `e2e` 标签，位于 `backend/tests/integration`，用 `make test-e2e` 运行，服务地址和测试凭据从环境变量读取。没有配置服务和供应商凭据时，结果只说明测试通过了编译。

调用线上供应商接口的测试默认跳过，分别由 `OPENAI_API_KEY`、`QODER_RUN_REAL_API_TESTS`、`QODER_RUN_LOCAL_AUTH_TESTS` 和 `TLSFINGERPRINT_NETWORK_TESTS` 开启。本机设置了这些变量时，`make test-go` 会向供应商发出请求。

`backend/tests/integration` 也存放跨模块的装配测试，具体的执行集合由文件的构建标签决定：

- `tests/integration/pricing_contract` 保存价格配置和市场价卡、Key 快照、完成处理和资金分配的跨模块测试，属于单元测试。纯的提供商统计匹配和计算测试在 `billing/pricing`。这些测试直接调用模块和已有的存储替身，没有连接数据库。
- 身份注册和邮箱绑定，使用 identity 和 PostgreSQL 适配层，在 SQLite 夹具上验证规则；批量任务的运行时使用 batchimage 和 miniredis。PostgreSQL 和 Redis 上的事务和并发行为由集成测试验证。
- Messages、Chat、Responses 和 Raw Chat 的协议测试，直接构造 `gateway/httpapi` 的单次执行器，共用实际的请求、响应和会话组件；纯流终态和用量 JSON 的断言在 `protocol/openai`。阻塞读取、响应关闭等 I/O 替身在 `gateway/testkit` 共用，测试不重建旧的网关应用图。
- 用量 HTTP、仪表盘和 DTO 的接口测试，直接构造 usage 和使用方的查询数据，不经过旧的 service 或完整的设置服务。日期测试明确指定 Calendar，分别覆盖用户时区的回退、夏令时和各入口的结束边界；清理任务的存储缺失错误，先由 PostgreSQL 适配层的测试核对，再用相同的错误链输入 HTTP 夹具。
- 团队所有权的两次有序 SQL 更新，由 `team/postgres` 同包的测试直接验证。sqlmock 夹具检查关闭错误时，同时登记关闭的预期，测试资源的清理不会被误判为业务 SQL 的失败。

identity 和 promotion 的集成测试通过 `postgrescontainer.Suite` 在各自的测试进程内复用 PostgreSQL 容器，模板数据库执行当前源码的全部迁移后关闭连接。每个测试从模板克隆独立数据库，支持提交、回滚和多连接；测试完成后依次关闭应用连接、连接池并删除数据库。删除使用 `DROP DATABASE ... WITH (FORCE)`，服务端还没处理完的断开会被直接终止，删除失败时测试失败。`TestMain` 最后回收容器。模板随进程销毁，迁移、恢复及实例级测试使用独立容器入口。

Redis 测试通过 `rediscontainer.Run` 启动独立容器，等待监听就绪及启动日志，等待上限为一分钟。调用方的 context 可提前取消；超时或就绪检查失败仍使测试失败。这个入口覆盖库默认的十秒监听等待，以容纳 Docker 并发启动时的延迟。

纯内存的重试与超时测试使用 `testing/synctest` 推进虚拟时间。网络测试使用本地服务和短退避配置，退避算法单独核对递增与上限，生产默认参数由正常配置提供。

### 推送前快检

每个检出执行一次 `make hooks`，它把 `core.hooksPath` 设为 `.githooks`。`pre-push` 调用 `tools/check_changed.py`，以待推送引用的远端旧提交为基准。新分支使用上游分支或远端默认分支的共同祖先，一次推送多个引用时取这些基准的共同祖先。比较范围是基准到当前工作区，未提交和未跟踪的文件也计入，检查在当前工作区执行。

| 改动 | 检查 |
| --- | --- |
| 任意文件 | `git diff --check` |
| `backend/**/*.go` | Go 格式、arch-go，改动包的 golangci-lint（`--new-from-rev`，带 integration 标签）和 `go test` |
| `backend/go.mod`、`backend/go.sum` | 同上，lint 和测试范围扩大到整个模块 |
| `frontend/src`、`frontend/public` | 改动文件的 eslint，`check:ui`、`typecheck` 和 `vitest related` |
| `frontend` 下的其他文件 | 依赖文件改动时先冻结安装，再跑完整 lint、typecheck 和 Vitest |
| `backend/internal/pkg/locale/*.json` | 引用这些文件的前端测试 |
| `deploy/`、`tools/goreleaser*` | `make test-scripts` |
| `tools/*.py`、`tools/declorder/`、`.githooks/` | `make test-tools` |

快检使用 Go 测试缓存，没有改动的包直接复用上次结果。集成测试、前端生产构建和改动包的下游使用方由 CI 检查。快检失败会阻止推送，修复后重新推送；确需跳过时使用 `git push --no-verify`。手动执行 `make check` 时，基准默认取上游分支的共同祖先，也可以传 `BASE=<提交>`。

### CI

`.github/workflows/ci.yml` 在每次 push 和 PR 上并行运行以下 job：

| job | 内容 |
| --- | --- |
| go-lint | `make lint-go`、`make test-tools`、`make fmt-check` |
| go-test | `make test-go` |
| go-integration | 安装 PostgreSQL 18 客户端，再执行 `make test-integration` |
| frontend | `make lint-frontend`、`make test-frontend` |
| build | `make build`、`make test-embed` |
| scripts | 在 macOS 上执行 `make test-scripts`，覆盖系统自带的 Bash 3.2 |
| installer | `make test-installer`，Linux 安装器依赖 Bash 4+ 和 `sha256sum` |

workflow 设置 `GOFLAGS=-count=1`，每次重新执行 Go 测试。同一分支推送新提交时，还在运行的旧检查会被取消。失败原因看对应 job 的日志，`go test` 在输出末尾列出失败的测试和断言。

Go 的模块、编译和 golangci-lint 缓存分 lint、test、build 三组，由 `.github/actions/setup-go` 恢复。缓存键由组名、`go.sum` 与 golangci-lint 版本的哈希、提交 SHA 组成，恢复时按前缀取最近一份。main 分支的 push 成功后保存缓存，go-integration 恢复 go-test 保存的 test 组。pnpm 的下载缓存由 `actions/setup-node` 管理。

### 本地全量验证

`make verify` 依次执行 lint、test-tools、test、test-integration、build、test-embed、test-scripts 和 test-installer，任何一步失败就停止。它需要 Docker、PostgreSQL 18 的 `pg_dump` 和 `psql`、`.golangci-version` 声明的 golangci-lint，以及 Python 3.10+。macOS 上的安装器测试通过 Ubuntu 24.04 容器执行。发布前或改动范围很大时运行它。

### 局部验证命令

```bash
# 改动所在的包
(cd backend && go test ./internal/provider/... ./internal/routing/...)

# 单个包的集成测试，需要 Docker
(cd backend && go test -tags=integration ./internal/usage/postgres/...)

# 架构测试和全量 golangci-lint
make lint-go
```

### 各模块的验证要求

- 资金：使用真实的 PostgreSQL 事务确认回滚、持久化去重、8 位和 10 位的精度，以及 Redis 用户锁的交错；SQLite 和 mock 不能代替这些证据。
- 身份：SDK 验证、令牌消费和认证缓存需要单元测试，以及 PostgreSQL 和 Redis 上的集成测试。
- 用量和观测：需要真实的队列、事务、取消事件和查询次数的证据，只编译或跳过都不能代替。
- 上游平台：分别验证 HTTP 提交、语义输出、可重试的窗口和已观测的用量。平台验证使用本地的 HTTP、TLS、WS 和隔离的存储夹具，这些结果不能当作对真实供应商的验证。
- 通知、站点、审核、搜索：真实的 SMTP 和 TLS 夹具、页面文件的访问范围、PostgreSQL 上审核的回滚、Redis 的预占和释放、配置的交错，以及有上限的关闭。
- 网关：分别核对实际的输出、HTTP 提交和重试窗口，不能只比较最终的字符串；失败可以带有已观测的用量，但完成资格按各入口的规则判断。对完成队列、模型快照、WS 和 Live、规则发布运行定向的 race 测试，真实的资金和 Redis 协议使用隔离的存储。不同的顶层测试如果共用第三方的全局测试设置，可以分开执行，内部的并发和断言保持不变，失败和补验的日志都要保留。
- 创作台和批量图片：真实的 PostgreSQL 测试要覆盖写任务表失败时整体回滚，以及旧请求 ID 的重放。平台和 GCS 的测试使用本地夹具，不用真实收费的生成做回归。成功的元数据、输出保存和资金变化分别核验：输出保存失败时，不会重新调用供应商；临时的 Redis 故障也不能直接认定结果永久丢失。
- 调度：测试使用兼容的报文验证存储格式。确认取得和故障放行、重复释放、锁过期后的继任和运行时的取消，要验证实际的资源数量，只检查返回码不够。
- 纯规则：测试直接传入值；平台选择、HTTP 的失败和取消、目录的热更新，还要验证实际的调用方。管理员目录完整的 JSON、25 项的顺序和 TypeScript 类型，由 app 的组合测试对照前端的 fixture，不能靠修改夹具掩盖输出的差异。
- 推广与支付：资金验证使用真实的 PostgreSQL，分别检查 Promo、返利转入、订单履约和退款的短事务；退款渠道使用本地夹具，不发起真实的付款或退款。回退新的退款代码之前，保留并核实 `REFUND_PREPARED` 记录，只替换二进制、再重新发起渠道退款是不行的。
- 备份和维护：只对隔离的 PostgreSQL、本地的 S3 和 HTTP 夹具，以及临时的可执行文件操作。恢复至少覆盖真实的成功提交、SQL 失败回滚、输入中断和取消；系统锁覆盖同一业务 ID 不同的认领版本。二进制替换的测试不能使用测试进程或部署实例的真实路径。初始化和两个维护命令，要验证退出前释放了连接，Wire 可以重复生成，Ent 和已发布的 SQL 保持不变。
- 装配：验证覆盖无标签、integration、wireinject、embed 和各 OS 的文件选择。lint 通过后，还需要确认规则实际检查到了目标文件。

### 其他环境要求

备份恢复的集成测试，还需要 PATH 里有 `pg_dump` 和 `psql`。目前的恢复夹具使用 PostgreSQL 18，CI 明确安装 PostgreSQL 18 的客户端；本地也使用同一个主版本，runner 自带的旧客户端可能无法备份测试数据库。断言时间戳不变时，比较操作前后从数据库读回的值；Ent 创建时返回的纳秒级内存值，和 PostgreSQL 保存的微秒值不能直接比较。

前端命令：

```bash
# ESLint、UI token 检查和类型检查
make lint-frontend

# 完整 Vitest
make test-frontend

# 生产资源和嵌入资源的后端
make build
```

部署文件变更时执行 `make test-scripts` 和 `make test-installer`。依赖安全检查使用 `make security`，Security Scan workflow 分别调用它的 `security-go` 和 `security-frontend`。govulncheck 版本在 `.govulncheck-version` 固定，漏洞数据在线更新。前端审计的 stderr 原样输出；报告为空、包含错误对象或结构不完整时返回失败，高危漏洞按 `.github/audit-exceptions.yml` 核对例外与有效期。pnpm 按高危等级过滤漏洞明细，汇总仍包含低等级计数；脚本检查高危计数与明细是否一致。

Vue 的最低版本为 `3.5.42`，该版本修复了 `@vue/server-renderer` 属性名检查中的 XSS 漏洞。`frontend/pnpm-workspace.yaml` 将低于 `1.2.2` 的 `source-map-js` 依赖提升到修复版本，覆盖 Vue 编译器和 i18n 引入的索引偏移 DoS 漏洞。更新这些依赖时同步维护锁文件，并运行前端测试、生产构建和安全扫描。

管理员用量导出通过动态导入 `xlsx` 生成工作簿，调用 `aoa_to_sheet`、`sheet_add_aoa` 和 `write`。依赖固定为 [SheetJS 官方分发](https://docs.sheetjs.com/docs/getting-started/installation/nodejs/)的 `0.20.3` tarball，锁文件记录下载地址和完整性校验值。该版本包含原型污染和 ReDoS 的修复。升级时验证分页数据追加、工作簿序列化、前端生产构建和安全扫描。


## 提交与文档

提交信息遵循 Conventional Commits，例如 `feat(gateway): ...`、`fix(billing): ...`、`docs(project): ...`。一次提交围绕一个可以验证的目的，生成文件、迁移和接口测试，和它们的源码变更一起提交。

### Go 格式化

每次提交代码之前，在仓库根目录运行 `make fmt`，再运行 `make fmt-check`。这两个入口需要 Python 3 和指定版本的 golangci-lint。`tools/format_go.py` 筛选文件后，调用 `golangci-lint fmt --config backend/.golangci.yml`，检查模式加上 `--diff`。配置启用 gofumpt 的默认规则，gci 按标准库、第三方、本仓库的顺序分组 import，并保留 gofmt 的 `interface{}` → `any`、`a[b:len(a)]` → `a[b:]` 两条重写规则；gofumpt 不单独维护版本。

命令处理暂存、未暂存和未跟踪的 Go 文件，按整个文件格式化，覆盖后端和仓库的工具模块；删除的文件、符号链接、vendor 和 node_modules，以及带标准生成标记的文件会被跳过。生成标记是 `package` 声明之前的 `// Code generated ... DO NOT EDIT.`，Ent schema 等手写的源文件照常参与格式化。脚本会预先排除生成文件，格式化配置也使用严格的生成文件识别。

格式化之后检查 diff，把属于这次提交的修改重新暂存。部分暂存的文件需要逐块核对，命令不会修改 Git 的暂存区。检查入口发现格式差异或工具执行失败时返回非零状态。推送前快检检查整个待推送区间的格式。

检查已经提交的改动，使用 `make fmt-check BASE=<基准提交>`，它按基准和 HEAD 的差异选择文件，工作区干净时也会检查。CI 的 PR 检出源提交，以目标分支和源提交的共同祖先为基准；普通 push 比较推送前后的提交，新分支第一次推送比较默认分支的共同祖先，默认分支第一次推送比较空树。基准无法解析时检查失败，不会悄悄跳过。

`make fmt` 先用 `tools/declorder` 重排[文件内的声明顺序](#backend_declaration_order)，再运行 `golangci-lint fmt`。golangci-lint 开始前，入口先校验版本，版本不符时任何文件都不会被改写。

`make lint-go` 对全部手写文件运行格式检查、声明顺序检查和静态分析，带 integration 标签覆盖无标签和集成文件，`make test-embed` 检查 embed 文件。除了 errcheck、gosec、govet、staticcheck 这类错误检查，lint 还启用 whitespace、unconvert、copyloopvar、usestdlibvars、intrange 和 nolintlint。nolintlint 要求每条 `//nolint` 写出 linter 名和原因。godot 要求顶层声明的文档注释以句号结尾，中文用“。”，英文用“.”，Project Doc 锚点和路由注释（`// GET /api/...`）除外。revive 检查接收者命名、多余的 import 别名、`TestMain` 里多余的 `os.Exit`，以及可以去掉的 if 和 else。gocritic 只开 assignOp、captLocal、emptyStringTest 等几项有明确改法的检查。modernize 要求使用标准库的新写法，例如 `min`、`max`、`slices.Contains`、`maps.Copy`、`sync.WaitGroup.Go` 和 `errors.AsType`。会改变 JSON 输出、字段类型或测试 context 行为的几项，以及 Go 1.26 以后的新语法，在配置里关闭。

被调函数返回具体的指针类型（例如 `*BetaBlockedError`）、外层函数返回 `error` 时，`if err := f(); err != nil { return err }; return nil` 不能改写成 `return f()`：nil 指针会被包成非 nil 的 error。revive 的 if-return 会报这类位置，在 if 上方加 `//nolint:revive` 并写明原因。

`tools/test_format_go.py` 在临时 Git 仓库里验证文件筛选、生成代码排除、暂存区保护、干净工作区下的提交差异、声明重排，以及两种格式化规则同时生效。`tools/declorder` 的 Go 测试覆盖合并、无注释单行声明的连写、iota、构造函数和单行方法的间距。CI 的 go-lint job 通过 `make test-tools` 运行这两组测试。

### 本地文件和文档

`SYNC.md` 是本地的同步进度，受 `.gitignore` 保护，不要提交。`refactor/` 保存本地的重构计划和验证资料，整个目录不纳入版本控制；需要共享的工程说明维护在 `docs/`。使用 Codex 计划模式时，实施之前按项目指令把完整的计划保存到 `.agents/plans/`，执行进度追加到末尾。工作区里来源不明的修改不要覆盖；提交之前，按文件核对暂存的范围。

每次代码变更，都根据[工程文档目录](../index.md)判断相关的文档；已经读过、上下文里仍然保留足够内容的索引和章节，按 `project-doc` 的"读取与上下文复用"规则复用，每次改文件或收尾时不需要重读。持久的架构、领域不变量、对外接口或运维流程变化时，同步更新正文、分类目录和代码锚点；局部的实现细节，不需要扩写成新的文档。README 保持为简洁的项目入口，工程细节放在 `docs/`。

## 同步上游

同步以 upstream 的一个 PR 或 commit 为最小的审查单位，逐项理解变更，并保留 fork 在产品、计费、安全和部署上的设计。解决冲突后，运行这一项涉及的测试，再形成符合 Conventional Commits 的本地提交；`SYNC.md` 只记录本地的进度，不进入提交。

两条 fork 专属的规则：

1. 上游新增 `backend/migrations/` 文件时，按上游的顺序，把前缀重新编号为本 fork 当前最大迁移 ID 依次加一，并修复所有精确引用了文件名的地方；不能按原名照搬。
2. 上游在 `README.md` 里新增的工程文档，不直接并入 README；把内容归到 `docs/` 里合适的 Project Doc 或相关的用户手册，没有合适的位置时，再新建规范命名的文档。

同步之后，只检查 `git diff upstream/...` 不足以证明行为正确，还要核对本 fork 的迁移顺序、默认配置、i18n、生成文件、部署样例、文档链接和安全检查。fork 已有的修改，不能为了减少冲突而悄悄回退。

## 发布

`.github/workflows/release.yml` 由 `v*` tag 或手动 dispatch 触发。标准发布只构建一次前端，再把 Linux、Windows 和 macOS 的五个 Go 目标，分配到独立的 runner 并行编译；最后的 job 通过 `tools/goreleaser_prebuilt.sh` 把这些二进制导入 GoReleaser，统一生成 Release 归档、校验和、双架构镜像和 manifest。GoReleaser 使用 `tokenrouter` build ID 生成五个平台归档，每个归档包含 `tokenrouter` 可执行文件，镜像复用其中的 Linux 二进制。

每个镜像架构只构建一次，同时打上 GHCR 和可选的 DockerHub 标签；没有配置 DockerHub 时，不会创建占位镜像。simple release 跳过二进制矩阵，只构建精简的镜像集合。workflow 从 annotated tag 的 body 读取 release notes，成功后把 `backend/cmd/server/VERSION` 同步回默认分支。

`release` 和 `sync-version-file` 成功后，`notify-discord` 向 Discord 发布英文版本公告，标准发布和 simple release 都会执行。仓库 Actions Secret `DISCORD_RELEASE_WEBHOOK_URL` 保存 `releases` 频道的专用 Webhook URL，地址使用 Discord 生成的完整值。通知步骤从 GitHub Release 读取版本名、更新说明和下载链接，`tools/notify_discord_release.py` 构造消息并附上本次 workflow 链接。草稿发布会被拒绝，长说明会截断并保留完整 Release 链接，消息关闭所有提及通知。

缺少 Secret 时通知步骤输出警告并跳过。发送失败会显示警告，发布结果仍为成功。脚本等待 Discord 返回消息回执；网络超时后由维护者检查频道，确认消息是否送达。重跑已成功的通知会再发送一条消息。GitHub feed 的提交动态使用仓库 push Webhook，发布公告由 Release workflow 发送。

发布之前，确认目标提交已经推送、CI 通过、数据库迁移可以滚动升级，并且备份已经验证。发布之后，检查 Release、镜像、二进制、VERSION 的回写和部署的冒烟测试；tag 只标识代码版本，迁移和恢复的检查仍然要做。

相关文档：[项目总览](../project_overview.md)、[系统架构](../architecture/system_architecture.md)、[配置](../interfaces/configuration.md)、[部署与数据库迁移](deployment_and_migrations.md)、[版本升级说明](upgrade_notes.md)、[运维目录](index.md)。
