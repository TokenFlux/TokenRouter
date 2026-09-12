// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	time "time"
)

var ErrAPIKeyNotFound = apikey.ErrAPIKeyNotFound

var ErrGroupNotAllowed = apikey.ErrGroupNotAllowed

var ErrGroupDisabledForUser = apikey.ErrGroupDisabledForUser

var ErrAPIKeyExists = apikey.ErrAPIKeyExists

var ErrAPIKeyLimitReached = apikey.ErrAPIKeyLimitReached

var ErrAPIKeyTooShort = apikey.ErrAPIKeyTooShort

var ErrAPIKeyInvalidChars = apikey.ErrAPIKeyInvalidChars

var ErrAPIKeyLimitInvalid = apikey.ErrAPIKeyLimitInvalid

var ErrAPIKeyExpiryInvalid = apikey.ErrAPIKeyExpiryInvalid

var ErrAPIKeyRateLimited = apikey.ErrAPIKeyRateLimited

var ErrAPIKeyAuthOverloaded = apikey.ErrAPIKeyAuthOverloaded

var ErrInvalidIPPattern = apikey.ErrInvalidIPPattern

var ErrInvalidAPIKeyFastModePolicy = apikey.ErrInvalidAPIKeyFastModePolicy

var ErrInvalidAPIKeyBillingMode = apikey.ErrInvalidAPIKeyBillingMode

var ErrPreferredSubscriptionRequired = apikey.ErrPreferredSubscriptionRequired

var ErrPreferredSubscriptionInvalid = apikey.ErrPreferredSubscriptionInvalid

var ErrPreferredSubscriptionGroup = apikey.ErrPreferredSubscriptionGroup

var ErrPreferredSubscriptionInsufficient = apikey.ErrPreferredSubscriptionInsufficient

var ErrCompositeKeyGroupsRequired = apikey.ErrCompositeKeyGroupsRequired

var ErrCompositeKeyTooManyGroups = apikey.ErrCompositeKeyTooManyGroups

var ErrCompositeKeyPrefixInvalid = apikey.ErrCompositeKeyPrefixInvalid

var ErrCompositeKeyPrefixDuplicate = apikey.ErrCompositeKeyPrefixDuplicate

var ErrCompositeKeyGroupDuplicate = apikey.ErrCompositeKeyGroupDuplicate

var ErrCompositeKeyGroupConflict = apikey.ErrCompositeKeyGroupConflict

var ErrCompositeKeyTargetRequired = apikey.ErrCompositeKeyTargetRequired

var ErrCompositeKeyPrefixRequired = apikey.ErrCompositeKeyPrefixRequired

var ErrCompositeKeyPrefixNotFound = apikey.ErrCompositeKeyPrefixNotFound

var ErrCompositeKeyUnsupported = apikey.ErrCompositeKeyUnsupported

var ErrAPIKeyExpired = apikey.ErrAPIKeyExpired

var ErrAPIKeyQuotaExhausted = apikey.ErrAPIKeyQuotaExhausted

var ErrAPIKeyRateLimit5hExceeded = apikey.ErrAPIKeyRateLimit5hExceeded

var ErrAPIKeyRateLimit1dExceeded = apikey.ErrAPIKeyRateLimit1dExceeded

var ErrAPIKeyRateLimit7dExceeded = apikey.ErrAPIKeyRateLimit7dExceeded

var ErrTeamActorInactive = apikey.ErrTeamActorInactive

var ErrTeamBillingOwnerInactive = apikey.ErrTeamBillingOwnerInactive

// NewAPIKeyLimitReachedError 委托 Key 模块的唯一实现。
func NewAPIKeyLimitReachedError(current int64, limit int) error {
	return apikey.NewAPIKeyLimitReachedError(current, limit)
}

const MaxAPIKeyCredentialBytes = apikey.MaxAPIKeyCredentialBytes

type APIKeyUpdateFields = apikey.APIKeyUpdateFields

