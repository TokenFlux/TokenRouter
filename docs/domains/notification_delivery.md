# 通知与邮件投递

本文记录通知事件、模板渲染、退订偏好、投递去重、身份邮件队列和 SMTP 确认规则。验证码与密码重置凭据归 `identity`，余额、订阅和提供商阈值由各自业务模块判定，通知模块接收已确定的事件与收件人。

## 章节导航

- [事件与模板](#notification_events)：支持的业务事件、模板覆盖与展示单位规则。
- [退订与语言](#notification_preferences)：可选退订范围、令牌签名、退订链接与语言优先级。
- [投递协调](#notification_coordination)：同键串行查重、结果语义与失败边界。
- [身份邮件队列](#notification_queue)：内存队列容量、凭据生成顺序与关机排空。
- [SMTP 与取消](#notification_smtp)：TLS/STARTTLS 协商与 DATA 确认不重发规则。
- [验证入口](#验证入口)：核心源码与单测位置。

<a id="notification_events"></a>
## 事件与模板

支持事件包括：认证验证码、密码重置、通知邮箱验证、团队邀请、团队所有权转让、订阅购买、订阅到期提醒、余额不足、充值成功、提供商额度告警（`provider.quota_alert`）、上游余额提醒（`provider.upstream_balance_low`）、审核违规、审核封禁（`content_moderation.account_disabled`）、Ops 告警、计划报告和插件消息（`plugin_message`）。

模板与渲染约束：

- 模板按事件和语言持久化在 settings 中，支持管理员覆盖、预览与占位符校验；自定义模板缺失或校验失败回退对应语言内置模板。HTML 变量默认转义，仅白名单占位符允许原始 HTML。
- 余额展示单位：充值成功与余额不足读取 `balance_unit_symbol` 和按邮件语言解析的 `balance_unit_name`（缺省为 `$` 与 `USD`），旧模板中紧贴 `current_balance`/`recharge_amount`/`threshold` 的 `$` 自动替换为配置符号（不改数据库原文）。提供商额度与上游钱包沿用各自计量单位。
- 上游余额提醒：`provider` 用 PostgreSQL 轮次认领防重，调用通知时省略来源标记，每轮最多发 1 次且 SMTP 失败本轮不重试，见[钱包余额邮件提醒](../interfaces/upstream_usage.md#钱包余额邮件提醒)。
- 插件消息：标题为「插件名: 主题」（按收件人语言解析，排空期可读上一版本名），正文转义 HTML 并将换行转 `<br>`，底部注明来源与退订链接；第三方占位邮箱视为无邮箱；退订覆盖该用户全部插件消息。

<a id="notification_preferences"></a>
## 退订与语言

- 可退订范围：仅订阅到期提醒、余额不足通知与插件消息允许退订；验证、交易与其他必需事件不可关闭。
- 退订令牌：含收件人、事件和 365 天有效期，使用持久密钥签名；首次生成密钥时进程内加锁串行，避免并发生成使刚签发令牌失效。
- 退订链接地址：优先数据库 `frontend_url`，空或失败回退部署 `site.frontend_url`，均空时保留相对路径 `/api/v1/settings/email-unsubscribe?token=...`（不使用 `api_base_url`）。详见[站点链接与客户端 API 地址](../interfaces/configuration.md#site_link_addresses)。
- 语言解析：支持 `en` 与 `zh-Hans`，依次取事件语言 > 账户偏好 > 历史用户偏好 > 邮箱偏好 > 站点默认，站点名、发件人名与充值链接随该语言解析，见[用户侧国际化](../interfaces/user_localization.md#language_selection)。

<a id="notification_coordination"></a>
## 投递协调

- 同键串行去重：投递键由「事件 + 来源类型 + 来源 ID + 收件人 + 提醒键」组成，查重、发送和写成功标记在单实例内串行（不同键并行，等待锁支持取消）。缺失来源类型或来源 ID 时跳过这层去重。
- 返回语义：`SendWithResult` 返回 `Delivered`（已获 SMTP 确认或已有成功标记）和 `Attempted`（调用过发送器）。无收件人或已退订时两者均为 false；SMTP 已确认但写标记失败时 `Delivered` 仍为 true。插件另有持久台账，见[插件写能力](../architecture/plugin_system.md#write_capabilities)。
- 一致性边界：SMTP 接收与成功标记非同一事务，SMTP 成功但落库失败存在重复投递窗口；通知失败绝不回滚已提交的资金、订单或风控处置。

<a id="notification_queue"></a>
## 身份邮件队列

- 队列规格：内存有界队列容量 100，默认 3 个 worker，单任务预算 30 秒；队列满或已停止返回错误；进程重启会丢失未发任务。
- 凭据生成时机：入队仅存意图，worker 执行时调用 `identity` 生成或复用凭据。普通验证码先存后发，通知邮箱验证先发后存，密码重置复用未过期令牌并受冷却约束；发送失败不统一回滚凭据，见[身份与租户](identity_and_tenancy.md)。
- 停机排空：先停上游生产者再停接新任务并排空队列，预算耗尽取消在途 SMTP 并报告未完成项；Stop 幂等且停止后不可重启，见[系统架构](../architecture/system_architecture.md#startup_and_shutdown)。

<a id="notification_smtp"></a>
## SMTP 与取消

- TLS 协商：`smtp_use_tls=true` 先试隐式 TLS，若服务端返回明文问候则强制 STARTTLS（不支持则失败）；`smtp_use_tls=false` 采用机会式 STARTTLS（不支持则明文发送）。连接测试与实际发送共用该路径。
- 确认边界：拨号、TLS 与 I/O 均受 context 与超时较早者约束；DATA 收到成功响应即视为送达，后续取消或非标 QUIT 响应不触发重发，见[通知与 SMTP](../interfaces/configuration.md#notification_delivery)。

## 验证入口

核心实现位于 `backend/internal/notification/service.go`、`coordinator.go`、`queue.go` 与 `smtp/`；单测分别覆盖同键并发与退订密钥（`service_test.go`）、锁取消（`coordinator_test.go`）、队列排空（`queue_test.go`）、发送取消（`mailer_test.go`）及 SMTP DATA/TLS 行为。
