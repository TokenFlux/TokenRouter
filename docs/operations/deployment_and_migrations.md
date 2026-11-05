# 部署与数据库迁移

本文记录 TokenRouter 的构建产物、运行方式、首次初始化、数据库迁移、升级和恢复的工程约束。安装命令见部署手册；本文用于核对启动、迁移和恢复的兼容要求。各次迁移的升级步骤见[版本升级说明](upgrade_notes.md)。

## 章节导航

- [构建与运行形态](#构建与运行形态)：修改产物或部署拓扑时读取。
- [产品名称与升级兼容](#product_name_compatibility)：从旧命名的部署升级时读取。
- [初始化与启动](#初始化与启动)：修改 setup、配置或健康检查时读取。
- [迁移执行](#migration_execution)：修改 runner 或迁移格式时读取。
- [新增与同步迁移](#新增与同步迁移)：新建本 fork 的迁移或同步上游时读取。
- [升级与恢复](#升级与恢复)：修改更新、备份或回退流程时读取。

## 构建与运行形态

项目按一个后端进程和一个前端实例部署，前后端作为同一版本整体升级。使用独立静态服务器交付前端时，也需要与后端同步切换版本。

标准发布先生成一次前端静态资源，再由独立的 runner 把同一份前端嵌进各平台的 Go 二进制，并行交叉编译；最后的发布阶段统一归档二进制，并用 Linux `amd64` 和 `arm64` 的产物组装多架构镜像和必要的运行时工具。GitHub Release 同时发布 Linux `amd64`、Linux `arm64` 等产物，具体的矩阵以 [release workflow](../../.github/workflows/release.yml) 为准。

源码镜像的前端阶段校验 `.node-version`，再按 `frontend/package.json` 的 `packageManager` 字段安装 pnpm，本地和 CI 读取同样的版本声明。默认 Node 镜像使用 26.12.1，覆盖 `NODE_IMAGE` 时仍需满足完整版本检查。Node 26 镜像通过 npm 安装声明的 pnpm 版本，冻结安装前复制 workspace 配置。

Go 构建使用 1.27.3，后端模块、两个开发工具模块、根目录和 `backend/` 的 Dockerfile 同步声明该版本。Go 标准库的安全修复需要重新编译并部署应用二进制才能生效。Node.js 用于依赖安装、前端构建和开发工具；标准生产镜像由 Go 进程提供嵌入的静态资源。

前端构建会导入 `backend/internal/pkg/locale/manifest.json` 和 `error_messages.json`。根目录的 Dockerfile 在前端构建阶段将这些文件复制到 `/app/backend/internal/pkg/locale/`，与源码中的相对导入路径一致。开发 Compose 和 `deploy/build_image.sh` 都使用这份完整应用构建入口；`backend/Dockerfile` 只构建后端，`Dockerfile.goreleaser` 只封装预编译的发布二进制。

`deploy/` 的运行入口和配置模板见[部署文件目录](../../deploy/README.md)。二进制归档在 `.goreleaser.yaml` 中逐项列出部署资产，不携带开发 Compose、构建辅助脚本、镜像入口脚本或测试夹具；安装器会将归档内的非隐藏部署文件复制到安装目录。镜像构建上下文中的 `deploy/` 只保留 `docker-entrypoint.sh`。

仓库支持以下运行方式：

| 方式 | 入口 | 依赖和说明 |
| --- | --- | --- |
| 单二进制、systemd | `deploy/install.sh` | 外部的 PostgreSQL 和 Redis；安装器管理二进制版本和服务 |
| 完整 Compose | `deploy/docker-compose.yml`、`docker-compose.local.yml` | 应用、PostgreSQL、Redis；分别使用命名卷或本地目录 |
| 独立应用容器 | `deploy/docker-compose.standalone.yml` | PostgreSQL 和 Redis 由部署环境提供 |
| 源码开发 Compose | `deploy/docker-compose.dev.yml` | 在本地构建应用，并启动配套的依赖 |
| Apple Container | `deploy/apple-container.sh` | 独立脚本管理容器、卷和健康状态 |

应用至少依赖 PostgreSQL 和 Redis。`/app/data` 或等价的 `DATA_DIR` 保存配置、安装锁和本地的运维产物；数据库、Redis 和对象存储各有自己的生命周期，只备份应用数据目录，不算完成了系统备份。

逐步操作见[中文部署指南](../guides/deployment/index.md)、[Docker 镜像说明](../../deploy/DOCKER.md)和 [Apple Container 指南](../guides/deployment/apple_container.md)。这些是给部署者的手册，本文的工程约束在它们之上。

所有 Compose 变体的应用健康检查都保留 120 秒启动宽限期。内置 PostgreSQL 的三个变体使用 `pg_isready` 加 SQL 查询确认就绪，并保留 60 秒宽限期和 18 次失败重试；部署测试比较这些公共健康检查，避免不同存储拓扑的启动策略漂移。

旧的 data management 接口已经下线：`backup` 的兼容入口固定返回 `DATA_MANAGEMENT_DEPRECATED`，不会连接 Unix Socket，也不会发起 gRPC 调用。旧安装脚本和 systemd 模板已移除，[datamanagementd 停用说明](../guides/deployment/datamanagementd.md)保留遗留部署的识别信息。备份和恢复使用独立的 backup 模块。

<a id="product_name_compatibility"></a>
## 产品名称与升级兼容

新的产物、安装目录、systemd 服务、容器用户和 Compose 服务都使用 `tokenrouter`。镜像保留 `/app/sub2api` 兼容链接，容器的 UID 和 GID 仍是 1000。安装器识别到 `/opt/sub2api`、旧配置和旧 unit 时，继续使用原来的目录、可执行文件路径、服务和用户；同时发现新旧两套安装时停止，以免覆盖另一套部署。

标准发布的归档命名为 `tokenrouter_<版本>_<系统>_<架构>`，包含 `tokenrouter` 可执行文件。CI 将矩阵编译的二进制导入 GoReleaser，所有归档都写进 `checksums.txt`。当前更新器优先使用 `tokenrouter_` 归档，并支持读取历史发布的 `sub2api_` 归档，替换的位置是当前实际的可执行文件路径。只识别 `sub2api_` 归档或包内 `sub2api` 文件的旧更新器需要手动升级。

官方价格补充和离线模型目录都内嵌在二进制里，后台在线更新、安装脚本、完整归档和 Docker 使用同一份默认数据。替换或回退二进制时，官方补充随版本切换，不需要额外安装资源文件。安装器不会创建或覆盖外部的价格补充。`model_catalog.supplement_file` 默认为空，手动配置的自定义文件照常读取，并优先于内嵌补充；旧的打包路径按[配置兼容规则](../interfaces/configuration.md#configuration_sources)处理。不再需要旧文件时，清空这个配置，文件本身由部署者管理。

已有的 Compose 部署保留原来的编排和 `.env`，只升级镜像即可。换用新模板时，先记下实际的应用、PostgreSQL 和 Redis 卷名，分别设置 `TOKENROUTER_DATA_VOLUME`、`TOKENROUTER_POSTGRES_VOLUME`、`TOKENROUTER_REDIS_VOLUME`；同时保留数据库名、用户和密钥，停止旧栈后再启动新模板，不要使用 `down -v`。新变量为空时，按 Compose 项目前缀生成新的卷名。

Apple container 的新栈使用 `org.tokenrouter.stack` 标签；已有的 `sub2api-apple*` 资源，继续按 `org.sub2api.stack` 标签核对归属并原地复用。两种资源同时出现，或者归属不符时，脚本停止。脚本和旧脚本共用同一把互斥锁，新旧脚本不会同时修改同一个栈。`TOKENROUTER_ENV_FILE` 和 `APPLE_CONTAINER_TOKENROUTER_IMAGE` 分别兼容对应的旧变量。

迁移 283 只修改恰好等于旧品牌名的展示设置。历史 SQL 文件和校验和不变，数据库、数据卷和安装锁都不改名。升级前照常按本页的要求备份；回退二进制不会把站点名称自动改回旧品牌。

## 初始化与启动

进程入口先处理离线 `-migrate-config` 命令，再按统一文件查找规则判断是否需要 setup。尚未安装时，可以使用 Web setup、`--setup` 命令行，或容器的 `AUTO_SETUP`。setup 依次测试 PostgreSQL 和 Redis、执行迁移、创建第一个管理员、写入配置，最后创建只读的安装锁。安装锁用来防止被人重新初始化，普通的配置问题不能靠删除它来修复。

正常启动先按[配置版本规则](../interfaces/configuration.md#config_versioning)备份并升级旧配置，再检查未知键、展开引用和校验。只读挂载使用离线迁移，恢复旧二进制前也要恢复旧配置，见[配置文件版本 2](upgrade_notes.md#config_version_2)。

正常启动时，`app/bootstrap` 在构造业务依赖图之前，执行同一套嵌入的迁移，所以每个新版本都在监听 HTTP 之前完成 schema 对齐。迁移或安全密钥初始化失败时，应用初始化失败，不会带着不完整的 schema 提供服务。升级先停止旧后端，再由目标版本完成迁移和初始化；各次迁移的额外准备、验证和恢复要求见[版本升级说明](upgrade_notes.md)。

`GET /health` 是容器的健康检查入口。健康响应只说明当前进程可以服务，升级后的业务抽样、账本核对和后台任务检查仍然要做。

<a id="migration_execution"></a>
## 迁移执行

迁移执行器在 `infra/postgres`，由 bootstrap 传入连接和迁移 FS；setup 和两个维护命令只调用需要的精简初始化能力。`backend/migrations/*.sql` 通过 `go:embed` 编进二进制，文件名就是迁移的身份，并按字典序决定执行顺序。执行器使用一个固定的 PostgreSQL advisory lock，让迁移调用串行执行；`schema_migrations` 记录文件名、去掉首尾空白后的 SHA-256 和执行时间。已有文件的校验和不匹配时，启动失败；只有 runner 里逐个列出的历史兼容文件可以放行。

普通的 `*.sql` 在单个事务里执行，SQL 和迁移记录一起提交或回滚。包含 `CREATE INDEX CONCURRENTLY` 或 `DROP INDEX CONCURRENTLY` 的文件，需要以 `_notx.sql` 结尾：这种模式在事务外逐条执行语句，只接受带 `IF NOT EXISTS` 或 `IF EXISTS` 的并发索引语句，也不允许出现 `BEGIN`、`COMMIT` 和 `ROLLBACK`。非事务迁移可能在 SQL 成功、但记录还没写入时中断，所以每条语句都要能安全地重复执行。

第一次检测到旧的 `schema_migrations`、但缺少 Atlas 记录时，runner 用当前最后一个迁移建立 `atlas_schema_revisions` 基线。这条兼容记录不影响 SQL 文件作为 schema 来源的地位。

迁移只向前执行，已发布的文件保持原样：

- 已经进入任何环境的文件，不能修改、删除或改名；修正需要新建迁移。
- 文件里不放可执行的 Down 段；runner 不解析 Goose 的 Up 和 Down 标记。
- 普通迁移尽量写成幂等的，并在 SQL 里用英文注释说明变更原因和兼容窗口。
- schema、数据回填、Repository 查询和 Ent schema 的变化，放在同一次整体升级里设计；在[版本升级说明](upgrade_notes.md)里写明特殊的停机准备、备份、验证和回退限制。

## 新增与同步迁移

新文件命名为 `<递增数字>_<snake_case 描述>.sql`；并发索引命名为 `<递增数字>_<描述>_notx.sql`。仓库历史上有重复的编号和字母后缀，这些不能当作复用编号的理由。每次新建之前，扫描 `backend/migrations/` 的数字前缀，取当前最大值加一，并确认按字典序排在预期的位置。

本 fork 同步上游时，上游在 `backend/migrations/` 新增的迁移不能按原名照搬：按上游的提交顺序，逐个把数字前缀改成本 fork 当前最大编号加一，同时更新测试、runner 特例、文档和其他引用了原文件名的地方。已经在 fork 里的迁移保持原名，不为了"整理顺序"重新编号。

迁移变更至少验证：

- runner 单元测试，包括事务模式、`_notx` 校验、锁和 checksum；
- 受影响的 schema 和数据迁移测试；
- 从空数据库完整执行一遍，以及在已有的 schema 上重复执行；
- `schema_migrations` 里的文件名符合预期，已有文件的 checksum 没有变。

## 升级与恢复

### 通用规则

升级在维护窗口内整体切换前后端：停止接收新流量，按应用关闭流程停止旧后端，确认在途请求和后台任务已结束，替换同一版本的前端资源与后端产物，再启动后端执行迁移和初始化。验证通过后恢复流量。嵌入式发布通过替换二进制或镜像完成前后端切换。

升级之前，先创建 PostgreSQL 备份并实际验证可以恢复，同时保存业务需要恢复的 Redis 和对象存储数据。后台备份服务可以把数据库 dump 流式写到本地或 S3 兼容存储，并用维护锁让备份和恢复串行执行；敏感的存储配置需要稳定的安全密钥。备份内容策略可能排除大的历史表，恢复之前要先核对备份的范围。

在线更新和安装脚本可以保留上一版的二进制或镜像，但这只能回退应用。镜像回退不会撤销数据库迁移；上线前要确认新迁移对旧版本是否向后兼容。schema 已经不兼容时，使用演练过的数据库备份恢复，或者新增一个前向修复的迁移；手工删除 `schema_migrations` 的记录不是回滚的办法。

升级完成后，至少检查：`/health`、登录和 API Key 鉴权、一个非流式和一个流式的网关请求、用量结算、关键的后台任务和迁移表。这些检查完成之前，保留旧产物和升级前的备份。

各次迁移的停机要求、缓存版本变化和回退方式，见[版本升级说明](upgrade_notes.md)。

<a id="maintenance_execution"></a>
### 备份与维护执行

备份配置、记录、定时和恢复的编排由 backup 负责，`backup/provider` 管理 dump 和 psql、压缩分卷、本地文件和 S3。系统更新和回退由 `ops/maintenance` 编排，技术适配层在可执行文件所在的目录下载并替换；版本查询复用 Ops 的发布查询实例（只有一个）。浏览器断开后，更新仍然继续；应用退出时，取消下载等准备工作；一旦进入二进制替换的临界区，就要么完成替换，要么恢复原文件。

备份停止时，立即拒绝新任务，取消启动回源、cron 和在途的任务，然后在应用剩余的后台预算内，等待子进程和清理完成。超时时保留未完成的状态，不算排空成功。恢复使用开启了 `ON_ERROR_STOP` 的单事务 psql；输入流损坏时，先取消进程再关闭标准输入，不完整输入的 EOF 不会被当成提交的条件。异步恢复只有在成功保存 running 记录之后才会启动。数据库最终提交之后，如果状态保存失败，数据库并没有回滚，不能据此认为恢复没有发生。

系统维护锁使用幂等表和 scope、key；processing 记录的 response_body 里保存一个独立的所有者令牌，续租和释放时，同时比较业务操作 ID 和令牌。旧的、没有令牌的记录，在原来的锁窗口内继续阻止认领，过期后可以被接管；普通的 HTTP 幂等记录不使用这套维护认领接口。回退旧二进制之前，先停止并等待维护操作完成，数据库恢复或二进制替换进行到一半时，不要切换实现。

相关文档：[系统架构](../architecture/system_architecture.md)、[配置](../interfaces/configuration.md)、[版本升级说明](upgrade_notes.md)、[运维目录](index.md)。

## MCP 管理接入

迁移 300 创建独立的 MCP 令牌表，保存认证摘要和加密凭据，迁移 301 增加默认关闭的跳过浏览器确认设置。服务沿用现有 PostgreSQL、Redis 和持久加密密钥；创建与复制令牌要求配置固定的 `security.secret_encryption_key`，重启和升级时保持该值，并与数据库一起备份。入口与网页使用同一监听端口，反向代理需要允许 `/mcp`。浏览器确认链接使用 `frontend_url`，原生客户端通过专用 Bearer 令牌连接，部署时按需限制管理入口的网络可达范围。令牌迁移、回退和临时数据规则见 [MCP 管理接口](../interfaces/mcp_management.md#client_setup)。

### MCP OAuth 部署

迁移 302 创建 OAuth 客户端、应用授权、短期请求和凭据表。单实例部署同步升级前后端；已有有效 `frontend_url` 的部署升级后即可使用 OAuth，无需新增环境变量。地址缺失或无效时，在原系统设置中补全前端地址即可，无需重启。MCP 接入配置显示当前地址或需要修正的原因，手工 Token 表及接入方式保持原结构。

`frontend_url` 对应的公开入口必须将 `/mcp`、`/mcp/oauth/*` 和 `/.well-known/oauth-*` 转发到后端，并提供 `/admin/mcp/authorize` 网页。即使前后端物理分开部署，也使用这个统一公开入口。嵌入前端的发行构建保留这些后端路径；跨域浏览器接入按 CORS 配置放行 Origin，并暴露 `WWW-Authenticate`。

备份包含 OAuth 表及 `security_secrets` 内独立的 `mcp_oauth_secret`。该密钥在后端重启后继续使用；固定 TOTP 加密密钥缺省时，MCP 临时文件加密密钥从该持久值独立派生。固定 TOTP 密钥已配置时沿用原文件加密方式。变更前端 origin 后，旧地址的 OAuth 凭据被拒绝，客户端需要重新连接。回退到不支持 OAuth 的版本时保留新增表。ChatGPT 联调需要可公开访问的 HTTPS 部署，不能由本地协议夹具测试代替。