type APIKeyRepository interface {
	Create(ctx context.Context, key *APIKey) error
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	// GetKeyAndOwnerID 仅获取 API Key 的 key 与所有者 ID，用于删除等轻量场景
	GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error)
	GetByKey(ctx context.Context, key string) (*APIKey, error)
	// GetByKeyForAuth 认证专用查询，返回最小字段集
	GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error)
	// Update 只写 fields 中显式声明的列，其余列保持库中当前值。
	Update(ctx context.Context, key *APIKey, fields APIKeyUpdateFields) error
	Delete(ctx context.Context, id int64) error
	// DeleteWithAudit 为兼容滚动升级保留历史接口名。
	// 实现必须以原子方式写入墓碑并软删除 Key，且不得保留已删除的凭据材料。
	DeleteWithAudit(ctx context.Context, id int64) error

	ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error)
	VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error)
	CountByUserID(ctx context.Context, userID int64) (int64, error)
	ExistsByKey(ctx context.Context, key string) (bool, error)
	ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error)
	SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error)
	ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error)
	// UpdateGroupIDByUserAndGroup 将用户下绑定 oldGroupID 的所有 Key 迁移到 newGroupID
	UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error)
	CountByGroupID(ctx context.Context, groupID int64) (int64, error)
	ListKeysByUserID(ctx context.Context, userID int64) ([]string, error)
	ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error)

	// Quota methods
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error)
	UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error

	// Rate limit methods
	IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error
	ResetRateLimitWindows(ctx context.Context, id int64) error
	GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error)
}

type APIKeyRateLimitData = apikey.APIKeyRateLimitData

type APIKeyQuotaUsageState = apikey.APIKeyQuotaUsageState

type APIKeyCache = apikey.APIKeyCache

// NotifyAuthCacheSubscriptionReady 委托 Key 模块的唯一实现。
func NotifyAuthCacheSubscriptionReady(ctx context.Context) {
	apikey.NotifyAuthCacheSubscriptionReady(KeyRequestContext(ctx))
}

type APIKeyAuthCacheInvalidator = apikey.APIKeyAuthCacheInvalidator

type CreateAPIKeyRequest = apikey.CreateAPIKeyRequest

type APIKeyBillingSubscriptionOption = apikey.APIKeyBillingSubscriptionOption

type UpdateAPIKeyRequest = apikey.UpdateAPIKeyRequest

// ValidateAPIKeyLimit 委托 Key 模块的唯一实现。
func ValidateAPIKeyLimit(field string, value float64) error {
	return apikey.ValidateAPIKeyLimit(field, value)
}

// ValidateAPIKeyExpiresInDays 委托 Key 模块的唯一实现。
func ValidateAPIKeyExpiresInDays(days int) error { return apikey.ValidateAPIKeyExpiresInDays(days) }

type RateLimitCacheInvalidator = apikey.RateLimitCacheInvalidator

type APIKeyService struct{ *apikey.APIKeyService }

type APIKeyAuthLookupMetrics = apikey.APIKeyAuthLookupMetrics

// AuthLookupMetrics 委托 Key 模块的唯一实现。
func (s *APIKeyService) AuthLookupMetrics() APIKeyAuthLookupMetrics {
	return s.APIKeyService.AuthLookupMetrics()
}

