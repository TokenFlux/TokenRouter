# TokenRouter 部署文件

按运行方式选择入口。安装、升级和备份步骤见[部署指南](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/index.md)，镜像配置见 [DOCKER.md](DOCKER.md)。

| 场景 | 入口 | 说明 |
| --- | --- | --- |
| Linux 二进制安装 | [install.sh](install.sh) | 下载发布归档并配置 systemd，依赖外部 PostgreSQL 和 Redis |
| Docker 一键准备 | [docker-deploy.sh](docker-deploy.sh) | 下载本地目录版 Compose 和环境示例，生成密钥后由部署者启动 |
| Docker 命名卷 | [docker-compose.yml](docker-compose.yml) | 应用、PostgreSQL、Redis 使用命名卷 |
| Docker 本地目录 | [docker-compose.local.yml](docker-compose.local.yml) | 应用、PostgreSQL、Redis 持久化到当前目录 |
| 外部数据库与 Redis | [docker-compose.standalone.yml](docker-compose.standalone.yml) | 只启动应用容器 |
| Apple Container | [apple-container.sh](apple-container.sh) | 管理应用及配套依赖，步骤见 [Apple Container 指南](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/apple_container.md) |

## 配置与模板

- [`.env.example`](.env.example)：容器环境变量示例，复制为 `.env` 后配置。
- [config.example.yaml](config.example.yaml)：应用完整配置示例。
- [Caddyfile](Caddyfile)：反向代理基线，使用前修改域名。
- [tokenrouter.service](tokenrouter.service)：手动安装的 systemd 模板；安装器会按实际路径生成服务配置。
- [codex-instructions.md.tmpl](codex-instructions.md.tmpl)：可选的 Codex 请求指令模板，由应用配置指定路径。

## 源码检出专用

`docker-compose.dev.yml` 和 `build_image.sh` 都使用仓库根目录的 `Dockerfile` 构建完整应用，运行时需要完整源码。`docker-entrypoint.sh` 由源码镜像和发布镜像的 Dockerfile 复制，负责卷权限与容器启动。部署测试集中在 `tests/`，从仓库根目录运行 `make test-scripts`。

二进制发布包按 `.goreleaser.yaml` 的明确清单附带部署文件，不包含开发 Compose、构建辅助脚本、镜像入口脚本或测试夹具。旧数据管理守护进程已停用，遗留部署识别见 [datamanagementd 停用说明](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/datamanagementd.md)。
