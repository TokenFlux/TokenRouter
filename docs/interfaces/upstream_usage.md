# API Key 上游用量查询

本文定义 `type=apikey` 提供商的上游用量查询、钱包余额邮件提醒，以及国产供应商可选的周期监控。`type=bedrock` 不在本文范围内；OAuth 和 Setup Token 的官方用量窗口由 `provider.OAuthUsageService` 单独维护。

管理员手动查询仅用于展示，不影响调度、自动暂停、提供商倍率、本地配额和结算；只有手动开启的上游用量周期监控会写入统一快照，并在余额过低时临时停调。

## 配置

提供商的 `extra.upstream_usage_query` 只保存非敏感配置：

```json
{
  "enabled": true,
  "adapter": "sub2api",
  "base_url": "https://gateway.example.com"
}
```

不同提供商的默认行为和可选适配器如下：

| 提供商类型 | 缺省 `upstream_usage_query` 时 | 适配器与根地址规则 |
| --- | --- | --- |
| 普通 API Key 提供商 | `enabled=true`，`adapter=sub2api` | 可选 `sub2api`、`new_api`、`zivv`、`derouter`，可设 `enabled=false` 关闭 |
| Jev | 默认关闭 | 管理员可配置兼容中继的查询适配器 |
| Kimi、Zhipu、DeepSeek、OpenCode | 按平台和 `provider_mode` 确定 | 忽略 `adapter` 字段，使用固定内置适配器；当前模式无查询协议时有效 `enabled` 固定为 `false` |

- 查询根地址（`base_url`）：只能覆盖查询根地址，不能带用户信息、查询串或片段；后端照常校验 HTTPS、allowlist、私网地址和 URL 格式。留空时，`derouter` 用 `https://cf-api.derouter.ai`，`opencode_go` 用 `https://opencode.ai`，其他适配器复用转发地址。
- 凭据隔离：API Key 始终从 `credentials` 读取，不出现在 `extra`、接口响应、审计请求体、浏览器缓存和日志中。用户无法配置任意路径、方法、Header 模板或脚本。
- New API 钱包凭据：需要用户级认证时，可在 `credentials` 保存 `new_api_user_access_token` 和可选 `new_api_user_id`。该令牌仅用于查询钱包，不参与转发也不回显，响应只返回 `credentials_status.has_new_api_user_access_token`。

## 适配器

所有适配器均拒绝以下响应：HTTP 非成功、认证失败、限流、超时、重定向、响应体过大、缺字段或数值互相矛盾。选定适配器失败时不自动切换其他协议，也不修改提供商配置。

### Sub2API

严格请求 `GET /v1/usage`，携带 `Authorization: Bearer <api_key>`。

- `quota_limited` 归一化为 Key 总限额和 `rate_limits`；`unrestricted` 归一化为钱包余额或订阅，订阅的日、周、月用量、限额、重置时间、套餐和到期时间写入 `subscription.limits`。
- 上游 `remaining=-1` 仅在适配器内部识别为 `unlimited=true`，前端不接触哨兵值。钱包负余额保留实际负数；周期限额的用量和限额须为有限非负数，且与响应剩余值一致。

### New API

先读取 `/api/status` 的显示配置（失败时退回默认的每单位 `500000` quota，仅影响内部换算，钱包和 Token 查询照常进行），再严格请求 API Key 专用的 `/api/usage/token/`。

- Token 配额：`total_granted`、`total_used` 和 `total_available` 归一化为当前 Key 的 `limits` 和 `subscription`，不写入 `balance`。
- 币种换算：根据 `quota_display_type`、`quota_per_unit` 和可选 `usd_exchange_rate` 换算为 `USD`、`CNY` 或 `TOKENS`。
- 到期与无限额度：`expires_at` 转为 UTC 到期时间；`unlimited_quota=true` 归一化为 `subscription.unlimited=true`，同时忽略上游返回的整数溢出负额度。

钱包余额按以下固定顺序查询：

1. Token 响应包含 fork 扩展 `user_balance_display` 或 `user_balance` 时直接使用。
2. 否则优先用 `new_api_user_access_token` 请求 `/api/user/self`（可带固定 `New-Api-User` 用户 ID），仅将当前 `quota` 归一化为钱包 `remaining`；生命周期 `used_quota` 不参与计算，不虚构钱包总额。
3. 最后尝试允许 API Key 访问的 `/user/balance`，解析 `balance_infos[].total_balance`。

只有钱包余额写入 `balance`，无限额度 Key 的 `100000000` 或整数溢出值不会当作余额显示。未开放钱包端点且未配置用户访问令牌时，返回 `UPSTREAM_USAGE_WALLET_UNAVAILABLE`，不退而使用 token 额度。

