# 边缘与 HTTP 入口安全

TokenRouter 支持长时间运行的 SSE 和 WebSocket 请求。入口保护不能依赖响应 `WriteTimeout`，因为写超时会终止正常的长耗时生成和流式响应。

本文拥有应用与反向代理入口限制、可信客户端 IP 解析、流式传输和 DDoS 责任边界。它不能替代云厂商防火墙或 CDN 策略，也不定义已认证账号的并发和计费限速。

## 章节导航

- [应用默认值](#http_ingress_limits)：修改 HTTP/H2C 限制或请求体分类时读取。
- [固定窗口限流](#fixed_window_rate_limits)：修改 Redis 计数、TTL、故障策略或限流响应时读取。
- [可信客户端 IP](#可信客户端-ip)：修改代理信任、IP ACL 或运行时安全设置时读取。
- [Nginx 基线](#nginx-基线)：使用 Nginx 作为边缘入口时读取。
- [Caddy 与 CDN](#caddy-与-cdn)：修改仓库内 Caddyfile 或接入 CDN 前读取。
- [DDoS 边界](#ddos-边界)：判断防护应位于应用还是网络边缘时读取。

<a id="http_ingress_limits"></a>
## 应用默认值

- `server.max_header_bytes: 65536` 把 HTTP/1 请求头限制为 64 KiB，Go 会把它映射为对应的 HTTP/2 Header List 限制。
- `server.read_header_timeout: 10` 限制慢请求头攻击，不限制请求处理时间或响应流时长。
- `server.max_request_body_size: 268435456` 是绝对的 256 MiB 安全上限。
- `gateway.max_body_size: 268435456` 继续供多模态、Gemini、图片、视频和批量图片端点使用。
- `gateway.text_max_body_size: 33554432` 把已知纯文本 `/embeddings` 和 `/alpha/search` 端点限制为 32 MiB。
- H2C 默认每条连接最多 50 个并发流，连接上传窗口为 2 MiB，单流上传窗口为 512 KiB。
- 进程内按可信客户端 IP 限制无效凭据滥用，IPv6 聚合到 `/64`：60 秒内允许 120 次失败，超过后封禁 60 秒。这只是单实例安全网，多实例强制限制仍应由负载均衡器、CDN 或 WAF 完成。

不要增加全应用共用的请求信号量，因为一个 SSE 请求可能正常占用它数分钟。连接和未认证请求限制应放在边缘；已认证用户或 API Key 的并发仍由应用负责。

<a id="fixed_window_rate_limits"></a>
## 固定窗口限流

`infra/redis.FixedWindowLimiter` 通过 Lua 原子递增，首次计数设置 TTL，并修复已有计数键缺失 TTL 的情况；后续请求不延长窗口。应用仍使用 `rate_limit:` 前缀。超限时尽力读取剩余 PTTL，查询失败时回退到完整窗口，HTTP 层继续向上取整输出 `Retry-After`。

`server/middleware.RateLimiter` 通过只返回标量结果的计数接口适配 HTTP，拥有客户端 IP 维度、故障策略和 429 格式。公开高风险认证路由显式 fail-close；面板限流 fail-open，认证请求按用户 ID 分桶，公开请求只按可公开路由的客户端 IP 分桶。设置缓存、管理员豁免和缺少 Redis 时的面板放行保持原有语义。计数器当前由既有路由装配创建，不由 HTTP 中间件构造数据库客户端。

<a id="trusted_client_ip"></a>
## 可信客户端 IP

地址解析和请求快照由 `server/clientip` 拥有，旧 `pkg/ip` 的 HTTP 入口委托它；纯 IP/CIDR 编译与匹配由 `pkg/ipmatch` 提供。Key 的黑白名单裁决仍由旧调用链拥有，不能把匹配成功直接当作授权。

为兼容升级，`security.trust_forwarded_ip_for_api_key_acl` 默认开启。开启后，原始转发请求头接管日志与安全敏感路径的客户端 IP 解析。`security.forwarded_client_ip_headers` 中的自定义请求头按配置顺序检查，优先于内置的 `CF-Connecting-IP`、`X-Real-IP` 和 `X-Forwarded-For` 回退。请求头名称不区分大小写，加载时会规范化并去重，最多允许 16 个唯一且有效的 HTTP 字段名。请求头值必须包含 IP 字面量；支持逗号分隔，跳过无效项，并优先选择公网地址而不是私网回退地址。

该列表可以通过 YAML 或逗号分隔的 `SECURITY_FORWARDED_CLIENT_IP_HEADERS` 环境变量提供；显式空环境变量会清除 YAML 值。管理后台安全设置也可以编辑它，并在无需重启的情况下更新运行状态。每个请求会同时快照开关与请求头列表，不能在同一次请求中混用新旧设置。开关关闭时完全忽略自定义请求头，此时以 Gin 的 `server.trusted_proxies` 链为准，只能配置直接连接 TokenRouter 的准确 CIDR 或 IP。显式空列表表示不信任任何转发客户端 IP。

首次升级到该模式时，只有在 `server.trusted_proxies` 未被显式配置的情况下，旧的 `false` 才会改成 `true`；已有明确代理策略的环境继续使用安全模式。新安装在数据库初始化期间持久化自定义请求头列表。旧安装会用 YAML 配置回填缺失的数据库值。隐藏迁移标记会阻止后续管理员修改被再次覆盖。设置读取失败或已持久化的自定义请求头列表格式错误时，进程会以无自定义请求头的可信代理模式安全关闭兼容路径。迁移写入失败时，本次进程仍使用计算后的模式，并在启动日志记录警告。

兼容接管模式会接受转发请求头而不校验直接对端，包括任何已配置的自定义请求头。开启时必须阻止外部直接访问源站。CDN 部署应通过防火墙只允许 CDN 或负载均衡器连接源站，并由该代理覆盖每个可信客户端 IP 请求头，而不是在不可信客户端值后追加。

同主机代理示例：

```yaml
server:
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128
```

## Nginx 基线

在 `http` 块中定义共享区域。限速值必须根据实际合法流量调整；下面的值只是保守起点，不是通用容量目标。

```nginx
limit_conn_zone $binary_remote_addr zone=sub2api_conn:20m;
limit_req_zone  $binary_remote_addr zone=sub2api_auth:20m rate=5r/s;
limit_req_zone  $binary_remote_addr zone=sub2api_api:40m rate=30r/s;
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
    limit_conn sub2api_conn 40;

    location ~ ^/(auth|api/auth)/ {
        limit_req zone=sub2api_auth burst=10 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location ~ ^/(v1/)?(embeddings|alpha/search)$ {
        client_max_body_size 32m;
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location / {
        limit_req zone=sub2api_api burst=60 nodelay;
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

如果在 `http` 块中启用 Nginx gzip，必须从 `gzip_types` 排除 `text/event-stream`，也不能为 TokenRouter 使用 `gzip_types *`。上面的 `proxy_buffering off` 会关闭代理缓冲，但不会关闭 gzip 响应过滤器。普通响应应使用明确列表：

```nginx
gzip on;
gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;
```

如果共享全局配置无法按内容类型排除 SSE，应在提供流式 API 的 location 中设置 `gzip off;`，同时保留 Web 管理界面和静态资源的 gzip。

除非 Nginx 的 Real IP 处理只信任明确的代理 CIDR，否则不要使用入站 `$http_x_forwarded_for` 值。

## Caddy 与 CDN

仓库内 `deploy/Caddyfile` 设置了 64 KiB 请求头上限、10 秒请求头超时和 256 MiB 绝对请求体上限，并根据 TCP 对端覆盖转发地址，因此它是客户端直连 Caddy 的基线。Caddy 位于 CDN 后方时，不能原样使用其中的 `{remote_host}` 转发行；否则所有客户端都会被归因到 CDN 出口地址，使拒绝聚合和无效认证限速错误地作用于互不相关的用户。

仓库内 Caddy 配置没有设置 `flush_interval`，让 Caddy 自动刷新 `text/event-stream` 响应，同时把客户端取消向上游传播。不要全局设置它：正值会增加流式延迟，而 Caddy 2.6.2 的特殊 `-1` 模式还会让反向代理请求在客户端断开后继续执行。配置使用明确的响应内容类型列表进行压缩，不能替换为 `text/*` 或简写 `encode gzip zstd`，因为二者都会匹配 `text/event-stream`，可能缓冲 SSE 直到响应结束。流式响应应保持不压缩，Web 管理界面、JSON 和静态资源仍可压缩。

CDN 部署应先通过防火墙限制源站，只允许当前 CDN 出口 CIDR 连接；再把这些准确网段配置为 Caddy 可信代理，并根据 Caddy 解析后的 `{client_ip}` 生成上游请求头。例如：

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

必须把示例网段替换为 CDN 已发布且自动维护的出口网段。只有在源站直连被阻止且 Caddy 只信任这些 TCP 对端时，`CF-Connecting-IP` 才是安全的。TokenRouter 的 `server.trusted_proxies` 应配置为 Caddy 地址或私有子网，使应用只接受 Caddy 重写的请求头。

Caddy 核心不提供通用请求限速器，应使用可信 CDN/WAF、受支持的限速模块或宿主机防火墙控制。

在 CDN/WAF 上，应在流量到达源站前配置连接数限制、请求头和请求体限制、机器人挑战，以及按 IP 或 ASN 的速率限制。源站只允许 CDN 出口 CIDR 或私有负载均衡器进入，不要把应用端口直接暴露到公网。

## DDoS 边界

应用检查只能减少连接到达 Go 进程后的放大效应，无法吸收容量型攻击、TLS 洪泛、带宽饱和或大规模分布式来源。这些威胁需要上游网络容量、CDN/WAF 过滤、云厂商防火墙规则和源站隔离。拒绝风暴期间应避免高基数指标或每请求数据库安全日志。

相关文档：[HTTP 接口边界](../interfaces/http_api.md)、[配置边界](../interfaces/configuration.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[可观测性与数据生命周期](observability_and_data_lifecycle.md)和[运维目录](index.md)。
