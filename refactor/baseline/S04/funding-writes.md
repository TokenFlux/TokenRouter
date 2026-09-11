# S04 资金写入与过渡事务账本

十二组写入分别记录资金操作与业务编排归属；未迁闭合事务不拆分提交。逐方法旧/新位置见 [完整账本](funding-writes.json)，文件、构建条件、直接包消费者及声明差异见 [文件所有权](file-ownership.json.gz)。

## F01 普通请求结算

- 资金操作：billing/Funds + postgres
- 剩余编排：S09/S11 用量与平台编排；S15/S16 兼容清理

实际链路：旧 Gateway/OpenAI recordUsage → Apply 兼容口 → Funds.Settle → SettlementStore.Apply/applyOnce → 独立 SQL Tx、去重、付款用户锁、AllocateSubscriptions、窗口/余额/Key/成员/账号/outbox → 提交 → SettlementEffects；用量日志仍由旧网关尽力记录。

事务：usageBillingRepository.applyOnce 自行 BeginTx/Commit，所有资金效果使用同一 *sql.Tx；死锁从完整事务重试。它不是可任意嵌入外层事务的参与端口。

字段：usage_billing_dedup；users.balance；user_subscriptions 各窗口；api_keys.quota_used/窗口；team_members 用量；accounts.extra 额度

提交后与失败：SettlementEffects 按原顺序处理余额、Key 窗口、账号完成、平台额度及确定后通知；重复 Apply 的调用者分支仍不再次累计。日志失败不再次扣款；simple 只记录。

## F02 订阅发放、时间链、窗口与兑换

- 资金操作：billing 订阅/兑换/套餐
- 剩余编排：S05 身份并发与展示；S10 邮件投递；S12 支付消费者；S15/S16 别名

实际链路：HTTP 直接绑定 billing/httpapi → SubscriptionService/Plans/RedeemService。发放先锁用户再复核来源订单；管理时间链在锁后重读；Redeem 独立 Ent Tx 内锁码、权益、usage/次数，提交后才失效认证与尽力返利。

事务：SubscriptionMutations 复用已有 Ent Tx 或持有闭合事务；BalanceInTx/SubscriptionsInTx/RedeemInTx 显式绑定调用方连接且不提交。管理先锁用户与订阅后重读；Redeem 独立持有闭合事务，usage 失败回滚全部权益。

字段：user_subscriptions 生命周期、starts/expires、usage/window；api_keys.preferred_subscription_id；redeem_codes/usage；余额或并发权益

提交后与失败：Redeem 在 Commit 后失效认证/Billing 缓存并尽力返利。SubscriptionService.InvalidateSubCache 当前为空兼容入口，不能当作缓存失效证据；逐调用入口的实际失效与读取链必须随 S04 核对，不假设有统一提交回调。

## F03 管理员余额 set/add/subtract

- 资金操作：billing 原子调账；旧管理员持有后续编排
- 剩余编排：S05 用户管理；S12 返利；S15/S16 转接

实际链路：旧 AdminService.UpdateUserBalance → 窄 BalanceAdjuster → BalanceStore 原子 set/add/subtract；真实旧余额从锁定 CTE 返回。充值后的记录/返利仍尽力执行。

事务：SetBalance/AdjustBalance 用单条 SQL 原子变更并返回旧/新值，使用 clientFromContext；常规管理员调用没有把调账、返利、调整记录统一包进一个外层事务。不能改为先读余额再覆盖。

字段：users.balance；调整记录/返利为后续独立效果；普通 Update 只写显式字段

提交后与失败：成功后失效缓存；管理员 add 返利及 createAppliedAdjustmentRedeemRecord 都是尽力记录，不回滚已完成调账。UpdateBalance 正增量会增加 total_recharged，而 SetBalance/AdjustBalance 的当前 SQL 不更新该字段；不得误写成相同语义。

## F04 注册、首次身份绑定与默认赠送

- 资金操作：旧身份/注册；订阅与原子增量参加已有事务
- 剩余编排：S05；初始化留 S14

实际链路：RegisterWithVerification / OAuth 注册 → resolveSignupGrantPlan → createRegisteredUser → userRepo.Create；首次绑定/邮箱绑定 → ApplyProviderDefaultSettingsOnFirstBind → 幂等 grant 记录 + AddBalance/AddConcurrency + 订阅发放。

