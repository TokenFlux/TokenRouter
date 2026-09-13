package service

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	time "time"
)

// AdminService interface defines admin management operations
type AdminService interface {
	AdminUpdateAPIKeyFields(context.Context, int64, *int64, bool) (*AdminUpdateAPIKeyGroupIDResult, error)
	// User management
	ListUsers(ctx context.Context, page, pageSize int, filters UserListFilters, sortBy, sortOrder string) ([]User, int64, error)
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserIncludeDeleted(ctx context.Context, id int64) (*User, error)
	CreateUser(ctx context.Context, input *CreateUserInput) (*User, error)
	UpdateUser(ctx context.Context, id int64, input *UpdateUserInput) (*User, error)
	DeleteUser(ctx context.Context, id int64) error
	UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*User, error)
	BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error)
	// BatchUpdateLimits 只覆盖非 nil 的并发数或 RPM 上限，并返回实际更新行数。
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	GetUserAPIKeys(ctx context.Context, userID int64, page, pageSize int, sortBy, sortOrder string) ([]APIKey, int64, error)
	GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error)
	GetUserRPMStatus(ctx context.Context, userID int64) (*UserRPMStatus, error)
	// GetUserBalanceHistory returns paginated balance/concurrency change records for a user.
	// codeType is optional - pass empty string to return all types.
	// Also returns totalRecharged (sum of all positive balance top-ups).
	GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]RedeemCode, int64, float64, error)
	BindUserAuthIdentity(ctx context.Context, userID int64, input AdminBindAuthIdentityInput) (*AdminBoundAuthIdentity, error)

	// Group management
	ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]Group, int64, error)
	GetAllGroups(ctx context.Context) ([]Group, error)
	GetAllGroupsByPlatform(ctx context.Context, platform string) ([]Group, error)
	// GetAllGroupsIncludingInactive 返回所有状态的分组（启用 + 禁用），按 sort_order 和 id 排序，
	// 供 API Key 分组筛选下拉框使用。
	GetAllGroupsIncludingInactive(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, id int64) (*Group, error)
	GetGroupModelsListCandidates(ctx context.Context, id int64, platform string) ([]string, error)
	CreateGroup(ctx context.Context, input *CreateGroupInput) (*Group, error)
	// DuplicateGroup 创建停用状态的独立配置副本，并保留账号绑定及其优先级。
	DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error)
	// RecoverDuplicateGroup 在重试结果不明确时返回已提交的副本，绝不创建分组。
	RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error)
	UpdateGroup(ctx context.Context, id int64, input *UpdateGroupInput) (*Group, error)
	DeleteGroup(ctx context.Context, id int64) error
	GetGroupAPIKeys(ctx context.Context, groupID int64, page, pageSize int) ([]APIKey, int64, error)
	GetGroupRateMultipliers(ctx context.Context, groupID int64) ([]UserGroupRateEntry, error)
	ClearGroupRateMultipliers(ctx context.Context, groupID int64) error
	BatchSetGroupRateMultipliers(ctx context.Context, groupID int64, entries []GroupRateMultiplierInput) error
	ClearGroupRPMOverrides(ctx context.Context, groupID int64) error
	BatchSetGroupRPMOverrides(ctx context.Context, groupID int64, entries []GroupRPMOverrideInput) error
	UpdateGroupSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error

	// API Key management (admin)
	AdminResetAPIKeyRateLimitUsage(ctx context.Context, keyID int64) (*APIKey, error)
	AdminUpdateAPIKeyGroupID(ctx context.Context, keyID int64, groupID *int64) (*AdminUpdateAPIKeyGroupIDResult, error)

	// ReplaceUserGroup 替换用户的专属分组：授予新分组权限、迁移 Key、移除旧分组权限
	ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*ReplaceUserGroupResult, error)

	// Account management
	ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]Account, int64, error)
	// ListAccountsForSchedulerScoreFilter 返回符合过滤条件的全部账号（不分页），
	// 作为账号列表页计算高级调度分数的过滤范围池。
	ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
	// ListSchedulableAccountsForAdvancedSchedulerScore 返回指定分组内可调度账号，
	// 用于按高级分组计算调度分数。
	ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]Account, error)
	GetAccount(ctx context.Context, id int64) (*Account, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]*Account, error)
	CreateAccount(ctx context.Context, input *CreateAccountInput) (*Account, error)
	// DuplicateAccount 根据已有配置创建独立账号；一级运行态列按普通创建路径重置。
	DuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Account, error)
	// RecoverDuplicateAccount 在重试结果不明时返回先前已提交的复制件，绝不创建账号。
	RecoverDuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Account, error)
	UpdateAccount(ctx context.Context, id int64, input *UpdateAccountInput) (*Account, error)
	// UpdateAccountExtra 仅对 Extra 做 JSONB key 级增量合并，不覆盖已有持久化配置。
	UpdateAccountExtra(ctx context.Context, id int64, updates map[string]any) error
	DeleteAccount(ctx context.Context, id int64) error
	RefreshAccountCredentials(ctx context.Context, id int64) (*Account, error)
	ClearAccountError(ctx context.Context, id int64) (*Account, error)
	SetAccountError(ctx context.Context, id int64, errorMsg string) error
	// EnsureOpenAIPrivacy 检查 OpenAI OAuth 账号 privacy_mode，未设置则尝试关闭训练数据共享并持久化。
	EnsureOpenAIPrivacy(ctx context.Context, account *Account) string
	// EnsureAntigravityPrivacy 检查 Antigravity OAuth 账号 privacy_mode，未设置则调用 setUserSettings 并持久化。
	EnsureAntigravityPrivacy(ctx context.Context, account *Account) string
	// ForceOpenAIPrivacy 强制重新设置 OpenAI OAuth 账号隐私，无论当前状态。
	ForceOpenAIPrivacy(ctx context.Context, account *Account) string
	// ForceAntigravityPrivacy 强制重新设置 Antigravity OAuth 账号隐私，无论当前状态。
	ForceAntigravityPrivacy(ctx context.Context, account *Account) string
	SetAccountSchedulable(ctx context.Context, id int64, schedulable bool) (*Account, error)
	BulkUpdateAccounts(ctx context.Context, input *BulkUpdateAccountsInput) (*BulkUpdateAccountsResult, error)
	CheckMixedChannelRisk(ctx context.Context, currentAccountID int64, currentAccountPlatform string, groupIDs []int64) error
	// RevertAccountProxyFallback 将账号的 proxy_id 切回 proxy_fallback_origin_id，并清空 origin 字段。
	// 若账号不存在返回 ErrAccountNotFound；若账号存在但不在 fallback 状态，返回 ErrAccountNotInFallback。
	RevertAccountProxyFallback(ctx context.Context, id int64) error
	// CreateShadow 为指定 OpenAI OAuth 母账号创建 spark 维度影子账号（一母一影）。
	// 影子账号不持凭据（Credentials 恒为空），透传母账号凭据；继承母账号的 ProxyID。
	CreateShadow(ctx context.Context, parentID int64, opts ShadowOptions) (*Account, error)

	// Proxy management
	ListProxies(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]Proxy, int64, error)
	ListProxiesWithAccountCount(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]ProxyWithAccountCount, int64, error)
	GetAllProxies(ctx context.Context) ([]Proxy, error)
	GetAllProxiesWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error)
	GetProxy(ctx context.Context, id int64) (*Proxy, error)
	GetProxiesByIDs(ctx context.Context, ids []int64) ([]Proxy, error)
	CreateProxy(ctx context.Context, input *CreateProxyInput) (*Proxy, error)
	UpdateProxy(ctx context.Context, id int64, input *UpdateProxyInput) (*Proxy, error)
	DeleteProxy(ctx context.Context, id int64) error
	BatchDeleteProxies(ctx context.Context, ids []int64) (*ProxyBatchDeleteResult, error)
	GetProxyAccounts(ctx context.Context, proxyID int64) ([]ProxyAccountSummary, error)
	CheckProxyExists(ctx context.Context, host string, port int, username, password string) (bool, error)
	TestProxy(ctx context.Context, id int64) (*ProxyTestResult, error)
	CheckProxyQuality(ctx context.Context, id int64) (*ProxyQualityCheckResult, error)

	// Redeem code management
	ListRedeemCodes(ctx context.Context, page, pageSize int, codeType, status, search string, sortBy, sortOrder string) ([]RedeemCode, int64, error)
	GetRedeemCode(ctx context.Context, id int64) (*RedeemCode, error)
	GenerateRedeemCodes(ctx context.Context, input *GenerateRedeemCodesInput) ([]RedeemCode, error)
	UpdateRedeemCode(ctx context.Context, id int64, input *UpdateRedeemCodeInput) (*RedeemCode, error)
	DeleteRedeemCode(ctx context.Context, id int64) error
	BatchDeleteRedeemCodes(ctx context.Context, ids []int64) (int64, error)
	ExpireRedeemCode(ctx context.Context, id int64) (*RedeemCode, error)
	ResetAccountQuota(ctx context.Context, id int64) error
}