func NewAPIKeyService(
	apiKeyRepo APIKeyRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache APIKeyCache,
	cfg *config.Config,
) *APIKeyService {
	var groups apikey.GroupRepository
	if groupRepo != nil {
		groups = legacyKeyGroups{groupRepo}
	}
	core := apikey.NewAPIKeyService(KeyRepositoryView(apiKeyRepo), IdentityRepository(userRepo), groups, userSubRepo, userGroupRateRepo, cache, KeyOptionsFromConfig(cfg))
	core.SetGroupFastPolicy(func(raw string, force bool) string {
		return (&Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
	})
	return &APIKeyService{APIKeyService: core}
}

// SetRateLimitCacheInvalidator 委托 Key 模块的唯一实现。
func (s *APIKeyService) SetRateLimitCacheInvalidator(inv RateLimitCacheInvalidator) {
	s.APIKeyService.SetRateLimitCacheInvalidator(inv)
}

// SetConcurrencyService 委托 Key 模块的唯一实现。
func (s *APIKeyService) SetConcurrencyService(concurrencyService *ConcurrencyService) {
	s.APIKeyService.SetConcurrencyService(concurrencyService)
}

// SetTeamRepository 委托 Key 模块的唯一实现。
func (s *APIKeyService) SetTeamRepository(repo TeamRepository) {
	s.APIKeyService.SetTeamRepository(repo)
}

// GenerateKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) GenerateKey() (string, error) { return s.APIKeyService.GenerateKey() }

// GenerateAPIKeyString 委托 Key 模块的唯一实现。
func GenerateAPIKeyString(prefix string) (string, error) {
	return apikey.GenerateAPIKeyString(prefix)
}

// ValidateCustomKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) ValidateCustomKey(key string) error {
	return s.APIKeyService.ValidateCustomKey(key)
}

// Create 委托 Key 模块的唯一实现。
func (s *APIKeyService) Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	v, e := s.APIKeyService.Create(KeyRequestContext(ctx), userID, req)
	return APIKeyFromView(v), e
}

// List 委托 Key 模块的唯一实现。
func (s *APIKeyService) List(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	v, page, e := s.APIKeyService.List(KeyRequestContext(ctx), userID, params, filters)
	return keyRowsFromView(v), page, e
}

// VerifyOwnership 委托 Key 模块的唯一实现。
func (s *APIKeyService) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return s.APIKeyService.VerifyOwnership(KeyRequestContext(ctx), userID, apiKeyIDs)
}

// GetByID 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	v, e := s.APIKeyService.GetByID(KeyRequestContext(ctx), id)
	return APIKeyFromView(v), e
}

// GetByKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	v, e := s.APIKeyService.GetByKey(KeyRequestContext(ctx), key)
	return APIKeyFromView(v), e
}

// SelectCompositeGroupForRequest 委托 Key 模块的唯一实现。
func (s *APIKeyService) SelectCompositeGroupForRequest(ctx context.Context, apiKey *APIKey, binding *APIKeyCompositeGroup) (*APIKey, error) {
	apiKeyView := APIKeyView(apiKey)
	v, e := s.APIKeyService.SelectCompositeGroupForRequest(KeyRequestContext(ctx), apiKeyView, apiKeyBindingView(binding))
	ApplyAPIKeyView(apiKey, apiKeyView)
	return APIKeyFromView(v), e
}

// Update 委托 Key 模块的唯一实现。
func (s *APIKeyService) Update(ctx context.Context, id int64, userID int64, req UpdateAPIKeyRequest) (*APIKey, error) {
	v, e := s.APIKeyService.Update(KeyRequestContext(ctx), id, userID, req)
	return APIKeyFromView(v), e
}

// Delete 委托 Key 模块的唯一实现。
func (s *APIKeyService) Delete(ctx context.Context, id int64, userID int64) error {
	return s.APIKeyService.Delete(KeyRequestContext(ctx), id, userID)
}

// ValidateKey 委托 Key 模块的唯一实现。
func (s *APIKeyService) ValidateKey(ctx context.Context, key string) (*APIKey, *User, error) {
	k, u, err := s.APIKeyService.ValidateKey(KeyRequestContext(ctx), key)
	return APIKeyFromView(k), UserFromIdentity(u), err
}

// ValidateTeamKeyLifecycle 委托 Key 模块的唯一实现。
func (s *APIKeyService) ValidateTeamKeyLifecycle(apiKey *APIKey) error {
	apiKeyView := APIKeyView(apiKey)
	result0 := s.APIKeyService.ValidateTeamKeyLifecycle(apiKeyView)
	ApplyAPIKeyView(apiKey, apiKeyView)
	return result0
}

