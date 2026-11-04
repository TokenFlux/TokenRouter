# 部署指南

本文面向部署和日常运维。工程上的生命周期和迁移约束，以 [Project Doc：部署与数据库迁移](../../operations/deployment_and_migrations.md) 为准。其他手册见[指南目录](../index.md)。

仓库提供三种部署方式：脚本安装二进制、Docker Compose 和源码编译。生产环境通常选前两种；Apple container 见单独的 [Apple container 部署指南](apple_container.md)。

## 从旧名称部署升级

下面的安装命令面向新部署。已有的 `sub2api` 安装，继续使用原来的目录、服务名、数据库和数据卷，安装脚本会自动识别。同时存在新旧两套资源时，脚本会停止，需要先确定保留哪一套部署。

Compose 用户保留现有的 YAML 和 `.env`，用原来的服务名升级镜像。不要直接用新模板覆盖后启动，否则会创建空的数据卷。确实要换成新模板时，先备份，并在 `docker volume ls` 里记下实际使用的卷名，然后在 `.env` 里设置 `TOKENROUTER_DATA_VOLUME`、`TOKENROUTER_POSTGRES_VOLUME`、`TOKENROUTER_REDIS_VOLUME`，保留原来的 `POSTGRES_USER`、`POSTGRES_DB`、密码和安全密钥，再停止旧栈、启动新模板。使用本地目录挂载的部署，继续使用原来的目录；独立容器保留原来的 `DATABASE_USER` 和 `DATABASE_DBNAME`。

直接运行二进制、配置里省略了数据库名的旧部署，先补上 `DATABASE_DBNAME=sub2api` 或对应的 YAML 键。新版也会读取 `/etc/sub2api/config.yaml`，新的系统目录 `/etc/tokenrouter` 优先；手动指定 `CONFIG_FILE` 的规则不变。

