# S10 迁移账本

| 原入口/职责 | 唯一实现 | 直接调用者与保留项 | 标签/验证 |
| --- | --- | --- | --- |
| service/notification_email_service.go：事件、模板、退订、去重 | notification/service.go；contract 纯叶子 | app 直接构造；旧类型别名，私有测试渲染入口委托，S15/S16 清理 | 原普通测试迁 notification，B01/B02 新回归 |
| service/email_service.go：SMTP 与 MIME | notification/Mailer + notification/smtp | app 唯一技术发送器；旧构造供兼容直接调用，B03 不新增重发 | SMTP 原 unit 测试移入技术包；实际取消回归 |
| service/email_service.go：验证码、重置凭据 | identity/EmailChallenges | identity 端口消费 notification 消息发送；保留 worker 内生成时点 | 身份/邮箱消费者 unit |
| repository/email_cache.go | identity/rediscache/email.go | 原构造委托；键、TTL、JSON 不变 | 原 key 单测迁移；真实 Redis 集成保留旧入口验证 |
| service/email_queue_service.go | notification/EmailQueueService | app 注入同一 identity 挑战实例；旧类型别名；StopContext 接应用预算 | 构造、重复启动、队满、停止和取消 |
| service/balance_notify_service.go | billing/balance_notifications.go + notification/alerts.go | 旧网关只投影 User/Account；billing 决策、notification 模板与投递 | 保留全部原余额/额度断言 |
| handler/admin/setting_handler_email.go、公开退订 | notification/httpapi | 路由直接 h.Notification；旧设置入口仅委托 | 原字段与 TLS 省略语义 unit |
| Ops、team、billing 通知 bridge | app 直接投影 | team 负责接收者读取；notification 不读取业务实体；S12 支付入口继续委托 | 消费者普通/unit 与进程验证通过 |

S10.2—S10.4 的职责继续列于下表；对应通知 bridge 已删除，其他 gateway 完成处理仍归 S11。

| 原入口/职责 | 唯一实现 | 直接调用者与保留项 | 标签/验证 |
| --- | --- | --- | --- |
| handler/page_handler.go | site/Pages + site/filesystem + site/httpapi | app 构造；server 只登记路由，旧构造待清理；图片无 JWT 的原范围保持 | 原图片路径测试迁 filesystem；B04 根内/越界/大小测试 |
| service/setting_public.go、公开 DTO | site/PublicService、site/httpapi/dto | app/legacybridge.SitePublicSource 仅转发公开来源，S15 退出；原 API 与 embed 保留各自字段 | 普通/unit 公开设置与 HTML/CSP 回归 |
| service/content_moderation.go、input/keyword/redact | moderation 核心 | 旧配置/输入/结果类型别名，旧网关透传输入；临时 Legacy 函数待消费者清零 | 原行为断言迁入核心，技术测试按符号移入 provider |
| service/content_moderation_media.go | moderation/provider/media.go + contract.Media | 删除旧实现，唯一 HTTP/DNS/快照执行；核心以端口调用 | 私网、重定向、类型/大小、取消、字节预算 |
| service/content_moderation_email.go | notification/RiskDelivery | moderation 只发送无正文/媒体/密钥的事件投影 | 旧正文与通知失败语义 |
| repository/content_moderation_repo.go | moderation/postgres.Store | Cyber 用户锁/状态写入通过 identity/postgres 同连接参与工厂；旧构造委托 | SQL 原断言及真实 PostgreSQL 提交/回滚 |
| repository/content_moderation_hash_cache.go | moderation/rediscache | 同一 Redis 集合及故障返回，没有新广播协议 | 缓存与审核行为回归 |
| admin/content_moderation_handler.go | moderation/httpapi | app 构造，旧 handler 类型别名；路由保留原权限和次序 | 管理 HTTP/查询/媒体 |
| pkg/websearch Manager 的选择与计数意图 | search/Manager、WorkGroup | 服务只消费明确技术输入；旧 pkg 构造/类型委托 | 原选择/月度窗口测试迁核心，B07 单次释放 |
| pkg/websearch Brave/Tavily 与 HTTP cache | search/provider.Executor | 保留超时、代理、缓存上限；替换代次只关闭空闲连接 | 原供应商/代理 HTTP 测试迁 provider |
| pkg/websearch Redis 脚本与 key | search/rediscache | 原 namespace、Lua、TTL 和未知结果放行 | 真实 Redis 取消回滚与 TTL 修复 |
| service/websearch_config.go | search/ConfigService | app 注入 proxy 投影、Manager 工厂及唯一注册表；旧 getter 只委托 | B05/B06 发布交错、独立副本、写失败 |
| admin/setting_handler_runtime.go 搜索四入口 | search/httpapi | 路由直接绑定新 handler；保留管理历史字段形状 | 配置/测试/重置与旧网关消费者 |

新 HTTP 适配使用 server/httpx。生产 Wire 只构造每个所属模块的一份状态；旧直接构造保留给尚未清理的兼容消费者和既有测试，不在 app 再构造第二份队列、缓存或协调器。

## 实际文件、符号与消费者

- [文件声明及摘要](final-file-declarations.json.gz)：181 个 Go 差异路径，含 106 个新增、22 个删除；2,441 条声明（含 import）。生成/手写、测试标签、OS 后缀和文档锚点逐文件记录。
- [逐符号连接](final-symbol-links.json.gz)：按普通/unit/integration 列出直接静态消费者和被引用符号。混合旧文件中的未迁声明保留原角色，不根据文件名声称整文件业务已迁移。
- [类型消费者与结构实现](final-consumer-refs-normal.json.gz)、[unit](final-consumer-refs-unit.json.gz)、[integration](final-consumer-refs-integration.json.gz)：结构符合只是候选，真实生产注入由 [Wire 模块引用](final-wire-module-bindings.json)和手写 app 构造确认。
- [构建选择](buildsets.json)包含完整 go list 参数和实际入选测试文件；[测试事件](final-test-results.json)与 [固定修复](fixed-issues-validation.json)分别确认真实执行。
- [清除无消费者包装](removed-unused-compat.json)及 [最后私有委托整理](final-compat-pruning.json)：只供 unit 测试的符号进入标签测试文件；原运行算法均已在唯一目标实现。
- [依赖许可](dependency-exceptions.json)为精确文件/import 账本；新增普通文件适用角色基础规则，未取得目录级历史豁免。

混合入口具体保留：旧 SettingService 只为 site 读取公共来源及未迁业务设置（S15）；UserService 只将通知邮箱事件投影给 notification（身份挑战在 identity）；余额包装只投影旧 User/Account，阈值规则在 billing；旧网关保留请求捕获、工具选择/合成协议、重试与完成处理（S11）；任务审核调用与支付通知触发分别归 S13/S12。
