# MCP 管理接口

TokenRouter 在主 HTTP 服务的 `/mcp` 提供 Streamable HTTP MCP，支持专用访问令牌和管理员 OAuth 应用授权。本文说明操作目录、授权、执行和浏览器接续。普通管理接口的业务规则见 [HTTP 接口](http_api.md)，管理员身份与 TOTP 见[身份与租户](../domains/identity_and_tenancy.md)。

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

各模块的 `management_operations.go` 声明稳定的操作 ID、说明、HTTP 方法、路径、输入 schema、读写属性和交互要求。声明在包初始化时通过 `server/adminapi.Define` 注册，HTTP 注册函数通过 `adminapi.Register` 挂载同一个描述及原 handler。

应用完成路由装配后，按实际存在的路由构造目录。`/api/v1/admin/*` 缺少描述时启动失败；管理用的 `/api/v1/pages` 也有显式登记。目录不会因为某个包包含声明，就暴露当前实例未挂载的接口。操作 ID 独立于 handler 名称，改名时保持已有权限含义。

请求结构复用 HTTP handler 解码的 Go 类型。`binding:"required"` 决定必填字段，指针允许显式 `null`，动态 map 保持开放的键空间，业务校验继续由原 handler 和服务执行。查询参数单独声明为字符串，路径参数按名称提交。新增结构体字段会进入 schema；新增手工读取的查询参数要同步登记。`omitempty` 位于 `oneof` 前时，字符串枚举保留空串。使用原始 JSON 成员存在性区分默认值的接口需要补充声明，例如告警规则显式允许 `enabled:null` 表示 false，省略则仍由 HTTP handler 默认启用。

使用记录列表、统计和清理，以及用量趋势、模型、分组与组合快照，支持与 HTTP 相同的 `request_source` 选择。`system` 汇总全部系统模型调用，也可以选择 `group_probe`、`provider_test`、`scheduled_test` 或 `quota_probe`。使用记录列表和统计默认包含全部来源；仪表盘图表默认统计用户流量，显式传 `all` 才合并系统请求。

新增接口时，在所属模块补一条描述并使用共享注册函数。读写属性按业务行为填写；POST 查询可以声明为只读，连接测试等会调用真实上游的动作使用执行工具。有限 SSE 声明终态事件；特殊交互填写浏览器入口。覆盖测试检查实际路由、目录、重复 ID 和查询别名，禁止未知接口自动获得通用调用入口。

目录同时供 MCP 发现、管理页面权限选择和 OpenAPI 导出使用。前端的模块译名属于展示文本，操作权限仍使用后端提供的 ID。WebSocket 原路由通过 `http_upgrade` 声明升级要求，OpenAPI 输出 101 响应；`x-tokenrouter-mcp-alias` 标明 MCP 实际使用的等效查询操作。

JSON 响应提供共享 HTTP envelope schema，已声明的具体响应 DTO 会展开其字段。动态 map 的结果保持开放，由操作说明解释字段和业务状态。`response_schema` 描述原 HTTP 结果，MCP 在外层补充执行状态和关联 ID，并按调用凭据的设置决定是否遮蔽响应字段。

<a id="access_tokens"></a>
## 访问令牌

管理员在 `/admin/mcp` 管理自己的令牌。后端入口位于 `/api/v1/admin/mcp`：

| 接口 | 用途 |
| --- | --- |
| `GET /tokens`、`POST /tokens` | 查询元数据、创建令牌 |
| `GET /tokens/:id/credential` | 所属管理员在浏览器中复制完整令牌 |
| `PUT /tokens/:id`、`DELETE /tokens/:id` | 更新名称、有效期、启用状态、权限和工具结果脱敏设置，或永久撤销 |
| `GET /operations`、`GET /openapi` | 权限目录、管理 API 描述 |
| `GET /confirmations/:id`、`POST /confirmations/:id/approve` | 浏览器查看和批准请求 |
| `POST /files`、`GET /files/:id` | 上传 JSON 请求体、下载受保护结果 |

