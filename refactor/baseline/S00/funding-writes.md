# 资金写入与跨模块事务基线

十二组入口已展开为实际方法，详见 [funding-writes.json](funding-writes.json)。每条方法包含源位置、事务标记、写入操作和调用语法候选；以下真实链路与事务结论经代码核实。当前仍存在多种事务所有者，不能宣称资金已经全部收敛到 billing。

## F01 普通请求结算

**退出阶段：** S04；编排 S09/S11

**实际调用链：** GatewayService.recordUsageCore → applyUsageBilling → UsageBillingRepository.Apply → applyOnce → claimUsageBillingKey/lockUsageBillingUser/applyUsageBillingEffects；OpenAI 完成处理也调用同一 Apply。

**事务边界：** usageBillingRepository.applyOnce 自行 BeginTx/Commit，所有资金效果使用同一 *sql.Tx；死锁从完整事务重试。它不是可任意嵌入外层事务的参与端口。

**副作用与失败语义：** Apply 成功后调用者同步余额缓存、Key/账号状态与通知，并尽力写 Usage Log；simple 分支跳过资金事务。结算失败记录待对账事实，不伪造成功扣款。

**字段范围：** usage_billing_dedup；users.balance；user_subscriptions 各窗口；api_keys.quota_used/窗口；team_members 用量；accounts.extra 额度

| 实际方法 | 源文件与行 |
| --- | --- |
| GatewayService.recordUsageCore | [backend/internal/service/gateway_usage_billing.go:596](../../../backend/internal/service/gateway_usage_billing.go) |
| applyUsageBilling | [backend/internal/service/gateway_usage_billing.go:290](../../../backend/internal/service/gateway_usage_billing.go) |
| syncBalanceCacheAfterDeduction | [backend/internal/service/gateway_usage_billing.go:327](../../../backend/internal/service/gateway_usage_billing.go) |
| writeUsageLogBestEffort | [backend/internal/service/gateway_usage_billing.go:468](../../../backend/internal/service/gateway_usage_billing.go) |
| usageBillingRepository.Apply | [backend/internal/repository/usage_billing_repo.go:29](../../../backend/internal/repository/usage_billing_repo.go) |
| usageBillingRepository.applyOnce | [backend/internal/repository/usage_billing_repo.go:48](../../../backend/internal/repository/usage_billing_repo.go) |
| usageBillingRepository.claimUsageBillingRequest | [backend/internal/repository/usage_billing_repo.go:227](../../../backend/internal/repository/usage_billing_repo.go) |
| usageBillingRepository.applyUsageBillingEffects | [backend/internal/repository/usage_billing_repo.go:700](../../../backend/internal/repository/usage_billing_repo.go) |
| deductUsageBillingBalance | [backend/internal/repository/usage_billing_repo.go:1294](../../../backend/internal/repository/usage_billing_repo.go) |
| allocateUsageBillingSubscriptions | [backend/internal/repository/usage_billing_repo.go:884](../../../backend/internal/repository/usage_billing_repo.go) |
| updateUsageBillingSubscription | [backend/internal/repository/usage_billing_repo.go:1244](../../../backend/internal/repository/usage_billing_repo.go) |
| incrementUsageBillingAPIKeyQuota | [backend/internal/repository/usage_billing_repo.go:1720](../../../backend/internal/repository/usage_billing_repo.go) |
| incrementUsageBillingAPIKeyRateLimit | [backend/internal/repository/usage_billing_repo.go:1748](../../../backend/internal/repository/usage_billing_repo.go) |
| incrementUsageBillingTeamMember | [backend/internal/repository/usage_billing_repo.go:772](../../../backend/internal/repository/usage_billing_repo.go) |
| incrementUsageBillingAccountQuota | [backend/internal/repository/usage_billing_repo.go:1774](../../../backend/internal/repository/usage_billing_repo.go) |

## F02 订阅发放、时间链、窗口与兑换

**退出阶段：** S04；外部调用 S05/S12

**实际调用链：** 支付 doSub、AuthService.assignSubscriptions/首次绑定、管理员订阅入口 → AssignOrExtendSubscription；RedeemService.Redeem → userRepo.UpdateBalance/ApplyRedeemBalanceAdjustment 或 AssignOrExtendSubscription → redeem usage 与次数写入。

**事务边界：** AssignOrExtendSubscription/withSubscriptionMutationTx 发现 TxFromContext 时复用外层，否则自建 Ent Tx；发放锁用户并复查来源订单。Redeem 独立持有 Ent Tx，兑换码锁、权益与 usage/次数同事务。自助撤销包含时间链平移和 Key 改绑。

**副作用与失败语义：** Redeem 在 Commit 后失效认证/Billing 缓存并尽力返利。SubscriptionService.InvalidateSubCache 当前为空兼容入口，不能当作缓存失效证据；逐调用入口的实际失效与读取链必须随 S04 核对，不假设有统一提交回调。

**字段范围：** user_subscriptions 生命周期、starts/expires、usage/window；api_keys.preferred_subscription_id；redeem_codes/usage；余额或并发权益

