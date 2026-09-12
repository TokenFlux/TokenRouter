# S05 生命周期与后续职责

| 资源/状态 | 唯一生产构造/绑定 | 启停与依赖 | 验证 |
| --- | --- | --- | --- |
| 身份 AuthService/UserStore/UserService | app/identity_auth.go、identity_users.go | 构造无后台启动；Profile 活动时间写入进入 app Tasks | 全量 unit、事务 integration、进程启动 |
| JWT/refresh/TOTP/Passkey 会话 | app/identity_security.go、app Wire；identity/rediscache | 共享原 Redis；无独立 worker；会话键、TTL 和序列化不变 | 刷新轮换、一次性消费、真实 SDK/Redis/PG |
| Key L1/L2、negative/singleflight/lookup slots/last-used | app/apikey.go；apikey/lifecycle.go | StartOrder 195；StopOrder 805。停止新认领后等待在途，再关订阅和 L1；超时不提前关闭共享资源 | lifecycle_test.go、定向 race、TestS05AuthPubSubReconnect |
| 认证 outbox worker | app Wire → apikey.ProvideAuthCacheInvalidationWorker | StartOrder 980；StopOrder 20。停止新领取、取消并等待当前批次；持久重试和延迟二次失效保留下次启动 | outbox unit、真实触发器/回滚；进程 SIGTERM |
| 无效认证滥用限制 | apikey 唯一 limiter | 沿用容量、窗口及阈值，不新增进程外协调 | 原 limiter unit/race |
| DingTalk app token/cache slot | app/identity_http.go → provider.DingTalkClients | 同一槽保存当前配置的客户端；同步通过 app Tasks，原 30 秒预算、取消解耦与 panic 观察 | provider 测试、DingTalk unit、app 停止屏障 |
| 团队邀请限流 | app/team.go、team/rediscache | 原 Redis 键和 TTL；无构造启动副作用 | Team unit/integration |
| JWT 维护命令 | bootstrap.NewJWTIdentity | 只加载用户读取和签发，不构造完整认证/后台 worker | jwtgen 构建与进程级参数/签名测试 |

## 剩余职责与退出阶段

| 项目 | 当前消费者/位置 | 退出 |
| --- | --- | --- |
| 分组/账号快照来源、Fast 默认策略、默认分组选取 | app/legacybridge/apikey.go 调用旧分组能力；旧网关按请求改写 | S06；不建第二份路由缓存 |
| RPM、并发与调度槽 | 旧 gateway/middleware、ConcurrencyService | S06/S07/S11；保持资金检查后累计顺序 |
| 用户/Key 列表用量排序、团队用量、最后活动读投影 | identity/postgres、apikey/postgres 的只读查询及 app/legacybridge.KeyUsageTotals | S08；保持数据库分页排序 |
| 通知投递与模板 | 旧 EmailService/EmailQueueService/团队通知/通知邮箱投递 | S10；新 identity 保留通知邮箱管理规则 |
| OAuth 上游账号与请求指纹 IdentityService | 原 service 中供应商 OAuth、IdentityService、上游 session/队列 | S06/S09/S11；不属于面板用户身份 |
| 请求模型提取/改写/响应恢复 | 旧网关；routing/modelmap 仅拥有纯匹配 | S09/S11；保持复合选组 → Key 改写 → 渠道/账号映射 |
| 推广/返利/支付与微信支付 OAuth | app/legacybridge 身份投影、独立 WeChatPaymentHandler、原闭合支付/推广事务 | S12；身份注册只调用窄接口 |
| creative/batchimage 的 Key 消费者 | 旧任务入口通过 Key 兼容形状 | S13；资金能力已在 billing |
| setup/初始化/恢复 | bootstrap 与旧维护入口；jwtgen 已精简 | S14 |
| 旧 User/APIKey/DTO、AuthHandler、AdminService 委托及私有测试转接 | file-ownership 中 retained_declarations、private-entry-dispositions | S15/S16；按消费者清零删除 |

API Key 的 Principal 表达行为身份；付款 owner 不继承为管理员角色。旧 context 只投影 canonical authenticationRecord。Key 快照 v40、发布订阅和 outbox 兼容性继续存在；S04 的平台额度互斥仍只覆盖一个服务进程。
