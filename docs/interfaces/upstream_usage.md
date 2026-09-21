# API Key 上游用量查询

本文定义 `type=apikey` 账号的上游用量查询，以及国产供应商可选的周期监控。管理员手动查询是纯展示能力，不参与 TokenRouter 调度、自动暂停、账号倍率、本地配额或结算；只有显式开启的 CN 周期监控可以写统一快照并按余额形成临时停调。`type=bedrock` 不在范围内，OAuth/Setup Token 的官方用量窗口仍由 `account.OAuthUsageService` 独立维护。

## 配置

账号 `extra.upstream_usage_query` 只保存非敏感配置：

```json
{
  "enabled": true,
  "adapter": "sub2api",
  "base_url": "https://gateway.example.com"
}
```

普通 API Key 账号缺少对象时按 `enabled=true`、`adapter=sub2api` 处理，根地址复用账号现有 API Base URL。只有显式 `enabled=false` 才关闭查询。管理员可选的 `adapter` 只有 `sub2api`、`new_api` 或 `zivv`；Kimi、Zhipu、DeepSeek 忽略该字段并根据平台与 `account_mode` 选择固定内置适配器。`base_url` 只能覆盖查询根地址，不能携带用户信息、查询串或片段。后端继续使用现有 HTTPS、allowlist、私网地址和 URL 格式校验。

API Key 永远从账号 `credentials` 读取。它不能写进 `extra`、接口响应、审计请求体、浏览器缓存或日志；用户也不能配置任意路径、方法、Header 模板或脚本。

New API 钱包若需要用户级认证，可在 `credentials` 中保存
`new_api_user_access_token`（敏感字段，只写入不回显）和可选的
`new_api_user_id`。前者只用于钱包查询，不能参与账号转发；后端响应只返回
`credentials_status.has_new_api_user_access_token`。

## 适配器

### Sub2API

严格请求 `GET /v1/usage`，携带 `Authorization: Bearer <api_key>`。`quota_limited` 归一化为 Key 总限额和 `rate_limits`；`unrestricted` 归一化为钱包余额或订阅。订阅的日、周、月使用量、限额、重置时间、套餐和到期时间会进入 `subscription.limits`。上游 `remaining=-1` 只在适配器内部识别为 `unlimited=true`，不会传给前端。钱包出现负余额时保留真实负数；周期限额的使用量和限额必须是有限非负数，且响应内的剩余值一致。

### New API

先读取 `/api/status` 的显示配置，再严格请求 API Key 专用的 `/api/usage/token/`，把 `total_granted`、`total_used` 和 `total_available` 归一化为当前 Key 的 `limits` 与 `subscription`，不写入结果的 `balance`。使用 `quota_display_type`、`quota_per_unit` 和可选 `usd_exchange_rate` 换算为 `USD`、`CNY` 或 `TOKENS`。`expires_at` 转为 UTC 到期时间，`unlimited_quota=true` 归一化为 `subscription.unlimited=true`，即使上游同时返回整数溢出的负额度也忽略这些字段，不向前端传递哨兵值。

钱包余额按以下固定顺序查询：若 token 响应包含 fork 扩展的 `user_balance_display`/`user_balance`，直接使用；否则优先使用配置的用户访问令牌请求 `/api/user/self`（可带固定的 `New-Api-User` 用户 ID），只把当前 `quota` 归一化为钱包 `remaining`，不把生命周期 `used_quota` 拼成虚构的钱包总额；最后尝试允许 API Key 访问的 `/user/balance`，解析 `balance_infos[].total_balance`。钱包余额才进入结果的 `balance`，因此即使 Key 是无限量，也不会把 `100000000` 或整数溢出值显示成余额。官方 New API 未开放 API Key 钱包端点且未配置用户访问令牌时，返回 `UPSTREAM_USAGE_WALLET_UNAVAILABLE`，不降级为 token quota。

不再请求用户级 `/v1/dashboard/billing/subscription` 或 `/v1/dashboard/billing/usage`，避免把全局额度或无限量哨兵误当作钱包；`/api/status` 失败时使用 New API 默认的 `500000` quota/单位，仅影响内部 quota 换算，不阻断钱包/Token 查询。

### Zivv