令牌由 32 字节随机数生成，使用 `tr_mcp_` 前缀。PostgreSQL 的 `mcp_access_tokens` 保存用于认证的 SHA-256 摘要、AES-256-GCM 加密的完整令牌、显示前缀和管理元数据。创建成功后返回完整值，所属管理员也可以在列表中按需复制。列表与认证查询不读取密文；复制接口解密后核对摘要，使用 `Cache-Control: no-store`，并记录 `admin.mcp.tokens.copy` 审计。停用和过期令牌仍可复制，撤销后拒绝读取。页面默认设置 30 天有效期，也可以清空为不过期。撤销保留记录，不能通过编辑恢复。

令牌更新可以携带 `expected_updated_at`，SQL 在同一次写入中比较所读取的配置时间；不匹配返回 `MCP_TOKEN_CHANGED`。管理页的编辑和启停提交都携带该值，各行分别记录忙状态，避免旧表单覆盖已经完成的修改。需要 TOTP 的页面操作依次发起请求、验证和重试；取消当前验证会释放该项忙状态，再处理排队的下一项。

创建和复制需要固定的 配置文件中的 `security.secret_encryption_key`，与现有加密组件共用。各实例必须使用同一密钥，备份时一起保存；缺少固定配置时拒绝创建和复制。更换密钥后，旧密文无法解密。普通令牌认证继续使用摘要。

每个请求读取当前令牌和所属用户，检查启用状态、有效期、撤销状态和管理员角色。操作执行前及进入原管理路由时再次检查权限。已提交的撤销、降权和权限收窄影响后续请求；在途业务仍遵循原 handler 的取消规则。

权限保存为具体操作 ID 集合。模块批量选择只包含当时选中的操作；升级新增接口不会扩大既有令牌权限。通过 MCP 创建或编辑其他自有令牌时，提交的权限必须包含在调用令牌现有权限内。

管理页提供只读查询、网关配置与运维、用户与订阅管理、全部管理操作四套预设。选择预设会替换当前权限草稿，然后可以逐项调整。预设按当时的操作目录展开，保存后仍是具体 ID 集合，不会随升级自动扩权。

专用令牌通过 `/mcp` 的 `Authorization: Bearer` 提交，不能直接作为普通管理 REST 接口的凭据。完整令牌复制、浏览器确认和文件下载要求所属管理员的 JWT 会话。

<a id="oauth_authorization"></a>
## OAuth 应用授权

OAuth 直接复用站点的 `frontend_url`：后台保存的非空地址优先，未填写时使用部署配置 `site.frontend_url`。地址须为 HTTPS origin，不含路径、用户信息、查询或片段；回环 HTTP 用于本地开发。尾部根斜杠会规范化。没有单独的 OAuth 地址或覆盖配置，`api_base_url`、请求 Host 和代理头不参与地址生成。有效前端地址使 OAuth 自动可用；地址缺失或无效时，状态接口说明原因，协议请求拒绝准入，手工 Token 继续工作。

资源发现提供 `/.well-known/oauth-protected-resource/mcp` 和无 `/mcp` 后缀的兼容入口；授权发现位于 `/.well-known/oauth-authorization-server`。`/mcp/oauth` 下的 `authorize`、`token`、`register` 和 `revoke` 分别处理授权、换票/刷新、DCR 和撤销。这些端点使用 OAuth JSON；管理页接口仍使用 HTTP envelope。OAuth 可用时，未认证的 `/mcp` 请求通过 `WWW-Authenticate` 公布资源元数据地址。

支持授权码与强制 S256 PKCE。客户端通过 HTTPS CIMD 文档或动态注册确定身份，DCR 支持 `none`、`client_secret_basic` 和 `client_secret_post`。CIMD 使用 `none`；客户端提供 `token_endpoint_auth_methods_supported` 数组时，按双方支持的方法取交集，数组包含 `none` 才接受；数组缺省时使用旧的单值字段。这支持 ChatGPT 同时公布 `private_key_jwt` 偏好和 `none` 能力的元数据。文档中的 client_id 与 URL 精确一致，读取禁止代理和重定向，DNS 解析及实际拨号排除内网、回环和保留地址。客户端显示名称属于未验证文本。回调与注册信息匹配，允许规范中的本机回环 HTTP 回调。

CIMD 的 `grant_types` 与服务端支持的授权类型取交集。非空列表必须包含 `authorization_code`，只有声明了 `refresh_token` 才支持刷新。DCR 提交的授权类型须全部受服务端支持。