type CreateUserInput = identity.CreateUserInput

type UpdateUserInput = identity.UpdateUserInput

type AdminBindAuthIdentityInput = identity.AdminBindAuthIdentityInput

type AdminBindAuthIdentityChannelInput = identity.AdminBindAuthIdentityChannelInput

type AdminBoundAuthIdentity = identity.AdminBoundAuthIdentity

type AdminBoundAuthIdentityChannel = identity.AdminBoundAuthIdentityChannel

type CreateGroupInput = routing.CreateGroupInput

type UpdateGroupInput = routing.UpdateGroupInput

type CreateAccountInput = acctcore.CreateAccountInput

type ShadowOptions = acctcore.ShadowOptions

type UpdateAccountInput = acctcore.UpdateAccountInput

type BulkUpdateAccountsInput = acctcore.BulkUpdateAccountsInput

type BulkUpdateAccountFilters = acctcore.BulkUpdateAccountFilters

type BulkUpdateAccountResult = acctcore.BulkUpdateAccountResult

// AdminUpdateAPIKeyGroupIDResult is the result of AdminUpdateAPIKeyGroupID.
type AdminUpdateAPIKeyGroupIDResult struct {
	APIKey                 *APIKey
	AutoGrantedGroupAccess bool   // true if a new exclusive group permission was auto-added
	GrantedGroupID         *int64 // the group ID that was auto-granted
	GrantedGroupName       string // the group name that was auto-granted
}

