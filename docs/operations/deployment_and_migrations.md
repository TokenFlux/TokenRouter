# 部署与数据库迁移

本文记录 TokenRouter 的构建产物、运行形态、初始化、数据库迁移、升级恢复与 MCP 部署约束。安装步骤见部署手册，各版本迁移准备与回退步骤见[版本升级说明](upgrade_notes.md)。

## 章节导航

- [构建与运行形态](#构建与运行形态)：修改产物或部署拓扑时读取。
- [产品名称与升级兼容](#product_name_compatibility)：从旧命名部署升级时读取。
- [初始化与启动](#初始化与启动)：修改 setup、配置或健康检查时读取。
- [迁移执行](#migration_execution)：修改 runner 或迁移格式时读取。
- [新增与同步迁移](#新增与同步迁移)：新建本 fork 迁移或同步上游时读取。
- [升级与恢复](#升级与恢复)：修改更新、备份或回退流程时读取。
- [MCP 管理接入](#mcp-管理接入)：部署或升级 MCP 管理与 OAuth 接入时读取。

## 构建与运行形态

项目按单后端进程加单前端实例部署，前后端同版本整体升级；独立静态服务器托管前端时同样须与后端同步切换。

- 构建流水线：标准发布只构建一次前端静态资源，再并行交叉编译各平台 Go 二进制（内嵌前端），最后统一归档并由 Linux `amd64`/`arm64` 产物组装多架构镜像。发布矩阵见 [release workflow](../../.github/workflows/release.yml)。
- 前端与 Go 版本：源码镜像前端阶段校验 `.node-version`（默认 `26.12.1`，覆盖 `NODE_IMAGE` 仍须通过版本检查），经 npm 按 `packageManager` 安装 pnpm 并在冻结安装前复制 workspace 配置。Go 构建统一使用 `1.27.4`（后端、工具模块及 Dockerfile），标准库安全修复须重编部署。
- Dockerfile 分工：根 Dockerfile 供开发 Compose 和 `deploy/build_image.sh` 使用，前端阶段复制 `backend/internal/pkg/locale/manifest.json` 与 `error_messages.json` 到 `/app/backend/internal/pkg/locale/`；`backend/Dockerfile` 仅编后端，`Dockerfile.goreleaser` 封装预编译二进制。
- 部署资产筛选：`.goreleaser.yaml` 显式列出归档部署资产（不含开发 Compose、构建脚本、入口脚本或夹具），安装器复制归档内非隐藏部署文件。镜像构建上下文下的 `deploy/` 仅保留 `docker-entrypoint.sh`，详见[部署文件目录](../../deploy/README.md)。

| 方式 | 入口 | 依赖与说明 |
| --- | --- | --- |
| 单二进制、systemd | `deploy/install.sh` | 外部 PostgreSQL 和 Redis；安装器管理二进制版本与服务 |
| 完整 Compose | `deploy/docker-compose.yml`、`docker-compose.local.yml` | 应用、PostgreSQL、Redis；分别使用命名卷或本地目录 |
| 独立应用容器 | `deploy/docker-compose.standalone.yml` | PostgreSQL 和 Redis 由外部提供 |
| 源码开发 Compose | `deploy/docker-compose.dev.yml` | 本地构建应用并启动配套依赖 |
| Apple Container | `deploy/apple-container.sh` | 独立脚本管理容器、卷和健康状态 |

- 依赖与数据目录：应用依赖 PostgreSQL 和 Redis。`/app/data`（`DATA_DIR`）存配置、安装锁与本地运维产物；数据库、Redis 与对象存储生命周期独立，仅备份数据目录不算完成系统备份。
- 部署手册：逐步操作见[中文部署指南](../guides/deployment/index.md)、[Docker 镜像说明](../../deploy/DOCKER.md)与 [Apple Container 指南](../guides/deployment/apple_container.md)。
- Compose 健康检查：所有 Compose 变体的应用健康检查均设 120 秒启动宽限期；内置 PostgreSQL 的三个变体以 `pg_isready` 加 SQL 探活，设 60 秒宽限期和 18 次重试（部署测试防漂移）。
- 旧接口停用：旧 data management 接口已下线，`backup` 兼容入口固定返回 `DATA_MANAGEMENT_DEPRECATED`（不连 Unix Socket 或 gRPC），旧脚本与 systemd 模板已移除；遗留识别见 [datamanagementd 停用说明](../guides/deployment/datamanagementd.md)。

<a id="product_name_compatibility"></a>
## 产品名称与升级兼容

| 领域 | 新部署默认行为 | 旧命名（`sub2api`）兼容规则 |
| --- | --- | --- |
| 目录、服务与镜像 | 产物、目录、systemd 服务、容器用户与 Compose 服务使用 `tokenrouter` | 镜像保留 `/app/sub2api` 软链，UID/GID 保持 `1000`；安装器检测到 `/opt/sub2api` 及旧 unit 时原地复用，新旧共存则停止 |
| 归档与在线更新 | 归档 `tokenrouter_<版本>_<系统>_<架构>` 含 `tokenrouter` 并写入 `checksums.txt` | 当前更新器优先 `tokenrouter_`，兼容历史 `sub2api_` 并原地替换当前可执行路径；仅识 `sub2api_` 的旧更新器需手动升级 |
| 价格补充与离线目录 | 内嵌于二进制，随版本自动切换，安装器不写外部补充文件 | `model_catalog.supplement_file` 默认为空；手动文件优先于内嵌，旧打包路径按[配置兼容规则](../interfaces/configuration.md#configuration_sources)处理 |
| Compose 升级 | 新模板空卷变量按项目前缀生成新卷 | 旧部署仅升镜像即可；切新模板前记录实际卷名并设 `TOKENROUTER_DATA_VOLUME`、`TOKENROUTER_POSTGRES_VOLUME`、`TOKENROUTER_REDIS_VOLUME`，保留库名/用户/密钥，停旧栈再启新栈（禁 `down -v`） |
| Apple Container | 新栈标记 `org.tokenrouter.stack` | `sub2api-apple*` 按 `org.sub2api.stack` 核对归属原地复用，混用或归属不符即停；新旧脚本共用互斥锁；`TOKENROUTER_ENV_FILE` 与 `APPLE_CONTAINER_TOKENROUTER_IMAGE` 兼容旧变量 |
| 数据库与品牌名 | 历史 SQL、校验和、库名、卷与安装锁不改名 | 迁移 283 仅更新恰好等于旧品牌名的展示设置；回退二进制不自动改回旧品牌名 |

## 初始化与启动

进程启动按以下顺序推进：

1. 处理离线 `-migrate-config` 命令。
2. 按统一查找规则判断是否需要初始化。未安装时通过 Web setup、`--setup` 或容器 `AUTO_SETUP` 依次测试 PostgreSQL 和 Redis、执行迁移、创建首个管理员、写配置并生成只读安装锁（防止重复初始化，常规配置错误不可通过删除安装锁修复）。
3. 正常启动先按[配置版本规则](../interfaces/configuration.md#config_versioning)备份并升级旧配置，再检查未知键、展开引用并校验。只读挂载须离线迁移，回退旧二进制前同步恢复旧配置，见[配置文件版本 2](upgrade_notes.md#config_version_2)。
4. 在装配业务依赖并监听 HTTP 之前执行内嵌迁移与安全密钥初始化；任一步失败即中止启动，不带不完整 schema 提供服务。升级须先停旧后端再由目标版本完成迁移。

`GET /health` 仅证明进程可响应 HTTP，不代替升级后的业务抽样、账本核对与后台任务检查。

<a id="migration_execution"></a>
## 迁移执行

`backend/migrations/*.sql` 通过 `go:embed` 编入二进制，文件名即迁移身份，按字典序串行执行（通过固定 PostgreSQL advisory lock 互斥）。`schema_migrations` 记录文件名、去首尾空白后的 SHA-256 与执行时间；已有文件校验和不匹配时启动失败，仅 runner 显式列出的历史兼容文件放行。

| 模式 | 文件命名 | 执行与约束 |
| --- | --- | --- |
| 事务迁移 | `<编号>_<描述>.sql` | 单事务执行，SQL 与 `schema_migrations` 记录一起提交或回滚 |
| 非事务迁移 | `<编号>_<描述>_notx.sql` | 事务外逐条执行，仅允许带 `IF NOT EXISTS`/`IF EXISTS` 的 `CREATE/DROP INDEX CONCURRENTLY`，禁止 `BEGIN`/`COMMIT`/`ROLLBACK`；每条语句须可安全重跑 |

- Atlas 兼容基线：首次检测到旧 `schema_migrations` 且缺少 Atlas 记录时，以当前最新迁移建立 `atlas_schema_revisions` 基线；SQL 文件仍是唯一 schema 来源。
- 前向约束：已进入任何环境的迁移禁止修改、删除或改名，修正一律新增迁移。不放可执行 Down 段（runner 不解析 Goose Up/Down 标记）。普通迁移尽量幂等，并用英文注释说明变更原因与兼容窗口。schema、回填、查询与 Ent schema 改动须纳入同一次升级，特殊停机与回退限制写入[版本升级说明](upgrade_notes.md)。

## 新增与同步迁移

- 命名规则：新建文件前扫描 `backend/migrations/` 取最大数字前缀加一（不效仿历史重复编号或字母后缀），命名 `<递增数字>_<snake_case 描述>.sql` 或 `*_notx.sql`，并确认字典序位置正确。
- 上游同步：同步上游新增迁移时不可照搬原名，须按上游提交顺序改写为本 fork 最大编号依次加一，并同步更新测试、runner 特例、文档及所有文件名引用；fork 已有迁移保持原名不重排。
- 必做验证：
  1. runner 单元测试（事务模式、`_notx` 校验、锁与 checksum）；
  2. 受影响的 schema 与数据迁移测试；
  3. 空库完整执行与已有 schema 重复执行；
  4. 核对 `schema_migrations` 文件名及已有文件 checksum 未变化。

## 升级与恢复

### 通用规则

1. 升级流程：在维护窗口停止新流量 -> 按优雅关闭流程停旧后端并确认在途请求与后台任务结束 -> 同步替换同版本前后端（嵌入式发布直接替换二进制或镜像） -> 启动新后端完成迁移与初始化 -> 验证通过后恢复流量。
2. 升级前备份：创建并验证可恢复的 PostgreSQL 备份，同时保留必要的 Redis 与对象存储数据。默认备份策略可能排除大历史表，恢复前须核对备份内容范围；敏感存储配置依赖稳定加密密钥。
3. 回退边界：在线更新和安装脚本仅能回退应用二进制或镜像，不会撤销数据库迁移。上线前确认迁移向后兼容；schema 不兼容时须恢复演练过的数据库备份或发布前向修复，禁止手工删 `schema_migrations` 记录。
4. 升级后验收：核对 `/health`、登录与 API Key 鉴权、非流式与流式网关请求、用量结算、关键后台任务和迁移表；验收完成前保留旧产物与升级前备份。各次迁移专项要求见[版本升级说明](upgrade_notes.md)。

<a id="maintenance_execution"></a>
### 备份与维护执行

备份编排（配置、记录、定时、恢复）归 `backup`，`pg_dump`/`psql`、压缩分卷和本地/S3 传输归 `backup/provider`；系统更新与回退归 `ops/maintenance`，在可执行文件目录下载替换。

- 更新临界区：浏览器断开不中止更新；应用退出时取消下载等准备阶段；一旦进入二进制替换临界区，必须完成替换或恢复原文件。
- 备份停止语义：立即拒绝新任务，取消启动恢复、cron 与在途任务，在应用剩余后台预算内等待子进程与清理完成；超时保留未完成状态，不算排空成功。
- 恢复安全性：使用开启 `ON_ERROR_STOP` 的单事务 `psql`；输入流损坏时先取消进程再关 stdin，防止截断 EOF 被当成提交。异步恢复仅在 `running` 记录保存成功后启动；若数据库已提交但最终状态记录写入失败，数据库变更不回滚，不可误判恢复未发生。
- 维护锁机制：基于幂等表的 `scope` 与 `key`，`processing` 记录在 `response_body` 存放独立所有者令牌，续租与释放同时比对业务操作 ID 与令牌。旧的无令牌记录在锁窗口内仍阻断认领，过期后可接管；普通 HTTP 幂等记录不走该路径。回退旧二进制前须停止并等待维护操作完成，避免在恢复或替换中途切换实现。

## MCP 管理接入

迁移 300 创建 MCP 令牌表（保存认证摘要与加密凭据），迁移 301 增加默认关闭的跳过浏览器确认设置。创建与复制令牌要求固定 `security.secret_encryption_key`，重启和升级保持该值并与数据库同备份。

MCP 入口复用网页监听端口，反向代理须放行 `/mcp`。浏览器确认链接使用 `frontend_url`，原生客户端以专用 Bearer 令牌接入，部署时按需限制网络可达性。令牌迁移与回退规则见 [MCP 管理接口](../interfaces/mcp_management.md#client_setup)。

### MCP OAuth 部署

- 迁移与启用：迁移 302 创建 OAuth 客户端、应用授权、短期请求与凭据表。单实例同步升级前后端，已配有效 `frontend_url` 即可直接使用（无需新环境变量）；缺失或无效时在系统设置补全即可生效，无需重启，手工 Token 表保持原结构。
- 统一公开入口：`frontend_url` 对应入口必须将 `/mcp`、`/mcp/oauth/*` 和 `/.well-known/oauth-*` 转发至后端，并提供 `/admin/mcp/authorize` 页面（前后端物理分离时同样统一入口）。嵌入式构建自动保留这些路径；跨域浏览器接入按 CORS 放行 Origin 并暴露 `WWW-Authenticate`。
- 密钥与备份：备份涵盖 OAuth 表及 `security_secrets` 中独立的 `mcp_oauth_secret`（重启持久保留；固定 TOTP 密钥缺省时 MCP 临时文件加密密钥从该值派生，已设 TOTP 密钥则沿用原加密）。
- 变更与回退：变更前端 origin 后旧凭据失效须重连；回退至不支持 OAuth 版本保留新增表。ChatGPT 联调需要公网 HTTPS 部署，不能以本地协议夹具替代。

相关文档：[系统架构](../architecture/system_architecture.md)、[配置](../interfaces/configuration.md)、[版本升级说明](upgrade_notes.md)、[运维目录](index.md)。