短期授权请求保存首次验证的完整回调地址。客户端只有一个已注册回调时可省略 `redirect_uri`，浏览器仍展示已解析的地址。决定授权时重新检查该地址是否仍被客户端注册允许；CIMD 回调变化不能替换待授权请求的回调。授权码保留原请求是否提交 `redirect_uri` 的信息，以便换票执行对应的参数校验。

授权服务器公布 RFC 9207 issuer 支持，并在已验证的成功或错误回调中返回 `iss`。授权和授权码换票需要匹配 resource；刷新请求省略 resource 时继承凭据族已绑定的 resource，提交不同值则拒绝。`mcp` scope 表示接入资格，实际可调用操作由持久授权中的具体 ID 决定。`offline_access` 由授权服务器公布，资源发现不将它作为必需权限。支持 refresh_token 的客户端获得可轮换的刷新凭据。

管理员通过 `/admin/mcp/authorize` 登录后，在 MCP 管理页的授权弹窗中决定授权。有效请求关闭时提交拒绝，过期或失败的流程可关闭并返回管理列表。十分钟请求绑定当前管理员及 JWT session，决定提交还需 session-bound nonce。首次默认勾选安全只读预设；已有应用展示当前权限，新增操作不会自动加入。批准使用现有近期 TOTP 策略，拒绝不要求 step-up。弹窗在请求、账户或可见状态变化后丢弃旧响应，并阻止旧请求再次发起 TOTP 重试。确认请求只能消费一次，客户端不能替用户批准。

每次 OAuth 请求和稳定授权检查读取当前站点地址；同一次协议交换使用固定的地址快照。修改前端 origin 后，旧地址的授权码、access token、refresh token 和待授权请求不能用于新地址，客户端需要重新连接，应用列表会显示需要重新连接。读取设置失败时拒绝 OAuth 准入。换票时的管理员角色、账户状态和删除状态查询复用当前数据库事务，避免持有事务连接时再等待同一连接池中的空闲连接。

授权码有效期五分钟，access token 有效期十五分钟，refresh token 每次使用后轮换并续期三十天。应用授权默认无固定期限，连续三十天未刷新需要重新连接。授权与 token 原文分开存储，OAuth token 不提供浏览器复制功能。重用已消费的 refresh token 会撤销其整个凭据族；其他应用和手工令牌不受该次撤销影响。

客户端撤销校验客户端与凭据的归属，应用停用或管理员暂时不能访问时仍可撤销。撤销成功使该族的 access token 和 refresh token 失效。未知凭据返回 HTTP 200；数据库读写或事务提交失败返回 OAuth `temporarily_unavailable` 和 HTTP 503，客户端应视凭据仍然存在并重试。

应用授权采用稳定 ID。请求、限流、内部 dispatch、确认及文件同时使用来源类型和 ID 区分手工令牌与 OAuth，旧 Redis 记录缺少来源时视为手工令牌。刷新不改变授权 ID 或配置版本；修改权限会使旧浏览器确认失效。后续请求和下载复查当前授权、用户状态和管理员角色。相同客户端重新授权时更新存量授权并撤销旧凭据族，已永久撤销的记录保持历史，新连接创建新记录。

浏览器管理入口位于 `/api/v1/admin/mcp/oauth`：`GET /status` 查询接入状态，`GET /requests/:id` 和 `POST /requests/:id/decision` 完成授权，`GET /grants`、`PUT /grants/:id` 和 `DELETE /grants/:id` 管理应用。所有入口要求管理员 JWT 会话。状态中的 `enabled` 表示 OAuth 是否可用，`reason` 区分地址缺失和地址无效。应用授权更新携带 `expected_updated_at`，防止旧草稿覆盖并发修改。