type ReplaceUserGroupResult = identity.ReplaceUserGroupResult

type UserRPMStatus = identity.UserRPMStatus

type UserGroupRPMStatus = identity.UserGroupRPMStatus

type BulkUpdateAccountsResult = acctcore.BulkUpdateAccountsResult

type CreateProxyInput = egress.CreateProxyInput

type UpdateProxyInput = egress.UpdateProxyInput

type GenerateRedeemCodesInput = billing.GenerateRedeemCodesInput

type ProxyBatchDeleteResult = egress.ProxyBatchDeleteResult

type ProxyBatchDeleteSkipped = egress.ProxyBatchDeleteSkipped

type ProxyTestResult = egress.ProxyTestResult

type ProxyQualityCheckResult = egress.ProxyQualityCheckResult

type ProxyQualityCheckItem = egress.ProxyQualityCheckItem

type ProxyExitInfo = egress.ProxyExitInfo

type ProxyExitInfoProber = egress.ProxyExitInfoProber

var ErrRPMStatusUnavailable = identity.ErrRPMStatusUnavailable

// adminServiceImpl implements AdminService
type adminServiceImpl struct {
	accountAdmin         *acctcore.Admin
	proxyAdmin           *egress.ProxyAdmin
	keyAdmin             *apikey.Admin
	identityAdmin        *identity.UserAdmin
	billingRedeem        *billing.RedeemAdmin
	billingBalance       billing.BalanceAdjuster
	userRepo             UserRepository
	groupRates           *billing.GroupRateAdmin
	routingAdmin         *routing.GroupAdmin
	groupRepo            GroupRepository
	groupDuplicateRepo   GroupDuplicateRepository
	groupSortOrderRepo   GroupSortOrderRepository
	accountRepo          AccountRepository
	accountDuplicateRepo AccountDuplicateRepository
	proxyRepo            ProxyRepository
	apiKeyRepo           APIKeyRepository
	redeemCodeRepo       RedeemCodeRepository
	userGroupRateRepo    UserGroupRateRepository
	userRPMCache         UserRPMCache
	billingCacheService  *BillingCacheService
	proxyProber          ProxyExitInfoProber
	proxyLatencyCache    ProxyLatencyCache
	authCacheInvalidator APIKeyAuthCacheInvalidator
	entClient            *dbent.Client // 用于开启数据库事务
	settingService       *SettingService
	defaultSubAssigner   DefaultSubscriptionAssigner
	userSubRepo          UserSubscriptionRepository
	privacyClientFactory PrivacyClientFactory
	runtimeBlocker       AccountRuntimeBlocker
	httpUpstream         HTTPUpstream
	tlsFPProfileService  *TLSFingerprintProfileService
	affiliateService     adminRechargeAffiliateAccruer
	// 分组平台变更后失效渠道缓存；可为 nil，此时缓存会在 TTL 到期后自然重建。
	channelCacheInvalidator ChannelCacheInvalidator
}

// ChannelCacheInvalidator 失效渠道缓存。
// 使用窄接口避免管理服务依赖整个 ChannelService。
type ChannelCacheInvalidator interface {
	InvalidateCache()
}

// adminRechargeAffiliateAccruer 抽象管理员充值返利能力，便于隔离测试计提行为。
type adminRechargeAffiliateAccruer interface {
	AccrueInviteRebate(ctx context.Context, inviteeUserID int64, purchasedPoints float64) (float64, error)
}

