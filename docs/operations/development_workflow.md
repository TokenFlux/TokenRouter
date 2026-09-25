# 开发、验证与上游同步

本文记录 TokenRouter 当前工具链、代码生成、测试分层、仓库约束、发布和 fork 同步流程。它替代旧开发指南中的个人机器路径、固定本地凭据和临时故障处置；具体版本始终以 manifest 与 CI 为准。

## 章节导航

- [工具链与本地运行](#工具链与本地运行)：准备环境或更新依赖时读取。
- [代码边界](#代码边界)：新增后端/前端模块时读取。
- [生成代码与迁移](#生成代码与迁移)：修改 Ent schema、Wire 或数据库时读取。
- [验证策略](#验证策略)：实现和提交前读取。
- [提交与文档](#提交与文档)：形成提交或维护 Project Doc 时读取。
- [同步上游](#同步上游)：引入 upstream PR/commit 时读取。
- [发布](#发布)：创建版本 tag 前读取。

## 工具链与本地运行

| 工具 | 当前来源 | 当前约束 |
| --- | --- | --- |
| Go | `backend/go.mod`、CI | `1.27.0` |
| Node.js | `.github/workflows/backend-ci.yml` | `20` |
| pnpm | CI 与根 Makefile | `9`；根命令默认使用 `npx --yes pnpm@9` |
| golangci-lint | CI | `v2.13`，配置在 `backend/.golangci.yml` |
| PostgreSQL、Redis | Compose 与集成测试 | 生产必需；测试可由 Testcontainers/Compose 提供 |

本地应安装与 CI 相同的 lint 版本，避免规则集差异造成只在 CI 出现的结果：

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13
```

升级 Go 时必须同时修改 `backend/go.mod`，以及 `backend-ci.yml`（两处）、`release.yml`（两处）和 `security-scan.yml` 中的 `go version` 硬断言；workflow 都通过 `go-version-file: backend/go.mod` 安装工具链，任一断言遗漏都会在版本校验步骤失败。

不要把个人数据库路径、固定密码或某台机器的服务配置写入工程文档。开发配置使用未提交的环境文件或 `backend/config.yaml`；可提交样例在 `deploy/`。前端开发服务器默认通过 `VITE_DEV_PROXY_TARGET` 代理后端，端口由 `VITE_DEV_PORT` 控制。

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

根 Makefile 中保留 `build-datamanagementd`/`test-datamanagementd` 目标，但当前仓库没有 `datamanagement/` 源树；不要把这些目标纳入默认通过条件，除非该可选源码已随工作范围提供。

<a id="backend_dependency_rules"></a>
## 代码边界

尚未迁移的 handler/server 仍调用 service，旧 repository 继续实现保留接口；billing 的核心、HTTP、PostgreSQL 和 Redis 已按角色分离，旧入口只作登记过的转接；通用技术实现已分布在 `internal/infra`，HTTP 工具在 `server/httpx`、`server/clientip`，纯工具在明确列出的 pkg 包中。旧目录里的兼容入口不代表其中所有能力仍拥有独立实现。综合设置的新增字段必须在 app 静态参与者中声明唯一字段/键所有权，并保持一次原子保存、提交后应用失败明确标记已持久化。原生 HTTP/DTO 不引用旧聚合 handler；仅剩历史测试的转接移入测试文件，并精确登记 S16 退出项。

`.golangci.yml` 保留普通 handler/service 对 repository、Redis、GORM 的原有限制，并按职责约束新业务核心、纯叶子契约、protocol、upstream、infra 和具体 Adapter。核心不依赖旧业务或框架/存储实现，HTTP Adapter 不直接访问数据库；具体上游不能依赖其他平台实现，技术包不反向读取完整 config 或业务 service。规则同时匹配目录直属文件和嵌套文件；新增的未分类路径也有默认约束。

protocol 的六组生产与测试规则使用精确的标准库白名单，允许内存解析、编码和调用方提供的流接口，不通过 `$gostd` 放行文件、进程或网络执行包。测试仅额外允许 `testing`；确需读取文件的测试应按实际源文件单独审核。`io` 的流接口可以使用，但允许包中的具体调用和间接副作用仍需代码审查。其他角色的标准库许可按各自职责维护，例如 ipmatch 的 `net` 地址解析依赖不受这项收紧影响。

保留路径的旧依赖按准确源文件和 import 登记，许可不覆盖同目录新文件，也不覆盖允许包的其他子包。每次添加路径或修改规则，要分别用普通、unit、integration 集合验证合法依赖与违规夹具；已有失败不能自动转成白名单。角色检查不能识别“通过接口绕过业务用例”或“借现有 import 增加耦合”，这两项仍需代码审查。

billing 的 singleflight、隔离倍率缓存和提醒设置读取按实际文件许可；app 的动态设置、渠道、通知、推广、账号 outbox 和支付桥接同样精确到文件/import。公告和 billing 的用户读取直接投影 identity，不再经旧身份仓储桥接。PostgreSQL 与 Redis Adapter 不互相继承存储客户端许可，新增核心文件不继承这些专用依赖。资金测试必须确认真实 PostgreSQL 事务回滚、持久去重、8/10 位精度及 Redis 用户锁交错；SQLite 和 mock 不能替代这些行为证据。

identity、team、apikey 的生产实例和同连接事务参与工厂由 app 固定；旧资料、Key 和管理接口只作投影与委托。身份 SDK 验证、令牌消费和认证缓存需要分别覆盖普通/unit 构建选择及真实 PostgreSQL/Redis；只剩测试消费者的私有转接放入对应标签的 `_test.go`，不保留生产算法副本。

usage、audit、ops 已使用各自核心和 Adapter；用户/Key/团队的用量 SQL 参与函数复用调用方连接，不能改成逐条查询或分页后排序。新核心不导入旧实体、Gin 或具体存储；纯 `querycache`、`logevent` 与已迁统计值拥有独立职责规则。历史构造、HTTP 上下文和测试适配许可继续精确到文件/import，普通新文件不会继承许可；验收需要真实队列/事务/取消事件和查询次数证据，不能用仅编译或跳过替代。

upstream 的具体平台不能相互导入，也不接收旧 Account、Gin 或完整 config。共享 Google 认证原语位于 upstream/internal/googleauth，纯 wire/转换继续由 protocol 提供；账号授权会话及凭据持久化归 account。旧网关的参数投影与重试时机保留到入站迁移，测试需要分别验证 HTTP 提交、语义输出、可重试边界和已观测用量。平台迁移使用本地 HTTP/TLS/WS 及隔离存储夹具，不能把这些结果当作真实供应商账号验证。

notification、site、moderation、search 的核心、纯契约和 Adapter 按职责匹配现有 depguard。邮件凭据留 identity，阈值留 billing，通知接受已确定事件；审核跨身份事务沿用同一 SQL 连接；文件读取归 site/filesystem；搜索的 HTTP 与 Redis 分开。旧转接不得拥有第二份状态或算法，迁出文件的历史许可及原排除同时删除。角色夹具需覆盖新文件、精确历史 import、非法子包和迁出后的同名文件。

这些模块的行为验证包括真实 SMTP/TLS 夹具、页面文件边界、PostgreSQL 审核回滚、Redis 预占释放、配置交错及有界关闭。全量普通、unit、integration 命令串行执行；integration 使用 `-p=4` 限制包级容器压力，保留测试内部并发和断言。测试事件、跳过、原失败及后续通过分别保存，不能用数量相同代替诊断逐项比较。

gateway 的请求值与固定 `Execute` 契约位于 `gateway/execution`；该叶子不能反向导入根执行器、text 或 Adapter。HTTP 构造时由 app 先绑定唯一完成 Recorder，再构造执行器；请求调用只传显式状态和同步输出。内部账号循环、平台同账号恢复和协议转换仍各有一个实现，不能通过新增回调再包装整个旧 handler。暂存兼容 Adapter 只能提供单步调用与投影。

网关验证分别核对真实输出、HTTP 提交和重试窗口，不能只比最终字符串；失败可以伴随已观测用量，但完成资格按入口保留。对完成队列、模型快照、WS/Live 和规则发布运行定向 race，真实资金与 Redis 协议使用隔离存储。不同顶层测试若共享第三方全局测试设置，可分别执行并保留内部并发与断言，失败与补验日志必须同时保留；这不允许修改清单外历史问题。

删除迁出文件的专用 depguard 规则时，必须同步移除普通角色中的对应排除项，否则旧路径可能变成未被任何角色匹配的空洞。验证覆盖删除后恢复同名文件、旧文件新增禁止 import、同目录新文件及非法子包，不能只检查迁出后的正向 lint。

creative、batchimage 的核心、HTTP、PostgreSQL、Redis 与平台 Adapter 使用各自角色门禁。任务资金引用的 scope 只在 app 注册，所属存储参与者不得开启或提交自己的事务；真实 PostgreSQL 测试必须覆盖投影失败整体回滚和旧请求 ID 重放。平台及 GCS 测试使用本地夹具，不能用真实收费生成替代回归。成功元数据、输出保存和资金效果分别核验，输出保存失败不能重新调用供应商；临时 Redis 故障也不能直接断言结果永久丢失。

所有手写代码都要写必要注释，注释使用中文；生成文件不手改。注释应解释约束、失败语义或非显然原因，不复述语句。跨模块不变量应同步到 Project Doc，并在关键手写入口添加唯一 `@project-doc` 锚点。

前端使用 Vue 3、TypeScript、Pinia、Vue Router、Vue I18n 和项目组件。修改界面时：

- 选择项使用 `frontend/src/components/common/Select.vue` 等自研选择框，不使用原生 `<select>`。
- 用户可见文案进入 `src/i18n/locales/`，中英文 key 保持同构。
- API 类型和调用放在 `src/api/`，跨页面状态进入 store/composable，避免在 view 复制协议。
- 修改依赖必须同步 `frontend/pnpm-lock.yaml`，CI 使用 frozen lockfile。

app 的旧图绑定许可精确到源文件与 import；legacybridge 已删除，其 import 由全局规则拒绝，原有文件许可与目录排除同步移除。repository 的 Wire 聚合已删除，原生存储、任务队列和上游客户端分别在 app 的 wireinject 集合中绑定；service/handler 的剩余聚合仍待清理。传输测试随 `gateway/provider/transport` 运行，旧 repository 测试许可不随路径迁移继承。setup 只有实际入口文件可以引用精简 bootstrap；模块仍禁止反向依赖 app。迁出文件恢复目标角色规则，新增同目录文件不得继承例外。验证要覆盖普通/unit/integration，以及 wireinject、embed 和 OS 文件选择，不能仅以 lint 没有报错推断规则命中。

协议哈希和 Gemini 迭代器的 `crypto/sha256`、`iter` 许可只匹配实际文件；clientmeta 的版本库、app 定价装配及目录 HTTP 契约测试也按文件许可。pricing、capability、clientmeta 使用明确标准库集合，不能增加文件/网络读取。纯规则的测试应直接传入值；平台选择、HTTP 失败/取消和目录热更新还要验证旧消费者。管理员目录的完整 JSON、24 项顺序及 TypeScript 类型由 app 组合测试对照前端 fixture，不能通过修改夹具掩盖输出差异。

scheduler 的核心、HTTP、Redis、PostgreSQL 分别使用角色门禁。评分、排序、会话和等待不接收旧实体、Gin 或存储客户端；必要的纯 AccountSnapshot/RoutePlan、singleflight 和散列依赖按实际文件精确许可。旧 `sched:v2` 完整账号 codec 暂留唯一兼容 Adapter，迁出的测试继续用原报文验证。确认取得/故障放行、重复释放、锁过期继任及运行时取消必须验证实际资源数量，不能只检查返回码。

## 生成代码与迁移

`backend/ent/` 大部分文件由 Ent 生成，`backend/internal/app/wire_gen.go` 由 Wire 生成。统一使用：

```bash
make -C backend generate
```

该目标依次执行 `go generate ./ent` 和 `go generate ./cmd/server`。纯 Wire 装配变更只运行 `(cd backend && go generate ./cmd/server)`；原入口委托 app 的生成位置，构建版本仍通过 `-X main.Version` 等变量注入。修改 `backend/ent/schema/`、生成 feature 或 Wire provider 后，提交对应生成差异，并检查差异只包含预期 schema/依赖变化。Go 1.27 的 jsonv2 生成代码可能把 Ent 的 JSON 字段表示为 `encoding/json/jsontext.Value`，这是预期的生成结果。

Ent schema 不是生产迁移器。数据库权威变更仍须新增 `backend/migrations/*.sql`，不能依赖 Ent auto-migrate，也不能修改既有迁移。编号、`_notx.sql`、checksum 和 fork 上游重编号规则见 [部署与数据库迁移](deployment_and_migrations.md)。

## 验证策略

验证范围随风险扩大，先运行受影响包/组件，再运行仓库门禁。后端常用命令：

```bash
# 受影响包
(cd backend && GOTOOLCHAIN=go1.27.0 go test ./internal/account/... ./internal/routing/... ./internal/egress/...)

# 与 CI 一致的测试分层
make -C backend test-unit
make -C backend test-integration

# 普通测试加 lint
make -C backend test
```

外部 E2E 测试位于 `backend/tests/integration`；`make -C backend test-e2e` 与 `test-e2e-local` 使用同一 Go 测试入口，继续读取原服务地址和测试凭据环境变量。未配置服务和供应商凭据时，只能报告测试入选或编译结果，不能据此声称行为通过。

该目录也承接跨模块装配契约，具体执行集合由文件的构建标签决定。`tests/integration/pricing_contract` 保存渠道/市场价卡、Key快照、完成处理和资金分配的跨模块合同，继续使用 unit 标签；纯账号统计匹配与计算测试位于 billing/pricing。测试直接调用原生模块与既有存储替身，目录名称不表示已运行真实数据库。身份注册/邮箱绑定使用原生 identity 与 PostgreSQL Adapter 在 SQLite 夹具下验证既有规则，批量任务运行时使用原生 batchimage 与 miniredis；这些 `unit` 测试不能代替真实 PostgreSQL/Redis 的事务和竞争证据。

用量 HTTP、仪表盘和 DTO 契约测试直接构造 usage 与消费者侧查询投影，不通过旧 service 或完整设置服务装配。日期测试显式指定 Calendar，分别覆盖用户时区回退、DST 与各入口的结束边界；清理任务的存储缺失错误由 PostgreSQL Adapter 测试核对后，再以相同错误链输入 HTTP 夹具。

团队所有权的两次有序 SQL 更新由 team/postgres 的同包测试直接验证；不为旧测试包装保留导出函数。sqlmock 夹具检查关闭错误时须同时登记关闭预期，不能把测试资源清理误判为业务 SQL 失败。

重构阶段分别串行运行普通、unit 和 integration 全量测试，避免多个 Ent schema loader 会话争用临时目录；用 go list 与 JSON 事件核对实际标签、OS 文件和测试执行。依赖门禁仍统一使用 `.golangci.yml` 的 depguard，旧耦合只按实际文件/import 登记；新文件不能继承历史许可。验证结果中的跳过与仅编译不算行为通过。

集成测试可能启动 PostgreSQL/Redis 容器；环境没有 Docker 时要明确报告未运行，不能用单元测试结果代替。涉及迁移时还要运行 migration runner 和对应 schema/data regression tests。

前端门禁：

```bash
# CI 使用 lint、类型检查和关键 Vitest 集
make test-frontend

# 变更涉及其它组件时运行其测试或完整套件
npx --yes pnpm@9 --dir frontend run test:run
npx --yes pnpm@9 --dir frontend run build
```

部署文件变更要运行 `.github/workflows/backend-ci.yml` 中对应的 shell/Compose 检查；依赖或安全边界变更还应运行 `make secret-scan`、`govulncheck` 或相应审计。最终至少执行 `git diff --check`，并确认没有意外生成物、环境文件或秘密。

## 提交与文档

提交信息遵循 Conventional Commits，例如 `feat(gateway): ...`、`fix(billing): ...`、`docs(project): ...`。一次提交应围绕一个可验证目的，生成文件、迁移和契约测试与其源变更一起提交。

`SYNC.md` 是本地同步进度，受 `.gitignore` 保护，永远不要提交。使用 Codex 计划模式时，实施前按项目指令保存完整计划；本次后端包重构按用户约定统一使用 `refactor/`，其他任务仍遵循 `AGENTS.md`。不要覆盖工作区中来源不明的修改；提交前按文件核对 staging 范围。

每次代码变更都依据 [工程文档目录](../index.md) 判断相关专题；已读取且仍保留足够内容的索引和章节按 `project-doc` 的“读取与上下文复用”规则复用，无须在每次改文件或收尾前重读。如果持久架构、领域不变量、外部契约或运维流程变化，同步正文、分类目录和代码锚点；局部实现细节不应无条件扩写成新文档。README 保持项目入口简洁，工程细节放入 `docs/`。

## 同步上游

同步以 upstream PR/commit 为最小可审查单元，逐项理解变更并保留 fork 的产品、计费、安全和部署语义。冲突解决后运行该项涉及的测试，再形成符合 Conventional Commits 的本地提交；`SYNC.md` 只记录本地进度，不进入提交。

两个 fork 专属规则不可省略：

1. 上游新增 `backend/migrations/` 文件时，按上游顺序把前缀重编号为本 fork 当前最大迁移 ID 依次加一，并修复所有精确文件名引用；不能原名照搬。
2. 上游在 `README.md` 新增的工程文档不直接并入 README；把内容归入 `docs/` 的合适 Project Doc 或相关用户手册，没有合适位置时再创建规范命名的新文档。

同步后检查 `git diff upstream/...` 不足以证明行为正确，还要核对本 fork 的迁移顺序、默认配置、i18n、生成文件、部署样例、文档链接和安全边界。已经存在的 fork 修改不能为减少冲突而静默回退。

## 发布

`.github/workflows/release.yml` 由 `v*` tag 或手动 dispatch 触发。标准发布只构建一次前端，再把 Linux、Windows 和 macOS 的五个 Go 目标分配到独立 runner 并行编译；最终 job 通过 `tools/goreleaser_prebuilt.sh` 把这些二进制导入 GoReleaser，统一生成 Release 归档、校验和、双架构镜像与 manifest。每个镜像架构只执行一次构建，并同时附加 GHCR 与可选 DockerHub 标签；未配置 DockerHub 时不会创建占位镜像。simple release 跳过二进制 matrix，只构建精简镜像集合。workflow 从 annotated tag body 读取 release notes，并在成功后把 `backend/cmd/server/VERSION` 同步回默认分支。

发布前确保目标提交已推送、CI 通过、数据库迁移可滚动升级且备份已验证。发布后检查 Release、镜像、二进制、VERSION 回写和部署 smoke test；tag 只标识代码版本，不替代迁移/恢复检查。

相关文档：[项目总览](../project_overview.md)、[系统架构](../architecture/system_architecture.md)、[配置边界](../interfaces/configuration.md)、[部署与数据库迁移](deployment_and_migrations.md)、[运维目录](index.md)。

推广与支付的依赖门禁已覆盖新核心、HTTP、PostgreSQL 和 app。原 payment 根包 Ent/config/Wire 许可已删除；billing 值、套餐 HTTP 复用和微信身份辅助分别按实际文件/import 许可，新文件与迁出文件不继承许可。资金验证使用真实 PostgreSQL，分别检查 Promo、返利转入、订单履约及退款短事务；退款渠道使用本地夹具，不进行真实付款/退款。回退新退款代码前保留并核实 `REFUND_PREPARED` 事实，不能仅替换二进制后重发渠道退款。

备份/维护验证只对隔离 PostgreSQL、本地 S3/HTTP 夹具及临时可执行文件操作。恢复至少覆盖真实成功提交、SQL 失败回滚、输入中断及取消；系统锁覆盖同业务 ID 的不同认领代次。二进制替换测试不得使用测试进程或部署实例的真实路径。初始化与两个维护命令应验证退出前释放连接，并保持 Wire 可重复生成、Ent 与已发布 SQL 不变。