Fosite 负责协议验证，identity/provider 负责 MCP resource、CIMD 和浏览器授权编排，identity/postgres 负责事务与持久化。迁移 302 增加 OAuth 表，独立签名密钥通过 `security_secrets` 持久化。未配置固定 TOTP 加密密钥时，OAuth 部署从持久签名密钥通过独立 HKDF 上下文派生临时文件的 AES 密钥，使后端重启后能解密仍有效的 Redis 文件；已配置固定 TOTP 密钥的部署沿用该密钥。授权码消费、刷新轮换和应用重新授权在事务中完成；重放拒绝会提交撤销结果，其他交换失败回滚。清理任务每小时删除过期请求和完全过期的凭据族。清理与换票按相同顺序获取应用及凭据族锁，并在取得锁后重新检查到期状态。已消费刷新记录保留至整个族过期，以便检测旧凭据重放。

公开注册每 IP 每分钟 10 次，授权 30 次，换票和撤销各 60 次；使用可信代理解析后的客户端地址及共享 Redis 计数。请求体上限 32 KiB，授权查询上限 16 KiB。数据库和限流故障拒绝准入，响应禁止缓存，日志及审计省略凭据和授权决定正文。

<a id="tool_execution"></a>
## 工具调用

| 工具 | 行为 |
| --- | --- |
| `search_operations` | 按模块和关键词搜索已授权操作，默认 20 项，最多 50 项 |
| `describe_operation` | 读取某个已授权操作的 schema、说明及示例 |
| `read_operation` | 执行明确声明为只读的操作 |
| `execute_operation` | 执行授权动作，或发起必要的浏览器接续 |

执行参数包含 `operation_id`、`path`、`query`、`body`，以及需要时使用的 `idempotency_key`、`confirmation_id`、`file_id`。省略 JSON body 与显式 `null` 保持区别。`file_id` 可以代替上传过的 JSON body，两者不能同时提供。

主动提供商用量查询和 Grok 配额探测可能恢复提供商状态或调整调度限流，使用 `execute_operation`。`providers.usage.passive.list` 对应 `GET /api/v1/admin/providers/:id/usage/passive`，只读取 Anthropic OAuth/SetupToken 的已采集数据；其他平台保留原被动查询的不支持错误。

执行器由目录决定方法和路径，拒绝可改变路由的路径分隔符、转义以及未知参数。调用方不能提供目标主机或认证请求头。内部 HTTP 请求通过私有 context 携带令牌身份，原管理员中间件复核最终路由，再运行已有的限流、审计、强认证和业务处理。

结果包含请求关联 ID、HTTP 状态和原接口数据。JSON 与有限 SSE 的数值在通用转换中保留原十进制表示，大整数 ID 不经过浮点舍入。文本结果与结构化结果使用同一份数据，业务错误通过 MCP 的 `isError` 返回。`succeeded` 表示该 API 调用完成。创建备份等后台任务后，仍要查询任务状态。执行器不自动重试写操作，也不提供跨管理接口的事务回滚。响应中断时，写操作返回 `outcome_unknown`，不能据此判断业务未执行。

在 MCP 管理页编辑手工令牌或 OAuth 应用时，可以开启「工具结果脱敏」（`redact_tool_results`）。新建和已有凭据默认关闭，保存后从下一次工具调用生效。管理 API 更新凭据时，省略该字段或传 `null` 会保留已保存的值。关闭时保留原接口返回的字段。开启时对内联 JSON（含业务错误）和有限 SSE 事件应用 MCP 字段规则。浏览器确认预览和审计按各自的规则脱敏，凭据导出仍通过受保护文件交付。

已有幂等接口可以接收 `idempotency_key`，继续使用原协调器；未声明支持的接口拒绝该参数。普通审计沿用异步尽力写入机制，额外记录 `mcp_token_id` 和 `mcp_operation_id`。审计额外记录 `mcp_source`：空值表示手工令牌，`oauth` 表示应用授权。应用授权使用 `oauth:<id>` 作为审计标识。审计关键词可以搜索显示前缀，管理页据此链接到对应令牌的记录。上传文件的审计正文整体省略，防止编码后的凭据绕过字段脱敏。

浏览器确认、状态查看和文件下载也记录关联令牌与操作，同时保留实际 JWT 认证方式。令牌的审计入口默认按显示前缀和 MCP 认证方式查询；将认证方式切换为“全部”后，可以同时查看智能体执行和管理员接续记录。链接里的筛选会回填到审计页控件，路由变化时重新同步。

<a id="browser_confirmation"></a>
## 浏览器确认

