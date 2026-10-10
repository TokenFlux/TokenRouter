# 上游传输安全

本文描述 TokenRouter 访问模型上游时的代理、连接池、TLS 指纹、目标校验和直连回退规则。本文只覆盖出站请求；入站 HTTP 的限制和可信代理的解析，见[边缘与 HTTP 入口安全](edge_security.md)。

## 章节导航

- [代理生命周期](#代理生命周期)：修改代理协议、健康、过期或回退时读取。
- [连接池隔离](#连接池隔离)：修改 client cache、HTTP/2 或提供商隔离时读取。
- [TLS 指纹路由](#tls-指纹路由)：修改 profile、router 或采集器时读取。
- [目标与重定向校验](#目标与重定向校验)：修改 base URL、DNS 或 SSRF 防护时读取。
- [Header 与凭据](#header-与凭据)：修改 override 或认证的传递时读取。
- [诊断与降级](#诊断与降级)：排查代理、TLS、直连和上游失败时读取。

<a id="upstream_proxy_lifecycle"></a>
## 代理生命周期

代理管理和 fallback 规则在 egress，PostgreSQL 和 Redis 的适配层随模块一起；app 绑定 outbox writer 和同连接的提供商参与者。代理支持 `http`、`https`、`socks5` 和 `socks5h`，可以保存过期时间、健康状态和延迟结果。管理端的测试和周期健康检查使用真实的代理链；普通的展示和日志不输出代理密码，管理员明确选择备份时，按文件格式导出凭据。代理的导入导出规则由 `egress.ProxyTransfer` 负责，HTTP 绑定 `egress/httpapi` 的处理器，备份的 envelope 在 `provider/transfer` 里作为纯格式共用。导入支持同一批内按名称引用备用代理、部分成功、状态同步和导入后的探测，后台探测由 app 的同一个任务持有者接管。

代理的到期维护由 app 管理：构造时不启动，重复调用 Start 不会增加扫描，Stop 之后不再启动；关闭时取消并等待当前的扫描。代理到期后，维护服务按配置选择：

- `none`：提供商保持原来的绑定，调度时按代理不可用处理。
- `proxy`：沿着配置的 fallback proxy 链，寻找没有过期的目标。
- `direct`：允许提供商解除代理，改为直连。

fallback 链出现循环、全部过期或目标缺失时，返回可以诊断的失败，不会无限递归。替换或解绑代理之后，要让受影响提供商的调度快照和 HTTP client 缓存失效。`direct` 是明确配置的降级方式，任意的代理错误都不会自动绕过代理。

`EgressPolicy` 持有每个请求的代理、TLS 身份、Header，以及目标和重定向的规则，技术适配层据此构造 transport options。嵌套的配置都会复制，安全 Header 在执行入口应用；平台报文的 Header 大小写，以及 Grok CLI 和 403 的策略，由对应的 upstream 提供。校验和实际连接仍然各自解析 DNS。

<a id="upstream_client_pool"></a>
## 连接池隔离

普通的共享 HTTP 客户端、req 客户端和按提供商隔离的上游池，都由 `infra/httpclient` 实现，但各自保留缓存的命名空间和 key，复用策略不合并。代理的解析和拨号、TLS 握手在它的技术子包里。`app/http_transport.go` 整理配置，`gateway/provider/transport` 把平台和请求标记转成每个请求的技术参数；OpenAI 在代理上的 H2 回退状态和决策，只由 egress 的 TransportPolicy 持有，传输适配层只传入请求类别和技术观测；Grok CLI 身份和小范围的可重放 403 回退，由 `upstream/grok` 负责。

调用方直接使用 `infra/httpclient.UpstreamTransport` 这个技术接口。app 只构造一个 `gateway/provider/transport.Client`，Wire 把同一个实例绑定给各个使用方；客户端池和隔离键保持各自独立。

`UpstreamPool.Do` 在请求失败时释放占用；成功时，把释放绑定到响应体的关闭上，重复关闭只减少一次计数。每个请求的重定向或 transport 包装，通过派生客户端完成，缓存里的客户端保持不变。调用方仍然要关闭响应体，才能释放在途的占用。

每次执行都先复制一份客户端，设置这一次的 `CheckRedirect`，再交给 `PrepareClient` 做平台适配；适配器可以覆盖当前请求的重定向规则，但不能修改共享的 transport。回调不进入缓存，传入 nil 时恢复默认的重定向行为。所以同一个池里，禁止重定向的请求、逐跳做公网校验的请求和普通请求可以同时使用，各自的策略不会随缓存预热的顺序变化，底层连接仍按隔离键复用。

HTTP client 池可以按 `proxy`、`provider` 或 `provider_proxy` 隔离，有最大条目数、空闲过期和逐出策略。隔离键还包括 TLS profile 等传输身份，不同的提供商或指纹不会错误地复用连接。池的配置变化时，要关闭或逐出旧的 transport；只修改之后的 key，已有的连接不会被释放。

普通的和 TLS 指纹的上游传输，都明确限制 DNS 和 TCP 建连、TLS 握手的时间，目前默认各 10 秒；TCP keepalive 探测的间隔是 30 秒。HTTP 代理使用调用方的建连拨号器；SOCKS5 和 SOCKS5H 会覆盖 `Transport.DialContext`，所以它们的 forward dialer 需要自己带上同样的上限，并响应请求的 context。`ResponseHeaderTimeout` 从连接建立后开始计时，只约束等待响应头的时间，建连阶段由上面这些超时控制。

普通 HTTP/2 传输通过标准库 `http.Protocols` 和 `http.HTTP2Config` 配置协商与空闲 PING。TLS 指纹连接返回 uTLS 的 `ConnectionState` 类型，使用 `x/net/http2.Transport` 的裸连接拨号接口接入，并检查实际协商到的 ALPN 为 `h2`。两个路径的空闲 PING 间隔和响应超时均为 15 秒。

直连、HTTP 和 SOCKS 都可以使用 TLS profile。HTTPS 代理对 transport 有单独的限制；OpenAI 等路径在代理不支持 HTTP/2 时，可以使用受控的 HTTP/1 回退。任何回退都只改变传输协商，目标 allowlist、认证和提供商归属保持不变。

<a id="upstream_tls_routing"></a>
## TLS 指纹路由

egress 负责 TLS Profile 和 Router 的配置和缓存（只有一份）；写入缓存和返回运行时数据时，都会复制切片、规则和可空字段，单次请求无法修改之后请求的策略。TLS fingerprint profile 描述 ClientHello 和 HTTP 的行为，提供商可以直接绑定 profile，也可以绑定 router。Router 根据平台、请求和配置选择 profile、User-Agent 或 originator，结果计入连接池的隔离键。配置缓存更新后，需要跨实例失效，同一个提供商不能长期在不同的实例上使用不同版本的规则。

调用方用 `TLSSelection` 明确传入提供商的资格、直接模板和路由匹配结果；`egress/provider.TLSProfiles` 只把策略结果转成传输指纹，不读取提供商，也不保存缓存。app 直接管理模板服务的启停，授权 token 的专用选择复用同一个实例。

TLS collector 的短期会话、到期和记录上限，由 egress 负责；监听、证书和 ClientHello 的捕获在 provider 适配层，按管理员的请求开启。TLS collector 可以采集受控的会话，用于建立或检查 profile。采集入口是管理员的诊断功能，不接收任意的公网目标，捕获到的 Authorization 和 Cookie 也不会作为普通样本保存。OAuth token、reset 等特殊请求，可以使用专用的 profile 和 UA，但仍然遵守目标和代理的校验。

## 目标与重定向校验

URL 格式、scheme、allowlist 和字面量地址的策略，由 egress 里的纯校验实现负责；DNS 查询由 `infra/httpclient` 执行。策略和执行分开实现。

自定义的 base URL，在转发和提供商测试等使用的地方，至少要通过格式和 scheme 的校验。开启 `security.url_allowlist` 后，还要求目标命中对应的 host allowlist，并按 `allow_private_hosts` 决定是否允许本地或私网的字面量地址。关闭 allowlist 时，只保留最基本的格式校验，HTTP 还需要 `allow_insecure_http` 明确放行，启动日志会提示 SSRF 检查已关闭。

普通的上游请求，只有在开启了 allowlist、并且 `allow_private_hosts=false` 时，才由 HTTP client 在发请求之前解析目标 host，并对之后的每次重定向，重新校验解析出的 IP。Images URL 回填的下载，另外带有请求级的公网限制：不管全局是否允许私网，都拒绝本地和私网的字面量地址和解析结果，每次重定向都做同样的检查，同时保留共享客户端原有的重定向限制。普通请求不会继承这个下载标记。目前的校验和实际的连接是两次独立的解析，代理也可能自己解析，所以这套检查不能防住所有的 DNS rebinding。

平台的默认端点、管理员允许的兼容上游和对象、媒体的下载，可能使用不同的 allowlist，但上游返回的任意 URL 都不会被直接信任。默认的上游 host 包括 Kimi 和 Moonshot、Zhipu 和 Z.ai、DeepSeek 的官方域名；CN 周期监控只直接访问这些官方 host；自定义中继即使可以用于手动请求，也只有在开启了 allowlist、并且明确命中时，后台的周期任务才会访问它。Grok 视频 content 等下载，通过服务端的凭据代理时，仍然要验证任务的归属和最终的目标。

## Header 与凭据

Header override 只对两类提供商生效：Anthropic、OpenAI、Kimi、Zhipu、DeepSeek、Jev 的 API Key 提供商，以及 Grok 的 API Key 和 OAuth 提供商。保存时规范化名称和值，拒绝重复或非法的条目；读取旧数据时，还会再过滤一次。Authorization、API Key、Proxy-Authorization、Host、Cookie、会话隔离头、hop-by-hop 头和 transport 控制头，都在禁止名单里，提供商的字段覆盖不了它们。

OpenAI 的 `x-codex-routing-hint` 也是网关自己控制的头：构造出站请求时，先删除调用方和提供商覆盖里任意大小写的这个头，再只为 OAuth 请求，按最终的模型和有效的服务层级生成；API Key 路径不会透传它。

哪些提供商类型可以使用 override，由 provider 判断；名称和值的安全规则只在 egress 执行。每次取得的覆写表都是独立的值，读取时不更新共享提供商里的派生缓存，调用方的修改和并发读取都不会影响之后的请求。

构建器通常先写入平台认证、客户端身份和会话头，最后应用允许的 override；所以允许的项可以有意地覆盖 User-Agent 等内置头，被禁止的项则无法遮住真实的凭据或固定的会话身份。新增转发路径时，复用同一套过滤和应用函数，不要直接遍历原始的 credentials。

代理 URL、API Key、OAuth token、AWS 和 Google 的凭据、TLS 采集的内容，都不会出现在普通的错误、Ops body 或前端的公开设置里。错误日志只记录代理、TLS 和 profile 的 ID、目标 host、阶段和脱敏后的分类。

OpenAI 代理断流的隔离状态，只由 `egress.ProxyStreamCircuit` 维护，按代理 ID 隔离，并限制条目数量。同一个复用连接上短时间内的并发断流，合并计为一次；默认一分钟内两次独立的失败，触发十分钟的隔离，成功一次就清零。网关仍然决定哪些提供商适用，以及第一次调度没有容量后的第二次 fail-open；代理隔离和提供商的永久禁用是两回事。

## 诊断与降级

排查时，依次区分：DNS 或目标被拒绝、代理连接、代理认证、TLS 握手、HTTP 协商、上游状态码、响应解析。代理的健康测试成功，不代表特定的 TLS profile 或目标可用；提供商测试失败，也不应该立即把共享的代理判定为永久不可用。

直连回退只在代理策略允许时发生。回退时，记录原来的代理、选择的结果和调度的失效；安全的目标校验失败时，换代理或关闭指纹都绕不过去。

相关文档：[边缘与 HTTP 入口安全](edge_security.md)、[提供商维护](provider_maintenance.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)。