// NewAdminService creates a new AdminService
func NewAdminService(
	userRepo UserRepository,
	groupRepo AdminGroupRepository,
	accountRepo AdminAccountRepository,
	proxyRepo ProxyRepository,
	apiKeyRepo APIKeyRepository,
	redeemCodeRepo RedeemCodeRepository,
	userGroupRateRepo UserGroupRateRepository,
	userRPMCache UserRPMCache,
	billingCacheService *BillingCacheService,
	proxyProber ProxyExitInfoProber,
	proxyLatencyCache ProxyLatencyCache,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	entClient *dbent.Client,
	settingService *SettingService,
	defaultSubAssigner DefaultSubscriptionAssigner,
	userSubRepo UserSubscriptionRepository,
	privacyClientFactory PrivacyClientFactory,
	runtimeBlocker AccountRuntimeBlocker,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
	affiliateService *AffiliateService,
	channelCacheInvalidator ChannelCacheInvalidator,
	billingRedeem *billing.RedeemAdmin,
	billingBalance billing.BalanceAdjuster,
	modules ...Administration,
) AdminService {
	admin := &adminServiceImpl{
		billingRedeem: billingRedeem, billingBalance: billingBalance,
		userRepo: userRepo,

		groupRepo:            groupRepo,
		groupDuplicateRepo:   groupRepo,
		groupSortOrderRepo:   groupRepo,
		accountRepo:          accountRepo,
		accountDuplicateRepo: accountRepo,
		proxyRepo:            proxyRepo,
		apiKeyRepo:           apiKeyRepo,
		redeemCodeRepo:       redeemCodeRepo,
		userGroupRateRepo:    userGroupRateRepo,
		userRPMCache:         userRPMCache,
		billingCacheService:  billingCacheService,
		proxyProber:          proxyProber,
		proxyLatencyCache:    proxyLatencyCache,
		authCacheInvalidator: authCacheInvalidator,
		entClient:            entClient,
		settingService:       settingService,
		defaultSubAssigner:   defaultSubAssigner,
		userSubRepo:          userSubRepo,
		privacyClientFactory: privacyClientFactory,
		runtimeBlocker:       runtimeBlocker,
		httpUpstream:         httpUpstream,
		tlsFPProfileService:  tlsFPProfileService,
		affiliateService:     affiliateService,

		channelCacheInvalidator: channelCacheInvalidator,
	}
	if len(modules) > 0 {
		admin.accountAdmin = modules[0].Accounts
		admin.groupRates = modules[0].Rates
		admin.routingAdmin = modules[0].Groups
		admin.proxyAdmin = modules[0].Proxies
		admin.identityAdmin = modules[0].Users
		admin.keyAdmin = modules[0].Keys
	}
	if admin.identityAdmin == nil {
		admin.identityAdmin = admin.identityAdministration()
	}
	if admin.keyAdmin == nil {
		admin.keyAdmin = admin.keyAdministration()
	}
	return admin
}

func (s *adminServiceImpl) UpdateRedeemCode(ctx context.Context, id int64, input *UpdateRedeemCodeInput) (*RedeemCode, error) {
	return s.redeemAdministration().UpdateRedeemCode(ctx, id, input)
}

func (s *adminServiceImpl) qoderRefreshHTTPUpstream() HTTPUpstream {
	if s == nil {
		return nil
	}
	return s.httpUpstream
}

func (s *adminServiceImpl) qoderRefreshTLSFingerprintService() *TLSFingerprintProfileService {
	if s == nil {
		return nil
	}
	return s.tlsFPProfileService
}

type UpdateRedeemCodeInput = billing.UpdateRedeemCodeInput

// redeemAdministration 兼容尚未使用构造器的旧测试；生产由 app 注入唯一用例。
func (s *adminServiceImpl) redeemAdministration() *billing.RedeemAdmin {
	if s.billingRedeem != nil {
		return s.billingRedeem
	}
	return billing.NewRedeemAdmin(s.redeemCodeRepo, billingpostgres.NewRedeemAdministrationMutations(s.entClient), time.Now)
}
func (s *adminServiceImpl) balanceAdjuster() billing.BalanceAdjuster {
	if s.billingBalance != nil {
		return s.billingBalance
	}
	return s.userRepo
}

// Administration 只传入 app 构造的唯一用例，旧聚合不再创建生产身份或 Key 规则。
type Administration struct {
	Accounts *acctcore.Admin
	Rates    *billing.GroupRateAdmin
	Groups   *routing.GroupAdmin
	Proxies  *egress.ProxyAdmin
	Users    *identity.UserAdmin
	Keys     *apikey.Admin
}
