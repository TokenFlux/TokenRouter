# 上游传输安全

本文描述 TokenRouter 到模型上游的代理、连接池、TLS 指纹、目标校验和直连回退边界。入站 HTTP 限制与可信代理解析由边缘安全专题拥有，本文只覆盖出站请求。

## 章节导航

- [代理生命周期](#代理生命周期)：修改代理协议、健康、过期或回退时读取。
- [连接池隔离](#连接池隔离)：修改 client cache、HTTP/2 或账号隔离时读取。
- [TLS 指纹路由](#tls-指纹路由)：修改 profile、router 或采集器时读取。
- [目标与重定向校验](#目标与重定向校验)：修改 base URL、DNS 或 SSRF 防护时读取。
- [Header 与凭据边界](#header-与凭据边界)：修改 override 或认证传递时读取。
- [诊断与降级](#诊断与降级)：排查代理、TLS、直连和上游失败时读取。

<a id="upstream_proxy_lifecycle"></a>
## 代理生命周期

代理支持 `http`、`https`、`socks5` 和 `socks5h`，可保存过期时间、健康状态和延迟结果。管理端测试与周期健康检查使用真实代理链，但不得把代理密码写入日志或响应。

代理到期后，维护服务按配置选择：

- `none`：保持账号原绑定，调度仍按不可用代理处理。
- `proxy`：沿显式 fallback proxy 链寻找未过期目标。
- `direct`：允许账号解除代理并转为直连。

fallback 链循环、全部过期或目标缺失时保留可诊断失败，不能无限递归。代理替换或解绑后必须失效受影响账号的调度快照和 HTTP client 缓存；`direct` 是明确配置的降级，不是任意代理错误后的自动绕过。

<a id="upstream_client_pool"></a>
## 连接池隔离

普通共享 HTTP 客户端、req 客户端和按账号隔离的上游池都由 `infra/httpclient` 实现，但保留各自的缓存命名空间与 key，不合并复用策略。proxy 解析与拨号、TLS 握手位于其技术子包。旧 `repository.NewHTTPUpstream` 将配置、平台和请求标记投影为每请求技术参数；OpenAI 的 H2 代理回退状态、Grok CLI 身份与窄范围 403 回退仍由这个旧适配层拥有。

`UpstreamPool.Do` 在请求失败时释放占用，在成功时将释放绑定到响应体关闭；重复关闭不会重复减少计数。每请求的重定向或 transport 包装通过客户端派生完成，不能修改缓存客户端。调用方仍必须关闭响应体，才能释放在途占用。

每次执行先复制客户端并设置本次 `CheckRedirect`，再交给 `PrepareClient` 做平台适配；适配器可以覆盖当前请求的重定向规则，但不能修改共享 transport。回调不进入缓存，传入 nil 时恢复默认重定向行为。因此，同一池中禁止重定向、公网逐跳校验和普通请求可以并发使用，各自策略不会随缓存预热顺序改变，底层连接仍按原隔离键复用。

HTTP client 池可按 `proxy`、`account` 或 `account_proxy` 隔离，并有最大条目、空闲过期和逐出策略。隔离键还包含 TLS profile 等传输身份，防止不同账号或指纹错误复用连接。池配置变化要关闭/逐出旧 transport，不能只修改后续 key。

普通与 TLS 指纹上游传输都显式限制 DNS/TCP 建连和 TLS 握手阶段，当前默认各为 10 秒；TCP keepalive 探测间隔为 30 秒。HTTP 代理保留调用方的建连拨号器，SOCKS5/SOCKS5H 因会覆盖 `Transport.DialContext`，其 forward dialer 必须自行携带同等上限并响应请求 context。`ResponseHeaderTimeout` 只约束建连后的响应头等待，不能替代这些阶段超时。

直连、HTTP 和 SOCKS 可以使用 TLS profile。HTTPS 代理存在单独 transport 限制；OpenAI 等路径可在代理不支持 HTTP/2 时使用受控的 HTTP/1 回退。任何回退都只改变传输协商，不改变目标 allowlist、认证和账号归属。

<a id="upstream_tls_routing"></a>
## TLS 指纹路由

TLS fingerprint profile 描述 ClientHello/HTTP 行为，账号可以直接绑定 profile，也可以绑定 router。Router 依据平台、请求和配置选择 profile、User-Agent 或 originator；结果进入连接池隔离键。配置缓存更新后需要跨实例失效，不能让同一账号长期使用不同规则版本。

TLS collector 可采集受控会话以建立或检查 profile。采集入口是管理员诊断面，不允许接收任意公网目标或把捕获的 Authorization/Cookie 作为普通样本保存。OAuth token/reset 等特殊请求可以使用专用 profile/UA，但仍遵守目标和代理校验。

## 目标与重定向校验

URL 格式、scheme、allowlist 与字面量地址策略由 `egress` 的纯校验实现拥有；DNS 查询由 `infra/httpclient` 执行，旧 urlvalidator 入口只做委托。策略与执行分开，不改变原先的解析时机或代理行为。

自定义 base URL 在转发和账号测试等使用入口至少经过格式与 scheme 校验。启用 `security.url_allowlist` 后，入口还要求目标命中对应 host allowlist，并按 `allow_private_hosts` 决定是否允许本地或私网字面量地址；关闭 allowlist 时只保留最小格式校验，HTTP 还必须由 `allow_insecure_http` 显式放行，启动日志会提示 SSRF 检查已关闭。

常规上游请求仅在 allowlist 已启用且 `allow_private_hosts=false` 时，由 HTTP client 在发起请求前解析目标 host，并对后续重定向重新执行解析后 IP 校验。Images URL 回填下载额外携带请求级公网限制：不论全局是否允许私网，都拒绝本地/私网字面量及解析结果，并在每次重定向上执行相同检查，同时保留共享客户端原有的重定向限制。普通请求不会继承此下载标记。当前校验与实际连接仍是分开的解析步骤，代理也可能自行解析，因此不能把它描述为绑定实际连接 IP 的完整 DNS rebinding 防护。

平台默认端点、管理员允许的兼容上游和对象/媒体下载可能使用不同 allowlist，但都不能直接信任上游返回的任意 URL。默认上游 host 包含 Kimi/Moonshot、Zhipu/Z.ai 和 DeepSeek 官方域名；CN 周期监控只对这些官方 host 直接运行，自定义中继即使可用于手动请求，也必须在 allowlist 已启用且显式命中时才能被后台周期访问。Grok 视频 content 等下载通过服务端凭据代理时仍需验证任务归属和最终目标。

## Header 与凭据边界

Header override 只对 Anthropic/OpenAI/Kimi/Zhipu/DeepSeek 的 API Key 账号，以及 Grok 的 API Key/OAuth 账号生效。保存时会规范化名称和值并拒绝重复或非法条目，读取旧数据时还会再次过滤。Authorization、API Key、Proxy-Authorization、Host、Cookie、会话隔离头、hop-by-hop 和 transport 控制头都在禁止名单中，不能通过账号字段覆盖。OpenAI 的 `x-codex-routing-hint` 也属于网关自有控制头：出站构造会先删除调用方与账号覆盖提供的所有大小写变体，再仅为 OAuth 请求按最终模型和有效服务层级生成，API Key 路径不得透传。

构建器通常先写入平台认证、客户端身份和会话头，再在末尾应用允许的 override；因此允许项可以有意覆盖 User-Agent 等内置头，而禁止项不会遮蔽真实凭据或固定会话身份。新增转发路径时必须复用同一套过滤与应用函数，不能直接遍历原始 credentials。

代理 URL、API Key、OAuth token、AWS/Google 凭据和 TLS 采集内容不得进入普通错误、Ops body 或前端公开设置。错误日志只记录代理/TLS/profile ID、目标 host、阶段和脱敏分类。

## 诊断与降级

排障顺序应区分 DNS/目标拒绝、代理连接、代理认证、TLS 握手、HTTP 协商、上游状态码和响应解析。代理健康测试成功不证明特定 TLS profile/目标可用；账号测试失败也不应立刻把共享代理永久判死。

直连回退只在代理策略允许时发生。发生回退时记录原代理、选择结果和调度失效；如果安全目标校验失败，不得通过换代理或关闭指纹绕过。

相关文档：[边缘与 HTTP 入口安全](edge_security.md)、[账号维护](account_maintenance.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)。