事务：注册用户和邀请兑换由 createRegisteredUser 的 Ent Tx 管理；用户仓储可复用传入事务。首次绑定发现外层 Tx 时参与，否则自建；grant 认领与权益一起提交。非关键初始化 runFailOpenDBStep 在已有事务中使用 SAVEPOINT 防止整事务 aborted。

字段：users 初始 balance/concurrency/APIKeyLimit；user_provider_default_grants；身份记录；user_platform_quotas；订阅

提交后与失败：保留注册成功边界、默认订阅和推广初始化的 fail-open；平台额度默认快照只初始化一次。首次绑定失败必须回滚身份和赠送，不把默认赠送当外部充值。

## F05 Key、团队成员与账号限额配置/累计/重置

- 资金操作：billing 拥有普通结算累计；配置和维护仍归 Key/团队/账号
- 剩余编排：S05/S06/S07；统计留 S08

实际链路：APIKeyService.Create/Update 负责配置与显式重置；UpdateQuotaUsed/UpdateRateLimitUsage 是仍保留但本次未找到生产直接调用的旧累计接口，不把存在声明等同活跃扣款链。TeamService.UpdateMemberLimits/ResetMemberUsage → teamRepository。adminServiceImpl.ResetAccountQuota → ResetQuotaUsedAndClearRateLimitCooldown；账号管理更新经锁定合并 extra。

事务：Key 配置复合关系使用现有 Ent Tx；总配额和速率累计各自原子 SQL，普通请求联合累计走 F01。团队限额/手动窗口重置是指定字段 SQL；owner 转移使用仓储闭合 SERIALIZABLE Tx。账号独立额度维护用 r.sql，不会因 context 中有 Ent Tx 就自动参与。

字段：配置：quota/limit、team member limits、account extra limits；重置：quota_used/窗口起点；消费：F01 专属累计及下列旧入口

提交后与失败：Key 超限失效认证；团队操作失效相关 Key。账号维护的 outbox/快照有既有尽力失败语义，不在 S00 宣称和额度更新组成新事务。 APIKeyService.IncrementUsage 只累计按日请求次数并设置 TTL，不属于金额扣减。

## F06 用户平台额度 Redis 执法与数据库镜像

- 资金操作：billing 额度/协调器 + postgres/rediscache
- 剩余编排：S05 默认注册投影；多进程协调不在本阶段支持范围

实际链路：旧准入投影 → Eligibility.Check；管理 QuotaHandler → PlatformQuotas；成功扣费 → SettlementEffects；Redis-first 回填/刷新/累计与 flusher/异步镜像共用 app 的唯一按用户协调器。

事务：单服务进程共享按用户互斥，ID 升序加锁/逆序释放。SPOP 后至读 Redis/整批 SQL 写回持锁；管理读取、提交及缓存失效持锁；异步增量写回完成后才释放。没有新增跨实例锁、事务 context 或缓存协议，Redis/PG 故障仍可能产生滞后。

字段：user_platform_quotas limit/usage/window/deleted_at；Redis schema_v1、quota hash、dirty set、TTL

提交后与失败：失败由日志/dirty 重入与既有降级处理；不强行并入 F01 SQL。管理窗口重置与 flusher 竞态是既有运维限制。

## F07 Promo Code 赠送余额

- 资金操作：旧 Promo 闭合事务
- 剩余编排：S12

实际链路：AuthService 的注册后 promo 应用 → PromoService.ApplyPromoCode → GetByCodeForUpdate → userRepo.UpdateBalance → CreateUsage → IncrementUsedCount。

事务：ApplyPromoCode 自建并拥有 Ent Tx；锁码、状态/次数复核、余额和累计充值、唯一 usage/次数同事务；完整保留至 S12.1，不能替换成自行提交的 billing 加款。

字段：promo_codes.used_count；promo_code_usages；users.balance/total_recharged

提交后与失败：Commit 后 InvalidateAuthCacheByUserID，并以有界后台 context 失效余额缓存。

## F08 返利冻结、解冻和转主余额

- 资金操作：旧返利闭合事务
- 剩余编排：S12

实际链路：AffiliateService.AccrueInviteRebate/AccrueInviteRebateForOrder → affiliateRepository.AccrueQuota；TransferAffiliateQuota → TransferQuotaToBalance → thawFrozenQuotaTx → 认领并清零返利 → 加余额/累计充值 → 插入 ledger。

事务：affiliateRepository.withTx 复用 TxFromContext 或自建 Ent Tx；转账的解冻、认领、余额和 ledger 同事务。订单来源去重留在返利/支付现有协作中。

