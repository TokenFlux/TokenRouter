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

现有 handler/server 仍调用 service，repository 实现 service 定义的接口；通用技术实现已分布在 `internal/infra`，HTTP 工具在 `server/httpx`、`server/clientip`，纯工具在明确列出的 pkg 包中。旧目录里的兼容入口不代表其中所有能力仍拥有独立实现。

`.golangci.yml` 保留普通 handler/service 对 repository、Redis、GORM 的原有限制，并按职责约束新业务核心、纯叶子契约、protocol、upstream、infra 和具体 Adapter。核心不依赖旧业务或框架/存储实现，HTTP Adapter 不直接访问数据库；具体上游不能依赖其他平台实现，技术包不反向读取完整 config 或业务 service。规则同时匹配目录直属文件和嵌套文件；新增的未分类路径也有默认约束。

保留路径的旧依赖按准确源文件和 import 登记，许可不覆盖同目录新文件，也不覆盖允许包的其他子包。每次添加路径或修改规则，要分别用普通、unit、integration 集合验证合法依赖与违规夹具；已有失败不能自动转成白名单。角色检查不能识别“通过接口绕过业务用例”或“借现有 import 增加耦合”，这两项仍需代码审查。

所有手写代码都要写必要注释，注释使用中文；生成文件不手改。注释应解释约束、失败语义或非显然原因，不复述语句。跨模块不变量应同步到 Project Doc，并在关键手写入口添加唯一 `@project-doc` 锚点。

前端使用 Vue 3、TypeScript、Pinia、Vue Router、Vue I18n 和项目组件。修改界面时：

- 选择项使用 `frontend/src/components/common/Select.vue` 等自研选择框，不使用原生 `<select>`。
- 用户可见文案进入 `src/i18n/locales/`，中英文 key 保持同构。
- API 类型和调用放在 `src/api/`，跨页面状态进入 store/composable，避免在 view 复制协议。
- 修改依赖必须同步 `frontend/pnpm-lock.yaml`，CI 使用 frozen lockfile。

## 生成代码与迁移

`backend/ent/` 大部分文件由 Ent 生成，`backend/cmd/server/wire_gen.go` 由 Wire 生成。统一使用：

```bash
make -C backend generate
```

该目标依次执行 `go generate ./ent` 和 `go generate ./cmd/server`。修改 `backend/ent/schema/`、生成 feature 或 Wire provider 后，提交对应生成差异，并检查差异只包含预期 schema/依赖变化。Go 1.27 的 jsonv2 生成代码可能把 Ent 的 JSON 字段表示为 `encoding/json/jsontext.Value`，这是预期的生成结果。

Ent schema 不是生产迁移器。数据库权威变更仍须新增 `backend/migrations/*.sql`，不能依赖 Ent auto-migrate，也不能修改既有迁移。编号、`_notx.sql`、checksum 和 fork 上游重编号规则见 [部署与数据库迁移](deployment_and_migrations.md)。

## 验证策略

验证范围随风险扩大，先运行受影响包/组件，再运行仓库门禁。后端常用命令：

```bash
# 受影响包
(cd backend && go test ./internal/service ./internal/handler)

# 与 CI 一致的测试分层
make -C backend test-unit
make -C backend test-integration

# 普通测试加 lint
make -C backend test
```

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
