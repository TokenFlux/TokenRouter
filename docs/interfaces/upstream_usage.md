# API Key 上游用量查询

本文定义 `type=apikey` 提供商的上游用量查询，以及国产供应商可选的周期监控。管理员手动查询只用于展示，和 TokenRouter 的调度、自动暂停、提供商倍率、本地配额和结算都无关；只有手动开启的 CN 周期监控，才会写入统一快照，并在余额过低时临时停调。`type=bedrock` 不在本文范围内；OAuth 和 Setup Token 的官方用量窗口由 `provider.OAuthUsageService` 单独维护。

## 配置

提供商的 `extra.upstream_usage_query` 只保存非敏感的配置：

```json
{
  "enabled": true,
  "adapter": "sub2api",
  "base_url": "https://gateway.example.com"
}
```

普通 API Key 提供商没有这个对象时，按 `enabled=true`、`adapter=sub2api` 处理，根地址使用提供商现有的 API Base URL。Jev 缺少这个对象时默认关闭查询，管理员可配置兼容中继的查询适配器。其他 API Key 提供商通过 `enabled=false` 关闭查询。管理员可以选择的 `adapter` 只有 `sub2api`、`new_api` 和 `zivv`；Kimi、Zhipu、DeepSeek 忽略这个字段，按平台和 `provider_mode` 选择固定的内置适配器。`base_url` 只能覆盖查询的根地址，不能带用户信息、查询串或片段。后端照常执行 HTTPS、allowlist、私网地址和 URL 格式的校验。

API Key 始终从提供商的 `credentials` 读取，不会出现在 `extra`、接口响应、审计请求体、浏览器缓存和日志里。用户也无法配置任意的路径、方法、Header 模板或脚本。

New API 的钱包需要用户级认证时，可以在 `credentials` 里保存 `new_api_user_access_token` 和可选的 `new_api_user_id`。这个访问令牌只用于查询钱包，不参与提供商转发，也不回显；响应里只返回 `credentials_status.has_new_api_user_access_token`。

## 适配器

### Sub2API

严格请求 `GET /v1/usage`，带 `Authorization: Bearer <api_key>`。`quota_limited` 归一化为 Key 的总限额和 `rate_limits`；`unrestricted` 归一化为钱包余额或订阅。订阅的日、周、月用量、限额、重置时间、套餐和到期时间放进 `subscription.limits`。上游的 `remaining=-1` 只在适配器内部识别为 `unlimited=true`，前端看不到这个哨兵值。钱包余额为负时，保留实际的负数；周期限额的用量和限额要求是有限的非负数，并且和响应里的剩余值一致。

### New API

先读取 `/api/status` 的显示配置，再严格请求 API Key 专用的 `/api/usage/token/`，把 `total_granted`、`total_used` 和 `total_available` 归一化为当前 Key 的 `limits` 和 `subscription`；这些值不写进结果的 `balance`。根据 `quota_display_type`、`quota_per_unit` 和可选的 `usd_exchange_rate`，换算成 `USD`、`CNY` 或 `TOKENS`。

`expires_at` 转成 UTC 的到期时间；`unlimited_quota=true` 归一化为 `subscription.unlimited=true`，这时上游同时返回的整数溢出的负额度会被忽略，前端看不到这些哨兵值。

钱包余额按固定顺序查询：

1. token 响应里有 fork 扩展的 `user_balance_display` 或 `user_balance` 时，直接使用。
2. 否则优先用配置的用户访问令牌请求 `/api/user/self`（可以带固定的 `New-Api-User` 用户 ID），只把当前的 `quota` 归一化为钱包的 `remaining`；生命周期的 `used_quota` 不参与计算，所以不会拼出一个虚构的钱包总额。
3. 最后尝试允许 API Key 访问的 `/user/balance`，解析 `balance_infos[].total_balance`。

只有钱包余额会写进结果的 `balance`，所以即使 Key 是无限额度，`100000000` 或整数溢出的值也不会被当成余额显示。官方 New API 没有开放 API Key 的钱包端点、也没有配置用户访问令牌时，返回 `UPSTREAM_USAGE_WALLET_UNAVAILABLE`，不会退而使用 token 的额度。

适配器不请求用户级的 `/v1/dashboard/billing/subscription` 和 `/v1/dashboard/billing/usage`，因为它们返回的全局额度和无限额度哨兵容易被误当成钱包余额。`/api/status` 失败时，使用 New API 默认的每单位 `500000` quota，这只影响内部的 quota 换算，钱包和 Token 查询照常进行。

### Zivv

严格请求 `GET /v1/user/balance`，带 `Authorization: Bearer <api_key>`。响应里的 `balance` 是钱包剩余余额，`total_used` 是累计已用金额；`key_limit` 和 `key_used` 归一化为 Key 的限额，`key_limit=0` 表示不限额，`plan_name` 用于展示订阅计划。`currency` 目前支持 `USD`、`CNY` 和 `TOKENS`。

