# 部署指南

本文面向部署和日常运维；工程生命周期与迁移约束以 [Project Doc：部署与数据库迁移](../../operations/deployment_and_migrations.md) 为准。其他手册见 [指南目录](../index.md)。

## 部署方式

### 方式一：脚本安装（推荐）

一键安装脚本，自动从 GitHub Releases 下载预编译的二进制文件。

#### 前置条件

- Linux 服务器（amd64 或 arm64）
- PostgreSQL 15+（已安装并运行）
- Redis 7+（已安装并运行）
- Root 权限

#### 安装步骤

```bash
curl -sSL https://raw.githubusercontent.com/BrandonVee/TokenRouter/main/deploy/install.sh | sudo bash
```

脚本会自动：
1. 检测系统架构
2. 下载最新版本
3. 安装二进制文件到 `/opt/sub2api`
4. 创建 systemd 服务
5. 配置系统用户和权限

#### 安装后配置

```bash
# 1. 启动服务
sudo systemctl start sub2api

# 2. 设置开机自启
sudo systemctl enable sub2api

# 3. 在浏览器中打开设置向导
# http://你的服务器IP:8080
```

设置向导将引导你完成：
- 数据库配置
- Redis 配置
- 管理员账号创建

#### 升级

可以直接在 **管理后台** 左上角点击 **检测更新** 按钮进行在线升级。

网页升级功能支持：
- 自动检测新版本
- 一键下载并应用更新
- 支持回滚

匿名访问 GitHub Release API 触发限流时，可在安装脚本进程环境或 Docker `.env`
中设置 `UPDATE_GITHUB_TOKEN`。该令牌只会发送给 `https://api.github.com` 的版本
检查请求，跨目标重定向会移除认证头；Release 资源与校验和下载始终保持匿名。
系统不会回退使用 `GITHUB_TOKEN` 或 `GH_TOKEN`。

#### 常用命令

```bash
# 查看状态
sudo systemctl status sub2api

# 查看日志
sudo journalctl -u sub2api -f

# 重启服务
sudo systemctl restart sub2api

# 卸载
curl -sSL https://raw.githubusercontent.com/BrandonVee/TokenRouter/main/deploy/install.sh | sudo bash -s -- uninstall -y
```

---

### 方式二：Docker Compose（推荐）

使用 Docker Compose 部署，包含 PostgreSQL 和 Redis 容器。

官方多架构镜像为 `ghcr.io/brandonvee/tokenrouter`。生产环境建议固定 `vX.Y.Z` 标签或镜像摘要；支持架构和独立容器所需变量见 [Docker 镜像说明](../../../deploy/DOCKER.md)。

#### 前置条件

- Docker 20.10+
- Docker Compose v2+

#### 快速开始（一键部署）

使用自动化部署脚本快速搭建：

```bash
# 创建部署目录
mkdir -p sub2api-deploy && cd sub2api-deploy

# 下载并运行部署准备脚本
curl -sSL https://raw.githubusercontent.com/BrandonVee/TokenRouter/main/deploy/docker-deploy.sh | bash

# 启动服务
docker compose up -d

# 查看日志
docker compose logs -f sub2api
```

**脚本功能：**
- 下载 `docker-compose.local.yml`（本地保存为 `docker-compose.yml`）和 `.env.example`
- 自动生成安全凭证（JWT_SECRET、TOTP_ENCRYPTION_KEY、POSTGRES_PASSWORD）
- 创建 `.env` 文件并填充自动生成的密钥
- 创建数据目录（使用本地目录，便于备份和迁移）
- 显示生成的凭证供你记录

#### 手动部署

如果你希望手动配置：

```bash
# 1. 克隆仓库
git clone https://github.com/BrandonVee/TokenRouter.git
cd TokenRouter/deploy

# 2. 复制环境配置文件
cp .env.example .env

# 3. 编辑配置（生成安全密码）
nano .env
```

**`.env` 必须配置项：**

```bash
# PostgreSQL 密码（必需）
POSTGRES_PASSWORD=your_secure_password_here

# JWT 密钥（推荐 - 重启后保持用户登录状态）
JWT_SECRET=your_jwt_secret_here

# TOTP 加密密钥（推荐 - 重启后保留双因素认证）
TOTP_ENCRYPTION_KEY=your_totp_key_here

# 可选：管理员账号
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=your_admin_password

# 可选：自定义端口
SERVER_PORT=8080
```