字段：user_affiliates.aff_quota/aff_frozen_quota/aff_history_quota；user_affiliate_ledger；users.balance/total_recharged

提交后与失败：服务成功后失效认证、余额及相关缓存；失败不能先清返利再独立补主余额。管理员充值返利的 fail-open 属 F03。

## F09 支付余额和订阅履约

- 资金操作：billing 发放/兑换；旧支付持有履约与返利事务
- 剩余编排：S12

实际链路：HandlePaymentNotification/RetryFulfillment/对账 → ExecuteBalanceFulfillment/doBalance → 内部充值码 Redeem；ExecuteSubscriptionFulfillment/doSub → AssignOrExtendSubscription(SourceOrderID) → 返利 → markCompleted。

事务：订单 lease 与状态条件更新控制唯一执行；余额兑换/订阅发放是独立幂等事务，外部支付、订单最终状态和通知不是一笔数据库事务。复用来源订单和充值码恢复。

字段：payment_orders 状态/fulfillment lease；redeem_codes；user_subscriptions.source_order_id；payment_audit_logs；返利

提交后与失败：markCompleted 后通知；返利有独立审计认领。中途失败保留可恢复状态，不重新外部付款或重复权益发放。

## F10 退款准备、补偿与最终确认

- 资金操作：旧退款闭合事务
- 剩余编排：S12

实际链路：管理员退款入口 → PrepareRefund/ExecuteRefund → prepDeduct/gwRefund/finishRefund；pending 查单 → QueryAndFinalizeRefund → finalizePendingRefundSuccess → applyRefundFinalDeduction → userRepo.DeductBalance 或订阅调整 → markRefundOkTx。

事务：pending 最终确认自建 Ent Tx，条件认领 REFUND_PENDING 后使用 NewTxContext 让真实 userRepo.sqlExecutorFromContext/订阅操作参与；余额、订单最终状态和成功审计同事务。外部 QueryRefund 在 BeginTx 前。初始退款路径的预扣/渠道调用/补偿仍是现有多步流程。

字段：users.balance；user_subscriptions；payment_orders refund/status；payment_audit_logs REFUND_SUCCESS

提交后与失败：初次准备可能要求 require_force；扣款最多为当前非负可用余额。pending 重放不重复扣减。审计失败必须回滚整条最终确认；S00 新增真实 PostgreSQL 测试覆盖此边界和竞争。

## F11 创作台/批量图片预占、捕获和释放

- 资金操作：billing 通用任务资金；postgres 暂知两张任务投影
- 剩余编排：S13 任务状态机及任务表投影

实际链路：旧 Creative/BatchImage 命令投影 → Funds.Reserve/Capture/Release → SettlementStore 同事务写冻结、分配、allowance 与任务表投影；历史 request ID/指纹与价格快照不重编码。

事务：applyBatchImageBalanceHoldOnce 自建完整 *sql.Tx；动作幂等、余额冻结/订阅分配、Key/成员预记和任务投影一起提交。CreativeEntity 仍选择 creative_runs / batch_image_jobs，不提前改命令或持久幂等键。

字段：users.balance/frozen_balance；subscriptions allocations；api_keys/team_members allowance；batch_image_jobs/creative_runs 的 hold/projection；usage_billing_dedup

提交后与失败：提交上游前指定订阅须完整覆盖；上游成功后只重试结算不再调用 provider。Redis 输出/outbox 补偿按既有任务状态机，已执行成功但结果丢失不能自动退款。

## F12 setup/simple 初始化、备份恢复及历史 SQL

- 资金操作：旧 setup/bootstrap/恢复编排
- 剩余编排：S14/S16

实际链路：setup.Install/AutoSetupFromEnv → initializeDatabase/createAdminUser；InitEnt → simple 默认组/管理员并发初始化。BackupService.restoreBackupArchive → dump/restore Adapter；SQL runner 执行已发布前向迁移。

事务：初始化/恢复可以受控直接创建或恢复持久数据，不能伪装成充值。SQL runner 的 tx/notx/advisory lock 按既有迁移契约；已发布 309 个 SQL checksum 冻结。simple 默认组/并发本身不是运行时加款入口。

字段：初始化用户余额；simple 配置/并发；备份包含的资金历史表；已发布 migration 数据修正

提交后与失败：恢复流程持有数据库重维护互斥并保存成功/失败记录；当前 completeRestore 不负责重启。S00 不执行真实备份恢复或 setup 写入。长期例外限定初始化/恢复，不能外推到普通 HTTP 调账。
