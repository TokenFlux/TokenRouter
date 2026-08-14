# TokenRouter Docker 镜像

TokenRouter 会向 GitHub Container Registry 发布多架构镜像：

```text
ghcr.io/brandonvee/tokenrouter:latest
```

应用依赖 PostgreSQL 和 Redis。推荐使用 Docker Compose 提供完整的运行配置、持久化存储、健康检查和依赖启动顺序。

## Docker Compose

根据部署拓扑选择配置文件：

| 文件 | 服务 | 存储 | 适用场景 |
| --- | --- | --- | --- |
| [`docker-compose.local.yml`](docker-compose.local.yml) | TokenRouter、PostgreSQL、Redis | 本地目录 | 侧重简单备份和迁移的生产部署 |
| [`docker-compose.yml`](docker-compose.yml) | TokenRouter、PostgreSQL、Redis | Docker 命名卷 | 使用 Docker 卷管理的生产部署 |
| [`docker-compose.standalone.yml`](docker-compose.standalone.yml) | 仅 TokenRouter | Docker 命名卷 | 已有外部 PostgreSQL 和 Redis 的环境 |
| [`docker-compose.dev.yml`](docker-compose.dev.yml) | 本地构建的 TokenRouter、PostgreSQL、Redis | 本地目录 | 开发和源码测试 |

完整步骤见 [部署指南](../docs/guides/deployment/index.md)，环境变量示例见 [`.env.example`](.env.example)。

## 独立容器

直接使用 `docker run` 时，必须提供与 `docker-compose.standalone.yml` 中 `sub2api` 服务相同的设置。至少需要配置：

| 变量 | 用途 |
| --- | --- |
| `AUTO_SETUP=true` | 启用无人值守容器初始化 |
| `SERVER_HOST=0.0.0.0` | 让应用监听容器内网卡 |
| `DATABASE_HOST`、`DATABASE_PORT` | PostgreSQL 地址 |
| `DATABASE_USER`、`DATABASE_PASSWORD`、`DATABASE_DBNAME` | PostgreSQL 凭据和数据库名 |
| `DATABASE_SSLMODE` | PostgreSQL TLS 模式 |
| `REDIS_HOST`、`REDIS_PORT` | Redis 地址 |
| `REDIS_USERNAME`、`REDIS_PASSWORD`、`REDIS_DB` | Redis 凭据和数据库编号 |
| `JWT_SECRET` | 登录会话的稳定签名密钥 |
| `TOTP_ENCRYPTION_KEY` | 双因素认证的稳定加密密钥 |

必须持久化 `/app/data`，把公开端口绑定到预期的宿主机接口，并应用独立 Compose 文件中的安全与资源限制。应用不使用旧的 `DATABASE_URL` 或 `REDIS_URL` 变量。

## 支持架构

- `linux/amd64`
- `linux/arm64`

## 镜像标签

- `latest`：最新稳定版本
- `vX.Y.Z`：不可变的版本标签

生产部署应固定版本标签或镜像摘要，并在升级前验证数据库备份。

## 相关链接

- [GitHub 仓库](https://github.com/BrandonVee/TokenRouter)
- [部署指南](../docs/guides/deployment/index.md)