**生成安全密钥：**
```bash
# 生成 JWT_SECRET
openssl rand -hex 32

# 生成 TOTP_ENCRYPTION_KEY
openssl rand -hex 32

# 生成 POSTGRES_PASSWORD
openssl rand -hex 32
```

```bash
# 4. 创建数据目录（本地版）
mkdir -p data postgres_data redis_data

# 5. 启动所有服务
# 选项 A：本地目录版（推荐 - 易于迁移）
docker compose -f docker-compose.local.yml up -d

# 选项 B：命名卷版（简单设置）
docker compose up -d

# 6. 查看状态
docker compose -f docker-compose.local.yml ps

# 7. 查看日志
docker compose -f docker-compose.local.yml logs -f sub2api
```

#### 简易模式

简易模式适合个人开发者或内部团队快速使用，不依赖完整 SaaS 功能。

- 启用方式：设置环境变量 `RUN_MODE=simple`
- 功能差异：隐藏 SaaS 相关功能，跳过计费流程
- 安全注意事项：生产环境需同时设置 `SIMPLE_MODE_CONFIRM=true` 才允许启动

#### 部署版本对比

| 版本                           | 数据存储 | 迁移便利性           | 适用场景      |
|------------------------------|------|-----------------|-----------|
| **docker-compose.local.yml** | 本地目录 | ✅ 简单（打包整个目录）    | 生产环境、频繁备份 |
| **docker-compose.yml**       | 命名卷  | ⚠️ 需要 docker 命令 | 简单设置      |

**推荐：** 使用 `docker-compose.local.yml`（脚本部署）以便更轻松地管理数据。

#### 启用“数据管理”功能（datamanagementd）

如需启用管理后台“数据管理”，需要额外部署宿主机数据管理进程 `datamanagementd`。

关键点：

- 主进程固定探测：`/tmp/sub2api-datamanagement.sock`
- 只有该 Socket 可连通时，数据管理功能才会开启
- Docker 场景需将宿主机 Socket 挂载到容器同路径

当前仓库不包含该进程的源码或二进制，启用前请先阅读 [datamanagementd 部署说明](datamanagementd.md)。

#### 访问

在浏览器中打开 `http://你的服务器IP:8080`

如果管理员密码是自动生成的，在日志中查找：
```bash
docker compose -f docker-compose.local.yml logs sub2api | grep "admin password"
```

#### 升级

```bash
# 拉取最新镜像并重建容器
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d
```

#### 轻松迁移（本地目录版）

使用 `docker-compose.local.yml` 时，可以轻松迁移到新服务器：

```bash
# 源服务器
docker compose -f docker-compose.local.yml down
cd ..
tar czf sub2api-complete.tar.gz sub2api-deploy/

# 传输到新服务器
scp sub2api-complete.tar.gz user@new-server:/path/

# 新服务器
tar xzf sub2api-complete.tar.gz
cd sub2api-deploy/
docker compose -f docker-compose.local.yml up -d
```

#### 常用命令

```bash
# 停止所有服务
docker compose -f docker-compose.local.yml down

# 重启
docker compose -f docker-compose.local.yml restart

# 查看所有日志
docker compose -f docker-compose.local.yml logs -f

# 删除所有数据（谨慎！）
docker compose -f docker-compose.local.yml down
rm -rf data/ postgres_data/ redis_data/
```

---

### 方式三：源码编译

从源码编译安装，适合开发或定制需求。

#### 前置条件

- Go 1.21+
- Node.js 18+
- PostgreSQL 15+
- Redis 7+

#### 编译步骤

```bash
# 1. 克隆仓库
git clone https://github.com/BrandonVee/TokenRouter.git
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
go build -tags embed -o sub2api ./cmd/server

# 5. 创建配置文件
cp ../deploy/config.example.yaml ./config.yaml

# 6. 编辑配置
nano config.yaml
```

> **注意：** `-tags embed` 参数会将前端嵌入到二进制文件中。不使用此参数编译的程序将不包含前端界面。

**`config.yaml` 关键配置：**

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
  dbname: "sub2api"

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

### Passkey / WebAuthn 部署配置

Passkey 由部署配置控制，不能在管理后台直接开启。编辑 `config.yaml`，填写浏览器
实际访问站点时使用的公开域名和 Origin：

