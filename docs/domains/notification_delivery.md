# 通知与邮件投递

本文说明通知事件、模板、退订、投递去重和邮件队列的规则。验证码与密码重置凭据由 identity 管理，余额、订阅和提供商阈值由所属业务模块判断；通知模块接收已经确定的事件与收件人。

## 章节导航

- [事件与模板](#notification_events)：修改业务事件或邮件内容时读取。
- [退订与语言](#notification_preferences)：修改偏好与退订令牌时读取。
- [投递协调](#notification_coordination)：修改去重和失败处理时读取。
- [身份邮件队列](#notification_queue)：修改入队、生成凭据与停机时读取。
- [SMTP 与取消](#notification_smtp)：修改连接、发送确认或超时时读取。
- [验证入口](#验证入口)：核实投递和并发行为。

<a id="notification_events"></a>
## 事件与模板

notification 支持这些事件：认证验证码、密码重置、通知邮箱验证、团队邀请、团队所有权转让、订阅购买、订阅到期提醒、余额不足、充值成功、提供商额度告警、上游余额提醒、审核违规、审核封禁、Ops 告警、计划报告和插件消息。调用方提供事件、收件人、来源标识和模板变量。业务模块完成订单或提供商判断后触发通知。

模板按事件和语言保存，包含内置模板、管理员覆盖、预览和占位符校验。HTML 变量默认转义，原始 HTML 只允许明确授权的占位符。用户自定义模板缺失或校验失败时，使用选定语言的内置模板。SMTP 发送失败或结果不明时，自动投递停止重发。Ops 提供报告的实际摘要和详情，预览用的样例数据不会出现在实际报告里。

充值成功和余额不足通知使用账户余额的展示单位。预览和发送都读取 `balance_unit_symbol`，`balance_unit_name` 按邮件语言解析；缺少或空白设置分别使用 `$` 和 `USD`。这两个变量可用于自定义模板，金额变量仍保留原来的数值文本。旧模板中紧贴 `current_balance`、`recharge_amount` 或 `threshold` 占位符的 `$` 前缀在渲染时改用配置符号，数据库中的模板正文保持原样。提供商额度和上游钱包通知使用其自身的计量单位。

上游余额提醒使用 `provider.upstream_balance_low`，包含提供商名称、ID、余额、阈值、单位和观测时间，模板支持英文与简体中文。provider 模块用 PostgreSQL 轮次认领控制重复执行，调用通知服务时省略来源标记，每轮可以发送一次，SMTP 失败本轮不重试。规则见[钱包余额邮件提醒](../interfaces/upstream_usage.md#钱包余额邮件提醒)。

提供商额度告警使用 `provider.quota_alert`；用户审核封禁仍使用 `content_moderation.account_disabled`，管理端按相同事件名展示本地化文案。

插件消息使用 `plugin_message`，标题为“插件名: 主题”，正文先转义 HTML，再将换行转换为 `<br>`，底部标注插件来源并提供退订链接。插件名按收件人语言解析，版本取发起调用的进程绑定；旧进程排空期间可读取上一版本名称。收件人由宿主按用户 ID 查找，第三方登录占位邮箱视为没有邮箱。退订偏好覆盖该收件人的所有插件消息。

通知配置和模板使用 settings 存储，SMTP 由 `notification/smtp` 实现。模板管理、SMTP 测试及公开退订位于 `notification/httpapi`。

<a id="notification_preferences"></a>
## 退订与语言

仅标记为可选的事件允许退订；当前为订阅到期提醒、余额不足通知和插件消息。验证、交易和其他必需的事件，退订入口关闭不了。偏好按事件与收件人保存，读取兼容已有键格式。

退订令牌包含收件人、事件和有效期，使用持久密钥签名，有效期为 365 天。第一次生成密钥时，服务通过进程内互斥让并发请求排队，刚发出的令牌不会因为并发生成而失效。

实际发送的退订链接使用站点前端地址：数据库 `frontend_url` 优先，空值或读取失败时使用部署 `site.frontend_url`。两者都为空时保留 `/api/v1/settings/email-unsubscribe?token=...` 相对链接，客户端接入用的 `api_base_url` 不参与选择。独立 Mailer 未装配站点读取器时只读取数据库前端地址；模板预览继续使用样例退订链接。地址选择和其他回调规则见[站点链接与客户端 API 地址](../interfaces/configuration.md#site_link_addresses)。

语言目录使用 `en` 和 `zh-Hans`。用户邮件依次读取事件语言、账户偏好、历史用户记录、邮箱记录和站点默认语言。站点名称、发件人名称及余额充值链接按选定语言解析。团队所有权转让也经过同一模板发送入口。语言和账户偏好见[用户侧国际化](../interfaces/user_localization.md#language_selection)。

<a id="notification_coordination"></a>
## 投递协调

同一投递键的查重、发送和成功标记在唯一 NotificationEmailService 实例内串行执行；不同键可以并行，等待锁的请求可以取消。投递身份由事件、来源类型、来源 ID、收件人和提醒键组成，重试时沿用同一组值，去重因此对重试同样有效。来源类型或来源 ID 缺失时不生成投递键，发送不经过这层查重；验证码等身份邮件还受 identity 自己的凭据和冷却规则控制。

`SendWithResult` 同时返回投递错误和结果：`Delivered` 表示已获 SMTP 确认或已存在成功标记，`Attempted` 表示调用了发送器。没有收件人或已退订时两者均为 false；SMTP 已确认而标记保存失败时，`Delivered` 仍为 true。原 `Send` 保留只返回错误的接口。插件另有持久化认领和结果记录，规则见[插件写能力](../architecture/plugin_system.md#write_capabilities)。

成功标记与 SMTP 接收不属于同一事务。SMTP 已确认但标记保存失败时，投递结果存在不确定性；进程内的锁和持久标记无法消除这段不确定窗口，重试仍可能重复投递。通知失败时，已经完成的资金提交、订单履约和风险处置保持不变。

<a id="notification_queue"></a>
## 身份邮件队列

EmailQueueService 使用内存队列，容量 100；构造时未指定有效 worker 数则使用 3 个 worker。构造不启动工作，Start 后处理验证码和密码重置任务，单任务预算 30 秒。队列满或已停止时返回错误，goroutine 数量固定为 worker 数；进程重启后，队列里的任务会丢失。

入队保存发送意图，worker 执行时才调用身份能力生成或复用凭据。普通验证码先存后发，通知邮箱验证先发后存；密码重置保留未过期令牌及冷却规则。发送失败时各流程保留已产生的结果，没有统一的凭据回滚，也不会统一生成新令牌，详见[身份与租户](identity_and_tenancy.md)。

关闭时先停止接收，再等待已有任务。应用先停止通知生产者，再排空邮件队列；预算耗尽会取消在途 SMTP，并报告排队及正在处理的未完成项。多次停止返回同一个结果，停止后无法重新启动。完整顺序见[系统架构](../architecture/system_architecture.md#startup_and_shutdown)。

<a id="notification_smtp"></a>
## SMTP 与取消

连接测试和实际发送共用建连路径与超时。`smtp_use_tls=true` 首先使用隐式 TLS，服务端返回明文 SMTP 问候时，改用强制 STARTTLS；服务端无法升级时，连接失败。`smtp_use_tls=false` 使用机会式 STARTTLS，服务端不支持这个扩展时，按明文发送。

拨号、TLS 和在途 I/O 都接收 context，采用请求截止时间与连接/I/O 上限中较早者。DATA 得到成功响应后，结果记为成功；之后的取消或不标准的 QUIT 响应都不会触发重发。发送确认的时点和配置说明见[通知与 SMTP](../interfaces/configuration.md#notification_delivery)。

## 验证入口

`backend/internal/notification/service.go` 定义事件、模板、偏好和投递流程，`coordinator.go` 与 `queue.go` 定义并发和生命周期。`service_test.go` 覆盖同键投递和首次退订密钥，`coordinator_test.go` 覆盖锁取消，`queue_test.go` 覆盖队列排空。`mailer_test.go` 检查发送取消，SMTP 子包测试覆盖 DATA 确认和 TLS 连接。identity 的邮件挑战测试核实凭据生成与存储顺序。