// CheckTeamMemberLimits 委托 Key 模块的唯一实现。
func (s *APIKeyService) CheckTeamMemberLimits(apiKey *APIKey) error {
	apiKeyView := APIKeyView(apiKey)
	result0 := s.APIKeyService.CheckTeamMemberLimits(apiKeyView)
	ApplyAPIKeyView(apiKey, apiKeyView)
	return result0
}

// TouchLastUsed 委托 Key 模块的唯一实现。
func (s *APIKeyService) TouchLastUsed(ctx context.Context, keyID int64) error {
	return s.APIKeyService.TouchLastUsed(KeyRequestContext(ctx), keyID)
}

// IncrementUsage 委托 Key 模块的唯一实现。
func (s *APIKeyService) IncrementUsage(ctx context.Context, keyID int64) error {
	return s.APIKeyService.IncrementUsage(KeyRequestContext(ctx), keyID)
}

// GetAvailableGroups 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error) {
	v, e := s.APIKeyService.GetAvailableGroups(KeyRequestContext(ctx), userID)
	return keyGroupsFromView(v), e
}

// GetAvailableGroupsForScope 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetAvailableGroupsForScope(ctx context.Context, userID int64, scope string) ([]Group, error) {
	v, e := s.APIKeyService.GetAvailableGroupsForScope(KeyRequestContext(ctx), userID, scope)
	return keyGroupsFromView(v), e
}

// GetAvailableGroupsForScopeWithSubscription 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetAvailableGroupsForScopeWithSubscription(ctx context.Context, userID int64, scope string, subscriptionID *int64) ([]Group, error) {
	v, e := s.APIKeyService.GetAvailableGroupsForScopeWithSubscription(KeyRequestContext(ctx), userID, scope, subscriptionID)
	return keyGroupsFromView(v), e
}

// ListBillingSubscriptionsForScope 委托 Key 模块的唯一实现。
func (s *APIKeyService) ListBillingSubscriptionsForScope(ctx context.Context, userID int64, scope string) ([]APIKeyBillingSubscriptionOption, error) {
	return s.APIKeyService.ListBillingSubscriptionsForScope(KeyRequestContext(ctx), userID, scope)
}

// SearchAPIKeys 委托 Key 模块的唯一实现。
func (s *APIKeyService) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	v, e := s.APIKeyService.SearchAPIKeys(KeyRequestContext(ctx), userID, keyword, limit)
	return keyRowsFromView(v), e
}

// GetUserGroupRates 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error) {
	return s.APIKeyService.GetUserGroupRates(KeyRequestContext(ctx), userID)
}

// GetUserGroupRatesForScope 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetUserGroupRatesForScope(ctx context.Context, userID int64, scope string) (map[int64]float64, error) {
	return s.APIKeyService.GetUserGroupRatesForScope(KeyRequestContext(ctx), userID, scope)
}

// CheckAPIKeyQuotaAndExpiry 委托 Key 模块的唯一实现。
func (s *APIKeyService) CheckAPIKeyQuotaAndExpiry(apiKey *APIKey) error {
	apiKeyView := APIKeyView(apiKey)
	result0 := s.APIKeyService.CheckAPIKeyQuotaAndExpiry(apiKeyView)
	ApplyAPIKeyView(apiKey, apiKeyView)
	return result0
}

// UpdateQuotaUsed 委托 Key 模块的唯一实现。
func (s *APIKeyService) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	return s.APIKeyService.UpdateQuotaUsed(KeyRequestContext(ctx), apiKeyID, cost)
}

// GetRateLimitData 委托 Key 模块的唯一实现。
func (s *APIKeyService) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	return s.APIKeyService.GetRateLimitData(KeyRequestContext(ctx), id)
}

// UpdateRateLimitUsage 委托 Key 模块的唯一实现。
func (s *APIKeyService) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	return s.APIKeyService.UpdateRateLimitUsage(KeyRequestContext(ctx), apiKeyID, cost)
}

// Start 委托 Key 模块的唯一实现。
func (s *APIKeyService) Start() { s.APIKeyService.Start() }

// Stop 委托 Key 模块的唯一实现。
func (s *APIKeyService) Stop() { s.APIKeyService.Stop() }