```yaml
webauthn:
  enabled: true
  rp_display_name: "TokenRouter"
  rp_id: "tokenrouter.example.com"
  rp_origins:
    - "https://tokenrouter.example.com"
```

- `webauthn.rp_id` 只能填写域名，不能包含协议、端口或路径。
- `webauthn.rp_origins` 必须填写完整 Origin，不能包含路径、查询参数或片段。生产环境
  必须使用 HTTPS；仅 `localhost`、`127.0.0.1` 和 `::1` 的本地开发环境可使用 HTTP。
- 每个 Origin 的主机必须等于 `rp_id` 或是它的子域名。经过反向代理部署时，应填写
  浏览器访问的公开 HTTPS Origin，而不是容器名、内网地址或后端监听端口。
- 修改配置后必须重启服务。`rp_id` 是 Passkey 凭据的安全边界，上线后应保持稳定；
  更换它会导致已注册凭据无法用于新的依赖方。

配置不完整或不符合上述约束时，服务会在启动校验阶段报出对应的 `webauthn.*` 错误，
Passkey 登录不会以不可信的 `Host` 或 `Origin` 请求头作为配置回退。

### OpenAI Responses WebSocket 首消息超时

账号级 WS mode（包括 `http_bridge`）仅在新版 mode router 开启时生效。关闭该开关时，
账号级 mode 会被忽略，网关继续使用 legacy `ctx_pool` 行为。可通过 YAML 开启：

```yaml
gateway:
  openai_ws:
    mode_router_v2_enabled: true
```

也可设置环境变量 `GATEWAY_OPENAI_WS_MODE_ROUTER_V2_ENABLED=true`。

`gateway.openai_ws.client_first_message_timeout_seconds` 限制 WebSocket 升级后完整读取并
解压首条客户端 `response.create` 消息的总时间，默认 30 秒。大上下文、图片较多或慢链路
场景可调高到 120-300 秒。该截止时间在 HTTP bridge 路由判断前生效，bridge 模式不会绕过它。

```yaml
gateway:
  openai_ws:
    client_first_message_timeout_seconds: 30
```

### Sora 功能状态（暂不可用）

> ⚠️ 当前 Sora 相关功能因上游接入与媒体链路存在技术问题，暂时不可用。
> 现阶段请勿在生产环境依赖 Sora 能力。
> 文档中的 `gateway.sora_*` 配置仅作预留，待技术问题修复后再恢复可用。

### Sora 媒体签名 URL（功能恢复后可选）

当配置 `gateway.sora_media_signing_key` 且 `gateway.sora_media_signed_url_ttl_seconds > 0` 时，网关会将 Sora 输出的媒体地址改写为临时签名 URL（`/sora/media-signed/...`）。这样无需 API Key 即可在浏览器中直接访问，且具备过期控制与防篡改能力（签名包含 path + query）。

```yaml
gateway:
  # /sora/media 是否强制要求 API Key（默认 false）
  sora_media_require_api_key: false
  # 媒体临时签名密钥（为空则禁用签名）
  sora_media_signing_key: "your-signing-key"
  # 临时签名 URL 有效期（秒）
  sora_media_signed_url_ttl_seconds: 900
```

> 若未配置签名密钥，`/sora/media-signed` 将返回 503。  
> 如需更严格的访问控制，可将 `sora_media_require_api_key` 设为 true，仅允许携带 API Key 的 `/sora/media` 访问。

访问策略说明：
- `/sora/media`：内部调用或客户端携带 API Key 才能下载
- `/sora/media-signed`：外部可访问，但有签名 + 过期控制

`config.yaml` 还支持以下安全相关配置：

- `cors.allowed_origins` 配置 CORS 白名单
- `security.url_allowlist` 配置上游/价格数据/CRS 主机白名单
- `security.url_allowlist.enabled` 可关闭 URL 校验（慎用）
- `security.url_allowlist.allow_insecure_http` 关闭校验时允许 HTTP URL
- `security.url_allowlist.allow_private_hosts` 允许私有/本地 IP 地址
- `security.response_headers.enabled` 可启用可配置响应头过滤（关闭时使用默认白名单）
- `security.csp` 配置 Content-Security-Policy
- `billing.circuit_breaker` 计费异常时 fail-closed
- `server.trusted_proxies` 启用可信代理解析 X-Forwarded-For
- `turnstile.required` 在 release 模式强制启用 Turnstile

