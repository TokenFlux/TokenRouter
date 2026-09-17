# S12 迁移账本（验收中）

冻结输入见 [原清点](initial-module-inventory.json.gz)；实际变更文件、声明、构建条件、直接 import 消费者与文档锚点见 [最终文件清单](final-migration-files.json.gz)。原计划正文不改写，当前 roadmap 仍为 12 / 17。

| 能力 | 唯一实现与生产绑定 | 事务、状态与后置责任 | 兼容退出 |
| --- | --- | --- | --- |
| 邀请码/关系/返利 | promotion/affiliate.go、promotion/postgres/affiliate.go；app/promotion.go | WithLockedInviter 复用原 Ent context，先锁推广档案再读取上限、计提；B06 返回实际金额 | 旧 service/repository 名称 S15/S16 |
| 返利转余额 | promotion/postgres.TransferQuotaToBalance | 清零、billing CreditAffiliateTransfer、累计充值与 ledger 同事务；真实 ledger 失败回滚已验证 | 身份/支付调用已直接绑定 promotion；聚合别名 S15/S16 |
| Promo | promotion/promo.go 与 postgres/promo_mutations.go | 锁码、校验、余额/累计充值、usage/次数同事务；原正数累计充值语义保留，提交后才失效 | 旧构造 S15/S16 |
| 推广/Promo HTTP | promotion/httpapi；用户路由直接调用新 handler | 原浅层 identity 用户投影、权限、分页与敏感字段，HTTP 不访问存储 | 旧 handler/DTO 名称 S15/S16 |
| 支付配置/实例选择 | payment.ConfigService、DefaultLoadBalancer、ProviderBindings；app/payment_infrastructure.go | 同一个 InstanceStore，factory/密钥/环境由 app 投影；完整候选构造后发布，整体失败保留旧表 | 旧配置签名投影 S15/S16 |
| 下单/续接/查单/统计 | payment.Checkout、OrderQueries、OrderLifecycle、PaymentResumeService | 原限额 SQL 顺序、金额及快照；公开普通查单不访问渠道，签名续接才可恢复 | 旧 PaymentService 方法委托 S15/S16 |
| 履约/状态 | payment.Fulfillment、OrderLifecycle、postgres.OrderStore | 原充值码、source_order_id、五分钟 lease、返利审计认领与计提同连接；保持多阶段履约 | 旧聚合委托 S15/S16 |
| 支付通知 | FulfillmentRuntime.Notify/Background，由 app 绑定 notification 与 lifecycle.Tasks | 业务事件/变量在 payment，通知投递与模板在 notification；原失败边界与去重键不变 | 原服务名只用于旧生命周期字段，S15/S16 |
| 退款 | payment.RefundWorkflow、postgres.RefundStore、app 的 billing 参与工厂 | 准备/预扣/必要审计、补偿、成功各为短事务；渠道在外；状态/版本/操作与渠道退款 ID 防止迟到结果覆盖 | 旧计划/结果形状投影 S15/S16 |
| 支付/管理/Webhook HTTP | payment/httpapi，app/payment_http.go | 原 body/query/Header 验签顺序、ACK 和 DTO；套餐 HTTP 复用 billing；REFUNDING 使用原查单 URL | 原 Handlers 字段仅类型别名，S15/S16 |
| 微信支付授权 | payment/httpapi/wechat.go；app/identity_http.go | Cookie/state/redirect 保持；OpenID 交换注入现有 identity provider，签名实例与下单共用 | 旧构造仅测试/过渡；pkg/oauth state 工具 S15/S16 |
| 支付后台 | payment.OrderExpiry；app/payment_expiry.go | 唯一启停屏障，运行 context 与逐阶段/逐项取消，原首轮/周期/leader key/回退 | 旧生命周期结构只包同一个 runner，S15/S16 |
| 套餐与支付订单关联 | billing.Plans 直接绑定 payment/postgres.InstanceStore | 保留在途订单计数查询，PlanOrders 旧桥接已删除 | 套餐实现仍归 billing |

旧私有转接无生产消费者的部分已从运行文件移到 unit 兼容文件，见 [清单](test-only-compatibility.json)。它们只帮助原行为断言继续调用新实现，不承载第二份状态或算法。最终 unit 全量与 lint 比较已完成；旧签名测试转接也包含在实际构建选择与静态引用清单中。

## 十二组资金写入归属

| 组 | 资金写入与事务拥有者 | 本阶段边界 |
| --- | --- | --- |
| 普通结算 | billing 闭合 Apply | 原持久化去重、金额精度、outbox 不变 |
| 订阅/兑换 | billing 权益及兑换闭合操作 | payment 只调用，来源订单去重保持 |
| 管理员调账 | billing 原子 set/add/subtract | promotion 接收原尽力返利调用；不扩大外层事务 |
| 注册赠送 | identity 外层事务 + billing 参与 | Promo 应用由 promotion 闭合写入；原失败/补偿顺序保持 |
| Key/成员/账号额度 | billing 累计与各模块配置拥有者 | 本阶段不改变配置/消费字段权限 |
| 平台额度 | billing，S04 进程内协调 | 不扩大多实例承诺 |
| Promo | promotion/postgres + billing CreditRegistrationPromo | 同事务写余额、累计充值、usage/次数 |
| 返利转余额 | promotion/postgres + billing CreditAffiliateTransfer | 同事务清零、余额/累计充值及转账 ledger |
| 支付履约 | payment 分阶段编排 + billing / promotion | 订单充值码及订阅来源幂等，返利审计/计提同事务 |
| 退款 | payment/postgres 闭合短事务 + billing 参与 | 必要恢复事实不能丢弃，渠道不持事务、不自动重发 |
| 任务资金 | billing + 旧 creative/batchimage 编排 | 业务状态机交 S13 |
| 初始化/恢复 | app/bootstrap 与旧维护用例 | 剩余初始化/维护交 S14 |

## 固定修复与限制

B01/B02 生命周期、B03 注册表、B04/B05 退款及 B06 返利均有独立回归，规划预期失败资料保留在 planning/。普通全量有清单外账号刷新测试失败，证据见 [当次观察](unplanned-account-refresh-observation.json)，已请求调整计划，尚未追加复现/修复，不能宣布验收完成。

退款补偿保留旧正数累计充值行为；本次没有重写金额算法。没有跨进程协调、渠道退款恰好一次或持久通知任务恢复的新保证。降级前必须处理或登记新准备记录对应的未完成退款，不能删除审计事实后盲目重试。
