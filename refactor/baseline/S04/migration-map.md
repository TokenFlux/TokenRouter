# S04 实现、兼容入口与剩余消费者

[逐文件与声明清单](file-ownership.json.gz)记录实际构建条件、符号、import 消费者及 Wire 引用；[十二组资金账本](funding-writes.md)区分资金操作与业务编排。[依赖规则](dependency-rules.json)与[精确许可](dependency-exceptions.json)记录现行门禁。包导入消费者不是完整动态调用图，资金组中的实际链路另经代码及事务测试核对。

| 已迁能力与旧入口 | 唯一实现与生产接入 | 保留职责及退出 |
| --- | --- | --- |
| service/domain 的分配、结算、订阅/套餐/兑换值类型 | billing 的值、错误、命令与结果；domain 保留 Ent 所需别名 | Ent 引用与旧 DTO 别名 S15/S16；不生成 Ent |
| repository.usageBillingRepository.Apply | 兼容 Adapter → Funds.Settle → billing/postgres.SettlementStore；AllocateSubscriptions 拥有纯分配和窗口计算 | 旧网关的请求 ID、供应商用量、日志、重试/取消及完成 worker 留 S09/S11；日志失败不再次扣费 |
| 旧 BatchImageBalanceHoldCommand 与 CreativeEntity | 旧形状投影为 TaskFundsCommand/TaskReference → Reserve/Capture/Release | 两张任务表的冻结/allowance 投影仅留 billing/postgres，S13 删除；不改历史指纹或 v1/v2/v3 快照 |
| BillingCacheService 资金准入与状态 | billing.Eligibility.Check、缓存工作池、singleflight 与窗口处理；app 构造唯一生产实例 | RPM 裁决留旧入口，S05/S06/S11；不复制 Redis key 或队列 |
| BillingService、ModelPricingResolver、账号成本与倍率 | Calculator、PriceResolver、AccountStats 及 GroupRateResolver；金额/展示复用 pricing | 老实体和平台身份由边界投影；渠道、账号、动态候选桥接 S06/S09；两个网关的倍率缓存保持隔离 |
| SubscriptionService / user_subscription_repo | billing.SubscriptionService + postgres.SubscriptionStore/SubscriptionMutations；旧服务类型为别名 | 用户/分组展示分别 S05/S06；旧空 InvalidateSubCache 不作为广播机制 |
| 订阅过期/提醒、维护队列 | billing.SubscriptionExpiryService，app 控制启停与 context 等待；7/3/1 天资格由 billing 判断 | 通知模板/收件人/发送 S10；无生产消费者的维护队列只迁实现，不额外启动 |
| PaymentConfigService 套餐 CRUD | billing.Plans + postgres.PlanStore；旧支付入口只投影值并委托 | 支付提供商、订单快照、删除前未完成订单查询留 S12；旧方法兼容 S15/S16 |
| RedeemService、兑换仓储与缓存 | billing.RedeemService + postgres.RedeemStore/RedeemMutations + rediscache.RedeemCache | 兑换并发数参与写入 S05；尽力返利 S12；邀请码注册入口留身份用例 |
| AdminService 兑换管理与调整记录 | billing.RedeemAdmin；新兑换 HTTP 注入消费者侧窄接口 | AdminService 保留用户管理及原调账后置顺序；必要旧方法仅委托，S05/S12/S15 清理 |
| user_repo 原子余额操作 | billing/postgres.BalanceStore；已有 Ent Tx 由 BalanceInTx 明确复用 | 身份、注册、Promo、返利和退款原外层事务分别 S05/S12；没有拆出独立提交 |
| 平台额度管理/缓存/flusher | PlatformQuotas、Eligibility、SettlementEffects 与 flusher 共用 app 的一个 QuotaCoordinator | 单服务进程协调；跨实例不在本次支持范围；初始化默认值留 S05/S14 |
| BalanceNotifyService 阈值 | billing 的余额/额度阈值规则与已提交状态；原发送入口只投影并投递 | 邮件传输、收件人处理、模板、幂等发送策略 S10 |
| 用户/管理员订阅、兑换、额度、套餐 HTTP 与 DTO | billing/httpapi，路由直接绑定；管理员套餐保留原 Ent JSON，公开套餐独立展示 | 老 handler/DTO 兼容名 S15/S16；用户身份和 usage 查询领域未迁 |
| 面板命令幂等 HTTP helper | idempotency/httpapi；旧 handler helper 委托 | coordinator/指标仍为 S02 唯一状态，旧名 S15/S16 |
| 公告有效订阅桥接 | app 直接把 billing 存取结果投影为 site.SubscriptionSnapshot | S02 的旧订阅桥接已删除；公告用户读取桥接仍待 S05 |

## 明确的外层事务参与

`BalanceInTx`、`SubscriptionsInTx`、`RedeemInTx` 接收调用方现有 Ent Tx，初始读取和实际写入使用同一连接，不 Begin/Commit/Rollback，也不发布缓存或通知。旧 repo 余额入口检测原 Ent context 后使用该参与对象，不新增 context key。普通 Funds.Settle 始终持有闭合 SQL 事务，不因 Ent context 自动改为参与模式。

订阅/兑换的新闭合存储操作保留原提交范围。普通结算用户锁、稳定订阅锁序、余额、Key/成员/账号累计及 outbox 一次提交；死锁重新开启整段事务。Promo、返利转余额、支付退款、setup/恢复等尚未迁移的闭合事务仍由原用例提交。管理员充值的尽力返利和记录失败不会回滚充值。

## 单实例修复与故障边界

平台额度协调器是进程内按用户互斥，管理替换/重置、受保护回源/回填、跨窗口刷新、累计及镜像共用它。flusher 在 Pop 后、读取 Redis 前按用户 ID 升序取锁，整批 SQL 结束后逆序释放。异步增量镜像也在完成前保持该锁，关闭拒绝入队时释放锁并告警。取消不允许绕过锁写旧快照；锁引用包括等待者，防止回收时出现两把锁。

Redis 与数据库仍不是原子提交。缓存失效、填充或持久化失败仍按原故障语义记录告警，可能发生 TTL 范围内滞后或待对账差异；本阶段没有新增版本列、分布式锁或缓存协议。

## 原 HEAD 复现与独立行为修正

| 旧缺陷 | 修复边界 | 行为证据 |
| --- | --- | --- |
| flusher 旧快照覆盖管理员重置 | 共享用户锁覆盖读取到整批写回 | original-defects；quota-real-coordination；quota-coordination-expanded |
| 原子 set 返回锁前旧余额 | 同一 CTE 锁定、读取和更新 | original-defects；balance-regression-fixed |
| 修改订阅有效期提前独立提交 | 加入原外层 Ent Tx，后置失败整体回滚 | original-validity；entitlement-real-transactions-fixed |
| 并发两次延长只有一次生效 | 用户/订阅锁后重读并计算整条时间链 | original-subscription-concurrency；entitlement-concurrency-fixed |
| 重置失效缓存后，Lua 缺失 key 吞掉在途新用量 | 锁内回填当前配置/窗口后再累计 | original-quota-after-reset；quota-after-reset-fixed |

上述原 HEAD 为 `8b93af63aa15c668b94cd4b8dee9e02384b6aa23`。临时原版树只增加对应复现测试，不修改生产实现。对应 `.result.json` 和测试源码均在本目录；失败的夹具准备试验与实际缺陷失败区分归档。回退这些修复会恢复原有覆盖或独立提交风险。
