# 身份与租户

本文说明用户、外部登录身份、会话、管理员权限和团队之间的关系，以及认证与归属变更需遵守的约束。端点格式和配置见 [HTTP 接口](../interfaces/http_api.md)和[配置](../interfaces/configuration.md)。

## 章节导航

- [核心实体](#核心实体)：模块归属、实体约束、MCP 凭据与用户查询规则。
- [认证入口](#authentication_boundaries)：邮箱域名名额、人机验证、Google One Tap 与 JWT 校验。
- [会话生命周期](#会话生命周期)：Token 轮换、多标签页刷新、撤销范围与邮箱挑战。
- [强认证与敏感操作](#强认证与敏感操作)：TOTP step-up 与 Passkey 流程约束。
- [外部身份接入](#外部身份接入)：OAuth pending 意图与防重放要求。
- [登录提供方差异](#登录提供方差异)：七类登录提供方的主体、流程与首次绑定权益。
- [身份绑定与解绑](#身份绑定与解绑)：第三方绑定、邮箱换绑别名查重与解绑清理。
- [用户属性与来源](#用户属性与来源)：精确查找、自定义属性定义与外部资料同步。
- [团队租户](#团队租户)：单层租户模型、报表范围、所有权转移与团队 Key 校验。
- [API Key 凭据轮换](#api_key_rotation)：凭据替换、并发保护与缓存失效传播。
- [领域不变量](#领域不变量)：改动前后的检查清单。

## 核心实体

身份与租户相关能力按主题拆分：

- `internal/identity`：维护用户资料、注册与绑定规则、会话、强认证和用户属性。
- `internal/team`：维护单层团队租户、成员限额与邀请流程。
- `internal/apikey`：维护 API Key 生命周期和认证缓存（缓存采用专用快照，读写时深复制）。

模块与上下文边界：

- 微信支付 OAuth 仅用于支付授权，与登录身份无关。
- Key 认证失败时仅面向 Ops 加载的诊断数据不能作为准入依据；资金来源和订阅字段的编码与读取时机由网关 HTTP 决定。
- `Principal` 表示已验证的身份和凭据种类；`AccessSnapshot` 分别记录 Key owner、付款用户、行为成员和团队。
- 核心 `User` 不嵌套 API Key，HTTP 关联字段由 DTO 补充。资金消费、调账和注册赠送写入归 `billing`，跨模块变更在事务提交后发布缓存失效通知。

| 实体 | 含义 | 关键约束 |
| --- | --- | --- |
| `User` | 本地账户，记录角色、状态、余额和并发额度 | 软删除；登录和每次请求复核启用状态；`token_version` 由当前邮箱和密码哈希推导，JWT 声明须与当前指纹一致 |
| `AuthIdentity` | 外部认证提供方中的稳定身份，归属一个用户 | `(provider_type, provider_key, provider_subject)` 全局唯一；须使用全局主体，渠道级标识不足以判重 |
| `PendingAuthSession` | OAuth 等跨页面流程的短期一次性状态 | 绑定浏览器会话、完成码摘要和过期时间，完成时原子消费 |
| JWT access token | 短期访问声明，用户状态以数据库为准 | 携带用户、角色、token version、refresh family `sid` 和可选绑定摘要；每次请求仍查询当前用户 |
| Refresh token | 续签凭据及其所属会话族 | 服务端仅存摘要；轮换后旧 token 失效，同一 `sid` 可整体撤销 |
| `Team` / `TeamMember` | 单层团队租户和成员关系 | 一个团队仅有一个 owner；成员角色和周期限额归属成员关系，与全局角色独立 |
| 团队 API Key | 团队上下文里的网关凭据 | owner 为付款用户，创建者或调用成员为行为主体，两者分别记录 |

角色、凭据与用户查询约定：

- 角色隔离：`User.role` 中的 `admin` 是全局后台角色，`TeamMember.role` 的 `owner` 和 `member` 仅在单团队内有效。请求授权由用户状态、团队状态、成员关系和资源归属共同决定。
- MCP 专用令牌：归属签发管理员，保存独立权限、有效期和撤销状态；认证校验摘要，完整令牌加密存储供所属管理员复制。每次调用读取当前用户和令牌，内部请求使用 `mcp_token` 身份，敏感操作默认需所属管理员浏览器会话确认（单令牌可配置跳过，近期 TOTP 要求仍生效）。
- MCP OAuth 应用授权：维护独立客户端、权限与撤销记录，使用短期访问凭据和轮换刷新凭据，以来源类型加稳定授权 ID 区分归属，刷新不改变确认归属。详见 [MCP 管理接口](../interfaces/mcp_management.md#access_tokens)。
- 管理用户列表查询：最近使用时间取自事务内维护的[用户活动汇总](../operations/observability_and_data_lifecycle.md#user_activity_summary)。排序在筛选后、分页前执行，升序空值在前、降序空值在后，相同时间按同方向用户 ID 排序。搜索按邮箱、用户名、备注和 API Key 分别查候选用户 ID 后去重合并，计数与分页共用条件。
- 语言与属性原文：`User.preferred_locale` 为可空账户语言偏好，选择顺序见[用户侧国际化](../interfaces/user_localization.md#language_selection)。管理员用户属性表单的定义和值按原文保存。

<a id="authentication_boundaries"></a>
## 认证入口

面板认证路由由 `RegisterAuthRoutes` 注册。公开的登录、注册、验证码、Passkey 登录、token 刷新、密码找回和高风险校验各有服务端限流；依赖 Redis 的限流器在缓存故障时拒绝请求（fail-close）。OAuth 的 start 和 callback 建立外部身份流程，受保护账户管理入口附加 JWT 中间件。

### 注册邮箱域名

- 域名白名单为空时，普通注册和 OAuth 邮箱补全允许任意域名；非空时默认拒绝白名单外域名。
- 开启运行时设置 `registration_email_domain_quota_enabled` 后，白名单外邮箱按公共后缀规则归并到可注册主域名（eTLD+1），每个主域名最多 1 个未删除用户（子域名共享名额），白名单域名不限数量。
- 发送验证码前预检名额；创建用户在注册事务内重读开关、锁住主域名并复查，防止并发或设置变更绕过。
- 当前用户绑定或换绑邮箱、已验证邮箱的 OAuth 自动建号仅认白名单，不适用名额规则。

### 人机验证

公开认证入口统一在 Cloudflare Turnstile、腾讯天御和阿里云验证码 2.0 间择一启用，三者互斥：

- 覆盖范围：普通登录、注册、发验证码和密码找回校验当前启用的验证码。腾讯天御和阿里云还保护 Passkey 登录的 begin 和 OAuth 登录的 start；票据仅随触发操作提交，finish 和 callback 不再复用。
- 豁免与缺失处理：OAuth 当前用户绑定的 start 要求已登录，不校验匿名登录验证码。验证码已启用但服务或凭据配置不完整时拒绝请求。
- 腾讯天御站点要求：`cn` 和 `intl` 要求前端 SDK 与服务端校验 endpoint 一致。国际站在当前表单容器展示 checkbox，票据仅缓存给一次操作，过期、失败或手动重置后立即销毁并重新初始化。
- 插件可选校验：`VerifyOptionalCaptcha` 返回当前配置是否启用验证码供插件判断关闭时的行为；认证入口 `VerifyCaptcha` 仍在该结果上执行启动配置中的必需验证码检查。

### Google One Tap

Google One Tap 是 Google 登录的浏览器端凭据入口，身份类型仍为 Google。

- 前端展示条件（须全部满足）：用户未登录、公开设置完整、非 backend mode、Origin 安全、满足登录协议要求，且腾讯与阿里云验证码均关闭（Turnstile 不覆盖此入口）。
- 服务端验签：只接受经 Google 官方验证器校验过签名、`aud`、`iss`、`exp` 的 ID Token，并要求 `sub` 非空、带邮箱且 `email_verified=true`。
- 归属与拒绝：`sub` 为稳定主体。已有用户直接检查状态并签发 token pair；新用户写入 `PendingAuthSession` 进入密码、邀请码、邮箱策略、注册开关和优惠码补全流程。One Tap 关闭、Google OAuth 配置不完整、验证码启用、backend mode、注册关闭或身份不可登录时均拒绝；原始 token 和完整 claims 不写日志。

### JWT 校验

普通面板请求经过 JWT 中间件时依次校验：

1. 仅接受配置的 HMAC 签名方法，限制 token 长度，解析签发与过期时间。
2. 从数据库读取当前用户，不存在、已删除或未启用时拒绝。
3. 核对声明中的 `token_version` 与 `ResolvedTokenVersion` 计算出的当前凭据指纹（由规范化邮箱和 `password_hash` 推导），不一致时拒绝。
4. 开启会话绑定时核对客户端绑定摘要 `bnd`，通过后将用户 ID、邮箱和角色写入 Gin 上下文。

管理员路由接受两种凭据：

- 管理员 JWT：照常校验用户状态和 `token_version`。
- `x-api-key` 管理密钥：代表配置中的第一个实际管理员账户。敏感设置开启 step-up 后，管理密钥无法通过二次验证，须改用带有效 `sid` 的管理员 JWT。
- WebSocket：可从约定子协议读取 JWT，同样核对当前用户。

## 会话生命周期

登录成功后签发 access token 和 refresh token。refresh token 原文仅发给客户端，服务端只存摘要，并按用户与 `sid` 维护会话族。

刷新流程：

1. 核对摘要、用户状态、`token_version` 和适用的绑定摘要。
2. 原子删除 Redis 中对应 key，删除成功的一方取得唯一消费权。
3. 在同一会话族内签发新一对 token。

并发与客户端协同约束：

- 并发刷新：仅消费成功的一方继续，消费失败返回服务不可用，新旧凭据不同时有效。校验阶段遇暂时性错误时不消费旧 token；新凭据生成失败时旧 token 保持失效。客户端须将刷新视为轮换，不能并发使用同一旧 token。
- 多标签页协同：同文档共享进行中的刷新 Promise；支持 Web Locks 时用固定锁名串行同源标签页刷新，持锁后若同用户已在其他标签页轮换则直接复用，不支持时失败方有限等待。写本地存储前复核 refresh token 和用户快照，轮换后的 token 最后写入作提交标记；期间登出或切换账号丢弃旧结果。

撤销范围与会话绑定：

- 主动登出删除提交的 refresh token；安全登出或绑定异常可撤销整个 `sid`。
- 用户停用由状态检查拒绝既有凭据；邮箱或密码变更由凭据指纹拒绝旧 token。
- 刷新发现用户失效、版本或客户端绑定不匹配时，删除对应会话族并停止签发。
- `RevokeAllUserTokens` 先确认用户存在再尝试删除其 refresh 会话，清理失败仅记日志。既有 access token 仍按过期时间、用户状态、凭据指纹和绑定校验；`users` 表无独立 `token_version` 列，仅修改内存值无持久撤销效果。
- 可选会话绑定使用安全解析的客户端 IP 和规范化 User-Agent 生成摘要。无摘要的旧 access token 放行至下次刷新；带摘要但不匹配时写审计并撤销会话族。仅在配置可信代理时才信任代理转发头。

<a id="email_challenges"></a>
### 邮箱挑战与通知

邮箱挑战与密码重置凭据由 `identity` 维护，通知模块仅接收准备好的验证码、重置链接或验证事件。验证码在队列 worker 执行时生成。

| 场景 | 存储与发送顺序 | 失败语义 |
| --- | --- | --- |
| 普通验证码 | 先存储再发送 | 保留已存凭据，不做回滚 |
| 通知邮箱验证 | 先发送再存储 | 发送失败不落库，不做统一回滚 |
| 密码重置 | 令牌未过期时复用旧令牌，带冷却时间 | 发送失败不回滚，不重新生成令牌 |

模板、取消和投递规则见[通知与邮件投递](notification_delivery.md)。

## 强认证与敏感操作

- TOTP 与 step-up：TOTP 密钥加密持久化，设置流程与登录挑战使用带 TTL 的缓存状态。管理员修改敏感设置需要绑定 JWT `sid` 的近期 TOTP step-up grant；TOTP 未启用、缺少 `sid`、grant 过期或服务不可用时均按 fail-close 拒绝。
- Passkey（WebAuthn）：持久凭据与短期 challenge/session 分离，finish 仅消费匹配状态；注册需已登录，登录 finish 签发标准 token 对并受同样会话约束。开启腾讯天御或阿里云验证码时，匿名登录 begin 消费票据，finish 不复用。验证前先消费 session 再交 WebAuthn SDK，响应损坏也无法重放。

## 外部身份接入

外部身份统一映射为 `AuthIdentity`，用户长期归属以稳定外部主体为准，邮箱仅作输入。OAuth pending 流程支持三种意图：登录或创建账号、绑定到当前用户、经用户确认后接纳同邮箱已有账户。

pending 流程安全要求：

- 回调先验证供应商状态，再写入短期 `PendingAuthSession`，回调 URL 不暴露长期登录 token。
- 浏览器会话键、完成码摘要、有效期和 verified 状态共同约束完成请求。
- 绑定、建号或接纳时在事务内复查外部主体唯一性，并原子写入决定与消费状态。
- 已消费、已过期或浏览器不匹配的 session 不可重放；确认目标用户和归属后才签发 token 对。
- 新增提供方须复用 pending 会话和身份唯一性检查，禁止按未验证 profile 字段直接合并用户。

## 登录提供方差异

`AuthIdentity.provider_type` 支持 `email`、`github`、`google`、`linuxdo`、`oidc`、`wechat` 和 `dingtalk`。`provider_key` 区分同类型下的发行方或兼容渠道，`provider_subject` 为该 key 下的稳定外部主体，三者组合全局唯一。

| 提供方 | 持久主体与验证要求 | 流程差异 |
| --- | --- | --- |
| Email | 规范化且已验证的邮箱；本地密码由用户安全状态管理 | 支持注册、登录、找回密码及当前用户验证码绑定；不能走通用第三方解绑 |
| LinuxDo | LinuxDo subject；profile 邮箱和用户名仅作已验证资料输入 | 支持 start/callback、pending exchange、建号或绑定已有登录、当前用户 bind |
| WeChat | 优先使用稳定 union/open identity，兼容历史 provider key 和 openid 渠道 | 登录和当前用户绑定走 pending 流程；支付 OAuth 与登录身份无关 |
| OIDC | `provider_key` 为 issuer，`provider_subject` 为该 issuer 下 subject | 支持建号、接纳、绑定已有登录和当前用户 bind；不同 issuer 下同名 subject 为不同人 |
| GitHub | GitHub user ID；注册须拿到已验证邮箱 | 支持登录和注册完成；个人资料页无自助绑定和解绑 |
| Google | Google `sub`；注册须拿到已验证邮箱 | OAuth 跳转与 One Tap 共用唯一性、pending 注册和首次绑定权益；个人资料页无自助绑定和解绑 |
| DingTalk | unionID 为稳定 subject，企业与部门信息存为 claims 或用户属性 | 支持组织策略、建号或绑定已有登录、当前用户 bind；跨组织降级仍以 unionID 为主体，企业内 user ID 仅作资料 |

新增绑定路由或前端入口前，须补齐服务端意图、CSRF/state、pending session、解绑安全检查和接口测试，不可直接复用 OAuth callback。

默认权益发放：

- 注册时合并全局默认值与认证来源默认值，形成一次性创建计划。
- 第三方身份首次绑定按 `(user, provider, first_bind)` 幂等发放允许的余额、并发或订阅默认值；重新绑定、callback 重放和 profile 更新不重复触发。

## 身份绑定与解绑

第三方绑定通过受保护入口生成后端 authorize URL，start 记录 `bind_current_user` 意图，callback 校验当前会话与外部主体。

Email 绑定与换绑规则：

- 尚未绑定实际邮箱的用户可随时首次绑定；验证并绑定相同邮箱不算换绑。
- 更换已有实际邮箱需开启 `user_email_change_enabled` 并验证当前密码（修改 profile 邮箱字段不属于绑定）。发验证码和提交换绑两阶段均检查该开关，配置缺失或读取失败时拒绝。
- 邮箱查重同时比对精确地址和收件箱 alias：Gmail 与 googlemail 忽略本地部分点号并统一域名；所有域名按既定策略折叠 `+` 后缀和域名根部的点。允许换绑时可换为自己的另一 alias；同一收件箱被其他用户占用时拒绝。

事务与解绑约束：

- 绑定事务校验外部三元组不属他人、当前用户与会话有效、pending 意图与 provider 一致，然后原子写入 identity、channel 和接纳决定。Email 换绑在同一事务中锁住规范化邮箱和收件箱 alias 复查占用者，再更新邮箱和密码哈希。管理员直接绑定遵守同等唯一约束和标准 `provider_key` 规则。
- 解绑后用户至少保留一个可登录身份。Email 不走第三方解绑；LinuxDo、OIDC、WeChat 和 DingTalk 自助解绑依据当前身份集合判定。
- 解绑成功且认证服务可用时调用 `RevokeAllUserTokens` 清理 refresh 会话，既有 access token 继续按会话校验规则判定。远端授权是否撤销取决于各 provider 实现，仅删本地记录时远端 token 可能仍有效。

## 用户属性与来源

精确用户查找：

- 集成侧使用 `UserService.LookupUsersExact`（与管理员模糊搜索分离），仅按 ID、用户名或大小写不敏感邮箱相等匹配未删除账号，最多返回 100 条资料及头像 URL。
- 不返回备注、密码、API Key 或任意身份 metadata；插件的邮箱权限和返回字段筛选由插件模块单独校验。

自定义用户属性：

- 结构与类型：分为 definition 和 value。definition 含唯一 key、展示名、类型（`text`、`textarea`、`number`、`email`、`url`、`date`、`select`、`multi_select`）、options、required、validation、排序和 enabled。
- 存储规则：每用户在每 definition 下最多 1 个 value，以字符串保存（`multi_select` 存 JSON 数组字符串）。
- 写入按类型、选项和 min、max、length、pattern 校验；`required` 仅约束编辑和收集流程，历史用户可为空。禁用 definition 后界面隐藏输入项但保留已有值；删除 definition 一并处理其 values，前端提交孤立 key 会被拒绝。

外部资料与策略隔离：

- OAuth claims 提供用户名、头像、企业邮箱、显示名或部门建议资料，DingTalk 等可同步到管理员配置的属性 key。外部资料记录 provider 与来源，上游缺字段时保留用户手填内容；保留域名合成的邮箱属占位邮箱，不算已验证邮箱。
- 自定义属性默认仅作资料展示，角色、团队、余额和网关授权均不读取；未来若用于策略，须配套明确规则、审计和缓存失效。

## 团队租户

团队是单层租户，无嵌套团队。owner 既是管理者，也是团队 Key 的付款主体。

- 消费归属：普通成员按权限创建或使用团队资源，每笔消费同时记录团队、付款 owner 和行为成员。成员日、周、月限额和累计用量归属 `TeamMember`；团队更换 owner 后，历史行为仍归原成员和原 owner。
- 团队报表：owner 可查看全队，普通成员查询固定为本人。总览和成员趋势同时应用成员与 API Key 筛选；历史有用量的离队成员出现在趋势中，无用量当前成员显示零值。数据源和刷新见[使用记录预聚合](../operations/pre_aggregation.md#query_routing_and_fallback)。
- 成员与所有权变更：邀请 token 存摘要并绑定目标邮箱，接受后建立成员关系。owner 须先转移所有权或解散团队才能离队。所有权转移、成员移除或离开、团队暂停和限额变更后，失效相关团队 Key 缓存。管理员强制转移所有权同样原子维护唯一 owner 和缓存。
- 团队 Key 认证检查：每次认证依次核验团队功能启用、团队 `active`、付款 owner `active`、行为成员存在且 `active`，且成员未在 Key 创建后离队重加。任一项不满足即拒绝，团队 Key 不退化为 owner 个人 Key。

<a id="api_key_rotation"></a>
## API Key 凭据轮换

用户可在 Keys 页更多菜单轮换本人名下的普通 Key 或复合 Key；团队 Key 按编辑规则校验团队上下文；系统托管 Key 不向用户开放轮换。

- 保留属性：轮换前需确认，成功后展示可复制新凭据。操作仅更新 `key` 和 `updated_at`，ID、归属、分组映射、状态、到期时间、额度、限流窗口和历史用量全部保留；已禁用或额度用完的 Key 轮换后状态不变。
- 原子替换：存储层按 ID、所有者和旧凭据条件原子替换，排除软删除和托管记录。并发轮换或删除导致条件不匹配时返回冲突，已生效新凭据不受影响。
- 失效传播：写入成功后清除新旧凭据认证缓存并广播失效；数据库触发器同事务为两者写失效 outbox，通知失败和在途回填由重试与延迟二次失效处理。已通过鉴权的在途请求继续执行，客户端需改用新凭据，统计与费用继续记在原 Key ID 下。

HTTP 接口见 [API Key 凭据轮换接口](../interfaces/http_api.md#api_key_rotation)。

## 领域不变量

- 授权以数据库当前状态为准；JWT、缓存和外部 profile 均为有时效的输入。
- 一个外部主体只属于一个用户；接纳已有账号需要明确、可审计、不可重放的决定。
- 身份由 provider type、key 和 subject 共同确定；邮箱、用户名、头像和组织资料都不是稳定主体。
- access token、refresh token、OAuth pending session、TOTP 和 Passkey challenge 各有用途与有效期，互不可代用。
- 管理员全局角色与团队内角色独立；API Key 的付款主体和行为主体分开记录。
- 会话撤销、`token_version`、团队生命周期和 Key 缓存失效在所有认证入口行为一致。
- 首次绑定默认权益幂等发放；自定义属性和外部 profile 默认不参与授权。
- 新增认证方式时，同步补齐公开入口限流、审计、会话撤销、前端 token 处理和对应安全测试。

相关文档：[项目总览](../project_overview.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[领域目录](index.md)。