Zivv 的公开开发者文档说明余额在控制台的钱包页面，适配器使用它前端生成的固定余额接口，不请求任意路径，也不执行脚本。参见 [Zivv 计费说明](https://docs.zivv.pro/billing/overview) 和 [API 端点](https://docs.zivv.pro/reference/endpoints)。

### 国产供应商

国产供应商的适配器由提供商身份自动选择，管理表单里无法改成其他协议：

| 平台和模式 | 内部适配器 | 固定的只读端点 | 归一化结果 |
| --- | --- | --- | --- |
| Kimi payg | `kimi_balance` | `/v1/users/me/balance` | CNY 的 `balance` |
| Kimi coding | `kimi_coding` | `/v1/usages` | `PERCENT` 周期限额 |
| Zhipu coding | `zhipu_coding` | `/api/monitor/usage/quota/limit` | `PERCENT` 周期限额 |
| DeepSeek payg | `deepseek_balance` | `/user/balance` | 多币种的 `balances[]`、主 `balance` 和 `available` |

Zhipu payg 没有公开的余额协议，DeepSeek coding 也不是合法的提供商组合，所以查询这两种时直接返回不支持，不发送请求。Kimi 和 Zhipu 的 coding 周期把使用百分比归一化为上限 `100`、已用百分比和剩余百分比。DeepSeek 保留所有合法币种的余额，只要还有一个币种高于监控阈值，就不会因为另一个币种余额低而停调。这四个适配器只解析供应商固定格式的 JSON 响应，不执行脚本，不接受自定义的方法或路径，也不直接写数据库。

适配器拒绝以下响应：HTTP 非成功、认证失败、限流、超时、重定向、响应体过大、缺字段或数值互相矛盾。选定的适配器失败时，不会自动换用另一个协议，也不会修改提供商配置。

<a id="native_usage_adapters"></a>
## 原生查询与提供商编排

Sub2API、New API、Zivv 的固定查询和归一化在 `upstream/usageprovider`，Kimi、Zhipu、DeepSeek 分别在对应的 `upstream` 包里。`provider/provider.UpstreamUsageExecution` 持有唯一的适配器注册表，`NewUpstreamUsageHTTPExecution` 根据提供商记录构造凭据、代理、TLS 和 Header 的技术快照。app 只整理出站配置和共享的传输接口，直接绑定一个 `provider.UpstreamUsageService`。

`upstream/usageview` 保存归一化后的值、错误和验证规则，provider 通过类型别名复用它们。`upstream/usagecontract.Request` 是一次查询的技术快照，其中的敏感字段不会出现在 JSON 或普通的字符串格式化结果里。共享的 `upstream/internal/usageclient` 负责固定的读取上限、请求头覆盖顺序、状态映射和关闭响应体。这些原生包不读取提供商仓储，也不写健康、调度或资金数据。

Ollama 的固定设置页抓取、HTML 解析、Retry-After、Chat 思考字段补齐和输出上限处理，在 `upstream/ollama`。`provider/provider.OllamaUsageFetcher` 只把受控的 Cookie、代理和并发参数交给共享的 HTTP 池；app 直接构造一个 `provider.OllamaCloudUsageService`，由它负责浏览器会话、分组、加密、singleflight、身份 CAS 和周期维护，HTTP 直接绑定它的 Handler。

用量查询是 OpenAI 兼容提供商的一项能力，没有对应的独立提供商平台。CN 的通用文本执行复用 Anthropic 和 OpenAI 的协议链；平台资格和全局重试与用量适配器无关。

## 管理员接口

- `POST /api/v1/admin/providers/:id/upstream-usage/query`
- `POST /api/v1/admin/providers/upstream-usage/query/batch`，请求体为 `{ "provider_ids": [1, 2] }`，最多 100 个正整数 ID。

成功结果的顶层包含 `provider_id`、`adapter`、`provider`、UTC 时间的 `observed_at`、`mode`、`unit`、`balance`、`balances`、`available`、`limits`、`subscription` 和 `expires_at`，不适用的字段省略。New API 的 `balance` 是钱包余额，`limits` 和 `subscription` 是当前 Key 的配额信息；DeepSeek 的 `balances` 保存多币种的钱包，coding 周期使用 `unit=PERCENT`。

`mode` 取值 `balance`、`quota`、`limits` 或 `subscription`。批量响应把成功结果和每个提供商的结构化错误分开返回，一个提供商失败不影响其他提供商。

每次操作的总超时约 60 秒，响应体上限 512 KiB，禁止重定向，并复用提供商的代理、TLS 指纹、Header Override 和 `HTTPUpstream`。查询前后各读取一次提供商；凭据、代理、Base URL、TLS 连接设置或规范化后的配置变了，就返回 `UPSTREAM_USAGE_IDENTITY_CHANGED`。同一个提供商和配置指纹的查询用 singleflight 合并，等待方可以各自取消，每个等待方拿到一份独立的结果副本。查询编排、身份复核、并发槽和指标只由 `provider.UpstreamUsageService` 负责，app 直接绑定提供商 Store 和 `provider/provider.UpstreamUsageExecution`，后者把技术快照交给对应的原生适配器。

手动查询的结果只保存在浏览器缓存里：提供商、Extra、调度快照、数据库和计费记录都不写入，所以查询失败不影响转发。上游查询有自己的并发槽，和网关的提供商调度槽分开计算。

构造时不启动后台任务。应用关闭时，拒绝新的认领，取消并等待已经脱离 HTTP 等待方的共享查询；执行还没结束时报告超时，依赖在这之前保持打开。

<a id="frontend_lifecycle"></a>
## 前端生命周期

API Key 提供商（包括 Kimi、Zhipu、DeepSeek）的用量栏，按以下顺序展示：上游余额或周期用量、本地今日统计、本地配额、查询按钮。本地统计和配额只在有数据或有配置时显示；上游查询失败、关闭或不支持时，本地数据照常显示。内容组件隐藏自己内部的查询按钮，用量栏底部提供唯一的查询入口，查询中禁用，失败后用同一个按钮重试。

展示、按钮、列表的单个和批量查询使用同一套资格判断。查询支持 API Key，Zhipu 非 coding 模式没有余额端点。`extra.upstream_usage_query.enabled=false` 时关闭查询。没有配置时 Jev 默认关闭，其他支持查询的提供商默认启用。不支持或已关闭时，上游查询区域隐藏，本地统计和配额照常显示。批量选择时跳过这些提供商。

列表加载、滚动进入视口和自动刷新都不会请求上游，管理员只能通过行内的刷新按钮或批量操作手动查询。成功的结果按管理员身份、提供商 ID、`updated_at`、代理和 Base URL、适配器和规范化后的配置，写进 `sessionStorage`，保存五分钟；失败的结果不缓存。强制刷新会绕过缓存；保存提供商，或者凭据、代理、Base URL、配置变化时，缓存立即失效。缓存里只有归一化后的结果，没有任何凭据。提供商有有效的 `extra.cn_usage_monitor_snapshot` 时，列表可以直接展示最近一次监控的结果，不发起请求。

## 国产供应商周期监控

`gateway.cn_providers.monitor_enabled` 默认是 `false`。开启后，`provider.CNUsageMonitor` 只扫描满足以下条件的 Kimi、Zhipu、DeepSeek 提供商：active、`type=apikey`、没有关闭用量查询、有固定的适配器。

- 第一次探测在一个完整周期之后执行。
- 多实例通过共享的 leader lock 保证每一轮只有一个执行者。
- 每轮有总预算，每个请求有单独的超时，并发数受配置限制。
- 服务关闭时取消当前这一轮并等待退出；Stop 之后无法再次启动；存储或执行接口不响应取消时，报告未完成，共享的依赖保持打开。

成功和失败的状态统一保存在 `extra.cn_usage_monitor_snapshot`。快照包括版本、适配器、完整的查询身份 hash、最近一次成功的归一化数据、最近一次尝试的时间和脱敏后的错误码；失败时只更新尝试时间和错误，最近一次成功的数据保留。provider/postgres 用提供商的 `updated_at` 做 CAS，并在同一条 SQL 里写 scheduler outbox。凭据、平台、模式、协议、代理、Base URL、TLS 或查询配置变化时，清掉旧快照；读取方也会重新计算身份 hash，旧身份的数据不会被使用。

余额模式下，余额低于 `balance_threshold` 时，监控写入一个带身份 hash 的临时不可调度原因；余额恢复到阈值以上时，只清除同一个身份创建的那个状态。健康状态的暂停和恢复，同样以读取时的 `updated_at` 做条件写入，恢复时还检查原因是否一致；管理员换了凭据或有其他健康写入之后，旧的观测结果无法覆盖新的状态。事务提交后尽力写 outbox，健康状态和 Redis 的更新不在同一个事务里。coding 模式的百分比窗口快照，用于已有的额度阈值和重置时间判断，它表示的是百分比，不是货币余额。

官方域名可以直接监控；自定义中继只有在开启 URL allowlist、并且 host 命中 `security.url_allowlist.upstream_hosts` 时，后台才会自动访问。监控复用提供商的代理、TLS 指纹、受保护的 Header Override 和 `UpstreamUsageService`，没有 CN 专用的 HTTP 管理接口。

旧的 `upstream_billing_probe` 是已经移除的自动倍率探测功能，本功能与它无关，不写它的旧快照，也不影响调度状态。
