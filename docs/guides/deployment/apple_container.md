# Apple container 部署指南

TokenRouter 可以通过 Apple 的 `container` CLI 运行原生三服务栈。该方式直接运行已发布的 TokenRouter、PostgreSQL 和 Redis OCI 镜像，不需要 Docker Desktop 或兼容 Docker 的守护进程。

其他部署方式见 [部署指南](index.md)，工程约束见 [部署与数据库迁移 Project Doc](../../operations/deployment_and_migrations.md)。

## 支持范围

Apple `container` 适合本地开发以及由运维人员直接管理的 Mac 部署，生产环境仍推荐使用 Docker Compose。

Apple `container` 1.1 不提供重启策略、自动启动、工作负载健康调度、Docker API Socket 或完整的 Compose 编排。`apple-container.sh` 会在每次调用时按顺序启动服务并执行就绪检查，但它不是持续运行的监督进程。

## 环境要求

- Apple 芯片 Mac
- macOS 26 或更高版本
- Apple `container` 1.1.0 或更高版本
- 用于生成初始密钥的 `openssl`
- 首次启动已发布容器时，允许 `container-runtime-linux` 访问本地网络

从 Apple `container` 的[官方发布页](https://github.com/apple/container/releases)安装后验证版本：

```bash
container --version
```

## 快速开始

```bash
git clone https://github.com/TokenFlux/TokenRouter.git
cd TokenRouter/deploy

# 创建 .env，并随机生成 PostgreSQL、JWT 和 TOTP 密钥。
./apple-container.sh init

# 启动前检查可选设置。
nano .env

# 创建卷、网络和容器，等待依赖就绪后启动 TokenRouter。
./apple-container.sh up

# 检查 PostgreSQL、Redis 和应用端点。
./apple-container.sh status
```

打开 `http://localhost:8080`。如果 `ADMIN_PASSWORD` 为空，可从日志中获取自动生成的密码：

```bash
./apple-container.sh logs app
```

环境文件使用字面量 `KEY=value` 语法。不要使用 `${VALUE:-default}` 等 Compose 表达式，也不要给值加引号，除非引号本身就是值的一部分。`BIND_HOST` 需要是 IPv4 地址，`SERVER_PORT` 需要在 1025 到 65535 之间。

## 常用命令

```bash
# 启动依赖，并使用当前依赖 IP 重新创建轻量应用容器。
./apple-container.sh up

# 同时重新创建 PostgreSQL 和 Redis 容器，并保留命名卷。
./apple-container.sh up --recreate

# 停止容器，保留全部资源和数据。
./apple-container.sh down

# 按依赖顺序重启 PostgreSQL、Redis 和 TokenRouter。
./apple-container.sh restart

# 显示资源状态并执行实时健康探测。
./apple-container.sh status

# 持续查看指定服务日志。
./apple-container.sh logs app -f
./apple-container.sh logs postgres -f
./apple-container.sh logs redis -f

# 拉取全部已配置的 linux/arm64 镜像，然后重建容器。
./apple-container.sh pull
./apple-container.sh up --recreate

# 删除容器和网络，保留命名卷。
./apple-container.sh destroy --yes

# 永久删除整套服务及应用、数据库和缓存数据。
./apple-container.sh destroy --volumes --yes
```

`destroy --volumes` 不会删除 `.env`、备份文件或已拉取镜像。停用部署时需要单独删除凭据和备份。只有在确认没有其他 Apple 容器使用某个镜像后，才能执行 `container image delete <image>`。

宿主机重启或执行 `container system stop` 后，需要再次运行 `./apple-container.sh up`。Apple `container` 不会自动重启已持久化的容器。

## 配置

脚本默认使用 `deploy/.env`，与 Docker Compose 共用安装输入和被配置文件引用的敏感变量。若当前 Shell 中的所有命令都要使用其他文件，可导出 `TOKENROUTER_ENV_FILE`：

```bash
export TOKENROUTER_ENV_FILE=/absolute/path/to/tokenrouter.env
./apple-container.sh init
./apple-container.sh up
```

可以单独覆盖 Apple 容器使用的镜像：

```dotenv
APPLE_CONTAINER_TOKENROUTER_IMAGE=ghcr.io/tokenflux/tokenrouter:latest
APPLE_CONTAINER_POSTGRES_IMAGE=postgres:18-alpine
APPLE_CONTAINER_REDIS_IMAGE=redis:8-alpine
```

普通 `up` 会重新创建应用容器。安装后在数据卷中的 `/app/storage/data/config.yaml` 修改应用设置；`.env` 只传递安装输入和显式引用所需的敏感变量，其余应用变量不会进入容器。修改 PostgreSQL、Redis 容器镜像或 Redis 运行配置时，应使用 `up --recreate`。持久化数据仍保留在命名卷中。

`POSTGRES_USER`、`POSTGRES_PASSWORD` 和 `POSTGRES_DB` 只在 PostgreSQL 初始化空数据卷时应用。修改 `.env` 并重建容器不会改变现有数据库。密码应通过 `ALTER ROLE` 轮换，用户或数据库变更应制定明确的迁移方案。若确实需要初始化全新空数据库，先备份旧数据，再使用 `destroy --volumes`。

Apple 工作流对共用设置的处理如下：

| 设置 | Apple 工作流行为 |
|---|---|
| 应用和网关设置 | 编辑 `/app/storage/data/config.yaml` 后重启应用 |
| 安装输入和引用所需的敏感变量 | 从 `.env` 的明确名单传给 TokenRouter |
| `BIND_HOST`、`SERVER_PORT` | 用于 macOS 公开端口 |
| `POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_DB` | 仅用于 PostgreSQL 首次初始化 |
| `REDIS_PASSWORD` | 同时应用到 Redis 和 TokenRouter |
| `DATABASE_PORT`、`REDIS_PORT` | 内部端口固定为 5432 和 6379 |
| `POSTGRES_MAX_*`、`REDIS_MAXCLIENTS` | 当前不会应用到数据库或缓存服务 |

旧配置首次启动会备份并升级为 v2。保留文件中 `${VAR}` 引用需要的变量；自定义变量名需要加入脚本的显式传递名单。只读文件、备份命名和回退步骤见[配置升级说明](../../operations/upgrade_notes.md#config_version_2)。

## 受管资源

脚本只创建带有 `org.tokenrouter.stack=apple-container` 标签的资源：

| 类型 | 名称 |
|---|---|
| 容器 | `tokenrouter-apple`、`tokenrouter-apple-postgres`、`tokenrouter-apple-redis` |
| 网络 | `tokenrouter-apple` |
| 卷 | `tokenrouter-apple-data`、`tokenrouter-apple-postgres-data`、`tokenrouter-apple-redis-data` |

PostgreSQL 卷挂载到 `/var/lib/postgresql`，从而保留 PostgreSQL 18 默认的子数据目录。TokenRouter 和 Redis 也把数据保存在各自 Apple 卷挂载点下的子目录中。Apple 命名卷没有 Docker 那样的初始内容复制和挂载点所有权处理，所以需要使用这种目录结构。

## 网络

Apple `container` 1.1 不提供 Compose 风格的网络内服务别名。PostgreSQL 和 Redis 启动后，脚本通过 `container inspect` 读取它们当前的私有网络 IPv4 地址，将地址注入新创建的应用容器，再启动 TokenRouter。脚本不会修改 `~/.config/container/config.toml` 或 macOS 宿主机解析器。

三个服务只连接到私有 `tokenrouter-apple` 网络。宿主机端口只由应用发布，数据库和 Redis 的端口留在私有网络里。

每次执行 `up` 和 `restart` 都会重新创建应用容器，因为依赖虚拟机停止后地址可能变化。应用数据保留在 `tokenrouter-apple-data` 中。

自动安装和旧配置升级会把 `database.host` 写为 `${DATABASE_HOST}`、`redis.host` 写为 `${REDIS_HOST}`。每次启动由脚本提供新地址，配置文件保留引用。如果已有 v2 文件仍保存旧 IP，先备份文件，再将这两个字段改为上述引用后执行 `up`；仅重新创建应用容器不会修改 v2 文件中的字面值。

脚本报告成功前会从 macOS 检查已发布的 `/health` 端点。首次启动时需要允许本地网络访问。如果内部探测成功，但宿主机端口探测因连接重置失败，应为 `container-runtime-linux` 开启本地网络权限，依次运行 `container system stop` 和 `container system start`，再执行 `up`。运行时升级后可能再次请求权限。

## 备份与升级

将该工作流用于持久数据前，应在 `.env` 中固定镜像版本标签或摘要。升级应用或数据库镜像前，在服务健康时创建备份：

```bash
umask 077
mkdir -p backups

# 创建 PostgreSQL 逻辑备份。
container exec tokenrouter-apple sh -c \
  'PGPASSWORD="$DATABASE_PASSWORD" pg_dump -h "$DATABASE_HOST" -U "$DATABASE_USER" "$DATABASE_DBNAME"' \
  > backups/tokenrouter.sql

# 备份应用配置和本地文件。
container exec tokenrouter-apple sh -c 'tar -C "$DATA_DIR" -czf - .' \
  > backups/tokenrouter-data.tar.gz

./apple-container.sh pull
./apple-container.sh up --recreate
./apple-container.sh status
```

数据库迁移只向前执行。升级后的服务验证完成前，应保留旧镜像引用和两份备份；仅回退镜像不能撤销已经执行的数据库迁移。重要数据不能只依赖未演练过的恢复流程。

将备份恢复到现有服务栈前，先确认镜像版本与备份兼容，然后停止写入并替换应用和数据库数据：

```bash
# 确保当前资源存在，然后停止服务栈。
./apple-container.sh up
./apple-container.sh down

# 只删除应用容器，以便辅助容器挂载它的命名卷。
container delete tokenrouter-apple
TOKENROUTER_IMAGE=ghcr.io/tokenflux/tokenrouter:latest # 应与 .env 中的 APPLE_CONTAINER_TOKENROUTER_IMAGE 一致。
container run --rm --name tokenrouter-apple-data-restore \
  --entrypoint /bin/sh \
  --volume tokenrouter-apple-data:/restore \
  --volume "$PWD/backups:/backup:ro" \
  "$TOKENROUTER_IMAGE" \
  -c 'rm -rf /restore/data && mkdir -p /restore/data && tar -xzf /backup/tokenrouter-data.tar.gz -C /restore/data'

# 在应用不存在时恢复 PostgreSQL 逻辑备份。
container start tokenrouter-apple-postgres
until container exec tokenrouter-apple-postgres sh -c 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"'; do sleep 1; done
container copy backups/tokenrouter.sql tokenrouter-apple-postgres:/tmp/tokenrouter.sql
container exec tokenrouter-apple-postgres sh -c '
  export PGPASSWORD="$POSTGRES_PASSWORD"
  dropdb -h 127.0.0.1 -U "$POSTGRES_USER" --if-exists --force "$POSTGRES_DB"
  createdb -h 127.0.0.1 -U "$POSTGRES_USER" "$POSTGRES_DB"
  psql -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -f /tmp/tokenrouter.sql
  rm /tmp/tokenrouter.sql
'

./apple-container.sh up
./apple-container.sh status
```

命名卷被删除后的灾难恢复，需要先运行一次 `up` 创建全新服务栈，再执行上述恢复步骤。应先用非生产数据演练恢复。

升级 Apple 运行时时执行：

```bash
./apple-container.sh down
container system stop
# 安装或更新到 Apple container 1.1.0 或更高版本。
container system start
./apple-container.sh up
```

## 运维限制

- 没有与 `restart: unless-stopped` 等价的机制。重启后需要运行 `up`，也可以自行配置 launchd 监督进程。
- 健康探测只在 `up`、`restart` 和 `status` 期间运行；Apple `container` 不会持续调度探测。
- Docker Compose、Testcontainers、Buildx 以及依赖 `/var/run/docker.sock` 的工具不能直接使用该运行时。
- 把这个工作流用于重要数据之前，先验证命名卷的备份和恢复流程。
- 脚本面向原生 `linux/arm64` 镜像，TokenRouter 正常发布物包含 arm64 版本。
- 包括凭据在内的运行环境值会保留在 Apple container 配置中，能够检查本地运行时的用户可以看到这些值。

## 旧命名资源兼容

已有 `sub2api-apple*` 容器、网络和卷会原位复用，仍要求 `org.sub2api.stack=apple-container` 归属标签。新部署使用本文中的 `tokenrouter-apple*` 名称。新旧资源同时存在时，命令直接失败，两套资源都保持原样。操作旧栈备份、恢复时，命令中的容器和卷名应使用实际旧名称。

`SUB2API_ENV_FILE` 和环境文件里的 `APPLE_CONTAINER_SUB2API_IMAGE` 仍可读取；非空 `TOKENROUTER_ENV_FILE` 优先，非空的新镜像配置优先。已有 `.env` 不自动改写，数据库用户、库名、密码和安全密钥继续保留。