**网关防御纵深建议（重点）**

- `gateway.upstream_response_read_max_bytes`：限制非流式上游响应读取大小（默认 `8MB`），用于防止异常响应导致内存放大。
- `gateway.proxy_probe_response_read_max_bytes`：限制代理探测响应读取大小（默认 `1MB`）。
- `gateway.gemini_debug_response_headers`：默认 `false`，仅在排障时短时开启，避免高频请求日志开销。
- `/auth/register`、`/auth/login`、`/auth/login/2fa`、`/auth/send-verify-code` 已提供服务端兜底限流（Redis 故障时 fail-close）。
- 推荐将 WAF/CDN 作为第一层防护，服务端限流与响应读取上限作为第二层兜底；两层同时保留，避免旁路流量与误配置风险。

**⚠️ 安全警告：HTTP URL 配置**

当 `security.url_allowlist.enabled=false` 时，系统默认执行最小 URL 校验，**拒绝 HTTP URL**，仅允许 HTTPS。要允许 HTTP URL（例如用于开发或内网测试），必须显式设置：

```yaml
security:
  url_allowlist:
    enabled: false                # 禁用白名单检查
    allow_insecure_http: true     # 允许 HTTP URL（⚠️ 不安全）
```

**或通过环境变量：**

```bash
SECURITY_URL_ALLOWLIST_ENABLED=false
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP=true
```

**允许 HTTP 的风险：**
- API 密钥和数据以**明文传输**（可被截获）
- 易受**中间人攻击 (MITM)**
- **不适合生产环境**

**适用场景：**
- ✅ 开发/测试环境的本地服务器（http://localhost）
- ✅ 内网可信端点
- ✅ 获取 HTTPS 前测试账号连通性
- ❌ 生产环境（仅使用 HTTPS）

**未设置此项时的错误示例：**
```
Invalid base URL: invalid url scheme: http
```

如关闭 URL 校验或响应头过滤，请加强网络层防护：
- 出站访问白名单限制上游域名/IP
- 阻断私网/回环/链路本地地址
- 强制仅允许 TLS 出站
- 在反向代理层移除敏感响应头

#### 重要：创建管理员账号

初始管理员账号只能通过 setup 向导创建（首次启动时访问 `http://<host>:8080`）。`config.yaml` 中的 `default.admin_email` / `default.admin_password` 字段不会被用来创建管理员，它们只是出于历史原因保留在模板里。

由于上面第 5 步预先创建了 `config.yaml`，setup 向导在首次启动时会被跳过：服务检测到 config 已存在，会直接进入正常模式，此时 `users` 表为空，首次登录会返回 `invalid email or password`。

创建管理员的两种方式：

1. 推荐让向导自动生成 `config.yaml`：跳过上面的第 5 步（不要执行 `cp`）。直接运行 `./sub2api`，访问 `http://localhost:8080`，向导会引导你完成数据库、Redis 和管理员账号配置，并自动写出 `config.yaml`。

2. 如果你已经创建了 `config.yaml`：首次启动前先把它临时移走以触发向导，完成后再恢复：
   ```bash
   mv config.yaml config.yaml.bak
   ./sub2api        # 向导在 http://localhost:8080 启动，并生成新的 config.yaml
   # 向导完成后 Ctrl+C 停服，再恢复你的配置：
   mv config.yaml.bak config.yaml
   ./sub2api        # 重启进入正常模式，用刚创建的管理员登录
   ```

```bash
# 6. 运行应用
./sub2api
```

#### HTTP/2 (h2c) 与 HTTP/1.1 回退

后端明文端口默认支持 h2c，并保留 HTTP/1.1 回退用于 WebSocket 与旧客户端。浏览器通常不支持 h2c，性能收益主要在反向代理或内网链路。

**反向代理示例（Caddy）：**

```caddyfile
transport http {
	versions h2c h1
}
```

**验证：**

```bash
# h2c 先验模式
curl --http2-prior-knowledge -I http://localhost:8080/health
# HTTP/1.1 回退
curl --http1.1 -I http://localhost:8080/health
# WebSocket 回退验证（需管理员 token）
websocat -H="Sec-WebSocket-Protocol: sub2api-admin, jwt.<ADMIN_TOKEN>" ws://localhost:8080/api/v1/admin/ops/ws/qps
```