发布归档保留了旧安装器需要的兼容文件，回退到历史版本时，仍可以读取旧归档。品牌设置只在恰好等于旧默认名时才更新；自定义的品牌、已有的登录和 TOTP 密钥都不会被重置。更多约束见[产品名称与升级兼容](../../operations/deployment_and_migrations.md#product_name_compatibility)。

### 运行模式的变化

所有部署统一执行余额、订阅、Key 配额检查和正常计费。旧的环境变量 `RUN_MODE`、YAML 的 `run_mode` 和 `SIMPLE_MODE_CONFIRM` 已经没有作用，留在配置里也不影响启动，可以直接删除。升级前，为需要继续调用的用户准备好余额或有效订阅；原来简易部署里的请求，升级后同样按正常规则计费。

已有的余额、订阅、管理员并发和历史用量保持原值，不会追补历史费用。新安装的管理员默认并发为 5。`GET /api/v1/auth/me` 不再返回 `run_mode`，外部调用方需要去掉对这个字段的依赖。

## 脚本安装

安装脚本从 GitHub Releases 下载预编译的二进制，并配置 systemd 服务。

前置条件：

- Linux 服务器（amd64 或 arm64）
- PostgreSQL 15 或更高，已经安装并运行
- Redis 7 或更高，已经安装并运行
- root 权限

安装：

```bash
curl -sSL https://raw.githubusercontent.com/TokenFlux/TokenRouter/main/deploy/install.sh | sudo bash
```

脚本会检测系统架构、下载最新版本、把二进制安装到 `/opt/tokenrouter`、创建 systemd 服务，并配置系统用户和权限。

安装后启动服务，并在浏览器里完成设置向导：

```bash
# 1. 启动服务
sudo systemctl start tokenrouter

# 2. 设置开机自启
sudo systemctl enable tokenrouter

# 3. 在浏览器中打开设置向导
# http://你的服务器IP:8080
```

设置向导会引导你配置数据库、Redis，并创建管理员账号。

### 升级

在管理后台左上角点击"检测更新"，可以在线升级：自动检测新版本，下载并应用更新，也支持回滚。

匿名访问 GitHub Release API 被限流时，可以在安装脚本的进程环境或 Docker `.env` 里设置 `UPDATE_GITHUB_TOKEN`。这个令牌只会发给 `https://api.github.com` 的版本检查请求，跨目标的重定向会移除认证头；Release 资源和校验和的下载始终是匿名的。系统不会改用 `GITHUB_TOKEN` 或 `GH_TOKEN`。

### 常用命令

```bash
# 查看状态
sudo systemctl status tokenrouter

# 查看日志
sudo journalctl -u tokenrouter -f

# 重启服务
sudo systemctl restart tokenrouter

# 卸载
curl -sSL https://raw.githubusercontent.com/TokenFlux/TokenRouter/main/deploy/install.sh | sudo bash -s -- uninstall -y
```

## Docker Compose

Compose 部署包含应用、PostgreSQL 和 Redis 三个容器。

官方的多架构镜像是 `ghcr.io/tokenflux/tokenrouter`。生产环境建议固定 `vX.Y.Z` 标签或镜像摘要；支持的架构和独立容器需要的变量，见 [Docker 镜像说明](../../../deploy/DOCKER.md)。

前置条件：Docker 20.10 或更高，Docker Compose v2 或更高。

### 用脚本快速部署

```bash
# 创建部署目录
mkdir -p tokenrouter-deploy && cd tokenrouter-deploy

# 下载并运行部署准备脚本
curl -sSL https://raw.githubusercontent.com/TokenFlux/TokenRouter/main/deploy/docker-deploy.sh | bash

# 启动服务
docker compose up -d

# 查看日志
docker compose logs -f tokenrouter
```

准备脚本会：

- 下载 `docker-compose.local.yml`（本地保存为 `docker-compose.yml`）和 `.env.example`。
- 自动生成 `JWT_SECRET`、`TOTP_ENCRYPTION_KEY` 和 `POSTGRES_PASSWORD`。
- 创建 `.env` 文件，并填入生成的密钥。
- 创建数据目录（使用本地目录，方便备份和迁移）。
- 显示生成的凭据，请记录下来。

### 手动部署

```bash
# 1. 克隆仓库
git clone https://github.com/TokenFlux/TokenRouter.git
cd TokenRouter/deploy

# 2. 复制环境配置文件
cp .env.example .env

# 3. 编辑配置（生成安全密码）
nano .env
```

`.env` 里需要配置的项：

```bash
# PostgreSQL 密码（必需）
POSTGRES_PASSWORD=your_secure_password_here

# JWT 密钥（建议设置，重启后用户保持登录）
JWT_SECRET=your_jwt_secret_here

# TOTP 加密密钥（建议设置，重启后保留双因素认证）
TOTP_ENCRYPTION_KEY=your_totp_key_here

# 可选：管理员账号
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=your_admin_password

# 可选：自定义端口
SERVER_PORT=8080
```

生成密钥：

```bash
# 生成 JWT_SECRET
openssl rand -hex 32

# 生成 TOTP_ENCRYPTION_KEY
openssl rand -hex 32

# 生成 POSTGRES_PASSWORD
openssl rand -hex 32
```

启动：

```bash
# 4. 创建数据目录（本地目录版）
mkdir -p data postgres_data redis_data

# 5. 启动所有服务
# 选项 A：本地目录版（便于迁移）
docker compose -f docker-compose.local.yml up -d

# 选项 B：命名卷版（设置简单）
docker compose up -d

# 6. 查看状态
docker compose -f docker-compose.local.yml ps

# 7. 查看日志
docker compose -f docker-compose.local.yml logs -f tokenrouter
```

两个 Compose 文件的区别：

| 文件 | 数据存储 | 迁移 | 适合 |
| --- | --- | --- | --- |
| `docker-compose.local.yml` | 本地目录 | 打包整个目录即可 | 生产环境、需要经常备份 |
| `docker-compose.yml` | 命名卷 | 需要用 docker 命令导出卷 | 简单试用 |

脚本部署默认使用 `docker-compose.local.yml`，数据管理更方便。

### 访问

在浏览器里打开 `http://你的服务器IP:8080`。

管理员密码是自动生成的时候，在日志里查找：

```bash
docker compose -f docker-compose.local.yml logs tokenrouter | grep "admin password"
```

### 升级

```bash
# 拉取最新镜像并重建容器
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d
```

### 迁移到新服务器（本地目录版）

使用 `docker-compose.local.yml` 时，打包整个部署目录即可迁移：

```bash
# 源服务器
docker compose -f docker-compose.local.yml down
cd ..
tar czf tokenrouter-complete.tar.gz tokenrouter-deploy/

# 传输到新服务器
scp tokenrouter-complete.tar.gz user@new-server:/path/

# 新服务器
tar xzf tokenrouter-complete.tar.gz
cd tokenrouter-deploy/
docker compose -f docker-compose.local.yml up -d
```

### 常用命令

```bash
# 停止所有服务
docker compose -f docker-compose.local.yml down

# 重启
docker compose -f docker-compose.local.yml restart

# 查看所有日志
docker compose -f docker-compose.local.yml logs -f

# 删除所有数据（不可恢复，谨慎操作）
docker compose -f docker-compose.local.yml down
rm -rf data/ postgres_data/ redis_data/
```

### 旧的数据管理守护进程

TokenRouter 已经停用 `datamanagementd` 接口，不再探测或连接它的 Unix Socket。新部署使用内置的 backup 模块做备份和恢复，不需要在宿主机安装守护进程。当前仓库已移除旧安装脚本和 systemd 模板，遗留服务的识别与清理见 [datamanagementd 停用说明](datamanagementd.md)。

## 源码编译

适合开发或需要定制的场景。

前置条件：

- Go，版本以 `backend/go.mod` 为准（当前 1.27）
- Node.js 20 和 pnpm 9
- PostgreSQL 15 或更高
- Redis 7 或更高

```bash
# 1. 克隆仓库
git clone https://github.com/TokenFlux/TokenRouter.git
cd TokenRouter

# 2. 安装 pnpm（如果还没有安装）
npm install -g pnpm

# 3. 编译前端
cd frontend
pnpm install
pnpm run build
# 构建产物输出到 ../backend/internal/web/dist/

# 4. 编译后端（嵌入前端）
cd ../backend
go build -tags embed -o tokenrouter ./cmd/server
```

`-tags embed` 会把前端嵌进二进制；不加这个参数编译出的程序没有前端界面。

### 创建管理员

初始管理员只能通过 setup 向导创建（第一次启动时访问 `http://<host>:8080`）。`config.yaml` 里的 `default.admin_email` 和 `default.admin_password` 字段不会被用来创建管理员，它们只是因为历史原因留在模板里。

如果在第一次启动前就创建了 `config.yaml`，服务会认为已经配置完成，跳过 setup 向导、直接进入正常模式；这时 `users` 表是空的，第一次登录会返回 `invalid email or password`。所以建议直接运行程序，让向导生成配置：

```bash
# 5. 运行应用，在 http://localhost:8080 完成向导
./tokenrouter
```

向导会引导你完成数据库、Redis 和管理员账号的配置，并写出 `config.yaml`。

如果已经手动创建了 `config.yaml`，第一次启动前先把它临时移走，触发向导，完成后再恢复：

```bash
mv config.yaml config.yaml.bak
./tokenrouter        # 向导在 http://localhost:8080 启动，并生成新的 config.yaml
# 向导完成后 Ctrl+C 停服，再恢复你的配置：
mv config.yaml.bak config.yaml
./tokenrouter        # 重启进入正常模式，用刚创建的管理员登录
```

### config.yaml 的关键配置

需要手动编辑配置时，从样例复制一份：

```bash
cp ../deploy/config.example.yaml ./config.yaml
```

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  mode: "release"

database:
  host: "localhost"
  port: 5432
  user: "postgres"
  password: "your_password"
  dbname: "tokenrouter"

redis:
  host: "localhost"
  port: 6379
  username: "" # Redis ACL 用户名；使用默认用户时留空
  password: ""

jwt:
  secret: "change-this-to-a-secure-random-string"
  expire_hour: 24

default:
  user_concurrency: 5
  user_balance: 0
  api_key_prefix: "sk-"
  rate_multiplier: 1.0
```

## 常用配置

### Passkey 和 WebAuthn

Passkey 由部署配置控制，无法在管理后台直接开启。编辑 `config.yaml`，填写浏览器实际访问站点时使用的公开域名和 Origin：

```yaml
webauthn:
  enabled: true
  rp_display_name: "TokenRouter"
  rp_id: "tokenrouter.example.com"
  rp_origins:
    - "https://tokenrouter.example.com"
```

- `webauthn.rp_id` 只填域名，不带协议、端口或路径。
- `webauthn.rp_origins` 填完整的 Origin，不带路径、查询参数或片段。生产环境要使用 HTTPS；只有 `localhost`、`127.0.0.1` 和 `::1` 的本地开发环境可以使用 HTTP。
- 每个 Origin 的主机，要等于 `rp_id` 或是它的子域名。通过反向代理部署时，填写浏览器访问的公开 HTTPS Origin，不要填容器名、内网地址或后端的监听端口。
- 修改配置后需要重启服务。`rp_id` 决定 Passkey 凭据属于哪个站点，上线后保持稳定；更换它之后，已经注册的凭据就无法使用了。

配置不完整或不符合上面的要求时，服务在启动校验阶段报出对应的 `webauthn.*` 错误；Passkey 登录不会改用请求里不可信的 `Host` 或 `Origin` 头作为配置。

### OpenAI Responses WebSocket 首条消息超时

提供商级的 WS mode（包括 `http_bridge`），只在新版 mode router 开启时生效。关闭时，提供商级的 mode 被忽略，网关继续使用旧的 `ctx_pool` 行为。可以通过 YAML 开启：

```yaml
gateway:
  openai_ws:
    mode_router_v2_enabled: true
```

也可以设置环境变量 `GATEWAY_OPENAI_WS_MODE_ROUTER_V2_ENABLED=true`。

`gateway.openai_ws.client_first_message_timeout_seconds` 限制 WebSocket 升级之后，完整读取并解压客户端第一条 `response.create` 消息的总时间，默认 30 秒。上下文很大、图片较多或链路较慢时，可以调到 120 到 300 秒。这个截止时间在 HTTP bridge 的路由判断之前生效，bridge 模式同样受它约束。

```yaml
gateway:
  openai_ws:
    client_first_message_timeout_seconds: 30
```

### 强制 OpenAI 上游使用 HTTP/SSE

出站代理或网络导致 OpenAI Responses 上游的 WebSocket 反复重连时，可以在持久化的 `config.yaml` 里开启全局回退：

```yaml
gateway:
  openai_ws:
    force_http: true
```

Docker Compose 和 Apple `container` 共用的 `.env` 也可以设置：

```bash
GATEWAY_OPENAI_WS_FORCE_HTTP=true
```

这个开关只把网关到 OpenAI 上游的传输改成 HTTP/SSE，客户端协议保持不变，也不会强制使用 HTTP/1.1。代理不兼容 HTTP/2 时，另外设置 `gateway.openai_http2.enabled: false` 或 `GATEWAY_OPENAI_HTTP2_ENABLED=false`。配置写进持久化的 `.env` 或 `config.yaml`，只在运行中的容器里临时修改的话，镜像更新或容器重建后就会丢失。

### 安全相关配置

`config.yaml` 还支持以下安全相关的配置：

- `cors.allowed_origins`：CORS 白名单。
- `security.url_allowlist`：上游、价格数据和 CRS 的主机白名单。
- `security.url_allowlist.enabled`：可以关闭 URL 校验，请谨慎使用。
- `security.url_allowlist.allow_insecure_http`：关闭校验时允许 HTTP URL。
- `security.url_allowlist.allow_private_hosts`：允许私有或本地的 IP 地址。
- `security.response_headers.enabled`：开启可配置的响应头过滤（关闭时使用默认白名单）。
- `security.csp`：Content-Security-Policy。
- `billing.circuit_breaker`：计费异常时拒绝请求（fail-closed）。
- `server.trusted_proxies`：开启可信代理，解析 X-Forwarded-For。
- `turnstile.required`：在 release 模式下强制启用 Turnstile。

网关的防护建议：

- `gateway.upstream_response_read_max_bytes`：限制非流式上游响应的读取大小（默认 `8MB`），防止异常响应占用过多内存。
- `gateway.proxy_probe_response_read_max_bytes`：限制代理探测响应的读取大小（默认 `1MB`）。
- `gateway.gemini_debug_response_headers`：默认 `false`，排查问题时短时间开启即可，常开会给高频请求带来日志开销。
- `/auth/register`、`/auth/login`、`/auth/login/2fa`、`/auth/send-verify-code` 在服务端也有限流，Redis 故障时拒绝请求。
- 建议把 WAF 或 CDN 作为第一层防护，服务端的限流和响应读取上限作为第二层；两层都保留，可以覆盖绕过 CDN 的流量和配置失误。

### 允许 HTTP URL

`security.url_allowlist.enabled=false` 时，系统仍执行最基本的 URL 校验：默认拒绝 HTTP URL，只允许 HTTPS。开发或内网测试需要使用 HTTP URL 时，手动设置：

```yaml
security:
  url_allowlist:
    enabled: false                # 关闭白名单检查
    allow_insecure_http: true     # 允许 HTTP URL（不安全）
```

或者通过环境变量：

```bash
SECURITY_URL_ALLOWLIST_ENABLED=false
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP=true
```

允许 HTTP 时，API 密钥和数据以明文传输，可能被截获，也容易受到中间人攻击，所以不适合生产环境。可以用于本地开发服务器（`http://localhost`）、可信的内网端点，以及申请到 HTTPS 证书之前测试提供商的连通性；生产环境只使用 HTTPS。

没有设置这一项时，会看到类似的错误：

```
Invalid base URL: invalid url scheme: http
```

关闭 URL 校验或响应头过滤时，在网络层加强防护：

- 用出站白名单限制上游的域名和 IP。
- 阻断私网、回环和链路本地地址。
- 出站只允许 TLS。
- 在反向代理层移除敏感的响应头。

### HTTP/2（h2c）与 HTTP/1.1 回退

后端的明文端口默认支持 h2c，并保留 HTTP/1.1 回退，用于 WebSocket 和旧客户端。浏览器通常不支持 h2c，性能收益主要体现在反向代理或内网链路上。

反向代理示例（Caddy）：

```caddyfile
transport http {
	versions h2c h1
}
```

验证：

```bash
# h2c 先验模式
curl --http2-prior-knowledge -I http://localhost:8080/health
# HTTP/1.1 回退
curl --http1.1 -I http://localhost:8080/health
# WebSocket 回退验证（需要管理员 token）
websocat -H="Sec-WebSocket-Protocol: tokenrouter-admin, jwt.<ADMIN_TOKEN>" ws://localhost:8080/api/v1/admin/ops/ws/qps
```
