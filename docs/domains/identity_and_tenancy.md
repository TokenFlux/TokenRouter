# 身份与租户

本文描述用户、外部认证身份、登录会话、管理员授权和团队租户之间的领域边界。它用于修改认证或归属关系时保持安全不变量，不记录逐端点请求格式、第三方 OAuth 字段或前端页面操作步骤。

## 章节导航

- [核心实体](#核心实体)：区分用户、身份、会话和团队关系。
- [认证边界](#认证边界)：核对公开入口、JWT 与管理员凭据。
- [会话生命周期](#会话生命周期)：修改签发、刷新、撤销和绑定时读取。
- [强认证与敏感操作](#强认证与敏感操作)：修改 TOTP、Passkey 或管理员 step-up 时读取。
- [外部身份接入](#外部身份接入)：修改 OAuth 登录、绑定或账号接纳时读取。
- [登录提供方差异](#登录提供方差异)：核对各 provider 的主体、邮箱和完成流程。
- [身份绑定与解绑](#身份绑定与解绑)：修改当前用户绑定、首次绑定权益或会话撤销时读取。
- [用户属性与来源](#用户属性与来源)：修改自定义字段或外部资料同步时读取。
- [团队租户](#团队租户)：修改成员、所有权或团队 Key 时读取。
- [领域不变量](#领域不变量)：实现变更前后的检查清单。

## 核心实体

用户资料、注册/绑定规则、会话、强认证和属性用例由 `internal/identity` 实现，团队用例由 `internal/team` 实现，Key 生命周期和认证缓存由 `internal/apikey` 实现。各自的 `postgres`、`rediscache`、`provider` 与 `httpapi` 按实际需要承接存储、外部验证和 HTTP。用户实体及仓储接口统一使用 identity 类型，app 向原生用例和剩余消费者交付同一 `identity/postgres.UserStore`，旧用户字段转换与 repository 用户转接已删除。公告及 billing 继续消费各自的只读投影。七类身份、通用 pending 和 OAuth 回调的生产 HTTP 由 app 一次性组合；旧 AuthHandler 及其 OAuth 转接已经删除，原跨模块 HTTP 断言直接使用 identity/httpapi 的同一组端点。微信支付 OAuth 独立由 payment/httpapi 与身份 provider 的交换端口协作，不构造登录身份图。

身份 HTTP 及调用方直接使用 `identity/httpapi/authctx`，旧 server middleware 和 identity HTTP 根包的主体转接已删除。Key 已认证投影、鉴权失败时仅供 Ops 读取的加载投影以及强制平台字段由 `apikey/httpapi` 提供；失败投影不成为准入依据。资金来源和订阅读取由 gateway HTTP 拥有，保留原字段编码与取值时机。

`Principal` 表达已验证的身份和凭据种类；`AccessSnapshot` 分别记录 Key owner、付款用户、行为成员和团队。身份核心的 `User` 不递归持有 API Key，旧 HTTP 的关联形状由 DTO 投影恢复。资金消费、调账及注册赠送的写入仍由 billing 负责，身份与团队事务通过同连接参与能力组合，提交前不发布成功失效。

Key 消费者直接使用 `apikey.APIKey`、`APIKeyRepository` 和唯一 `APIKeyService`，分组策略使用 `routing.Group`。app 交付同一 `apikey/postgres.KeyStore`，认证缓存仍使用 v40 专用快照及原深复制边界；同连接删除和分组迁移直接绑定原生存储参与能力。

| 实体 | 身份与所有权含义 | 关键约束 |
| --- | --- | --- |
| `User` | 本地账户和最终授权主体，拥有角色、状态、余额、并发与安全版本 | 软删除；登录和请求都必须重新确认启用状态；`token_version` 变化使旧 JWT 失效 |
| `AuthIdentity` | 一个外部认证提供方中的稳定身份，归属一个用户 | `(provider_type, provider_key, provider_subject)` 唯一；渠道级标识不能替代全局主体 |
| `PendingAuthSession` | OAuth 等跨页面流程的短期、一次性状态 | 绑定浏览器会话、完成码摘要和过期时间；完成时必须原子消费 |
| JWT access token | 短期访问声明，不是用户状态的权威副本 | 携带用户、角色、token version、refresh family `sid` 和可选绑定摘要；每次请求仍查当前用户 |
| Refresh token | 续签凭据及其会话族 | 服务端只保存摘要；轮换后旧 token 失效，同一 `sid` 可整体撤销 |
| `Team` / `TeamMember` | 单层团队租户及用户成员关系 | 一个团队只有一个 owner；成员角色和周期限额属于成员关系，不替代用户全局角色 |
| 团队 API Key | 团队上下文中的网关凭据 | owner 是付款用户，创建者/当前调用成员是行为主体，两者必须分别记录 |

`User.role` 的 `admin` 是全局后台角色；`TeamMember.role` 的 `owner/member` 只在一个团队内生效。两套角色不能互相推导。用户状态、团队状态、成员关系和具体资源归属共同决定请求是否允许。

<a id="authentication_boundaries"></a>
## 认证边界

`RegisterAuthRoutes` 拥有面板认证入口的路由边界。公开登录、注册、验证码、Passkey 登录、token 刷新、密码恢复和高风险校验分别使用服务端限流；这些入口依赖 Redis 的限流器时采用 fail-close，不能在缓存故障时自动放行。OAuth start/callback 负责建立外部身份流程，受保护的账户管理入口再叠加 JWT 中间件。

邮箱域名白名单为空时，普通注册和 OAuth 邮箱补全允许所有域名。白名单非空时默认严格拒绝非白名单域名；只有显式开启数据库运行时设置 `registration_email_domain_quota_enabled`，才允许其它邮箱按公共后缀规则归一为可注册主域名（eTLD+1），且每个主域名只允许一个未删除用户，子域名共享额度，白名单域名仍不限注册数量。验证码发送前会预检额度，最终创建仍须在注册事务内重新读取开关、持有主域名锁并复查，避免设置变化或并发请求穿透；当前用户邮箱绑定/换绑与已验证邮箱 OAuth 自动建号保持严格白名单语义，不使用该额度放宽。

公开认证动作通过统一验证码边界选择 Cloudflare Turnstile、腾讯天御或阿里云验证码 2.0，三个提供方不能同时启用。普通登录、注册、验证码发送和密码找回校验当前启用的提供方；腾讯天御与阿里云还保护 Passkey 登录 begin 与 OAuth 登录 start，票据只随触发动作提交且不能复用到 finish/callback。腾讯天御的 `cn` 与 `intl` 站点必须在前端 SDK 和服务端校验 endpoint 上保持一致；国际站先在当前表单容器展示 checkbox，成功票据只缓存到一次动作消费，过期、动作失败或显式重置后立即销毁并重新初始化。OAuth 当前用户绑定 start 保留既有已认证边界，不重复要求匿名登录验证码；动作验证码已启用但服务或必要凭据不完整时必须 fail-close。

Google One Tap 是现有 Google 登录的浏览器凭据入口，不创建新的身份类型。前端仅在未登录、公开设置完整、非 backend mode、安全 Origin、登录协议已满足且腾讯/阿里云动作验证码关闭时请求 GIS 展示；Cloudflare Turnstile 不扩大到该入口。服务端只接受经 Google 官方验证器校验签名、`aud`、`iss` 和 `exp` 后的 ID Token，并严格要求非空 `sub`、邮箱及 `email_verified=true`。`sub` 继续作为 Google `AuthIdentity` 的稳定主体；已有用户进入统一 token pair 签发和用户状态检查，新用户写入现有 `PendingAuthSession` 后进入相同的密码、邀请码、邮箱策略、注册开关和优惠补全流程。One Tap 关闭、Google OAuth 配置不完整、动作验证码启用、backend mode、注册关闭或身份不可登录时都必须 fail-close，原始 token 与完整 claims 不得写日志。

普通面板请求使用 JWT 中间件，验证流程至少包括：

1. 只接受配置的 HMAC 签名方法并限制 token 长度，解析签发与过期声明。
2. 从数据库读取当前用户，拒绝不存在、已删除或非启用用户。
3. 比较声明中的 `token_version` 与当前用户版本；密码或身份安全变更可以统一淘汰旧 token。
4. 启用会话绑定时校验 `sid` 对应的客户端绑定摘要，再把当前用户 ID、邮箱和角色写入 Gin 上下文。

管理员路由允许两类凭据，但能力并不完全相同：管理员 JWT 仍经过用户状态和 token version 校验；`x-api-key` 管理密钥只代表配置的首个真实管理员账户。敏感设置启用 step-up 后，管理密钥不能绕过二次验证，必须使用具有真实 `sid` 的管理员 JWT。WebSocket 场景可以从约定的子协议读取 JWT，但仍遵守相同的当前用户校验。

## 会话生命周期

登录成功签发 access/refresh token 对。refresh token 原文只交给客户端，服务端保存摘要并按用户和 `sid` 维护会话族。刷新时先验证摘要、用户状态、token version 和适用的绑定摘要，再以 Redis 原 key 的原子删除结果取得唯一消费权，成功后才在同一会话族签发新的一对 token。并发请求只有实际消费凭据的一方可以继续；消费失败返回服务不可用，不能保留旧凭据同时签发新凭据。用户读取等校验阶段的暂时错误不会提前消费 token，后继凭据生成失败则继续保持旧 token 已失效的原语义。客户端仍须把刷新视为轮换，不能并发复用旧 token。

浏览器内同一文档的刷新调用共享一个进行中的 Promise；支持 Web Locks 的浏览器还按固定锁名串行化同源标签页。取得锁后必须重新读取持久 token，并优先采用同一用户由其他标签页刚完成的轮换结果；不支持 Web Locks 时，竞争失败方在有界窗口内等待新 token 发布。刷新响应落盘前要再次核对 refresh token 和用户快照，轮换 token 最后写入作为提交标记；刷新期间发生登出或换号时，旧请求既不能恢复旧会话，也不能清除新会话。

以下事件需要撤销一个 token、一个会话族或用户的全部会话，具体范围由业务意图决定：

- 主动登出删除提交的 refresh token；安全登出或检测到绑定异常可撤销整个 `sid`。
- 用户停用、密码重置以及需要强制重新认证的身份变更会通过 token version 或会话索引淘汰既有凭据。
- 刷新时发现用户失效、版本不一致或客户端绑定不一致，会删除对应会话族而不是继续签发。

可选会话绑定使用安全解析后的客户端 IP 和规范化 User-Agent 生成摘要。access token 中缺少旧版绑定摘要时可暂时兼容到下次刷新；一旦带有摘要，发现不匹配必须审计并撤销会话族。代理头只有在可信代理配置下才参与安全客户端 IP，不能直接信任任意来路的转发头。

<a id="email_challenges"></a>
### 邮箱挑战与通知

邮箱挑战与密码重置凭据由 identity.EmailChallenges 和 identity/rediscache 唯一维护，通知模块仅接收已经准备好的验证码、重置链接或验证事件。队列仍在 worker 执行时生成验证码；普通验证码先存后发，通知邮箱验证先发后存，密码重置继续复用未过期令牌和原冷却。发送失败不会被统一改成回滚所有凭据或重新生成令牌。模板、取消与投递边界见[通知与 SMTP](../interfaces/configuration.md#notification_delivery)。

## 强认证与敏感操作

TOTP 密钥以加密形式持久化，设置和登录挑战使用有过期时间的缓存状态。管理员敏感设置要求近期 TOTP step-up grant，grant 绑定 JWT 的 `sid`；TOTP 未启用、会话 ID 缺失、grant 过期或 grant 服务不可用都应拒绝操作。该检查开启后按 fail-close 工作。

Passkey 使用 WebAuthn 的注册和登录 ceremony：持久凭据与短期 challenge/session 分离，finish 只能消费匹配的 ceremony 状态。启用腾讯天御或阿里云验证码时，匿名登录 begin 必须先消费验证码票据，finish 不再携带或重复校验该票据。注册入口必须先有已认证用户，登录 finish 最终仍签发标准 token 对并进入相同的用户状态、版本和会话约束，不能创建一条旁路授权体系。

Passkey 核心通过验证端口接收响应内容，先消费 session，再由 provider 中的 WebAuthn SDK 解析和验证；损坏的 credential 响应同样不能重放已消费的挑战。

## 外部身份接入

LinuxDo、微信和邮件等外部身份最终都映射到 `AuthIdentity`，而不是仅凭当前邮箱长期识别用户。OAuth 待处理流程显式记录意图，主要包括登录/创建、绑定当前用户，以及经用户确认接纳已存在的同邮箱账户。

待处理流程的安全边界为：

- provider 回调先验证供应商状态并写入短期 `PendingAuthSession`，不在回调 URL 中直接暴露可长期使用的登录 token。
- 浏览器会话键、完成码摘要、有效期和 verified 状态共同约束后续完成请求。
- 身份绑定、创建用户或接纳既有用户要在事务中再次检查外部主体唯一性，并原子写入决定与消费状态。
- 已消费、过期或浏览器不匹配的 session 不能重放；只有完成目标用户解析并确认身份归属后才能签发 token 对。

邮箱可作为注册或接纳决策的输入，但 provider subject 的唯一关系才是外部登录归属的持久证据。增加提供方时，应复用待处理会话和身份唯一性边界，不要在 handler 中直接按未经验证的 profile 字段合并用户。

## 登录提供方差异

`AuthIdentity.provider_type` 当前允许 `email`、`github`、`google`、`linuxdo`、`oidc`、`wechat` 和 `dingtalk`。`provider_key` 区分同类型的发行方或兼容渠道，`provider_subject` 保存该 key 下稳定的外部主体；三元组全局唯一。

| 提供方 | 持久主体与验证要求 | 当前流程差异 |
| --- | --- | --- |
| Email | 规范化且已验证的邮箱；本地密码仍由用户安全状态管理 | 注册/登录、找回和当前用户的验证码绑定；邮箱身份不能通过通用第三方解绑入口删除 |
| LinuxDo | LinuxDo subject；profile 邮箱/用户名只是已验证资料输入 | 有 start/callback、pending exchange、创建/绑定既有登录和当前用户 bind 流程 |
| WeChat | 优先稳定 union/open identity，并兼容历史 provider key/openid channel | 登录与当前用户绑定使用 pending 流程；支付 OAuth 是支付授权，不等同于登录身份 |
| OIDC | `provider_key` 为 issuer，`provider_subject` 为 issuer 下 subject | 支持创建、接纳、绑定既有登录和当前用户 bind；不能跨 issuer 合并同名 subject |
| GitHub | GitHub user ID；注册必须取得 verified email | 有登录/注册完成流程；当前个人资料绑定面不提供 GitHub 自助 bind/unbind |
| Google | Google `sub`；注册必须取得 verified email | OAuth redirect 与 One Tap 共用身份唯一性、pending 注册和首次绑定权益；当前个人资料绑定面不提供 Google 自助 bind/unbind |
| DingTalk | unionID 作为稳定 subject，企业/部门资料保存为 claims/属性 | 支持组织策略、创建/绑定既有登录和当前用户 bind；跨组织降级不能用企业内 user ID 替代 unionID |

GitHub/Google 与 LinuxDo/OIDC 等共享 `AuthIdentity` 唯一性，但 HTTP 流程并不完全相同。新增 bind 路由或前端入口前应先补齐服务端意图、CSRF/state、pending session、解绑安全和契约测试，不能只复用 OAuth callback。

认证来源可以配置注册默认权益。注册时把全局默认与来源默认解析为一次性创建计划；第三方身份第一次绑定还可幂等应用该来源允许的余额、并发或订阅默认值，使用 `(user, provider, first_bind)` 事实防止重复赠送。身份重绑、callback 重放和 provider profile 更新不得再次触发首次绑定权益。

## 身份绑定与解绑

当前用户绑定第三方身份时，先通过受保护入口生成 provider 的后端 authorize URL，start 路由记录 `bind_current_user` 意图，callback 再验证当前会话和外部主体。Email 绑定使用独立验证码与密码设置流程；尚无真实邮箱的用户始终可以完成首次绑定，验证并绑定与当前记录相同的邮箱也不算换绑，只有把已有真实邮箱改成另一地址时才要求运行时设置 `user_email_change_enabled` 已开启，并且还要验证现有密码，不能把修改 profile email 当作绑定。服务端在发送验证码和提交换绑两个阶段都执行开关门禁，设置缺失或读取失败时拒绝换绑。邮箱查重同时按精确地址和收件箱 alias 归一身份处理：Gmail/googlemail 忽略本地点号并统一域名，所有域名的本地 `+` 后缀与域名根点按既定策略折叠；允许换绑时当前用户可以更换自己的 alias，其他用户占用同一收件箱时必须拒绝。

绑定必须在事务中确认：目标外部三元组尚未属于其他用户、当前用户和会话仍有效、pending intent 与 provider 一致，并原子写入 identity/channel/接纳决定。Email 换绑还要在同一事务内锁定规范化邮箱与收件箱 alias 身份，复查占用者后再写入用户邮箱和密码哈希，避免并发请求穿透服务层预检。管理员直接绑定仍要遵守相同唯一约束和 canonical provider key 规则，不得制造两个用户共享主体。

解绑前至少保留一个可登录身份。Email 不走第三方解绑；LinuxDo、OIDC、WeChat 和 DingTalk 的自助解绑根据当前身份集合决定是否允许。成功解绑后撤销用户全部 token，会话必须重新登录，避免旧 JWT 继续代表已改变的安全身份。外部 provider 的远端授权是否同时撤销由相应实现决定，不能只凭本地删除宣称远端 token 已失效。

## 用户属性与来源

自定义用户属性由 definition 和 value 分离：definition 拥有唯一 key、展示名、类型、options、required、validation、排序和 enabled；每个用户与 definition 最多一个 value。支持 `text`、`textarea`、`number`、`email`、`url`、`date`、`select` 和 `multi_select`，值持久化为字符串，multi-select 使用 JSON 数组字符串。

写值必须通过 definition 的类型、选项和 min/max/length/pattern 规则。`required` 约束编辑/收集流程，不意味着历史用户天然已有值；definition disabled 后不再作为活跃输入，但已有值不会因此成为授权依据。删除 definition 时要协调其 values，不能留下前端仍可提交的孤立 key。

OAuth claims 可提供用户名、头像、企业邮箱、显示名或部门等建议资料；DingTalk 等 provider 还可把字段同步到管理员配置的属性 key。外部资料需要记录 provider/source，上游缺失字段不能静默清空用户显式资料，保留域合成邮箱也不能冒充真实 verified email。自定义属性默认只是资料，不参与角色、团队、余额或网关授权；若未来用于策略，必须新增显式规则、审计和缓存失效。

## 团队租户

团队是单层租户，不存在嵌套团队。owner 既是管理者，也是团队 Key 的付款主体；普通成员可以按权限创建或使用团队资源，但消费必须同时保存团队、付款 owner 和实际成员归属。成员的日/周/月限额及累计用量属于 `TeamMember`，团队切换 owner 不能把历史行为错误归到新 owner 名下。

邀请 token 以摘要保存并绑定目标邮箱；接受邀请后形成成员关系。owner 不能像普通成员一样直接离队，必须先完成显式所有权转移或解散团队。所有权转移、成员移除/离开、团队暂停和限额变化后都要失效相关团队 Key 缓存，避免缓存继续认可旧关系。

团队 Key 每次认证都执行生命周期检查：团队功能仍启用、团队 active、付款 owner active、行为成员仍存在且 active，并且成员关系没有在 Key 创建后离开再加入。任一条件不满足都拒绝，而不是退化成 owner 的个人 Key。管理员强制转移同样必须原子维护唯一 owner 和相关缓存。

## 领域不变量

- 授权使用数据库当前状态；JWT、缓存和外部 profile 都只是带时效的输入。
- 外部主体只能归属一个用户，账号接纳必须有显式、可审计且不可重放的决定。
- Provider type、key 和 subject 共同构成身份；邮箱、用户名、头像和组织资料不能替代稳定 subject。
- access token、refresh token、OAuth pending session、TOTP/Passkey challenge 各有独立用途和有效期，不能互相替代。
- 管理员全局角色和团队内角色分离；API Key 的付款主体与行为主体分开记录。
- 会话撤销、token version、团队生命周期与 Key 缓存失效必须在所有认证入口保持一致。
- 首次绑定默认权益必须幂等；自定义属性和外部 profile 默认不参与授权。
- 新增认证方式时，应同时更新公开入口限流、审计、会话撤销、前端 token 处理和相关安全测试。

相关文档：[项目总览](../project_overview.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[领域目录](index.md)。