严格请求 `GET /v1/user/balance`，携带 `Authorization: Bearer <api_key>`。响应中的 `balance` 是钱包剩余余额，`total_used` 是累计已用金额；`key_limit`/`key_used` 归一化为 Key 限额，`key_limit=0` 表示不限量，`plan_name` 进入订阅计划展示。`currency` 目前支持 `USD`、`CNY` 和 `TOKENS`。Zivv 的公开开发者文档说明余额位于控制台钱包页面，适配器使用其前端生成的固定余额接口，不请求任意路径或脚本。参见 [Zivv 计费说明](https://docs.zivv.pro/billing/overview) 与 [API 端点](https://docs.zivv.pro/reference/endpoints)。

### 国产供应商

国产供应商适配器由账号身份自动选择，不能在管理表单中改成其它协议：

| 平台与模式 | 内部适配器 | 固定只读端点 | 归一化结果 |
| --- | --- | --- | --- |
| Kimi payg | `kimi_balance` | `/v1/users/me/balance` | CNY `balance` |
| Kimi coding | `kimi_coding` | `/v1/usages` | `PERCENT` 周期限额 |
| Zhipu coding | `zhipu_coding` | `/api/monitor/usage/quota/limit` | `PERCENT` 周期限额 |
| DeepSeek payg | `deepseek_balance` | `/user/balance` | 多币种 `balances[]`、主 `balance` 和 `available` |

Zhipu payg 没有公开余额协议，DeepSeek coding 也不是合法账号组合，因此查询明确返回不支持且不发送请求。Kimi/Zhipu 的 coding 周期把使用百分比归一化为上限 `100`、已用百分比和剩余百分比；DeepSeek 保留全部合法币种余额，任何一个币种仍高于监控阈值时都不会因另一个低余额币种停调。四个适配器只解析供应商固定 JSON 响应，不执行脚本、不接受自定义方法/路径，也不直接写数据库。

适配器拒绝 HTTP 非成功、认证失败、限流、超时、重定向、超大响应体、缺字段或不一致数值。选择的适配器失败时不会自动回退到另一个协议，也不会修改账号配置。

<a id="native_usage_adapters"></a>
## 原生查询与账号编排

Sub2API、New API、Zivv 的固定查询及归一化位于 `upstream/usageprovider`，Kimi、Zhipu、DeepSeek 分别进入对应的 `upstream` 包。`account/provider.UpstreamUsageExecution` 持有唯一适配器注册表，`NewUpstreamUsageHTTPExecution` 从原生账号记录构造凭据、代理、TLS 和 Header 技术快照。app 只投影出站配置与共享传输端口，直接绑定唯一 `account.UpstreamUsageService`；旧 service 查询入口及旧管理员 HTTP 转接已删除。

`upstream/usageview` 保存归一化值、错误和验证规则，account 的旧值入口使用别名。`upstream/usagecontract.Request` 是本次查询的技术快照，敏感字段不参与 JSON 或普通字符串格式化；共享 `upstream/internal/usageclient` 保留固定读取上限、请求头覆盖顺序、状态映射和响应体关闭。原生包不读取账号仓储，不写健康、调度或资金。

Ollama 的固定设置页抓取、HTML 解析、Retry-After 及 Chat 思考字段补齐和输出上限处理进入 `upstream/ollama`。`account/provider.OllamaUsageFetcher` 仅将受控 Cookie、代理和并发参数交给共享 HTTP 池；app 直接构造唯一 `account.OllamaCloudUsageService`，由其拥有浏览器会话、分组、加密、singleflight、身份 CAS 与周期维护，HTTP 直接绑定原生 Handler。它仍是现有 OpenAI 兼容账号的一种能力，不新增独立账号平台。CN 的通用文本执行复用 Anthropic/OpenAI 协议链；平台资格与全局重试不进入用量适配器。

## 管理员接口

- `POST /api/v1/admin/accounts/:id/upstream-usage/query`
- `POST /api/v1/admin/accounts/upstream-usage/query/batch`，请求体为 `{ "account_ids": [1, 2] }`，最多 100 个正整数 ID。

成功结果在顶层包含 `account_id`、`adapter`、`provider`、UTC `observed_at`、`mode`、`unit`、`balance`、`balances`、`available`、`limits`、`subscription` 和 `expires_at`；未适用字段省略。New API 的 `balance` 是钱包余额，`limits`/`subscription` 是当前 Key 的配额信息；DeepSeek 的 `balances` 保存多币种钱包，coding 周期使用 `unit=PERCENT`。`mode` 为 `balance`、`quota`、`limits` 或 `subscription`。批量响应将成功结果和每个账号的结构化错误分开，单个账号失败不取消其它账号。

每次操作使用约 60 秒总超时、512 KiB 响应体上限、禁止重定向，并复用账号代理、TLS 指纹、Header Override 和 `HTTPUpstream`。查询前后重新读取账号；凭据、代理、Base URL、TLS 连接设置或规范化配置改变时返回 `UPSTREAM_USAGE_IDENTITY_CHANGED`。同一账号和配置指纹使用 singleflight，等待方可以独立取消；每个等待方取得独立结果副本。查询编排、身份复核、并发槽和指标由 `account.UpstreamUsageService` 唯一持有，app 直接绑定账号 Store 和 `account/provider.UpstreamUsageExecution`；后者将技术快照交给对应原生适配器。构造不启动后台任务；应用关闭会阻止新认领、取消并等待脱离 HTTP 等待方的共享查询，执行未结束时报告超时，不能提前宣布依赖已释放。

<a id="frontend_lifecycle"></a>
## 前端生命周期

API Key 账号（含 Kimi、Zhipu、DeepSeek）统一按上游余额/周期用量、本地今日统计、本地配额、查询按钮的顺序展示。本地统计与配额只在具有相应数据或配置时显示；上游查询失败、关闭或不支持不隐藏本地数据。内容组件隐藏内部查询按钮，由用量栏底部提供唯一入口，查询中禁用，失败后通过同一按钮重试。

展示、按钮及列表单次/批量查询共用资格：仅 API Key 支持，Zhipu 非 coding 模式没有余额端点，显式 `extra.upstream_usage_query.enabled=false` 时关闭，缺少配置时默认启用。不支持或关闭时显示对应提示并隐藏按钮；批量选择跳过这些账号，不把它们计为查询失败。

列表加载、滚动进入视口和自动刷新不会请求上游。管理员只能通过行内刷新按钮或批量操作触发手动查询；成功结果按管理员身份、账号 ID、`updated_at`、代理/Base URL、适配器和规范化配置写入 `sessionStorage` 五分钟，失败结果不缓存。强制刷新绕过缓存；账号保存、凭据/代理/Base URL/配置变化立即失效。该缓存只保存归一化结果，不保存任何凭据。存在有效 `extra.cn_usage_monitor_snapshot` 时，列表可以直接展示最近监控结果而不触发请求。

## 国产供应商周期监控

`gateway.cn_providers.monitor_enabled` 默认 `false`；启用后，`account.CNUsageMonitor` 只扫描 active、`type=apikey`、用量查询未关闭且具有固定适配器的 Kimi/Zhipu/DeepSeek 账号。首次探测等待一个完整周期，多实例通过共享 leader lock 保证同轮只有一个执行者；整轮有总预算，每个请求有独立超时，并发受配置限制，服务关闭会取消当前轮并等待退出；Stop 后不能再次启动，存储或执行端口未响应取消时报告未完成，不提前关闭共享依赖。

成功或失败状态统一保存到 `extra.cn_usage_monitor_snapshot`。快照包含版本、适配器、完整查询身份 hash、最近成功的归一化数据、最近尝试时间和脱敏错误码；失败只更新尝试/错误，不抹掉最近成功数据。account/postgres 用账号 `updated_at` 做 CAS，并在同一 SQL 中写 scheduler outbox；凭据、平台、模式、协议、代理、Base URL、TLS 或查询配置变化会清理旧快照，读取方也必须重新计算身份 hash，不能消费旧身份数据。

余额模式低于 `balance_threshold` 时，监控写入带身份 hash 的临时不可调度原因；恢复到阈值以上时只清除同一身份创建的状态。健康暂停/恢复也以读取时的 `updated_at` 做条件写入，恢复还检查原原因；管理员换凭据或其它健康写入后，旧观测不再覆盖新状态。保持原提交后尽力 outbox 语义，不把健康与 Redis 更新描述为跨系统原子操作。coding 的百分比窗口快照用于已有额度阈值与重置时间判断，不把百分比伪装成货币余额。官方域名可直接监控；自定义中继只有在启用 URL allowlist 且 host 命中 `security.url_allowlist.upstream_hosts` 时才允许后台自动访问。监控复用账号代理、TLS 指纹、受保护 Header Override 和 `UpstreamUsageService`，不新增 CN 专用 HTTP 管理接口。

旧的 `upstream_billing_probe` 是已移除的自动倍率探测能力，本功能不恢复它，也不写入旧快照或调度状态。