综合设置、身份与权限变更、凭据导出、MCP 令牌管理等操作在目录中标记为敏感。默认调用先返回 `confirmation_required` 和确认链接，业务尚未执行。

管理员可以为单个令牌开启 `skip_browser_confirmation`（跳过浏览器确认），默认关闭。开启后，授权范围内的敏感操作可以直接执行；权限检查、受保护文件交付及浏览器授权流程继续生效。存在已启用的近期 TOTP 要求时，仍通过浏览器确认取得当前会话授权。目录的 `step_up_when` 用参数 schema 表达条件要求，例如用户创建和编辑仅在 `body.role=admin` 时要求近期验证。执行按实际参数判断，确认记录保留该次判断，浏览器批准时完成验证。发现阶段没有具体参数时将此类操作提示为可能需要确认；`describe_operation` 给出条件。工具发现返回按当前令牌计算的 `requires_confirmation`，审计用 `mcp_confirmation_skipped` 标记直接执行。

开关与权限一起持久化，每次调用重新读取。直接执行也绑定已读取的配置版本；关闭开关或修改令牌后，尚未进入原路由的旧请求会被拒绝。

确认记录保存在 Redis，包含管理员、令牌、令牌配置版本、操作描述与精确请求摘要、脱敏预览和十分钟有效期。数字的摘要和脱敏预览保留原始十进制表示，公开配置的空值、零值和布尔值保留原类型。摘要始终绑定原始参数，预览脱敏不会使不同凭据或不同价格上限共用一次批准。服务端格式化预览，页面直接显示该文本，避免大整数经过浏览器数值解析后改变显示值。预览省略超过 256 KiB 的输入，紧凑 JSON 超过 16 KiB 时截断并标记，再对完整 JSON 缩进。敏感原文由后续工具重试重新提交；普通 HTTP 审计仍使用独立的审计脱敏规则。

管理员在浏览器登录后，确认链接在 MCP 管理页直接打开操作确认弹窗，展示操作说明和脱敏请求预览。加载、失败和批准结果都在弹窗中显示；取消或关闭只移除 URL 中的确认参数，不会批准操作。提交批准或等待近期 TOTP 验证时，弹窗保持打开；需要近期 TOTP 的操作复用现有验证流程。工具携带确认 ID 重新提交相同参数，服务端复核权限、摘要、版本及必要的近期验证，再原子消费确认。已消费、过期、参数改变、目录改变或令牌配置变化的记录不能继续执行。

确认 URL 本身不提供权限。MCP 内部身份及管理 API Key 不能替代浏览器会话进行批准。需要直接输入 TOTP 的审计清空操作，接续到已有操作日志页面完成。

拥有 `mcp.confirmations.get` 权限的令牌可以查询自己发起的确认状态，同时仍需保有所确认操作的权限。同一管理员的其他令牌不能读取该记录；查询只返回脱敏预览，不返回会话标识或参数摘要。

<a id="files_and_interaction"></a>
## 文件与交互流程

普通 JSON 输出保留原 DTO 的脱敏。开启工具结果脱敏后，MCP 再遮蔽敏感键。已登记的 token 数量、限制、价格、配置开关、凭据存在标记及 API Key 管理元数据按明确的字段名和 JSON 类型保留。`null` 保留为空值，例如定价区间的 `max_tokens:null` 仍表示无上限。元数据对象和数组继续递归检查内部凭据，异常类型不会通过元数据规则放行。

字段归一化兼容大小写和分隔符差异；不同协议共用归一化名称时，明确登记各自允许的结构。例如 OpenAI 的 `prompt_tokens_details` 对象和 Gemini 的 `promptTokensDetails` 对象数组都保持可读，内部凭据仍被遮蔽。审计的 `extra.mcp_token_id` 保留数字类型，提供商的 `session_token_present` 和 `antigravity_force_token_refresh` 保留布尔类型。

`key`、`code` 和 `authorization` 等歧义名称还需匹配操作和字段路径：默认价格项标识、用户属性键、运维锁名称和协议授权类别保持可读，HTTP 错误代码和 SSE 错误事件代码也保留。相同名称在凭据或其他嵌套位置继续遮蔽，真实凭据不会因其值是数字或布尔值而放行。字段处理不修改原响应或确认参数，也不将数字字符串转换成数值。

