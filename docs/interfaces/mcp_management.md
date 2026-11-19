# MCP 管理接口

TokenRouter 在主 HTTP 服务的 `/mcp` 提供 Streamable HTTP MCP，支持专用访问令牌和管理员 OAuth 应用授权。本文说明操作目录、授权、执行和浏览器接续。普通管理接口业务规则见 [HTTP 接口](http_api.md)，管理员身份与 TOTP 见[身份与租户](../domains/identity_and_tenancy.md)。

## 章节导航

- [操作目录](#operation_catalog)：登记管理功能、参数和响应处理方式。
- [访问令牌](#access_tokens)：签发、授权、撤销和接口范围。
- [OAuth 应用授权](#oauth_authorization)：发现、客户端注册、浏览器授权和凭据轮换。
- [工具调用](#tool_execution)：发现、校验、执行及错误结果。
- [浏览器确认](#browser_confirmation)：敏感操作与近期 TOTP。
- [文件与交互流程](#files_and_interaction)：导入、导出、OAuth 和实时查询。
- [客户端接入与部署](#client_setup)：连接配置、迁移和运行限制。

<a id="operation_catalog"></a>
## 操作目录

各模块的 `management_operations.go` 声明稳定的操作 ID、说明、HTTP 方法、路径、输入 schema、读写属性和交互要求；包初始化时经 `server/adminapi.Define` 注册，HTTP 经 `adminapi.Register` 挂载同一描述及原 handler。

- 按实际路由装配：路由装配完成后按实际存在的路由构造目录，`/api/v1/admin/*` 缺少描述时启动失败，管理用 `/api/v1/pages` 显式登记。未挂载的接口不会暴露；操作 ID 独立于 handler 名称，改名保持已有权限含义。
- 请求结构体与参数：复用 HTTP handler 解码的 Go 类型，`binding:"required"` 决定必填，指针允许显式 `null`，动态 map 保持开放键空间，业务校验仍由原 handler 和服务执行。查询参数单独声明为字符串，路径参数按名称提交；新增结构体字段自动进入 schema，手工读取的查询参数须同步登记。
- 枚举与显式 null 约定：`omitempty` 位于 `oneof` 前时，字符串枚举保留空串；依赖原始 JSON 成员存在性区分默认值的接口需补充声明（例如告警规则允许 `enabled:null` 表示 `false`，省略则由 handler 默认启用）。
- `request_source` 筛选：使用记录列表、统计、清理、后台导出及用量趋势、模型、分组、组合快照支持与 HTTP 相同的 `request_source`（`system` 汇总全部系统模型调用，或选 `group_probe`、`provider_test`、`scheduled_test`、`quota_probe`）。
- `request_source` 默认口径：使用记录列表和统计默认包含全部来源；仪表盘图表默认统计用户流量，显式传 `all` 才合并系统请求。
- 新增接口规范：在所属模块补充描述并使用共享注册函数。读写属性按业务填写（POST 查询可声明为只读，连接测试等触发真实上游动作的归执行工具），有限 SSE 声明终态事件，特殊交互填写浏览器入口。覆盖测试检查实际路由、目录、重复 ID 和查询别名，禁止未知接口自动获得通用调用入口。
- 导出与升级标记：目录同时供 MCP 发现、管理页权限选择和 OpenAPI 导出（前端模块译名为展示文本，权限仍用后端 ID）。WebSocket 原路由以 `http_upgrade` 声明升级要求，OpenAPI 输出 101 响应；`x-tokenrouter-mcp-alias` 标明 MCP 等效查询操作。
- 响应 schema：JSON 响应提供共享 HTTP envelope schema 并展开具体 DTO 字段（动态 map 保持开放，由操作说明解释）；`response_schema` 描述原 HTTP 结果，MCP 外层补充执行状态和关联 ID，并按凭据设置决定是否遮蔽响应字段。

<a id="access_tokens"></a>
## 访问令牌

管理员在 `/admin/mcp` 管理自己的令牌，后端入口位于 `/api/v1/admin/mcp`：

| 接口 | 用途 |
| --- | --- |
| `GET /tokens`、`POST /tokens` | 查询元数据、创建令牌 |
| `GET /tokens/:id/credential` | 所属管理员在浏览器中复制完整令牌 |
| `PUT /tokens/:id`、`DELETE /tokens/:id` | 更新名称、有效期、启用状态、权限和工具结果脱敏设置，或永久撤销 |
| `GET /operations`、`GET /openapi` | 权限目录、管理 API 描述 |
| `GET /confirmations/:id`、`POST /confirmations/:id/approve` | 浏览器查看和批准请求 |
| `POST /files`、`GET /files/:id` | 上传 JSON 请求体、下载受保护结果 |

- 生成与存储：令牌由 32 字节随机数加 `tr_mcp_` 前缀构成。PostgreSQL `mcp_access_tokens` 保存认证用 SHA-256 摘要、AES-256-GCM 密文、显示前缀和管理元数据；页面默认 30 天有效期（可清空为不过期）；撤销保留记录且不可恢复。
- 完整令牌复制：创建后返回完整值，所属管理员可按需复制（列表与认证不读密文）。复制接口解密后核对摘要，返回 `Cache-Control: no-store` 并记录 `admin.mcp.tokens.copy` 审计；停用和过期令牌仍可复制，撤销后拒绝读取。
- 固定加密密钥：创建和复制要求固定的 `security.secret_encryption_key`（与现有加密组件共用），各实例须同密钥并随备份保存，缺固定配置拒绝创建和复制。换密钥后旧密文无法解密，普通认证继续走摘要。
- 乐观并发与 TOTP：更新可携带 `expected_updated_at`，SQL 同次写入比对配置时间，不匹配返回 `MCP_TOKEN_CHANGED`。管理页编辑和启停均携带该值，各行独立记录忙状态防旧表单覆盖。需 TOTP 的操作依次请求、验证和重试；取消验证释放该项忙状态再处理下一排队项。
- 权限校验：每次请求读取当前令牌和所属用户，检查启用状态、有效期、撤销状态和管理员角色，操作执行前及进入原路由时再次检权；已提交撤销、降权和收窄影响后续请求（在途业务遵循原 handler 取消规则）。
- 权限集合与预设：权限保存为具体操作 ID 集合，升级新增接口不自动扩权；经 MCP 创建或编辑其他自有令牌时，权限不得超出调用令牌现有范围。管理页提供只读查询、网关配置与运维、用户与订阅管理、全部管理操作四套预设，按当时目录展开并可逐项调整。
- 认证隔离：专用令牌仅经 `/mcp` 的 `Authorization: Bearer` 提交，不能直接访问普通管理 REST 接口；完整令牌复制、浏览器确认和文件下载要求所属管理员 JWT 会话。

<a id="oauth_authorization"></a>
## OAuth 应用授权

OAuth 直接复用站点的 `frontend_url`（后台非空值优先，未填用部署配置 `site.frontend_url`），须为 HTTPS origin 且不含路径、用户信息、查询或片段（回环 HTTP 仅用于本地开发），尾部根斜杠规范化。

没有单独的 OAuth 地址或覆盖配置，`api_base_url`、请求 Host 和代理头不参与生成。有效地址自动启用 OAuth；缺失或无效时状态接口说明原因、协议请求拒绝准入、手工 Token 继续工作。

每次 OAuth 请求和稳定授权检查读取当前站点地址（同次协议交换使用固定快照），读取设置失败拒绝准入。修改前端 origin 后，旧地址的授权码、access token、refresh token 和待授权请求不能用于新地址，应用列表提示需要重新连接。

### 端点清单

| 路径 | 用途与响应格式 |
| --- | --- |
| `/.well-known/oauth-protected-resource/mcp` 及无 `/mcp` 兼容入口 | 资源发现（OAuth JSON）；OAuth 可用时未认证 `/mcp` 经 `WWW-Authenticate` 公布 |
| `/.well-known/oauth-authorization-server` | 授权服务器发现（OAuth JSON） |
| `/mcp/oauth/{authorize,token,register,revoke}` | 授权、换票/刷新、DCR 和撤销（OAuth JSON） |
| `/api/v1/admin/mcp/oauth/status` | 管理员 JWT（HTTP envelope）；`enabled` 表可用性，`reason` 区分地址缺失与无效 |
| `/api/v1/admin/mcp/oauth/requests/:id`、`/requests/:id/decision` | 管理员 JWT；读取待授权请求、提交批准或拒绝 |
| `/api/v1/admin/mcp/oauth/grants`、`/grants/:id` | 管理员 JWT；应用授权列表、更新（带 `expected_updated_at` 防并发覆盖）和删除 |

### 协议校验与客户端身份

- 授权码与 Resource：强制 S256 PKCE，公布 RFC 9207 `iss` 并在已验证成功或错误回调中返回。授权和换票须匹配 `resource`；刷新省略 `resource` 时继承凭据族绑定值，提交不同值拒绝。
- Scope 约定：`mcp` scope 表接入资格，实际可调操作由持久授权的具体 ID 决定；`offline_access` 由授权服务器公布，资源发现不将其作为必需权限；支持 `refresh_token` 的客户端获得可轮换刷新凭据。
- DCR 客户端：支持 `none`、`client_secret_basic` 和 `client_secret_post`，提交的 `grant_types` 须全部受服务端支持。
- CIMD 认证与授权类型：使用 `none`；提供 `token_endpoint_auth_methods_supported` 时取交集且须含 `none`（缺省用旧单值字段，兼容 ChatGPT 同时公布 `private_key_jwt` 与 `none`）。`grant_types` 取交集，非空须含 `authorization_code`，声明 `refresh_token` 才支持刷新。
- CIMD 安全校验：文档 `client_id` 须与 URL 精确一致，读取禁代理和重定向，DNS 解析与实际拨号排除内网、回环和保留地址；显示名称为未验证文本。
- 回调一致性：回调须匹配注册信息，允许规范中的本机回环 HTTP 回调。短期授权请求保存首次验证的完整回调地址；客户端只有一个已注册回调时可省 `redirect_uri`（浏览器仍展示已解析地址）；决定授权时复检该地址是否仍被允许，CIMD 回调变化不能替换待授权回调；授权码保留原请求是否提交 `redirect_uri` 以执行对应换票校验。

### 浏览器授权与凭据生命周期

- 浏览器授权入口：管理员经 `/admin/mcp/authorize` 登录后在 MCP 管理页弹窗决定；有效请求关闭提交拒绝，过期或失败流程可关闭回列表。10 分钟请求绑定当前管理员与 JWT session，决定提交另需 session-bound nonce。
- 授权弹窗与 TOTP：首次默认勾选安全只读预设；已有应用展示当前权限，新增操作不自动加入。批准遵循近期 TOTP 策略，拒绝不要求 step-up；弹窗在请求、账户或可见状态变化后丢弃旧响应并阻止旧请求再触发 TOTP 重试；确认请求只能消费一次，客户端不能代批。
- 有效期与轮换：授权码 5 分钟，access token 15 分钟，refresh token 每次使用后轮换并续期 30 天；应用授权默认无固定期限，连续 30 天未刷新须重新连接。授权与 token 原文分开存储，不提供浏览器复制。重用已消费的 refresh token 会撤销整个凭据族，不影响其他应用和手工令牌。
- 撤销语义：客户端撤销校验客户端与凭据归属，应用停用或管理员暂不可访问时仍可撤销，成功后使该族 access/refresh token 失效。未知凭据返回 HTTP 200；数据库读写或事务提交失败返回 OAuth `temporarily_unavailable` 和 HTTP 503，客户端应视凭据仍在并重试。
- 稳定 ID 与并发隔离：请求、限流、内部 dispatch、确认及文件同时用来源类型和稳定 ID 区分手工令牌与 OAuth（旧 Redis 记录缺来源视为手工令牌）。刷新不改变授权 ID 或配置版本；改权限使旧浏览器确认失效；后续请求和下载复查授权、用户状态和管理员角色。同客户端重新授权更新存量授权并撤销旧凭据族；已永久撤销记录保留历史，新连接创建新记录。

### 存储、密钥与限流

- 模块与表结构：Fosite 负责协议验证，`identity/provider` 负责 MCP resource、CIMD 和浏览器授权编排，`identity/postgres` 负责事务与持久化。迁移 302 新增 OAuth 表，独立签名密钥持久化在 `security_secrets`。
- 临时文件加密密钥：未配置固定 TOTP 加密密钥时，从持久签名密钥经独立 HKDF 上下文派生临时文件 AES 密钥（重启后可解密有效 Redis 文件）；已配固定 TOTP 密钥的部署沿用该密钥。
- 事务与清理：换票时管理员角色、账户和删除状态查询复用当前事务，避免连接池等待。授权码消费、刷新轮换和重新授权在事务中完成，重放拒绝提交撤销，其他失败回滚。清理任务每小时删除过期请求和完全过期凭据族，与换票同序加应用及凭据族锁并在锁内复检到期；已消费刷新记录保留至整族过期以检测重放。
- 限流与配额：按可信代理解析后的客户端 IP 与共享 Redis 计数，每 IP 每分钟公开注册 10 次、授权 30 次、换票和撤销各 60 次。请求体上限 32 KiB，授权查询上限 16 KiB。数据库和限流故障拒绝准入，响应禁止缓存，日志和审计省略凭据与授权决定正文。

<a id="tool_execution"></a>
## 工具调用

| 工具 | 行为 |
| --- | --- |
| `search_operations` | 按模块和关键词搜索已授权操作，默认 20 项，最多 50 项 |
| `describe_operation` | 读取某个已授权操作的 schema、说明及示例 |
| `read_operation` | 执行明确声明为只读的操作 |
| `execute_operation` | 执行授权动作，或发起必要的浏览器接续 |

- 执行参数：支持 `operation_id`、`path`、`query`、`body`，按需附带 `idempotency_key`、`confirmation_id`、`file_id`。省略 JSON body 与显式 `null` 保持区别；`file_id` 可代替上传的 JSON body，两者互斥。
- 特殊探测操作：主动提供商用量查询与 Grok 配额探测可能恢复状态或调整限流，归 `execute_operation`；`providers.usage.passive.list`（`GET /api/v1/admin/providers/:id/usage/passive`）只读 Anthropic OAuth/SetupToken 已采集数据，其他平台保留原不支持错误。
- 安全分发：目录决定方法和路径，拒绝可改变路由的路径分隔符、转义和未知参数，调用方不能指定目标主机或认证头。内部 HTTP 经私有 context 携带令牌身份，原管理员中间件复核最终路由后再执行限流、审计、强认证和业务处理。
- 结果与失败语义：结果含关联 ID、HTTP 状态和原数据；JSON 与有限 SSE 数值保留十进制表示（大整数不浮点舍入）。文本与结构化结果同源，业务错误以 MCP `isError` 返回；`succeeded` 只表示 API 调用完成（备份等后台任务需另查状态）。不自动重试写操作，无跨接口回滚；响应中断时写操作返回 `outcome_unknown`。
- 工具结果脱敏（`redact_tool_results`）：编辑手工令牌或 OAuth 应用时可开启，新建和已有凭据默认关闭，保存后自下次调用生效；更新时省略或传 `null` 保留原值。关闭保留原接口字段；开启后对内联 JSON（含业务错误）和有限 SSE 事件应用 MCP 字段规则。浏览器确认预览和审计各有独立脱敏规则；凭据导出仍走受保护文件。
- 幂等与核心审计：已声明幂等的接口接收 `idempotency_key` 并走原协调器，未声明接口拒绝该参数。审计异步尽力写入，附加 `mcp_token_id`、`mcp_operation_id` 和 `mcp_source`（空值表手工令牌，`oauth` 表应用授权，标识 `oauth:<id>`）；关键词可搜显示前缀并由管理页跳转；上传文件审计正文整体省略。
- 接续审计筛选：浏览器确认、状态查看和文件下载也记录关联令牌与操作并保留 JWT 认证方式。令牌审计入口默认按显示前缀和 MCP 认证查询，切换为「全部」可同时查看智能体执行与管理员接续记录，链接筛选回填控件并随路由重同步。

<a id="browser_confirmation"></a>
## 浏览器确认

综合设置、身份与权限变更、凭据导出、MCP 令牌管理等操作在目录中标记为敏感，默认先返回 `confirmation_required` 和确认链接，业务尚未执行。

- 跳过确认开关（`skip_browser_confirmation`）：按单令牌配置，默认关闭，与权限一同持久化且每次重读。开启后授权内敏感操作可直接执行（权限检查、受保护文件和浏览器授权仍生效）；存在已启用的近期 TOTP 要求时仍须浏览器确认。发现返回按当前令牌计算的 `requires_confirmation`，直接执行审计标 `mcp_confirmation_skipped`。
- 条件强认证与版本绑定：目录 `step_up_when` 用参数 schema 表达条件（例如用户创建和编辑仅 `body.role=admin` 需近期验证），执行按实参判断并记入确认记录，发现阶段提示可能需确认、`describe_operation` 给出条件。直接执行绑定已读配置版本，关开关或改令牌后尚未进入原路由的旧请求被拒。
- 确认记录与摘要：保存于 Redis（10 分钟 TTL），含管理员、令牌、配置版本、操作描述、精确请求摘要和脱敏预览。数字保留十进制表示，公开配置空值、零值和布尔值保留原类型；摘要绑定原始参数，脱敏预览不使不同凭据或价格上限共用批准。
- 预览格式与截断：服务端格式化预览并由页面直显（防浏览器解析改变大整数）；省略超过 256 KiB 的输入，紧凑 JSON 超 16 KiB 截断并标记后再缩进。敏感原文由后续重试重新提交，普通 HTTP 审计走独立脱敏。
- 浏览器批准与消费：管理员登录后，确认链接在 MCP 管理页直接打开弹窗展示说明和脱敏预览；取消或关闭仅移除 URL 参数，提交或等近期 TOTP 时弹窗保持打开。工具携带 `confirmation_id` 重提相同参数，服务端复核权限、摘要、版本及近期验证后原子消费；已消费、过期、参数/目录/令牌配置变化均拒绝。
- 批准边界与状态查询：确认 URL 本身无权限，MCP 内部身份及管理 API Key 不能代浏览器批准；需直输 TOTP 的审计清空接续到原操作日志页。拥有 `mcp.confirmations.get` 且仍有所确认操作权限的令牌可查自己发起的确认状态（同管理员其他令牌不可读），仅返脱敏预览，不返会话标识或参数摘要。

<a id="files_and_interaction"></a>
## 文件与交互流程

### 字段脱敏规则

普通 JSON 保留原 DTO 脱敏；开启 `redact_tool_results` 后，MCP 追加遮蔽敏感键：

- 明确保留字段：已登记的 token 数量、限制、价格、配置开关、凭据存在标记及 API Key 管理元数据按明确字段名和 JSON 类型保留；`null` 保留为空值（如定价区间 `max_tokens:null` 表无上限）。元数据对象和数组继续递归检查内部凭据，异常类型不放行。
- 跨协议归一化：字段归一化兼容大小写和分隔符，多协议同名时分别登记允许结构（如 OpenAI `prompt_tokens_details` 对象与 Gemini `promptTokensDetails` 数组均保持可读，内部凭据仍遮蔽）。
- 特定保留类型：审计 `extra.mcp_token_id` 保留数字类型，提供商 `session_token_present` 和 `antigravity_force_token_refresh` 保留布尔类型。
- 歧义名称路径约束：`key`、`code`、`authorization` 另须匹配操作和字段路径，默认价格项标识、用户属性键、运维锁名、协议授权类别、HTTP 错误码和 SSE 错误事件码可读，凭据或其他嵌套位置继续遮蔽。真实凭据不因值为数字或布尔放行；处理不改原响应或确认参数，不将数字字符串转数值。

### 受保护文件与上传

- 文件交付范围：原始凭据导出、新签发 MCP 令牌、兑换码与含优惠码结果、独立刷新得到的上游凭据、用量导出 CSV、二进制结果和较大 JSON 均以受保护文件交付，不展开到模型上下文。
- 大导出分流：`usage.export_tasks.download.get` 在响应捕获上限内返回受保护文件；更大导出经 `usage.export_tasks.download_ticket.create` 签短期下载链接由浏览器流式下载。
- 加密与流式备份：临时文件绑定管理员、令牌和目标操作（10 分钟有效），Redis 内容及元数据用部署 AES 密钥加密，每次下载复查管理员与令牌权限。大型备份保存受保护下载请求，浏览器下载进原流式 handler，不将归档整体放入 Redis。
- 下载文件名与跨域：文件名沿用原响应（无名 JSON 用 `<operation_id>.json`）；页面读 `Content-Disposition` 保留普通或 UTF-8 文件名与扩展名，已允许跨域响应暴露该头以支持前后端分离。
- 上传约束：上传仅接受编码 JSON，禁止服务器文件路径或远程 URL。MCP 调用默认绑定当前授权（省略 `token_id`、`source`），浏览器调用提交所属 ID 及来源；工具引用时复检操作和归属。

### 特殊交互流程

提供商连接测试复用原测试入口，有限 SSE 需成功终态，流中错误不因 HTTP 200 视作成功。OAuth、PAT、SSO 等交互授权接续到原提供商管理页（「添加提供商」或「重新授权」），保存后可用 `providers.list` 查询并续配。实时 QPS/TPS 使用现有查询接口返回对应时间窗口数据。

<a id="client_setup"></a>
## 客户端接入与部署

### 反向代理与客户端配置

嵌入前端资源的发行构建将 `/mcp` 保留为后端入口，GET、POST 和 DELETE 均跳过 SPA 回退；反向代理须将该路径转发给主 HTTP 服务。

同机反向代理可连接 `127.0.0.1:8080` 并保留公开域名 `Host`：`/mcp` 在 SDK 分发前执行专用 Bearer 认证和 Origin 检查，并关闭 SDK 面向本地 MCP 的回环 Host 限制（直接运行二进制与 Docker 端口映射均可沿用）。

手工令牌客户端配置 Streamable HTTP 地址与专用 Bearer 令牌，支持环境变量的客户端应将凭据留在密钥配置中（例如 Codex，参见[官方文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)）：

```toml
[mcp_servers.tokenrouter]
url = "https://gateway.example.com/mcp"
bearer_token_env_var = "TOKENROUTER_MCP_TOKEN"
```

OAuth 客户端填写同一个 `/mcp` 地址，走上文的发现和浏览器授权流程。

浏览器 Origin 须命中明确配置的 CORS origin 或站点公共 URL，`*` 不作为允许来源；原生客户端可省略 Origin。配置 `frontend_url` 后，浏览器接续返回对应公开站点完整链接；未配置时返回同站路径，由客户端按 MCP 来源解析。

### 限流、配额与迁移

MCP 限制与推理网关流量独立计算：

| 限制项 | 阈值与行为 |
| --- | --- |
| 单令牌请求频率 | 每分钟最多 300 次 MCP HTTP 请求，Redis 不可用时拒绝准入 |
| 后端并发数 | 最多同时处理 8 个 MCP HTTP 请求 |
| 请求体上限 | 外层入口与 SDK 均为 16 MiB |
| 响应捕获上限 | 内部有限响应捕获 16 MiB（大 JSON 走文件交付，备份按原接口流式传输） |
| 单次执行超时 | 2 分钟 |

- 数据库迁移与回退：迁移 300 新增令牌表，迁移 301 新增默认关闭的跳过确认设置，迁移 310 为令牌和 OAuth 应用授权新增工具结果脱敏设置（已有行默认 `false`）。回退到支持 MCP 但早于此开关的版本时可保留新增列，旧后端会对工具结果执行额外脱敏。
- 兼容与备份：既有管理员、网关 Key 和登录凭据不转换；沿用 PostgreSQL、Redis 和持久加密密钥；临时确认与文件按 TTL 过期。完整数据备份包含令牌摘要与密文，恢复完整令牌另需对应固定加密密钥。迁移与回退见[部署与数据库迁移](../operations/deployment_and_migrations.md)。
