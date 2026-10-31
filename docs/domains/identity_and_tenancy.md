# 身份与租户

本文说明用户、登录身份、会话、管理员权限和团队之间的关系，以及认证和归属变更需要遵守的约束。端点格式和配置见 [HTTP 接口](../interfaces/http_api.md)和[配置](../interfaces/configuration.md)。

## 章节导航

- [核心实体](#核心实体)：用户、身份、会话和团队各是什么。
- [认证入口](#认证入口)：公开入口、JWT 和管理员凭据。
- [会话生命周期](#会话生命周期)：修改签发、刷新、撤销和绑定时读取。
- [强认证与敏感操作](#强认证与敏感操作)：修改 TOTP、Passkey 或管理员 step-up 时读取。
- [外部身份接入](#外部身份接入)：修改 OAuth 登录、绑定或账号接纳时读取。
- [登录提供方差异](#登录提供方差异)：各登录提供方的主体、邮箱和完成流程。
- [身份绑定与解绑](#身份绑定与解绑)：修改当前用户绑定、首次绑定权益或会话撤销时读取。
- [用户属性与来源](#用户属性与来源)：修改自定义字段或外部资料同步时读取。
- [团队租户](#团队租户)：修改成员、所有权或团队 Key 时读取。
- [API Key 凭据轮换](#api_key_rotation)：修改凭据替换、并发保护或旧凭据失效时读取。
- [领域不变量](#领域不变量)：改动前后的检查清单。

## 核心实体

用户资料、注册和绑定规则、会话、强认证和用户属性在 `internal/identity` 实现，团队在 `internal/team` 实现，Key 的生命周期和认证缓存在 `internal/apikey` 实现。各模块按需要使用 `postgres`、`rediscache`、`provider` 和 `httpapi` 子包处理存储、外部验证和 HTTP。用户实体和仓储接口都使用 identity 的类型，app 把同一个 `identity/postgres.UserStore` 交给用例和所有调用方。

公告和 billing 各自读取自己需要的只读用户数据。七类身份、通用 pending 流程和 OAuth 回调的生产 HTTP 由 app 一次性组装；跨模块的 HTTP 测试使用 `identity/httpapi` 的同一组端点。微信支付 OAuth 由 `payment/httpapi` 调用身份 provider 的交换接口完成，它只用于支付授权，与登录身份无关。

身份 HTTP 和调用方直接使用 `identity/httpapi/authctx`。Key 认证成功后的数据、认证失败时只给 Ops 读取的加载数据，以及强制平台字段，由 `apikey/httpapi` 提供；认证失败时加载出的数据不能作为准入依据。资金来源和订阅的读取由 gateway HTTP 负责，字段编码和读取时机由它决定。

`Principal` 表示已验证的身份和凭据种类；`AccessSnapshot` 分别记录 Key owner、付款用户、实际行为的成员和团队。身份核心里的 `User` 不嵌套 API Key，HTTP 需要的关联字段由 DTO 补充。资金消费、调账和注册赠送的写入由 billing 负责；身份和团队的事务通过同连接参与能力把 billing 纳入同一事务，事务提交后才发布失效通知。

Key 的使用方直接使用 `apikey.APIKey`、`APIKeyRepository` 和 `APIKeyService`（只有一个实例），分组策略使用 `routing.Group`。app 交付同一个 `apikey/postgres.KeyStore`；认证缓存使用专用快照，读写时深复制。同连接删除和分组迁移直接绑定存储的参与能力。

| 实体 | 含义 | 关键约束 |
| --- | --- | --- |
| `User` | 本地账户，记录角色、状态、余额和并发额度 | 软删除；登录和每个请求都重新确认用户处于启用状态；`token_version` 来自当前邮箱和密码哈希的指纹，JWT 声明需与当前指纹一致 |
| `AuthIdentity` | 某个外部认证提供方里的一个稳定身份，属于一个用户 | `(provider_type, provider_key, provider_subject)` 唯一；判断身份要用全局主体，渠道级标识不够 |
| `PendingAuthSession` | OAuth 等跨页面流程的短期一次性状态 | 绑定浏览器会话、完成码摘要和过期时间；完成时原子消费 |
| JWT access token | 短期访问声明，用户状态以数据库为准 | 携带用户、角色、token version、refresh family `sid` 和可选的绑定摘要；每个请求仍查询当前用户 |
| Refresh token | 续签凭据和它所属的会话族 | 服务端只保存摘要；轮换后旧 token 失效，同一个 `sid` 可以整体撤销 |
| `Team` / `TeamMember` | 单层的团队租户和成员关系 | 一个团队只有一个 owner；成员角色和周期限额属于成员关系，和用户的全局角色分开 |
| 团队 API Key | 团队上下文里的网关凭据 | owner 是付款用户，创建者或当前调用的成员是行为主体，两者分别记录 |

`User.role` 里的 `admin` 是全局后台角色；`TeamMember.role` 的 `owner` 和 `member` 只在一个团队内有效。两套角色互相独立。请求是否允许，由用户状态、团队状态、成员关系和具体资源的归属共同决定。

MCP 专用令牌属于签发它的管理员，保存独立的操作权限、有效期和撤销状态。认证使用摘要，完整令牌加密存储，所属管理员可以在浏览器中复制。每次调用读取当前用户和令牌；内部管理请求使用 `mcp_token` 身份，敏感操作默认通过所属管理员的浏览器会话确认；每个令牌可选择跳过 MCP 确认，既有近期 TOTP 要求继续生效。OAuth 应用授权拥有独立的客户端、权限及撤销记录，使用短期访问凭据和轮换刷新凭据。授权、确认和文件以来源类型加稳定授权 ID 区分，刷新不改变确认归属。完整规则见 [MCP 管理接口](../interfaces/mcp_management.md#access_tokens)。

管理员用户列表和详情的最近使用时间来自事务内维护的[用户活动汇总](../operations/observability_and_data_lifecycle.md#user_activity_summary)。排序在筛选后、分页前完成，升序把空时间放在前面，降序放在末尾；相同时间再按同方向的用户 ID 排序。搜索按邮箱、用户名、备注和 API Key 内容匹配，各字段分别查询候选用户 ID，再去重合并。计数和分页使用同一组条件。

`User.preferred_locale` 是可为空的账户语言偏好，当前用户资料接口负责查询和修改。主动选择使用规范语言代码，登录、浏览器保存和邮件选择的顺序见[用户侧国际化](../interfaces/user_localization.md#language_selection)。管理员用户属性表单的定义和值按原文保存。

<a id="authentication_boundaries"></a>
## 认证入口

面板认证入口的路由由 `RegisterAuthRoutes` 注册。公开的登录、注册、验证码、Passkey 登录、token 刷新、密码找回和高风险校验各自有服务端限流；依赖 Redis 的限流器在缓存故障时拒绝请求（fail-close）。OAuth 的 start 和 callback 负责建立外部身份流程，受保护的账户管理入口再加上 JWT 中间件。

### 注册邮箱域名

邮箱域名白名单为空时，普通注册和 OAuth 邮箱补全允许任何域名。白名单非空时，默认拒绝白名单以外的域名。如果在数据库运行时设置里开启 `registration_email_domain_quota_enabled`，其他邮箱可以按公共后缀规则归到可注册主域名（eTLD+1）注册，每个主域名最多一个未删除的用户，子域名共享这个名额；白名单里的域名不限数量。发送验证码前会先检查名额；最终创建用户时，在注册事务里重新读取开关、锁住主域名再复查一次，所以设置变化和并发请求都绕不过去。当前用户绑定或换绑邮箱、已验证邮箱的 OAuth 自动建号，都只认白名单，名额规则对它们不适用。

### 人机验证

公开的认证操作通过统一的验证码入口选择 Cloudflare Turnstile、腾讯天御或阿里云验证码 2.0，三者同时只能启用一个。普通登录、注册、验证码发送和密码找回会校验当前启用的验证码。腾讯天御和阿里云还保护 Passkey 登录的 begin 和 OAuth 登录的 start；票据只随触发它的操作提交，finish 和 callback 不再使用这张票据。腾讯天御的 `cn` 和 `intl` 站点要求前端 SDK 和服务端校验 endpoint 一致。国际站先在当前表单容器里显示 checkbox，成功拿到的票据只缓存给一次操作使用，过期、操作失败或手动重置后立即销毁并重新初始化。

OAuth 当前用户绑定的 start 要求用户已登录，匿名登录用的验证码在这里不需要。验证码已启用、但服务或必要凭据配置不完整时，拒绝请求。

### Google One Tap

Google One Tap 是 Google 登录在浏览器端的另一个凭据入口，身份类型仍是 Google。前端满足以下全部条件才请求 GIS 显示 One Tap：用户未登录、公开设置完整、不是 backend mode、Origin 安全、已满足登录协议要求、腾讯和阿里云的验证码都关闭。Cloudflare Turnstile 不覆盖这个入口。服务端只接受经 Google 官方验证器校验过签名、`aud`、`iss` 和 `exp` 的 ID Token，并要求 `sub` 非空、带邮箱、`email_verified=true`。

`sub` 作为 Google `AuthIdentity` 的稳定主体。已有用户直接走统一的 token pair 签发和用户状态检查；新用户写入 `PendingAuthSession`，然后进入同样的密码、邀请码、邮箱策略、注册开关和优惠码补全流程。One Tap 关闭、Google OAuth 配置不完整、验证码启用、backend mode、注册关闭或身份不可登录时，请求都会被拒绝。原始 token 和完整 claims 不写日志。

### JWT 校验

普通面板请求经过 JWT 中间件，至少依次检查：

1. 只接受配置的 HMAC 签名方法，限制 token 长度，解析签发时间和过期时间。
2. 从数据库读取当前用户，用户不存在、已删除或未启用时拒绝。
3. 比较声明里的 `token_version` 和 `ResolvedTokenVersion` 得到的当前凭据指纹。指纹由规范化后的邮箱和 `password_hash` 推导；声明与当前指纹不一致时拒绝。
4. 开启会话绑定时，校验 JWT 的客户端绑定摘要 `bnd`，然后把当前用户 ID、邮箱和角色写入 Gin 上下文。

管理员路由接受两种凭据，能力不完全相同。管理员 JWT 照常检查用户状态和 token version。`x-api-key` 管理密钥代表配置里的第一个实际管理员账户。敏感设置开启 step-up 后，管理密钥无法通过二次验证，需要使用带有效 `sid` 的管理员 JWT。WebSocket 场景可以从约定的子协议里读取 JWT，同样检查当前用户。

## 会话生命周期

登录成功后签发一对 access token 和 refresh token。refresh token 原文只交给客户端，服务端保存摘要，并按用户和 `sid` 维护会话族。刷新流程：

1. 验证摘要、用户状态、token version 和适用的绑定摘要。
2. 原子删除 Redis 里对应的 key，删除成功的一方取得唯一的消费权。
3. 消费成功后，在同一个会话族里签发新的一对 token。

并发刷新时，只有实际消费了凭据的一方能继续，消费失败时返回服务不可用，新旧凭据不会同时有效。读取用户等校验阶段出现暂时性错误时，token 保持未消费；新凭据生成失败时，旧 token 保持失效。客户端要把刷新当作轮换，同一个旧 token 不能并发使用。

浏览器里，同一个文档的刷新调用共享一个进行中的 Promise；支持 Web Locks 的浏览器还用固定的锁名让同源标签页串行刷新。拿到锁后重新读取持久保存的 token，如果同一用户刚在别的标签页完成了轮换，直接采用那个结果。不支持 Web Locks 时，竞争失败的一方在有限时间内等待新 token 发布。刷新响应写入本地存储前，再核对一次 refresh token 和用户快照，轮换后的 token 最后写入，作为提交标记。刷新期间如果用户登出或切换账号，旧请求的结果直接丢弃，新会话保持不变。

以下事件会撤销一个 token、一个会话族或用户的全部会话，范围由具体操作决定：

- 主动登出删除提交的 refresh token；安全登出或检测到绑定异常时，可以撤销整个 `sid`。
- 用户停用后，用户状态检查拒绝现有凭据；邮箱或密码变更后，按当前凭据指纹校验 token 版本。
- 刷新时发现用户已失效、版本不一致或客户端绑定不一致，删除对应的会话族，并停止签发。

`RevokeAllUserTokens` 先确认用户存在，再尝试删除该用户的 refresh token 会话；清理失败只记录日志。现有 access token 的有效性继续由过期时间、用户状态、凭据指纹和可选的客户端绑定校验决定。`users` 表没有独立的 `token_version` 列，仅修改内存中的版本值不会在后续请求中产生持久撤销效果。

可选的会话绑定用安全解析出的客户端 IP 和规范化后的 User-Agent 生成摘要。旧版 access token 没有绑定摘要时，暂时放行到下次刷新；带有摘要的 token 一旦不匹配，记录审计并撤销会话族。代理头只在配置了可信代理时才参与客户端 IP 的解析，其他来源的转发头不被信任。

<a id="email_challenges"></a>
### 邮箱挑战与通知

邮箱挑战和密码重置凭据由 `identity.EmailChallenges` 和 `identity/rediscache` 维护，通知模块只接收已经准备好的验证码、重置链接或验证事件。验证码在队列 worker 执行时生成。普通验证码先存储再发送；通知邮箱的验证先发送再存储；密码重置在令牌未过期时复用旧令牌，并有冷却时间。发送失败时，各流程保留上述顺序产生的结果，没有统一的凭据回滚，令牌也不会因此重新生成。模板、取消和投递规则见[通知与邮件投递](notification_delivery.md)。

## 强认证与敏感操作

TOTP 密钥加密后持久化，设置流程和登录挑战使用带过期时间的缓存状态。管理员修改敏感设置时，需要近期的 TOTP step-up grant，grant 绑定 JWT 的 `sid`。TOTP 未启用、会话 ID 缺失、grant 过期或 grant 服务不可用时，操作都被拒绝；这项检查开启后按 fail-close 工作。

Passkey 使用 WebAuthn 的注册和登录流程：持久凭据和短期的 challenge、session 分开存放，finish 只能消费与之匹配的流程状态。启用腾讯天御或阿里云验证码时，匿名登录的 begin 要先消费验证码票据，finish 不再携带或校验票据。注册入口要求用户已登录。登录的 finish 最终签发标准的 token 对，并经过同样的用户状态、版本和会话约束，Passkey 没有单独的授权体系。

Passkey 核心通过验证接口接收响应内容：先消费 session，再由 provider 里的 WebAuthn SDK 解析和验证。credential 响应损坏时，已经消费的挑战也无法重放。

## 外部身份接入

LinuxDo、微信、邮件等外部身份最终都映射为 `AuthIdentity`，用户的长期识别依据是外部主体，当前邮箱只是一个输入。OAuth 的 pending 流程记录本次的意图，主要有三种：登录或创建账号、绑定到当前用户、经用户确认后接纳已存在的同邮箱账户。

pending 流程的安全要求：

- provider 回调先验证供应商状态，再写入短期的 `PendingAuthSession`；回调 URL 里不出现可以长期使用的登录 token。
- 浏览器会话键、完成码摘要、有效期和 verified 状态共同约束后续的完成请求。
- 绑定身份、创建用户或接纳已有用户时，在事务里再检查一次外部主体的唯一性，并原子写入决定和消费状态。
- 已消费、已过期或浏览器不匹配的 session 无法重放；确定目标用户、确认身份归属之后，才签发 token 对。

邮箱可以作为注册或接纳决定的输入，外部登录的归属以 provider subject 的唯一关系为准。新增登录提供方时，复用 pending 会话和身份唯一性检查；handler 里按未验证的 profile 字段直接合并用户，会把两个人的账号并在一起。

## 登录提供方差异

`AuthIdentity.provider_type` 目前允许 `email`、`github`、`google`、`linuxdo`、`oidc`、`wechat` 和 `dingtalk`。`provider_key` 区分同一类型的不同发行方或兼容渠道，`provider_subject` 保存这个 key 下稳定的外部主体；三者组合全局唯一。

| 提供方 | 持久主体和验证要求 | 流程差异 |
| --- | --- | --- |
| Email | 规范化并已验证的邮箱；本地密码由用户安全状态管理 | 注册、登录、找回密码，以及当前用户的验证码绑定；邮箱身份无法通过通用的第三方解绑入口删除 |
| LinuxDo | LinuxDo subject；profile 里的邮箱和用户名只作为已验证的资料输入 | 有 start/callback、pending exchange、创建或绑定已有登录，以及当前用户 bind 流程 |
| WeChat | 优先使用稳定的 union/open identity，并兼容历史的 provider key 和 openid 渠道 | 登录和当前用户绑定走 pending 流程；支付 OAuth 是支付授权，和登录身份无关 |
| OIDC | `provider_key` 是 issuer，`provider_subject` 是该 issuer 下的 subject | 支持创建、接纳、绑定已有登录和当前用户 bind；不同 issuer 下同名的 subject 是不同的人 |
| GitHub | GitHub user ID；注册时要求拿到已验证的邮箱 | 有登录和注册完成流程；个人资料页没有 GitHub 的自助绑定和解绑 |
| Google | Google `sub`；注册时要求拿到已验证的邮箱 | OAuth 跳转和 One Tap 共用身份唯一性、pending 注册和首次绑定权益；个人资料页没有 Google 的自助绑定和解绑 |
| DingTalk | unionID 作为稳定的 subject，企业和部门资料保存为 claims 或用户属性 | 支持组织策略、创建或绑定已有登录、当前用户 bind；跨组织降级时仍以 unionID 为主体，企业内 user ID 只是资料 |

GitHub、Google 和 LinuxDo、OIDC 等共用 `AuthIdentity` 的唯一性规则，但 HTTP 流程不完全一样。新增绑定路由或前端入口前，先补齐服务端的意图、CSRF 和 state、pending session、解绑安全检查和接口测试；直接复用 OAuth callback 会缺少这些检查。

认证来源可以配置注册时的默认权益。注册时把全局默认值和来源默认值合并成一份一次性的创建计划。第三方身份第一次绑定时，也可以幂等地发放这个来源允许的余额、并发或订阅默认值，用 `(user, provider, first_bind)` 记录防止重复发放。身份重新绑定、callback 重放和 provider profile 更新都不会再次触发首次绑定权益。

## 身份绑定与解绑

当前用户绑定第三方身份时，先通过受保护的入口生成 provider 的后端 authorize URL，start 路由记录 `bind_current_user` 意图，callback 再验证当前会话和外部主体。

Email 绑定有单独的验证码和密码设置流程：

- 还没有实际邮箱的用户，随时可以完成首次绑定。
- 验证并绑定和当前记录相同的邮箱，不算换绑。
- 把已有的实际邮箱改成另一个地址时，要求运行时设置 `user_email_change_enabled` 已开启，还要验证当前密码。修改 profile 里的 email 字段和绑定是两回事。

服务端在发送验证码和提交换绑两个阶段都检查这个开关，设置缺失或读取失败时拒绝换绑。邮箱查重同时比较精确地址和收件箱 alias：Gmail 和 googlemail 忽略本地部分的点号并统一域名；所有域名都按既定策略折叠本地部分的 `+` 后缀和域名根部的点。允许换绑时，用户可以换成自己的另一个 alias；同一收件箱已被其他用户占用时，拒绝换绑。

绑定在事务里确认以下条件：目标外部三元组不属于其他用户、当前用户和会话仍然有效、pending 意图和 provider 一致；然后原子写入 identity、channel 和接纳决定。Email 换绑还要在同一事务里锁住规范化的邮箱和收件箱 alias，复查占用者，再写入用户邮箱和密码哈希，服务层预检和事务之间的并发请求因此无法穿透。管理员直接绑定同样遵守唯一约束和标准 provider key 规则，两个用户不能共享一个主体。

解绑后，用户至少要保留一个可以登录的身份。Email 不走第三方解绑；LinuxDo、OIDC、WeChat 和 DingTalk 的自助解绑，根据用户当前的身份集合判断是否允许。实际解绑成功且认证服务可用时，handler 调用 `RevokeAllUserTokens` 尝试清理 refresh token 会话；已有 access token 仍按上面的会话校验规则判断有效性。外部 provider 远端的授权是否一并撤销，取决于各自的实现；只删除本地记录时，远端 token 可能仍然有效。

## 用户属性与来源

集成侧的精确用户查找使用 `UserService.LookupUsersExact`，与管理员模糊搜索分开。它只按 ID、用户名或大小写不敏感的邮箱相等条件读取未删除账号，返回最多 100 条资料及头像 URL，不读取备注、密码、API Key 或任意身份 metadata。插件的邮箱权限与返回字段筛选由插件模块另外检查。

自定义用户属性分为 definition 和 value。definition 包含唯一 key、展示名、类型、options、required、validation、排序和 enabled；每个用户在每个 definition 下最多一个 value。类型支持 `text`、`textarea`、`number`、`email`、`url`、`date`、`select` 和 `multi_select`。值以字符串保存，multi-select 保存为 JSON 数组字符串。

写入的值要通过 definition 的类型、选项和 min、max、length、pattern 规则。`required` 只约束编辑和收集流程，历史用户可能没有这个值。definition 被禁用后，界面上不再显示这个输入项，已有的值保留，和授权无关。删除 definition 时要一并处理它的 values，前端提交的孤立 key 会被拒绝。

OAuth claims 可以提供用户名、头像、企业邮箱、显示名或部门等建议资料；DingTalk 等 provider 还可以把字段同步到管理员配置的属性 key。外部资料要记录 provider 和来源；上游缺少某个字段时，用户自己填写的资料保持不变；用保留域名合成的邮箱是占位邮箱，不算已验证的邮箱。自定义属性默认是资料字段，角色、团队、余额和网关授权都不读取它们；将来要用于策略时，需要新增明确的规则、审计和缓存失效。

## 团队租户

团队报表由团队用例先限制成员范围，再交给统一用量读取器。Owner 可以查看全队，普通成员的查询固定到本人。总览和成员趋势同时应用成员及 API Key 筛选；历史上有用量的离队成员仍出现在范围内的趋势中，无用量的当前成员显示零值。数据源和刷新规则见[使用记录预聚合](../operations/pre_aggregation.md#query_routing_and_fallback)。

团队是单层租户，没有嵌套团队。owner 既是管理者，也是团队 Key 的付款主体。普通成员可以按权限创建或使用团队资源，每笔消费同时记录团队、付款的 owner 和实际行为的成员。成员的日、周、月限额和累计用量属于 `TeamMember`；团队更换 owner 后，历史行为仍归原来的成员和 owner。

邀请 token 以摘要保存并绑定目标邮箱，接受邀请后建立成员关系。owner 需要先转移所有权或解散团队，才能离开团队。所有权转移、成员移除或离开、团队暂停和限额变化后，都要让相关团队 Key 的缓存失效，缓存里的旧成员关系随之作废。

团队 Key 每次认证都检查生命周期：团队功能仍启用、团队处于 active、付款 owner 处于 active、行为成员仍存在且 active，并且成员没有在 Key 创建后离开再重新加入。任何一项不满足都拒绝请求，团队 Key 不会退化成 owner 的个人 Key。管理员强制转移所有权时，同样原子维护唯一的 owner 和相关缓存。

<a id="api_key_rotation"></a>
## API Key 凭据轮换

用户可以在 Keys 页的更多菜单里轮换自己名下的普通 Key 或复合 Key；团队 Key 按编辑操作的规则校验团队上下文；系统托管的 Key 不向用户开放轮换。操作前先弹出确认框，成功后显示可以复制的新凭据。轮换只更新原记录的 `key` 和 `updated_at`，ID、归属、分组映射、状态、到期时间、额度、限流窗口和历史用量都保持原样。已禁用或额度用完的 Key 轮换后状态不变。

存储层以 ID、所有者和旧凭据为条件原子替换，软删除的记录和托管记录不在替换范围内。并发轮换或删除导致条件不匹配时，返回冲突，已经生效的新凭据保持不变。写入成功后，服务清除新旧两个凭据的认证缓存并广播失效；数据库的触发器在同一事务里为两者写入失效 outbox，通知失败和在途回填由重试和延迟二次失效处理。失效传播遵循认证缓存的现有协议，已经通过鉴权的请求继续执行。

客户端需要改用新凭据，统计和费用仍记在原来的 Key ID 下。HTTP 接口见 [API Key 凭据轮换接口](../interfaces/http_api.md#api_key_rotation)。

## 领域不变量

- 授权以数据库的当前状态为准；JWT、缓存和外部 profile 都是有时效的输入。
- 一个外部主体只属于一个用户；接纳已有账号需要明确的、可审计的、不可重放的决定。
- 身份由 provider type、key 和 subject 共同确定；邮箱、用户名、头像和组织资料都不是稳定的主体。
- access token、refresh token、OAuth pending session、TOTP 和 Passkey 的 challenge 各有用途和有效期，互相不能代用。
- 管理员的全局角色和团队内角色分开；API Key 的付款主体和行为主体分开记录。
- 会话撤销、token version、团队生命周期和 Key 缓存失效，在所有认证入口上的行为一致。
- 首次绑定的默认权益幂等发放；自定义属性和外部 profile 默认不参与授权。
- 新增认证方式时，同步更新公开入口限流、审计、会话撤销、前端 token 处理和相关安全测试。

相关文档：[项目总览](../project_overview.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[领域目录](index.md)。