原始凭据导出、新签发的 MCP 令牌、兑换码和包含优惠码的结果、独立刷新得到的上游凭据、二进制结果和较大的 JSON 以受保护文件交付，不展开到模型上下文。

临时文件绑定管理员、令牌和目标操作，有效期十分钟；Redis 中的文件内容及元数据使用部署已有的 AES 密钥加密。每次下载重新检查所属管理员和当前令牌权限。大型备份保存受保护的下载请求，在浏览器下载时进入原流式 handler，不把归档整体放入 Redis。

下载沿用原响应的文件名；没有文件名的 JSON 结果使用操作 ID 加 `.json`。页面读取 `Content-Disposition`，保留普通或 UTF-8 文件名和扩展名；已允许的跨域响应也暴露该响应头，支持前后端分开部署。

上传只接受编码的 JSON 数据，不能用服务器文件路径或任意远程 URL 作为文件来源。MCP 调用默认绑定当前授权，省略 `token_id` 和 `source` 即可上传；浏览器调用提交所属令牌或应用的 ID 及来源。工具引用上传文件时再次校验操作和归属。

提供商连接测试复用原测试入口，有限 SSE 需要成功终态，流中错误不会因 HTTP 200 被当成成功。OAuth、PAT、SSO 等交互授权操作接续到已有提供商管理页，通过“添加提供商”或“重新授权”完成；保存后可用 `providers.list` 查询并继续配置。实时 QPS/TPS 的 MCP 操作使用现有查询接口，返回相应时间窗口的数据。

<a id="client_setup"></a>
## 客户端接入与部署

嵌入前端资源的发行构建将 `/mcp` 保留为后端入口，GET、POST 和 DELETE 均跳过 SPA 页面回退。反向代理也需要把这个路径转发给主 HTTP 服务。

同机反向代理可以连接 `127.0.0.1:8080` 并保留公开域名的 `Host`。`/mcp` 在 SDK 分发前执行专用 Bearer 认证和下述 Origin 检查，并关闭 SDK 面向本地 MCP 服务的回环 Host 限制。直接运行二进制和使用 Docker 端口映射时，都可以沿用这一代理配置。

客户端配置 Streamable HTTP 地址及专用 Bearer 令牌。支持读取环境变量的客户端应把凭据留在其密钥配置中。例如 Codex：

```toml
[mcp_servers.tokenrouter]
url = "https://gateway.example.com/mcp"
bearer_token_env_var = "TOKENROUTER_MCP_TOKEN"
```

这是手工令牌接入。OAuth 客户端填写同一个 `/mcp` 地址，通过下文的发现和浏览器授权流程连接。Codex 的 HTTP Bearer 配置见[官方文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。

浏览器 Origin 必须命中明确配置的 CORS origin 或站点公共 URL；`*` 不作为 MCP 允许来源。原生客户端可以省略 Origin。配置 `frontend_url` 后，浏览器接续返回对应公开站点的完整链接；未配置时返回同站路径，客户端按 MCP 服务来源解析。

每个令牌每分钟最多 300 个 MCP HTTP 请求，Redis 计数不可用时拒绝准入。后端最多同时处理八个 MCP HTTP 请求；外层入口与 SDK 的请求体限制均为 16 MiB，内部有限响应捕获上限也为 16 MiB，单次执行使用两分钟超时。大 JSON 使用文件交付，备份下载按原接口流式传输。限制与推理网关流量分别处理。

迁移 300 增加令牌表，迁移 301 增加默认关闭的跳过确认设置。迁移 310 给令牌和 OAuth 应用授权增加工具结果脱敏设置，已有行取默认值 `false`。回退到支持 MCP 但早于此开关的版本时，可以保留新增列，旧后端会对工具结果执行额外脱敏。既有管理员、网关 Key 和登录凭据不转换。部署沿用现有 PostgreSQL、Redis 和持久加密密钥。临时确认和文件按 TTL 过期。完整数据备份包含令牌摘要与密文，恢复完整令牌还需要对应的固定加密密钥。迁移与回退遵循[部署与数据库迁移](../operations/deployment_and_migrations.md)。
