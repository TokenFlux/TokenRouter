# 边缘与 HTTP 入口安全

TokenRouter 支持长时间运行的 SSE 和 WebSocket 请求，因此不能用响应 `WriteTimeout` 保护入口，否则会中断正常的长耗时流式响应。本文说明应用默认值、固定窗口限流、可信客户端 IP 解析、反向代理基线及 DDoS 防护分工。

## 章节导航

- [应用默认值](#http_ingress_limits)：HTTP/H2C 请求头、请求体上限与无效凭据防滥用。
- [固定窗口限流](#fixed_window_rate_limits)：Redis 固定窗口计数、故障策略与响应头。
- [可信客户端 IP](#trusted_client_ip)：转发头接管模式、可信代理链与 SSE 代理要求。
- [Nginx 基线](#nginx-基线)：Nginx 限速、流式转发与 gzip 排除规则。
- [Caddy 与 CDN](#caddy-与-cdn)：仓库 Caddyfile 约束与 CDN 出口网段配置。
- [DDoS 防护分工](#ddos-防护分工)：应用层与网络边缘的职责边界。

<a id="http_ingress_limits"></a>
## 应用默认值

| 配置项 / 机制 | 默认值 | 说明 |
| --- | --- | --- |
| `server.max_header_bytes` | `65536`（64 KiB） | 限制 HTTP/1 请求头并映射为 HTTP/2 Header List 上限 |
| `server.read_header_timeout_seconds` | `10` | 防慢请求头攻击，不限制请求处理或流响应时长 |
| `server.max_request_body_bytes` | `268435456`（256 MiB） | 全局绝对请求体安全上限 |
| `gateway.max_request_body_bytes` | `268435456`（256 MiB） | 多模态、Gemini、图片、视频与批量图片端点上限 |
| `gateway.text_max_request_body_bytes` | `33554432`（32 MiB） | 纯文本 `/embeddings` 与 `/alpha/search` 上限 |
| H2C 连接限制 | 50 并发流 / 2 MiB 连接窗口 / 512 KiB 单流窗口 | 通过 `x/net/http2.ConfigureServer` 单独应用 `server.h2c.idle_timeout_seconds`（HTTP/1 用 `server.idle_timeout_seconds`） |
| 无效凭据滥用限制 | 60 秒 120 次失败后封禁 60 秒 | 进程内按可信客户端 IP（IPv6 聚合 `/64`）生效 |

安全原则：不要加全应用共享的请求信号量，因为单个 SSE 请求可正常占用数分钟。连接与未认证流量限制放在网络边缘，已认证用户与 API Key 并发由应用层管理。

<a id="fixed_window_rate_limits"></a>
## 固定窗口限流

- 原子计数：`infra/redis.FixedWindowLimiter` 用 Lua 在 `rate_limit:` 前缀下原子递增，首次计数设 TTL 并自动修复缺失 TTL 的键，后续请求不续期。超限时读取 PTTL（失败回退完整窗口），HTTP 层向上取整输出 `Retry-After`。
- 路由故障策略：高风险公开认证路由手动设为 fail-close；面板限流设为 fail-open（无 Redis 时放行），认证请求按用户 ID 分桶，公开请求按可公网路由的客户端 IP 分桶，管理员豁免限流。

<a id="trusted_client_ip"></a>
## 可信客户端 IP

地址解析归 `server/clientip`，IP/CIDR 匹配归 `pkg/ipmatch`，API Key IP 黑白名单由 `apikey` 认证用例执行。

| 模式 | 生效条件 | 行为与安全要求 |
| --- | --- | --- |
| 转发头接管模式（默认） | `server.client_ip.trust_forwarded_headers=true` | 按序读取 `server.client_ip.forwarded_headers`（最多 16 个去重合法头名，不区分大小写），再回退 `CF-Connecting-IP`、`X-Real-IP`、`X-Forwarded-For`。逗号分隔值跳过非法项，公网 IP 优先于私网。不校验直接对端，因此必须阻止公网直连源站，且上游代理须整头覆盖而非追加。 |
| 严格可信代理模式 | `server.client_ip.trust_forwarded_headers=false` | 忽略自定义转发头，仅信任 `server.client_ip.trusted_proxies` 中直接连接 TokenRouter 的准确 IP/CIDR；空列表表示不信任任何转发 IP。 |

运行与升级细节：

- YAML 传 `[]` 可清空自定义转发头列表；管理后台安全设置支持热更新，单次请求内取开关与列表的同一版本快照。
- 首次升级仅在未配置 `trusted_proxies` 时将旧 `false` 转为 `true`；已显式配代理策略的环境维持严格模式。数据库设置读取失败或格式非法时，自动安全回退到无自定义头的可信代理模式。
- 文本 SSE 默认空闲 10 秒发送心跳（详见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)），反向代理必须及时透传，避免缓冲或误切空闲超时。

同主机代理配置示例：

```yaml
server:
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128
```

## Nginx 基线

以下保守起点需按实际合法流量调优；除非 Nginx Real IP 仅信任明确代理 CIDR，否则不要直接使用入站 `$http_x_forwarded_for`：

```nginx
limit_conn_zone $binary_remote_addr zone=tokenrouter_conn:20m;
limit_req_zone  $binary_remote_addr zone=tokenrouter_auth:20m rate=5r/s;
limit_req_zone  $binary_remote_addr zone=tokenrouter_api:40m rate=30r/s;
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name api.example.com;

    client_header_timeout 10s;
    client_max_body_size 256m;
    large_client_header_buffers 4 16k;
    limit_conn tokenrouter_conn 40;

    location ~ ^/(auth|api/auth|api/v1/auth)/ {
        limit_req zone=tokenrouter_auth burst=10 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location ~ ^/(v1/)?(embeddings|alpha/search)$ {
        client_max_body_size 32m;
        limit_req zone=tokenrouter_api burst=60 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location / {
        limit_req zone=tokenrouter_api burst=60 nodelay;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 1800s;
        proxy_send_timeout 1800s;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

开启 Nginx gzip 时必须排除 `text/event-stream`（`proxy_buffering off` 不会关闭 gzip 缓冲），禁止 `gzip_types *`；若共享全局配置无法排除，在流式 API `location` 显式设置 `gzip off;`：

```nginx
gzip on;
gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;
```

## Caddy 与 CDN

- 仓库 `deploy/Caddyfile` 预设 64 KiB 请求头、10 秒读头超时与 256 MiB 请求体上限，面向客户端直连 Caddy。若 Caddy 位于 CDN 后方，须将 `{remote_host}` 改为可信代理后的 `{client_ip}`，否则所有客户端会合并为 CDN 出口 IP 并误触发限速。
- 流式与压缩：不要全局设置 `flush_interval`（正值增加延迟，`-1` 在旧版 Caddy 会阻止客户端断连取消传播）；压缩规则必须显式列内容类型，避免 `text/*` 或简写 `encode gzip zstd` 缓冲 `text/event-stream`。
- CDN 部署需用防火墙限制源站仅放行 CDN 出口网段，并在 Caddy 声明 `trusted_proxies_strict`，同时把 TokenRouter `trusted_proxies` 设为 Caddy 地址或私网段：

```caddyfile
{
	servers {
		trusted_proxies static 192.0.2.0/24 2001:db8:1234::/48
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP X-Forwarded-For
	}
}

api.example.com {
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Real-IP {client_ip}
		header_up X-Forwarded-For {client_ip}
	}
}
```

Caddy 核心不内置通用限速，应由 CDN/WAF、限速模块或宿主机防火墙在入口前完成连接、包体、机器人挑战与 IP/ASN 限速。

## DDoS 防护分工

- 应用层职责：降低合法连接到达 Go 进程后的资源放大效应；拒绝风暴期间避免高基数指标，不逐请求写数据库安全日志。
- 网络边缘职责：容量型攻击、TLS 洪泛、带宽打满和大规模分布式来源必须由 CDN、WAF、云防火墙与源站隔离拦截，应用端口不直接暴露公网。

相关文档：[HTTP 接口](../interfaces/http_api.md)、[配置](../interfaces/configuration.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[可观测性与数据生命周期](observability_and_data_lifecycle.md)、[运维目录](index.md)。
