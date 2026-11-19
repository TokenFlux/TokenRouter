# 上游传输安全

本文记录 TokenRouter 访问上游模型时的代理生命周期、连接池隔离、TLS 指纹路由、目标与重定向校验、Header 控制及直连回退规则。入站 HTTP 限制与可信代理见[边缘与 HTTP 入口安全](edge_security.md)。

## 章节导航

- [代理生命周期](#upstream_proxy_lifecycle)：修改代理协议、健康、过期或回退时读取。
- [连接池隔离](#upstream_client_pool)：修改 client cache、HTTP/2 或提供商隔离时读取。
- [TLS 指纹路由](#upstream_tls_routing)：修改 profile、router 或采集器时读取。
- [目标与重定向校验](#目标与重定向校验)：修改 base URL、DNS 或 SSRF 防护时读取。
- [Header 与凭据](#header-与凭据)：修改 override 或认证传递时读取。
- [诊断与降级](#诊断与降级)：排查代理、TLS、直连和上游失败时读取。

<a id="upstream_proxy_lifecycle"></a>
## 代理生命周期

代理管理与回退策略归 `egress`，支持 `http`、`https`、`socks5` 和 `socks5h`，记录过期时间、健康状态和延迟。

- 测试与导出：管理端测试与周期健康检查走真实代理链。普通展示和日志不输出代理密码；管理员显式备份时按 `provider/transfer` 格式导出凭据。导入支持同批按名称引用备用代理、部分成功、状态同步及导入后后台探测。
- 到期维护：构造时不启动，重复 `Start` 不增加扫描，`Stop` 后不可重启；停机时取消并等待当前扫描完成。

代理到期后按配置的回退策略处理：

| 策略 | 行为 |
| --- | --- |
| `none` | 保持原绑定，调度时按代理不可用处理 |
| `proxy` | 沿配置的 fallback proxy 链寻找未过期目标 |
| `direct` | 解除提供商代理绑定，改为直连 |

- 安全与失效约束：fallback 链成环、全部过期或目标缺失时返回可诊断错误，不无限递归。替换或解绑代理后须失效受影响提供商的调度快照和 HTTP client 缓存。`direct` 仅在显式配置时生效，代理报错不会自动绕过代理。
- 请求策略：`EgressPolicy` 封装单次请求的代理、TLS 身份、Header、目标及重定向规则（复制嵌套配置）；安全 Header 在执行入口应用，平台 Header 大小写及 Grok CLI/403 策略归 `upstream`。校验与实际连接仍各自解析 DNS。

<a id="upstream_client_pool"></a>
## 连接池隔离

共享 HTTP 客户端、浏览器伪装 req 客户端与按提供商隔离的上游池均位于 `infra/httpclient`，各自维护独立命名空间、隔离键和复用策略，不合并。常规 OAuth、OIDC Discovery、JWKS 与资源下载统一通过标准库 `GetClient` 或 `UpstreamPool` 发起；`imroc/req/v3` 共享池默认禁用跨请求 CookieJar，仅限 `claude.ai` Cookie 授权与 `chatgpt.com` 网页隐私接口两处 Cloudflare 浏览器端点使用，并由架构检查锁死文件白名单。OpenAI 代理 H2 回退状态归 `egress` 的 `TransportPolicy`，Grok CLI 身份与小范围可重放 403 回退归 `upstream/grok`。

- 占用释放：`UpstreamPool.Do` 在请求失败时立即释放占用，成功时将释放绑定至响应体 `Close`（重复关闭仅扣减一次），调用方必须关闭响应体。
- 请求级重定向隔离：每次执行先克隆缓存客户端、设置本次 `CheckRedirect`（`nil` 恢复默认）再交平台适配（可覆盖重定向但不得修改共享 transport）。回调不入缓存，禁重定向、逐跳公网校验和普通请求可按隔离键共用连接而不受预热顺序影响。
- 隔离键与逐出：连接池按 `proxy`、`provider` 或 `provider_proxy` 外加 TLS profile 等传输身份隔离，设有最大条目数、空闲过期和逐出策略。配置变更须关闭或逐出旧 transport，仅改后续隔离键不会释放已有连接。
- 建连超时：普通与 TLS 指纹传输默认限制 DNS/TCP 建连 10 秒、TLS 握手 10 秒、TCP keepalive 间隔 30 秒。SOCKS5/SOCKS5H 覆盖 `Transport.DialContext`，其 forward dialer 须施加相同上限并响应请求 context。`ResponseHeaderTimeout` 从建连完成后起算。
- HTTP/2 与空闲 PING：普通 HTTP/2 通过标准库 `http.Protocols` 和 `http.HTTP2Config` 配置；TLS 指纹连接经 `x/net/http2.Transport` 裸连接拨号并核验协商 ALPN 为 `h2`。两路径空闲 PING 间隔与响应超时均为 15 秒。
- 协议回退边界：直连、HTTP 与 SOCKS 均支持 TLS profile；HTTPS 代理存在独立 transport 限制。代理不支持 HTTP/2 时（如 OpenAI）可做受控 HTTP/1 回退，仅改变传输协商，不放宽目标 allowlist、认证或提供商归属。

<a id="upstream_tls_routing"></a>
## TLS 指纹路由

`egress` 持有 TLS Profile 与 Router 的唯一配置与缓存，写入缓存和返回运行时数据均复制切片、规则和可空字段，防止单次请求污染后续策略。

- 绑定与失效：提供商可直接绑定 profile 或绑定 router（按平台、请求和配置选出 profile、User-Agent 或 originator），匹配结果计入连接池隔离键，配置更新经 Redis 失效通知刷新缓存。
- 传输边界：调用方通过 `TLSSelection` 显式传入资格、直接模板与路由结果，传输适配层不读提供商也不自持缓存。OAuth token、reset 等特殊请求可使用专用 profile 和 UA，仍须遵守目标与代理校验。
- TLS 采集器：用于管理员受控诊断（建立或检查 profile），由 `egress` 管理短期会话、到期和记录上限。拒绝任意公网目标，捕获的 `Authorization` 和 `Cookie` 不作为普通样本保存。

## 目标与重定向校验

URL 格式、scheme、allowlist 和字面量地址校验归 `egress`，DNS 查询由 `infra/httpclient` 执行。

| 场景 | 校验规则 |
| --- | --- |
| 自定义 Base URL（转发、测试） | 始终执行格式与 scheme 校验；开启 `security.url_allowlist` 后须命中 host allowlist，按 `allow_private_hosts` 控制本地/私网字面量地址；关闭 allowlist 时仅保留格式校验（HTTP 仍需 `allow_insecure_http`）并在启动日志提示 SSRF 检查已关闭 |
| 普通上游请求 DNS 校验 | 仅在开启 allowlist 且 `allow_private_hosts=false` 时，发请求前与每次重定向均解析并校验目标 IP |
| Images URL 回填下载 | 带请求级公网限制，无论全局 `allow_private_hosts` 为何，初次请求与每次重定向均拒绝本地/私网字面量及解析 IP，同时保留原重定向次数限制；普通请求不继承此标记 |

- DNS rebinding 边界：校验与实际连接执行两次独立解析，且代理可能自行解析 DNS，因此不能完全防住 DNS rebinding。
- 上游域名与下载信任：默认官方 host 含 Kimi/Moonshot、Zhipu/Z.ai 和 DeepSeek。上游用量周期监控仅直连这些官方 host；自定义中继即使可用于手动请求，后台周期任务也仅在开启 allowlist 且明确命中时访问。上游返回的任意 URL 均不直接信任；服务端凭据代理下载（如 Grok 视频内容）须校验任务归属与最终目标。

## Header 与凭据

- 生效范围：Header override 仅对两类提供商生效：Anthropic、OpenAI、Kimi、Zhipu、DeepSeek、Jev 的 API Key 提供商，以及 Grok 的 API Key 和 OAuth 提供商。保存时规范化名称与值并拒绝重复或非法项，读取旧数据时再次过滤。
- 禁改名单：`Authorization`、API Key、`Proxy-Authorization`、`Host`、`Cookie`、会话隔离头、hop-by-hop 头和 transport 控制头均在禁止名单内，无法被覆盖。
- OpenAI 路由提示头：`x-codex-routing-hint` 由网关独占控制。构包时先清除调用方和提供商覆盖中任意大小写的该头，再仅对 OAuth 请求按最终模型和有效服务层级生成，API Key 路径不透传。
- 隔离与应用顺序：提供商资格判断归 `provider`，名称与值安全规则归 `egress`；读取覆写表返回独立副本且不改动共享缓存。构建器先写入平台认证、客户端身份与会话头，最后应用允许的 override（可覆盖 `User-Agent` 等内置头而不遮蔽认证或会话身份）。新转发路径须复用同一过滤和应用函数，禁止直接遍历原始 credentials。
- 日志脱敏：代理 URL、API Key、OAuth token、AWS/Google 凭据和 TLS 采集内容均不出现在普通错误、Ops body 或前端公开设置中；错误日志仅记代理/TLS/profile ID、目标 host、阶段及脱敏分类。
- OpenAI 代理断流隔离：`egress.ProxyStreamCircuit` 按代理 ID 维护有界隔离状态。同一复用连接短时间并发断流合并计 1 次；默认 1 分钟内 2 次独立失败触发 10 分钟隔离，单次成功即清零。网关仍决定适用提供商及首次无容量时的二次 fail-open；代理隔离与提供商永久禁用相互独立。

## 诊断与降级

排查出站故障按以下顺序逐层定位：

1. DNS 解析或目标安全校验被拒。
2. 代理建连失败。
3. 代理认证失败。
4. TLS 握手或指纹不匹配。
5. HTTP 协议协商（H2/H1）失败。
6. 上游状态码异常。
7. 响应解析失败。

- 排障边界：代理健康测试通过不代表特定 TLS profile 或目标可用；单个提供商测试失败不应直接将共享代理判为永久不可用。
- 回退原则：直连回退仅在代理策略允许时发生，并记录原代理、回退决策与调度失效；目标安全校验失败无法通过更换代理或关闭指纹绕过。

相关文档：[边缘与 HTTP 入口安全](edge_security.md)、[提供商维护](provider_maintenance.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)。