| 实际方法 | 源文件与行 |
| --- | --- |
| SubscriptionService.AssignSubscription | [backend/internal/service/subscription_service.go:168](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.AssignOrExtendSubscription | [backend/internal/service/subscription_service.go:173](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.assignOrExtendSubscriptionInTx | [backend/internal/service/subscription_service.go:237](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.BulkAssignSubscription | [backend/internal/service/subscription_service.go:322](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.RevokeSubscription | [backend/internal/service/subscription_service.go:369](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.RevokeOwnExhaustedSubscription | [backend/internal/service/subscription_service.go:397](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.revokeOwnExhaustedSubscriptionInTx | [backend/internal/service/subscription_service.go:424](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.rebindSubscriptionAPIKeys | [backend/internal/service/subscription_service.go:507](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.RestoreSubscription | [backend/internal/service/subscription_service.go:527](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.ExtendSubscription | [backend/internal/service/subscription_service.go:627](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.SetSubscriptionValidityDays | [backend/internal/service/subscription_service.go:710](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.withSubscriptionMutationTx | [backend/internal/service/subscription_service.go:690](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.AdminResetQuota | [backend/internal/service/subscription_service.go:997](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.checkAndResetWindowsAt | [backend/internal/service/subscription_service.go:1017](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.EnsureWindowMaintenance | [backend/internal/service/subscription_service.go:1048](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.DoWindowMaintenance | [backend/internal/service/subscription_service.go:1120](../../../backend/internal/service/subscription_service.go) |
| SubscriptionService.RecordUsage | [backend/internal/service/subscription_service.go:1132](../../../backend/internal/service/subscription_service.go) |
| RedeemService.Redeem | [backend/internal/service/redeem_service.go:422](../../../backend/internal/service/redeem_service.go) |
| RedeemService.invalidateRedeemCaches | [backend/internal/service/redeem_service.go:562](../../../backend/internal/service/redeem_service.go) |
| RedeemService.tryAccrueAffiliateRebateForRedeem | [backend/internal/service/redeem_service.go:591](../../../backend/internal/service/redeem_service.go) |
| userSubscriptionRepository.Create | [backend/internal/repository/user_subscription_repo.go:25](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.Update | [backend/internal/repository/user_subscription_repo.go:116](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.Delete | [backend/internal/repository/user_subscription_repo.go:150](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.ExtendExpiry | [backend/internal/repository/user_subscription_repo.go:412](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.UpdateStatus | [backend/internal/repository/user_subscription_repo.go:420](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.ResetUsageWindows | [backend/internal/repository/user_subscription_repo.go:455](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.ResetDailyUsage | [backend/internal/repository/user_subscription_repo.go:471](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.ResetWeeklyUsage | [backend/internal/repository/user_subscription_repo.go:486](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.ResetMonthlyUsage | [backend/internal/repository/user_subscription_repo.go:501](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.IncrementUsage | [backend/internal/repository/user_subscription_repo.go:536](../../../backend/internal/repository/user_subscription_repo.go) |
| userSubscriptionRepository.BatchUpdateExpiredStatus | [backend/internal/repository/user_subscription_repo.go:564](../../../backend/internal/repository/user_subscription_repo.go) |
| redeemCodeRepository.Use | [backend/internal/repository/redeem_code_repo.go:276](../../../backend/internal/repository/redeem_code_repo.go) |
| redeemCodeRepository.CreateUsage | [backend/internal/repository/redeem_code_repo.go:343](../../../backend/internal/repository/redeem_code_repo.go) |
| redeemCodeRepository.Update | [backend/internal/repository/redeem_code_repo.go:146](../../../backend/internal/repository/redeem_code_repo.go) |
| userRepository.UpdateBalance | [backend/internal/repository/user_repo.go:836](../../../backend/internal/repository/user_repo.go) |
| userRepository.ApplyRedeemBalanceAdjustment | [backend/internal/repository/user_repo.go:872](../../../backend/internal/repository/user_repo.go) |
| userRepository.UpdateConcurrency | [backend/internal/repository/user_repo.go:1007](../../../backend/internal/repository/user_repo.go) |
| userRepository.ApplyRedeemConcurrencyAdjustment | [backend/internal/repository/user_repo.go:1020](../../../backend/internal/repository/user_repo.go) |

## F03 管理员余额 set/add/subtract

**退出阶段：** 资金 S04；管理入口 S05

**实际调用链：** admin.UserHandler 的余额操作 → AdminService.UpdateUserBalance → userRepository.SetBalance/AdjustBalance；结果再交认证/Billing 失效、返利与调整记录。

**事务边界：** SetBalance/AdjustBalance 用单条 SQL 原子变更并返回旧/新值，使用 clientFromContext；常规管理员调用没有把调账、返利、调整记录统一包进一个外层事务。不能改为先读余额再覆盖。

**副作用与失败语义：** 成功后失效缓存；管理员 add 返利及 createAppliedAdjustmentRedeemRecord 都是尽力记录，不回滚已完成调账。UpdateBalance 正增量会增加 total_recharged，而 SetBalance/AdjustBalance 的当前 SQL 不更新该字段；不得误写成相同语义。

**字段范围：** users.balance；调整记录/返利为后续独立效果；普通 Update 只写显式字段

| 实际方法 | 源文件与行 |
| --- | --- |
| adminServiceImpl.UpdateUserBalance | [backend/internal/service/admin_user.go:578](../../../backend/internal/service/admin_user.go) |
| adminServiceImpl.tryAccrueAffiliateRebateForAdminRecharge | [backend/internal/service/admin_user.go:632](../../../backend/internal/service/admin_user.go) |
| adminServiceImpl.CreateUser | [backend/internal/service/admin_user.go:119](../../../backend/internal/service/admin_user.go) |
| adminServiceImpl.UpdateUser | [backend/internal/service/admin_user.go:214](../../../backend/internal/service/admin_user.go) |
| UserService.UpdateBalance | [backend/internal/service/user_service.go:1153](../../../backend/internal/service/user_service.go) |
| userRepository.SetBalance | [backend/internal/repository/user_repo.go:934](../../../backend/internal/repository/user_repo.go) |
| userRepository.AdjustBalance | [backend/internal/repository/user_repo.go:910](../../../backend/internal/repository/user_repo.go) |
| userRepository.currentBalance | [backend/internal/repository/user_repo.go:961](../../../backend/internal/repository/user_repo.go) |
| scanBalanceChange | [backend/internal/repository/user_repo.go:985](../../../backend/internal/repository/user_repo.go) |

## F04 注册、首次身份绑定与默认赠送

**退出阶段：** S05

**实际调用链：** RegisterWithVerification / OAuth 注册 → resolveSignupGrantPlan → createRegisteredUser → userRepo.Create；首次绑定/邮箱绑定 → ApplyProviderDefaultSettingsOnFirstBind → 幂等 grant 记录 + AddBalance/AddConcurrency + 订阅发放。

**事务边界：** 注册用户和邀请兑换由 createRegisteredUser 的 Ent Tx 管理；用户仓储可复用传入事务。首次绑定发现外层 Tx 时参与，否则自建；grant 认领与权益一起提交。非关键初始化 runFailOpenDBStep 在已有事务中使用 SAVEPOINT 防止整事务 aborted。

**副作用与失败语义：** 保留注册成功边界、默认订阅和推广初始化的 fail-open；平台额度默认快照只初始化一次。首次绑定失败必须回滚身份和赠送，不把默认赠送当外部充值。

**字段范围：** users 初始 balance/concurrency/APIKeyLimit；user_provider_default_grants；身份记录；user_platform_quotas；订阅

| 实际方法 | 源文件与行 |
| --- | --- |
| AuthService.RegisterWithVerification | [backend/internal/service/auth_service.go:171](../../../backend/internal/service/auth_service.go) |
| AuthService.loginOrRegisterOAuthWithTokenPair | [backend/internal/service/auth_service.go:682](../../../backend/internal/service/auth_service.go) |
| AuthService.createRegisteredUser | [backend/internal/service/auth_service.go:999](../../../backend/internal/service/auth_service.go) |
| AuthService.resolveSignupGrantPlan | [backend/internal/service/auth_service.go:1090](../../../backend/internal/service/auth_service.go) |
| AuthService.assignSubscriptions | [backend/internal/service/auth_service.go:901](../../../backend/internal/service/auth_service.go) |
| AuthService.runFailOpenDBStep | [backend/internal/service/auth_service.go:920](../../../backend/internal/service/auth_service.go) |
| AuthService.postAuthUserBootstrap | [backend/internal/service/auth_service.go:1165](../../../backend/internal/service/auth_service.go) |
| AuthService.snapshotPlatformQuotaDefaults | [backend/internal/service/auth_service.go:2053](../../../backend/internal/service/auth_service.go) |
| AuthService.ApplyProviderDefaultSettingsOnFirstBind | [backend/internal/service/auth_oauth_first_bind.go:16](../../../backend/internal/service/auth_oauth_first_bind.go) |
| AuthService.applyProviderDefaultSettingsOnFirstBind | [backend/internal/service/auth_oauth_first_bind.go:42](../../../backend/internal/service/auth_oauth_first_bind.go) |
| AuthService.BindEmailIdentity | [backend/internal/service/auth_email_binding.go:29](../../../backend/internal/service/auth_email_binding.go) |
| AuthService.updateBoundEmailIdentityTx | [backend/internal/service/auth_email_binding.go:286](../../../backend/internal/service/auth_email_binding.go) |
| AuthService.updateBoundEmailIdentityWithClient | [backend/internal/service/auth_email_binding.go:314](../../../backend/internal/service/auth_email_binding.go) |
| userRepository.Create | [backend/internal/repository/user_repo.go:63](../../../backend/internal/repository/user_repo.go) |
| userRepository.CreateWithRegistrationEmailGuards | [backend/internal/repository/user_repo.go:77](../../../backend/internal/repository/user_repo.go) |
| userRepository.createWithNormalizationGuard | [backend/internal/repository/user_repo.go:81](../../../backend/internal/repository/user_repo.go) |
| userRepository.createWithClient | [backend/internal/repository/user_repo.go:1779](../../../backend/internal/repository/user_repo.go) |

## F05 Key、团队成员与账号限额配置/累计/重置

**退出阶段：** S05—S07；普通累计 S04

**实际调用链：** APIKeyService.Create/Update 负责配置与显式重置；UpdateQuotaUsed/UpdateRateLimitUsage 是仍保留但本次未找到生产直接调用的旧累计接口，不把存在声明等同活跃扣款链。TeamService.UpdateMemberLimits/ResetMemberUsage → teamRepository。adminServiceImpl.ResetAccountQuota → ResetQuotaUsedAndClearRateLimitCooldown；账号管理更新经锁定合并 extra。

**事务边界：** Key 配置复合关系使用现有 Ent Tx；总配额和速率累计各自原子 SQL，普通请求联合累计走 F01。团队限额/手动窗口重置是指定字段 SQL；owner 转移使用仓储闭合 SERIALIZABLE Tx。账号独立额度维护用 r.sql，不会因 context 中有 Ent Tx 就自动参与。

**副作用与失败语义：** Key 超限失效认证；团队操作失效相关 Key。账号维护的 outbox/快照有既有尽力失败语义，不在 S00 宣称和额度更新组成新事务。

**字段范围：** 配置：quota/limit、team member limits、account extra limits；重置：quota_used/窗口起点；消费：F01 专属累计及下列旧入口

| 实际方法 | 源文件与行 |
| --- | --- |
| APIKeyService.Create | [backend/internal/service/api_key_service.go:695](../../../backend/internal/service/api_key_service.go) |
| APIKeyService.Update | [backend/internal/service/api_key_service.go:1271](../../../backend/internal/service/api_key_service.go) |
| APIKeyService.IncrementUsage | [backend/internal/service/api_key_service.go:1753](../../../backend/internal/service/api_key_service.go) |
| APIKeyService.UpdateQuotaUsed | [backend/internal/service/api_key_service.go:1917](../../../backend/internal/service/api_key_service.go) |
| APIKeyService.UpdateRateLimitUsage | [backend/internal/service/api_key_service.go:1970](../../../backend/internal/service/api_key_service.go) |
| apiKeyRepository.Create | [backend/internal/repository/api_key_repo.go:73](../../../backend/internal/repository/api_key_repo.go) |
| apiKeyRepository.Update | [backend/internal/repository/api_key_repo.go:416](../../../backend/internal/repository/api_key_repo.go) |
| apiKeyRepository.IncrementQuotaUsed | [backend/internal/repository/api_key_repo.go:1134](../../../backend/internal/repository/api_key_repo.go) |
| apiKeyRepository.IncrementQuotaUsedAndGetState | [backend/internal/repository/api_key_repo.go:1150](../../../backend/internal/repository/api_key_repo.go) |
| apiKeyRepository.IncrementRateLimitUsage | [backend/internal/repository/api_key_repo.go:1191](../../../backend/internal/repository/api_key_repo.go) |
| apiKeyRepository.ResetRateLimitWindows | [backend/internal/repository/api_key_repo.go:1207](../../../backend/internal/repository/api_key_repo.go) |
| TeamService.UpdateDefaultMemberLimits | [backend/internal/service/team.go:340](../../../backend/internal/service/team.go) |
| TeamService.UpdateMemberLimits | [backend/internal/service/team.go:689](../../../backend/internal/service/team.go) |
| TeamService.ResetMemberUsage | [backend/internal/service/team.go:704](../../../backend/internal/service/team.go) |
| TeamService.ResolveOwnershipTransfer | [backend/internal/service/team.go:756](../../../backend/internal/service/team.go) |
| TeamService.AdminForceTransfer | [backend/internal/service/team.go:844](../../../backend/internal/service/team.go) |
| teamRepository.SetDefaultMemberLimits | [backend/internal/repository/team_repo.go:123](../../../backend/internal/repository/team_repo.go) |
| teamRepository.UpdateMemberLimits | [backend/internal/repository/team_repo.go:438](../../../backend/internal/repository/team_repo.go) |
| teamRepository.ResetMemberUsage | [backend/internal/repository/team_repo.go:452](../../../backend/internal/repository/team_repo.go) |
| teamRepository.ResolveOwnershipTransfer | [backend/internal/repository/team_repo.go:507](../../../backend/internal/repository/team_repo.go) |
| teamRepository.ForceTransfer | [backend/internal/repository/team_repo.go:622](../../../backend/internal/repository/team_repo.go) |
| transferTeamOwnership | [backend/internal/repository/team_repo.go:656](../../../backend/internal/repository/team_repo.go) |
| userRepository.BatchSetConcurrency | [backend/internal/repository/user_repo.go:1041](../../../backend/internal/repository/user_repo.go) |
| userRepository.BatchAddConcurrency | [backend/internal/repository/user_repo.go:1062](../../../backend/internal/repository/user_repo.go) |
| userRepository.BatchUpdateLimits | [backend/internal/repository/user_repo.go:1081](../../../backend/internal/repository/user_repo.go) |
| adminServiceImpl.CreateAccount | [backend/internal/service/admin_account.go:586](../../../backend/internal/service/admin_account.go) |
| adminServiceImpl.UpdateAccount | [backend/internal/service/admin_account.go:675](../../../backend/internal/service/admin_account.go) |
| adminServiceImpl.BulkUpdateAccounts | [backend/internal/service/admin_account.go:987](../../../backend/internal/service/admin_account.go) |
| adminServiceImpl.ResetAccountQuota | [backend/internal/service/admin_account.go:1618](../../../backend/internal/service/admin_account.go) |
| accountRepository.updateLockedAccount | [backend/internal/repository/account_repo.go:498](../../../backend/internal/repository/account_repo.go) |
| lockAndMergeAccountManagedExtra | [backend/internal/repository/account_repo.go:589](../../../backend/internal/repository/account_repo.go) |
| accountRepository.UpdateExtra | [backend/internal/repository/account_repo.go:2420](../../../backend/internal/repository/account_repo.go) |
| accountRepository.BulkUpdate | [backend/internal/repository/account_repo.go:2645](../../../backend/internal/repository/account_repo.go) |
| accountRepository.IncrementQuotaUsed | [backend/internal/repository/account_repo.go:3404](../../../backend/internal/repository/account_repo.go) |
| accountRepository.ResetQuotaUsedAndClearRateLimitCooldown | [backend/internal/repository/account_repo.go:3476](../../../backend/internal/repository/account_repo.go) |

## F06 用户平台额度 Redis 执法与数据库镜像

**退出阶段：** S04

**实际调用链：** applyUsageBilling 的成功后处理 → BillingCacheService.IncrementUserPlatformQuotaUsage → billingCache.IncrUserPlatformQuotaUsageCache；flusher.flushOneBatch → BatchSnapshotUsage；管理员和注册入口经 UserPlatformQuotaServiceAdapter。

**事务边界：** Redis Lua 更新用量与 dirty set；flusher 将绝对值批量镜像到 PostgreSQL。独立数据库 IncrementUsageWithReset/ResetExpiredWindow 通过 withTx 行锁，可复用外层 Ent Tx。Redis 和 PostgreSQL 不是同一事务。

**副作用与失败语义：** 失败由日志/dirty 重入与既有降级处理；不强行并入 F01 SQL。管理窗口重置与 flusher 竞态是既有运维限制。

**字段范围：** user_platform_quotas limit/usage/window/deleted_at；Redis schema_v1、quota hash、dirty set、TTL

| 实际方法 | 源文件与行 |
| --- | --- |
| BillingCacheService.IncrementUserPlatformQuotaUsage | [backend/internal/service/billing_cache_service.go:565](../../../backend/internal/service/billing_cache_service.go) |
| BillingCacheService.checkUserPlatformQuotaEligibility | [backend/internal/service/billing_cache_service.go:955](../../../backend/internal/service/billing_cache_service.go) |
| UserPlatformQuotaUsageFlusher.flushOneBatch | [backend/internal/service/user_platform_quota_flusher.go:121](../../../backend/internal/service/user_platform_quota_flusher.go) |
| UserPlatformQuotaUsageFlusher.readdOrCountLost | [backend/internal/service/user_platform_quota_flusher.go:109](../../../backend/internal/service/user_platform_quota_flusher.go) |
| UserPlatformQuotaUsageFlusher.flush | [backend/internal/service/user_platform_quota_flusher.go:225](../../../backend/internal/service/user_platform_quota_flusher.go) |
| UserPlatformQuotaUsageFlusher.Stop | [backend/internal/service/user_platform_quota_flusher.go:264](../../../backend/internal/service/user_platform_quota_flusher.go) |
| billingCache.SetUserPlatformQuotaCache | [backend/internal/repository/billing_cache.go:302](../../../backend/internal/repository/billing_cache.go) |
| billingCache.DeleteUserPlatformQuotaCache | [backend/internal/repository/billing_cache.go:342](../../../backend/internal/repository/billing_cache.go) |
| billingCache.IncrUserPlatformQuotaUsageCache | [backend/internal/repository/billing_cache.go:390](../../../backend/internal/repository/billing_cache.go) |
| billingCache.PopDirtyUserPlatformQuotaKeys | [backend/internal/repository/billing_cache.go:425](../../../backend/internal/repository/billing_cache.go) |
| billingCache.ReaddDirtyUserPlatformQuotaKeys | [backend/internal/repository/billing_cache.go:448](../../../backend/internal/repository/billing_cache.go) |
| userPlatformQuotaRepository.BulkInsertInitial | [backend/internal/repository/user_platform_quota_repo.go:91](../../../backend/internal/repository/user_platform_quota_repo.go) |
| userPlatformQuotaRepository.IncrementUsageWithReset | [backend/internal/repository/user_platform_quota_repo.go:179](../../../backend/internal/repository/user_platform_quota_repo.go) |
| userPlatformQuotaRepository.ResetExpiredWindow | [backend/internal/repository/user_platform_quota_repo.go:244](../../../backend/internal/repository/user_platform_quota_repo.go) |
| userPlatformQuotaRepository.UpsertForUser | [backend/internal/repository/user_platform_quota_repo.go:339](../../../backend/internal/repository/user_platform_quota_repo.go) |
| softDeleteMissingPlatforms | [backend/internal/repository/user_platform_quota_repo.go:368](../../../backend/internal/repository/user_platform_quota_repo.go) |
| updateLimitsRow | [backend/internal/repository/user_platform_quota_repo.go:397](../../../backend/internal/repository/user_platform_quota_repo.go) |
| insertLimitsRow | [backend/internal/repository/user_platform_quota_repo.go:415](../../../backend/internal/repository/user_platform_quota_repo.go) |
| userPlatformQuotaRepository.BatchSnapshotUsage | [backend/internal/repository/user_platform_quota_repo.go:452](../../../backend/internal/repository/user_platform_quota_repo.go) |
| userPlatformQuotaRepository.withTx | [backend/internal/repository/user_platform_quota_repo.go:273](../../../backend/internal/repository/user_platform_quota_repo.go) |

## F07 Promo Code 赠送余额

**退出阶段：** S12.1

**实际调用链：** AuthService 的注册后 promo 应用 → PromoService.ApplyPromoCode → GetByCodeForUpdate → userRepo.UpdateBalance → CreateUsage → IncrementUsedCount。

**事务边界：** ApplyPromoCode 自建并拥有 Ent Tx；锁码、状态/次数复核、余额和累计充值、唯一 usage/次数同事务；完整保留至 S12.1，不能替换成自行提交的 billing 加款。

**副作用与失败语义：** Commit 后 InvalidateAuthCacheByUserID，并以有界后台 context 失效余额缓存。

**字段范围：** promo_codes.used_count；promo_code_usages；users.balance/total_recharged

| 实际方法 | 源文件与行 |
| --- | --- |
| PromoService.ApplyPromoCode | [backend/internal/service/promo_service.go:91](../../../backend/internal/service/promo_service.go) |
| PromoService.invalidatePromoCaches | [backend/internal/service/promo_service.go:165](../../../backend/internal/service/promo_service.go) |
| promoCodeRepository.GetByCodeForUpdate | [backend/internal/repository/promo_code_repo.go:75](../../../backend/internal/repository/promo_code_repo.go) |
| promoCodeRepository.CreateUsage | [backend/internal/repository/promo_code_repo.go:191](../../../backend/internal/repository/promo_code_repo.go) |
| promoCodeRepository.IncrementUsedCount | [backend/internal/repository/promo_code_repo.go:247](../../../backend/internal/repository/promo_code_repo.go) |
| userRepository.UpdateBalance | [backend/internal/repository/user_repo.go:836](../../../backend/internal/repository/user_repo.go) |

## F08 返利冻结、解冻和转主余额

**退出阶段：** S12.1

**实际调用链：** AffiliateService.AccrueInviteRebate/AccrueInviteRebateForOrder → affiliateRepository.AccrueQuota；TransferAffiliateQuota → TransferQuotaToBalance → thawFrozenQuotaTx → 认领并清零返利 → 加余额/累计充值 → 插入 ledger。

**事务边界：** affiliateRepository.withTx 复用 TxFromContext 或自建 Ent Tx；转账的解冻、认领、余额和 ledger 同事务。订单来源去重留在返利/支付现有协作中。

**副作用与失败语义：** 服务成功后失效认证、余额及相关缓存；失败不能先清返利再独立补主余额。管理员充值返利的 fail-open 属 F03。

**字段范围：** user_affiliates.aff_quota/aff_frozen_quota/aff_history_quota；user_affiliate_ledger；users.balance/total_recharged

| 实际方法 | 源文件与行 |
| --- | --- |
| AffiliateService.AccrueInviteRebate | [backend/internal/service/affiliate_service.go:316](../../../backend/internal/service/affiliate_service.go) |
| AffiliateService.AccrueInviteRebateForOrder | [backend/internal/service/affiliate_service.go:322](../../../backend/internal/service/affiliate_service.go) |
| AffiliateService.TransferAffiliateQuota | [backend/internal/service/affiliate_service.go:413](../../../backend/internal/service/affiliate_service.go) |
| AffiliateService.invalidateAffiliateCaches | [backend/internal/service/affiliate_service.go:482](../../../backend/internal/service/affiliate_service.go) |
| affiliateRepository.AccrueQuota | [backend/internal/repository/affiliate_repo.go:117](../../../backend/internal/repository/affiliate_repo.go) |
| affiliateRepository.ThawFrozenQuota | [backend/internal/repository/affiliate_repo.go:183](../../../backend/internal/repository/affiliate_repo.go) |
| thawFrozenQuotaTx | [backend/internal/repository/affiliate_repo.go:194](../../../backend/internal/repository/affiliate_repo.go) |
| affiliateRepository.TransferQuotaToBalance | [backend/internal/repository/affiliate_repo.go:235](../../../backend/internal/repository/affiliate_repo.go) |
| affiliateRepository.withTx | [backend/internal/repository/affiliate_repo.go:745](../../../backend/internal/repository/affiliate_repo.go) |

## F09 支付余额和订阅履约

**退出阶段：** S12.3

**实际调用链：** HandlePaymentNotification/RetryFulfillment/对账 → ExecuteBalanceFulfillment/doBalance → 内部充值码 Redeem；ExecuteSubscriptionFulfillment/doSub → AssignOrExtendSubscription(SourceOrderID) → 返利 → markCompleted。

**事务边界：** 订单 lease 与状态条件更新控制唯一执行；余额兑换/订阅发放是独立幂等事务，外部支付、订单最终状态和通知不是一笔数据库事务。复用来源订单和充值码恢复。

**副作用与失败语义：** markCompleted 后通知；返利有独立审计认领。中途失败保留可恢复状态，不重新外部付款或重复权益发放。

**字段范围：** payment_orders 状态/fulfillment lease；redeem_codes；user_subscriptions.source_order_id；payment_audit_logs；返利

| 实际方法 | 源文件与行 |
| --- | --- |
| PaymentService.HandlePaymentNotification | [backend/internal/service/payment_fulfillment.go:35](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.confirmPayment | [backend/internal/service/payment_fulfillment.go:133](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.executeFulfillment | [backend/internal/service/payment_fulfillment.go:327](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.ExecuteBalanceFulfillment | [backend/internal/service/payment_fulfillment.go:338](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.acquirePaymentFulfillmentLease | [backend/internal/service/payment_fulfillment.go:366](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.doBalance | [backend/internal/service/payment_fulfillment.go:441](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.ExecuteSubscriptionFulfillment | [backend/internal/service/payment_fulfillment.go:575](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.doSub | [backend/internal/service/payment_fulfillment.go:606](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.applyAffiliateRebateForOrder | [backend/internal/service/payment_fulfillment.go:654](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.tryClaimAffiliateRebateAudit | [backend/internal/service/payment_fulfillment.go:740](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.markCompleted | [backend/internal/service/payment_fulfillment.go:470](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.dispatchPaymentFulfillmentNotification | [backend/internal/service/payment_fulfillment.go:501](../../../backend/internal/service/payment_fulfillment.go) |
| PaymentService.RetryFulfillment | [backend/internal/service/payment_fulfillment.go:857](../../../backend/internal/service/payment_fulfillment.go) |

## F10 退款准备、补偿与最终确认

**退出阶段：** S12.4

**实际调用链：** 管理员退款入口 → PrepareRefund/ExecuteRefund → prepDeduct/gwRefund/finishRefund；pending 查单 → QueryAndFinalizeRefund → finalizePendingRefundSuccess → applyRefundFinalDeduction → userRepo.DeductBalance 或订阅调整 → markRefundOkTx。

**事务边界：** pending 最终确认自建 Ent Tx，条件认领 REFUND_PENDING 后使用 NewTxContext 让真实 userRepo.sqlExecutorFromContext/订阅操作参与；余额、订单最终状态和成功审计同事务。外部 QueryRefund 在 BeginTx 前。初始退款路径的预扣/渠道调用/补偿仍是现有多步流程。

**副作用与失败语义：** 初次准备可能要求 require_force；扣款最多为当前非负可用余额。pending 重放不重复扣减。审计失败必须回滚整条最终确认；S00 新增真实 PostgreSQL 测试覆盖此边界和竞争。

**字段范围：** users.balance；user_subscriptions；payment_orders refund/status；payment_audit_logs REFUND_SUCCESS

| 实际方法 | 源文件与行 |
| --- | --- |
| PaymentService.PrepareRefund | [backend/internal/service/payment_refund.go:205](../../../backend/internal/service/payment_refund.go) |
| PaymentService.prepDeduct | [backend/internal/service/payment_refund.go:254](../../../backend/internal/service/payment_refund.go) |
| PaymentService.ExecuteRefund | [backend/internal/service/payment_refund.go:298](../../../backend/internal/service/payment_refund.go) |
| PaymentService.gwRefund | [backend/internal/service/payment_refund.go:351](../../../backend/internal/service/payment_refund.go) |
| PaymentService.finishRefund | [backend/internal/service/payment_refund.go:408](../../../backend/internal/service/payment_refund.go) |
| PaymentService.QueryAndFinalizeRefund | [backend/internal/service/payment_refund.go:423](../../../backend/internal/service/payment_refund.go) |
| PaymentService.finalizePendingRefundSuccess | [backend/internal/service/payment_refund.go:470](../../../backend/internal/service/payment_refund.go) |
| PaymentService.applyRefundFinalDeduction | [backend/internal/service/payment_refund.go:537](../../../backend/internal/service/payment_refund.go) |
| PaymentService.markRefundOk | [backend/internal/service/payment_refund.go:615](../../../backend/internal/service/payment_refund.go) |
| PaymentService.markRefundOkTx | [backend/internal/service/payment_refund.go:630](../../../backend/internal/service/payment_refund.go) |
| PaymentService.RollbackRefund | [backend/internal/service/payment_refund.go:707](../../../backend/internal/service/payment_refund.go) |
| userRepository.DeductBalance | [backend/internal/repository/user_repo.go:894](../../../backend/internal/repository/user_repo.go) |
| userRepository.sqlExecutorFromContext | [backend/internal/repository/user_repo.go:1666](../../../backend/internal/repository/user_repo.go) |
| deductUserBalance | [backend/internal/repository/user_repo.go:1673](../../../backend/internal/repository/user_repo.go) |
| userRepository.AddBalance | [backend/internal/repository/user_repo.go:853](../../../backend/internal/repository/user_repo.go) |

## F11 创作台/批量图片预占、捕获和释放

**退出阶段：** 资金实现 S04；任务投影去耦 S13

**实际调用链：** Creative reserve/capture/release helper 及 BatchImage 同名 helper → UsageBillingRepository.ReserveBatchImageBalance/CaptureBatchImageBalance/ReleaseBatchImageBalance；recovery/outbox 重试同一动作。

**事务边界：** applyBatchImageBalanceHoldOnce 自建完整 *sql.Tx；动作幂等、余额冻结/订阅分配、Key/成员预记和任务投影一起提交。CreativeEntity 仍选择 creative_runs / batch_image_jobs，不提前改命令或持久幂等键。

**副作用与失败语义：** 提交上游前指定订阅须完整覆盖；上游成功后只重试结算不再调用 provider。Redis 输出/outbox 补偿按既有任务状态机，已执行成功但结果丢失不能自动退款。

**字段范围：** users.balance/frozen_balance；subscriptions allocations；api_keys/team_members allowance；batch_image_jobs/creative_runs 的 hold/projection；usage_billing_dedup

| 实际方法 | 源文件与行 |
| --- | --- |
| reserveBatchImageBalanceHold | [backend/internal/service/batch_image_billing_hold.go:97](../../../backend/internal/service/batch_image_billing_hold.go) |
| captureBatchImageBalanceHold | [backend/internal/service/batch_image_billing_hold.go:137](../../../backend/internal/service/batch_image_billing_hold.go) |
| releaseBatchImageBalanceHold | [backend/internal/service/batch_image_billing_hold.go:153](../../../backend/internal/service/batch_image_billing_hold.go) |
| BatchImageBillingRecoveryService.ReleaseStaleUnsubmittedOnce | [backend/internal/service/batch_image_billing_recovery.go:26](../../../backend/internal/service/batch_image_billing_recovery.go) |
| reserveCreativeBalanceHold | [backend/internal/service/creative_billing.go:91](../../../backend/internal/service/creative_billing.go) |
| captureCreativeBalanceHold | [backend/internal/service/creative_billing.go:133](../../../backend/internal/service/creative_billing.go) |
| releaseCreativeBalanceHold | [backend/internal/service/creative_billing.go:154](../../../backend/internal/service/creative_billing.go) |
| usageBillingRepository.ReserveBatchImageBalance | [backend/internal/repository/usage_billing_repo.go:270](../../../backend/internal/repository/usage_billing_repo.go) |
| usageBillingRepository.CaptureBatchImageBalance | [backend/internal/repository/usage_billing_repo.go:274](../../../backend/internal/repository/usage_billing_repo.go) |
| usageBillingRepository.ReleaseBatchImageBalance | [backend/internal/repository/usage_billing_repo.go:278](../../../backend/internal/repository/usage_billing_repo.go) |
| usageBillingRepository.applyBatchImageBalanceHoldOnce | [backend/internal/repository/usage_billing_repo.go:320](../../../backend/internal/repository/usage_billing_repo.go) |
| applyBatchImageAllowance | [backend/internal/repository/usage_billing_repo.go:390](../../../backend/internal/repository/usage_billing_repo.go) |
| setBatchImageAllowanceReserved | [backend/internal/repository/usage_billing_repo.go:470](../../../backend/internal/repository/usage_billing_repo.go) |
| batchImageBillingEntityTable | [backend/internal/repository/usage_billing_repo.go:493](../../../backend/internal/repository/usage_billing_repo.go) |
| reserveUsageBillingBatchImageBilling | [backend/internal/repository/usage_billing_repo.go:1330](../../../backend/internal/repository/usage_billing_repo.go) |
| captureUsageBillingBatchImageBilling | [backend/internal/repository/usage_billing_repo.go:1405](../../../backend/internal/repository/usage_billing_repo.go) |
| releaseUsageBillingBatchImageBilling | [backend/internal/repository/usage_billing_repo.go:1445](../../../backend/internal/repository/usage_billing_repo.go) |
| persistBatchImageBillingHold | [backend/internal/repository/usage_billing_repo.go:1464](../../../backend/internal/repository/usage_billing_repo.go) |
| reserveBatchImageAPIKeyAllowance | [backend/internal/repository/usage_billing_repo.go:512](../../../backend/internal/repository/usage_billing_repo.go) |
| reserveBatchImageMemberAllowance | [backend/internal/repository/usage_billing_repo.go:586](../../../backend/internal/repository/usage_billing_repo.go) |
| releaseBatchImageAPIKeyAllowance | [backend/internal/repository/usage_billing_repo.go:656](../../../backend/internal/repository/usage_billing_repo.go) |
| releaseBatchImageMemberAllowance | [backend/internal/repository/usage_billing_repo.go:669](../../../backend/internal/repository/usage_billing_repo.go) |
| chargeLegacyBatchImageAPIKey | [backend/internal/repository/usage_billing_repo.go:684](../../../backend/internal/repository/usage_billing_repo.go) |
| releaseBatchImageSubscriptionAllocations | [backend/internal/repository/usage_billing_repo.go:1496](../../../backend/internal/repository/usage_billing_repo.go) |
| reserveUsageBillingBatchImageBalance | [backend/internal/repository/usage_billing_repo.go:1581](../../../backend/internal/repository/usage_billing_repo.go) |
| captureUsageBillingBatchImageBalance | [backend/internal/repository/usage_billing_repo.go:1608](../../../backend/internal/repository/usage_billing_repo.go) |
| releaseUsageBillingBatchImageFrozenBalance | [backend/internal/repository/usage_billing_repo.go:1642](../../../backend/internal/repository/usage_billing_repo.go) |

## F12 setup/simple 初始化、备份恢复及历史 SQL

**退出阶段：** S14；长期权限 S16 核验

**实际调用链：** setup.Install/AutoSetupFromEnv → initializeDatabase/createAdminUser；InitEnt → simple 默认组/管理员并发初始化。BackupService.restoreBackupArchive → dump/restore Adapter；SQL runner 执行已发布前向迁移。

**事务边界：** 初始化/恢复可以受控直接创建或恢复持久数据，不能伪装成充值。SQL runner 的 tx/notx/advisory lock 按既有迁移契约；已发布 309 个 SQL checksum 冻结。simple 默认组/并发本身不是运行时加款入口。

**副作用与失败语义：** 恢复流程持有数据库重维护互斥并保存成功/失败记录；当前 completeRestore 不负责重启。S00 不执行真实备份恢复或 setup 写入。长期例外限定初始化/恢复，不能外推到普通 HTTP 调账。

**字段范围：** 初始化用户余额；simple 配置/并发；备份包含的资金历史表；已发布 migration 数据修正

| 实际方法 | 源文件与行 |
| --- | --- |
| Install | [backend/internal/setup/setup.go:297](../../../backend/internal/setup/setup.go) |
| initializeDatabase | [backend/internal/setup/setup.go:351](../../../backend/internal/setup/setup.go) |
| createAdminUser | [backend/internal/setup/setup.go:381](../../../backend/internal/setup/setup.go) |
| AutoSetupFromEnv | [backend/internal/setup/setup.go:564](../../../backend/internal/setup/setup.go) |
| ensureSimpleModeDefaultGroups | [backend/internal/repository/simple_mode_default_groups.go:16](../../../backend/internal/repository/simple_mode_default_groups.go) |
| createGroupIfNotExists | [backend/internal/repository/simple_mode_default_groups.go:63](../../../backend/internal/repository/simple_mode_default_groups.go) |
| ensureSimpleModeAdminConcurrency | [backend/internal/repository/simple_mode_admin_concurrency.go:20](../../../backend/internal/repository/simple_mode_admin_concurrency.go) |
| BackupService.RestoreBackup | [backend/internal/service/backup_service.go:1047](../../../backend/internal/service/backup_service.go) |
| BackupService.StartRestore | [backend/internal/service/backup_service.go:1114](../../../backend/internal/service/backup_service.go) |
| BackupService.executeRestore | [backend/internal/service/backup_service.go:1190](../../../backend/internal/service/backup_service.go) |
| BackupService.restoreBackupArchive | [backend/internal/service/backup_service.go:1237](../../../backend/internal/service/backup_service.go) |
| BackupService.completeRestore | [backend/internal/service/backup_service.go:1254](../../../backend/internal/service/backup_service.go) |
| PgDumper.Restore | [backend/internal/repository/backup_pg_dumper.go:62](../../../backend/internal/repository/backup_pg_dumper.go) |
| ApplyMigrations | [backend/internal/repository/migrations_runner.go:108](../../../backend/internal/repository/migrations_runner.go) |

## 迁移约束与长期例外

- F01/F11 自行拥有完整 SQL 事务，不能因为 context 中放了 Ent Tx 就宣称参与成功。F02/F07/F08/F10 的外层参与必须沿 TxFromContext → 同一 driver/连接验证。
- 对外部支付、Redis、通知与 PostgreSQL 不伪造原子性。F03 的尽力返利/记录、F06 的 dirty mirror、F09 的 lease 恢复要保留现状。
- 普通请求事后订阅不足形成余额欠费，与任务提交前必须完整预占的规则不同。
- 未迁 runtime 写入口按上述阶段销项；F12 只保留具体初始化/恢复权限，S16 再审核。
- 退款完整流程的现有 unit 测试是 SQLite/替身仓储；新增真实 PostgreSQL 公开入口测试补齐审计失败整体回滚、重试和并发认领证据。

## 易混淆的保留入口

APIKeyService.IncrementUsage 仅累计 Redis 按日请求次数，不是金额扣减；UserService.UpdateBalance、APIKeyService.UpdateQuotaUsed/UpdateRateLimitUsage 与 accountRepository.IncrementQuotaUsed 保留在旧接口中，但本次未找到其新的生产直接调用链，迁移时先复核消费者再删除。不能把仍有声明的函数一律算作活跃扣款路径。