适配器不请求用户级 `/v1/dashboard/billing/subscription` 和 `/v1/dashboard/billing/usage`，避免将全局额度或无限哨兵误作钱包余额。

### Zivv

严格请求 `GET /v1/user/balance`，携带 `Authorization: Bearer <api_key>`。响应的 `balance` 为钱包剩余余额，`total_used` 为累计已用金额。

`key_limit` 和 `key_used` 归一化为 Key 限额（`key_limit=0` 表示不限额），`plan_name` 展示订阅计划；`currency` 支持 `USD`、`CNY` 和 `TOKENS`。

适配器使用固定余额接口，不请求任意路径，也不执行脚本。参见 [Zivv 计费说明](https://docs.zivv.pro/billing/overview) 和 [API 端点](https://docs.zivv.pro/reference/endpoints)。

### derouter.ai Account Key

使用提供商现有 Account Key，以 `Authorization: Bearer <api_key>` 请求管理根地址 `GET /balance`。官方管理根地址为 `https://cf-api.derouter.ai`，与转发地址独立配置。

自定义查询根地址经相同 URL 安全校验，固定管理路径移除末尾版本段并保留部署前缀。本适配器支持 Account Key；Client Key 的 `/sub-key/balance` 需不同解析。

- 金额校验：响应中的 `balance`、`locked_balance` 和 `available` 接受十进制字符串或 JSON 数字，三者须全部存在且为有限数值；`locked_balance` 非负，`available` 须在金额比较容差内等于 `balance - locked_balance`（缺失、null 或不一致导致查询失败）。
- 归一化输出：结果为 `mode=balance`、`unit=USD`，`balance.remaining` 取 `available`（零余额和负余额按实返回），省略 `used` 和 `total`（锁定金额不代表历史消费）。协议来源：[derouter.ai 管理 API](https://derouter.ai/docs/api)。

### OpenCode Go

OpenCode 的 `go` 模式自动选择 `opencode_go`，读取固定 `/zen/go/v1/usage`，展示三个百分比窗口及重置时间；管理根地址可独立编辑，留空使用 `https://opencode.ai`。`payg` 模式无查询协议，查询保持关闭，手动查询直接返回不支持，不发请求。规则见 [OpenCode 上游](opencode_upstream.md)。

### 国产供应商

国产供应商适配器由提供商身份自动选择，管理表单不可改选其他协议：

| 平台和模式 | 内部适配器 | 固定的只读端点 | 归一化结果 |
| --- | --- | --- | --- |
| Kimi payg | `kimi_balance` | `/v1/users/me/balance` | CNY 的 `balance` |
| Kimi coding | `kimi_coding` | `/v1/usages` | `PERCENT` 周期限额 |
| Zhipu coding | `zhipu_coding` | `/api/monitor/usage/quota/limit` | `PERCENT` 周期限额 |
| DeepSeek payg | `deepseek_balance` | `/user/balance` | 多币种 `balances[]`、主 `balance` 和 `available` |

- 无协议组合：Zhipu payg 无公开余额协议，DeepSeek coding 非合法组合。两者有效配置保持关闭（保存的开关无法开启），手动查询直接返回不支持，不发请求。
- 归一化细节：Kimi 和 Zhipu 的 coding 周期归一化为上限 `100`、已用百分比和剩余百分比；DeepSeek 保留全部合法币种余额，只要任一币种高于监控阈值就不会因另一币种余额低而停调。
- 安全边界：四个适配器只解析固定格式 JSON，不执行脚本，不接受自定义方法或路径，不直接写库。

<a id="native_usage_adapters"></a>
## 原生查询与提供商编排

- 适配器归属：Sub2API、New API、Zivv、derouter.ai 的固定查询与归一化在 `upstream/usageprovider`，OpenCode、Kimi、Zhipu、DeepSeek 在各自 `upstream` 包。原生包不读取提供商仓储，也不写健康、调度或资金数据。
- 配置解析：各平台模式声明决定固定用量适配器，`provider.EffectiveUpstreamUsageConfig` 解析有效配置并为独立管理根地址适配器填入默认根地址，确保执行与身份复核访问同一目的地。
- 公共契约与客户端：`upstream/usageview` 保存归一化值、错误和校验规则；`upstream/usagecontract.Request` 为查询技术快照，敏感字段不出现在 JSON 或普通字符串格式化结果中；`upstream/internal/usageclient` 统一读取上限、请求头覆盖顺序、状态映射和响应体关闭。
- 服务编排：`provider.UpstreamUsageService` 负责查询编排、身份复核、并发槽和指标；`provider/provider.UpstreamUsageExecution` 持有适配器注册表并构造凭据、代理、TLS 和 Header 快照。
- Ollama 边界：固定设置页抓取、HTML 解析、Retry-After、Chat 思考字段补齐和输出上限在 `upstream/ollama`；`provider.OllamaCloudUsageService` 负责浏览器会话、分组、加密、singleflight、身份 CAS 和周期维护。
- 平台定位：用量查询是 OpenAI 兼容提供商的一项能力，不对应独立提供商平台。Kimi、Zhipu、DeepSeek 的通用文本执行复用 Anthropic 和 OpenAI 协议链，平台资格和全局重试与用量适配器无关。

## 管理员接口

- `POST /api/v1/admin/providers/:id/upstream-usage/query`
- `POST /api/v1/admin/providers/upstream-usage/query/batch`，请求体 `{ "provider_ids": [1, 2] }`，最多 100 个正整数 ID。

成功响应顶层字段包括 `provider_id`、`adapter`、`provider`、UTC `observed_at`、`mode`（`balance`、`quota`、`limits` 或 `subscription`）、`unit`、`balance`、`balances`、`available`、`limits`、`subscription` 和 `expires_at`（不适用字段省略）。

New API 的 `balance` 为钱包余额，`limits` 和 `subscription` 为当前 Key 配额；DeepSeek 的 `balances` 保存多币种钱包；coding 周期使用 `unit=PERCENT`。批量响应将成功结果与各提供商结构化错误分开返回，单个失败不影响其他提供商。

查询执行与生命周期约束：

- 超时与传输：每次操作总超时约 60 秒，响应体上限 512 KiB，禁止重定向，复用提供商代理、TLS 指纹、Header Override 和 `HTTPUpstream`。
- 身份复核与合并：查询前后各读取一次提供商，凭据、代理、Base URL、TLS 连接设置或规范化配置变化时返回 `UPSTREAM_USAGE_IDENTITY_CHANGED`。同提供商和配置指纹的查询经 singleflight 合并，等待方可各自取消并获得独立结果副本。
- 只读隔离：手动查询结果仅保存于浏览器缓存，不写入提供商、Extra、调度快照、数据库和计费记录，失败不影响转发；上游查询使用独立并发槽，与网关调度槽分开计算。
- 启停行为：构造时不启动后台任务；应用关闭时拒绝新认领，取消并等待已脱离 HTTP 等待方的共享查询，执行未结束时报告超时，共享依赖在此之前保持打开。

<a id="frontend_lifecycle"></a>
## 前端生命周期

API Key 提供商（含 Kimi、Zhipu、DeepSeek）用量栏按固定顺序展示：

1. 上游余额或周期用量。
2. 本地今日统计（有数据时显示）。
3. 本地配额（有配置时显示）。
4. 底部唯一查询按钮（内容组件隐藏内部按钮；查询中禁用，失败后同按钮重试）。

上游查询失败、关闭或不支持时，本地统计和配额照常显示。

- 统一资格判断：前端的资格、启用状态和有效适配器由同一解析入口提供，展示、按钮、单个和批量查询共用该结果。支持 API Key（Zhipu 非 coding 模式无余额端点）；`extra.upstream_usage_query.enabled=false` 时关闭；缺省时 Jev 默认关闭，其余支持商默认启用。不支持或已关闭时隐藏上游区域，批量选择跳过这些提供商。
- 触发与缓存：列表加载、滚动进入视口和自动刷新均不请求上游，仅行内刷新按钮或批量操作手动触发。成功结果按管理员身份、提供商 ID、`updated_at`、代理、Base URL、服务模式、适配器和规范化配置写入 `sessionStorage` 5 分钟（不含凭据）；失败不缓存；强制刷新绕过缓存；保存提供商或凭据、代理、Base URL、配置变化时缓存立即失效。
- 防乱序与监控快照：请求完成时复核当前页面提供商及管理员身份，迟到结果不能覆盖已改配置。提供商带有有效 `extra.cn_usage_monitor_snapshot` 时，列表直接展示最近监控结果，不发起请求。

## 国产供应商周期监控

监控由 `provider.UsageMonitor` 实现，候选平台和官方域名由各平台声明（当前启用：Kimi、Zhipu、DeepSeek），启动配置对应 `GatewayUsageMonitorConfig`。

部署键 `provider.usage_monitor`、快照键 `extra.cn_usage_monitor_snapshot`、任务锁键和停调原因前缀沿用既有格式。旧的 `upstream_billing_probe` 自动倍率探测功能已移除，用量监控使用独立的快照和余额停调规则。

`provider.usage_monitor.enabled` 默认为 `false`。开启后仅扫描满足以下条件的 Kimi、Zhipu、DeepSeek 提供商：`active`、`type=apikey`、未关闭用量查询、具备固定适配器。

- 调度与锁：首次探测在一个完整周期后执行；每轮先获取任务锁（锁被占用跳过本轮，Redis 不可用时尝试 PostgreSQL advisory lock）；每轮有总预算，单请求有独立超时，并发数受配置限制。
- 停止语义：服务关闭时取消当前轮并等待退出，`Stop` 后不能再次启动；存储或执行接口不响应取消时报告未完成，共享依赖保持打开。
- 快照结构：状态写入 `extra.cn_usage_monitor_snapshot`，含版本、适配器、完整查询身份 hash、最近成功归一化数据、最近尝试时间和脱敏错误码；失败仅更新尝试时间和错误，保留上次成功数据。
- 快照 CAS 与失效：`provider/postgres` 按 `updated_at` 做 CAS 并在同一 SQL 写 scheduler outbox。凭据、平台、模式、协议、代理、Base URL、TLS 或查询配置变化时清除旧快照；读取方重算身份 hash，忽略旧身份数据。
- 余额停调与恢复：余额低于 `provider.usage_monitor.pause_below_balance` 时写入带身份 hash 的临时不可调度原因；恢复到阈值以上时只清除同一身份创建的状态。暂停和恢复按读取时的 `updated_at` 条件写入，恢复另校验原因一致。
- Outbox 与 coding 快照：健康状态暂停与恢复事务提交后尽力写 outbox，健康状态与 Redis 更新不在同一事务。coding 模式百分比窗口快照供已有额度阈值和重置时间判断，表示百分比而非货币余额。
- 访问安全：官方域名可直接监控；自定义中继仅在开启 URL allowlist 且 host 命中 `security.url_allowlist.upstream_hosts` 时后台自动访问。复用提供商代理、TLS 指纹、受保护 Header Override 和 `UpstreamUsageService`。

## 钱包余额邮件提醒

管理员在提供商创建或编辑页配置 `extra.upstream_balance_alert`：

| 字段 | 默认值与约束 |
| --- | --- |
| `enabled` | 默认 `false`；仅支持钱包查询适配器，OAuth、Coding Plan 周期额度和 Zhipu payg 不支持 |
| `threshold` | 默认 `1000` |
| `interval_minutes` | 默认 `60`，范围 5 至 1440 |
| `recipient_email` | 单个收件邮箱 |

编辑时省略该对象保留原配置，复制提供商时删除提醒配置；关闭上游查询或停用提供商会暂停检查。

- 前端表单保存（`frontend/src/utils/upstreamBalanceAlert.ts`）：创建页只在开启提醒时写入该对象。编辑页有改动时提交完整对象（关闭开关时阈值、间隔和邮箱随 `enabled=false` 保存）；无改动保留已有对象，原无提醒不新增默认对象；不支持的提供商开关置灰且提交时 `enabled=false`。
- 前端状态拉取：编辑页展开提醒设置后读取状态接口，切换提供商或关闭面板时使在途请求失效，并清空旧展示和加载状态。
- 后台扫描与认领：`provider.BalanceAlertMonitor` 每分钟扫描启用的 API Key 提供商，最多并行 4 个检查，单次检查和发信合计最多 2 分钟。`provider_balance_alert_states` 在查询前原子认领轮次并持久化推进下次检查时间。
- 停机与超时语义：首次启用在下一次扫描检查，停机后只执行当前到期一轮；进程中断或 SMTP 结果不确定时本轮不重试，下次到期重查。超时或服务停止取消后，结果落库仍有独立 5 秒预算，应用等该步结束后再关闭共享存储。
- 查询与触发条件：查询复用 `UpstreamUsageService`，地址校验同管理员手动查询（全局白名单关闭按配置 URL 格式和 HTTP 规则，开启检上游域名白名单和私网权限）。`balance.remaining` 严格小于阈值时每轮发送一次 `provider.upstream_balance_low` 事件，币种仅展示不参与比较。
- 失真防护与复核：查询失败或余额缺失不当作零余额，也不使用旧结果发信。发送前复核查询身份和提醒配置，修改凭据、收件人或阈值后旧结果失效。
- 状态查询：`GET /api/v1/admin/providers/:id/upstream-balance-alert` 返回最近轮次、检查时间、下次检查时间、余额、单位、观测时间、发送确认时间和脱敏错误码（尚无当前配置观测时只返回提供商 ID）。该接口只读持久状态，不发起查询或发信；后台运行状态独立于提供商配置及调度快照，提醒任务不暂停或恢复提供商。
